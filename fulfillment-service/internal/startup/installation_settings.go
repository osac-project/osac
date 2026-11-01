/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package startup contains the checks that the fulfillment service runs when it starts, before it serves requests.
package startup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"regexp"
	"slices"

	"github.com/jackc/pgx/v5"
)

// Exact messages of the startup refusals (unified networking design).
const (
	ProvisioningChangedMessage = "global.networking.provisioningEnabled cannot change after installation; " +
		"reinstall to change it"
	ServicesEnabledMessage = "Enabling BMaaS or CaaS after networking resources exist is not supported in this " +
		"release; delete every VirtualNetwork or reinstall"
	ReleaseChangedMessage = "in-place upgrade or downgrade between minor releases is not supported; delete all " +
		"workloads and networking resources or reinstall"
)

// InstallationSettings are the settings of the running fulfillment service that are compared with the stored ones.
type InstallationSettings struct {
	// ProvisioningEnabled is global.networking.provisioningEnabled.
	ProvisioningEnabled bool

	// EnabledServices are the enabled services, for example "vmaas" and "bmaas".
	EnabledServices []string

	// Release is the OSAC release of the running binary.
	Release string

	// ExcludedIPv4CIDRs is global.networking.excludedIpv4Cidrs.
	ExcludedIPv4CIDRs []netip.Prefix
}

// Querier is the database access that the check needs; *pgxpool.Pool and pgx.Tx implement it.
type Querier interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// CheckInstallationSettings stores the installation settings at the first start and, on later starts, refuses to
// start on a change that isn't supported: a changed provisioningEnabled, BMaaS or CaaS newly enabled while a
// VirtualNetwork exists, a different minor release while networking or workload records exist, or an excluded range
// that overlaps an existing network. It runs in one transaction that holds an exclusive lock on the settings table, so
// concurrent starts run one after the other.
func CheckInstallationSettings(ctx context.Context, logger *slog.Logger, db Querier,
	settings InstallationSettings) (err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin the installation settings transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
			return
		}
		err = tx.Commit(ctx)
	}()
	if _, err = tx.Exec(ctx, `lock table installation_settings in exclusive mode`); err != nil {
		return fmt.Errorf("failed to lock the installation settings: %w", err)
	}

	var stored struct {
		provisioningEnabled bool
		enabledServices     []string
		release             string
	}
	err = tx.QueryRow(ctx,
		`select provisioning_enabled, enabled_services, release from installation_settings where id = 1`,
	).Scan(&stored.provisioningEnabled, &stored.enabledServices, &stored.release)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx,
			`insert into installation_settings (id, provisioning_enabled, enabled_services, release)
			 values (1, $1, $2, $3)`,
			settings.ProvisioningEnabled, nonNil(settings.EnabledServices), settings.Release)
		if err != nil {
			return fmt.Errorf("failed to store the installation settings: %w", err)
		}
		logger.InfoContext(ctx, "Stored installation settings",
			slog.Bool("provisioning_enabled", settings.ProvisioningEnabled),
			slog.Any("enabled_services", settings.EnabledServices),
			slog.String("release", settings.Release))
		return checkExcludedRanges(ctx, tx, settings.ExcludedIPv4CIDRs)
	}
	if err != nil {
		return fmt.Errorf("failed to read the installation settings: %w", err)
	}

	if stored.provisioningEnabled != settings.ProvisioningEnabled {
		return errors.New(ProvisioningChangedMessage)
	}

	newlyEnabled := false
	for _, service := range []string{"bmaas", "caas"} {
		if slices.Contains(settings.EnabledServices, service) && !slices.Contains(stored.enabledServices, service) {
			newlyEnabled = true
		}
	}
	if newlyEnabled {
		exists, err := anyRow(ctx, tx, "virtual_networks")
		if err != nil {
			return err
		}
		if exists {
			return errors.New(ServicesEnabledMessage) //nolint:staticcheck // ST1005: exact design text
		}
	}

	if releaseChanged(stored.release, settings.Release) {
		for _, table := range []string{
			"virtual_networks", "external_ip_pools", "compute_instances", "bare_metal_instances", "clusters",
		} {
			exists, err := anyRow(ctx, tx, table)
			if err != nil {
				return err
			}
			if exists {
				return errors.New(ReleaseChangedMessage)
			}
		}
	}

	if err = checkExcludedRanges(ctx, tx, settings.ExcludedIPv4CIDRs); err != nil {
		return err
	}

	_, err = tx.Exec(ctx,
		`update installation_settings set enabled_services = $1, release = $2 where id = 1`,
		nonNil(settings.EnabledServices), settings.Release)
	if err != nil {
		return fmt.Errorf("failed to update the installation settings: %w", err)
	}
	return nil
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func anyRow(ctx context.Context, tx pgx.Tx, table string) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, fmt.Sprintf(`select exists (select 1 from %s)`, pgx.Identifier{table}.Sanitize())).
		Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check the %s table: %w", table, err)
	}
	return exists, nil
}

var semverPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$`)

// releaseChanged reports whether two releases have a different major or minor version. A release that isn't a
// semantic version, for example a development build, is never considered a change.
func releaseChanged(stored, current string) bool {
	storedMatch := semverPattern.FindStringSubmatch(stored)
	currentMatch := semverPattern.FindStringSubmatch(current)
	if storedMatch == nil || currentMatch == nil {
		return false
	}
	return storedMatch[1] != currentMatch[1] || storedMatch[2] != currentMatch[2]
}

// checkExcludedRanges refuses an excluded range that overlaps, in this order, the NetworkClass default VirtualNetwork
// CIDR, a VirtualNetwork or an ExternalIPPool. Cluster pod and service networks are internal to a cluster, so an
// excluded infrastructure range is not compared with them.
func checkExcludedRanges(ctx context.Context, tx pgx.Tx, excluded []netip.Prefix) error {
	if len(excluded) == 0 {
		return nil
	}
	type target struct {
		kind  string
		label string
		cidr  string
	}
	queries := []struct {
		kind  string
		query string
		label func(id, cidr string) string
	}{
		{
			kind:  "the NetworkClass default virtual_network_ipv4_cidr",
			query: `select id, data->'spec'->'defaults'->>'virtual_network_ipv4_cidr' from network_classes`,
			label: func(_, cidr string) string { return cidr },
		},
		{
			kind:  "VirtualNetwork",
			query: `select id, data->'spec'->>'ipv4_cidr' from virtual_networks order by id`,
			label: func(id, _ string) string { return id },
		},
		{
			kind: "ExternalIPPool",
			query: `select id, cidr from external_ip_pools,
				jsonb_array_elements_text(coalesce(data->'spec'->'cidrs', '[]'::jsonb)) as cidr order by id`,
			label: func(id, _ string) string { return id },
		},
	}
	for _, query := range queries {
		rows, err := tx.Query(ctx, query.query)
		if err != nil {
			return fmt.Errorf("failed to check the excluded ranges: %w", err)
		}
		var targets []target
		for rows.Next() {
			var id string
			var cidr *string
			if err := rows.Scan(&id, &cidr); err != nil {
				rows.Close()
				return fmt.Errorf("failed to check the excluded ranges: %w", err)
			}
			if cidr != nil {
				targets = append(targets, target{kind: query.kind, label: query.label(id, *cidr), cidr: *cidr})
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("failed to check the excluded ranges: %w", err)
		}
		for _, item := range targets {
			prefix, err := netip.ParsePrefix(item.cidr)
			if err != nil {
				continue
			}
			for _, range_ := range excluded {
				if range_.Overlaps(prefix) {
					return fmt.Errorf("excludedIpv4Cidrs range %s overlaps %s %s", range_, item.kind, item.label)
				}
			}
		}
	}
	return nil
}
