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
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateWorkloadClustersServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	hubsServer        privatev1.HubsServer
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.WorkloadClustersServer = (*PrivateWorkloadClustersServer)(nil)

type PrivateWorkloadClustersServer struct {
	privatev1.UnimplementedWorkloadClustersServer
	generic    *GenericServer[*privatev1.WorkloadCluster]
	hubsServer privatev1.HubsServer
}

func NewPrivateWorkloadClustersServer() *PrivateWorkloadClustersServerBuilder {
	return &PrivateWorkloadClustersServerBuilder{}
}

func (b *PrivateWorkloadClustersServerBuilder) SetLogger(value *slog.Logger) *PrivateWorkloadClustersServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateWorkloadClustersServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateWorkloadClustersServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateWorkloadClustersServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateWorkloadClustersServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateWorkloadClustersServerBuilder) SetHubsServer(value privatev1.HubsServer) *PrivateWorkloadClustersServerBuilder {
	b.hubsServer = value
	return b
}

func (b *PrivateWorkloadClustersServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateWorkloadClustersServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateWorkloadClustersServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateWorkloadClustersServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateWorkloadClustersServerBuilder) Build() (result *PrivateWorkloadClustersServer, err error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.tenancyLogic == nil {
		return nil, errors.New("tenancy logic is mandatory")
	}
	if b.hubsServer == nil {
		return nil, errors.New("hubs server is mandatory")
	}

	s := &PrivateWorkloadClustersServer{hubsServer: b.hubsServer}
	s.generic, err = NewGenericServer[*privatev1.WorkloadCluster]().
		SetLogger(b.logger).
		SetService(privatev1.WorkloadClusters_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		SetAllowedTenants(auth.SharedTenant).
		Build()
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *PrivateWorkloadClustersServer) List(ctx context.Context,
	request *privatev1.WorkloadClustersListRequest) (*privatev1.WorkloadClustersListResponse, error) {
	var response *privatev1.WorkloadClustersListResponse
	err := s.generic.List(ctx, request, &response)
	return response, err
}

func (s *PrivateWorkloadClustersServer) Get(ctx context.Context,
	request *privatev1.WorkloadClustersGetRequest) (*privatev1.WorkloadClustersGetResponse, error) {
	var response *privatev1.WorkloadClustersGetResponse
	err := s.generic.Get(ctx, request, &response)
	return response, err
}

func (s *PrivateWorkloadClustersServer) Create(ctx context.Context,
	request *privatev1.WorkloadClustersCreateRequest) (*privatev1.WorkloadClustersCreateResponse, error) {
	object := request.GetObject()
	if err := s.validateSourceHub(ctx, object.GetSpec().GetSourceHubId()); err != nil {
		return nil, err
	}
	if err := s.rejectDuplicate(ctx, object); err != nil {
		return nil, err
	}
	if object.GetMetadata() == nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "metadata is required")
	}
	object.GetMetadata().SetTenant(auth.SharedTenant)

	var response *privatev1.WorkloadClustersCreateResponse
	err := s.generic.CreateWithCandidatePreparation(ctx, request, &response, func(
		_ context.Context, _ *privatev1.WorkloadCluster, candidate *privatev1.WorkloadCluster,
	) error {
		candidate.SetStatus(initialWorkloadClusterStatus())
		return nil
	})
	return response, err
}

func (s *PrivateWorkloadClustersServer) Update(ctx context.Context,
	request *privatev1.WorkloadClustersUpdateRequest) (*privatev1.WorkloadClustersUpdateResponse, error) {
	var response *privatev1.WorkloadClustersUpdateResponse
	err := s.generic.UpdateWithValidation(ctx, request, &response, func(
		_ context.Context, candidate, current *privatev1.WorkloadCluster,
	) error {
		if candidate.GetSpec().GetSourceHubId() != current.GetSpec().GetSourceHubId() {
			return grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.source_hub_id' is immutable")
		}
		if candidate.GetSpec().GetManagedClusterName() != current.GetSpec().GetManagedClusterName() {
			return grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.managed_cluster_name' is immutable")
		}
		candidate.GetMetadata().SetTenant(auth.SharedTenant)
		return nil
	})
	return response, err
}

func (s *PrivateWorkloadClustersServer) Delete(ctx context.Context,
	request *privatev1.WorkloadClustersDeleteRequest) (*privatev1.WorkloadClustersDeleteResponse, error) {
	var response *privatev1.WorkloadClustersDeleteResponse
	err := s.generic.Delete(ctx, request, &response)
	return response, err
}

func (s *PrivateWorkloadClustersServer) Signal(ctx context.Context,
	request *privatev1.WorkloadClustersSignalRequest) (*privatev1.WorkloadClustersSignalResponse, error) {
	var response *privatev1.WorkloadClustersSignalResponse
	err := s.generic.Signal(ctx, request, &response)
	return response, err
}

func initialWorkloadClusterStatus() *privatev1.WorkloadClusterStatus {
	return privatev1.WorkloadClusterStatus_builder{
		Conditions: []*privatev1.WorkloadClusterCondition{
			privatev1.WorkloadClusterCondition_builder{Type: "Available", Status: "Unknown"}.Build(),
			privatev1.WorkloadClusterCondition_builder{Type: "HubReachable", Status: "Unknown"}.Build(),
		},
	}.Build()
}

func (s *PrivateWorkloadClustersServer) validateSourceHub(ctx context.Context, id string) error {
	if id == "" {
		return grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.source_hub_id' is required")
	}
	_, err := s.hubsServer.Get(ctx, privatev1.HubsGetRequest_builder{Id: id}.Build())
	if err != nil {
		if grpcstatus.Code(err) == grpccodes.NotFound {
			return grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.source_hub_id' must reference an existing Hub")
		}
		return err
	}
	return nil
}

func (s *PrivateWorkloadClustersServer) rejectDuplicate(ctx context.Context, object *privatev1.WorkloadCluster) error {
	if object == nil || object.GetSpec() == nil {
		return nil
	}
	filter := "this.spec.source_hub_id == " + strconv.Quote(object.GetSpec().GetSourceHubId()) +
		" && this.spec.managed_cluster_name == " + strconv.Quote(object.GetSpec().GetManagedClusterName())
	response, err := s.List(ctx, privatev1.WorkloadClustersListRequest_builder{Filter: &filter}.Build())
	if err != nil {
		return err
	}
	if response.GetTotal() > 0 {
		return grpcstatus.Error(grpccodes.AlreadyExists, "a workload cluster with the specified hub identity already exists")
	}
	return nil
}
