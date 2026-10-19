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

import "testing"

func TestNormalizeRecurringRate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		amount      string
		unit        RateUnit
		wantHourly  string
		wantMonthly string
		wantError   bool
	}{
		{
			name:        "hour rate",
			amount:      "0.125",
			unit:        RateUnitHour,
			wantHourly:  "0.125",
			wantMonthly: "91.25",
		},
		{
			name:        "month rate",
			amount:      "91.25",
			unit:        RateUnitMonth,
			wantHourly:  "0.125",
			wantMonthly: "91.25",
		},
		{
			name:        "repeating conversion keeps configured precision",
			amount:      "1",
			unit:        RateUnitMonth,
			wantHourly:  "0.00137",
			wantMonthly: "1",
		},
		{
			name:        "negative hourly credit rate",
			amount:      "-1",
			unit:        RateUnitHour,
			wantHourly:  "-1",
			wantMonthly: "-730",
		},
		{
			name:      "unknown unit",
			amount:    "1",
			unit:      "day",
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			hourly, monthly, err := NormalizeRecurringRate(test.amount, test.unit)
			if test.wantError {
				if err == nil {
					t.Fatal("NormalizeRecurringRate() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeRecurringRate() error = %v", err)
			}
			if hourly != test.wantHourly || monthly != test.wantMonthly {
				t.Fatalf("NormalizeRecurringRate() = (%q, %q), want (%q, %q)", hourly, monthly, test.wantHourly, test.wantMonthly)
			}
		})
	}
}

func TestComponentRateAmount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		rate       RateCardItem
		dimensions map[string]string
		want       string
		wantError  bool
	}{
		{name: "ten nodes", rate: RateCardItem{Amount: "2.75", Unit: RateUnitHour, QuantityDimension: "node_count"}, dimensions: map[string]string{"node_count": "10"}, want: "27.5"},
		{name: "disk size", rate: RateCardItem{Amount: "0.02", Unit: RateUnitHour, QuantityDimension: "additional_disk_size_gib"}, dimensions: map[string]string{"additional_disk_size_gib": "50"}, want: "1"},
		{name: "exponent notation", rate: RateCardItem{Amount: "1e-3", Unit: RateUnitHour, QuantityDimension: "node_count"}, dimensions: map[string]string{"node_count": "10"}, want: "0.01"},
		{name: "positive exponent notation", rate: RateCardItem{Amount: "1E+2", Unit: RateUnitHour, QuantityDimension: "node_count"}, dimensions: map[string]string{"node_count": "10"}, want: "1000"},
		{name: "flat monthly rate", rate: RateCardItem{Amount: "5", Unit: RateUnitMonth}, dimensions: map[string]string{"node_count": "10"}, want: "5"},
		{name: "VM hourly rate", rate: RateCardItem{Amount: "4", Unit: RateUnitHour}, dimensions: map[string]string{"additional_disk_size_gib": "50"}, want: "4"},
		{name: "missing quantity", rate: RateCardItem{Amount: "2", QuantityDimension: "node_count"}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := ComponentRateAmount(test.rate, test.dimensions)
			if (err != nil) != test.wantError || (!test.wantError && got != test.want) {
				t.Fatalf("ComponentRateAmount() = (%q, %v), want (%q, error=%t)", got, err, test.want, test.wantError)
			}
		})
	}
}
