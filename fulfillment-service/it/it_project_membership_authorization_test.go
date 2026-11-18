/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package it

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Project membership authorization", func() {
	It("hides a same-tenant project from a user without membership", func(ctx context.Context) {
		adminConn := tool.InternalView().AdminConn()
		projectsAdmin := privatev1.NewProjectsClient(adminConn)
		membershipsAdmin := privatev1.NewProjectMembershipsClient(adminConn)
		usersAdmin := privatev1.NewUsersClient(adminConn)

		projectName := "membership-access-" + uuid.New()[24:]
		projectResponse, err := projectsAdmin.Create(ctx, privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   projectName,
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{Title: projectName}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		projectID := projectResponse.GetObject().GetId()
		deferMembershipAuthorizationProjectDeletion(projectsAdmin, projectID)

		projectFilter := fmt.Sprintf(
			"this.metadata.name == %q && this.metadata.tenant == %q", projectName, usersGroup,
		)
		adminProjects, err := projectsAdmin.List(ctx, privatev1.ProjectsListRequest_builder{
			Filter: &projectFilter,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(adminProjects.GetItems()).To(HaveLen(1), "project fixture must exist before access checks")

		projects := publicv1.NewProjectsClient(tool.ExternalView().UserConn())
		By("omitting the project from the same-tenant non-member's List response")
		listResponse, err := projects.List(ctx, publicv1.ProjectsListRequest_builder{
			Filter: &projectFilter,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetItems()).To(BeEmpty())

		userFilter := fmt.Sprintf(
			"this.spec.username == %q && this.metadata.tenant == %q", userUsername, usersGroup,
		)
		users, err := usersAdmin.List(ctx, privatev1.UsersListRequest_builder{Filter: &userFilter}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(users.GetItems()).To(HaveLen(1), "the caller must belong to the project tenant")
		userID := users.GetItems()[0].GetId()
		membershipFilter := fmt.Sprintf(
			"this.metadata.tenant == %q && this.metadata.project == %q", usersGroup, projectName,
		)
		memberships, err := membershipsAdmin.List(ctx, privatev1.ProjectMembershipsListRequest_builder{
			Filter: &membershipFilter,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		for _, membership := range memberships.GetItems() {
			for _, member := range membership.GetSpec().GetUsers() {
				Expect(member.GetId()).ToNot(Equal(userID), "the caller must not have a ProjectMembership")
			}
		}

		By("denying direct Get to the same-tenant non-member")
		_, err = projects.Get(ctx, publicv1.ProjectsGetRequest_builder{Id: projectID}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied))

		By("denying a membership grant by the authenticated non-member without persisting it")
		grantName := projectName + "-viewer"
		grant := publicv1.ProjectMembershipsCreateRequest_builder{
			Object: publicv1.ProjectMembership_builder{
				Metadata: publicv1.Metadata_builder{Name: grantName, Tenant: usersGroup, Project: projectName}.Build(),
				Spec: publicv1.ProjectMembershipSpec_builder{
					Role:  publicv1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_VIEWER,
					Users: []*publicv1.UserReference{publicv1.UserReference_builder{Id: userID}.Build()},
				}.Build(),
			}.Build(),
		}.Build()
		membershipsUser := publicv1.NewProjectMembershipsClient(tool.ExternalView().UserConn())
		_, err = membershipsUser.Create(ctx, grant)
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied))
		grantFilter := fmt.Sprintf("%s && this.metadata.name == %q", membershipFilter, grantName)
		denied, err := membershipsAdmin.List(ctx, privatev1.ProjectMembershipsListRequest_builder{Filter: &grantFilter}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(denied.GetItems()).To(BeEmpty())

		By("allowing the platform administrator to grant membership for the same live project")
		publicAdmin := publicv1.NewProjectMembershipsClient(tool.ExternalView().AdminConn())
		created, err := publicAdmin.Create(ctx, grant)
		Expect(err).NotTo(HaveOccurred())
		deferProjectMembershipDeletion(membershipsAdmin, created.GetObject().GetId())
		Expect(created.GetObject().GetMetadata().GetTenant()).To(Equal(usersGroup))
		Expect(created.GetObject().GetMetadata().GetProject()).To(Equal(projectName))
	})

	It("allows a tenant-bound manager to grant membership only in the project they manage", func(ctx context.Context) {
		adminConn := tool.InternalView().AdminConn()
		projectsAdmin := privatev1.NewProjectsClient(adminConn)
		usersAdmin := privatev1.NewUsersClient(adminConn)
		membershipsAdmin := privatev1.NewProjectMembershipsClient(adminConn)

		// Create through the public API so the project controller can grant its
		// initial manager membership to a real User instead of the system actor.
		createProject := func(username string) (projectName, userID string) {
			GinkgoHelper()
			revokeTenantAdmin := grantMCPDiscoveryTenantAdmin(ctx, username)
			conn, err := tool.MakeUserConnection(ctx, username, usersPassword)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(conn.Close)
			projects := publicv1.NewProjectsClient(conn)
			// Provision the Fulfillment User through the normal authentication path.
			_, err = projects.List(ctx, publicv1.ProjectsListRequest_builder{}.Build())
			Expect(err).NotTo(HaveOccurred())
			filter := fmt.Sprintf("this.spec.username == %q && this.metadata.tenant == %q", username, usersGroup)
			users, err := usersAdmin.List(ctx, privatev1.UsersListRequest_builder{Filter: &filter}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(users.GetItems()).To(HaveLen(1))
			userID = users.GetItems()[0].GetId()
			Expect(userID).NotTo(BeEmpty())

			projectName = "membership-access-" + uuid.New()[24:]
			response, err := projects.Create(ctx, publicv1.ProjectsCreateRequest_builder{
				Object: publicv1.Project_builder{
					Metadata: publicv1.Metadata_builder{Name: projectName, Tenant: usersGroup}.Build(),
					Spec:     publicv1.ProjectSpec_builder{Title: projectName}.Build(),
				}.Build(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			projectID := response.GetObject().GetId()
			deferMembershipAuthorizationProjectDeletion(projectsAdmin, projectID)
			revokeTenantAdmin(ctx)
			Expect(response.GetObject().GetMetadata().GetCreator()).To(Equal(userID))

			Eventually(ctx, func(g Gomega, ctx context.Context) {
				response, err := projectsAdmin.Get(ctx, privatev1.ProjectsGetRequest_builder{Id: projectID}.Build())
				g.Expect(err).NotTo(HaveOccurred())
				status := response.GetObject().GetStatus()
				g.Expect(status.GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_ACTIVE), status.GetMessage())
			}, time.Minute, time.Second).Should(Succeed())
			return projectName, userID
		}

		By("creating two projects with distinct owners in the same tenant")
		managedProject, managerID := createProject(userUsername)
		otherProject, viewerID := createProject(sshKeyProjectOwnerUsername)
		Expect(viewerID).NotTo(Equal(managerID))

		By("waiting for the controller to reconcile the caller's creator membership")
		membershipFilter := fmt.Sprintf("this.metadata.tenant == %q && this.metadata.project == %q", usersGroup, managedProject)
		Eventually(ctx, func(g Gomega, ctx context.Context) {
			response, err := membershipsAdmin.List(ctx, privatev1.ProjectMembershipsListRequest_builder{Filter: &membershipFilter}.Build())
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(response.GetItems()).To(HaveLen(1))
			membership := response.GetItems()[0]
			g.Expect(membership.GetSpec().GetRole()).To(Equal(privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER))
			g.Expect(membership.GetSpec().GetUsers()).To(HaveLen(1))
			g.Expect(membership.GetSpec().GetUsers()[0].GetId()).To(Equal(managerID))
			status := membership.GetStatus()
			g.Expect(status.GetState()).To(Equal(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY), status.GetMessage())
			g.Expect(status.GetUsers()).To(HaveLen(1))
		}, time.Minute, time.Second).Should(Succeed())

		// Log in after reconciliation so Keycloak includes the manager group in the token.
		managerConn, err := tool.MakeUserConnection(ctx, userUsername, usersPassword)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(managerConn.Close()).To(Succeed()) })
		memberships := publicv1.NewProjectMembershipsClient(managerConn)
		grant := func(project string) *publicv1.ProjectMembershipsCreateRequest {
			return publicv1.ProjectMembershipsCreateRequest_builder{
				Object: publicv1.ProjectMembership_builder{
					Metadata: publicv1.Metadata_builder{
						Name: project + "-viewer", Tenant: usersGroup, Project: project,
					}.Build(),
					Spec: publicv1.ProjectMembershipSpec_builder{
						Role:  publicv1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_VIEWER,
						Users: []*publicv1.UserReference{publicv1.UserReference_builder{Id: viewerID}.Build()},
					}.Build(),
				}.Build(),
			}.Build()
		}

		By("persisting the authenticated manager's viewer grant in their project")
		created, err := memberships.Create(ctx, grant(managedProject))
		Expect(err).NotTo(HaveOccurred())
		grantID := created.GetObject().GetId()
		deferProjectMembershipDeletion(membershipsAdmin, grantID)
		stored, err := membershipsAdmin.Get(ctx, privatev1.ProjectMembershipsGetRequest_builder{Id: grantID}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.GetObject().GetMetadata().GetCreator()).To(Equal(managerID))
		Expect(stored.GetObject().GetMetadata().GetTenant()).To(Equal(usersGroup))
		Expect(stored.GetObject().GetMetadata().GetProject()).To(Equal(managedProject))
		Expect(stored.GetObject().GetSpec().GetRole()).To(Equal(privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_VIEWER))
		Expect(stored.GetObject().GetSpec().GetUsers()).To(HaveLen(1))
		Expect(stored.GetObject().GetSpec().GetUsers()[0].GetId()).To(Equal(viewerID))

		By("rejecting a viewer grant in the other fixture project without storing it")
		outOfScope := grant(otherProject)
		unexpected, err := memberships.Create(ctx, outOfScope)
		if id := unexpected.GetObject().GetId(); id != "" {
			deferProjectMembershipDeletion(membershipsAdmin, id)
		}
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied))
		grantFilter := fmt.Sprintf("this.metadata.name == %q", outOfScope.GetObject().GetMetadata().GetName())
		denied, err := membershipsAdmin.List(ctx, privatev1.ProjectMembershipsListRequest_builder{Filter: &grantFilter}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(denied.GetItems()).To(BeEmpty())
	})
})

func deferMembershipAuthorizationProjectDeletion(client privatev1.ProjectsClient, id string) {
	GinkgoHelper()
	Expect(id).NotTo(BeEmpty())
	DeferCleanup(func(ctx context.Context) {
		_, err := client.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{Id: id}.Build())
		if grpcstatus.Code(err) != grpccodes.NotFound {
			Expect(err).NotTo(HaveOccurred())
		}
		Eventually(ctx, func(g Gomega, ctx context.Context) {
			_, err := client.Get(ctx, privatev1.ProjectsGetRequest_builder{Id: id}.Build())
			g.Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
		}, time.Minute, time.Second).Should(Succeed())
	}, NodeTimeout(2*time.Minute))
}

func deferProjectMembershipDeletion(client privatev1.ProjectMembershipsClient, id string) {
	GinkgoHelper()
	Expect(id).NotTo(BeEmpty())
	DeferCleanup(func(ctx context.Context) {
		_, err := client.Delete(ctx, privatev1.ProjectMembershipsDeleteRequest_builder{Id: id}.Build())
		if grpcstatus.Code(err) != grpccodes.NotFound {
			Expect(err).NotTo(HaveOccurred())
		}
		Eventually(ctx, func(g Gomega, ctx context.Context) {
			_, err := client.Get(ctx, privatev1.ProjectMembershipsGetRequest_builder{Id: id}.Build())
			g.Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
		}, time.Minute, time.Second).Should(Succeed())
	}, NodeTimeout(2*time.Minute))
}
