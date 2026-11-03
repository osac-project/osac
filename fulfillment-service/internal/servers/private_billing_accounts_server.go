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
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// billingAccountsDiscoverPageSize is the page size discovery requests from the provider on every call, per the
// AC's "all pages, page size max 20".
const billingAccountsDiscoverPageSize = 20

// billingAccountsDiscoverFreshness is how long a successful discovery keeps the stored account list fresh.
// Within the window List serves the stored page without re-reading the provider, so a polling client cannot
// multiply provider load and connection-pool pressure; the next List after the window re-discovers. It is a
// per-process default: OSAC-6894 owns wiring the chart's OSAC_BILLING_* tuning to it.
const billingAccountsDiscoverFreshness = 30 * time.Second

// defaultDiscoverDeadline bounds one discovery walk. The M360 transport's own retry budget already cuts off a
// hanging provider on the first page (four 10-second attempts plus backoff, about 42 seconds); the deadline
// bounds the remaining case, a slow-but-succeeding provider whose up-to-billing.MaxListPages pages could
// otherwise stretch the walk - and the request transaction it pins - for many minutes. Two minutes fits a full
// healthy 50-page walk with margin. It is a per-process default: OSAC-6894 owns wiring the chart's
// OSAC_BILLING_* tuning to it.
const defaultDiscoverDeadline = 2 * time.Minute

// defaultDiscoverFailureBackoff is how long a failed discovery walk suppresses the next attempt: a List inside
// the window fails fast as UNAVAILABLE instead of stacking another full walk per List, so a polling client
// cannot multiply provider load and connection-pool pressure against a provider that is down or hanging. Only
// a failed walk opens the window; a successful one opens the freshness window instead. It is a per-process
// default: OSAC-6894 owns wiring the chart's OSAC_BILLING_* tuning to it.
const defaultDiscoverFailureBackoff = 10 * time.Second

// errDiscoveryInProgress reports that another List's discovery walk is still running: this List fails fast as
// UNAVAILABLE rather than queue its request transaction - and the pool connection that transaction pins -
// behind the in-flight walk.
var errDiscoveryInProgress = errors.New("billing account discovery is already in progress")

// errDiscoveryBackoff reports that a recent discovery attempt failed and the failure backoff window is still
// open: this List fails fast as UNAVAILABLE rather than start another walk that the window exists to suppress.
var errDiscoveryBackoff = errors.New("billing account discovery failed recently")

// PrivateBillingAccountsServerBuilder contains the data and logic needed to create a new private billing accounts
// server.
type PrivateBillingAccountsServerBuilder struct {
	logger   *slog.Logger
	provider *billing.Provider
}

var _ privatev1.BillingAccountsServer = (*PrivateBillingAccountsServer)(nil)

// PrivateBillingAccountsServer discovers accounts from the configured billing provider, upserts them, and serves
// the stored page.
type PrivateBillingAccountsServer struct {
	privatev1.UnimplementedBillingAccountsServer
	logger   *slog.Logger
	provider *billing.Provider
	// discoverMu guards the discovery timestamps and the in-flight flag below, and serializes the decision to
	// start a walk: at most one walk runs at a time, whatever the pool size.
	discoverMu sync.Mutex
	// lastDiscovery is when the last successful discovery started the current freshness window.
	lastDiscovery time.Time
	// lastDiscoveryFailure is when the last failed walk ended; the failure backoff window runs from it.
	lastDiscoveryFailure time.Time
	// discoverInFlight reports whether a walk is currently running.
	discoverInFlight bool
	// discoverDeadline bounds one discovery walk; discoverFailureBackoff is how long a failed walk suppresses
	// the next attempt. Both are fields so the suite can shorten them.
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
	result = &PrivateBillingAccountsServer{
		logger: b.logger, provider: b.provider, discoverDeadline: defaultDiscoverDeadline,
		discoverFailureBackoff: defaultDiscoverFailureBackoff,
	}
	return
}

