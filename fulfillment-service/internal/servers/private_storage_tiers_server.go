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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateStorageTiersServerBuilder struct {
	logger             *slog.Logger
	attributionLogic   auth.AttributionLogic
	tenancyLogic       auth.TenancyLogic
	metricsRegisterer  prometheus.Registerer
	storageBackendsDAO *dao.GenericDAO[*privatev1.StorageBackend]
	filterDesc         protoreflect.MessageDescriptor
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

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateStorageTiersServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateStorageTiersServerBuilder {
	b.filterDesc = value
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
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		AddAllowedTenants(auth.SharedTenant).
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

	// Set id from metadata.name (name-as-primary-key, aligns with InstanceType pattern):
	st.SetId(st.GetMetadata().GetName())

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

	err = s.generic.UpdateWithValidation(ctx, request, &response, s.validateStorageTierUpdate)
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
	if err := s.generic.validator.Validate(st); err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "validation failed: %s", err)
	}
	_, err := s.validateBackends(ctx, st.GetSpec())
	return err
}

func (s *PrivateStorageTiersServer) validateStorageTierUpdate(ctx context.Context,
	candidate, current *privatev1.StorageTier) error {
	if len(candidate.GetSpec().GetBackends()) == 0 {
		return grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.backends' is required and must not be empty")
	}
	if candidate.GetMetadata().GetName() != current.GetMetadata().GetName() {
		return grpcstatus.Error(grpccodes.InvalidArgument, "field 'metadata.name' is immutable")
	}
	binding := proto.Clone(candidate.GetSpec()).(*privatev1.StorageTierSpec)
	binding.SetDescription(current.GetSpec().GetDescription())
	if proto.Equal(binding, current.GetSpec()) {
		return nil
	}
	candidateOntap, err := s.validateBackends(ctx, candidate.GetSpec())
	if err != nil {
		return err
	}
	currentOntap, err := s.validateBackends(ctx, current.GetSpec())
	if err != nil {
		return err
	}
	if currentOntap || candidateOntap {
		return grpcstatus.Error(grpccodes.InvalidArgument, "ONTAP tier backend, protocol, QoS and encryption settings are immutable; create a replacement tier")
	}
	return nil
}

func (s *PrivateStorageTiersServer) validateBackends(ctx context.Context,
	spec *privatev1.StorageTierSpec) (bool, error) {
	backends := spec.GetBackends()
	if len(backends) == 0 {
		return false, grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.backends' is required and must not be empty")
	}
	if len(backends) > 1 {
		return false, grpcstatus.Errorf(grpccodes.InvalidArgument,
			"only one backend association is supported in v0.1, but %d were provided", len(backends))
	}
	association := backends[0]
	if association.GetBackendId() == "" {
		return false, grpcstatus.Error(grpccodes.InvalidArgument, "field 'backends[].backend_id' is required")
	}
	response, err := s.storageBackendsDAO.Get().SetId(association.GetBackendId()).Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return false, grpcstatus.Errorf(grpccodes.NotFound,
				"storage backend with identifier '%s' not found", association.GetBackendId())
		}
		return false, ConvertDAOErrorToGRPC(err, "get", association.GetBackendId())
	}
	ontap := response.GetObject().GetSpec().GetProvider() == "ontap"
	if !ontap && association.GetOntap() != nil {
		return false, grpcstatus.Error(grpccodes.InvalidArgument, "ONTAP QoS configuration requires an ONTAP backend")
	}
	if ontap {
		if spec.GetProtocol() != privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK {
			return false, grpcstatus.Error(grpccodes.InvalidArgument, "ONTAP tiers require BLOCK protocol")
		}
		if association.GetMaxReadBandwidthMbs() != 0 || association.GetMaxWriteBandwidthMbs() != 0 {
			return false, grpcstatus.Error(grpccodes.InvalidArgument, "ONTAP tiers use native IOPS configuration; generic bandwidth limits must be zero")
		}
	}
	return ontap, nil
}
