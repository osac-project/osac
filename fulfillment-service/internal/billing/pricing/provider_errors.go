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
	"errors"

	"github.com/shopspring/decimal"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
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

// parseUnitRate is the one usable-row rule shared by coverage resolution and rate-card listing: a row is usable only
// when its price method is QUANTITY, its unit rate is non-empty, and that unit rate parses as a decimal. It returns
// the amount and, on failure, the reason both call paths report: ReasonRateShapeUnsupported, which the listing side
// reuses as SkipRateShapeUnsupported. A row failing the rule is never priced and never guessed.
func parseUnitRate(row billing.RateRow) (decimal.Decimal, string, bool) {
	if row.PriceMethod != priceMethodQuantity || row.UnitRate == "" {
		return decimal.Decimal{}, ReasonRateShapeUnsupported, false
	}
	amount, err := decimal.NewFromString(row.UnitRate)
	if err != nil {
		return decimal.Decimal{}, ReasonRateShapeUnsupported, false
	}
	return amount, "", true
}

// classifyServiceLookup maps a FindServiceByName failure. Zero exact matches is a coverage GAP, more than one is
// AMBIGUOUS; any other failure is a classified provider error.
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

// classifyRateCardLookup maps a GetServiceRateCard failure: no card for the currency is a missing rate, which must
// be a GAP, never a silent zero.
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
