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
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateStorageBackendsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.StorageBackendsServer = (*PrivateStorageBackendsServer)(nil)

type PrivateStorageBackendsServer struct {
	privatev1.UnimplementedStorageBackendsServer

	logger     *slog.Logger
	generic    *GenericServer[*privatev1.StorageBackend]
	secretsDao *dao.GenericDAO[*privatev1.Secret]
}

func NewPrivateStorageBackendsServer() *PrivateStorageBackendsServerBuilder {
	return &PrivateStorageBackendsServerBuilder{}
}

func (b *PrivateStorageBackendsServerBuilder) SetLogger(value *slog.Logger) *PrivateStorageBackendsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateStorageBackendsServerBuilder) SetNotifier(value events.Notifier) *PrivateStorageBackendsServerBuilder {
	b.notifier = value
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
		logger: b.logger,
	}

	// Create the generic server:
	s.generic, err = NewGenericServer[*privatev1.StorageBackend]().
		SetLogger(b.logger).
		SetService(privatev1.StorageBackends_ServiceDesc.ServiceName).
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

// redact clears sensitive fields from the storage backend before it is included in event notification payloads.
func (s *PrivateStorageBackendsServer) redact(object *privatev1.StorageBackend) *privatev1.StorageBackend {
	spec := object.GetSpec()
	if spec == nil {
		return object
	}
	credentials := spec.GetCredentials()
	if credentials == nil {
		return object
	}
	credentials.SetPassword("")
	return object
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

	getRequest := &privatev1.StorageBackendsGetRequest{}
	getRequest.SetId(id)
	var getResponse *privatev1.StorageBackendsGetResponse
	err = s.generic.Get(ctx, getRequest, &getResponse)
	if err != nil {
		return
	}

	existingSB := getResponse.GetObject()

	err = s.validateStorageBackendUpdate(ctx, request, existingSB)
	if err != nil {
		return
	}

	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateStorageBackendsServer) Delete(ctx context.Context,
	request *privatev1.StorageBackendsDeleteRequest) (response *privatev1.StorageBackendsDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

const (
	passwordField       = "spec.credentials.password"
	passwordSecretField = "spec.credentials.password_secret"
	passwordExclusive   = "password and password_secret are mutually exclusive"
)

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
	return s.validatePasswordSecret(ctx, sb.GetSpec().GetCredentials())
}

func (s *PrivateStorageBackendsServer) validateStorageBackendUpdate(ctx context.Context,
	request *privatev1.StorageBackendsUpdateRequest, existingSB *privatev1.StorageBackend) error {

	newSB := request.GetObject()
	if newSB.GetSpec().GetProvider() != "" && newSB.GetSpec().GetProvider() != existingSB.GetSpec().GetProvider() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.provider' is immutable and cannot be changed from '%s' to '%s'",
			existingSB.GetSpec().GetProvider(), newSB.GetSpec().GetProvider())
	}
	if err := s.validatePasswordMutualExclusionForUpdate(request, existingSB); err != nil {
		return err
	}
	return s.validatePasswordSecret(ctx, newSB.GetSpec().GetCredentials())
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

func (s *PrivateStorageBackendsServer) validatePasswordMutualExclusionForUpdate(
	request *privatev1.StorageBackendsUpdateRequest, existingSB *privatev1.StorageBackend) error {
	creds := request.GetObject().GetSpec().GetCredentials()
	if credentialsPasswordSet(creds) && credentialsPasswordSecretSet(creds) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, passwordExclusive)
	}

	mask := request.GetUpdateMask()
	if mask == nil || len(mask.GetPaths()) == 0 {
		return s.validatePasswordExactlyOne(creds)
	}

	existingCreds := existingSB.GetSpec().GetCredentials()

	// Simulate post-merge state: masked fields come from request, others from existing.
	hasPassword := credentialsPasswordSet(creds)
	if !updateIncludesField(mask, passwordField) {
		hasPassword = credentialsPasswordSet(existingCreds)
	}
	hasSecret := credentialsPasswordSecretSet(creds)
	if !updateIncludesField(mask, passwordSecretField) {
		hasSecret = credentialsPasswordSecretSet(existingCreds)
	}

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
		s.logger.ErrorContext(ctx, "Failed to resolve password_secret reference", "error", err)
		return grpcstatus.Errorf(grpccodes.Internal, "failed to resolve password_secret reference")
	}
	resolvedRef := &privatev1.SecretLocalReference{}
	resolvedRef.SetId(resolved.ID)
	resolvedRef.SetName(resolved.Name)
	creds.SetPasswordSecret(resolvedRef)
	return nil
}
