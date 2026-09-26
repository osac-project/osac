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

	cloudevents "github.com/cloudevents/sdk-go/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac-metering/adapters"
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
})
