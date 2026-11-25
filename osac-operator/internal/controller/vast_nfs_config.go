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
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	vastNFSSecretOwner  = "system"
	storageProviderVAST = "vast"
	storageProtocolNFS  = "nfs"
)

type vastNFSSecretsClient interface {
	SecretsClient
	List(context.Context, *privatev1.SecretsListRequest, ...grpc.CallOption) (*privatev1.SecretsListResponse, error)
}

// VASTNFSSecretsClient is the SecretsService surface needed to resolve the
// delegated tenant configuration used by the VAST NFS path.
type VASTNFSSecretsClient interface {
	SecretsClient
	List(context.Context, *privatev1.SecretsListRequest, ...grpc.CallOption) (*privatev1.SecretsListResponse, error)
}

type tenantVASTNFSPreparationResolution struct {
	// Configs exists only in reconcile-local memory. Callers may use its
	// presence as readiness evidence, but must not add its values to context,
	// AAP inputs, Kubernetes resources or logs.
	Configs map[string]VASTNFSConfig
	// DelegatedBackends identifies endpoint-only OSAC-managed VAST backends
	// serving at least one NFS tier. These backends do not require the legacy
	// per-tenant hub Secret for the NFS path.
	DelegatedBackends map[string]struct{}
	UnreadyBackends   map[string]struct{}
}

// VASTNFSConfig holds the validated configuration and credentials for VAST NFS operations.
type VASTNFSConfig struct {
	ManagementEndpoint string
	StorageTenant      string
	Username           string
	Password           string
	RootExport         string
	ViewPolicy         string
	NFSAddress         string
	VIPPoolName        string
}

// resolveTenantVASTNFSPreparations resolves endpoint-only VAST NFS preparation
// once per backend for a tenant reconciliation. Credentialed backends retain
// the existing provisioning path.
func resolveTenantVASTNFSPreparations(
	ctx context.Context,
	tenantName string,
	tiers []provisioning.TierDefinition,
	backends StorageBackendsClient,
	secrets VASTNFSSecretsClient,
) *tenantVASTNFSPreparationResolution {
	result := &tenantVASTNFSPreparationResolution{
		Configs:           make(map[string]VASTNFSConfig),
		DelegatedBackends: make(map[string]struct{}),
		UnreadyBackends:   make(map[string]struct{}),
	}

	backendIDs := make(map[string]struct{})
	for _, tier := range tiers {
		if tier.Provider == storageProviderVAST && tier.Protocol == storageProtocolNFS && tier.BackendID != "" {
			backendIDs[tier.BackendID] = struct{}{}
		}
	}
	if len(backendIDs) == 0 {
		return result
	}

	ids := make([]string, 0, len(backendIDs))
	for id := range backendIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		if backends == nil {
			result.UnreadyBackends[id] = struct{}{}
			continue
		}
		response, err := backends.Get(ctx, privatev1.StorageBackendsGetRequest_builder{Id: id}.Build())
		if err != nil || response.GetObject() == nil {
			// A VAST NFS backend whose ownership or credential mode cannot be
			// confirmed is held from the delegated path. Keep errors out of
			// readiness messages because upstream responses may contain details.
			result.DelegatedBackends[id] = struct{}{}
			result.UnreadyBackends[id] = struct{}{}
			continue
		}
		backend := response.GetObject()
		if !isEndpointOnlyVASTNFSBackend(backend) {
			continue
		}

		result.DelegatedBackends[id] = struct{}{}
		if secrets == nil {
			result.UnreadyBackends[id] = struct{}{}
			continue
		}
		config, err := ResolveVASTNFSConfig(ctx, secrets, tenantName, backend)
		if err != nil || config.NFSAddress == "" {
			result.UnreadyBackends[id] = struct{}{}
			continue
		}
		result.Configs[id] = config
	}

	return result
}

func isEndpointOnlyVASTNFSBackend(backend *privatev1.StorageBackend) bool {
	if backend == nil || backend.GetSpec().GetProvider() != storageProviderVAST || backend.GetSpec().GetCspManaged() {
		return false
	}
	credentials := backend.GetSpec().GetCredentials()
	return credentials == nil || (credentials.GetUsername() == "" && credentials.GetPassword() == "" && credentials.GetPasswordSecret() == nil)
}

