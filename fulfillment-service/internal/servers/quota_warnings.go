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
	"encoding/json"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
)

// quotaWarning is the payload of one outbox row.
type quotaWarning struct {
	Dimension string `json:"dimension"`
	Percent   int32  `json:"percent"`
	Usage     int64  `json:"usage"`
	Limit     int64  `json:"limit"`
}

// writeQuotaWarning writes one warning to the outbox, in the request's transaction. The
// notifications API (OSAC-75) does not exist yet, so warnings accumulate in the outbox by
// design; its publisher will drain the table.
func (e *quotaEnforcer) writeQuotaWarning(
	ctx context.Context, tenant, project, dimension string, percent int32, usage, limit int64,
) error {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return err
	}
	data, err := json.Marshal(quotaWarning{
		Dimension: dimension,
		Percent:   percent,
		Usage:     usage,
		Limit:     limit,
	})
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx,
		`insert into quota_outbox (tenant, project, dimension, data) values ($1, $2::ltree, $3, $4)`,
		tenant, project, dimension, data,
	); err != nil {
		return err
	}
	e.metrics.recordOutboxInsert()
	return nil
}

// atOrAbove tells whether the usage reached the threshold: the given percent of the limit. A
// limit of zero or less has no percentage to cross, so no usage is ever at or above it: without
// that, the zero usage of a dimension with no limit would fire every threshold at once. The
// comparison multiplies by one hundred instead of dividing, so it stays in whole numbers.
func atOrAbove(usage, limit int64, percent int32) bool {
	if limit <= 0 {
		return false
	}
	return usage*100 >= int64(percent)*limit
}

