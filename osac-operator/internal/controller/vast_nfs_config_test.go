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

package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const vastNFSFixtureBundle = "schema_version: 1\nprovider: vast\nstorage_tenant: acme-on-vast\n" +
	"credentials:\n  username: fixture-admin\n  password: ' fixture-only '\n" +
	"endpoints:\n  data: acme-nfs.example.com\n" +
	"options:\n  root_export: /acme/claas\n  view_policy: fixture-view-policy\n"

const vastNFSFixtureName = "vast-nfs-fd9e866f0a49cdb899762530a3a8c1cb604b342c3de2ec733141e6"

const vastNFSFixtureFilter = "this.metadata.tenant == \"system\" && " +
	"this.metadata.project == \"\" && this.metadata.name == \"" + vastNFSFixtureName + "\""

type vastNFSTestSecrets struct {
	listResult *privatev1.SecretsListResponse
	getResult  *privatev1.SecretsGetResponse
	listErr    error
	getErr     error
	listed     []*privatev1.SecretsListRequest
	gotIDs     []string
}

func (f *vastNFSTestSecrets) List(_ context.Context, in *privatev1.SecretsListRequest, _ ...grpc.CallOption) (*privatev1.SecretsListResponse, error) {
	f.listed = append(f.listed, in)
	return f.listResult, f.listErr
}

func (f *vastNFSTestSecrets) Get(_ context.Context, in *privatev1.SecretsGetRequest, _ ...grpc.CallOption) (*privatev1.SecretsGetResponse, error) {
	f.gotIDs = append(f.gotIDs, in.GetId())
	return f.getResult, f.getErr
}

func vastNFSFixtureBackend() *privatev1.StorageBackend {
	return privatev1.StorageBackend_builder{
		Id:       "backend-id-vast-east",
		Metadata: privatev1.Metadata_builder{Name: "vast-east", Tenant: "shared"}.Build(),
		Spec: privatev1.StorageBackendSpec_builder{
			Provider: "vast",
			Endpoint: "https://vast-manager.example.com",
		}.Build(),
	}.Build()
}

func vastNFSFixtureSecret(payload string) *privatev1.Secret {
	var data map[string][]byte
	if payload != "" {
		data = map[string][]byte{"config.yaml": []byte(payload)}
	}
	return privatev1.Secret_builder{
		Id: "prepared-secret-id",
		Metadata: privatev1.Metadata_builder{
			Tenant: "system", Name: vastNFSFixtureName,
		}.Build(),
		Type: privatev1.SecretType_SECRET_TYPE_OPAQUE,
		Data: data,
	}.Build()
}

func vastNFSFixtureClient(payload string) *vastNFSTestSecrets {
	return &vastNFSTestSecrets{
		listResult: privatev1.SecretsListResponse_builder{
			Total: 1,
			Items: []*privatev1.Secret{vastNFSFixtureSecret("")},
		}.Build(),
		getResult: privatev1.SecretsGetResponse_builder{Object: vastNFSFixtureSecret(payload)}.Build(),
	}
}

func TestResolveVASTNFSConfig(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		address string
		vipPool string
	}{
		{name: "Gold with an NFS address", payload: vastNFSFixtureBundle, address: "acme-nfs.example.com"},
		{name: "Silver on the same backend", payload: vastNFSFixtureBundle, address: "acme-nfs.example.com"},
		{
			name: "named VIP pool",
			payload: strings.ReplaceAll(vastNFSFixtureBundle, "endpoints:\n  data: acme-nfs.example.com\n", "") +
				"  vip_pool_name: fixture-vip-pool\n",
			vipPool: "fixture-vip-pool",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := vastNFSFixtureClient(tc.payload)
			config, err := ResolveVASTNFSConfig(t.Context(), client, "acme", vastNFSFixtureBackend())
			if err != nil {
				t.Fatalf("resolve configuration: %v", err)
			}
			if len(client.listed) != 1 || client.listed[0].GetFilter() != vastNFSFixtureFilter || client.listed[0].GetLimit() != 2 {
				t.Fatal("expected one bounded List for the generated name in the default system project")
			}
			if len(client.gotIDs) != 1 || client.gotIDs[0] != "prepared-secret-id" {
				t.Fatal("expected Get by the matching Secret's ID")
			}
			if config.ManagementEndpoint != "https://vast-manager.example.com" ||
				config.StorageTenant != "acme-on-vast" || config.Username != "fixture-admin" ||
				config.Password != " fixture-only " || config.RootExport != "/acme/claas" ||
				config.ViewPolicy != "fixture-view-policy" ||
				config.NFSAddress != tc.address || config.VIPPoolName != tc.vipPool {
				t.Fatal("resolved configuration differs from the selected backend and fixture bundle")
			}
		})
	}
}

