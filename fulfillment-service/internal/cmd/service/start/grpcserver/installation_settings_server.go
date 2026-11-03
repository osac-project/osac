/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package grpcserver

import (
	"context"

	"github.com/osac-project/osac/fulfillment-service/internal/networking"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// The private gRPC authorization interceptor limits this method to admins.
type installationSettingsServer struct {
	privatev1.UnimplementedInstallationSettingsServer
	store networking.InstallationSettingStore
}

func (s *installationSettingsServer) Get(
	ctx context.Context, _ *privatev1.InstallationSettingsGetRequest,
) (*privatev1.InstallationSettingsGetResponse, error) {
	value, err := networking.ReadInstalledProvisioning(ctx, s.store)
	if err != nil {
		return nil, err
	}
	return privatev1.InstallationSettingsGetResponse_builder{NetworkingProvisioningEnabled: value}.Build(), nil
}
