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
	"fmt"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type preparationTestBackends struct {
	items map[string]*privatev1.StorageBackend
	calls map[string]int
}

func (f *preparationTestBackends) Get(_ context.Context, in *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
	f.calls[in.GetId()]++
	backend, ok := f.items[in.GetId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "not found")
	}
	return privatev1.StorageBackendsGetResponse_builder{Object: backend}.Build(), nil
}

func (f *preparationTestBackends) List(context.Context, *privatev1.StorageBackendsListRequest, ...grpc.CallOption) (*privatev1.StorageBackendsListResponse, error) {
	return &privatev1.StorageBackendsListResponse{}, nil
}

type preparationTestSecrets struct {
	items   []*privatev1.Secret
	payload string
	gets    int
}

func (f *preparationTestSecrets) List(_ context.Context, in *privatev1.SecretsListRequest, _ ...grpc.CallOption) (*privatev1.SecretsListResponse, error) {
	if in.GetFilter() == "" || in.GetLimit() != 2 {
		return nil, status.Error(codes.InvalidArgument, "expected bounded exact lookup")
	}
	return privatev1.SecretsListResponse_builder{Total: int32(len(f.items)), Items: f.items}.Build(), nil
}

func (f *preparationTestSecrets) Get(_ context.Context, in *privatev1.SecretsGetRequest, _ ...grpc.CallOption) (*privatev1.SecretsGetResponse, error) {
	f.gets++
	for _, item := range f.items {
		if item.GetId() == in.GetId() {
			copy := item
			payload := f.payload
			if payload == "" {
				payload = vastNFSFixtureBundle
			}
			copy.SetData(map[string][]byte{"config.yaml": []byte(payload)})
			return privatev1.SecretsGetResponse_builder{Object: copy}.Build(), nil
		}
	}
	return nil, status.Error(codes.NotFound, "not found")
}

type preparationTestTiers struct {
	items     []*privatev1.StorageTier
	listCalls []*privatev1.StorageTiersListRequest
	listErr   error
}

func (f *preparationTestTiers) List(_ context.Context, in *privatev1.StorageTiersListRequest, _ ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
	f.listCalls = append(f.listCalls, in)
	return privatev1.StorageTiersListResponse_builder{Total: int32(len(f.items)), Items: f.items}.Build(), f.listErr
}

func endpointOnlyVASTBackend(id, name string) *privatev1.StorageBackend {
	return privatev1.StorageBackend_builder{
		Id: id,
		Metadata: privatev1.Metadata_builder{
			Name: name,
		}.Build(),
		Spec: privatev1.StorageBackendSpec_builder{
			Provider:   "vast",
			Endpoint:   "https://vast-manager.example.com",
			CspManaged: false,
		}.Build(),
	}.Build()
}

func TestResolveTenantVASTNFSPreparations(t *testing.T) {
	backends := &preparationTestBackends{
		items: map[string]*privatev1.StorageBackend{
			"backend-a": endpointOnlyVASTBackend("backend-a", "vast-east"),
			"backend-b": endpointOnlyVASTBackend("backend-b", "vast-west"),
		},
		calls: make(map[string]int),
	}
	secrets := &preparationTestSecrets{items: []*privatev1.Secret{{
		Id: "secret-a",
		Metadata: privatev1.Metadata_builder{
			Tenant: "system",
			Name:   "acme--vast-east",
		}.Build(),
		Type: privatev1.SecretType_SECRET_TYPE_OPAQUE,
	}}}
	definitions := []provisioning.TierDefinition{
		{Name: "gold", Protocol: "nfs", Provider: "vast", BackendID: "backend-a"},
		{Name: "silver", Protocol: "nfs", Provider: "vast", BackendID: "backend-a"},
		{Name: "bronze", Protocol: "nfs", Provider: "vast", BackendID: "backend-b"},
		{Name: "fast", Protocol: "block", Provider: "vast", BackendID: "backend-a"},
		{Name: "other", Protocol: "nfs", Provider: "pure", BackendID: "backend-a"},
	}

	got := resolveTenantVASTNFSPreparations(context.Background(), "acme", definitions, backends, secrets)
	if got == nil {
		t.Fatal("expected a preparation resolution")
	}
	if _, ok := got.DelegatedBackends["backend-a"]; !ok {
		t.Fatal("backend-a was not recognized as endpoint-only VAST NFS")
	}
	if _, ok := got.DelegatedBackends["backend-b"]; !ok {
		t.Fatal("backend-b was not recognized as endpoint-only VAST NFS")
	}
	if got.Configs["backend-a"].NFSAddress != "acme-nfs.example.com" {
		t.Fatal("expected the validated data FQDN from backend-a preparation")
	}
	if _, ok := got.Configs["backend-b"]; ok {
		t.Fatal("backend-b must remain unready when its preparation is missing")
	}
	if _, ok := got.UnreadyBackends["backend-b"]; !ok {
		t.Fatal("backend-b missing preparation was not reported")
	}
	if _, ok := got.DelegatedBackends["backend-a"]; !ok {
		t.Fatal("backend-a should be resolved once for both tiers")
	}
	if backends.calls["backend-a"] != 1 || backends.calls["backend-b"] != 1 {
		t.Fatalf("backend gets = %#v, want one per unique backend", backends.calls)
	}
	if secrets.gets != 1 {
		t.Fatalf("Secret gets = %d, want one for the present backend", secrets.gets)
	}
}

