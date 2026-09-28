/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalworker

import (
	"context"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// nicMACs extracts the NIC MAC addresses from a BareMetalInstance's status.hardware.nics
// (populated by the inventory backend at allocation time, OSAC-4203). Returns nil when no
// NIC data is present yet.
func nicMACs(bmi *privatev1.BareMetalInstance) []string {
	nics := bmi.GetStatus().GetHardware().GetNics()
	if len(nics) == 0 {
		return nil
	}
	macs := make([]string, 0, len(nics))
	for _, nic := range nics {
		if m := nic.GetMac(); m != "" {
			macs = append(macs, m)
		}
	}
	if len(macs) == 0 {
		return nil
	}
	return macs
}

// resolveHostMACs is the production MACResolver: it fetches the BMI from the fulfillment
// service and returns its host NIC MACs. Returns nil on error or when no NIC data exists yet
// (correlation simply waits and retries).
func (r *Reconciler) resolveHostMACs(ctx context.Context, bmiID string) []string {
	bmi, err := r.fulfillment.GetBareMetalInstance(ctx, bmiID)
	if err != nil {
		return nil
	}
	return nicMACs(bmi)
}
