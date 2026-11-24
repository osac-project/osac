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

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateQuotaLimitRequestsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.QuotaLimitRequestsServer = (*PrivateQuotaLimitRequestsServer)(nil)

// PrivateQuotaLimitRequestsServer serves the limit increase requests. A review, an update that
// sets a pending request's state, sets the requested limit for its tenant and dimension in the
// same transaction as the approval; a denial changes no limit.
type PrivateQuotaLimitRequestsServer struct {
	privatev1.UnimplementedQuotaLimitRequestsServer

	logger           *slog.Logger
	attributionLogic auth.AttributionLogic
	tenancyLogic     auth.TenancyLogic
	generic          *GenericServer[*privatev1.QuotaLimitRequest]
	limitsDao        *dao.GenericDAO[*privatev1.QuotaLimit]
}

func NewPrivateQuotaLimitRequestsServer() *PrivateQuotaLimitRequestsServerBuilder {
	return &PrivateQuotaLimitRequestsServerBuilder{}
}

func (b *PrivateQuotaLimitRequestsServerBuilder) SetLogger(value *slog.Logger) *PrivateQuotaLimitRequestsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateQuotaLimitRequestsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateQuotaLimitRequestsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateQuotaLimitRequestsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateQuotaLimitRequestsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateQuotaLimitRequestsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateQuotaLimitRequestsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateQuotaLimitRequestsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateQuotaLimitRequestsServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateQuotaLimitRequestsServerBuilder) Build() (result *PrivateQuotaLimitRequestsServer, err error) {
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
	limitsDao, err := dao.NewGenericDAO[*privatev1.QuotaLimit]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}
	generic, err := NewGenericServer[*privatev1.QuotaLimitRequest]().
		SetLogger(b.logger).
		SetService(privatev1.QuotaLimitRequests_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}
	result = &PrivateQuotaLimitRequestsServer{
		logger:           b.logger,
		attributionLogic: b.attributionLogic,
		tenancyLogic:     b.tenancyLogic,
		generic:          generic,
		limitsDao:        limitsDao,
	}
	return
}

func (s *PrivateQuotaLimitRequestsServer) List(ctx context.Context,
	request *privatev1.QuotaLimitRequestsListRequest) (response *privatev1.QuotaLimitRequestsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateQuotaLimitRequestsServer) Get(ctx context.Context,
	request *privatev1.QuotaLimitRequestsGetRequest) (response *privatev1.QuotaLimitRequestsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateQuotaLimitRequestsServer) Create(ctx context.Context,
	request *privatev1.QuotaLimitRequestsCreateRequest) (response *privatev1.QuotaLimitRequestsCreateResponse, err error) {
	limitRequest := request.GetObject()
	if err = validateQuotaDimension(limitRequest.GetSpec().GetDimension()); err != nil {
		return
	}
	// The plain create path runs no protovalidate in-process, so the value's presence and
	// sign are checked here as the gateway's interceptor would check them: an omitted
	// value must not silently become the zero that refuses everything, and a negative
	// one would ask for a limit that refuses nothing.
	if !limitRequest.GetSpec().HasRequestedValue() {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "requested limit value is mandatory")
		return
	}
	if limitRequest.GetSpec().GetRequestedValue() < 0 {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "requested limit value can't be negative")
		return
	}
	if limitRequest.GetMetadata().GetProject() != "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'metadata.project' must be empty: a limit request asks for a limit that covers the whole tenant")
		return
	}
	if err = rejectOutputStatusOnCreate(limitRequest.HasStatus()); err != nil {
		return
	}
	if limitRequest.GetMetadata().GetName() == "" {
		if limitRequest.GetMetadata() == nil {
			limitRequest.SetMetadata(&privatev1.Metadata{})
		}
		// A reviewed request doesn't block a new one, so the generated name carries a unique
		// suffix instead of the dimension alone, which every request of the dimension shares.
		limitRequest.GetMetadata().SetName(quotaRequestName(limitRequest.GetSpec().GetDimension()))
	}
	limitRequest.SetStatus(privatev1.QuotaLimitRequestStatus_builder{
		State: privatev1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_PENDING,
	}.Build())
	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateQuotaLimitRequestsServer) Update(ctx context.Context,
	request *privatev1.QuotaLimitRequestsUpdateRequest) (response *privatev1.QuotaLimitRequestsUpdateResponse, err error) {
	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}
	err = s.generic.UpdateWithValidation(ctx, request, &response, s.validateRequestUpdate)
	return
}

