/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestInstallationSettingsStartupRPC(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run("networking="+strconv.FormatBool(enabled), func(t *testing.T) {
			testInstallationSettingsStartupRPC(t, enabled)
		})
	}
}

func testInstallationSettingsStartupRPC(t *testing.T, enabled bool) {
	t.Helper()
	fixture := &fixtureServer{
		expectedBearer:                "fixture-test-access-marker",
		networkingProvisioningEnabled: enabled,
	}
	server := grpc.NewServer(grpc.UnaryInterceptor(fixture.unaryAuthInterceptor))
	fixture.registerGRPCServices(server)
	listener := bufconn.Listen(1024 * 1024)
	t.Cleanup(server.Stop)
	go func() {
		if err := server.Serve(listener); err != nil {
			t.Errorf("serve fixture gRPC: %v", err)
		}
	}()
	conn, err := grpc.NewClient("passthrough:///cspfixture",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := privatev1.NewInstallationSettingsClient(conn)
	authenticated := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+fixture.expectedBearer)
	response, err := client.Get(authenticated, &privatev1.InstallationSettingsGetRequest{})
	if err != nil {
		t.Fatalf("read installation settings during operator startup: %v", err)
	}
	if got := response.GetNetworkingProvisioningEnabled(); got != enabled {
		t.Fatalf("networking provisioning: got %t, want %t", got, enabled)
	}
	if _, err := client.Get(ctx, &privatev1.InstallationSettingsGetRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unauthenticated settings request: expected Unauthenticated, got %v", err)
	}
}
