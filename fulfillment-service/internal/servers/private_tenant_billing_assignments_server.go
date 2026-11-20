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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"buf.build/go/protovalidate"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// Stable connection_state column values; see TenantBillingAssignmentConnectionState in the proto.
const (
	tbaStatePending   = "PENDING"
	tbaStateConnected = "CONNECTED"
	tbaStateFailed    = "FAILED"
)

// tbaAccountAlreadyBoundReason and tbaTenantAlreadyBoundReason are the stable reasons Create/Update report when
// the account or the tenant already has an active assignment. The tenant-side reason mirrors the account-side one.
const (
	tbaAccountAlreadyBoundReason = "ACCOUNT_ALREADY_BOUND"
	tbaTenantAlreadyBoundReason  = "TENANT_ALREADY_BOUND"
	tbaAccountInactiveReason     = "ACCOUNT_INACTIVE"
	tbaAccountStateUnknownReason = "ACCOUNT_STATE_UNKNOWN"

	// billingOperationRetryReason reports a changed snapshot or SQL conflict; the caller retries.
	billingOperationRetryReason = "BILLING_OPERATION_RETRY"
)

// Provider I/O shares one bounded child context before any business locks are taken. The ordinary request
// transaction may hold a pooled connection. This is a per-process default; deployment tuning is wired separately.
const defaultAssignProviderDeadline = time.Minute

// maxConcurrentAssigns bounds provider load; SQL locks and constraints settle resource contention after I/O.
const maxConcurrentAssigns = 2

// errAssignBusy reports that maxConcurrentAssigns assigns are already resolving against the provider.
var errAssignBusy = errors.New("too many concurrent billing assignment operations")

var errAssignChanged = errors.New("billing assignment inputs changed; retry")

// PrivateTenantBillingAssignmentsServerBuilder builds a PrivateTenantBillingAssignmentsServer.
type PrivateTenantBillingAssignmentsServerBuilder struct {
	logger   *slog.Logger
	provider *billing.Provider
}

var _ privatev1.TenantBillingAssignmentsServer = (*PrivateTenantBillingAssignmentsServer)(nil)

// PrivateTenantBillingAssignmentsServer reads and changes tenant/billing-account assignments.
type PrivateTenantBillingAssignmentsServer struct {
	privatev1.UnimplementedTenantBillingAssignmentsServer
	logger      *slog.Logger
	provider    *billing.Provider
	validator   protovalidate.Validator
	assignments *dao.GenericDAO[*privatev1.TenantBillingAssignment]
	bindings    *dao.GenericDAO[*privatev1.TenantBillingAssignment]
	accounts    *dao.GenericDAO[*privatev1.BillingAccount]
	tenants     *dao.GenericDAO[*privatev1.Tenant]
	// assignProviderDeadline bounds all provider I/O in a request, before authoritative locks.
	assignProviderDeadline time.Duration
	// assignSem caps concurrent assigns in their provider-I/O window, see maxConcurrentAssigns.
	assignSem chan struct{}
}

func NewPrivateTenantBillingAssignmentsServer() *PrivateTenantBillingAssignmentsServerBuilder {
	return &PrivateTenantBillingAssignmentsServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *PrivateTenantBillingAssignmentsServerBuilder) SetLogger(
	value *slog.Logger) *PrivateTenantBillingAssignmentsServerBuilder {
	b.logger = value
	return b
}

// SetProvider sets the billing provider. This is mandatory.
func (b *PrivateTenantBillingAssignmentsServerBuilder) SetProvider(
	value *billing.Provider) *PrivateTenantBillingAssignmentsServerBuilder {
	b.provider = value
	return b
}

// Build validates that SetLogger and SetProvider were both called and returns the configured server.
func (b *PrivateTenantBillingAssignmentsServerBuilder) Build() (
	result *PrivateTenantBillingAssignmentsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.provider == nil {
		err = errors.New("provider is mandatory")
		return
	}
	validator, err := protovalidate.New()
	if err != nil {
		err = fmt.Errorf("failed to create protovalidate validator: %w", err)
		return
	}
	assignments, err := dao.NewGenericDAO[*privatev1.TenantBillingAssignment]().SetLogger(b.logger).
		SetTableName("tenant_billing_assignments").SetTenancyLogic(billingAssignmentTenancy{}).
		SetFilterDesc((*publicv1.TenantBillingAssignment)(nil).ProtoReflect().Descriptor()).Build()
	if err != nil {
		return nil, err
	}
	// Binding checks must detect foreign current assignments too, without disclosing their contents.
	bindings, err := dao.NewGenericDAO[*privatev1.TenantBillingAssignment]().SetLogger(b.logger).
		SetTableName("tenant_billing_assignments").SetTenancyLogic(billingAssignmentTenancy{inventory: true}).Build()
	if err != nil {
		return nil, err
	}
	accounts, err := dao.NewGenericDAO[*privatev1.BillingAccount]().SetLogger(b.logger).
		SetTableName("billing_accounts").SetTenancyLogic(billingAccountsTenancy{}).Build()
	if err != nil {
		return nil, err
	}
	tenants, err := dao.NewGenericDAO[*privatev1.Tenant]().SetLogger(b.logger).
		SetTenancyLogic(billingAssignmentTenancy{inventory: true}).Build()
	if err != nil {
		return nil, err
	}
	result = &PrivateTenantBillingAssignmentsServer{
		logger:      b.logger,
		provider:    b.provider,
		validator:   validator,
		assignments: assignments, bindings: bindings, accounts: accounts, tenants: tenants,
		assignProviderDeadline: defaultAssignProviderDeadline,
		assignSem:              make(chan struct{}, maxConcurrentAssigns),
	}
	return
}

