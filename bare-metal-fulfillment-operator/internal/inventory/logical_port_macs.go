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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"

	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

// ParseLogicalPortMAC resolves and normalizes the MAC for one logical port.
func ParseLogicalPortMAC(macs map[string]string, logicalPort string) (string, error) {
	if logicalPort == "" {
		return "", fmt.Errorf("%w: logical port name is empty", ErrInvalidLogicalPortMAC)
	}

	value, ok := macs[logicalPort]
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%w: no MAC for logical port %q", ErrInvalidLogicalPortMAC, logicalPort)
	}

	parsed, err := net.ParseMAC(strings.TrimSpace(value))
	if err != nil || len(parsed) != 6 {
		return "", fmt.Errorf("%w: invalid MAC for logical port %q", ErrInvalidLogicalPortMAC, logicalPort)
	}
	return strings.ToLower(parsed.String()), nil
}

// decodeLogicalPortMACMapping converts the provider's candidate metadata into
// the common logical-port-to-MAC representation. Providers may expose this as
// a JSON object or as a JSON-encoded string.
func decodeLogicalPortMACMapping(value any) (map[string]string, error) {
	if value == nil {
		return map[string]string{}, nil
	}

	var raw []byte
	if text, ok := value.(string); ok {
		if strings.TrimSpace(text) == "" {
			return map[string]string{}, nil
		}
		raw = []byte(text)
	} else {
		var err error
		raw, err = json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to marshal mapping: %v", ErrMalformedLogicalPortMACMapping, err)
		}
	}

	macs := map[string]string{}
	if err := json.Unmarshal(raw, &macs); err != nil {
		return nil, fmt.Errorf("%w: failed to parse mapping: %v", ErrMalformedLogicalPortMACMapping, err)
	}
	return macs, nil
}

// ResolveHostLogicalPortMAC reads and parses a host's selected logical-port MAC.
func ResolveHostLogicalPortMAC(ctx context.Context, client Client, hostID, logicalPort string) (string, error) {
	macs, err := client.GetHostLogicalPortMACs(ctx, hostID)
	if err != nil {
		return "", err
	}
	return ParseLogicalPortMAC(macs, logicalPort)
}

func requestedLogicalPort(selectedLogicalPorts []string) (string, error) {
	switch len(selectedLogicalPorts) {
	case 0:
		return "", nil
	case 1:
		if selectedLogicalPorts[0] == "" {
			return "", fmt.Errorf("%w: logical port name is empty", ErrInvalidLogicalPortMAC)
		}
		return selectedLogicalPorts[0], nil
	default:
		return "", fmt.Errorf("%w: only one selected logical port is supported", ErrInvalidLogicalPortMAC)
	}
}

func hostSupportsLogicalPort(
	ctx context.Context,
	host *Host,
	logicalPort string,
	macs map[string]string,
	mappingErr error,
) (bool, error) {
	if logicalPort == "" {
		return true, nil
	}
	log := ctrllog.FromContext(ctx)
	if host.Name == "" {
		log.Info("Skipping host without an inventory name for selected logical port", "port", logicalPort)
		return false, nil
	}

	if mappingErr != nil && !errors.Is(mappingErr, ErrMalformedLogicalPortMACMapping) {
		return false, mappingErr
	}
	if mappingErr != nil {
		log.Info("Skipping host with malformed selected-port MAC mapping", "host", host.Name, "port", logicalPort)
		return false, nil
	}

	mac, err := ParseLogicalPortMAC(macs, logicalPort)
	if err != nil {
		if errors.Is(err, ErrInvalidLogicalPortMAC) {
			log.Info(fmt.Sprintf("host %s skipped: no valid MAC for port %s", host.Name, logicalPort))
			return false, nil
		}
		return false, err
	}
	host.SelectedLogicalPortMAC = mac
	return true, nil
}
