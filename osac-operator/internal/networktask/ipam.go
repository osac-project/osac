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

package networktask

import (
	"fmt"
	"math/bits"
	"net/netip"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// Layout computes a Subnet's address layout from its CIDR and address plan
// (contract §4.8): the ranges overlay, fabric and lbVip follow each other from
// the network address, each the fewest aligned CIDRs covering it exactly, in
// ascending order; a share of 0 gives an empty list; the gateway is the
// network address plus one. Compute it once at Subnet create and record it in
// status.ipam; never recompute it.
func Layout(cidr string, plan v1alpha1.SubnetAddressPlan) (*v1alpha1.SubnetIPAM, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil || !prefix.Addr().Is4() || prefix.Masked() != prefix {
		return nil, fmt.Errorf("subnet CIDR %q is not a canonical IPv4 CIDR", cidr)
	}
	shares := []int32{plan.Overlay, plan.Fabric, plan.LBVIP}
	sum := int32(0)
	for _, share := range shares {
		if share < 0 || share > 16 {
			return nil, fmt.Errorf("address plan share %d is outside 0..16 sixteenths", share)
		}
		sum += share
	}
	if sum != 16 {
		return nil, fmt.Errorf("address plan shares sum to %d sixteenths, not 16", sum)
	}
	if prefix.Bits() > 28 {
		return nil, fmt.Errorf("subnet CIDR %s is too small for an address plan in sixteenths", cidr)
	}
	size := uint64(1) << (32 - prefix.Bits())
	sixteenth := size / 16
	base := uint64(addrToUint32(prefix.Addr()))
	lists := make([][]string, 3)
	start := base
	for i, share := range shares {
		end := start + uint64(share)*sixteenth
		lists[i] = coverRange(start, end)
		start = end
	}
	return &v1alpha1.SubnetIPAM{
		GatewayIPv4:       uint32ToAddr(uint32(base + 1)).String(),
		OverlayIPv4CIDRs:  lists[0],
		PhysicalIPv4CIDRs: lists[1],
		LBVIPIPv4CIDRs:    lists[2],
	}, nil
}

// coverRange returns the fewest aligned CIDRs covering [start, end), ascending.
func coverRange(start, end uint64) []string {
	result := []string{}
	for start < end {
		// largest block aligned at start
		size := uint64(1) << 32
		if start != 0 {
			size = uint64(1) << bits.TrailingZeros64(start)
		}
		for start+size > end {
			size >>= 1
		}
		prefixLen := 32 - (bits.Len64(size) - 1)
		result = append(result, netip.PrefixFrom(uint32ToAddr(uint32(start)), prefixLen).String())
		start += size
	}
	return result
}

func addrToUint32(addr netip.Addr) uint32 {
	b := addr.As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func uint32ToAddr(v uint32) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// IPAMVars is the snake_case ipam key of the task input; empty ranges are [].
func IPAMVars(ipam *v1alpha1.SubnetIPAM) map[string]any {
	list := func(values []string) []any {
		out := make([]any, 0, len(values))
		for _, v := range values {
			out = append(out, v)
		}
		return out
	}
	if ipam == nil {
		return nil
	}
	return map[string]any{
		"gateway_ipv4":        ipam.GatewayIPv4,
		"overlay_ipv4_cidrs":  list(ipam.OverlayIPv4CIDRs),
		"physical_ipv4_cidrs": list(ipam.PhysicalIPv4CIDRs),
		"lb_vip_ipv4_cidrs":   list(ipam.LBVIPIPv4CIDRs),
	}
}

// inCIDRs reports whether addr is inside one of the CIDRs.
func inCIDRs(addr netip.Addr, cidrs []string) bool {
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err == nil && p.Contains(addr) {
			return true
		}
	}
	return false
}

// networkAndBroadcast returns the network and broadcast addresses of an IPv4 CIDR.
func networkAndBroadcast(cidr string) (netip.Addr, netip.Addr, error) {
	p, err := netip.ParsePrefix(cidr)
	if err != nil || !p.Addr().Is4() {
		return netip.Addr{}, netip.Addr{}, fmt.Errorf("%q is not an IPv4 CIDR", cidr)
	}
	p = p.Masked()
	base := addrToUint32(p.Addr())
	last := base | (uint32(0xffffffff) >> p.Bits())
	if p.Bits() == 0 {
		last = 0xffffffff
	}
	return p.Addr(), uint32ToAddr(last), nil
}
