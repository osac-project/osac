/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("cidr validation helpers", func() {
	Describe("parseAndValidateCIDR", func() {
		It("masks host bits in canonical notation", func() {
			canonical, err := parseAndValidateCIDR("10.0.1.5/24", cidrIPv4)
			Expect(err).ToNot(HaveOccurred())
			Expect(canonical).To(Equal("10.0.1.0/24"))
		})
	})

	Describe("cidrPrefixesEqual", func() {
		It("returns true for identical strings", func() {
			equal, err := cidrPrefixesEqual("10.0.1.0/24", "10.0.1.0/24")
			Expect(err).ToNot(HaveOccurred())
			Expect(equal).To(BeTrue())
		})

		It("returns true for canonically equivalent prefixes", func() {
			equal, err := cidrPrefixesEqual("10.0.1.5/24", "10.0.1.0/24")
			Expect(err).ToNot(HaveOccurred())
			Expect(equal).To(BeTrue())
		})

		It("returns false for different networks", func() {
			equal, err := cidrPrefixesEqual("10.0.0.0/16", "192.168.0.0/16")
			Expect(err).ToNot(HaveOccurred())
			Expect(equal).To(BeFalse())
		})
	})

	Describe("validateImmutableCIDR", func() {
		It("allows canonically equivalent rewrite", func() {
			err := validateImmutableCIDR("spec.ipv4_cidr", "10.0.1.5/24", "10.0.1.0/24", cidrIPv4)
			Expect(err).ToNot(HaveOccurred())
		})

		It("rejects a real network change with canonical values in the error", func() {
			err := validateImmutableCIDR("spec.ipv4_cidr", "10.0.0.0/16", "192.168.0.0/16", cidrIPv4)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("immutable"))
			Expect(err.Error()).To(ContainSubstring("10.0.0.0/16"))
			Expect(err.Error()).To(ContainSubstring("192.168.0.0/16"))
		})
	})

	Describe("canonicalizeDualStackCIDRs", func() {
		It("canonicalizes both address families when present", func() {
			ipv4 := "10.0.1.5/24"
			ipv6 := "2001:db8::1/32"
			err := canonicalizeDualStackCIDRs(
				func() string { return ipv4 },
				func(v string) { ipv4 = v },
				func() string { return ipv6 },
				func(v string) { ipv6 = v },
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(ipv4).To(Equal("10.0.1.0/24"))
			Expect(ipv6).To(Equal("2001:db8::/32"))
		})
	})
})
