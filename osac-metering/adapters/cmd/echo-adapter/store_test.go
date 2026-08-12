/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac-metering/adapters"
)

func makeEvent(id, eventType, source, resourceID string) adapters.MeteringEvent {
	ce := cloudevents.NewEvent()
	ce.SetID(id)
	ce.SetType(eventType)
	ce.SetSource(source)
	ce.SetTime(time.Now().UTC())
	ce.SetExtension("osacresourceid", resourceID)
	ce.SetExtension("osacresourcetype", "compute_instance")
	ce.SetExtension("osactenant", "test-tenant")
	_ = ce.SetData(cloudevents.ApplicationJSON, map[string]any{
		"resource_id":    resourceID,
		"resource_type":  "ComputeInstance",
		"tenant_id":      "test-tenant",
		"schema_version": "v1",
		"billing_dimensions": map[string]any{
			"instance_type":      "m5.large",
			"image_ref":          "rhel-9",
			"boot_disk_size_gib": 50,
		},
		"transition_time": time.Now().UTC().Format(time.RFC3339),
	})
	return adapters.MeteringEvent{
		CloudEvent: ce,
		Topic:      "osac.metering.lifecycle",
		Partition:  0,
		Offset:     42,
	}
}

var _ = Describe("eventStore", func() {
	var store *eventStore

	BeforeEach(func() {
		store = newEventStore(100)
	})

	Describe("add and query", func() {
		It("returns the full CloudEvent with extensions and data", func() {
			ev := makeEvent("evt-1", "osac.resource.created.v1", "osac-metering", "res-aaa")
			store.add(ev)

			results := store.query("", "", time.Time{}, 0)
			Expect(results).To(HaveLen(1))

			var parsed map[string]any
			Expect(json.Unmarshal(results[0], &parsed)).To(Succeed())

			Expect(parsed["specversion"]).To(Equal("1.0"))
			Expect(parsed["id"]).To(Equal("evt-1"))
			Expect(parsed["type"]).To(Equal("osac.resource.created.v1"))
			Expect(parsed["source"]).To(Equal("osac-metering"))
			Expect(parsed["osacresourceid"]).To(Equal("res-aaa"))
			Expect(parsed["osacresourcetype"]).To(Equal("compute_instance"))
			Expect(parsed["osactenant"]).To(Equal("test-tenant"))

			data, ok := parsed["data"].(map[string]any)
			Expect(ok).To(BeTrue(), "data should be a map")
			Expect(data["resource_id"]).To(Equal("res-aaa"))
			Expect(data["resource_type"]).To(Equal("ComputeInstance"))
			Expect(data["tenant_id"]).To(Equal("test-tenant"))
			Expect(data["schema_version"]).To(Equal("v1"))

			bd, ok := data["billing_dimensions"].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(bd["instance_type"]).To(Equal("m5.large"))
			Expect(bd["image_ref"]).To(Equal("rhel-9"))
			Expect(bd["boot_disk_size_gib"]).To(BeNumerically("==", 50))
		})

		It("does not pollute CloudEvent JSON with Kafka metadata", func() {
			ev := makeEvent("evt-2", "osac.resource.created.v1", "osac-metering", "res-bbb")
			ev.Topic = "osac.metering.lifecycle"
			ev.Partition = 2
			ev.Offset = 99
			store.add(ev)

			results := store.query("", "", time.Time{}, 0)
			var parsed map[string]any
			Expect(json.Unmarshal(results[0], &parsed)).To(Succeed())

			Expect(parsed).NotTo(HaveKey("topic"))
			Expect(parsed).NotTo(HaveKey("partition"))
			Expect(parsed).NotTo(HaveKey("offset"))
			Expect(parsed).NotTo(HaveKey("received_at"))
		})
	})

	Describe("filtering", func() {
		BeforeEach(func() {
			store.add(makeEvent("e1", "osac.resource.created.v1", "osac-metering", "res-1"))
			store.add(makeEvent("e2", "osac.resource.started.v1", "osac-metering", "res-1"))
			store.add(makeEvent("e3", "osac.resource.created.v1", "osac-metering", "res-2"))
		})

		It("filters by event type", func() {
			results := store.query("osac.resource.created.v1", "", time.Time{}, 0)
			Expect(results).To(HaveLen(2))
		})

		It("filters by resource ID", func() {
			results := store.query("", "res-1", time.Time{}, 0)
			Expect(results).To(HaveLen(2))
		})

		It("filters by type and resource ID", func() {
			results := store.query("osac.resource.created.v1", "res-1", time.Time{}, 0)
			Expect(results).To(HaveLen(1))
		})

		It("respects limit", func() {
			results := store.query("", "", time.Time{}, 1)
			Expect(results).To(HaveLen(1))
		})

		It("counts correctly", func() {
			Expect(store.count("osac.resource.created.v1", "", time.Time{})).To(Equal(2))
			Expect(store.count("", "res-1", time.Time{})).To(Equal(2))
			Expect(store.count("", "", time.Time{})).To(Equal(3))
		})
	})

	Describe("getByID", func() {
		It("returns the full event", func() {
			store.add(makeEvent("find-me", "osac.resource.created.v1", "osac-metering", "res-x"))

			raw := store.getByID("find-me")
			Expect(raw).NotTo(BeNil())

			var parsed map[string]any
			Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
			Expect(parsed["id"]).To(Equal("find-me"))
			Expect(parsed["osacresourceid"]).To(Equal("res-x"))
		})

		It("returns nil for unknown ID", func() {
			Expect(store.getByID("nope")).To(BeNil())
		})
	})

	Describe("ring buffer eviction", func() {
		It("drops oldest when at capacity", func() {
			small := newEventStore(2)
			small.add(makeEvent("old", "osac.resource.created.v1", "osac-metering", "r1"))
			small.add(makeEvent("mid", "osac.resource.created.v1", "osac-metering", "r2"))
			small.add(makeEvent("new", "osac.resource.created.v1", "osac-metering", "r3"))

			Expect(small.getByID("old")).To(BeNil())
			Expect(small.getByID("mid")).NotTo(BeNil())
			Expect(small.getByID("new")).NotTo(BeNil())
		})
	})

	Describe("clear", func() {
		It("removes all events", func() {
			store.add(makeEvent("e1", "osac.resource.created.v1", "osac-metering", "r1"))
			store.clear()
			Expect(store.count("", "", time.Time{})).To(Equal(0))
		})
	})

	Describe("HTTP handlers", func() {
		BeforeEach(func() {
			store.add(makeEvent("h1", "osac.resource.created.v1", "osac-metering", "res-http"))
		})

		It("GET /events returns full CloudEvent array", func() {
			req := httptest.NewRequest("GET", "/events?type=osac.resource.created.v1&resource_id=res-http", nil)
			w := httptest.NewRecorder()
			store.handleEvents(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))

			var events []map[string]any
			Expect(json.Unmarshal(w.Body.Bytes(), &events)).To(Succeed())
			Expect(events).To(HaveLen(1))
			Expect(events[0]["specversion"]).To(Equal("1.0"))
			Expect(events[0]["osacresourceid"]).To(Equal("res-http"))
			Expect(events[0]).To(HaveKey("data"))
		})

		It("GET /events returns empty array when no match", func() {
			req := httptest.NewRequest("GET", "/events?type=nope", nil)
			w := httptest.NewRecorder()
			store.handleEvents(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			Expect(w.Body.String()).To(Equal("[]\n"))
		})

		It("GET /events/count returns count", func() {
			req := httptest.NewRequest("GET", "/events/count", nil)
			w := httptest.NewRecorder()
			store.handleCount(w, req)

			var result map[string]int
			Expect(json.Unmarshal(w.Body.Bytes(), &result)).To(Succeed())
			Expect(result["count"]).To(Equal(1))
		})

		It("DELETE /events clears store", func() {
			req := httptest.NewRequest("DELETE", "/events", nil)
			w := httptest.NewRecorder()
			store.handleDeleteEvents(w, req)

			Expect(w.Code).To(Equal(http.StatusNoContent))
			Expect(store.count("", "", time.Time{})).To(Equal(0))
		})
	})
})
