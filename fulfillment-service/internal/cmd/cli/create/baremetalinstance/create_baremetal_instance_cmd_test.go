/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalinstance

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
)

var _ = Describe("parseBareMetalNetworkAttachmentFlag", func() {
	DescribeTable("when input is valid it should parse",
		func(input, wantSubnet string, wantIface *string, wantPrimary *bool, wantSGs []string) {
			got, err := parseBareMetalNetworkAttachmentFlag(input)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.GetSubnet().GetId()).To(Equal(wantSubnet))
			if wantIface != nil {
				Expect(got.HasInterface()).To(BeTrue())
				Expect(got.GetInterface()).To(Equal(*wantIface))
			} else {
				Expect(got.HasInterface()).To(BeFalse())
			}
			if wantPrimary != nil {
				Expect(got.HasPrimary()).To(BeTrue())
				Expect(got.GetPrimary()).To(Equal(*wantPrimary))
			} else {
				Expect(got.HasPrimary()).To(BeFalse())
			}
			var gotSGs []string
			for _, sg := range got.GetSecurityGroups() {
				gotSGs = append(gotSGs, sg.GetId())
			}
			if wantSGs == nil {
				Expect(gotSGs).To(BeNil())
			} else {
				Expect(gotSGs).To(Equal(wantSGs))
			}
		},
		Entry("bare subnet id",
			"  sub-1  ", "sub-1", nil, nil, nil),
		Entry("subnet=key form",
			"subnet=sub-2", "sub-2", nil, nil, nil),
		Entry("subnet with security_groups alias",
			"subnet=a,security_groups=g1,g2", "a", nil, nil, []string{"g1", "g2"}),
		Entry("subnet with security-groups",
			"subnet=b,security-groups=x", "b", nil, nil, []string{"x"}),
		Entry("subnet with interface",
			"subnet=s1,interface=data-0", "s1", strPtr("data-0"), nil, nil),
		Entry("subnet with interface and primary",
			"subnet=s1,interface=data-0,primary", "s1", strPtr("data-0"), boolPtr(true), nil),
		Entry("all fields",
			"subnet=s1,interface=eth0,primary,security-groups=sg1,sg2", "s1", strPtr("eth0"), boolPtr(true), []string{"sg1", "sg2"}),
		Entry("order-independent keys",
			"interface=data-1,subnet=s2,primary", "s2", strPtr("data-1"), boolPtr(true), nil),
	)

	DescribeTable("when input is invalid it should error",
		func(input string) {
			_, err := parseBareMetalNetworkAttachmentFlag(input)
			Expect(err).To(HaveOccurred())
		},
		Entry("empty string", "   "),
		Entry("unknown key", "subnet=a,foo=bar"),
		Entry("missing subnet in key=value form", "interface=eth0"),
		Entry("empty value after equals", "subnet="),
		Entry("duplicate subnet", "subnet=a,subnet=b"),
		Entry("duplicate interface", "subnet=a,interface=x,interface=y"),
	)
})

var _ = Describe("applyNetworkingFlags", func() {
	It("should populate attachments when network-attachment flags are set", func() {
		c := &runnerContext{}
		c.args.networkAttachments = []string{
			"subnet=n1,interface=data-0,primary",
			"subnet=n2,interface=data-1,security-groups=g1",
		}
		spec := publicv1.BareMetalInstanceSpec_builder{}
		err := c.applyNetworkingFlags(&spec)
		Expect(err).NotTo(HaveOccurred())

		iface0 := "data-0"
		iface1 := "data-1"
		isPrimary := true
		want := publicv1.BareMetalInstanceSpec_builder{
			NetworkAttachments: []*publicv1.BareMetalNetworkAttachment{
				publicv1.BareMetalNetworkAttachment_builder{
					Subnet:    &publicv1.SubnetLocalReference{Id: "n1"},
					Interface: &iface0,
					Primary:   &isPrimary,
				}.Build(),
				publicv1.BareMetalNetworkAttachment_builder{
					Subnet:         &publicv1.SubnetLocalReference{Id: "n2"},
					Interface:      &iface1,
					SecurityGroups: []*publicv1.SecurityGroupLocalReference{{Id: "g1"}},
				}.Build(),
			},
		}.Build()
		Expect(proto.Equal(spec.Build(), want)).To(BeTrue(), "spec should equal expected spec")
	})

	It("should leave attachments nil when no network flags are set", func() {
		c := &runnerContext{}
		spec := publicv1.BareMetalInstanceSpec_builder{}
		err := c.applyNetworkingFlags(&spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(spec.Build().GetNetworkAttachments()).To(BeEmpty())
	})

	It("should return error when network-attachment value is invalid", func() {
		c := &runnerContext{}
		c.args.networkAttachments = []string{"subnet=a,foo=bar"}
		spec := publicv1.BareMetalInstanceSpec_builder{}
		err := c.applyNetworkingFlags(&spec)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Create baremetalinstance flag registration", func() {
	It("should register --network-attachment flag", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		flag := cmd.Flags().Lookup("network-attachment")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Usage).To(ContainSubstring("network attachment"))
	})
})

func strPtr(s string) *string {
	return &s
}

func boolPtr(b bool) *bool {
	return &b
}
