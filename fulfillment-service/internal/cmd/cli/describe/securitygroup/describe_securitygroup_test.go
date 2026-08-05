/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package securitygroup

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

func formatSecurityGroup(sg *publicv1.SecurityGroup) string {
	var buf bytes.Buffer
	RenderSecurityGroup(&buf, sg)
	return buf.String()
}

var _ = Describe("Describe Security Group", func() {
	Describe("Rendering tests", func() {
		It("should show (none) for ingress and egress when no rules", func() {
			sg := publicv1.SecurityGroup_builder{
				Id: "sg-001",
				Spec: publicv1.SecurityGroupSpec_builder{
					VirtualNetwork: "vnet-abc123",
				}.Build(),
			}.Build()

			output := formatSecurityGroup(sg)
			Expect(output).To(ContainSubstring("Ingress Rules:  (none)"))
			Expect(output).To(ContainSubstring("Egress Rules:  (none)"))
		})

		It("should render TCP ingress rule with port 80", func() {
			sg := publicv1.SecurityGroup_builder{
				Id: "sg-002",
				Spec: publicv1.SecurityGroupSpec_builder{
					VirtualNetwork: "vnet-abc123",
					Ingress: []*publicv1.SecurityRule{
						publicv1.SecurityRule_builder{
							Protocol: publicv1.Protocol_PROTOCOL_TCP,
							PortFrom: new(int32(80)),
							PortTo:   new(int32(80)),
							Ipv4Cidr: new("0.0.0.0/0"),
						}.Build(),
					},
				}.Build(),
			}.Build()

			output := formatSecurityGroup(sg)
			Expect(output).To(ContainSubstring("tcp"))
			Expect(output).To(ContainSubstring("80"))
			Expect(output).To(ContainSubstring("0.0.0.0/0"))
		})

		It("should show '-' for ports when ICMP rule has no ports", func() {
			sg := publicv1.SecurityGroup_builder{
				Id: "sg-003",
				Spec: publicv1.SecurityGroupSpec_builder{
					VirtualNetwork: "vnet-abc123",
					Ingress: []*publicv1.SecurityRule{
						publicv1.SecurityRule_builder{
							Protocol: publicv1.Protocol_PROTOCOL_ICMP,
							Ipv4Cidr: new("10.0.0.0/8"),
						}.Build(),
					},
				}.Build(),
			}.Build()

			output := formatSecurityGroup(sg)
			Expect(output).To(ContainSubstring("icmp"))
			Expect(output).To(MatchRegexp(`icmp\s+-\s+-\s+`))
		})

		It("should show '-' for IPV4-CIDR and IPV6-CIDR when not set", func() {
			sg := publicv1.SecurityGroup_builder{
				Id: "sg-004",
				Spec: publicv1.SecurityGroupSpec_builder{
					VirtualNetwork: "vnet-abc123",
					Ingress: []*publicv1.SecurityRule{
						publicv1.SecurityRule_builder{
							Protocol: publicv1.Protocol_PROTOCOL_ALL,
						}.Build(),
					},
				}.Build(),
			}.Build()

			output := formatSecurityGroup(sg)
			Expect(output).To(MatchRegexp(`all\s+-\s+-\s+-\s+-`))
		})

		It("should strip PROTOCOL_ prefix and lowercase for protocol display", func() {
			sg := publicv1.SecurityGroup_builder{
				Id: "sg-005",
				Spec: publicv1.SecurityGroupSpec_builder{
					VirtualNetwork: "vnet-abc123",
					Ingress: []*publicv1.SecurityRule{
						publicv1.SecurityRule_builder{
							Protocol: publicv1.Protocol_PROTOCOL_TCP,
							PortFrom: new(int32(443)),
							PortTo:   new(int32(443)),
						}.Build(),
					},
				}.Build(),
			}.Build()

			output := formatSecurityGroup(sg)
			Expect(output).To(ContainSubstring("tcp"))
			Expect(output).NotTo(ContainSubstring("PROTOCOL_TCP"))
		})

		It("should strip SECURITY_GROUP_STATE_ prefix from state", func() {
			sg := publicv1.SecurityGroup_builder{
				Id: "sg-006",
				Spec: publicv1.SecurityGroupSpec_builder{
					VirtualNetwork: "vnet-abc123",
				}.Build(),
				Status: publicv1.SecurityGroupStatus_builder{
					State: publicv1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
				}.Build(),
			}.Build()

			output := formatSecurityGroup(sg)
			Expect(output).To(ContainSubstring("READY"))
			Expect(output).NotTo(ContainSubstring("SECURITY_GROUP_STATE_"))
		})

		It("should show '-' for state and message when status is nil", func() {
			sg := publicv1.SecurityGroup_builder{
				Id: "sg-007",
				Spec: publicv1.SecurityGroupSpec_builder{
					VirtualNetwork: "vnet-abc123",
				}.Build(),
			}.Build()

			output := formatSecurityGroup(sg)
			Expect(output).To(MatchRegexp(`State:\s+-`))
			Expect(output).To(MatchRegexp(`Message:\s+-`))
		})

		It("should display all header fields when set", func() {
			msg := "Security group active"
			sg := publicv1.SecurityGroup_builder{
				Id: "sg-008",
				Metadata: publicv1.Metadata_builder{
					Name: "web-sg",
				}.Build(),
				Spec: publicv1.SecurityGroupSpec_builder{
					VirtualNetwork: "vnet-abc123",
				}.Build(),
				Status: publicv1.SecurityGroupStatus_builder{
					State:   publicv1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
					Message: &msg,
				}.Build(),
			}.Build()

			output := formatSecurityGroup(sg)
			Expect(output).To(ContainSubstring("sg-008"))
			Expect(output).To(ContainSubstring("web-sg"))
			Expect(output).To(ContainSubstring("vnet-abc123"))
			Expect(output).To(ContainSubstring("READY"))
			Expect(output).To(ContainSubstring("Security group active"))
		})

		It("should render egress rules separately from ingress rules", func() {
			sg := publicv1.SecurityGroup_builder{
				Id: "sg-009",
				Spec: publicv1.SecurityGroupSpec_builder{
					VirtualNetwork: "vnet-abc123",
					Egress: []*publicv1.SecurityRule{
						publicv1.SecurityRule_builder{
							Protocol: publicv1.Protocol_PROTOCOL_ALL,
						}.Build(),
					},
				}.Build(),
			}.Build()

			output := formatSecurityGroup(sg)
			Expect(output).To(ContainSubstring("Ingress Rules:  (none)"))
			Expect(output).To(ContainSubstring("Egress Rules:"))
			Expect(output).To(ContainSubstring("all"))
		})
	})
})