func TestResolveVASTNFSConfigNamesTenantBackendPairs(t *testing.T) {
	for _, tc := range []struct {
		tenant  string
		backend string
		want    string
	}{
		{tenant: "baz-corp", backend: "vast-east", want: "vast-nfs-105377b78e5bec44eb57340e960bb77b9abbcee1969f72710315a6"},
		{tenant: "acme", backend: "vast-west", want: "vast-nfs-2af46cc8b569df881287d73d4b78aebae9431fbc668acdb6bc9465"},
		{tenant: "a-b", backend: "c", want: "vast-nfs-4e84717d75bd540d02715af25a29b419f8c59fb0f5166490f97deb"},
		{tenant: "a", backend: "b-c", want: "vast-nfs-b88f83c840abe581bdc67faf64ff9b84582b26c24d2ed146154dac"},
		{tenant: strings.Repeat("a", 63), backend: strings.Repeat("b", 63), want: "vast-nfs-278079d66457fed9566a42a46de65395b054f6b14c1618c85e3386"},
	} {
		t.Run(tc.tenant+"/"+tc.backend, func(t *testing.T) {
			backend := vastNFSFixtureBackend()
			backend.GetMetadata().SetName(tc.backend)
			f := vastNFSFixtureClient("")
			f.listResult = &privatev1.SecretsListResponse{}
			_, err := ResolveVASTNFSConfig(t.Context(), f, tc.tenant, backend)
			if status.Code(err) != codes.NotFound {
				t.Fatalf("empty lookup error = %s, want NotFound", status.Code(err))
			}
			want := "this.metadata.tenant == \"system\" && this.metadata.project == \"\" && this.metadata.name == \"" + tc.want + "\""
			if len(f.listed) != 1 || f.listed[0].GetFilter() != want || len(f.gotIDs) != 0 {
				t.Fatal("expected one exact lookup for this pair in the default system project")
			}
		})
	}
}

func TestResolveVASTNFSConfigRejectsMismatchedRecords(t *testing.T) {
	for _, tc := range []struct {
		name        string
		change      func(*vastNFSTestSecrets)
		code        codes.Code
		expectedGet int
	}{
		{"missing preparation", func(f *vastNFSTestSecrets) { f.listResult = &privatev1.SecretsListResponse{} }, codes.NotFound, 0},
		{"multiple matches", func(f *vastNFSTestSecrets) { f.listResult.SetTotal(2) }, codes.FailedPrecondition, 0},
		{"unreadable list", func(f *vastNFSTestSecrets) { f.listErr = status.Error(codes.Unavailable, "fixture-only") }, codes.Unavailable, 0},
		{"cancelled list", func(f *vastNFSTestSecrets) { f.listErr = context.Canceled }, codes.Canceled, 0},
		{"no item in incomplete List", func(f *vastNFSTestSecrets) { f.listResult.SetItems(nil) }, codes.FailedPrecondition, 0},
		{"same name in another project", func(f *vastNFSTestSecrets) {
			f.listResult.GetItems()[0].GetMetadata().SetProject("baz-corp.storage")
		}, codes.FailedPrecondition, 0},
		{"same address under tenant owner", func(f *vastNFSTestSecrets) {
			f.listResult.GetItems()[0].GetMetadata().SetTenant("acme")
		}, codes.FailedPrecondition, 0},
		{"other backend in the default project", func(f *vastNFSTestSecrets) {
			f.listResult.GetItems()[0].GetMetadata().SetName("vast-nfs-2af46cc8b569df881287d73d4b78aebae9431fbc668acdb6bc9465")
		}, codes.FailedPrecondition, 0},
		{"missing canonical ID", func(f *vastNFSTestSecrets) {
			f.listResult.GetItems()[0].SetId("")
		}, codes.FailedPrecondition, 0},
		{"deleting listed record", func(f *vastNFSTestSecrets) {
			f.listResult.GetItems()[0].GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		}, codes.FailedPrecondition, 0},
		{"read denied", func(f *vastNFSTestSecrets) {
			f.getErr = status.Error(codes.PermissionDenied, "fixture-only")
		}, codes.PermissionDenied, 1},
		{"removed between List and Get", func(f *vastNFSTestSecrets) {
			f.getErr = status.Error(codes.NotFound, "fixture-only")
		}, codes.NotFound, 1},
		{"changed ID on Get", func(f *vastNFSTestSecrets) {
			f.getResult.GetObject().SetId("other-secret")
		}, codes.FailedPrecondition, 1},
		{"changed project on Get", func(f *vastNFSTestSecrets) {
			f.getResult.GetObject().GetMetadata().SetProject("baz-corp.storage")
		}, codes.FailedPrecondition, 1},
		{"deleting record on Get", func(f *vastNFSTestSecrets) {
			f.getResult.GetObject().GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		}, codes.FailedPrecondition, 1},
		{"wrong type on Get", func(f *vastNFSTestSecrets) {
			f.getResult.GetObject().SetType(privatev1.SecretType_SECRET_TYPE_VALUE)
		}, codes.FailedPrecondition, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := vastNFSFixtureClient(vastNFSFixtureBundle)
			tc.change(f)
			config, err := ResolveVASTNFSConfig(t.Context(), f, "acme", vastNFSFixtureBackend())
			if status.Code(err) != tc.code {
				t.Fatalf("error code = %s, want %s", status.Code(err), tc.code)
			}
			if strings.Contains(err.Error(), "fixture-only") || config != (VASTNFSConfig{}) {
				t.Fatal("an unsuccessful lookup must return a safe error and no configuration")
			}
			if len(f.gotIDs) != tc.expectedGet {
				t.Fatalf("Get calls = %d, want %d", len(f.gotIDs), tc.expectedGet)
			}
		})
	}
}

