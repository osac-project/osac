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

type VirtualNetworksServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.VirtualNetworksServer = (*VirtualNetworksServer)(nil)

type VirtualNetworksServer struct {
	publicv1.UnimplementedVirtualNetworksServer

	logger    *slog.Logger
	delegate  privatev1.VirtualNetworksServer
	inMapper  *GenericMapper[*publicv1.VirtualNetwork, *privatev1.VirtualNetwork]
	outMapper *GenericMapper[*privatev1.VirtualNetwork, *publicv1.VirtualNetwork]
}

func NewVirtualNetworksServer() *VirtualNetworksServerBuilder {
	return &VirtualNetworksServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *VirtualNetworksServerBuilder) SetLogger(value *slog.Logger) *VirtualNetworksServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *VirtualNetworksServerBuilder) SetNotifier(value events.Notifier) *VirtualNetworksServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *VirtualNetworksServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *VirtualNetworksServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *VirtualNetworksServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *VirtualNetworksServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *VirtualNetworksServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *VirtualNetworksServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *VirtualNetworksServerBuilder) Build() (result *VirtualNetworksServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Find the network_class field in VirtualNetworkSpec so that we can configure the inMapper to ignore it.
	// This prevents public Update from clearing network_class (which would trigger a false immutability error
	// since the field is now OPTIONAL). The Create method restores network_class explicitly after Copy.
	ncField := new(publicv1.VirtualNetworkSpec).ProtoReflect().Descriptor().Fields().ByName("network_class")
	if ncField == nil {
		err = fmt.Errorf("failed to find the network_class field of type '%s'", new(publicv1.VirtualNetworkSpec).ProtoReflect().Descriptor().FullName())
		return
	}

	// Create the mappers:
	inMapper, err := NewGenericMapper[*publicv1.VirtualNetwork, *privatev1.VirtualNetwork]().
		SetLogger(b.logger).
		SetStrict(true).
		AddIgnoredFields(ncField.FullName()).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.VirtualNetwork, *publicv1.VirtualNetwork]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	// Create the private server to delegate to:
	delegate, err := NewPrivateVirtualNetworksServer().
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
	result = &VirtualNetworksServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *VirtualNetworksServer) List(ctx context.Context,
	request *publicv1.VirtualNetworksListRequest) (response *publicv1.VirtualNetworksListResponse, err error) {
	// Create private request with same parameters:
	privateRequest := &privatev1.VirtualNetworksListRequest{}
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
	publicItems := make([]*publicv1.VirtualNetwork, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.VirtualNetwork{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private virtual network to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process virtual networks")
		}
		publicItems[i] = publicItem
	}

	// Create the public response:
	response = &publicv1.VirtualNetworksListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *VirtualNetworksServer) Get(ctx context.Context,
	request *publicv1.VirtualNetworksGetRequest) (response *publicv1.VirtualNetworksGetResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.VirtualNetworksGetRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateVirtualNetwork := privateResponse.GetObject()
	publicVirtualNetwork := &publicv1.VirtualNetwork{}
	err = s.outMapper.Copy(ctx, privateVirtualNetwork, publicVirtualNetwork)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private virtual network to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process virtual network")
	}

	// Create the public response:
	response = &publicv1.VirtualNetworksGetResponse{}
	response.SetObject(publicVirtualNetwork)
	return
}

