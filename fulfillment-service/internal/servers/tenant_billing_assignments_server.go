/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type PrivateTenantBillingAssignmentsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	provider          billing.Provider
	accountStore      BillingAccountStore
}

type PrivateTenantBillingAssignmentsServer struct {
	privatev1.UnimplementedTenantBillingAssignmentsServer
	logger       *slog.Logger
	generic      *GenericServer[*privatev1.TenantBillingAssignment]
	provider     billing.Provider
	accountStore BillingAccountStore
}

var _ privatev1.TenantBillingAssignmentsServer = (*PrivateTenantBillingAssignmentsServer)(nil)

func NewPrivateTenantBillingAssignmentsServer() *PrivateTenantBillingAssignmentsServerBuilder {
	return &PrivateTenantBillingAssignmentsServerBuilder{}
}

func (b *PrivateTenantBillingAssignmentsServerBuilder) SetLogger(value *slog.Logger) *PrivateTenantBillingAssignmentsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateTenantBillingAssignmentsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateTenantBillingAssignmentsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateTenantBillingAssignmentsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateTenantBillingAssignmentsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateTenantBillingAssignmentsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateTenantBillingAssignmentsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateTenantBillingAssignmentsServerBuilder) SetProvider(value billing.Provider) *PrivateTenantBillingAssignmentsServerBuilder {
	b.provider = value
	return b
}

func (b *PrivateTenantBillingAssignmentsServerBuilder) SetAccountStore(value BillingAccountStore) *PrivateTenantBillingAssignmentsServerBuilder {
	b.accountStore = value
	return b
}

func (b *PrivateTenantBillingAssignmentsServerBuilder) Build() (*PrivateTenantBillingAssignmentsServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.tenancyLogic == nil {
		return nil, errors.New("tenancy logic is mandatory")
	}
	if b.accountStore == nil {
		b.accountStore = billing.NewAccountStore()
	}
	generic, err := NewGenericServer[*privatev1.TenantBillingAssignment]().
		SetLogger(b.logger).
		SetService(privatev1.TenantBillingAssignments_ServiceDesc.ServiceName).
		SetTableName("tenant_billing_assignments").
		SetDeleteEnabled(false).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc((*publicv1.TenantBillingAssignment)(nil).ProtoReflect().Descriptor()).
		Build()
	if err != nil {
		return nil, err
	}
	return &PrivateTenantBillingAssignmentsServer{
		logger:       b.logger,
		generic:      generic,
		provider:     b.provider,
		accountStore: b.accountStore,
	}, nil
}

