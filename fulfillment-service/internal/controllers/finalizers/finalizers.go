/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package finalizers

import (
	"slices"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// Names of well known finalizers.
const (
	Controller                 = "fulfillment-controller"
	TenantLifecycle            = Controller + "-tenant-lifecycle"
	TenantOnboarding           = Controller + "-tenant-onboarding"
	ProjectMembershipFinalizer = "projectmembership.osac.io/finalizer"
)

// PrepareTenant installs both cleanup barriers before provisioning. During
// deletion, missing barriers represent completed work and must not be restored.
// The caller must persist a change with a locked update and return immediately.
func PrepareTenant(tenant *privatev1.Tenant) bool {
	if !tenant.HasMetadata() {
		tenant.SetMetadata(&privatev1.Metadata{})
	}
	metadata := tenant.GetMetadata()
	list := metadata.GetFinalizers()
	if metadata.HasDeletionTimestamp() {
		return false
	}
	changed := false
	for _, barrier := range []string{TenantLifecycle, TenantOnboarding} {
		if !slices.Contains(list, barrier) {
			list = append(list, barrier)
			changed = true
		}
	}
	if changed {
		metadata.SetFinalizers(list)
	}
	return changed
}
