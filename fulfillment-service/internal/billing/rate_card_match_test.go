/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package billing

import (
	"errors"
	"testing"
)

func TestResolveRateCardItemPrefersMoreSpecificRule(t *testing.T) {
	rates := []RateCardItem{
		{
			Service:      "compute_instance",
			CurrencyCode: "USD",
			Amount:       "4.50",
			Unit:         RateUnitHour,
			Conditions: []RateCondition{
				{Dimension: "image_ref", Operator: RateMatchAny},
			},
		},
		{
			Service:      "compute_instance",
			CurrencyCode: "USD",
			Amount:       "3.00",
			Unit:         RateUnitHour,
			Conditions: []RateCondition{
				{Dimension: "image_ref", Operator: RateMatchEquals, Value: "fedora-16"},
				{Dimension: "boot_disk_size_gib", Operator: RateMatchEquals, Value: "20"},
			},
		},
		{
			Service:      "compute_instance",
			CurrencyCode: "EUR",
			Amount:       "2.00",
			Unit:         RateUnitHour,
		},
	}

	got, err := ResolveRateCardItem(rates, "compute_instance", "USD", map[string]string{
		"image_ref":          "fedora-16",
		"boot_disk_size_gib": "20",
	})
	if err != nil {
		t.Fatalf("ResolveRateCardItem() error = %v", err)
	}
	if got.Amount != "3.00" || got.CurrencyCode != "USD" {
		t.Fatalf("ResolveRateCardItem() = %+v, want the matching USD rate 3.00", got)
	}
}

func TestResolveRateCardItemReportsUncoveredConfiguration(t *testing.T) {
	rates := []RateCardItem{{
		Service:      "compute_instance",
		CurrencyCode: "USD",
		Amount:       "3.00",
		Unit:         RateUnitHour,
		Conditions: []RateCondition{
			{Dimension: "image_ref", Operator: RateMatchEquals, Value: "fedora-14"},
		},
	}}

	_, err := ResolveRateCardItem(rates, "compute_instance", "USD", map[string]string{"image_ref": "fedora-16"})
	if !errors.Is(err, ErrRateCoverageGap) {
		t.Fatalf("ResolveRateCardItem() error = %v, want ErrRateCoverageGap", err)
	}
}

func TestResolveRateCardItemRequiresEveryRequestedDimensionCovered(t *testing.T) {
	request := map[string]string{
		"instance_type":            "small",
		"additional_disk_size_gib": "100",
	}
	instanceTypeOnly := RateCardItem{
		Service:      "compute_instance",
		CurrencyCode: "USD",
		Amount:       "3.00",
		Unit:         RateUnitHour,
		Conditions: []RateCondition{{
			Dimension: "instance_type",
			Operator:  RateMatchEquals,
			Value:     "small",
		}},
	}
	diskOnly := RateCardItem{
		Service:      "compute_instance",
		CurrencyCode: "USD",
		Amount:       "2.00",
		Unit:         RateUnitHour,
		Conditions: []RateCondition{{
			Dimension: "additional_disk_size_gib",
			Operator:  RateMatchEquals,
			Value:     "100",
		}},
	}

	t.Run("uncovered extra dimension is a coverage gap", func(t *testing.T) {
		_, err := ResolveRateCardItem([]RateCardItem{instanceTypeOnly}, "compute_instance", "USD", request)
		if !errors.Is(err, ErrRateCoverageGap) {
			t.Fatalf("ResolveRateCardItem() error = %v, want ErrRateCoverageGap", err)
		}
	})

	t.Run("separate partial rules do not jointly cover a request", func(t *testing.T) {
		_, err := ResolveRateCardItem([]RateCardItem{instanceTypeOnly, diskOnly}, "compute_instance", "USD", request)
		if !errors.Is(err, ErrRateCoverageGap) {
			t.Fatalf("ResolveRateCardItem() error = %v, want ErrRateCoverageGap", err)
		}
	})

	t.Run("combined rule covers both dimensions", func(t *testing.T) {
		combined := instanceTypeOnly
		combined.Amount = "1.00"
		combined.Conditions = append(append([]RateCondition(nil), instanceTypeOnly.Conditions...), diskOnly.Conditions...)

		got, err := ResolveRateCardItem([]RateCardItem{instanceTypeOnly, combined}, "compute_instance", "USD", request)
		if err != nil {
			t.Fatalf("ResolveRateCardItem() error = %v", err)
		}
		if got.Amount != "1.00" {
			t.Fatalf("ResolveRateCardItem() amount = %q, want combined rule amount 1.00", got.Amount)
		}
	})
}

func TestResolveRateCardItemMatchesEqualsDimension(t *testing.T) {
	item := RateCardItem{
		Service:      "compute_instance",
		CurrencyCode: "USD",
		Amount:       "3.00",
		Unit:         RateUnitHour,
		Conditions: []RateCondition{{
			Dimension: "instance_type",
			Operator:  RateMatchEquals,
			Value:     "small",
		}},
	}

	got, err := ResolveRateCardItem([]RateCardItem{item}, "compute_instance", "USD", map[string]string{"instance_type": "small"})
	if err != nil {
		t.Fatalf("ResolveRateCardItem() error = %v", err)
	}
	if got.Amount != item.Amount {
		t.Fatalf("ResolveRateCardItem() amount = %q, want %q", got.Amount, item.Amount)
	}
}

