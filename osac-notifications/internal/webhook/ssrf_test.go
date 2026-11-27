/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package webhook

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ValidateURL", func() {
	It("rejects non-https and loopback", func() {
		Expect(ValidateURL("http://example.com/hook")).To(HaveOccurred())
		Expect(ValidateURL("https://127.0.0.1/hook")).To(HaveOccurred())
		Expect(ValidateURL("https://user:pass@example.com/hook")).To(HaveOccurred())
	})
})

var _ = Describe("NewHTTPClient SSRF dial", func() {
	It("refuses to dial loopback even when the request URL is https", func() {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		DeferCleanup(srv.Close)

		client := NewHTTPClient(5 * time.Second)
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, nil)
		Expect(err).ToNot(HaveOccurred())
		_, err = client.Do(req)
		Expect(err).To(HaveOccurred())
	})

	It("re-checks blocked IPs in dialPublic", func() {
		dialer := &net.Dialer{Timeout: time.Second}
		_, err := dialPublic(context.Background(), dialer, "tcp", "127.0.0.1:443")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not a public address"))
	})
})
