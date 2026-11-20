package it

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/service/start/mcpserver"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("MCP catalog offering administration", func() {
	It("creates, updates, publishes, and deletes as the authorized caller", func(ctx context.Context) {
		tenant, _, tokenSource := createMCPTenantAdmin(ctx)
		publicConn, err := tool.makeGrpcConn(externalServiceAddr, tokenSource)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(publicConn.Close()).To(Succeed()) })
		offerings := publicv1.NewComputeInstanceCatalogItemsClient(publicConn)

		httpServer := startInProcessMCPServer(ctx, "fulfillment-mcp-catalog-it")
		session := connectMCPTokenSource(ctx, httpServer.URL, "tenant-admin", tokenSource)
		DeferCleanup(func() { Expect(session.Close()).To(Succeed()) })

		templateID := createCatalogItemComputeInstanceTemplateFixture(ctx, nil, nil)
		created, err := callMCPTool[mcpserver.CatalogOfferingOutput](
			ctx, session, "create_catalog_offering", mcpserver.CreateCatalogOfferingInput{
				Kind:        mcpserver.CatalogOfferingKindComputeInstance,
				Name:        catalogItemFixtureName(),
				Title:       "Original offering",
				Description: "Original description",
				TemplateID:  templateID,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(created.Kind).To(Equal("compute_instance"))
		Expect(created.ID).ToNot(BeEmpty())
		Expect(created.Name).ToNot(BeEmpty())
		Expect(created.Version).To(BeNumerically(">=", 0))
		Expect(created.Published).To(BeFalse())

		adminOfferings := privatev1.NewComputeInstanceCatalogItemsClient(tool.InternalView().AdminConn())
		deferCatalogItemFixtureDeletion(
			func(ctx context.Context) error {
				_, err := adminOfferings.Delete(ctx, privatev1.ComputeInstanceCatalogItemsDeleteRequest_builder{
					Id: created.ID,
				}.Build())
				return err
			},
			func(ctx context.Context) (bool, error) {
				_, err := offerings.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{
					Id: created.ID,
				}.Build())
				if status.Code(err) == codes.NotFound {
					return true, nil
				}
				return false, err
			},
		)

		initial, err := offerings.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{
			Id: created.ID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(initial.GetObject().GetMetadata().GetTenant()).To(Equal(tenant))
		Expect(initial.GetObject().GetPublished()).To(BeFalse())

		unauthorizedTokenSource, err := tool.makeKeycloakTokenSource(ctx, "ben", usersPassword)
		Expect(err).ToNot(HaveOccurred())
		unauthorizedSession := connectMCPTokenSource(
			ctx, httpServer.URL, "unauthorized-catalog", unauthorizedTokenSource,
		)
		DeferCleanup(func() { Expect(unauthorizedSession.Close()).To(Succeed()) })
		_, err = callMCPTool[mcpserver.CatalogOfferingOutput](
			ctx, unauthorizedSession, "update_catalog_offering", mcpserver.UpdateCatalogOfferingInput{
				Kind:    mcpserver.CatalogOfferingKindComputeInstance,
				ID:      created.ID,
				Version: initial.GetObject().GetMetadata().GetVersion(),
				Title:   projectStringPointer("Should not persist"),
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`"category":"authorization"`))
		Expect(err.Error()).To(ContainSubstring(`"grpc_code":"PermissionDenied"`))

		afterDenial, err := offerings.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{
			Id: created.ID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(afterDenial.GetObject().GetTitle()).To(Equal("Original offering"))
		Expect(afterDenial.GetObject().GetMetadata().GetVersion()).To(Equal(
			initial.GetObject().GetMetadata().GetVersion(),
		))

		updated, err := callMCPTool[mcpserver.CatalogOfferingOutput](
			ctx, session, "update_catalog_offering", mcpserver.UpdateCatalogOfferingInput{
				Kind:    mcpserver.CatalogOfferingKindComputeInstance,
				ID:      created.ID,
				Version: afterDenial.GetObject().GetMetadata().GetVersion(),
				Title:   projectStringPointer("Updated offering"),
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(updated.Version).To(BeNumerically(">", afterDenial.GetObject().GetMetadata().GetVersion()))

		afterUpdate, err := offerings.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{
			Id: created.ID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(afterUpdate.GetObject().GetTitle()).To(Equal("Updated offering"))
		Expect(afterUpdate.GetObject().GetDescription()).To(Equal("Original description"))

		_, err = callMCPTool[mcpserver.CatalogOfferingOutput](
			ctx, unauthorizedSession, "publish_catalog_offering", mcpserver.PublishCatalogOfferingInput{
				Kind:    mcpserver.CatalogOfferingKindComputeInstance,
				ID:      created.ID,
				Version: updated.Version,
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`"category":"authorization"`))
		Expect(err.Error()).To(ContainSubstring(`"grpc_code":"PermissionDenied"`))

		beforeAuthorizedPublish, err := offerings.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{
			Id: created.ID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(beforeAuthorizedPublish.GetObject().GetPublished()).To(BeFalse())

		published, err := callMCPTool[mcpserver.CatalogOfferingOutput](
			ctx, session, "publish_catalog_offering", mcpserver.PublishCatalogOfferingInput{
				Kind:    mcpserver.CatalogOfferingKindComputeInstance,
				ID:      created.ID,
				Version: updated.Version,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(published.Published).To(BeTrue())

		afterPublish, err := offerings.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{
			Id: created.ID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(afterPublish.GetObject().GetPublished()).To(BeTrue())

		deleted, err := callMCPTool[mcpserver.DeleteCatalogOfferingOutput](
			ctx, session, "delete_catalog_offering", mcpserver.DeleteCatalogOfferingInput{
				Kind: mcpserver.CatalogOfferingKindComputeInstance,
				ID:   created.ID,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleted).To(Equal(mcpserver.DeleteCatalogOfferingOutput{
			Kind: "compute_instance", ID: created.ID,
		}))

		_, err = offerings.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{
			Id: created.ID,
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.NotFound))
	})

	It("isolates ComputeInstance offering mutations between tenant admins", func(ctx context.Context) {
		templateID := createCatalogItemComputeInstanceTemplateFixture(ctx, nil, nil)
		assertMcpCatalogOfferingTenantIsolation(
			ctx, mcpserver.CatalogOfferingKindComputeInstance, templateID,
		)
	})

	It("isolates Cluster offering mutations between tenant admins", func(ctx context.Context) {
		templateID := createCatalogItemClusterTemplateFixture(ctx, nil, nil)
		assertMcpCatalogOfferingTenantIsolation(ctx, mcpserver.CatalogOfferingKindCluster, templateID)
	})

	It("isolates BareMetalInstance offering mutations between tenant admins", func(ctx context.Context) {
		requireMCPBareMetalCatalogEnabled(ctx)
		templateID := createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, nil)
		assertMcpCatalogOfferingTenantIsolation(
			ctx, mcpserver.CatalogOfferingKindBareMetalInstance, templateID,
		)
	})

	It("reads current Cluster versions for stale retries", func(ctx context.Context) {
		_, _, tokenSource := createMCPTenantAdmin(ctx)
		publicConn, err := tool.makeGrpcConn(externalServiceAddr, tokenSource)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(publicConn.Close()).To(Succeed()) })
		clusters := publicv1.NewClusterCatalogItemsClient(publicConn)

		httpServer := startInProcessMCPServer(ctx, "fulfillment-mcp-catalog-read-it")
		session := connectMCPTokenSource(ctx, httpServer.URL, "tenant-admin", tokenSource)
		DeferCleanup(func() { Expect(session.Close()).To(Succeed()) })

		clusterTemplateID := createCatalogItemClusterTemplateFixture(ctx, nil, nil)
		clusterTemplates, err := callMCPTool[mcpserver.ListResourcesOutput](
			ctx, session, "list_resources", mcpserver.ListResourcesInput{
				ResourceType: mcpserver.ResourceTypeClusterTemplate,
				Filter:       fmt.Sprintf("this.id == %q", clusterTemplateID),
				PageSize:     1,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(clusterTemplates.Items).To(HaveLen(1))
		Expect(clusterTemplates.Items[0].ID).To(Equal(clusterTemplateID))

		clusterTemplate, err := callMCPTool[mcpserver.GetResourceOutput](
			ctx, session, "get_resource", mcpserver.GetResourceInput{
				ResourceType: mcpserver.ResourceTypeClusterTemplate,
				ID:           clusterTemplateID,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(clusterTemplate.Resource).To(SatisfyAll(
			HaveLen(3),
			HaveKeyWithValue("id", clusterTemplateID),
		))

		clusterItem := createClusterCatalogItemFixture(ctx, publicConn, publicv1.ClusterCatalogItem_builder{
			Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
			Template: publicv1.ClusterTemplateReference_builder{Id: clusterTemplateID}.Build(),
			Title:    "Original cluster offering",
		}.Build())

		clusterBefore, err := clusters.Update(ctx, publicv1.ClusterCatalogItemsUpdateRequest_builder{
			Object: publicv1.ClusterCatalogItem_builder{
				Id: clusterItem.GetId(),
				Metadata: publicv1.Metadata_builder{
					Version: clusterItem.GetMetadata().GetVersion(),
				}.Build(),
				Title: "Updated outside MCP",
			}.Build(),
			UpdateMask: catalogItemUpdateMask("title"),
			Lock:       true,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(clusterBefore.GetObject().GetMetadata().GetVersion()).To(BeNumerically(">", clusterItem.GetMetadata().GetVersion()))

		_, err = callMCPTool[mcpserver.CatalogOfferingOutput](
			ctx, session, "update_catalog_offering", mcpserver.UpdateCatalogOfferingInput{
				Kind:    mcpserver.CatalogOfferingKindCluster,
				ID:      clusterItem.GetId(),
				Version: clusterItem.GetMetadata().GetVersion(),
				Title:   projectStringPointer("Stale cluster update"),
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`"category":"conflict"`))
		Expect(err.Error()).To(ContainSubstring(`"grpc_code":"Aborted"`))

		clusterGet, err := callMCPTool[mcpserver.GetResourceOutput](
			ctx, session, "get_resource", mcpserver.GetResourceInput{
				ResourceType: mcpserver.ResourceTypeClusterCatalogItem,
				ID:           clusterItem.GetId(),
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(clusterGet.Resource).To(HaveKeyWithValue("version", float64(
			clusterBefore.GetObject().GetMetadata().GetVersion(),
		)))
		Expect(clusterGet.Resource).To(HaveLen(4))
		Expect(clusterGet.Resource).ToNot(HaveKey("title"))

		clusterList, err := callMCPTool[mcpserver.ListResourcesOutput](
			ctx, session, "list_resources", mcpserver.ListResourcesInput{
				ResourceType: mcpserver.ResourceTypeClusterCatalogItem,
				Filter:       fmt.Sprintf("this.id == %q", clusterItem.GetId()),
				PageSize:     1,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(clusterList.Items).To(ConsistOf(mcpserver.ResourceSummary{
			ID:      clusterItem.GetId(),
			Name:    clusterItem.GetMetadata().GetName(),
			Version: clusterBefore.GetObject().GetMetadata().GetVersion(),
			State:   "unpublished",
		}))
		clusterAfterStale, err := clusters.Get(ctx, publicv1.ClusterCatalogItemsGetRequest_builder{
			Id: clusterItem.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(clusterAfterStale.GetObject().GetTitle()).To(Equal("Updated outside MCP"))
		Expect(clusterAfterStale.GetObject().GetMetadata().GetVersion()).To(Equal(
			clusterBefore.GetObject().GetMetadata().GetVersion(),
		))

		clusterRetry, err := callMCPTool[mcpserver.CatalogOfferingOutput](
			ctx, session, "update_catalog_offering", mcpserver.UpdateCatalogOfferingInput{
				Kind:    mcpserver.CatalogOfferingKindCluster,
				ID:      clusterItem.GetId(),
				Version: clusterBefore.GetObject().GetMetadata().GetVersion(),
				Title:   projectStringPointer("Cluster retry succeeded"),
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(clusterRetry.Version).To(BeNumerically(">", clusterBefore.GetObject().GetMetadata().GetVersion()))

		unauthorizedTokenSource, err := tool.makeKeycloakTokenSource(ctx, "ben", usersPassword)
		Expect(err).ToNot(HaveOccurred())
		unauthorizedSession := connectMCPTokenSource(
			ctx, httpServer.URL, "foreign-catalog-reader", unauthorizedTokenSource,
		)
		DeferCleanup(func() { Expect(unauthorizedSession.Close()).To(Succeed()) })

		for _, offering := range []struct {
			resourceType mcpserver.ResourceType
			id           string
		}{
			{mcpserver.ResourceTypeClusterCatalogItem, clusterItem.GetId()},
		} {
			_, err := callMCPTool[mcpserver.GetResourceOutput](
				ctx, unauthorizedSession, "get_resource", mcpserver.GetResourceInput{
					ResourceType: offering.resourceType,
					ID:           offering.id,
				},
			)
			expectSafeMcpCatalogOfferingDenial(err, offering.id)

			listed, err := callMCPTool[mcpserver.ListResourcesOutput](
				ctx, unauthorizedSession, "list_resources", mcpserver.ListResourcesInput{
					ResourceType: offering.resourceType,
					Filter:       fmt.Sprintf("this.id == %q", offering.id),
					PageSize:     1,
				},
			)
			if err != nil {
				expectSafeMcpCatalogOfferingDenial(err, offering.id)
			} else {
				Expect(listed.Total).To(Equal(int32(0)))
				Expect(listed.Items).To(BeEmpty())
			}
		}
	})

	It("reads current BareMetalInstance versions for stale retries", func(ctx context.Context) {
		_, _, tokenSource := createMCPTenantAdmin(ctx)
		publicConn, err := tool.makeGrpcConn(externalServiceAddr, tokenSource)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(publicConn.Close()).To(Succeed()) })
		bareMetal := publicv1.NewBareMetalInstanceCatalogItemsClient(publicConn)

		one := int32(1)
		_, err = bareMetal.List(ctx, publicv1.BareMetalInstanceCatalogItemsListRequest_builder{
			Limit: &one,
		}.Build())
		if status.Code(err) == codes.Unavailable && status.Convert(err).Message() ==
			"the BMaaS service is not enabled on this server" {
			Skip("BMaaS is disabled on this Fulfillment server")
		}
		Expect(err).ToNot(HaveOccurred())

		httpServer := startInProcessMCPServer(ctx, "fulfillment-mcp-bare-metal-read-it")
		session := connectMCPTokenSource(ctx, httpServer.URL, "tenant-admin", tokenSource)
		DeferCleanup(func() { Expect(session.Close()).To(Succeed()) })

		bareMetalTemplateID := createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, nil)
		bareMetalTemplates := publicv1.NewBareMetalInstanceTemplatesClient(publicConn)
		bareMetalTemplateResponse, err := bareMetalTemplates.Get(
			ctx, publicv1.BareMetalInstanceTemplatesGetRequest_builder{
				Id: bareMetalTemplateID,
			}.Build(),
		)
		Expect(err).ToNot(HaveOccurred())
		bareMetalTemplateMetadata := bareMetalTemplateResponse.GetObject().GetMetadata()

		listedTemplates, err := callMCPTool[mcpserver.ListResourcesOutput](
			ctx, session, "list_resources", mcpserver.ListResourcesInput{
				ResourceType: mcpserver.ResourceTypeBareMetalInstanceTemplate,
				Filter:       fmt.Sprintf("this.id == %q", bareMetalTemplateID),
				PageSize:     1,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(listedTemplates.Items).To(ConsistOf(mcpserver.ResourceSummary{
			ID:      bareMetalTemplateID,
			Name:    bareMetalTemplateMetadata.GetName(),
			Version: bareMetalTemplateMetadata.GetVersion(),
		}))

		bareMetalTemplate, err := callMCPTool[mcpserver.GetResourceOutput](
			ctx, session, "get_resource", mcpserver.GetResourceInput{
				ResourceType: mcpserver.ResourceTypeBareMetalInstanceTemplate,
				ID:           bareMetalTemplateID,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(bareMetalTemplate.Resource).To(Equal(map[string]any{
			"id":      bareMetalTemplateID,
			"name":    bareMetalTemplateMetadata.GetName(),
			"version": float64(bareMetalTemplateMetadata.GetVersion()),
		}))

		bareMetalItem := createBareMetalInstanceCatalogItemFixture(
			ctx, publicConn, publicv1.BareMetalInstanceCatalogItem_builder{
				Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template: publicv1.BareMetalInstanceTemplateReference_builder{Id: bareMetalTemplateID}.Build(),
				Title:    "Original bare metal offering",
			}.Build(),
		)

		bareMetalBefore, err := bareMetal.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
			Object: publicv1.BareMetalInstanceCatalogItem_builder{
				Id: bareMetalItem.GetId(),
				Metadata: publicv1.Metadata_builder{
					Version: bareMetalItem.GetMetadata().GetVersion(),
				}.Build(),
				Title: "Updated outside MCP",
			}.Build(),
			UpdateMask: catalogItemUpdateMask("title"),
			Lock:       true,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(bareMetalBefore.GetObject().GetMetadata().GetVersion()).To(BeNumerically(">", bareMetalItem.GetMetadata().GetVersion()))

		_, err = callMCPTool[mcpserver.CatalogOfferingOutput](
			ctx, session, "update_catalog_offering", mcpserver.UpdateCatalogOfferingInput{
				Kind:    mcpserver.CatalogOfferingKindBareMetalInstance,
				ID:      bareMetalItem.GetId(),
				Version: bareMetalItem.GetMetadata().GetVersion(),
				Title:   projectStringPointer("Stale bare metal update"),
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`"category":"conflict"`))
		Expect(err.Error()).To(ContainSubstring(`"grpc_code":"Aborted"`))

		bareMetalGet, err := callMCPTool[mcpserver.GetResourceOutput](
			ctx, session, "get_resource", mcpserver.GetResourceInput{
				ResourceType: mcpserver.ResourceTypeBareMetalInstanceCatalogItem,
				ID:           bareMetalItem.GetId(),
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(bareMetalGet.Resource).To(HaveKeyWithValue("version", float64(
			bareMetalBefore.GetObject().GetMetadata().GetVersion(),
		)))
		Expect(bareMetalGet.Resource).To(HaveLen(4))
		Expect(bareMetalGet.Resource).ToNot(HaveKey("title"))

		bareMetalList, err := callMCPTool[mcpserver.ListResourcesOutput](
			ctx, session, "list_resources", mcpserver.ListResourcesInput{
				ResourceType: mcpserver.ResourceTypeBareMetalInstanceCatalogItem,
				Filter:       fmt.Sprintf("this.id == %q", bareMetalItem.GetId()),
				PageSize:     1,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(bareMetalList.Items).To(ConsistOf(mcpserver.ResourceSummary{
			ID:      bareMetalItem.GetId(),
			Name:    bareMetalItem.GetMetadata().GetName(),
			Version: bareMetalBefore.GetObject().GetMetadata().GetVersion(),
			State:   "unpublished",
		}))
		bareMetalAfterStale, err := bareMetal.Get(ctx, publicv1.BareMetalInstanceCatalogItemsGetRequest_builder{
			Id: bareMetalItem.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(bareMetalAfterStale.GetObject().GetTitle()).To(Equal("Updated outside MCP"))
		Expect(bareMetalAfterStale.GetObject().GetMetadata().GetVersion()).To(Equal(
			bareMetalBefore.GetObject().GetMetadata().GetVersion(),
		))

		bareMetalRetry, err := callMCPTool[mcpserver.CatalogOfferingOutput](
			ctx, session, "update_catalog_offering", mcpserver.UpdateCatalogOfferingInput{
				Kind:    mcpserver.CatalogOfferingKindBareMetalInstance,
				ID:      bareMetalItem.GetId(),
				Version: bareMetalBefore.GetObject().GetMetadata().GetVersion(),
				Title:   projectStringPointer("Bare metal retry succeeded"),
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(bareMetalRetry.Version).To(BeNumerically(">", bareMetalBefore.GetObject().GetMetadata().GetVersion()))

		unauthorizedTokenSource, err := tool.makeKeycloakTokenSource(ctx, "ben", usersPassword)
		Expect(err).ToNot(HaveOccurred())
		unauthorizedSession := connectMCPTokenSource(
			ctx, httpServer.URL, "foreign-catalog-reader", unauthorizedTokenSource,
		)
		DeferCleanup(func() { Expect(unauthorizedSession.Close()).To(Succeed()) })

		_, err = callMCPTool[mcpserver.GetResourceOutput](
			ctx, unauthorizedSession, "get_resource", mcpserver.GetResourceInput{
				ResourceType: mcpserver.ResourceTypeBareMetalInstanceCatalogItem,
				ID:           bareMetalItem.GetId(),
			},
		)
		expectSafeMcpCatalogOfferingDenial(err, bareMetalItem.GetId())

		listed, err := callMCPTool[mcpserver.ListResourcesOutput](
			ctx, unauthorizedSession, "list_resources", mcpserver.ListResourcesInput{
				ResourceType: mcpserver.ResourceTypeBareMetalInstanceCatalogItem,
				Filter:       fmt.Sprintf("this.id == %q", bareMetalItem.GetId()),
				PageSize:     1,
			},
		)
		if err != nil {
			expectSafeMcpCatalogOfferingDenial(err, bareMetalItem.GetId())
		} else {
			Expect(listed.Total).To(Equal(int32(0)))
			Expect(listed.Items).To(BeEmpty())
		}
	})
})

type mcpCatalogOfferingSnapshot struct {
	Tenant      string
	Name        string
	Title       string
	Description string
	Version     int32
	Published   bool
}

type mcpCatalogOfferingAccess struct {
	get    func(context.Context, string) (mcpCatalogOfferingSnapshot, error)
	delete func(context.Context, string) error
}

func assertMcpCatalogOfferingTenantIsolation(
	ctx context.Context, kind mcpserver.CatalogOfferingKind, templateID string,
) {
	GinkgoHelper()

	tenantA := createCatalogItemTenantAdminIdentityFixture(ctx)
	tenantB := createCatalogItemTenantAdminIdentityFixture(ctx)
	Expect(tenantB.TenantID).ToNot(Equal(tenantA.TenantID))
	tokenA, err := tool.makeKeycloakTokenSource(ctx, tenantA.Username, tenantA.Password)
	Expect(err).ToNot(HaveOccurred())
	tokenB, err := tool.makeKeycloakTokenSource(ctx, tenantB.Username, tenantB.Password)
	Expect(err).ToNot(HaveOccurred())

	httpServer := startInProcessMCPServer(ctx, "fulfillment-mcp-catalog-tenant-isolation-it")
	sessionA := connectMCPTokenSource(ctx, httpServer.URL, "tenant-a-catalog-admin", tokenA)
	DeferCleanup(func() { Expect(sessionA.Close()).To(Succeed()) })
	sessionB := connectMCPTokenSource(ctx, httpServer.URL, "tenant-b-catalog-admin", tokenB)
	DeferCleanup(func() { Expect(sessionB.Close()).To(Succeed()) })

	accessA := mcpCatalogOfferingAccessFor(kind, tenantA.Conn)
	accessB := mcpCatalogOfferingAccessFor(kind, tenantB.Conn)

	createdA, err := callMCPTool[mcpserver.CatalogOfferingOutput](
		ctx, sessionA, "create_catalog_offering", mcpserver.CreateCatalogOfferingInput{
			Kind: kind, Name: catalogItemFixtureName(), Title: "Tenant A offering",
			Description: "Tenant A description", TemplateID: templateID,
		},
	)
	Expect(err).ToNot(HaveOccurred())
	deferMcpCatalogOfferingDeletion(accessA, createdA.ID)
	createdB, err := callMCPTool[mcpserver.CatalogOfferingOutput](
		ctx, sessionB, "create_catalog_offering", mcpserver.CreateCatalogOfferingInput{
			Kind: kind, Name: catalogItemFixtureName(), Title: "Tenant B offering",
			Description: "Tenant B description", TemplateID: templateID,
		},
	)
	Expect(err).ToNot(HaveOccurred())
	deferMcpCatalogOfferingDeletion(accessB, createdB.ID)
	Expect(createdA.ID).ToNot(Equal(createdB.ID))

	foreignOffering, err := accessA.get(ctx, createdA.ID)
	Expect(err).ToNot(HaveOccurred())
	Expect(foreignOffering.Tenant).To(Equal(tenantA.TenantID))
	Expect(foreignOffering.Title).To(Equal("Tenant A offering"))
	Expect(foreignOffering.Published).To(BeFalse())

	ownOffering, err := accessB.get(ctx, createdB.ID)
	Expect(err).ToNot(HaveOccurred())
	Expect(ownOffering.Tenant).To(Equal(tenantB.TenantID))
	Expect(ownOffering.Title).To(Equal("Tenant B offering"))
	resourceType := map[mcpserver.CatalogOfferingKind]mcpserver.ResourceType{
		mcpserver.CatalogOfferingKindComputeInstance:   mcpserver.ResourceTypeComputeInstanceCatalogItem,
		mcpserver.CatalogOfferingKindCluster:           mcpserver.ResourceTypeClusterCatalogItem,
		mcpserver.CatalogOfferingKindBareMetalInstance: mcpserver.ResourceTypeBareMetalInstanceCatalogItem,
	}[kind]
	Expect(resourceType).ToNot(BeEmpty())

	ownResource, err := callMCPTool[mcpserver.GetResourceOutput](
		ctx, sessionB, "get_resource", mcpserver.GetResourceInput{
			ResourceType: resourceType,
			ID:           createdB.ID,
		},
	)
	Expect(err).ToNot(HaveOccurred())
	Expect(ownResource.Resource).To(SatisfyAll(
		HaveKeyWithValue("id", createdB.ID),
		HaveKeyWithValue("version", float64(ownOffering.Version)),
	))
	ownList, err := callMCPTool[mcpserver.ListResourcesOutput](
		ctx, sessionB, "list_resources", mcpserver.ListResourcesInput{
			ResourceType: resourceType,
			Filter:       fmt.Sprintf("this.id == %q", createdB.ID),
			PageSize:     1,
		},
	)
	Expect(err).ToNot(HaveOccurred())
	Expect(ownList.Total).To(Equal(int32(1)))
	Expect(ownList.Items).To(ConsistOf(HaveField("ID", createdB.ID)))

	_, err = callMCPTool[mcpserver.GetResourceOutput](
		ctx, sessionB, "get_resource", mcpserver.GetResourceInput{
			ResourceType: resourceType,
			ID:           createdA.ID,
		},
	)
	expectSafeMcpCatalogOfferingDenial(err, createdA.ID)
	foreignList, err := callMCPTool[mcpserver.ListResourcesOutput](
		ctx, sessionB, "list_resources", mcpserver.ListResourcesInput{
			ResourceType: resourceType,
			Filter:       fmt.Sprintf("this.id == %q", createdA.ID),
			PageSize:     1,
		},
	)
	if err != nil {
		expectSafeMcpCatalogOfferingDenial(err, createdA.ID)
	} else {
		Expect(foreignList.Total).To(Equal(int32(0)))
		Expect(foreignList.Items).To(BeEmpty())
	}

	updatedOwnOffering, err := callMCPTool[mcpserver.CatalogOfferingOutput](
		ctx, sessionB, "update_catalog_offering", mcpserver.UpdateCatalogOfferingInput{
			Kind: kind, ID: createdB.ID, Version: ownOffering.Version,
			Title: projectStringPointer("Tenant B updated offering"),
		},
	)
	Expect(err).ToNot(HaveOccurred())
	Expect(updatedOwnOffering.Version).To(BeNumerically(">", ownOffering.Version))
	updatedOwnSnapshot, err := accessB.get(ctx, createdB.ID)
	Expect(err).ToNot(HaveOccurred())
	Expect(updatedOwnSnapshot.Title).To(Equal("Tenant B updated offering"))
	Expect(updatedOwnSnapshot.Version).To(Equal(updatedOwnOffering.Version))

	publishedOwnOffering, err := callMCPTool[mcpserver.CatalogOfferingOutput](
		ctx, sessionB, "publish_catalog_offering", mcpserver.PublishCatalogOfferingInput{
			Kind: kind, ID: createdB.ID, Version: updatedOwnSnapshot.Version,
		},
	)
	Expect(err).ToNot(HaveOccurred())
	Expect(publishedOwnOffering.Published).To(BeTrue())
	updatedOwnSnapshot, err = accessB.get(ctx, createdB.ID)
	Expect(err).ToNot(HaveOccurred())
	Expect(updatedOwnSnapshot.Published).To(BeTrue())
	Expect(updatedOwnSnapshot.Version).To(Equal(publishedOwnOffering.Version))

	deletedOwnOffering, err := callMCPTool[mcpserver.DeleteCatalogOfferingOutput](
		ctx, sessionB, "delete_catalog_offering", mcpserver.DeleteCatalogOfferingInput{
			Kind: kind, ID: createdB.ID,
		},
	)
	Expect(err).ToNot(HaveOccurred())
	Expect(deletedOwnOffering).To(Equal(mcpserver.DeleteCatalogOfferingOutput{
		Kind: string(kind), ID: createdB.ID,
	}))
	_, err = accessB.get(ctx, createdB.ID)
	Expect(status.Code(err)).To(Equal(codes.NotFound))

	_, err = callMCPTool[mcpserver.CatalogOfferingOutput](
		ctx, sessionB, "update_catalog_offering", mcpserver.UpdateCatalogOfferingInput{
			Kind: kind, ID: createdA.ID, Version: foreignOffering.Version,
			Title: projectStringPointer("Should not persist"),
		},
	)
	expectSafeMcpCatalogOfferingDenial(err, createdA.ID)
	expectMcpCatalogOfferingUnchanged(ctx, accessA, createdA.ID, foreignOffering)

	_, err = callMCPTool[mcpserver.CatalogOfferingOutput](
		ctx, sessionB, "publish_catalog_offering", mcpserver.PublishCatalogOfferingInput{
			Kind: kind, ID: createdA.ID, Version: foreignOffering.Version,
		},
	)
	expectSafeMcpCatalogOfferingDenial(err, createdA.ID)
	expectMcpCatalogOfferingUnchanged(ctx, accessA, createdA.ID, foreignOffering)

	_, err = callMCPTool[mcpserver.DeleteCatalogOfferingOutput](
		ctx, sessionB, "delete_catalog_offering", mcpserver.DeleteCatalogOfferingInput{
			Kind: kind, ID: createdA.ID,
		},
	)
	expectSafeMcpCatalogOfferingDenial(err, createdA.ID)
	expectMcpCatalogOfferingUnchanged(ctx, accessA, createdA.ID, foreignOffering)
}

func mcpCatalogOfferingAccessFor(kind mcpserver.CatalogOfferingKind, conn *grpc.ClientConn) mcpCatalogOfferingAccess {
	GinkgoHelper()
	adminConn := tool.InternalView().AdminConn()
	switch kind {
	case mcpserver.CatalogOfferingKindComputeInstance:
		client := publicv1.NewComputeInstanceCatalogItemsClient(conn)
		admin := privatev1.NewComputeInstanceCatalogItemsClient(adminConn)
		return mcpCatalogOfferingAccess{
			get: func(ctx context.Context, id string) (mcpCatalogOfferingSnapshot, error) {
				response, err := client.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{Id: id}.Build())
				if err != nil {
					return mcpCatalogOfferingSnapshot{}, err
				}
				object := response.GetObject()
				return mcpCatalogOfferingSnapshot{
					Tenant: object.GetMetadata().GetTenant(), Name: object.GetMetadata().GetName(),
					Title: object.GetTitle(), Description: object.GetDescription(),
					Version: object.GetMetadata().GetVersion(), Published: object.GetPublished(),
				}, nil
			},
			delete: func(ctx context.Context, id string) error {
				_, err := admin.Delete(ctx, privatev1.ComputeInstanceCatalogItemsDeleteRequest_builder{Id: id}.Build())
				return err
			},
		}
	case mcpserver.CatalogOfferingKindCluster:
		client := publicv1.NewClusterCatalogItemsClient(conn)
		admin := privatev1.NewClusterCatalogItemsClient(adminConn)
		return mcpCatalogOfferingAccess{
			get: func(ctx context.Context, id string) (mcpCatalogOfferingSnapshot, error) {
				response, err := client.Get(ctx, publicv1.ClusterCatalogItemsGetRequest_builder{Id: id}.Build())
				if err != nil {
					return mcpCatalogOfferingSnapshot{}, err
				}
				object := response.GetObject()
				return mcpCatalogOfferingSnapshot{
					Tenant: object.GetMetadata().GetTenant(), Name: object.GetMetadata().GetName(),
					Title: object.GetTitle(), Description: object.GetDescription(),
					Version: object.GetMetadata().GetVersion(), Published: object.GetPublished(),
				}, nil
			},
			delete: func(ctx context.Context, id string) error {
				_, err := admin.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{Id: id}.Build())
				return err
			},
		}
	case mcpserver.CatalogOfferingKindBareMetalInstance:
		client := publicv1.NewBareMetalInstanceCatalogItemsClient(conn)
		admin := privatev1.NewBareMetalInstanceCatalogItemsClient(adminConn)
		return mcpCatalogOfferingAccess{
			get: func(ctx context.Context, id string) (mcpCatalogOfferingSnapshot, error) {
				response, err := client.Get(ctx, publicv1.BareMetalInstanceCatalogItemsGetRequest_builder{Id: id}.Build())
				if err != nil {
					return mcpCatalogOfferingSnapshot{}, err
				}
				object := response.GetObject()
				return mcpCatalogOfferingSnapshot{
					Tenant: object.GetMetadata().GetTenant(), Name: object.GetMetadata().GetName(),
					Title: object.GetTitle(), Description: object.GetDescription(),
					Version: object.GetMetadata().GetVersion(), Published: object.GetPublished(),
				}, nil
			},
			delete: func(ctx context.Context, id string) error {
				_, err := admin.Delete(ctx, privatev1.BareMetalInstanceCatalogItemsDeleteRequest_builder{Id: id}.Build())
				return err
			},
		}
	default:
		Fail(fmt.Sprintf("unsupported catalog offering kind %q", kind))
		return mcpCatalogOfferingAccess{}
	}
}

func deferMcpCatalogOfferingDeletion(access mcpCatalogOfferingAccess, id string) {
	GinkgoHelper()
	deferCatalogItemFixtureDeletion(
		func(ctx context.Context) error { return access.delete(ctx, id) },
		func(ctx context.Context) (bool, error) {
			_, err := access.get(ctx, id)
			if status.Code(err) == codes.NotFound {
				return true, nil
			}
			return false, err
		},
	)
}

func expectSafeMcpCatalogOfferingDenial(err error, id string) {
	GinkgoHelper()
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(MatchRegexp(`"category":"(authorization|not_found)"`))
	Expect(err.Error()).To(MatchRegexp(`"grpc_code":"(PermissionDenied|NotFound)"`))
	Expect(err.Error()).To(ContainSubstring(`"details":[]`))
	Expect(err.Error()).ToNot(ContainSubstring(id))
}

func expectMcpCatalogOfferingUnchanged(
	ctx context.Context, access mcpCatalogOfferingAccess, id string,
	expected mcpCatalogOfferingSnapshot,
) {
	GinkgoHelper()
	actual, err := access.get(ctx, id)
	Expect(err).ToNot(HaveOccurred())
	Expect(actual).To(Equal(expected))
}

func requireMCPBareMetalCatalogEnabled(ctx context.Context) {
	GinkgoHelper()
	_, _, tokenSource := createMCPTenantAdmin(ctx)
	publicConn, err := tool.makeGrpcConn(externalServiceAddr, tokenSource)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { Expect(publicConn.Close()).To(Succeed()) })
	limit := int32(1)
	_, err = publicv1.NewBareMetalInstanceCatalogItemsClient(publicConn).List(
		ctx, publicv1.BareMetalInstanceCatalogItemsListRequest_builder{Limit: &limit}.Build(),
	)
	if status.Code(err) == codes.Unavailable && status.Convert(err).Message() ==
		"the BMaaS service is not enabled on this server" {
		Skip("BMaaS is disabled on this Fulfillment server")
	}
	Expect(err).ToNot(HaveOccurred())
}
