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
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/quotausage"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	quota "github.com/osac-project/osac/proto/quota"
)

// quotaExceededReason is the ErrorInfo reason of a refused request.
const quotaExceededReason = "QUOTA_EXCEEDED"

// quotaEnforcer checks each create and update of a quota-annotated kind against the tenant's
// limits, inside the request's transaction, after the private server filled in the request. The
// check and the write share the transaction, so a request over a limit writes nothing.
//
// The enforcer names no kind: the checked kinds, their price and the spec fields that trigger the
// update check all come from the quota registry (proto/quota). The built-in shared and system
// tenants hold the provider's own records and are never checked.
type quotaEnforcer struct {
	logger      *slog.Logger
	counter     *quotausage.Counter
	defaults    *quotaDefaultLimitsStore
	checked     map[string]bool
	specPaths   map[string][]string
	kindTables  map[string]string
	kindMessage map[string]protoreflect.FullName
	metrics     *quotaMetrics
}

// newQuotaEnforcer builds the enforcer. It fails when the quota registry or the usage counter
// can't be built, so a broken annotation is a startup failure.
func newQuotaEnforcer(logger *slog.Logger, registerer prometheus.Registerer) (result *quotaEnforcer, err error) {
	if logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	counter, err := quotausage.NewCounter().
		SetLogger(logger).
		Build()
	if err != nil {
		return nil, err
	}
	defaults, err := newQuotaDefaultLimitsStore(logger)
	if err != nil {
		return nil, err
	}
	metrics, err := newQuotaMetrics(registerer)
	if err != nil {
		return nil, err
	}
	kinds, err := quota.Kinds()
	if err != nil {
		return nil, fmt.Errorf("failed to load the quota registry: %w", err)
	}
	// A type is a kind other kinds resolve through; it counts toward no limit, so creates and
	// updates of a type are never checked:
	types := map[string]bool{}
	for _, kind := range kinds {
		for _, field := range kind.Fields {
			if field.Through != "" {
				types[field.Through] = true
			}
		}
	}
	result = &quotaEnforcer{
		logger:      logger,
		counter:     counter,
		defaults:    defaults,
		checked:     map[string]bool{},
		specPaths:   map[string][]string{},
		kindTables:  map[string]string{},
		kindMessage: map[string]protoreflect.FullName{},
		metrics:     metrics,
	}
	for i := range kinds {
		kind := kinds[i]
		result.kindMessage[kind.Name] = kind.Message
		result.kindTables[kind.Name] = quotausage.Table(kind.Message)
		if types[kind.Name] {
			continue
		}
		result.checked[kind.Name] = true
		result.specPaths[kind.Name] = quotaSpecPaths(&kind)
	}
	return result, nil
}

// quotaSpecPaths returns the spec field paths of the kind that its annotations read: the
// annotated fields themselves and the brace paths of their dimensions, such as
// "spec.storage_tier" from "storage.{spec.storage_tier}.gib".
func quotaSpecPaths(kind *quota.Kind) []string {
	var paths []string
	add := func(path string) {
		if strings.HasPrefix(path, "spec.") {
			paths = append(paths, path)
		}
	}
	for _, field := range kind.Fields {
		add(field.Path)
		if field.Dimension == "" {
			continue
		}
		bracePaths, err := quota.BracePaths(field.Dimension)
		if err != nil {
			continue
		}
		for _, path := range bracePaths {
			add(path)
		}
	}
	return paths
}

// quotaKindName returns the kind's short name, such as "ComputeInstance".
func quotaKindName(message proto.Message) string {
	return string(message.ProtoReflect().Descriptor().FullName().Name())
}

// Checks reports whether the message's kind is checked: annotated, and not a type.
func (e *quotaEnforcer) Checks(message proto.Message) bool {
	return e.checked[quotaKindName(message)]
}

