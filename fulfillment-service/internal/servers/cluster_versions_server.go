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

// clusterVersionFilterDefaults defines per-field default predicates for public List requests.
// Each default is applied independently: if the user's filter references a field, that field's
// default is skipped. Uses numeric enum values because CEL types proto enums as int.
var clusterVersionFilterDefaults = []filterDefault{
	{
		field: "this.spec.state",
		predicate: fmt.Sprintf("(this.spec.state == %d || this.spec.state == %d)",
			int32(privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_ACTIVE),
			int32(privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_DEPRECATED)),
	},
	{
		field:     "this.spec.enabled",
		predicate: "this.spec.enabled == true",
	},
}

type ClusterVersionsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.ClusterVersionsServer = (*ClusterVersionsServer)(nil)

type ClusterVersionsServer struct {
	publicv1.UnimplementedClusterVersionsServer

	logger    *slog.Logger
	delegate  privatev1.ClusterVersionsServer
	inMapper  *GenericMapper[*publicv1.ClusterVersion, *privatev1.ClusterVersion]
	outMapper *GenericMapper[*privatev1.ClusterVersion, *publicv1.ClusterVersion]
}

func NewClusterVersionsServer() *ClusterVersionsServerBuilder {
	return &ClusterVersionsServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *ClusterVersionsServerBuilder) SetLogger(value *slog.Logger) *ClusterVersionsServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *ClusterVersionsServerBuilder) SetNotifier(value events.Notifier) *ClusterVersionsServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is mandatory.
func (b *ClusterVersionsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *ClusterVersionsServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *ClusterVersionsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *ClusterVersionsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *ClusterVersionsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *ClusterVersionsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *ClusterVersionsServerBuilder) Build() (*ClusterVersionsServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.attributionLogic == nil {
		return nil, errors.New("attribution logic is mandatory")
	}
	if b.tenancyLogic == nil {
		return nil, errors.New("tenancy logic is mandatory")
	}

	inMapper, err := NewGenericMapper[*publicv1.ClusterVersion, *privatev1.ClusterVersion]().
		SetLogger(b.logger).
		SetStrict(true).
		AddIgnoredFields("osac.public.v1.ClusterVersionSpec.is_default").
		Build()
	if err != nil {
		return nil, err
	}
	outMapper, err := NewGenericMapper[*privatev1.ClusterVersion, *publicv1.ClusterVersion]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return nil, err
	}

	delegate, err := NewPrivateClusterVersionsServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return nil, err
	}

	return &ClusterVersionsServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}, nil
}

func (s *ClusterVersionsServer) List(ctx context.Context,
	request *publicv1.ClusterVersionsListRequest) (*publicv1.ClusterVersionsListResponse, error) {
	privateRequest := &privatev1.ClusterVersionsListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	privateRequest.SetLimit(request.GetLimit())
	composedFilter, err := composeFilterDefaults(request.GetFilter(), clusterVersionFilterDefaults)
	if err != nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "%v", err)
	}
	privateRequest.SetFilter(composedFilter)
	privateRequest.SetOrder(request.GetOrder())

	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.ClusterVersion, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.ClusterVersion{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private cluster version to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster versions")
		}
		publicItems[i] = publicItem
	}

	response := &publicv1.ClusterVersionsListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return response, nil
}

func (s *ClusterVersionsServer) Get(ctx context.Context,
	request *publicv1.ClusterVersionsGetRequest) (*publicv1.ClusterVersionsGetResponse, error) {
	privateRequest := &privatev1.ClusterVersionsGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateClusterVersion := privateResponse.GetObject()
	publicClusterVersion := &publicv1.ClusterVersion{}
	err = s.outMapper.Copy(ctx, privateClusterVersion, publicClusterVersion)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private cluster version to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster version")
	}

	response := &publicv1.ClusterVersionsGetResponse{}
	response.SetObject(publicClusterVersion)
	return response, nil
}

func (s *ClusterVersionsServer) Create(ctx context.Context,
	request *publicv1.ClusterVersionsCreateRequest) (*publicv1.ClusterVersionsCreateResponse, error) {
	publicClusterVersion := request.GetObject()
	if publicClusterVersion == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
	}
	privateClusterVersion := &privatev1.ClusterVersion{}
	err := s.inMapper.Copy(ctx, publicClusterVersion, privateClusterVersion)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public cluster version to private",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster version")
	}

	privateRequest := &privatev1.ClusterVersionsCreateRequest{}
	privateRequest.SetObject(privateClusterVersion)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	createdPrivateClusterVersion := privateResponse.GetObject()
	createdPublicClusterVersion := &publicv1.ClusterVersion{}
	err = s.outMapper.Copy(ctx, createdPrivateClusterVersion, createdPublicClusterVersion)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private cluster version to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster version")
	}

	response := &publicv1.ClusterVersionsCreateResponse{}
	response.SetObject(createdPublicClusterVersion)
	return response, nil
}

func (s *ClusterVersionsServer) Update(ctx context.Context,
	request *publicv1.ClusterVersionsUpdateRequest) (*publicv1.ClusterVersionsUpdateResponse, error) {
	publicClusterVersion := request.GetObject()
	if publicClusterVersion == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
	}
	id := publicClusterVersion.GetId()
	if id == "" {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
	}

	getRequest := &privatev1.ClusterVersionsGetRequest{}
	getRequest.SetId(id)
	getResponse, err := s.delegate.Get(ctx, getRequest)
	if err != nil {
		return nil, err
	}
	privateClusterVersion := getResponse.GetObject()

	// Map the public changes to the private object (preserving private-only fields like spec.image):
	err = s.inMapper.Copy(ctx, publicClusterVersion, privateClusterVersion)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public cluster version to private",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster version")
	}

	privateRequest := &privatev1.ClusterVersionsUpdateRequest{}
	privateRequest.SetObject(privateClusterVersion)
	privateRequest.SetUpdateMask(request.GetUpdateMask())
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	updatedPrivateClusterVersion := privateResponse.GetObject()
	updatedPublicClusterVersion := &publicv1.ClusterVersion{}
	err = s.outMapper.Copy(ctx, updatedPrivateClusterVersion, updatedPublicClusterVersion)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private cluster version to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster version")
	}

	response := &publicv1.ClusterVersionsUpdateResponse{}
	response.SetObject(updatedPublicClusterVersion)
	return response, nil
}

func (s *ClusterVersionsServer) Delete(ctx context.Context,
	request *publicv1.ClusterVersionsDeleteRequest) (*publicv1.ClusterVersionsDeleteResponse, error) {
	privateRequest := &privatev1.ClusterVersionsDeleteRequest{}
	privateRequest.SetId(request.GetId())

	_, err := s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	return &publicv1.ClusterVersionsDeleteResponse{}, nil
}
