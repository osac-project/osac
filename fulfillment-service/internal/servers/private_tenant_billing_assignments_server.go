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
	"fmt"
	"log/slog"
	"time"

	"buf.build/go/protovalidate"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// Stable connection_state column values; see TenantBillingAssignmentConnectionState in the proto.
const (
	tbaStatePending   = "PENDING"
	tbaStateConnected = "CONNECTED"
	tbaStateFailed    = "FAILED"
)

// tbaAccountAlreadyBoundReason and tbaTenantAlreadyBoundReason are the stable reasons Create/Update report when
// the account or the tenant already has an active assignment. Neither appears anywhere in the OSAC-3784 design
// document; they come from this story's acceptance criteria ("ACCOUNT_ALREADY_BOUND (or the tenant-side
// equivalent)"), which name only the account-side reason. tbaTenantAlreadyBoundReason is this implementation's
// choice for the unnamed tenant-side equivalent.
const (
	tbaAccountAlreadyBoundReason = "ACCOUNT_ALREADY_BOUND"
	tbaTenantAlreadyBoundReason  = "TENANT_ALREADY_BOUND"
	tbaAccountInactiveReason     = "ACCOUNT_INACTIVE"
	tbaAccountStateUnknownReason = "ACCOUNT_STATE_UNKNOWN"

	// billingOperationRetryReason reports a retryable conflict on a billing operation lease (the concurrency guard a
	// concurrent assign or discovery holds); the caller retries, it is not a permanent failure.
	billingOperationRetryReason = "BILLING_OPERATION_RETRY"
)

// Provider I/O is deadline-bounded and runs without a database transaction or
// pooled connection. OSAC-6894 owns wiring deployment tuning to this default.
const defaultAssignProviderDeadline = time.Minute

// maxConcurrentAssigns bounds provider load. Durable resource guards are acquired
// before this semaphore so same-account/tenant contenders never occupy its slots.
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
	logger    *slog.Logger
	provider  *billing.Provider
	validator protovalidate.Validator
	// assignProviderDeadline bounds provider I/O, not an open transaction.
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
	result = &PrivateTenantBillingAssignmentsServer{
		logger:                 b.logger,
		provider:               b.provider,
		validator:              validator,
		assignProviderDeadline: defaultAssignProviderDeadline,
		assignSem:              make(chan struct{}, maxConcurrentAssigns),
	}
	return
}

// List returns every assignment matching the request's filter/order/pagination. Only Cloud Provider Admins reach
// this method (design.md §4.3/§4.7; enforced by authz.rego's is_admin catch-all, no tenant self-service path).
func (s *PrivateTenantBillingAssignmentsServer) List(ctx context.Context,
	request *privatev1.TenantBillingAssignmentsListRequest) (
	response *privatev1.TenantBillingAssignmentsListResponse, err error) {
	if _, err = requireAdapter(s.provider); err != nil {
		return nil, err
	}

	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.ReportError(&err)

	// An unset limit serves the DAO's default page; an explicit one is clamped to its maximum.
	rows, total, err := database.ListTenantBillingAssignments(
		ctx, tx, request.GetOffset(), request.GetLimit(), request.GetFilter(), request.GetOrder())
	if err != nil {
		var invalid *database.ErrInvalidFlatTableQuery
		if errors.As(err, &invalid) {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
		s.logger.ErrorContext(ctx, "Failed to list tenant billing assignments", slog.Any("error", err))
		return nil, status.Errorf(codes.Internal, "failed to list tenant billing assignments")
	}
	return buildTenantBillingAssignmentsListResponse(rows, total), nil
}

// Get returns one assignment interval. Only Cloud Provider Admins reach this method (design.md §4.3/§4.7;
// enforced by authz.rego's is_admin catch-all, no tenant self-service path).
func (s *PrivateTenantBillingAssignmentsServer) Get(ctx context.Context,
	request *privatev1.TenantBillingAssignmentsGetRequest) (
	response *privatev1.TenantBillingAssignmentsGetResponse, err error) {
	if _, err = requireAdapter(s.provider); err != nil {
		return nil, err
	}

	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.ReportError(&err)

	row, err := database.GetTenantBillingAssignment(ctx, tx, request.GetId())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, status.Errorf(codes.NotFound, "tenant billing assignment %q not found", request.GetId())
		}
		s.logger.ErrorContext(ctx, "Failed to get tenant billing assignment", slog.Any("error", err))
		return nil, status.Errorf(codes.Internal, "failed to get tenant billing assignment")
	}

	return privatev1.TenantBillingAssignmentsGetResponse_builder{
		Object: toTenantBillingAssignmentProto(row),
	}.Build(), nil
}