// updateChecked reports whether an update of the message's kind runs the check: a checked kind,
// and either no field mask or a mask that names a spec field the annotations read. Status-only
// updates never run the check.
func (e *quotaEnforcer) updateChecked(message proto.Message, mask *fieldmaskpb.FieldMask) bool {
	name := quotaKindName(message)
	if !e.checked[name] {
		return false
	}
	if mask == nil || len(mask.GetPaths()) == 0 {
		return true
	}
	for _, path := range mask.GetPaths() {
		for _, annotated := range e.specPaths[name] {
			if quotaPathCovers(path, annotated) {
				return true
			}
		}
	}
	return false
}

// quotaPathCovers reports whether a mask path and an annotated path name the same field or one
// names a field inside the other, compared segment by segment so a mask of "spec" covers every
// spec field and a node set key sits inside "spec.node_sets".
func quotaPathCovers(maskPath, annotated string) bool {
	maskSegments := strings.Split(maskPath, ".")
	annotatedSegments := strings.Split(annotated, ".")
	count := min(len(maskSegments), len(annotatedSegments))
	for i := range count {
		if maskSegments[i] != annotatedSegments[i] {
			return false
		}
	}
	return true
}

// lockTenant locks the tenant's row before any other row of the request, so two concurrent
// requests in one tenant never both pass the check: the second waits for the lock and then
// counts the first, at READ COMMITTED. FOR NO KEY UPDATE doesn't block the foreign-key checks
// on the tenant's row, so inserts that don't take the lock don't wait for it.
func (e *quotaEnforcer) lockTenant(ctx context.Context, tenant string) error {
	if tenant == "" || tenant == auth.SystemTenant || tenant == auth.SharedTenant {
		return nil
	}
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return err
	}
	lockStart := time.Now()
	var deleting bool
	err = tx.QueryRow(ctx,
		`select deletion_timestamp <> 'epoch' from tenants where id = $1 for no key update`, tenant,
	).Scan(&deleting)
	e.metrics.observeLockWait(time.Since(lockStart))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == pgerrcode.DeadlockDetected {
			return grpcstatus.Error(grpccodes.Aborted, "concurrent modification detected, please retry")
		}
		return grpcstatus.Errorf(grpccodes.Internal, "failed to lock tenant '%s'", tenant)
	}
	if deleting {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "tenant '%s' is being deleted", tenant)
	}
	return nil
}

// lockTenantOfRow locks the tenant row of the stored object with the given identifier, for a
// checked update: the tenant never changes, so an unlocked read finds it, and the lock comes
// before the resource's own row. A missing row locks nothing; the update fails NotFound later.
func (e *quotaEnforcer) lockTenantOfRow(ctx context.Context, object proto.Message, id string) error {
	if id == "" {
		return nil
	}
	table := e.kindTables[quotaKindName(object)]
	if table == "" {
		return nil
	}
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return err
	}
	lockStart := time.Now()
	var deleting bool
	err = tx.QueryRow(ctx, fmt.Sprintf(
		`select deletion_timestamp <> 'epoch' from tenants
		where id = (select tenant from %s where id = $1) for no key update`, table), id,
	).Scan(&deleting)
	e.metrics.observeLockWait(time.Since(lockStart))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == pgerrcode.DeadlockDetected {
			return grpcstatus.Error(grpccodes.Aborted, "concurrent modification detected, please retry")
		}
		return grpcstatus.Errorf(grpccodes.Internal, "failed to lock the tenant of '%s'", id)
	}
	if deleting {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"the tenant of '%s' is being deleted", id)
	}
	return nil
}

// checkCreate refuses a create that would take one of its scopes over a limit.
func (e *quotaEnforcer) checkCreate(ctx context.Context, candidate proto.Message) error {
	checkStart := time.Now()
	err := e.check(ctx, nil, candidate)
	e.metrics.observeCheck(time.Since(checkStart))
	return err
}

// checkUpdate refuses an update that would take one of its scopes over a limit. The update adds
// the positive difference between the new and the old version of the resource, so a resize down
// frees no quota until the resource runs smaller.
func (e *quotaEnforcer) checkUpdate(ctx context.Context, current, candidate proto.Message) error {
	checkStart := time.Now()
	err := e.check(ctx, current, candidate)
	e.metrics.observeCheck(time.Since(checkStart))
	return err
}

