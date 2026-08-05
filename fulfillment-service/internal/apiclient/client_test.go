/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/fulfillment-service/internal/auth"
)

var _ = Describe("HTTP Client", func() {
	var (
		ctx    context.Context
		client *Client
		server *httptest.Server
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	AfterEach(func() {
		if server != nil {
			server.Close()
		}
	})

	Describe("DoRequest", func() {
		It("makes a successful authenticated request", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Header.Get("Authorization")).To(Equal("Bearer test-token"))
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			}))

			client = createTestClient(server.URL)

			resp, err := client.DoRequest(ctx, http.MethodGet, "/test", nil)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		})

		It("returns an APIError for 404 not found", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			}))

			client = createTestClient(server.URL)

			_, err := client.DoRequest(ctx, http.MethodGet, "/test", nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("API error"))

			var apiErr *APIError
			Expect(errors.As(err, &apiErr)).To(BeTrue())
			Expect(apiErr.StatusCode).To(Equal(http.StatusNotFound))
		})

		It("returns an APIError for 409 conflict", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
			}))

			client = createTestClient(server.URL)

			_, err := client.DoRequest(ctx, http.MethodPost, "/test", map[string]string{"test": "data"})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("API error"))

			var apiErr *APIError
			Expect(errors.As(err, &apiErr)).To(BeTrue())
			Expect(apiErr.StatusCode).To(Equal(http.StatusConflict))
		})

		It("returns an error for 401 unauthorized", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			}))

			client = createTestClient(server.URL)

			_, err := client.DoRequest(ctx, http.MethodGet, "/test", nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("API error"))
		})

		It("returns an error for 403 forbidden", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
			}))

			client = createTestClient(server.URL)

			_, err := client.DoRequest(ctx, http.MethodGet, "/test", nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("API error"))
		})

		It("returns an APIError for 400 bad request with body", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte("invalid input"))
			}))

			client = createTestClient(server.URL)

			_, err := client.DoRequest(ctx, http.MethodPost, "/test", map[string]string{"bad": "data"})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("API error"))

			var apiErr *APIError
			Expect(errors.As(err, &apiErr)).To(BeTrue())
			Expect(apiErr.StatusCode).To(Equal(http.StatusBadRequest))
			Expect(apiErr.Body).To(Equal("invalid input"))
		})

		It("returns an error for 500 internal server error", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)

			_, err := client.DoRequest(ctx, http.MethodGet, "/test", nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("API error"))
			Expect(err.Error()).To(ContainSubstring("status=500"))
		})

		It("includes Content-Type header when body is provided", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Header.Get("Content-Type")).To(Equal("application/json"))
				Expect(r.Header.Get("Authorization")).To(Equal("Bearer test-token"))
				w.WriteHeader(http.StatusOK)
			}))

			client = createTestClient(server.URL)

			resp, err := client.DoRequest(ctx, http.MethodPost, "/test", map[string]string{"key": "value"})
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()
		})

		It("does not include Content-Type header when body is nil", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Header.Get("Content-Type")).To(BeEmpty())
				Expect(r.Header.Get("Authorization")).To(Equal("Bearer test-token"))
				w.WriteHeader(http.StatusOK)
			}))

			client = createTestClient(server.URL)

			resp, err := client.DoRequest(ctx, http.MethodGet, "/test", nil)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()
		})

		It("limits error response body to 1MB to prevent memory exhaustion", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				// Write 2MB of data (exceeds 1MB limit)
				largeBody := make([]byte, 2<<20) // 2 MB
				for i := range largeBody {
					largeBody[i] = 'A'
				}
				w.Write(largeBody)
			}))

			client = createTestClient(server.URL)

			_, err := client.DoRequest(ctx, http.MethodGet, "/test", nil)
			Expect(err).To(HaveOccurred())

			var apiErr *APIError
			Expect(errors.As(err, &apiErr)).To(BeTrue())
			Expect(apiErr.StatusCode).To(Equal(http.StatusBadRequest))
			// Body should be truncated to 1MB (1<<20 bytes)
			Expect(apiErr.Body).To(HaveLen(1 << 20))
		})
	})

	Describe("Client Builder", func() {
		It("returns an error when logger is missing", func() {
			tokenSource, err := auth.NewStaticTokenSource().
				SetLogger(logger).
				SetToken(&auth.Token{Access: "test-token"}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = NewClient().
				SetBaseURL("http://localhost").
				SetTokenSource(tokenSource).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger is mandatory"))
		})

		It("returns an error when base URL is missing", func() {
			tokenSource, err := auth.NewStaticTokenSource().
				SetLogger(logger).
				SetToken(&auth.Token{Access: "test-token"}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = NewClient().
				SetLogger(logger).
				SetTokenSource(tokenSource).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("base URL is mandatory"))
		})

		It("returns an error when token source is missing", func() {
			_, err := NewClient().
				SetLogger(logger).
				SetBaseURL("http://localhost").
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("token source is mandatory"))
		})
	})
})

func createTestClient(serverURL string) *Client {
	tokenSource, err := auth.NewStaticTokenSource().
		SetLogger(logger).
		SetToken(&auth.Token{Access: "test-token"}).
		Build()
	Expect(err).ToNot(HaveOccurred())

	client, err := NewClient().
		SetLogger(logger).
		SetBaseURL(serverURL).
		SetTokenSource(tokenSource).
		Build()
	Expect(err).ToNot(HaveOccurred())

	return client
}