// Create assigns a tenant to a billing account for the first time.
func (s *PrivateTenantBillingAssignmentsServer) Create(ctx context.Context,
	request *privatev1.TenantBillingAssignmentsCreateRequest) (
	response *privatev1.TenantBillingAssignmentsCreateResponse, err error) {
	if _, err = requireAdapter(s.provider); err != nil {
		return nil, err
	}

	object := request.GetObject()
	if object.GetSpec() == nil {
		return nil, status.Errorf(codes.InvalidArgument, "spec is required")
	}
	if err = s.validator.Validate(object); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	tenantID := object.GetSpec().GetTenantId()
	accountID := object.GetSpec().GetBillingAccountId()

	row, err := s.assign(ctx, accountID, "", tenantBillingAssignmentAssignInput{
		Name: object.GetMetadata().GetName(), TenantID: tenantID, Creator: auth.SubjectFromContext(ctx).User,
	})
	if err != nil {
		return nil, err
	}

	return privatev1.TenantBillingAssignmentsCreateResponse_builder{
		Object: toTenantBillingAssignmentProto(row),
	}.Build(), nil
}

type tenantBillingAssignmentSnapshot struct {
	account database.BillingAccountForAssignment
	current database.TenantBillingAssignmentRow
}

// assignmentSnapshot is called in short preflight and final transactions. The
// final call locks authoritative rows against deletion/discovery updates until
// commit. It never calls a provider.
func (s *PrivateTenantBillingAssignmentsServer) assignmentSnapshot(
	ctx context.Context, accountID, closeID string, in tenantBillingAssignmentAssignInput, lock bool,
) (tenantBillingAssignmentSnapshot, error) {
	var snapshot tenantBillingAssignmentSnapshot
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return snapshot, err
	}
	if lock {
		var name string
		err = tx.QueryRow(ctx, `select name from tenants where name = $1
			and deletion_timestamp = 'epoch' for update`, in.TenantID).Scan(&name)
		if errors.Is(err, pgx.ErrNoRows) {
			return snapshot, status.Error(codes.InvalidArgument, "spec.tenant_id does not reference an existing tenant")
		}
		if err != nil {
			return snapshot, err
		}
		if err = database.LockBillingAccountForAssignment(ctx, tx, accountID); err != nil {
			return snapshot, err
		}
		var id string
		err = tx.QueryRow(ctx, `select id from billing_accounts where id = $1 for update`, accountID).Scan(&id)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return snapshot, err
		}
		if closeID != "" {
			err = tx.QueryRow(ctx, `select id from tenant_billing_assignments where id = $1 for update`, closeID).Scan(&id)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return snapshot, err
			}
		}
	}
	// The tenant FK alone cannot reject a deleted tenant: OSAC-6886's tombstone keeps the row live-but-marked
	// so assignment history stays queryable, and the FK stays satisfied. Reject it here, before any account or
	// provider work, with the same contract the FK violation reports for a tenant that never existed.
	live, err := database.TenantLiveForAssignment(ctx, tx, in.TenantID)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to read tenant", slog.Any("error", err))
		return snapshot, status.Errorf(codes.Internal, "failed to read tenant")
	}
	if !live {
		return snapshot,
			status.Errorf(codes.InvalidArgument, "spec.tenant_id does not reference an existing tenant")
	}
	account, err := database.GetBillingAccountForAssignment(ctx, tx, accountID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return snapshot, status.Errorf(codes.InvalidArgument,
				"billing account %q does not exist", accountID)
		}
		s.logger.ErrorContext(ctx, "Failed to read billing account", slog.Any("error", err))
		return snapshot, status.Errorf(codes.Internal, "failed to read billing account")
	}
	if err = requireActiveBillingAccount(account); err != nil {
		return snapshot, err
	}
	snapshot.account = account
	if closeID == "" {
		return snapshot, s.rejectIfAlreadyBound(ctx, tx, in.TenantID, accountID)
	}
	existing, err := s.currentAssignment(ctx, closeID)
	if err != nil {
		return snapshot, err
	}
	if existing.TenantID != in.TenantID {
		return snapshot, errAssignChanged
	}
	snapshot.current = existing
	if existing.BillingAccountID != accountID {
		return snapshot, s.rejectIfAccountAlreadyBound(ctx, tx, accountID)
	}
	return snapshot, nil
}

