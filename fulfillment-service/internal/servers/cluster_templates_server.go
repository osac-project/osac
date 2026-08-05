/*
Copyright (c) 2025 Red Hat Inc.

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

type ClusterTemplatesServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.ClusterTemplatesServer = (*ClusterTemplatesServer)(nil)

type ClusterTemplatesServer struct {
	publicv1.UnimplementedClusterTemplatesServer

	logger    *slog.Logger
	delegate  privatev1.ClusterTemplatesServer
	inMapper  *GenericMapper[*publicv1.ClusterTemplate, *privatev1.ClusterTemplate]
	outMapper *GenericMapper[*privatev1.ClusterTemplate, *publicv1.ClusterTemplate]
}

func NewClusterTemplatesServer() *ClusterTemplatesServerBuilder {
	return &ClusterTemplatesServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *ClusterTemplatesServerBuilder) SetLogger(value *slog.Logger) *ClusterTemplatesServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *ClusterTemplatesServerBuilder) SetNotifier(value events.Notifier) *ClusterTemplatesServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *ClusterTemplatesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *ClusterTemplatesServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *ClusterTemplatesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *ClusterTemplatesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *ClusterTemplatesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *ClusterTemplatesServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *ClusterTemplatesServerBuilder) Build() (result *ClusterTemplatesServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Create the mappers:
	inMapper, err := NewGenericMapper[*publicv1.ClusterTemplate, *privatev1.ClusterTemplate]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.ClusterTemplate, *publicv1.ClusterTemplate]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	// Create the private server to delegate to:
	delegate, err := NewPrivateClusterTemplatesServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &ClusterTemplatesServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *ClusterTemplatesServer) List(ctx context.Context,
	request *publicv1.ClusterTemplatesListRequest) (response *publicv1.ClusterTemplatesListResponse, err error) {
	// Create private request with same parameters:
	privateRequest := &privatev1.ClusterTemplatesListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	privateRequest.SetLimit(request.GetLimit())
	privateRequest.SetFilter(request.GetFilter())

	// Delegate to private server:
	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.ClusterTemplate, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.ClusterTemplate{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private cluster template to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster templates")
		}
		publicItems[i] = publicItem
	}

	// Create the public response:
	response = &publicv1.ClusterTemplatesListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *ClusterTemplatesServer) Get(ctx context.Context,
	request *publicv1.ClusterTemplatesGetRequest) (response *publicv1.ClusterTemplatesGetResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.ClusterTemplatesGetRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateClusterTemplate := privateResponse.GetObject()
	publicClusterTemplate := &publicv1.ClusterTemplate{}
	err = s.outMapper.Copy(ctx, privateClusterTemplate, publicClusterTemplate)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private cluster template to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster template")
	}

	// Create the public response:
	response = &publicv1.ClusterTemplatesGetResponse{}
	response.SetObject(publicClusterTemplate)
	return
}

func (s *ClusterTemplatesServer) Create(ctx context.Context,
	request *publicv1.ClusterTemplatesCreateRequest) (response *publicv1.ClusterTemplatesCreateResponse, err error) {
	// Map the public cluster template to private format:
	publicClusterTemplate := request.GetObject()
	if publicClusterTemplate == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateClusterTemplate := &privatev1.ClusterTemplate{}
	err = s.inMapper.Copy(ctx, publicClusterTemplate, privateClusterTemplate)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public cluster template to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster template")
		return
	}

	// Delegate to the private server:
	privateRequest := &privatev1.ClusterTemplatesCreateRequest{}
	privateRequest.SetObject(privateClusterTemplate)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	createdPrivateClusterTemplate := privateResponse.GetObject()
	createdPublicClusterTemplate := &publicv1.ClusterTemplate{}
	err = s.outMapper.Copy(ctx, createdPrivateClusterTemplate, createdPublicClusterTemplate)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private cluster template to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster template")
		return
	}

	// Create the public response:
	response = &publicv1.ClusterTemplatesCreateResponse{}
	response.SetObject(createdPublicClusterTemplate)
	return
}

func (s *ClusterTemplatesServer) Update(ctx context.Context,
	request *publicv1.ClusterTemplatesUpdateRequest) (response *publicv1.ClusterTemplatesUpdateResponse, err error) {
	// Validate the request:
	publicClusterTemplate := request.GetObject()
	if publicClusterTemplate == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	id := publicClusterTemplate.GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	// Get the existing object from the private server:
	getRequest := &privatev1.ClusterTemplatesGetRequest{}
	getRequest.SetId(id)
	getResponse, err := s.delegate.Get(ctx, getRequest)
	if err != nil {
		return nil, err
	}
	existingPrivateClusterTemplate := getResponse.GetObject()

	// Map the public changes to the existing private object (preserving private data):
	err = s.inMapper.Copy(ctx, publicClusterTemplate, existingPrivateClusterTemplate)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public cluster template to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster template")
		return
	}

	// Delegate to the private server with the merged object:
	privateRequest := &privatev1.ClusterTemplatesUpdateRequest{}
	privateRequest.SetObject(existingPrivateClusterTemplate)
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	updatedPrivateClusterTemplate := privateResponse.GetObject()
	updatedPublicClusterTemplate := &publicv1.ClusterTemplate{}
	err = s.outMapper.Copy(ctx, updatedPrivateClusterTemplate, updatedPublicClusterTemplate)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private cluster template to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster template")
		return
	}

	// Create the public response:
	response = &publicv1.ClusterTemplatesUpdateResponse{}
	response.SetObject(updatedPublicClusterTemplate)
	return
}

func (s *ClusterTemplatesServer) Delete(ctx context.Context,
	request *publicv1.ClusterTemplatesDeleteRequest) (response *publicv1.ClusterTemplatesDeleteResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.ClusterTemplatesDeleteRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Create the public response:
	response = &publicv1.ClusterTemplatesDeleteResponse{}
	return
}
