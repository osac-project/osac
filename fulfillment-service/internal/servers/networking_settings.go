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
	"fmt"
	"net/netip"
	"strings"
)

// NetworkingSettings holds the installation settings that the networking and workload servers check. The values come
// from the command line flags of the gRPC server, which the startup checks compare with the stored installation
// settings.
type NetworkingSettings struct {
	// ProvisioningEnabled is the value of global.networking.provisioningEnabled.
	ProvisioningEnabled bool

	// ExcludedIPv4CIDRs is the value of global.networking.excludedIpv4Cidrs.
	ExcludedIPv4CIDRs []netip.Prefix

	// BareMetalEnabled tells whether BMaaS is enabled. FabricDomain creates need it.
	BareMetalEnabled bool
}

// DefaultNetworkingSettings returns the settings used when a server builder doesn't receive explicit settings:
// provider networking enabled, BMaaS enabled and no excluded ranges.
func DefaultNetworkingSettings() NetworkingSettings {
	return NetworkingSettings{
		ProvisioningEnabled: true,
		BareMetalEnabled:    true,
	}
}

// ParseExcludedIPv4CIDRs parses the excluded ranges setting. Each value must be a canonical IPv4 CIDR.
func ParseExcludedIPv4CIDRs(values []string) (result []netip.Prefix, err error) {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		prefix, parseErr := netip.ParsePrefix(value)
		if parseErr != nil || !prefix.Addr().Is4() || prefix.Masked() != prefix || prefix.String() != value {
			err = fmt.Errorf("excludedIpv4Cidrs value '%s' is not a canonical IPv4 CIDR", value)
			return
		}
		result = append(result, prefix)
	}
	return
}

// firstExcludedOverlap returns the first excluded range that overlaps the given prefix.
func (s NetworkingSettings) firstExcludedOverlap(prefix netip.Prefix) (netip.Prefix, bool) {
	for _, excluded := range s.ExcludedIPv4CIDRs {
		if excluded.Overlaps(prefix) {
			return excluded, true
		}
	}
	return netip.Prefix{}, false
}

func resolveNetworkingSettings(value *NetworkingSettings) NetworkingSettings {
	if value == nil {
		return DefaultNetworkingSettings()
	}
	return *value
}
