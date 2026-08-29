/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac-metering/adapters"
	"github.com/osac-project/osac-metering/schema"
)

func canonicalEvent(id, resourceID, resourceType string) cloudevents.Event {
	ce := cloudevents.NewEvent()
	ce.SetSpecVersion("1.0")
	ce.SetID(id)
	ce.SetType("osac.resource.lifecycle.v1")
	ce.SetSource("osac-metering-service")
	ce.SetSubject(resourceType + "/" + resourceID)
	ce.SetTime(time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC))
	ce.SetDataContentType(cloudevents.ApplicationJSON)
	ce.SetExtension(schema.ExtResourceID, resourceID)
	ce.SetExtension(schema.ExtResourceType, resourceType)
	ce.SetExtension(schema.ExtTenant, "tenant-acme")
	ExpectWithOffset(1, ce.SetData(cloudevents.ApplicationJSON, map[string]any{
		"resource_id":     resourceID,
		"resource_type":   resourceType,
		"tenant_id":       "tenant-acme",
		"current_state":   "running",
		"transition_time": "2026-08-28T10:00:00Z",
		"schema_version":  "v1",
	})).To(Succeed())
	return ce
}

func adapterEvent(id, resourceID, resourceType string) adapters.MeteringEvent {
	return adapters.MeteringEvent{CloudEvent: canonicalEvent(id, resourceID, resourceType)}
}

