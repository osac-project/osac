/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package subnet

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

func formatSubnet(s *publicv1.Subnet) string {
	var buf bytes.Buffer
	RenderSubnet(&buf, s)
	return buf.String()
}

var _ = Describe("Describe Subnet", func() {
	Describe("Rendering tests", func() {
		It("should display all fields when set", func() {
			msg := "Subnet ready"
			s := publicv1.Subnet_builder{
				Id: "subnet-001",
				Metadata: publicv1.Metadata_builder{
					Name: "example-subnet",
				}.Build(),
				Spec: publicv1.SubnetSpec_builder{
					VirtualNetwork: "vnet-abc123",
					Ipv4Cidr:       new("10.0.1.0/24"),
					Ipv6Cidr:       new("2001:db8::/64"),
				}.Build(),
				Status: publicv1.SubnetStatus_builder{
					State:   publicv1.SubnetState_SUBNET_STATE_READY,
					Message: &msg,
				}.Build(),
			}.Build()

			output := formatSubnet(s)
			Expect(output).To(ContainSubstring("subnet-001"))
			Expect(output).To(ContainSubstring("example-subnet"))
			Expect(output).To(ContainSubstring("vnet-abc123"))
			Expect(output).To(ContainSubstring("10.0.1.0/24"))
			Expect(output).To(ContainSubstring("2001:db8::/64"))
			Expect(output).To(ContainSubstring("READY"))
			Expect(output).To(ContainSubstring("Subnet ready"))
		})

		It("should show '-' for state and message when status is nil", func() {
			s := publicv1.Subnet_builder{
				Id: "subnet-002",
				Spec: publicv1.SubnetSpec_builder{
					VirtualNetwork: "vnet-abc123",
				}.Build(),
			}.Build()

			output := formatSubnet(s)
			Expect(output).To(MatchRegexp(`Name:\s+-`))
			Expect(output).To(MatchRegexp(`State:\s+-`))
			Expect(output).To(MatchRegexp(`Message:\s+-`))
		})

		It("should show '-' for IPv4 CIDR when not set", func() {
			s := publicv1.Subnet_builder{
				Id: "subnet-003",
				Spec: publicv1.SubnetSpec_builder{
					VirtualNetwork: "vnet-abc123",
					Ipv6Cidr:       new("2001:db8::/64"),
				}.Build(),
			}.Build()

			output := formatSubnet(s)
			Expect(output).To(MatchRegexp(`IPv4 CIDR:\s+-`))
			Expect(output).To(ContainSubstring("2001:db8::/64"))
		})

		It("should strip SUBNET_STATE_ prefix from state", func() {
			s := publicv1.Subnet_builder{
				Id: "subnet-004",
				Spec: publicv1.SubnetSpec_builder{
					VirtualNetwork: "vnet-abc123",
				}.Build(),
				Status: publicv1.SubnetStatus_builder{
					State: publicv1.SubnetState_SUBNET_STATE_READY,
				}.Build(),
			}.Build()

			output := formatSubnet(s)
			Expect(output).To(ContainSubstring("READY"))
			Expect(output).NotTo(ContainSubstring("SUBNET_STATE_"))
		})

		It("should show '-' for IPv6 CIDR when not set", func() {
			s := publicv1.Subnet_builder{
				Id: "subnet-006",
				Spec: publicv1.SubnetSpec_builder{
					VirtualNetwork: "vnet-abc123",
					Ipv4Cidr:       new("10.0.1.0/24"),
				}.Build(),
			}.Build()

			output := formatSubnet(s)
			Expect(output).To(ContainSubstring("10.0.1.0/24"))
			Expect(output).To(MatchRegexp(`IPv6 CIDR:\s+-`))
		})

		It("should show '-' for message when status has no message", func() {
			s := publicv1.Subnet_builder{
				Id: "subnet-005",
				Spec: publicv1.SubnetSpec_builder{
					VirtualNetwork: "vnet-abc123",
				}.Build(),
				Status: publicv1.SubnetStatus_builder{
					State: publicv1.SubnetState_SUBNET_STATE_PENDING,
				}.Build(),
			}.Build()

			output := formatSubnet(s)
			Expect(output).To(MatchRegexp(`Message:\s+-`))
		})
	})
})
