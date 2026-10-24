/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package serviceaccounts_test

import (
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac/fulfillment-service/internal/serviceaccounts"
)

func TestServiceAccounts(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Service account identity and roles")
}

var _ = Describe("Service account identity", func() {
	It("derives a stable client ID from the tenant and account UUID", func() {
		Expect(serviceaccounts.ClientID("tenant-a", "d3c4e3db-95b8-481d-8b5c-963da477da12")).To(Equal(
			"osac-sa-tenant-a-d3c4e3db-95b8-481d-8b5c-963da477da12"))
		Expect(serviceaccounts.ClientID(strings.Repeat("t", 63), "d3c4e3db-95b8-481d-8b5c-963da477da12")).To(HaveLen(108))
	})
	It("uses a stable Vault entry name for the scoped Keycloak token", func() {
		accountID := "d3c4e3db-95b8-481d-8b5c-963da477da12"
		name := serviceaccounts.RegistrationSecretName(accountID)
		Expect(name).To(Equal("osac-internal-sa-" + accountID))
		Expect(serviceaccounts.IsRegistrationSecretName(name)).To(BeTrue())
		Expect(serviceaccounts.IsRegistrationSecretName("osac-internal-sa-user-data")).To(BeFalse())
		Expect(serviceaccounts.RegistrationAccessTokenKey).To(Equal("registration_access_token"))
	})
	DescribeTable("limits stored grants to the supported catalog", func(name string, assignable, machineOnly bool) {
		Expect(serviceaccounts.IsAssignableRole(name)).To(Equal(assignable))
		Expect(serviceaccounts.IsMachineOnlyRole(name)).To(Equal(machineOnly))
	},
		Entry("automation", "tenant-automation", true, true),
		Entry("CSI", "osac-csi-driver", true, true),
		Entry("admin", "tenant-admin", true, false),
		Entry("IdP manager", "tenant-idp-manager", true, false),
		Entry("removed user", "tenant-user", false, false),
		Entry("removed reader", "tenant-reader", false, false),
		Entry("provider", "cloud-provider-admin", false, false),
		Entry("realm", "realm-management", false, false),
		Entry("unknown", "custom", false, false),
		Entry("empty", "", false, false),
	)
})
