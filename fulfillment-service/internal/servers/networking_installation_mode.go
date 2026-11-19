/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
)

const (
	// provisioningSettingChangedMessage is the exact refusal of the installation settings check.
	provisioningSettingChangedMessage = "global.networking.provisioningEnabled cannot change after installation; " +
		"reinstall to change it"

	// readInstalledProvisioningSQL reads the mode accepted at the first start of the fulfillment service.
	readInstalledProvisioningSQL = `select provisioning_enabled from installation_settings where id = 1`
)

// validateProvisioningMode validates the provisioning mode of a NetworkClass create against the value the installation
// accepted at its first start. The accepted value is the historical authority: a NetworkClass can be deleted and
// recreated, but never with a different mode, so recreating it cannot bypass the reinstall requirement.
func validateProvisioningMode(ctx context.Context, mode *bool) error {
	if mode == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.provisioning_enabled' is required")
	}
	installed, err := readInstalledProvisioning(ctx)
	if err != nil {
		return err
	}
	if *mode != installed {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"spec.provisioning_enabled is %t but the installation stored %t: %s", *mode, installed,
			provisioningSettingChangedMessage)
	}
	return nil
}

// readInstalledProvisioning reads the provisioning mode stored at the first start of the fulfillment service.
func readInstalledProvisioning(ctx context.Context) (bool, error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return false, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"installation settings are not available: %v", err)
	}
	var value bool
	err = tx.QueryRow(ctx, readInstalledProvisioningSQL).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"installation settings are not initialized; start the fulfillment service before creating a NetworkClass")
	}
	if err != nil {
		return false, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"failed to read the installation networking setting: %v", err)
	}
	return value, nil
}
