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
	"encoding/base32"
	"errors"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

const netBoxHostNamePrefix = "netbox-"
const netBoxEncodedHostNamePrefix = "nb-"

var errNetBoxClientNotImplemented = errors.New("NetBox inventory client is not implemented")

// NetBoxClient is the scaffold for the NetBox-backed inventory implementation.
// Its inventory operations return an error until the secure API adapter is implemented.
type NetBoxClient struct{}

var (
	_ Client        = (*NetBoxClient)(nil)
	_ NewClientFunc = NewNetBoxClient
)

// NewNetBoxClient constructs the NetBox inventory client scaffold.
func NewNetBoxClient(_ context.Context, _ *Config) (Client, error) {
	return &NetBoxClient{}, nil
}

func (*NetBoxClient) FindFreeHost(_ context.Context, _ map[string]string) (*Host, error) {
	return nil, errNetBoxClientNotImplemented
}

func (*NetBoxClient) AssignHost(_ context.Context, _ string, _ string, _ map[string]string) (*Host, error) {
	return nil, errNetBoxClientNotImplemented
}

func (*NetBoxClient) UnassignHost(_ context.Context, _ string, _ []string) error {
	return errNetBoxClientNotImplemented
}

func (*NetBoxClient) GetHostNICs(_ context.Context, _ string) ([]HostNIC, error) {
	return nil, errNetBoxClientNotImplemented
}

// GetHostLogicalPortMACs is not implemented until the NetBox API adapter is available.
func (*NetBoxClient) GetHostLogicalPortMACs(_ context.Context, _ string) (map[string]string, error) {
	return nil, errNetBoxClientNotImplemented
}

type netBoxHostIdentity struct {
	namespace       string
	deviceID        string
	name            string
	inventoryHostID string
}

func newNetBoxHostIdentity(namespace, deviceID string) (netBoxHostIdentity, error) {
	if namespace == "" || strings.Contains(namespace, "/") {
		return netBoxHostIdentity{}, fmt.Errorf("invalid Metal3 namespace for NetBox host identity")
	}
	if deviceID == "" {
		return netBoxHostIdentity{}, fmt.Errorf("invalid NetBox device ID: expected a non-empty string")
	}

	name, err := netBoxHostName(deviceID)
	if err != nil {
		return netBoxHostIdentity{}, err
	}
	return netBoxHostIdentity{
		namespace:       namespace,
		deviceID:        deviceID,
		name:            name,
		inventoryHostID: namespace + "/" + name,
	}, nil
}

func parseNetBoxHostIdentity(inventoryHostID, configuredNamespace string) (netBoxHostIdentity, error) {
	namespace, name, found := strings.Cut(inventoryHostID, "/")
	if !found || namespace == "" || namespace != configuredNamespace {
		return netBoxHostIdentity{}, fmt.Errorf("invalid persisted NetBox host identity")
	}

	var deviceID string
	switch {
	case strings.HasPrefix(name, netBoxEncodedHostNamePrefix):
		encodedID := strings.TrimPrefix(name, netBoxEncodedHostNamePrefix)
		decodedID, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(encodedID))
		if err != nil {
			return netBoxHostIdentity{}, fmt.Errorf("invalid persisted NetBox host identity")
		}
		deviceID = string(decodedID)
	case strings.HasPrefix(name, netBoxHostNamePrefix):
		deviceID = strings.TrimPrefix(name, netBoxHostNamePrefix)
	default:
		return netBoxHostIdentity{}, fmt.Errorf("invalid persisted NetBox host identity")
	}

	identity, err := newNetBoxHostIdentity(namespace, deviceID)
	if err != nil {
		return netBoxHostIdentity{}, fmt.Errorf("invalid persisted NetBox host identity: %w", err)
	}
	if identity.inventoryHostID != inventoryHostID {
		return netBoxHostIdentity{}, fmt.Errorf("invalid persisted NetBox host identity")
	}
	return identity, nil
}

func netBoxHostName(deviceID string) (string, error) {
	name := netBoxHostNamePrefix + deviceID
	if len(validation.IsDNS1035Label(name)) == 0 {
		return name, nil
	}

	encodedID := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(deviceID)))
	name = netBoxEncodedHostNamePrefix + encodedID
	if len(validation.IsDNS1035Label(name)) != 0 {
		return "", fmt.Errorf("NetBox device ID is too long to encode as a Kubernetes host name")
	}
	return name, nil
}
