/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package costmanagement

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

// buildHeartbeat constructs an OSAC v1 heartbeat CloudEvent shaped like the
// metering service publishes it.
func buildHeartbeat(
	id, resourceType, resourceID, meter, component, quantity, unit string,
	dimensions map[string]string,
	billingAcct *string,
) cloudevents.Event {
	ce := cloudevents.NewEvent()
	ce.SetSpecVersion(cloudevents.VersionV1)
	ce.SetID(id)
	ce.SetType(schema.HeartbeatType)
	ce.SetSource("osac-metering")
	ce.SetTime(time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC))
	ce.SetDataContentType(cloudevents.ApplicationJSON)
	ce.SetExtension(schema.ExtResourceID, resourceID)
	ce.SetExtension(schema.ExtResourceType, resourceType)
	ce.SetExtension(schema.ExtTenant, "acme")

	data := schema.HeartbeatData{
		TenantID:                 "acme",
		ResourceType:             resourceType,
		ResourceID:               resourceID,
		Meter:                    meter,
		Component:                component,
		BillingDimensions:        dimensions,
		HeartbeatTime:            "2026-10-05T10:01:00Z",
		LastAppliedOffset:        41,
		RulesRevision:            "0001",
		ExternalBillingAccountID: billingAcct,
		Usage: schema.Usage{
			Semantics: schema.UsageSemanticsInterval,
			From:      "2026-10-05T10:00:00Z",
			To:        "2026-10-05T10:01:00Z",
			Quantity:  quantity,
			Unit:      unit,
			Precision: schema.UsagePrecisionSecond,
		},
	}
	Expect(ce.SetData(cloudevents.ApplicationJSON, data)).To(Succeed())
	return ce
}

