/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package database

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
)

// BillingAccountUpsert carries one discovered account's provider-observed values into
// UpsertBillingAccount.
type BillingAccountUpsert struct {
	ID                       string
	ProviderKey              string
	ProviderAccountReference string
	// DisplayName and BaseCurrency are "" when the provider did not supply one for this read; an
	// existing stored value is preserved rather than cleared.
	DisplayName  string
	BaseCurrency string
	// CorrelationID is "" when the provider has no native correlation value for this account.
	CorrelationID    string
	State            string
	StatusObservedAt time.Time
}

// UpsertBillingAccount inserts a new billing_accounts row keyed by (ProviderKey,
// ProviderAccountReference), or refreshes an existing one. DisplayName and BaseCurrency only
// overwrite a stored value when the incoming value is non-empty: discovery never clears a known
// value just because this read of the provider did not carry it. State and StatusObservedAt are
// always refreshed - this call is itself the fresh observation. CorrelationID is written only
// when the local value is currently null, the incoming value is non-empty, and no other row
// already holds it (the partial unique index would otherwise reject the write); any other case -
// including a local value that already differs from the incoming one - leaves the stored
// correlation_id unchanged. That disagreement is a conflict for a later resolver to detect
// (OSAC-6885), not something discovery corrects.
func UpsertBillingAccount(ctx context.Context, tx Tx, in BillingAccountUpsert) (id string, err error) {
	err = tx.QueryRow(ctx, `
		insert into billing_accounts (
			id, provider_key, provider_account_reference, display_name, base_currency,
			correlation_id, state, status_observed_at
		)
		select
			$1, $2, $3, $4, $5,
			-- A brand-new row must honor the unique index too: an insert (as opposed to the
			-- on-conflict update below) is not covered by that update's own conflict check.
			case
				when nullif($6, '') is not null
				 and not exists (select 1 from billing_accounts b2 where b2.correlation_id = nullif($6, ''))
				then nullif($6, '')
				else null
			end,
			$7, $8
		on conflict (provider_key, provider_account_reference) do update set
			display_name = case when excluded.display_name <> '' then excluded.display_name
				else billing_accounts.display_name end,
			base_currency = case when excluded.base_currency <> '' then excluded.base_currency
				else billing_accounts.base_currency end,
			state = excluded.state,
			status_observed_at = excluded.status_observed_at,
			correlation_id = case
				when billing_accounts.correlation_id is null
				 and excluded.correlation_id is not null
				 and not exists (
					select 1 from billing_accounts b2 where b2.correlation_id = excluded.correlation_id
				 )
				then excluded.correlation_id
				else billing_accounts.correlation_id
			end
		returning id`,
		in.ID, in.ProviderKey, in.ProviderAccountReference, in.DisplayName, in.BaseCurrency,
		in.CorrelationID, in.State, in.StatusObservedAt).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("upsert billing account: %w", err)
	}
	return id, nil
}

// MarkBillingAccountsUnobserved marks UNKNOWN every billing_accounts row for providerKey whose
// provider_account_reference is not in seenReferences, i.e. the account disappeared from the
// provider's current list. The row is retained, never deleted. A nil seenReferences is treated as empty:
// pgx encodes a nil slice as SQL NULL, and "not (x = any(NULL))" is NULL, which would match no rows and
// silently skip the sweep for a provider whose list has become empty.
func MarkBillingAccountsUnobserved(
	ctx context.Context, tx Tx, providerKey string, seenReferences []string, observedAt time.Time,
) error {
	if seenReferences == nil {
		seenReferences = []string{}
	}
	_, err := tx.Exec(ctx, `
		update billing_accounts
		set state = 'UNKNOWN', status_observed_at = $1
		where provider_key = $2
		  and not (provider_account_reference = any($3::text[]))
		  and state <> 'UNKNOWN'`,
		observedAt, providerKey, seenReferences)
	if err != nil {
		return fmt.Errorf("mark unobserved billing accounts: %w", err)
	}
	return nil
}

