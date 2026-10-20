/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package rules_test

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/osac-project/osac-metering/internal/rules"
)

// The default meters ship twice: in code as the fallback definition and in the chart as the
// ConfigMap installs get. They must never drift apart.
func TestDefaultMetersMatchTheChart(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	chartPath := filepath.Join(
		filepath.Dir(testFile), "..", "..", "..", "charts", "osac-metering", "rules", "0001-meters.yaml",
	)
	chart, err := os.ReadFile(chartPath)
	if err != nil {
		t.Fatalf("reading the chart's default meters: %v", err)
	}

	revision, err := rules.DefaultRevision()
	if err != nil {
		t.Fatalf("parsing the default revision: %v", err)
	}
	parsed, err := rules.Parse(
		rules.NamePrefix+"0001", "1970-01-01T00:00:00Z", string(chart),
	)
	if err != nil {
		t.Fatalf("the chart's meters do not parse: %v", err)
	}
	if !sameMeters(parsed.Meters, revision.Meters) {
		t.Fatal("the chart's default meters differ from the ones in code")
	}
}

// sameMeters compares two meter definition lists by value. The component is compared by
// its effective value, so an omitted default matches an explicit one, but different
// components never match: heartbeats carry the component, so a drift there is a billing
// contract drift.
func sameMeters(a, b []rules.Meter) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		left, right := a[i], b[i]
		if left.EffectiveComponent() != right.EffectiveComponent() {
			return false
		}
		left.Component, right.Component = "", ""
		if !reflect.DeepEqual(left, right) {
			return false
		}
	}
	return true
}

// TestSameMetersComparesComponents checks that the drift test fails when the chart and the
// code name different components for the same meter.
func TestSameMetersComparesComponents(t *testing.T) {
	base := rules.Meter{Resource: "ComputeInstance", Meter: "compute", Unit: "second"}
	if sameMeters(
		[]rules.Meter{base},
		[]rules.Meter{{Resource: "ComputeInstance", Meter: "compute", Unit: "second",
			Component: "hypervisor"}},
	) {
		t.Fatal("meters with different components must not match")
	}
	// An explicit default component is the same billing contract as an omitted one.
	if !sameMeters(
		[]rules.Meter{base},
		[]rules.Meter{{Resource: "ComputeInstance", Meter: "compute", Unit: "second",
			Component: "compute"}},
	) {
		t.Fatal("an explicit default component must match an omitted one")
	}
}