// List intersects the request filter with the trusted subject's tenant scope before counting and pagination.
// Only is_admin callers reach this method; tenant membership does not grant billing method access.
func (s *PrivateTenantBillingAssignmentsServer) List(ctx context.Context,
	request *privatev1.TenantBillingAssignmentsListRequest) (
	response *privatev1.TenantBillingAssignmentsListResponse, err error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.ReportError(&err)
	if _, err = requireAdapter(s.provider); err != nil {
		return nil, err
	}

	if request.GetOffset() < 0 || request.GetLimit() < 0 {
		return nil, status.Error(codes.InvalidArgument, "limit and offset must be non-negative")
	}
	limit := request.GetLimit()
	if limit == 0 {
		limit = 100
	}
	page, err := s.listAssignments(ctx, request.GetFilter(), request.GetOffset(), limit)
	if err != nil {
		var invalid *dao.ErrValidation
		var invalidFilter *dao.ErrInvalidFilter
		if errors.As(err, &invalid) || errors.As(err, &invalidFilter) {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
		s.logger.ErrorContext(ctx, "Failed to list tenant billing assignments", slog.Any("error", err))
		return nil, status.Errorf(codes.Internal, "failed to list tenant billing assignments")
	}
	return privatev1.TenantBillingAssignmentsListResponse_builder{
		Size: page.GetSize(), Total: page.GetTotal(), Items: page.GetItems(),
	}.Build(), nil
}

// Get returns one assignment interval. Only is_admin callers reach this method (authz.rego's explicit billing
// allow-list; there is no tenant self-service path).
func (s *PrivateTenantBillingAssignmentsServer) Get(ctx context.Context,
	request *privatev1.TenantBillingAssignmentsGetRequest) (
	response *privatev1.TenantBillingAssignmentsGetResponse, err error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.ReportError(&err)
	if _, err = requireAdapter(s.provider); err != nil {
		return nil, err
	}

	if request.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	stored, err := s.getAssignment(ctx, request.GetId())
	if err != nil {
		var missing *dao.ErrNotFound
		if errors.As(err, &missing) {
			return nil, status.Errorf(codes.NotFound, "tenant billing assignment %q not found", request.GetId())
		}
		s.logger.ErrorContext(ctx, "Failed to get tenant billing assignment", slog.Any("error", err))
		return nil, status.Errorf(codes.Internal, "failed to get tenant billing assignment")
	}
	return privatev1.TenantBillingAssignmentsGetResponse_builder{
		Object: stored,
	}.Build(), nil
}

// listAssignments owns every assignment list query. Visibility handles finite sets, but cannot express
// complements. Add their trusted predicate here, before the DAO counts or pages, and parenthesize the caller
// expression so an OR can only narrow scope. Do not return a query builder that could replace this constraint.
// Use retained assignment ownership, not live tenants: history outlives the tenant row.
func (s *PrivateTenantBillingAssignmentsServer) listAssignments(ctx context.Context,
	filter string, offset, limit int32,
) (*dao.ListResponse[*privatev1.TenantBillingAssignment], error) {
	set := auth.SubjectFromContext(ctx).Tenants
	if !set.Finite() && !set.Universal() {
		excluded := set.Exclusions()
		slices.Sort(excluded)
		// JSON string literals are valid CEL literals, including quotes, backslashes and UTF-8. The
		// existing descriptor-validated translator owns SQL escaping; never interpolate raw SQL here.
		literal, err := json.Marshal(excluded)
		if err != nil {
			return nil, err
		}
		trusted := fmt.Sprintf("!(this.metadata.tenant in %s)", literal)
		if filter == "" {
			filter = trusted
		} else {
			// Validate the caller expression on its own before composition: unmatched parentheses
			// must not escape the trusted AND. Newlines keep a trailing line comment inside its group.
			if err := validateCELSyntax(filter); err != nil {
				return nil, &dao.ErrInvalidFilter{Reason: err.Error()}
			}
			filter = fmt.Sprintf("(%s) && (\n%s\n)", trusted, filter)
		}
	}
	return s.assignments.List().SetFilter(filter).SetOffset(offset).SetLimit(limit).Do(ctx)
}

// getAssignment is shared by Get and stored-target authorization for Update/Signal and their rereads.
// Complementary scopes need the same SQL constraint as List; finite/universal scopes keep the indexed Get
// fast path. An expected miss uses a bounded List, not a failed DAO Get followed by another query in a poisoned tx.
func (s *PrivateTenantBillingAssignmentsServer) getAssignment(ctx context.Context,
	id string,
) (*privatev1.TenantBillingAssignment, error) {
	set := auth.SubjectFromContext(ctx).Tenants
	if set.Finite() || set.Universal() {
		stored, err := s.assignments.Get().SetId(id).Do(ctx)
		if err != nil {
			return nil, err
		}
		return stored.GetObject(), nil
	}
	literal, err := json.Marshal(id)
	if err != nil {
		return nil, err
	}
	page, err := s.listAssignments(ctx, fmt.Sprintf("this.id == %s", literal), 0, 1)
	if err != nil {
		return nil, err
	}
	items := page.GetItems()
	if len(items) == 0 {
		return nil, &dao.ErrNotFound{IDs: []string{id}}
	}
	return items[0], nil
}

// Create assigns a tenant to a billing account for the first time.
func (s *PrivateTenantBillingAssignmentsServer) Create(ctx context.Context,
	request *privatev1.TenantBillingAssignmentsCreateRequest) (
	response *privatev1.TenantBillingAssignmentsCreateResponse, err error) {
	// Creation opens an interval from provider evidence in the same transaction. It has no safe preview
	// contract, so reject the dry-run request before any provider call or database change. Public Create
	// delegates here, and direct private callers take the same path.
	if isDryRun(ctx) {
		return nil, status.Error(codes.Unimplemented,
			"dry-run is not supported for tenant billing assignment creation")
	}
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.ReportError(&err)
	if _, err = requireAdapter(s.provider); err != nil {
		return nil, err
	}

	if request.GetObject() == nil {
		return nil, status.Error(codes.InvalidArgument, "object is required")
	}
	object := proto.Clone(request.GetObject()).(*privatev1.TenantBillingAssignment)
	if object.GetSpec() == nil {
		return nil, status.Errorf(codes.InvalidArgument, "spec is required")
	}
	if err = s.validator.Validate(object); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	object, err = s.canonicalAssignment(ctx, object)
	if err != nil {
		return nil, s.mapTenantBillingAssignmentError(ctx, err)
	}
	row, err := s.assign(ctx, object.GetSpec().GetBillingAccount().GetId(), "", tenantBillingAssignmentAssignInput{
		Name: object.GetMetadata().GetName(), TenantID: object.GetSpec().GetTenant().GetId(),
		Creator: auth.SubjectFromContext(ctx).User, Object: object,
	})
	if err != nil {
		return nil, err
	}

	return privatev1.TenantBillingAssignmentsCreateResponse_builder{
		Object: row,
	}.Build(), nil
}

type tenantBillingAssignmentSnapshot struct {
	tenant   *privatev1.Tenant
	account  *privatev1.BillingAccount
	previous *privatev1.BillingAccount
	current  *privatev1.TenantBillingAssignment
	floor    time.Time
}

// assignmentSnapshot first reads without locks, then locks/rereads authoritative rows in the same request
// transaction after provider I/O. Final locks last until the outer commit. It never calls a provider.
func (s *PrivateTenantBillingAssignmentsServer) assignmentSnapshot(
	ctx context.Context, accountID, closeID string, in tenantBillingAssignmentAssignInput,
	expected *tenantBillingAssignmentSnapshot,
) (tenantBillingAssignmentSnapshot, error) {
	var snapshot tenantBillingAssignmentSnapshot
	// Preflight is strictly read-only. Finalization locks the original complete set, never a newly discovered
	// previous account (which could have a lower SQL id). A changed required set aborts instead.
	if expected != nil {
		// Assignment-only finalization is fenced by this bounded tenant row lock, not the
		// lifecycle identity key. Keep the canonical DAO reread and material checks below.
		tx, err := database.TxFromContext(ctx)
		if err != nil {
			return snapshot, err
		}
		var lockedID string
		if err = tx.QueryRow(ctx, `select id from tenants where id = $1 for update`, in.TenantID).Scan(&lockedID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return snapshot, errAssignChanged
			}
			return snapshot, err
		}
		ids := []string{accountID}
		if expected.current != nil {
			ids = append(ids, expected.current.GetSpec().GetBillingAccount().GetId())
		}
		if _, err := s.accounts.Lock().AddIds(ids...).SetReturnObjects(true).Do(ctx); err != nil {
			return snapshot, billingSnapshotLockError(err)
		}
		if closeID != "" {
			if _, err := s.assignments.Lock().AddId(closeID).SetReturnObjects(true).Do(ctx); err != nil {
				return snapshot, billingSnapshotLockError(err)
			}
		}
	}
	tenant, err := s.tenants.Get().SetId(in.TenantID).Do(ctx)
	if err != nil {
		return snapshot, err
	}
	snapshot.tenant = tenant.GetObject()
	if !billingResourceLive(snapshot.tenant.GetMetadata()) {
		return snapshot, status.Error(codes.InvalidArgument, "spec.tenant does not reference an existing tenant")
	}
	account, err := s.accounts.Get().SetId(accountID).Do(ctx)
	if err != nil {
		return snapshot, err
	}
	snapshot.account = account.GetObject()
	if !billingResourceLive(snapshot.account.GetMetadata()) {
		return snapshot, errAssignChanged
	}
	if closeID != "" {
		snapshot.current, err = s.currentAssignment(ctx, closeID)
		if err != nil {
			if expected != nil && (status.Code(err) == codes.FailedPrecondition || status.Code(err) == codes.NotFound) {
				return snapshot, errAssignChanged
			}
			return snapshot, err
		}
		if !sameBillingAssignmentSelection(snapshot.current, in, expected) {
			return snapshot, errAssignChanged
		}
		previousID := snapshot.current.GetSpec().GetBillingAccount().GetId()
		previous, getErr := s.accounts.Get().SetId(previousID).Do(ctx)
		if getErr != nil {
			return snapshot, getErr
		}
		snapshot.previous = previous.GetObject()
	}
	active, err := s.activeTenantAssignments(ctx, in.TenantID)
	if err != nil {
		return snapshot, err
	}
	if closeID == "" && len(active) != 0 {
		if expected != nil {
			return snapshot, errAssignChanged
		}
		return snapshot, failedPreconditionWithReason(tbaTenantAlreadyBoundReason, "tenant already has an active billing assignment")
	}
	if closeID != "" && (len(active) != 1 || active[0].GetId() != closeID) {
		return snapshot, errAssignChanged
	}
	if snapshot.current == nil || snapshot.current.GetSpec().GetBillingAccount().GetId() != accountID {
		if err = s.rejectIfAccountAlreadyBound(ctx, accountID); err != nil {
			return snapshot, err
		}
	}
	snapshot.floor, err = database.TenantBillingHistoryFloor(ctx, in.TenantID)
	return snapshot, err
}

