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
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgerrcode"
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

// billingAccountsDiscoverPageSize is the page size discovery requests from the provider on every call.
const billingAccountsDiscoverPageSize = 20

// billingAccountsDiscoverFreshness is how long a successful discovery serves as fresh. Within the window List
// returns the stored page without re-reading the provider, so polling cannot multiply provider load. This is a
// per-process default; deployment tuning is wired separately.
const billingAccountsDiscoverFreshness = 30 * time.Second

// defaultDiscoverDeadline bounds one discovery walk. The M360 transport already cuts off a hanging provider on the
// first page, so this covers a slow-but-succeeding provider whose pages could stretch the walk for minutes. Two
// minutes fits a healthy 50-page walk with margin. This is a per-process default; deployment tuning is wired
// separately.
const defaultDiscoverDeadline = 2 * time.Minute

// defaultDiscoverFailureBackoff is how long a failed discovery suppresses the next attempt: a List inside the
// window fails fast as UNAVAILABLE instead of stacking another walk per call. Only a failed walk opens this
// window; a successful one opens the freshness window instead. This is a per-process default; deployment tuning
// is wired separately.
const defaultDiscoverFailureBackoff = 10 * time.Second

// errDiscoveryInProgress reports that another List's discovery walk is still running; this List fails fast as
// UNAVAILABLE.
var errDiscoveryInProgress = errors.New("billing account discovery is already in progress")

// errDiscoveryBackoff reports that a recent discovery failed and the backoff window is still open; this List
// fails fast as UNAVAILABLE.
var errDiscoveryBackoff = errors.New("billing account discovery failed recently")

// PrivateBillingAccountsServerBuilder builds a PrivateBillingAccountsServer.
type PrivateBillingAccountsServerBuilder struct {
	logger   *slog.Logger
	provider *billing.Provider
}

var _ privatev1.BillingAccountsServer = (*PrivateBillingAccountsServer)(nil)

// PrivateBillingAccountsServer discovers accounts from the billing provider, upserts them, and serves the stored
// page.
type PrivateBillingAccountsServer struct {
	privatev1.UnimplementedBillingAccountsServer
	logger   *slog.Logger
	provider *billing.Provider
	dao      *dao.GenericDAO[*privatev1.BillingAccount]
	// discoverMu guards the discovery timestamps and in-flight flag, and serializes the decision to start a walk,
	// so at most one walk runs at a time.
	discoverMu sync.Mutex
	// lastDiscovery is when the last successful discovery started the current freshness window.
	lastDiscovery time.Time
	// lastDiscoveryFailure is when the last failed walk ended; the failure backoff window runs from it.
	lastDiscoveryFailure time.Time
	// discoverInFlight reports whether a walk is currently running.
	discoverInFlight bool
	// discoverDeadline bounds one walk; discoverFailureBackoff is how long a failed walk suppresses the next
	// attempt. Both are fields so the suite can shorten them.
	discoverDeadline       time.Duration
	discoverFailureBackoff time.Duration
}

func NewPrivateBillingAccountsServer() *PrivateBillingAccountsServerBuilder {
	return &PrivateBillingAccountsServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *PrivateBillingAccountsServerBuilder) SetLogger(value *slog.Logger) *PrivateBillingAccountsServerBuilder {
	b.logger = value
	return b
}

// SetProvider sets the billing provider. This is mandatory.
func (b *PrivateBillingAccountsServerBuilder) SetProvider(
	value *billing.Provider) *PrivateBillingAccountsServerBuilder {
	b.provider = value
	return b
}

// Build validates that SetLogger and SetProvider were both called and returns the configured server.
func (b *PrivateBillingAccountsServerBuilder) Build() (result *PrivateBillingAccountsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.provider == nil {
		err = errors.New("provider is mandatory")
		return
	}
	accountsDAO, err := dao.NewGenericDAO[*privatev1.BillingAccount]().
		SetLogger(b.logger).
		SetTableName("billing_accounts").
		SetFilterDesc((*publicv1.BillingAccount)(nil).ProtoReflect().Descriptor()).
		SetTenancyLogic(billingAccountsTenancy{}).
		Build()
	if err != nil {
		return nil, err
	}
	result = &PrivateBillingAccountsServer{
		logger: b.logger, provider: b.provider, discoverDeadline: defaultDiscoverDeadline,
		dao:                    accountsDAO,
		discoverFailureBackoff: defaultDiscoverFailureBackoff,
	}
	return
}

