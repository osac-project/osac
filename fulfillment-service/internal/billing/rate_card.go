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
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/text/currency"
)

type RateCardSource int

const (
	RateCardSourceUnspecified RateCardSource = iota
	RateCardSourceDefault
	RateCardSourceContract
)

type RateMatchOperator int

const (
	RateMatchUnspecified RateMatchOperator = iota
	RateMatchAny
	RateMatchEquals
)

type RateUnit string

const (
	RateUnitHour  RateUnit = "hour"
	RateUnitMonth RateUnit = "month"
)

type RateCondition struct {
	Dimension string
	Operator  RateMatchOperator
	Value     string
}

type RateCardItem struct {
	Service            string
	CurrencyCode       string
	Source             RateCardSource
	Conditions         []RateCondition
	Amount             string
	Unit               RateUnit
	QuantityDimension  string
	CatalogItemMatches []string
}

type RateCardQuery struct {
	AccountReference string
	Service          string
	CurrencyCode     string
}

// RateCardProvider reads provider pricing into OSAC's provider-neutral model.
type RateCardProvider interface {
	ListRateCardItems(context.Context, RateCardQuery) ([]RateCardItem, error)
}

var (
	ErrRateCoverageGap   = errors.New("no provider rate covers the requested configuration")
	ErrAmbiguousRateCard = errors.New("multiple equally specific provider rates cover the requested configuration")
	ErrInvalidRateCard   = errors.New("provider returned an invalid rate rule")
)

// ResolveRateCardItem selects the most specific provider rule covering a
// configuration in the requested service and currency.
func ResolveRateCardItem(
	items []RateCardItem,
	service, currency string,
	dimensions map[string]string,
) (RateCardItem, error) {
	service = strings.TrimSpace(service)
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if service == "" || !validCurrencyCode(currency) {
		return RateCardItem{}, fmt.Errorf("%w: service and currency are required", ErrInvalidRateCard)
	}

	var (
		match            RateCardItem
		matchSpecificity = -1
		ambiguous        bool
	)
	for _, item := range items {
		if strings.TrimSpace(item.Service) != service {
			continue
		}
		itemCurrency := strings.ToUpper(strings.TrimSpace(item.CurrencyCode))
		if !validCurrencyCode(itemCurrency) {
			return RateCardItem{}, fmt.Errorf("%w: rate currency must be an ISO-4217 code", ErrInvalidRateCard)
		}
		if itemCurrency != currency {
			continue
		}
		item.CurrencyCode = itemCurrency
		specificity, matches, err := rateCardItemSpecificity(item, dimensions)
		if err != nil {
			return RateCardItem{}, err
		}
		if !matches {
			continue
		}
		if specificity > matchSpecificity {
			match = item
			matchSpecificity = specificity
			ambiguous = false
		} else if specificity == matchSpecificity {
			ambiguous = true
		}
	}
	if matchSpecificity < 0 {
		return RateCardItem{}, ErrRateCoverageGap
	}
	if ambiguous {
		return RateCardItem{}, ErrAmbiguousRateCard
	}
	return match, nil
}

func validCurrencyCode(value string) bool {
	_, err := currency.ParseISO(strings.ToUpper(strings.TrimSpace(value)))
	return err == nil
}

func rateCardItemSpecificity(item RateCardItem, dimensions map[string]string) (int, bool, error) {
	if strings.TrimSpace(item.Amount) == "" || strings.TrimSpace(string(item.Unit)) == "" {
		return 0, false, fmt.Errorf("%w: amount and unit are required", ErrInvalidRateCard)
	}

	specificity := 0
	seen := make(map[string]struct{}, len(item.Conditions))
	for _, condition := range item.Conditions {
		if condition.Dimension == "" {
			return 0, false, fmt.Errorf("%w: condition dimension is required", ErrInvalidRateCard)
		}
		if _, exists := seen[condition.Dimension]; exists {
			return 0, false, fmt.Errorf("%w: duplicate condition dimension", ErrInvalidRateCard)
		}
		seen[condition.Dimension] = struct{}{}

		switch condition.Operator {
		case RateMatchAny:
		case RateMatchEquals:
			if condition.Value == "" {
				return 0, false, fmt.Errorf("%w: equality condition value is required", ErrInvalidRateCard)
			}
			value, ok := dimensions[condition.Dimension]
			if !ok || value != condition.Value {
				return 0, false, nil
			}
			specificity++
		default:
			return 0, false, fmt.Errorf("%w: condition operator is unsupported", ErrInvalidRateCard)
		}
	}
	for dimension := range dimensions {
		if _, covered := seen[dimension]; !covered {
			return 0, false, nil
		}
	}
	return specificity, true, nil
}
