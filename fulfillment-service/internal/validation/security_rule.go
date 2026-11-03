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
	"fmt"
	"strings"
)

type SecurityRuleProtocol int32

const (
	SecurityRuleProtocolUnspecified SecurityRuleProtocol = 0
	SecurityRuleProtocolTCP         SecurityRuleProtocol = 1
	SecurityRuleProtocolUDP         SecurityRuleProtocol = 2
	SecurityRuleProtocolICMP        SecurityRuleProtocol = 3
	SecurityRuleProtocolAll         SecurityRuleProtocol = 4
)

type SecurityRule struct {
	Protocol    SecurityRuleProtocol
	PortFrom    int32
	PortTo      int32
	HasPortFrom bool
	HasPortTo   bool
	IPv4CIDR    string
}

func ParseSecurityRuleProtocol(value string) (SecurityRuleProtocol, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "tcp":
		return SecurityRuleProtocolTCP, nil
	case "udp":
		return SecurityRuleProtocolUDP, nil
	case "icmp":
		return SecurityRuleProtocolICMP, nil
	case "all":
		return SecurityRuleProtocolAll, nil
	default:
		return SecurityRuleProtocolUnspecified, fmt.Errorf(
			"protocol must be tcp, udp, icmp, or all",
		)
	}
}

func ValidateSecurityRule(rule SecurityRule, field string, index int) error {
	switch rule.Protocol {
	case SecurityRuleProtocolTCP, SecurityRuleProtocolUDP:
		if !rule.HasPortFrom || !rule.HasPortTo {
			return fmt.Errorf(
				"%s: tcp and udp rules require port_from and port_to",
				securityRuleField(field, index),
			)
		}
		if rule.PortFrom < 1 || rule.PortFrom > 65535 {
			return fmt.Errorf(
				"%s: port_from must be between 1 and 65535",
				securityRuleField(field, index),
			)
		}
		if rule.PortTo < 1 || rule.PortTo > 65535 {
			return fmt.Errorf(
				"%s: port_to must be between 1 and 65535",
				securityRuleField(field, index),
			)
		}
		if rule.PortFrom > rule.PortTo {
			return fmt.Errorf(
				"%s: port_from (%d) cannot be greater than port_to (%d)",
				securityRuleField(field, index), rule.PortFrom, rule.PortTo,
			)
		}
	case SecurityRuleProtocolICMP, SecurityRuleProtocolAll:
		if rule.HasPortFrom || rule.HasPortTo {
			return fmt.Errorf(
				"%s: icmp and all rules must not set port_from or port_to",
				securityRuleField(field, index),
			)
		}
	default:
		return fmt.Errorf(
			"%s: protocol is required",
			securityRuleField(field, index),
		)
	}

	return ValidateCanonicalIPv4CIDR(
		securityRuleField(field, index)+".ipv4_cidr",
		rule.IPv4CIDR,
	)
}

func securityRuleField(field string, index int) string {
	return fmt.Sprintf("%s[%d]", field, index)
}

func ValidateCanonicalIPv4CIDR(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required and must be a canonical IPv4 CIDR", field)
	}
	if _, err := ParseCanonicalIPv4CIDR(value); err != nil {
		return fmt.Errorf("%s must be a canonical IPv4 CIDR with no host bits set", field)
	}
	return nil
}
