/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package m360

import (
	"encoding/json"
	"errors"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac-metering/adapters"
	"github.com/osac-project/osac-metering/schema"
)

// buildHeartbeat builds a heartbeat CloudEvent shaped like the metering service writes it.
func buildHeartbeat(
	id string, resourceType, resourceID, meter, component, quantity, unit string,
	dimensions map[string]any,
) cloudevents.Event {
	ce := cloudevents.NewEvent()
	ce.SetSpecVersion("1.0")
	ce.SetID(id)
	ce.SetType(schema.HeartbeatType)
	ce.SetSource("osac-metering")
	ce.SetTime(time.Date(2026, 10, 5, 10, 1, 0, 0, time.UTC))
	ce.SetDataContentType("application/json")

	data := map[string]any{
		"tenant_id":           "acme",
		"resource_type":       resourceType,
		"resource_id":         resourceID,
		"meter":               meter,
		"component":           component,
		"heartbeat_time":      "2026-10-05T10:01:00Z",
		"last_applied_offset": 41,
		"rules_revision":      "0001",
		"usage": map[string]any{
			"semantics": "interval",
			"from":      "2026-10-05T10:00:00Z",
			"to":        "2026-10-05T10:01:00Z",
			"quantity":  quantity,
			"unit":      unit,
			"precision": "second",
		},
	}
	if dimensions != nil {
		data["billing_dimensions"] = dimensions
	}
	Expect(ce.SetData(cloudevents.ApplicationJSON, data)).To(Succeed())
	return ce
}

