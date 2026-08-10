/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package adapters

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

var _ = Describe("adapterMetrics", func() {
	var m *adapterMetrics

	BeforeEach(func() {
		m = newAdapterMetrics()
	})

	Describe("counter increments", func() {
		It("increments events submitted", func() {
			m.eventsSubmitted.WithLabelValues("test-provider", "topic-1").Inc()
			val := testutil.ToFloat64(m.eventsSubmitted.WithLabelValues("test-provider", "topic-1"))
			Expect(val).To(Equal(float64(1)))
		})

		It("increments events failed with error type", func() {
			m.eventsFailed.WithLabelValues("test-provider", "non_retryable").Inc()
			val := testutil.ToFloat64(m.eventsFailed.WithLabelValues("test-provider", "non_retryable"))
			Expect(val).To(Equal(float64(1)))
		})

		It("increments duplicates suppressed", func() {
			m.duplicatesSuppressed.WithLabelValues("test-provider").Add(5)
			val := testutil.ToFloat64(m.duplicatesSuppressed.WithLabelValues("test-provider"))
			Expect(val).To(Equal(float64(5)))
		})

		It("increments out of order events", func() {
			m.outOfOrderEvents.WithLabelValues("test-provider").Inc()
			val := testutil.ToFloat64(m.outOfOrderEvents.WithLabelValues("test-provider"))
			Expect(val).To(Equal(float64(1)))
		})

		It("increments flush errors", func() {
			m.flushErrors.WithLabelValues("test-provider").Inc()
			val := testutil.ToFloat64(m.flushErrors.WithLabelValues("test-provider"))
			Expect(val).To(Equal(float64(1)))
		})
	})

	Describe("histogram observations", func() {
		It("observes retry duration", func() {
			m.retryDuration.WithLabelValues("test-provider").Observe(1.5)
			m.retryDuration.WithLabelValues("test-provider").Observe(3.0)
			count := testutil.CollectAndCount(m.retryDuration)
			Expect(count).To(Equal(1)) // one metric family
		})

		It("observes flush duration", func() {
			m.flushDuration.WithLabelValues("test-provider").Observe(0.05)
			count := testutil.CollectAndCount(m.flushDuration)
			Expect(count).To(Equal(1))
		})
	})

	Describe("handler", func() {
		It("serves metrics via HTTP", func() {
			m.eventsSubmitted.WithLabelValues("test-provider", "topic-1").Inc()

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			m.handler().ServeHTTP(w, req)

			body, err := io.ReadAll(w.Result().Body)
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.Contains(string(body), "osac_adapter_events_submitted_total")).To(BeTrue())
		})
	})
})
