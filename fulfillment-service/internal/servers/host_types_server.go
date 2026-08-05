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

type HostTypesServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.HostTypesServer = (*HostTypesServer)(nil)

type HostTypesServer struct {
	publicv1.UnimplementedHostTypesServer

	logger    *slog.Logger
	delegate  privatev1.HostTypesServer
	inMapper  *GenericMapper[*publicv1.HostType, *privatev1.HostType]
	outMapper *GenericMapper[*privatev1.HostType, *publicv1.HostType]
}

func NewHostTypesServer() *HostTypesServerBuilder {
	return &HostTypesServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *HostTypesServerBuilder) SetLogger(value *slog.Logger) *HostTypesServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *HostTypesServerBuilder) SetNotifier(value events.Notifier) *HostTypesServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *HostTypesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *HostTypesServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *HostTypesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *HostTypesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *HostTypesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *HostTypesServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *HostTypesServerBuilder) Build() (result *HostTypesServer, err error) {
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
	inMapper, err := NewGenericMapper[*publicv1.HostType, *privatev1.HostType]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.HostType, *publicv1.HostType]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	// Create the private server to delegate to:
	delegate, err := NewPrivateHostTypesServer().
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
	result = &HostTypesServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *HostTypesServer) List(ctx context.Context,
	request *publicv1.HostTypesListRequest) (response *publicv1.HostTypesListResponse, err error) {
	// Create private request with same parameters:
	privateRequest := &privatev1.HostTypesListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	privateRequest.SetLimit(request.GetLimit())
	privateRequest.SetFilter(request.GetFilter())
	privateRequest.SetOrder(request.GetOrder())

	// Delegate to private server:
	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.HostType, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.HostType{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private host type to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process host types")
		}
		publicItems[i] = publicItem
	}

	// Create the public response:
	response = &publicv1.HostTypesListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *HostTypesServer) Get(ctx context.Context,
	request *publicv1.HostTypesGetRequest) (response *publicv1.HostTypesGetResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.HostTypesGetRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateHostType := privateResponse.GetObject()
	publicHostType := &publicv1.HostType{}
	err = s.outMapper.Copy(ctx, privateHostType, publicHostType)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private host type to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process host type")
	}

	// Create the public response:
	response = &publicv1.HostTypesGetResponse{}
	response.SetObject(publicHostType)
	return
}

func (s *HostTypesServer) Create(ctx context.Context,
	request *publicv1.HostTypesCreateRequest) (response *publicv1.HostTypesCreateResponse, err error) {
	// Map the public host type to private format:
	publicHostType := request.GetObject()
	if publicHostType == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateHostType := &privatev1.HostType{}
	err = s.inMapper.Copy(ctx, publicHostType, privateHostType)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public host type to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process host type")
		return
	}

	// Delegate to the private server:
	privateRequest := &privatev1.HostTypesCreateRequest{}
	privateRequest.SetObject(privateHostType)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	createdPrivateHostType := privateResponse.GetObject()
	createdPublicHostType := &publicv1.HostType{}
	err = s.outMapper.Copy(ctx, createdPrivateHostType, createdPublicHostType)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private host type to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process host type")
		return
	}

	// Create the public response:
	response = &publicv1.HostTypesCreateResponse{}
	response.SetObject(createdPublicHostType)
	return
}

func (s *HostTypesServer) Update(ctx context.Context,
	request *publicv1.HostTypesUpdateRequest) (response *publicv1.HostTypesUpdateResponse, err error) {
	// Validate the request:
	publicHostType := request.GetObject()
	if publicHostType == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	id := publicHostType.GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	// Get the existing object from the private server:
	getRequest := &privatev1.HostTypesGetRequest{}
	getRequest.SetId(id)
	getResponse, err := s.delegate.Get(ctx, getRequest)
	if err != nil {
		return nil, err
	}
	existingPrivateHostType := getResponse.GetObject()

	// Map the public changes to the existing private object (preserving private data):
	err = s.inMapper.Copy(ctx, publicHostType, existingPrivateHostType)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public host type to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process host type")
		return
	}

	// Delegate to the private server with the merged object:
	privateRequest := &privatev1.HostTypesUpdateRequest{}
	privateRequest.SetObject(existingPrivateHostType)
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	updatedPrivateHostType := privateResponse.GetObject()
	updatedPublicHostType := &publicv1.HostType{}
	err = s.outMapper.Copy(ctx, updatedPrivateHostType, updatedPublicHostType)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private host type to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process host type")
		return
	}

	// Create the public response:
	response = &publicv1.HostTypesUpdateResponse{}
	response.SetObject(updatedPublicHostType)
	return
}

func (s *HostTypesServer) Delete(ctx context.Context,
	request *publicv1.HostTypesDeleteRequest) (response *publicv1.HostTypesDeleteResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.HostTypesDeleteRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Create the public response:
	response = &publicv1.HostTypesDeleteResponse{}
	return
}
