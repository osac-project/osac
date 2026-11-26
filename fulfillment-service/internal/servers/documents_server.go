/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type DocumentsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.DocumentsServer = (*DocumentsServer)(nil)

type DocumentsServer struct {
	publicv1.UnimplementedDocumentsServer
	delegate  privatev1.DocumentsServer
	outMapper *GenericMapper[*privatev1.Document, *publicv1.Document]
}

func NewDocumentsServer() *DocumentsServerBuilder {
	return &DocumentsServerBuilder{}
}

func (b *DocumentsServerBuilder) SetLogger(value *slog.Logger) *DocumentsServerBuilder {
	b.logger = value
	return b
}

func (b *DocumentsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *DocumentsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *DocumentsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *DocumentsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *DocumentsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *DocumentsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *DocumentsServerBuilder) Build() (*DocumentsServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.tenancyLogic == nil {
		return nil, errors.New("tenancy logic is mandatory")
	}

	outMapper, err := NewGenericMapper[*privatev1.Document, *publicv1.Document]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return nil, err
	}

	delegate, err := NewPrivateDocumentsServer().
		SetLogger(b.logger).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc((*publicv1.Document)(nil).ProtoReflect().Descriptor()).
		Build()
	if err != nil {
		return nil, err
	}

	return &DocumentsServer{
		delegate:  delegate,
		outMapper: outMapper,
	}, nil
}

func (s *DocumentsServer) List(ctx context.Context,
	request *publicv1.DocumentsListRequest) (*publicv1.DocumentsListResponse, error) {
	privateRequest := &privatev1.DocumentsListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	if request.HasLimit() {
		privateRequest.SetLimit(request.GetLimit())
	}
	privateRequest.SetFilter(request.GetFilter())
	privateRequest.SetOrder(request.GetOrder())

	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	items := make([]*publicv1.Document, len(privateResponse.GetItems()))
	for i, item := range privateResponse.GetItems() {
		items[i] = &publicv1.Document{}
		if err := s.outMapper.Copy(ctx, item, items[i]); err != nil {
			return nil, grpcstatus.Error(grpccodes.Internal, "failed to process documents")
		}
	}

	return publicv1.DocumentsListResponse_builder{
		Size:  privateResponse.GetSize(),
		Total: privateResponse.GetTotal(),
		Items: items,
	}.Build(), nil
}

func (s *DocumentsServer) Get(ctx context.Context,
	request *publicv1.DocumentsGetRequest) (*publicv1.DocumentsGetResponse, error) {
	privateRequest := &privatev1.DocumentsGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	item := &publicv1.Document{}
	if err := s.outMapper.Copy(ctx, privateResponse.GetObject(), item); err != nil {
		return nil, grpcstatus.Error(grpccodes.Internal, "failed to process document")
	}

	return publicv1.DocumentsGetResponse_builder{Object: item}.Build(), nil
}