// check prices the candidate, counts the tenant's usage from the database and compares each
// dimension the request adds to against the limits of the request's project, each ancestor
// project and the tenant. A request that adds nothing to a dimension fits even at a limit of 0.
func (e *quotaEnforcer) check(
	ctx context.Context, current, candidate proto.Message,
) error {
	metadata := metadataOf(candidate)
	if metadata == nil {
		return nil
	}
	tenant := metadata.GetTenant()
	if tenant == "" || tenant == auth.SystemTenant || tenant == auth.SharedTenant {
		return nil
	}
	project := metadata.GetProject()

	lookup := &quotaTypeLookup{ctx: ctx, tenant: tenant, enforcer: e}
	candidateQuote, err := quota.Price(candidate.ProtoReflect(), lookup)
	if lookup.failure != nil {
		return e.internalCheckError(ctx, tenant, lookup.failure)
	}
	if err != nil {
		return e.internalCheckError(ctx, tenant, err)
	}

	added := map[string]int64{}
	if current == nil {
		for dimension, amount := range candidateQuote.Dimensions {
			if amount > 0 {
				added[dimension] = amount
			}
		}
	} else {
		currentQuote, err := quota.Price(current.ProtoReflect(), lookup)
		if lookup.failure != nil {
			return e.internalCheckError(ctx, tenant, lookup.failure)
		}
		if err != nil {
			return e.internalCheckError(ctx, tenant, err)
		}
		for dimension, amount := range candidateQuote.Dimensions {
			delta := amount - currentQuote.Dimensions[dimension]
			if delta > 0 {
				added[dimension] = delta
			}
		}
	}
	if len(added) == 0 {
		return nil
	}

	usage, err := e.counter.Count(ctx, tenant)
	if err != nil {
		return e.internalCheckError(ctx, tenant, err)
	}
	state, err := e.readLimits(ctx, tenant)
	if err != nil {
		return e.internalCheckError(ctx, tenant, err)
	}

	scopes := quotaProjectChain(project)
	for i, scope := range scopes {
		scopeUsage := quotaSubtreeUsage(usage, scope)
		for _, dimension := range quotaSortedDimensions(added) {
			limit := state.limitOf(scope, dimension)
			if scopeUsage[dimension]+added[dimension] > limit {
				return e.refuse(tenant, scope, i, dimension, limit, scopeUsage[dimension],
					added[dimension], candidateQuote.Sources[dimension])
			}
		}
	}
	tenantUsage := quotaSubtreeUsage(usage, "")
	for _, dimension := range quotaSortedDimensions(added) {
		limit := state.limitOf("", dimension)
		if tenantUsage[dimension]+added[dimension] > limit {
			return e.refuse(tenant, "", -1, dimension, limit, tenantUsage[dimension],
				added[dimension], candidateQuote.Sources[dimension])
		}
	}
	if err = e.warnCrossings(ctx, tenant, usage, state, scopes, added); err != nil {
		return e.internalCheckError(ctx, tenant, err)
	}
	return nil
}

// internalCheckError hides the failure's detail from the caller and keeps it in the log.
func (e *quotaEnforcer) internalCheckError(ctx context.Context, tenant string, err error) error {
	e.logger.ErrorContext(ctx, "Failed to check the quota",
		slog.String("tenant", tenant),
		slog.Any("error", err))
	return grpcstatus.Errorf(grpccodes.Internal, "failed to check the quota")
}

