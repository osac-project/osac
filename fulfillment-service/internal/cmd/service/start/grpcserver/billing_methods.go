/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package grpcserver

import (
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// billingAssignmentMethods returns the provider-dependent assignment methods whose request carries a
// BillingAccountReference. They report a disabled deployment before that reference is resolved.
func billingAssignmentMethods() []string {
	return []string{
		publicv1.TenantBillingAssignments_Create_FullMethodName,
		privatev1.TenantBillingAssignments_Create_FullMethodName,
		publicv1.TenantBillingAssignments_Update_FullMethodName,
		privatev1.TenantBillingAssignments_Update_FullMethodName,
	}
}

// tenantBillingAssignmentUpdateMethods returns the Update methods whose handler treats an explicitly
// present, empty update mask as a validated no-op without provider calls. Nothing is selected, so the
// stale request body is not resolved; Create and updates selecting spec.billing_account keep the
// ordinary reference validation.
func tenantBillingAssignmentUpdateMethods() []string {
	return []string{
		publicv1.TenantBillingAssignments_Update_FullMethodName,
		privatev1.TenantBillingAssignments_Update_FullMethodName,
	}
}