func TestResolveTenantVASTNFSPreparationsPreservesCredentialedPath(t *testing.T) {
	backend := endpointOnlyVASTBackend("backend-a", "vast-east")
	backend.GetSpec().SetCredentials(privatev1.StorageBackendCredentials_builder{
		Username: "legacy-admin",
		Password: "legacy-password",
	}.Build())
	backends := &preparationTestBackends{items: map[string]*privatev1.StorageBackend{"backend-a": backend}, calls: make(map[string]int)}
	secrets := &preparationTestSecrets{items: []*privatev1.Secret{{
		Id: "secret-a",
		Metadata: privatev1.Metadata_builder{
			Tenant: "system",
			Name:   "acme--vast-east",
		}.Build(),
		Type: privatev1.SecretType_SECRET_TYPE_OPAQUE,
	}}}
	definitions := []provisioning.TierDefinition{{Name: "gold", Protocol: "nfs", Provider: "vast", BackendID: "backend-a"}}

	got := resolveTenantVASTNFSPreparations(context.Background(), "acme", definitions, backends, secrets)
	if _, ok := got.DelegatedBackends["backend-a"]; ok {
		t.Fatal("credentialed backend must stay on the existing path")
	}
	if len(got.UnreadyBackends) != 0 || len(got.Configs) != 0 {
		t.Fatalf("credentialed path unexpectedly resolved a delegated preparation: %#v", got)
	}
	if secrets.gets != 0 {
		t.Fatalf("Secret service gets = %d, want 0", secrets.gets)
	}
}

func TestResolveTenantVASTNFSPreparationsRequiresDataFQDN(t *testing.T) {
	backend := endpointOnlyVASTBackend("backend-a", "vast-east")
	backends := &preparationTestBackends{
		items: map[string]*privatev1.StorageBackend{"backend-a": backend},
		calls: make(map[string]int),
	}
	secrets := &preparationTestSecrets{
		payload: "schema_version: 1\nprovider: vast\nstorage_tenant: acme-on-vast\n" +
			"credentials:\n  username: fixture-user\n  password: fixture-password\n" +
			"options:\n  root_export: /acme\n  view_policy: acme\n  vip_pool_name: pool-east\n",
		items: []*privatev1.Secret{{
			Id:       "secret-a",
			Metadata: privatev1.Metadata_builder{Tenant: "system", Name: "acme--vast-east"}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_OPAQUE,
		}},
	}
	got := resolveTenantVASTNFSPreparations(context.Background(), "acme", []provisioning.TierDefinition{
		{Name: "gold", Protocol: "nfs", Provider: "vast", BackendID: "backend-a"},
	}, backends, secrets)
	if _, ready := got.Configs["backend-a"]; ready {
		t.Fatal("pool-name-only configuration must not make the CLaaS class ready")
	}
	if _, blocked := got.UnreadyBackends["backend-a"]; !blocked {
		t.Fatal("pool-name-only configuration must keep the backend unready")
	}
}