// List refreshes the stored account record from the provider when the last successful discovery is older than
// billingAccountsDiscoverFreshness, then returns the stored page. Billing disabled fails with
// FAILED_PRECONDITION/BILLING_NOT_CONFIGURED before any provider call. A provider that cannot be reached fails
// the whole call with UNAVAILABLE: this method never falls back to serving the stored record unmarked as current
// - only a successful discovery opens a freshness window, and a failed one is retried once the failure backoff
// has elapsed, so a polling client cannot stack one full walk per List. At most one walk runs at a time: a List
// that arrives while another's walk is still running fails fast the same way rather than queue its request
// transaction - and the pool connection that transaction pins - behind it. A discovery that exceeds
// s.discoverDeadline fails the same way rather than pinning the request's transaction indefinitely.
func (s *PrivateBillingAccountsServer) List(ctx context.Context,
	request *privatev1.BillingAccountsListRequest) (response *privatev1.BillingAccountsListResponse, err error) {
	if _, err = requireAdapter(s.provider); err != nil {
		return nil, err
	}

	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, err
	}
	// Report every error below to the transaction: a handler's returned error does not by itself roll back the
	// per-request transaction (see database_tx_interceptor.go), so without this a discovery failure partway
	// through pagination would still commit whatever accounts were upserted before the failure.
	defer tx.ReportError(&err)

	if err = s.discoverSync(ctx, tx); err != nil {
		s.logger.WarnContext(ctx, "Billing account discovery failed", slog.Any("error", err))
		return nil, mapBillingDiscoveryError(err)
	}

	// An unset limit serves the DAO's default page; an explicit one is clamped to its maximum.
	rows, total, err := database.ListBillingAccounts(
		ctx, tx, request.GetOffset(), request.GetLimit(), request.GetFilter(), request.GetOrder())
	if err != nil {
		var invalid *database.ErrInvalidFlatTableQuery
		if errors.As(err, &invalid) {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
		s.logger.ErrorContext(ctx, "Failed to list billing accounts", slog.Any("error", err))
		return nil, status.Errorf(codes.Internal, "failed to list billing accounts")
	}

	items := make([]*privatev1.BillingAccount, 0, len(rows))
	for _, row := range rows {
		items = append(items, toPrivateBillingAccount(row))
	}
	response = privatev1.BillingAccountsListResponse_builder{
		Size:  int32(len(items)), // #nosec G115 -- bounded by page size
		Total: total,
		Items: items,
	}.Build()
	return response, nil
}

// discoverSync refreshes the stored account page when it is stale. At most one walk runs at a time: a List that
// arrives while another's walk is running fails fast with errDiscoveryInProgress rather than queue its request
// transaction behind the walk. A failed walk records when it ended and opens the failure backoff window: Lists
// inside that window fail fast with errDiscoveryBackoff, again never serving the stored record unmarked as
// current. Only a successful walk opens the freshness window. All timestamps and the in-flight flag are guarded
// by discoverMu; the walk itself runs with the mutex released.
func (s *PrivateBillingAccountsServer) discoverSync(ctx context.Context, tx database.Tx) error {
	s.discoverMu.Lock()
	if time.Since(s.lastDiscovery) < billingAccountsDiscoverFreshness {
		s.discoverMu.Unlock()
		return nil
	}
	if time.Since(s.lastDiscoveryFailure) < s.discoverFailureBackoff {
		s.discoverMu.Unlock()
		return errDiscoveryBackoff
	}
	if s.discoverInFlight {
		s.discoverMu.Unlock()
		return errDiscoveryInProgress
	}
	s.discoverInFlight = true
	s.discoverMu.Unlock()
	defer func() {
		s.discoverMu.Lock()
		s.discoverInFlight = false
		s.discoverMu.Unlock()
	}()

	err := s.discoverFresh(ctx, tx)
	if err != nil {
		s.discoverMu.Lock()
		s.lastDiscoveryFailure = time.Now()
		s.discoverMu.Unlock()
		return err
	}
	s.discoverMu.Lock()
	s.lastDiscovery = time.Now()
	s.discoverMu.Unlock()
	return nil
}