// List refreshes the stored accounts when the last successful discovery is older than
// billingAccountsDiscoverFreshness, then returns the stored page. With billing disabled it fails with
// FAILED_PRECONDITION/BILLING_NOT_CONFIGURED before any provider call. An unreachable provider fails the call with
// UNAVAILABLE rather than serving the stored record as current; a failed walk is retried after the backoff, and a
// List arriving during a walk or after the deadline fails fast instead of queueing.
//
// Discovery, storage, the requested page and public mapping share the ordinary request transaction. Provider reads
// finish before any business row locks; freshness is published only after the outer commit succeeds.
func (s *PrivateBillingAccountsServer) List(ctx context.Context,
	request *privatev1.BillingAccountsListRequest) (response *privatev1.BillingAccountsListResponse, err error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.ReportError(&err)
	if _, err = requireAdapter(s.provider); err != nil {
		return nil, err
	}
	if request.GetLimit() < 0 || request.GetOffset() < 0 {
		return nil, status.Error(codes.InvalidArgument, "limit and offset must be non-negative")
	}
	if err = validateBillingAccountOrder(request.GetOrder()); err != nil {
		return nil, err
	}

	refreshed, err := s.discoverSync(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "Billing account discovery failed", slog.Any("error", err))
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		return nil, mapBillingDiscoveryError(err)
	}

	// An unset limit serves the billing default; an explicit one is clamped to the DAO maximum.
	limit := request.GetLimit()
	if limit == 0 {
		limit = 100
	}
	page, err := s.dao.List().SetOffset(request.GetOffset()).SetLimit(limit).
		SetFilter(request.GetFilter()).SetOrder(request.GetOrder()).Do(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		var invalidFilter *dao.ErrInvalidFilter
		var invalid *dao.ErrValidation
		if errors.As(err, &invalid) || errors.As(err, &invalidFilter) {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
		s.logger.ErrorContext(ctx, "Failed to list billing accounts", slog.Any("error", err))
		return nil, status.Errorf(codes.Internal, "failed to list billing accounts")
	}

	response = privatev1.BillingAccountsListResponse_builder{
		Size:  page.GetSize(),
		Total: page.GetTotal(),
		Items: page.GetItems(),
	}.Build()
	if refreshed {
		tx.OnCommit(func() {
			s.discoverMu.Lock()
			s.lastDiscovery = time.Now()
			s.discoverMu.Unlock()
		})
	}
	return response, nil
}

// discoverSync refreshes the stored page when it is stale, running at most one walk at a time. A List that
// arrives during a walk fails with errDiscoveryInProgress; one inside the failure backoff fails with
// errDiscoveryBackoff. Only a walk whose store transaction committed durably opens the freshness window. All
// timestamps and the in-flight flag are guarded by discoverMu; the walk runs with the mutex released.
func (s *PrivateBillingAccountsServer) discoverSync(ctx context.Context) (refreshed bool, err error) {
	s.discoverMu.Lock()
	if time.Since(s.lastDiscovery) < billingAccountsDiscoverFreshness {
		s.discoverMu.Unlock()
		return false, nil
	}
	if time.Since(s.lastDiscoveryFailure) < s.discoverFailureBackoff {
		s.discoverMu.Unlock()
		return false, errDiscoveryBackoff
	}
	if s.discoverInFlight {
		s.discoverMu.Unlock()
		return false, errDiscoveryInProgress
	}
	s.discoverInFlight = true
	s.discoverMu.Unlock()
	defer func() {
		s.discoverMu.Lock()
		s.discoverInFlight = false
		s.discoverMu.Unlock()
	}()

	err = s.discoverFresh(ctx)
	if err != nil {
		// A caller that cancels its walk or hits its own deadline says nothing about the provider, so its failure
		// must not open the server-wide backoff. A walk cut off by the child discoverDeadline is still recorded,
		// because that child never cancels this context.
		var providerErr *billing.Error
		if ctx.Err() == nil && errors.As(err, &providerErr) {
			s.discoverMu.Lock()
			s.lastDiscoveryFailure = time.Now()
			s.discoverMu.Unlock()
		}
		return false, err
	}
	return true, nil
}

