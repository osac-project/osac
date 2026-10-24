package provisioning

import (
	"encoding/json"
	"fmt"
	"net/netip"
)

// ValidateExternalIPResult checks the v1 AAP result identity and operation data.
func ValidateExternalIPResult(
	outputs map[string]any,
	operation string,
	resourceUID string,
	generation int64,
) (string, error) {
	data, err := validateAAPResultEnvelope(
		outputs, externalIPResultKey, "ExternalIP", operation, resourceUID, generation,
	)
	if err != nil {
		return "", err
	}
	switch operation {
	case "external_ip.allocate":
		encodedAddress, ok := data["address"]
		if !ok {
			return "", fmt.Errorf("ExternalIP allocation result has no address")
		}
		var address string
		if err := json.Unmarshal(encodedAddress, &address); err != nil || address == "" {
			return "", fmt.Errorf("ExternalIP allocation result address is invalid")
		}
		return address, nil
	case "external_ip.release":
		if len(data) != 0 {
			return "", fmt.Errorf("ExternalIP release result data must be empty")
		}
		return "", nil
	default:
		return "", fmt.Errorf("unsupported ExternalIP operation %q", operation)
	}
}

// ValidateExternalIPPoolCIDR validates the IPv4 range accepted by the ExternalIP contract.
func ValidateExternalIPPoolCIDR(cidrs []string) (string, error) {
	if len(cidrs) != 1 {
		return "", fmt.Errorf("ExternalIPPool must contain exactly one IPv4 CIDR")
	}
	prefix, err := netip.ParsePrefix(cidrs[0])
	if err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() || prefix.String() != cidrs[0] {
		return "", fmt.Errorf("ExternalIPPool CIDR must be canonical IPv4")
	}
	return prefix.String(), nil
}

// ValidateExternalIPAddress validates a canonical IPv4 address within its pool.
func ValidateExternalIPAddress(address string, poolCIDR string) (string, error) {
	parsedAddress, err := netip.ParseAddr(address)
	if err != nil || !parsedAddress.Is4() || parsedAddress.String() != address || parsedAddress.IsUnspecified() {
		return "", fmt.Errorf("ExternalIP address must be canonical IPv4")
	}
	cidr, err := ValidateExternalIPPoolCIDR([]string{poolCIDR})
	if err != nil {
		return "", err
	}
	pool, _ := netip.ParsePrefix(cidr)
	if !pool.Contains(parsedAddress) {
		return "", fmt.Errorf("ExternalIP address is outside the selected pool")
	}
	return address, nil
}
