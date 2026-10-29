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
	"fmt"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/serviceaccounts"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// PrivateRoleBindingsServerBuilder is a builder for creating instances of PrivateRoleBindingsServer.
type PrivateRoleBindingsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.RoleBindingsServer = (*PrivateRoleBindingsServer)(nil)

// PrivateRoleBindingsServer implements the private role bindings gRPC service.
type PrivateRoleBindingsServer struct {
	privatev1.UnimplementedRoleBindingsServer

	logger  *slog.Logger
	generic *GenericServer[*privatev1.RoleBinding]
	roles   *dao.GenericDAO[*privatev1.Role]
}

// NewPrivateRoleBindingsServer creates a new builder for the private role bindings server.
func NewPrivateRoleBindingsServer() *PrivateRoleBindingsServerBuilder {
	return &PrivateRoleBindingsServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *PrivateRoleBindingsServerBuilder) SetLogger(value *slog.Logger) *PrivateRoleBindingsServerBuilder {
	b.logger = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *PrivateRoleBindingsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateRoleBindingsServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *PrivateRoleBindingsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateRoleBindingsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateRoleBindingsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateRoleBindingsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateRoleBindingsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateRoleBindingsServerBuilder {
	b.filterDesc = value
	return b
}

// Build creates the private role bindings server from the builder configuration.
func (b *PrivateRoleBindingsServerBuilder) Build() (result *PrivateRoleBindingsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	generic, err := NewGenericServer[*privatev1.RoleBinding]().
		SetLogger(b.logger).
		SetService(privatev1.RoleBindings_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		AddAllowedTenants(auth.SharedTenant).
		Build()
	if err != nil {
		return
	}

	roles, err := dao.NewGenericDAO[*privatev1.Role]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		Build()
	if err != nil {
		return
	}
	result = &PrivateRoleBindingsServer{
		logger:  b.logger,
		generic: generic,
		roles:   roles,
	}
	return
}

func (s *PrivateRoleBindingsServer) List(ctx context.Context,
	request *privatev1.RoleBindingsListRequest) (response *privatev1.RoleBindingsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateRoleBindingsServer) Get(ctx context.Context,
	request *privatev1.RoleBindingsGetRequest) (response *privatev1.RoleBindingsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateRoleBindingsServer) Create(ctx context.Context,
	request *privatev1.RoleBindingsCreateRequest) (response *privatev1.RoleBindingsCreateResponse, err error) {
	err = s.generic.CreateWithCandidatePreparation(ctx, request, &response, s.validateRoleBinding)
	return
}

func (s *PrivateRoleBindingsServer) Update(ctx context.Context,
	request *privatev1.RoleBindingsUpdateRequest) (response *privatev1.RoleBindingsUpdateResponse, err error) {
	err = s.generic.UpdateWithCandidatePreparation(ctx, request, &response, s.validateRoleBinding)
	return
}

func (s *PrivateRoleBindingsServer) Delete(ctx context.Context,
	request *privatev1.RoleBindingsDeleteRequest) (response *privatev1.RoleBindingsDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateRoleBindingsServer) Signal(ctx context.Context,
	request *privatev1.RoleBindingsSignalRequest) (response *privatev1.RoleBindingsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

func (s *PrivateRoleBindingsServer) validateRoleBinding(ctx context.Context, _, candidate *privatev1.RoleBinding) error {
	ref := candidate.GetSpec().GetRole()
	key := ref.GetId()
	if key == "" {
		key = ref.GetName()
	}
	serviceAccountRefs := candidate.GetSpec().GetServiceAccounts()
	hasUsers := len(candidate.GetSpec().GetUsers()) > 0

	// The controller resolves either selector as an ID first, then as a name.
	if serviceaccounts.IsMachineOnlyRole(key) && hasUsers {
		return status.Error(codes.InvalidArgument, "machine-only roles cannot be assigned to human users")
	}
	if key == "" {
		if len(serviceAccountRefs) > 0 {
			return status.Error(codes.InvalidArgument, "a role is required for service account subjects")
		}
		return nil
	}
	roleResponse, err := s.roles.Get().SetId(key).Do(ctx)
	var role *privatev1.Role
	if err != nil {
		// Preserve the existing behavior for unresolved role references.
		if _, ok := errors.AsType[*dao.ErrNotFound](err); !ok {
			return err
		}
		if len(serviceAccountRefs) == 0 {
			return nil
		}
		if ref.GetId() != "" {
			return status.Error(codes.InvalidArgument, "service account role reference ID was not found")
		}
		// RoleBinding references may use a predefined role name instead of its ID.
		if !serviceaccounts.IsAssignableRole(ref.GetName()) {
			return status.Error(codes.InvalidArgument, "service accounts may only receive predefined OSAC roles")
		}
		filter := fmt.Sprintf("this.metadata.name == %q", ref.GetName())
		listResponse, listErr := s.roles.List().SetFilter(filter).SetLimit(2).Do(ctx)
		if listErr != nil {
			return listErr
		}
		if listResponse.GetSize() != 1 {
			return status.Error(codes.InvalidArgument, "service account role reference was not found")
		}
		role = listResponse.GetItems()[0]
	} else {
		role = roleResponse.GetObject()
	}
	roleName := role.GetMetadata().GetName()
	if serviceaccounts.IsMachineOnlyRole(roleName) && hasUsers {
		return status.Error(codes.InvalidArgument, "machine-only roles cannot be assigned to human users")
	}
	if len(serviceAccountRefs) == 0 {
		return nil
	}
	if !serviceaccounts.IsAssignableRole(roleName) || role.GetMetadata().GetTenant() != auth.SharedTenant {
		return status.Error(codes.InvalidArgument, "service accounts may only receive predefined OSAC roles")
	}

	tenant := candidate.GetMetadata().GetTenant()
	seen := make(map[string]struct{}, len(serviceAccountRefs))
	for _, ref := range serviceAccountRefs {
		id := ref.GetId()
		if id == "" {
			return status.Error(codes.InvalidArgument, "service account reference ID is required")
		}
		if _, exists := seen[id]; exists {
			return status.Error(codes.InvalidArgument, "service account references must be unique")
		}
		seen[id] = struct{}{}
		if err = serviceaccounts.ValidateRoleBindingSubject(ctx, id, tenant); err != nil {
			if errors.Is(err, serviceaccounts.ErrRoleBindingSubjectNotActive) {
				return status.Error(codes.InvalidArgument, "service account must be active and provisioned in the RoleBinding tenant")
			}
			return err
		}
	}
	return nil
}
