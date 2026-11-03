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

package inventory

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

var logicalPortMACPattern = regexp.MustCompile(
	`(?i)^(?:(?:[0-9a-f]{2}:){5}[0-9a-f]{2}|(?:[0-9a-f]{2}-){5}[0-9a-f]{2})$`,
)

// ErrMalformedLogicalPortMACs marks an invalid provider mapping so selection
// can skip that host without hiding transport or inventory failures.
var ErrMalformedLogicalPortMACs = errors.New("malformed logical-port MAC mapping")

// NormalizeMACAddress accepts six-byte MAC addresses with colon or hyphen
// separators and returns the canonical lowercase colon-separated form.
func NormalizeMACAddress(value string) (string, error) {
	if !logicalPortMACPattern.MatchString(value) {
		return "", fmt.Errorf("%q must be a six-byte MAC with colon or hyphen separators", value)
	}
	address, err := net.ParseMAC(strings.ReplaceAll(value, "-", ":"))
	if err != nil {
		return "", fmt.Errorf("parsing MAC address: %w", err)
	}
	if len(address) != 6 {
		return "", fmt.Errorf("%q must be a six-byte MAC", value)
	}
	return address.String(), nil
}

// HasValidNetworkPortMAC reports whether mapping contains a valid six-byte
// MAC for the selected logical interface.
func HasValidNetworkPortMAC(mapping map[string]string, interfaceName string) bool {
	mac, ok := mapping[interfaceName]
	if !ok {
		return false
	}
	_, err := NormalizeMACAddress(mac)
	return err == nil
}

// LogicalPortMACsFromValue parses the provider-specific JSON value used for
// the logical-port-to-MAC map. Providers may return an object or a JSON string.
func LogicalPortMACsFromValue(value any) (map[string]string, error) {
	if value == nil {
		return map[string]string{}, nil
	}
	var raw []byte
	if text, ok := value.(string); ok {
		raw = []byte(text)
	} else {
		var err error
		raw, err = json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("marshal logical-port MAC mapping: %w", err)
		}
	}
	if len(raw) == 0 {
		return map[string]string{}, nil
	}
	mapping := map[string]string{}
	if err := json.Unmarshal(raw, &mapping); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedLogicalPortMACs, err)
	}
	return mapping, nil
}
