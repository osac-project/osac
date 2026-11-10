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

// PrivateQuotaLimitRequestsServer serves the limit increase requests. Approving a pending request
// sets the requested limit for its tenant and dimension in the same transaction as the approval;
// denying changes no limit.
type PrivateQuotaLimitRequestsServer struct {
	privatev1.UnimplementedQuotaLimitRequestsServer

	logger           *slog.Logger
	attributionLogic auth.AttributionLogic
	generic          *GenericServer[*privatev1.QuotaLimitRequest]
	requestsDao      *dao.GenericDAO[*privatev1.QuotaLimitRequest]
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
	requestsDao, err := dao.NewGenericDAO[*privatev1.QuotaLimitRequest]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
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
		generic:          generic,
		requestsDao:      requestsDao,
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
	if limitRequest.GetMetadata().GetProject() != "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'metadata.project' must be empty: a limit request asks for a tenant limit")
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

func (s *PrivateQuotaLimitRequestsServer) Approve(ctx context.Context,
	request *privatev1.QuotaLimitRequestsApproveRequest) (response *privatev1.QuotaLimitRequestsApproveResponse, err error) {
	limitRequest, err := s.lockPendingRequest(ctx, request.GetId(), "approve")
	if err != nil {
		return
	}
	// The requested limit is set in the same transaction as the approval, so a client can never
	// see an approved request whose limit didn't land:
	if err = s.applyRequestedLimit(ctx, limitRequest); err != nil {
		return
	}
	limitRequest.GetStatus().SetState(privatev1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_APPROVED)
	if _, err = s.requestsDao.Update().SetObject(limitRequest).Do(ctx); err != nil {
		return nil, ConvertDAOErrorToGRPC(err, "update", request.GetId())
	}
	response = &privatev1.QuotaLimitRequestsApproveResponse{}
	return
}

func (s *PrivateQuotaLimitRequestsServer) Deny(ctx context.Context,
	request *privatev1.QuotaLimitRequestsDenyRequest) (response *privatev1.QuotaLimitRequestsDenyResponse, err error) {
	limitRequest, err := s.lockPendingRequest(ctx, request.GetId(), "deny")
	if err != nil {
		return
	}
	limitRequest.GetStatus().SetState(privatev1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_DENIED)
	if _, err = s.requestsDao.Update().SetObject(limitRequest).Do(ctx); err != nil {
		return nil, ConvertDAOErrorToGRPC(err, "update", request.GetId())
	}
	response = &privatev1.QuotaLimitRequestsDenyResponse{}
	return
}

func (s *PrivateQuotaLimitRequestsServer) Signal(ctx context.Context,
	request *privatev1.QuotaLimitRequestsSignalRequest) (response *privatev1.QuotaLimitRequestsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

// lockPendingRequest loads and locks a request that a review can act on: only a pending request
// can be approved or denied, and the lock keeps two reviews from racing.
func (s *PrivateQuotaLimitRequestsServer) lockPendingRequest(
	ctx context.Context, id, action string) (*privatev1.QuotaLimitRequest, error) {
	if id == "" {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
	}
	response, err := s.requestsDao.Get().
		SetId(id).
		SetLock(true).
		Do(ctx)
	if err != nil {
		return nil, ConvertDAOErrorToGRPC(err, "get", id)
	}
	limitRequest := response.GetObject()
	if limitRequest.GetStatus().GetState() != privatev1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_PENDING {
		return nil, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"quota limit request '%s' is %s, and only a pending request can be %sd",
			id, limitRequest.GetStatus().GetState().String(), action)
	}
	return limitRequest, nil
}

// applyRequestedLimit sets the requested limit for the request's tenant and dimension: a new
// tenant override when none exists, else the override's new value.
func (s *PrivateQuotaLimitRequestsServer) applyRequestedLimit(
	ctx context.Context, limitRequest *privatev1.QuotaLimitRequest) error {
	tenant := limitRequest.GetMetadata().GetTenant()
	dimension := limitRequest.GetSpec().GetDimension()
	filter := fmt.Sprintf(
		"this.spec.dimension == %s && this.metadata.tenant == %s && this.metadata.project == \"\"",
		strconv.Quote(dimension), strconv.Quote(tenant),
	)
	response, err := s.limitsDao.List().
		SetFilter(filter).
		SetLimit(1).
		Do(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to query the tenant's quota limit",
			slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to apply the requested limit")
	}
	if len(response.GetItems()) > 0 {
		limit := response.GetItems()[0]
		limit.GetSpec().SetValue(limitRequest.GetSpec().GetRequestedValue())
		_, err = s.limitsDao.Update().SetObject(limit).Do(ctx)
	} else {
		creator, creatorErr := s.attributionLogic.DetermineAssignedCreator(ctx)
		if creatorErr != nil {
			return grpcstatus.Errorf(grpccodes.Internal, "failed to apply the requested limit")
		}
		limit := privatev1.QuotaLimit_builder{
			Metadata: privatev1.Metadata_builder{
				Name:    quotaNameFromDimension(dimension),
				Tenant:  tenant,
				Creator: creator,
			}.Build(),
			Spec: privatev1.QuotaLimitSpec_builder{
				Dimension: dimension,
				Value:     limitRequest.GetSpec().GetRequestedValue(),
			}.Build(),
		}.Build()
		_, err = s.limitsDao.Create().SetObject(limit).Do(ctx)
	}
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to write the tenant's quota limit",
			slog.Any("error", err))
		return ConvertDAOErrorToGRPC(err, "update", limitRequest.GetId())
	}
	return nil
}

// validateRequestUpdate rejects a change to a request's identity, a change to its review state,
// and any change to a request that is no longer pending: a new request replaces a reviewed one.
func (s *PrivateQuotaLimitRequestsServer) validateRequestUpdate(
	_ context.Context, candidate, current *privatev1.QuotaLimitRequest) error {
	if current.GetStatus().GetState() != privatev1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_PENDING {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"quota limit request is %s, and only a pending request can be changed",
			current.GetStatus().GetState().String())
	}
	if candidate.GetStatus().GetState() == privatev1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_UNSPECIFIED {
		// An update that omits the status leaves the stored one in place:
		candidate.SetStatus(current.GetStatus())
	} else if candidate.GetStatus().GetState() != current.GetStatus().GetState() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'status.state' is set only by the Approve and Deny operations")
	}
	if candidate.GetSpec().GetDimension() != current.GetSpec().GetDimension() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.dimension' is immutable and cannot be changed from '%s' to '%s'",
			current.GetSpec().GetDimension(), candidate.GetSpec().GetDimension())
	}
	if candidate.GetMetadata().GetTenant() != current.GetMetadata().GetTenant() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'metadata.tenant' is immutable")
	}
	if candidate.GetMetadata().GetName() != current.GetMetadata().GetName() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'metadata.name' is immutable")
	}
	return nil
}
