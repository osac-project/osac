/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package grpcserver

import (
	"log/slog"
	"math"
	"strconv"
	"time"
)

const (
	// catalogCacheDefaultSecondsEnv is the shared refresh interval, in seconds, of every catalog-pricing cache that
	// has no override of its own.
	catalogCacheDefaultSecondsEnv = "OSAC_CATALOG_CACHE_DEFAULT_SECONDS"

	// catalogCacheInventorySecondsEnv overrides the shared interval for the catalog inventory caches.
	catalogCacheInventorySecondsEnv = "OSAC_CATALOG_CACHE_INVENTORY_SECONDS"

	// catalogCacheRateCardSecondsEnv overrides the shared interval for the provider rate-card cache.
	catalogCacheRateCardSecondsEnv = "OSAC_CATALOG_CACHE_RATE_CARD_SECONDS"

	// defaultCatalogCacheInterval is the shared refresh interval when the deployment configures none. It matches the
	// chart's service.tuning.cache.defaultSeconds value.
	defaultCatalogCacheInterval = 300 * time.Second

	// maxCatalogCacheSeconds is the largest whole-second value that still fits the duration the caches use. A larger
	// value would wrap into a negative or tiny interval and make every cache read fail, so it counts as unusable.
	maxCatalogCacheSeconds = math.MaxInt64 / int64(time.Second)
)

// catalogCacheTTLs holds the resolved refresh intervals of the catalog-pricing caches. They are read at startup
// whatever the billing state, because the catalog inventory cache answers CatalogPricingInputs.List with no provider
// configured; only the rate-card cache needs one.
type catalogCacheTTLs struct {
	inventory time.Duration
	rateCard  time.Duration
}

// resolveCatalogCacheTTLs reads the three cache tuning variables through getenv. Each resolves to its own override
// when set and positive, else to the shared default, which itself falls back to defaultCatalogCacheInterval. A
// malformed, zero or negative value is a tuning mistake, not a reason to refuse to serve: it is logged as a warning
// and the variable behaves as if it were unset, so the API stays available with the default interval.
func resolveCatalogCacheTTLs(getenv func(string) string, logger *slog.Logger) catalogCacheTTLs {
	shared := cacheSeconds(getenv, catalogCacheDefaultSecondsEnv, defaultCatalogCacheInterval, logger)
	return catalogCacheTTLs{
		inventory: cacheSeconds(getenv, catalogCacheInventorySecondsEnv, shared, logger),
		rateCard:  cacheSeconds(getenv, catalogCacheRateCardSecondsEnv, shared, logger),
	}
}

// cacheSeconds resolves one tuning variable to a duration, or returns the fallback when the variable is unset or
// unusable.
func cacheSeconds(getenv func(string) string, name string, fallback time.Duration, logger *slog.Logger) time.Duration {
	raw := getenv(name)
	if raw == "" {
		return fallback
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 || int64(seconds) > maxCatalogCacheSeconds {
		logger.Warn("Ignoring invalid catalog cache tuning value",
			slog.String("variable", name),
			slog.String("value", raw),
			slog.Duration("fallback", fallback),
		)
		return fallback
	}
	return time.Duration(seconds) * time.Second
}
