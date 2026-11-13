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
	"strings"
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
	result = &PrivateBillingAccountsServer{
		logger: b.logger, provider: b.provider, discoverDeadline: defaultDiscoverDeadline,
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
// This handler owns its transaction scopes instead of using the interceptor's per-request transaction (List is a
// manager-only method): the provider walk holds no transaction or pool connection, one short transaction stores
// the results, and the page read runs in its own short transaction.
func (s *PrivateBillingAccountsServer) List(ctx context.Context,
	request *privatev1.BillingAccountsListRequest) (response *privatev1.BillingAccountsListResponse, err error) {
	if _, err = requireAdapter(s.provider); err != nil {
		return nil, err
	}

	if err = s.discoverSync(ctx); err != nil {
		s.logger.WarnContext(ctx, "Billing account discovery failed", slog.Any("error", err))
		return nil, mapBillingDiscoveryError(err)
	}

	// An unset limit serves the DAO default; an explicit one is clamped to its maximum. The read runs in its own
	// short transaction so it sees only committed discovery results.
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

// billingAccountsPage carries one List read's rows and total out of its short transaction.
type billingAccountsPage struct {
	rows  []database.BillingAccountRow
	total int32
}

// discoverSync refreshes the stored page when it is stale, running at most one walk at a time. A List that
// arrives during a walk fails with errDiscoveryInProgress; one inside the failure backoff fails with
// errDiscoveryBackoff. Only a walk whose store transaction committed durably opens the freshness window. All
// timestamps and the in-flight flag are guarded by discoverMu; the walk runs with the mutex released.
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
		// A caller that cancels its walk or hits its own deadline says nothing about the provider, so its failure
		// must not open the server-wide backoff. A walk cut off by the child discoverDeadline is still recorded,
		// because that child never cancels this context.
		if ctx.Err() == nil {
			s.discoverMu.Lock()
			s.lastDiscoveryFailure = time.Now()
			s.discoverMu.Unlock()
		}
		return err
	}
	s.discoverMu.Lock()
	s.lastDiscovery = time.Now()
	s.discoverMu.Unlock()
	return nil
}

// discoverFresh builds the provider adapter and runs one discovery walk under s.discoverDeadline, in two phases.
// fetch reads every provider page under the deadline with no transaction open; store applies them in one short
// independent transaction that commits upserts and the unobserved sweep together, or rolls back entirely. The
// deadline covers both phases.
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

// fetchAccounts reads the provider's full account list into memory without holding a transaction or pool
// connection while the provider answers. Memory stays bounded because the walk is bounded at most
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

// storeAccounts normalizes and persists one fetched walk in a single short transaction: every upsert and the
// unobserved sweep commit together or not at all. Each account becomes one row keyed by (ProviderKey,
// ProviderAccountReference); accounts the provider no longer returns are marked UNKNOWN and retained. seen starts
// empty and never nil so an empty provider list still marks every known account, since pgx encodes nil as SQL
// NULL.
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
				// The display name is provider-supplied free text and is sanitized before storage; the correlation
				// value is stored raw because the resolver compares it exactly, and the CLI sanitizes it for display.
				DisplayName: billing.SanitizeProviderText(account.Name),
				// M360 supplies no account currency, and this path assigns no default.
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
