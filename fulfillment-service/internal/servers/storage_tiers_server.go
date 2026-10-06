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

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type StorageTiersServerBuilder struct {
	logger             *slog.Logger
	attributionLogic   auth.AttributionLogic
	tenancyLogic       auth.TenancyLogic
	metricsRegisterer  prometheus.Registerer
	storageBackendsDAO *dao.GenericDAO[*privatev1.StorageBackend]
}

var _ publicv1.StorageTiersServer = (*StorageTiersServer)(nil)

type StorageTiersServer struct {
	publicv1.UnimplementedStorageTiersServer

	logger    *slog.Logger
	delegate  privatev1.StorageTiersServer
	outMapper *GenericMapper[*privatev1.StorageTier, *publicv1.StorageTier]

	// filterValidator only validates against the PUBLIC schema to close a CEL-filter oracle (its
	// Translate() result is unused) -- do not remove this as apparently-dead code.
	filterValidator *dao.FilterTranslator
}

func NewStorageTiersServer() *StorageTiersServerBuilder {
	return &StorageTiersServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *StorageTiersServerBuilder) SetLogger(value *slog.Logger) *StorageTiersServerBuilder {
	b.logger = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is mandatory.
func (b *StorageTiersServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *StorageTiersServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *StorageTiersServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *StorageTiersServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *StorageTiersServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *StorageTiersServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetStorageBackendsDAO sets the DAO used by the private delegate to validate backend references. This is
// mandatory.
func (b *StorageTiersServerBuilder) SetStorageBackendsDAO(value *dao.GenericDAO[*privatev1.StorageBackend]) *StorageTiersServerBuilder {
	b.storageBackendsDAO = value
	return b
}

func (b *StorageTiersServerBuilder) Build() (result *StorageTiersServer, err error) {
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
	if b.storageBackendsDAO == nil {
		err = errors.New("storage backends DAO is mandatory")
		return
	}

	// spec is ignored here since it's hand-mapped in toPublicTier, not copied field-by-name:
	outMapper, err := NewGenericMapper[*privatev1.StorageTier, *publicv1.StorageTier]().
		SetLogger(b.logger).
		SetStrict(false).
		AddIgnoredFields("spec").
		Build()
	if err != nil {
		return
	}

	// Create the filter validator (see the field comment on StorageTiersServer for why):
	filterValidator, err := dao.NewFilterTranslator().
		SetLogger(b.logger).
		SetDescriptor((*publicv1.StorageTier)(nil).ProtoReflect().Descriptor()).
		Build()
	if err != nil {
		return
	}

	// Create the private server to delegate to:
	delegate, err := NewPrivateStorageTiersServer().
		SetLogger(b.logger).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetStorageBackendsDAO(b.storageBackendsDAO).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &StorageTiersServer{
		logger:          b.logger,
		delegate:        delegate,
		outMapper:       outMapper,
		filterValidator: filterValidator,
	}
	return
}

func (s *StorageTiersServer) List(ctx context.Context,
	request *publicv1.StorageTiersListRequest) (response *publicv1.StorageTiersListResponse, err error) {
	filter := request.GetFilter()
	if filter != "" {
		// Reject filters that don't compile against the public schema. This
		// is the fix for the CEL-filter oracle described on the filterValidator field.
		_, err = s.filterValidator.Translate(ctx, filter)
		if err != nil {
			err = grpcstatus.Errorf(grpccodes.InvalidArgument, "invalid filter: %v", err)
			return
		}
	}

	// Create private request with same parameters:
	privateRequest := &privatev1.StorageTiersListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	if request.HasLimit() {
		privateRequest.SetLimit(request.GetLimit())
	}
	privateRequest.SetFilter(filter)
	privateRequest.SetOrder(request.GetOrder())

	// Delegate to private server:
	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return
	}

	// A malformed tier is a cloud-provider-admin data problem, not something a tenant can act on, so
	// List logs and omits it instead of failing the whole page (Get still fails the one it was asked for).
	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.StorageTier, 0, len(privateItems))
	for _, privateItem := range privateItems {
		publicItem, itemErr := s.toPublicTier(ctx, privateItem, "failed to process storage tiers")
		if itemErr != nil {
			continue
		}
		publicItems = append(publicItems, publicItem)
	}

	// Total is corrected for dropped tiers only when this page provably holds the entire result set
	// (offset 0, every row fetched) -- otherwise the drop count outside this page is unknowable.
	total := privateResponse.GetTotal()
	if request.GetOffset() <= 0 && len(privateItems) == int(total) {
		dropped := len(privateItems) - len(publicItems)
		total -= int32(dropped) // #nosec G115 -- dropped <= len(privateItems) == total in this branch
	}
	response = &publicv1.StorageTiersListResponse{}
	response.SetSize(int32(len(publicItems))) // #nosec G115 -- bounded by page size
	response.SetTotal(total)
	response.SetItems(publicItems)
	return
}

func (s *StorageTiersServer) Get(ctx context.Context,
	request *publicv1.StorageTiersGetRequest) (response *publicv1.StorageTiersGetResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.StorageTiersGetRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return
	}

	// Map private response to public format:
	publicTier, err := s.toPublicTier(ctx, privateResponse.GetObject(), "failed to process storage tier")
	if err != nil {
		return
	}

	// Create the public response:
	response = &publicv1.StorageTiersGetResponse{}
	response.SetObject(publicTier)
	return
}

// toPublicTier maps a private tier to the public shape; errMsg lets List/Get report their own
// wording without leaking internal detail on failure.
func (s *StorageTiersServer) toPublicTier(ctx context.Context, privateTier *privatev1.StorageTier,
	errMsg string) (*publicv1.StorageTier, error) {
	publicTier := &publicv1.StorageTier{}
	err := s.outMapper.Copy(ctx, privateTier, publicTier)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private storage tier to public", slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "%s", errMsg)
	}

	backends := privateTier.GetSpec().GetBackends()
	if len(backends) == 0 {
		s.logger.ErrorContext(ctx, "Storage tier has an unexpected number of backend associations",
			slog.String("id", privateTier.GetId()), slog.Int("count", len(backends)))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "%s", errMsg)
	}
	publicTier.SetSpec(publicv1.StorageTierSpec_builder{
		Description: privateTier.GetSpec().GetDescription(),
		Protocol:    s.toPublicStorageProtocol(ctx, privateTier.GetSpec().GetProtocol()),
	}.Build())
	return publicTier, nil
}

// toPublicStorageProtocol maps explicitly (not a numeric cast) so a future private-only value logs
// instead of silently mismapping.
func (s *StorageTiersServer) toPublicStorageProtocol(ctx context.Context,
	p privatev1.StorageProtocol) publicv1.StorageProtocol {
	switch p {
	case privatev1.StorageProtocol_STORAGE_PROTOCOL_UNSPECIFIED:
		return publicv1.StorageProtocol_STORAGE_PROTOCOL_UNSPECIFIED
	case privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS:
		return publicv1.StorageProtocol_STORAGE_PROTOCOL_NFS
	case privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK:
		return publicv1.StorageProtocol_STORAGE_PROTOCOL_BLOCK
	default:
		s.logger.WarnContext(ctx, "Unknown private StorageProtocol mapped to UNSPECIFIED",
			slog.Int("value", int(p)))
		return publicv1.StorageProtocol_STORAGE_PROTOCOL_UNSPECIFIED
	}
}