// resolveVASTNFSVolumeConfig selects the tier's current registered backend and
// resolves its tenant preparation. Backend selection is repeated for each
// operation; lifecycle protection against tier/backend edits is deferred.
func resolveVASTNFSVolumeConfig(
	ctx context.Context,
	tiers StorageTiersLister,
	backends StorageBackendsClient,
	secrets VASTNFSSecretsClient,
	tenantName string,
	tierName string,
) (string, VASTNFSConfig, error) {
	if tiers == nil {
		return "", VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "storage tier lookup is unavailable")
	}
	filter := "this.metadata.name == " + strconv.Quote(tierName)
	response, err := tiers.List(ctx, privatev1.StorageTiersListRequest_builder{
		Filter: &filter,
		Limit:  new(int32(2)),
	}.Build())
	if err != nil {
		return "", VASTNFSConfig{}, status.Error(codes.Unavailable, "cannot resolve the storage tier for VAST NFS volume")
	}
	if response == nil {
		return "", VASTNFSConfig{}, status.Error(codes.Unavailable, "storage tier lookup returned no response")
	}
	items := response.GetItems()
	if response.GetTotal() > 1 || len(items) > 1 {
		return "", VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS volume tier is ambiguous")
	}
	if len(items) != 1 || items[0].GetMetadata().GetName() != tierName {
		return "", VASTNFSConfig{}, status.Error(codes.NotFound, "VAST NFS volume tier was not found")
	}
	if items[0].GetSpec().GetProtocol() != privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS {
		return "", VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS volume tier does not use NFS")
	}
	associations := items[0].GetSpec().GetBackends()
	if len(associations) == 0 || associations[0].GetBackendId() == "" {
		return "", VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS volume tier has no registered backend")
	}
	backendID := associations[0].GetBackendId()
	if backends == nil {
		return backendID, VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "storage backend lookup is unavailable")
	}
	backendResponse, err := backends.Get(ctx, privatev1.StorageBackendsGetRequest_builder{Id: backendID}.Build())
	if err != nil || backendResponse == nil || backendResponse.GetObject() == nil {
		return backendID, VASTNFSConfig{}, status.Error(codes.Unavailable, "cannot resolve the registered VAST backend")
	}
	backend := backendResponse.GetObject()
	if !isEndpointOnlyVASTNFSBackend(backend) {
		return backendID, VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS volume requires an endpoint-only OSAC-managed VAST backend")
	}
	config, err := ResolveVASTNFSConfig(ctx, secrets, tenantName, backend)
	if err != nil {
		return backendID, VASTNFSConfig{}, err
	}
	if config.NFSAddress == "" {
		return backendID, VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation must provide endpoints.data as a DNS name; VIP pool names are not supported")
	}
	return backendID, config, nil
}

// ResolveVASTNFSConfig reads preparation for an authorized tenant and an already selected OSAC-managed VAST backend.
func ResolveVASTNFSConfig(
	ctx context.Context,
	secrets vastNFSSecretsClient,
	tenantName string,
	backend *privatev1.StorageBackend,
) (VASTNFSConfig, error) {
	backendName := backend.GetMetadata().GetName()
	spec := backend.GetSpec()
	if len(validation.IsDNS1123Label(tenantName)) != 0 || len(validation.IsDNS1123Label(backendName)) != 0 || strings.TrimSpace(spec.GetEndpoint()) == "" {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS requires valid tenant and backend names and a management endpoint")
	}
	if spec.GetProvider() != storageProviderVAST || spec.GetCspManaged() {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation requires an OSAC-managed VAST backend")
	}
	if secrets == nil {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation requires a SecretsService client")
	}

	if strings.Contains(tenantName, "--") || strings.Contains(backendName, "--") {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS tenant and backend names cannot contain the reserved separator '--'")
	}
	secretName := tenantName + "--" + backendName
	if len(secretName) > validation.DNS1123LabelMaxLength {
		return VASTNFSConfig{}, status.Errorf(codes.FailedPrecondition, "VAST NFS Secret name <tenant>--<backend> exceeds %d characters", validation.DNS1123LabelMaxLength)
	}
	filter := fmt.Sprintf(
		"this.metadata.tenant == %q && this.metadata.project == %q && this.metadata.name == %q",
		vastNFSSecretOwner, "", secretName,
	)
	list, err := secrets.List(ctx, privatev1.SecretsListRequest_builder{
		Filter: &filter,
		Limit:  new(int32(2)),
	}.Build())
	if err != nil {
		return VASTNFSConfig{}, vastNFSSecretReadError("list", err)
	}
	if list == nil {
		return VASTNFSConfig{}, status.Error(codes.Unavailable, "VAST NFS preparation lookup returned no response")
	}
	items := list.GetItems()
	if list.GetTotal() == 0 && len(items) == 0 {
		return VASTNFSConfig{}, status.Errorf(codes.NotFound, "VAST NFS preparation Secret %q is missing from the default system project", secretName)
	}
	if list.GetTotal() != 1 || len(items) != 1 {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation requires exactly one Secret at the expected address")
	}
	id := items[0].GetId()
	if id == "" || !matchesVASTNFSSecret(items[0], secretName) {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "listed VAST NFS preparation Secret has an invalid ID, address or deletion state")
	}

	response, err := secrets.Get(ctx, privatev1.SecretsGetRequest_builder{Id: id}.Build())
	if err != nil {
		return VASTNFSConfig{}, vastNFSSecretReadError("get", err)
	}
	if response == nil || response.GetObject() == nil {
		return VASTNFSConfig{}, status.Error(codes.NotFound, "VAST NFS preparation Secret was not returned")
	}
	secret := response.GetObject()
	if secret.GetId() != id || !matchesVASTNFSSecret(secret, secretName) {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "retrieved VAST NFS preparation Secret has an unexpected ID, address or deletion state")
	}
	if secret.GetType() != privatev1.SecretType_SECRET_TYPE_OPAQUE {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation Secret must be OPAQUE")
	}
	return decodeVASTNFSConfig(secret.GetData()["config.yaml"], spec.GetEndpoint())
}

