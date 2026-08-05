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

// Note: BareMetalInstanceTypes do not require filter defaults (unlike InstanceTypes)
// because they represent static hardware configurations without lifecycle states.
// All BareMetalInstanceTypes are available for discovery without state-based filtering.

type BareMetalInstanceTypesServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.BareMetalInstanceTypesServer = (*BareMetalInstanceTypesServer)(nil)

type BareMetalInstanceTypesServer struct {
	publicv1.UnimplementedBareMetalInstanceTypesServer

	logger    *slog.Logger
	delegate  privatev1.BareMetalInstanceTypesServer
	inMapper  *GenericMapper[*publicv1.BareMetalInstanceType, *privatev1.BareMetalInstanceType]
	outMapper *GenericMapper[*privatev1.BareMetalInstanceType, *publicv1.BareMetalInstanceType]
}

func NewBareMetalInstanceTypesServer() *BareMetalInstanceTypesServerBuilder {
	return &BareMetalInstanceTypesServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *BareMetalInstanceTypesServerBuilder) SetLogger(value *slog.Logger) *BareMetalInstanceTypesServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *BareMetalInstanceTypesServerBuilder) SetNotifier(value events.Notifier) *BareMetalInstanceTypesServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is mandatory.
func (b *BareMetalInstanceTypesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *BareMetalInstanceTypesServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *BareMetalInstanceTypesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *BareMetalInstanceTypesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *BareMetalInstanceTypesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *BareMetalInstanceTypesServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *BareMetalInstanceTypesServerBuilder) Build() (result *BareMetalInstanceTypesServer, err error) {
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
	inMapper, err := NewGenericMapper[*publicv1.BareMetalInstanceType, *privatev1.BareMetalInstanceType]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.BareMetalInstanceType, *publicv1.BareMetalInstanceType]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	// Create the private server to delegate to:
	delegate, err := NewPrivateBareMetalInstanceTypesServer().
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
	result = &BareMetalInstanceTypesServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *BareMetalInstanceTypesServer) List(ctx context.Context,
	request *publicv1.BareMetalInstanceTypesListRequest) (response *publicv1.BareMetalInstanceTypesListResponse, err error) {
	// Create private request with same parameters:
	privateRequest := &privatev1.BareMetalInstanceTypesListRequest{}
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
	publicItems := make([]*publicv1.BareMetalInstanceType, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.BareMetalInstanceType{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private bare metal instance type to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process bare metal instance types")
		}
		publicItems[i] = publicItem
	}

	// Create the public response:
	response = &publicv1.BareMetalInstanceTypesListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *BareMetalInstanceTypesServer) Get(ctx context.Context,
	request *publicv1.BareMetalInstanceTypesGetRequest) (response *publicv1.BareMetalInstanceTypesGetResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.BareMetalInstanceTypesGetRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateBareMetalInstanceType := privateResponse.GetObject()
	publicBareMetalInstanceType := &publicv1.BareMetalInstanceType{}
	err = s.outMapper.Copy(ctx, privateBareMetalInstanceType, publicBareMetalInstanceType)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private bare metal instance type to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process bare metal instance type")
	}

	// Create the public response:
	response = &publicv1.BareMetalInstanceTypesGetResponse{}
	response.SetObject(publicBareMetalInstanceType)
	return
}
