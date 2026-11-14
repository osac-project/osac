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
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/osac-project/osac/fulfillment-service/internal/collections"
)

// TenantBillingAssignmentInsert carries the fields of one new assignment interval.
type TenantBillingAssignmentInsert struct {
	ID               string
	Name             string
	TenantID         string
	BillingAccountID string
	ConnectionState  string
	Message          string
	Creator          string
}

// TenantBillingAssignmentRow is one row read back from tenant_billing_assignments. EffectiveTo is nil while the
// interval is current.
type TenantBillingAssignmentRow struct {
	ID                string
	Name              string
	TenantID          string
	BillingAccountID  string
	EffectiveFrom     time.Time
	EffectiveTo       *time.Time
	ConnectionState   string
	Message           string
	CreationTimestamp time.Time
	Creator           string
}

const tenantBillingAssignmentColumns = `
	id, name, tenant_id, billing_account_id, effective_from, effective_to, connection_state, message,
	creation_timestamp, creator`

func scanTenantBillingAssignmentRow(row pgx.Row) (TenantBillingAssignmentRow, error) {
	var r TenantBillingAssignmentRow
	err := row.Scan(&r.ID, &r.Name, &r.TenantID, &r.BillingAccountID, &r.EffectiveFrom, &r.EffectiveTo,
		&r.ConnectionState, &r.Message, &r.CreationTimestamp, &r.Creator)
	return r, err
}

// CloseAndInsertTenantBillingAssignment closes closeID's interval and inserts a new one in the same transaction.
// PostgreSQL's now() is constant within a transaction, so effective_to and the new effective_from match exactly:
// no gap, no overlap. closeID is "" to open the first interval. The partial unique indexes are the sole guard
// against two active intervals.
func CloseAndInsertTenantBillingAssignment(
	ctx context.Context, tx Tx, closeID string, in TenantBillingAssignmentInsert,
) (id string, err error) {
	if closeID != "" {
		if _, err = tx.Exec(ctx, `
			update tenant_billing_assignments set effective_to = now()
			where id = $1 and effective_to is null`, closeID); err != nil {
			return "", fmt.Errorf("close tenant billing assignment: %w", err)
		}
	}
	err = tx.QueryRow(ctx, `
		insert into tenant_billing_assignments (id, name, tenant_id, billing_account_id, connection_state, message, creator)
		values ($1, $2, $3, $4, $5, $6, $7)
		returning id`,
		in.ID, in.Name, in.TenantID, in.BillingAccountID, in.ConnectionState, in.Message, in.Creator).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert tenant billing assignment: %w", err)
	}
	return id, nil
}

// TenantBillingAssignmentDeletedMessage marks the interval closed by tenant deletion without exposing
// provider identifiers or changing its last validated connection state.
const TenantBillingAssignmentDeletedMessage = "TENANT_DELETED: tenant deleted; assignment retained as billing history"

// CloseTenantBillingAssignmentOnDeletion closes only the tenant's current interval in the caller's deletion
// transaction. The caller holds the tenant row lock, serializing deletion with assignment finalization.
// That lock can be acquired after a newer assignment transaction commits, so clamp the transaction timestamp
// to the interval's start rather than recording a negative interval.
// Closed history (including an interval closed by an earlier Delete) is never rewritten.
func CloseTenantBillingAssignmentOnDeletion(ctx context.Context, tx Tx, tenantID string) error {
	_, err := tx.Exec(ctx, `
		update tenant_billing_assignments set effective_to = greatest(now(), effective_from), message = $2
		where tenant_id = $1 and effective_to is null`, tenantID, TenantBillingAssignmentDeletedMessage)
	if err != nil {
		return fmt.Errorf("close tenant billing assignment on tenant deletion: %w", err)
	}
	return nil
}

// GetTenantBillingAssignment returns one assignment interval by id, or a pgx.ErrNoRows-wrapping error when it
// does not exist.
func GetTenantBillingAssignment(ctx context.Context, tx Tx, id string) (TenantBillingAssignmentRow, error) {
	row, err := scanTenantBillingAssignmentRow(tx.QueryRow(ctx,
		"select "+tenantBillingAssignmentColumns+" from tenant_billing_assignments where id = $1", id))
	if err != nil {
		return TenantBillingAssignmentRow{}, fmt.Errorf("get tenant billing assignment: %w", err)
	}
	return row, nil
}

// GetActiveTenantBillingAssignmentByTenant returns tenantID's current (effective_to is null) assignment, or a
// pgx.ErrNoRows-wrapping error when it has none.
func GetActiveTenantBillingAssignmentByTenant(ctx context.Context, tx Tx, tenantID string) (
	TenantBillingAssignmentRow, error) {
	row, err := scanTenantBillingAssignmentRow(tx.QueryRow(ctx,
		"select "+tenantBillingAssignmentColumns+
			" from tenant_billing_assignments where tenant_id = $1 and effective_to is null", tenantID))
	if err != nil {
		return TenantBillingAssignmentRow{}, fmt.Errorf("get active tenant billing assignment by tenant: %w", err)
	}
	return row, nil
}

