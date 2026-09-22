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
	"bytes"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Describe fabric domain", func() {
	It("renders the domain details and condition", func() {
		domain := publicv1.FabricDomain_builder{
			Id: "fd-001",
			Metadata: publicv1.Metadata_builder{
				Name: "tenant-a-gpu-ew",
			}.Build(),
			Spec: publicv1.FabricDomainSpec_builder{
				Type:            publicv1.FabricDomainType_FABRIC_DOMAIN_TYPE_ETHERNET_EW,
				Servers:         []string{"hgx-01", "hgx-02"},
				VirtualNetworks: []string{"vnet-001"},
			}.Build(),
			Status: publicv1.FabricDomainStatus_builder{
				Conditions: []*publicv1.FabricDomainCondition{
					publicv1.FabricDomainCondition_builder{
						Type:    publicv1.FabricDomainConditionType_FABRIC_DOMAIN_CONDITION_TYPE_READY,
						Status:  publicv1.ConditionStatus_CONDITION_STATUS_TRUE,
						Message: new("Fabric domain is ready"),
					}.Build(),
				},
			}.Build(),
		}.Build()

		var output bytes.Buffer
		RenderFabricDomain(&output, domain)

		Expect(output.String()).To(ContainSubstring("fd-001"))
		Expect(output.String()).To(ContainSubstring("tenant-a-gpu-ew"))
		Expect(output.String()).To(ContainSubstring("ETHERNET_EW"))
		Expect(output.String()).To(ContainSubstring("hgx-01, hgx-02"))
		Expect(output.String()).To(ContainSubstring("vnet-001"))
		Expect(output.String()).To(ContainSubstring("READY"))
		Expect(output.String()).To(ContainSubstring("TRUE"))
		Expect(output.String()).To(ContainSubstring("Fabric domain is ready"))
	})

	It("renders placeholders when optional fields are absent", func() {
		var output bytes.Buffer
		RenderFabricDomain(&output, publicv1.FabricDomain_builder{Id: "fd-002"}.Build())

		Expect(output.String()).To(MatchRegexp(`Name:\s+-`))
		Expect(output.String()).To(MatchRegexp(`State:\s+-`))
		Expect(output.String()).To(MatchRegexp(`Status:\s+-`))
		Expect(output.String()).To(MatchRegexp(`Message:\s+-`))
	})
})
