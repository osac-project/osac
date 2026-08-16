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
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/osac-project/osac-metering/adapters"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("m360Client", func() {
	var (
		server *httptest.Server
		client *m360Client
	)

	AfterEach(func() {
		if server != nil {
			server.Close()
		}
	})

	Describe("post", func() {
		It("sends POST with correct URL, auth header, and body", func() {
			var capturedReq *http.Request
			var capturedBody []byte
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedReq = r
				capturedBody, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"output":{"event_id":"m360-ev-1"}}`))
			}))
			client = newM360Client(server.URL, "v1", "test-api-key")

			payload := map[string]any{"event_id": "ce-123", "resource_type": "compute_instance"}
			err := client.post(context.Background(), "/vmaas/event", payload)

			Expect(err).NotTo(HaveOccurred())
			Expect(capturedReq.Method).To(Equal("POST"))
			Expect(capturedReq.URL.Path).To(Equal("/api/v1/external/run/vmaas/event"))
			Expect(capturedReq.Header.Get("Authorization")).To(Equal("Bearer test-api-key"))
			Expect(capturedReq.Header.Get("Content-Type")).To(Equal("application/json"))

			var body map[string]any
			Expect(json.Unmarshal(capturedBody, &body)).To(Succeed())
			Expect(body["event_id"]).To(Equal("ce-123"))
		})

		It("uses configurable API version in URL", func() {
			var capturedPath string
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedPath = r.URL.Path
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"output":{"event_id":"ev-1"}}`))
			}))
			client = newM360Client(server.URL, "v2", "key")

			err := client.post(context.Background(), "/caas/event", map[string]any{})
			Expect(err).NotTo(HaveOccurred())
			Expect(capturedPath).To(Equal("/api/v2/external/run/caas/event"))
		})

		It("returns NonRetryableError on 400", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"bad request"}`))
			}))
			client = newM360Client(server.URL, "v1", "key")

			err := client.post(context.Background(), "/vmaas/event", map[string]any{})

			Expect(err).To(HaveOccurred())
			var nonRetryable *adapters.NonRetryableError
			Expect(errors.As(err, &nonRetryable)).To(BeTrue())
		})

		It("returns NonRetryableError on 401", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			}))
			client = newM360Client(server.URL, "v1", "bad-key")

			err := client.post(context.Background(), "/vmaas/event", map[string]any{})

			var nonRetryable *adapters.NonRetryableError
			Expect(errors.As(err, &nonRetryable)).To(BeTrue())
		})

		It("returns retryable error on 408", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusRequestTimeout)
			}))
			client = newM360Client(server.URL, "v1", "key")

			err := client.post(context.Background(), "/vmaas/event", map[string]any{})

			Expect(err).To(HaveOccurred())
			var nonRetryable *adapters.NonRetryableError
			Expect(errors.As(err, &nonRetryable)).To(BeFalse())
		})

		It("returns retryable error on 429", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			client = newM360Client(server.URL, "v1", "key")

			err := client.post(context.Background(), "/vmaas/event", map[string]any{})

			Expect(err).To(HaveOccurred())
			var nonRetryable *adapters.NonRetryableError
			Expect(errors.As(err, &nonRetryable)).To(BeFalse())
		})

		It("returns retryable error on 500", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))
			client = newM360Client(server.URL, "v1", "key")

			err := client.post(context.Background(), "/vmaas/event", map[string]any{})

			Expect(err).To(HaveOccurred())
			var nonRetryable *adapters.NonRetryableError
			Expect(errors.As(err, &nonRetryable)).To(BeFalse())
		})

		It("returns error when context is cancelled", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			client = newM360Client(server.URL, "v1", "key")

			ctx, cancel := context.WithCancel(context.Background())
			cancel() // cancel immediately

			err := client.post(ctx, "/vmaas/event", map[string]any{})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("healthCheck", func() {
		It("returns nil when server responds with 200", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			client = newM360Client(server.URL, "v1", "key")

			err := client.healthCheck(context.Background())
			Expect(err).NotTo(HaveOccurred())
		})

		It("returns nil when server responds with non-2xx (reachable)", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))
			client = newM360Client(server.URL, "v1", "key")

			err := client.healthCheck(context.Background())
			Expect(err).NotTo(HaveOccurred())
		})

		It("sends HEAD to the base URL", func() {
			var capturedMethod string
			var capturedPath string
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedMethod = r.Method
				capturedPath = r.URL.Path
				w.WriteHeader(http.StatusOK)
			}))
			client = newM360Client(server.URL, "v1", "key")

			err := client.healthCheck(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(capturedMethod).To(Equal("HEAD"))
			Expect(capturedPath).To(Equal("/"))
		})

		It("returns error when server is unreachable", func() {
			client = newM360Client("http://127.0.0.1:1", "v1", "key")

			err := client.healthCheck(context.Background())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("M360 health check"))
		})

		It("returns error when context is cancelled", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			client = newM360Client(server.URL, "v1", "key")

			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			err := client.healthCheck(ctx)
			Expect(err).To(HaveOccurred())
		})
	})
})
