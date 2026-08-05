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
	"net/netip"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

const (
	cidrIPv4 = "IPv4"
	cidrIPv6 = "IPv6"
)

// parseAndValidateCIDR parses cidrStr, validates it matches the expected IP version,
// and returns the canonical CIDR notation.
func parseAndValidateCIDR(cidrStr string, ipVersion string) (string, error) {
	prefix, err := netip.ParsePrefix(cidrStr)
	if err != nil {
		return "", grpcstatus.Errorf(grpccodes.InvalidArgument,
			"invalid %s CIDR format '%s': %v", ipVersion, cidrStr, err)
	}

	isIPv4 := prefix.Addr().Is4()
	if ipVersion == cidrIPv4 && !isIPv4 {
		return "", grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'ipv4_cidr' contains IPv6 address: %s", cidrStr)
	}
	if ipVersion == cidrIPv6 && isIPv4 {
		return "", grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'ipv6_cidr' contains IPv4 address: %s", cidrStr)
	}

	return prefix.Masked().String(), nil
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

// validateImmutableCIDR rejects updates that change the network prefix; canonically equivalent
// notation (e.g. 10.0.1.5/24 vs 10.0.1.0/24) is allowed.
func validateImmutableCIDR(fieldName, existingCIDR, newCIDR, ipVersion string) error {
	equal, err := cidrPrefixesEqual(existingCIDR, newCIDR)
	if err == nil && equal {
		return nil
	}
	from, to := existingCIDR, newCIDR
	if existingCIDR != "" {
		if canonical, err := parseAndValidateCIDR(existingCIDR, ipVersion); err == nil {
			from = canonical
		}
	}
	if canonical, err := parseAndValidateCIDR(newCIDR, ipVersion); err == nil {
		to = canonical
	}
	return grpcstatus.Errorf(grpccodes.InvalidArgument,
		"field '%s' is immutable and cannot be changed from '%s' to '%s'",
		fieldName, from, to)
}

// immutableCIDRField groups optional CIDR field accessors for preserve-and-validate on Update.
type immutableCIDRField struct {
	fieldName       string
	ipVersion       string
	existingHasCIDR func() bool
	existingCIDR    func() string
	newHasCIDR      func() bool
	newCIDR         func() string
	setNewCIDR      func(string)
}

func (f immutableCIDRField) preserveAndValidate() error {
	if f.existingHasCIDR() && !f.newHasCIDR() {
		f.setNewCIDR(f.existingCIDR())
	}
	if !f.newHasCIDR() {
		return nil
	}
	existing := f.existingCIDR()
	if !f.existingHasCIDR() {
		existing = ""
	}
	return validateImmutableCIDR(f.fieldName, existing, f.newCIDR(), f.ipVersion)
}

// canonicalizeDualStackCIDRs validates and persists masked IPv4/IPv6 CIDRs when present.
func canonicalizeDualStackCIDRs(
	ipv4 func() string, setIPv4 func(string),
	ipv6 func() string, setIPv6 func(string),
) error {
	if cidr := ipv4(); cidr != "" {
		canonical, err := parseAndValidateCIDR(cidr, cidrIPv4)
		if err != nil {
			return err
		}
		setIPv4(canonical)
	}
	if cidr := ipv6(); cidr != "" {
		canonical, err := parseAndValidateCIDR(cidr, cidrIPv6)
		if err != nil {
			return err
		}
		setIPv6(canonical)
	}
	return nil
}
