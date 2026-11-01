/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controllers

import (
	"fmt"
	"strings"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// ConvertSecurityRules converts API security rules to hub CR rules. The single IPv4 CIDR of an API rule is the source
// of an ingress rule and the destination of an egress rule.
func ConvertSecurityRules(rules []*privatev1.SecurityRule, ingress bool) ([]osacv1alpha1.SecurityRule, error) {
	result := make([]osacv1alpha1.SecurityRule, 0, len(rules))
	direction := "egress"
	if ingress {
		direction = "ingress"
	}
	for index, rule := range rules {
		if rule == nil || !rule.HasIpv4Cidr() || rule.GetIpv4Cidr() == "" {
			return nil, fmt.Errorf("security group %s rule %d must contain an IPv4 CIDR", direction, index)
		}
		r := osacv1alpha1.SecurityRule{
			Protocol: osacv1alpha1.SecurityGroupProtocol(ProtocolName(rule.GetProtocol())),
		}
		if rule.HasPortFrom() {
			portFrom := rule.GetPortFrom()
			r.PortFrom = &portFrom
		}
		if rule.HasPortTo() {
			portTo := rule.GetPortTo()
			r.PortTo = &portTo
		}
		if ingress {
			r.SourceCIDR = rule.GetIpv4Cidr()
		} else {
			r.DestinationCIDR = rule.GetIpv4Cidr()
		}
		result = append(result, r)
	}
	return result, nil
}

// ProtocolName returns the hub CR name of a protocol.
func ProtocolName(p privatev1.Protocol) string {
	switch p {
	case privatev1.Protocol_PROTOCOL_TCP:
		return "tcp"
	case privatev1.Protocol_PROTOCOL_UDP:
		return "udp"
	case privatev1.Protocol_PROTOCOL_ICMP:
		return "icmp"
	case privatev1.Protocol_PROTOCOL_ALL:
		return "all"
	default:
		return strings.ToLower(p.String())
	}
}
