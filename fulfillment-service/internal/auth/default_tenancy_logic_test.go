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
	"github.com/osac-project/fulfillment-service/internal/collections"
)

var _ = Describe("Default tenancy logic", func() {
	var (
		ctx   context.Context
		logic *DefaultTenancyLogic
	)

	BeforeEach(func() {
		var err error

		// Create the context:
		ctx = context.Background()

		// Create the tenancy logic:
		logic, err = NewDefaultTenancyLogic().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(logic).ToNot(BeNil())
	})

	Describe("Builder", func() {
		It("Fails if logger is not set", func() {
			logic, err := NewDefaultTenancyLogic().
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger is mandatory"))
			Expect(logic).To(BeNil())
		})
	})

	Describe("Determine assignable tenants", func() {
		It("Returns the tenants from the subject", func() {
			subject := &Subject{
				User:    "my_user",
				Tenants: collections.NewSet("tenant-a", "tenant-b"),
			}
			ctx = ContextWithSubject(ctx, subject)
			result, err := logic.DetermineAssignableTenants(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Equal(collections.NewSet("tenant-a", "tenant-b"))).To(BeTrue())
		})

		It("Returns multiple tenants when present", func() {
			subject := &Subject{
				User:    "my_user",
				Tenants: collections.NewSet("tenant-a", "tenant-b", "tenant-c"),
			}
			ctx = ContextWithSubject(ctx, subject)
			result, err := logic.DetermineAssignableTenants(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Equal(collections.NewSet("tenant-a", "tenant-b", "tenant-c"))).To(BeTrue())
		})

		It("Returns universal set when tenants is universal", func() {
			subject := &Subject{
				User:    "my_user",
				Tenants: AllTenants,
			}
			ctx = ContextWithSubject(ctx, subject)
			result, err := logic.DetermineAssignableTenants(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Equal(AllTenants)).To(BeTrue())
		})

		It("Fails if the subject has an empty tenants set", func() {
			subject := &Subject{
				User:    "my_user",
				Tenants: collections.NewSet[string](),
			}
			ctx = ContextWithSubject(ctx, subject)
			_, err := logic.DetermineAssignableTenants(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least one tenant"))
		})

		It("Returns an empty set when there is an error", func() {
			subject := &Subject{
				User: "my_user",
			}
			ctx = ContextWithSubject(ctx, subject)
			result, err := logic.DetermineAssignableTenants(ctx)
			Expect(err).To(HaveOccurred())
			Expect(result.Empty()).To(BeTrue())
		})
	})

	Describe("Determine default tenant", func() {
		It("Returns a tenant from the subject", func() {
			subject := &Subject{
				User:    "my_user",
				Tenants: collections.NewSet("tenant-a", "tenant-b"),
			}
			ctx = ContextWithSubject(ctx, subject)
			result, err := logic.DetermineDefaultTenant(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(BeElementOf("tenant-a", "tenant-b"))
		})

		It("Returns shared when tenants is universal", func() {
			subject := &Subject{
				User:    "my_user",
				Tenants: AllTenants,
			}
			ctx = ContextWithSubject(ctx, subject)
			result, err := logic.DetermineDefaultTenant(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(SharedTenant))
		})

		It("Fails if the subject has an empty tenants set", func() {
			subject := &Subject{
				User:    "my_user",
				Tenants: collections.NewSet[string](),
			}
			ctx = ContextWithSubject(ctx, subject)
			_, err := logic.DetermineDefaultTenant(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least one tenant"))
		})
	})

	Describe("Determine visible tenants", func() {
		It("Returns tenants and shared", func() {
			subject := &Subject{
				User:    "my_user",
				Tenants: collections.NewSet("tenant-a", "tenant-b"),
			}
			ctx = ContextWithSubject(ctx, subject)
			result, err := logic.DetermineVisibleTenants(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Equal(SharedTenants.Union(collections.NewSet("tenant-a", "tenant-b")))).To(BeTrue())
		})

		It("Returns universal set when tenants is universal", func() {
			subject := &Subject{
				User:    "my_user",
				Tenants: AllTenants,
			}
			ctx = ContextWithSubject(ctx, subject)
			result, err := logic.DetermineVisibleTenants(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Equal(AllTenants)).To(BeTrue())
		})

		It("Returns only shared when tenants is empty", func() {
			subject := &Subject{
				User:    "my_user",
				Tenants: collections.NewSet[string](),
			}
			ctx = ContextWithSubject(ctx, subject)
			result, err := logic.DetermineVisibleTenants(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Equal(SharedTenants)).To(BeTrue())
		})
	})
})
