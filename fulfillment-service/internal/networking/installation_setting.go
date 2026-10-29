/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package networking

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	ProvisioningSettingName           = "global.networking.provisioningEnabled"
	ProvisioningSettingChangedMessage = ProvisioningSettingName +
		" cannot change after installation; reinstall to change it"
	insertProvisioningSettingSQL = `insert into installation_settings (name, bool_value)
		values ($1, $2) on conflict (name) do nothing`
	readProvisioningSettingSQL = `select bool_value from installation_settings where name = $1`
)

// InstallationSettingStore is the part of a database pool needed by the startup check.
type InstallationSettingStore interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// ReadInstalledProvisioning returns the value committed by the first API startup.
func ReadInstalledProvisioning(ctx context.Context, store InstallationSettingStore) (bool, error) {
	var value bool
	if err := store.QueryRow(ctx, readProvisioningSettingSQL, ProvisioningSettingName).Scan(&value); err != nil {
		return false, errors.New("read installation networking setting failed")
	}
	return value, nil
}

// CheckInstallationProvisioning saves the first value, then checks the durable value.
// PostgreSQL's unique constraint makes concurrent first starts settle on one value.
func CheckInstallationProvisioning(ctx context.Context, store InstallationSettingStore, configured bool) error {
	if _, err := store.Exec(ctx, insertProvisioningSettingSQL, ProvisioningSettingName, configured); err != nil {
		return errors.New("store installation networking setting failed")
	}
	installed, err := ReadInstalledProvisioning(ctx, store)
	if err != nil {
		return err
	}
	if installed != configured {
		return errors.New(ProvisioningSettingChangedMessage)
	}
	return nil
}
