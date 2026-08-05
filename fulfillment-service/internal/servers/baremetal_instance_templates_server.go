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

type BareMetalInstanceTemplatesServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.BareMetalInstanceTemplatesServer = (*BareMetalInstanceTemplatesServer)(nil)

type BareMetalInstanceTemplatesServer struct {
	publicv1.UnimplementedBareMetalInstanceTemplatesServer

	logger    *slog.Logger
	delegate  privatev1.BareMetalInstanceTemplatesServer
	outMapper *GenericMapper[*privatev1.BareMetalInstanceTemplate, *publicv1.BareMetalInstanceTemplate]
}

func NewBareMetalInstanceTemplatesServer() *BareMetalInstanceTemplatesServerBuilder {
	return &BareMetalInstanceTemplatesServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *BareMetalInstanceTemplatesServerBuilder) SetLogger(value *slog.Logger) *BareMetalInstanceTemplatesServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *BareMetalInstanceTemplatesServerBuilder) SetNotifier(value events.Notifier) *BareMetalInstanceTemplatesServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *BareMetalInstanceTemplatesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *BareMetalInstanceTemplatesServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *BareMetalInstanceTemplatesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *BareMetalInstanceTemplatesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *BareMetalInstanceTemplatesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *BareMetalInstanceTemplatesServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *BareMetalInstanceTemplatesServerBuilder) Build() (result *BareMetalInstanceTemplatesServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	outMapper, err := NewGenericMapper[*privatev1.BareMetalInstanceTemplate, *publicv1.BareMetalInstanceTemplate]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	delegate, err := NewPrivateBareMetalInstanceTemplatesServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &BareMetalInstanceTemplatesServer{
		logger:    b.logger,
		delegate:  delegate,
		outMapper: outMapper,
	}
	return
}

func (s *BareMetalInstanceTemplatesServer) List(ctx context.Context,
	request *publicv1.BareMetalInstanceTemplatesListRequest) (response *publicv1.BareMetalInstanceTemplatesListResponse, err error) {
	privateRequest := &privatev1.BareMetalInstanceTemplatesListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	privateRequest.SetLimit(request.GetLimit())
	privateRequest.SetFilter(request.GetFilter())

	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.BareMetalInstanceTemplate, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.BareMetalInstanceTemplate{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to map private bare metal instance template to public",
				slog.Any("error", err))
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process bare metal instance templates")
		}
		publicItems[i] = publicItem
	}

	response = &publicv1.BareMetalInstanceTemplatesListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *BareMetalInstanceTemplatesServer) Get(ctx context.Context,
	request *publicv1.BareMetalInstanceTemplatesGetRequest) (response *publicv1.BareMetalInstanceTemplatesGetResponse, err error) {
	privateRequest := &privatev1.BareMetalInstanceTemplatesGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	publicTemplate := &publicv1.BareMetalInstanceTemplate{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicTemplate)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private bare metal instance template to public",
			slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process bare metal instance template")
	}

	response = &publicv1.BareMetalInstanceTemplatesGetResponse{}
	response.SetObject(publicTemplate)
	return
}