// Update replaces the tenant's active account, or fully revalidates the current
// one without opening a new billing interval.
func (s *PrivateTenantBillingAssignmentsServer) Update(ctx context.Context,
	request *privatev1.TenantBillingAssignmentsUpdateRequest) (
	response *privatev1.TenantBillingAssignmentsUpdateResponse, err error) {
	if _, err = requireAdapter(s.provider); err != nil {
		return nil, err
	}

	object := request.GetObject()
	if object.GetSpec() == nil {
		return nil, status.Errorf(codes.InvalidArgument, "spec is required")
	}
	if err = s.validator.Validate(object); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	id := object.GetId()
	if id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "id is required")
	}

	existing, err := database.WithNewTx(ctx, func(txCtx context.Context) (database.TenantBillingAssignmentRow, error) {
		return s.currentAssignment(txCtx, id)
	})
	if err != nil {
		return nil, s.mapTenantBillingAssignmentError(ctx, err)
	}

	// spec.tenant_id is a required, non-empty field (enforced by validator.Validate above), so this is a real
	// mismatch whenever it triggers, never an "omitted" case.
	if object.GetSpec().GetTenantId() != existing.TenantID {
		return nil, status.Errorf(codes.InvalidArgument, "spec.tenant_id is immutable and cannot be changed by Update")
	}

	accountID := object.GetSpec().GetBillingAccountId()

	row, err := s.assign(ctx, accountID, existing.ID, tenantBillingAssignmentAssignInput{
		Name: existing.Name, TenantID: existing.TenantID, Creator: existing.Creator,
	})
	if err != nil {
		return nil, err
	}

	return privatev1.TenantBillingAssignmentsUpdateResponse_builder{
		Object: toTenantBillingAssignmentProto(row),
	}.Build(), nil
}

func (s *PrivateTenantBillingAssignmentsServer) currentAssignment(
	ctx context.Context, id string,
) (database.TenantBillingAssignmentRow, error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return database.TenantBillingAssignmentRow{}, err
	}
	row, err := database.GetTenantBillingAssignment(ctx, tx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, status.Errorf(codes.NotFound, "tenant billing assignment %q not found", id)
	}
	if err != nil {
		return row, err
	}
	if row.EffectiveTo != nil {
		return row, status.Errorf(codes.FailedPrecondition,
			"tenant billing assignment %q is no longer the current interval", id)
	}
	return row, nil
}

// Signal synchronously revalidates only a current assignment. Authorization is
// enforced by the controller/admin-only private RPC policy, not in this handler.
func (s *PrivateTenantBillingAssignmentsServer) Signal(ctx context.Context,
	request *privatev1.TenantBillingAssignmentsSignalRequest,
) (*privatev1.TenantBillingAssignmentsSignalResponse, error) {
	if _, err := requireAdapter(s.provider); err != nil {
		return nil, err
	}
	if request.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	existing, err := database.WithNewTx(ctx, func(txCtx context.Context) (database.TenantBillingAssignmentRow, error) {
		return s.currentAssignment(txCtx, request.GetId())
	})
	if err != nil {
		return nil, s.mapTenantBillingAssignmentError(ctx, err)
	}
	_, err = s.assign(ctx, existing.BillingAccountID, existing.ID, tenantBillingAssignmentAssignInput{
		Name: existing.Name, TenantID: existing.TenantID, Creator: existing.Creator,
	})
	if err != nil {
		return nil, err
	}
	return &privatev1.TenantBillingAssignmentsSignalResponse{}, nil
}

// rejectIfAlreadyBound fails with ACCOUNT_ALREADY_BOUND or TENANT_ALREADY_BOUND when tenantID or accountID
// already has an active assignment. Used only by Create: Update's "account changing" path only needs the
// account-side check, since the tenant side is the assignment being updated.
func (s *PrivateTenantBillingAssignmentsServer) rejectIfAlreadyBound(
	ctx context.Context, tx database.Tx, tenantID, accountID string) error {
	if _, err := database.GetActiveTenantBillingAssignmentByTenant(ctx, tx, tenantID); err == nil {
		return failedPreconditionWithReason(tbaTenantAlreadyBoundReason,
			"tenant %q already has an active billing assignment", tenantID)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		s.logger.ErrorContext(ctx, "Failed to check tenant billing assignment", slog.Any("error", err))
		return status.Errorf(codes.Internal, "failed to check tenant billing assignment")
	}
	return s.rejectIfAccountAlreadyBound(ctx, tx, accountID)
}

