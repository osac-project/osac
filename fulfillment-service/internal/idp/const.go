/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package idp

// realmManagementClientID is the clientId of the built-in Keycloak client that contains
// all administrative roles for managing a realm. This client exists by default in every
// realm and is the only client we interact with for role assignments.
const realmManagementClientID = "realm-management"

// Authorization group constants for hierarchical project access control. These define the group
// names used in Keycloak organization groups. The "system:" prefix prevents collisions with
// user-created project names, since the colon character is not valid in DNS labels.
const (
	// GroupNameViewers is the name for viewer access groups.
	GroupNameViewers = "system:viewers"

	// GroupNameManagers is the name for manager access groups.
	GroupNameManagers = "system:managers"
)
