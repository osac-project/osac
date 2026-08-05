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
	"fmt"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/events"
)

// instanceTypeFilterDefaults defines per-field default predicates for public List requests.
// Each default is applied independently: if the user's filter references a field, that field's
// default is skipped. Uses numeric enum values because CEL types proto enums as int.
var instanceTypeFilterDefaults = []filterDefault{
	{
		field: "this.spec.state",
		predicate: fmt.Sprintf("(this.spec.state == %d || this.spec.state == %d)",
			int32(privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE),
			int32(privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED)),
	},
}

type InstanceTypesServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.InstanceTypesServer = (*InstanceTypesServer)(nil)

type InstanceTypesServer struct {
	publicv1.UnimplementedInstanceTypesServer

	logger    *slog.Logger
	delegate  privatev1.InstanceTypesServer
	inMapper  *GenericMapper[*publicv1.InstanceType, *privatev1.InstanceType]
	outMapper *GenericMapper[*privatev1.InstanceType, *publicv1.InstanceType]
}

func NewInstanceTypesServer() *InstanceTypesServerBuilder {
	return &InstanceTypesServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *InstanceTypesServerBuilder) SetLogger(value *slog.Logger) *InstanceTypesServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *InstanceTypesServerBuilder) SetNotifier(value events.Notifier) *InstanceTypesServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is mandatory.
func (b *InstanceTypesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *InstanceTypesServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *InstanceTypesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *InstanceTypesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *InstanceTypesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *InstanceTypesServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *InstanceTypesServerBuilder) Build() (result *InstanceTypesServer, err error) {
	// Check parameters:
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

	// Create the mappers:
	inMapper, err := NewGenericMapper[*publicv1.InstanceType, *privatev1.InstanceType]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.InstanceType, *publicv1.InstanceType]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	// Create the private server to delegate to:
	delegate, err := NewPrivateInstanceTypesServer().
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
	result = &InstanceTypesServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *InstanceTypesServer) List(ctx context.Context,
	request *publicv1.InstanceTypesListRequest) (response *publicv1.InstanceTypesListResponse, err error) {
	// Create private request with same parameters:
	privateRequest := &privatev1.InstanceTypesListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	privateRequest.SetLimit(request.GetLimit())
	composedFilter, err := composeFilterDefaults(request.GetFilter(), instanceTypeFilterDefaults)
	if err != nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "%v", err)
	}
	privateRequest.SetFilter(composedFilter)
	privateRequest.SetOrder(request.GetOrder())

	// Delegate to private server:
	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.InstanceType, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.InstanceType{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private instance type to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process instance types")
		}
		publicItems[i] = publicItem
	}

	// Create the public response:
	response = &publicv1.InstanceTypesListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *InstanceTypesServer) Get(ctx context.Context,
	request *publicv1.InstanceTypesGetRequest) (response *publicv1.InstanceTypesGetResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.InstanceTypesGetRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateInstanceType := privateResponse.GetObject()
	publicInstanceType := &publicv1.InstanceType{}
	err = s.outMapper.Copy(ctx, privateInstanceType, publicInstanceType)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private instance type to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process instance type")
	}

	// Create the public response:
	response = &publicv1.InstanceTypesGetResponse{}
	response.SetObject(publicInstanceType)
	return
}

func (s *InstanceTypesServer) Create(ctx context.Context,
	request *publicv1.InstanceTypesCreateRequest) (response *publicv1.InstanceTypesCreateResponse, err error) {
	// Map the public instance type to private format:
	publicInstanceType := request.GetObject()
	if publicInstanceType == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateInstanceType := &privatev1.InstanceType{}
	err = s.inMapper.Copy(ctx, publicInstanceType, privateInstanceType)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public instance type to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process instance type")
		return
	}

	// Delegate to the private server:
	privateRequest := &privatev1.InstanceTypesCreateRequest{}
	privateRequest.SetObject(privateInstanceType)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	createdPrivateInstanceType := privateResponse.GetObject()
	createdPublicInstanceType := &publicv1.InstanceType{}
	err = s.outMapper.Copy(ctx, createdPrivateInstanceType, createdPublicInstanceType)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private instance type to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process instance type")
		return
	}

	// Create the public response:
	response = &publicv1.InstanceTypesCreateResponse{}
	response.SetObject(createdPublicInstanceType)
	return
}

func (s *InstanceTypesServer) Update(ctx context.Context,
	request *publicv1.InstanceTypesUpdateRequest) (response *publicv1.InstanceTypesUpdateResponse, err error) {
	// Validate the request:
	publicInstanceType := request.GetObject()
	if publicInstanceType == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	id := publicInstanceType.GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	// Get the existing object from the private server:
	getRequest := &privatev1.InstanceTypesGetRequest{}
	getRequest.SetId(id)
	getResponse, err := s.delegate.Get(ctx, getRequest)
	if err != nil {
		return nil, err
	}
	existingPrivateInstanceType := getResponse.GetObject()

	// Map the public changes to the existing private object (preserving private data):
	err = s.inMapper.Copy(ctx, publicInstanceType, existingPrivateInstanceType)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public instance type to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process instance type")
		return
	}

	// Delegate to the private server with the merged object:
	privateRequest := &privatev1.InstanceTypesUpdateRequest{}
	privateRequest.SetObject(existingPrivateInstanceType)
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	updatedPrivateInstanceType := privateResponse.GetObject()
	updatedPublicInstanceType := &publicv1.InstanceType{}
	err = s.outMapper.Copy(ctx, updatedPrivateInstanceType, updatedPublicInstanceType)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private instance type to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process instance type")
		return
	}

	// Create the public response:
	response = &publicv1.InstanceTypesUpdateResponse{}
	response.SetObject(updatedPublicInstanceType)
	return
}

func (s *InstanceTypesServer) Delete(ctx context.Context,
	request *publicv1.InstanceTypesDeleteRequest) (response *publicv1.InstanceTypesDeleteResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.InstanceTypesDeleteRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Create the public response:
	response = &publicv1.InstanceTypesDeleteResponse{}
	return
}
