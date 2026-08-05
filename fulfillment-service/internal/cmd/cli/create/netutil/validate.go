/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package netutil

import (
	"fmt"
	"net/netip"
)

// ValidateVirtualNetwork checks that a virtual network ID is provided.
func ValidateVirtualNetwork(virtualNetwork string) error {
	if virtualNetwork == "" {
		return fmt.Errorf("virtual network is required")
	}
	return nil
}

// ValidateCIDRs checks that at least one CIDR is provided and that each is valid for its address family.
func ValidateCIDRs(ipv4Cidr, ipv6Cidr string) error {
	if ipv4Cidr == "" && ipv6Cidr == "" {
		return fmt.Errorf("at least one of --ipv4-cidr or --ipv6-cidr is required")
	}
	if ipv4Cidr != "" {
		prefix, err := netip.ParsePrefix(ipv4Cidr)
		if err != nil || !prefix.Addr().Is4() {
			if err == nil {
				err = fmt.Errorf("address is not IPv4")
			}
			return fmt.Errorf("invalid IPv4 CIDR %q: %w", ipv4Cidr, err)
		}
	}
	if ipv6Cidr != "" {
		prefix, err := netip.ParsePrefix(ipv6Cidr)
		if err != nil || !prefix.Addr().Is6() {
			if err == nil {
				err = fmt.Errorf("address is not IPv6")
			}
			return fmt.Errorf("invalid IPv6 CIDR %q: %w", ipv6Cidr, err)
		}
	}
	return nil
}
