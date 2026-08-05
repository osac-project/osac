package it

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/uuid"
)

var _ = Describe("Multitenancy authentication error handling", Label("multitenancy", "autherrors"), func() {
	DescribeTable(
		"Returns error when authenticating with invalid token",
		func(ctx context.Context, endpoint string) {
			Eventually(func(g Gomega) {
				// Prepare a request with an invalid token:
				request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
				g.Expect(err).ToNot(HaveOccurred())
				request.Header.Set("Authorization", "Bearer junk")

				// Send the request:
				response, err := tool.ExternalView().AnonymousClient().Do(request)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(response).ToNot(BeNil())
				defer response.Body.Close()

				// Verify that the request is unauthorized:
				g.Expect(response.StatusCode).To(Equal(http.StatusUnauthorized))

				// Verify that error is returned in the response body:
				body, err := io.ReadAll(response.Body)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(body).To(ContainSubstring("token is not valid"))
			}).Should(Succeed())
		},
		Entry("clusters", "/api/fulfillment/v1/clusters"),
		Entry("Cluster templates", "/api/fulfillment/v1/cluster_templates"),
		Entry("Host types", "/api/fulfillment/v1/host_types"),
	)

	DescribeTable(
		"Returns error when user is not authenticated",
		func(ctx context.Context, endpoint string) {
			Eventually(func(g Gomega) {
				// Prepare a request without a token:
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
				g.Expect(err).ToNot(HaveOccurred())

				// Send the request:
				resp, err := tool.ExternalView().AnonymousClient().Do(req)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(resp).ToNot(BeNil())
				defer resp.Body.Close()

				// Verify that the request is unauthorized:
				g.Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))

				// Verify that error is returned in the response body:
				body, err := io.ReadAll(resp.Body)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(body).To(ContainSubstring("requires authentication"))
			}).Should(Succeed())
		},
		Entry("Clusters", "/api/fulfillment/v1/clusters"),
		Entry("Cluster templates", "/api/fulfillment/v1/cluster_templates"),
		Entry("Host types", "/api/fulfillment/v1/host_types"),
	)
})

