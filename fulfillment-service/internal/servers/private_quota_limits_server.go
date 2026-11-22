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
	"fmt"
	"log/slog"
	"strconv"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateQuotaLimitsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.QuotaLimitsServer = (*PrivateQuotaLimitsServer)(nil)

// PrivateQuotaLimitsServer serves the tenant overrides and project limits through the generic
// machinery, and the default limits, which belong to no tenant, through their own store.
type PrivateQuotaLimitsServer struct {
	privatev1.UnimplementedQuotaLimitsServer

	logger           *slog.Logger
	generic          *GenericServer[*privatev1.QuotaLimit]
	defaultsDao      *dao.GenericDAO[*privatev1.QuotaDefaultLimit]
	attributionLogic auth.AttributionLogic
}

func NewPrivateQuotaLimitsServer() *PrivateQuotaLimitsServerBuilder {
	return &PrivateQuotaLimitsServerBuilder{}
}

func (b *PrivateQuotaLimitsServerBuilder) SetLogger(value *slog.Logger) *PrivateQuotaLimitsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateQuotaLimitsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateQuotaLimitsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateQuotaLimitsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateQuotaLimitsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateQuotaLimitsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateQuotaLimitsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateQuotaLimitsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateQuotaLimitsServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateQuotaLimitsServerBuilder) Build() (result *PrivateQuotaLimitsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	if b.attributionLogic == nil {
		err = errors.New("attribution logic is mandatory")
		return
	}
	defaultsDao, err := dao.NewGenericDAO[*privatev1.QuotaDefaultLimit]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}
	generic, err := NewGenericServer[*privatev1.QuotaLimit]().
		SetLogger(b.logger).
		SetService(privatev1.QuotaLimits_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}
	result = &PrivateQuotaLimitsServer{
		logger:           b.logger,
		generic:          generic,
		defaultsDao:      defaultsDao,
		attributionLogic: b.attributionLogic,
	}
	return
}

func (s *PrivateQuotaLimitsServer) List(ctx context.Context,
	request *privatev1.QuotaLimitsListRequest) (response *privatev1.QuotaLimitsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateQuotaLimitsServer) Get(ctx context.Context,
	request *privatev1.QuotaLimitsGetRequest) (response *privatev1.QuotaLimitsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateQuotaLimitsServer) Create(ctx context.Context,
	request *privatev1.QuotaLimitsCreateRequest) (response *privatev1.QuotaLimitsCreateResponse, err error) {
	limit := request.GetObject()
	if err = validateQuotaDimension(limit.GetSpec().GetDimension()); err != nil {
		return
	}
	// The plain create path runs no protovalidate in-process, so the value's presence is
	// checked here as the gateway's interceptor would check it.
	if !limit.GetSpec().HasValue() {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "limit value is mandatory")
		return
	}
	if limit.GetSpec().GetValue() < 0 {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "limit value can't be negative")
		return
	}
	fillQuotaName(limit, limit.GetSpec().GetDimension())
	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateQuotaLimitsServer) Update(ctx context.Context,
	request *privatev1.QuotaLimitsUpdateRequest) (response *privatev1.QuotaLimitsUpdateResponse, err error) {
	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}
	err = s.generic.UpdateWithValidation(ctx, request, &response, validateQuotaLimitUpdate)
	return
}

func (s *PrivateQuotaLimitsServer) Delete(ctx context.Context,
	request *privatev1.QuotaLimitsDeleteRequest) (response *privatev1.QuotaLimitsDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateQuotaLimitsServer) Signal(ctx context.Context,
	request *privatev1.QuotaLimitsSignalRequest) (response *privatev1.QuotaLimitsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

func (s *PrivateQuotaLimitsServer) ListDefaults(ctx context.Context,
	request *privatev1.QuotaLimitsListDefaultsRequest) (response *privatev1.QuotaLimitsListDefaultsResponse, err error) {
	listRequest := s.defaultsDao.List().
		SetFilter(request.GetFilter()).
		SetOffset(request.GetOffset())
	if request.HasLimit() {
		listRequest.SetLimit(request.GetLimit())
	}
	listResponse, listErr := listRequest.Do(ctx)
	if listErr != nil {
		err = ConvertDAOErrorToGRPC(listErr, "list", "")
		return
	}
	response = &privatev1.QuotaLimitsListDefaultsResponse{}
	response.SetItems(listResponse.GetItems())
	response.SetSize(int32(len(listResponse.GetItems()))) // #nosec G115 -- bounded by the table size
	response.SetTotal(listResponse.GetTotal())
	return
}

func (s *PrivateQuotaLimitsServer) GetDefault(ctx context.Context,
	request *privatev1.QuotaLimitsGetDefaultRequest) (response *privatev1.QuotaLimitsGetDefaultResponse, err error) {
	if request.GetId() == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}
	getResponse, getErr := s.defaultsDao.Get().SetId(request.GetId()).Do(ctx)
	if getErr != nil {
		err = ConvertDAOErrorToGRPC(getErr, "get", request.GetId())
		return
	}
	response = &privatev1.QuotaLimitsGetDefaultResponse{}
	response.SetObject(getResponse.GetObject())
	return
}