// GetActiveTenantBillingAssignmentByAccount returns accountID's current (effective_to is null) assignment, or a
// pgx.ErrNoRows-wrapping error when it has none.
func GetActiveTenantBillingAssignmentByAccount(ctx context.Context, tx Tx, accountID string) (
	TenantBillingAssignmentRow, error) {
	row, err := scanTenantBillingAssignmentRow(tx.QueryRow(ctx,
		"select "+tenantBillingAssignmentColumns+
			" from tenant_billing_assignments where billing_account_id = $1 and effective_to is null", accountID))
	if err != nil {
		return TenantBillingAssignmentRow{}, fmt.Errorf("get active tenant billing assignment by account: %w", err)
	}
	return row, nil
}

// tenantBillingAssignmentFilterColumns maps the API field paths accepted in filter expressions - spec./status.
// prefixed, matching the TenantBillingAssignment proto shape - to their SQL columns.
var tenantBillingAssignmentFilterColumns = map[string]string{
	"spec.tenant_id":          "tenant_id",
	"spec.billing_account_id": "billing_account_id",
	"status.connection_state": "connection_state",
}

// tenantBillingAssignmentOrderColumns maps the bare field names accepted in order expressions to their SQL
// columns.
var tenantBillingAssignmentOrderColumns = map[string]string{
	"id":                 "id",
	"name":               "name",
	"tenant_id":          "tenant_id",
	"billing_account_id": "billing_account_id",
	"connection_state":   "connection_state",
	"effective_from":     "effective_from",
	"effective_to":       "effective_to",
}

// tenantBillingAssignmentEqualityPattern matches one `this.spec.<field> == "<value>"` or
// `this.status.<field> == "<value>"` clause.
//
// Like billing_account.go's filterClausePattern, this supports only exact-match equality and the has()/!has()
// check, chained with " && " - no full CEL. A richer filter needs dao.FilterTranslator.
var tenantBillingAssignmentEqualityPattern = regexp.MustCompile(`^this\.(spec|status)\.([a-z_]+) == "([^"]*)"$`)

// tenantBillingAssignmentHasPattern matches `has(this.status.effective_to)` (historical only) or
// `!has(this.status.effective_to)` (current only).
var tenantBillingAssignmentHasPattern = regexp.MustCompile(`^(!?)has\(this\.status\.effective_to\)$`)

// tenantBillingAssignmentFilterSQL translates a filter expression into a SQL WHERE clause and args, handling
// equality and the has(this.status.effective_to) check. Returns "", nil, nil for an empty filter.
func tenantBillingAssignmentFilterSQL(filter string) (string, []any, error) {
	return translateFlatTableFilter(filter, func(clause string, addArg func(any) string) (string, error) {
		if m := tenantBillingAssignmentHasPattern.FindStringSubmatch(clause); m != nil {
			if m[1] == "!" {
				return "effective_to is null", nil
			}
			return "effective_to is not null", nil
		}
		m := tenantBillingAssignmentEqualityPattern.FindStringSubmatch(clause)
		if m == nil {
			return "", invalidFlatTableQueryf(
				`unsupported filter clause %q: only this.spec.<field> == "<value>", this.status.<field> == `+
					`"<value>" and has(this.status.effective_to) clauses joined by && are supported`, clause)
		}
		column, ok := tenantBillingAssignmentFilterColumns[m[1]+"."+m[2]]
		if !ok {
			return "", invalidFlatTableQueryf("unknown filter field %q", m[1]+"."+m[2])
		}
		return column + " = " + addArg(m[3]), nil
	})
}

// tenantBillingAssignmentOrderSQL translates an order expression into ORDER BY, defaulting to "effective_from
// desc". A caller appends withIDTieBreaker for deterministic pagination, preserving latest-first semantics.
func tenantBillingAssignmentOrderSQL(order string) (string, error) {
	translated, err := translateFlatTableOrder(order, tenantBillingAssignmentOrderColumns, "effective_from desc")
	if err != nil {
		return "", err
	}
	return withIDTieBreaker(translated), nil
}

// ListTenantBillingAssignments returns the matching page and the total count ignoring offset/limit, ordered
// current-first by default. An unset limit serves flatTableDefaultLimit rows; an explicit one is clamped.
func ListTenantBillingAssignments(ctx context.Context, tx Tx, offset, limit int32, filter, order string) (
	items []TenantBillingAssignmentRow, total int32, err error) {
	return ListTenantBillingAssignmentsForTenants(ctx, tx, offset, limit, filter, order,
		collections.NewUniversalSet[string]())
}

