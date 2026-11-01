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
	RunSpecs(t, "Service account identity")
}

var _ = Describe("Service account identity", func() {
	It("derives a stable client ID from the tenant and account UUID", func() {
		Expect(serviceaccounts.ClientID("tenant-a", "d3c4e3db-95b8-481d-8b5c-963da477da12")).To(Equal(
			"osac-sa-tenant-a-d3c4e3db-95b8-481d-8b5c-963da477da12"))
		Expect(serviceaccounts.ClientID(strings.Repeat("t", 63), "d3c4e3db-95b8-481d-8b5c-963da477da12")).To(HaveLen(108))
	})
	It("uses a stable account ID for registration Secret names", func() {
		accountID := "d3c4e3db-95b8-481d-8b5c-963da477da12"
		Expect(serviceaccounts.RegistrationSecretName(accountID)).To(Equal("osac-registration-" + accountID))
		Expect(serviceaccounts.RegistrationSecretName(accountID)).ToNot(Equal(
			serviceaccounts.RegistrationSecretName("d3c4e3db-95b8-481d-8b5c-963da477da13")))
		Expect(serviceaccounts.RegistrationSecretName("")).To(BeEmpty())
		longID := strings.Repeat("a", 63)
		Expect(len(serviceaccounts.RegistrationSecretName(longID))).To(BeNumerically("<=", 63))
		Expect(serviceaccounts.RegistrationAccessTokenKey).To(Equal("registration_access_token"))
	})
	It("uses a tenant-safe resource name for the client credential Secret", func() {
		accountID := "d3c4e3db-95b8-481d-8b5c-963da477da12"
		name := serviceaccounts.ClientSecretName(accountID)
		Expect(name).To(Equal("osac-service-account-d3c4e3db-95b8-481d-8b5c-963da477da12"))
		Expect(len(name)).To(BeNumerically("<=", 63))
		Expect(name).ToNot(Equal(serviceaccounts.RegistrationSecretName(accountID)))
	})
})

var _ = Describe("Service-account authorization roles", func() {
	DescribeTable("limits grants to the supported role catalog", func(name string, assignable bool) {
		Expect(serviceaccounts.IsAssignableRole(name)).To(Equal(assignable))
	},
		Entry("automation", "tenant-automation", true),
		Entry("CSI", "osac-csi-driver", true),
		Entry("tenant admin", "tenant-admin", true),
		Entry("IdP manager", "tenant-idp-manager", true),
		Entry("removed user", "tenant-user", false),
		Entry("removed reader", "tenant-reader", false),
		Entry("provider", "cloud-provider-admin", false),
		Entry("realm role", "realm-management", false),
		Entry("unknown", "custom", false),
		Entry("empty", "", false),
	)
})
