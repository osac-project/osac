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

// catalogInventoryCacheDefaultTTL is the fallback TTL used when a caller builds the inventory cache without resolving
// deployment tuning, as tests and hand-built servers do. Deployments get the resolved value from the command package,
// whose shared default is 300s and whose shipped chart override for the inventory cache is 30s. The value only bounds
// how long a List can keep reflecting a catalog state older than the last sweep, so it stays short.
const catalogInventoryCacheDefaultTTL = 30 * time.Second

// catalogInventoryCacheFillBudget caps how long one detached inventory fill may run. newTTLCache clamps it to at most
// half the configured TTL, so a deployment TTL below 30s shrinks it to ttl/2 and a fill that completes within budget
// is never born expired.
const catalogInventoryCacheFillBudget = 15 * time.Second

// catalogInventoryKey is the fixed key of the single aggregated inventory entry: every List of one server instance
// reads the same inventory, whatever the caller's request parameters.
type catalogInventoryKey struct{}

// newCatalogInventoryCache builds the single-entry inventory cache for a TTL configured through the service's chart
// values and environment. The fill budget is a fixed 15s cap that newTTLCache clamps to at most half the configured
// TTL, so a fill that completes within budget is never stored already expired. One cache belongs to one server
// instance: each of the two pricing servers sweeps with its own tenancy-scoped DAOs, so within one TTL window a mixed
// load sweeps the catalog tables at most once per server rather than once per request. Both servers' Lists are
// admin-only (total visibility), which their authorization DescribeTables pin, so a cached sweep is never served
// across visibility scopes; the cached rows are shared read-only among that server's callers.
func newCatalogInventoryCache(ttl time.Duration) *ttlCache[catalogInventoryKey, []*privatev1.CatalogPricingInput] {
	return newTTLCache[catalogInventoryKey, []*privatev1.CatalogPricingInput](ttl, catalogInventoryCacheFillBudget, 1,
		func(catalogInventoryKey) string { return "catalog-inventory" })
}
