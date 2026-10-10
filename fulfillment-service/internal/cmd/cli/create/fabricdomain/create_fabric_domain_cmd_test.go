/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package fabricdomain

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Create fabric domain", func() {
	DescribeTable("parses fabric domain types",
		func(input string, expected publicv1.FabricDomainType) {
			actual, err := parseType(input)
			Expect(err).NotTo(HaveOccurred())
			Expect(actual).To(Equal(expected))
		},
		Entry("ethernet east-west", "ethernet_ew", publicv1.FabricDomainType_FABRIC_DOMAIN_TYPE_ETHERNET_EW),
		Entry("case and whitespace variations", " Ethernet_EW ", publicv1.FabricDomainType_FABRIC_DOMAIN_TYPE_ETHERNET_EW),
	)

	It("rejects unsupported types", func() {
		_, err := parseType("infiniband_ew")
		Expect(err).To(MatchError("unsupported fabric domain type \"infiniband_ew\"; only 'ethernet_ew' is supported"))
	})
})