func (s *PrivateTenantBillingAssignmentsServer) List(ctx context.Context,
	request *privatev1.TenantBillingAssignmentsListRequest,
) (*privatev1.TenantBillingAssignmentsListResponse, error) {
	var response *privatev1.TenantBillingAssignmentsListResponse
	if err := s.generic.List(ctx, request, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (s *PrivateTenantBillingAssignmentsServer) Get(ctx context.Context,
	request *privatev1.TenantBillingAssignmentsGetRequest,
) (*privatev1.TenantBillingAssignmentsGetResponse, error) {
	var response *privatev1.TenantBillingAssignmentsGetResponse
	if err := s.generic.Get(ctx, request, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (s *PrivateTenantBillingAssignmentsServer) Create(ctx context.Context,
	request *privatev1.TenantBillingAssignmentsCreateRequest,
) (*privatev1.TenantBillingAssignmentsCreateResponse, error) {
	if request.GetObject() == nil || request.GetObject().GetSpec() == nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "assignment tenant and billing account are required")
	}
	assignment, err := s.prepareNewAssignment(ctx, request.GetObject(), request.GetObject().GetSpec().GetTenantId(),
		request.GetObject().GetSpec().GetBillingAccountId())
	if err != nil {
		return nil, err
	}
	if err := s.lockActiveTenant(ctx, assignment.GetSpec().GetTenantId()); err != nil {
		return nil, err
	}
	request = privatev1.TenantBillingAssignmentsCreateRequest_builder{Object: assignment}.Build()
	var response *privatev1.TenantBillingAssignmentsCreateResponse
	if err = s.generic.Create(ctx, request, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (s *PrivateTenantBillingAssignmentsServer) Update(ctx context.Context,
	request *privatev1.TenantBillingAssignmentsUpdateRequest,
) (*privatev1.TenantBillingAssignmentsUpdateResponse, error) {
	requested := request.GetObject()
	if requested == nil || requested.GetId() == "" || requested.GetSpec() == nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "assignment identifier, tenant, and billing account are required")
	}
	if !updateMaskContains(request.GetUpdateMask(), "spec.billing_account_id") {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "update_mask must include spec.billing_account_id")
	}
	var getResponse *privatev1.TenantBillingAssignmentsGetResponse
	if err := s.generic.Get(ctx, &privatev1.TenantBillingAssignmentsGetRequest{Id: requested.GetId()}, &getResponse); err != nil {
		return nil, err
	}
	current := getResponse.GetObject()
	if current.GetStatus().GetEffectiveTo() != nil {
		return nil, grpcstatus.Error(grpccodes.FailedPrecondition, "only the active assignment can be changed")
	}
	if requested.GetSpec().GetTenantId() != current.GetSpec().GetTenantId() {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "an assignment cannot be moved to another tenant")
	}
	if requested.GetSpec().GetBillingAccountId() == current.GetSpec().GetBillingAccountId() {
		return privatev1.TenantBillingAssignmentsUpdateResponse_builder{Object: current}.Build(), nil
	}
	if err := s.validateBillingAccount(ctx, requested.GetSpec().GetBillingAccountId()); err != nil {
		return nil, err
	}

	effectiveAt := timestamppb.New(time.Now().UTC())
	next, err := newTenantBillingAssignment(requested, current.GetSpec().GetTenantId(),
		requested.GetSpec().GetBillingAccountId(), effectiveAt)
	if err != nil {
		return nil, err
	}
	if err := s.lockActiveTenant(ctx, current.GetSpec().GetTenantId()); err != nil {
		return nil, err
	}
	closed := proto.Clone(current).(*privatev1.TenantBillingAssignment)
	closed.GetStatus().SetEffectiveTo(effectiveAt)
	if request.GetLock() && requested.GetMetadata() != nil {
		closed.GetMetadata().SetVersion(requested.GetMetadata().GetVersion())
	}
	closeRequest := privatev1.TenantBillingAssignmentsUpdateRequest_builder{
		Object:     closed,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.effective_to"}},
		Lock:       request.GetLock(),
	}.Build()
	var closeResponse *privatev1.TenantBillingAssignmentsUpdateResponse
	if err := s.generic.Update(ctx, closeRequest, &closeResponse); err != nil {
		return nil, err
	}

	createRequest := privatev1.TenantBillingAssignmentsCreateRequest_builder{Object: next}.Build()
	var createResponse *privatev1.TenantBillingAssignmentsCreateResponse
	if err = s.generic.Create(ctx, createRequest, &createResponse); err != nil {
		return nil, err
	}
	return privatev1.TenantBillingAssignmentsUpdateResponse_builder{Object: createResponse.GetObject()}.Build(), nil
}

