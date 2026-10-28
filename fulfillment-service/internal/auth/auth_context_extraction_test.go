/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Auth context extraction", func() {
	It("maps tenant-scoped OAuth client credentials to a tenant subject", func() {
		token := &jwt.Token{Claims: jwt.MapClaims{
			"client_id":   "osac-sa-tenant-account",
			"osac_tenant": "tenant-a",
		}}

		result, err := ExtractAuthContext(token)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.AuthMethod).To(Equal("jwt"))
		Expect(result.Username).To(Equal("osac-sa-tenant-account"))
		Expect(result.Organization).To(Equal([]any{"tenant-a"}))
	})

	It("prefers user identity and organization claims when present", func() {
		token := &jwt.Token{Claims: jwt.MapClaims{
			"preferred_username": "alice",
			"client_id":          "osac-sa-tenant-account",
			"organization":       []any{"tenant-from-organization"},
			"osac_tenant":        "tenant-from-client",
		}}

		result, err := ExtractAuthContext(token)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.Username).To(Equal("alice"))
		Expect(result.Organization).To(Equal([]any{"tenant-from-organization"}))
	})

	It("does not treat an unscoped OAuth client as a tenant subject", func() {
		token := &jwt.Token{Claims: jwt.MapClaims{
			"client_id": "unscoped-client",
		}}

		result, err := ExtractAuthContext(token)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.Username).To(BeEmpty())
		Expect(result.Organization).To(BeNil())
	})
})