// rejectIfAccountAlreadyBound fails with ACCOUNT_ALREADY_BOUND when accountID already has an active assignment.
func (s *PrivateTenantBillingAssignmentsServer) rejectIfAccountAlreadyBound(
	ctx context.Context, tx database.Tx, accountID string) error {
	if _, err := database.GetActiveTenantBillingAssignmentByAccount(ctx, tx, accountID); err == nil {
		return failedPreconditionWithReason(tbaAccountAlreadyBoundReason,
			"billing account %q is already linked to a tenant", accountID)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		s.logger.ErrorContext(ctx, "Failed to check billing account assignment", slog.Any("error", err))
		return status.Errorf(codes.Internal, "failed to check billing account assignment")
	}
	return nil
}

// requireActiveBillingAccount fails with ACCOUNT_INACTIVE or ACCOUNT_STATE_UNKNOWN unless account.State is ACTIVE.
func requireActiveBillingAccount(account database.BillingAccountForAssignment) error {
	switch billing.AccountState(account.State) {
	case billing.AccountActive:
		return nil
	case billing.AccountInactive:
		return failedPreconditionWithReason(tbaAccountInactiveReason, "billing account %q is inactive", account.ID)
	default:
		return failedPreconditionWithReason(tbaAccountStateUnknownReason,
			"billing account %q has an unknown state", account.ID)
	}
}

// tenantBillingAssignmentAssignInput carries the fields of the assignment interval assign is about to open.
type tenantBillingAssignmentAssignInput struct {
	Name     string
	TenantID string
	Creator  string
}

