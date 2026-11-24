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
	"google.golang.org/protobuf/proto"
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
	users   *dao.GenericDAO[*privatev1.User]
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
		AddAllowedTenants(auth.SharedTenant, auth.SystemTenant).
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
	users, err := dao.NewGenericDAO[*privatev1.User]().
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
		users:   users,
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
	prepare := func(ctx context.Context, current, candidate *privatev1.RoleBinding) error {
		if !updateIncludesField(request.GetUpdateMask(), "spec") {
			return nil
		}
		return s.validateRoleBinding(ctx, current, candidate)
	}
	err = s.generic.UpdateWithCandidatePreparation(ctx, request, &response, prepare)
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

func (s *PrivateRoleBindingsServer) validateRoleBinding(ctx context.Context, current, candidate *privatev1.RoleBinding) error {
	// The generic server has already applied the update mask. Metadata and status updates should
	// remain possible even if the binding's Role has since been deleted.
	if current != nil && proto.Equal(current.GetSpec(), candidate.GetSpec()) {
		return nil
	}

	ref := candidate.GetSpec().GetRole()
	if ref.GetId() == "" && ref.GetName() == "" {
		return status.Error(codes.InvalidArgument, "a role reference must specify an ID or name")
	}
	role, err := s.resolveRoleReference(ctx, candidate)
	if err != nil {
		return err
	}
	if err := s.validateUserSubjects(ctx, candidate); err != nil {
		return err
	}
	serviceAccountRefs := candidate.GetSpec().GetServiceAccounts()
	if len(serviceAccountRefs) == 0 {
		return nil
	}
	if err := validateServiceAccountRole(role, candidate.GetMetadata().GetTenant()); err != nil {
		return err
	}
	return validateServiceAccountSubjects(ctx, serviceAccountRefs, candidate.GetMetadata().GetTenant())
}

// validateUserSubjects resolves input selectors after tenant assignment and update-mask merging.
// The stored ID is authoritative for grants; retained names and selectors describe the target.
func (s *PrivateRoleBindingsServer) validateUserSubjects(ctx context.Context, candidate *privatev1.RoleBinding) error {
	tenant := candidate.GetMetadata().GetTenant()
	for _, ref := range candidate.GetSpec().GetUsers() {
		if (ref.GetTenant() != "" && ref.GetTenant() != tenant) || (ref.GetShared() && tenant != auth.SharedTenant) {
			return status.Error(codes.InvalidArgument, "user reference must belong to the RoleBinding tenant")
		}
		user, err := resolveAndCanonicalizeReference(ctx, s.users, candidate.GetMetadata(), ref,
			"user", codes.InvalidArgument)
		if err != nil {
			return err
		}
		if user.GetMetadata().GetTenant() != tenant {
			return status.Error(codes.InvalidArgument, "user reference must belong to the RoleBinding tenant")
		}
	}
	return nil
}

func validateServiceAccountRole(role *privatev1.Role, bindingTenant string) error {
	roleName := role.GetMetadata().GetName()
	roleTenant := role.GetMetadata().GetTenant()
	sharedRole := roleTenant == auth.SharedTenant
	systemCSIRole := roleName == "osac-csi-driver" && roleTenant == auth.SystemTenant &&
		bindingTenant == auth.SystemTenant
	if !serviceaccounts.IsAssignableRole(roleName) || (!sharedRole && !systemCSIRole) {
		return status.Error(codes.InvalidArgument, "service accounts may only receive predefined OSAC roles")
	}
	return nil
}

func validateServiceAccountSubjects(ctx context.Context, refs []*privatev1.ServiceAccountReference, tenant string) error {
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		id := ref.GetId()
		if id == "" {
			return status.Error(codes.InvalidArgument, "service account reference ID is required")
		}
		if _, exists := seen[id]; exists {
			return status.Error(codes.InvalidArgument, "service account references must be unique")
		}
		seen[id] = struct{}{}
		if err := serviceaccounts.ValidateRoleBindingSubject(ctx, id, tenant); err != nil {
			if errors.Is(err, serviceaccounts.ErrRoleBindingSubjectNotActive) {
				return status.Error(codes.InvalidArgument, "service account must be active and provisioned in the RoleBinding tenant")
			}
			return err
		}
	}
	return nil
}

func (s *PrivateRoleBindingsServer) resolveRoleReference(ctx context.Context,
	candidate *privatev1.RoleBinding) (*privatev1.Role, error) {
	ref := candidate.GetSpec().GetRole()
	if ref.GetId() == "" && ref.GetName() != "" && ref.GetTenant() == "" && !ref.GetShared() && ref.GetProject() == "" {
		// Role names are unique globally across all tenants, so a given name
		// identifies at most one Role. Search the binding tenant first so local Roles need no
		// qualification, then the shared catalog; resolveAndCanonicalizeReference rejects a name whose
		// Role lives in any other tenant.
		filter := fmt.Sprintf("this.metadata.name == %q && this.metadata.tenant == %q && this.metadata.deletion_timestamp == null",
			ref.GetName(), candidate.GetMetadata().GetTenant())
		local, err := s.roles.List().SetFilter(filter).SetLimit(1).Do(ctx)
		if err != nil {
			return nil, err
		}
		if len(local.GetItems()) == 0 {
			ref.SetTenant(auth.SharedTenant)
		} else {
			ref.SetId(local.GetItems()[0].GetId())
		}
	}
	role, err := resolveAndCanonicalizeReferenceWithGet(ctx, s.roles, candidate.GetMetadata(), ref,
		"role", codes.InvalidArgument, getShareLockedReferenceResource[*privatev1.Role])
	if err != nil {
		return nil, err
	}
	roleName := role.GetMetadata().GetName()
	roleTenant := role.GetMetadata().GetTenant()
	bindingTenant := candidate.GetMetadata().GetTenant()
	if roleTenant != bindingTenant && roleTenant != auth.SharedTenant {
		return nil, status.Error(codes.InvalidArgument, "role reference does not identify an allowed Role")
	}
	if roleTenant == auth.SystemTenant && (roleName != "osac-csi-driver" || bindingTenant != auth.SystemTenant) {
		return nil, status.Error(codes.InvalidArgument, "system Roles may only be bound in the system tenant")
	}
	return role, nil
}