// discoverFresh builds the provider adapter and runs one discovery walk under s.discoverDeadline, in two phases.
// fetch reads every provider page before store acquires business locks. Both phases use the same ambient request
// transaction, and the deadline covers both phases.
func (s *PrivateBillingAccountsServer) discoverFresh(ctx context.Context) error {
	adapter, err := s.provider.Adapter()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, s.discoverDeadline)
	defer cancel()
	// The observation timestamp marks when this walk read the provider, not when its rows are stored.
	observedAt := time.Now().UTC()
	accounts, err := s.fetchAccounts(ctx, adapter)
	if err != nil {
		return err
	}
	return s.storeAccounts(ctx, accounts, observedAt)
}

// mapBillingDiscoveryError classifies a discovery failure: an unavailable or timed-out provider becomes
// UNAVAILABLE/BILLING_PROVIDER_UNAVAILABLE, while any other classified provider error surfaces its own reason as
// FAILED_PRECONDITION. Backoff and in-progress failures map to the same UNAVAILABLE.
func mapBillingDiscoveryError(err error) error {
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, "billing account discovery canceled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, "billing account discovery deadline exceeded")
	}
	if status.Code(err) == codes.Aborted {
		return status.Error(codes.Aborted, "billing account inventory changed; retry the request")
	}
	var deadlock *dao.ErrDeadlock
	var pgErr *pgconn.PgError
	if errors.As(err, &deadlock) || (errors.As(err, &pgErr) &&
		(pgErr.Code == pgerrcode.DeadlockDetected || pgErr.Code == pgerrcode.SerializationFailure)) {
		return status.Error(codes.Aborted, "billing account inventory changed; retry the request")
	}
	var providerErr *billing.Error
	if errors.As(err, &providerErr) {
		return mapClassifiedBillingError(providerErr, billingProviderUnavailableReason)
	}
	if errors.Is(err, errDiscoveryInProgress) || errors.Is(err, errDiscoveryBackoff) {
		return billingFailure(codes.Unavailable, "billing provider account API is unavailable",
			billingProviderUnavailableReason)
	}
	return status.Error(codes.Internal, "failed to store billing account inventory")
}

// mapBillingAccountState maps a billing_accounts.state value to its proto enum, returning UNKNOWN otherwise.
func mapBillingAccountState(state string) privatev1.BillingAccountState {
	switch billing.AccountState(state) {
	case billing.AccountActive:
		return privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE
	case billing.AccountInactive:
		return privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_INACTIVE
	default:
		return privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_UNKNOWN
	}
}

// fetchAccounts reads the provider's full account list into memory before taking business locks. The ordinary
// request may already hold a pooled connection. Memory stays bounded because the walk is bounded at most
// billing.MaxListPages pages of billingAccountsDiscoverPageSize accounts.
func (s *PrivateBillingAccountsServer) fetchAccounts(
	ctx context.Context, adapter billing.Adapter) ([]billing.Account, error) {
	var accounts []billing.Account
	err := billing.ForEachAccount(
		ctx, adapter, billing.AccountFilter{},
		billing.Page{Size: billingAccountsDiscoverPageSize}, func(account billing.Account) error {
			accounts = append(accounts, account)
			return nil
		})
	if err != nil {
		return nil, err
	}
	return accounts, nil
}

