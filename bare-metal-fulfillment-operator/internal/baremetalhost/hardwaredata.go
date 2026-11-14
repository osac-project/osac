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

package baremetalhost

import (
	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
)

// ResolveHardwareDetails selects the Metal3 hardware inventory to read NIC
// data from, preferring the standalone HardwareData resource over the
// deprecated BareMetalHost.Status.HardwareDetails. It returns
// hd.Spec.HardwareDetails when that carries usable NIC inventory (at least one
// NIC with a non-empty MAC), otherwise bmh.Status.HardwareDetails (which may
// itself be nil). hd may be nil when no companion HardwareData exists, and bmh
// may be nil when the caller only has HardwareData in hand. An empty or
// all-empty-MAC HardwareData does not shadow a populated status: precedence
// means "prefer HardwareData when it actually has usable NIC data". Gating on a
// usable MAC (rather than NIC count) matters because NIC.MAC is
// json:"mac,omitempty", so a NIC entry can exist with no MAC — callers
// drop such entries (HardwareMACs skips empty MACs), so treating them
// as present here would strand the status fallback.
func ResolveHardwareDetails(hd *metal3api.HardwareData, bmh *metal3api.BareMetalHost) *metal3api.HardwareDetails {
	if hd != nil && hd.Spec.HardwareDetails != nil && hasUsableNIC(hd.Spec.HardwareDetails) {
		return hd.Spec.HardwareDetails
	}
	if bmh != nil {
		return bmh.Status.HardwareDetails
	}
	return nil
}

// hasUsableNIC reports whether details carries at least one NIC with a
// non-empty MAC address.
func hasUsableNIC(details *metal3api.HardwareDetails) bool {
	for _, nic := range details.NIC {
		if nic.MAC != "" {
			return true
		}
	}
	return false
}
