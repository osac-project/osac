/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
)

// quotaWarning is the payload of one outbox row.
type quotaWarning struct {
	Dimension string `json:"dimension"`
	Threshold int64  `json:"threshold"`
	Usage     int64  `json:"usage"`
	Limit     int64  `json:"limit"`
}

// writeQuotaWarning writes one warning to the outbox, in the request's transaction. The
// notifications API (OSAC-75) does not exist yet, so warnings accumulate in the outbox by
// design; its publisher will drain the table.
func (e *quotaEnforcer) writeQuotaWarning(
	ctx context.Context, tenant, project, dimension string, threshold, usage, limit int64,
) error {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return err
	}
	data, err := json.Marshal(quotaWarning{
		Dimension: dimension,
		Threshold: threshold,
		Usage:     usage,
		Limit:     limit,
	})
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx,
		`insert into quota_outbox (tenant, project, dimension, data) values ($1, $2::ltree, $3, $4)`,
		tenant, project, dimension, data,
	); err != nil {
		return err
	}
	e.metrics.recordOutboxInsert()
	return nil
}

// warnCrossings writes one warning for each threshold a fitting request takes its scope from
// below to at or above. The scopes are the request's project chain; a threshold always belongs
// to a named project.
func (e *quotaEnforcer) warnCrossings(
	ctx context.Context,
	tenant string,
	usage map[string]map[string]int64,
	limits *quotaLimits,
	scopes []string,
	added map[string]int64,
) error {
	thresholds, err := e.readThresholds(ctx, tenant)
	if err != nil {
		return err
	}
	if len(thresholds) == 0 {
		return nil
	}
	for _, scope := range scopes {
		if scope == "" {
			continue
		}
		scopeThresholds := thresholds[scope]
		if len(scopeThresholds) == 0 {
			continue
		}
		scopeUsage := quotaSubtreeUsage(usage, scope)
		for dimension, threshold := range scopeThresholds {
			if added[dimension] <= 0 {
				continue
			}
			before := scopeUsage[dimension]
			after := before + added[dimension]
			if before < threshold && after >= threshold {
				if err = e.writeQuotaWarning(ctx, tenant, scope, dimension, threshold, after,
					limits.limitOf(scope, dimension)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// warnThresholdChange writes a warning when setting a threshold puts its project at or above
// it: the usage was below the old value, or there was no old value, and reaches the new one.
func (e *quotaEnforcer) warnThresholdChange(
	ctx context.Context, tenant, project, dimension string, oldValue, newValue int64,
) error {
	if tenant == "" || tenant == auth.SystemTenant || tenant == auth.SharedTenant || project == "" {
		return nil
	}
	usage, err := e.counter.Count(ctx, tenant)
	if err != nil {
		return err
	}
	used := quotaSubtreeUsage(usage, project)[dimension]
	if used < newValue || (oldValue > 0 && used >= oldValue) {
		return nil
	}
	limits, err := e.readLimits(ctx, tenant)
	if err != nil {
		return err
	}
	return e.writeQuotaWarning(ctx, tenant, project, dimension, newValue, used,
		limits.limitOf(project, dimension))
}

// warnScopeAtOrAbove writes a warning for each threshold of the dimension, in the projects of
// the subtree, whose usage is at or above it. Only a change that tightens the scope's limit
// warns: the limit the change leaves behind is below the one it replaced. A limit change
// doesn't move usage or a threshold, so the warning reports the state the change leaves behind.
func (e *quotaEnforcer) warnScopeAtOrAbove(ctx context.Context, tenant, project, dimension string, oldLimit int64) error {
	if tenant == "" || tenant == auth.SystemTenant || tenant == auth.SharedTenant {
		return nil
	}
	thresholds, err := e.readThresholds(ctx, tenant)
	if err != nil {
		return err
	}
	var projects []string
	for thresholdProject, dimensions := range thresholds {
		if _, ok := dimensions[dimension]; !ok {
			continue
		}
		if project != "" && thresholdProject != project && !isDescendantProject(thresholdProject, project) {
			continue
		}
		projects = append(projects, thresholdProject)
	}
	if len(projects) == 0 {
		return nil
	}
	limits, err := e.readLimits(ctx, tenant)
	if err != nil {
		return err
	}
	if limits.limitOf(project, dimension) >= oldLimit {
		return nil
	}
	usage, err := e.counter.Count(ctx, tenant)
	if err != nil {
		return err
	}
	for _, thresholdProject := range projects {
		threshold := thresholds[thresholdProject][dimension]
		used := quotaSubtreeUsage(usage, thresholdProject)[dimension]
		if used < threshold {
			continue
		}
		if err = e.writeQuotaWarning(ctx, tenant, thresholdProject, dimension, threshold, used,
			limits.limitOf(thresholdProject, dimension)); err != nil {
			return err
		}
	}
	return nil
}

// warnCreatedLimit writes the warnings of a newly created limit. The scope's old limit is what
// it falls back to without a row: the tenant's limit for a project, the default for the tenant.
func (e *quotaEnforcer) warnCreatedLimit(ctx context.Context, tenant, project, dimension string) error {
	limits, err := e.readLimits(ctx, tenant)
	if err != nil {
		return err
	}
	oldLimit := resolveQuotaLimit(nil, limits.defaults, dimension)
	if project != "" {
		oldLimit = limits.limitOf("", dimension)
	}
	return e.warnScopeAtOrAbove(ctx, tenant, project, dimension, oldLimit)
}

// isDescendantProject reports whether the project sits inside the parent.
func isDescendantProject(project, parent string) bool {
	if parent == "" {
		return true
	}
	return project != parent && len(project) > len(parent)+1 &&
		project[:len(parent)] == parent && project[len(parent)] == '.'
}

// readThresholds returns the tenant's thresholds: for each project, the threshold of each
// dimension. A cleared threshold is deleted, so only rows without a deletion timestamp apply.
func (e *quotaEnforcer) readThresholds(ctx context.Context, tenant string) (map[string]map[string]int64, error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		select project::text, data -> 'spec' ->> 'dimension', (data -> 'spec' ->> 'value')::bigint
		from quota_thresholds
		where tenant = $1 and deletion_timestamp = 'epoch'`, tenant,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]map[string]int64{}
	for rows.Next() {
		var project, dimension string
		var value int64
		if err = rows.Scan(&project, &dimension, &value); err != nil {
			return nil, err
		}
		if result[project] == nil {
			result[project] = map[string]int64{}
		}
		result[project][dimension] = value
	}
	return result, rows.Err()
}

// scanDefaultLimitChange writes, one tenant at a time in its own transaction, a warning for
// each threshold the dimension leaves at or above its usage, but only for tenants whose
// effective limit the change tightened. It runs after the change commits, because a default
// belongs to every tenant and no request transaction should scan them all.
func (e *quotaEnforcer) scanDefaultLimitChange(ctx context.Context, dimension string, oldDefault int64) {
	manager, err := database.TxManagerFromContext(ctx)
	if err != nil {
		e.logger.ErrorContext(ctx, "Failed to scan the tenants after a default limit change",
			slog.Any("error", err))
		return
	}
	tx, err := manager.Begin(ctx)
	if err != nil {
		e.logger.ErrorContext(ctx, "Failed to scan the tenants after a default limit change",
			slog.Any("error", err))
		return
	}
	tenantCtx := database.TxIntoContext(ctx, tx)
	tenants, err := e.listTenants(tenantCtx)
	if endErr := tx.End(ctx); endErr != nil || err != nil {
		e.logger.ErrorContext(ctx, "Failed to scan the tenants after a default limit change",
			slog.Any("error", err))
		return
	}
	for _, tenant := range tenants {
		scanTx, err := manager.Begin(ctx)
		if err != nil {
			e.logger.ErrorContext(ctx, "Failed to scan the tenants after a default limit change",
				slog.Any("error", err))
			return
		}
		scanCtx := database.TxIntoContext(ctx, scanTx)
		if err = e.warnTenantAfterDefaultChange(scanCtx, tenant, dimension, oldDefault); err != nil {
			e.logger.ErrorContext(ctx, "Failed to write the quota warnings of a tenant",
				slog.String("tenant", tenant),
				slog.Any("error", err))
		}
		if err = scanTx.End(ctx); err != nil {
			e.logger.ErrorContext(ctx, "Failed to write the quota warnings of a tenant",
				slog.String("tenant", tenant),
				slog.Any("error", err))
			return
		}
	}
}

// warnTenantAfterDefaultChange warns a tenant whose effective limit the default change
// tightened. A tenant with its own override keeps its limit, so only the default moved.
func (e *quotaEnforcer) warnTenantAfterDefaultChange(
	ctx context.Context, tenant, dimension string, oldDefault int64,
) error {
	limits, err := e.readLimits(ctx, tenant)
	if err != nil {
		return err
	}
	oldLimit := oldDefault
	if override, ok := limits.overrides[dimension]; ok {
		oldLimit = override
	}
	return e.warnScopeAtOrAbove(ctx, tenant, "", dimension, oldLimit)
}

// listTenants returns every tenant except the built-in ones.
func (e *quotaEnforcer) listTenants(ctx context.Context) ([]string, error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx,
		`select name from tenants where name <> $1 and name <> $2`,
		auth.SystemTenant, auth.SharedTenant,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tenants []string
	for rows.Next() {
		var tenant string
		if err = rows.Scan(&tenant); err != nil {
			return nil, err
		}
		tenants = append(tenants, tenant)
	}
	return tenants, rows.Err()
}