// storeAccounts owns the complete batch. Provider I/O has finished before the provider guard, active-system-tenant
// SHARE lock and fresh whole-provider union locks, in that order. The mutex above is only process-local admission;
// the transaction-scoped provider guard serializes independent instances and remains held outside savepoints.
func (s *PrivateBillingAccountsServer) storeAccounts(
	ctx context.Context, accounts []billing.Account, observedAt time.Time) (err error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return err
	}
	defer tx.ReportError(&err)
	providerKey := s.provider.Key()
	observations, err := billingAccountObservations(providerKey, accounts, observedAt)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`select pg_advisory_xact_lock(hashtextextended('osac/billing-discovery/' || $1,0))`, providerKey); err != nil {
		return err
	}
	var deleting bool
	if err := tx.QueryRow(ctx, `select deletion_timestamp <> 'epoch' from tenants where name = 'system' for share`).
		Scan(&deleting); err != nil {
		return err
	}
	if deleting {
		return errors.New("system tenant is being deleted")
	}
	rows, err := tx.Query(ctx, `select id from billing_accounts where provider_key = $1 order by id for update`, providerKey)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// Fully consume the union before any mutation. Get materializes canonical metadata/data without new locks.
	for _, id := range ids {
		stored, err := s.dao.Get().SetId(id).Do(ctx)
		if err != nil {
			return err
		}
		current := stored.GetObject()
		ref := current.GetStatus().GetProviderAccountReference()
		observed, seen := observations[ref]
		if !seen {
			observed = privatev1.BillingAccountStatus_builder{
				State:                     privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_UNKNOWN,
				StateObservationTimestamp: timestamppb.New(observedAt),
			}.Build()
		}
		delete(observations, ref)
		merged, fresh := mergeBillingAccountObservation(current, observed)
		if fresh {
			if observed.HasProviderExternalId() {
				merged.GetStatus().SetProviderExternalId(observed.GetProviderExternalId())
			} else {
				merged.GetStatus().ClearProviderExternalId()
			}
		}
		if !proto.Equal(current, merged) {
			if _, err := s.dao.Update().SetObject(merged).Do(ctx); err != nil {
				return err
			}
		}
	}
	// Go string ordering compares UTF-8 bytes, independently of database collation. New rows are private to
	// this transaction; every existing row was already locked above in SQL OSAC-id order.
	refs := make([]string, 0, len(observations))
	for ref := range observations {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	for _, ref := range refs {
		id := uuid.New()
		name, err := billingAccountName(id)
		if err != nil {
			return err
		}
		candidate := privatev1.BillingAccount_builder{
			Id: id, Metadata: privatev1.Metadata_builder{Name: name, Tenant: auth.SystemTenant, Creator: "system"}.Build(),
			Spec: &privatev1.BillingAccountSpec{}, Status: observations[ref],
		}.Build()
		if _, err := s.dao.Create().SetObject(candidate).Do(ctx); err != nil {
			var conflict *dao.ErrAlreadyExists
			if errors.As(err, &conflict) && conflict.ConstraintName == "billing_accounts_unique_provider_account" {
				// Never acquire a late winner lock: an unguarded writer is a protocol violation.
				return status.Error(codes.Aborted, "billing account inventory changed; retry the request")
			}
			return err
		}
	}
	return nil
}

// billingAccountObservations prepares and validates the bounded inventory before any SQL lock is taken.
func billingAccountObservations(providerKey string, accounts []billing.Account,
	observedAt time.Time) (map[string]*privatev1.BillingAccountStatus, error) {
	if len(accounts) > billing.MaxListPages*billingAccountsDiscoverPageSize {
		return nil, billing.NewError(billing.ReasonMalformed, "ListAccounts", false, 0)
	}
	observations := make(map[string]*privatev1.BillingAccountStatus, len(accounts))
	for _, account := range accounts {
		ref := account.Ref.ID()
		if _, duplicate := observations[ref]; duplicate || ref == "" {
			return nil, billing.NewError(billing.ReasonMalformed, "ListAccounts", false, 0)
		}
		observed := privatev1.BillingAccountStatus_builder{
			ProviderKey: providerKey, ProviderAccountReference: ref,
			DisplayName: billing.SanitizeProviderText(account.Name), BaseCurrency: validCurrency(account.BaseCurrency),
			State: mapBillingAccountState(string(account.State)), StateObservationTimestamp: timestamppb.New(observedAt),
		}.Build()
		// The adapter uses an empty scalar for absence. Preserve every other value exactly.
		if account.ExternalID != "" {
			observed.SetProviderExternalId(account.ExternalID)
		}
		observations[ref] = observed
	}
	return observations, nil
}

