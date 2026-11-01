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
	"fmt"
	"time"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	installationSettingRequestTimeout = 15 * time.Second
	installationSettingRetryTimeout   = time.Minute
	installationSettingInitialBackoff = 250 * time.Millisecond
	installationSettingMaxBackoff     = 5 * time.Second
)

// CheckRemoteInstallationProvisioning rejects a controller with a different mode.
func CheckRemoteInstallationProvisioning(
	ctx context.Context, client privatev1.InstallationSettingsClient, configured bool,
) error {
	ctx, cancel := context.WithTimeout(ctx, installationSettingRetryTimeout)
	defer cancel()

	backoff := installationSettingInitialBackoff
	for {
		requestCtx, cancelRequest := context.WithTimeout(ctx, installationSettingRequestTimeout)
		response, err := client.Get(requestCtx, &privatev1.InstallationSettingsGetRequest{})
		cancelRequest()
		if err == nil {
			if response.GetNetworkingProvisioningEnabled() != configured {
				return fmt.Errorf("%s", ProvisioningSettingChangedMessage)
			}
			return nil
		}
		if !isRetryableInstallationSettingError(err) {
			return installationSettingReadError(err)
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return installationSettingReadError(status.FromContextError(ctx.Err()).Err())
		case <-timer.C:
		}
		backoff *= 2
		if backoff > installationSettingMaxBackoff {
			backoff = installationSettingMaxBackoff
		}
	}
}

func isRetryableInstallationSettingError(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	default:
		return false
	}
}

func installationSettingReadError(err error) error {
	return fmt.Errorf("read installation networking setting: gRPC status %s", status.Code(err))
}
