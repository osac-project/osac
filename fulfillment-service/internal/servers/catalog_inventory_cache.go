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
	"time"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// catalogInventoryCacheTTL is how long one successful sweep of both catalog tables is reused before the next List
// re-reads them. Unlike the provider rate-card cache there is no chart-level tuning default to align with; the value
// only bounds how long a List can keep reflecting a catalog state older than the last sweep, so it stays short.
const catalogInventoryCacheTTL = 30 * time.Second

// catalogInventoryKey is the fixed key of the single aggregated inventory entry: every List of one server instance
// reads the same inventory, whatever the caller's request parameters.
type catalogInventoryKey struct{}

// newCatalogInventoryCache builds the single-entry inventory cache. One cache belongs to one server instance: each
// of the two pricing servers sweeps with its own tenancy-scoped DAOs, so within one TTL window a mixed load sweeps
// the catalog tables at most once per server rather than once per request. Both servers' Lists are admin-only
// (total visibility), which their authorization DescribeTables pin, so a cached sweep is never served across
// visibility scopes; the cached rows are shared read-only among that server's callers.
func newCatalogInventoryCache() *ttlCache[catalogInventoryKey, []*privatev1.CatalogPricingInput] {
	return newTTLCache[catalogInventoryKey, []*privatev1.CatalogPricingInput](catalogInventoryCacheTTL, 1,
		func(catalogInventoryKey) string { return "catalog-inventory" })
}
