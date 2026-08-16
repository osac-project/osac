/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package helpers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ComputeVIPRange", func() {
	type testCase struct {
		subnetCIDR      string
		vipPrefixLength int
		wantStart       string
		wantEnd         string
	}

	DescribeTable("computes the correct VIP sub-range",
		func(tc testCase) {
			start, end, err := ComputeVIPRange(tc.subnetCIDR, tc.vipPrefixLength)
			Expect(err).NotTo(HaveOccurred())
			Expect(start.String()).To(Equal(tc.wantStart))
			Expect(end.String()).To(Equal(tc.wantEnd))
		},
		Entry("/24 subnet with /28 VIP prefix", testCase{
			subnetCIDR: "10.0.1.0/24", vipPrefixLength: 28,
			wantStart: "10.0.1.240", wantEnd: "10.0.1.255",
		}),
		Entry("/24 subnet with /26 VIP prefix", testCase{
			subnetCIDR: "10.0.1.0/24", vipPrefixLength: 26,
			wantStart: "10.0.1.192", wantEnd: "10.0.1.255",
		}),
		Entry("/22 subnet with /28 VIP prefix", testCase{
			subnetCIDR: "172.16.0.0/22", vipPrefixLength: 28,
			wantStart: "172.16.3.240", wantEnd: "172.16.3.255",
		}),
		Entry("/24 subnet with /30 VIP prefix", testCase{
			subnetCIDR: "192.168.1.0/24", vipPrefixLength: 30,
			wantStart: "192.168.1.252", wantEnd: "192.168.1.255",
		}),
	)

	DescribeTable("rejects invalid inputs",
		func(subnetCIDR string, vipPrefixLength int) {
			_, _, err := ComputeVIPRange(subnetCIDR, vipPrefixLength)
			Expect(err).To(HaveOccurred())
		},
		Entry("invalid CIDR", "not-a-cidr", 28),
		Entry("VIP prefix equal to subnet prefix", "10.0.1.0/24", 24),
		Entry("VIP prefix smaller than subnet prefix", "10.0.1.0/24", 20),
		Entry("VIP prefix exceeds 32", "10.0.1.0/24", 33),
	)
})

var _ = Describe("FormatVIPRangeCIDR", func() {
	It("formats the VIP range as a CIDR string", func() {
		cidr, err := FormatVIPRangeCIDR("10.0.1.0/24", 28)
		Expect(err).NotTo(HaveOccurred())
		Expect(cidr).To(Equal("10.0.1.240/28"))
	})
})

var _ = Describe("FormatVIPRangeDash", func() {
	It("formats the VIP range as a start-end string", func() {
		dash, err := FormatVIPRangeDash("10.0.1.0/24", 28)
		Expect(err).NotTo(HaveOccurred())
		Expect(dash).To(Equal("10.0.1.240-10.0.1.255"))
	})
})