func TestResolveVASTNFSVolumeConfigSelectsAndPinsBackend(t *testing.T) {
	tier := privatev1.StorageTier_builder{
		Metadata: privatev1.Metadata_builder{Name: "gold", Tenant: "shared"}.Build(),
		Spec: privatev1.StorageTierSpec_builder{
			Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
			Backends: []*privatev1.BackendAssociation{
				privatev1.BackendAssociation_builder{BackendId: "backend-a"}.Build(),
			},
		}.Build(),
	}.Build()
	backend := endpointOnlyVASTBackend("backend-a", "vast-east")

	tiers := &preparationTestTiers{items: []*privatev1.StorageTier{tier}}
	backends := &preparationTestBackends{
		items: map[string]*privatev1.StorageBackend{"backend-a": backend},
		calls: make(map[string]int),
	}
	secrets := &preparationTestSecrets{items: []*privatev1.Secret{{
		Id:       "secret-a",
		Metadata: privatev1.Metadata_builder{Tenant: "system", Name: "acme--vast-east"}.Build(),
		Type:     privatev1.SecretType_SECRET_TYPE_OPAQUE,
	}}}

	backendID, config, err := resolveVASTNFSVolumeConfig(
		context.Background(), tiers, backends, secrets, "acme", "gold",
	)
	if err != nil {
		t.Fatalf("resolve volume config: %v", err)
	}
	if backendID != "backend-a" || config.NFSAddress != "acme-nfs.example.com" {
		t.Fatalf("resolved backend/config = %q/%#v", backendID, config)
	}
	if len(tiers.listCalls) != 1 {
		t.Fatalf("tier List calls = %d, want 1 on each resolution", len(tiers.listCalls))
	}
	if backends.calls["backend-a"] != 1 || secrets.gets != 1 {
		t.Fatalf("backend/secret lookups = %#v/%d, want one each", backends.calls, secrets.gets)
	}
}

func TestResolveVASTNFSVolumeConfigRejectsVIPPoolNameOnly(t *testing.T) {
	tier := privatev1.StorageTier_builder{
		Metadata: privatev1.Metadata_builder{Name: "gold", Tenant: "shared"}.Build(),
		Spec: privatev1.StorageTierSpec_builder{
			Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
			Backends: []*privatev1.BackendAssociation{
				privatev1.BackendAssociation_builder{BackendId: "backend-a"}.Build(),
			},
		}.Build(),
	}.Build()
	secrets := &preparationTestSecrets{
		payload: "schema_version: 1\nprovider: vast\nstorage_tenant: acme-on-vast\n" +
			"credentials:\n  username: fixture-user\n  password: fixture-password\n" +
			"options:\n  root_export: /acme\n  view_policy: acme\n  vip_pool_name: pool-east\n",
		items: []*privatev1.Secret{{
			Id:       "secret-a",
			Metadata: privatev1.Metadata_builder{Tenant: "system", Name: "acme--vast-east"}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_OPAQUE,
		}},
	}
	backends := &preparationTestBackends{
		items: map[string]*privatev1.StorageBackend{"backend-a": endpointOnlyVASTBackend("backend-a", "vast-east")},
		calls: make(map[string]int),
	}

	_, _, err := resolveVASTNFSVolumeConfig(
		context.Background(),
		&preparationTestTiers{items: []*privatev1.StorageTier{tier}},
		backends,
		secrets,
		"acme",
		"gold",
	)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("pool-name-only error code = %s, want FailedPrecondition", status.Code(err))
	}
}

func TestVASTNFSCreateOutcomeUncertainOnlyForAmbiguousGRPCErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "unavailable", err: status.Error(codes.Unavailable, "unavailable"), want: true},
		{name: "deadline exceeded", err: status.Error(codes.DeadlineExceeded, "timeout"), want: true},
		{name: "wrapped gRPC unknown", err: fmt.Errorf("vendor create: %w", status.Error(codes.Unknown, "unknown")), want: true},
		{name: "permission denied", err: status.Error(codes.PermissionDenied, "rejected"), want: false},
		{name: "local configuration error", err: errors.New("vendor endpoint is not configured"), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := vastNFSCreateOutcomeUncertain(tc.err); got != tc.want {
				t.Fatalf("vastNFSCreateOutcomeUncertain(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}
