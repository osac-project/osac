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
	"math"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
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

func (b *PrivateBillingAccountsServerBuilder) Build() (result *PrivateBillingAccountsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.provider == nil {
		err = errors.New("provider is mandatory")
		return
	}
	result = &PrivateBillingAccountsServer{logger: b.logger, provider: b.provider}
	return
}

// List reads the provider's current account list (refreshing the stored record), then returns the stored page.
// Billing disabled fails with FAILED_PRECONDITION/BILLING_NOT_CONFIGURED before any provider call. A provider
// that cannot be reached fails the whole call with UNAVAILABLE: this method never falls back to serving the
// stored record unmarked as current.
func (s *PrivateBillingAccountsServer) List(ctx context.Context,
	request *privatev1.BillingAccountsListRequest) (response *privatev1.BillingAccountsListResponse, err error) {
	if err = requireBilling(s.provider); err != nil {
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

	adapter, err := s.provider.Adapter()
	if err == nil {
		err = s.discover(ctx, tx, adapter)
	}
	if err != nil {
		s.logger.WarnContext(ctx, "Billing account discovery failed", slog.Any("error", err))
		result := status.New(codes.Unavailable, "billing provider account API is unavailable")
		if detailed, derr := result.WithDetails(&errdetails.ErrorInfo{Reason: billingProviderUnavailableReason}); derr == nil {
			result = detailed
		}
		err = result.Err()
		return nil, err
	}

	limit := int32(math.MaxInt32)
	if request.HasLimit() {
		limit = request.GetLimit()
	}
	rows, total, err := database.ListBillingAccounts(
		ctx, tx, request.GetOffset(), limit, request.GetFilter(), request.GetOrder())
	if err != nil {
		var invalid *database.ErrInvalidBillingAccountsQuery
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
// the provider no longer returned - it retains the row rather than deleting it.
func (s *PrivateBillingAccountsServer) discover(ctx context.Context, tx database.Tx, adapter billing.Adapter) error {
	providerKey := s.provider.Key()
	observedAt := time.Now().UTC()
	var seen []string
	page := billing.Page{Size: billingAccountsDiscoverPageSize}
	for {
		result, err := adapter.ListAccounts(ctx, billing.AccountFilter{}, page)
		if err != nil {
			return err
		}
		for _, account := range result.Accounts {
			seen = append(seen, account.Ref.ID())
			if _, err := database.UpsertBillingAccount(ctx, tx, database.BillingAccountUpsert{
				ID:                       uuid.New(),
				ProviderKey:              providerKey,
				ProviderAccountReference: account.Ref.ID(),
				DisplayName:              account.Name,
				BaseCurrency:             validCurrency(account.BaseCurrency),
				CorrelationID:            account.ExternalID,
				State:                    string(account.State),
				StatusObservedAt:         observedAt,
			}); err != nil {
				return err
			}
		}
		if result.Next == "" {
			break
		}
		page.Token = result.Next
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
