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

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
)

// VolumesServerBuilder configures and constructs a VolumesServer. Use NewVolumesServer to create one.
type VolumesServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	tierResolver      TierResolverFunc
}

var _ publicv1.VolumesServer = (*VolumesServer)(nil)

// VolumesServer is the public-facing gRPC server for volumes. It delegates to the private server
// and maps between public and private protobuf types, filtering out private-only fields.
type VolumesServer struct {
	publicv1.UnimplementedVolumesServer

	logger    *slog.Logger
	delegate  privatev1.VolumesServer
	inMapper  *GenericMapper[*publicv1.Volume, *privatev1.Volume]
	outMapper *GenericMapper[*privatev1.Volume, *publicv1.Volume]
}

// NewVolumesServer creates a new builder for the public volumes server.
func NewVolumesServer() *VolumesServerBuilder {
	return &VolumesServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *VolumesServerBuilder) SetLogger(value *slog.Logger) *VolumesServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *VolumesServerBuilder) SetNotifier(value events.Notifier) *VolumesServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is mandatory.
func (b *VolumesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *VolumesServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *VolumesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *VolumesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *VolumesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *VolumesServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetTierResolver sets the tier resolver function. This is mandatory.
func (b *VolumesServerBuilder) SetTierResolver(value TierResolverFunc) *VolumesServerBuilder {
	b.tierResolver = value
	return b
}

// Build constructs the VolumesServer from the configured parameters. Returns an error if any
// mandatory parameter is missing.
func (b *VolumesServerBuilder) Build() (result *VolumesServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	if b.tierResolver == nil {
		err = errors.New("tier resolver is mandatory")
		return
	}

	// Create the mappers:
	inMapper, err := NewGenericMapper[*publicv1.Volume, *privatev1.Volume]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.Volume, *publicv1.Volume]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	// Create the private server to delegate to:
	delegate, err := NewPrivateVolumesServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetTierResolver(b.tierResolver).
		SetFilterDesc((*publicv1.Volume)(nil).ProtoReflect().Descriptor()).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &VolumesServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

// List retrieves volumes visible to the current tenant, mapping results from private to public format.
func (s *VolumesServer) List(ctx context.Context,
	request *publicv1.VolumesListRequest) (response *publicv1.VolumesListResponse, err error) {
	// Create private request with same parameters:
	privateRequest := &privatev1.VolumesListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	if request.HasLimit() {
		privateRequest.SetLimit(request.GetLimit())
	}
	privateRequest.SetFilter(request.GetFilter())

	// Delegate to private server:
	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.Volume, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.Volume{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private volume to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process volumes")
		}
		publicItems[i] = publicItem
	}

	// Create the public response:
	response = &publicv1.VolumesListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

// Get retrieves a single volume by ID, mapping the result from private to public format.
func (s *VolumesServer) Get(ctx context.Context,
	request *publicv1.VolumesGetRequest) (response *publicv1.VolumesGetResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.VolumesGetRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateVolume := privateResponse.GetObject()
	publicVolume := &publicv1.Volume{}
	err = s.outMapper.Copy(ctx, privateVolume, publicVolume)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private volume to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process volume")
	}

	// Create the public response:
	response = &publicv1.VolumesGetResponse{}
	response.SetObject(publicVolume)
	return
}

// Create creates a new volume, mapping the request from public to private format and the response back.
func (s *VolumesServer) Create(ctx context.Context,
	request *publicv1.VolumesCreateRequest) (response *publicv1.VolumesCreateResponse, err error) {
	// Map the public volume to private format:
	publicVolume := request.GetObject()
	if publicVolume == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateVolume := &privatev1.Volume{}
	err = s.inMapper.Copy(ctx, publicVolume, privateVolume)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public volume to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process volume")
		return
	}

	// Delegate to the private server:
	privateRequest := &privatev1.VolumesCreateRequest{}
	privateRequest.SetObject(privateVolume)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	createdPrivateVolume := privateResponse.GetObject()
	createdPublicVolume := &publicv1.Volume{}
	err = s.outMapper.Copy(ctx, createdPrivateVolume, createdPublicVolume)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private volume to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process volume")
		return
	}

	// Create the public response:
	response = &publicv1.VolumesCreateResponse{}
	response.SetObject(createdPublicVolume)
	return
}

// Update modifies an existing volume, mapping between public and private formats and respecting field masks.
func (s *VolumesServer) Update(ctx context.Context,
	request *publicv1.VolumesUpdateRequest) (response *publicv1.VolumesUpdateResponse, err error) {
	// Validate the request:
	publicVolume := request.GetObject()
	if publicVolume == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	id := publicVolume.GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	// Determine how to prepare the private volume based on whether there's a field mask.
	// When there's a field mask, copy to a new object and let the generic server handle the merge
	// with the database object, which correctly applies field mask semantics.
	var privateVolume *privatev1.Volume
	updateMask := request.GetUpdateMask()
	if len(updateMask.GetPaths()) > 0 {
		privateVolume = &privatev1.Volume{}
		privateVolume.SetId(id)
	} else {
		getRequest := &privatev1.VolumesGetRequest{}
		getRequest.SetId(id)
		getResponse, getErr := s.delegate.Get(ctx, getRequest)
		if getErr != nil {
			return nil, getErr
		}
		privateVolume = getResponse.GetObject()
	}
	err = s.inMapper.Copy(ctx, publicVolume, privateVolume)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public volume to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process volume")
		return
	}

	// Delegate to the private server:
	privateRequest := &privatev1.VolumesUpdateRequest{}
	privateRequest.SetObject(privateVolume)
	privateRequest.SetUpdateMask(updateMask)
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	updatedPrivateVolume := privateResponse.GetObject()
	updatedPublicVolume := &publicv1.Volume{}
	err = s.outMapper.Copy(ctx, updatedPrivateVolume, updatedPublicVolume)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private volume to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process volume")
		return
	}

	// Create the public response:
	response = &publicv1.VolumesUpdateResponse{}
	response.SetObject(updatedPublicVolume)
	return
}

// Delete removes a volume by ID, delegating to the private server.
func (s *VolumesServer) Delete(ctx context.Context,
	request *publicv1.VolumesDeleteRequest) (response *publicv1.VolumesDeleteResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.VolumesDeleteRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Create the public response:
	response = &publicv1.VolumesDeleteResponse{}
	return
}
