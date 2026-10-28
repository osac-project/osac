/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package m360

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac-metering/adapters"
	"github.com/osac-project/osac-metering/schema"
)

var _ = Describe("m360Adapter Submit", func() {
	It("posts heartbeats to their versioned resource endpoints", func() {
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
			usageUnit  string
			quantity   string
		}{
			{
				name: "ExternalIP",
				cloudEvent: buildHeartbeat(
					"ce-submit-ip", schema.ResourceTypeExternalIP, "ip-submit", "allocation",
					"allocation", "60.000000", "second",
					map[string]any{"deployment": "installation-a", "pool": "pool-ipv4",
						"ip_family": "ipv4", "attached": false},
				),
				path:      "/api/v2/external/run/networking/externalip/event",
				usageUnit: "second",
				quantity:  "60.000000",
			},
			{
				name: "NATGateway",
				cloudEvent: buildHeartbeat(
					"ce-submit-nat", schema.ResourceTypeNATGateway, "nat-submit", "allocation",
					"allocation", "60.000000", "second",
					map[string]any{"deployment": "installation-a", "virtual_network": "vnet-1",
						"external_ip": "ip-submit"},
				),
				path:      "/api/v2/external/run/networking/natgateway/event",
				usageUnit: "second",
				quantity:  "60.000000",
			},
			{
				name: "Volume",
				cloudEvent: buildHeartbeat(
					"ce-submit-volume", schema.ResourceTypeVolume, "volume-submit", "capacity",
					"capacity", "6000.000000", "gibibyte_second",
					map[string]any{"storage_tier": "gold"},
				),
				path:      "/api/v2/external/run/storage/volume/event",
				usageUnit: "gibibyte_second",
				quantity:  "6000.000000",
			},
			{
				name: "BareMetalInstance",
				cloudEvent: buildHeartbeat(
					"ce-submit-bm", schema.ResourceTypeBareMetalInstance, "bmi-001", "consumption",
					"consumption", "120.000000", "second",
					map[string]any{"instance_type": "bmi-type-gpu-large"},
				),
				path:      "/api/v2/external/run/bmaas/event",
				usageUnit: "second",
				quantity:  "120.000000",
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
			Expect(payload["event_id"]).To(Equal(tt.cloudEvent.ID()))
			Expect(payload["usage_unit"]).To(Equal(tt.usageUnit))
			Expect(payload["usage_quantity"]).To(Equal(tt.quantity))
		}
	})

	It("returns the server error when the provider rejects the heartbeat", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer server.Close()

		adapter := &m360Adapter{client: newM360Client(server.URL, "v1", "key")}
		event := buildHeartbeat(
			"ce-rejected", schema.ResourceTypeComputeInstance, "vm-7", "compute", "compute",
			"60.000000", "second", nil,
		)
		err := adapter.Submit(context.Background(), adapters.MeteringEvent{CloudEvent: event})
		Expect(err).To(HaveOccurred())
	})
})
