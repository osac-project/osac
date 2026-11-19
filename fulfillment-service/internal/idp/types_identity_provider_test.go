/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package idp

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Keycloak identity provider conversion", func() {
	It("hides providers until their organization is resolved", func() {
		provider := &IdentityProvider{
			Alias: "tenant-example",
			Config: map[string]string{
				"clientId":                             "example-client",
				keycloakHideIdPWhenOrganizationUnknown: "false",
			},
		}

		keycloakProvider := toKeycloakIdentityProvider(provider)

		Expect(keycloakProvider.Config).To(HaveKeyWithValue(keycloakHideIdPWhenOrganizationUnknown, "true"))
		Expect(keycloakProvider.Config).To(HaveKeyWithValue("clientId", "example-client"))
		Expect(provider.Config).To(HaveKeyWithValue(keycloakHideIdPWhenOrganizationUnknown, "false"))
	})

	It("adds the visibility setting when provider config is empty", func() {
		keycloakProvider := toKeycloakIdentityProvider(&IdentityProvider{Alias: "tenant-example"})

		Expect(keycloakProvider.Config).To(HaveKeyWithValue(keycloakHideIdPWhenOrganizationUnknown, "true"))
	})

	It("does not expose the visibility setting as provider-specific config", func() {
		provider := fromKeycloakIdentityProvider(&keycloakIdentityProvider{
			Alias:      "tenant-example",
			ProviderID: "oidc",
			Config: map[string]string{
				"clientId":                             "example-client",
				keycloakHideIdPWhenOrganizationUnknown: "true",
			},
		})

		Expect(provider.Config).To(Equal(map[string]string{"clientId": "example-client"}))
	})
})
