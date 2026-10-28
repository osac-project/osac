/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is
distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and limitations under the License.
*/

// Package metertest reads labeled metric values from the default registry, where the
// metrics package registers its metrics. Tests across packages use it to assert what
// the loops actually exported.
package metertest

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// LabeledValue returns the value of the series the metric family carries for exactly the
// given labels, or 0 when no such series exists. Counters and gauges are both read.
func LabeledValue(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if !matchesLabels(metric, labels) {
				continue
			}
			if metric.GetCounter() != nil {
				return metric.GetCounter().GetValue()
			}
			if metric.GetGauge() != nil {
				return metric.GetGauge().GetValue()
			}
		}
	}
	return 0
}

// matchesLabels reports whether the series carries exactly the given labels.
func matchesLabels(metric *dto.Metric, want map[string]string) bool {
	seen := map[string]string{}
	for _, label := range metric.GetLabel() {
		seen[label.GetName()] = label.GetValue()
	}
	if len(seen) != len(want) {
		return false
	}
	for name, value := range want {
		if seen[name] != value {
			return false
		}
	}
	return true
}
