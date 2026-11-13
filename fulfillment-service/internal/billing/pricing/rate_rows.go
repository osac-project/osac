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
	"errors"

	"github.com/shopspring/decimal"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

// Match operators for a RateCondition. These mirror BillingRateMatchOperator without importing proto into this
// package; the server layer maps them to the proto enum.
const (
	OperatorAny    = "ANY"
	OperatorEquals = "EQUALS"
)

// Skip reasons for a row ListServiceRateCardItems could not normalize. These are for the caller's unsupported-row
// metric; they are never returned to a client as a Reason on a result, unlike the pricing Reason* constants.
const (
	// SkipRateShapeUnsupported is a price method other than QUANTITY, or a missing/unparsable unit rate: a
	// FLAT_RATE, TIERED, AMOUNT, NO_PRICE, percentage-rate or pricing-logic shape.
	SkipRateShapeUnsupported = ReasonRateShapeUnsupported
	// SkipDimensionUnmapped is a row whose priced attribute has no OSAC dimension name, so the row cannot identify
	// what it prices.
	SkipDimensionUnmapped = "BILLING_RATE_DIMENSION_UNMAPPED"
	// SkipConditionValueEmpty is an EQUALS condition with an empty value, which is reserved for ANY.
	SkipConditionValueEmpty = "BILLING_RATE_CONDITION_VALUE_EMPTY"
)

// RateCondition is one normalized match condition of a RateCardItem.
type RateCondition struct {
	Dimension string
	// Operator is OperatorAny or OperatorEquals.
	Operator string
	// Value is set only for OperatorEquals.
	Value string
}

// RateCardItem is one normalized, supported rate-card row: a provider rule translated into OSAC's provider-neutral
// shape. It carries no provider rule id, rate-card id, service id or native account id.
type RateCardItem struct {
	Service  string
	Currency string
	// Dimension is the priced dimension name, unlike Conditions, which describe the configurations the row applies to.
	Dimension string
	// Conditions contain only the provider's restrictions; empty means every configuration of the service.
	Conditions []RateCondition
	// Amount is the provider decimal exactly as returned, never rounded.
	Amount decimal.Decimal
	// Unit is always unitSecond: OSAC treats every rate as per second and performs no unit validation.
	Unit string
}

// ListServiceRateCardItems reads service's current rate card in currency and normalizes every QUANTITY row with a
// parsable unitRate into a RateCardItem. It never evaluates a provider formula and never guesses a price for an
// unsupported row: such a row is omitted and counted in skipped, keyed by one of the Skip* reasons above.
//
// A service name with no exact provider match, or a currency with no rate card, yields an empty, error-free result:
// both mean nothing to list, not a provider failure. Any other adapter failure (ambiguous service match, timeout,
// malformed response) is returned as a classified *billing.Error for the caller to map to UNAVAILABLE, the same way
// resolveComponent does.
//
// This performs no tenant/account resolution and no override lookup: OSAC applies no separate override tier, so the
// rows returned here are identical whether or not a caller has already validated a tenant's account.
func (r *Resolver) ListServiceRateCardItems(ctx context.Context, service, currency string) (
	items []RateCardItem, skipped map[string]int, err error) {
	skipped = map[string]int{}

	svc, err := r.adapter.FindServiceByName(ctx, service)
	if err != nil {
		if errors.Is(err, billing.ErrServiceNotFound) {
			return nil, skipped, nil
		}
		return nil, nil, err
	}

	attrs, err := r.adapter.ListPriceableAttributes(ctx, svc)
	if err != nil {
		return nil, nil, err
	}
	names := make(map[string]string, len(attrs)) // attribute id -> OSAC dimension name
	for _, a := range attrs {
		names[a.ID] = a.Name
	}

	card, err := r.adapter.GetServiceRateCard(ctx, svc, currency)
	if err != nil {
		if errors.Is(err, billing.ErrRateCardNotFound) {
			return nil, skipped, nil
		}
		return nil, nil, err
	}

	for _, row := range card.Rows {
		item, reason, ok := normalizeRateRow(row, names, card.Currency)
		if !ok {
			skipped[reason]++
			continue
		}
		item.Service = service
		items = append(items, item)
	}
	return items, skipped, nil
}

// normalizeRateRow converts one provider row into a RateCardItem, or reports why it was skipped. Dimension carries
// the priced dimension; Conditions contain only the provider's own restrictions. Empty AttributeValues means no
// conditions, while each supplied entry produces an EQUALS condition using its attribute Name (the same shape
// matchRow/rowMatches use for request-side matching). Empty EQUALS values are invalid because empty is reserved for
// ANY; those rows are rejected so the caller can skip and count them.
func normalizeRateRow(row billing.RateRow, names map[string]string, cardCurrency string) (
	item RateCardItem, skipReason string, ok bool) {
	name, known := names[row.DimensionID]
	if !known || name == "" {
		return RateCardItem{}, SkipDimensionUnmapped, false
	}
	amount, reason, ok := parseUnitRate(row)
	if !ok {
		return RateCardItem{}, reason, false
	}

	var conditions []RateCondition
	for _, av := range row.AttributeValues {
		if av.Value == "" {
			return RateCardItem{}, SkipConditionValueEmpty, false
		}
		conditions = append(conditions, RateCondition{Dimension: av.Name, Operator: OperatorEquals, Value: av.Value})
	}

	return RateCardItem{
		Currency:   cardCurrency,
		Dimension:  name,
		Conditions: conditions,
		Amount:     amount,
		Unit:       unitSecond,
	}, "", true
}