var _ = Describe("Multitenancy basic tenant isolation", Ordered, Label("multitenancy", "isolation"), func() {
	Describe("serviceaccount tenants", func() {
		var tenantUserMapping map[string][]string

		BeforeAll(func() {

			// Create map to track which users belong to which tenants
			tenantUserMapping = make(map[string][]string)

			Expect(len(ServiceAccountTenants)).To(BeNumerically(">", 1))

			// Populate map to track which users belong to which tenants
			for user, tenant := range ServiceAccountTenants {
				tenantUserMapping[tenant] = append(tenantUserMapping[tenant], user)
			}
		})

		Describe("cluster resources", func() {
			var tenantClusterMapping map[string][]string

			BeforeAll(func(ctx context.Context) {
				// Create map to track which clusters belong to which tenants
				tenantClusterMapping = make(map[string][]string)

				// Create host type for testing
				hostTypesClient := privatev1.NewHostTypesClient(tool.InternalView().AdminConn())
				hostTypeId := fmt.Sprintf("sa-isolation-hosttype-%s", uuid.New())
				_, err := hostTypesClient.Create(ctx, privatev1.HostTypesCreateRequest_builder{
					Object: privatev1.HostType_builder{
						Id: hostTypeId,
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(func(ctx context.Context) {
					_, err := hostTypesClient.Delete(ctx, privatev1.HostTypesDeleteRequest_builder{
						Id: hostTypeId,
					}.Build())
					Expect(err).ToNot(HaveOccurred())
				})

				// Create the tenants used by the tests:
				tenantsClient := privatev1.NewTenantsClient(tool.InternalView().AdminConn())
				for _, tenant := range ServiceAccountTenants {
					_, err := tenantsClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
						Object: privatev1.Tenant_builder{
							Metadata: privatev1.Metadata_builder{
								Name: tenant,
							}.Build(),
						}.Build(),
					}.Build())
					status, ok := grpcstatus.FromError(err)
					if ok && status.Code() == grpccodes.AlreadyExists {
						err = nil
					}
					Expect(err).ToNot(HaveOccurred())
				}

				// Create cluster template for testing
				templateId := fmt.Sprintf("sa-isolation-template-%s", uuid.New())
				templatesClient := privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())
				_, err = templatesClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
					Object: privatev1.ClusterTemplate_builder{
						Id: templateId,
						NodeSets: map[string]*privatev1.ClusterTemplateNodeSet{
							"my-node-set": privatev1.ClusterTemplateNodeSet_builder{
								HostType: hostTypeId,
								Size:     3,
							}.Build(),
						},
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(func(ctx context.Context) {
					_, err := templatesClient.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
						Id: templateId,
					}.Build())
					Expect(err).ToNot(HaveOccurred())
				})

				// Create a cluster object for each user
				for user, tenant := range ServiceAccountTenants {
					tokenSource, err := tool.makeKubernetesTokenSource(ctx, user, tenant)
					Expect(err).ToNot(HaveOccurred())
					conn, err := tool.makeGrpcConn(externalServiceAddr, tokenSource)
					Expect(err).ToNot(HaveOccurred())

					clustersClient := publicv1.NewClustersClient(conn)
					createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
						Object: publicv1.Cluster_builder{
							Spec: publicv1.ClusterSpec_builder{
								Template: templateId,
							}.Build(),
						}.Build(),
					}.Build())
					Expect(err).ToNot(HaveOccurred())
					clusterObject := createResponse.GetObject()

					DeferCleanup(func(ctx context.Context) {
						err := deleteCluster(ctx, clusterObject.GetId())
						Expect(err).ToNot(HaveOccurred())
					})

					// Populate map to track which clusters belong to which tenants
					tenantClusterMapping[tenant] = append(tenantClusterMapping[tenant], clusterObject.GetId())
				}
			})

			It("shared within the same tenant", func(ctx context.Context) {
				// List clusters for each user
				for user, tenant := range ServiceAccountTenants {
					tokenSource, err := tool.makeKubernetesTokenSource(ctx, user, tenant)
					Expect(err).ToNot(HaveOccurred())
					conn, err := tool.makeGrpcConn(externalServiceAddr, tokenSource)
					Expect(err).ToNot(HaveOccurred())

					response := listClusters(ctx, conn)
					Expect(response).ToNot(BeNil())
					Expect(response.Items).To(HaveLen(len(tenantUserMapping[tenant])))

					// Check that each cluster appears under expected tenant
					for _, cluster := range response.GetItems() {
						Expect(tenantClusterMapping[tenant]).To(ContainElement(cluster.GetId()))
					}
				}
			})

			It("isolated between tenants", func(ctx context.Context) {
				// List clusters for each user
				for user, tenant := range ServiceAccountTenants {
					tokenSource, err := tool.makeKubernetesTokenSource(ctx, user, tenant)
					Expect(err).ToNot(HaveOccurred())
					conn, err := tool.makeGrpcConn(externalServiceAddr, tokenSource)
					Expect(err).ToNot(HaveOccurred())

					response := listClusters(ctx, conn)
					Expect(response).ToNot(BeNil())
					Expect(response.Items).To(HaveLen(len(tenantUserMapping[tenant])))

					// Check that each cluster is isolated between tenants
					for _, cluster := range response.GetItems() {
						for _, otherTenant := range ServiceAccountTenants {
							if otherTenant != tenant {
								Expect(tenantClusterMapping[otherTenant]).ToNot(ContainElement(cluster.GetId()))
							}
						}
					}
				}
			})

			It("assigned the correct tenant after creation", func(ctx context.Context) {
				for tenant, clusters := range tenantClusterMapping {
					for _, cluster := range clusters {
						clustersClient := privatev1.NewClustersClient(tool.InternalView().AdminConn())

						clusterResponse, err := clustersClient.Get(ctx, privatev1.ClustersGetRequest_builder{
							Id: cluster,
						}.Build())
						Expect(err).ToNot(HaveOccurred())
						Expect(clusterResponse.GetObject().GetMetadata().GetTenant()).To(Equal(tenant))
					}
				}
			})

			DescribeTable(
				"cross-tenant",
				func(ctx context.Context, operation func(ctx context.Context, client publicv1.ClustersClient, clusterID string) error) {
					for clusterTenant, clusters := range tenantClusterMapping {
						for user, userTenant := range ServiceAccountTenants {
							// Skip if cluster is owned by the same tenant
							if clusterTenant == userTenant {
								continue
							}

							tokenSource, err := tool.makeKubernetesTokenSource(ctx, user, userTenant)
							Expect(err).ToNot(HaveOccurred())
							conn, err := tool.makeGrpcConn(externalServiceAddr, tokenSource)
							Expect(err).ToNot(HaveOccurred())

							clustersClient := publicv1.NewClustersClient(conn)

							for _, cluster := range clusters {
								err := operation(ctx, clustersClient, cluster)
								Expect(err).To(HaveOccurred())
								status, ok := grpcstatus.FromError(err)
								Expect(ok).To(BeTrue())
								Expect(status.Code()).To(Equal(grpccodes.NotFound))
							}
						}
					}

				},
				Entry(
					"Deletion is not allowed",
					func(ctx context.Context, client publicv1.ClustersClient, clusterID string) error {
						_, err := client.Delete(ctx, publicv1.ClustersDeleteRequest_builder{
							Id: clusterID,
						}.Build())

						return err
					},
				),
				Entry(
					"Update is not allowed",
					func(ctx context.Context, client publicv1.ClustersClient, clusterID string) error {
						_, err := client.Update(ctx, publicv1.ClustersUpdateRequest_builder{
							Object: publicv1.Cluster_builder{
								Id: clusterID,
								Spec: publicv1.ClusterSpec_builder{
									Template: "cross-tenant-update-template",
								}.Build(),
							}.Build(),
						}.Build())

						return err
					},
				),
			)
		})

	})

	Describe("OIDC tenants", func() {
		var tenantUserMapping map[string][]string

		BeforeAll(func(ctx context.Context) {
			// Create map to track which users belong to which tenants
			tenantUserMapping = make(map[string][]string)

			Expect(len(OIDCTenants)).To(BeNumerically(">", 1))

			// Populate map to track which users belong to which tenants
			for user, tenants := range OIDCTenants {
				for _, tenant := range tenants {
					tenantUserMapping[tenant] = append(tenantUserMapping[tenant], user)
				}
			}

			// Create the tenants used by the tests:
			tenantsClient := privatev1.NewTenantsClient(tool.InternalView().AdminConn())
			for tenant := range tenantUserMapping {
				_, err := tenantsClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
					Object: privatev1.Tenant_builder{
						Metadata: privatev1.Metadata_builder{
							Name: tenant,
						}.Build(),
					}.Build(),
				}.Build())
				status, ok := grpcstatus.FromError(err)
				if ok && status.Code() == grpccodes.AlreadyExists {
					err = nil
				}
				Expect(err).ToNot(HaveOccurred())
			}
			// Tenants are already created in tool setup (createTenants),
			// so we don't need to create them again here.
		})

		Describe("cluster resources", func() {
			var (
				tenantClusterMapping map[string][]string
				clusterTenantMapping map[string][]string
			)

			BeforeAll(func(ctx context.Context) {
				// Create map to track which clusters belong to which tenants
				tenantClusterMapping = make(map[string][]string)

				// Create map to track which clusters can be seen by which tenants
				clusterTenantMapping = make(map[string][]string)

				// Create the tenants used by the tests:
				tenantsClient := privatev1.NewTenantsClient(tool.InternalView().AdminConn())
				uniqueTenants := make(map[string]bool)
				for _, tenants := range OIDCTenants {
					for _, tenant := range tenants {
						uniqueTenants[tenant] = true
					}
				}
				for tenant := range uniqueTenants {
					_, err := tenantsClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
						Object: privatev1.Tenant_builder{
							Metadata: privatev1.Metadata_builder{
								Name: tenant,
							}.Build(),
						}.Build(),
					}.Build())
					status, ok := grpcstatus.FromError(err)
					if ok && status.Code() == grpccodes.AlreadyExists {
						err = nil
					}
					Expect(err).ToNot(HaveOccurred())
				}

				// Create host type for testing
				hostTypeId := fmt.Sprintf("oidc-isolation-hosttype-%s", uuid.New())
				hostTypesClient := privatev1.NewHostTypesClient(tool.InternalView().AdminConn())
				_, err := hostTypesClient.Create(ctx, privatev1.HostTypesCreateRequest_builder{
					Object: privatev1.HostType_builder{
						Id: hostTypeId,
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(func(ctx context.Context) {
					_, err := hostTypesClient.Delete(ctx, privatev1.HostTypesDeleteRequest_builder{
						Id: hostTypeId,
					}.Build())
					Expect(err).ToNot(HaveOccurred())
				})

				// Create cluster template for testing
				templateId := fmt.Sprintf("oidc-isolation-template-%s", uuid.New())
				templatesClient := privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())
				_, err = templatesClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
					Object: privatev1.ClusterTemplate_builder{
						Id: templateId,
						NodeSets: map[string]*privatev1.ClusterTemplateNodeSet{
							"my-node-set": privatev1.ClusterTemplateNodeSet_builder{
								HostType: hostTypeId,
								Size:     3,
							}.Build(),
						},
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(func(ctx context.Context) {
					_, err := templatesClient.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
						Id: templateId,
					}.Build())
					Expect(err).ToNot(HaveOccurred())
				})

				// Create a cluster object for each user
				for user := range OIDCTenants {
					tokenSource, err := tool.makeKeycloakTokenSource(ctx, user, usersPassword)
					Expect(err).ToNot(HaveOccurred())
					conn, err := tool.makeGrpcConn(externalServiceAddr, tokenSource)
					Expect(err).ToNot(HaveOccurred())

					clustersClient := publicv1.NewClustersClient(conn)
					createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
						Object: publicv1.Cluster_builder{
							Spec: publicv1.ClusterSpec_builder{
								Template: templateId,
							}.Build(),
						}.Build(),
					}.Build())
					Expect(err).ToNot(HaveOccurred())
					clusterObject := createResponse.GetObject()

					DeferCleanup(func(ctx context.Context) {
						err := deleteCluster(ctx, clusterObject.GetId())
						Expect(err).ToNot(HaveOccurred())
					})

					// Each object now has a single tenant; record the actual assigned tenant:
					tenant := clusterObject.GetMetadata().GetTenant()
					Expect(tenant).ToNot(BeEmpty())
					tenantClusterMapping[tenant] = append(tenantClusterMapping[tenant], clusterObject.GetId())
					clusterTenantMapping[clusterObject.GetId()] = []string{tenant}
				}
			})

			It("shared within the same tenant", func(ctx context.Context) {
				// List clusters for each user
				for user, tenants := range OIDCTenants {
					tokenSource, err := tool.makeKeycloakTokenSource(ctx, user, usersPassword)
					Expect(err).ToNot(HaveOccurred())
					conn, err := tool.makeGrpcConn(externalServiceAddr, tokenSource)
					Expect(err).ToNot(HaveOccurred())

					response := listClusters(ctx, conn)
					Expect(response).ToNot(BeNil())

					Expect(response.Items).To(HaveLen(calculateResponseSize(tenantClusterMapping, tenants)))

					// Check that each cluster appears under expected tenant
					for _, cluster := range response.GetItems() {
						Expect(checkTenantMembership(tenantClusterMapping, tenants, cluster.GetId())).To(BeTrue())
					}
				}
			})

			It("isolated between tenants", func(ctx context.Context) {
				// List clusters for each user
				for user, tenants := range OIDCTenants {
					tokenSource, err := tool.makeKeycloakTokenSource(ctx, user, usersPassword)
					Expect(err).ToNot(HaveOccurred())
					conn, err := tool.makeGrpcConn(externalServiceAddr, tokenSource)
					Expect(err).ToNot(HaveOccurred())

					response := listClusters(ctx, conn)
					Expect(response).ToNot(BeNil())
					Expect(response.Items).To(HaveLen(calculateResponseSize(tenantClusterMapping, tenants)))

					// Check that each cluster is isolated between tenants
					for _, cluster := range response.GetItems() {
						clusterId := cluster.GetId()
						expectedTenants := clusterTenantMapping[clusterId]

						// For each tenant, verify correct isolation
						for tenant, tenantClusters := range tenantClusterMapping {
							if slices.Contains(expectedTenants, tenant) {
								// Tenant should have access - verify cluster is in their list
								Expect(tenantClusters).To(ContainElement(clusterId))
							} else {
								// Tenant should NOT have access - verify cluster is isolated
								Expect(tenantClusters).ToNot(ContainElement(clusterId))
							}
						}
					}
				}
			})
		})

	})
})

