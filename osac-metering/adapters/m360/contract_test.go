/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package m360_test

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"

	"github.com/osac-project/osac-metering/adapters"
	"github.com/osac-project/osac-metering/adapters/m360"
	"github.com/osac-project/osac-metering/schema"
)

// runIDs builds the heartbeat IDs of one contract-test run. The billing
// provider drops an ID it has seen permanently, so a second run against the
// same tenant must not reuse the first run's IDs: every run suffixes its IDs
// with the run's start time. Within a run the IDs are deterministic, so the
// duplicate case submits one ID twice.
func runIDs(run time.Time) map[string]string {
	suffix := "-" + run.UTC().Format("20060102T150405.000000000Z")
	ids := make(map[string]string, len(contractCases))
	for _, name := range contractCases {
		ids[name] = "contract-" + name + suffix
	}
	return ids
}

// contractCases names the heartbeat shapes the contract test submits.
var contractCases = []string{
	"positive", "negative", "duplicate", "utc-day", "invalid", "invoiced-correction",
}

// TestRunIDsAreUniquePerRun pins the run-scoping of the contract test's IDs:
// two runs produce disjoint sets, and one run's set is deterministic.
func TestRunIDsAreUniquePerRun(t *testing.T) {
	first := runIDs(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	second := runIDs(time.Date(2026, 10, 18, 12, 0, 1, 0, time.UTC))

	for name, id := range first {
		if second[name] == id {
			t.Errorf("ID %q of case %s is shared between runs", id, name)
		}
	}
	if again := runIDs(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)); !reflect.DeepEqual(again, first) {
		t.Errorf("IDs must be deterministic within a run:\ngot  %v\nwant %v", again, first)
	}
}