// assign holds committed expiring guards, never a transaction, across provider
// I/O. All local reads and writes use independent short transactions.
func (s *PrivateTenantBillingAssignmentsServer) assign(
	ctx context.Context, accountID, closeID string, in tenantBillingAssignmentAssignInput,
) (row database.TenantBillingAssignmentRow, err error) {
	defer func() {
		if err != nil {
			err = s.mapTenantBillingAssignmentError(ctx, err)
		}
	}()
	// Do not silently nest underneath an ambient transaction: WithNewTx shadows
	// but cannot release its connection. Startup config supplies manager-only ctx.
	if _, txErr := database.TxFromContext(ctx); txErr == nil {
		return row, errors.New("billing provider operation requires a manager-only context")
	}
	operationCtx, cancelOperation := context.WithTimeout(ctx, s.assignProviderDeadline+10*time.Second)
	defer cancelOperation()
	lease, err := database.AcquireBillingOperation(operationCtx, s.assignProviderDeadline+10*time.Second,
		database.BillingOperationKey("account", accountID), database.BillingOperationKey("tenant", in.TenantID))
	if err != nil {
		return row, err
	}
	defer func() {
		if releaseErr := lease.Release(operationCtx); releaseErr != nil {
			s.logger.WarnContext(ctx, "Failed to release billing operation guard", slog.Any("error", releaseErr))
		}
	}()
	select {
	case s.assignSem <- struct{}{}:
		defer func() { <-s.assignSem }()
	default:
		return database.TenantBillingAssignmentRow{}, errAssignBusy
	}

	snapshot, err := database.WithNewTx(operationCtx, func(txCtx context.Context) (tenantBillingAssignmentSnapshot, error) {
		return s.assignmentSnapshot(txCtx, accountID, closeID, in, false)
	})
	if err != nil {
		return row, err
	}
	account := snapshot.account
	adapter, err := s.provider.Adapter()
	if err != nil {
		return row, mapBillingProviderBuildError(err)
	}
	ref := billing.NewAccountRef(account.ProviderAccountReference)
	providerCtx, cancel := context.WithTimeout(operationCtx, s.assignProviderDeadline)
	resolution, err := billing.ResolveAccountCorrelationID(providerCtx, billing.AccountCorrelationInput{
		Adapter: adapter, AccountID: account.ID, Ref: ref, LocalValue: account.CorrelationID,
		LocalDuplicate: func(dupCtx context.Context, value, excludeAccountID string) (bool, error) {
			if reserveErr := lease.Reserve(dupCtx, database.BillingOperationKey("correlation", value)); reserveErr != nil {
				return false, reserveErr
			}
			return database.WithNewTx(dupCtx, func(txCtx context.Context) (bool, error) {
				tx, _ := database.TxFromContext(txCtx)
				return database.BillingAccountCorrelationConflict(txCtx, tx, value, excludeAccountID)
			})
		},
	})
	cancel()
	if err != nil {
		return database.TenantBillingAssignmentRow{}, err
	}
	// The already-consistent resolver branch performs no write and may skip
	// LocalDuplicate. Reserve that successful value too before local adoption.
	if err := lease.Reserve(operationCtx, database.BillingOperationKey("correlation", resolution.Value)); err != nil {
		return row, err
	}

	// Derive the connection-state count from the resolver's bounded uniqueness
	// scan: ProviderMatches counted accounts carrying the resolved value before any
	// write, and a successful write set the value on exactly this account, so the current count is
	// ProviderMatches plus one when the resolver wrote. This is the same verdict a fresh post-resolution
	// listing would return, absent out-of-band provider changes during this operation.
	matches := resolution.ProviderMatches
	if resolution.ProviderWritten {
		matches++
	}

	var state, message string
	switch matches {
	case 1:
		state = tbaStateConnected
	case 0:
		state = tbaStatePending
		if resolution.ProviderWriteSkippedReason != "" {
			message = resolution.ProviderWriteSkippedReason +
				": set externalId on the account in the provider to the correlation_id shown by BillingAccounts.List"
		} else {
			message = "no provider account currently carries the resolved correlation id; set it on the " +
				"account in the provider to the value shown by BillingAccounts.List"
		}
	default:
		return database.TenantBillingAssignmentRow{}, &billing.CorrelationResolutionError{
			Reason: billing.ReasonAccountAmbiguous,
		}
	}

	return database.WithNewTx(operationCtx, func(txCtx context.Context) (database.TenantBillingAssignmentRow, error) {
		tx, _ := database.TxFromContext(txCtx)
		if err := lease.Check(txCtx, tx); err != nil {
			return row, err
		}
		latest, err := s.assignmentSnapshot(txCtx, accountID, closeID, in, true)
		if err != nil {
			return row, err
		}
		if latest != snapshot {
			return row, errAssignChanged
		}
		// Recheck expiry after row-lock waits. These leases fence only this local
		// commit, not remote writes; provider changes may survive a failed commit.
		if err := lease.Check(txCtx, tx); err != nil {
			return row, err
		}
		if err := database.SetBillingAccountCorrelationID(txCtx, tx, account.ID, resolution.Value); err != nil {
			return row, err
		}
		if closeID != "" && snapshot.current.BillingAccountID == accountID {
			_, err = tx.Exec(txCtx, `update tenant_billing_assignments set connection_state = $2, message = $3
				where id = $1 and effective_to is null`, closeID, state, message)
			if err != nil {
				return row, err
			}
			return database.GetTenantBillingAssignment(txCtx, tx, closeID)
		}
		newID, err := database.CloseAndInsertTenantBillingAssignment(txCtx, tx, closeID,
			database.TenantBillingAssignmentInsert{
				ID: uuid.New(), Name: in.Name, TenantID: in.TenantID, BillingAccountID: account.ID,
				ConnectionState: state, Message: message, Creator: in.Creator,
			})
		if err != nil {
			return row, err
		}
		return database.GetTenantBillingAssignment(txCtx, tx, newID)
	})
}

// buildTenantBillingAssignmentsListResponse converts rows to their proto form and wraps them with the page total.
func buildTenantBillingAssignmentsListResponse(
	rows []database.TenantBillingAssignmentRow, total int32) *privatev1.TenantBillingAssignmentsListResponse {
	items := make([]*privatev1.TenantBillingAssignment, 0, len(rows))
	for _, row := range rows {
		items = append(items, toTenantBillingAssignmentProto(row))
	}
	return privatev1.TenantBillingAssignmentsListResponse_builder{
		Size:  int32(len(items)), // #nosec G115 -- bounded by page size
		Total: total,
		Items: items,
	}.Build()
}