func (s *PrivateTenantBillingAssignmentsServer) Signal(ctx context.Context,
	request *privatev1.TenantBillingAssignmentsSignalRequest,
) (*privatev1.TenantBillingAssignmentsSignalResponse, error) {
	var response *privatev1.TenantBillingAssignmentsSignalResponse
	if err := s.generic.Signal(ctx, request, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (s *PrivateTenantBillingAssignmentsServer) UpdateStatus(ctx context.Context,
	request *privatev1.TenantBillingAssignmentsUpdateStatusRequest,
) (*privatev1.TenantBillingAssignmentsUpdateStatusResponse, error) {
	if request.GetId() == "" {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "assignment identifier is required")
	}
	switch request.GetConnectionState() {
	case privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING,
		privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED,
		privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_FAILED:
	default:
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "a valid assignment connection state is required")
	}
	var getResponse *privatev1.TenantBillingAssignmentsGetResponse
	if err := s.generic.Get(ctx, &privatev1.TenantBillingAssignmentsGetRequest{Id: request.GetId()}, &getResponse); err != nil {
		return nil, err
	}
	assignment := proto.Clone(getResponse.GetObject()).(*privatev1.TenantBillingAssignment)
	if assignment.GetStatus() == nil {
		assignment.SetStatus(&privatev1.TenantBillingAssignmentStatus{})
	}
	assignment.GetStatus().SetConnectionState(request.GetConnectionState())
	if request.HasMessage() {
		assignment.GetStatus().SetMessage(request.GetMessage())
	} else {
		assignment.GetStatus().ClearMessage()
	}
	updateRequest := privatev1.TenantBillingAssignmentsUpdateRequest_builder{
		Object: assignment,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
			"status.connection_state",
			"status.message",
		}},
	}.Build()
	var updateResponse *privatev1.TenantBillingAssignmentsUpdateResponse
	if err := s.generic.Update(ctx, updateRequest, &updateResponse); err != nil {
		return nil, err
	}
	return privatev1.TenantBillingAssignmentsUpdateStatusResponse_builder{}.Build(), nil
}

func (s *PrivateTenantBillingAssignmentsServer) prepareNewAssignment(
	ctx context.Context,
	requested *privatev1.TenantBillingAssignment,
	tenantID string,
	accountID string,
) (*privatev1.TenantBillingAssignment, error) {
	if err := s.validateBillingAccount(ctx, accountID); err != nil {
		return nil, err
	}
	return newTenantBillingAssignment(requested, tenantID, accountID, timestamppb.New(time.Now().UTC()))
}

// Hold the tenant row lock until this request commits. Tenant Delete updates
// the same row, so concurrent assignment creation either finishes before
// Delete closes the assignment or sees the tenant's deletion timestamp.
func (s *PrivateTenantBillingAssignmentsServer) lockActiveTenant(ctx context.Context, tenantID string) (err error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return err
	}
	defer tx.ReportError(&err)
	var id string
	err = tx.QueryRow(ctx, `
		select id from tenants
		where name = $1 and deletion_timestamp = 'epoch'
		for update
	`, tenantID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return grpcstatus.Error(grpccodes.NotFound, "active tenant was not found")
	}
	if err != nil {
		return fmt.Errorf("failed to validate assignment tenant: %w", err)
	}
	return nil
}

func newTenantBillingAssignment(
	requested *privatev1.TenantBillingAssignment,
	tenantID string,
	accountID string,
	effectiveFrom *timestamppb.Timestamp,
) (*privatev1.TenantBillingAssignment, error) {
	if requested == nil || requested.GetSpec() == nil || tenantID == "" || accountID == "" {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "assignment tenant and billing account are required")
	}
	if effectiveFrom == nil {
		effectiveFrom = timestamppb.New(time.Now().UTC())
	}
	assignment := proto.Clone(requested).(*privatev1.TenantBillingAssignment)
	id := uuid.New()
	assignment.SetId(id)
	metadata := assignment.GetMetadata()
	if metadata == nil {
		metadata = &privatev1.Metadata{}
		assignment.SetMetadata(metadata)
	}
	if metadata.GetTenant() != "" && metadata.GetTenant() != tenantID {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "metadata.tenant must match spec.tenant_id")
	}
	metadata.SetTenant(tenantID)
	metadata.SetName(id)
	assignment.SetSpec(privatev1.TenantBillingAssignmentSpec_builder{
		TenantId:         tenantID,
		BillingAccountId: accountID,
	}.Build())
	assignment.SetStatus(privatev1.TenantBillingAssignmentStatus_builder{
		ConnectionState: privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING,
		EffectiveFrom:   proto.Clone(effectiveFrom).(*timestamppb.Timestamp),
	}.Build())
	return assignment, nil
}

