//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package servers

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// PrivateServiceAccountsServerBuilder is a builder for creating instances of PrivateServiceAccountsServer.
type PrivateServiceAccountsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.ServiceAccountsServer = (*PrivateServiceAccountsServer)(nil)

// PrivateServiceAccountsServer implements the private service accounts gRPC service.
type PrivateServiceAccountsServer struct {
	privatev1.UnimplementedServiceAccountsServer

	generic *GenericServer[*privatev1.ServiceAccount]
}

// NewPrivateServiceAccountsServer creates a new builder for the private service accounts server.
func NewPrivateServiceAccountsServer() *PrivateServiceAccountsServerBuilder {
	return &PrivateServiceAccountsServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *PrivateServiceAccountsServerBuilder) SetLogger(value *slog.Logger) *PrivateServiceAccountsServerBuilder {
	b.logger = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *PrivateServiceAccountsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateServiceAccountsServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *PrivateServiceAccountsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateServiceAccountsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateServiceAccountsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateServiceAccountsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateServiceAccountsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateServiceAccountsServerBuilder {
	b.filterDesc = value
	return b
}

// Build creates the private service accounts server from the builder configuration.
func (b *PrivateServiceAccountsServerBuilder) Build() (result *PrivateServiceAccountsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	generic, err := NewGenericServer[*privatev1.ServiceAccount]().
		SetLogger(b.logger).
		SetService(privatev1.ServiceAccounts_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}
	result = &PrivateServiceAccountsServer{
		generic: generic,
	}
	return
}

func (s *PrivateServiceAccountsServer) List(ctx context.Context,
	request *privatev1.ServiceAccountsListRequest) (response *privatev1.ServiceAccountsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateServiceAccountsServer) Get(ctx context.Context,
	request *privatev1.ServiceAccountsGetRequest) (response *privatev1.ServiceAccountsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateServiceAccountsServer) Create(ctx context.Context,
	request *privatev1.ServiceAccountsCreateRequest) (response *privatev1.ServiceAccountsCreateResponse, err error) {
	if request.GetObject().GetId() != "" {
		return nil, status.Error(codes.InvalidArgument, "service-account ID is assigned by the server")
	}
	err = s.generic.CreateWithCandidatePreparation(ctx, request, &response, s.prepareCreate)
	return
}

func (s *PrivateServiceAccountsServer) prepareCreate(_ context.Context, _, account *privatev1.ServiceAccount) error {
	account.SetStatus(privatev1.ServiceAccountStatus_builder{
		State: privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_PENDING,
	}.Build())
	metadata := account.GetMetadata()
	if metadata.GetDisplayName() != "" || metadata.GetDescription() != "" {
		return status.Error(codes.InvalidArgument, "display_name and description are not supported for service accounts")
	}
	metadata.SetFinalizers([]string{finalizers.Controller})
	annotations := metadata.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations["osac.openshift.io/tenant"] = metadata.GetTenant()
	metadata.SetAnnotations(annotations)
	return nil
}

func (s *PrivateServiceAccountsServer) Update(ctx context.Context,
	request *privatev1.ServiceAccountsUpdateRequest) (response *privatev1.ServiceAccountsUpdateResponse, err error) {
	return s.update(ctx, request)
}

func (s *PrivateServiceAccountsServer) Delete(ctx context.Context,
	request *privatev1.ServiceAccountsDeleteRequest) (response *privatev1.ServiceAccountsDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateServiceAccountsServer) Signal(ctx context.Context,
	request *privatev1.ServiceAccountsSignalRequest) (response *privatev1.ServiceAccountsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

func (s *PrivateServiceAccountsServer) RegenerateSecret(ctx context.Context,
	request *privatev1.ServiceAccountsRegenerateSecretRequest) (*privatev1.ServiceAccountsRegenerateSecretResponse, error) {
	if request.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "service-account ID is mandatory")
	}
	object := &privatev1.ServiceAccount{}
	object.SetId(request.GetId())
	updateRequest := privatev1.ServiceAccountsUpdateRequest_builder{
		Object: object,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
			"status.secret_rotation_state", "status.secret_rotation_error", "status.secret_rotation_generation",
		}},
	}.Build()
	var updateResponse *privatev1.ServiceAccountsUpdateResponse
	err := s.generic.UpdateWithCandidatePreparation(ctx, updateRequest, &updateResponse,
		func(_ context.Context, current, candidate *privatev1.ServiceAccount) error {
			if current.GetMetadata().HasDeletionTimestamp() {
				return status.Error(codes.FailedPrecondition, "service account is being deleted")
			}
			currentStatus := current.GetStatus()
			rotationState := currentStatus.GetSecretRotationState()
			if rotationState == privatev1.ServiceAccountSecretRotationState_SERVICE_ACCOUNT_SECRET_ROTATION_STATE_PENDING ||
				rotationState == privatev1.ServiceAccountSecretRotationState_SERVICE_ACCOUNT_SECRET_ROTATION_STATE_ERROR {
				if currentStatus.GetSecretRotationGeneration() == "" {
					return status.Error(codes.Internal, "service-account rotation state is missing its generation")
				}
				candidateStatus := candidate.GetStatus()
				if candidateStatus == nil {
					candidateStatus = &privatev1.ServiceAccountStatus{}
					candidate.SetStatus(candidateStatus)
				}
				candidateStatus.SetSecretRotationState(rotationState)
				candidateStatus.SetSecretRotationError(currentStatus.GetSecretRotationError())
				candidateStatus.SetSecretRotationGeneration(currentStatus.GetSecretRotationGeneration())
				return nil
			}
			if currentStatus.GetState() != privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_READY ||
				currentStatus.GetKeycloakClientUuid() == "" || currentStatus.GetClientSecret() == nil {
				return status.Error(codes.FailedPrecondition, "service account is not fully provisioned")
			}
			candidateStatus := candidate.GetStatus()
			if candidateStatus == nil {
				candidateStatus = &privatev1.ServiceAccountStatus{}
				candidate.SetStatus(candidateStatus)
			}
			candidateStatus.SetSecretRotationState(
				privatev1.ServiceAccountSecretRotationState_SERVICE_ACCOUNT_SECRET_ROTATION_STATE_PENDING)
			candidateStatus.SetSecretRotationError("")
			candidateStatus.SetSecretRotationGeneration(uuid.NewString())
			return nil
		})
	if err != nil {
		return nil, err
	}
	response := &privatev1.ServiceAccountsRegenerateSecretResponse{}
	response.SetObject(updateResponse.GetObject())
	return response, nil
}

