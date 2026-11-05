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
// otherwise stretch the walk - and the List call waiting on it - for many minutes. Two minutes fits a full
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
// UNAVAILABLE rather than stack a second walk behind the in-flight one.
var errDiscoveryInProgress = errors.New("billing account discovery is already in progress")

// errDiscoveryBackoff reports that a recent discovery attempt failed and the failure backoff window is still
// open: this List fails fast as UNAVAILABLE rather than start another walk that the window exists to suppress.
var errDiscoveryBackoff = errors.New("billing account discovery failed recently")

// billingProviderUnavailableReason is the stable reason carried when the provider cannot be reached during
// discovery: never an unmarked stale list, see PrivateBillingAccountsServer.List.
const billingProviderUnavailableReason = "BILLING_PROVIDER_UNAVAILABLE"

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
// that arrives while another's walk is still running fails fast the same way rather than queue behind it. A
// discovery that exceeds s.discoverDeadline fails the same way rather than hanging the List indefinitely.
//
// This handler owns its transaction scopes instead of running in the interceptor's per-request transaction
// (BillingAccounts/List is a manager-only method, see database_tx_interceptor.go): the discovery walk holds no
// database transaction - and pins no pool connection - while reading the provider, the walk's results are
// stored by one short independent transaction that commits everything or nothing, and the page read runs in its
// own short transaction that observes only durably committed discovery results.
func (s *PrivateBillingAccountsServer) List(ctx context.Context,
	request *privatev1.BillingAccountsListRequest) (response *privatev1.BillingAccountsListResponse, err error) {
	if err = requireBilling(s.provider); err != nil {
		return nil, err
	}

	if err = s.discoverSync(ctx); err != nil {
		s.logger.WarnContext(ctx, "Billing account discovery failed", slog.Any("error", err))
		return nil, mapBillingDiscoveryError(err)
	}

	// An unset limit serves the DAO's default page; an explicit one is clamped to its maximum. The read runs in
	// its own short transaction so it observes only committed discovery results, never uncommitted state.
	page, err := database.WithNewTx(ctx, func(ctx context.Context) (billingAccountsPage, error) {
		tx, txErr := database.TxFromContext(ctx)
		if txErr != nil {
			return billingAccountsPage{}, txErr
		}
		rows, total, listErr := database.ListBillingAccounts(
			ctx, tx, request.GetOffset(), request.GetLimit(), request.GetFilter(), request.GetOrder())
		return billingAccountsPage{rows: rows, total: total}, listErr
	})
	if err != nil {
		var invalid *database.ErrInvalidFlatTableQuery
		if errors.As(err, &invalid) {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
		s.logger.ErrorContext(ctx, "Failed to list billing accounts", slog.Any("error", err))
		return nil, status.Errorf(codes.Internal, "failed to list billing accounts")
	}

	items := make([]*privatev1.BillingAccount, 0, len(page.rows))
	for _, row := range page.rows {
		items = append(items, toPrivateBillingAccount(row))
	}
	response = privatev1.BillingAccountsListResponse_builder{
		Size:  int32(len(items)), // #nosec G115 -- bounded by page size
		Total: page.total,
		Items: items,
	}.Build()
	return response, nil
}

// billingAccountsPage carries one List read's rows and total out of the short transaction
// database.WithNewTx runs it in.
type billingAccountsPage struct {
	rows  []database.BillingAccountRow
	total int32
}

// discoverSync refreshes the stored account page when it is stale. At most one walk runs at a time: a List that
// arrives while another's walk is running fails fast with errDiscoveryInProgress rather than stack a second
// walk behind it. A failed walk records when it ended and opens the failure backoff window: Lists inside that
// window fail fast with errDiscoveryBackoff, again never serving the stored record unmarked as current. Only a
// walk whose store transaction committed durably opens the freshness window: lastDiscovery is set after
// discoverFresh returns nil, which is after database.WithNewTx has committed the walk's writes. All timestamps
// and the in-flight flag are guarded by discoverMu; the walk itself runs with the mutex released.
func (s *PrivateBillingAccountsServer) discoverSync(ctx context.Context) error {
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

	err := s.discoverFresh(ctx)
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

// discoverFresh builds the provider adapter and runs one discovery walk under s.discoverDeadline. The walk has
// two phases. fetch reads every provider page into memory under the deadline with no database transaction
// open, so no pool connection is pinned while the provider answers. store then applies the fetched accounts in
// one short independent transaction (database.WithNewTx) that commits the whole walk - upserts and unobserved
// sweep together - or rolls back entirely. The deadline covers both phases: a provider that stretches the
// fetch, or a store that cannot finish, both cut off rather than hang the List.
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
	return statusWithReason(codes.Unavailable, billingProviderUnavailableReason, "billing provider account API is unavailable")
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

// fetchAccounts reads the provider's full account list into memory. No database transaction - and no pool
// connection - is held while the provider answers: interleaving provider reads with upserts on one long
// transaction would pin that transaction's connection for the whole walk. Memory stays bounded because the
// walk itself is bounded: at most billing.MaxListPages pages of at most billingAccountsDiscoverPageSize
// accounts each. The paging bound and its MALFORMED terminal are billing.ForEachAccount's.
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

// storeAccounts normalizes and persists one fetched walk in a single short independent transaction: every
// upsert and the unobserved sweep commit together or not at all, so a failure partway through the store leaves
// no partial rows behind. Each fetched account becomes one billing_accounts row keyed by
// (ProviderKey, ProviderAccountReference); accounts the provider no longer returns are marked UNKNOWN and
// retained, never deleted. seen starts as an empty, never nil, slice: a provider whose list has become empty
// must still mark every previously known account UNKNOWN, and pgx encodes a nil slice as SQL NULL, which the
// sweep's not-in predicate would never match.
func (s *PrivateBillingAccountsServer) storeAccounts(
	ctx context.Context, accounts []billing.Account, observedAt time.Time) error {
	providerKey := s.provider.Key()
	seen := []string{}
	_, err := database.WithNewTx(ctx, func(ctx context.Context) (struct{}, error) {
		tx, txErr := database.TxFromContext(ctx)
		if txErr != nil {
			return struct{}{}, txErr
		}
		for _, account := range accounts {
			seen = append(seen, account.Ref.ID())
			_, err := database.UpsertBillingAccount(ctx, tx, database.BillingAccountUpsert{
				ID:                       uuid.New(),
				ProviderKey:              providerKey,
				ProviderAccountReference: account.Ref.ID(),
				// The display name is provider-supplied free text and is stored sanitized; the correlation
				// value is stored raw because the resolver must compare it exactly against the provider's
				// live value - the CLI sanitizes it for display instead.
				DisplayName: billing.SanitizeProviderText(account.Name),
				// M360 supplies no account currency. The design §2.3 rate-card fallback is deferred to
				// https://redhat.atlassian.net/browse/OSAC-6873; this path assigns no default.
				BaseCurrency:     validCurrency(account.BaseCurrency),
				CorrelationID:    account.ExternalID,
				State:            string(account.State),
				StatusObservedAt: observedAt,
			})
			if err != nil {
				return struct{}{}, err
			}
		}
		return struct{}{}, database.MarkBillingAccountsUnobserved(ctx, tx, providerKey, seen, observedAt)
	})
	return err
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