// Update replaces the tenant's active account, or revalidates the current one without opening a new billing
// interval.
func (s *PrivateTenantBillingAssignmentsServer) Update(ctx context.Context,
	request *privatev1.TenantBillingAssignmentsUpdateRequest) (
	response *privatev1.TenantBillingAssignmentsUpdateResponse, err error) {
	// An update reads provider evidence and writes account evidence plus a new interval in the same
	// transaction. It has no safe preview contract, so reject the dry-run request before any provider call or
	// database change. Public Update delegates here, and direct private callers take the same path.
	if isDryRun(ctx) {
		return nil, status.Error(codes.Unimplemented,
			"dry-run is not supported for tenant billing assignment updates")
	}
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.ReportError(&err)
	if _, err = requireAdapter(s.provider); err != nil {
		return nil, err
	}

	object := request.GetObject()
	id := object.GetId()
	if id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "id is required")
	}

	existing, err := s.currentAssignment(ctx, id)
	if err != nil {
		return nil, s.mapTenantBillingAssignmentError(ctx, err)
	}

	// Stored target authorization above deliberately precedes all caller-controlled fields and mask validation.
	if err = validateBillingAssignmentIdentity(existing, object); err != nil {
		return nil, err
	}
	for _, path := range request.GetUpdateMask().GetPaths() {
		if path != "spec.billing_account" {
			return nil, status.Error(codes.InvalidArgument, "only atomic spec.billing_account is writable")
		}
	}
	merged := proto.Clone(existing).(*privatev1.TenantBillingAssignment)
	emptyMask := request.GetUpdateMask() != nil && len(request.GetUpdateMask().GetPaths()) == 0
	if !emptyMask {
		if object.GetSpec().GetBillingAccount() == nil {
			merged.GetSpec().ClearBillingAccount()
		} else {
			merged.GetSpec().SetBillingAccount(proto.Clone(object.GetSpec().GetBillingAccount()).(*privatev1.BillingAccountReference))
		}
	}
	if err = s.validator.Validate(merged); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	if emptyMask {
		return privatev1.TenantBillingAssignmentsUpdateResponse_builder{Object: existing}.Build(), nil
	}
	merged, err = s.canonicalAssignment(ctx, merged)
	if err != nil {
		return nil, s.mapTenantBillingAssignmentError(ctx, err)
	}
	row, err := s.assign(ctx, merged.GetSpec().GetBillingAccount().GetId(), existing.GetId(), tenantBillingAssignmentAssignInput{
		Name: existing.GetMetadata().GetName(), TenantID: existing.GetSpec().GetTenant().GetId(),
		Creator: auth.SubjectFromContext(ctx).User, Object: merged,
	})
	if err != nil {
		return nil, err
	}

	return privatev1.TenantBillingAssignmentsUpdateResponse_builder{
		Object: row,
	}.Build(), nil
}

