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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
)

var _ = Describe("Protovalidate validation", func() {
	var (
		ctx            context.Context
		tenantClient   privatev1.TenantsClient
		projectsClient privatev1.ProjectsClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		tenantClient = privatev1.NewTenantsClient(tool.InternalView().AdminConn())
		projectsClient = privatev1.NewProjectsClient(tool.InternalView().AdminConn())

		// Create test tenant for Project tests
		_, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "my-tenant",
				}.Build(),
			}.Build(),
		}.Build())
		if err != nil && !strings.Contains(err.Error(), "already exists") {
			Expect(err).ToNot(HaveOccurred())
		}
	})

	It("Rejects Tenant with invalid metadata name (too long)", func() {
		// Create a Tenant with name > 63 chars:
		invalidName := "this-name-is-way-too-long-and-exceeds-the-sixty-three-character-limit-for-dns-labels"

		_, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: invalidName,
				}.Build(),
			}.Build(),
		}.Build())

		// Verify validation error:
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue(), "error should be a gRPC status error")
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument), "should return InvalidArgument")
		Expect(status.Message()).To(ContainSubstring("validation failed"))
		Expect(status.Message()).To(ContainSubstring("metadata.name"))
		Expect(status.Message()).To(ContainSubstring("must be at most 63 characters"))
	})

	It("Rejects Tenant with invalid metadata name pattern", func() {
		// Create a Tenant with uppercase (invalid for DNS label):
		invalidName := "Invalid-Name-With-Uppercase"

		_, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: invalidName,
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("validation failed"))
		Expect(status.Message()).To(ContainSubstring("metadata.name"))
		Expect(status.Message()).To(ContainSubstring("does not match regex pattern"))
	})

	It("Rejects Tenant with label key that is too long", func() {
		// Create a label key > 316 chars (253 prefix + 1 slash + 62 name):
		longKey := ""
		for i := 0; i < 320; i++ {
			longKey = longKey + "a"
		}

		_, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "valid-name",
					Labels: map[string]string{
						longKey: "value",
					},
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		// Go validation enforces DNS subdomain format (max 63 chars per segment)
		Expect(status.Message()).To(ContainSubstring("metadata.labels"))
		Expect(status.Message()).To(ContainSubstring("must be between 1 and 63 characters long"))
	})

	It("Accepts Tenant with valid metadata", func() {
		validName := "valid-tenant-name"

		response, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: validName,
					Labels: map[string]string{
						"key1":             "value1",
						"example.com/key2": "value2",
					},
					Annotations: map[string]string{
						"annotation-key": "annotation-value",
					},
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		Expect(response.Object.Metadata.Name).To(Equal(validName))

		// Clean up:
		DeferCleanup(func() {
			_, _ = tenantClient.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
				Id: response.Object.Id,
			}.Build())
		})
	})

	It("Accepts Project with dot-separated hierarchical name", func() {
		// Projects use field-level CEL validation that allows dot-separated names
		// like "org.team.project" (each segment is a DNS label, connected with dots)

		// Create parent project hierarchy first: org -> org.team-a -> org.team-a.frontend
		orgProject, err := projectsClient.Create(ctx, privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "org",
					Tenant: "my-tenant",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Organization",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = projectsClient.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{
				Id: orgProject.Object.Id,
			}.Build())
		})

		teamProject, err := projectsClient.Create(ctx, privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "org.team-a",
					Tenant: "my-tenant",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Team A",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = projectsClient.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{
				Id: teamProject.Object.Id,
			}.Build())
		})

		hierarchicalName := "org.team-a.frontend"
		response, err := projectsClient.Create(ctx, privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   hierarchicalName,
					Tenant: "my-tenant",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Frontend Team",
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		Expect(response.Object.Metadata.Name).To(Equal(hierarchicalName))
		// Server should derive parent project from the hierarchical name:
		Expect(response.Object.Metadata.Project).To(Equal("org.team-a"))

		// Clean up:
		DeferCleanup(func() {
			_, _ = projectsClient.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{
				Id: response.Object.Id,
			}.Build())
		})
	})

	It("Rejects Project with invalid segment in hierarchical name", func() {
		// Each dot-separated segment must be a valid DNS label
		invalidName := "org.Team-A.frontend" // "Team-A" has uppercase

		_, err := projectsClient.Create(ctx, privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   invalidName,
					Tenant: "my-tenant",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Invalid Project",
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		// Should be rejected by CEL validation (project_name_segments)
		Expect(status.Message()).To(ContainSubstring("validation failed"))
		Expect(status.Message()).To(ContainSubstring("project name must be empty (root project) or dot-separated DNS labels"))
	})

	It("Rejects Tenant with dots (proves IGNORE_ALWAYS is working for Projects)", func() {
		// Tenants DON'T use IGNORE_ALWAYS, so dots should be rejected by Metadata pattern.
		// This proves that when Projects accept dots, it's because IGNORE_ALWAYS is working.
		nameWithDots := "org.team"

		_, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: nameWithDots,
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		// Should be rejected by Metadata.name pattern (doesn't allow dots)
		Expect(status.Message()).To(ContainSubstring("validation failed"))
		Expect(status.Message()).To(ContainSubstring("metadata.name"))
		Expect(status.Message()).To(ContainSubstring("does not match regex pattern"))
	})

	It("Rejects Project Update with invalid segment in hierarchical name", func() {
		// Create a valid project first
		validProject, err := projectsClient.Create(ctx, privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "valid-project",
					Tenant: "my-tenant",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Valid Project",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = projectsClient.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{
				Id: validProject.Object.Id,
			}.Build())
		})

		// Try to update with invalid name containing uppercase
		invalidName := "Invalid-Name-With-Uppercase"
		_, err = projectsClient.Update(ctx, privatev1.ProjectsUpdateRequest_builder{
			Object: privatev1.Project_builder{
				Id: validProject.Object.Id,
				Metadata: privatev1.Metadata_builder{
					Name:   invalidName,
					Tenant: "my-tenant",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Updated Title",
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		// Should be rejected by message-level CEL validation
		Expect(status.Message()).To(ContainSubstring("validation failed"))
		Expect(status.Message()).To(ContainSubstring("project name must be empty (root project) or dot-separated DNS labels"))
	})

	It("Accepts partial Update with empty name not in mask (update_mask bypass)", func() {
		// Critical test for update_mask flow:
		// - Interceptor skips validation for Update requests
		// - Server merges partial request with DB object
		// - Server validates the merged result (not the partial request)

		// Create a valid project
		validProject, err := projectsClient.Create(ctx, privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "valid-project",
					Tenant: "my-tenant",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Original Title",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = projectsClient.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{
				Id: validProject.Object.Id,
			}.Build())
		})

		// Partial update: only update title, send minimal/invalid data for other fields
		// Name is empty (would fail validation), but it's NOT in the mask
		response, err := projectsClient.Update(ctx, privatev1.ProjectsUpdateRequest_builder{
			Object: privatev1.Project_builder{
				Id: validProject.Object.Id,
				Metadata: privatev1.Metadata_builder{
					Name: "", // Empty! Would fail validation if interceptor ran
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Updated Title",
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"spec.title"}, // Only updating title, NOT name
			},
		}.Build())

		// Should succeed because:
		// 1. Interceptor skips validation (has update_mask)
		// 2. Server merges: takes title from request, name from DB
		// 3. Server validates merged object (has valid name from DB)
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		Expect(response.Object.Spec.Title).To(Equal("Updated Title"))
		Expect(response.Object.Metadata.Name).To(Equal("valid-project")) // Unchanged from DB
	})

	It("Rejects Update with invalid name in mask (validates merged object)", func() {
		// Tests that server validation runs on the merged object:
		// - Client explicitly updates name to invalid value (in mask)
		// - Server merges and validates
		// - Validation rejects the invalid merged object

		// Create a valid project
		validProject, err := projectsClient.Create(ctx, privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "valid-project",
					Tenant: "my-tenant",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Original Title",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = projectsClient.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{
				Id: validProject.Object.Id,
			}.Build())
		})

		// Try to update name to invalid value WITH name in the mask
		invalidName := "Invalid-Name-With-Uppercase"
		_, err = projectsClient.Update(ctx, privatev1.ProjectsUpdateRequest_builder{
			Object: privatev1.Project_builder{
				Id: validProject.Object.Id,
				Metadata: privatev1.Metadata_builder{
					Name:   invalidName,
					Tenant: "my-tenant",
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"metadata.name"}, // Explicitly updating name
			},
		}.Build())

		// Should fail - server validates merged object with invalid name
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		// Should be rejected by message-level CEL validation
		Expect(status.Message()).To(ContainSubstring("validation failed"))
		Expect(status.Message()).To(ContainSubstring("project name must be empty (root project) or dot-separated DNS labels"))
	})

	It("Rejects Update with invalid label in mask (Go validation on labels)", func() {
		// Tests Go validation on labels works in Update flow

		// Create a valid tenant
		validTenant, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "update-labels-test",
					Labels: map[string]string{
						"key1": "value1",
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = tenantClient.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
				Id: validTenant.Object.Id,
			}.Build())
		})

		// Try to update with label key > 63 chars (DNS label limit)
		longKey := strings.Repeat("a", 70)

		_, err = tenantClient.Update(ctx, privatev1.TenantsUpdateRequest_builder{
			Object: privatev1.Tenant_builder{
				Id: validTenant.Object.Id,
				Metadata: privatev1.Metadata_builder{
					Labels: map[string]string{
						longKey: "value",
					},
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"metadata.labels"},
			},
		}.Build())

		// Should fail - Go DNS label validation (max 63 chars per segment)
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("metadata.labels"))
		Expect(status.Message()).To(ContainSubstring("must be between 1 and 63 characters long"))
	})
})
