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
	"errors"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
)

const (
	// NetworkRepairRequiredLabel is the Metal3 BMH label that excludes a host
	// from FindFreeHost until an admin clears it (design repair mark).
	NetworkRepairRequiredLabel = shared.OsacPrefix + "/network-repair-required"

	// NetworkRepairDetailsAnnotation carries JSON repair context on a Metal3 BMH.
	NetworkRepairDetailsAnnotation = shared.OsacPrefix + "/network-repair-details"

	// OpenStackNetworkRepairRequiredKey is the Ironic node.extra mark key.
	OpenStackNetworkRepairRequiredKey = "osac_network_repair_required"

	// OpenStackNetworkRepairDetailsKey is the Ironic node.extra details key.
	OpenStackNetworkRepairDetailsKey = "osac_network_repair_details"

	// BCMNetworkRepairRequiredKey marks a BCM device that requires manual repair.
	BCMNetworkRepairRequiredKey = "osac_network_repair_required"

	// BCMNetworkRepairDetailsKey carries JSON repair context on a BCM device.
	BCMNetworkRepairDetailsKey = "osac_network_repair_details"

	// NetBoxNetworkRepairRequiredKey marks a NetBox device that requires repair.
	NetBoxNetworkRepairRequiredKey = "osac_network_repair_required"

	// NetBoxNetworkRepairDetailsKey carries JSON repair context on a NetBox device.
	NetBoxNetworkRepairDetailsKey = "osac_network_repair_details"
)

// ErrNetworkRepairMarkUnsupported is reserved for inventory backends that do
// not persist network repair marks.
var ErrNetworkRepairMarkUnsupported = errors.New("network repair mark not supported by this inventory backend")

// networkRepairRequired reports whether an Ironic extra value is a set mark.
func networkRepairRequired(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true" || t == "True" || t == "1"
	default:
		return false
	}
}