func (s *PrivateTenantBillingAssignmentsServer) currentAssignment(
	ctx context.Context, id string,
) (*privatev1.TenantBillingAssignment, error) {
	row, err := s.getAssignment(ctx, id)
	var missing *dao.ErrNotFound
	if errors.As(err, &missing) {
		return nil, status.Errorf(codes.NotFound, "tenant billing assignment %q not found", id)
	}
	if err != nil {
		return nil, err
	}
	// Check the stored tenant before interval state or caller-supplied spec.tenant. Otherwise a spoofed tenant
	// could authorize a foreign row, including a historical interval.
	if !auth.SubjectFromContext(ctx).Tenants.Contains(row.GetSpec().GetTenant().GetId()) {
		return row, status.Errorf(codes.NotFound, "tenant billing assignment %q not found", id)
	}
	if row.GetStatus().GetEffectiveToTimestamp() != nil {
		return row, status.Errorf(codes.FailedPrecondition,
			"tenant billing assignment %q is no longer the current interval", id)
	}
	return row, nil
}

// Signal synchronously revalidates only a current assignment in the trusted subject's tenant scope. The private
// RPC policy separately restricts method access to administrators/controllers.
func (s *PrivateTenantBillingAssignmentsServer) Signal(ctx context.Context,
	request *privatev1.TenantBillingAssignmentsSignalRequest,
) (response *privatev1.TenantBillingAssignmentsSignalResponse, err error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.ReportError(&err)
	if _, err = requireAdapter(s.provider); err != nil {
		return nil, err
	}
	if request.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	existing, err := s.currentAssignment(ctx, request.GetId())
	if err != nil {
		return nil, s.mapTenantBillingAssignmentError(ctx, err)
	}
	_, err = s.assign(ctx, existing.GetSpec().GetBillingAccount().GetId(), existing.GetId(), tenantBillingAssignmentAssignInput{
		Name: existing.GetMetadata().GetName(), TenantID: existing.GetSpec().GetTenant().GetId(),
		Creator: auth.SubjectFromContext(ctx).User, Object: existing,
	})
	if err != nil {
		return nil, err
	}
	return &privatev1.TenantBillingAssignmentsSignalResponse{}, nil
}

// activeTenantAssignments reads the tenant's zero or one active assignment across all scopes.
func (s *PrivateTenantBillingAssignmentsServer) activeTenantAssignments(ctx context.Context,
	tenantID string,
) ([]*privatev1.TenantBillingAssignment, error) {
	page, err := s.bindings.List().SetLimit(1).SetFilter(fmt.Sprintf(
		"this.spec.tenant.id == %q && !has(this.status.effective_to_timestamp)", tenantID)).Do(ctx)
	if err != nil {
		return nil, err
	}
	items := page.GetItems()
	// The count and page are separate statements. Do not proceed on conflicting evidence.
	if int(page.GetTotal()) != len(items) {
		return nil, errAssignChanged
	}
	return items, nil
}

// rejectIfAccountAlreadyBound fails with ACCOUNT_ALREADY_BOUND when accountID already has an active assignment.
func (s *PrivateTenantBillingAssignmentsServer) rejectIfAccountAlreadyBound(
	ctx context.Context, accountID string) error {
	page, err := s.bindings.List().SetLimit(0).SetFilter(fmt.Sprintf(
		"this.spec.billing_account.id == %q && !has(this.status.effective_to_timestamp)", accountID)).Do(ctx)
	if err != nil {
		return err
	}
	if page.GetTotal() > 0 {
		return failedPreconditionWithReason(tbaAccountAlreadyBoundReason,
			"billing account %q is already linked to a tenant", accountID)
	}
	return nil
}

// requireActiveBillingAccountState fails with ACCOUNT_INACTIVE or ACCOUNT_STATE_UNKNOWN unless state is ACTIVE.
// Assignment applies it to its fresh provider observation or newer stored evidence, including commit-time state.
func requireActiveBillingAccountState(state billing.AccountState, accountID string) error {
	switch state {
	case billing.AccountActive:
		return nil
	case billing.AccountInactive:
		return failedPreconditionWithReason(tbaAccountInactiveReason, "billing account %q is inactive", accountID)
	default:
		return failedPreconditionWithReason(tbaAccountStateUnknownReason,
			"billing account %q has an unknown state", accountID)
	}
}

