/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
	"github.com/osac-project/osac/fulfillment-service/internal/references"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateIdentityProvidersServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
	secretStore       vault.SecretStore
}

var _ privatev1.IdentityProvidersServer = (*PrivateIdentityProvidersServer)(nil)

type PrivateIdentityProvidersServer struct {
	privatev1.UnimplementedIdentityProvidersServer
	logger      *slog.Logger
	generic     *GenericServer[*privatev1.IdentityProvider]
	dao         *dao.GenericDAO[*privatev1.IdentityProvider]
	secretsDao  *dao.GenericDAO[*privatev1.Secret]
	secretStore vault.SecretStore
}

func NewPrivateIdentityProvidersServer() *PrivateIdentityProvidersServerBuilder {
	return &PrivateIdentityProvidersServerBuilder{}
}

func (b *PrivateIdentityProvidersServerBuilder) SetLogger(value *slog.Logger) *PrivateIdentityProvidersServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateIdentityProvidersServerBuilder) SetNotifier(value events.Notifier) *PrivateIdentityProvidersServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateIdentityProvidersServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateIdentityProvidersServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateIdentityProvidersServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateIdentityProvidersServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateIdentityProvidersServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateIdentityProvidersServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateIdentityProvidersServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateIdentityProvidersServerBuilder {
	b.filterDesc = value
	return b
}

// SetSecretStore sets the Vault secret store used to read the value of a Vault-backed secret
// referenced by client_secret_secret. It is optional: when unset, the create/update validation
// only sees data carried in the database column (which covers non-Vault backends and tests).
func (b *PrivateIdentityProvidersServerBuilder) SetSecretStore(value vault.SecretStore) *PrivateIdentityProvidersServerBuilder {
	b.secretStore = value
	return b
}

func (b *PrivateIdentityProvidersServerBuilder) Build() (result *PrivateIdentityProvidersServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Create the server early so that we can use its functions to set up other objects:
	s := &PrivateIdentityProvidersServer{
		logger:      b.logger,
		secretStore: b.secretStore,
	}

	// Create the generic server:
	s.generic, err = NewGenericServer[*privatev1.IdentityProvider]().
		SetLogger(b.logger).
		SetService(privatev1.IdentityProviders_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}

	// Create the DAO:
	s.dao, err = dao.NewGenericDAO[*privatev1.IdentityProvider]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	s.secretsDao, err = dao.NewGenericDAO[*privatev1.Secret]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Return the server:
	result = s
	return
}

func (s *PrivateIdentityProvidersServer) Create(ctx context.Context,
	request *privatev1.IdentityProvidersCreateRequest) (response *privatev1.IdentityProvidersCreateResponse, err error) {
	if err = s.validateClientSecretSecret(ctx, request.GetObject()); err != nil {
		return
	}
	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateIdentityProvidersServer) List(ctx context.Context,
	request *privatev1.IdentityProvidersListRequest) (response *privatev1.IdentityProvidersListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateIdentityProvidersServer) Get(ctx context.Context,
	request *privatev1.IdentityProvidersGetRequest) (response *privatev1.IdentityProvidersGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateIdentityProvidersServer) Update(ctx context.Context,
	request *privatev1.IdentityProvidersUpdateRequest) (response *privatev1.IdentityProvidersUpdateResponse, err error) {
	if err = s.validateClientSecretSecret(ctx, request.GetObject()); err != nil {
		return
	}
	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateIdentityProvidersServer) Delete(ctx context.Context,
	request *privatev1.IdentityProvidersDeleteRequest) (response *privatev1.IdentityProvidersDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateIdentityProvidersServer) Signal(ctx context.Context,
	request *privatev1.IdentityProvidersSignalRequest) (response *privatev1.IdentityProvidersSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

const (
	// secretValueKey is the key in a Secret's data map that holds the client secret value.
	secretValueKey = "value"
)

func oidcFrom(idp *privatev1.IdentityProvider) *privatev1.OidcConfig {
	if idp == nil {
		return nil
	}
	return idp.GetSpec().GetOidc()
}

func (s *PrivateIdentityProvidersServer) validateClientSecretSecret(
	ctx context.Context, idp *privatev1.IdentityProvider) error {
	oidc := oidcFrom(idp)
	if oidc == nil {
		return nil
	}
	ref := oidc.GetClientSecretSecret()
	if ref == nil {
		return nil
	}
	if ref.GetId() == "" && ref.GetName() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "client_secret_secret must specify id or name")
	}
	resolved, err := references.NewDAOLookupFunc(s.secretsDao)(ctx, "", "", ref.GetId(), ref.GetName())
	if err != nil {
		var deniedErr *dao.ErrDenied
		if errors.As(err, &deniedErr) {
			return grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
		}
		var nf interface{ IsNotFound() bool }
		if errors.As(err, &nf) && nf.IsNotFound() {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"there is no secret with identifier or name '%s'", refKey(ref))
		}
		s.logger.ErrorContext(ctx, "Failed to resolve client_secret_secret reference", "error", err)
		return grpcstatus.Errorf(grpccodes.Internal, "failed to resolve client_secret_secret reference")
	}
	if resolved.Tenant == auth.SharedTenant {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"shared secrets cannot be used as identity provider client_secret_secret references")
	}
	// Load the resolved Secret and ensure it carries a non-empty data["value"] entry, as required
	// by the reconciler that consumes it. Rejecting here surfaces the problem as an INVALID_ARGUMENT
	// at write time instead of a silent reconcile failure later.
	secretResp, err := s.secretsDao.Get().SetId(resolved.ID).Do(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to load client_secret_secret reference", "error", err)
		return grpcstatus.Errorf(grpccodes.Internal, "failed to resolve client_secret_secret reference")
	}
	secret := secretResp.GetObject()
	data := secret.GetData()
	// Vault-backed secrets do not carry their data in the database column: the Secrets server nulls
	// it after writing to Vault. Hydrate the value from the store so this create/update-time check
	// reads the same value the reconciler later resolves via the Secrets API. Secrets that keep
	// their data in the database column (non-Vault backends, tests) are validated directly.
	if len(data) == 0 && s.secretStore != nil &&
		secret.GetBackend() == privatev1.SecretBackend_SECRET_BACKEND_VAULT {
		metadata := secret.GetMetadata()
		fetched, fetchErr := s.secretStore.Fetch(ctx,
			metadata.GetTenant(), metadata.GetProject(), metadata.GetName())
		if fetchErr != nil {
			s.logger.ErrorContext(ctx, "Failed to load client_secret_secret value from store", "error", fetchErr)
			return grpcstatus.Errorf(grpccodes.Internal, "failed to resolve client_secret_secret reference")
		}
		data = fetched
	}
	if value, ok := data[secretValueKey]; !ok || len(value) == 0 {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"secret '%s' referenced by client_secret_secret must contain a non-empty '%s' entry",
			refKey(ref), secretValueKey)
	}
	resolvedRef := &privatev1.SecretLocalReference{}
	resolvedRef.SetId(resolved.ID)
	resolvedRef.SetName(resolved.Name)
	oidc.SetClientSecretSecret(resolvedRef)
	return nil
}
