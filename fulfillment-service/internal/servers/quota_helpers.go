/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/utils"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// fnv32a returns the FNV-32a hash of the value, printed as hex.
func fnv32a(value string) uint32 {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(value))
	return hash.Sum32()
}

// deleteQuotaOutboxRows removes the unsent quota warnings the SQL matches, inside the context's
// transaction. The outbox rows reference tenants and projects with foreign keys and have no API of
// their own, so each Delete path removes its own rows there: leaving them behind would keep the
// parent from being archived after its finalizers are gone. The message names the parent in the
// Internal error a failure returns; the attrs enter the failure logs.
func deleteQuotaOutboxRows(
	ctx context.Context,
	logger *slog.Logger,
	sql string,
	args []any,
	message string,
	attrs ...slog.Attr,
) error {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "Failed to get transaction for quota outbox deletion",
			append(slices.Clone(attrs), slog.Any("error", err))...)
		return grpcstatus.Error(grpccodes.Internal, message)
	}
	_, err = tx.Exec(ctx, sql, args...)
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "Failed to delete quota outbox rows",
			append(slices.Clone(attrs), slog.Any("error", err))...)
		return grpcstatus.Error(grpccodes.Internal, message)
	}
	return nil
}

// quotaNameFromDimension converts a dimension to a DNS-1123 name: lowercased, non-alphanumerics
// dashed, trimmed, and suffixed with a short hash of the original dimension so two dimensions that
// sanitize to the same base still get different names.
func quotaNameFromDimension(dimension string) string {
	base := utils.SanitizeDNS1123Name(dimension, 54, nil)
	if base == "" {
		// A dimension of only punctuation sanitizes to nothing; a name still needs a
		// valid label, so the base falls back to the kind of object it names.
		base = "quota"
	}
	return fmt.Sprintf("%s-%x", base, fnv32a(dimension))
}

// isUniqueViolation tells whether the error is PostgreSQL's unique-violation error
// (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code == pgerrcode.UniqueViolation
	}
	return false
}

// isAlreadyExists tells whether the error says a row already holds the object's unique scope:
// the DAO translates a unique violation to its own error, so both shapes mean the same race.
func isAlreadyExists(err error) bool {
	if _, ok := errors.AsType[*dao.ErrAlreadyExists](err); ok {
		return true
	}
	return isUniqueViolation(err)
}

// rejectQuotaNameChange refuses a fresh object whose name differs from the stored row's, so a
// caller cannot learn one name back after sending another.
func rejectQuotaNameChange(existing, fresh quotaNamedObject) error {
	if fresh.GetMetadata().GetName() != existing.GetMetadata().GetName() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'metadata.name' is immutable and cannot be changed from '%s' to '%s'",
			existing.GetMetadata().GetName(), fresh.GetMetadata().GetName())
	}
	return nil
}

// upsertQuotaLimit writes the fresh object when no row matches the filter, and copies the
// fresh object's spec onto the matching row otherwise. A lost insert race recovers through a
// savepoint: the DAO translates the unique violation to its own error and records it in the
// transaction, which would roll the whole request back, so the insert runs in a savepoint of
// its own; the loser rolls back only its insert, re-reads the winner's row and updates it.
// A fresh name that differs from the stored one is refused, so a caller cannot learn one name
// back after sending another, and a name another dimension already holds reads as
// AlreadyExists, not as the internal error of a lost scope.
func upsertQuotaLimit[O quotaNamedObject](
	ctx context.Context,
	guard *dao.GenericDAO[O],
	filter string,
	fresh O,
	merge func(existing, fresh O) O,
) (result O, err error) {
	response, err := guard.List().
		SetFilter(filter).
		SetLimit(1).
		Do(ctx)
	if err != nil {
		return fresh, err
	}
	if len(response.GetItems()) > 0 {
		existing := response.GetItems()[0]
		if err := rejectQuotaNameChange(existing, fresh); err != nil {
			return fresh, err
		}
		update, err := guard.Update().SetObject(merge(existing, fresh)).Do(ctx)
		if err != nil {
			return fresh, err
		}
		return update.GetObject(), nil
	}
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return fresh, err
	}
	insertErr := tx.Savepoint(ctx, func(spCtx context.Context) error {
		created, createErr := guard.Create().SetObject(fresh).Do(spCtx)
		if createErr != nil {
			return createErr
		}
		result = created.GetObject()
		return nil
	})
	if insertErr == nil {
		return result, nil
	}
	if !isAlreadyExists(insertErr) {
		return fresh, insertErr
	}
	response, err = guard.List().
		SetFilter(filter).
		SetLimit(1).
		Do(ctx)
	if err != nil {
		return fresh, err
	}
	if len(response.GetItems()) == 0 {
		// The insert lost its unique scope without the scope's row appearing: another
		// dimension's row holds the name. Say so instead of an internal error:
		byName, nameErr := guard.List().
			SetFilter(fmt.Sprintf(
				"this.metadata.name == %s && this.metadata.tenant == %s && this.metadata.project == %s",
				strconv.Quote(fresh.GetMetadata().GetName()),
				strconv.Quote(fresh.GetMetadata().GetTenant()),
				strconv.Quote(fresh.GetMetadata().GetProject()),
			)).
			SetLimit(1).
			Do(ctx)
		if nameErr == nil && len(byName.GetItems()) > 0 {
			return fresh, grpcstatus.Errorf(grpccodes.AlreadyExists,
				"the name '%s' is already used by another limit", fresh.GetMetadata().GetName())
		}
		return fresh, fmt.Errorf("the limit row vanished from its unique scope")
	}
	existing := response.GetItems()[0]
	if err := rejectQuotaNameChange(existing, fresh); err != nil {
		return fresh, err
	}
	update, err := guard.Update().SetObject(merge(existing, fresh)).Do(ctx)
	if err != nil {
		return fresh, err
	}
	return update.GetObject(), nil
}