func (s *PrivateServiceAccountsServer) update(ctx context.Context, request *privatev1.ServiceAccountsUpdateRequest) (*privatev1.ServiceAccountsUpdateResponse, error) {
	if request.GetObject().GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "object identifier is mandatory")
	}
	request = proto.Clone(request).(*privatev1.ServiceAccountsUpdateRequest)
	if metadata := request.GetObject().GetMetadata(); metadata.GetDisplayName() != "" || metadata.GetDescription() != "" {
		return nil, status.Error(codes.InvalidArgument, "display_name and description are not supported for service accounts")
	}
	mask := request.GetUpdateMask()
	if mask == nil {
		mask = &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels"}}
		if request.GetObject().GetMetadata().GetName() != "" {
			mask.Paths = append(mask.Paths, "metadata.name")
		}
		request.SetUpdateMask(mask)
	}
	for _, path := range mask.GetPaths() {
		switch path {
		case "metadata.name", "metadata.labels":
		case "status.keycloak_client_uuid", "status.client_secret",
			"status.state", "status.message", "status.registration_secret",
			"status.secret_rotation_state", "status.secret_rotation_error", "status.secret_rotation_generation":
			// GenericServer rejects public status writes; keep these paths writable for private callers.
		case "metadata.finalizers":
			if isPublicAPIView(ctx) {
				return nil, status.Error(codes.InvalidArgument, "finalizers are server-owned")
			}
		default:
			return nil, status.Errorf(codes.InvalidArgument, "field %q is not mutable", path)
		}
	}
	var response *privatev1.ServiceAccountsUpdateResponse
	prepare := s.prepareUpdate
	if len(mask.GetPaths()) == 1 && mask.GetPaths()[0] == "metadata.finalizers" {
		prepare = nil
	}
	err := s.generic.UpdateWithCandidatePreparation(ctx, request, &response, prepare)
	return response, err
}

func (s *PrivateServiceAccountsServer) prepareUpdate(_ context.Context, current, candidate *privatev1.ServiceAccount) error {
	if current.GetMetadata().GetName() != candidate.GetMetadata().GetName() {
		return status.Error(codes.InvalidArgument, "service-account name is immutable")
	}
	return nil
}