// tenantBillingAssignmentAssignInput carries the fields of the assignment interval assign is about to open.
type tenantBillingAssignmentAssignInput struct {
	Name     string
	TenantID string
	Creator  string
	Object   *privatev1.TenantBillingAssignment
}

// observeSelectedAccount collects read-only evidence. Successful assignment evidence is persisted only with the
// token and interval changes, after authoritative locks. ctx is the shared provider child budget.
func (s *PrivateTenantBillingAssignmentsServer) observeSelectedAccount(
	ctx context.Context, adapter billing.Adapter, ref billing.AccountRef,
) (billing.Account, *privatev1.BillingAccountStatus, error) {
	// Match discovery's conservative observation-start timestamp: a delayed response may already be stale when
	// it arrives, so its completion time must not let it supersede a newer observation that committed meanwhile.
	// Keep nanoseconds for freshness comparisons; only the SQL projection truncates them.
	observedAt := time.Now().UTC()
	observed, err := adapter.GetAccount(ctx, ref)
	if err != nil {
		return billing.Account{}, nil, err
	}
	if observed.Ref != ref {
		return billing.Account{}, nil, billing.NewError(billing.ReasonMalformed, "GetAccount", false, 0)
	}
	return observed, privatev1.BillingAccountStatus_builder{State: mapBillingAccountState(string(observed.State)),
		StateObservationTimestamp: timestamppb.New(observedAt),
	}.Build(), nil
}

// assign reads an unlocked snapshot, gathers bounded read-only provider evidence, then locks and revalidates the
// original affected set. All local work uses the same ambient request transaction; provider I/O precedes locks.
func (s *PrivateTenantBillingAssignmentsServer) assign(
	ctx context.Context, accountID, closeID string, in tenantBillingAssignmentAssignInput,
) (row *privatev1.TenantBillingAssignment, err error) {
	if !auth.SubjectFromContext(ctx).Tenants.Contains(in.TenantID) {
		return row, status.Error(codes.PermissionDenied, "tenant is outside the authorized billing assignment scope")
	}
	// Reserved platform tenants never have billing accounts or correlation IDs. Reject before
	// generating a correlation ID, or performing any provider I/O, including Update/Signal of a legacy row.
	if in.TenantID == auth.SharedTenant || in.TenantID == auth.SystemTenant {
		return row, status.Error(codes.InvalidArgument, "reserved tenants cannot have billing assignments")
	}
	defer func() {
		if err != nil {
			if ctx.Err() != nil {
				err = status.FromContextError(ctx.Err()).Err()
			} else {
				err = s.mapTenantBillingAssignmentError(ctx, err)
			}
		}
	}()
	operationCtx, cancelOperation := context.WithTimeout(ctx, s.assignProviderDeadline+10*time.Second)
	defer cancelOperation()
	select {
	case s.assignSem <- struct{}{}:
		defer func() { <-s.assignSem }()
	default:
		return nil, errAssignBusy
	}

	snapshot, err := s.assignmentSnapshot(operationCtx, accountID, closeID, in, nil)
	if err != nil {
		return row, err
	}
	account := snapshot.account
	adapter, err := s.provider.Adapter()
	if err != nil {
		return row, mapBillingProviderBuildError(err)
	}
	ref := billing.NewAccountRef(account.GetStatus().GetProviderAccountReference())

	// Share one child budget across state, value and the complete exact-carrier walk. Do not persist observations
	// or take business locks before correlation I/O. A rejected RPC cannot commit an observation independently.
	providerCtx, cancel := context.WithTimeout(operationCtx, s.assignProviderDeadline)
	defer cancel()
	selected, observed, err := s.observeSelectedAccount(providerCtx, adapter, ref)
	if err != nil {
		if providerCtx.Err() != nil && operationCtx.Err() == nil {
			return nil, billing.NewError(billing.ReasonTimeout, "GetAccount", true, 0)
		}
		return row, err
	}
	if err := requireActiveBillingAccountState(selected.State, accountID); err != nil {
		return nil, err
	}

	resolution, carrier, err := billing.ResolveObservedAccountCorrelationID(providerCtx, billing.AccountCorrelationInput{
		Adapter: adapter, AccountID: account.GetId(), Ref: ref, LocalValue: account.GetStatus().GetExternalBillingAccountId(),
		LocalDuplicate: func(dupCtx context.Context, value, excludeAccountID string) (bool, error) {
			page, listErr := s.accounts.List().SetLimit(0).SetFilter(fmt.Sprintf(
				"this.status.external_billing_account_id == %q && this.id != %q", value, excludeAccountID)).Do(dupCtx)
			if listErr != nil {
				return false, listErr
			}
			return page.GetTotal() > 0, nil
		},
	}, selected)
	if err != nil {
		if providerCtx.Err() != nil && operationCtx.Err() == nil {
			return nil, billing.NewError(billing.ReasonTimeout, "ResolveAccountCorrelationID", true, 0)
		}
		return nil, err
	}
	if carrier != nil && carrier.State != "" {
		// A later explicit state on the exact selected carrier is eligibility evidence, not just a match count.
		if err := requireActiveBillingAccountState(carrier.State, accountID); err != nil {
			return nil, err
		}
		observed.SetState(mapBillingAccountState(string(carrier.State)))
		observed.SetStateObservationTimestamp(timestamppb.New(carrier.At))
	}
	// Only the selected account's exact, unique provider-side carrier establishes a match. No remote writes occur.
	matches := resolution.ProviderMatches

	var state, message string
	switch matches {
	case 1:
		state = tbaStateConnected
	case 0:
		state = tbaStatePending
		message = billing.ReasonProviderCorrelationWriteUnsupported +
			": set externalId on the account in the provider to the external_billing_account_id shown by BillingAccounts.List"
	default:
		return nil, &billing.CorrelationResolutionError{
			Reason: billing.ReasonAccountAmbiguous,
		}
	}

	return s.finalizeAssignment(operationCtx, snapshot, in, observed, resolution, state, message)
}