func matchesVASTNFSSecret(secret *privatev1.Secret, name string) bool {
	metadata := secret.GetMetadata()
	return metadata.GetTenant() == vastNFSSecretOwner &&
		metadata.GetProject() == "" &&
		metadata.GetName() == name &&
		!metadata.HasDeletionTimestamp()
}

func vastNFSSecretReadError(operation string, err error) error {
	code := status.Code(err)
	if code == codes.Unknown {
		code = status.FromContextError(err).Code()
	}
	// Upstream error text can contain payload fragments.
	return status.Errorf(code, "cannot %s VAST NFS preparation Secret", operation)
}

func decodeVASTNFSConfig(payload []byte, endpoint string) (VASTNFSConfig, error) {
	var bundle struct {
		SchemaVersion any    `yaml:"schema_version"`
		Provider      string `yaml:"provider"`
		StorageTenant string `yaml:"storage_tenant"`
		Credentials   struct {
			Username string `yaml:"username"`
			Password string `yaml:"password"`
		} `yaml:"credentials"`
		Endpoints struct {
			Data string `yaml:"data"`
		} `yaml:"endpoints"`
		Options struct {
			RootExport  string `yaml:"root_export"`
			ViewPolicy  string `yaml:"view_policy"`
			VIPPoolName string `yaml:"vip_pool_name"`
		} `yaml:"options"`
	}

	if len(payload) == 0 {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation Secret must contain config.yaml")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(payload))
	decoder.KnownFields(true)
	if err := decoder.Decode(&bundle); err != nil {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation config.yaml contains invalid YAML or fields")
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation config.yaml must contain one YAML document")
	}

	// Decoding directly into an int would truncate fractional YAML versions.
	version, _ := bundle.SchemaVersion.(int)
	if version != 1 || bundle.Provider != storageProviderVAST {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation requires schema_version 1 and provider vast")
	}
	if strings.TrimSpace(bundle.StorageTenant) == "" || strings.TrimSpace(bundle.Credentials.Username) == "" || bundle.Credentials.Password == "" {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation requires storage_tenant, credentials.username and credentials.password")
	}
	if strings.TrimSpace(bundle.Options.RootExport) == "" || strings.TrimSpace(bundle.Options.ViewPolicy) == "" {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation requires options.root_export and options.view_policy")
	}
	nfsAddress, vipPoolName := strings.TrimSpace(bundle.Endpoints.Data), strings.TrimSpace(bundle.Options.VIPPoolName)
	if (nfsAddress == "") == (vipPoolName == "") {
		return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS preparation requires exactly one of endpoints.data or options.vip_pool_name")
	}
	if nfsAddress != "" {
		if len(validation.IsFullyQualifiedDomainName(nil, strings.ToLower(nfsAddress))) != 0 || net.ParseIP(strings.TrimSuffix(nfsAddress, ".")) != nil {
			return VASTNFSConfig{}, status.Error(codes.FailedPrecondition, "VAST NFS endpoints.data must be the fully qualified DNS name of the VIP pool")
		}
	}
	return VASTNFSConfig{
		ManagementEndpoint: endpoint,
		StorageTenant:      bundle.StorageTenant,
		Username:           bundle.Credentials.Username,
		Password:           bundle.Credentials.Password,
		RootExport:         bundle.Options.RootExport,
		ViewPolicy:         bundle.Options.ViewPolicy,
		NFSAddress:         nfsAddress,
		VIPPoolName:        vipPoolName,
	}, nil
}
