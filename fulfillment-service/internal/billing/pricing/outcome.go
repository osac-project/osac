/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package pricing is the single internal resolver of provider rates and coverage for a configuration. Catalog
// pricing, estimates and billing gates all call it so that they agree on the same price and the same gap; no
// second resolver exists elsewhere. It talks to a billing provider only through billing.Adapter, resolves the
// provider's globally configured service rate (never a tenant-specific override), persists no price, and never
// calls a provider quote/estimate endpoint.
package pricing

import (
	"github.com/shopspring/decimal"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

// State is the coverage outcome of one metered component. There is no STALE member: staleness belongs to the cache
// layer above this resolver, not to a resolver outcome.
type State string

const (
	StateCovered     State = "COVERED"
	StateGap         State = "GAP"
	StateUnavailable State = "UNAVAILABLE"
	StateAmbiguous   State = "AMBIGUOUS"
)

// Aggregate summarizes a Result across every component: any GAP makes a configuration Gapped; any UNAVAILABLE or
// AMBIGUOUS makes it Unavailable (checked first, so it takes precedence over a GAP elsewhere); only every
// component COVERED is Current.
type Aggregate string

const (
	AggregateCurrent     Aggregate = "CURRENT"
	AggregateGapped      Aggregate = "GAPPED"
	AggregateUnavailable Aggregate = "UNAVAILABLE"
)

// Stable reason codes for a component that is not COVERED. A provider lookup failure instead carries the
// provider's own classified billing.Reason (e.g. "TIMEOUT", "UNAUTHORIZED", "MALFORMED") so the caller sees the
// real cause rather than a generic one.
const (
	ReasonCoverageGap          = "BILLING_COVERAGE_GAP"
	ReasonRateAmbiguous        = "BILLING_RATE_AMBIGUOUS"
	ReasonCurrencyMismatch     = "BILLING_CURRENCY_MISMATCH"
	ReasonCurrencyUnresolved   = "BILLING_CURRENCY_UNRESOLVED"
	ReasonRateShapeUnsupported = "BILLING_RATE_SHAPE_UNSUPPORTED"
	// ReasonProviderError is used only when a provider lookup fails with an unclassified error (not a
	// *billing.Error); every adapter in this codebase returns *billing.Error, so this is a defensive fallback.
	ReasonProviderError = "BILLING_PROVIDER_ERROR"
)

// ComponentRequest asks the resolver to price one required metered component of a configuration.
type ComponentRequest struct {
	// Service is the OSAC service name. The resolver finds the provider service whose name equals it exactly;
	// there is no OSAC-side service mapping.
	Service string
	// Dimension is the OSAC dimension name that prices this component. The resolver finds the provider
	// priceable attribute whose name equals it exactly; there is no OSAC-side dimension mapping.
	Dimension string
	// Values is the component's normalized configuration, keyed by OSAC dimension name. It must carry a
	// non-empty entry for Dimension; a configuration missing that value is a validation error, not a GAP.
	// Other entries let the resolver's ANY/EQUALS predicate disambiguate rate rows keyed on more than one
	// attribute.
	Values map[string]string
}

// Request asks the resolver to price every required component of a configuration against one billing account.
type Request struct {
	// Account identifies the billing account the rates are resolved for. It is not sent to the provider as a
	// query by this resolver; it is carried for the caller's and future stories' use.
	Account billing.AccountRef
	// Currency is the account's billing currency, already resolved by the caller: provider-supplied, from the
	// account where the provider exposes one, otherwise from the provider's current rate-card currency (design
	// §2.3). There is no configured default. Empty yields UNAVAILABLE/BILLING_CURRENCY_UNRESOLVED for every
	// component: the billing.Adapter boundary has no operation that returns a rate card without naming a
	// currency, so an unresolved currency can never be turned into a provider call.
	Currency   string
	Components []ComponentRequest
}

// ComponentResult is one component's coverage outcome. Currency, Unit and the Per* amounts are only meaningful
// when State is StateCovered; a non-covered component carries none of them, so a missing rate is never rendered
// as a zero amount.
type ComponentResult struct {
	Service   string
	Dimension string
	State     State
	// Reason is empty when State is StateCovered, and otherwise one of the Reason* constants above or a
	// provider billing.Reason value.
	Reason   string
	Currency string
	Unit     string
	// PerSecond is the provider amount exactly as returned. PerHour and PerMonth are OSAC-derived: PerHour =
	// PerSecond * 3600, PerMonth = PerHour * 730, using exact decimal arithmetic.
	PerSecond decimal.Decimal
	PerHour   decimal.Decimal
	PerMonth  decimal.Decimal
}

// Result is the resolver's outcome for a full configuration.
type Result struct {
	Components []ComponentResult
	Aggregate  Aggregate
}