func (s *PrivateQuotaLimitRequestsServer) Delete(ctx context.Context,
	request *privatev1.QuotaLimitRequestsDeleteRequest) (response *privatev1.QuotaLimitRequestsDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateQuotaLimitRequestsServer) Signal(ctx context.Context,
	request *privatev1.QuotaLimitRequestsSignalRequest) (response *privatev1.QuotaLimitRequestsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

// applyRequestedLimit sets the requested limit for the request's tenant and dimension: a new
// tenant override when none exists, else the override's new value.
func (s *PrivateQuotaLimitRequestsServer) applyRequestedLimit(
	ctx context.Context, limitRequest *privatev1.QuotaLimitRequest) error {
	tenant := limitRequest.GetMetadata().GetTenant()
	dimension := limitRequest.GetSpec().GetDimension()
	creator, creatorErr := s.attributionLogic.DetermineAssignedCreator(ctx)
	if creatorErr != nil {
		return grpcstatus.Errorf(grpccodes.Internal, "failed to apply the requested limit")
	}
	requested := limitRequest.GetSpec().GetRequestedValue()
	filter := fmt.Sprintf(
		"this.spec.dimension == %s && this.metadata.tenant == %s && this.metadata.project == \"\"",
		strconv.Quote(dimension), strconv.Quote(tenant),
	)
	// The stored override may carry a caller-chosen name, and the upsert refuses a fresh
	// name that differs from the stored one, so the fresh object takes the stored row's
	// name; a dimension with no override yet keeps the derived name:
	name := quotaNameFromDimension(dimension)
	stored, err := s.limitsDao.List().
		SetFilter(filter).
		SetLimit(1).
		Do(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to read the tenant's quota limit",
			slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to apply the requested limit")
	}
	if len(stored.GetItems()) > 0 {
		name = stored.GetItems()[0].GetMetadata().GetName()
	}
	fresh := privatev1.QuotaLimit_builder{
		Metadata: privatev1.Metadata_builder{
			Name:    name,
			Tenant:  tenant,
			Creator: creator,
		}.Build(),
		Spec: privatev1.QuotaLimitSpec_builder{
			Dimension: dimension,
			Value:     &requested,
			Scope:     privatev1.QuotaScope_QUOTA_SCOPE_TENANT.Enum(),
		}.Build(),
	}.Build()
	_, err = upsertQuotaLimit(
		ctx,
		s.limitsDao,
		filter,
		fresh,
		func(existing, fresh *privatev1.QuotaLimit) *privatev1.QuotaLimit {
			existing.SetSpec(fresh.GetSpec())
			return existing
		},
	)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to write the tenant's quota limit",
			slog.Any("error", err))
		// A rejection the upsert phrased for the caller, like a taken name, passes
		// through; the rest reads as the update's failure:
		if _, ok := grpcstatus.FromError(err); ok {
			return err
		}
		return ConvertDAOErrorToGRPC(err, "update", limitRequest.GetId())
	}
	return nil
}

// validateRequestUpdate rejects a change to a request's identity, a change to a request that is
// no longer pending, and a review by a caller who may not review: a new request replaces a
// reviewed one, and only a cloud provider admin may approve or deny.
func (s *PrivateQuotaLimitRequestsServer) validateRequestUpdate(
	ctx context.Context, candidate, current *privatev1.QuotaLimitRequest) error {
	if current.GetStatus().GetState() != privatev1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_PENDING {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"quota limit request is %s, and only a pending request can be changed",
			current.GetStatus().GetState().String())
	}
	if candidate.GetStatus().GetState() == privatev1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_UNSPECIFIED {
		// An update that omits the status leaves the stored one in place:
		candidate.SetStatus(current.GetStatus())
	}
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
	if candidate.GetStatus().GetState() != current.GetStatus().GetState() {
		return s.reviewRequest(ctx, candidate)
	}
	return nil
}

// reviewRequest applies the review, the change of a pending request's state. Only a cloud
// provider admin may review: an approval sets the requested limit in the same transaction as the
// state change, so a client can never see an approved request whose limit didn't land, and a
// denial writes nothing but the state.
func (s *PrivateQuotaLimitRequestsServer) reviewRequest(
	ctx context.Context, candidate *privatev1.QuotaLimitRequest) error {
	state := candidate.GetStatus().GetState()
	if state != privatev1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_APPROVED &&
		state != privatev1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_DENIED {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'status.state' can only be changed to %s or %s",
			privatev1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_APPROVED,
			privatev1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_DENIED)
	}
	visibility, err := s.tenancyLogic.DetermineVisibility(ctx)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.Internal, "failed to determine the visible tenants")
	}
	if !visibility.IsTotal() {
		return grpcstatus.Errorf(grpccodes.PermissionDenied,
			"the review of a quota limit request is a cloud provider admin's to make")
	}
	if state == privatev1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_DENIED {
		return nil
	}
	return s.applyRequestedLimit(ctx, candidate)
}