func (s *PrivateTenantBillingAssignmentsServer) validateBillingAccount(ctx context.Context, id string) error {
	if s.provider == nil {
		return grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_NOT_CONFIGURED")
	}
	account, err := s.accountStore.GetAccount(ctx, id)
	if errors.Is(err, billing.ErrAccountNotFound) {
		return grpcstatus.Error(grpccodes.NotFound, "billing account was not found")
	}
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to retrieve billing account", slog.Any("error", err))
		return grpcstatus.Error(grpccodes.Internal, "failed to retrieve billing account")
	}
	if account.GetState() != privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE {
		return grpcstatus.Error(grpccodes.FailedPrecondition, "billing account is not active")
	}
	providerAccount, err := billing.RetryProviderRead(ctx, func(ctx context.Context) (billing.Account, error) {
		return s.provider.GetAccount(ctx, account.GetProviderAccountReference())
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to verify billing account state")
		return grpcstatus.Error(grpccodes.Unavailable, "billing account state could not be verified")
	}
	if providerAccount.State != billing.AccountStateActive {
		return grpcstatus.Error(grpccodes.FailedPrecondition, "billing account is not active")
	}
	return nil
}

func updateMaskContains(mask *fieldmaskpb.FieldMask, expected string) bool {
	if mask == nil {
		return false
	}
	for _, path := range mask.GetPaths() {
		if path == expected || path == "*" {
			return true
		}
	}
	return false
}

type TenantBillingAssignmentsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	provider          billing.Provider
	accountStore      BillingAccountStore
}

type TenantBillingAssignmentsServer struct {
	publicv1.UnimplementedTenantBillingAssignmentsServer
	private   privatev1.TenantBillingAssignmentsServer
	inMapper  *GenericMapper[*publicv1.TenantBillingAssignment, *privatev1.TenantBillingAssignment]
	outMapper *GenericMapper[*privatev1.TenantBillingAssignment, *publicv1.TenantBillingAssignment]
	logger    *slog.Logger
}

var _ publicv1.TenantBillingAssignmentsServer = (*TenantBillingAssignmentsServer)(nil)

func NewTenantBillingAssignmentsServer() *TenantBillingAssignmentsServerBuilder {
	return &TenantBillingAssignmentsServerBuilder{}
}

func (b *TenantBillingAssignmentsServerBuilder) SetLogger(value *slog.Logger) *TenantBillingAssignmentsServerBuilder {
	b.logger = value
	return b
}

func (b *TenantBillingAssignmentsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *TenantBillingAssignmentsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *TenantBillingAssignmentsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *TenantBillingAssignmentsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *TenantBillingAssignmentsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *TenantBillingAssignmentsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *TenantBillingAssignmentsServerBuilder) SetProvider(value billing.Provider) *TenantBillingAssignmentsServerBuilder {
	b.provider = value
	return b
}

func (b *TenantBillingAssignmentsServerBuilder) SetAccountStore(value BillingAccountStore) *TenantBillingAssignmentsServerBuilder {
	b.accountStore = value
	return b
}

func (b *TenantBillingAssignmentsServerBuilder) Build() (*TenantBillingAssignmentsServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	privateServer, err := NewPrivateTenantBillingAssignmentsServer().
		SetLogger(b.logger).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetProvider(b.provider).
		SetAccountStore(b.accountStore).
		Build()
	if err != nil {
		return nil, err
	}
	inMapper, err := NewGenericMapper[*publicv1.TenantBillingAssignment, *privatev1.TenantBillingAssignment]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return nil, err
	}
	outMapper, err := NewGenericMapper[*privatev1.TenantBillingAssignment, *publicv1.TenantBillingAssignment]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return nil, err
	}
	return &TenantBillingAssignmentsServer{private: privateServer, inMapper: inMapper, outMapper: outMapper, logger: b.logger}, nil
}

