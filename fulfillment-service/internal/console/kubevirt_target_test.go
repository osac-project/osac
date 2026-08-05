/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package console

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/rest"
)

var _ = Describe("authHeadersFromConfig", func() {
	It("should return Authorization header for BearerToken", func() {
		config := &rest.Config{
			Host:        "https://localhost:6443",
			BearerToken: "my-test-token",
		}
		headers, err := authHeadersFromConfig(config)
		Expect(err).NotTo(HaveOccurred())
		Expect(headers.Get("Authorization")).To(Equal("Bearer my-test-token"))
	})

	It("should return Authorization header for BearerTokenFile", func() {
		tokenDir := GinkgoT().TempDir()
		tokenFile := filepath.Join(tokenDir, "token")
		err := os.WriteFile(tokenFile, []byte("file-based-token"), 0600)
		Expect(err).NotTo(HaveOccurred())

		config := &rest.Config{
			Host:            "https://localhost:6443",
			BearerTokenFile: tokenFile,
		}
		headers, err := authHeadersFromConfig(config)
		Expect(err).NotTo(HaveOccurred())
		Expect(headers.Get("Authorization")).To(Equal("Bearer file-based-token"))
	})

	It("should read updated token from BearerTokenFile", func() {
		tokenDir := GinkgoT().TempDir()
		tokenFile := filepath.Join(tokenDir, "token")
		err := os.WriteFile(tokenFile, []byte("initial-token"), 0600)
		Expect(err).NotTo(HaveOccurred())

		config := &rest.Config{
			Host:            "https://localhost:6443",
			BearerTokenFile: tokenFile,
		}

		headers, err := authHeadersFromConfig(config)
		Expect(err).NotTo(HaveOccurred())
		Expect(headers.Get("Authorization")).To(Equal("Bearer initial-token"))

		// Update the token file and verify the new token is read.
		err = os.WriteFile(tokenFile, []byte("rotated-token"), 0600)
		Expect(err).NotTo(HaveOccurred())

		headers, err = authHeadersFromConfig(config)
		Expect(err).NotTo(HaveOccurred())
		Expect(headers.Get("Authorization")).To(Equal("Bearer rotated-token"))
	})

	It("should return no Authorization header when no auth is configured", func() {
		config := &rest.Config{
			Host: "https://localhost:6443",
		}
		headers, err := authHeadersFromConfig(config)
		Expect(err).NotTo(HaveOccurred())
		Expect(headers.Get("Authorization")).To(BeEmpty())
	})

	It("should prefer BearerTokenFile over BearerToken when both are set", func() {
		tokenDir := GinkgoT().TempDir()
		tokenFile := filepath.Join(tokenDir, "token")
		err := os.WriteFile(tokenFile, []byte("file-token"), 0600)
		Expect(err).NotTo(HaveOccurred())

		config := &rest.Config{
			Host:            "https://localhost:6443",
			BearerToken:     "inline-token",
			BearerTokenFile: tokenFile,
		}
		headers, err := authHeadersFromConfig(config)
		Expect(err).NotTo(HaveOccurred())
		// client-go treats BearerTokenFile as a refreshable source that
		// takes precedence over the static BearerToken.
		Expect(headers.Get("Authorization")).To(Equal("Bearer file-token"))
	})
})
