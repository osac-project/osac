/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package secretref

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/osac-installer/pkg/install"
)

var _ = Describe("Parse", func() {
	It("parses namespace/name with no keys", func() {
		ref, err := Parse("osac/config-as-code-manifest-ig")

		Expect(err).NotTo(HaveOccurred())
		Expect(ref).To(Equal(install.SecretRef{Namespace: "osac", Name: "config-as-code-manifest-ig"}))
	})

	It("parses namespace/name:key1,key2", func() {
		ref, err := Parse("osac/osac-db-config:url,user,password")

		Expect(err).NotTo(HaveOccurred())
		Expect(ref).To(Equal(install.SecretRef{
			Namespace: "osac", Name: "osac-db-config", Keys: []string{"url", "user", "password"},
		}))
	})

	It("rejects malformed values", func() {
		for _, raw := range []string{"just-a-name", "/name", "namespace/", "namespace/:key1"} {
			_, err := Parse(raw)
			Expect(err).To(HaveOccurred(), "raw=%q", raw)
		}
	})
})

var _ = Describe("ParseAll", func() {
	It("parses every value in order", func() {
		refs, err := ParseAll([]string{"osac/a", "osac/b:k1"})

		Expect(err).NotTo(HaveOccurred())
		Expect(refs).To(Equal([]install.SecretRef{
			{Namespace: "osac", Name: "a"},
			{Namespace: "osac", Name: "b", Keys: []string{"k1"}},
		}))
	})

	It("returns an empty slice, not nil, for no input", func() {
		refs, err := ParseAll(nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(refs).To(BeEmpty())
	})

	It("fails on the first invalid value", func() {
		_, err := ParseAll([]string{"osac/a", "invalid"})

		Expect(err).To(HaveOccurred())
	})
})
