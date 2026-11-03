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
	"strings"
	"time"
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
// provider's current list. The row is retained, never deleted.
func MarkBillingAccountsUnobserved(
	ctx context.Context, tx Tx, providerKey string, seenReferences []string, observedAt time.Time,
) error {
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

// ErrInvalidBillingAccountsQuery reports a malformed filter or order expression in a ListBillingAccounts call.
// Callers should map it to INVALID_ARGUMENT; any other error ListBillingAccounts returns is a database execution
// failure and should be treated as an internal error instead - the two must not be conflated, or a transient
// database problem gets reported to the caller as "your request is invalid".
type ErrInvalidBillingAccountsQuery struct{ error }

func invalidQueryf(format string, args ...any) error {
	return &ErrInvalidBillingAccountsQuery{fmt.Errorf(format, args...)}
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

func billingAccountFilterSQL(filter string) (sql string, args []any, err error) {
	if filter == "" {
		return "", nil, nil
	}
	var sqlClauses []string
	for _, clause := range strings.Split(filter, "&&") {
		match := filterClausePattern.FindStringSubmatch(strings.TrimSpace(clause))
		if match == nil {
			return "", nil, invalidQueryf(
				`unsupported filter clause %q: only this.<field> == "<value>" clauses joined by && are supported`,
				strings.TrimSpace(clause))
		}
		column, ok := billingAccountColumns[match[1]]
		if !ok {
			return "", nil, invalidQueryf("unknown filter field %q", match[1])
		}
		args = append(args, match[2])
		sqlClauses = append(sqlClauses, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	return strings.Join(sqlClauses, " and "), args, nil
}

// orderPattern matches "<field>" or "<field> asc|desc".
//
// shortcut: order supports only a single field, not a comma-separated list. Upgrade alongside the
// filter translator above if a future story needs multi-field ordering on a flat table.
var orderPattern = regexp.MustCompile(`^([a-z_]+)(?: (asc|desc))?$`)

func billingAccountOrderSQL(order string) (sql string, err error) {
	if order == "" {
		return "id", nil
	}
	match := orderPattern.FindStringSubmatch(strings.TrimSpace(order))
	if match == nil {
		return "", invalidQueryf(`unsupported order expression %q: only "<field>" or "<field> asc|desc" is supported`, order)
	}
	column, ok := billingAccountColumns[match[1]]
	if !ok {
		return "", invalidQueryf("unknown order field %q", match[1])
	}
	if match[2] != "" {
		return column + " " + match[2], nil
	}
	return column, nil
}

// ListBillingAccounts returns the page of billing_accounts rows matching filter, ordered by
// order, along with the total count of matching rows ignoring offset/limit.
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
	where := ""
	if whereSQL != "" {
		where = " where " + whereSQL
	}

	if err = tx.QueryRow(ctx, "select count(*) from billing_accounts"+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count billing accounts: %w", err)
	}

	args = append(args, limit, offset)
	rows, err := tx.Query(ctx, fmt.Sprintf(`
		select id, provider_key, display_name, base_currency, coalesce(correlation_id, ''), state, status_observed_at
		from billing_accounts%s
		order by %s
		limit $%d offset $%d`, where, orderSQL, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list billing accounts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row BillingAccountRow
		if err = rows.Scan(&row.ID, &row.ProviderKey, &row.DisplayName, &row.BaseCurrency,
			&row.CorrelationID, &row.State, &row.StatusObservedAt); err != nil {
			return nil, 0, fmt.Errorf("scan billing account: %w", err)
		}
		items = append(items, row)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list billing accounts: %w", err)
	}
	return items, total, nil
}
