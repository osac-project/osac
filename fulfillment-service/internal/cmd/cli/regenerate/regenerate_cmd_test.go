/*
Copyright (c) 2026 Red Hat Inc.

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

package regenerate

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func TestCommandUsesVerbResourceSyntax(t *testing.T) {
	cmd := Cmd()
	if cmd.Use != "regenerate [FLAG...] OBJECT ID|NAME" {
		t.Fatalf("Use = %q", cmd.Use)
	}
	if cmd.RunE == nil {
		t.Fatal("command has no runnerContext RunE")
	}
	if err := cmd.Args(cmd, []string{"serviceaccount", "worker"}); err != nil {
		t.Fatalf("valid arguments rejected: %v", err)
	}
	if err := cmd.Args(cmd, []string{"serviceaccount"}); err == nil {
		t.Fatal("command accepted a missing account reference")
	}
}

func TestRunRejectsUnsupportedResource(t *testing.T) {
	runner := &runnerContext{}
	err := runner.run(&cobra.Command{}, []string{"cluster", "worker"})
	if err == nil || !strings.Contains(err.Error(), `not supported for resource "cluster"`) {
		t.Fatalf("run() error = %v, want unsupported resource", err)
	}
}

func TestRegenerateServiceAccountWritesCredentialOnceAsJSON(t *testing.T) {
	account := testAccount()
	rotationRequested := false
	accounts := &serviceAccountsClientStub{
		list: func(_ context.Context, _ *publicv1.ServiceAccountsListRequest,
			_ ...grpc.CallOption) (*publicv1.ServiceAccountsListResponse, error) {
			return publicv1.ServiceAccountsListResponse_builder{Items: []*publicv1.ServiceAccount{account}}.Build(), nil
		},
		get: func(_ context.Context, request *publicv1.ServiceAccountsGetRequest,
			_ ...grpc.CallOption) (*publicv1.ServiceAccountsGetResponse, error) {
			if request.GetId() != account.GetId() {
				t.Fatalf("Get ID = %q, want %q", request.GetId(), account.GetId())
			}
			return publicv1.ServiceAccountsGetResponse_builder{Object: account}.Build(), nil
		},
		regenerate: func(_ context.Context, request *publicv1.ServiceAccountsRegenerateSecretRequest,
			_ ...grpc.CallOption) (*publicv1.ServiceAccountsRegenerateSecretResponse, error) {
			if request.GetId() != account.GetId() {
				t.Fatalf("RegenerateSecret ID = %q, want %q", request.GetId(), account.GetId())
			}
			rotationRequested = true
			return &publicv1.ServiceAccountsRegenerateSecretResponse{}, nil
		},
	}
	secrets := secretsClientFunc(func(_ context.Context, request *publicv1.SecretsGetRequest,
		_ ...grpc.CallOption) (*publicv1.SecretsGetResponse, error) {
		if request.GetId() != "secret-reference-id" {
			t.Fatalf("Secrets/Get ID = %q, want secret-reference-id", request.GetId())
		}
		secret := "old-secret"
		if rotationRequested {
			secret = "new-secret-once"
		}
		return publicv1.SecretsGetResponse_builder{
			Object: publicv1.Secret_builder{Data: map[string][]byte{
				"client_id": []byte("worker-client"), "client_secret": []byte(secret),
			}}.Build(),
		}.Build(), nil
	})
	runner := &runnerContext{accounts: accounts, secrets: secrets}
	var output bytes.Buffer
	if err := runner.regenerateServiceAccount(context.Background(), &output, "worker"); err != nil {
		t.Fatal(err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	var clientID, secret string
	if err := json.Unmarshal(result["client_id"], &clientID); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(result["client_secret"], &secret); err != nil {
		t.Fatal(err)
	}
	if clientID != "worker-client" || secret != "new-secret-once" {
		t.Fatalf("credential output = (%q, %q)", clientID, secret)
	}
	if bytes.Contains(result["service_account"], []byte("new-secret-once")) {
		t.Fatal("service account object included credential bytes")
	}
}

func TestWaitForRotationTimeoutIncludesAccount(t *testing.T) {
	account := testAccount()
	runner := &runnerContext{
		accounts: &serviceAccountsClientStub{
			get: func(_ context.Context, _ *publicv1.ServiceAccountsGetRequest,
				_ ...grpc.CallOption) (*publicv1.ServiceAccountsGetResponse, error) {
				return publicv1.ServiceAccountsGetResponse_builder{Object: account}.Build(), nil
			},
		},
		secrets: secretsClientFunc(func(_ context.Context, _ *publicv1.SecretsGetRequest,
			_ ...grpc.CallOption) (*publicv1.SecretsGetResponse, error) {
			return publicv1.SecretsGetResponse_builder{
				Object: publicv1.Secret_builder{Data: map[string][]byte{
					"client_id": []byte("worker-client"), "client_secret": []byte("old-secret"),
				}}.Build(),
			}.Build(), nil
		}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, _, _, err := runner.waitForRotation(ctx, account.GetId(), "secret-reference-id", "old-secret")
	if err == nil {
		t.Fatal("waitForRotation() unexpectedly succeeded")
	}
	for _, text := range []string{"timed out", `"account-id"`, "credential Secret to change"} {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("timeout %q does not contain %q", err, text)
		}
	}
}

func TestAPIErrorSuggestsUpgradeForUnimplementedRPC(t *testing.T) {
	runner := &runnerContext{}
	err := runner.apiError("failed to rotate service account", status.Error(codes.Unimplemented, "unknown method"))
	for _, text := range []string{"failed to rotate service account is not implemented", "upgrade the Fulfillment service", "OSAC-6555"} {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("error %q does not contain %q", err, text)
		}
	}
}

type serviceAccountsClientStub struct {
	list       func(context.Context, *publicv1.ServiceAccountsListRequest, ...grpc.CallOption) (*publicv1.ServiceAccountsListResponse, error)
	get        func(context.Context, *publicv1.ServiceAccountsGetRequest, ...grpc.CallOption) (*publicv1.ServiceAccountsGetResponse, error)
	regenerate func(context.Context, *publicv1.ServiceAccountsRegenerateSecretRequest, ...grpc.CallOption) (*publicv1.ServiceAccountsRegenerateSecretResponse, error)
}

func (c *serviceAccountsClientStub) List(ctx context.Context, request *publicv1.ServiceAccountsListRequest,
	options ...grpc.CallOption) (*publicv1.ServiceAccountsListResponse, error) {
	return c.list(ctx, request, options...)
}

func (c *serviceAccountsClientStub) Get(ctx context.Context, request *publicv1.ServiceAccountsGetRequest,
	options ...grpc.CallOption) (*publicv1.ServiceAccountsGetResponse, error) {
	return c.get(ctx, request, options...)
}

func (c *serviceAccountsClientStub) RegenerateSecret(ctx context.Context, request *publicv1.ServiceAccountsRegenerateSecretRequest,
	options ...grpc.CallOption) (*publicv1.ServiceAccountsRegenerateSecretResponse, error) {
	return c.regenerate(ctx, request, options...)
}

type secretsClientFunc func(context.Context, *publicv1.SecretsGetRequest, ...grpc.CallOption) (*publicv1.SecretsGetResponse, error)

func (f secretsClientFunc) Get(ctx context.Context, request *publicv1.SecretsGetRequest,
	options ...grpc.CallOption) (*publicv1.SecretsGetResponse, error) {
	return f(ctx, request, options...)
}

func testAccount() *publicv1.ServiceAccount {
	return publicv1.ServiceAccount_builder{
		Id:       "account-id",
		Metadata: publicv1.Metadata_builder{Name: "worker", Tenant: "tenant-a"}.Build(),
		Status: publicv1.ServiceAccountStatus_builder{
			ClientSecret: publicv1.SecretLocalReference_builder{Id: "secret-reference-id"}.Build(),
		}.Build(),
	}.Build()
}
