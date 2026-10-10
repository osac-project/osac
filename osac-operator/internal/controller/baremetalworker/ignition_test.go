/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalworker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// captureRoundTripper records the request context and returns a canned response.
type captureRoundTripper struct {
	lastCtx context.Context
	resp    *http.Response
}

func (rt *captureRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.lastCtx = req.Context()
	return rt.resp, nil
}

var _ = Describe("IgnitionFetcher", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("returns the body on a 2xx response", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ignition-content"))
		}))
		defer srv.Close()

		body, err := NewIgnitionFetcher(nil).FetchIgnition(ctx, srv.URL)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(body)).To(Equal("ignition-content"))
	})

	It("fetches HTTPS ignition when the server CA is trusted", func() {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ignition-content"))
		}))
		defer srv.Close()

		roots := x509.NewCertPool()
		roots.AddCert(srv.Certificate())
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{RootCAs: roots}
		defer transport.CloseIdleConnections()

		body, err := NewIgnitionFetcher(&http.Client{Transport: transport}).FetchIgnition(ctx, srv.URL)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(body)).To(Equal("ignition-content"))
	})

	It("errors on a non-2xx response and includes a body snippet", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom detail"))
		}))
		defer srv.Close()

		_, err := NewIgnitionFetcher(nil).FetchIgnition(ctx, srv.URL)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("500"))
		Expect(err.Error()).To(ContainSubstring("boom detail"))
	})

	It("applies the per-call deadline to the request", func() {
		rt := &captureRoundTripper{resp: &http.Response{
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
		}}
		_, err := NewIgnitionFetcher(&http.Client{Transport: rt}).FetchIgnition(ctx, "http://example.test/ignition")
		Expect(err).ToNot(HaveOccurred())
		expectCallDeadline(rt.lastCtx)
	})
})
