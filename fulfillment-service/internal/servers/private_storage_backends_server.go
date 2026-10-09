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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateStorageBackendsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
	secretsServer     privatev1.SecretsServer
	registrationProbe StorageBackendRegistrationProbe
}

var _ privatev1.StorageBackendsServer = (*PrivateStorageBackendsServer)(nil)

type PrivateStorageBackendsServer struct {
	privatev1.UnimplementedStorageBackendsServer

	logger            *slog.Logger
	generic           *GenericServer[*privatev1.StorageBackend]
	secretsDao        *dao.GenericDAO[*privatev1.Secret]
	secretsServer     privatev1.SecretsServer
	registrationProbe StorageBackendRegistrationProbe
}

func NewPrivateStorageBackendsServer() *PrivateStorageBackendsServerBuilder {
	return &PrivateStorageBackendsServerBuilder{}
}

func (b *PrivateStorageBackendsServerBuilder) SetLogger(value *slog.Logger) *PrivateStorageBackendsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateStorageBackendsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateStorageBackendsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateStorageBackendsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateStorageBackendsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateStorageBackendsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateStorageBackendsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateStorageBackendsServerBuilder) SetSecretsServer(value privatev1.SecretsServer) *PrivateStorageBackendsServerBuilder {
	b.secretsServer = value
	return b
}

func (b *PrivateStorageBackendsServerBuilder) SetRegistrationProbe(value StorageBackendRegistrationProbe) *PrivateStorageBackendsServerBuilder {
	b.registrationProbe = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateStorageBackendsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateStorageBackendsServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateStorageBackendsServerBuilder) Build() (result *PrivateStorageBackendsServer, err error) {
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
	s := &PrivateStorageBackendsServer{
		logger:            b.logger,
		secretsServer:     b.secretsServer,
		registrationProbe: b.registrationProbe,
	}
	if s.registrationProbe == nil {
		s.registrationProbe = NewOntapRegistrationProbe(nil)
	}

	// Create the generic server:
	s.generic, err = NewGenericServer[*privatev1.StorageBackend]().
		SetLogger(b.logger).
		SetService(privatev1.StorageBackends_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		AddAllowedTenants(auth.SharedTenant).
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

func (s *PrivateStorageBackendsServer) List(ctx context.Context,
	request *privatev1.StorageBackendsListRequest) (response *privatev1.StorageBackendsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateStorageBackendsServer) Get(ctx context.Context,
	request *privatev1.StorageBackendsGetRequest) (response *privatev1.StorageBackendsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateStorageBackendsServer) Create(ctx context.Context,
	request *privatev1.StorageBackendsCreateRequest) (response *privatev1.StorageBackendsCreateResponse, err error) {
	err = s.validateStorageBackendCreate(ctx, request.GetObject())
	if err != nil {
		return
	}

	sb := request.GetObject()
	if sb.Status == nil {
		sb.SetStatus(&privatev1.StorageBackendStatus{})
	}
	sb.GetStatus().SetState(privatev1.StorageBackendState_STORAGE_BACKEND_STATE_READY)

	sb.SetId("")

	// StorageBackend is platform-scoped; force tenant to "shared" so all authenticated users can see it.
	if sb.GetMetadata() == nil {
		sb.SetMetadata(&privatev1.Metadata{})
	}
	sb.GetMetadata().SetTenant(auth.SharedTenant)

	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateStorageBackendsServer) Update(ctx context.Context,
	request *privatev1.StorageBackendsUpdateRequest) (response *privatev1.StorageBackendsUpdateResponse, err error) {
	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	// Prepare the effective request without locking the row. Slow Secret retrieval
	// and ONTAP discovery must not block another administrator updating/deleting it.
	current, candidate, err := s.generic.prepareUpdateCandidate(ctx, request,
		func(ctx context.Context, current, candidate *privatev1.StorageBackend) error {
			return s.validateStorageBackendUpdate(ctx, candidate, current)
		}, true, false)
	if err != nil {
		return nil, err
	}
	if current.GetSpec().GetProvider() != "ontap" {
		err = s.generic.UpdateWithValidation(ctx, request, &response, s.validateStorageBackendUpdate)
		return
	}
	if !proto.Equal(candidate.GetSpec().GetCredentials(), current.GetSpec().GetCredentials()) {
		if err = s.probeOntapBackend(ctx, candidate); err != nil {
			return nil, err
		}
	}

	// Reuse the normal locked merge, rejecting concurrent changes even when the
	// caller did not request optimistic locking. Never save unprobed credentials.
	err = s.generic.UpdateWithValidation(ctx, request, &response,
		func(ctx context.Context, updated, stored *privatev1.StorageBackend) error {
			if stored.GetMetadata().GetVersion() != current.GetMetadata().GetVersion() {
				return grpcstatus.Error(grpccodes.Aborted, "storage backend changed during validation; retry the update")
			}
			if err := s.validateStorageBackendUpdate(ctx, updated, stored); err != nil {
				return err
			}
			if !proto.Equal(updated.GetSpec(), candidate.GetSpec()) {
				return grpcstatus.Error(grpccodes.Aborted, "storage backend configuration changed during validation; retry the update")
			}
			return nil
		})
	return
}

func (s *PrivateStorageBackendsServer) Delete(ctx context.Context,
	request *privatev1.StorageBackendsDeleteRequest) (response *privatev1.StorageBackendsDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

const passwordExclusive = "password and password_secret are mutually exclusive"

func (s *PrivateStorageBackendsServer) validateStorageBackendCreate(ctx context.Context,
	sb *privatev1.StorageBackend) error {

	if sb == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "storage backend is mandatory")
	}
	if sb.GetMetadata() == nil || sb.GetMetadata().GetName() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'metadata.name' is required")
	}
	if err := s.validatePasswordExactlyOne(sb.GetSpec().GetCredentials()); err != nil {
		return err
	}
	if err := s.validatePasswordSecret(ctx, sb.GetSpec().GetCredentials()); err != nil {
		return err
	}
	if sb.GetSpec().GetProvider() == "ontap" {
		if err := s.generic.validator.Validate(sb); err != nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument, "validation failed: %s", err)
		}
		return s.probeOntapBackend(ctx, sb)
	}
	return nil
}

