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
)

// billingAssignmentMethods returns the provider-dependent assignment methods whose request carries a
// BillingAccountReference. They report a disabled deployment before that reference is resolved.
func billingAssignmentMethods() []string {
	return []string{
		privatev1.TenantBillingAssignments_Create_FullMethodName,
		privatev1.TenantBillingAssignments_Update_FullMethodName,
	}
}
