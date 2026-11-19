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
	})
})
