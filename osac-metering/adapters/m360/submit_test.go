/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package m360

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac-metering/adapters"
	"github.com/osac-project/osac-metering/schema"
)

var _ = Describe("m360Adapter Submit", func() {
	It("posts network and storage events to their versioned resource endpoints", func() {
		var (
			capturedMethod        string
			capturedPath          string
			capturedAuthorization string
			capturedBody          []byte
		)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedMethod = r.Method
			capturedPath = r.URL.Path
			capturedAuthorization = r.Header.Get("Authorization")
			capturedBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		adapter := &m360Adapter{client: newM360Client(server.URL, "v2", "test-api-key")}
		tests := []struct {
			name       string
			cloudEvent cloudevents.Event
			path       string
			eventID    string
			usageUnit  string
		}{
			{
				name: "ExternalIP",
				cloudEvent: withUsage(buildCloudEvent(
					"ce-submit-ip", "osac.resource.started.v1", "ip-submit", "external_ip",
					"tenant-acme", "project-net", map[string]any{
						"deployment": "installation-a", "pool": "pool-ipv4", "ip_family": "ipv4", "attached": false,
					},
				), "60.000000", "resource_second"),
				path:      "/api/v2/external/run/networking/externalip/event",
				eventID:   "ce-submit-ip",
				usageUnit: "resource_second",
			},
			{
				name: "NATGateway",
				cloudEvent: withUsage(buildCloudEvent(
					"ce-submit-nat", "osac.resource.started.v1", "nat-submit", "nat_gateway",
					"tenant-acme", "project-net", map[string]any{
						"deployment": "installation-a", "virtual_network": "vnet-1", "external_ip": "ip-submit",
					},
				), "60.000000", "resource_second"),
				path:      "/api/v2/external/run/networking/natgateway/event",
				eventID:   "ce-submit-nat",
				usageUnit: "resource_second",
			},
			{
				name: "Volume",
				cloudEvent: withUsage(buildCloudEvent(
					"ce-submit-volume", "osac.resource.started.v1", "volume-submit", "volume",
					"tenant-acme", "project-storage", map[string]any{
						"volume_id": "volume-submit", "storage_tier": "gold", "size_gib": 100,
					},
				), "6000.000000", "gibibyte_second"),
				path:      "/api/v2/external/run/storage/volume/event",
				eventID:   "ce-submit-volume",
				usageUnit: "gibibyte_second",
			},
		}

		for _, tt := range tests {
			By("submitting " + tt.name)
			err := adapter.Submit(context.Background(), adapters.MeteringEvent{CloudEvent: tt.cloudEvent})

			Expect(err).NotTo(HaveOccurred())
			Expect(capturedMethod).To(Equal(http.MethodPost))
			Expect(capturedPath).To(Equal(tt.path))
			Expect(capturedAuthorization).To(Equal("Bearer test-api-key"))

			var payload map[string]any
			Expect(json.Unmarshal(capturedBody, &payload)).To(Succeed())
			Expect(payload["event_id"]).To(Equal(tt.eventID))
			Expect(payload["usage_unit"]).To(Equal(tt.usageUnit))
		}
	})

	It("submits BMaaS events with the translated timing and resource contract", func() {
		var (
			capturedMethod string
			capturedPath   string
			capturedAuth   string
			capturedBody   []byte
		)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedMethod = r.Method
			capturedPath = r.URL.Path
			capturedAuth = r.Header.Get("Authorization")
			capturedBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		}))
		DeferCleanup(server.Close)

		adapter := &m360Adapter{client: newM360Client(server.URL, "v1", "key")}
		cases := []struct {
			name             string
			id               string
			ceType           string
			meterType        string
			durationSeconds  float64
			includeLifecycle bool
		}{
			{
				name:             "allocation started",
				id:               "ce-bmaas-allocation",
				ceType:           "osac.resource.started.v1",
				meterType:        "allocation",
				includeLifecycle: true,
			},
			{
				name:            "consumption heartbeat",
				id:              "hb/bmi-001/1726315200/consumption",
				ceType:          "osac.resource.heartbeat.v1",
				meterType:       "consumption",
				durationSeconds: 3600,
			},
		}

		for _, tc := range cases {
			By(tc.name)
			eventTime := time.Date(2026, 9, 14, 12, 0, 5, 0, time.UTC)
			transitionTime := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			ce := buildBMaaSCloudEvent(
				tc.id, tc.ceType, tc.meterType, "RUNNING",
				eventTime, transitionTime, tc.durationSeconds, tc.includeLifecycle,
			)

			err := adapter.Submit(context.Background(), adapters.MeteringEvent{CloudEvent: ce})

			Expect(err).NotTo(HaveOccurred())
			Expect(capturedMethod).To(Equal(http.MethodPost))
			Expect(capturedPath).To(Equal("/api/v1/external/run/bmaas/event"))
			Expect(capturedAuth).To(Equal("Bearer key"))

			var payload map[string]any
			Expect(json.Unmarshal(capturedBody, &payload)).To(Succeed())
			Expect(payload["meter_type"]).To(Equal(tc.meterType))
			Expect(payload["duration_seconds"]).To(BeEquivalentTo(tc.durationSeconds))
			Expect(payload["resource_type"]).To(Equal(schema.ResourceTypeBareMetalInstance))
			Expect(payload["event_time"]).To(Equal("2026-09-14T12:00:05Z"))
			if tc.includeLifecycle {
				Expect(payload["event_time"]).NotTo(Equal(payload["transition_time"]))
			}
			if tc.ceType == "osac.resource.heartbeat.v1" {
				Expect(payload["duration_seconds"]).To(Equal(float64(3600)))
			}
		}
	})
})