// toTenantBillingAssignmentProto converts a database row into its private proto representation.
func toTenantBillingAssignmentProto(row database.TenantBillingAssignmentRow) *privatev1.TenantBillingAssignment {
	statusBuilder := privatev1.TenantBillingAssignmentStatus_builder{
		ConnectionState: mapTenantBillingAssignmentConnectionState(row.ConnectionState),
		EffectiveFrom:   timestamppb.New(row.EffectiveFrom),
		Message:         row.Message,
	}
	if row.EffectiveTo != nil {
		statusBuilder.EffectiveTo = timestamppb.New(*row.EffectiveTo)
	}
	return privatev1.TenantBillingAssignment_builder{
		Id: row.ID,
		Metadata: privatev1.Metadata_builder{
			Name:   row.Name,
			Tenant: row.TenantID,
		}.Build(),
		Spec: privatev1.TenantBillingAssignmentSpec_builder{
			TenantId:         row.TenantID,
			BillingAccountId: row.BillingAccountID,
		}.Build(),
		Status: statusBuilder.Build(),
	}.Build()
}

// mapTenantBillingAssignmentConnectionState maps a connection_state column value to its proto enum, returning
// UNSPECIFIED for anything else.
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

// failedPreconditionWithReason builds a FAILED_PRECONDITION status carrying reason in its ErrorInfo detail, the
// shape every billing failure in this story uses.
func failedPreconditionWithReason(reason, format string, args ...any) error {
	return billingFailure(codes.FailedPrecondition, fmt.Sprintf(format, args...), reason)
}

// mapBillingProviderBuildError maps a configured-but-unbuildable provider (e.g. an unreadable credential Secret)
// the same way private_billing_accounts_server.go maps a provider that cannot be reached: UNAVAILABLE, never a
// silently empty result.
func mapBillingProviderBuildError(err error) error {
	var provErr *billing.Error
	if errors.As(err, &provErr) {
		return mapClassifiedBillingError(provErr, string(provErr.Reason))
	}
	return billingFailure(codes.Unavailable, "billing provider account API is unavailable", billingProviderUnavailableReason)
}

// mapTenantBillingAssignmentError maps every error assign can return to a gRPC status: a resolution-specific
// *billing.CorrelationResolutionError, the DAO's correlation-conflict sentinel, a classified provider *billing.Error,
// a race on the 1:1 partial unique indexes (the early rejectIfAlreadyBound checks are a fast path, not the guard;
// a concurrent request can still lose the race at this final insert), a reference to a tenant that does not exist,
// or an infrastructure error. The infrastructure fallback logs the cause server-side and returns a fixed message:
// pgx error strings can name constraints and schema objects, so echoing the raw error would leak database detail.
func (s *PrivateTenantBillingAssignmentsServer) mapTenantBillingAssignmentError(ctx context.Context, err error) error {
	if errors.Is(err, database.ErrBillingOperationBusy) || errors.Is(err, database.ErrBillingOperationExpired) ||
		errors.Is(err, errAssignChanged) {
		return billingFailure(codes.Aborted, "billing operation conflicted or expired; retry",
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
		return failedPreconditionWithReason(resErr.Reason, "billing correlation resolution failed: %s", resErr.Reason)
	}
	if errors.Is(err, database.ErrBillingAccountCorrelationConflict) {
		return failedPreconditionWithReason(billing.ReasonAccountCorrelationConflict,
			"billing account correlation id is already in use by another account")
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.ConstraintName {
		case "tenant_billing_assignments_unique_active_tenant":
			return failedPreconditionWithReason(tbaTenantAlreadyBoundReason,
				"tenant already has an active billing assignment")
		case "tenant_billing_assignments_unique_active_account":
			return failedPreconditionWithReason(tbaAccountAlreadyBoundReason,
				"billing account is already linked to a tenant")
		case "tenant_billing_assignments_tenant_id_fkey":
			return status.Errorf(codes.InvalidArgument, "spec.tenant_id does not reference an existing tenant")
		}
	}
	var provErr *billing.Error
	if errors.As(err, &provErr) {
		return mapClassifiedBillingError(provErr, string(provErr.Reason))
	}
	s.logger.ErrorContext(ctx, "Failed to assign billing account", slog.Any("error", err))
	return status.Error(codes.Internal, "failed to assign billing account")
}
