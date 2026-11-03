package it

import (
	"context"
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/service/start/mcpserver"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("MCP Project administration", func() {
	It("creates and updates display fields as a tenant administrator", func(ctx context.Context) {
		tenant, username, tokenSource := createMCPTenantAdmin(ctx)
		publicConn, err := tool.makeGrpcConn(externalServiceAddr, tokenSource)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = publicConn.Close() })
		projects := publicv1.NewProjectsClient(publicConn)

		httpServer := startInProcessMCPServer(ctx, "fulfillment-mcp-project-it")
		session := connectMCPTokenSource(ctx, httpServer.URL, "tenant-admin", tokenSource)
		DeferCleanup(func() { _ = session.Close() })

		name := fmt.Sprintf("mcp-project-%s", uuid.New()[24:32])
		created, err := callMCPTool[mcpserver.ProjectOutput](ctx, session, "create_project", mcpserver.CreateProjectInput{
			Name:        name,
			Title:       "Original title",
			Description: projectStringPointer("Original description"),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(created.ID).ToNot(BeEmpty())
		Expect(created.Name).To(Equal(name))
		Expect(created.Version).To(BeNumerically(">=", 0))
		Expect(created.State).ToNot(BeEmpty())

		adminProjects := privatev1.NewProjectsClient(tool.InternalView().AdminConn())
		DeferCleanup(func(ctx context.Context) {
			deleteProject(ctx, adminProjects, created.ID)
		})

		users := privatev1.NewUsersClient(tool.InternalView().AdminConn())
		userFilter := fmt.Sprintf("this.spec.username == %q", username)
		userResponse, err := users.List(ctx, privatev1.UsersListRequest_builder{
			Filter: &userFilter,
			Limit:  proto.Int32(1),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(userResponse.GetSize()).To(Equal(int32(1)))
		userID := userResponse.GetItems()[0].GetId()

		initial, err := projects.Get(ctx, publicv1.ProjectsGetRequest_builder{Id: created.ID}.Build())
		Expect(err).ToNot(HaveOccurred())
		initialProject := initial.GetObject()
		Expect(initialProject.GetMetadata().GetTenant()).To(Equal(tenant))
		Expect(initialProject.GetMetadata().GetCreator()).To(Equal(userID))
		Expect(initialProject.GetMetadata().GetProject()).To(BeEmpty())

		retrieved, err := callMCPTool[mcpserver.GetResourceOutput](ctx, session, "get_resource", mcpserver.GetResourceInput{
			ResourceType: mcpserver.ResourceTypeProject,
			ID:           created.ID,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(retrieved.Resource).To(SatisfyAll(
			HaveLen(4),
			HaveKeyWithValue("id", created.ID),
			HaveKeyWithValue("name", initialProject.GetMetadata().GetName()),
			HaveKeyWithValue("version", BeNumerically(
				">=", float64(initialProject.GetMetadata().GetVersion()),
			)),
			HaveKeyWithValue("state", initialProject.GetStatus().GetState().String()),
		))
		Expect(retrieved.Resource).ToNot(HaveKey("title"))
		Expect(retrieved.Resource).ToNot(HaveKey("description"))

		updatedTitle, err := callMCPTool[mcpserver.ProjectOutput](ctx, session, "update_project", mcpserver.UpdateProjectInput{
			ID:    created.ID,
			Title: projectStringPointer("Updated title"),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(updatedTitle.ID).To(Equal(created.ID))
		Expect(updatedTitle.Version).To(BeNumerically(">", initialProject.GetMetadata().GetVersion()))

		afterTitle, err := projects.Get(ctx, publicv1.ProjectsGetRequest_builder{Id: created.ID}.Build())
		Expect(err).ToNot(HaveOccurred())
		afterTitleProject := afterTitle.GetObject()
		Expect(afterTitleProject.GetSpec().GetTitle()).To(Equal("Updated title"))
		Expect(afterTitleProject.GetSpec().GetDescription()).To(Equal("Original description"))
		Expect(afterTitleProject.GetMetadata().GetVersion()).To(BeNumerically(">", initialProject.GetMetadata().GetVersion()))

		updatedDescription, err := callMCPTool[mcpserver.ProjectOutput](ctx, session, "update_project", mcpserver.UpdateProjectInput{
			ID:          created.ID,
			Description: projectStringPointer("Updated description"),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(updatedDescription.ID).To(Equal(created.ID))
		Expect(updatedDescription.Version).To(BeNumerically(">", afterTitleProject.GetMetadata().GetVersion()))

		afterDescription, err := projects.Get(ctx, publicv1.ProjectsGetRequest_builder{Id: created.ID}.Build())
		Expect(err).ToNot(HaveOccurred())
		afterDescriptionProject := afterDescription.GetObject()
		Expect(afterDescriptionProject.GetSpec().GetTitle()).To(Equal("Updated title"))
		Expect(afterDescriptionProject.GetSpec().GetDescription()).To(Equal("Updated description"))
		Expect(afterDescriptionProject.GetId()).To(Equal(initialProject.GetId()))
		Expect(afterDescriptionProject.GetMetadata().GetName()).To(Equal(initialProject.GetMetadata().GetName()))
		Expect(afterDescriptionProject.GetMetadata().GetTenant()).To(Equal(initialProject.GetMetadata().GetTenant()))
		Expect(afterDescriptionProject.GetMetadata().GetProject()).To(Equal(initialProject.GetMetadata().GetProject()))
		Expect(afterDescriptionProject.GetMetadata().GetCreator()).To(Equal(initialProject.GetMetadata().GetCreator()))
		Expect(afterDescriptionProject.GetStatus().GetState()).To(Equal(initialProject.GetStatus().GetState()))
		Expect(afterDescriptionProject.GetStatus().GetMessage()).To(Equal(initialProject.GetStatus().GetMessage()))

		lastWrite, err := callMCPTool[mcpserver.ProjectOutput](ctx, session, "update_project", mcpserver.UpdateProjectInput{
			ID:    created.ID,
			Title: projectStringPointer("Last-write title"),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(lastWrite.ID).To(Equal(created.ID))
		afterLastWrite, err := projects.Get(ctx, publicv1.ProjectsGetRequest_builder{Id: created.ID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(afterLastWrite.GetObject().GetSpec().GetTitle()).To(Equal("Last-write title"))
		Expect(afterLastWrite.GetObject().GetSpec().GetDescription()).To(Equal("Updated description"))

		_, err = adminProjects.Signal(ctx, privatev1.ProjectsSignalRequest_builder{Id: created.ID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Eventually(func(g Gomega) {
			response, err := adminProjects.Get(ctx, privatev1.ProjectsGetRequest_builder{Id: created.ID}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(
				privatev1.ProjectState_PROJECT_STATE_ACTIVE,
			))
		}, time.Minute, time.Second).Should(Succeed())

		versionBeforeUnsupported, err := projects.Get(ctx, publicv1.ProjectsGetRequest_builder{Id: created.ID}.Build())
		Expect(err).ToNot(HaveOccurred())
		_, err = callMCPTool[mcpserver.ProjectOutput](ctx, session, "update_project", map[string]any{
			"id": created.ID, "name": "renamed-project", "title": "Rejected title",
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("name"))
		versionAfterUnsupported, err := projects.Get(ctx, publicv1.ProjectsGetRequest_builder{Id: created.ID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(versionAfterUnsupported.GetObject().GetMetadata().GetVersion()).To(Equal(
			versionBeforeUnsupported.GetObject().GetMetadata().GetVersion(),
		))
		Expect(versionAfterUnsupported.GetObject().GetSpec().GetTitle()).To(Equal("Last-write title"))
		Expect(versionAfterUnsupported.GetObject().GetSpec().GetDescription()).To(Equal("Updated description"))
	})

	It("enforces Project manager and tenant boundaries", func(ctx context.Context) {
		adminConn := tool.InternalView().AdminConn()
		revokeTenantAdmin := grantMCPDiscoveryTenantAdmin(ctx, userUsername)
		projectConn, err := tool.MakeUserConnection(ctx, userUsername, usersPassword)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = projectConn.Close() })
		projects := publicv1.NewProjectsClient(projectConn)

		managerProject, managerMembership := createMCPProjectFixture(
			ctx, projects, userUsername, usersGroup, "manager", "Manager title", "Manager description",
		)
		viewerProject, viewerManagerMembership := createMCPProjectFixture(
			ctx, projects, userUsername, usersGroup, "viewer", "Viewer title", "Viewer description",
		)
		adminMemberships := privatev1.NewProjectMembershipsClient(adminConn)
		_, err = adminMemberships.Delete(ctx, privatev1.ProjectMembershipsDeleteRequest_builder{
			Id: viewerManagerMembership.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Eventually(func(g Gomega) {
			_, err := adminMemberships.Get(ctx, privatev1.ProjectMembershipsGetRequest_builder{
				Id: viewerManagerMembership.GetId(),
			}.Build())
			g.Expect(status.Code(err)).To(Equal(codes.NotFound))
		}, time.Minute, time.Second).Should(Succeed())
		viewerMembership := createMCPProjectMembership(
			ctx, usersGroup, viewerProject.GetMetadata().GetName(), userUsername,
			privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_VIEWER,
		)
		revokeTenantAdmin(ctx)

		httpServer := startInProcessMCPServer(ctx, "fulfillment-mcp-project-roles-it")
		userTokenSource, err := tool.makeKeycloakTokenSource(ctx, userUsername, usersPassword)
		Expect(err).ToNot(HaveOccurred())
		userSession := connectMCPTokenSource(ctx, httpServer.URL, "user-discovery", userTokenSource)
		DeferCleanup(func() { _ = userSession.Close() })
		benTokenSource, err := tool.makeKeycloakTokenSource(ctx, "ben", usersPassword)
		Expect(err).ToNot(HaveOccurred())
		benSession := connectMCPTokenSource(ctx, httpServer.URL, "ben-discovery", benTokenSource)
		DeferCleanup(func() { _ = benSession.Close() })

		updated, err := callMCPTool[mcpserver.ProjectOutput](ctx, userSession, "update_project", mcpserver.UpdateProjectInput{
			ID:    managerProject.GetId(),
			Title: projectStringPointer("Manager updated title"),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(updated.ID).To(Equal(managerProject.GetId()))

		adminProjects := privatev1.NewProjectsClient(adminConn)
		afterManagerUpdate, err := adminProjects.Get(ctx, privatev1.ProjectsGetRequest_builder{
			Id: managerProject.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		assertProjectIdentityPreserved(managerProject, afterManagerUpdate.GetObject())

		memberships := privatev1.NewProjectMembershipsClient(adminConn)
		afterManagerMembership, err := memberships.Get(ctx, privatev1.ProjectMembershipsGetRequest_builder{
			Id: managerMembership.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(proto.Equal(managerMembership, afterManagerMembership.GetObject())).To(BeTrue())

		_, err = callMCPTool[mcpserver.ProjectOutput](ctx, userSession, "update_project", mcpserver.UpdateProjectInput{
			ID:    viewerProject.GetId(),
			Title: projectStringPointer("Rejected viewer title"),
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`"category":"authorization"`))
		Expect(err.Error()).To(ContainSubstring(`"grpc_code":"PermissionDenied"`))

		_, err = callMCPTool[mcpserver.ProjectOutput](ctx, benSession, "update_project", mcpserver.UpdateProjectInput{
			ID:    managerProject.GetId(),
			Title: projectStringPointer("Rejected cross-tenant title"),
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`"category":"authorization"`))
		Expect(err.Error()).To(ContainSubstring(`"grpc_code":"PermissionDenied"`))

		afterViewerDenial, err := adminProjects.Get(ctx, privatev1.ProjectsGetRequest_builder{
			Id: viewerProject.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(afterViewerDenial.GetObject().GetSpec().GetTitle()).To(Equal("Viewer title"))
		Expect(afterViewerDenial.GetObject().GetMetadata().GetVersion()).To(Equal(
			viewerProject.GetMetadata().GetVersion(),
		))
		afterViewerMembership, err := memberships.Get(ctx, privatev1.ProjectMembershipsGetRequest_builder{
			Id: viewerMembership.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(proto.Equal(viewerMembership, afterViewerMembership.GetObject())).To(BeTrue())
	})
})

func createMCPTenantAdmin(ctx context.Context) (string, string, auth.TokenSource) {
	GinkgoHelper()
	username := userUsername
	keycloakUserID, err := tool.keycloakUserID(ctx, username)
	Expect(err).ToNot(HaveOccurred())

	role := getOrCreateKeycloakTenantAdminRole(ctx)
	assignKeycloakRealmRole(ctx, keycloakUserID, role)
	roleMappings := []map[string]any{role}
	DeferCleanup(func(ctx context.Context) {
		status, _, deleteErr := tool.KeycloakAdminRequest(ctx, http.MethodDelete,
			fmt.Sprintf("/users/%s/role-mappings/realm", keycloakUserID), roleMappings)
		Expect(deleteErr).ToNot(HaveOccurred())
		Expect(status).To(Equal(http.StatusNoContent))
	})

	tokenSource, err := tool.makeKeycloakTokenSource(ctx, username, usersPassword)
	Expect(err).ToNot(HaveOccurred())
	return usersGroup, username, tokenSource
}

func createMCPProjectMembership(
	ctx context.Context, tenant, projectName, username string, role privatev1.ProjectMembershipRole,
) *privatev1.ProjectMembership {
	GinkgoHelper()
	users := privatev1.NewUsersClient(tool.InternalView().AdminConn())
	filter := fmt.Sprintf("this.spec.username == %q", username)
	userResponse, err := users.List(ctx, privatev1.UsersListRequest_builder{
		Filter: &filter, Limit: proto.Int32(1),
	}.Build())
	Expect(err).ToNot(HaveOccurred())
	Expect(userResponse.GetSize()).To(Equal(int32(1)))

	client := privatev1.NewProjectMembershipsClient(tool.InternalView().AdminConn())
	response, err := client.Create(ctx, privatev1.ProjectMembershipsCreateRequest_builder{
		Object: privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Name:    fmt.Sprintf("%s-%s-membership", projectName, username),
				Project: projectName,
				Tenant:  tenant,
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Role: role,
				Users: []*privatev1.UserReference{{
					Id: userResponse.GetItems()[0].GetId(),
				}},
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).ToNot(HaveOccurred())
	membership := response.GetObject()
	membershipID := membership.GetId()
	DeferCleanup(func(ctx context.Context) {
		_, err := client.Delete(ctx, privatev1.ProjectMembershipsDeleteRequest_builder{
			Id: membershipID,
		}.Build())
		if status.Code(err) != codes.NotFound {
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
		}
	})
	EventuallyWithOffset(1, func(g Gomega) {
		getResponse, err := client.Get(ctx, privatev1.ProjectMembershipsGetRequest_builder{
			Id: membership.GetId(),
		}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(getResponse.GetObject().GetStatus().GetState()).To(Equal(
			privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY,
		))
		membership = getResponse.GetObject()
	}, time.Minute, time.Second).Should(Succeed())
	return membership
}

func assertProjectIdentityPreserved(before, after *privatev1.Project) {
	GinkgoHelper()
	Expect(after.GetId()).To(Equal(before.GetId()))
	Expect(after.GetMetadata().GetName()).To(Equal(before.GetMetadata().GetName()))
	Expect(after.GetMetadata().GetTenant()).To(Equal(before.GetMetadata().GetTenant()))
	Expect(after.GetMetadata().GetProject()).To(Equal(before.GetMetadata().GetProject()))
	Expect(after.GetMetadata().GetCreator()).To(Equal(before.GetMetadata().GetCreator()))
	Expect(after.GetStatus().GetState()).To(Equal(before.GetStatus().GetState()))
	Expect(after.GetStatus().GetMessage()).To(Equal(before.GetStatus().GetMessage()))
}

func projectStringPointer(value string) *string {
	return &value
}
