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
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateDocumentsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.DocumentsServer = (*PrivateDocumentsServer)(nil)

type PrivateDocumentsServer struct {
	privatev1.UnimplementedDocumentsServer
	generic *GenericServer[*privatev1.Document]
}

func NewPrivateDocumentsServer() *PrivateDocumentsServerBuilder {
	return &PrivateDocumentsServerBuilder{}
}

func (b *PrivateDocumentsServerBuilder) SetLogger(value *slog.Logger) *PrivateDocumentsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateDocumentsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateDocumentsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateDocumentsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateDocumentsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateDocumentsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateDocumentsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateDocumentsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateDocumentsServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateDocumentsServerBuilder) Build() (*PrivateDocumentsServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.tenancyLogic == nil {
		return nil, errors.New("tenancy logic is mandatory")
	}

	generic, err := NewGenericServer[*privatev1.Document]().
		SetLogger(b.logger).
		SetService(privatev1.Documents_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return nil, err
	}

	return &PrivateDocumentsServer{generic: generic}, nil
}

func (s *PrivateDocumentsServer) List(ctx context.Context,
	request *privatev1.DocumentsListRequest) (*privatev1.DocumentsListResponse, error) {
	var response *privatev1.DocumentsListResponse
	err := s.generic.List(ctx, request, &response)
	return response, err
}

func (s *PrivateDocumentsServer) Get(ctx context.Context,
	request *privatev1.DocumentsGetRequest) (*privatev1.DocumentsGetResponse, error) {
	var response *privatev1.DocumentsGetResponse
	err := s.generic.Get(ctx, request, &response)
	return response, err
}

func (s *PrivateDocumentsServer) Create(ctx context.Context,
	request *privatev1.DocumentsCreateRequest) (*privatev1.DocumentsCreateResponse, error) {
	var response *privatev1.DocumentsCreateResponse
	err := s.generic.Create(ctx, request, &response)
	return response, err
}

func (s *PrivateDocumentsServer) Update(ctx context.Context,
	request *privatev1.DocumentsUpdateRequest) (*privatev1.DocumentsUpdateResponse, error) {
	var response *privatev1.DocumentsUpdateResponse
	err := s.generic.Update(ctx, request, &response)
	return response, err
}

func (s *PrivateDocumentsServer) Delete(ctx context.Context,
	request *privatev1.DocumentsDeleteRequest) (*privatev1.DocumentsDeleteResponse, error) {
	var response *privatev1.DocumentsDeleteResponse
	err := s.generic.Delete(ctx, request, &response)
	return response, err
}

func (s *PrivateDocumentsServer) Signal(ctx context.Context,
	request *privatev1.DocumentsSignalRequest) (*privatev1.DocumentsSignalResponse, error) {
	return nil, grpcstatus.Error(grpccodes.Unimplemented, "documents do not support signal")
}
