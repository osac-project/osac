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

import "strings"

// toDNSLabel converts a string to DNS-label format by replacing underscores with hyphens.
// Used to derive metadata.name from domain-specific identifiers for catalog resources
// that historically didn't set metadata.name (NetworkClass, Templates, HostType).
func toDNSLabel(s string) string {
	return strings.ReplaceAll(s, "_", "-")
}

// templateNameFromID extracts the role name from a template's deterministic ID
// (format: "{collection}.{role_name}") and converts it to DNS-label format.
// For example, "osac.templates.ocp_virt_vm" becomes "ocp-virt-vm".
func templateNameFromID(id string) string {
	if i := strings.LastIndex(id, "."); i >= 0 {
		return toDNSLabel(id[i+1:])
	}
	return toDNSLabel(id)
}
