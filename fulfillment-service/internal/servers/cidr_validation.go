/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"math"
	"math/bits"
	"net/netip"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// cidrIPv4 names the IP version in CIDR error messages. The networking API is IPv4 only.
const cidrIPv4 = "IPv4"

// parseAndValidateCIDR parses cidrStr, validates it matches the expected IP version,
// and returns the canonical CIDR notation.
func parseAndValidateCIDR(cidrStr string, ipVersion string) (string, error) {
	prefix, err := netip.ParsePrefix(cidrStr)
	if err != nil {
		return "", grpcstatus.Errorf(grpccodes.InvalidArgument,
			"invalid %s CIDR format '%s': %v", ipVersion, cidrStr, err)
	}

	if !prefix.Addr().Is4() {
		return "", grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'ipv4_cidr' contains IPv6 address: %s", cidrStr)
	}

	return prefix.Masked().String(), nil
}

// parseAndValidateCanonicalCIDR validates a CIDR without silently changing the
// address supplied by the caller. Networking API contracts use canonical
// prefixes so that the persisted value and the request value are identical.
func parseAndValidateCanonicalCIDR(cidrStr string, ipVersion string) (string, error) {
	canonical, err := parseAndValidateCIDR(cidrStr, ipVersion)
	if err != nil {
		return "", err
	}
	if canonical != cidrStr {
		return "", grpcstatus.Errorf(grpccodes.InvalidArgument,
			"CIDR '%s' is not canonical; use '%s'", cidrStr, canonical)
	}
	return canonical, nil
}

func validateCanonicalIPv4Address(fieldPath, address string) error {
	if address == "" {
		return nil
	}

	parsed, err := netip.ParseAddr(address)
	if err != nil || !parsed.Is4() || parsed.String() != address {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field '%s' must be a canonical IPv4 address without a CIDR prefix; got '%s'", fieldPath, address)
	}
	return nil
}

// cidrPrefixesEqual reports whether two CIDR strings denote the same network prefix.
func cidrPrefixesEqual(a, b string) (bool, error) {
	if a == b {
		return true, nil
	}
	prefixA, err := netip.ParsePrefix(a)
	if err != nil {
		return false, err
	}
	prefixB, err := netip.ParsePrefix(b)
	if err != nil {
		return false, err
	}
	return prefixA.Masked() == prefixB.Masked(), nil
}

// validateImmutableCIDR rejects updates that change the network prefix or
// provide a non-canonical replacement. The existing value is normalized only
// for comparison and error reporting because legacy records may predate the
// strict input contract.
func validateImmutableCIDR(fieldName, existingCIDR, newCIDR, ipVersion string) error {
	if newCIDR == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field '%s' is immutable and cannot be cleared", fieldName)
	}
	canonicalNew, err := parseAndValidateCanonicalCIDR(newCIDR, ipVersion)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field '%s': %v", fieldName, err)
	}

	equal, err := cidrPrefixesEqual(existingCIDR, newCIDR)
	if err == nil && equal {
		return nil
	}
	from := existingCIDR
	if existingCIDR != "" {
		if canonical, err := parseAndValidateCIDR(existingCIDR, ipVersion); err == nil {
			from = canonical
		}
	}
	return grpcstatus.Errorf(grpccodes.InvalidArgument,
		"field '%s' is immutable and cannot be changed from '%s' to '%s'",
		fieldName, from, canonicalNew)
}

// addressPlanSixteenths converts the shares of an address plan to sixteenths. Each share must be a multiple of 1/16
// from 0 to 1, and the shares must sum to exactly 1.
func addressPlanSixteenths(plan *privatev1.AddressPlan) (overlay, fabric, lbVip int, err error) {
	if plan == nil {
		err = grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.address_plan' is required")
		return
	}
	convert := func(name string, value float64) (int, error) {
		scaled := value * 16
		if math.IsNaN(scaled) || scaled < 0 || scaled > 16 || scaled != math.Trunc(scaled) {
			return 0, grpcstatus.Errorf(grpccodes.InvalidArgument,
				"field 'spec.address_plan.%s' must be a multiple of 0.0625 from 0 to 1", name)
		}
		return int(scaled), nil
	}
	if overlay, err = convert("overlay", plan.GetOverlay()); err != nil {
		return
	}
	if fabric, err = convert("fabric", plan.GetFabric()); err != nil {
		return
	}
	if lbVip, err = convert("lb_vip", plan.GetLbVip()); err != nil {
		return
	}
	if overlay+fabric+lbVip != 16 {
		err = grpcstatus.Error(grpccodes.InvalidArgument,
			"fields 'spec.address_plan.overlay', 'fabric' and 'lb_vip' must sum to exactly 1")
	}
	return
}

// addressPlanPrefixLimit returns the longest prefix allowed for a VirtualNetwork or Subnet with the given plan: every
// range of the layout holds at least 16 addresses. With g the largest power of two, at most 1, that divides every
// non-zero share, the limit is 28 - log2(1/g).
func addressPlanPrefixLimit(plan *privatev1.AddressPlan) (int, error) {
	overlay, fabric, lbVip, err := addressPlanSixteenths(plan)
	if err != nil {
		return 0, err
	}
	// granule is g in sixteenths: 16, 8, 4, 2 or 1.
	granule := 16
	for _, share := range []int{overlay, fabric, lbVip} {
		for share != 0 && share%granule != 0 {
			granule /= 2
		}
	}
	return 28 - bits.Len(uint(16/granule)) + 1, nil
}