// billingAccountName never derives an identity from a mutable provider label/native reference.
func billingAccountName(id string) (string, error) {
	if id == "" {
		return "", errors.New("billing account id is mandatory")
	}
	digest := sha256.Sum256([]byte(id))
	return fmt.Sprintf("ba-%x", digest)[:63], nil
}

// mergeBillingAccountObservation is shared with assignment revalidation. It owns its result, preserves all
// metadata and immutable provider identity, and never persists or increments a version itself. Its second return
// value tells discovery whether it can apply inventory-only fields; assignment never writes those fields.
func mergeBillingAccountObservation(current *privatev1.BillingAccount,
	observed *privatev1.BillingAccountStatus) (merged *privatev1.BillingAccount, fresh bool) {
	merged = proto.Clone(current).(*privatev1.BillingAccount)
	if observed == nil || observed.GetStateObservationTimestamp() == nil {
		return merged, false
	}
	previous := current.GetStatus()
	if previous.GetStateObservationTimestamp() != nil && observed.GetStateObservationTimestamp().AsTime().
		Before(previous.GetStateObservationTimestamp().AsTime()) {
		return merged, false
	}
	if merged.GetStatus() == nil {
		merged.SetStatus(&privatev1.BillingAccountStatus{})
	}
	next := merged.GetStatus()
	next.SetState(observed.GetState())
	next.SetStateObservationTimestamp(proto.Clone(observed.GetStateObservationTimestamp()).(*timestamppb.Timestamp))
	if label := billing.SanitizeProviderText(observed.GetDisplayName()); strings.TrimSpace(label) != "" {
		next.SetDisplayName(label)
	}
	if currency := validCurrency(observed.GetBaseCurrency()); currency != "" {
		next.SetBaseCurrency(currency)
	}
	return merged, true
}

// Accounts are platform inventory, not resources of the caller-selected customer tenant. This fixed visibility
// is deliberately narrower than total visibility and does not replace the public method authorization layer.
type billingAccountsTenancy struct{}

func (billingAccountsTenancy) DetermineAssignableTenants(context.Context) (collections.Set[string], error) {
	return auth.SystemTenants, nil
}

func (billingAccountsTenancy) DetermineDefaultTenant(context.Context) (string, error) {
	return auth.SystemTenant, nil
}

func (billingAccountsTenancy) DetermineVisibility(context.Context) (*auth.Visibility, error) {
	return auth.NewVisibility().AddVisibleTenant(auth.SystemTenant).Build()
}

// The generic translator validates descriptor leaves/directions/term bounds. Billing further limits the
// resource's approved sortable paths; metadata filtering remains supported independently of this order policy.
func validateBillingAccountOrder(order string) error {
	if strings.TrimSpace(order) == "" {
		return nil
	}
	for _, term := range strings.Split(order, ",") {
		words := strings.Fields(term)
		if len(words) == 0 {
			return status.Error(codes.InvalidArgument, "invalid billing account order")
		}
		switch words[0] {
		case "id", "metadata.name", "status.provider_key", "status.display_name", "status.base_currency",
			"status.state", "status.state_observation_timestamp", "status.external_billing_account_id":
		default:
			return status.Error(codes.InvalidArgument, "unsupported billing account order path")
		}
	}
	return nil
}

// validCurrency returns code in uppercase when it is exactly three ASCII letters, and "" otherwise.
func validCurrency(code string) string {
	if len(code) != 3 {
		return ""
	}
	for _, r := range code {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return ""
		}
	}
	return strings.ToUpper(code)
}
