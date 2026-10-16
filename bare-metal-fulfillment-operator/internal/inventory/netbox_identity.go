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
	"encoding/base32"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

const netBoxHostNamePrefix = "netbox-"
const netBoxEncodedHostNamePrefix = "nb-"

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