// discoverFresh builds the provider adapter and runs one discovery walk under s.discoverDeadline. The deadline
// covers the walk's provider reads and its upserts alike: discovery runs inside the caller's request
// transaction, so an unbounded walk would pin that transaction's pool connection for as long as the provider
// cares to stretch it.
func (s *PrivateBillingAccountsServer) discoverFresh(ctx context.Context, tx database.Tx) error {
	adapter, err := s.provider.Adapter()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, s.discoverDeadline)
	defer cancel()
	return s.discover(ctx, tx, adapter)
}

// mapBillingDiscoveryError classifies a discovery failure: an unavailable or timed-out provider is
// UNAVAILABLE/BILLING_PROVIDER_UNAVAILABLE (never an unmarked stale list), while any other classified provider
// error - a provider that never reports a last page is MALFORMED, not merely down - surfaces its own reason as
// FAILED_PRECONDITION. Anything that is not a classified provider error - the failure backoff window, a walk
// still in progress - maps to the same UNAVAILABLE.
func mapBillingDiscoveryError(err error) error {
	var providerErr *billing.Error
	if errors.As(err, &providerErr) {
		return mapClassifiedBillingError(providerErr, billingProviderUnavailableReason)
	}
	return billingFailure(codes.Unavailable, "billing provider account API is unavailable",
		billingProviderUnavailableReason)
}

func toPrivateBillingAccount(row database.BillingAccountRow) *privatev1.BillingAccount {
	return privatev1.BillingAccount_builder{
		Id:               row.ID,
		ProviderKey:      row.ProviderKey,
		DisplayName:      row.DisplayName,
		BaseCurrency:     row.BaseCurrency,
		State:            mapBillingAccountState(row.State),
		StatusObservedAt: timestamppb.New(row.StatusObservedAt),
		CorrelationId:    row.CorrelationID,
	}.Build()
}

// mapBillingAccountState maps a billing_accounts.state column value to its proto enum, returning UNKNOWN for
// anything else.
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

// discover pages through every provider account (page size capped at billingAccountsDiscoverPageSize), upserting
// one billing_accounts row per account, then marks UNKNOWN any previously known account under this provider that
// the provider no longer returned - it retains the row rather than deleting it. The paging bound and its
// MALFORMED terminal are billing.ForEachAccount's. seen starts as an empty, never nil, slice: a provider whose
// list has become empty must still mark every previously known account UNKNOWN, and pgx encodes a nil slice as
// SQL NULL, which the sweep's not-in predicate would never match.
func (s *PrivateBillingAccountsServer) discover(ctx context.Context, tx database.Tx, adapter billing.Adapter) error {
	providerKey := s.provider.Key()
	observedAt := time.Now().UTC()
	seen := []string{}
	upsert := func(account billing.Account) error {
		seen = append(seen, account.Ref.ID())
		_, err := database.UpsertBillingAccount(ctx, tx, database.BillingAccountUpsert{
			ID:                       uuid.New(),
			ProviderKey:              providerKey,
			ProviderAccountReference: account.Ref.ID(),
			// The display name is provider-supplied free text and is stored sanitized; the correlation
			// value is stored raw because the resolver must compare it exactly against the provider's
			// live value - the CLI sanitizes it for display instead.
			DisplayName:      billing.SanitizeProviderText(account.Name),
			BaseCurrency:     validCurrency(account.BaseCurrency),
			CorrelationID:    account.ExternalID,
			State:            string(account.State),
			StatusObservedAt: observedAt,
		})
		return err
	}
	err := billing.ForEachAccount(
		ctx, adapter, billing.AccountFilter{},
		billing.Page{Size: billingAccountsDiscoverPageSize}, upsert)
	if err != nil {
		return err
	}
	return database.MarkBillingAccountsUnobserved(ctx, tx, providerKey, seen, observedAt)
}

// validCurrency returns code unchanged when it is a plausible ISO-4217 alphabetic code (exactly three ASCII
// letters), and "" otherwise. An implausible code is ignored rather than stored, per the edge case of a currency
// whose code is not three letters.
func validCurrency(code string) string {
	if len(code) != 3 {
		return ""
	}
	for _, r := range code {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return ""
		}
	}
	return code
}
