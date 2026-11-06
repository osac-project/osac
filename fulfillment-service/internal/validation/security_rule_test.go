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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Security rule validation", func() {
	DescribeTable("validates the shared field contract",
		func(rule SecurityRule, expectedError string) {
			err := ValidateSecurityRule(rule, "ingress_rules", 0)
			if expectedError == "" {
				Expect(err).NotTo(HaveOccurred())
				return
			}
			Expect(err).To(MatchError(ContainSubstring(expectedError)))
		},
		Entry("accepts TCP with a valid range", SecurityRule{
			Protocol: SecurityRuleProtocolTCP, PortFrom: 22, PortTo: 80,
			HasPortFrom: true, HasPortTo: true,
			IPv4CIDR: "192.168.0.0/24",
		}, ""),
		Entry("rejects an unsupported protocol", SecurityRule{
			Protocol: SecurityRuleProtocolUnspecified, IPv4CIDR: "0.0.0.0/0",
		}, "protocol is required"),
		Entry("rejects missing TCP ports", SecurityRule{
			Protocol: SecurityRuleProtocolTCP, IPv4CIDR: "0.0.0.0/0",
		}, "require port_from and port_to"),
		Entry("rejects ports on ICMP", SecurityRule{
			Protocol: SecurityRuleProtocolICMP, PortFrom: 1, PortTo: 1,
			HasPortFrom: true, HasPortTo: true,
			IPv4CIDR: "0.0.0.0/0",
		}, "must not set port_from or port_to"),
		Entry("rejects a reversed port range", SecurityRule{
			Protocol: SecurityRuleProtocolTCP, PortFrom: 100, PortTo: 22,
			HasPortFrom: true, HasPortTo: true,
			IPv4CIDR: "0.0.0.0/0",
		}, "cannot be greater than port_to"),
		Entry("rejects a non-canonical CIDR", SecurityRule{
			Protocol: SecurityRuleProtocolAll, IPv4CIDR: "192.168.0.1/24",
		}, "canonical IPv4 CIDR"),
	)

	It("parses supported protocol names", func() {
		protocol, err := ParseSecurityRuleProtocol(" TCP ")
		Expect(err).NotTo(HaveOccurred())
		Expect(protocol).To(Equal(SecurityRuleProtocolTCP))
	})
})
