/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import "github.com/osac-project/osac/fulfillment-service/internal/collections"

// Subject represents an entity, such as person or a service account.
type Subject struct {
	// User is the name of the user.
	User string

	// Tenant is the subject's home tenant. It is empty when the home tenant is unknown.
	Tenant string

	// Tenants is the set of tenants visible to the subject, before tenancy logic adds shared.
	// It may be the universal set if all tenants are visible.
	Tenants collections.Set[string]

	// ManagedServiceAccount marks an authenticated, Fulfillment-managed client for this request.
	ManagedServiceAccount bool
}
