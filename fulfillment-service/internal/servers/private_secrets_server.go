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

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
)

type PrivateSecretsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	secretStore       vault.SecretStore
}

var _ privatev1.SecretsServer = (*PrivateSecretsServer)(nil)

type PrivateSecretsServer struct {
	privatev1.UnimplementedSecretsServer

	logger       *slog.Logger
	generic      *GenericServer[*privatev1.Secret]
	secretStore  vault.SecretStore
	tenancyLogic auth.TenancyLogic
}

func NewPrivateSecretsServer() *PrivateSecretsServerBuilder {
	return &PrivateSecretsServerBuilder{}
}

func (b *PrivateSecretsServerBuilder) SetLogger(value *slog.Logger) *PrivateSecretsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateSecretsServerBuilder) SetNotifier(value events.Notifier) *PrivateSecretsServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateSecretsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateSecretsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateSecretsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateSecretsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateSecretsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateSecretsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateSecretsServerBuilder) SetSecretStore(value vault.SecretStore) *PrivateSecretsServerBuilder {
	b.secretStore = value
	return b
}

func (b *PrivateSecretsServerBuilder) Build() (result *PrivateSecretsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	s := &PrivateSecretsServer{
		logger:       b.logger,
		secretStore:  b.secretStore,
		tenancyLogic: b.tenancyLogic,
	}

	s.generic, err = NewGenericServer[*privatev1.Secret]().
		SetLogger(b.logger).
		SetService(privatev1.Secrets_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetRedactFunc(s.redact).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = s
	return
}

func (s *PrivateSecretsServer) redact(object *privatev1.Secret) *privatev1.Secret {
	object.SetData(nil)
	return object
}

// List fetches a list of secret objects from postgres.
// Secret data itself should never be included in the response, and users should use Get
// to fetch individual secrets with populated data.
func (s *PrivateSecretsServer) List(ctx context.Context,
	request *privatev1.SecretsListRequest) (response *privatev1.SecretsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateSecretsServer) Get(ctx context.Context,
	request *privatev1.SecretsGetRequest) (response *privatev1.SecretsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	if err != nil {
		return
	}

	obj := response.GetObject()
	if s.secretStore != nil && obj.GetBackend() == privatev1.SecretBackend_SECRET_BACKEND_VAULT {
		tenant := obj.GetMetadata().GetTenant()
		project := obj.GetMetadata().GetProject()
		name := obj.GetMetadata().GetName()

		data, fetchErr := s.secretStore.Fetch(ctx, tenant, project, name)
		if fetchErr != nil {
			err = vault.ToGrpcError(fetchErr)
			return
		}

		obj.SetData(data)
	}

	return
}

func (s *PrivateSecretsServer) Create(ctx context.Context,
	request *privatev1.SecretsCreateRequest) (response *privatev1.SecretsCreateResponse, err error) {

	secret := request.GetObject()

	err = s.validateSecretCreate(secret)
	if err != nil {
		return
	}

	secret.SetId("")

	// Default unspecified backend to Vault.
	if secret.GetBackend() == privatev1.SecretBackend_SECRET_BACKEND_UNSPECIFIED {
		secret.SetBackend(privatev1.SecretBackend_SECRET_BACKEND_VAULT)
	}

	if s.secretStore != nil && secret.GetBackend() == privatev1.SecretBackend_SECRET_BACKEND_VAULT {
		tenant, tenantErr := s.determineTenant(ctx, secret)
		if tenantErr != nil {
			err = tenantErr
			return
		}
		project := secret.GetMetadata().GetProject()
		name := secret.GetMetadata().GetName()

		err = s.secretStore.Store(ctx, tenant, project, name, secret.GetData())
		if err != nil {
			err = vault.ToGrpcError(err)
			return
		}

		secret.SetData(nil)
	}

	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateSecretsServer) Update(ctx context.Context,
	request *privatev1.SecretsUpdateRequest) (response *privatev1.SecretsUpdateResponse, err error) {
	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	getRequest := &privatev1.SecretsGetRequest{}
	getRequest.SetId(id)
	var getResponse *privatev1.SecretsGetResponse
	err = s.generic.Get(ctx, getRequest, &getResponse)
	if err != nil {
		return
	}

	existingSecret := getResponse.GetObject()

	err = s.validateSecretUpdate(ctx, request.GetObject(), existingSecret)
	if err != nil {
		return
	}

	if s.secretStore != nil &&
		existingSecret.GetBackend() == privatev1.SecretBackend_SECRET_BACKEND_VAULT &&
		len(request.GetObject().GetData()) > 0 {

		tenant := existingSecret.GetMetadata().GetTenant()
		project := existingSecret.GetMetadata().GetProject()
		name := existingSecret.GetMetadata().GetName()

		err = s.secretStore.Store(ctx, tenant, project, name, request.GetObject().GetData())
		if err != nil {
			err = vault.ToGrpcError(err)
			return
		}

		request.GetObject().SetData(nil)
	}

	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateSecretsServer) Delete(ctx context.Context,
	request *privatev1.SecretsDeleteRequest) (response *privatev1.SecretsDeleteResponse, err error) {
	// Report errors to the transaction so that post-DB failures (e.g. Vault delete) trigger rollback.
	tx, txErr := database.TxFromContext(ctx)
	if txErr == nil {
		defer tx.ReportError(&err)
	}

	var obj *privatev1.Secret
	if s.secretStore != nil {
		getRequest := &privatev1.SecretsGetRequest{}
		getRequest.SetId(request.GetId())
		var getResponse *privatev1.SecretsGetResponse
		err = s.generic.Get(ctx, getRequest, &getResponse)
		if err != nil {
			return
		}
		obj = getResponse.GetObject()
	}

	err = s.generic.Delete(ctx, request, &response)
	if err != nil {
		return
	}

	if obj != nil && obj.GetBackend() == privatev1.SecretBackend_SECRET_BACKEND_VAULT {
		tenant := obj.GetMetadata().GetTenant()
		project := obj.GetMetadata().GetProject()
		name := obj.GetMetadata().GetName()

		deleteErr := s.secretStore.Delete(ctx, tenant, project, name)
		if deleteErr != nil {
			err = vault.ToGrpcError(deleteErr)
			return
		}
	}

	return
}

func (s *PrivateSecretsServer) Signal(ctx context.Context,
	request *privatev1.SecretsSignalRequest) (response *privatev1.SecretsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

func (s *PrivateSecretsServer) validateSecretCreate(secret *privatev1.Secret) error {
	if secret == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "secret is mandatory")
	}
	if secret.GetMetadata() == nil || secret.GetMetadata().GetName() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'metadata.name' is required")
	}

	switch secret.GetBackend() {
	case privatev1.SecretBackend_SECRET_BACKEND_HUB:
		if err := s.validateHubSecretCreate(secret); err != nil {
			return err
		}
	default:
		if len(secret.GetData()) == 0 {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"field 'data' is required")
		}
	}

	return nil
}

