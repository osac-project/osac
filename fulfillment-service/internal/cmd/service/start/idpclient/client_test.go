//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
// License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package idpclient

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRealmFromIssuer(t *testing.T) {
	for _, tc := range []struct {
		issuer string
		realm  string
	}{
		{"https://keycloak.example.com/realms/osac", "osac"},
		{"https://keycloak.example.com/realms/demoav1", "demoav1"},
		{"https://keycloak.example.com/realms/demoav1/", "demoav1"},
		{"https://kc.example.com:8443/realms/run-42", "run-42"},
		// Legacy shapes without a realms segment fall back to the default.
		{"https://keycloak.example.com/auth/realms/osac", "osac"},
		{"https://keycloak.example.com", "osac"},
		{"", "osac"},
		// A trailing realms segment with no name also falls back.
		{"https://keycloak.example.com/realms/", "osac"},
	} {
		if got := realmFromIssuer(tc.issuer); got != tc.realm {
			t.Errorf("realmFromIssuer(%q) = %q, want %q", tc.issuer, got, tc.realm)
		}
	}
}

var _ = Describe("realmFromIssuer", func() {
	It("derives the realm from the issuer URL", func() {
		Expect(realmFromIssuer("https://keycloak.example.com/realms/demoav1")).To(Equal("demoav1"))
	})
})