func listClusters(ctx context.Context, conn *grpc.ClientConn) *publicv1.ClustersListResponse {
	clustersClient := publicv1.NewClustersClient(conn)
	response, err := clustersClient.List(ctx, publicv1.ClustersListRequest_builder{}.Build())
	Expect(err).ToNot(HaveOccurred(), "error listing clusters")

	return response
}

func deleteCluster(ctx context.Context, clusterId string) error {
	clusterClient := privatev1.NewClustersClient(tool.InternalView().AdminConn())
	_, err := clusterClient.Delete(ctx, privatev1.ClustersDeleteRequest_builder{
		Id: clusterId,
	}.Build())
	return err
}

// calculateResponseSize calculates the response size based on a tenant-resource mapping and a user's tenant membership
// The response size is the number of the distinct objects visible by all of the user's tenants
func calculateResponseSize(mapping map[string][]string, tenants []string) int {
	seenResources := make(map[string]bool)
	for _, tenant := range tenants {
		for _, resource := range mapping[tenant] {
			seenResources[resource] = true
		}
	}
	return len(seenResources)
}

// checkTenantMembership checks if a resource is visible to a user based on their tenant membership
// The resource is visible if it is seen by any of the user's tenants
func checkTenantMembership(tenantResourceMapping map[string][]string, tenants []string, resourceId string) bool {
	for _, tenant := range tenants {
		objects := tenantResourceMapping[tenant]
		if slices.Contains(objects, resourceId) {
			return true
		}
	}
	return false
}
