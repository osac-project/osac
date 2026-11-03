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

var _ = Describe("ValidateIPv4CIDR", func() {
	DescribeTable("valid cases",
		func(cidr string) {
			Expect(ValidateIPv4CIDR(cidr)).To(Succeed())
		},
		Entry("private network", "10.0.0.0/16"),
		Entry("/0 network", "0.0.0.0/0"),
	)

	DescribeTable("error cases",
		func(cidr string) {
			Expect(ValidateIPv4CIDR(cidr)).NotTo(Succeed())
		},
		Entry("missing", ""),
		Entry("not a CIDR", "not-a-cidr"),
		Entry("IPv6 CIDR", "fd00::/48"),
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
