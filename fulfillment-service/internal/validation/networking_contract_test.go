/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package validation

import (
	"buf.build/go/protovalidate"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("IPv4-only networking protobuf contracts", func() {
	var validator protovalidate.Validator

	BeforeEach(func() {
		var err error
		validator, err = protovalidate.New()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("VirtualNetworkSpec", func() {
		It("accepts a canonical IPv4 CIDR", func() {
			spec := privatev1.VirtualNetworkSpec_builder{
				Ipv4Cidr: new("10.0.0.0/16"),
				Region:   "region-a",
			}.Build()

			Expect(validator.Validate(spec)).To(Succeed())
		})

		It("rejects a missing IPv4 CIDR", func() {
			spec := privatev1.VirtualNetworkSpec_builder{
				Region: "region-a",
			}.Build()

			Expect(validator.Validate(spec)).To(MatchError(ContainSubstring("ipv4_cidr")))
		})

		It("rejects an empty IPv4 CIDR", func() {
			spec := privatev1.VirtualNetworkSpec_builder{
				Ipv4Cidr: new(""),
				Region:   "region-a",
			}.Build()

			Expect(validator.Validate(spec)).To(MatchError(ContainSubstring("ipv4_cidr")))
		})

		It("rejects an IPv4 CIDR with host bits", func() {
			spec := privatev1.VirtualNetworkSpec_builder{
				Ipv4Cidr: new("10.0.0.1/16"),
				Region:   "region-a",
			}.Build()

			Expect(validator.Validate(spec)).To(MatchError(ContainSubstring("canonical")))
		})

		It("rejects an IPv6-only request", func() {
			spec := privatev1.VirtualNetworkSpec_builder{
				Ipv6Cidr: new("2001:db8::/32"),
				Region:   "region-a",
			}.Build()

			Expect(validator.Validate(spec)).To(MatchError(ContainSubstring("ipv4_cidr")))
		})

		It("rejects a dual-stack request", func() {
			spec := privatev1.VirtualNetworkSpec_builder{
				Ipv4Cidr: new("10.0.0.0/16"),
				Ipv6Cidr: new("2001:db8::/32"),
				Region:   "region-a",
			}.Build()

			Expect(validator.Validate(spec)).To(MatchError(ContainSubstring("IPv6")))
		})
	})

	Describe("SubnetSpec", func() {
		It("accepts a canonical IPv4 CIDR", func() {
			spec := privatev1.SubnetSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vnet-a"}.Build(),
				Ipv4Cidr:       new("10.0.1.0/24"),
			}.Build()

			Expect(validator.Validate(spec)).To(Succeed())
		})

		It("rejects a missing IPv4 CIDR", func() {
			spec := privatev1.SubnetSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vnet-a"}.Build(),
			}.Build()

			Expect(validator.Validate(spec)).To(MatchError(ContainSubstring("ipv4_cidr")))
		})

		It("rejects an IPv4 CIDR with host bits", func() {
			spec := privatev1.SubnetSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vnet-a"}.Build(),
				Ipv4Cidr:       new("10.0.1.1/24"),
			}.Build()

			Expect(validator.Validate(spec)).To(MatchError(ContainSubstring("canonical")))
		})

		It("rejects IPv6-only and dual-stack requests", func() {
			ipv6Only := privatev1.SubnetSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vnet-a"}.Build(),
				Ipv6Cidr:       new("2001:db8:1::/64"),
			}.Build()
			dualStack := privatev1.SubnetSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vnet-a"}.Build(),
				Ipv4Cidr:       new("10.0.1.0/24"),
				Ipv6Cidr:       new("2001:db8:1::/64"),
			}.Build()

			Expect(validator.Validate(ipv6Only)).To(MatchError(ContainSubstring("ipv4_cidr")))
			Expect(validator.Validate(dualStack)).To(MatchError(ContainSubstring("IPv6")))
		})
	})

	Describe("SecurityRule", func() {
		It("accepts a canonical IPv4 CIDR", func() {
			rule := privatev1.SecurityRule_builder{
				Protocol: privatev1.Protocol_PROTOCOL_ALL,
				Ipv4Cidr: new("0.0.0.0/0"),
			}.Build()

			Expect(validator.Validate(rule)).To(Succeed())
		})

		It("rejects a missing IPv4 CIDR", func() {
			rule := privatev1.SecurityRule_builder{
				Protocol: privatev1.Protocol_PROTOCOL_ALL,
			}.Build()

			Expect(validator.Validate(rule)).To(MatchError(ContainSubstring("ipv4_cidr")))
		})

		It("rejects a host-bit IPv4 CIDR", func() {
			rule := privatev1.SecurityRule_builder{
				Protocol: privatev1.Protocol_PROTOCOL_ALL,
				Ipv4Cidr: new("192.168.1.7/24"),
			}.Build()

			Expect(validator.Validate(rule)).To(MatchError(ContainSubstring("canonical")))
		})

		It("rejects IPv6-only and dual-stack rules", func() {
			ipv6Only := privatev1.SecurityRule_builder{
				Protocol: privatev1.Protocol_PROTOCOL_ALL,
				Ipv6Cidr: new("2001:db8::/32"),
			}.Build()
			dualStack := privatev1.SecurityRule_builder{
				Protocol: privatev1.Protocol_PROTOCOL_ALL,
				Ipv4Cidr: new("0.0.0.0/0"),
				Ipv6Cidr: new("2001:db8::/32"),
			}.Build()

			Expect(validator.Validate(ipv6Only)).To(MatchError(ContainSubstring("ipv4_cidr")))
			Expect(validator.Validate(dualStack)).To(MatchError(ContainSubstring("IPv6")))
		})
	})

	Describe("ExternalIPPoolSpec", func() {
		It("accepts one canonical IPv4 CIDR and the IPv4 family", func() {
			spec := privatev1.ExternalIPPoolSpec_builder{
				Cidrs:    []string{"192.168.10.0/24"},
				IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
			}.Build()

			Expect(validator.Validate(spec)).To(Succeed())
		})

		It("rejects an unspecified family", func() {
			spec := privatev1.ExternalIPPoolSpec_builder{
				Cidrs: []string{"192.168.10.0/24"},
			}.Build()

			Expect(validator.Validate(spec)).To(MatchError(ContainSubstring("ip_family")))
		})

		It("rejects the IPv6 family", func() {
			spec := privatev1.ExternalIPPoolSpec_builder{
				Cidrs:    []string{"2001:db8::/64"},
				IpFamily: privatev1.IPFamily_IP_FAMILY_IPV6,
			}.Build()

			Expect(validator.Validate(spec)).To(MatchError(ContainSubstring("ip_family")))
		})

		It("rejects zero CIDRs", func() {
			spec := privatev1.ExternalIPPoolSpec_builder{
				IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
			}.Build()

			Expect(validator.Validate(spec)).To(MatchError(ContainSubstring("cidrs")))
		})

		It("rejects multiple CIDRs", func() {
			spec := privatev1.ExternalIPPoolSpec_builder{
				Cidrs:    []string{"192.168.10.0/24", "192.168.11.0/24"},
				IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
			}.Build()

			Expect(validator.Validate(spec)).To(MatchError(ContainSubstring("no more than 1")))
		})

		It("rejects a host-bit IPv4 CIDR", func() {
			spec := privatev1.ExternalIPPoolSpec_builder{
				Cidrs:    []string{"192.168.10.7/24"},
				IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
			}.Build()

			Expect(validator.Validate(spec)).To(MatchError(ContainSubstring("canonical")))
		})

		It("rejects an IPv6 CIDR in an IPv4 pool", func() {
			spec := privatev1.ExternalIPPoolSpec_builder{
				Cidrs:    []string{"2001:db8::/64"},
				IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
			}.Build()

			Expect(validator.Validate(spec)).To(MatchError(ContainSubstring("IPv4")))
		})
	})
})