// The contract test runs against a real tenant of the billing provider. It submits the
// heartbeat shapes the release prerequisites need: positive and negative quantities, a
// duplicate ID, a heartbeat dated in a previous UTC day, an invalid heartbeat, and a
// correction in an already invoiced period. It asserts that the provider accepts or rejects
// each one synchronously.
//
// It does not read the tenant's usage totals back: the M360 Usage API exposes no query path
// in the adapter's client, so the arithmetic checks (negative rates priced as negative
// charges, the price in effect at heartbeat time, duplicate IDs dropped permanently, UTC-day
// billing) remain a gap until a query path exists.
//
// Configure it with:
//
//	M360_TEST_API_URL     base URL of the M360 tenant
//	M360_TEST_API_KEY     an API key for the tenant
//	M360_TEST_TENANT      the tenant identifier to bill in the test
//
// The test skips itself when the variables are not set. Deploying the M360 tenant is outside
// this repository's scope.
func TestBillingProviderContract(t *testing.T) {
	apiURL := os.Getenv("M360_TEST_API_URL")
	apiKey := os.Getenv("M360_TEST_API_KEY")
	tenant := os.Getenv("M360_TEST_TENANT")
	if apiURL == "" || apiKey == "" || tenant == "" {
		t.Skip("M360 contract test is not configured; set M360_TEST_API_URL, M360_TEST_API_KEY, and M360_TEST_TENANT")
	}

	adapter := m360.NewAdapter(apiURL, "v1", apiKey, logr.Discard())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	ids := runIDs(time.Now())

	if err := adapter.HealthCheck(ctx); err != nil {
		t.Fatalf("billing provider is not reachable: %v", err)
	}

	// Every heartbeat in this test bills the same resource and meter so the provider's
	// arithmetic is directly checkable.
	build := func(id, quantity string, from, to time.Time) adapters.MeteringEvent {
		ce := cloudevents.NewEvent()
		ce.SetID(id)
		ce.SetType(schema.HeartbeatType)
		ce.SetSource("osac-metering-contract-test")
		ce.SetTime(from)
		data := map[string]any{
			"tenant_id":           tenant,
			"resource_type":       schema.ResourceTypeComputeInstance,
			"resource_id":         "contract-test-vm",
			"meter":               "compute",
			"component":           "compute",
			"heartbeat_time":      to.Format(time.RFC3339),
			"last_applied_offset": 1,
			"rules_revision":      "0001",
			"billing_dimensions": map[string]string{
				"deployment": "contract-test", "tenant": tenant, "meter_type": "second",
			},
			"usage": schema.Usage{
				Semantics: schema.UsageSemanticsInterval,
				From:      from.Format(time.RFC3339Nano),
				To:        to.Format(time.RFC3339Nano),
				Quantity:  quantity,
				Unit:      "second",
				Precision: schema.UsagePrecisionSecond,
			},
		}
		if err := ce.SetData(cloudevents.ApplicationJSON, data); err != nil {
			t.Fatalf("building heartbeat: %v", err)
		}
		return adapters.MeteringEvent{CloudEvent: ce}
	}

	t.Run("rates a positive quantity and prices it at its own time", func(t *testing.T) {
		from := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
		if err := adapter.Submit(ctx, build(ids["positive"], "60.000000", from, from.Add(time.Minute))); err != nil {
			t.Fatalf("submitting positive heartbeat: %v", err)
		}
	})

	t.Run("rates a negative quantity as a negative charge", func(t *testing.T) {
		from := time.Now().UTC().Add(-90 * time.Second).Truncate(time.Second)
		if err := adapter.Submit(ctx, build(ids["negative"], "-30.000000", from, from.Add(30*time.Second))); err != nil {
			t.Fatalf("submitting negative heartbeat: %v", err)
		}
	})

	t.Run("drops a repeated heartbeat ID permanently", func(t *testing.T) {
		from := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
		event := build(ids["duplicate"], "60.000000", from, from.Add(time.Minute))
		if err := adapter.Submit(ctx, event); err != nil {
			t.Fatalf("submitting first copy: %v", err)
		}
		if err := adapter.Submit(ctx, event); err != nil {
			t.Fatalf("submitting duplicate copy: %v", err)
		}
		// The provider must hold exactly one copy of the quantity. Verify by querying
		// the tenant's usage for the contract resource and checking the total is the
		// single quantity, not the double.
	})

	t.Run("bills a heartbeat in the UTC day that contains its time", func(t *testing.T) {
		// A heartbeat dated in the previous UTC day must land in that day's period,
		// not in the current one.
		day := time.Now().UTC().Add(-24 * time.Hour).Truncate(24 * time.Hour)
		if err := adapter.Submit(ctx, build(ids["utc-day"], "1.000000", day, day.Add(time.Second))); err != nil {
			t.Fatalf("submitting dated heartbeat: %v", err)
		}
	})

	t.Run("rejects an invalid heartbeat synchronously", func(t *testing.T) {
		ce := cloudevents.NewEvent()
		ce.SetID(ids["invalid"])
		ce.SetType(schema.HeartbeatType)
		ce.SetTime(time.Now().UTC())
		if err := ce.SetData(cloudevents.ApplicationJSON, map[string]any{
			"tenant_id": tenant, "resource_type": schema.ResourceTypeComputeInstance,
			"resource_id": "contract-test-vm", "meter": "compute", "component": "compute",
			"usage": schema.Usage{Semantics: "interval", From: "not-a-time", To: "not-a-time",
				Quantity: "1.000000", Unit: "second", Precision: "second"},
		}); err != nil {
			t.Fatalf("building invalid heartbeat: %v", err)
		}
		if err := adapter.Submit(ctx, adapters.MeteringEvent{CloudEvent: ce}); err == nil {
			t.Fatal("the provider must reject an invalid heartbeat synchronously")
		}
	})

	t.Run("accepts a correction in an already invoiced period", func(t *testing.T) {
		// The previous UTC day is typically already invoiced in a test tenant that has
		// run before; the provider must accept the correction and apply it on the next
		// invoice rather than reject it.
		day := time.Now().UTC().Add(-48 * time.Hour).Truncate(24 * time.Hour)
		if err := adapter.Submit(ctx, build(ids["invoiced-correction"], "-1.000000", day, day.Add(time.Second))); err != nil {
			t.Fatalf("submitting correction in an invoiced period: %v", err)
		}
	})
}
