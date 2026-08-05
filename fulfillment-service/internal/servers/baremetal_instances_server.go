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

type BareMetalInstancesServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.BareMetalInstancesServer = (*BareMetalInstancesServer)(nil)

type BareMetalInstancesServer struct {
	publicv1.UnimplementedBareMetalInstancesServer

	logger    *slog.Logger
	delegate  privatev1.BareMetalInstancesServer
	inMapper  *GenericMapper[*publicv1.BareMetalInstance, *privatev1.BareMetalInstance]
	outMapper *GenericMapper[*privatev1.BareMetalInstance, *publicv1.BareMetalInstance]
}

func NewBareMetalInstancesServer() *BareMetalInstancesServerBuilder {
	return &BareMetalInstancesServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *BareMetalInstancesServerBuilder) SetLogger(value *slog.Logger) *BareMetalInstancesServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *BareMetalInstancesServerBuilder) SetNotifier(value events.Notifier) *BareMetalInstancesServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *BareMetalInstancesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *BareMetalInstancesServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *BareMetalInstancesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *BareMetalInstancesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *BareMetalInstancesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *BareMetalInstancesServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *BareMetalInstancesServerBuilder) Build() (result *BareMetalInstancesServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	inMapper, err := NewGenericMapper[*publicv1.BareMetalInstance, *privatev1.BareMetalInstance]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.BareMetalInstance, *publicv1.BareMetalInstance]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	delegate, err := NewPrivateBareMetalInstancesServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &BareMetalInstancesServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *BareMetalInstancesServer) List(ctx context.Context,
	request *publicv1.BareMetalInstancesListRequest) (response *publicv1.BareMetalInstancesListResponse, err error) {
	privateRequest := &privatev1.BareMetalInstancesListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	privateRequest.SetLimit(request.GetLimit())
	privateRequest.SetFilter(request.GetFilter())

	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.BareMetalInstance, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.BareMetalInstance{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to map private bare metal instance to public",
				slog.Any("error", err))
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process bare metal instances")
		}
		publicItems[i] = publicItem
	}

	response = &publicv1.BareMetalInstancesListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *BareMetalInstancesServer) Get(ctx context.Context,
	request *publicv1.BareMetalInstancesGetRequest) (response *publicv1.BareMetalInstancesGetResponse, err error) {
	privateRequest := &privatev1.BareMetalInstancesGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	publicBMI := &publicv1.BareMetalInstance{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicBMI)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private bare metal instance to public",
			slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process bare metal instance")
	}

	response = &publicv1.BareMetalInstancesGetResponse{}
	response.SetObject(publicBMI)
	return
}

func (s *BareMetalInstancesServer) Create(ctx context.Context,
	request *publicv1.BareMetalInstancesCreateRequest) (response *publicv1.BareMetalInstancesCreateResponse, err error) {
	publicBMI := request.GetObject()
	if publicBMI == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateBMI := &privatev1.BareMetalInstance{}
	err = s.inMapper.Copy(ctx, publicBMI, privateBMI)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map public bare metal instance to private",
			slog.Any("error", err))
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process bare metal instance")
		return
	}

	privateRequest := &privatev1.BareMetalInstancesCreateRequest{}
	privateRequest.SetObject(privateBMI)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	createdPublicBMI := &publicv1.BareMetalInstance{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), createdPublicBMI)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private bare metal instance to public",
			slog.Any("error", err))
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process bare metal instance")
		return
	}

	response = &publicv1.BareMetalInstancesCreateResponse{}
	response.SetObject(createdPublicBMI)
	return
}

func (s *BareMetalInstancesServer) Update(ctx context.Context,
	request *publicv1.BareMetalInstancesUpdateRequest) (response *publicv1.BareMetalInstancesUpdateResponse, err error) {
	publicBMI := request.GetObject()
	if publicBMI == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	id := publicBMI.GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	// When there's a field mask, copy to a new private object and let the generic server handle the
	// merge with the database object, which correctly applies field mask semantics.
	var privateBMI *privatev1.BareMetalInstance
	updateMask := request.GetUpdateMask()
	if len(updateMask.GetPaths()) > 0 {
		privateBMI = &privatev1.BareMetalInstance{}
		privateBMI.SetId(id)
	} else {
		getRequest := &privatev1.BareMetalInstancesGetRequest{}
		getRequest.SetId(id)
		getResponse, err := s.delegate.Get(ctx, getRequest)
		if err != nil {
			return nil, err
		}
		privateBMI = getResponse.GetObject()
	}
	err = s.inMapper.Copy(ctx, publicBMI, privateBMI)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map public bare metal instance to private",
			slog.Any("error", err))
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process bare metal instance")
		return
	}

	privateRequest := &privatev1.BareMetalInstancesUpdateRequest{}
	privateRequest.SetObject(privateBMI)
	privateRequest.SetUpdateMask(updateMask)
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	updatedPublicBMI := &publicv1.BareMetalInstance{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), updatedPublicBMI)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private bare metal instance to public",
			slog.Any("error", err))
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process bare metal instance")
		return
	}

	response = &publicv1.BareMetalInstancesUpdateResponse{}
	response.SetObject(updatedPublicBMI)
	return
}

func (s *BareMetalInstancesServer) Delete(ctx context.Context,
	request *publicv1.BareMetalInstancesDeleteRequest) (response *publicv1.BareMetalInstancesDeleteResponse, err error) {
	privateRequest := &privatev1.BareMetalInstancesDeleteRequest{}
	privateRequest.SetId(request.GetId())

	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	response = &publicv1.BareMetalInstancesDeleteResponse{}
	return
}