var _ = Describe("translateEvent", func() {
	It("translates a compute heartbeat to the vmaas endpoint", func() {
		ce := buildHeartbeat(
			"hb-1", schema.ResourceTypeComputeInstance, "vm-7", "compute", "compute",
			"60.000000", "second",
			map[string]any{"instance_type": "small", "project": "web"},
		)
		endpoint, payload, err := translateEvent(ce)
		Expect(err).NotTo(HaveOccurred())
		Expect(endpoint).To(Equal("/vmaas/event"))
		Expect(payload).To(SatisfyAll(
			HaveKeyWithValue("event_id", "hb-1"),
			HaveKeyWithValue("tenant_id", "acme"),
			HaveKeyWithValue("resource_id", "vm-7"),
			HaveKeyWithValue("resource_type", schema.ResourceTypeComputeInstance),
			HaveKeyWithValue("meter", "compute"),
			HaveKeyWithValue("component", "compute"),
			HaveKeyWithValue("rules_revision", "0001"),
			HaveKeyWithValue("last_applied_offset", float64(41)),
			HaveKeyWithValue("usage_semantics", "interval"),
			HaveKeyWithValue("interval_from", "2026-10-05T10:00:00Z"),
			HaveKeyWithValue("interval_to", "2026-10-05T10:01:00Z"),
			HaveKeyWithValue("usage_quantity", "60.000000"),
			HaveKeyWithValue("usage_unit", "second"),
			HaveKeyWithValue("usage_precision", "second"),
			HaveKeyWithValue("instance_type", "small"),
			HaveKeyWithValue("project", "web"),
		))
	})

	It("routes every resource type to its endpoint", func() {
		for resourceType, endpoint := range map[string]string{
			schema.ResourceTypeComputeInstance:   "/vmaas/event",
			schema.ResourceTypeClusterOrder:      "/caas/event",
			schema.ResourceTypeBareMetalInstance: "/bmaas/event",
			schema.ResourceTypeExternalIP:        "/networking/externalip/event",
			schema.ResourceTypeNATGateway:        "/networking/natgateway/event",
			schema.ResourceTypeVolume:            "/storage/volume/event",
		} {
			ce := buildHeartbeat(
				"hb-x", resourceType, "r-1", "allocation", "allocation",
				"-30.000000", "second", nil,
			)
			got, payload, err := translateEvent(ce)
			Expect(err).NotTo(HaveOccurred(), resourceType)
			Expect(got).To(Equal(endpoint))
			// A correction keeps its negative quantity.
			Expect(payload).To(HaveKeyWithValue("usage_quantity", "-30.000000"))
		}
	})

	It("translates a MaaS inference event from the shared inference topic", func() {
		ce := cloudevents.NewEvent()
		ce.SetSpecVersion("1.0")
		ce.SetID("infer-1")
		ce.SetType("osac.inference.usage.v1")
		ce.SetSource("osac-metering")
		ce.SetTime(time.Date(2026, 10, 5, 10, 1, 0, 0, time.UTC))
		Expect(ce.SetData(cloudevents.ApplicationJSON, map[string]any{
			"tenant_id":     "acme",
			"resource_type": "maas_inference",
			"resource_id":   "infer-1",
			"billing_dimensions": map[string]any{
				"model_id": "granite-8b",
			},
			"usage": map[string]any{
				"semantics": "delta",
				"quantity":  "1200.000000",
				"unit":      "token",
			},
		})).To(Succeed())

		endpoint, payload, err := translateEvent(ce)
		Expect(err).NotTo(HaveOccurred())
		Expect(endpoint).To(Equal("/maas/event"))
		Expect(payload).To(HaveKeyWithValue("event_id", "infer-1"))
		Expect(payload).To(HaveKeyWithValue("model_id", "granite-8b"))
		Expect(payload).To(HaveKeyWithValue("usage_quantity", "1200.000000"))
	})

	It("replaces missing canonical fields with the space string", func() {
		ce := buildHeartbeat(
			"hb-2", schema.ResourceTypeVolume, "vol-1", "capacity", "capacity",
			"600.000000", schema.UsageUnitGiByteSecond, nil,
		)
		var data map[string]any
		Expect(ce.DataAs(&data)).To(Succeed())
		delete(data, "rules_revision")
		Expect(ce.SetData(cloudevents.ApplicationJSON, data)).To(Succeed())
		_, payload, err := translateEvent(ce)
		Expect(err).NotTo(HaveOccurred())
		Expect(payload).To(HaveKeyWithValue("rules_revision", " "))
	})

	It("skips non-billable billing dimensions", func() {
		ce := buildHeartbeat(
			"hb-3", schema.ResourceTypeVolume, "vol-1", "capacity", "capacity",
			"600.000000", schema.UsageUnitGiByteSecond,
			map[string]any{"storage_tier": "gold", "empty": "", "zero": 0, "nil": nil},
		)
		_, payload, err := translateEvent(ce)
		Expect(err).NotTo(HaveOccurred())
		Expect(payload).To(HaveKeyWithValue("storage_tier", "gold"))
		Expect(payload).NotTo(HaveKey("empty"))
		Expect(payload).NotTo(HaveKey("zero"))
		Expect(payload).NotTo(HaveKey("nil"))
	})

	It("prevents billing dimensions from overwriting canonical fields", func() {
		ce := buildHeartbeat(
			"hb-4", schema.ResourceTypeComputeInstance, "vm-7", "compute", "compute",
			"60.000000", "second",
			map[string]any{"event_id": "spoofed", "tenant_id": "other"},
		)
		_, payload, err := translateEvent(ce)
		Expect(err).NotTo(HaveOccurred())
		Expect(payload).To(HaveKeyWithValue("event_id", "hb-4"))
		Expect(payload).To(HaveKeyWithValue("tenant_id", "acme"))
	})

	It("returns NonRetryableError for unknown resource types", func() {
		ce := buildHeartbeat(
			"hb-5", "mystery", "r-1", "compute", "compute", "1.000000", "second", nil,
		)
		_, _, err := translateEvent(ce)
		var nonRetryable *adapters.NonRetryableError
		Expect(errors.As(err, &nonRetryable)).To(BeTrue())
	})

	It("returns NonRetryableError for an empty CloudEvent ID", func() {
		ce := buildHeartbeat(
			"", schema.ResourceTypeVolume, "vol-1", "capacity", "capacity",
			"1.000000", "second", nil,
		)
		_, _, err := translateEvent(ce)
		var nonRetryable *adapters.NonRetryableError
		Expect(errors.As(err, &nonRetryable)).To(BeTrue())
	})

	It("returns NonRetryableError for a zero CloudEvent timestamp", func() {
		ce := buildHeartbeat(
			"hb-6", schema.ResourceTypeVolume, "vol-1", "capacity", "capacity",
			"1.000000", "second", nil,
		)
		ce.SetTime(time.Time{})
		_, _, err := translateEvent(ce)
		var nonRetryable *adapters.NonRetryableError
		Expect(errors.As(err, &nonRetryable)).To(BeTrue())
	})

	It("returns NonRetryableError for data that is not an object", func() {
		ce := cloudevents.NewEvent()
		ce.SetID("hb-7")
		ce.SetType(schema.HeartbeatType)
		ce.SetTime(time.Now())
		Expect(ce.SetData(cloudevents.ApplicationJSON, json.RawMessage(`"just a string"`))).To(Succeed())
		_, _, err := translateEvent(ce)
		var nonRetryable *adapters.NonRetryableError
		Expect(errors.As(err, &nonRetryable)).To(BeTrue())
	})
})
