/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package costmanagement_test

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"

	"github.com/osac-project/osac-metering/adapters"
	"github.com/osac-project/osac-metering/adapters/costmanagement"
	"github.com/osac-project/osac-metering/schema"
)

// runIDs scopes contract-test event IDs to one run so repeated executions
// against the same Cost Management receiver do not collide with permanently
// deduplicated IDs from earlier runs.
func runIDs(run time.Time) map[string]string {
	suffix := "-" + run.UTC().Format("20060102T150405.000000000Z")
	ids := make(map[string]string, len(contractCases))
	for _, name := range contractCases {
		ids[name] = "cm-contract-" + name + suffix
	}
	return ids
}

var contractCases = []string{
	"positive", "negative", "duplicate", "utc-day", "invalid", "invoiced-correction",
}

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

// TestCostManagementContract verifies the live Cost Management receiver
// against the OSAC billing invariants: positive usage, negative correction,
// duplicate ID idempotency, backdated UTC-day interval, invalid heartbeat
// rejection, and correction in an invoiced period.
//
// Configure with:
//
//	COST_MANAGEMENT_TEST_API_URL     base URL of the Cost Management receiver
//	COST_MANAGEMENT_TEST_API_TOKEN   optional Bearer token
//	COST_MANAGEMENT_TEST_TENANT      tenant identifier to bill in the test
//
// The test skips itself when COST_MANAGEMENT_TEST_API_URL or
// COST_MANAGEMENT_TEST_TENANT is unset.
func TestCostManagementContract(t *testing.T) {
	apiURL := os.Getenv("COST_MANAGEMENT_TEST_API_URL")
	token := os.Getenv("COST_MANAGEMENT_TEST_API_TOKEN")
	tenant := os.Getenv("COST_MANAGEMENT_TEST_TENANT")
	if apiURL == "" || tenant == "" {
		t.Skip("Cost Management contract test is not configured; set COST_MANAGEMENT_TEST_API_URL and COST_MANAGEMENT_TEST_TENANT")
	}

	adapter := costmanagement.NewAdapter(apiURL, token, logr.Discard())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	ids := runIDs(time.Now())

	if err := adapter.HealthCheck(ctx); err != nil {
		t.Fatalf("Cost Management receiver is not reachable: %v", err)
	}

	build := func(id, quantity string, from, to time.Time) adapters.MeteringEvent {
		ce := cloudevents.NewEvent()
		ce.SetSpecVersion(cloudevents.VersionV1)
		ce.SetID(id)
		ce.SetType(schema.HeartbeatType)
		ce.SetSource("osac-metering-contract-test")
		ce.SetTime(from)
		ce.SetDataContentType(cloudevents.ApplicationJSON)
		ce.SetExtension(schema.ExtResourceID, "contract-test-vm")
		ce.SetExtension(schema.ExtResourceType, schema.ResourceTypeComputeInstance)
		ce.SetExtension(schema.ExtTenant, tenant)

		data := schema.HeartbeatData{
			TenantID:          tenant,
			ResourceType:      schema.ResourceTypeComputeInstance,
			ResourceID:        "contract-test-vm",
			Meter:             "compute",
			Component:         "compute",
			HeartbeatTime:     to.Format(time.RFC3339),
			LastAppliedOffset: 1,
			RulesRevision:     "0001",
			BillingDimensions: map[string]string{
				"deployment": "contract-test",
				"tenant":     tenant,
				"meter_type": "second",
			},
			Usage: schema.Usage{
				Semantics: schema.UsageSemanticsInterval,
				From:      from.Format(time.RFC3339),
				To:        to.Format(time.RFC3339),
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

	t.Run("accepts positive interval usage", func(t *testing.T) {
		from := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
		if err := adapter.Submit(ctx, build(ids["positive"], "60.000000", from, from.Add(time.Minute))); err != nil {
			t.Fatalf("submitting positive heartbeat: %v", err)
		}
	})

	t.Run("accepts negative quantity as a correction", func(t *testing.T) {
		from := time.Now().UTC().Add(-90 * time.Second).Truncate(time.Second)
		if err := adapter.Submit(ctx, build(ids["negative"], "-30.000000", from, from.Add(30*time.Second))); err != nil {
			t.Fatalf("submitting negative heartbeat: %v", err)
		}
	})

	t.Run("drops repeated heartbeat ID idempotently", func(t *testing.T) {
		from := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
		event := build(ids["duplicate"], "60.000000", from, from.Add(time.Minute))
		if err := adapter.Submit(ctx, event); err != nil {
			t.Fatalf("submitting first copy: %v", err)
		}
		if err := adapter.Submit(ctx, event); err != nil {
			t.Fatalf("submitting duplicate copy: %v", err)
		}
	})

	t.Run("accepts heartbeat dated in the previous UTC day", func(t *testing.T) {
		day := time.Now().UTC().Add(-24 * time.Hour).Truncate(24 * time.Hour)
		if err := adapter.Submit(ctx, build(ids["utc-day"], "1.000000", day, day.Add(time.Second))); err != nil {
			t.Fatalf("submitting dated heartbeat: %v", err)
		}
	})

	t.Run("rejects invalid heartbeat synchronously", func(t *testing.T) {
		ce := cloudevents.NewEvent()
		ce.SetSpecVersion(cloudevents.VersionV1)
		ce.SetID(ids["invalid"])
		ce.SetType(schema.HeartbeatType)
		ce.SetSource("osac-metering-contract-test")
		ce.SetTime(time.Now().UTC())
		ce.SetDataContentType(cloudevents.ApplicationJSON)
		if err := ce.SetData(cloudevents.ApplicationJSON, map[string]any{
			"tenant_id": tenant, "resource_type": schema.ResourceTypeComputeInstance,
			"resource_id": "contract-test-vm", "meter": "compute", "component": "compute",
			"heartbeat_time": time.Now().UTC().Format(time.RFC3339),
			"usage": schema.Usage{
				Semantics: "interval", From: "not-a-time", To: "not-a-time",
				Quantity: "1.000000", Unit: "second", Precision: "second",
			},
		}); err != nil {
			t.Fatalf("building invalid heartbeat: %v", err)
		}
		if err := adapter.Submit(ctx, adapters.MeteringEvent{CloudEvent: ce}); err == nil {
			t.Fatal("invalid heartbeat must be rejected synchronously")
		}
	})

	t.Run("accepts correction in an already invoiced period", func(t *testing.T) {
		day := time.Now().UTC().Add(-48 * time.Hour).Truncate(24 * time.Hour)
		if err := adapter.Submit(ctx, build(ids["invoiced-correction"], "-1.000000", day, day.Add(time.Second))); err != nil {
			t.Fatalf("submitting correction in invoiced period: %v", err)
		}
	})
}
