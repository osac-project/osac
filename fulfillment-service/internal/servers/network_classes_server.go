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

type NetworkClassesServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.NetworkClassesServer = (*NetworkClassesServer)(nil)

type NetworkClassesServer struct {
	publicv1.UnimplementedNetworkClassesServer

	logger    *slog.Logger
	delegate  privatev1.NetworkClassesServer
	inMapper  *GenericMapper[*publicv1.NetworkClass, *privatev1.NetworkClass]
	outMapper *GenericMapper[*privatev1.NetworkClass, *publicv1.NetworkClass]
}

func NewNetworkClassesServer() *NetworkClassesServerBuilder {
	return &NetworkClassesServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *NetworkClassesServerBuilder) SetLogger(value *slog.Logger) *NetworkClassesServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *NetworkClassesServerBuilder) SetNotifier(value events.Notifier) *NetworkClassesServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *NetworkClassesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *NetworkClassesServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *NetworkClassesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *NetworkClassesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *NetworkClassesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *NetworkClassesServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *NetworkClassesServerBuilder) Build() (result *NetworkClassesServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Find OUTPUT_ONLY fields so that we can configure the inMapper to ignore them.
	// This prevents public API callers from directly setting these fields.
	ncDescriptor := new(publicv1.NetworkClass).ProtoReflect().Descriptor()
	isDefaultField := ncDescriptor.Fields().ByName("is_default")
	if isDefaultField == nil {
		err = fmt.Errorf("failed to find the is_default field of type '%s'", ncDescriptor.FullName())
		return
	}
	specField := ncDescriptor.Fields().ByName("spec")
	if specField == nil {
		err = fmt.Errorf("failed to find the spec field of type '%s'", ncDescriptor.FullName())
		return
	}

	// Create the mappers:
	inMapper, err := NewGenericMapper[*publicv1.NetworkClass, *privatev1.NetworkClass]().
		SetLogger(b.logger).
		SetStrict(true).
		AddIgnoredFields(isDefaultField.FullName(), specField.FullName()).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.NetworkClass, *publicv1.NetworkClass]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	// Create the private server to delegate to:
	delegate, err := NewPrivateNetworkClassesServer().
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
	result = &NetworkClassesServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *NetworkClassesServer) List(ctx context.Context,
	request *publicv1.NetworkClassesListRequest) (response *publicv1.NetworkClassesListResponse, err error) {
	// Create private request with same parameters:
	privateRequest := &privatev1.NetworkClassesListRequest{}
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
	publicItems := make([]*publicv1.NetworkClass, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.NetworkClass{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private network class to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process network classes")
		}
		publicItems[i] = publicItem
	}

	// Create the public response:
	response = &publicv1.NetworkClassesListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *NetworkClassesServer) Get(ctx context.Context,
	request *publicv1.NetworkClassesGetRequest) (response *publicv1.NetworkClassesGetResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.NetworkClassesGetRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateNetworkClass := privateResponse.GetObject()
	publicNetworkClass := &publicv1.NetworkClass{}
	err = s.outMapper.Copy(ctx, privateNetworkClass, publicNetworkClass)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private network class to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process network class")
	}

	// Create the public response:
	response = &publicv1.NetworkClassesGetResponse{}
	response.SetObject(publicNetworkClass)
	return
}

func (s *NetworkClassesServer) Create(ctx context.Context,
	request *publicv1.NetworkClassesCreateRequest) (response *publicv1.NetworkClassesCreateResponse, err error) {
	// Map the public network class to private format:
	publicNetworkClass := request.GetObject()
	if publicNetworkClass == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateNetworkClass := &privatev1.NetworkClass{}
	err = s.inMapper.Copy(ctx, publicNetworkClass, privateNetworkClass)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public network class to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process network class")
		return
	}

	// Delegate to the private server:
	privateRequest := &privatev1.NetworkClassesCreateRequest{}
	privateRequest.SetObject(privateNetworkClass)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	createdPrivateNetworkClass := privateResponse.GetObject()
	createdPublicNetworkClass := &publicv1.NetworkClass{}
	err = s.outMapper.Copy(ctx, createdPrivateNetworkClass, createdPublicNetworkClass)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private network class to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process network class")
		return
	}

	// Create the public response:
	response = &publicv1.NetworkClassesCreateResponse{}
	response.SetObject(createdPublicNetworkClass)
	return
}

func (s *NetworkClassesServer) Update(ctx context.Context,
	request *publicv1.NetworkClassesUpdateRequest) (response *publicv1.NetworkClassesUpdateResponse, err error) {
	// Validate the request:
	publicNetworkClass := request.GetObject()
	if publicNetworkClass == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	id := publicNetworkClass.GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	// Get the existing object from the private server:
	getRequest := &privatev1.NetworkClassesGetRequest{}
	getRequest.SetId(id)
	getResponse, err := s.delegate.Get(ctx, getRequest)
	if err != nil {
		return nil, err
	}
	existingPrivateNetworkClass := getResponse.GetObject()

	// Map the public changes to the existing private object (preserving private data):
	err = s.inMapper.Copy(ctx, publicNetworkClass, existingPrivateNetworkClass)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public network class to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process network class")
		return
	}

	// Delegate to the private server with the merged object:
	privateRequest := &privatev1.NetworkClassesUpdateRequest{}
	privateRequest.SetObject(existingPrivateNetworkClass)
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	updatedPrivateNetworkClass := privateResponse.GetObject()
	updatedPublicNetworkClass := &publicv1.NetworkClass{}
	err = s.outMapper.Copy(ctx, updatedPrivateNetworkClass, updatedPublicNetworkClass)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private network class to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process network class")
		return
	}

	// Create the public response:
	response = &publicv1.NetworkClassesUpdateResponse{}
	response.SetObject(updatedPublicNetworkClass)
	return
}

func (s *NetworkClassesServer) Delete(ctx context.Context,
	request *publicv1.NetworkClassesDeleteRequest) (response *publicv1.NetworkClassesDeleteResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.NetworkClassesDeleteRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Create the public response:
	response = &publicv1.NetworkClassesDeleteResponse{}
	return
}
