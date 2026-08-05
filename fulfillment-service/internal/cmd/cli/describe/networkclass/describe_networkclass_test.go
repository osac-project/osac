/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package networkclass

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

func formatNetworkClass(nc *publicv1.NetworkClass) string {
	var buf bytes.Buffer
	renderNetworkClass(&buf, nc)
	return buf.String()
}

func boolPtr(v bool) *bool {
	return &v
}

var _ = Describe("Describe NetworkClass", func() {
	Describe("Rendering tests", func() {
		It("should display all fields when set", func() {
			msg := "All backends healthy"
			nc := publicv1.NetworkClass_builder{
				Id: "nc-001",
				Metadata: publicv1.Metadata_builder{
					Name: "udn-net",
				}.Build(),
				Title:       "UDN Network",
				Description: "User-Defined Network backed by OVN-Kubernetes",
				Capabilities: publicv1.NetworkClassCapabilities_builder{
					SupportsIpv4:      true,
					SupportsIpv6:      true,
					SupportsDualStack: true,
				}.Build(),
				Status: publicv1.NetworkClassStatus_builder{
					State:   publicv1.NetworkClassState_NETWORK_CLASS_STATE_READY,
					Message: &msg,
				}.Build(),
				IsDefault: boolPtr(true),
			}.Build()

			output := formatNetworkClass(nc)
			Expect(output).To(ContainSubstring("nc-001"))
			Expect(output).To(ContainSubstring("udn-net"))
			Expect(output).To(ContainSubstring("UDN Network"))
			Expect(output).To(ContainSubstring("User-Defined Network backed by OVN-Kubernetes"))
			Expect(output).To(MatchRegexp(`Supports IPv4:\s+yes`))
			Expect(output).To(MatchRegexp(`Supports IPv6:\s+yes`))
			Expect(output).To(MatchRegexp(`Supports Dual Stack:\s+yes`))
			Expect(output).To(MatchRegexp(`Default:\s+yes`))
			Expect(output).To(ContainSubstring("READY"))
			Expect(output).To(ContainSubstring("All backends healthy"))
		})

		It("should show '-' for state and message when status is nil", func() {
			nc := publicv1.NetworkClass_builder{
				Id: "nc-002",
			}.Build()

			output := formatNetworkClass(nc)
			Expect(output).To(MatchRegexp(`State:\s+-`))
			Expect(output).To(MatchRegexp(`Message:\s+-`))
		})

		It("should show '-' for capabilities when not set", func() {
			nc := publicv1.NetworkClass_builder{
				Id: "nc-003",
			}.Build()

			output := formatNetworkClass(nc)
			Expect(output).To(MatchRegexp(`Supports IPv4:\s+-`))
			Expect(output).To(MatchRegexp(`Supports IPv6:\s+-`))
			Expect(output).To(MatchRegexp(`Supports Dual Stack:\s+-`))
		})

		It("should strip NETWORK_CLASS_STATE_ prefix from state", func() {
			nc := publicv1.NetworkClass_builder{
				Id: "nc-004",
				Status: publicv1.NetworkClassStatus_builder{
					State: publicv1.NetworkClassState_NETWORK_CLASS_STATE_READY,
				}.Build(),
			}.Build()

			output := formatNetworkClass(nc)
			Expect(output).To(ContainSubstring("READY"))
			Expect(output).NotTo(ContainSubstring("NETWORK_CLASS_STATE_"))
		})

		It("should show '-' for optional fields when not set", func() {
			nc := publicv1.NetworkClass_builder{
				Id: "nc-005",
			}.Build()

			output := formatNetworkClass(nc)
			Expect(output).To(MatchRegexp(`Name:\s+-`))
			Expect(output).To(MatchRegexp(`Title:\s+-`))
			Expect(output).To(MatchRegexp(`Description:\s+-`))
			Expect(output).To(MatchRegexp(`Default:\s+-`))
		})

		It("should show 'no' for unsupported capabilities", func() {
			nc := publicv1.NetworkClass_builder{
				Id: "nc-006",
				Capabilities: publicv1.NetworkClassCapabilities_builder{
					SupportsIpv4:      true,
					SupportsIpv6:      false,
					SupportsDualStack: false,
				}.Build(),
			}.Build()

			output := formatNetworkClass(nc)
			Expect(output).To(MatchRegexp(`Supports IPv4:\s+yes`))
			Expect(output).To(MatchRegexp(`Supports IPv6:\s+no`))
			Expect(output).To(MatchRegexp(`Supports Dual Stack:\s+no`))
		})

		It("should show 'no' for is_default when explicitly false", func() {
			nc := publicv1.NetworkClass_builder{
				Id:        "nc-007",
				IsDefault: boolPtr(false),
			}.Build()

			output := formatNetworkClass(nc)
			Expect(output).To(MatchRegexp(`Default:\s+no`))
		})

		It("should show '-' for message when status has no message", func() {
			nc := publicv1.NetworkClass_builder{
				Id: "nc-008",
				Status: publicv1.NetworkClassStatus_builder{
					State: publicv1.NetworkClassState_NETWORK_CLASS_STATE_PENDING,
				}.Build(),
			}.Build()

			output := formatNetworkClass(nc)
			Expect(output).To(MatchRegexp(`Message:\s+-`))
		})
	})
})
