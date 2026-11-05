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
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

// State is the coverage outcome of one metered component. There is no STALE member: that state belongs to a
// later cache-layer story, not this resolver.
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

// unitSecond is the only unit OSAC reports: every provider rate is treated as per second, without unit
// validation or matching (design §2.3).
const unitSecond = "second"

// priceMethodQuantity is the only provider price method this resolver understands; any other value (FLAT_RATE,
// TIERED, AMOUNT, NO_PRICE, a percentage rate, or a pricing-logic formula) is an unsupported shape.
const priceMethodQuantity = "QUANTITY"

// hourSeconds and monthHours are OSAC's own derivation convention, not a provider field. No OSAC rounding is
// applied to either multiplication.
const (
	hourSeconds = 3600
	monthHours  = 730
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

// Resolver resolves provider rates and coverage for a configuration. It is the single pricing path shared by
// catalog pricing, estimates and billing gates.
type Resolver struct {
	adapter billing.Adapter
}

// NewResolver builds a Resolver over adapter, the billing adapter boundary (see billing.Adapter). adapter must be
// non-nil: a resolver always requires a configured provider. Handling the disabled, no-provider case is the
// caller's concern.
func NewResolver(adapter billing.Adapter) *Resolver {
	return &Resolver{adapter: adapter}
}

// Resolve prices every component of req against fresh provider reads; there is no cross-request cache layer here,
// but components of one service share that service's reads within the call (see resolution), so a configuration
// costs one provider sweep per distinct service rather than one per component. It returns an error only for a
// malformed request, such as a component missing its required dimension value or an empty component list; every
// provider-side or coverage failure is instead reported as a per-component State, see ComponentResult.
func (r *Resolver) Resolve(ctx context.Context, req Request) (Result, error) {
	if r.adapter == nil {
		return Result{}, fmt.Errorf("pricing: resolver has no configured provider adapter")
	}
	if len(req.Components) == 0 {
		return Result{}, fmt.Errorf("pricing: request has no components")
	}
	resolutions := make(map[string]*resolution)
	components := make([]ComponentResult, 0, len(req.Components))
	for _, c := range req.Components {
		if c.Service == "" || c.Dimension == "" {
			return Result{}, fmt.Errorf("pricing: component requires a service and a dimension name")
		}
		if c.Values[c.Dimension] == "" {
			return Result{}, fmt.Errorf(
				"pricing: component %s/%s has no configured value for its required dimension", c.Service, c.Dimension)
		}
		components = append(components, r.resolveComponent(ctx, req, c, resolutions))
	}
	return Result{Components: components, Aggregate: aggregate(components)}, nil
}

// aggregate implements the precedence documented on the Aggregate type: UNAVAILABLE/AMBIGUOUS beats GAP
// regardless of component order.
func aggregate(components []ComponentResult) Aggregate {
	gapped := false
	for _, c := range components {
		switch c.State {
		case StateUnavailable, StateAmbiguous:
			return AggregateUnavailable
		case StateGap:
			gapped = true
		}
	}
	if gapped {
		return AggregateGapped
	}
	return AggregateCurrent
}

// resolveComponent runs the AC2/§2.3 name-matching pipeline for one component: service by exact name, dimension
// by exact name, then the rate card in the request currency, then the matching rate row. The per-service reads
// are shared with the request's other components through resolutions.
func (r *Resolver) resolveComponent(ctx context.Context, req Request, c ComponentRequest,
	resolutions map[string]*resolution) ComponentResult {
	result := ComponentResult{Service: c.Service, Dimension: c.Dimension}
	if req.Currency == "" {
		result.State, result.Reason = StateUnavailable, ReasonCurrencyUnresolved
		return result
	}

	res := r.resolveService(ctx, c.Service, resolutions)
	if res.serviceOutcome.failed {
		result.State, result.Reason = res.serviceOutcome.state, res.serviceOutcome.reason
		return result
	}
	attrID, ok := attributeID(res.attrs, c.Dimension)
	if !ok {
		result.State, result.Reason = StateGap, ReasonCoverageGap
		return result
	}

	card, outcome := r.resolveCard(ctx, req, res)
	if outcome.failed {
		result.State, result.Reason = outcome.state, outcome.reason
		return result
	}

	row, state, reason := matchRow(card.Rows, attrID, c.Values)
	if state != StateCovered {
		result.State, result.Reason = state, reason
		return result
	}

	amount, parseErr := decimal.NewFromString(row.UnitRate)
	if row.PriceMethod != priceMethodQuantity || row.UnitRate == "" || parseErr != nil {
		result.State, result.Reason = StateUnavailable, ReasonRateShapeUnsupported
		return result
	}

	result.State = StateCovered
	result.Currency = req.Currency
	result.Unit = unitSecond
	result.PerSecond = amount
	result.PerHour = amount.Mul(decimal.NewFromInt(hourSeconds))
	result.PerMonth = result.PerHour.Mul(decimal.NewFromInt(monthHours))
	return result
}

// resolution is one service's provider reads, shared by every component of that service within a single Resolve
// call: the service lookup and its priceable attributes are read once up front, and the rate card in the request
// currency is read once, lazily, after a component's dimension is known to exist. Outcomes are shared, including
// failures, so components of one service cannot split between states when the provider fails flakily.
type resolution struct {
	svc   billing.ServiceRef
	attrs []billing.PriceableAttribute
	// serviceOutcome is the classified outcome of the service lookup and the attribute listing.
	serviceOutcome readOutcome
	// cardRead marks that the rate-card read has run; cardOutcome is its classified outcome and card its data.
	cardRead    bool
	cardOutcome readOutcome
	card        billing.RateCard
}

// readOutcome is a classified provider-read failure: failed false means the read succeeded.
type readOutcome struct {
	failed bool
	state  State
	reason string
}

// resolveService reads the service lookup and its priceable attributes once per service per Resolve call,
// sharing the outcome - failure included - with every component of that service.
func (r *Resolver) resolveService(ctx context.Context, service string,
	resolutions map[string]*resolution) *resolution {
	if res, ok := resolutions[service]; ok {
		return res
	}
	res := &resolution{}
	resolutions[service] = res
	svc, err := r.adapter.FindServiceByName(ctx, service)
	if err != nil {
		state, reason := classifyServiceLookup(err)
		res.serviceOutcome = readOutcome{failed: true, state: state, reason: reason}
		return res
	}
	res.svc = svc
	attrs, err := r.adapter.ListPriceableAttributes(ctx, svc)
	if err != nil {
		state, reason := classifyProviderError(err)
		res.serviceOutcome = readOutcome{failed: true, state: state, reason: reason}
		return res
	}
	res.attrs = attrs
	return res
}

// resolveCard reads the service's rate card in the request currency once per resolution: the first component of
// the service that needs it pays for the read, and every later one shares the outcome, failures included.
func (r *Resolver) resolveCard(ctx context.Context, req Request, res *resolution) (billing.RateCard, readOutcome) {
	if res.cardRead {
		return res.card, res.cardOutcome
	}
	res.cardRead = true
	card, err := r.adapter.GetServiceRateCard(ctx, res.svc, req.Currency)
	if err != nil {
		state, reason := classifyRateCardLookup(err)
		res.cardOutcome = readOutcome{failed: true, state: state, reason: reason}
		return res.card, res.cardOutcome
	}
	if !strings.EqualFold(card.Currency, req.Currency) {
		res.cardOutcome = readOutcome{failed: true, state: StateUnavailable, reason: ReasonCurrencyMismatch}
		return res.card, res.cardOutcome
	}
	res.card = card
	return card, res.cardOutcome
}

// classifyServiceLookup maps a FindServiceByName failure. Per the implementation guidance: zero exact matches is
// a coverage GAP, more than one is AMBIGUOUS; any other failure is a classified provider error.
func classifyServiceLookup(err error) (State, string) {
	if errors.Is(err, billing.ErrServiceNotFound) {
		return StateGap, ReasonCoverageGap
	}
	var be *billing.Error
	if errors.As(err, &be) && be.Reason == billing.ReasonAmbiguous {
		return StateAmbiguous, ReasonRateAmbiguous
	}
	return classifyProviderError(err)
}

// classifyRateCardLookup maps a GetServiceRateCard failure: no card for the currency is a missing rate, which
// AC8 requires to be a GAP, never a silent zero.
func classifyRateCardLookup(err error) (State, string) {
	if errors.Is(err, billing.ErrRateCardNotFound) {
		return StateGap, ReasonCoverageGap
	}
	return classifyProviderError(err)
}

// classifyProviderError reports any other adapter failure (timeout, 5xx, 401/403, malformed response) as
// UNAVAILABLE with the provider's own classified reason, never substituting another rate.
func classifyProviderError(err error) (State, string) {
	var be *billing.Error
	if errors.As(err, &be) {
		return StateUnavailable, string(be.Reason)
	}
	return StateUnavailable, ReasonProviderError
}

func attributeID(attrs []billing.PriceableAttribute, name string) (string, bool) {
	for _, a := range attrs {
		if a.Name == name {
			return a.ID, true
		}
	}
	return "", false
}

// matchRow finds the rate row that prices attrID under the ANY/EQUALS predicate: a row with no AttributeValues
// entry for a name is unconstrained on it (ANY), while a present entry must equal the configured value (EQUALS).
// Per task OSAC-6890, a more-specific matching row wins over less-specific rows; two or more matching rows tied at
// the highest specificity are AMBIGUOUS.
func matchRow(rows []billing.RateRow, attrID string, values map[string]string) (billing.RateRow, State, string) {
	var best []billing.RateRow
	bestSpecificity := -1
	for _, row := range rows {
		if row.DimensionID != attrID || !rowMatches(row, values) {
			continue
		}
		specificity := len(row.AttributeValues)
		switch {
		case specificity > bestSpecificity:
			best = []billing.RateRow{row}
			bestSpecificity = specificity
		case specificity == bestSpecificity:
			best = append(best, row)
		}
	}
	switch len(best) {
	case 0:
		return billing.RateRow{}, StateGap, ReasonCoverageGap
	case 1:
		return best[0], StateCovered, ""
	default:
		return billing.RateRow{}, StateAmbiguous, ReasonRateAmbiguous
	}
}

func rowMatches(row billing.RateRow, values map[string]string) bool {
	for _, av := range row.AttributeValues {
		value, ok := values[av.Name]
		// Empty is reserved for ANY, which is represented by an absent condition, not an empty EQUALS value.
		if av.Value == "" || !ok || value != av.Value {
			return false
		}
	}
	return true
}