func TestResolveRateCardItemPrefersEqualsOverAny(t *testing.T) {
	rates := []RateCardItem{
		{
			Service:      "compute_instance",
			CurrencyCode: "USD",
			Amount:       "4.50",
			Unit:         RateUnitHour,
			Conditions: []RateCondition{{
				Dimension: "instance_type",
				Operator:  RateMatchAny,
			}},
		},
		{
			Service:      "compute_instance",
			CurrencyCode: "USD",
			Amount:       "3.00",
			Unit:         RateUnitHour,
			Conditions: []RateCondition{{
				Dimension: "instance_type",
				Operator:  RateMatchEquals,
				Value:     "small",
			}},
		},
	}

	got, err := ResolveRateCardItem(rates, "compute_instance", "USD", map[string]string{"instance_type": "small"})
	if err != nil {
		t.Fatalf("ResolveRateCardItem() error = %v", err)
	}
	if got.Amount != "3.00" {
		t.Fatalf("ResolveRateCardItem() amount = %q, want EQUALS rate amount 3.00", got.Amount)
	}
}

func TestResolveRateCardItemEmptyConditionsOnlyMatchEmptyDimensions(t *testing.T) {
	item := RateCardItem{
		Service:      "compute_instance",
		CurrencyCode: "USD",
		Amount:       "3.00",
		Unit:         RateUnitHour,
	}

	got, err := ResolveRateCardItem([]RateCardItem{item}, "compute_instance", "USD", nil)
	if err != nil {
		t.Fatalf("ResolveRateCardItem() with empty dimensions error = %v", err)
	}
	if got.Amount != item.Amount {
		t.Fatalf("ResolveRateCardItem() amount = %q, want %q", got.Amount, item.Amount)
	}

	_, err = ResolveRateCardItem([]RateCardItem{item}, "compute_instance", "USD", map[string]string{"instance_type": "small"})
	if !errors.Is(err, ErrRateCoverageGap) {
		t.Fatalf("ResolveRateCardItem() with specified dimensions error = %v, want ErrRateCoverageGap", err)
	}
}

func TestResolveRateCardItemRejectsEquallySpecificMatches(t *testing.T) {
	condition := RateCondition{Dimension: "image_ref", Operator: RateMatchEquals, Value: "fedora-16"}
	rates := []RateCardItem{
		{Service: "compute_instance", CurrencyCode: "USD", Amount: "3.00", Unit: RateUnitHour, Conditions: []RateCondition{condition}},
		{Service: "compute_instance", CurrencyCode: "USD", Amount: "4.00", Unit: RateUnitHour, Conditions: []RateCondition{condition}},
	}

	_, err := ResolveRateCardItem(rates, "compute_instance", "USD", map[string]string{"image_ref": "fedora-16"})
	if !errors.Is(err, ErrAmbiguousRateCard) {
		t.Fatalf("ResolveRateCardItem() error = %v, want ErrAmbiguousRateCard", err)
	}
}

func TestResolveRateCardItemAllowsWildcardForUnknownDimensions(t *testing.T) {
	rates := []RateCardItem{{
		Service:      "compute_instance",
		CurrencyCode: "USD",
		Amount:       "4.50",
		Unit:         RateUnitHour,
		Conditions: []RateCondition{
			{Dimension: "image_ref", Operator: RateMatchAny},
		},
	}}

	got, err := ResolveRateCardItem(rates, "compute_instance", "USD", nil)
	if err != nil {
		t.Fatalf("ResolveRateCardItem() error = %v", err)
	}
	if got.Amount != "4.50" {
		t.Fatalf("ResolveRateCardItem() amount = %q, want 4.50", got.Amount)
	}
}

func TestResolveRateCardItemRejectsInvalidRuleAndCurrency(t *testing.T) {
	tests := []struct {
		name     string
		items    []RateCardItem
		currency string
	}{
		{
			name: "missing rate amount",
			items: []RateCardItem{{
				Service: "compute_instance", CurrencyCode: "USD", Unit: RateUnitHour,
			}},
			currency: "USD",
		},
		{
			name: "unsupported match operator",
			items: []RateCardItem{{
				Service:      "compute_instance",
				CurrencyCode: "USD",
				Amount:       "4.50",
				Unit:         RateUnitHour,
				Conditions: []RateCondition{{
					Dimension: "image_ref",
					Operator:  RateMatchUnspecified,
				}},
			}},
			currency: "USD",
		},
		{
			name:     "invalid currency code",
			items:    []RateCardItem{},
			currency: "U$D",
		},
		{
			name:     "unrecognized ISO currency code",
			items:    []RateCardItem{},
			currency: "ZZZ",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ResolveRateCardItem(test.items, "compute_instance", test.currency, nil)
			if !errors.Is(err, ErrInvalidRateCard) {
				t.Fatalf("ResolveRateCardItem() error = %v, want ErrInvalidRateCard", err)
			}
		})
	}
}

func TestResolveRateCardItemNormalizesISO4217CurrencyCodes(t *testing.T) {
	item := RateCardItem{
		Service:      "compute_instance",
		CurrencyCode: "usd",
		Amount:       "3.00",
		Unit:         RateUnitHour,
	}

	got, err := ResolveRateCardItem([]RateCardItem{item}, "compute_instance", "usd", nil)
	if err != nil {
		t.Fatalf("ResolveRateCardItem() error = %v", err)
	}
	if got.CurrencyCode != "USD" {
		t.Fatalf("ResolveRateCardItem() currency = %q, want normalized ISO code USD", got.CurrencyCode)
	}
}

func TestResolveRateCardItemRejectsUnrecognizedRateCurrency(t *testing.T) {
	item := RateCardItem{
		Service:      "compute_instance",
		CurrencyCode: "ZZZ",
		Amount:       "3.00",
		Unit:         RateUnitHour,
	}

	_, err := ResolveRateCardItem([]RateCardItem{item}, "compute_instance", "USD", nil)
	if !errors.Is(err, ErrInvalidRateCard) {
		t.Fatalf("ResolveRateCardItem() error = %v, want ErrInvalidRateCard", err)
	}
}