// finalizeAssignment owns the post-I/O lock/reread/merge handshake in the ambient request transaction.
// The token, observation and interval changes commit or roll back together.
func (s *PrivateTenantBillingAssignmentsServer) finalizeAssignment(txCtx context.Context,
	snapshot tenantBillingAssignmentSnapshot, in tenantBillingAssignmentAssignInput,
	observed *privatev1.BillingAccountStatus, resolution billing.Resolution, state, message string,
) (row *privatev1.TenantBillingAssignment, err error) {
	accountID := snapshot.account.GetId()
	closeID := snapshot.current.GetId()
	tx, err := database.TxFromContext(txCtx)
	if err != nil {
		return nil, err
	}
	defer tx.ReportError(&err)
	latest, err := s.assignmentSnapshot(txCtx, accountID, closeID, in, &snapshot)
	if err != nil {
		return row, err
	}
	if !sameBillingAssignmentInputs(snapshot, latest) {
		return row, errAssignChanged
	}
	// Compare observation times, not commit order: the fresh provider evidence cannot supersede a newer durable
	// observation from discovery or another assignment that committed while provider I/O was in flight.
	mergedAccount, _ := mergeBillingAccountObservation(latest.account, observed)
	if err := requireActiveBillingAccountState(assignmentAccountState(mergedAccount), accountID); err != nil {
		return nil, err
	}
	// Absence, present-empty, whitespace and exact nonempty bytes are distinct. Only an unestablished value
	// may be adopted; neither this code nor the database can rotate an established token.
	storedToken := mergedAccount.GetStatus().GetExternalBillingAccountId()
	if strings.TrimSpace(storedToken) != "" && storedToken != resolution.Value {
		return nil, errAssignChanged
	}
	mergedAccount.GetStatus().SetExternalBillingAccountId(resolution.Value)
	if !proto.Equal(mergedAccount, latest.account) {
		if _, err = s.accounts.Update().SetObject(mergedAccount).Do(txCtx); err != nil {
			return nil, err
		}
	}
	if closeID != "" && latest.current.GetSpec().GetBillingAccount().GetId() == accountID {
		updated := proto.Clone(latest.current).(*privatev1.TenantBillingAssignment)
		updated.GetStatus().SetConnectionState(mapTenantBillingAssignmentConnectionState(state))
		updated.GetStatus().SetMessage(message)
		if proto.Equal(updated, latest.current) {
			return latest.current, nil
		}
		result, updateErr := s.assignments.Update().SetObject(updated).Do(txCtx)
		if updateErr != nil {
			return nil, updateErr
		}
		return result.GetObject(), nil
	}
	// The destination account is already locked, so its closed history cannot advance under us.
	accountFloor, err := database.BillingAccountHistoryFloor(txCtx, accountID)
	if err != nil {
		return nil, err
	}
	floor := latest.floor
	if accountFloor.After(floor) {
		floor = accountFloor
	}
	// The first interval cannot start before its owning tenant existed. This also covers a stored
	// creation timestamp ahead of the database clock, which a clock sample alone would precede.
	if created := latest.tenant.GetMetadata().GetCreationTimestamp(); created != nil && created.AsTime().After(floor) {
		floor = created.AsTime()
	}
	at, err := database.BillingFinalizationTime(txCtx, floor)
	if err != nil {
		return nil, err
	}
	if closeID != "" {
		closed := proto.Clone(latest.current).(*privatev1.TenantBillingAssignment)
		closed.GetStatus().SetEffectiveToTimestamp(timestamppb.New(at))
		if _, err = s.assignments.Update().SetObject(closed).Do(txCtx); err != nil {
			var missing *dao.ErrNotFound
			if errors.As(err, &missing) {
				return nil, errAssignChanged
			}
			return nil, err
		}
	}
	candidate := proto.Clone(in.Object).(*privatev1.TenantBillingAssignment)
	candidate.SetId(uuid.New())
	candidate.GetMetadata().SetCreator(in.Creator)
	candidate.GetMetadata().ClearCreationTimestamp()
	candidate.GetMetadata().ClearDeletionTimestamp()
	candidate.GetMetadata().SetVersion(0)
	candidate.SetStatus(privatev1.TenantBillingAssignmentStatus_builder{
		ConnectionState: mapTenantBillingAssignmentConnectionState(state), Message: message,
		EffectiveFromTimestamp: timestamppb.New(at),
	}.Build())
	created, err := s.assignments.Create().SetObject(candidate).Do(txCtx)
	if err != nil {
		return nil, err
	}
	return created.GetObject(), nil
}

