package validation

import (
	"fmt"
	"net/netip"
)

func ParseCanonicalIPv4CIDR(value string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid IPv4 CIDR format")
	}
	if !prefix.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("IPv6 address is not supported")
	}
	if prefix != prefix.Masked() {
		return netip.Prefix{}, fmt.Errorf("CIDR is not canonical")
	}
	return prefix, nil
}