func (s *PrivateSecretsServer) validateHubSecretCreate(secret *privatev1.Secret) error {
	if len(secret.GetCoordinates()) == 0 {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'coordinates' is required when backend is HUB")
	}
	if len(secret.GetData()) > 0 {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'data' must be empty when backend is HUB")
	}
	return nil
}

func (s *PrivateSecretsServer) determineTenant(ctx context.Context, secret *privatev1.Secret) (string, error) {
	if t := secret.GetMetadata().GetTenant(); t != "" {
		return t, nil
	}
	t, err := s.tenancyLogic.DetermineDefaultTenant(ctx)
	if err != nil {
		return "", grpcstatus.Errorf(grpccodes.Internal, "failed to determine tenant: %v", err)
	}
	return t, nil
}

func (s *PrivateSecretsServer) validateSecretUpdate(_ context.Context,
	newSecret *privatev1.Secret, existingSecret *privatev1.Secret) error {
	if newSecret.GetBackend() != privatev1.SecretBackend_SECRET_BACKEND_UNSPECIFIED &&
		newSecret.GetBackend() != existingSecret.GetBackend() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'backend' is immutable and cannot be changed from '%s' to '%s'",
			existingSecret.GetBackend(), newSecret.GetBackend())
	}
	if existingSecret.GetBackend() == privatev1.SecretBackend_SECRET_BACKEND_HUB &&
		len(newSecret.GetData()) > 0 {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'data' must be empty when backend is HUB")
	}
	return nil
}
