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
	"google.golang.org/protobuf/types/known/fieldmaskpb"

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
	filterDesc        protoreflect.MessageDescriptor
	hubSecretFetcher  HubSecretFetcher
}

var _ privatev1.SecretsServer = (*PrivateSecretsServer)(nil)

type PrivateSecretsServer struct {
	privatev1.UnimplementedSecretsServer

	logger           *slog.Logger
	generic          *GenericServer[*privatev1.Secret]
	secretStore      vault.SecretStore
	hubSecretFetcher HubSecretFetcher
	tenancyLogic     auth.TenancyLogic
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

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateSecretsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateSecretsServerBuilder {
	b.filterDesc = value
	return b
}

// SetHubSecretFetcher sets the hub secret fetcher used to retrieve secrets from hub clusters.
// This is optional. When unset, hub secrets cannot be retrieved.
func (b *PrivateSecretsServerBuilder) SetHubSecretFetcher(value HubSecretFetcher) *PrivateSecretsServerBuilder {
	b.hubSecretFetcher = value
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
		logger:           b.logger,
		secretStore:      b.secretStore,
		hubSecretFetcher: b.hubSecretFetcher,
		tenancyLogic:     b.tenancyLogic,
	}

	s.generic, err = NewGenericServer[*privatev1.Secret]().
		SetLogger(b.logger).
		SetService(privatev1.Secrets_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetRedactFunc(s.redact).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		AddAllowedTenants(auth.SharedTenant).
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
	if err = s.authorizeSharedSecretManagement(ctx, obj); err != nil {
		return
	}
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

	if obj.GetBackend() == privatev1.SecretBackend_SECRET_BACKEND_HUB {
		if s.hubSecretFetcher == nil {
			err = grpcstatus.Errorf(grpccodes.FailedPrecondition,
				"hub secret retrieval is not configured")
			return
		}
		data, fetchErr := s.hubSecretFetcher.Fetch(ctx, obj.GetCoordinates())
		if fetchErr != nil {
			err = fetchErr
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

	persistInVault := s.secretStore != nil && secret.GetBackend() == privatev1.SecretBackend_SECRET_BACKEND_VAULT
	var data map[string][]byte
	if persistInVault {
		data = secret.GetData()
		secret.SetData(nil)
	}

	create := func(opCtx context.Context) error {
		if createErr := s.generic.Create(opCtx, request, &response); createErr != nil {
			return createErr
		}
		created := response.GetObject()
		if authErr := s.authorizeSharedSecretManagement(opCtx, created); authErr != nil {
			return authErr
		}
		if created.GetMetadata().GetTenant() == auth.SharedTenant {
			if created.GetBackend() != privatev1.SecretBackend_SECRET_BACKEND_VAULT {
				return grpcstatus.Errorf(grpccodes.InvalidArgument, "shared Secrets must use the Vault backend")
			}
			if s.secretStore == nil {
				return grpcstatus.Errorf(grpccodes.FailedPrecondition,
					"shared Secrets require a configured Vault backend")
			}
		}
		if !persistInVault || isDryRun(opCtx) {
			return nil
		}
		storeErr := s.secretStore.Store(
			opCtx,
			created.GetMetadata().GetTenant(),
			created.GetMetadata().GetProject(),
			created.GetMetadata().GetName(),
			data,
		)
		return vault.ToGrpcError(storeErr)
	}

	err = s.withSavepoint(ctx, create)
	if err != nil {
		response = nil
	}
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
	if err = s.authorizeSharedSecretManagement(ctx, existingSecret); err != nil {
		return
	}

	err = s.validateSecretUpdate(ctx, request.GetObject(), request.GetUpdateMask(), existingSecret)
	if err != nil {
		return
	}

	persistInVault := s.secretStore != nil &&
		existingSecret.GetBackend() == privatev1.SecretBackend_SECRET_BACKEND_VAULT &&
		len(request.GetObject().GetData()) > 0
	var data map[string][]byte
	if persistInVault {
		data = request.GetObject().GetData()
		request.GetObject().SetData(nil)
	}

	update := func(opCtx context.Context) error {
		if updateErr := s.generic.Update(opCtx, request, &response); updateErr != nil {
			return updateErr
		}
		if !persistInVault || isDryRun(opCtx) {
			return nil
		}
		storeErr := s.secretStore.Store(
			opCtx,
			existingSecret.GetMetadata().GetTenant(),
			existingSecret.GetMetadata().GetProject(),
			existingSecret.GetMetadata().GetName(),
			data,
		)
		return vault.ToGrpcError(storeErr)
	}

	err = s.withSavepoint(ctx, update)
	if err != nil {
		response = nil
	}
	return
}

func (s *PrivateSecretsServer) Delete(ctx context.Context,
	request *privatev1.SecretsDeleteRequest) (response *privatev1.SecretsDeleteResponse, err error) {
	// Report errors to the transaction so that post-DB failures (e.g. Vault delete) trigger rollback.
	tx, txErr := database.TxFromContext(ctx)
	if txErr == nil {
		defer tx.ReportError(&err)
	}

	getRequest := &privatev1.SecretsGetRequest{}
	getRequest.SetId(request.GetId())
	var getResponse *privatev1.SecretsGetResponse
	err = s.generic.Get(ctx, getRequest, &getResponse)
	if err != nil {
		return
	}
	obj := getResponse.GetObject()
	if err = s.authorizeSharedSecretManagement(ctx, obj); err != nil {
		return
	}

	err = s.generic.Delete(ctx, request, &response)
	if err != nil {
		return
	}

	if s.secretStore != nil && obj != nil && obj.GetBackend() == privatev1.SecretBackend_SECRET_BACKEND_VAULT {
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

// authorizeSharedSecretManagement restricts decrypted reads and mutations of shared Secrets to
// platform administrators and controllers. Both identities have universal tenant scope. Metadata
// remains listable so shared template references can be resolved without exposing credential data.
func (s *PrivateSecretsServer) authorizeSharedSecretManagement(ctx context.Context, secret *privatev1.Secret) error {
	if secret == nil || secret.GetMetadata().GetTenant() != auth.SharedTenant {
		return nil
	}
	allowed, err := s.canManageSharedSecrets(ctx)
	if err != nil {
		return err
	}
	if !allowed {
		return grpcstatus.Errorf(
			grpccodes.PermissionDenied,
			"shared Secrets can only be read or managed by platform administrators and controllers",
		)
	}
	return nil
}

func (s *PrivateSecretsServer) canManageSharedSecrets(ctx context.Context) (bool, error) {
	assignable, err := s.tenancyLogic.DetermineAssignableTenants(ctx)
	if err != nil {
		return false, grpcstatus.Errorf(grpccodes.Internal, "failed to determine shared Secret access")
	}
	return assignable.Universal(), nil
}

func (s *PrivateSecretsServer) withSavepoint(ctx context.Context, operation func(context.Context) error) error {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return operation(ctx)
	}
	return tx.Savepoint(ctx, operation)
}

func (s *PrivateSecretsServer) Signal(ctx context.Context,
	request *privatev1.SecretsSignalRequest) (response *privatev1.SecretsSignalResponse, err error) {
	getRequest := &privatev1.SecretsGetRequest{}
	getRequest.SetId(request.GetId())
	var getResponse *privatev1.SecretsGetResponse
	if err = s.generic.Get(ctx, getRequest, &getResponse); err != nil {
		return
	}
	if err = s.authorizeSharedSecretManagement(ctx, getResponse.GetObject()); err != nil {
		return
	}
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
	if secret.GetType() == privatev1.SecretType_SECRET_TYPE_UNSPECIFIED {
		secret.SetType(privatev1.SecretType_SECRET_TYPE_OPAQUE)
	}

	switch secret.GetBackend() {
	case privatev1.SecretBackend_SECRET_BACKEND_HUB:
		if err := s.validateHubSecretCreate(secret); err != nil {
			return err
		}
	default:
		return validateSecretData(secret.GetType(), secret.GetData())
	}

	return nil
}

func (s *PrivateSecretsServer) validateHubSecretCreate(secret *privatev1.Secret) error {
	if len(secret.GetCoordinates()) == 0 {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'coordinates' is required when backend is HUB")
	}
	for _, key := range []string{CoordinateHubID, CoordinateNamespace, CoordinateSecretName} {
		if secret.GetCoordinates()[key] == "" {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"coordinate %q is required when backend is HUB", key)
		}
	}
	if len(secret.GetData()) > 0 {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'data' must be empty when backend is HUB")
	}
	return nil
}

func (s *PrivateSecretsServer) validateSecretUpdate(_ context.Context,
	newSecret *privatev1.Secret, updateMask *fieldmaskpb.FieldMask, existingSecret *privatev1.Secret) error {
	if newSecret.GetBackend() != privatev1.SecretBackend_SECRET_BACKEND_UNSPECIFIED &&
		newSecret.GetBackend() != existingSecret.GetBackend() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'backend' is immutable and cannot be changed from '%s' to '%s'",
			existingSecret.GetBackend(), newSecret.GetBackend())
	}
	typeUpdated := updateMask == nil || len(updateMask.GetPaths()) == 0 ||
		updateIncludesField(updateMask, "type")
	if typeUpdated && newSecret.GetType() != existingSecret.GetType() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'type' is immutable and cannot be changed from '%s' to '%s'",
			existingSecret.GetType(), newSecret.GetType())
	}

	dataUpdated := updateMask == nil || len(updateMask.GetPaths()) == 0 ||
		updateIncludesField(updateMask, "data")
	if !dataUpdated {
		return nil
	}
	if existingSecret.GetBackend() == privatev1.SecretBackend_SECRET_BACKEND_HUB && len(newSecret.GetData()) > 0 {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'data' must be empty when backend is HUB")
	}
	if existingSecret.GetBackend() == privatev1.SecretBackend_SECRET_BACKEND_HUB {
		return nil
	}
	return validateSecretData(existingSecret.GetType(), newSecret.GetData())
}

func validateSecretData(secretType privatev1.SecretType, data map[string][]byte) error {
	key := ""
	switch secretType {
	case privatev1.SecretType_SECRET_TYPE_OPAQUE, privatev1.SecretType_SECRET_TYPE_UNSPECIFIED:
		return nil
	case privatev1.SecretType_SECRET_TYPE_PULL_SECRET:
		key = ".dockerconfigjson"
	case privatev1.SecretType_SECRET_TYPE_KUBECONFIG:
		key = "kubeconfig"
	case privatev1.SecretType_SECRET_TYPE_USER_DATA:
		key = "userdata"
	case privatev1.SecretType_SECRET_TYPE_VALUE:
		key = "value"
	default:
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'type' has unknown value %d", secretType)
	}
	if len(data[key]) == 0 {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"secret type %s requires a non-empty data[%q] entry", secretType, key)
	}
	return nil
}
