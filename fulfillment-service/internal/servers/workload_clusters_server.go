/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
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

type WorkloadClustersServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	hubsServer        privatev1.HubsServer
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.WorkloadClustersServer = (*WorkloadClustersServer)(nil)

type WorkloadClustersServer struct {
	publicv1.UnimplementedWorkloadClustersServer
	logger    *slog.Logger
	delegate  privatev1.WorkloadClustersServer
	inMapper  *GenericMapper[*publicv1.WorkloadCluster, *privatev1.WorkloadCluster]
	outMapper *GenericMapper[*privatev1.WorkloadCluster, *publicv1.WorkloadCluster]
}

func NewWorkloadClustersServer() *WorkloadClustersServerBuilder {
	return &WorkloadClustersServerBuilder{}
}

func (b *WorkloadClustersServerBuilder) SetLogger(value *slog.Logger) *WorkloadClustersServerBuilder {
	b.logger = value
	return b
}

func (b *WorkloadClustersServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *WorkloadClustersServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *WorkloadClustersServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *WorkloadClustersServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *WorkloadClustersServerBuilder) SetHubsServer(value privatev1.HubsServer) *WorkloadClustersServerBuilder {
	b.hubsServer = value
	return b
}

func (b *WorkloadClustersServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *WorkloadClustersServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *WorkloadClustersServerBuilder) Build() (result *WorkloadClustersServer, err error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.attributionLogic == nil {
		return nil, errors.New("attribution logic is mandatory")
	}
	if b.tenancyLogic == nil {
		return nil, errors.New("tenancy logic is mandatory")
	}
	if b.hubsServer == nil {
		return nil, errors.New("hubs server is mandatory")
	}

	inMapper, err := NewGenericMapper[*publicv1.WorkloadCluster, *privatev1.WorkloadCluster]().
		SetLogger(b.logger).SetStrict(false).Build()
	if err != nil {
		return nil, err
	}
	outMapper, err := NewGenericMapper[*privatev1.WorkloadCluster, *publicv1.WorkloadCluster]().
		SetLogger(b.logger).SetStrict(false).Build()
	if err != nil {
		return nil, err
	}
	delegate, err := NewPrivateWorkloadClustersServer().
		SetLogger(b.logger).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetHubsServer(b.hubsServer).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc((*publicv1.WorkloadCluster)(nil).ProtoReflect().Descriptor()).
		Build()
	if err != nil {
		return nil, err
	}
	return &WorkloadClustersServer{
		logger: b.logger, delegate: delegate, inMapper: inMapper, outMapper: outMapper,
	}, nil
}

func (s *WorkloadClustersServer) List(ctx context.Context,
	request *publicv1.WorkloadClustersListRequest) (*publicv1.WorkloadClustersListResponse, error) {
	privateRequest := privatev1.WorkloadClustersListRequest_builder{}.Build()
	privateRequest.SetOffset(request.GetOffset())
	if request.HasLimit() {
		privateRequest.SetLimit(request.GetLimit())
	}
	if request.HasFilter() {
		privateRequest.SetFilter(request.GetFilter())
	}
	if request.HasOrder() {
		privateRequest.SetOrder(request.GetOrder())
	}
	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	items := make([]*publicv1.WorkloadCluster, 0, len(privateResponse.GetItems()))
	for _, privateItem := range privateResponse.GetItems() {
		publicItem := &publicv1.WorkloadCluster{}
		if err := s.outMapper.Copy(ctx, privateItem, publicItem); err != nil {
			return nil, grpcstatus.Error(grpccodes.Internal, "failed to process workload clusters")
		}
		items = append(items, publicItem)
	}
	return publicv1.WorkloadClustersListResponse_builder{
		Size:  int32(len(items)), // #nosec G115 -- page size is bounded by the request limit.
		Total: privateResponse.GetTotal(),
		Items: items,
	}.Build(), nil
}

func (s *WorkloadClustersServer) Get(ctx context.Context,
	request *publicv1.WorkloadClustersGetRequest) (*publicv1.WorkloadClustersGetResponse, error) {
	privateResponse, err := s.delegate.Get(ctx, privatev1.WorkloadClustersGetRequest_builder{Id: request.GetId()}.Build())
	if err != nil {
		return nil, err
	}
	object := &publicv1.WorkloadCluster{}
	if err := s.outMapper.Copy(ctx, privateResponse.GetObject(), object); err != nil {
		return nil, grpcstatus.Error(grpccodes.Internal, "failed to process workload cluster")
	}
	return publicv1.WorkloadClustersGetResponse_builder{Object: object}.Build(), nil
}

func (s *WorkloadClustersServer) Create(ctx context.Context,
	request *publicv1.WorkloadClustersCreateRequest) (*publicv1.WorkloadClustersCreateResponse, error) {
	privateObject := &privatev1.WorkloadCluster{}
	if err := s.inMapper.Copy(ctx, request.GetObject(), privateObject); err != nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "failed to process workload cluster")
	}
	privateResponse, err := s.delegate.Create(ctx, privatev1.WorkloadClustersCreateRequest_builder{Object: privateObject}.Build())
	if err != nil {
		return nil, err
	}
	object := &publicv1.WorkloadCluster{}
	if err := s.outMapper.Copy(ctx, privateResponse.GetObject(), object); err != nil {
		return nil, grpcstatus.Error(grpccodes.Internal, "failed to process workload cluster")
	}
	return publicv1.WorkloadClustersCreateResponse_builder{Object: object}.Build(), nil
}

func (s *WorkloadClustersServer) Update(ctx context.Context,
	request *publicv1.WorkloadClustersUpdateRequest) (*publicv1.WorkloadClustersUpdateResponse, error) {
	ctx = withPublicAPIView(ctx)
	privateObject := &privatev1.WorkloadCluster{}
	if err := s.inMapper.Copy(ctx, request.GetObject(), privateObject); err != nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "failed to process workload cluster")
	}
	privateRequest := privatev1.WorkloadClustersUpdateRequest_builder{Object: privateObject, Lock: request.GetLock()}.Build()
	privateRequest.SetUpdateMask(request.GetUpdateMask())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	object := &publicv1.WorkloadCluster{}
	if err := s.outMapper.Copy(ctx, privateResponse.GetObject(), object); err != nil {
		return nil, grpcstatus.Error(grpccodes.Internal, "failed to process workload cluster")
	}
	return publicv1.WorkloadClustersUpdateResponse_builder{Object: object}.Build(), nil
}

func (s *WorkloadClustersServer) Delete(ctx context.Context,
	request *publicv1.WorkloadClustersDeleteRequest) (*publicv1.WorkloadClustersDeleteResponse, error) {
	return nil, grpcstatus.Error(grpccodes.Unimplemented, "deleting workload clusters is not supported")
}
