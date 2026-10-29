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
	"path/filepath"
	"strings"
	"testing"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/grpc"
)

type fakeInstallationReader struct {
	value bool
	err   error
}

func (f fakeInstallationReader) Get(
	context.Context, *privatev1.InstallationSettingsGetRequest, ...grpc.CallOption,
) (*privatev1.InstallationSettingsGetResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return privatev1.InstallationSettingsGetResponse_builder{NetworkingProvisioningEnabled: f.value}.Build(), nil
}

func TestInstallationSettingStartupGuard(t *testing.T) {
	for _, value := range []bool{true, false} {
		if err := verifyInstallationProvisioning(
			context.Background(), fakeInstallationReader{value: value}, value,
		); err != nil {
			t.Fatal(err)
		}
		err := verifyInstallationProvisioning(context.Background(), fakeInstallationReader{value: value}, !value)
		if err == nil || err.Error() != installationChangedMessage {
			t.Fatalf("mismatch: %v", err)
		}
	}
	if err := verifyInstallationProvisioning(
		context.Background(), fakeInstallationReader{err: errors.New("unavailable")}, true,
	); err == nil {
		t.Fatal("unavailable API was accepted")
	}
	const sensitiveEndpoint = "https://private.example.test/token"
	if err := verifyInstallationProvisioning(
		context.Background(), fakeInstallationReader{err: errors.New(sensitiveEndpoint)}, true,
	); err == nil || strings.Contains(err.Error(), sensitiveEndpoint) {
		t.Fatalf("API error exposed endpoint: %v", err)
	}
}

func TestInstallationSettingMissingCredentials(t *testing.T) {
	t.Setenv("OSAC_INSTALLATION_API_URL", "https://fulfillment-internal-api:8001")
	t.Setenv("OSAC_INSTALLATION_API_ISSUER_URL", "https://keycloak.example.test/realms/osac")
	t.Setenv("OSAC_INSTALLATION_API_CLIENT_ID_FILE", filepath.Join(t.TempDir(), "missing"))
	err := checkInstallationProvisioning(context.Background(), true)
	if err == nil || err.Error() != installationCredentialsMessage {
		t.Fatalf("missing credentials: %v", err)
	}
}