// refuse builds the FAILED_PRECONDITION error with the ErrorInfo reason and the QuotaFailure
// detail that name the failing scope, the dimension, the limit, the usage and what the request
// adds. scopeIndex -1 names the tenant, 0 the request's project, above that an ancestor project.
func (e *quotaEnforcer) refuse(
	tenant, project string, scopeIndex int, dimension string,
	limit, usage, added int64, sources []string,
) error {
	e.metrics.recordRefusal(dimension)
	scope := "project"
	if scopeIndex < 0 {
		scope = "tenant"
	} else if scopeIndex > 0 {
		scope = "ancestor_project"
	}
	scopeName := project
	if scopeName == "" {
		scopeName = "default project"
	}
	subject := fmt.Sprintf("project %s", scopeName)
	if scope == "tenant" {
		subject = fmt.Sprintf("tenant %s", tenant)
	}
	status := grpcstatus.New(grpccodes.FailedPrecondition, fmt.Sprintf(
		"quota exceeded: %s, %s: limit %d, usage %d, adding %d",
		subject, dimension, limit, usage, added,
	))
	detailed, err := status.WithDetails(
		&errdetails.ErrorInfo{Reason: quotaExceededReason, Domain: "osac"},
		publicv1.QuotaFailure_builder{
			Scope:     scope,
			Tenant:    tenant,
			Project:   project,
			Dimension: dimension,
			Limit:     limit,
			Usage:     usage,
			Added:     added,
			Sources:   sources,
		}.Build(),
	)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.Internal, "failed to attach the quota failure detail: %v", err)
	}
	return detailed.Err()
}

// quotaLimits holds the tenant's limits: its overrides, its projects' limits and the defaults.
type quotaLimits struct {
	defaults  map[string]int64
	overrides map[string]int64
	projects  map[string]map[string]int64
}

// limitOf resolves the limit of a dimension at a scope: the project's own limit when one
// exists, else the tenant's override, the default, 0.
func (l *quotaLimits) limitOf(scope, dimension string) int64 {
	if project, ok := l.projects[scope]; ok {
		if value, ok := project[dimension]; ok {
			return value
		}
	}
	return resolveQuotaLimit(l.overrides, l.defaults, dimension)
}

// readLimits reads the tenant's overrides, its projects' limits and the default limits, in the
// request's transaction. A cleared limit is deleted, so only rows without a deletion timestamp
// apply. It reads the limits directly rather than through the DAO because the DAO applies caller
// visibility, which the check must not: it always sees all of the tenant's limits.
func (e *quotaEnforcer) readLimits(ctx context.Context, tenant string) (result *quotaLimits, err error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, err
	}
	result = &quotaLimits{
		defaults:  map[string]int64{},
		overrides: map[string]int64{},
		projects:  map[string]map[string]int64{},
	}
	result.defaults, err = e.defaults.values(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		select project::text, data -> 'spec' ->> 'dimension', (data -> 'spec' ->> 'value')::bigint
		from quota_limits
		where tenant = $1 and deletion_timestamp = 'epoch'`, tenant,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var project, dimension string
		var value int64
		if err = rows.Scan(&project, &dimension, &value); err != nil {
			return nil, err
		}
		if project == "" {
			result.overrides[dimension] = value
			continue
		}
		if result.projects[project] == nil {
			result.projects[project] = map[string]int64{}
		}
		result.projects[project][dimension] = value
	}
	return result, rows.Err()
}

// quotaProjectChain returns the project and each of its named ancestors, most specific
// first. The default project has no name of its own: a limit on it is the tenant's override,
// so the tenant check covers it and the chain of a request in the default project is empty.
func quotaProjectChain(project string) []string {
	if project == "" {
		return nil
	}
	segments := strings.Split(project, ".")
	chain := make([]string, 0, len(segments))
	for i := len(segments); i >= 1; i-- {
		chain = append(chain, strings.Join(segments[:i], "."))
	}
	return chain
}

// quotaSubtreeUsage sums the usage of the scope's project and the projects inside it. The
// default project is the root of the tree, so an empty scope sums the whole tenant.
func quotaSubtreeUsage(usage map[string]map[string]int64, scope string) map[string]int64 {
	result := map[string]int64{}
	for project, dimensions := range usage {
		if scope != "" && project != scope && !strings.HasPrefix(project, scope+".") {
			continue
		}
		for dimension, amount := range dimensions {
			result[dimension] += amount
		}
	}
	return result
}

// quotaSortedDimensions returns the dimensions of the amounts, sorted, so the first refusal a
// caller sees is stable.
func quotaSortedDimensions(amounts map[string]int64) []string {
	dimensions := make([]string, 0, len(amounts))
	for dimension := range amounts {
		dimensions = append(dimensions, dimension)
	}
	sort.Strings(dimensions)
	return dimensions
}
