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

package helpers

import (
	"encoding/binary"
	"fmt"
	"net"
)

// ComputeVIPRange computes the VIP sub-range from a subnet CIDR and a VIP
// prefix length. The VIP range occupies the highest addresses of the subnet.
//
// Example: ComputeVIPRange("10.0.1.0/24", 28) returns "10.0.1.240-10.0.1.255".
func ComputeVIPRange(subnetCIDR string, vipPrefixLength int) (start, end net.IP, err error) {
	_, ipNet, err := net.ParseCIDR(subnetCIDR)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid subnet CIDR %q: %w", subnetCIDR, err)
	}

	subnetOnes, subnetBits := ipNet.Mask.Size()
	if subnetBits != 32 {
		return nil, nil, fmt.Errorf("only IPv4 CIDRs are supported, got %q", subnetCIDR)
	}

	if vipPrefixLength <= subnetOnes || vipPrefixLength > 32 {
		return nil, nil, fmt.Errorf(
			"VIP prefix length %d must be greater than subnet prefix %d and at most 32",
			vipPrefixLength, subnetOnes,
		)
	}

	subnetIP := binary.BigEndian.Uint32(ipNet.IP.To4())
	subnetSize := uint32(1) << (32 - subnetOnes)
	vipSize := uint32(1) << (32 - vipPrefixLength)

	vipStart := subnetIP + subnetSize - vipSize
	vipEnd := subnetIP + subnetSize - 1

	startIP := make(net.IP, 4)
	endIP := make(net.IP, 4)
	binary.BigEndian.PutUint32(startIP, vipStart)
	binary.BigEndian.PutUint32(endIP, vipEnd)

	return startIP, endIP, nil
}

// FormatVIPRangeCIDR returns the VIP sub-range as a CIDR string suitable for
// MetalLB IPAddressPool addresses.
func FormatVIPRangeCIDR(subnetCIDR string, vipPrefixLength int) (string, error) {
	start, _, err := ComputeVIPRange(subnetCIDR, vipPrefixLength)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/%d", start.String(), vipPrefixLength), nil
}

// FormatVIPRangeDash returns the VIP sub-range as a "start-end" string.
func FormatVIPRangeDash(subnetCIDR string, vipPrefixLength int) (string, error) {
	start, end, err := ComputeVIPRange(subnetCIDR, vipPrefixLength)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s", start.String(), end.String()), nil
}
