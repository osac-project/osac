/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package virtualnetwork

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

func formatVirtualNetwork(vn *publicv1.VirtualNetwork) string {
	var buf bytes.Buffer
	RenderVirtualNetwork(&buf, vn)
	return buf.String()
}

var _ = Describe("Describe Virtual Network", func() {
	Describe("Rendering tests", func() {
		It("should display all fields when set", func() {
			msg := "Network ready"
			vn := publicv1.VirtualNetwork_builder{
				Id: "vnet-001",
				Metadata: publicv1.Metadata_builder{
					Name: "example-vn",
				}.Build(),
				Spec: publicv1.VirtualNetworkSpec_builder{
					NetworkClass: "udn-net",
					Ipv4Cidr:     new("10.0.0.0/16"),
					Ipv6Cidr:     new("2001:db8::/48"),
				}.Build(),
				Status: publicv1.VirtualNetworkStatus_builder{
					State:   publicv1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
					Message: &msg,
				}.Build(),
			}.Build()

			output := formatVirtualNetwork(vn)
			Expect(output).To(ContainSubstring("vnet-001"))
			Expect(output).To(ContainSubstring("example-vn"))
			Expect(output).To(ContainSubstring("udn-net"))
			Expect(output).To(ContainSubstring("10.0.0.0/16"))
			Expect(output).To(ContainSubstring("2001:db8::/48"))
			Expect(output).To(ContainSubstring("READY"))
			Expect(output).To(ContainSubstring("Network ready"))
		})

		It("should show '-' for name, state, and message when status is nil", func() {
			vn := publicv1.VirtualNetwork_builder{
				Id: "vnet-002",
				Spec: publicv1.VirtualNetworkSpec_builder{
					NetworkClass: "udn-net",
				}.Build(),
			}.Build()

			output := formatVirtualNetwork(vn)
			Expect(output).To(MatchRegexp(`Name:\s+-`))
			Expect(output).To(MatchRegexp(`State:\s+-`))
			Expect(output).To(MatchRegexp(`Message:\s+-`))
		})

		It("should show '-' for IPv4 CIDR when not set", func() {
			vn := publicv1.VirtualNetwork_builder{
				Id: "vnet-003",
				Spec: publicv1.VirtualNetworkSpec_builder{
					NetworkClass: "udn-net",
					Ipv6Cidr:     new("2001:db8::/48"),
				}.Build(),
			}.Build()

			output := formatVirtualNetwork(vn)
			Expect(output).To(MatchRegexp(`IPv4 CIDR:\s+-`))
			Expect(output).To(ContainSubstring("2001:db8::/48"))
		})

		It("should show '-' for IPv6 CIDR when not set", func() {
			vn := publicv1.VirtualNetwork_builder{
				Id: "vnet-004",
				Spec: publicv1.VirtualNetworkSpec_builder{
					NetworkClass: "udn-net",
					Ipv4Cidr:     new("10.0.0.0/16"),
				}.Build(),
			}.Build()

			output := formatVirtualNetwork(vn)
			Expect(output).To(ContainSubstring("10.0.0.0/16"))
			Expect(output).To(MatchRegexp(`IPv6 CIDR:\s+-`))
		})

		It("should strip VIRTUAL_NETWORK_STATE_ prefix from state", func() {
			vn := publicv1.VirtualNetwork_builder{
				Id: "vnet-005",
				Spec: publicv1.VirtualNetworkSpec_builder{
					NetworkClass: "udn-net",
				}.Build(),
				Status: publicv1.VirtualNetworkStatus_builder{
					State: publicv1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
				}.Build(),
			}.Build()

			output := formatVirtualNetwork(vn)
			Expect(output).To(ContainSubstring("READY"))
			Expect(output).NotTo(ContainSubstring("VIRTUAL_NETWORK_STATE_"))
		})

		It("should show '-' for message when status has no message", func() {
			vn := publicv1.VirtualNetwork_builder{
				Id: "vnet-006",
				Spec: publicv1.VirtualNetworkSpec_builder{
					NetworkClass: "udn-net",
				}.Build(),
				Status: publicv1.VirtualNetworkStatus_builder{
					State: publicv1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING,
				}.Build(),
			}.Build()

			output := formatVirtualNetwork(vn)
			Expect(output).To(MatchRegexp(`Message:\s+-`))
		})
	})
})
