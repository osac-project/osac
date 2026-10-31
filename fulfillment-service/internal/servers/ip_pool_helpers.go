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
	"net/netip"
	"sort"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func validatePoolCIDRFormat(cidrStr string, ipFamily privatev1.IPFamily, idx int) (string, error) {
	if ipFamily != privatev1.IPFamily_IP_FAMILY_IPV4 {
		return "", grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.ip_family' must be IP_FAMILY_IPV4; IPv6 and unspecified-family pools are not supported")
	}

	prefix, err := netip.ParsePrefix(cidrStr)
	if err != nil {
		return "", grpcstatus.Errorf(grpccodes.InvalidArgument,
			"invalid CIDR format in field 'spec.cidrs[%d]': '%s': %v", idx, cidrStr, err)
	}

	isIPv4 := prefix.Addr().Is4()
	if !isIPv4 {
		return "", grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.cidrs[%d]' must be a canonical IPv4 CIDR; IPv6 address is not supported: %s", idx, cidrStr)
	}
	canonical := prefix.Masked().String()
	if canonical != cidrStr {
		return "", grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.cidrs[%d]' CIDR '%s' is not canonical; use '%s'", idx, cidrStr, canonical)
	}

	return canonical, nil
}

func validateSingleIPv4PoolCIDR(cidrs []string) ([]string, error) {
	if len(cidrs) != 1 {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.cidrs' must contain exactly one canonical IPv4 CIDR")
	}

	canonical, err := validatePoolCIDRFormat(cidrs[0], privatev1.IPFamily_IP_FAMILY_IPV4, 0)
	if err != nil {
		return nil, err
	}
	return []string{canonical}, nil
}

// calculatePoolCapacity returns the usable addresses of the IPv4 CIDRs of a pool.
func calculatePoolCapacity(cidrs []string) int64 {
	var total int64
	for _, cidr := range cidrs {
		cap := calculateCIDRCapacity(cidr)
		if total > math.MaxInt64-cap {
			return math.MaxInt64
		}
		total += cap
	}
	return total
}

// calculateCIDRCapacity returns the usable addresses of an IPv4 CIDR: all but the network and broadcast addresses,
// except /31 and /32. Pools are IPv4 only, so anything else counts 0.
func calculateCIDRCapacity(cidrStr string) int64 {
	prefix, err := netip.ParsePrefix(cidrStr)
	if err != nil || !prefix.Addr().Is4() {
		return 0
	}
	hostBits := 32 - prefix.Bits()
	total := int64(1) << hostBits
	if hostBits >= 2 {
		return total - 2 // subtract network and broadcast
	}
	return total // /31 = 2 (point-to-point), /32 = 1 (host route)
}

func cidrSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	canonA := canonicalizeCIDRs(a)
	canonB := canonicalizeCIDRs(b)
	sort.Strings(canonA)
	sort.Strings(canonB)
	for i := range canonA {
		if canonA[i] != canonB[i] {
			return false
		}
	}
	return true
}

func canonicalizeCIDRs(cidrs []string) []string {
	result := make([]string, len(cidrs))
	for i, cidr := range cidrs {
		p, err := netip.ParsePrefix(cidr)
		if err != nil {
			result[i] = cidr
			continue
		}
		result[i] = p.Masked().String()
	}
	return result
}