var _ = Describe("TranslateEvent", func() {
	It("locks the exact Cost Management JSON contract and strips OSAC-internal fields", func() {
		acct := "org-123456"
		ce := buildHeartbeat(
			"hb-golden-1",
			schema.ResourceTypeComputeInstance,
			"vm-7",
			"compute",
			"compute",
			"60.000000",
			"second",
			map[string]string{
				"deployment":    "cluster-a",
				"instance_type": "small",
				"project":       "web",
			},
			&acct,
		)

		translated, err := TranslateEvent(ce)
		Expect(err).NotTo(HaveOccurred())

		raw, err := json.Marshal(translated)
		Expect(err).NotTo(HaveOccurred())

		const expectedJSON = `{
			"specversion": "1.0",
			"id": "hb-golden-1",
			"source": "osac-metering",
			"type": "osac.resource.heartbeat.v1",
			"datacontenttype": "application/json",
			"time": "2026-10-05T10:00:00Z",
			"osacresourceid": "vm-7",
			"osacresourcetype": "compute_instance",
			"osactenant": "acme",
			"osacproject": "web",
			"data": {
				"schema_version": "v1",
				"tenant_id": "acme",
				"external_billing_account_id": "org-123456",
				"project_id": "web",
				"resource_type": "compute_instance",
				"resource_id": "vm-7",
				"meter": "compute",
				"component": "compute",
				"heartbeat_time": "2026-10-05T10:01:00Z",
				"usage": {
					"semantics": "interval",
					"from": "2026-10-05T10:00:00Z",
					"to": "2026-10-05T10:01:00Z",
					"quantity": "60.000000",
					"unit": "second",
					"precision": "second"
				},
				"billing_dimensions": {
					"deployment": "cluster-a",
					"instance_type": "small",
					"project": "web"
				}
			}
		}`
		Expect(raw).To(MatchJSON(expectedJSON))

		// Verify OSAC-internal bookkeeping fields never leak into the Cost
		// Management payload.
		var decoded map[string]any
		Expect(json.Unmarshal(raw, &decoded)).To(Succeed())
		dataObj := decoded["data"].(map[string]any)
		Expect(dataObj).NotTo(HaveKey("last_applied_offset"))
		Expect(dataObj).NotTo(HaveKey("rules_revision"))
	})

	It("translates every OSAC resource type including negative correction quantities", func() {
		for _, resourceType := range []string{
			schema.ResourceTypeComputeInstance,
			schema.ResourceTypeClusterOrder,
			schema.ResourceTypeBareMetalInstance,
			schema.ResourceTypeExternalIP,
			schema.ResourceTypeNATGateway,
			schema.ResourceTypeVolume,
		} {
			ce := buildHeartbeat(
				"hb-corr-"+resourceType, resourceType, "r-1", "allocation", "allocation",
				"-30.000000", "second", nil, nil,
			)
			got, err := TranslateEvent(ce)
			Expect(err).NotTo(HaveOccurred(), resourceType)
			Expect(got.ResourceType).To(Equal(resourceType))
			Expect(got.Data.ResourceType).To(Equal(resourceType))
			Expect(got.Data.Usage.Quantity).To(Equal("-30.000000"))
			Expect(got.Data.ExternalBillingAccountID).To(BeEmpty())
		}
	})

	It("translates MaaS inference usage events from the shared inference topic", func() {
		ce := cloudevents.NewEvent()
		ce.SetSpecVersion(cloudevents.VersionV1)
		ce.SetID("infer-1")
		ce.SetType(EventTypeInferenceUsage)
		ce.SetSource("osac-metering")
		ce.SetTime(time.Date(2026, 10, 5, 10, 1, 0, 0, time.UTC))
		ce.SetDataContentType(cloudevents.ApplicationJSON)
		Expect(ce.SetData(cloudevents.ApplicationJSON, map[string]any{
			"tenant_id":     "acme",
			"resource_type": ResourceTypeMaaSInference,
			"resource_id":   "infer-1",
			"billing_dimensions": map[string]any{
				"model_id":      "granite-8b",
				"prompt_tokens": 800,
				"empty":         "",
				"zero":          0,
			},
			"usage": map[string]any{
				"semantics": "delta",
				"quantity":  "1200.000000",
				"unit":      "token",
			},
		})).To(Succeed())

		got, err := TranslateEvent(ce)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Type).To(Equal(EventTypeInferenceUsage))
		Expect(got.ResourceType).To(Equal(ResourceTypeMaaSInference))
		Expect(got.Data.Meter).To(Equal("inference"))
		Expect(got.Data.Component).To(Equal("inference"))
		Expect(got.Data.Usage.Semantics).To(Equal("delta"))
		Expect(got.Data.Usage.Quantity).To(Equal("1200.000000"))
		Expect(got.Data.Usage.Unit).To(Equal("token"))
		Expect(got.Data.BillingDimensions).To(Equal(map[string]string{
			"model_id":      "granite-8b",
			"prompt_tokens": "800",
		}))
	})

	DescribeTable("rejects invalid events with NonRetryableError",
		func(mutate func(*cloudevents.Event)) {
			ce := buildHeartbeat(
				"hb-invalid", schema.ResourceTypeComputeInstance, "vm-1",
				"compute", "compute", "60.000000", "second", nil, nil,
			)
			mutate(&ce)
			_, err := TranslateEvent(ce)
			Expect(err).To(HaveOccurred())
			var nonRetryable *adapters.NonRetryableError
			Expect(errors.As(err, &nonRetryable)).To(BeTrue(), "expected NonRetryableError, got %T: %v", err, err)
		},
		Entry("empty ID", func(ce *cloudevents.Event) {
			*ce = buildHeartbeat("", schema.ResourceTypeComputeInstance, "vm-1", "compute", "compute", "1.000000", "second", nil, nil)
		}),
		Entry("zero timestamp", func(ce *cloudevents.Event) {
			ce.SetTime(time.Time{})
		}),
		Entry("empty source", func(ce *cloudevents.Event) {
			fresh := cloudevents.NewEvent()
			fresh.SetSpecVersion(cloudevents.VersionV1)
			fresh.SetID("hb-no-src")
			fresh.SetType(schema.HeartbeatType)
			fresh.SetTime(time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC))
			*ce = fresh
		}),
		Entry("unsupported CloudEvent type (future schema version)", func(ce *cloudevents.Event) {
			ce.SetType("osac.resource.heartbeat.v2")
		}),
		Entry("legacy lifecycle CloudEvent type", func(ce *cloudevents.Event) {
			ce.SetType("osac.resource.created.v1")
		}),
		Entry("unsupported datacontenttype", func(ce *cloudevents.Event) {
			ce.SetDataContentType("text/plain")
		}),
		Entry("unknown resource_type", func(ce *cloudevents.Event) {
			*ce = buildHeartbeat("hb-bad-rt", "mystery", "r-1", "compute", "compute", "1.000000", "second", nil, nil)
		}),
		Entry("missing meter", func(ce *cloudevents.Event) {
			*ce = buildHeartbeat("hb-no-meter", schema.ResourceTypeComputeInstance, "vm-1", "", "compute", "1.000000", "second", nil, nil)
		}),
		Entry("non-decimal quantity", func(ce *cloudevents.Event) {
			*ce = buildHeartbeat("hb-bad-qty", schema.ResourceTypeComputeInstance, "vm-1", "compute", "compute", "not-a-number", "second", nil, nil)
		}),
		Entry("extension tenant mismatch", func(ce *cloudevents.Event) {
			ce.SetExtension(schema.ExtTenant, "other-tenant")
		}),
		Entry("extension resource ID mismatch", func(ce *cloudevents.Event) {
			ce.SetExtension(schema.ExtResourceID, "other-vm")
		}),
		Entry("non-JSON-object payload", func(ce *cloudevents.Event) {
			Expect(ce.SetData(cloudevents.ApplicationJSON, json.RawMessage(`"just a string"`))).To(Succeed())
		}),
		Entry("usage.to before usage.from", func(ce *cloudevents.Event) {
			var data map[string]any
			Expect(ce.DataAs(&data)).To(Succeed())
			usage := data["usage"].(map[string]any)
			usage["from"] = "2026-10-05T10:05:00Z"
			usage["to"] = "2026-10-05T10:01:00Z"
			Expect(ce.SetData(cloudevents.ApplicationJSON, data)).To(Succeed())
		}),
		Entry("invalid heartbeat_time", func(ce *cloudevents.Event) {
			var data map[string]any
			Expect(ce.DataAs(&data)).To(Succeed())
			data["heartbeat_time"] = "not-a-timestamp"
			Expect(ce.SetData(cloudevents.ApplicationJSON, data)).To(Succeed())
		}),
	)
})