// quotaDefaultValues returns the default limit of every dimension, from the live rows of
// the shared tenant.
func quotaDefaultValues(
	ctx context.Context, guard *dao.GenericDAO[*privatev1.QuotaDefaultLimit],
) (map[string]int64, error) {
	items, err := listAllRows(ctx, guard, fmt.Sprintf(
		"this.metadata.tenant == %s", strconv.Quote(auth.SharedTenant)))
	if err != nil {
		return nil, err
	}
	result := map[string]int64{}
	for _, item := range items {
		result[item.GetSpec().GetDimension()] = item.GetSpec().GetValue()
	}
	return result, nil
}

// readQuotaLimits returns the tenant's overrides and each project's limits, reading every row the
// tenant holds, in pages.
func readQuotaLimits(
	ctx context.Context, limits *dao.GenericDAO[*privatev1.QuotaLimit], logger *slog.Logger, tenant string,
) (overrides map[string]int64, projects map[string]map[string]int64, err error) {
	items, err := listAllRows(ctx, limits, fmt.Sprintf("this.metadata.tenant == %s", strconv.Quote(tenant)))
	if err != nil {
		logger.ErrorContext(ctx, "Failed to read the quota limits",
			slog.String("tenant", tenant),
			slog.Any("error", err))
		return nil, nil, grpcstatus.Errorf(grpccodes.Internal, "failed to read the quota limits")
	}
	overrides = map[string]int64{}
	projects = map[string]map[string]int64{}
	for _, limit := range items {
		project := limit.GetMetadata().GetProject()
		if project == "" {
			overrides[limit.GetSpec().GetDimension()] = limit.GetSpec().GetValue()
			continue
		}
		if projects[project] == nil {
			projects[project] = map[string]int64{}
		}
		projects[project][limit.GetSpec().GetDimension()] = limit.GetSpec().GetValue()
	}
	return overrides, projects, nil
}

// resolveQuotaLimitAt resolves the limit of a dimension at one scope: the scope's own limit,
// else the nearest ancestor project's, else the tenant's override, else the default. The second
// result reports whether a limit exists at all, which is not the same as a limit of zero.
func resolveQuotaLimitAt(
	overrides, defaults map[string]int64, projects map[string]map[string]int64, project, dimension string,
) (int64, bool) {
	for scope := project; ; scope = parentProject(scope) {
		if value, ok := projects[scope][dimension]; ok {
			return value, true
		}
		if scope == "" {
			break
		}
	}
	if value, ok := overrides[dimension]; ok {
		return value, true
	}
	if value, ok := defaults[dimension]; ok {
		return value, true
	}
	return 0, false
}

// parentProject returns the project above the given one: "team.web" has "team", and "team" has
// the tenant's own scope, the empty name.
func parentProject(project string) string {
	index := strings.LastIndex(project, ".")
	if index < 0 {
		return ""
	}
	return project[:index]
}
