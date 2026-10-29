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

package serviceaccount

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func TestCommandHelpAndSubcommands(t *testing.T) {
	cmd := Cmd()
	if got := len(cmd.Commands()); got != 6 {
		t.Fatalf("expected 6 subcommands, got %d", got)
	}
	for _, name := range []string{"create", "get", "list", "update", "delete", "regenerate-secret"} {
		sub, _, err := cmd.Find([]string{name})
		if err != nil || sub == nil {
			t.Errorf("missing %q subcommand", name)
		}
	}
	for _, text := range []string{"All command output is JSON", "RoleBinding", "Secrets/Get", "no role grants"} {
		if !strings.Contains(longHelp, text) {
			t.Errorf("help does not contain %q", text)
		}
	}
}

func TestDecodeAccountAcceptsDirectAndAnyYaml(t *testing.T) {
	inputs := []string{
		"metadata:\n  name: worker\n",
		`"@type": "type.googleapis.com/osac.public.v1.ServiceAccount"` + "\nmetadata:\n  name: worker\n",
	}
	for _, input := range inputs {
		account, _, err := decodeAccount(strings.NewReader(input))
		if err != nil {
			t.Fatalf("decodeAccount(%q): %v", input, err)
		}
		if account.GetMetadata().GetName() != "worker" {
			t.Errorf("name = %q, want worker", account.GetMetadata().GetName())
		}
	}
}

func TestDecodeAccountRejectsRemovedServiceAccountRoles(t *testing.T) {
	input := `{"metadata":{"name":"worker"},"spec":{"roles":[{"id":"tenant-automation"}]}}`
	if _, _, err := decodeAccount(strings.NewReader(input)); err == nil {
		t.Fatal("decodeAccount accepted roles on the empty ServiceAccountSpec")
	}
}

func TestDecodeAccountRejectsWrongAnyTypeAndMultipleDocuments(t *testing.T) {
	for _, input := range []string{
		`"@type": "type.googleapis.com/osac.public.v1.Secret"` + "\nmetadata:\n  name: worker\n",
		"metadata:\n  name: worker\n---\nmetadata:\n  name: second\n",
	} {
		if _, _, err := decodeAccount(strings.NewReader(input)); err == nil {
			t.Errorf("decodeAccount accepted invalid input %q", input)
		}
	}
}

func TestClearCreateOnlyFields(t *testing.T) {
	account := publicv1.ServiceAccount_builder{
		Id: "forged-id",
		Metadata: publicv1.Metadata_builder{
			Name: "worker", Tenant: "another-tenant", Creator: "user", Version: 8,
		}.Build(),
		Status: publicv1.ServiceAccountStatus_builder{
			ClientSecret: publicv1.SecretLocalReference_builder{Id: "forged-secret"}.Build(),
		}.Build(),
	}.Build()
	clearCreateOnlyFields(account)
	if account.GetId() != "" || account.GetStatus() != nil {
		t.Fatalf("create retained server-owned identity: %v", account)
	}
	if account.GetMetadata().GetTenant() != "" || account.GetMetadata().GetCreator() != "" || account.GetMetadata().GetVersion() != 0 {
		t.Fatalf("create retained server-owned metadata: %v", account.GetMetadata())
	}
	if account.GetMetadata().GetName() != "worker" {
		t.Fatalf("create lost desired name: %q", account.GetMetadata().GetName())
	}
}

func TestApplyPatchBuildsMutableFieldMask(t *testing.T) {
	current := publicv1.ServiceAccount_builder{
		Id: "account-id",
		Metadata: publicv1.Metadata_builder{
			Name: "worker", Version: 7, DisplayName: "Old", Labels: map[string]string{"keep": "value"},
		}.Build(),
	}.Build()
	patch, raw, err := decodeAccount(strings.NewReader(`{"metadata":{"display_name":"","labels":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	object, paths, err := applyPatch(current, patch, raw)
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{"metadata.display_name", "metadata.labels"}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("paths = %v, want %v", paths, wantPaths)
	}
	if object.GetId() != "account-id" || object.GetMetadata().GetVersion() != 7 {
		t.Fatalf("patch lost object identity/version: %v", object)
	}
	if object.GetMetadata().GetDisplayName() != "" || len(object.GetMetadata().GetLabels()) != 0 {
		t.Fatalf("patch did not preserve explicit empty values: %v", object)
	}
	if object.GetStatus() != nil {
		t.Fatalf("patch included server-owned status: %v", object.GetStatus())
	}
}

func TestAccountStructuredOutputOmitsCredentialBytes(t *testing.T) {
	account := testAccount()
	var output bytes.Buffer
	if err := writeAccount(&output, account); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "do-not-print-this-secret") {
		t.Fatalf("account output included credential bytes: %s", output.String())
	}
	if !strings.Contains(output.String(), "secret-reference-id") {
		t.Fatalf("account output omitted Secret reference: %s", output.String())
	}
}

func TestCredentialOutputIsStructured(t *testing.T) {
	account := testAccount()
	var output bytes.Buffer
	if err := writeCredential(&output, account, "worker-client", "new-secret-once"); err != nil {
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
		t.Fatalf("credential output = client_id %q, secret %q", clientID, secret)
	}
	if bytes.Contains(result["service_account"], []byte("new-secret-once")) {
		t.Fatalf("account object included credential bytes: %s", result["service_account"])
	}
}

func TestReadCredentialGetsClientIDAndSecretFromReferencedSecret(t *testing.T) {
	account := testAccount()
	client := secretsClientFunc(func(_ context.Context, request *publicv1.SecretsGetRequest, _ ...grpc.CallOption) (*publicv1.SecretsGetResponse, error) {
		if request.GetId() != "secret-reference-id" {
			t.Fatalf("Secrets/Get ID = %q, want secret-reference-id", request.GetId())
		}
		return publicv1.SecretsGetResponse_builder{
			Object: publicv1.Secret_builder{Data: map[string][]byte{
				"client_id": []byte("worker-client"), "client_secret": []byte("new-secret-once"),
			}}.Build(),
		}.Build(), nil
	})
	clientID, secret, err := readCredential(context.Background(), client, account)
	if err != nil {
		t.Fatal(err)
	}
	if clientID != "worker-client" || secret != "new-secret-once" {
		t.Fatalf("readCredential() = (%q, %q), want (worker-client, new-secret-once)", clientID, secret)
	}
}

func TestOlderServiceErrorNamesContractVersion(t *testing.T) {
	err := olderServiceError("missing status.client_secret")
	for _, text := range []string{"missing status.client_secret", "upgrade the Fulfillment service", "OSAC-6555"} {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("error %q does not contain %q", err, text)
		}
	}
}

func TestAPIErrorSuggestsUpgradeForUnimplementedRPC(t *testing.T) {
	err := apiError("failed to create service account", status.Error(codes.Unimplemented, "unknown method"))
	for _, text := range []string{"failed to create service account is not implemented", "upgrade the Fulfillment service", "OSAC-6555"} {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("error %q does not contain %q", err, text)
		}
	}
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

type secretsClientFunc func(context.Context, *publicv1.SecretsGetRequest, ...grpc.CallOption) (*publicv1.SecretsGetResponse, error)

func (f secretsClientFunc) Get(ctx context.Context, request *publicv1.SecretsGetRequest, options ...grpc.CallOption) (*publicv1.SecretsGetResponse, error) {
	return f(ctx, request, options...)
}
