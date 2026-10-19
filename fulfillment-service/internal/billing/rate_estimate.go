/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package billing

import (
	"errors"
	"math/big"
	"strconv"
	"strings"
)

// ComponentRateAmount applies a provider's quantity rate to the component it
// prices. Flat rates have no quantity dimension and retain the provider amount.
func ComponentRateAmount(rate RateCardItem, dimensions map[string]string) (string, error) {
	if rate.QuantityDimension == "" {
		return rate.Amount, nil
	}
	quantity, err := strconv.ParseInt(dimensions[rate.QuantityDimension], 10, 64)
	if err != nil || quantity <= 0 {
		return "", errors.New("rate quantity must be a positive integer")
	}
	unitRate, ok := new(big.Rat).SetString(strings.TrimSpace(rate.Amount))
	if !ok {
		return "", errors.New("rate amount must be a decimal")
	}
	amount := new(big.Rat).Mul(unitRate, big.NewRat(quantity, 1))
	// A decimal or exponent-form rate has a terminating decimal expansion.
	// Derive its exact precision from the product, not the provider's notation.
	denominator := new(big.Int).Set(amount.Denom())
	twos := 0
	for denominator.Bit(0) == 0 {
		denominator.Rsh(denominator, 1)
		twos++
	}
	fives := 0
	five := big.NewInt(5)
	quotient, remainder := new(big.Int), new(big.Int)
	for {
		quotient.QuoRem(denominator, five, remainder)
		if remainder.Sign() != 0 {
			break
		}
		denominator.Set(quotient)
		fives++
	}
	if denominator.Cmp(big.NewInt(1)) != 0 {
		return "", errors.New("rate amount must have a finite decimal expansion")
	}
	places := max(twos, fives)
	formatted := amount.FloatString(places)
	if places > 0 {
		formatted = strings.TrimRight(formatted, "0")
		formatted = strings.TrimRight(formatted, ".")
	}
	return formatted, nil
}

const (
	hoursPerBillingMonth     = 730
	derivedRateDecimalPlaces = 6
)

// NormalizeRecurringRate returns hourly and monthly equivalents for provider
// hourly or monthly rates. Monthly-to-hourly conversion is represented to six
// decimal places; the provider amount and unit remain unchanged in the API.
func NormalizeRecurringRate(amount string, unit RateUnit) (hourly, monthly string, err error) {
	value, ok := new(big.Rat).SetString(strings.TrimSpace(amount))
	if !ok {
		return "", "", errors.New("rate amount must be a decimal")
	}
	month := big.NewRat(hoursPerBillingMonth, 1)
	switch unit {
	case RateUnitHour:
		return formatDerivedRate(value), formatDerivedRate(new(big.Rat).Mul(value, month)), nil
	case RateUnitMonth:
		return formatDerivedRate(new(big.Rat).Quo(value, month)), formatDerivedRate(value), nil
	default:
		return "", "", errors.New("rate unit is not a supported recurring time unit")
	}
}

func formatDerivedRate(value *big.Rat) string {
	formatted := value.FloatString(derivedRateDecimalPlaces)
	formatted = strings.TrimRight(formatted, "0")
	return strings.TrimRight(formatted, ".")
}
