package servers

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"sort"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var errClusterIngressAllocationExhausted = errors.New("subnet load-balancer VIP ranges are exhausted")

// selectClusterIngressAddress selects the first unused eligible address in
// ascending order from the Subnet's committed LB VIP ranges.
func selectClusterIngressAddress(
	subnetCIDR string,
	ipam *privatev1.SubnetIPAM,
	used map[string]struct{},
) (string, error) {
	if ipam == nil {
		return "", fmt.Errorf("subnet has no recorded IPAM layout")
	}
	subnet, err := parseCanonicalIPv4Prefix(subnetCIDR, "Subnet CIDR")
	if err != nil {
		return "", err
	}
	if len(ipam.GetLbVipIpv4Cidrs()) == 0 {
		return "", fmt.Errorf("subnet has no recorded load-balancer VIP range")
	}

	reserved := make([]netip.Prefix, 0,
		1+len(ipam.GetOverlayIpv4Cidrs())+len(ipam.GetPhysicalIpv4Cidrs()))
	reserved = append(reserved, netip.PrefixFrom(subnet.Addr(), 32))
	reserved = append(reserved, netip.PrefixFrom(lastIPv4Address(subnet), 32))

	gateway, err := netip.ParseAddr(ipam.GetGatewayIpv4())
	if err != nil || !gateway.Is4() {
		return "", fmt.Errorf("subnet IPAM has an invalid IPv4 gateway")
	}
	reserved = append(reserved, netip.PrefixFrom(gateway, 32))
	for _, field := range []struct {
		name   string
		values []string
	}{
		{name: "overlay_ipv4_cidrs", values: ipam.GetOverlayIpv4Cidrs()},
		{name: "physical_ipv4_cidrs", values: ipam.GetPhysicalIpv4Cidrs()},
	} {
		ranges, parseErr := parseSubnetIPv4Ranges(field.name, field.values, subnet)
		if parseErr != nil {
			return "", parseErr
		}
		reserved = append(reserved, ranges...)
	}

	lbRanges, err := parseSubnetIPv4Ranges("lb_vip_ipv4_cidrs", ipam.GetLbVipIpv4Cidrs(), subnet)
	if err != nil {
		return "", err
	}
	sort.Slice(lbRanges, func(i, j int) bool {
		return lbRanges[i].Addr().Compare(lbRanges[j].Addr()) < 0
	})

	for _, addressRange := range lbRanges {
		last := lastIPv4Address(addressRange)
		for address := addressRange.Addr(); address.IsValid() && address.Compare(last) <= 0; address = address.Next() {
			if _, exists := used[address.String()]; !exists && !addressInRanges(address, reserved) {
				return address.String(), nil
			}
			if address == last {
				break
			}
		}
	}
	return "", errClusterIngressAllocationExhausted
}

func parseCanonicalIPv4Prefix(value, name string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(value)
	if err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() {
		return netip.Prefix{}, fmt.Errorf("%s is not a canonical IPv4 CIDR", name)
	}
	return prefix, nil
}

func parseSubnetIPv4Ranges(name string, values []string, subnet netip.Prefix) ([]netip.Prefix, error) {
	ranges := make([]netip.Prefix, 0, len(values))
	for i, value := range values {
		prefix, err := parseCanonicalIPv4Prefix(value, fmt.Sprintf("Subnet IPAM %s[%d]", name, i))
		if err != nil {
			return nil, err
		}
		if !subnet.Contains(prefix.Addr()) || !subnet.Contains(lastIPv4Address(prefix)) {
			return nil, fmt.Errorf("subnet IPAM %s[%d] is outside the subnet CIDR", name, i)
		}
		ranges = append(ranges, prefix)
	}
	return ranges, nil
}

func lastIPv4Address(prefix netip.Prefix) netip.Addr {
	address := prefix.Masked().Addr().As4()
	value := binary.BigEndian.Uint32(address[:])
	hostBits := uint(32 - prefix.Bits())
	if hostBits > 0 {
		if hostBits == 32 {
			value = ^uint32(0)
		} else {
			value |= (uint32(1) << hostBits) - 1
		}
	}
	var last [4]byte
	binary.BigEndian.PutUint32(last[:], value)
	return netip.AddrFrom4(last)
}

func addressInRanges(address netip.Addr, ranges []netip.Prefix) bool {
	for _, prefix := range ranges {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
