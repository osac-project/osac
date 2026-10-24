/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package serviceaccounts

import (
	"strings"

	"github.com/google/uuid"
)

const RegistrationAccessTokenKey = "registration_access_token"
const registrationSecretPrefix = "osac-internal-sa-"

// ClientID derives an immutable client identifier from validated tenant and account identities.
func ClientID(tenantID, accountID string) string {
	return "osac-sa-" + tenantID + "-" + accountID
}

func RegistrationSecretName(accountID string) string {
	return registrationSecretPrefix + accountID
}

func IsRegistrationSecretName(name string) bool {
	accountID, ok := strings.CutPrefix(name, registrationSecretPrefix)
	if !ok {
		return false
	}
	_, err := uuid.Parse(accountID)
	return err == nil
}
