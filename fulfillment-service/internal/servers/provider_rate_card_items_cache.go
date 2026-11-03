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
	"fmt"
	"time"

	"github.com/osac-project/osac/fulfillment-service/internal/billing/pricing"
)

// rateCardCacheTTL is how long one successful provider read of a service's rate card is reused before the next List
// re-reads it. It matches the chart's documented global.billing.tuning.cacheFreshTtlSeconds default; reading that
// value (and the other cache tuning env vars) from the environment is deliberately left to OSAC-6894, which owns
// the tuning knobs, so this stays a constant here.
const rateCardCacheTTL = 5 * time.Minute

// rateCardCacheMaxEntries bounds the cache. Keys come from request parameters (service, currency), so without a
// bound a client cycling through arbitrary names could grow it without limit.
const rateCardCacheMaxEntries = 1024

// rateCardKey identifies one provider read: the OSAC service name and the currency its rate card is read in.
type rateCardKey struct {
	service  string
	currency string
}

// rateCardEntry is one cached, successful read. The slice and map are shared with every caller served from the
// entry, so callers must treat them as read-only.
type rateCardEntry struct {
	items   []pricing.RateCardItem
	skipped map[string]int
}

// newRateCardCache builds the rate-card cache over the shared ttlCache skeleton, with the default TTL and capacity.
func newRateCardCache() *ttlCache[rateCardKey, rateCardEntry] {
	return newTTLCache[rateCardKey, rateCardEntry](rateCardCacheTTL, rateCardCacheMaxEntries, flightKey)
}

// flightKey encodes key as an injective singleflight string: %q escapes any character that could otherwise let two
// different keys collide on the same string.
func flightKey(key rateCardKey) string {
	return fmt.Sprintf("%q\x00%q", key.service, key.currency)
}
