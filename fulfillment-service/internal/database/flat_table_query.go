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

	"github.com/jackc/pgx/v5"
)

// ErrInvalidFlatTableQuery reports a malformed filter or order expression in a flat-table list call. Callers map
// it to INVALID_ARGUMENT; every other error from those functions is a database failure and maps to INTERNAL.
type ErrInvalidFlatTableQuery struct{ error }

func invalidFlatTableQueryf(format string, args ...any) error {
	return &ErrInvalidFlatTableQuery{fmt.Errorf(format, args...)}
}

// flatTableDefaultLimit and flatTableMaxLimit bound flat-table list pages: an unset limit serves the default page
// rather than the whole table, and an explicit limit is clamped rather than rejected. The assignment table is
// append-only history that is never pruned, so an unbounded list would materialize it in full.
//
// This keeps the platform convention shared with the generic DAO: 0 (proto3's unset value) and an omitted limit
// both serve 100, and a limit above the maximum clamps to 1000.
const (
	flatTableDefaultLimit = 100
	flatTableMaxLimit     = 1000
)

// normalizeFlatTableLimit applies flatTableDefaultLimit and flatTableMaxLimit to a caller-supplied limit.
func normalizeFlatTableLimit(limit int32) int32 {
	if limit == 0 {
		return flatTableDefaultLimit
	}
	if limit > flatTableMaxLimit {
		return flatTableMaxLimit
	}
	return limit
}

// flatTableOrderPattern matches "<field>" or "<field> asc|desc" - the order grammar shared by the flat billing
// tables.
//
// shortcut: order supports only a single field, not a comma-separated list.
var flatTableOrderPattern = regexp.MustCompile(`^([a-z_]+)(?: (asc|desc))?$`)

// translateFlatTableFilter splits filter on "&&", translates each trimmed clause with parseClause, and joins the
// fragments with " and ". parseClause passes literal values to addArg and splices the "$<n>" placeholder into its
// fragment. An empty filter yields "", nil, nil.
func translateFlatTableFilter(
	filter string,
	parseClause func(clause string, addArg func(any) string) (string, error),
) (sql string, args []any, err error) {
	if filter == "" {
		return "", nil, nil
	}
	var clauses []string
	for _, clause := range strings.Split(filter, "&&") {
		clause = strings.TrimSpace(clause)
		fragment, err := parseClause(clause, func(value any) string {
			args = append(args, value)
			return fmt.Sprintf("$%d", len(args))
		})
		if err != nil {
			return "", nil, err
		}
		clauses = append(clauses, fragment)
	}
	return strings.Join(clauses, " and "), args, nil
}

// translateFlatTableOrder translates an order expression into a SQL ORDER BY column (with optional asc/desc)
// over columns, defaulting to defaultOrder for an empty order.
func translateFlatTableOrder(order string, columns map[string]string, defaultOrder string) (string, error) {
	if order == "" {
		return defaultOrder, nil
	}
	match := flatTableOrderPattern.FindStringSubmatch(strings.TrimSpace(order))
	if match == nil {
		return "", invalidFlatTableQueryf(
			`unsupported order expression %q: only "<field>" or "<field> asc|desc" is supported`, order)
	}
	column, ok := columns[match[1]]
	if !ok {
		return "", invalidFlatTableQueryf("unknown order field %q", match[1])
	}
	if match[2] != "" {
		return column + " " + match[2], nil
	}
	return column, nil
}

// withIDTieBreaker appends ", id" unless the ordering is already on id alone, making any partial ordering total so
// offset/limit pagination cannot repeat or skip rows across pages. It preserves the primary order and direction.
// orderSQL is already allowlisted, so appending the literal ", id" needs no further validation.
func withIDTieBreaker(orderSQL string) string {
	switch orderSQL {
	case "id", "id asc", "id desc":
		return orderSQL
	}
	return orderSQL + ", id"
}

// listFlatTable counts the rows matching whereSQL/args, then selects selectColumns from table ordered by orderSQL
// with the normalized limit and offset, scanning each row with scanRow. label names the table in error messages;
// both billing list functions share this query so their page bounds cannot drift apart.
func listFlatTable[T any](ctx context.Context, tx Tx, table, label, selectColumns, whereSQL, orderSQL string,
	args []any, limit, offset int32, scanRow func(pgx.Row) (T, error)) (items []T, total int32, err error) {
	// Reject negative page bounds before SQL: they are a client mistake that callers surface as INVALID_ARGUMENT,
	// while Postgres would answer with an error surfaced as an opaque INTERNAL.
	if limit < 0 {
		return nil, 0, invalidFlatTableQueryf("limit must not be negative")
	}
	if offset < 0 {
		return nil, 0, invalidFlatTableQueryf("offset must not be negative")
	}
	where := ""
	if whereSQL != "" {
		where = " where " + whereSQL
	}
	if err = tx.QueryRow(ctx, "select count(*) from "+table+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count %s: %w", label, err)
	}
	args = append(args, normalizeFlatTableLimit(limit), offset)
	rows, err := tx.Query(ctx, fmt.Sprintf(`
		select %s
		from %s%s
		order by %s
		limit $%d offset $%d`, selectColumns, table, where, orderSQL, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list %s: %w", label, err)
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanRow(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list %s: %w", label, err)
	}
	return items, total, nil
}
