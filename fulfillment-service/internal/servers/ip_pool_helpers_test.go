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

var _ = Describe("IP pool helpers", func() {
	DescribeTable("calculateCIDRCapacity",
		func(cidr string, expected int64) {
			Expect(calculateCIDRCapacity(cidr)).To(BeNumerically("==", expected))
		},
		Entry("IPv4 /24 → 254", "192.168.1.0/24", int64(254)),
		Entry("IPv4 /28 → 14", "10.0.0.0/28", int64(14)),
		Entry("IPv4 /30 → 2", "10.0.0.0/30", int64(2)),
		Entry("IPv4 /31 → 2", "10.0.0.0/31", int64(2)),
		Entry("IPv4 /32 → 1", "10.0.0.1/32", int64(1)),
		Entry("IPv4 /16 → 65534", "10.0.0.0/16", int64(65534)),
		Entry("IPv6 → 0 (pools are IPv4 only)", "2001:db8::/127", int64(0)),
	)

	It("calculateCIDRCapacity returns 0 for an unparseable CIDR", func() {
		result := calculateCIDRCapacity("not-a-cidr")
		Expect(result).To(BeNumerically("==", 0))
	})

	Describe("cidrSlicesEqual", func() {
		It("treats semantically equal CIDRs with different host bits as equal", func() {
			Expect(cidrSlicesEqual(
				[]string{"10.0.1.5/24"},
				[]string{"10.0.1.0/24"},
			)).To(BeTrue())
		})

		It("treats different order as equal", func() {
			Expect(cidrSlicesEqual(
				[]string{"10.0.0.0/24", "10.0.1.0/24"},
				[]string{"10.0.1.0/24", "10.0.0.0/24"},
			)).To(BeTrue())
		})

		It("detects different CIDRs", func() {
			Expect(cidrSlicesEqual(
				[]string{"10.0.0.0/24"},
				[]string{"10.0.1.0/24"},
			)).To(BeFalse())
		})

		It("detects different lengths", func() {
			Expect(cidrSlicesEqual(
				[]string{"10.0.0.0/24"},
				[]string{"10.0.0.0/24", "10.0.1.0/24"},
			)).To(BeFalse())
		})
	})
})
