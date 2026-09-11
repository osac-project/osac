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

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type ClustersServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.ClustersServer = (*ClustersServer)(nil)

type ClustersServer struct {
	publicv1.UnimplementedClustersServer

	logger    *slog.Logger
	private   privatev1.ClustersServer
	inMapper  *GenericMapper[*publicv1.Cluster, *privatev1.Cluster]
	outMapper *GenericMapper[*privatev1.Cluster, *publicv1.Cluster]
}

func NewClustersServer() *ClustersServerBuilder {
	return &ClustersServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *ClustersServerBuilder) SetLogger(value *slog.Logger) *ClustersServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *ClustersServerBuilder) SetNotifier(value events.Notifier) *ClustersServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *ClustersServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *ClustersServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *ClustersServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *ClustersServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *ClustersServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *ClustersServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *ClustersServerBuilder) Build() (result *ClustersServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Find the full name of the 'status' field so that we can configure the generic server to ignore it. This is
	// because users don't have permission to change the status.
	var object *publicv1.Cluster
	objectReflect := object.ProtoReflect()
	objectDesc := objectReflect.Descriptor()
	statusField := objectDesc.Fields().ByName("status")
	if statusField == nil {
		err = fmt.Errorf("failed to find the status field of type '%s'", objectDesc.FullName())
		return
	}

	// Create the mappers:
	inMapper, err := NewGenericMapper[*publicv1.Cluster, *privatev1.Cluster]().
		SetLogger(b.logger).
		SetStrict(true).
		AddIgnoredFields(statusField.FullName()).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.Cluster, *publicv1.Cluster]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	// Create the private server to delegate to:
	delegate, err := NewPrivateClustersServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(objectDesc).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &ClustersServer{
		logger:    b.logger,
		private:   delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *ClustersServer) List(ctx context.Context,
	request *publicv1.ClustersListRequest) (response *publicv1.ClustersListResponse, err error) {
	// Create private request with same parameters:
	privateRequest := &privatev1.ClustersListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	if request.HasLimit() {
		privateRequest.SetLimit(request.GetLimit())
	}
	privateRequest.SetFilter(request.GetFilter())

	// Delegate to private server:
	privateResponse, err := s.private.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.Cluster, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.Cluster{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private cluster to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process clusters")
		}
		publicItems[i] = publicItem
	}

	// Create the public response:
	response = &publicv1.ClustersListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *ClustersServer) Get(ctx context.Context,
	request *publicv1.ClustersGetRequest) (response *publicv1.ClustersGetResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.ClustersGetRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	privateResponse, err := s.private.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateCluster := privateResponse.GetObject()
	publicCluster := &publicv1.Cluster{}
	err = s.outMapper.Copy(ctx, privateCluster, publicCluster)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private cluster to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster")
	}

	// Create the public response:
	response = &publicv1.ClustersGetResponse{}
	response.SetObject(publicCluster)
	return
}

func (s *ClustersServer) Create(ctx context.Context,
	request *publicv1.ClustersCreateRequest) (response *publicv1.ClustersCreateResponse, err error) {
	// Map the public cluster to private format:
	publicCluster := request.GetObject()
	if publicCluster == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateCluster := &privatev1.Cluster{}
	err = s.inMapper.Copy(ctx, publicCluster, privateCluster)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public cluster to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster")
		return
	}

	// Delegate to the private server:
	privateRequest := &privatev1.ClustersCreateRequest{}
	privateRequest.SetObject(privateCluster)
	privateResponse, err := s.private.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	createdPrivateCluster := privateResponse.GetObject()
	createdPublicCluster := &publicv1.Cluster{}
	err = s.outMapper.Copy(ctx, createdPrivateCluster, createdPublicCluster)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private cluster to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster")
		return
	}

	// Create the public response:
	response = &publicv1.ClustersCreateResponse{}
	response.SetObject(createdPublicCluster)
	return
}

func (s *ClustersServer) Update(ctx context.Context,
	request *publicv1.ClustersUpdateRequest) (response *publicv1.ClustersUpdateResponse, err error) {
	// Validate the request:
	publicCluster := request.GetObject()
	if publicCluster == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	id := publicCluster.GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	// Determine how to prepare the private cluster based on whether there's a field mask. When there's a field mask,
	// we don't want to merge into the existing object because that would prevent proper replacement of map fields
	// (like node sets). Instead, we copy to a new object and let the generic server handle the merge with the
	// database object, which correctly applies field mask semantics.
	var privateCluster *privatev1.Cluster
	updateMask := request.GetUpdateMask()
	if len(updateMask.GetPaths()) > 0 {
		privateCluster = &privatev1.Cluster{}
		privateCluster.SetId(id)
	} else {
		getRequest := &privatev1.ClustersGetRequest{}
		getRequest.SetId(id)
		var getResponse *privatev1.ClustersGetResponse
		getResponse, err = s.private.Get(ctx, getRequest)
		if err != nil {
			return nil, err
		}
		privateCluster = getResponse.GetObject()
	}
	err = s.inMapper.Copy(ctx, publicCluster, privateCluster)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public cluster to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster")
		return
	}

	// Delegate to the private server:
	privateRequest := &privatev1.ClustersUpdateRequest{}
	privateRequest.SetObject(privateCluster)
	privateRequest.SetUpdateMask(updateMask)
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.private.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	updatedPrivateCluster := privateResponse.GetObject()
	updatedPublicCluster := &publicv1.Cluster{}
	err = s.outMapper.Copy(ctx, updatedPrivateCluster, updatedPublicCluster)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private cluster to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster")
		return
	}

	// Create the public response:
	response = &publicv1.ClustersUpdateResponse{}
	response.SetObject(updatedPublicCluster)
	return
}

func (s *ClustersServer) Delete(ctx context.Context,
	request *publicv1.ClustersDeleteRequest) (response *publicv1.ClustersDeleteResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.ClustersDeleteRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	_, err = s.private.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Create the public response:
	response = &publicv1.ClustersDeleteResponse{}
	return
}
