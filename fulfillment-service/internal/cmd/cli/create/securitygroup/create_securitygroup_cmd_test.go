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
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

var _ = Describe("parseSecurityRules", func() {
	DescribeTable("valid cases",
		func(ruleArgs []string, validate func([]*publicv1.SecurityRule)) {
			rules, err := parseSecurityRules(ruleArgs)
			Expect(err).NotTo(HaveOccurred())
			if validate != nil {
				validate(rules)
			}
		},
		Entry("empty input returns nil",
			[]string{},
			func(rules []*publicv1.SecurityRule) {
				Expect(rules).To(BeNil())
			},
		),
		Entry("single TCP rule with all fields",
			[]string{"protocol=tcp,port-from=80,port-to=80,ipv4-cidr=0.0.0.0/0"},
			func(rules []*publicv1.SecurityRule) {
				Expect(rules).To(HaveLen(1))
				Expect(rules[0].GetProtocol()).To(Equal(publicv1.Protocol_PROTOCOL_TCP))
				Expect(rules[0].HasPortFrom()).To(BeTrue())
				Expect(rules[0].GetPortFrom()).To(Equal(int32(80)))
				Expect(rules[0].HasPortTo()).To(BeTrue())
				Expect(rules[0].GetPortTo()).To(Equal(int32(80)))
				Expect(rules[0].GetIpv4Cidr()).To(Equal("0.0.0.0/0"))
			},
		),
		Entry("ICMP rule without ports",
			[]string{"protocol=icmp,ipv4-cidr=0.0.0.0/0"},
			func(rules []*publicv1.SecurityRule) {
				Expect(rules).To(HaveLen(1))
				Expect(rules[0].GetProtocol()).To(Equal(publicv1.Protocol_PROTOCOL_ICMP))
				Expect(rules[0].HasPortFrom()).To(BeFalse())
				Expect(rules[0].HasPortTo()).To(BeFalse())
				Expect(rules[0].GetIpv4Cidr()).To(Equal("0.0.0.0/0"))
			},
		),
		Entry("ALL traffic protocol",
			[]string{"protocol=all"},
			func(rules []*publicv1.SecurityRule) {
				Expect(rules).To(HaveLen(1))
				Expect(rules[0].GetProtocol()).To(Equal(publicv1.Protocol_PROTOCOL_ALL))
			},
		),
		Entry("protocol-only TCP rule",
			[]string{"protocol=tcp"},
			func(rules []*publicv1.SecurityRule) {
				Expect(rules).To(HaveLen(1))
				Expect(rules[0].GetProtocol()).To(Equal(publicv1.Protocol_PROTOCOL_TCP))
				Expect(rules[0].HasPortFrom()).To(BeFalse())
				Expect(rules[0].HasPortTo()).To(BeFalse())
			},
		),
		Entry("dual-stack rule with both IPv4 and IPv6 CIDRs",
			[]string{"protocol=tcp,port-from=443,port-to=443,ipv4-cidr=0.0.0.0/0,ipv6-cidr=::/0"},
			func(rules []*publicv1.SecurityRule) {
				Expect(rules).To(HaveLen(1))
				Expect(rules[0].GetIpv4Cidr()).To(Equal("0.0.0.0/0"))
				Expect(rules[0].GetIpv6Cidr()).To(Equal("::/0"))
			},
		),
		Entry("multiple rules",
			[]string{
				"protocol=tcp,port-from=80,port-to=80",
				"protocol=icmp",
			},
			func(rules []*publicv1.SecurityRule) {
				Expect(rules).To(HaveLen(2))
				Expect(rules[0].GetProtocol()).To(Equal(publicv1.Protocol_PROTOCOL_TCP))
				Expect(rules[1].GetProtocol()).To(Equal(publicv1.Protocol_PROTOCOL_ICMP))
			},
		),
		Entry("case-insensitive protocol TCP",
			[]string{"protocol=TCP"},
			func(rules []*publicv1.SecurityRule) {
				Expect(rules).To(HaveLen(1))
				Expect(rules[0].GetProtocol()).To(Equal(publicv1.Protocol_PROTOCOL_TCP))
			},
		),
		Entry("UDP protocol",
			[]string{"protocol=udp,port-from=53,port-to=53"},
			func(rules []*publicv1.SecurityRule) {
				Expect(rules).To(HaveLen(1))
				Expect(rules[0].GetProtocol()).To(Equal(publicv1.Protocol_PROTOCOL_UDP))
			},
		),
		Entry("port boundary: minimum port 1",
			[]string{"protocol=tcp,port-from=1,port-to=1"},
			func(rules []*publicv1.SecurityRule) {
				Expect(rules).To(HaveLen(1))
				Expect(rules[0].GetPortFrom()).To(Equal(int32(1)))
				Expect(rules[0].GetPortTo()).To(Equal(int32(1)))
			},
		),
		Entry("port boundary: maximum port 65535",
			[]string{"protocol=tcp,port-from=65535,port-to=65535"},
			func(rules []*publicv1.SecurityRule) {
				Expect(rules).To(HaveLen(1))
				Expect(rules[0].GetPortFrom()).To(Equal(int32(65535)))
				Expect(rules[0].GetPortTo()).To(Equal(int32(65535)))
			},
		),
	)

	DescribeTable("error cases",
		func(ruleArgs []string, errMatcher OmegaMatcher) {
			_, err := parseSecurityRules(ruleArgs)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(errMatcher)
		},
		Entry("missing protocol",
			[]string{"port-from=80,port-to=80"},
			ContainSubstring("protocol"),
		),
		Entry("invalid protocol value",
			[]string{"protocol=ftp"},
			ContainSubstring("invalid protocol"),
		),
		Entry("port below range (port-from=0)",
			[]string{"protocol=tcp,port-from=0,port-to=80"},
			ContainSubstring("out of range"),
		),
		Entry("port above range (port-from=65536)",
			[]string{"protocol=tcp,port-from=65536,port-to=65536"},
			ContainSubstring("out of range"),
		),
		Entry("unknown key",
			[]string{"protocol=tcp,foo=bar"},
			ContainSubstring("foo"),
		),
		Entry("half-specified ports: port-from without port-to",
			[]string{"protocol=tcp,port-from=80"},
			ContainSubstring("both be specified or both be omitted"),
		),
		Entry("half-specified ports: port-to without port-from",
			[]string{"protocol=tcp,port-to=80"},
			ContainSubstring("both be specified or both be omitted"),
		),
		Entry("reversed ports (port-from > port-to)",
			[]string{"protocol=tcp,port-from=443,port-to=80"},
			ContainSubstring("less than or equal to"),
		),
		Entry("duplicate keys",
			[]string{"protocol=tcp,protocol=udp"},
			ContainSubstring("duplicate key"),
		),
		Entry("invalid CIDR notation",
			[]string{"protocol=tcp,ipv4-cidr=not-a-cidr"},
			ContainSubstring("invalid ipv4-cidr"),
		),
		Entry("IPv4 address in ipv6-cidr field",
			[]string{"protocol=tcp,ipv6-cidr=192.168.0.0/16"},
			ContainSubstring("not IPv6"),
		),
		Entry("IPv6 address in ipv4-cidr field",
			[]string{"protocol=tcp,ipv4-cidr=::/0"},
			ContainSubstring("not IPv4"),
		),
		Entry("no equals sign in pair",
			[]string{"protocol=tcp,nodash"},
			ContainSubstring("invalid key=value pair"),
		),
	)
})