var _ = Describe("costManagementAdapter", func() {
	Describe("configuration", func() {
		It("accepts absolute HTTP(S) endpoint URLs and rejects unsafe values", func() {
			Expect(validateCostManagementURL("https://cost.example.test")).To(Succeed())
			Expect(validateCostManagementURL("http://cost.example.test/api")).To(Succeed())
			Expect(validateCostManagementURL("cost.example.test")).NotTo(Succeed())
			Expect(validateCostManagementURL("ftp://cost.example.test")).NotTo(Succeed())
		})
	})

	Describe("Submit", func() {
		It("buffers canonical VMaaS, CaaS, and MaaS CloudEvents unchanged", func() {
			adapter := newCostManagementAdapter(newCostManagementClient("https://cost.example.test", "token"))
			for _, event := range []adapters.MeteringEvent{
				adapterEvent("vm-1", "vm-1", schema.ResourceTypeComputeInstance),
				adapterEvent("cluster-1", "cluster-1", schema.ResourceTypeClusterOrder),
				adapterEvent("inference-1", "request-1", resourceTypeMaaSInference),
			} {
				Expect(adapter.Submit(context.Background(), event)).To(Succeed())
			}

			Expect(adapter.pendingCount()).To(Equal(3))
		})

		It("returns a non-retryable error for a malformed CloudEvent before buffering", func() {
			adapter := newCostManagementAdapter(newCostManagementClient("https://cost.example.test", "token"))
			event := adapterEvent("bad-1", "vm-1", schema.ResourceTypeComputeInstance)
			event.CloudEvent.SetExtension(schema.ExtResourceID, "other-resource")

			err := adapter.Submit(context.Background(), event)

			var nonRetryable *adapters.NonRetryableError
			Expect(errors.As(err, &nonRetryable)).To(BeTrue())
			Expect(adapter.pendingCount()).To(BeZero())
		})

		It("returns a non-retryable error when one event cannot fit in a Cost batch", func() {
			adapter := newCostManagementAdapter(newCostManagementClient("https://cost.example.test", "token"))
			event := adapterEvent("too-large", "vm-1", schema.ResourceTypeComputeInstance)
			Expect(event.CloudEvent.SetData(cloudevents.ApplicationJSON, map[string]any{
				"resource_id":     "vm-1",
				"resource_type":   schema.ResourceTypeComputeInstance,
				"tenant_id":       "tenant-acme",
				"current_state":   "running",
				"transition_time": "2026-08-28T10:00:00Z",
				"schema_version":  "v1",
				"padding":         strings.Repeat("x", maxBatchBytes),
			})).To(Succeed())

			err := adapter.Submit(context.Background(), event)

			var nonRetryable *adapters.NonRetryableError
			Expect(errors.As(err, &nonRetryable)).To(BeTrue())
			Expect(adapter.pendingCount()).To(BeZero())
		})
	})

	Describe("Flush", func() {
		var (
			server       *httptest.Server
			adapter      *costManagementAdapter
			capturedBody []byte
			capturedAuth string
			mu           sync.Mutex
		)

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		It("posts a batch to the Cost endpoint with bearer authentication and preserves structured CloudEvents", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodPost))
				Expect(r.URL.Path).To(Equal(costBatchPath))
				mu.Lock()
				capturedAuth = r.Header.Get("Authorization")
				capturedBody, _ = io.ReadAll(r.Body)
				mu.Unlock()
				w.WriteHeader(http.StatusNoContent)
			}))
			adapter = newCostManagementAdapter(newCostManagementClient(server.URL, "cost-token"))
			for _, event := range []adapters.MeteringEvent{
				adapterEvent("vm-1", "vm-1", schema.ResourceTypeComputeInstance),
				adapterEvent("cluster-1", "cluster-1", schema.ResourceTypeClusterOrder),
				adapterEvent("inference-1", "request-1", resourceTypeMaaSInference),
			} {
				Expect(adapter.Submit(context.Background(), event)).To(Succeed())
			}

			_, err := adapter.Flush(context.Background())

			Expect(err).NotTo(HaveOccurred())
			mu.Lock()
			defer mu.Unlock()
			Expect(capturedAuth).To(Equal("Bearer cost-token"))
			var batch batchRequest
			Expect(json.Unmarshal(capturedBody, &batch)).To(Succeed())
			Expect(batch.Events).To(HaveLen(3))
			Expect(batch.Events[0].ID()).To(Equal("vm-1"))
			Expect(batch.Events[1].Extensions()[schema.ExtResourceType]).To(Equal(schema.ResourceTypeClusterOrder))
			Expect(batch.Events[2].Extensions()[schema.ExtResourceType]).To(Equal(resourceTypeMaaSInference))
			Expect(adapter.pendingCount()).To(BeZero())
		})

		It("retains its batch after retryable receiver failures", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			adapter = newCostManagementAdapter(newCostManagementClient(server.URL, "cost-token"))
			Expect(adapter.Submit(context.Background(), adapterEvent("vm-1", "vm-1", schema.ResourceTypeComputeInstance))).To(Succeed())

			_, err := adapter.Flush(context.Background())

			Expect(err).To(HaveOccurred())
			Expect(adapter.pendingCount()).To(Equal(1))
		})

		It("retains its batch after 429", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			adapter = newCostManagementAdapter(newCostManagementClient(server.URL, "cost-token"))
			Expect(adapter.Submit(context.Background(), adapterEvent("vm-1", "vm-1", schema.ResourceTypeComputeInstance))).To(Succeed())

			_, err := adapter.Flush(context.Background())

			Expect(err).To(HaveOccurred())
			Expect(adapter.pendingCount()).To(Equal(1))
		})

		It("retains its batch for receiver 4xx because a batch response cannot identify a poison event", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
			}))
			adapter = newCostManagementAdapter(newCostManagementClient(server.URL, "cost-token"))
			Expect(adapter.Submit(context.Background(), adapterEvent("vm-1", "vm-1", schema.ResourceTypeComputeInstance))).To(Succeed())

			_, err := adapter.Flush(context.Background())

			Expect(err).To(HaveOccurred())
			Expect(adapter.pendingCount()).To(Equal(1))
		})

		It("splits batches at 100 events", func() {
			var requests int
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				w.WriteHeader(http.StatusNoContent)
			}))
			adapter = newCostManagementAdapter(newCostManagementClient(server.URL, "cost-token"))
			for i := range maxBatchEvents + 1 {
				Expect(adapter.Submit(context.Background(), adapterEvent(fmt.Sprintf("event-%d", i), fmt.Sprintf("vm-%d", i), schema.ResourceTypeComputeInstance))).To(Succeed())
			}

			_, err := adapter.Flush(context.Background())

			Expect(err).NotTo(HaveOccurred())
			Expect(requests).To(Equal(2))
			Expect(adapter.pendingCount()).To(BeZero())
		})

		It("splits batches before the one-mebibyte request limit", func() {
			var requests int
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				w.WriteHeader(http.StatusNoContent)
			}))
			adapter = newCostManagementAdapter(newCostManagementClient(server.URL, "cost-token"))
			for i := range 2 {
				event := adapterEvent(fmt.Sprintf("large-%d", i), fmt.Sprintf("vm-%d", i), schema.ResourceTypeComputeInstance)
				Expect(event.CloudEvent.SetData(cloudevents.ApplicationJSON, map[string]any{
					"resource_id":     fmt.Sprintf("vm-%d", i),
					"resource_type":   schema.ResourceTypeComputeInstance,
					"tenant_id":       "tenant-acme",
					"current_state":   "running",
					"transition_time": "2026-08-28T10:00:00Z",
					"schema_version":  "v1",
					"padding":         strings.Repeat("x", maxBatchBytes/2),
				})).To(Succeed())
				Expect(adapter.Submit(context.Background(), event)).To(Succeed())
			}

			_, err := adapter.Flush(context.Background())

			Expect(err).NotTo(HaveOccurred())
			Expect(requests).To(Equal(2))
		})
	})
})