// canonicalAssignment owns a detached candidate and resolves only live write targets. Historical Get/List never
// call it: canonical references already stored in intervals remain readable after their targets are removed.
func (s *PrivateTenantBillingAssignmentsServer) canonicalAssignment(ctx context.Context,
	object *privatev1.TenantBillingAssignment) (*privatev1.TenantBillingAssignment, error) {
	object = proto.Clone(object).(*privatev1.TenantBillingAssignment)
	ref := object.GetSpec().GetTenant()
	if ref == nil || (ref.GetId() == "" && ref.GetName() == "") {
		return nil, status.Error(codes.InvalidArgument, "spec.tenant is required")
	}
	id := ref.GetId()
	if id == "" {
		page, err := s.tenants.List().SetLimit(2).SetFilter(fmt.Sprintf("this.metadata.name == %q", ref.GetName())).Do(ctx)
		if err != nil {
			return nil, err
		}
		items := page.GetItems()
		if page.GetTotal() != 1 || len(items) != 1 {
			return nil, status.Error(codes.InvalidArgument, "spec.tenant does not reference an existing tenant")
		}
		id = items[0].GetId()
	}
	if !auth.SubjectFromContext(ctx).Tenants.Contains(id) {
		return nil, status.Error(codes.PermissionDenied, "tenant is outside the authorized billing assignment scope")
	}
	if id == auth.SystemTenant || id == auth.SharedTenant {
		return nil, status.Error(codes.InvalidArgument, "reserved tenants cannot have billing assignments")
	}
	tenant, err := s.tenants.Get().SetId(id).Do(ctx)
	if err != nil {
		var missing *dao.ErrNotFound
		if errors.As(err, &missing) {
			return nil, status.Error(codes.InvalidArgument, "spec.tenant does not reference an existing tenant")
		}
		return nil, err
	}
	if !billingResourceLive(tenant.GetObject().GetMetadata()) {
		return nil, status.Error(codes.InvalidArgument, "spec.tenant does not reference an existing tenant")
	}
	if ref.GetName() != "" && ref.GetName() != tenant.GetObject().GetMetadata().GetName() {
		return nil, status.Error(codes.InvalidArgument, "spec.tenant id and name contradict")
	}
	canonicalizeResourceReference(ref, tenant.GetObject())
	metadata := object.GetMetadata()
	if metadata == nil {
		return nil, status.Error(codes.InvalidArgument, "metadata is required")
	}
	if metadata.GetProject() != "" || (metadata.GetTenant() != "" && metadata.GetTenant() != id) {
		return nil, status.Error(codes.InvalidArgument, "assignment ownership must match its nonproject tenant")
	}
	metadata.SetTenant(id)
	annotations := metadata.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	if owner, exists := annotations["osac.openshift.io/tenant"]; exists && owner != id {
		return nil, status.Error(codes.InvalidArgument, "assignment tenant annotation contradicts spec.tenant")
	}
	annotations["osac.openshift.io/tenant"] = id
	metadata.SetAnnotations(annotations)
	accountRef := object.GetSpec().GetBillingAccount()
	if accountRef == nil {
		return nil, status.Error(codes.InvalidArgument, "spec.billing_account is required")
	}
	account, err := resolveResourceInScope(ctx, s.accounts, referenceScope{tenant: auth.SystemTenant},
		accountRef.GetId(), accountRef.GetName(), "billing account", "", codes.InvalidArgument)
	if err != nil {
		return nil, err
	}
	if !billingResourceLive(account.GetMetadata()) {
		return nil, status.Error(codes.InvalidArgument, "billing account is deleted")
	}
	canonicalizeResourceReference(accountRef, account)
	if err = s.validator.Validate(object); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	return object, nil
}

func billingResourceLive(metadata *privatev1.Metadata) bool {
	return metadata != nil && (metadata.GetDeletionTimestamp() == nil || metadata.GetDeletionTimestamp().AsTime().Equal(time.Unix(0, 0)))
}

func validateBillingAssignmentIdentity(current, request *privatev1.TenantBillingAssignment) error {
	if ref := request.GetSpec().GetTenant(); ref != nil &&
		((ref.GetId() != "" && ref.GetId() != current.GetSpec().GetTenant().GetId()) ||
			(ref.GetName() != "" && ref.GetName() != current.GetSpec().GetTenant().GetName())) {
		return status.Error(codes.InvalidArgument, "spec.tenant is immutable")
	}
	before, after := current.GetMetadata(), request.GetMetadata()
	if after != nil && ((after.GetName() != "" && after.GetName() != before.GetName()) ||
		(after.GetTenant() != "" && after.GetTenant() != before.GetTenant()) ||
		after.GetProject() != before.GetProject() ||
		(after.GetCreator() != "" && after.GetCreator() != before.GetCreator()) ||
		(after.GetCreationTimestamp() != nil && !proto.Equal(after.GetCreationTimestamp(), before.GetCreationTimestamp())) ||
		(after.GetDeletionTimestamp() != nil && !proto.Equal(after.GetDeletionTimestamp(), before.GetDeletionTimestamp()))) {
		return status.Error(codes.InvalidArgument, "assignment metadata identity is immutable")
	}
	return nil
}

func sameBillingAccountIdentity(a, b *privatev1.BillingAccount) bool {
	if a == nil || b == nil {
		return a == b
	}
	x, y := a.GetStatus(), b.GetStatus()
	return a.GetId() == b.GetId() && a.GetMetadata().GetName() == b.GetMetadata().GetName() &&
		proto.Equal(a.GetMetadata().GetCreationTimestamp(), b.GetMetadata().GetCreationTimestamp()) &&
		x.GetProviderKey() == y.GetProviderKey() && x.GetProviderAccountReference() == y.GetProviderAccountReference() &&
		x.HasExternalBillingAccountId() == y.HasExternalBillingAccountId() &&
		x.GetExternalBillingAccountId() == y.GetExternalBillingAccountId()
}

func sameBillingAssignmentSelection(current *privatev1.TenantBillingAssignment, in tenantBillingAssignmentAssignInput,
	expected *tenantBillingAssignmentSnapshot) bool {
	return current.GetSpec().GetTenant().GetId() == in.TenantID &&
		current.GetMetadata().GetVersion() == in.Object.GetMetadata().GetVersion() &&
		proto.Equal(current.GetStatus().GetEffectiveFromTimestamp(), in.Object.GetStatus().GetEffectiveFromTimestamp()) &&
		(expected == nil || current.GetSpec().GetBillingAccount().GetId() == expected.current.GetSpec().GetBillingAccount().GetId())
}

func billingSnapshotLockError(err error) error {
	var missing *dao.ErrNotFound
	if errors.As(err, &missing) {
		return errAssignChanged
	}
	return err
}

func sameBillingAssignmentInputs(a, b tenantBillingAssignmentSnapshot) bool {
	return a.tenant.GetId() == b.tenant.GetId() &&
		proto.Equal(a.tenant.GetMetadata().GetCreationTimestamp(), b.tenant.GetMetadata().GetCreationTimestamp()) &&
		sameBillingAccountIdentity(a.account, b.account) && sameBillingAccountIdentity(a.previous, b.previous) &&
		proto.Equal(a.current, b.current) && a.floor.Equal(b.floor)
}

func assignmentAccountState(account *privatev1.BillingAccount) billing.AccountState {
	switch account.GetStatus().GetState() {
	case privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE:
		return billing.AccountActive
	case privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_INACTIVE:
		return billing.AccountInactive
	default:
		return billing.AccountUnknown
	}
}

