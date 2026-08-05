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

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/events"
)

// ProjectMembershipsServerBuilder is a builder for creating instances of ProjectMembershipsServer.
type ProjectMembershipsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.ProjectMembershipsServer = (*ProjectMembershipsServer)(nil)

// ProjectMembershipsServer implements the public project memberships gRPC service by delegating to the private server.
type ProjectMembershipsServer struct {
	publicv1.UnimplementedProjectMembershipsServer

	logger    *slog.Logger
	delegate  privatev1.ProjectMembershipsServer
	inMapper  *GenericMapper[*publicv1.ProjectMembership, *privatev1.ProjectMembership]
	outMapper *GenericMapper[*privatev1.ProjectMembership, *publicv1.ProjectMembership]
}

// NewProjectMembershipsServer creates a new builder for the public project memberships server.
func NewProjectMembershipsServer() *ProjectMembershipsServerBuilder {
	return &ProjectMembershipsServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *ProjectMembershipsServerBuilder) SetLogger(value *slog.Logger) *ProjectMembershipsServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *ProjectMembershipsServerBuilder) SetNotifier(value events.Notifier) *ProjectMembershipsServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *ProjectMembershipsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *ProjectMembershipsServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *ProjectMembershipsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *ProjectMembershipsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *ProjectMembershipsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *ProjectMembershipsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// Build creates the public project memberships server from the builder configuration.
func (b *ProjectMembershipsServerBuilder) Build() (result *ProjectMembershipsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	inMapper, err := NewGenericMapper[*publicv1.ProjectMembership, *privatev1.ProjectMembership]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.ProjectMembership, *publicv1.ProjectMembership]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	delegate, err := NewPrivateProjectMembershipsServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &ProjectMembershipsServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *ProjectMembershipsServer) List(ctx context.Context,
	request *publicv1.ProjectMembershipsListRequest) (response *publicv1.ProjectMembershipsListResponse, err error) {
	privateRequest := &privatev1.ProjectMembershipsListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	privateRequest.SetLimit(request.GetLimit())
	privateRequest.SetFilter(request.GetFilter())
	privateRequest.SetOrder(request.GetOrder())

	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.ProjectMembership, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.ProjectMembership{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private project membership to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process project memberships")
		}
		publicItems[i] = publicItem
	}

	response = &publicv1.ProjectMembershipsListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *ProjectMembershipsServer) Get(ctx context.Context,
	request *publicv1.ProjectMembershipsGetRequest) (response *publicv1.ProjectMembershipsGetResponse, err error) {
	privateRequest := &privatev1.ProjectMembershipsGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateProjectMembership := privateResponse.GetObject()
	publicProjectMembership := &publicv1.ProjectMembership{}
	err = s.outMapper.Copy(ctx, privateProjectMembership, publicProjectMembership)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private project membership to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process project membership")
	}

	response = &publicv1.ProjectMembershipsGetResponse{}
	response.SetObject(publicProjectMembership)
	return
}

func (s *ProjectMembershipsServer) Create(ctx context.Context,
	request *publicv1.ProjectMembershipsCreateRequest) (response *publicv1.ProjectMembershipsCreateResponse, err error) {
	publicProjectMembership := request.GetObject()
	if publicProjectMembership == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateProjectMembership := &privatev1.ProjectMembership{}
	err = s.inMapper.Copy(ctx, publicProjectMembership, privateProjectMembership)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public project membership to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process project membership")
		return
	}

	privateRequest := &privatev1.ProjectMembershipsCreateRequest{}
	privateRequest.SetObject(privateProjectMembership)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	createdPrivateProjectMembership := privateResponse.GetObject()
	createdPublicProjectMembership := &publicv1.ProjectMembership{}
	err = s.outMapper.Copy(ctx, createdPrivateProjectMembership, createdPublicProjectMembership)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private project membership to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process project membership")
		return
	}

	response = &publicv1.ProjectMembershipsCreateResponse{}
	response.SetObject(createdPublicProjectMembership)
	return
}

func (s *ProjectMembershipsServer) Update(ctx context.Context,
	request *publicv1.ProjectMembershipsUpdateRequest) (response *publicv1.ProjectMembershipsUpdateResponse, err error) {
	publicProjectMembership := request.GetObject()
	if publicProjectMembership == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	if publicProjectMembership.GetId() == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	// When no update mask is provided, fetch the current private object and copy public fields
	// into it. This preserves private-only fields (like status.users) that don't exist in the
	// public proto. With an update mask, start from a fresh object — the generic server will
	// handle merging with the database object using field mask semantics.
	var privateProjectMembership *privatev1.ProjectMembership
	updateMask := request.GetUpdateMask()
	if len(updateMask.GetPaths()) > 0 {
		privateProjectMembership = &privatev1.ProjectMembership{}
		privateProjectMembership.SetId(publicProjectMembership.GetId())
	} else {
		getRequest := &privatev1.ProjectMembershipsGetRequest{}
		getRequest.SetId(publicProjectMembership.GetId())
		var getResponse *privatev1.ProjectMembershipsGetResponse
		getResponse, err = s.delegate.Get(ctx, getRequest)
		if err != nil {
			return nil, err
		}
		privateProjectMembership = getResponse.GetObject()
	}
	err = s.inMapper.Copy(ctx, publicProjectMembership, privateProjectMembership)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public project membership to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process project membership")
		return
	}

	privateRequest := &privatev1.ProjectMembershipsUpdateRequest{}
	privateRequest.SetObject(privateProjectMembership)
	privateRequest.SetUpdateMask(updateMask)
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	updatedPrivateProjectMembership := privateResponse.GetObject()
	updatedPublicProjectMembership := &publicv1.ProjectMembership{}
	err = s.outMapper.Copy(ctx, updatedPrivateProjectMembership, updatedPublicProjectMembership)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private project membership to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process project membership")
		return
	}

	response = &publicv1.ProjectMembershipsUpdateResponse{}
	response.SetObject(updatedPublicProjectMembership)
	return
}

func (s *ProjectMembershipsServer) Delete(ctx context.Context,
	request *publicv1.ProjectMembershipsDeleteRequest) (response *publicv1.ProjectMembershipsDeleteResponse, err error) {
	privateRequest := &privatev1.ProjectMembershipsDeleteRequest{}
	privateRequest.SetId(request.GetId())

	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	response = &publicv1.ProjectMembershipsDeleteResponse{}
	return
}
