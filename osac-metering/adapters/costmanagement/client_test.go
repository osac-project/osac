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
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac-metering/adapters"
)

var _ = Describe("ValidateURL", func() {
	It("accepts http and https URLs", func() {
		Expect(ValidateURL("https://cost.example.com")).To(Succeed())
		Expect(ValidateURL("http://cost-event-consumer.cost-mgmt.svc:8020")).To(Succeed())
	})

	It("rejects relative and non-HTTP URLs", func() {
		Expect(ValidateURL("")).To(HaveOccurred())
		Expect(ValidateURL("/api/v1/events")).To(HaveOccurred())
		Expect(ValidateURL("ftp://cost.example.com")).To(HaveOccurred())
	})
})

var _ = Describe("client", func() {
	var (
		server *httptest.Server
		cl     *client
	)

	AfterEach(func() {
		if server != nil {
			server.Close()
		}
	})

	sampleEvent := func() *CloudEventEnvelope {
		return &CloudEventEnvelope{
			SpecVersion:     "1.0",
			ID:              "ev-123",
			Source:          "osac-metering",
			Type:            "osac.resource.heartbeat.v1",
			DataContentType: "application/json",
			Time:            "2026-10-05T10:00:00Z",
			ResourceID:      "vm-1",
			ResourceType:    "compute_instance",
			Tenant:          "acme",
			Data: UsageData{
				SchemaVersion: ContractVersion,
				TenantID:      "acme",
				ResourceType:  "compute_instance",
				ResourceID:    "vm-1",
				Meter:         "compute",
				Component:     "compute",
				Usage: UsageInterval{
					Semantics: "interval",
					From:      "2026-10-05T10:00:00Z",
					To:        "2026-10-05T10:01:00Z",
					Quantity:  "60.000000",
					Unit:      "second",
					Precision: "second",
				},
			},
		}
	}

	Describe("post", func() {
		It("posts CloudEventEnvelope with Bearer token and succeeds on 204", func() {
			var capturedReq *http.Request
			var capturedBody []byte
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedReq = r
				capturedBody, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusNoContent)
			}))
			cl = newClient(server.URL+"/", "secret-token", "", logr.Discard())

			Expect(cl.post(context.Background(), sampleEvent())).To(Succeed())
			Expect(capturedReq.Method).To(Equal(http.MethodPost))
			Expect(capturedReq.URL.Path).To(Equal("/api/v1/events"))
			Expect(capturedReq.Header.Get("Authorization")).To(Equal("Bearer secret-token"))
			Expect(capturedReq.Header.Get("Content-Type")).To(Equal("application/json"))

			var body CloudEventEnvelope
			Expect(json.Unmarshal(capturedBody, &body)).To(Succeed())
			Expect(body.ID).To(Equal("ev-123"))
			Expect(body.Data.SchemaVersion).To(Equal(ContractVersion))
		})

		It("omits Authorization header when token is empty", func() {
			var authHeader string
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				authHeader = r.Header.Get("Authorization")
				w.WriteHeader(http.StatusNoContent)
			}))
			cl = newClient(server.URL, "", "", logr.Discard())

			Expect(cl.post(context.Background(), sampleEvent())).To(Succeed())
			Expect(authHeader).To(BeEmpty())
		})

		It("reloads rotated Bearer tokens from tokenFile on every request", func() {
			tmpDir := GinkgoT().TempDir()
			tokenPath := filepath.Join(tmpDir, "token")
			Expect(os.WriteFile(tokenPath, []byte("token-v1\n"), 0o600)).To(Succeed())

			var received []string
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received = append(received, r.Header.Get("Authorization"))
				w.WriteHeader(http.StatusNoContent)
			}))
			cl = newClient(server.URL, "", tokenPath, logr.Discard())

			Expect(cl.post(context.Background(), sampleEvent())).To(Succeed())
			Expect(os.WriteFile(tokenPath, []byte("token-v2\n"), 0o600)).To(Succeed())
			Expect(cl.post(context.Background(), sampleEvent())).To(Succeed())

			Expect(received).To(Equal([]string{"Bearer token-v1", "Bearer token-v2"}))
		})

		It("returns NonRetryableError on 400 and 409 without leaking tokens", func() {
			for _, status := range []int{http.StatusBadRequest, http.StatusConflict, http.StatusUnprocessableEntity} {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"error":"bad payload"}`))
				}))
				cl = newClient(server.URL, "super-secret-value", "", logr.Discard())

				err := cl.post(context.Background(), sampleEvent())
				Expect(err).To(HaveOccurred())
				var nonRetryable *adapters.NonRetryableError
				Expect(errors.As(err, &nonRetryable)).To(BeTrue(), "status %d should be non-retryable", status)
				Expect(err.Error()).NotTo(ContainSubstring("super-secret-value"))
				server.Close()
				server = nil
			}
		})

		It("returns retryable errors on 408, 429, and 5xx", func() {
			for _, status := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(status)
					_, _ = w.Write([]byte("try later"))
				}))
				cl = newClient(server.URL, "", "", logr.Discard())

				err := cl.post(context.Background(), sampleEvent())
				Expect(err).To(HaveOccurred())
				var nonRetryable *adapters.NonRetryableError
				Expect(errors.As(err, &nonRetryable)).To(BeFalse(), "status %d should be retryable", status)
				server.Close()
				server = nil
			}
		})

		It("truncates long error responses to keep DLQ headers bounded", func() {
			longBody := strings.Repeat("x", maxBodyLog*3)
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(longBody))
			}))
			cl = newClient(server.URL, "", "", logr.Discard())

			err := cl.post(context.Background(), sampleEvent())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("...(truncated)"))
			Expect(len(err.Error())).To(BeNumerically("<", len(longBody)))
		})
	})

	Describe("healthCheck", func() {
		It("succeeds when /readyz returns 200", func() {
			var capturedPath string
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedPath = r.URL.Path
				w.WriteHeader(http.StatusOK)
			}))
			cl = newClient(server.URL, "", "", logr.Discard())

			Expect(cl.healthCheck(context.Background())).To(Succeed())
			Expect(capturedPath).To(Equal("/readyz"))
		})

		It("fails when /readyz returns 503", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			cl = newClient(server.URL, "", "", logr.Discard())

			Expect(cl.healthCheck(context.Background())).To(HaveOccurred())
		})

		It("uses HEAD reachability when ReadyPath is explicitly empty (gateway mode)", func() {
			var capturedMethod string
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedMethod = r.Method
				// Authenticating gateways return 401 to unauthenticated HEAD probes.
				w.WriteHeader(http.StatusUnauthorized)
			}))
			var err error
			cl, err = newClientFromConfig(ClientConfig{
				BaseURL:      server.URL + "/api/cost-events",
				EventsPath:   "/v1/events",
				ReadyPath:    "",
				ReadyPathSet: true,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(cl.healthCheck(context.Background())).To(Succeed())
			Expect(capturedMethod).To(Equal(http.MethodHead))
		})
	})

	Describe("two-cluster HTTPS and custom path support", func() {
		It("posts to a custom EventsPath over HTTPS verified by CACertFile", func() {
			var capturedPath string
			server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedPath = r.URL.Path
				w.WriteHeader(http.StatusNoContent)
			}))

			certPEM := pem.EncodeToMemory(&pem.Block{
				Type:  "CERTIFICATE",
				Bytes: server.Certificate().Raw,
			})
			caPath := filepath.Join(GinkgoT().TempDir(), "ca.crt")
			Expect(os.WriteFile(caPath, certPEM, 0o600)).To(Succeed())

			a, err := NewAdapterWithConfig(ClientConfig{
				BaseURL:      server.URL + "/api/cost-events",
				EventsPath:   "/v1/events",
				ReadyPath:    "",
				ReadyPathSet: true,
				CACertFile:   caPath,
				Logger:       logr.Discard(),
			})
			Expect(err).NotTo(HaveOccurred())

			cl, err := newClientFromConfig(ClientConfig{
				BaseURL:    server.URL + "/api/cost-events",
				EventsPath: "/v1/events",
				CACertFile: caPath,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(cl.post(context.Background(), sampleEvent())).To(Succeed())
			Expect(capturedPath).To(Equal("/api/cost-events/v1/events"))
			Expect(a.HealthCheck(context.Background())).To(Succeed())
		})

		It("rejects unreadable or invalid CACertFile at startup", func() {
			_, err := NewAdapterWithConfig(ClientConfig{
				BaseURL:    "https://cost.example.com",
				CACertFile: filepath.Join(GinkgoT().TempDir(), "missing.crt"),
			})
			Expect(err).To(HaveOccurred())

			badCA := filepath.Join(GinkgoT().TempDir(), "bad.crt")
			Expect(os.WriteFile(badCA, []byte("not-a-cert"), 0o600)).To(Succeed())
			_, err = NewAdapterWithConfig(ClientConfig{
				BaseURL:    "https://cost.example.com",
				CACertFile: badCA,
			})
			Expect(err).To(HaveOccurred())
		})
	})
})
