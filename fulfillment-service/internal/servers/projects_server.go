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

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database"
)

// ProjectsServerBuilder contains the data and logic needed to create a public projects server.
type ProjectsServerBuilder struct {
	logger            *slog.Logger
	notifier          *database.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.ProjectsServer = (*ProjectsServer)(nil)

// ProjectsServer is the implementation of the public projects gRPC service. It delegates to the private server
// after mapping between public and private types.
type ProjectsServer struct {
	publicv1.UnimplementedProjectsServer

	logger    *slog.Logger
	private   privatev1.ProjectsServer
	inMapper  *GenericMapper[*publicv1.Project, *privatev1.Project]
	outMapper *GenericMapper[*privatev1.Project, *publicv1.Project]
}

// NewProjectsServer creates a new builder for the public projects server.
func NewProjectsServer() *ProjectsServerBuilder {
	return &ProjectsServerBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *ProjectsServerBuilder) SetLogger(value *slog.Logger) *ProjectsServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the database notifier.
func (b *ProjectsServerBuilder) SetNotifier(value *database.Notifier) *ProjectsServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic.
func (b *ProjectsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *ProjectsServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic. This is mandatory.
func (b *ProjectsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *ProjectsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *ProjectsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *ProjectsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// Build creates the public projects server.
func (b *ProjectsServerBuilder) Build() (result *ProjectsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.attributionLogic == nil {
		err = errors.New("attribution logic is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	inMapper, err := NewGenericMapper[*publicv1.Project, *privatev1.Project]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.Project, *publicv1.Project]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	delegate, err := NewPrivateProjectsServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &ProjectsServer{
		logger:    b.logger,
		private:   delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *ProjectsServer) List(ctx context.Context,
	request *publicv1.ProjectsListRequest) (response *publicv1.ProjectsListResponse, err error) {
	privateRequest := &privatev1.ProjectsListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	privateRequest.SetLimit(request.GetLimit())
	privateRequest.SetOrder(request.GetOrder())
	privateRequest.SetFilter(request.GetFilter())
	privateResponse, err := s.private.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.Project, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.Project{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to map private project to public", slog.Any("error", err))
			return nil, err
		}
		publicItems[i] = publicItem
	}

	response = &publicv1.ProjectsListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *ProjectsServer) Get(ctx context.Context,
	request *publicv1.ProjectsGetRequest) (response *publicv1.ProjectsGetResponse, err error) {
	privateRequest := &privatev1.ProjectsGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.private.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	publicObject := &publicv1.Project{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicObject)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private project to public", slog.Any("error", err))
		return nil, err
	}

	response = &publicv1.ProjectsGetResponse{}
	response.SetObject(publicObject)
	return
}

func (s *ProjectsServer) Create(ctx context.Context,
	request *publicv1.ProjectsCreateRequest) (response *publicv1.ProjectsCreateResponse, err error) {
	privateObject := &privatev1.Project{}
	err = s.inMapper.Copy(ctx, request.GetObject(), privateObject)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map public project to private", slog.Any("error", err))
		return nil, err
	}

	privateRequest := &privatev1.ProjectsCreateRequest{}
	privateRequest.SetObject(privateObject)

	privateResponse, err := s.private.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	publicObject := &publicv1.Project{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicObject)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private project to public", slog.Any("error", err))
		return nil, err
	}

	response = &publicv1.ProjectsCreateResponse{}
	response.SetObject(publicObject)
	return
}

func (s *ProjectsServer) Update(ctx context.Context,
	request *publicv1.ProjectsUpdateRequest) (response *publicv1.ProjectsUpdateResponse, err error) {
	privateObject := &privatev1.Project{}
	err = s.inMapper.Copy(ctx, request.GetObject(), privateObject)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map public project to private", slog.Any("error", err))
		return nil, err
	}

	privateRequest := &privatev1.ProjectsUpdateRequest{}
	privateRequest.SetObject(privateObject)
	privateRequest.SetUpdateMask(request.GetUpdateMask())

	privateResponse, err := s.private.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	publicObject := &publicv1.Project{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicObject)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private project to public", slog.Any("error", err))
		return nil, err
	}

	response = &publicv1.ProjectsUpdateResponse{}
	response.SetObject(publicObject)
	return
}

func (s *ProjectsServer) Delete(ctx context.Context,
	request *publicv1.ProjectsDeleteRequest) (response *publicv1.ProjectsDeleteResponse, err error) {
	privateRequest := &privatev1.ProjectsDeleteRequest{}
	privateRequest.SetId(request.GetId())

	_, err = s.private.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	response = &publicv1.ProjectsDeleteResponse{}
	return
}