func (s *TenantBillingAssignmentsServer) List(ctx context.Context,
	request *publicv1.TenantBillingAssignmentsListRequest,
) (*publicv1.TenantBillingAssignmentsListResponse, error) {
	privateRequest := &privatev1.TenantBillingAssignmentsListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	if request.HasLimit() {
		privateRequest.SetLimit(request.GetLimit())
	}
	privateRequest.SetFilter(request.GetFilter())
	privateRequest.SetOrder(request.GetOrder())
	privateResponse, err := s.private.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	items, err := s.mapAssignments(ctx, privateResponse.GetItems())
	if err != nil {
		return nil, err
	}
	return publicv1.TenantBillingAssignmentsListResponse_builder{
		Size:  privateResponse.GetSize(),
		Total: privateResponse.GetTotal(),
		Items: items,
	}.Build(), nil
}

func (s *TenantBillingAssignmentsServer) Get(ctx context.Context,
	request *publicv1.TenantBillingAssignmentsGetRequest,
) (*publicv1.TenantBillingAssignmentsGetResponse, error) {
	privateResponse, err := s.private.Get(ctx,
		privatev1.TenantBillingAssignmentsGetRequest_builder{Id: request.GetId()}.Build())
	if err != nil {
		return nil, err
	}
	object := &publicv1.TenantBillingAssignment{}
	if err = s.outMapper.Copy(ctx, privateResponse.GetObject(), object); err != nil {
		return nil, s.mappingError(ctx, err)
	}
	return publicv1.TenantBillingAssignmentsGetResponse_builder{Object: object}.Build(), nil
}

func (s *TenantBillingAssignmentsServer) Create(ctx context.Context,
	request *publicv1.TenantBillingAssignmentsCreateRequest,
) (*publicv1.TenantBillingAssignmentsCreateResponse, error) {
	object := &privatev1.TenantBillingAssignment{}
	if err := s.inMapper.Copy(ctx, request.GetObject(), object); err != nil {
		return nil, s.mappingError(ctx, err)
	}
	privateResponse, err := s.private.Create(ctx,
		privatev1.TenantBillingAssignmentsCreateRequest_builder{Object: object}.Build())
	if err != nil {
		return nil, err
	}
	publicObject := &publicv1.TenantBillingAssignment{}
	if err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicObject); err != nil {
		return nil, s.mappingError(ctx, err)
	}
	return publicv1.TenantBillingAssignmentsCreateResponse_builder{Object: publicObject}.Build(), nil
}

func (s *TenantBillingAssignmentsServer) Update(ctx context.Context,
	request *publicv1.TenantBillingAssignmentsUpdateRequest,
) (*publicv1.TenantBillingAssignmentsUpdateResponse, error) {
	object := &privatev1.TenantBillingAssignment{}
	if err := s.inMapper.Copy(ctx, request.GetObject(), object); err != nil {
		return nil, s.mappingError(ctx, err)
	}
	privateRequest := privatev1.TenantBillingAssignmentsUpdateRequest_builder{
		Object:     object,
		UpdateMask: request.GetUpdateMask(),
		Lock:       request.GetLock(),
	}.Build()
	privateResponse, err := s.private.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	publicObject := &publicv1.TenantBillingAssignment{}
	if err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicObject); err != nil {
		return nil, s.mappingError(ctx, err)
	}
	return publicv1.TenantBillingAssignmentsUpdateResponse_builder{Object: publicObject}.Build(), nil
}

func (s *TenantBillingAssignmentsServer) mapAssignments(ctx context.Context,
	privateItems []*privatev1.TenantBillingAssignment,
) ([]*publicv1.TenantBillingAssignment, error) {
	items := make([]*publicv1.TenantBillingAssignment, 0, len(privateItems))
	for _, item := range privateItems {
		publicItem := &publicv1.TenantBillingAssignment{}
		if err := s.outMapper.Copy(ctx, item, publicItem); err != nil {
			return nil, s.mappingError(ctx, err)
		}
		items = append(items, publicItem)
	}
	return items, nil
}

func (s *TenantBillingAssignmentsServer) mappingError(ctx context.Context, err error) error {
	s.logger.ErrorContext(ctx, "failed to map tenant billing assignment", slog.Any("error", err))
	return grpcstatus.Error(grpccodes.Internal, "failed to map tenant billing assignment")
}