// warnCrossings writes one warning for each threshold a fitting request takes its scope from
// below to at or above. The scopes are the request's project chain and the tenant itself: a
// threshold belongs to a named project or to the whole tenant.
func (e *quotaEnforcer) warnCrossings(
	ctx context.Context,
	tenant string,
	usage map[string]map[string]int64,
	limits *quotaLimits,
	scopes []string,
	added map[string]int64,
) error {
	thresholds, err := e.readThresholds(ctx, tenant)
	if err != nil {
		return err
	}
	if len(thresholds) == 0 {
		return nil
	}
	for _, scope := range scopes {
		scopeThresholds := thresholds[scope]
		if len(scopeThresholds) == 0 {
			continue
		}
		scopeUsage := quotaSubtreeUsage(usage, scope)
		for dimension, percent := range scopeThresholds {
			if added[dimension] <= 0 {
				continue
			}
			limit := limits.effectiveLimitOf(scope, dimension)
			before := scopeUsage[dimension]
			after := before + added[dimension]
			if !atOrAbove(before, limit, percent) && atOrAbove(after, limit, percent) {
				if err = e.writeQuotaWarning(ctx, tenant, scope, dimension, percent, after, limit); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// warnThresholdChange writes a warning when setting a threshold puts its scope at or above
// it: the usage reached the new percent of the limit and was below the old one. A
// tenant-scoped threshold measures the tenant-total usage against the tenant's limit.
func (e *quotaEnforcer) warnThresholdChange(
	ctx context.Context, tenant, project, dimension string, oldPercent, newPercent int32,
) error {
	if tenant == "" || tenant == auth.SystemTenant || tenant == auth.SharedTenant {
		return nil
	}
	usage, err := e.counter.Count(ctx, tenant)
	if err != nil {
		return err
	}
	used := quotaSubtreeUsage(usage, project)[dimension]
	limits, err := e.readLimits(ctx, tenant)
	if err != nil {
		return err
	}
	limit := limits.effectiveLimitOf(project, dimension)
	if !atOrAbove(used, limit, newPercent) {
		return nil
	}
	if oldPercent > 0 && atOrAbove(used, limit, oldPercent) {
		return nil
	}
	return e.writeQuotaWarning(ctx, tenant, project, dimension, newPercent, used, limit)
}

// warnScopeAtOrAbove writes a warning for each threshold of the dimension, in the projects of
// the subtree, whose usage is at or above it. Only a change that tightens the scope's limit
// warns: the limit the change leaves behind is below the one it replaced. A limit change
// doesn't move usage or a threshold, so the warning reports the state the change leaves behind.
func (e *quotaEnforcer) warnScopeAtOrAbove(ctx context.Context, tenant, project, dimension string, oldLimit int64) error {
	if tenant == "" || tenant == auth.SystemTenant || tenant == auth.SharedTenant {
		return nil
	}
	thresholds, err := e.readThresholds(ctx, tenant)
	if err != nil {
		return err
	}
	scopes := thresholdScopes(thresholds, project, dimension)
	if len(scopes) == 0 {
		return nil
	}
	limits, err := e.readLimits(ctx, tenant)
	if err != nil {
		return err
	}
	if limits.limitOf(project, dimension) >= oldLimit {
		return nil
	}
	return e.warnThresholdsAtOrAbove(ctx, tenant, dimension, thresholds, scopes, limits)
}

// thresholdScopes returns the scopes holding a threshold of the dimension under a project: the
// project itself and the projects inside it, or every scope of the tenant when it is empty.
func thresholdScopes(thresholds map[string]map[string]int32, project, dimension string) []string {
	var scopes []string
	for thresholdProject, dimensions := range thresholds {
		if _, ok := dimensions[dimension]; !ok {
			continue
		}
		if project != "" && thresholdProject != project && !isDescendantProject(thresholdProject, project) {
			continue
		}
		scopes = append(scopes, thresholdProject)
	}
	return scopes
}

// warnThresholdsAtOrAbove writes one warning for each of the given threshold scopes whose usage
// is at or above the threshold, measured against the limit that binds the scope.
func (e *quotaEnforcer) warnThresholdsAtOrAbove(
	ctx context.Context, tenant, dimension string, thresholds map[string]map[string]int32,
	scopes []string, limits *quotaLimits,
) error {
	usage, err := e.counter.Count(ctx, tenant)
	if err != nil {
		return err
	}
	for _, scope := range scopes {
		percent := thresholds[scope][dimension]
		limit := limits.effectiveLimitOf(scope, dimension)
		used := quotaSubtreeUsage(usage, scope)[dimension]
		if !atOrAbove(used, limit, percent) {
			continue
		}
		if err = e.writeQuotaWarning(ctx, tenant, scope, dimension, percent, used, limit); err != nil {
			return err
		}
	}
	return nil
}

// warnCreatedLimit writes the warnings of a newly created limit. The scope's old limit is what
// it falls back to without a row: the tenant's limit for a project, the default for the tenant.
func (e *quotaEnforcer) warnCreatedLimit(ctx context.Context, tenant, project, dimension string) error {
	limits, err := e.readLimits(ctx, tenant)
	if err != nil {
		return err
	}
	oldLimit := resolveQuotaLimit(nil, limits.defaults, dimension)
	if project != "" {
		oldLimit = limits.limitOf("", dimension)
	}
	return e.warnScopeAtOrAbove(ctx, tenant, project, dimension, oldLimit)
}

// isDescendantProject reports whether the project sits inside the parent.
func isDescendantProject(project, parent string) bool {
	if parent == "" {
		return true
	}
	return project != parent && len(project) > len(parent)+1 &&
		project[:len(parent)] == parent && project[len(parent)] == '.'
}

// readThresholds returns the tenant's thresholds: for each project, the percent of each
// dimension's limit. A cleared threshold is deleted, so only rows without a deletion timestamp
// apply.
func (e *quotaEnforcer) readThresholds(ctx context.Context, tenant string) (map[string]map[string]int32, error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		select project::text, data -> 'spec' ->> 'dimension', coalesce((data -> 'spec' ->> 'percent')::int, 0)
		from quota_thresholds
		where tenant = $1 and deletion_timestamp = 'epoch'`, tenant,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]map[string]int32{}
	for rows.Next() {
		var project, dimension string
		var percent int32
		if err = rows.Scan(&project, &dimension, &percent); err != nil {
			return nil, err
		}
		if result[project] == nil {
			result[project] = map[string]int32{}
		}
		result[project][dimension] = percent
	}
	return result, rows.Err()
}

// warnTenantAfterDefaultChange warns a tenant whose effective limit the default change
// tightened. A tenant with its own override keeps its limit, and a project's own limit still
// caps the tenant's, so each threshold scope compares the limit that bound it before the change
// with the one the change leaves behind.
func (e *quotaEnforcer) warnTenantAfterDefaultChange(
	ctx context.Context, tenant, dimension string, oldDefault int64,
) error {
	if tenant == "" || tenant == auth.SystemTenant || tenant == auth.SharedTenant {
		return nil
	}
	limits, err := e.readLimits(ctx, tenant)
	if err != nil {
		return err
	}
	thresholds, err := e.readThresholds(ctx, tenant)
	if err != nil {
		return err
	}
	var tightened []string
	for _, scope := range thresholdScopes(thresholds, "", dimension) {
		before, found := limitBeforeDefault(limits, scope, dimension, oldDefault)
		after := limits.effectiveLimitOf(scope, dimension)
		// A scope no limit bound before had no percentage to cross, so the first limit that
		// covers it can leave its usage at or above a threshold; a scope a limit bound warns
		// only when the change lowers that limit.
		if after > 0 && (!found || after < before) {
			tightened = append(tightened, scope)
		}
	}
	if len(tightened) == 0 {
		return nil
	}
	return e.warnThresholdsAtOrAbove(ctx, tenant, dimension, thresholds, tightened, limits)
}

// limitBeforeDefault resolves the limit that bound a scope before a default change: the tenant's
// override, else the default the change replaces, tightened by the own limits of the scope and
// its named ancestors. The second result is false when nothing bound the scope: a dimension with
// no limit has no percentage to cross, so the first default that covers it is a new bound.
func limitBeforeDefault(
	limits *quotaLimits, scope, dimension string, oldDefault int64,
) (int64, bool) {
	limit, found := int64(0), false
	if override, ok := limits.overrides[dimension]; ok {
		limit, found = override, true
	} else if oldDefault > 0 {
		limit, found = oldDefault, true
	}
	for _, project := range quotaProjectChain(scope) {
		value, ok := limits.projects[project][dimension]
		if !ok {
			continue
		}
		if !found || value < limit {
			limit, found = value, true
		}
	}
	return limit, found
}

// listTenants returns every tenant except the built-in ones.
func (e *quotaEnforcer) listTenants(ctx context.Context) ([]string, error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx,
		`select name from tenants where name <> $1 and name <> $2`,
		auth.SystemTenant, auth.SharedTenant,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tenants []string
	for rows.Next() {
		var tenant string
		if err = rows.Scan(&tenant); err != nil {
			return nil, err
		}
		tenants = append(tenants, tenant)
	}
	return tenants, rows.Err()
}
