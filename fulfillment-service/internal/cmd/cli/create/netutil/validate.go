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

// ValidateIPv4CIDR checks that an IPv4 CIDR is provided and valid.
func ValidateIPv4CIDR(ipv4Cidr string) error {
	if ipv4Cidr == "" {
		return fmt.Errorf("--ipv4-cidr is required")
	}
	prefix, err := netip.ParsePrefix(ipv4Cidr)
	if err != nil || !prefix.Addr().Is4() {
		if err == nil {
			err = fmt.Errorf("address is not IPv4")
		}
		return fmt.Errorf("invalid IPv4 CIDR %q: %w", ipv4Cidr, err)
	}
	return nil
}
