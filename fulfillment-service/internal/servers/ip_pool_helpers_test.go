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
	"math"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
)

var _ = Describe("IP pool helpers", func() {
	DescribeTable("calculateCIDRCapacity",
		func(cidr string, family privatev1.IPFamily, expected int64) {
			Expect(calculateCIDRCapacity(cidr, family)).To(BeNumerically("==", expected))
		},
		Entry("IPv4 /24 → 254", "192.168.1.0/24", privatev1.IPFamily_IP_FAMILY_IPV4, int64(254)),
		Entry("IPv4 /28 → 14", "10.0.0.0/28", privatev1.IPFamily_IP_FAMILY_IPV4, int64(14)),
		Entry("IPv4 /30 → 2", "10.0.0.0/30", privatev1.IPFamily_IP_FAMILY_IPV4, int64(2)),
		Entry("IPv4 /31 → 2", "10.0.0.0/31", privatev1.IPFamily_IP_FAMILY_IPV4, int64(2)),
		Entry("IPv4 /32 → 1", "10.0.0.1/32", privatev1.IPFamily_IP_FAMILY_IPV4, int64(1)),
		Entry("IPv4 /16 → 65534", "10.0.0.0/16", privatev1.IPFamily_IP_FAMILY_IPV4, int64(65534)),
		Entry("IPv6 /128 → 1", "2001:db8::1/128", privatev1.IPFamily_IP_FAMILY_IPV6, int64(1)),
		Entry("IPv6 /127 → 2", "2001:db8::/127", privatev1.IPFamily_IP_FAMILY_IPV6, int64(2)),
		Entry("IPv6 /64 → MaxInt64", "2001:db8::/64", privatev1.IPFamily_IP_FAMILY_IPV6, int64(math.MaxInt64)),
	)

	It("calculateCIDRCapacity returns 0 for an unparseable CIDR", func() {
		result := calculateCIDRCapacity("not-a-cidr", privatev1.IPFamily_IP_FAMILY_IPV4)
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
