/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"fmt"
	"net/netip"
)

func validateAllocatedAddress(address string, poolCIDRs []string) error {
	parsedAddress, err := netip.ParseAddr(address)
	if err != nil {
		return fmt.Errorf("allocated address %q is invalid: %w", address, err)
	}
	if !parsedAddress.Is4() || parsedAddress.String() != address {
		return fmt.Errorf("allocated address %q must be canonical IPv4 text", address)
	}
	if len(poolCIDRs) == 0 {
		return fmt.Errorf("ExternalIPPool has no CIDRs")
	}

	contained := false
	for _, cidr := range poolCIDRs {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return fmt.Errorf("ExternalIPPool CIDR %q is invalid: %w", cidr, err)
		}
		if !prefix.Addr().Is4() {
			return fmt.Errorf("ExternalIPPool CIDR %q is not IPv4", cidr)
		}
		if prefix.Contains(parsedAddress) {
			contained = true
		}
	}
	if !contained {
		return fmt.Errorf("allocated address %q is outside ExternalIPPool CIDRs %v", address, poolCIDRs)
	}
	return nil
}
