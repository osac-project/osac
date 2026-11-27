/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package costmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac-metering/adapters"
	"github.com/osac-project/osac-metering/schema"
)

var _ = Describe("costmanagement Adapter Submit", func() {
	It("translates and submits heartbeats to POST /api/v1/events", func() {
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
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		a := NewAdapter(server.URL, "test-token", logr.Discard())
		Expect(a.Name()).To(Equal("cost-management"))

		acct := "org-99"
		ce := buildHeartbeat(
			"ce-submit-vm", schema.ResourceTypeComputeInstance, "vm-100",
			"compute", "compute", "60.000000", "second",
			map[string]string{"instance_type": "standard-4-8", "project": "alpha"},
			&acct,
		)

		Expect(a.Submit(context.Background(), adapters.MeteringEvent{CloudEvent: ce})).To(Succeed())
		Expect(capturedMethod).To(Equal(http.MethodPost))
		Expect(capturedPath).To(Equal("/api/v1/events"))
		Expect(capturedAuth).To(Equal("Bearer test-token"))

		var payload CloudEventEnvelope
		Expect(json.Unmarshal(capturedBody, &payload)).To(Succeed())
		Expect(payload.ID).To(Equal("ce-submit-vm"))
		Expect(payload.Data.SchemaVersion).To(Equal(ContractVersion))
		Expect(payload.Data.ExternalBillingAccountID).To(Equal("org-99"))
		Expect(payload.Data.Usage.Quantity).To(Equal("60.000000"))

		res, err := a.Flush(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Idempotent).To(BeTrue())
		Expect(a.Close()).To(Succeed())
	})

	It("rejects invalid events locally without calling the HTTP endpoint", func() {
		called := false
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		a := NewAdapter(server.URL, "", logr.Discard())
		ce := cloudevents.NewEvent()
		ce.SetID("bad-ev")
		ce.SetType("osac.resource.created.v1")

		err := a.Submit(context.Background(), adapters.MeteringEvent{CloudEvent: ce})
		Expect(err).To(HaveOccurred())
		var nonRetryable *adapters.NonRetryableError
		Expect(errors.As(err, &nonRetryable)).To(BeTrue())
		Expect(called).To(BeFalse())
	})

	It("returns NonRetryableError when the receiver rejects an event with 400", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer server.Close()

		a := NewAdapter(server.URL, "", logr.Discard())
		ce := buildHeartbeat(
			"ce-rejected", schema.ResourceTypeVolume, "vol-1",
			"capacity", "capacity", "600.000000", schema.UsageUnitGiByteSecond,
			nil, nil,
		)

		err := a.Submit(context.Background(), adapters.MeteringEvent{CloudEvent: ce})
		Expect(err).To(HaveOccurred())
		var nonRetryable *adapters.NonRetryableError
		Expect(errors.As(err, &nonRetryable)).To(BeTrue())
	})
})