// ListTenantBillingAssignmentsForTenants intersects the caller's trusted tenant scope with the request filter
// in SQL, before counting or paginating. An empty (including zero-value) set matches no rows. The scope is not
// expressed as a client filter: the flat filter grammar cannot represent a multi-tenant OR expression.
func ListTenantBillingAssignmentsForTenants(ctx context.Context, tx Tx, offset, limit int32, filter, order string,
	tenants collections.Set[string],
) (items []TenantBillingAssignmentRow, total int32, err error) {
	whereSQL, args, err := tenantBillingAssignmentFilterSQL(filter)
	if err != nil {
		return nil, 0, err
	}
	if !tenants.Universal() {
		var scopeSQL string
		switch {
		case tenants.Empty():
			scopeSQL = "false"
		case tenants.Finite():
			args = append(args, tenants.Inclusions())
			scopeSQL = fmt.Sprintf("tenant_id = any($%d::text[])", len(args))
		default:
			args = append(args, tenants.Exclusions())
			scopeSQL = fmt.Sprintf("not (tenant_id = any($%d::text[]))", len(args))
		}
		if whereSQL != "" {
			whereSQL += " and "
		}
		whereSQL += scopeSQL
	}
	orderSQL, err := tenantBillingAssignmentOrderSQL(order)
	if err != nil {
		return nil, 0, err
	}
	return listFlatTable(ctx, tx, "tenant_billing_assignments", "tenant billing assignments",
		tenantBillingAssignmentColumns, whereSQL, orderSQL, args, limit, offset,
		func(row pgx.Row) (TenantBillingAssignmentRow, error) {
			item, err := scanTenantBillingAssignmentRow(row)
			if err != nil {
				return TenantBillingAssignmentRow{}, fmt.Errorf("scan tenant billing assignment: %w", err)
			}
			return item, nil
		})
}

// LockBillingAccountForAssignment serializes assignment and correlation-resolution attempts for one account for
// the lifetime of tx. It does not enforce the 1:1 invariant (the partial unique indexes do).
func LockBillingAccountForAssignment(ctx context.Context, tx Tx, accountID string) error {
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1, 1))`, accountID); err != nil {
		return fmt.Errorf("lock billing account for assignment: %w", err)
	}
	return nil
}

// BillingAccountCorrelationConflict reports whether value is already stored as another billing_accounts row's
// correlation_id, excluding excludeAccountID. It backs billing.LocalCorrelationLookup.
func BillingAccountCorrelationConflict(ctx context.Context, tx Tx, value, excludeAccountID string) (bool, error) {
	var conflict bool
	if err := tx.QueryRow(ctx, `
		select exists(select 1 from billing_accounts where correlation_id = $1 and id <> $2)`,
		value, excludeAccountID).Scan(&conflict); err != nil {
		return false, fmt.Errorf("check billing account correlation conflict: %w", err)
	}
	return conflict, nil
}

// ErrBillingAccountCorrelationConflict reports that the correlation id hit the unique index on
// billing_accounts.correlation_id. Callers map it to FAILED_PRECONDITION/ACCOUNT_CORRELATION_CONFLICT.
var ErrBillingAccountCorrelationConflict = errors.New("billing account correlation id already in use")

// SetBillingAccountCorrelationID commits the resolved correlation value to billing_accounts.correlation_id.
func SetBillingAccountCorrelationID(ctx context.Context, tx Tx, accountID, value string) error {
	if _, err := tx.Exec(ctx, `update billing_accounts set correlation_id = $1 where id = $2`,
		value, accountID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName == "billing_accounts_unique_correlation_id" {
			return ErrBillingAccountCorrelationConflict
		}
		return fmt.Errorf("set billing account correlation id: %w", err)
	}
	return nil
}

// BillingAccountForAssignment holds the internal fields needed to resolve and validate a tenant assignment: the
// provider-native reference, correlation id, and state.
type BillingAccountForAssignment struct {
	ID                       string
	ProviderAccountReference string
	CorrelationID            string
	State                    string
}

// GetBillingAccountForAssignment returns accountID's assignment-relevant fields, or a pgx.ErrNoRows-wrapping
// error when it does not exist.
func GetBillingAccountForAssignment(ctx context.Context, tx Tx, accountID string) (BillingAccountForAssignment, error) {
	var row BillingAccountForAssignment
	err := tx.QueryRow(ctx, `
		select id, provider_account_reference, coalesce(correlation_id, ''), state
		from billing_accounts where id = $1`, accountID).
		Scan(&row.ID, &row.ProviderAccountReference, &row.CorrelationID, &row.State)
	if err != nil {
		return BillingAccountForAssignment{}, fmt.Errorf("get billing account for assignment: %w", err)
	}
	return row, nil
}

// TenantLiveForAssignment reports whether a live, never deletion-marked tenants row exists for name. The
// tenant_billing_assignments FK alone cannot reject a deleted tenant: a deletion tombstone keeps the row so
// assignment history stays queryable, so the assign path rejects such tenants explicitly.
func TenantLiveForAssignment(ctx context.Context, tx Tx, name string) (bool, error) {
	var live bool
	err := tx.QueryRow(ctx, `
		select exists(
			select 1 from tenants
			where name = $1 and deletion_timestamp = 'epoch'
		)`, name).Scan(&live)
	if err != nil {
		return false, fmt.Errorf("check tenant live for assignment: %w", err)
	}
	return live, nil
}
