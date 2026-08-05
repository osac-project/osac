/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package netutil

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = Describe("ValidateCIDRs", func() {
	DescribeTable("valid cases",
		func(ipv4Cidr, ipv6Cidr string) {
			err := ValidateCIDRs(ipv4Cidr, ipv6Cidr)
			Expect(err).NotTo(HaveOccurred())
		},
		Entry("valid IPv4 CIDR only", "10.0.0.0/16", ""),
		Entry("valid IPv6 CIDR only", "", "fd00::/48"),
		Entry("valid dual-stack", "10.0.0.0/16", "fd00::/48"),
		Entry("IPv4 /0 network", "0.0.0.0/0", ""),
		Entry("IPv6 /0 network", "", "::/0"),
	)

	DescribeTable("error cases",
		func(ipv4Cidr, ipv6Cidr, errSubstring string) {
			err := ValidateCIDRs(ipv4Cidr, ipv6Cidr)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(errSubstring))
		},
		Entry("missing both CIDRs",
			"", "", "at least one of --ipv4-cidr or --ipv6-cidr is required",
		),
		Entry("invalid IPv4 CIDR",
			"not-a-cidr", "", "invalid IPv4 CIDR",
		),
		Entry("IPv6 address in --ipv4-cidr flag",
			"fd00::/48", "", "invalid IPv4 CIDR",
		),
		Entry("invalid IPv6 CIDR",
			"", "not-a-cidr", "invalid IPv6 CIDR",
		),
		Entry("IPv4 address in --ipv6-cidr flag",
			"", "10.0.0.0/16", "invalid IPv6 CIDR",
		),
	)
})

var _ = Describe("ValidateVirtualNetwork", func() {
	It("should return an error when virtual network is empty", func() {
		err := ValidateVirtualNetwork("")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("virtual network is required"))
	})

	It("should return nil when virtual network is provided", func() {
		err := ValidateVirtualNetwork("vnet-abc123")
		Expect(err).NotTo(HaveOccurred())
	})
})
