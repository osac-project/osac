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
	"crypto/tls"
	"encoding/pem"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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
		if err := checkInstallationProvisioning(
			context.Background(), fakeInstallationReader{value: value}, value,
		); err != nil {
			t.Fatal(err)
		}
		err := checkInstallationProvisioning(context.Background(), fakeInstallationReader{value: value}, !value)
		if err == nil || err.Error() != installationChangedMessage {
			t.Fatalf("mismatch: %v", err)
		}
	}
	if err := checkInstallationProvisioning(
		context.Background(), fakeInstallationReader{err: errors.New("unavailable")}, true,
	); err == nil {
		t.Fatal("unavailable API was accepted")
	}
	const sensitiveEndpoint = "https://private.example.test/token"
	if err := checkInstallationProvisioning(
		context.Background(), fakeInstallationReader{err: errors.New(sensitiveEndpoint)}, true,
	); err == nil || strings.Contains(err.Error(), sensitiveEndpoint) {
		t.Fatalf("API error exposed endpoint: %v", err)
	}
}

type testInstallationSettingsServer struct {
	privatev1.UnimplementedInstallationSettingsServer
	value bool
}

func (s testInstallationSettingsServer) Get(
	context.Context, *privatev1.InstallationSettingsGetRequest,
) (*privatev1.InstallationSettingsGetResponse, error) {
	return privatev1.InstallationSettingsGetResponse_builder{
		NetworkingProvisioningEnabled: s.value,
	}.Build(), nil
}

func TestVerifiedFulfillmentInstallationSettingBeforeManagerStart(t *testing.T) {
	fixture := httptest.NewTLSServer(nil)
	certificate := fixture.TLS.Certificates[0]
	caBundle := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fixture.Certificate().Raw})
	fixture.Close()
	caFile := filepath.Join(t.TempDir(), "bundle.pem")
	if err := os.WriteFile(caFile, caBundle, 0600); err != nil {
		t.Fatal(err)
	}

	for _, saved := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[saved], func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
				Certificates: []tls.Certificate{certificate},
			})))
			privatev1.RegisterInstallationSettingsServer(server, testInstallationSettingsServer{value: saved})
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(server.Stop)

			client := &verifiedFulfillmentConn{address: listener.Addr().String(), caFile: caFile}
			t.Cleanup(client.close)
			if client.current.Load() != nil {
				t.Fatal("verified client connected before startup check")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := checkInstallationProvisioningAtStartup(ctx, client, saved); err != nil {
				t.Fatalf("matching installation value rejected before manager start: %v", err)
			}
			if client.current.Load() == nil {
				t.Fatal("startup check did not initialize verified connection")
			}
			err = checkInstallationProvisioningAtStartup(ctx, client, !saved)
			if err == nil || err.Error() != installationChangedMessage {
				t.Fatalf("mismatched installation value returned %v", err)
			}
		})
	}
}
