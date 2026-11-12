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

// BillingAccountUpsert carries one discovered account's provider-observed values into UpsertBillingAccount.
type BillingAccountUpsert struct {
	ID                       string
	ProviderKey              string
	ProviderAccountReference string
	// DisplayName and BaseCurrency are "" when the provider did not supply one; an existing stored value is
	// preserved rather than cleared.
	DisplayName  string
	BaseCurrency string
	// CorrelationID is "" when the provider has no native correlation value for this account.
	CorrelationID    string
	State            string
	StatusObservedAt time.Time
}

// UpsertBillingAccount inserts a billing_accounts row keyed by (ProviderKey, ProviderAccountReference) or
// refreshes it. State and StatusObservedAt only move forward: an observation older than the stored one is ignored,
// so out-of-order walks cannot restore a stale state. DisplayName and BaseCurrency change only when the incoming
// value is non-empty and not older, so discovery never clears a known value. CorrelationID is written only when
// the local value is null, the incoming value is non-empty, and no other row holds it; otherwise the stored value
// stays, and any disagreement is left for a later resolver.
func UpsertBillingAccount(ctx context.Context, tx Tx, in BillingAccountUpsert) (id string, err error) {
	err = tx.QueryRow(ctx, `
		insert into billing_accounts (
			id, provider_key, provider_account_reference, display_name, base_currency,
			correlation_id, state, status_observed_at
		)
		select
			$1, $2, $3, $4, $5,
			-- A new row must honor the unique index too: the insert path is not covered by the on-conflict
			-- check below.
			case
				when nullif($6, '') is not null
				 and not exists (select 1 from billing_accounts b2 where b2.correlation_id = nullif($6, ''))
				then nullif($6, '')
				else null
			end,
			$7, $8
		on conflict (provider_key, provider_account_reference) do update set
			-- An older observation must not restore a stale state or move status_observed_at backwards. A
			-- conditional assignment is used instead of a WHERE on the conflict branch: a WHERE would suppress
			-- the row, and RETURNING id would then yield nothing.
			display_name = case
				when excluded.status_observed_at >= billing_accounts.status_observed_at
				 and excluded.display_name <> ''
				then excluded.display_name
				else billing_accounts.display_name
			end,
			base_currency = case
				when excluded.status_observed_at >= billing_accounts.status_observed_at
				 and excluded.base_currency <> ''
				then excluded.base_currency
				else billing_accounts.base_currency
			end,
			state = case
				when excluded.status_observed_at >= billing_accounts.status_observed_at
				then excluded.state
				else billing_accounts.state
			end,
			status_observed_at = greatest(
				billing_accounts.status_observed_at, excluded.status_observed_at),
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

// MarkBillingAccountsUnobserved marks UNKNOWN every row for providerKey whose reference is not in
// seenReferences, retaining the row. Only rows not newer than observedAt are marked, so a newer walk's discovery
// is not demoted. A row that is already UNKNOWN still takes the newer observedAt: without that, a walk that read
// the account before this sweep could commit later and restore it, because UpsertBillingAccount only compares
// observation times. A nil seenReferences is treated as empty: pgx encodes nil as SQL NULL, and the sweep would
// otherwise match nothing.
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
		  and status_observed_at <= $1`,
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

// billingAccountColumns maps API field names in filter/order expressions to SQL columns. Only API-visible
// fields are selectable: provider_account_reference is private and must never be reachable from a filter or
// order string.
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
// Filtering supports only exact-match equality on a known column, chained with " && ": no !=, ||, or
// numeric/date comparisons. Richer filtering needs a real translator, which cannot be reused as-is because it
// emits `data->>'field'` SQL tied to the generic Metadata/Spec/Status shape.
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

// billingAccountOrderSQL translates an order expression into ORDER BY, defaulting to "id". A caller appends
// withIDTieBreaker so offset/limit pagination stays deterministic across ties.
func billingAccountOrderSQL(order string) (string, error) {
	translated, err := translateFlatTableOrder(order, billingAccountColumns, "id")
	if err != nil {
		return "", err
	}
	return withIDTieBreaker(translated), nil
}

// ListBillingAccounts returns the matching page and the total count ignoring offset/limit. An unset limit serves
// flatTableDefaultLimit rows; an explicit one is clamped to flatTableMaxLimit.
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
