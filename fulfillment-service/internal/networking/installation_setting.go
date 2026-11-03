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
)

const (
	ProvisioningSettingName           = "global.networking.provisioningEnabled"
	ProvisioningSettingChangedMessage = ProvisioningSettingName +
		" cannot change after installation; reinstall to change it"
	readProvisioningSettingSQL = `select provisioning_enabled from installation_settings where id = 1`
)

// InstallationSettingStore is the read-only part of the database pool needed by the API.
type InstallationSettingStore interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// ReadInstalledProvisioning returns the value committed by the first API startup.
func ReadInstalledProvisioning(ctx context.Context, store InstallationSettingStore) (bool, error) {
	var value bool
	if err := store.QueryRow(ctx, readProvisioningSettingSQL).Scan(&value); err != nil {
		return false, errors.New("read installation networking setting failed")
	}
	return value, nil
}
