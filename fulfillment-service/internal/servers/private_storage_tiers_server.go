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
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
	"github.com/osac-project/fulfillment-service/internal/events"
)

type PrivateStorageTiersServerBuilder struct {
	logger             *slog.Logger
	notifier           events.Notifier
	attributionLogic   auth.AttributionLogic
	tenancyLogic       auth.TenancyLogic
	metricsRegisterer  prometheus.Registerer
	storageBackendsDAO *dao.GenericDAO[*privatev1.StorageBackend]
}

var _ privatev1.StorageTiersServer = (*PrivateStorageTiersServer)(nil)

type PrivateStorageTiersServer struct {
	privatev1.UnimplementedStorageTiersServer

	logger             *slog.Logger
	generic            *GenericServer[*privatev1.StorageTier]
	storageBackendsDAO *dao.GenericDAO[*privatev1.StorageBackend]
}

func NewPrivateStorageTiersServer() *PrivateStorageTiersServerBuilder {
	return &PrivateStorageTiersServerBuilder{}
}

func (b *PrivateStorageTiersServerBuilder) SetLogger(value *slog.Logger) *PrivateStorageTiersServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateStorageTiersServerBuilder) SetNotifier(value events.Notifier) *PrivateStorageTiersServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateStorageTiersServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateStorageTiersServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateStorageTiersServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateStorageTiersServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateStorageTiersServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateStorageTiersServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateStorageTiersServerBuilder) SetStorageBackendsDAO(value *dao.GenericDAO[*privatev1.StorageBackend]) *PrivateStorageTiersServerBuilder {
	b.storageBackendsDAO = value
	return b
}

func (b *PrivateStorageTiersServerBuilder) Build() (result *PrivateStorageTiersServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
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

	generic, err := NewGenericServer[*privatev1.StorageTier]().
		SetLogger(b.logger).
		SetService(privatev1.StorageTiers_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &PrivateStorageTiersServer{
		logger:             b.logger,
		generic:            generic,
		storageBackendsDAO: b.storageBackendsDAO,
	}
	return
}

func (s *PrivateStorageTiersServer) List(ctx context.Context,
	request *privatev1.StorageTiersListRequest) (response *privatev1.StorageTiersListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateStorageTiersServer) Get(ctx context.Context,
	request *privatev1.StorageTiersGetRequest) (response *privatev1.StorageTiersGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateStorageTiersServer) Create(ctx context.Context,
	request *privatev1.StorageTiersCreateRequest) (response *privatev1.StorageTiersCreateResponse, err error) {
	err = s.validateStorageTierCreate(ctx, request.GetObject())
	if err != nil {
		return
	}

	st := request.GetObject()
	st.SetStatus(privatev1.StorageTierStatus_builder{
		State: privatev1.StorageTierState_STORAGE_TIER_STATE_ACTIVE,
	}.Build())

	st.SetId("")

	// StorageTier is platform-scoped; force tenant to "shared" so all authenticated users can see it.
	if st.GetMetadata() == nil {
		st.SetMetadata(&privatev1.Metadata{})
	}
	st.GetMetadata().SetTenant(auth.SharedTenant)

	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateStorageTiersServer) Update(ctx context.Context,
	request *privatev1.StorageTiersUpdateRequest) (response *privatev1.StorageTiersUpdateResponse, err error) {
	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	getRequest := &privatev1.StorageTiersGetRequest{}
	getRequest.SetId(id)
	var getResponse *privatev1.StorageTiersGetResponse
	err = s.generic.Get(ctx, getRequest, &getResponse)
	if err != nil {
		return
	}

	existingST := getResponse.GetObject()

	err = s.validateStorageTierUpdate(ctx, request.GetObject(), existingST, request.GetUpdateMask())
	if err != nil {
		return
	}

	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateStorageTiersServer) Delete(ctx context.Context,
	request *privatev1.StorageTiersDeleteRequest) (response *privatev1.StorageTiersDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateStorageTiersServer) Signal(ctx context.Context,
	request *privatev1.StorageTiersSignalRequest) (response *privatev1.StorageTiersSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

func (s *PrivateStorageTiersServer) validateStorageTierCreate(ctx context.Context,
	st *privatev1.StorageTier) error {

	if st == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "storage tier is mandatory")
	}
	if st.GetMetadata() == nil || st.GetMetadata().GetName() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'metadata.name' is required")
	}
	backends := st.GetSpec().GetBackends()
	if len(backends) == 0 {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.backends' is required and must not be empty")
	}
	return s.validateBackends(ctx, backends)
}

func (s *PrivateStorageTiersServer) validateStorageTierUpdate(ctx context.Context,
	newST *privatev1.StorageTier, existingST *privatev1.StorageTier,
	updateMask *fieldmaskpb.FieldMask) error {

	if newST.GetMetadata() != nil && newST.GetMetadata().GetName() != "" &&
		newST.GetMetadata().GetName() != existingST.GetMetadata().GetName() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'metadata.name' is immutable and cannot be changed from '%s' to '%s'",
			existingST.GetMetadata().GetName(), newST.GetMetadata().GetName())
	}
	backends := newST.GetSpec().GetBackends()
	if updateMask != nil {
		for _, path := range updateMask.GetPaths() {
			if path == "spec.backends" && len(backends) == 0 {
				return grpcstatus.Errorf(grpccodes.InvalidArgument,
					"field 'spec.backends' is required and must not be empty")
			}
		}
	} else if len(backends) == 0 {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.backends' is required and must not be empty")
	}
	if len(backends) > 0 {
		if err := s.validateBackends(ctx, backends); err != nil {
			return err
		}
	}
	return nil
}

func (s *PrivateStorageTiersServer) validateBackends(ctx context.Context,
	backends []*privatev1.BackendAssociation) error {
	if len(backends) > 1 {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"only one backend association is supported in v0.1, but %d were provided", len(backends))
	}
	for _, ba := range backends {
		if ba.GetBackendId() == "" {
			return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'backends[].backend_id' is required")
		}
		_, err := s.storageBackendsDAO.Get().SetId(ba.GetBackendId()).Do(ctx)
		if err != nil {
			var notFoundErr *dao.ErrNotFound
			if errors.As(err, &notFoundErr) {
				return grpcstatus.Errorf(grpccodes.NotFound,
					"storage backend with identifier '%s' not found", ba.GetBackendId())
			}
			return grpcstatus.Errorf(grpccodes.Internal,
				"failed to validate storage backend '%s'", ba.GetBackendId())
		}
	}
	return nil
}
