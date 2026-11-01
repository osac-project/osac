//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package serviceaccounts

import (
	"strings"
)

const RegistrationAccessTokenKey = "registration_access_token"
const RegistrationClientUUIDKey = "client_uuid"
const RegistrationServiceAccountIDKey = "service_account_id"
const PendingClientSecretKey = "client_secret"
const PendingClientSecretRefIDKey = "client_secret_ref_id"
const PendingClientSecretRefNameKey = "client_secret_ref_name"
const ManagedClientIDPrefix = "osac-sa-"
const PendingRotationSecretKey = "rotation_client_secret"
const PendingRotationGenerationKey = "rotation_generation"
const clientSecretPrefix = "osac-service-account-"
const registrationSecretPrefix = "osac-registration-"

// ClientID derives an immutable client identifier from validated tenant and account identities.
func ClientID(tenantID, accountID string) string {
	return ManagedClientIDPrefix + tenantID + "-" + accountID
}

// RegistrationSecretName derives a stable system-tenant Secret name from the account ID.
func RegistrationSecretName(accountID string) string {
	if accountID == "" {
		return ""
	}
	base := registrationSecretPrefix + accountID
	if len(base) > 63 {
		base = base[:63]
	}
	base = strings.TrimRight(base, "-")
	return base
}

// ClientSecretName returns the tenant-scoped Secret resource name for a service-account credential.
func ClientSecretName(accountID string) string {
	return clientSecretPrefix + accountID
}
