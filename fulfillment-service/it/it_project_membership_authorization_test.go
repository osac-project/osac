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

	. "github.com/onsi/ginkgo/v2/dsl/core"
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
		DeferCleanup(func(cleanupCtx context.Context) {
			_, deleteErr := projectsAdmin.Delete(cleanupCtx, privatev1.ProjectsDeleteRequest_builder{
				Id: projectID,
			}.Build())
			if grpcstatus.Code(deleteErr) != grpccodes.NotFound {
				Expect(deleteErr).ToNot(HaveOccurred())
			}
			Eventually(func(g Gomega) {
				_, getErr := projectsAdmin.Get(cleanupCtx, privatev1.ProjectsGetRequest_builder{
					Id: projectID,
				}.Build())
				g.Expect(grpcstatus.Code(getErr)).To(Equal(grpccodes.NotFound))
			}, time.Minute, time.Second).Should(Succeed())
		})

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
		usersAdmin := privatev1.NewUsersClient(adminConn)
		membershipsAdmin := privatev1.NewProjectMembershipsClient(adminConn)

		By("provisioning the tenant user and viewer through the normal authentication path")
		viewerConn, err := tool.MakeUserConnection(ctx, sshKeyProjectOwnerUsername, usersPassword)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(viewerConn.Close()).To(Succeed()) })
		for _, projects := range []publicv1.ProjectsClient{
			publicv1.NewProjectsClient(tool.ExternalView().UserConn()),
			publicv1.NewProjectsClient(viewerConn),
		} {
			_, err := projects.List(ctx, publicv1.ProjectsListRequest_builder{}.Build())
			Expect(err).NotTo(HaveOccurred())
		}
		userID := func(username string) string {
			GinkgoHelper()
			filter := fmt.Sprintf("this.spec.username == %q && this.metadata.tenant == %q", username, usersGroup)
			response, err := usersAdmin.List(ctx, privatev1.UsersListRequest_builder{Filter: &filter}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(1))
			return response.GetItems()[0].GetId()
		}
		managerID := userID(userUsername)
		viewerID := userID(sshKeyProjectOwnerUsername)

		By("granting the tenant user a manager membership in just one fixture project")
		managedProject := createCatalogItemProjectFixture(ctx, usersGroup)
		otherProject := createCatalogItemProjectFixture(ctx, usersGroup)
		projectsAdmin := privatev1.NewProjectsClient(adminConn)
		for _, project := range []string{managedProject, otherProject} {
			filter := fmt.Sprintf("this.metadata.tenant == %q && this.metadata.name == %q", usersGroup, project)
			Eventually(func(g Gomega) {
				response, err := projectsAdmin.List(ctx, privatev1.ProjectsListRequest_builder{Filter: &filter}.Build())
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(response.GetItems()).To(HaveLen(1))
				g.Expect(response.GetItems()[0].GetStatus().GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_ACTIVE))
			}, time.Minute, time.Second).Should(Succeed())
		}
		membership, err := membershipsAdmin.Create(ctx, privatev1.ProjectMembershipsCreateRequest_builder{
			Object: privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Name: managedProject + "-manager", Tenant: usersGroup, Project: managedProject,
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
					Users: []*privatev1.UserReference{privatev1.UserReference_builder{Id: managerID}.Build()},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		membershipID := membership.GetObject().GetId()
		deferProjectMembershipDeletion(membershipsAdmin, membershipID)
		Eventually(func(g Gomega) {
			response, err := membershipsAdmin.Get(ctx, privatev1.ProjectMembershipsGetRequest_builder{Id: membershipID}.Build())
			g.Expect(err).NotTo(HaveOccurred())
			status := response.GetObject().GetStatus()
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

func deferProjectMembershipDeletion(client privatev1.ProjectMembershipsClient, id string) {
	GinkgoHelper()
	Expect(id).NotTo(BeEmpty())
	DeferCleanup(func(ctx context.Context) {
		_, err := client.Delete(ctx, privatev1.ProjectMembershipsDeleteRequest_builder{Id: id}.Build())
		if grpcstatus.Code(err) != grpccodes.NotFound {
			Expect(err).NotTo(HaveOccurred())
		}
		Eventually(func(g Gomega) {
			_, err := client.Get(ctx, privatev1.ProjectMembershipsGetRequest_builder{Id: id}.Build())
			g.Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
		}, time.Minute, time.Second).Should(Succeed())
	})
}
