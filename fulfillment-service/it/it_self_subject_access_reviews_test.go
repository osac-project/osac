/*
Copyright (c) 2026 Red Hat Inc.

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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("SelfSubjectAccessReview", func() {
	var (
		ctx                       context.Context
		systemAdminReviewClient   publicv1.SelfSubjectAccessReviewsClient
		systemAdminClustersClient publicv1.ClustersClient
		tenantAdminConn           *grpc.ClientConn
		normalUserConn            *grpc.ClientConn
		tenantAdminReviewClient   publicv1.SelfSubjectAccessReviewsClient
		tenantAdminClustersClient publicv1.ClustersClient
		normalUserReviewClient    publicv1.SelfSubjectAccessReviewsClient
		tenantName                string
		templateID                string
		hostTypeID                string
	)

	BeforeEach(func() {
		var err error
		ctx = context.Background()

		systemAdminReviewClient = publicv1.NewSelfSubjectAccessReviewsClient(tool.InternalView().AdminConn())
		systemAdminClustersClient = publicv1.NewClustersClient(tool.InternalView().AdminConn())

		// Create user connections
		tenantAdminConn, err = tool.MakeUserConnection(ctx, "adam", "password")
		Expect(err).ToNot(HaveOccurred())

		normalUserConn, err = tool.MakeUserConnection(ctx, "ben", "password")
		Expect(err).ToNot(HaveOccurred())

		tenantAdminReviewClient = publicv1.NewSelfSubjectAccessReviewsClient(tenantAdminConn)
		tenantAdminClustersClient = publicv1.NewClustersClient(tenantAdminConn)
		normalUserReviewClient = publicv1.NewSelfSubjectAccessReviewsClient(normalUserConn)

		tenantName = "engineering"

		// Create host type
		templateClient := privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())
		hostTypeClient := privatev1.NewHostTypesClient(tool.InternalView().AdminConn())

		hostTypeID = fmt.Sprintf("host-type-%s", uuid.New())
		_, err = hostTypeClient.Create(ctx, privatev1.HostTypesCreateRequest_builder{
			Object: privatev1.HostType_builder{
				Id: hostTypeID,
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("host-type-%s", uuid.New()[24:32]),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Create template
		templateID = fmt.Sprintf("template-%s", uuid.New())
		_, err = templateClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id: templateID,
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("template-%s", uuid.New()[24:32]),
				}.Build(),
				Title:       "Test Template",
				Description: "Test template for integration tests",
				NodeSets: map[string]*privatev1.ClusterTemplateNodeSet{
					"workers": privatev1.ClusterTemplateNodeSet_builder{
						HostType: privatev1.HostTypeReference_builder{Id: hostTypeID}.Build(),
						Size:     3,
					}.Build(),
				},
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Authorization Consistency", func() {
		Context("System Admin user", func() {
			It("should return allowed=true for Create permission check and succeed on actual Create", func() {
				// Check permission
				reviewResp, err := systemAdminReviewClient.Create(ctx, publicv1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: publicv1.SelfSubjectAccessReview_builder{
						Metadata: publicv1.Metadata_builder{
							Name:   fmt.Sprintf("create-cluster-%s", uuid.New()[24:32]),
							Tenant: tenantName,
						}.Build(),
						Spec: publicv1.SelfSubjectAccessReviewSpec_builder{
							Service: "osac.public.v1.Clusters",
							Method:  "Create",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(reviewResp.GetObject().GetStatus().GetAllowed()).To(BeTrue())

				// Verify actual operation succeeds
				clusterName := fmt.Sprintf("test-cluster-%s", uuid.New()[24:32])
				createResp, err := systemAdminClustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
					Object: publicv1.Cluster_builder{
						Metadata: publicv1.Metadata_builder{
							Name: clusterName,
						}.Build(),
						Spec: publicv1.ClusterSpec_builder{
							Template: publicv1.ClusterTemplateReference_builder{Id: templateID}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(func() {
					_, _ = systemAdminClustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{
						Id: createResp.GetObject().GetId(),
					}.Build())
				})
			})

			It("should return allowed=true for List permission check and succeed on actual List", func() {
				// Check permission
				reviewResp, err := systemAdminReviewClient.Create(ctx, publicv1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: publicv1.SelfSubjectAccessReview_builder{
						Metadata: publicv1.Metadata_builder{
							Name:   fmt.Sprintf("list-cluster-%s", uuid.New()[24:32]),
							Tenant: tenantName,
						}.Build(),
						Spec: publicv1.SelfSubjectAccessReviewSpec_builder{
							Service: "osac.public.v1.Clusters",
							Method:  "List",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(reviewResp.GetObject().GetStatus().GetAllowed()).To(BeTrue())

				// Verify actual operation succeeds
				_, err = systemAdminClustersClient.List(ctx, publicv1.ClustersListRequest_builder{}.Build())
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("Tenant Admin user", func() {
			It("should return allowed=true for Create in member tenant and succeed on actual Create", func() {
				// Check permission for member tenant
				reviewResp, err := tenantAdminReviewClient.Create(ctx, publicv1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: publicv1.SelfSubjectAccessReview_builder{
						Metadata: publicv1.Metadata_builder{
							Name:   fmt.Sprintf("create-cluster-%s", uuid.New()[24:32]),
							Tenant: tenantName,
						}.Build(),
						Spec: publicv1.SelfSubjectAccessReviewSpec_builder{
							Service: "osac.public.v1.Clusters",
							Method:  "Create",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(reviewResp.GetObject().GetStatus().GetAllowed()).To(BeTrue())

				// Verify actual operation succeeds
				clusterName := fmt.Sprintf("test-cluster-%s", uuid.New()[24:32])
				createResp, err := tenantAdminClustersClient.Create(
					ctx,
					publicv1.ClustersCreateRequest_builder{
						Object: publicv1.Cluster_builder{
							Metadata: publicv1.Metadata_builder{
								Name:   clusterName,
								Tenant: tenantName,
							}.Build(),
							Spec: publicv1.ClusterSpec_builder{
								Template: publicv1.ClusterTemplateReference_builder{Id: templateID}.Build(),
							}.Build(),
						}.Build(),
					}.Build(),
				)
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(func() {
					_, _ = tenantAdminClustersClient.Delete(
						ctx,
						publicv1.ClustersDeleteRequest_builder{
							Id: createResp.GetObject().GetId(),
						}.Build(),
					)
				})
			})

			It("should return allowed=false for Create in non-member tenant and fail on actual Create", func() {
				// Check permission for non-member tenant
				reviewResp, err := tenantAdminReviewClient.Create(ctx, publicv1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: publicv1.SelfSubjectAccessReview_builder{
						Metadata: publicv1.Metadata_builder{
							Name:   fmt.Sprintf("create-cluster-%s", uuid.New()[24:32]),
							Tenant: "development", // User does not belong to tenant 'development'
						}.Build(),
						Spec: publicv1.SelfSubjectAccessReviewSpec_builder{
							Service: "osac.public.v1.Clusters",
							Method:  "Create",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(reviewResp.GetObject().GetStatus().GetAllowed()).To(BeFalse())

				// Verify actual operation fails
				clusterName := fmt.Sprintf("test-cluster-%s", uuid.New()[24:32])
				_, err = publicv1.NewClustersClient(tool.ExternalView().UserConn()).Create(
					ctx,
					publicv1.ClustersCreateRequest_builder{
						Object: publicv1.Cluster_builder{
							Metadata: publicv1.Metadata_builder{
								Name:   clusterName,
								Tenant: "b",
							}.Build(),
							Spec: publicv1.ClusterSpec_builder{
								Template: publicv1.ClusterTemplateReference_builder{Id: templateID}.Build(),
							}.Build(),
						}.Build(),
					}.Build(),
				)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
			})
		})

	})

	Describe("Service and Method Validation", func() {
		It("should support all standard methods for Clusters service", func() {
			methods := []string{"Create", "Get", "List", "Update", "Delete"}
			for _, method := range methods {
				reviewResp, err := systemAdminReviewClient.Create(ctx, publicv1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: publicv1.SelfSubjectAccessReview_builder{
						Metadata: publicv1.Metadata_builder{
							Name:   fmt.Sprintf("create-cluster-%s", uuid.New()[24:32]),
							Tenant: tenantName,
						}.Build(),
						Spec: publicv1.SelfSubjectAccessReviewSpec_builder{
							Service: "osac.public.v1.Clusters",
							Method:  method,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred(), "Method %s should be valid", method)
				// Admin should have permission for all methods
				Expect(reviewResp.GetObject().GetStatus().GetAllowed()).To(BeTrue(),
					"Admin should have permission for %s", method)
			}
		})

		It("should reject Create for ExternalIPPools (read-only service)", func() {
			_, err := systemAdminReviewClient.Create(ctx, publicv1.SelfSubjectAccessReviewsCreateRequest_builder{
				Object: publicv1.SelfSubjectAccessReview_builder{
					Metadata: publicv1.Metadata_builder{
						Name:   fmt.Sprintf("create-external-ip-pool-%s", uuid.New()[24:32]),
						Tenant: tenantName,
					}.Build(),
					Spec: publicv1.SelfSubjectAccessReviewSpec_builder{
						Service: "osac.public.v1.ExternalIPPools",
						Method:  "Create",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("method Create not supported for service osac.public.v1.ExternalIPPools"))
		})

		It("should accept Get and List for ExternalIPPools", func() {
			methods := []string{"Get", "List"}
			for _, method := range methods {
				reviewResp, err := systemAdminReviewClient.Create(ctx, publicv1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: publicv1.SelfSubjectAccessReview_builder{
						Metadata: publicv1.Metadata_builder{
							Name:   fmt.Sprintf("create-external-ip-pool-%s", uuid.New()[24:32]),
							Tenant: tenantName,
						}.Build(),
						Spec: publicv1.SelfSubjectAccessReviewSpec_builder{
							Service: "osac.public.v1.ExternalIPPools",
							Method:  method,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred(), "Method %s should be valid for ExternalIPPools", method)
				Expect(reviewResp.GetObject().GetStatus()).ToNot(BeNil())
			}
		})
	})

	Describe("Input Validation", func() {
		It("should reject unknown service", func() {
			_, err := systemAdminReviewClient.Create(ctx, publicv1.SelfSubjectAccessReviewsCreateRequest_builder{
				Object: publicv1.SelfSubjectAccessReview_builder{
					Metadata: publicv1.Metadata_builder{
						Name:   fmt.Sprintf("create-non-existent-service-%s", uuid.New()[24:32]),
						Tenant: tenantName,
					}.Build(),
					Spec: publicv1.SelfSubjectAccessReviewSpec_builder{
						Service: "osac.public.v1.NonExistentService",
						Method:  "Create",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("unknown service: osac.public.v1.NonExistentService"))
		})

		It("should reject invalid method for a valid service", func() {
			_, err := systemAdminReviewClient.Create(ctx, publicv1.SelfSubjectAccessReviewsCreateRequest_builder{
				Object: publicv1.SelfSubjectAccessReview_builder{
					Metadata: publicv1.Metadata_builder{
						Name:   fmt.Sprintf("create-invalid-method-%s", uuid.New()[24:32]),
						Tenant: tenantName,
					}.Build(),
					Spec: publicv1.SelfSubjectAccessReviewSpec_builder{
						Service: "osac.public.v1.Clusters",
						Method:  "InvalidMethod",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("method InvalidMethod not supported for service osac.public.v1.Clusters"))
		})

		It("should reject empty service", func() {
			_, err := systemAdminReviewClient.Create(ctx, publicv1.SelfSubjectAccessReviewsCreateRequest_builder{
				Object: publicv1.SelfSubjectAccessReview_builder{
					Metadata: publicv1.Metadata_builder{
						Name:   fmt.Sprintf("create-empty-service-%s", uuid.New()[24:32]),
						Tenant: tenantName,
					}.Build(),
					Spec: publicv1.SelfSubjectAccessReviewSpec_builder{
						Service: "",
						Method:  "Create",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		})

		It("should reject empty method", func() {
			_, err := systemAdminReviewClient.Create(ctx, publicv1.SelfSubjectAccessReviewsCreateRequest_builder{
				Object: publicv1.SelfSubjectAccessReview_builder{
					Metadata: publicv1.Metadata_builder{
						Name:   fmt.Sprintf("create-empty-method-%s", uuid.New()[24:32]),
						Tenant: tenantName,
					}.Build(),
					Spec: publicv1.SelfSubjectAccessReviewSpec_builder{
						Service: "osac.public.v1.Clusters",
						Method:  "",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		})
	})

	Describe("Information Disclosure Prevention", func() {
		It("should not reveal tenant names in denial reason", func() {
			// User from tenant 'a' checks permission on tenant 'b'
			reviewResp, err := normalUserReviewClient.Create(ctx, publicv1.SelfSubjectAccessReviewsCreateRequest_builder{
				Object: publicv1.SelfSubjectAccessReview_builder{
					Metadata: publicv1.Metadata_builder{
						Name:   fmt.Sprintf("create-non-member-tenant-%s", uuid.New()[24:32]),
						Tenant: "b", // Non-member tenant
					}.Build(),
					Spec: publicv1.SelfSubjectAccessReviewSpec_builder{
						Service: "osac.public.v1.VirtualNetworks",
						Method:  "Create",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(reviewResp.GetObject().GetStatus().GetAllowed()).To(BeFalse())

			// Reason should NOT contain the tenant name 'b'
			reason := reviewResp.GetObject().GetStatus().GetReason()
			Expect(reason).ToNot(ContainSubstring("b"))
			Expect(reason).ToNot(ContainSubstring("tenant b"))
			Expect(reason).ToNot(ContainSubstring("user is not a member of tenant b"))
		})

		It("should not reveal tenant names in actual operation errors", func() {
			// Verify actual operation error also doesn't reveal tenant name
			vnetName := fmt.Sprintf("test-vnet-%s", uuid.New()[24:32])
			_, err := publicv1.NewVirtualNetworksClient(tool.ExternalView().UserConn()).Create(
				ctx,
				publicv1.VirtualNetworksCreateRequest_builder{
					Object: publicv1.VirtualNetwork_builder{
						Metadata: publicv1.Metadata_builder{
							Name:   vnetName,
							Tenant: "b",
						}.Build(),
						Spec: publicv1.VirtualNetworkSpec_builder{
							Ipv4Cidr: new("10.0.0.0/16"),
						}.Build(),
					}.Build(),
				}.Build(),
			)
			Expect(err).To(HaveOccurred())

			// Error message should NOT reveal the tenant name
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Message()).ToNot(ContainSubstring("b"))
			Expect(status.Message()).ToNot(ContainSubstring("tenant b"))
		})
	})

	Describe("Unauthenticated Requests", func() {
		It("should reject requests without authentication", func() {
			// Use anonymous connection
			anonymousClient := publicv1.NewSelfSubjectAccessReviewsClient(tool.ExternalView().AnonymousConn())
			_, err := anonymousClient.Create(ctx, publicv1.SelfSubjectAccessReviewsCreateRequest_builder{
				Object: publicv1.SelfSubjectAccessReview_builder{
					Metadata: publicv1.Metadata_builder{
						Name:   fmt.Sprintf("create-unauthenticated-%s", uuid.New()[24:32]),
						Tenant: tenantName,
					}.Build(),
					Spec: publicv1.SelfSubjectAccessReviewSpec_builder{
						Service: "osac.public.v1.Clusters",
						Method:  "Create",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.Unauthenticated))
		})
	})

	Describe("Multiple Services Coverage", func() {
		It("should check permissions across different public services", func() {
			services := []struct {
				name   string
				method string
			}{
				{"osac.public.v1.Clusters", "Create"},
				{"osac.public.v1.ComputeInstances", "List"},
				{"osac.public.v1.VirtualNetworks", "Get"},
				{"osac.public.v1.StorageTiers", "List"},
				{"osac.public.v1.BareMetalInstances", "Create"},
				{"osac.public.v1.Secrets", "Update"},
				{"osac.public.v1.ExternalIPPools", "List"},
			}

			for _, svc := range services {
				reviewResp, err := systemAdminReviewClient.Create(ctx, publicv1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: publicv1.SelfSubjectAccessReview_builder{
						Metadata: publicv1.Metadata_builder{
							Name:   fmt.Sprintf("create-service-%s", uuid.New()[24:32]),
							Tenant: tenantName,
						}.Build(),
						Spec: publicv1.SelfSubjectAccessReviewSpec_builder{
							Service: svc.name,
							Method:  svc.method,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred(), "Should accept service %s with method %s", svc.name, svc.method)
				Expect(reviewResp.GetObject().GetStatus()).ToNot(BeNil())
			}
		})

		It("should reject private services", func() {
			// Private services are not supported - only osac.public.v1.* services
			_, err := systemAdminReviewClient.Create(ctx, publicv1.SelfSubjectAccessReviewsCreateRequest_builder{
				Object: publicv1.SelfSubjectAccessReview_builder{
					Metadata: publicv1.Metadata_builder{
						Name:   fmt.Sprintf("create-private-service-%s", uuid.New()[24:32]),
						Tenant: tenantName,
					}.Build(),
					Spec: publicv1.SelfSubjectAccessReviewSpec_builder{
						Service: "osac.private.v1.Tenants",
						Method:  "List",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		})
	})
})
