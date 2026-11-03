package it

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func createMCPProjectFixture(
	ctx context.Context,
	projects publicv1.ProjectsClient,
	caller, tenant, suffix, title, description string,
) (*privatev1.Project, *privatev1.ProjectMembership) {
	GinkgoHelper()
	adminConn := tool.InternalView().AdminConn()
	adminProjects := privatev1.NewProjectsClient(adminConn)
	adminMemberships := privatev1.NewProjectMembershipsClient(adminConn)
	users := privatev1.NewUsersClient(adminConn)

	name := fmt.Sprintf("mcp-%s-project-%s", suffix, uuid.New()[24:32])
	_, err := projects.List(ctx, publicv1.ProjectsListRequest_builder{
		Limit: proto.Int32(1),
	}.Build())
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	userFilter := fmt.Sprintf("this.spec.username == %q", caller)
	userResponse, err := users.List(ctx, privatev1.UsersListRequest_builder{
		Filter: &userFilter,
		Limit:  proto.Int32(1),
	}.Build())
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, userResponse.GetSize()).To(Equal(int32(1)))
	userID := userResponse.GetItems()[0].GetId()

	response, err := projects.Create(ctx, publicv1.ProjectsCreateRequest_builder{
		Object: publicv1.Project_builder{
			Metadata: publicv1.Metadata_builder{Name: name, Tenant: tenant}.Build(),
			Spec: publicv1.ProjectSpec_builder{
				Title:       title,
				Description: proto.String(description),
			}.Build(),
		}.Build(),
	}.Build())
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, response.GetObject().GetMetadata().GetCreator()).To(Equal(userID))
	projectID := response.GetObject().GetId()
	var project *privatev1.Project
	var membership *privatev1.ProjectMembership
	_, err = adminProjects.Signal(ctx, privatev1.ProjectsSignalRequest_builder{Id: projectID}.Build())
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	DeferCleanup(func(ctx context.Context) {
		_, err := adminProjects.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{Id: projectID}.Build())
		if status.Code(err) != codes.NotFound {
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
		}
	})
	EventuallyWithOffset(1, func(g Gomega) {
		getResponse, err := adminProjects.Get(ctx, privatev1.ProjectsGetRequest_builder{Id: projectID}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(getResponse.GetObject().GetStatus().GetState()).To(Equal(
			privatev1.ProjectState_PROJECT_STATE_ACTIVE,
		))
		project = getResponse.GetObject()
	}, time.Minute, time.Second).Should(Succeed())

	membershipFilter := fmt.Sprintf(
		"this.metadata.tenant == %q && this.metadata.project == %q", tenant, name,
	)
	EventuallyWithOffset(1, func(g Gomega) {
		listResponse, err := adminMemberships.List(ctx, privatev1.ProjectMembershipsListRequest_builder{
			Filter: &membershipFilter,
		}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		var membershipID string
		for _, item := range listResponse.GetItems() {
			if item.GetSpec().GetRole() != privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER {
				continue
			}
			for _, user := range item.GetSpec().GetUsers() {
				if user.GetId() == userID {
					membershipID = item.GetId()
					break
				}
			}
		}
		g.Expect(membershipID).ToNot(BeEmpty())
		getResponse, err := adminMemberships.Get(ctx, privatev1.ProjectMembershipsGetRequest_builder{
			Id: membershipID,
		}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(getResponse.GetObject().GetStatus().GetState()).To(Equal(
			privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY,
		))
		membership = getResponse.GetObject()
	}, time.Minute, time.Second).Should(Succeed())
	return project, membership
}
