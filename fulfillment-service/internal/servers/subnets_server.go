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

type SubnetsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.SubnetsServer = (*SubnetsServer)(nil)

type SubnetsServer struct {
	publicv1.UnimplementedSubnetsServer

	logger    *slog.Logger
	delegate  privatev1.SubnetsServer
	inMapper  *GenericMapper[*publicv1.Subnet, *privatev1.Subnet]
	outMapper *GenericMapper[*privatev1.Subnet, *publicv1.Subnet]
}

func NewSubnetsServer() *SubnetsServerBuilder {
	return &SubnetsServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *SubnetsServerBuilder) SetLogger(value *slog.Logger) *SubnetsServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *SubnetsServerBuilder) SetNotifier(value events.Notifier) *SubnetsServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *SubnetsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *SubnetsServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *SubnetsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *SubnetsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *SubnetsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *SubnetsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *SubnetsServerBuilder) Build() (result *SubnetsServer, err error) {
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
	inMapper, err := NewGenericMapper[*publicv1.Subnet, *privatev1.Subnet]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.Subnet, *publicv1.Subnet]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	// Create the private server to delegate to:
	delegate, err := NewPrivateSubnetsServer().
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
	result = &SubnetsServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *SubnetsServer) List(ctx context.Context,
	request *publicv1.SubnetsListRequest) (response *publicv1.SubnetsListResponse, err error) {
	// Create private request with same parameters:
	privateRequest := &privatev1.SubnetsListRequest{}
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
	publicItems := make([]*publicv1.Subnet, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.Subnet{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private subnet to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process subnets")
		}
		publicItems[i] = publicItem
	}

	// Create the public response:
	response = &publicv1.SubnetsListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *SubnetsServer) Get(ctx context.Context,
	request *publicv1.SubnetsGetRequest) (response *publicv1.SubnetsGetResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.SubnetsGetRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateSubnet := privateResponse.GetObject()
	publicSubnet := &publicv1.Subnet{}
	err = s.outMapper.Copy(ctx, privateSubnet, publicSubnet)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private subnet to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process subnet")
	}

	// Create the public response:
	response = &publicv1.SubnetsGetResponse{}
	response.SetObject(publicSubnet)
	return
}

func (s *SubnetsServer) Create(ctx context.Context,
	request *publicv1.SubnetsCreateRequest) (response *publicv1.SubnetsCreateResponse, err error) {
	// Map the public subnet to private format:
	publicSubnet := request.GetObject()
	if publicSubnet == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateSubnet := &privatev1.Subnet{}
	err = s.inMapper.Copy(ctx, publicSubnet, privateSubnet)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public subnet to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process subnet")
		return
	}

	// Delegate to the private server:
	privateRequest := &privatev1.SubnetsCreateRequest{}
	privateRequest.SetObject(privateSubnet)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	createdPrivateSubnet := privateResponse.GetObject()
	createdPublicSubnet := &publicv1.Subnet{}
	err = s.outMapper.Copy(ctx, createdPrivateSubnet, createdPublicSubnet)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private subnet to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process subnet")
		return
	}

	// Create the public response:
	response = &publicv1.SubnetsCreateResponse{}
	response.SetObject(createdPublicSubnet)
	return
}

func (s *SubnetsServer) Update(ctx context.Context,
	request *publicv1.SubnetsUpdateRequest) (response *publicv1.SubnetsUpdateResponse, err error) {
	// Validate the request:
	publicSubnet := request.GetObject()
	if publicSubnet == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	id := publicSubnet.GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	// Get the existing object from the private server:
	getRequest := &privatev1.SubnetsGetRequest{}
	getRequest.SetId(id)
	getResponse, err := s.delegate.Get(ctx, getRequest)
	if err != nil {
		return nil, err
	}
	existingPrivateSubnet := getResponse.GetObject()

	// Map the public changes to the existing private object (preserving private data):
	err = s.inMapper.Copy(ctx, publicSubnet, existingPrivateSubnet)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public subnet to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process subnet")
		return
	}

	// Delegate to the private server with the merged object:
	privateRequest := &privatev1.SubnetsUpdateRequest{}
	privateRequest.SetObject(existingPrivateSubnet)
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	updatedPrivateSubnet := privateResponse.GetObject()
	updatedPublicSubnet := &publicv1.Subnet{}
	err = s.outMapper.Copy(ctx, updatedPrivateSubnet, updatedPublicSubnet)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private subnet to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process subnet")
		return
	}

	// Create the public response:
	response = &publicv1.SubnetsUpdateResponse{}
	response.SetObject(updatedPublicSubnet)
	return
}

func (s *SubnetsServer) Delete(ctx context.Context,
	request *publicv1.SubnetsDeleteRequest) (response *publicv1.SubnetsDeleteResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.SubnetsDeleteRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Create the public response:
	response = &publicv1.SubnetsDeleteResponse{}
	return
}
