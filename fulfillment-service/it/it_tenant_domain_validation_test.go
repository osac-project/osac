/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
)

var _ = Describe("Tenant domain validation (protovalidate)", func() {
	var (
		ctx          context.Context
		tenantClient privatev1.TenantsClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		tenantClient = privatev1.NewTenantsClient(tool.InternalView().AdminConn())
	})

	It("Accepts valid domain with two labels", func() {
		response, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "valid-tenant-1",
				}.Build(),
				Spec: privatev1.TenantSpec_builder{
					Domains: []string{"example.com"},
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).ToNot(HaveOccurred())
		Expect(response.Object.Spec.Domains).To(Equal([]string{"example.com"}))

		DeferCleanup(func() {
			_, _ = tenantClient.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
				Id: response.Object.Id,
			}.Build())
		})
	})

	It("Accepts valid domain with multiple labels", func() {
		response, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "valid-tenant-2",
				}.Build(),
				Spec: privatev1.TenantSpec_builder{
					Domains: []string{"mail.corp.example.com"},
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).ToNot(HaveOccurred())
		Expect(response.Object.Spec.Domains).To(Equal([]string{"mail.corp.example.com"}))

		DeferCleanup(func() {
			_, _ = tenantClient.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
				Id: response.Object.Id,
			}.Build())
		})
	})

	It("Accepts multiple valid domains", func() {
		response, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "valid-tenant-3",
				}.Build(),
				Spec: privatev1.TenantSpec_builder{
					Domains: []string{"domain-validation-test-1.com", "domain-validation-test-2.org", "test.domain-validation.net"},
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).ToNot(HaveOccurred())
		Expect(response.Object.Spec.Domains).To(ConsistOf("domain-validation-test-1.com", "domain-validation-test-2.org", "test.domain-validation.net"))

		DeferCleanup(func() {
			_, _ = tenantClient.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
				Id: response.Object.Id,
			}.Build())
		})
	})

	It("Rejects empty domain string", func() {
		_, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "invalid-tenant",
				}.Build(),
				Spec: privatev1.TenantSpec_builder{
					Domains: []string{""},
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("validation failed"))
		Expect(status.Message()).To(ContainSubstring("spec.domains"))
	})

	It("Rejects domain longer than 253 characters", func() {
		// Create a domain > 253 chars
		longDomain := ""
		for i := 0; i < 30; i++ {
			longDomain += "verylonglabel."
		}
		longDomain += "com" // Total > 253

		_, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "invalid-tenant",
				}.Build(),
				Spec: privatev1.TenantSpec_builder{
					Domains: []string{longDomain},
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("validation failed"))
		Expect(status.Message()).To(ContainSubstring("spec.domains"))
		Expect(status.Message()).To(ContainSubstring("must be at most 253"))
	})

	It("Rejects IPv4 address", func() {
		_, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "invalid-tenant",
				}.Build(),
				Spec: privatev1.TenantSpec_builder{
					Domains: []string{"192.168.1.1"},
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("validation failed"))
		Expect(status.Message()).To(ContainSubstring("must be a DNS hostname, not an IP address"))
	})

	It("Rejects IPv6 address", func() {
		_, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "invalid-tenant",
				}.Build(),
				Spec: privatev1.TenantSpec_builder{
					Domains: []string{"2001:db8::1"},
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("validation failed"))
		Expect(status.Message()).To(ContainSubstring("must be a DNS hostname, not an IP address"))
	})

	It("Rejects domain with only one label", func() {
		_, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "invalid-tenant",
				}.Build(),
				Spec: privatev1.TenantSpec_builder{
					Domains: []string{"example"},
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("validation failed"))
		Expect(status.Message()).To(ContainSubstring("must have at least two labels"))
	})

	It("Rejects domain with label longer than 63 characters", func() {
		longLabel := ""
		for i := 0; i < 64; i++ {
			longLabel += "a"
		}
		invalidDomain := longLabel + ".com"

		_, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "invalid-tenant",
				}.Build(),
				Spec: privatev1.TenantSpec_builder{
					Domains: []string{invalidDomain},
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("validation failed"))
		Expect(status.Message()).To(ContainSubstring("valid DNS labels"))
		Expect(status.Message()).To(ContainSubstring("max 63 chars"))
	})

	It("Rejects domain with uppercase letters", func() {
		_, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "invalid-tenant",
				}.Build(),
				Spec: privatev1.TenantSpec_builder{
					Domains: []string{"Example.Com"},
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("validation failed"))
		Expect(status.Message()).To(ContainSubstring("valid DNS labels"))
		Expect(status.Message()).To(ContainSubstring("lowercase"))
	})

	It("Rejects domain with label starting with hyphen", func() {
		_, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "invalid-tenant",
				}.Build(),
				Spec: privatev1.TenantSpec_builder{
					Domains: []string{"-example.com"},
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("validation failed"))
		Expect(status.Message()).To(ContainSubstring("valid DNS labels"))
		Expect(status.Message()).To(ContainSubstring("alphanumeric start"))
	})

	It("Rejects domain with label ending with hyphen", func() {
		_, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "invalid-tenant",
				}.Build(),
				Spec: privatev1.TenantSpec_builder{
					Domains: []string{"example-.com"},
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("validation failed"))
		Expect(status.Message()).To(ContainSubstring("valid DNS labels"))
		Expect(status.Message()).To(ContainSubstring("alphanumeric"))
		Expect(status.Message()).To(ContainSubstring("end"))
	})

	It("Rejects domain with invalid characters", func() {
		_, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "invalid-tenant",
				}.Build(),
				Spec: privatev1.TenantSpec_builder{
					Domains: []string{"exam_ple.com"},
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("validation failed"))
		Expect(status.Message()).To(ContainSubstring("valid DNS labels"))
	})

	It("Rejects duplicate domains", func() {
		_, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "invalid-tenant",
				}.Build(),
				Spec: privatev1.TenantSpec_builder{
					Domains: []string{"example.com", "test.com", "example.com"},
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("validation failed"))
		Expect(status.Message()).To(ContainSubstring("spec.domains"))
		Expect(status.Message()).To(ContainSubstring("unique"))
	})

	It("Accepts Update with valid domains in mask", func() {
		// Create tenant with initial domain
		created, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "update-test-tenant",
				}.Build(),
				Spec: privatev1.TenantSpec_builder{
					Domains: []string{"initial-update-test.com"},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = tenantClient.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
				Id: created.Object.Id,
			}.Build())
		})

		// Update domains to new valid values
		updated, err := tenantClient.Update(ctx, privatev1.TenantsUpdateRequest_builder{
			Object: privatev1.Tenant_builder{
				Id: created.Object.Id,
				Metadata: privatev1.Metadata_builder{
					Name: created.Object.Metadata.Name,
				}.Build(),
				Spec: privatev1.TenantSpec_builder{
					Domains: []string{"upd-new-" + created.Object.Id + ".com", "upd-another-" + created.Object.Id + ".org"},
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).ToNot(HaveOccurred())
		Expect(updated.Object.Spec.Domains).To(HaveLen(2))
		Expect(updated.Object.Spec.Domains).To(ContainElement(ContainSubstring("upd-new-")))
		Expect(updated.Object.Spec.Domains).To(ContainElement(ContainSubstring("upd-another-")))
	})

	It("Rejects Update with invalid domain in mask", func() {
		// Create tenant with valid domain
		created, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "update-test-tenant-2",
				}.Build(),
				Spec: privatev1.TenantSpec_builder{
					Domains: []string{"update-invalid-test.com"},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = tenantClient.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
				Id: created.Object.Id,
			}.Build())
		})

		// Try to update with invalid domain (only one label)
		_, err = tenantClient.Update(ctx, privatev1.TenantsUpdateRequest_builder{
			Object: privatev1.Tenant_builder{
				Id: created.Object.Id,
				Spec: privatev1.TenantSpec_builder{
					Domains: []string{"invalid"},
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("validation failed"))
		Expect(status.Message()).To(ContainSubstring("must have at least two labels"))
	})
})
