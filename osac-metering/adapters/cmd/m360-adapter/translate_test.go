/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package main

import (
	"errors"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac-metering/adapters"
)

// helper to build a CloudEvent from the canonical format
func buildCloudEvent(
	id, ceType, resourceID, resourceType, tenantID, projectID string,
	billingDimensions map[string]any,
) cloudevents.Event {
	ce := cloudevents.NewEvent()
	ce.SetSpecVersion("1.0")
	ce.SetID(id)
	ce.SetType(ceType)
	ce.SetSource("osac-metering-service")
	ce.SetSubject(resourceType + "/" + resourceID)
	ce.SetTime(time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC))
	ce.SetDataContentType("application/json")

	data := map[string]any{
		"resource_id":      resourceID,
		"resource_type":    resourceType,
		"tenant_id":        tenantID,
		"project_id":       projectID,
		"catalog_item_id":  "ci-rhel10-gpu",
		"template_id":      "tmpl-rhel10-large-gpu",
		"previous_state":   nil,
		"current_state":    "STARTING",
		"transition_time":  "2026-07-20T10:00:00Z",
		"duration_seconds": 0,
		"schema_version":   "v1",
	}
	if billingDimensions != nil {
		data["billing_dimensions"] = billingDimensions
	}
	ExpectWithOffset(1, ce.SetData(cloudevents.ApplicationJSON, data)).To(Succeed())
	return ce
}