func TestResolveVASTNFSConfigRejectsInvalidBundles(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
	}{
		{"missing config.yaml", ""},
		{"malformed YAML", "credentials: [ fixture-only\n"},
		{"unknown field", vastNFSFixtureBundle + "  fixture-only: true\n"},
		{"duplicate YAML key", vastNFSFixtureBundle + "provider: fixture-only\n"},
		{"multiple documents", vastNFSFixtureBundle + "---\nprovider: fixture-only\n"},
		{"unsupported schema", strings.Replace(vastNFSFixtureBundle, "schema_version: 1", "schema_version: 2", 1)},
		{"wrong provider", strings.Replace(vastNFSFixtureBundle, "provider: vast", "provider: fixture-only", 1)},
		{"no VAST tenant", strings.Replace(vastNFSFixtureBundle, "storage_tenant: acme-on-vast\n", "", 1)},
		{"no username", strings.Replace(vastNFSFixtureBundle, "  username: fixture-admin\n", "", 1)},
		{"no password", strings.Replace(vastNFSFixtureBundle, "  password: ' fixture-only '\n", "", 1)},
		{"no export root", strings.Replace(vastNFSFixtureBundle, "  root_export: /acme/claas\n", "", 1)},
		{"no view policy", strings.Replace(vastNFSFixtureBundle, "  view_policy: fixture-view-policy\n", "", 1)},
		{"both VIP choices", vastNFSFixtureBundle + "  vip_pool_name: fixture-pool\n"},
		{"neither VIP choice", strings.Replace(vastNFSFixtureBundle, "endpoints:\n  data: acme-nfs.example.com\n", "", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := vastNFSFixtureClient(tc.payload)
			config, err := ResolveVASTNFSConfig(t.Context(), f, "acme", vastNFSFixtureBackend())
			if status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("invalid bundle error code = %s, want FailedPrecondition", status.Code(err))
			}
			if strings.Contains(err.Error(), "fixture-only") || config != (VASTNFSConfig{}) {
				t.Fatal("an invalid bundle must return a safe error and no configuration")
			}

			f.getResult.SetObject(vastNFSFixtureSecret(vastNFSFixtureBundle))
			if _, err := ResolveVASTNFSConfig(t.Context(), f, "acme", vastNFSFixtureBackend()); err != nil {
				t.Fatalf("resolve corrected bundle: %v", err)
			}
		})
	}
}

func TestResolveVASTNFSConfigRejectsInvalidSelection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tenant string
		change func(*privatev1.StorageBackend)
	}{
		{"missing tenant", "", func(*privatev1.StorageBackend) {}},
		{"invalid tenant name", "acme/other", func(*privatev1.StorageBackend) {}},
		{"invalid backend name", "acme", func(b *privatev1.StorageBackend) { b.GetMetadata().SetName("vast/east") }},
		{"missing backend name", "acme", func(b *privatev1.StorageBackend) { b.GetMetadata().SetName("") }},
		{"missing backend endpoint", "acme", func(b *privatev1.StorageBackend) { b.GetSpec().SetEndpoint("") }},
		{"different provider", "acme", func(b *privatev1.StorageBackend) { b.GetSpec().SetProvider("pure") }},
		{"CSP-managed backend", "acme", func(b *privatev1.StorageBackend) { b.GetSpec().SetCspManaged(true) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := vastNFSFixtureBackend()
			tc.change(backend)
			f := vastNFSFixtureClient(vastNFSFixtureBundle)
			if _, err := ResolveVASTNFSConfig(t.Context(), f, tc.tenant, backend); status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("invalid selection code = %s, want FailedPrecondition", status.Code(err))
			}
			if len(f.listed) != 0 || len(f.gotIDs) != 0 {
				t.Fatal("invalid selection must fail before calling SecretsService")
			}
		})
	}
}

func TestResolveVASTNFSConfigRedactsUnstructuredErrors(t *testing.T) {
	f := vastNFSFixtureClient(vastNFSFixtureBundle)
	f.getErr = errors.New("fixture-only")
	_, err := ResolveVASTNFSConfig(t.Context(), f, "acme", vastNFSFixtureBackend())
	if status.Code(err) != codes.Unknown || strings.Contains(err.Error(), "fixture-only") {
		t.Fatal("expected redacted error for an unstructured Get failure")
	}
}