func (s *PrivateQuotaLimitsServer) SetDefault(ctx context.Context,
	request *privatev1.QuotaLimitsSetDefaultRequest) (response *privatev1.QuotaLimitsSetDefaultResponse, err error) {
	limit := request.GetObject()
	if err = validateQuotaDimension(limit.GetSpec().GetDimension()); err != nil {
		return
	}
	// The custom write path runs no protovalidate, so the value's presence and sign are
	// checked here: an omitted value must not silently become the zero that refuses
	// everything, and a negative limit would refuse nothing.
	if !limit.GetSpec().HasValue() {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "limit value is mandatory")
		return
	}
	if limit.GetSpec().GetValue() < 0 {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "limit value can't be negative")
		return
	}
	fillQuotaName(limit, limit.GetSpec().GetDimension())
	creator, creatorErr := s.attributionLogic.DetermineAssignedCreator(ctx)
	if creatorErr != nil {
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to determine the creator")
		return
	}
	limit.GetMetadata().SetCreator(creator)
	// A default belongs to the shared tenant, the way every platform-scoped kind does:
	limit.GetMetadata().SetTenant(auth.SharedTenant)
	object, setErr := upsertQuotaLimit(
		ctx,
		s.defaultsDao,
		fmt.Sprintf("this.spec.dimension == %s", strconv.Quote(limit.GetSpec().GetDimension())),
		limit,
		func(existing, fresh *privatev1.QuotaDefaultLimit) *privatev1.QuotaDefaultLimit {
			existing.SetSpec(fresh.GetSpec())
			existing.GetMetadata().SetCreator(fresh.GetMetadata().GetCreator())
			return existing
		},
	)
	if setErr != nil {
		err = s.internalDefaultsError(ctx, setErr)
		return
	}
	response = &privatev1.QuotaLimitsSetDefaultResponse{}
	response.SetObject(object)
	return
}

func (s *PrivateQuotaLimitsServer) ClearDefault(ctx context.Context,
	request *privatev1.QuotaLimitsClearDefaultRequest) (response *privatev1.QuotaLimitsClearDefaultResponse, err error) {
	if request.GetId() == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}
	if _, clearErr := s.defaultsDao.Delete().SetId(request.GetId()).Do(ctx); clearErr != nil {
		err = ConvertDAOErrorToGRPC(clearErr, "delete", request.GetId())
		return
	}
	response = &privatev1.QuotaLimitsClearDefaultResponse{}
	return
}

// internalDefaultsError logs the failure and hides its detail from the caller, except for a
// rejection the upsert already phrased for the caller, like a taken name.
func (s *PrivateQuotaLimitsServer) internalDefaultsError(ctx context.Context, err error) error {
	if _, ok := grpcstatus.FromError(err); ok {
		return err
	}
	s.logger.ErrorContext(ctx, "Failed to manage the quota default limits",
		slog.Any("error", err))
	return grpcstatus.Errorf(grpccodes.Internal, "failed to manage the quota default limits")
}

// validateQuotaDimension rejects a quota object whose spec names no dimension.
func validateQuotaDimension(dimension string) error {
	if dimension == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.dimension' is required")
	}
	return nil
}

// quotaRequestName derives a request's name from its dimension with a unique suffix, so a new
// request of a dimension doesn't collide with a reviewed one that still holds the plain name.
func quotaRequestName(dimension string) string {
	name := quotaNameFromDimension(dimension)
	if len(name) > 54 {
		name = name[:54]
	}
	return name + "-" + uuid.NewString()[:8]
}

// fillQuotaName gives the object the name derived from its dimension when the caller sent none.
func fillQuotaName(object quotaNamedObject, dimension string) {
	metadata := object.GetMetadata()
	if metadata == nil {
		metadata = &privatev1.Metadata{}
		object.SetMetadata(metadata)
	}
	if metadata.GetName() == "" {
		metadata.SetName(quotaNameFromDimension(dimension))
	}
}

// quotaNamedObject is any quota object the generic DAO stores whose metadata carries its name.
type quotaNamedObject interface {
	dao.Object
	GetMetadata() *privatev1.Metadata
	SetMetadata(*privatev1.Metadata)
}

// validateQuotaLimitUpdate rejects a change to a limit's identity: the dimension, tenant, project
// and name never change, only the value does.
func validateQuotaLimitUpdate(_ context.Context, candidate, current *privatev1.QuotaLimit) error {
	if candidate.GetSpec().GetDimension() != current.GetSpec().GetDimension() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.dimension' is immutable and cannot be changed from '%s' to '%s'",
			current.GetSpec().GetDimension(), candidate.GetSpec().GetDimension())
	}
	if candidate.GetMetadata().GetTenant() != current.GetMetadata().GetTenant() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'metadata.tenant' is immutable")
	}
	if candidate.GetMetadata().GetProject() != current.GetMetadata().GetProject() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'metadata.project' is immutable")
	}
	if candidate.GetMetadata().GetName() != current.GetMetadata().GetName() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'metadata.name' is immutable")
	}
	return nil
}