var _ = Describe("translateEvent", func() {
	Describe("VMaaS events", func() {
		It("translates a compute_instance event to flat M360 payload", func() {
			ce := buildCloudEvent(
				"ce-abc123",
				"osac.resource.created.v1",
				"vm-001",
				"compute_instance",
				"tenant-acme",
				"project-gpu",
				map[string]any{
					"instance_type":      "large-gpu",
					"image_ref":          "rhel-10.2-x86_64",
					"boot_disk_size_gib": 100,
				},
			)

			endpoint, payload, err := translateEvent(ce)

			Expect(err).NotTo(HaveOccurred())
			Expect(endpoint).To(Equal("/vmaas/event"))
			Expect(payload["event_id"]).To(Equal("ce-abc123"))
			Expect(payload["event_time"]).To(Equal("2026-07-20T10:00:00Z"))
			Expect(payload["resource_type"]).To(Equal("compute_instance"))
			Expect(payload["instance_type"]).To(Equal("large-gpu"))
			Expect(payload["image_ref"]).To(Equal("rhel-10.2-x86_64"))
			Expect(payload["boot_disk_size_gib"]).To(BeEquivalentTo(100))
		})

		It("replaces nil previous_state with space string", func() {
			ce := buildCloudEvent(
				"ce-nil-test",
				"osac.resource.created.v1",
				"vm-002",
				"compute_instance",
				"tenant-acme",
				"project-gpu",
				map[string]any{"instance_type": "medium"},
			)

			_, payload, err := translateEvent(ce)

			Expect(err).NotTo(HaveOccurred())
			Expect(payload["previous_state"]).To(Equal(" "))
		})

		It("replaces empty string fields with space string", func() {
			ce := buildCloudEvent(
				"ce-empty-test",
				"osac.resource.created.v1",
				"vm-003",
				"compute_instance",
				"tenant-acme",
				"",
				map[string]any{"instance_type": "medium"},
			)

			_, payload, err := translateEvent(ce)

			Expect(err).NotTo(HaveOccurred())
			Expect(payload["project_id"]).To(Equal(" "))
		})

		It("handles missing billing_dimensions gracefully", func() {
			ce := buildCloudEvent(
				"ce-no-dims",
				"osac.resource.created.v1",
				"vm-004",
				"compute_instance",
				"tenant-acme",
				"project-test",
				nil,
			)

			endpoint, payload, err := translateEvent(ce)

			Expect(err).NotTo(HaveOccurred())
			Expect(endpoint).To(Equal("/vmaas/event"))
			Expect(payload).NotTo(HaveKey("instance_type"))
		})

		It("skips non-billable billing_dimensions (nil, empty, zero)", func() {
			ce := buildCloudEvent(
				"ce-zero-dims",
				"osac.resource.created.v1",
				"vm-005",
				"compute_instance",
				"tenant-acme",
				"project-test",
				map[string]any{
					"instance_type":      "large-gpu",
					"image_ref":          "",
					"boot_disk_size_gib": 0,
					"extra_field":        nil,
				},
			)

			_, payload, err := translateEvent(ce)

			Expect(err).NotTo(HaveOccurred())
			Expect(payload["instance_type"]).To(Equal("large-gpu"))
			Expect(payload).NotTo(HaveKey("image_ref"))
			Expect(payload).NotTo(HaveKey("boot_disk_size_gib"))
			Expect(payload).NotTo(HaveKey("extra_field"))
		})

		It("prevents billing_dimensions from overwriting canonical fields", func() {
			ce := buildCloudEvent(
				"ce-overwrite",
				"osac.resource.created.v1",
				"vm-006",
				"compute_instance",
				"tenant-acme",
				"project-test",
				map[string]any{
					"event_id":      "injected-id",
					"resource_type": "injected-type",
					"instance_type": "large-gpu",
				},
			)

			_, payload, err := translateEvent(ce)

			Expect(err).NotTo(HaveOccurred())
			Expect(payload["event_id"]).To(Equal("ce-overwrite"))
			Expect(payload["resource_type"]).To(Equal("compute_instance"))
			Expect(payload["instance_type"]).To(Equal("large-gpu"))
		})
	})

	Describe("CaaS events", func() {
		It("translates a cluster_order event to /caas/event", func() {
			ce := buildCloudEvent(
				"ce-caas-001",
				"osac.resource.created.v1",
				"cluster-001",
				"cluster_order",
				"tenant-acme",
				"project-ml",
				map[string]any{
					"cluster_template": "ocp-ci-small",
					"release_image":    "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64",
					"component":        "control_plane",
					"host_type":        "_control_plane",
					"node_count":       1,
				},
			)

			endpoint, payload, err := translateEvent(ce)

			Expect(err).NotTo(HaveOccurred())
			Expect(endpoint).To(Equal("/caas/event"))
			Expect(payload["cluster_template"]).To(Equal("ocp-ci-small"))
			Expect(payload["component"]).To(Equal("control_plane"))
			Expect(payload["node_count"]).To(BeEquivalentTo(1))
		})
	})

	Describe("MaaS events", func() {
		It("translates a maas_inference event to /maas/event", func() {
			ce := buildCloudEvent(
				"ce-maas-001",
				"osac.inference.usage.v1",
				"req-001",
				"maas_inference",
				"tenant-acme",
				"project-nlp",
				map[string]any{
					"provider":              "anthropic",
					"model":                 "claude-sonnet-4-20250514",
					"prompt_tokens":         1500,
					"completion_tokens":     800,
					"total_tokens":          2300,
					"cached_input_tokens":   200,
					"cache_creation_tokens": 0,
					"reasoning_tokens":      150,
					"duration_ms":           3200,
					"organization_id":       "acme-corp",
					"cost_center":           "engineering",
					"subscription":          "acme-premium-sub",
				},
			)

			endpoint, payload, err := translateEvent(ce)

			Expect(err).NotTo(HaveOccurred())
			Expect(endpoint).To(Equal("/maas/event"))
			Expect(payload["provider"]).To(Equal("anthropic"))
			Expect(payload["prompt_tokens"]).To(BeEquivalentTo(1500))
			Expect(payload["total_tokens"]).To(BeEquivalentTo(2300))
		})
	})

	Describe("error cases", func() {
		It("returns NonRetryableError for unknown resource_type", func() {
			ce := buildCloudEvent(
				"ce-unknown",
				"osac.resource.created.v1",
				"res-001",
				"unknown_type",
				"tenant-acme",
				"project-x",
				nil,
			)

			_, _, err := translateEvent(ce)

			Expect(err).To(HaveOccurred())
			var nonRetryable *adapters.NonRetryableError
			Expect(errors.As(err, &nonRetryable)).To(BeTrue())
		})

		It("returns NonRetryableError for empty CloudEvent ID", func() {
			ce := buildCloudEvent(
				"",
				"osac.resource.created.v1",
				"vm-001",
				"compute_instance",
				"tenant-acme",
				"project-test",
				nil,
			)

			_, _, err := translateEvent(ce)

			Expect(err).To(HaveOccurred())
			var nonRetryable *adapters.NonRetryableError
			Expect(errors.As(err, &nonRetryable)).To(BeTrue())
		})

		It("returns NonRetryableError for zero CloudEvent timestamp", func() {
			ce := cloudevents.NewEvent()
			ce.SetID("ce-zero-time")
			ce.SetType("osac.resource.created.v1")
			ce.SetSource("osac-metering-service")
			// Deliberately not setting time — ce.Time() returns zero value.
			ce.SetDataContentType("application/json")
			ExpectWithOffset(0, ce.SetData(cloudevents.ApplicationJSON, map[string]any{
				"resource_type": "compute_instance",
			})).To(Succeed())

			_, _, err := translateEvent(ce)

			Expect(err).To(HaveOccurred())
			var nonRetryable *adapters.NonRetryableError
			Expect(errors.As(err, &nonRetryable)).To(BeTrue())
		})

		It("returns NonRetryableError for malformed billing_dimensions", func() {
			ce := cloudevents.NewEvent()
			ce.SetID("ce-bad-dims")
			ce.SetType("osac.resource.created.v1")
			ce.SetSource("osac-metering-service")
			ce.SetSubject("compute_instance/vm-001")
			ce.SetTime(time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC))
			ce.SetDataContentType("application/json")
			ExpectWithOffset(0, ce.SetData(cloudevents.ApplicationJSON, map[string]any{
				"resource_type":      "compute_instance",
				"resource_id":        "vm-001",
				"tenant_id":          "tenant-acme",
				"billing_dimensions": "not-a-map",
			})).To(Succeed())

			_, _, err := translateEvent(ce)

			Expect(err).To(HaveOccurred())
			var nonRetryable *adapters.NonRetryableError
			Expect(errors.As(err, &nonRetryable)).To(BeTrue())
		})

		It("returns NonRetryableError for malformed data", func() {
			ce := cloudevents.NewEvent()
			ce.SetID("ce-bad")
			ce.SetType("osac.resource.created.v1")
			ce.SetSource("osac-metering-service")
			ce.SetTime(time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC))
			// Set invalid JSON data
			ce.DataEncoded = []byte("not-json{{{")
			ce.SetDataContentType("application/json")

			_, _, err := translateEvent(ce)

			Expect(err).To(HaveOccurred())
			var nonRetryable *adapters.NonRetryableError
			Expect(errors.As(err, &nonRetryable)).To(BeTrue())
		})
	})
})
