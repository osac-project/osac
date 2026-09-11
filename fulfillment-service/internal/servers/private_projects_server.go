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
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// PrivateProjectsServerBuilder contains the data and logic needed to create a private projects server.
type PrivateProjectsServerBuilder struct {
	logger            *slog.Logger
	notifier          *database.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
	defaultNetworking *DefaultNetworkingProvisioner
}

var _ privatev1.ProjectsServer = (*PrivateProjectsServer)(nil)

// PrivateProjectsServer is the implementation of the private projects gRPC service.
type PrivateProjectsServer struct {
	privatev1.UnimplementedProjectsServer
	logger            *slog.Logger
	generic           *GenericServer[*privatev1.Project]
	defaultNetworking *DefaultNetworkingProvisioner
}

// NewPrivateProjectsServer creates a new builder for the private projects server.
func NewPrivateProjectsServer() *PrivateProjectsServerBuilder {
	return &PrivateProjectsServerBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *PrivateProjectsServerBuilder) SetLogger(value *slog.Logger) *PrivateProjectsServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the database notifier.
func (b *PrivateProjectsServerBuilder) SetNotifier(value *database.Notifier) *PrivateProjectsServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic.
func (b *PrivateProjectsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateProjectsServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic. This is mandatory.
func (b *PrivateProjectsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateProjectsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateProjectsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateProjectsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateProjectsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateProjectsServerBuilder {
	b.filterDesc = value
	return b
}

// SetDefaultNetworkingProvisioner sets the provisioner used to deprovision a tenant's default
// networking resources when its root project is deleted. This is optional; if unset, root project
// deletion will fail whenever default networking resources still exist for the tenant.
func (b *PrivateProjectsServerBuilder) SetDefaultNetworkingProvisioner(
	value *DefaultNetworkingProvisioner,
) *PrivateProjectsServerBuilder {
	b.defaultNetworking = value
	return b
}

// Build creates the private projects server.
func (b *PrivateProjectsServerBuilder) Build() (result *PrivateProjectsServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Create the server early, so that we can use its methods:
	s := &PrivateProjectsServer{
		logger:            b.logger,
		defaultNetworking: b.defaultNetworking,
	}

	// Create the generic server:
	s.generic, err = NewGenericServer[*privatev1.Project]().
		SetLogger(b.logger).
		SetService(privatev1.Projects_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}

	// Return the server:
	result = s
	return
}

func (s *PrivateProjectsServer) List(ctx context.Context,
	request *privatev1.ProjectsListRequest) (response *privatev1.ProjectsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateProjectsServer) Get(ctx context.Context,
	request *privatev1.ProjectsGetRequest) (response *privatev1.ProjectsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateProjectsServer) Create(ctx context.Context,
	request *privatev1.ProjectsCreateRequest) (response *privatev1.ProjectsCreateResponse, err error) {
	// To avoid potential issues with the length of full project names we limit the number of segments of a
	// full project path.
	const max = 4
	object := request.GetObject()
	metadata := object.GetMetadata()
	if metadata == nil {
		err = grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"field 'metadata' is mandatory",
		)
		return
	}

	name := metadata.GetName()
	path := strings.Split(name, ".")
	count := len(path)
	if count > max {
		err = grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"field 'metadata.name' must have at most %d segments, but it has %d",
			max, count,
		)
		return
	}

	// When a project is created, the 'metadata.name' field must be the full name of the project, and the
	// 'metadata.project' field must be the name of the parent project. If the parent project is not specified
	// then it will be calculated from the the name, removing the last component.
	var project string
	if count > 1 {
		project = strings.Join(path[:count-1], ".")
	}
	input := metadata.GetProject()
	if input != "" && input != project {
		err = grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"field 'metadata.project' must be left empty to be derived automatically from "+
				"'metadata.name', or set to '%s' to match the parent prefix, but it is '%s'",
			project, input,
		)
		return
	}
	metadata.SetProject(project)

	// Call the generic server to create the project:
	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateProjectsServer) Update(ctx context.Context,
	request *privatev1.ProjectsUpdateRequest) (response *privatev1.ProjectsUpdateResponse, err error) {

	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateProjectsServer) Delete(ctx context.Context,
	request *privatev1.ProjectsDeleteRequest) (response *privatev1.ProjectsDeleteResponse, err error) {
	// The root project (empty name) owns whatever default networking resources Provision created
	// for the tenant. Those are protected from direct deletion via the API (see
	// validateNotDefault), so nothing else ever removes them — deprovision them here, before the
	// root project's own deletion is attempted, or that deletion will fail with a foreign key
	// violation.
	if s.defaultNetworking != nil {
		getRequest := &privatev1.ProjectsGetRequest{}
		getRequest.SetId(request.GetId())
		var getResponse *privatev1.ProjectsGetResponse
		if err = s.generic.Get(ctx, getRequest, &getResponse); err != nil {
			return
		}
		project := getResponse.GetObject()
		if project.GetMetadata().GetName() == "" {
			tenantName := project.GetMetadata().GetTenant()
			if deprovisionErr := s.defaultNetworking.Deprovision(ctx, tenantName); deprovisionErr != nil {
				err = s.translateDeprovisionError(ctx, tenantName, deprovisionErr)
				return
			}
		}
	}
	err = s.generic.Delete(ctx, request, &response)
	return
}

// translateDeprovisionError translates an error from deprovisioning default networking resources
// into a gRPC status. It mirrors GenericServer.Delete's translation so that, for example, a
// Subnet still in use by a live ComputeInstance surfaces as an actionable FailedPrecondition
// instead of an opaque Internal error.
func (s *PrivateProjectsServer) translateDeprovisionError(ctx context.Context, tenantName string, err error) error {
	if inUseErr, ok := errors.AsType[*dao.ErrInUse](err); ok {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "%s", inUseErr.Error())
	}
	if _, ok := errors.AsType[*dao.ErrDeadlock](err); ok {
		return grpcstatus.Errorf(grpccodes.Aborted, "concurrent modification detected, please retry")
	}
	s.logger.ErrorContext(ctx, "Failed to deprovision default networking",
		slog.String("tenant", tenantName),
		slog.Any("error", err))
	return grpcstatus.Errorf(grpccodes.Internal, "failed to deprovision default networking resources")
}

func (s *PrivateProjectsServer) Signal(ctx context.Context,
	request *privatev1.ProjectsSignalRequest) (response *privatev1.ProjectsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}
