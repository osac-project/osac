/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

const installationChangedMessage = "global.networking.provisioningEnabled cannot change after installation; " +
	"reinstall to change it"

// checkInstallationProvisioningAtStartup opens the verified connection before
// the manager starts its CA watcher and then checks the saved installation mode.
func checkInstallationProvisioningAtStartup(
	ctx context.Context, conn grpc.ClientConnInterface, configured bool,
) error {
	if verified, ok := conn.(*verifiedFulfillmentConn); ok {
		if err := verified.reload(ctx); err != nil {
			return fmt.Errorf("initialize verified fulfillment connection: %w", err)
		}
	}
	return checkInstallationProvisioning(ctx, privatev1.NewInstallationSettingsClient(conn), configured)
}

func checkInstallationProvisioning(
	ctx context.Context, client privatev1.InstallationSettingsClient, configured bool,
) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	response, err := client.Get(ctx, &privatev1.InstallationSettingsGetRequest{})
	if err != nil {
		return fmt.Errorf("read installation networking setting: gRPC status %s", status.Code(err))
	}
	if response.GetNetworkingProvisioningEnabled() != configured {
		return errors.New(installationChangedMessage)
	}
	return nil
}