// Finite read visibility is enforced by the DAO. Visibility cannot represent exclusions: all read entry points
// MUST use listAssignments/getAssignment, which add their SQL constraint. Mutation paths separately check
// Contains on the stored target. Internal binding checks/tenant resolution intentionally use complete inventory;
// they cannot grant method or object access.
type billingAssignmentTenancy struct{ inventory bool }

func (billingAssignmentTenancy) DetermineAssignableTenants(context.Context) (collections.Set[string], error) {
	return auth.AllTenants, nil
}
func (billingAssignmentTenancy) DetermineDefaultTenant(context.Context) (string, error) {
	return "", nil
}
func (t billingAssignmentTenancy) DetermineVisibility(ctx context.Context) (*auth.Visibility, error) {
	if t.inventory {
		return auth.TotalVisibility(), nil
	}
	set := auth.SubjectFromContext(ctx).Tenants
	if !set.Finite() {
		// Coarse DAO visibility only. The read helpers enforce exclusions without enumerating live tenants
		// or changing the subject; writes retain their existing stored-target Contains authorization.
		return auth.TotalVisibility(), nil
	}
	return auth.NewVisibility().AddVisibleTenants(set.Inclusions()...).Build()
}

// mapTenantBillingAssignmentConnectionState maps a connection_state value to its proto enum, returning
// UNSPECIFIED otherwise.
func mapTenantBillingAssignmentConnectionState(state string) privatev1.TenantBillingAssignmentConnectionState {
	switch state {
	case tbaStatePending:
		return privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING
	case tbaStateConnected:
		return privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED
	case tbaStateFailed:
		return privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_FAILED
	default:
		return privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_UNSPECIFIED
	}
}

// failedPreconditionWithReason builds a FAILED_PRECONDITION status carrying reason in its ErrorInfo detail.
func failedPreconditionWithReason(reason, format string, args ...any) error {
	return billingFailure(codes.FailedPrecondition, fmt.Sprintf(format, args...), reason)
}

// mapBillingProviderBuildError maps a configured-but-unbuildable provider (e.g. an unreadable credential Secret)
// to UNAVAILABLE, never a silently empty result.
func mapBillingProviderBuildError(err error) error {
	var provErr *billing.Error
	if errors.As(err, &provErr) {
		return mapClassifiedBillingError(provErr, string(provErr.Reason))
	}
	return billingFailure(codes.Unavailable, "billing provider account API is unavailable", billingProviderUnavailableReason)
}

// mapTenantBillingAssignmentError maps an assign error to a gRPC status: a correlation-resolution error, the DAO's
// correlation-conflict sentinel, a classified provider error, a race on the 1:1 partial unique indexes, a missing
// tenant, or an infrastructure error. The fallback logs the cause and returns a fixed message, because pgx error
// strings can leak constraint and schema detail.
func (s *PrivateTenantBillingAssignmentsServer) mapTenantBillingAssignmentError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return status.FromContextError(ctx.Err()).Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}
	var deadlock *dao.ErrDeadlock
	var pgErr *pgconn.PgError
	if errors.As(err, &deadlock) || (errors.As(err, &pgErr) &&
		(pgErr.Code == pgerrcode.DeadlockDetected || pgErr.Code == pgerrcode.SerializationFailure)) {
		return billingFailure(codes.Aborted, "billing operation conflicted; retry", billingOperationRetryReason)
	}
	if errors.Is(err, errAssignChanged) {
		return billingFailure(codes.Aborted, "billing operation inputs changed; retry",
			billingOperationRetryReason)
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	if errors.Is(err, errAssignBusy) {
		return status.Error(codes.ResourceExhausted, errAssignBusy.Error())
	}
	var resErr *billing.CorrelationResolutionError
	if errors.As(err, &resErr) {
		if resErr.Reason == billing.ReasonAccountAmbiguous {
			return failedPreconditionWithReason(resErr.Reason,
				"billing provider external ID is ambiguous; verify that only the selected provider account reports it")
		}
		return failedPreconditionWithReason(resErr.Reason, "billing correlation resolution failed: %s", resErr.Reason)
	}
	if errors.Is(err, database.ErrBillingAccountExternalIDConflict) {
		return failedPreconditionWithReason(billing.ReasonAccountCorrelationConflict,
			"external billing account id is already in use by another account")
	}
	var conflict *dao.ErrAlreadyExists
	constraint := ""
	if errors.As(err, &conflict) {
		constraint = conflict.ConstraintName
		// GenericDAO intentionally keeps its ordinary error contract; it does not expose PostgreSQL constraint names.
		// During assignment, the only unique billing-account UPDATE is adoption of the external correlation token.
		if constraint == "" && conflict.Kind == "billing account" {
			return failedPreconditionWithReason(billing.ReasonAccountCorrelationConflict,
				"external billing account id is already in use by another account")
		}
	}
	if errors.As(err, &pgErr) {
		constraint = pgErr.ConstraintName
	}
	if constraint != "" {
		switch constraint {
		case "billing_accounts_unique_correlation_id":
			return failedPreconditionWithReason(billing.ReasonAccountCorrelationConflict, "external billing account id is already in use by another account")
		case "tenant_billing_assignments_unique_active_tenant":
			return failedPreconditionWithReason(tbaTenantAlreadyBoundReason,
				"tenant already has an active billing assignment")
		case "tenant_billing_assignments_unique_active_account":
			return failedPreconditionWithReason(tbaAccountAlreadyBoundReason,
				"billing account is already linked to a tenant")
		case "tenant_billing_assignments_tenant_id_fkey":
			return status.Errorf(codes.InvalidArgument, "spec.tenant does not reference an existing tenant")
		}
	}
	var provErr *billing.Error
	if errors.As(err, &provErr) {
		return mapClassifiedBillingError(provErr, string(provErr.Reason))
	}
	s.logger.ErrorContext(ctx, "Failed to assign billing account", slog.Any("error", err))
	return status.Error(codes.Internal, "failed to assign billing account")
}
