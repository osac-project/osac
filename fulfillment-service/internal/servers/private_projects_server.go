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
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// PrivateProjectsServerBuilder contains the data and logic needed to create a private projects server.
type PrivateProjectsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.ProjectsServer = (*PrivateProjectsServer)(nil)

// PrivateProjectsServer is the implementation of the private projects gRPC service.
type PrivateProjectsServer struct {
	privatev1.UnimplementedProjectsServer
	logger  *slog.Logger
	generic *GenericServer[*privatev1.Project]
	dao     *dao.GenericDAO[*privatev1.Project]
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
		logger: b.logger,
	}

	// Create the DAO used to read the project this server is asked to delete:
	s.dao, err = dao.NewGenericDAO[*privatev1.Project]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the generic server:
	s.generic, err = NewGenericServer[*privatev1.Project]().
		SetLogger(b.logger).
		SetService(privatev1.Projects_ServiceDesc.ServiceName).
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

	// metadata.project must remain empty to derive the parent project from the path
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
	// The project's unsent quota warnings reference the project with a foreign key and have
	// no API of their own. Remove them here, when the deletion is accepted, so they cannot
	// block the final removal of the project row:
	if err = s.deleteQuotaOutbox(ctx, request.GetId()); err != nil {
		return
	}
	err = s.generic.Delete(ctx, request, &response)
	return
}

// deleteQuotaOutbox removes the project's unsent quota warnings, the way the tenants server
// removes a tenant's. The outbox rows reference the project with a foreign key and have no API
// of their own, so they are removed in the Projects/Delete transaction: leaving them behind
// would keep the project from being archived after its finalizers are gone.
func (s *PrivateProjectsServer) deleteQuotaOutbox(ctx context.Context, id string) error {
	getResponse, err := s.dao.Get().SetId(id).Do(ctx)
	if err != nil {
		// The generic Delete below refuses a missing project the same way:
		return ConvertDAOErrorToGRPC(err, "get", id)
	}
	metadata := getResponse.GetObject().GetMetadata()
	return deleteQuotaOutboxRows(ctx, s.logger,
		`delete from quota_outbox where tenant = $1 and project::text = $2`,
		[]any{metadata.GetTenant(), metadata.GetName()},
		fmt.Sprintf("failed to delete project '%s'", metadata.GetName()),
		slog.String("tenant", metadata.GetTenant()),
		slog.String("project", metadata.GetName()),
	)
}

func (s *PrivateProjectsServer) Signal(ctx context.Context,
	request *privatev1.ProjectsSignalRequest) (response *privatev1.ProjectsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}
