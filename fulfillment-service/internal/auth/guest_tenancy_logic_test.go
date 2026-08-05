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

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Guest tenancy logic", func() {
	var (
		ctx   context.Context
		logic *GuestTenancyLogic
	)

	BeforeEach(func() {
		var err error

		// Create the context:
		ctx = context.Background()

		// Create the tenancy logic:
		logic, err = NewGuestTenancyLogic().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(logic).ToNot(BeNil())
	})

	Describe("Determine assignable tenants", func() {
		It("Should return the guest tenant", func() {
			result, err := logic.DetermineAssignableTenants(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Equal(GuestTenants)).To(BeTrue())
		})
	})

	Describe("Determine default tenant", func() {
		It("Should return the guest tenant", func() {
			result, err := logic.DetermineDefaultTenant(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("guest"))
		})
	})

	Describe("Determine visible tenants", func() {
		It("Should return the guest and shared tenants", func() {
			result, err := logic.DetermineVisibleTenants(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Equal(GuestTenants.Union(SharedTenants))).To(BeTrue())
		})
	})
})
