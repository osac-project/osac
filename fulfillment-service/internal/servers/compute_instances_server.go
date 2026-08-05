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

type ComputeInstancesServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.ComputeInstancesServer = (*ComputeInstancesServer)(nil)

type ComputeInstancesServer struct {
	publicv1.UnimplementedComputeInstancesServer

	logger    *slog.Logger
	delegate  privatev1.ComputeInstancesServer
	inMapper  *GenericMapper[*publicv1.ComputeInstance, *privatev1.ComputeInstance]
	outMapper *GenericMapper[*privatev1.ComputeInstance, *publicv1.ComputeInstance]
}

func NewComputeInstancesServer() *ComputeInstancesServerBuilder {
	return &ComputeInstancesServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *ComputeInstancesServerBuilder) SetLogger(value *slog.Logger) *ComputeInstancesServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *ComputeInstancesServerBuilder) SetNotifier(value events.Notifier) *ComputeInstancesServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *ComputeInstancesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *ComputeInstancesServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *ComputeInstancesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *ComputeInstancesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *ComputeInstancesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *ComputeInstancesServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *ComputeInstancesServerBuilder) Build() (result *ComputeInstancesServer, err error) {
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
	inMapper, err := NewGenericMapper[*publicv1.ComputeInstance, *privatev1.ComputeInstance]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.ComputeInstance, *publicv1.ComputeInstance]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	// Create the private server to delegate to:
	delegate, err := NewPrivateComputeInstancesServer().
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
	result = &ComputeInstancesServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *ComputeInstancesServer) List(ctx context.Context,
	request *publicv1.ComputeInstancesListRequest) (response *publicv1.ComputeInstancesListResponse, err error) {
	// Create private request with same parameters:
	privateRequest := &privatev1.ComputeInstancesListRequest{}
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
	publicItems := make([]*publicv1.ComputeInstance, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.ComputeInstance{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private compute instance to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process compute instances")
		}
		publicItems[i] = publicItem
	}

	// Create the public response:
	response = &publicv1.ComputeInstancesListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *ComputeInstancesServer) Get(ctx context.Context,
	request *publicv1.ComputeInstancesGetRequest) (response *publicv1.ComputeInstancesGetResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.ComputeInstancesGetRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateComputeInstance := privateResponse.GetObject()
	publicComputeInstance := &publicv1.ComputeInstance{}
	err = s.outMapper.Copy(ctx, privateComputeInstance, publicComputeInstance)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private compute instance to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process compute instance")
	}

	// Create the public response:
	response = &publicv1.ComputeInstancesGetResponse{}
	response.SetObject(publicComputeInstance)
	return
}

func (s *ComputeInstancesServer) Create(ctx context.Context,
	request *publicv1.ComputeInstancesCreateRequest) (response *publicv1.ComputeInstancesCreateResponse, err error) {
	// Map the public compute instance to private format:
	publicComputeInstance := request.GetObject()
	if publicComputeInstance == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateComputeInstance := &privatev1.ComputeInstance{}
	err = s.inMapper.Copy(ctx, publicComputeInstance, privateComputeInstance)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public compute instance to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process compute instance")
		return
	}

	// Delegate to the private server:
	privateRequest := &privatev1.ComputeInstancesCreateRequest{}
	privateRequest.SetObject(privateComputeInstance)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	createdPrivateComputeInstance := privateResponse.GetObject()
	createdPublicComputeInstance := &publicv1.ComputeInstance{}
	err = s.outMapper.Copy(ctx, createdPrivateComputeInstance, createdPublicComputeInstance)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private compute instance to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process compute instance")
		return
	}

	// Create the public response:
	response = &publicv1.ComputeInstancesCreateResponse{}
	response.SetObject(createdPublicComputeInstance)
	// Propagate warnings from the private server response (deprecation notices
	// for DEPRECATED instance types).
	response.SetWarnings(privateResponse.GetWarnings())
	return
}

func (s *ComputeInstancesServer) Update(ctx context.Context,
	request *publicv1.ComputeInstancesUpdateRequest) (response *publicv1.ComputeInstancesUpdateResponse, err error) {
	// Validate the request:
	publicComputeInstance := request.GetObject()
	if publicComputeInstance == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	id := publicComputeInstance.GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	// Determine how to prepare the private compute instance based on whether there's a field mask.
	// When there's a field mask, copy to a new object and let the generic server handle the merge
	// with the database object, which correctly applies field mask semantics.
	var privateComputeInstance *privatev1.ComputeInstance
	updateMask := request.GetUpdateMask()
	if len(updateMask.GetPaths()) > 0 {
		privateComputeInstance = &privatev1.ComputeInstance{}
		privateComputeInstance.SetId(id)
	} else {
		getRequest := &privatev1.ComputeInstancesGetRequest{}
		getRequest.SetId(id)
		getResponse, err := s.delegate.Get(ctx, getRequest)
		if err != nil {
			return nil, err
		}
		privateComputeInstance = getResponse.GetObject()
	}
	err = s.inMapper.Copy(ctx, publicComputeInstance, privateComputeInstance)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public compute instance to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process compute instance")
		return
	}

	// Delegate to the private server:
	privateRequest := &privatev1.ComputeInstancesUpdateRequest{}
	privateRequest.SetObject(privateComputeInstance)
	privateRequest.SetUpdateMask(updateMask)
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	updatedPrivateComputeInstance := privateResponse.GetObject()
	updatedPublicComputeInstance := &publicv1.ComputeInstance{}
	err = s.outMapper.Copy(ctx, updatedPrivateComputeInstance, updatedPublicComputeInstance)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private compute instance to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process compute instance")
		return
	}

	// Create the public response:
	response = &publicv1.ComputeInstancesUpdateResponse{}
	response.SetObject(updatedPublicComputeInstance)
	return
}

func (s *ComputeInstancesServer) Delete(ctx context.Context,
	request *publicv1.ComputeInstancesDeleteRequest) (response *publicv1.ComputeInstancesDeleteResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.ComputeInstancesDeleteRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Create the public response:
	response = &publicv1.ComputeInstancesDeleteResponse{}
	return
}