func (s *PrivateStorageBackendsServer) validateStorageBackendUpdate(ctx context.Context,
	newSB, existingSB *privatev1.StorageBackend) error {

	if err := s.validatePasswordExactlyOne(newSB.GetSpec().GetCredentials()); err != nil {
		return err
	}
	if newSB.GetSpec().GetProvider() != existingSB.GetSpec().GetProvider() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.provider' is immutable and cannot be changed from '%s' to '%s'",
			existingSB.GetSpec().GetProvider(), newSB.GetSpec().GetProvider())
	}
	ontap := existingSB.GetSpec().GetProvider() == "ontap"
	if ontap && newSB.GetSpec().GetEndpoint() != existingSB.GetSpec().GetEndpoint() {
		return grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.endpoint' is immutable for ONTAP; register a replacement backend")
	}
	if proto.Equal(newSB.GetSpec().GetCredentials(), existingSB.GetSpec().GetCredentials()) {
		return nil
	}
	if err := s.validatePasswordSecret(ctx, newSB.GetSpec().GetCredentials()); err != nil {
		return err
	}
	if ontap {
		if err := s.generic.validator.Validate(newSB); err != nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument, "validation failed: %s", err)
		}
	}
	return nil
}

func credentialsPasswordSet(creds *privatev1.StorageBackendCredentials) bool {
	return creds.GetPassword() != ""
}

func credentialsPasswordSecretSet(creds *privatev1.StorageBackendCredentials) bool {
	return creds.GetPasswordSecret() != nil
}

func (s *PrivateStorageBackendsServer) validatePasswordExactlyOne(
	creds *privatev1.StorageBackendCredentials) error {
	if creds == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.credentials' is required")
	}
	hasPassword := credentialsPasswordSet(creds)
	hasSecret := credentialsPasswordSecretSet(creds)
	if hasPassword && hasSecret {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, passwordExclusive)
	}
	if !hasPassword && !hasSecret {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"exactly one of password or password_secret must be set")
	}
	return nil
}

func (s *PrivateStorageBackendsServer) validatePasswordSecret(ctx context.Context,
	creds *privatev1.StorageBackendCredentials) error {
	if creds == nil {
		return nil
	}
	ref := creds.GetPasswordSecret()
	if ref == nil {
		return nil
	}
	if ref.GetId() == "" && ref.GetName() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "password_secret must specify id or name")
	}
	resolved, err := resolveSecretReferenceOfType(ctx, s.logger, s.secretsDao, ref,
		"password_secret", privatev1.SecretType_SECRET_TYPE_VALUE)
	if err != nil {
		return err
	}
	resolvedRef := &privatev1.SecretLocalReference{}
	resolvedRef.SetId(resolved.ID)
	resolvedRef.SetName(resolved.Name)
	creds.SetPasswordSecret(resolvedRef)
	return nil
}

func (s *PrivateStorageBackendsServer) probeOntapBackend(ctx context.Context, backend *privatev1.StorageBackend) error {
	spec := backend.GetSpec()
	credentials := spec.GetCredentials()
	password := credentials.GetPassword()
	if ref := credentials.GetPasswordSecret(); ref != nil {
		if s.secretsServer == nil {
			return grpcstatus.Error(grpccodes.FailedPrecondition, "ONTAP discovery Secret retrieval is not configured")
		}
		response, err := s.secretsServer.Get(ctx, privatev1.SecretsGetRequest_builder{Id: ref.GetId()}.Build())
		if err != nil {
			return grpcstatus.Error(grpcstatus.Code(err), "unable to read ONTAP discovery password Secret")
		}
		secret := response.GetObject()
		if err := validateResolvedSecretLifecycleAndType(secret, ref.GetId(), "password_secret", privatev1.SecretType_SECRET_TYPE_VALUE); err != nil {
			return err
		}
		password = string(secret.GetData()["value"])
		if password == "" {
			return grpcstatus.Error(grpccodes.InvalidArgument, "ONTAP discovery password Secret must have non-empty data[\"value\"]")
		}
	}
	return s.registrationProbe.Probe(ctx, spec.GetEndpoint(), credentials.GetUsername(), password)
}