// BillingAccountRow is one row read back by ListBillingAccounts. CorrelationID is "" when null.
type BillingAccountRow struct {
	ID               string
	ProviderKey      string
	DisplayName      string
	BaseCurrency     string
	CorrelationID    string
	State            string
	StatusObservedAt time.Time
}

// billingAccountColumns maps the API field names accepted in filter/order expressions to their
// SQL columns. Only API-visible fields are selectable here: the private provider_account_reference
// must never be reachable through a filter or order string (see dao.FilterTranslator's docs on the
// filter-oracle boundary between public and private data - the same boundary applies here even
// though this table does not use that translator).
var billingAccountColumns = map[string]string{
	"id":             "id",
	"provider_key":   "provider_key",
	"display_name":   "display_name",
	"base_currency":  "base_currency",
	"state":          "state",
	"correlation_id": "correlation_id",
}

// filterClausePattern matches one `this.<field> == "<value>"` clause.
//
// shortcut: filter supports only exact-match equality on a known column, clauses chained with
// " && " - no !=, no ||, no numeric/date comparisons, no CEL functions. Upgrade to a real
// CEL-based translator (see internal/database/dao/filter_translator.go) if a future billing list
// (e.g. ProviderRateCardItems.List) needs richer filtering on a flat, non-jsonb table; that
// translator cannot be reused as-is because it always emits `data->>'field'` SQL tied to the
// generic Metadata/Spec/Status object shape, which billing_accounts deliberately does not use.
var filterClausePattern = regexp.MustCompile(`^this\.([a-z_]+) == "([^"]*)"$`)

// billingAccountFilterSQL translates a filter expression into a SQL WHERE clause and its args, using
// filterClausePattern and billingAccountColumns. Returns "", nil, nil for an empty filter.
func billingAccountFilterSQL(filter string) (string, []any, error) {
	return translateFlatTableFilter(filter, func(clause string, addArg func(any) string) (string, error) {
		match := filterClausePattern.FindStringSubmatch(clause)
		if match == nil {
			return "", invalidFlatTableQueryf(
				`unsupported filter clause %q: only this.<field> == "<value>" clauses joined by && are supported`,
				clause)
		}
		column, ok := billingAccountColumns[match[1]]
		if !ok {
			return "", invalidFlatTableQueryf("unknown filter field %q", match[1])
		}
		return column + " = " + addArg(match[2]), nil
	})
}

// billingAccountOrderSQL translates an order expression into a SQL ORDER BY column (with optional
// asc/desc), defaulting to "id" for an empty order.
func billingAccountOrderSQL(order string) (string, error) {
	return translateFlatTableOrder(order, billingAccountColumns, "id")
}

// ListBillingAccounts returns the page of billing_accounts rows matching filter, ordered by
// order, along with the total count of matching rows ignoring offset/limit. An unset limit serves
// flatTableDefaultLimit rows and an explicit one is clamped to flatTableMaxLimit.
func ListBillingAccounts(ctx context.Context, tx Tx, offset, limit int32, filter, order string) (
	items []BillingAccountRow, total int32, err error) {
	whereSQL, args, err := billingAccountFilterSQL(filter)
	if err != nil {
		return nil, 0, err
	}
	orderSQL, err := billingAccountOrderSQL(order)
	if err != nil {
		return nil, 0, err
	}
	return listFlatTable(ctx, tx, "billing_accounts", "billing accounts",
		"id, provider_key, display_name, base_currency, coalesce(correlation_id, ''), state, status_observed_at",
		whereSQL, orderSQL, args, limit, offset, func(row pgx.Row) (BillingAccountRow, error) {
			var item BillingAccountRow
			if err := row.Scan(&item.ID, &item.ProviderKey, &item.DisplayName, &item.BaseCurrency,
				&item.CorrelationID, &item.State, &item.StatusObservedAt); err != nil {
				return BillingAccountRow{}, fmt.Errorf("scan billing account: %w", err)
			}
			return item, nil
		})
}
