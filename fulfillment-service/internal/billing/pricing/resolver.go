/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package pricing

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

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

// resolveComponent runs the name-matching pipeline for one component: service by exact name, dimension by exact
// name, then the rate card in the request currency, then the matching rate row. The per-service reads are shared
// with the request's other components through resolutions.
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
	attrID, attrMatch := attributeID(res.attrs, c.Dimension)
	switch attrMatch {
	case attributeMissing:
		result.State, result.Reason = StateGap, ReasonCoverageGap
		return result
	case attributeAmbiguous:
		// The provider's own attribute data is ambiguous, the same state classifyServiceLookup reports for a
		// duplicate provider service name: no attribute ID is guessed, so no rate row is priced.
		result.State, result.Reason = StateAmbiguous, ReasonRateAmbiguous
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

	amount, reason, ok := parseUnitRate(row)
	if !ok {
		result.State, result.Reason = StateUnavailable, reason
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

// attributeMatch is how a dimension name resolved to a provider's priceable attributes. Only attributeUnique
// identifies a single attribute ID, the only case a rate row can be priced from.
type attributeMatch int

const (
	// attributeMissing means no priceable attribute carries the name.
	attributeMissing attributeMatch = iota
	// attributeUnique means exactly one distinct attribute ID carries the name.
	attributeUnique
	// attributeAmbiguous means two or more distinct attribute IDs carry the name.
	attributeAmbiguous
)

// attributeID resolves a dimension name to the one provider attribute ID that prices it, matching by exact name as
// service lookup does. A provider may list two attributes sharing one name; that is ambiguous provider data, so it
// reports attributeAmbiguous and no ID rather than the first of the two: guessing would price the component against
// rows keyed to one ID and report a false coverage gap when the rate rows are keyed to the other. A repeated name on
// the same attribute ID is not a duplicate, since it still identifies one attribute.
func attributeID(attrs []billing.PriceableAttribute, name string) (string, attributeMatch) {
	id := ""
	match := attributeMissing
	for _, a := range attrs {
		if a.Name != name {
			continue
		}
		if match == attributeMissing {
			id, match = a.ID, attributeUnique
			continue
		}
		if a.ID != id {
			return "", attributeAmbiguous
		}
	}
	return id, match
}

// matchRow finds the rate row that prices attrID under the ANY/EQUALS predicate: a row with no AttributeValues
// entry for a name is unconstrained on it (ANY), while a present entry must equal the configured value (EQUALS).
// A row constraining more distinct attribute names is more specific and wins over less specific rows; two or more
// matching rows tied at the highest specificity are AMBIGUOUS.
func matchRow(rows []billing.RateRow, attrID string, values map[string]string) (billing.RateRow, State, string) {
	var best []billing.RateRow
	bestSpecificity := -1
	for _, row := range rows {
		if row.DimensionID != attrID || !rowMatches(row, values) {
			continue
		}
		specificity := distinctConditionNames(row)
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

// distinctConditionNames counts the distinct attribute names a row constrains: its specificity. Counting entries
// instead would let a row that repeats one name, with the same or a different value, outscore a row constraining a
// genuinely different name and so win the match on data that is not more specific.
func distinctConditionNames(row billing.RateRow) int {
	names := make(map[string]struct{}, len(row.AttributeValues))
	for _, av := range row.AttributeValues {
		names[av.Name] = struct{}{}
	}
	return len(names)
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
