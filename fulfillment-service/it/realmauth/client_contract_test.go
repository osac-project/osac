/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package realmauth

import (
	"encoding/json"

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type clientContract struct {
	clientID        string
	subject         string
	requestedScopes []string
	audiences       []string
	scopes          []string
	realmRoles      []string
	clientRoles     map[string][]string
}

// Reviewed constants, never generated from the imported JSON or issued token.
// Both exports declare these users/default scopes; roles/profile/basic/osac-api
// mappers and the account/realm-management composites explain the emitted sets.
// Consumer references and policy boundaries are documented in README.md.
func contractFor(clientID string) clientContract {
	c := clientContract{clientID: clientID, audiences: []string{"osac-api"}, scopes: []string{"profile", "email"}}
	switch clientID {
	case "osac-csi-driver":
		c.subject = "e6f7a8b9-c0d1-4e2f-3a4b-6789abcdef01"
		c.realmRoles = []string{"osac-csi-driver"}
	case "osac-ui-backend":
		c.subject = "47dc1206-1ee4-4105-a1ea-0af2b4f1d44d"
	case "osac-controller", "osac-admin":
		c.requestedScopes = []string{"openid"}
		c.scopes = []string{"openid", "profile", "email"}
		c.audiences = []string{"osac-api", "account"}
		c.realmRoles = []string{"default-roles-my realm", "offline_access", "uma_authorization"}
		c.clientRoles = map[string][]string{"account": {"view-profile", "manage-account", "manage-account-links"}}
		c.subject = "c3e4f5a6-b7c8-4d9e-0f1a-2b3c4d5e6f7a"
		if clientID == "osac-controller" {
			c.subject = "b6c7d8e9-f0a1-2345-6789-abcdef012345"
			c.audiences = []string{"osac-api", "account", "realm-management"}
			c.clientRoles["realm-management"] = []string{
				"manage-realm", "manage-users", "view-realm", "view-users", "manage-identity-providers",
				"view-identity-providers", "create-client", "view-clients", "query-groups", "query-users", "query-clients",
			}
		}
	default:
		panic("no reviewed machine-client contract")
	}
	return c
}

// This compiling, nonmutating interface is introduced with behavioral RED
// controls before its implementation. It must report only fixed field names.
func claimMismatches(_ jwt.MapClaims, _ string, _ clientContract) []string {
	return nil
}

const syntheticIssuer = "https://issuer.example.invalid/realms/osac"

// Pure assertion fixtures only: no signed/synthetic token is ever sent to a
// runtime permission test. These literals are independent of contractFor.
func syntheticClientClaims(clientID string) jwt.MapClaims {
	data := map[string]string{
		"osac-csi-driver": `{"iss":"https://issuer.example.invalid/realms/osac","azp":"osac-csi-driver",
		"sub":"e6f7a8b9-c0d1-4e2f-3a4b-6789abcdef01","username":"service-account-osac-csi-driver",
		"aud":"osac-api","scope":"email profile","realm_access":{"roles":["osac-csi-driver"]}}`,
		"osac-ui-backend": `{"iss":"https://issuer.example.invalid/realms/osac","azp":"osac-ui-backend",
		"sub":"47dc1206-1ee4-4105-a1ea-0af2b4f1d44d","username":"service-account-osac-ui-backend",
		"aud":"osac-api","scope":"profile email"}`,
		"osac-admin": `{"iss":"https://issuer.example.invalid/realms/osac","azp":"osac-admin",
		"sub":"c3e4f5a6-b7c8-4d9e-0f1a-2b3c4d5e6f7a","username":"service-account-osac-admin",
		"aud":["account","osac-api"],"scope":"email openid profile",
		"realm_access":{"roles":["uma_authorization","offline_access","default-roles-my realm"]},
		"resource_access":{"account":{"roles":["manage-account-links","view-profile","manage-account"]}}}`,
		"osac-controller": `{"iss":"https://issuer.example.invalid/realms/osac","azp":"osac-controller",
		"sub":"b6c7d8e9-f0a1-2345-6789-abcdef012345","username":"service-account-osac-controller",
		"aud":["realm-management","account","osac-api"],"scope":"openid email profile",
		"realm_access":{"roles":["offline_access","default-roles-my realm","uma_authorization"]},
		"resource_access":{"account":{"roles":["manage-account","manage-account-links","view-profile"]},
		"realm-management":{"roles":["view-clients","query-clients","query-users","query-groups","manage-realm",
		"manage-users","view-realm","view-users","manage-identity-providers","view-identity-providers","create-client"]}}}`,
	}
	var claims jwt.MapClaims
	Expect(json.Unmarshal([]byte(data[clientID]), &claims)).To(Succeed())
	return claims
}

var _ = Describe("Synthetic machine-client claim controls", func() {
	DescribeTable("accepts the reviewed claim shape without changing input", func(clientID string) {
		claims := syntheticClientClaims(clientID)
		before, err := json.Marshal(claims)
		Expect(err).ToNot(HaveOccurred())
		Expect(claimMismatches(claims, syntheticIssuer, contractFor(clientID))).To(BeEmpty())
		after, err := json.Marshal(claims)
		Expect(err).ToNot(HaveOccurred())
		unchanged := string(before) == string(after)
		Expect(unchanged).To(BeTrue(), "claim checks must not rewrite their input")
	}, Entry("CSI", "osac-csi-driver"), Entry("controller", "osac-controller"),
		Entry("admin", "osac-admin"), Entry("UI", "osac-ui-backend"))

	DescribeTable("rejects claim drift using fixed-field diagnostics", func(clientID, field string, mutate func(jwt.MapClaims)) {
		claims := syntheticClientClaims(clientID)
		mutate(claims)
		Expect(claimMismatches(claims, syntheticIssuer, contractFor(clientID))).To(ContainElement(field))
	},
		Entry("foreign issuer", "osac-csi-driver", "iss", func(c jwt.MapClaims) { c["iss"] = "https://foreign.example.invalid" }),
		Entry("missing issuer", "osac-csi-driver", "iss", func(c jwt.MapClaims) { delete(c, "iss") }),
		Entry("other authorized party", "osac-csi-driver", "azp", func(c jwt.MapClaims) { c["azp"] = "osac-admin" }),
		Entry("different subject", "osac-csi-driver", "sub", func(c jwt.MapClaims) { c["sub"] = "different-user" }),
		Entry("administrator username", "osac-csi-driver", "username", func(c jwt.MapClaims) { c["username"] = "service-account-osac-admin" }),
		Entry("additional audience", "osac-csi-driver", "aud", func(c jwt.MapClaims) { c["aud"] = []any{"osac-api", "account"} }),
		Entry("missing audience", "osac-admin", "aud", func(c jwt.MapClaims) { c["aud"] = "osac-api" }),
		Entry("duplicate audience", "osac-csi-driver", "aud", func(c jwt.MapClaims) { c["aud"] = []any{"osac-api", "osac-api"} }),
		Entry("non-string audience", "osac-csi-driver", "aud", func(c jwt.MapClaims) { c["aud"] = []any{42} }),
		Entry("missing consumer openid", "osac-controller", "scope", func(c jwt.MapClaims) { c["scope"] = "profile email" }),
		Entry("unexpected optional scope", "osac-ui-backend", "scope", func(c jwt.MapClaims) { c["scope"] = "profile email organization" }),
		Entry("malformed scope", "osac-csi-driver", "scope", func(c jwt.MapClaims) { c["scope"] = []any{"profile", "email"} }),
		Entry("missing CSI role", "osac-csi-driver", "realm_access", func(c jwt.MapClaims) { delete(c, "realm_access") }),
		Entry("tenant privilege", "osac-admin", "realm_access", func(c jwt.MapClaims) {
			c["realm_access"].(map[string]any)["roles"] = []any{"default-roles-my realm", "offline_access", "uma_authorization", "tenant-admin"}
		}),
		Entry("UI CSI privilege", "osac-ui-backend", "realm_access", func(c jwt.MapClaims) {
			c["realm_access"] = map[string]any{"roles": []any{"osac-csi-driver"}}
		}),
		Entry("malformed realm roles", "osac-csi-driver", "realm_access", func(c jwt.MapClaims) {
			c["realm_access"] = map[string]any{"roles": "osac-csi-driver"}
		}),
		Entry("unexpected account role", "osac-admin", "resource_access", func(c jwt.MapClaims) {
			c["resource_access"].(map[string]any)["account"] = map[string]any{"roles": []any{"view-profile", "manage-account", "manage-account-links", "delete-account"}}
		}),
		Entry("realm-admin privilege", "osac-controller", "resource_access", func(c jwt.MapClaims) {
			c["resource_access"].(map[string]any)["realm-management"] = map[string]any{"roles": []any{"realm-admin"}}
		}),
		Entry("missing composite child", "osac-controller", "resource_access", func(c jwt.MapClaims) {
			c["resource_access"].(map[string]any)["account"] = map[string]any{"roles": []any{"manage-account", "view-profile"}}
		}),
		Entry("unexpected resource client", "osac-ui-backend", "resource_access", func(c jwt.MapClaims) {
			c["resource_access"] = map[string]any{"realm-management": map[string]any{"roles": []any{"manage-users"}}}
		}),
		Entry("malformed client roles", "osac-admin", "resource_access", func(c jwt.MapClaims) { c["resource_access"] = "invalid" }),
		Entry("administrator group", "osac-ui-backend", "groups", func(c jwt.MapClaims) { c["groups"] = []any{"admins"} }),
		Entry("unexpected tenant claim", "osac-csi-driver", "organization", func(c jwt.MapClaims) { c["organization"] = []any{"tenant"} }),
	)
	It("detects changed expectations independently of the claim input", func() {
		c := contractFor("osac-csi-driver")
		c.realmRoles = []string{"osac-csi-driver", "tenant-admin"}
		Expect(claimMismatches(syntheticClientClaims("osac-csi-driver"), syntheticIssuer, c)).To(ContainElement("realm_access"))
	})
})