func (s *VirtualNetworksServer) Create(ctx context.Context,
	request *publicv1.VirtualNetworksCreateRequest) (response *publicv1.VirtualNetworksCreateResponse, err error) {
	// Map the public virtual network to private format:
	publicVirtualNetwork := request.GetObject()
	if publicVirtualNetwork == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateVirtualNetwork := &privatev1.VirtualNetwork{}
	err = s.inMapper.Copy(ctx, publicVirtualNetwork, privateVirtualNetwork)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public virtual network to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process virtual network")
		return
	}

	// Restore network_class from the public request. AddIgnoredFields drops it during Copy
	// to protect Update from false immutability errors, but on Create the caller's explicit
	// network_class must be honored. If empty, the private server applies the default.
	if publicVirtualNetwork.HasSpec() && privateVirtualNetwork.HasSpec() && publicVirtualNetwork.GetSpec().GetNetworkClass() != "" {
		privateVirtualNetwork.GetSpec().SetNetworkClass(publicVirtualNetwork.GetSpec().GetNetworkClass())
	}

	// Set default region if not provided (public API doesn't expose region):
	if privateVirtualNetwork.HasSpec() && privateVirtualNetwork.GetSpec().GetRegion() == "" {
		privateVirtualNetwork.GetSpec().SetRegion("default")
	}

	// Delegate to the private server:
	privateRequest := &privatev1.VirtualNetworksCreateRequest{}
	privateRequest.SetObject(privateVirtualNetwork)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	createdPrivateVirtualNetwork := privateResponse.GetObject()
	createdPublicVirtualNetwork := &publicv1.VirtualNetwork{}
	err = s.outMapper.Copy(ctx, createdPrivateVirtualNetwork, createdPublicVirtualNetwork)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private virtual network to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process virtual network")
		return
	}

	// Create the public response:
	response = &publicv1.VirtualNetworksCreateResponse{}
	response.SetObject(createdPublicVirtualNetwork)
	return
}

func (s *VirtualNetworksServer) Update(ctx context.Context,
	request *publicv1.VirtualNetworksUpdateRequest) (response *publicv1.VirtualNetworksUpdateResponse, err error) {
	// Validate the request:
	publicVirtualNetwork := request.GetObject()
	if publicVirtualNetwork == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	id := publicVirtualNetwork.GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	// Get the existing object from the private server:
	getRequest := &privatev1.VirtualNetworksGetRequest{}
	getRequest.SetId(id)
	getResponse, err := s.delegate.Get(ctx, getRequest)
	if err != nil {
		return nil, err
	}
	existingPrivateVirtualNetwork := getResponse.GetObject()

	// Reject explicit network_class changes — the mapper ignores this field to prevent false
	// immutability errors on Update, but without this check a client sending a different value
	// gets silent success instead of the expected immutability error.
	if publicVirtualNetwork.HasSpec() && publicVirtualNetwork.GetSpec().GetNetworkClass() != "" {
		existingNC := existingPrivateVirtualNetwork.GetSpec().GetNetworkClass()
		if publicVirtualNetwork.GetSpec().GetNetworkClass() != existingNC {
			return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
				"field 'spec.network_class' is immutable and cannot be changed")
		}
	}

	// Map the public changes to the existing private object (preserving private data):
	err = s.inMapper.Copy(ctx, publicVirtualNetwork, existingPrivateVirtualNetwork)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public virtual network to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process virtual network")
		return
	}

	// Delegate to the private server with the merged object:
	privateRequest := &privatev1.VirtualNetworksUpdateRequest{}
	privateRequest.SetObject(existingPrivateVirtualNetwork)
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	updatedPrivateVirtualNetwork := privateResponse.GetObject()
	updatedPublicVirtualNetwork := &publicv1.VirtualNetwork{}
	err = s.outMapper.Copy(ctx, updatedPrivateVirtualNetwork, updatedPublicVirtualNetwork)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private virtual network to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process virtual network")
		return
	}

	// Create the public response:
	response = &publicv1.VirtualNetworksUpdateResponse{}
	response.SetObject(updatedPublicVirtualNetwork)
	return
}

func (s *VirtualNetworksServer) Delete(ctx context.Context,
	request *publicv1.VirtualNetworksDeleteRequest) (response *publicv1.VirtualNetworksDeleteResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.VirtualNetworksDeleteRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Create the public response:
	response = &publicv1.VirtualNetworksDeleteResponse{}
	return
}
