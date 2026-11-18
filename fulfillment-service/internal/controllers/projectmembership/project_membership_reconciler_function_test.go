/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package projectmembership

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/idp"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// userRefs creates a slice of UserReference messages from the given names.
func userRefs(names ...string) []*privatev1.UserReference {
	refs := make([]*privatev1.UserReference, len(names))
	for i, n := range names {
		refs[i] = privatev1.UserReference_builder{Name: n}.Build()
	}
	return refs
}

func projectFixture(name, parent string) *privatev1.Project {
	return privatev1.Project_builder{
		Id: "id-" + name,
		Metadata: privatev1.Metadata_builder{
			Name: name, Tenant: "acme", Project: parent,
		}.Build(),
	}.Build()
}

func expectProject(client *MockProjectsClient, project *privatev1.Project) *gomock.Call {
	metadata := project.GetMetadata()
	return client.EXPECT().List(gomock.Any(), projectFilter(metadata.GetTenant(), metadata.GetName())).
		Return(&privatev1.ProjectsListResponse{Items: []*privatev1.Project{project}}, nil)
}

func projectFilter(tenant, name string) gomock.Matcher {
	filter := fmt.Sprintf("this.metadata.tenant == %q && this.metadata.name == %q", tenant, name)
	return gomock.Cond(func(req *privatev1.ProjectsListRequest) bool {
		return req.GetFilter() == filter
	})
}

var _ = Describe("Finalizer Management", func() {
	It("should add finalizer on first call", func() {
		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{},
			}.Build(),
		}.Build()

		task := &task{
			membership: membership,
		}

		added := task.addFinalizer()
		Expect(added).To(BeTrue())
		Expect(membership.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.ProjectMembershipFinalizer))
	})

	It("should not add finalizer if already present", func() {
		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
			}.Build(),
		}.Build()

		task := &task{
			membership: membership,
		}

		added := task.addFinalizer()
		Expect(added).To(BeFalse())
		Expect(membership.GetMetadata().GetFinalizers()).To(HaveLen(1))
	})
})

var _ = Describe("Default Values", func() {
	It("should set default status if missing", func() {
		membership := privatev1.ProjectMembership_builder{}.Build()

		task := &task{
			membership: membership,
		}

		task.setDefaults()

		Expect(membership.HasStatus()).To(BeTrue())
		Expect(membership.GetStatus().GetState()).To(Equal(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_PENDING))
	})

	It("should set default state if unspecified", func() {
		membership := privatev1.ProjectMembership_builder{
			Status: privatev1.ProjectMembershipStatus_builder{
				State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_UNSPECIFIED,
			}.Build(),
		}.Build()

		task := &task{
			membership: membership,
		}

		task.setDefaults()

		Expect(membership.GetStatus().GetState()).To(Equal(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_PENDING))
	})

	It("should not change existing non-unspecified state", func() {
		membership := privatev1.ProjectMembership_builder{
			Status: privatev1.ProjectMembershipStatus_builder{
				State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY,
			}.Build(),
		}.Build()

		task := &task{
			membership: membership,
		}

		task.setDefaults()

		Expect(membership.GetStatus().GetState()).To(Equal(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY))
	})
})

var _ = Describe("Update with FAILED state", func() {
	var (
		ctrl                         *gomock.Controller
		mockProjectsClient           *MockProjectsClient
		mockProjectMembershipsClient *MockProjectMembershipsClient
		mockUsersClient              *MockUsersClient
		mockIdpClient                *idp.MockClientInterface
		ctx                          context.Context
		functionObj                  *function
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockProjectsClient = NewMockProjectsClient(ctrl)
		mockProjectMembershipsClient = NewMockProjectMembershipsClient(ctrl)
		mockUsersClient = NewMockUsersClient(ctrl)
		mockIdpClient = idp.NewMockClientInterface(ctrl)
		ctx = context.Background()

		functionObj = &function{
			logger:                   logger,
			projectMembershipsClient: mockProjectMembershipsClient,
			projectsClient:           mockProjectsClient,
			usersClient:              mockUsersClient,
			idpClient:                mockIdpClient,
		}
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	It("should use diff path when FAILED with previous users", func() {
		newUser := privatev1.User_builder{
			Status: privatev1.UserStatus_builder{
				KeycloakUserId: "keycloak-new-id",
			}.Build(),
		}.Build()

		project := projectFixture("my-project", "")

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Tenant:     "acme",
				Project:    "my-project",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: userRefs("existing-user", "new-user"),
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State:   privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
				Message: proto.String("previous error"),
				Users:   userRefs("existing-user"),
			}.Build(),
		}.Build()

		// isTerminalFailure fetches the project, then passes it to handleUserListChange→resolveProjectGroup
		expectProject(mockProjectsClient, project)

		mockIdpClient.EXPECT().
			GetGroupIDByPath(gomock.Any(), "acme", "/my-project/system:managers").
			Return("group-id", nil)

		mockUsersClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.UsersGetResponse{Object: newUser}, nil)

		mockIdpClient.EXPECT().
			AddUserToGroup(gomock.Any(), "acme", "keycloak-new-id", "group-id").
			Return(nil)

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetStatus().GetUsers()).To(Equal(userRefs("existing-user", "new-user")))
		Expect(membership.GetStatus().GetMessage()).ToNot(ContainSubstring("previous error"))
	})

	It("should fall through to full sync when FAILED with no previous users", func() {
		user := privatev1.User_builder{
			Status: privatev1.UserStatus_builder{
				KeycloakUserId: "keycloak-alice-id",
			}.Build(),
		}.Build()

		project := projectFixture("my-project", "")

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Tenant:     "acme",
				Project:    "my-project",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: userRefs("user-id"),
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State:   privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
				Message: proto.String("previous error"),
			}.Build(),
		}.Build()

		// isTerminalFailure fetches the project, then passes it to syncProjectMembership→resolveProjectGroup
		expectProject(mockProjectsClient, project)

		mockIdpClient.EXPECT().
			GetGroupIDByPath(gomock.Any(), "acme", "/my-project/system:managers").
			Return("group-id", nil)

		mockUsersClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.UsersGetResponse{Object: user}, nil)

		mockIdpClient.EXPECT().
			AddUserToGroup(gomock.Any(), "acme", "keycloak-alice-id", "group-id").
			Return(nil)

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetStatus().GetState()).To(Equal(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY))
		Expect(membership.GetStatus().GetUsers()).To(Equal(userRefs("user-id")))
		Expect(membership.GetStatus().GetMessage()).To(Equal(""))
	})

	It("should not retry when project is deleted (terminal failure)", func() {
		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Tenant:     "acme",
				Project:    "deleted-project",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: userRefs("user-id"),
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State:   privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
				Message: proto.String("previous transient error"),
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsListResponse{}, nil)

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetStatus().GetState()).To(Equal(
			privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
		))
		Expect(membership.GetStatus().GetMessage()).To(ContainSubstring("no longer exists"))
	})

	It("should not retry when project is being deleted (terminal failure)", func() {
		project := projectFixture("my-project", "")
		project.GetMetadata().SetDeletionTimestamp(timestamppb.Now())

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Tenant:     "acme",
				Project:    "my-project",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: userRefs("user-id"),
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State:   privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
				Message: proto.String("previous error"),
			}.Build(),
		}.Build()

		expectProject(mockProjectsClient, project)

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetStatus().GetState()).To(Equal(
			privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
		))
		Expect(membership.GetStatus().GetMessage()).To(ContainSubstring("is being deleted"))
	})

	It("should not retry when role is invalid (terminal failure)", func() {
		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Project:    "project-id",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: userRefs("user-id"),
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_UNSPECIFIED,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State:   privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
				Message: proto.String("previous error"),
			}.Build(),
		}.Build()

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetStatus().GetState()).To(Equal(
			privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
		))
		Expect(membership.GetStatus().GetMessage()).To(ContainSubstring("Unknown project membership role"))
	})

	It("should not retry when membership has no tenant (terminal failure)", func() {
		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Project:    "project-id",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: userRefs("user-id"),
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State:   privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
				Message: proto.String("previous error"),
			}.Build(),
		}.Build()

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetStatus().GetState()).To(Equal(
			privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
		))
		Expect(membership.GetStatus().GetMessage()).To(ContainSubstring("no organization tenant"))
	})

	It("should retry when project fetch fails transiently", func() {
		user := privatev1.User_builder{
			Status: privatev1.UserStatus_builder{
				KeycloakUserId: "keycloak-id",
			}.Build(),
		}.Build()

		project := projectFixture("my-project", "")

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Tenant:     "acme",
				Project:    "my-project",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: userRefs("user-id"),
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State:   privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
				Message: proto.String("previous error"),
			}.Build(),
		}.Build()

		// First lookup is for isTerminalFailure — returns transient error.
		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(nil, status.Errorf(codes.Unavailable, "temporarily unavailable"))

		// isTerminalFailure sees transient error and returns nil project, so retry re-fetches.
		expectProject(mockProjectsClient, project)

		mockIdpClient.EXPECT().
			GetGroupIDByPath(gomock.Any(), "acme", "/my-project/system:managers").
			Return("group-id", nil)

		mockUsersClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.UsersGetResponse{Object: user}, nil)

		mockIdpClient.EXPECT().
			AddUserToGroup(gomock.Any(), "acme", "keycloak-id", "group-id").
			Return(nil)

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetStatus().GetState()).To(Equal(
			privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY,
		))
	})

	It("should clear error message when FAILED state is recovered with previous users", func() {
		project := projectFixture("my-project", "")

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Tenant:     "acme",
				Project:    "my-project",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: userRefs("user-a"),
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State:   privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
				Message: proto.String("Failed to sync user changes: add user user-a: failed to fetch user"),
				Users:   userRefs("user-a"),
			}.Build(),
		}.Build()

		// isTerminalFailure fetches the project to verify it still exists
		expectProject(mockProjectsClient, project)

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetStatus().GetMessage()).To(Not(ContainSubstring("Failed to sync")))
	})
})

var _ = Describe("Partial success tracking", func() {
	var (
		ctrl                         *gomock.Controller
		mockProjectsClient           *MockProjectsClient
		mockProjectMembershipsClient *MockProjectMembershipsClient
		mockUsersClient              *MockUsersClient
		mockIdpClient                *idp.MockClientInterface
		ctx                          context.Context
		functionObj                  *function
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockProjectsClient = NewMockProjectsClient(ctrl)
		mockProjectMembershipsClient = NewMockProjectMembershipsClient(ctrl)
		mockUsersClient = NewMockUsersClient(ctrl)
		mockIdpClient = idp.NewMockClientInterface(ctrl)
		ctx = context.Background()

		functionObj = &function{
			logger:                   logger,
			projectMembershipsClient: mockProjectMembershipsClient,
			projectsClient:           mockProjectsClient,
			usersClient:              mockUsersClient,
			idpClient:                mockIdpClient,
		}
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	Context("syncProjectMembership (full sync)", func() {
		It("should record successfully synced users on partial failure", func() {
			userA := privatev1.User_builder{
				Status: privatev1.UserStatus_builder{
					KeycloakUserId: "keycloak-a",
				}.Build(),
			}.Build()

			project := projectFixture("my-project", "")

			membership := privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.ProjectMembershipFinalizer},
					Tenant:     "acme",
					Project:    "my-project",
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Users: userRefs("user-a", "user-b"),
					Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
				}.Build(),
				Status: privatev1.ProjectMembershipStatus_builder{
					State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_PENDING,
				}.Build(),
			}.Build()

			expectProject(mockProjectsClient, project)

			mockIdpClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "acme", "/my-project/system:managers").
				Return("group-id", nil)

			// user-a succeeds
			mockUsersClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *privatev1.UsersGetRequest, _ ...interface{}) (*privatev1.UsersGetResponse, error) {
					if req.GetId() == "user-a" {
						return &privatev1.UsersGetResponse{Object: userA}, nil
					}
					return nil, status.Errorf(codes.NotFound, "user not found")
				}).Times(2)

			mockIdpClient.EXPECT().
				AddUserToGroup(gomock.Any(), "acme", "keycloak-a", "group-id").
				Return(nil)

			task := &task{
				r:          functionObj,
				membership: membership,
			}

			err := task.syncProjectMembership(ctx, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(membership.GetStatus().GetState()).To(Equal(
				privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
			))
			Expect(membership.GetStatus().GetUsers()).To(Equal(userRefs("user-a")))
			Expect(membership.GetStatus().GetMessage()).To(ContainSubstring("user-b"))
		})
	})

	Context("handleUserListChange (diff sync)", func() {
		It("should track partial add progress on failure", func() {
			userB := privatev1.User_builder{
				Status: privatev1.UserStatus_builder{
					KeycloakUserId: "keycloak-b",
				}.Build(),
			}.Build()

			project := projectFixture("my-project", "")

			membership := privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.ProjectMembershipFinalizer},
					Tenant:     "acme",
					Project:    "my-project",
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Users: userRefs("user-a", "user-b", "user-c"),
					Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
				}.Build(),
				Status: privatev1.ProjectMembershipStatus_builder{
					State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY,
					Users: userRefs("user-a"),
				}.Build(),
			}.Build()

			expectProject(mockProjectsClient, project)

			mockIdpClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "acme", "/my-project/system:managers").
				Return("group-id", nil)

			// user-b succeeds, user-c fails
			mockUsersClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *privatev1.UsersGetRequest, _ ...interface{}) (*privatev1.UsersGetResponse, error) {
					if req.GetId() == "user-b" {
						return &privatev1.UsersGetResponse{Object: userB}, nil
					}
					return nil, status.Errorf(codes.NotFound, "user not found")
				}).Times(2)

			mockIdpClient.EXPECT().
				AddUserToGroup(gomock.Any(), "acme", "keycloak-b", "group-id").
				Return(nil)

			task := &task{
				r:          functionObj,
				membership: membership,
			}

			err := task.handleUserListChange(ctx, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(membership.GetStatus().GetState()).To(Equal(
				privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
			))
			Expect(membership.GetStatus().GetUsers()).To(ConsistOf(userRefs("user-a", "user-b")))
			Expect(membership.GetStatus().GetMessage()).To(ContainSubstring("user-c"))
		})

		It("should track partial remove progress on failure", func() {
			userB := privatev1.User_builder{
				Status: privatev1.UserStatus_builder{
					KeycloakUserId: "keycloak-b",
				}.Build(),
			}.Build()

			project := projectFixture("my-project", "")

			membership := privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.ProjectMembershipFinalizer},
					Tenant:     "acme",
					Project:    "my-project",
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Users: userRefs("user-a"),
					Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
				}.Build(),
				Status: privatev1.ProjectMembershipStatus_builder{
					State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY,
					Users: userRefs("user-a", "user-b", "user-c"),
				}.Build(),
			}.Build()

			expectProject(mockProjectsClient, project)

			mockIdpClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "acme", "/my-project/system:managers").
				Return("group-id", nil)

			// user-b removal succeeds, user-c removal fails
			mockUsersClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *privatev1.UsersGetRequest, _ ...interface{}) (*privatev1.UsersGetResponse, error) {
					if req.GetId() == "user-b" {
						return &privatev1.UsersGetResponse{Object: userB}, nil
					}
					return nil, status.Errorf(codes.Internal, "internal error")
				}).Times(2)

			mockIdpClient.EXPECT().
				RemoveUserFromGroup(gomock.Any(), "acme", "keycloak-b", "group-id").
				Return(nil)

			task := &task{
				r:          functionObj,
				membership: membership,
			}

			err := task.handleUserListChange(ctx, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(membership.GetStatus().GetState()).To(Equal(
				privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
			))
			Expect(membership.GetStatus().GetUsers()).To(ConsistOf(userRefs("user-a", "user-c")))
			Expect(membership.GetStatus().GetMessage()).To(ContainSubstring("user-c"))
		})
	})

	Context("retry after partial failure", func() {
		It("should only process remaining users on retry from partial sync", func() {
			userB := privatev1.User_builder{
				Status: privatev1.UserStatus_builder{
					KeycloakUserId: "keycloak-b",
				}.Build(),
			}.Build()

			project := projectFixture("my-project", "")

			membership := privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.ProjectMembershipFinalizer},
					Tenant:     "acme",
					Project:    "my-project",
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Users: userRefs("user-a", "user-b"),
					Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
				}.Build(),
				Status: privatev1.ProjectMembershipStatus_builder{
					State:   privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
					Message: proto.String("Failed to sync 1 user(s): user user-b: failed to fetch user"),
					Users:   userRefs("user-a"),
				}.Build(),
			}.Build()

			// isTerminalFailure fetches the project, then passes it to handleUserListChange→resolveProjectGroup
			expectProject(mockProjectsClient, project)

			mockIdpClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "acme", "/my-project/system:managers").
				Return("group-id", nil)

			// Only user-b should be fetched — user-a is already in status.users
			mockUsersClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.UsersGetResponse{Object: userB}, nil)

			mockIdpClient.EXPECT().
				AddUserToGroup(gomock.Any(), "acme", "keycloak-b", "group-id").
				Return(nil)

			task := &task{
				r:          functionObj,
				membership: membership,
			}

			err := task.update(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(membership.GetStatus().GetUsers()).To(Equal(userRefs("user-a", "user-b")))
			Expect(membership.GetStatus().GetMessage()).ToNot(ContainSubstring("Failed"))
		})

		It("should preserve user-a and stay FAILED when user-b still fails on retry", func() {
			project := projectFixture("my-project", "")

			membership := privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.ProjectMembershipFinalizer},
					Tenant:     "acme",
					Project:    "my-project",
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Users: userRefs("user-a", "user-b"),
					Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
				}.Build(),
				Status: privatev1.ProjectMembershipStatus_builder{
					State:   privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
					Message: proto.String("Failed to sync 1 user(s): user user-b: failed to fetch user"),
					Users:   userRefs("user-a"),
				}.Build(),
			}.Build()

			// isTerminalFailure fetches the project — non-terminal, passes it downstream
			expectProject(mockProjectsClient, project)

			// handleUserListChange → resolveProjectGroup reuses the project from isTerminalFailure
			mockIdpClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "acme", "/my-project/system:managers").
				Return("group-id", nil)

			// user-b still fails on retry
			mockUsersClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(nil, status.Errorf(codes.Unavailable, "service unavailable"))

			task := &task{
				r:          functionObj,
				membership: membership,
			}

			err := task.update(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(membership.GetStatus().GetState()).To(Equal(
				privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
			))
			Expect(membership.GetStatus().GetUsers()).To(Equal(userRefs("user-a")))
			Expect(membership.GetStatus().GetMessage()).To(ContainSubstring("user-b"))
		})
	})
})

var _ = Describe("Role to Group Suffix Mapping", func() {
	It("should map VIEWER role to system:viewers suffix", func() {
		t := &task{}
		suffix := t.mapRoleToGroupSuffix(privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_VIEWER)
		Expect(suffix).To(Equal("system:viewers"))
	})

	It("should map MANAGER role to system:managers suffix", func() {
		t := &task{}
		suffix := t.mapRoleToGroupSuffix(privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER)
		Expect(suffix).To(Equal("system:managers"))
	})

	It("should return empty string for unspecified role", func() {
		t := &task{}
		suffix := t.mapRoleToGroupSuffix(privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_UNSPECIFIED)
		Expect(suffix).To(Equal(""))
	})
})

var _ = Describe("Project Group Path Building", func() {
	var (
		ctrl                         *gomock.Controller
		mockProjectsClient           *MockProjectsClient
		mockProjectMembershipsClient *MockProjectMembershipsClient
		mockUsersClient              *MockUsersClient
		mockIdpClient                *idp.MockClientInterface
		ctx                          context.Context
		functionObj                  *function
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockProjectsClient = NewMockProjectsClient(ctrl)
		mockProjectMembershipsClient = NewMockProjectMembershipsClient(ctrl)
		mockUsersClient = NewMockUsersClient(ctrl)
		mockIdpClient = idp.NewMockClientInterface(ctrl)
		ctx = context.Background()

		functionObj = &function{
			logger:                   logger,
			projectMembershipsClient: mockProjectMembershipsClient,
			projectsClient:           mockProjectsClient,
			usersClient:              mockUsersClient,
			idpClient:                mockIdpClient,
		}
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	Context("Top-level projects (no parent)", func() {
		It("should build simple path for top-level project", func() {
			project := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "my-project",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			task := &task{
				r: functionObj,
			}

			path, err := task.buildProjectGroupPath(ctx, project, "system:managers")
			Expect(err).ToNot(HaveOccurred())
			Expect(path).To(Equal("/my-project/system:managers"))
		})

		It("should build path with viewers suffix", func() {
			project := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "my-project",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			task := &task{
				r: functionObj,
			}

			path, err := task.buildProjectGroupPath(ctx, project, "system:viewers")
			Expect(err).ToNot(HaveOccurred())
			Expect(path).To(Equal("/my-project/system:viewers"))
		})
	})

	Context("Nested projects (with parent)", func() {
		It("should use the full name for a legacy project without a stored parent", func() {
			project := projectFixture("parent.child", "")
			task := &task{r: functionObj}
			path, err := task.buildProjectGroupPath(ctx, project, "system:viewers")
			Expect(err).NotTo(HaveOccurred())
			Expect(path).To(Equal("/parent/child/system:viewers"))
		})

		It("should build hierarchical path for one-level nesting", func() {
			childProject := projectFixture("parent.child", "parent")
			expectProject(mockProjectsClient, projectFixture("parent", ""))

			task := &task{
				r: functionObj,
			}

			path, err := task.buildProjectGroupPath(ctx, childProject, "system:managers")
			Expect(err).ToNot(HaveOccurred())
			Expect(path).To(Equal("/parent/child/system:managers"))
		})

		It("should build hierarchical path for two-level nesting", func() {
			childProject := projectFixture("root.parent.child", "root.parent")
			expectProject(mockProjectsClient, projectFixture("root.parent", "root"))
			expectProject(mockProjectsClient, projectFixture("root", ""))

			task := &task{
				r: functionObj,
			}

			path, err := task.buildProjectGroupPath(ctx, childProject, "system:viewers")
			Expect(err).ToNot(HaveOccurred())
			Expect(path).To(Equal("/root/parent/child/system:viewers"))
		})

		It("should build hierarchical path for three-level nesting", func() {
			componentProject := projectFixture("org.team.product.component", "org.team.product")
			expectProject(mockProjectsClient, projectFixture("org.team.product", "org.team"))
			expectProject(mockProjectsClient, projectFixture("org.team", "org"))
			expectProject(mockProjectsClient, projectFixture("org", ""))

			task := &task{
				r: functionObj,
			}

			path, err := task.buildProjectGroupPath(ctx, componentProject, "system:managers")
			Expect(err).ToNot(HaveOccurred())
			Expect(path).To(Equal("/org/team/product/component/system:managers"))
		})
	})

	Context("Error handling", func() {
		It("should return error when parent project fetch fails", func() {
			childProject := projectFixture("parent.child", "parent")

			mockProjectsClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsListResponse{Items: []*privatev1.Project{}}, nil)

			task := &task{
				r: functionObj,
			}

			path, err := task.buildProjectGroupPath(ctx, childProject, "managers")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to fetch parent project"))
			Expect(path).To(Equal(""))
		})

		It("should reject a parent that does not match the name prefix", func() {
			childProject := projectFixture("parent.child", "other-parent")
			task := &task{r: functionObj}
			path, err := task.buildProjectGroupPath(ctx, childProject, "system:managers")
			Expect(err).To(MatchError(ContainSubstring("parent project does not match the project name prefix")))
			Expect(path).To(Equal(""))
		})
	})
})

var _ = Describe("Synchronization", func() {
	var (
		ctrl                         *gomock.Controller
		mockProjectsClient           *MockProjectsClient
		mockProjectMembershipsClient *MockProjectMembershipsClient
		mockUsersClient              *MockUsersClient
		mockIdpClient                *idp.MockClientInterface
		ctx                          context.Context
		functionObj                  *function
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockProjectsClient = NewMockProjectsClient(ctrl)
		mockProjectMembershipsClient = NewMockProjectMembershipsClient(ctrl)
		mockUsersClient = NewMockUsersClient(ctrl)
		mockIdpClient = idp.NewMockClientInterface(ctrl)
		ctx = context.Background()

		functionObj = &function{
			logger:                   logger,
			projectMembershipsClient: mockProjectMembershipsClient,
			projectsClient:           mockProjectsClient,
			usersClient:              mockUsersClient,
			idpClient:                mockIdpClient,
		}
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	Context("Top-level project membership", func() {
		It("should add user to authorization group for top-level project", func() {
			user := privatev1.User_builder{
				Spec: privatev1.UserSpec_builder{
					Username: "alice",
				}.Build(),
				Status: privatev1.UserStatus_builder{
					KeycloakUserId: "keycloak-alice-id",
				}.Build(),
			}.Build()

			project := projectFixture("my-project", "")

			membership := privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: "acme", Project: "my-project",
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Users: userRefs("user-id"),
					Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
				}.Build(),
				Status: privatev1.ProjectMembershipStatus_builder{
					State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_PENDING,
				}.Build(),
			}.Build()

			mockUsersClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.UsersGetResponse{Object: user}, nil)

			expectProject(mockProjectsClient, project)

			mockIdpClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "acme", "/my-project/system:managers").
				Return("group-id", nil)

			mockIdpClient.EXPECT().
				AddUserToGroup(gomock.Any(), "acme", "keycloak-alice-id", "group-id").
				Return(nil)

			task := &task{
				r:          functionObj,
				membership: membership,
			}

			err := task.syncProjectMembership(ctx, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(membership.GetStatus().GetState()).To(Equal(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY))
			Expect(membership.GetStatus().GetMessage()).To(Equal(""))
			Expect(membership.GetStatus().GetUsers()).To(Equal(userRefs("user-id")))
		})
	})

	Context("Nested project membership", func() {
		It("should add user to hierarchical authorization group", func() {
			user := privatev1.User_builder{
				Spec: privatev1.UserSpec_builder{
					Username: "alice",
				}.Build(),
				Status: privatev1.UserStatus_builder{
					KeycloakUserId: "keycloak-alice-id",
				}.Build(),
			}.Build()

			parentProject := projectFixture("parent", "")
			childProject := projectFixture("parent.child", "parent")

			membership := privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: "acme", Project: "parent.child",
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Users: userRefs("user-id"),
					Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_VIEWER,
				}.Build(),
				Status: privatev1.ProjectMembershipStatus_builder{
					State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_PENDING,
				}.Build(),
			}.Build()

			mockUsersClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.UsersGetResponse{Object: user}, nil)

			expectProject(mockProjectsClient, childProject)
			expectProject(mockProjectsClient, parentProject)

			mockIdpClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "acme", "/parent/child/system:viewers").
				Return("group-id", nil)

			mockIdpClient.EXPECT().
				AddUserToGroup(gomock.Any(), "acme", "keycloak-alice-id", "group-id").
				Return(nil)

			task := &task{
				r:          functionObj,
				membership: membership,
			}

			err := task.syncProjectMembership(ctx, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(membership.GetStatus().GetState()).To(Equal(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY))
			Expect(membership.GetStatus().GetUsers()).To(Equal(userRefs("user-id")))
		})
	})

	Context("Error handling", func() {
		It("should fail when user does not exist", func() {
			project := projectFixture("my-project", "")

			membership := privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: "acme", Project: "my-project",
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Users: userRefs("nonexistent-user"),
					Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
				}.Build(),
				Status: privatev1.ProjectMembershipStatus_builder{
					State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_PENDING,
				}.Build(),
			}.Build()

			expectProject(mockProjectsClient, project)

			mockIdpClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "acme", "/my-project/system:managers").
				Return("group-id", nil)

			mockUsersClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(nil, status.Error(codes.NotFound, "user not found"))

			task := &task{
				r:          functionObj,
				membership: membership,
			}

			err := task.syncProjectMembership(ctx, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(membership.GetStatus().GetState()).To(Equal(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED))
			Expect(membership.GetStatus().GetMessage()).To(ContainSubstring("failed to fetch user"))
		})

		It("should fail when project does not exist", func() {
			membership := privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: "acme", Project: "nonexistent-project",
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Users: userRefs("user-id"),
					Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
				}.Build(),
				Status: privatev1.ProjectMembershipStatus_builder{
					State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_PENDING,
				}.Build(),
			}.Build()

			mockProjectsClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsListResponse{Items: []*privatev1.Project{}}, nil)

			task := &task{
				r:          functionObj,
				membership: membership,
			}

			err := task.syncProjectMembership(ctx, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(membership.GetStatus().GetState()).To(Equal(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED))
			Expect(membership.GetStatus().GetMessage()).To(ContainSubstring("Failed to fetch project"))
		})

		It("should fail when authorization group does not exist", func() {
			project := projectFixture("my-project", "")

			membership := privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: "acme", Project: "my-project",
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Users: userRefs("user-id"),
					Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
				}.Build(),
				Status: privatev1.ProjectMembershipStatus_builder{
					State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_PENDING,
				}.Build(),
			}.Build()

			expectProject(mockProjectsClient, project)

			mockIdpClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "acme", "/my-project/system:managers").
				Return("", status.Error(codes.NotFound, "group not found"))

			task := &task{
				r:          functionObj,
				membership: membership,
			}

			err := task.syncProjectMembership(ctx, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(membership.GetStatus().GetState()).To(Equal(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED))
			Expect(membership.GetStatus().GetMessage()).To(ContainSubstring("Failed to find authorization group"))
		})
	})
})

var _ = Describe("Stored project binding", func() {
	DescribeTable("rejects a mismatched project before touching the identity provider", func(target, lookup, returnedName, returnedTenant string) {
		ctrl := gomock.NewController(GinkgoT())
		projects := NewMockProjectsClient(ctrl)
		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Tenant:     "acme",
				Project:    target,
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: userRefs("user-id"),
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
		}.Build()
		task := &task{
			r: &function{
				logger:         logger,
				projectsClient: projects,
				usersClient:    NewMockUsersClient(ctrl),
				idpClient:      idp.NewMockClientInterface(ctrl),
			},
			membership: membership,
		}
		if target != lookup {
			expectProject(projects, projectFixture(target, lookup)).Times(2)
		}
		returned := projectFixture(returnedName, "")
		returned.GetMetadata().SetTenant(returnedTenant)
		projects.EXPECT().List(gomock.Any(), projectFilter("acme", lookup)).
			Return(&privatev1.ProjectsListResponse{Items: []*privatev1.Project{returned}}, nil).Times(2)

		ctx := context.Background()
		Expect(task.update(ctx)).To(Succeed())
		Expect(membership.GetStatus().GetState()).To(Equal(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED))
		Expect(membership.GetStatus().GetMessage()).To(ContainSubstring("did not match the stored tenant and project reference"))
		Expect(status.Code(task.delete(ctx))).To(Equal(codes.FailedPrecondition))
		Expect(membership.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.ProjectMembershipFinalizer))
	},
		Entry("membership target tenant differs", "parent", "parent", "parent", "other-tenant"),
		Entry("membership target name differs", "parent", "parent", "other-project", "acme"),
		Entry("parent tenant differs", "parent.child", "parent", "parent", "other-tenant"),
		Entry("parent name differs", "parent.child", "parent", "other-project", "acme"),
	)
})

var _ = Describe("Deletion Cleanup", func() {
	var (
		ctrl                         *gomock.Controller
		mockProjectsClient           *MockProjectsClient
		mockProjectMembershipsClient *MockProjectMembershipsClient
		mockUsersClient              *MockUsersClient
		mockIdpClient                *idp.MockClientInterface
		ctx                          context.Context
		functionObj                  *function
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockProjectsClient = NewMockProjectsClient(ctrl)
		mockProjectMembershipsClient = NewMockProjectMembershipsClient(ctrl)
		mockUsersClient = NewMockUsersClient(ctrl)
		mockIdpClient = idp.NewMockClientInterface(ctrl)
		ctx = context.Background()

		functionObj = &function{
			logger:                   logger,
			projectMembershipsClient: mockProjectMembershipsClient,
			projectsClient:           mockProjectsClient,
			usersClient:              mockUsersClient,
			idpClient:                mockIdpClient,
		}
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	It("should remove users from authorization group on deletion", func() {
		user := privatev1.User_builder{
			Spec: privatev1.UserSpec_builder{
				Username: "alice",
			}.Build(),
			Status: privatev1.UserStatus_builder{
				KeycloakUserId: "keycloak-alice-id",
			}.Build(),
		}.Build()

		project := projectFixture("my-project", "")

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Tenant:     "acme",
				Project:    "my-project",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: userRefs("user-id"),
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				Users: userRefs("user-id"),
			}.Build(),
		}.Build()

		expectProject(mockProjectsClient, project).Times(2)

		mockIdpClient.EXPECT().
			GetGroupIDByPath(gomock.Any(), "acme", "/my-project/system:managers").
			Return("group-id", nil)

		mockUsersClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.UsersGetResponse{Object: user}, nil)

		mockIdpClient.EXPECT().
			RemoveUserFromGroup(gomock.Any(), "acme", "keycloak-alice-id", "group-id").
			Return(nil)

		mockProjectsClient.EXPECT().
			Signal(gomock.Any(), gomock.Cond(func(req *privatev1.ProjectsSignalRequest) bool {
				return req.GetId() == project.GetId()
			})).
			Return(privatev1.ProjectsSignalResponse_builder{}.Build(), nil)

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.ProjectMembershipFinalizer))
	})

	It("should remove users from nested project authorization group on deletion", func() {
		user := privatev1.User_builder{
			Spec: privatev1.UserSpec_builder{
				Username: "alice",
			}.Build(),
			Status: privatev1.UserStatus_builder{
				KeycloakUserId: "keycloak-alice-id",
			}.Build(),
		}.Build()

		parentProject := projectFixture("parent", "")
		childProject := projectFixture("parent.child", "parent")
		childProject.GetMetadata().SetDeletionTimestamp(timestamppb.Now())

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Tenant:     "acme",
				Project:    "parent.child",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: userRefs("user-id"),
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_VIEWER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				Users: userRefs("user-id"),
			}.Build(),
		}.Build()

		expectProject(mockProjectsClient, childProject).Times(2)
		expectProject(mockProjectsClient, parentProject)

		mockIdpClient.EXPECT().
			GetGroupIDByPath(gomock.Any(), "acme", "/parent/child/system:viewers").
			Return("group-id", nil)

		mockUsersClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.UsersGetResponse{Object: user}, nil)

		mockIdpClient.EXPECT().
			RemoveUserFromGroup(gomock.Any(), "acme", "keycloak-alice-id", "group-id").
			Return(nil)

		mockProjectsClient.EXPECT().
			Signal(gomock.Any(), gomock.Any()).
			Return(privatev1.ProjectsSignalResponse_builder{}.Build(), nil)

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.ProjectMembershipFinalizer))
	})

	It("should remove finalizer when project not found during cleanup", func() {
		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Tenant:     "acme",
				Project:    "my-project",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: userRefs("user-id"),
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				Users: userRefs("user-id"),
			}.Build(),
		}.Build()

		// Neither cleanup nor signaling finds the project by its tenant and name.
		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsListResponse{}, nil).Times(2)

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.ProjectMembershipFinalizer))
	})

	It("should handle group not found during cleanup with gRPC status code", func() {
		project := projectFixture("my-project", "")

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Tenant:     "acme",
				Project:    "my-project",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: userRefs("user-id"),
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				Users: userRefs("user-id"),
			}.Build(),
		}.Build()

		expectProject(mockProjectsClient, project).Times(2)

		mockIdpClient.EXPECT().
			GetGroupIDByPath(gomock.Any(), "acme", "/my-project/system:managers").
			Return("", status.Error(codes.NotFound, "group not found"))

		mockProjectsClient.EXPECT().
			Signal(gomock.Any(), gomock.Any()).
			Return(privatev1.ProjectsSignalResponse_builder{}.Build(), nil)

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.ProjectMembershipFinalizer))
	})

	It("should handle group not found during cleanup with wrapped error message", func() {
		project := projectFixture("my-project", "")

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Tenant:     "acme",
				Project:    "my-project",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: userRefs("user-id"),
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_VIEWER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				Users: userRefs("user-id"),
			}.Build(),
		}.Build()

		expectProject(mockProjectsClient, project).Times(2)

		mockIdpClient.EXPECT().
			GetGroupIDByPath(gomock.Any(), "acme", "/my-project/system:viewers").
			Return("", fmt.Errorf("wrapped error: group not found in keycloak"))

		mockProjectsClient.EXPECT().
			Signal(gomock.Any(), gomock.Any()).
			Return(privatev1.ProjectsSignalResponse_builder{}.Build(), nil)

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.ProjectMembershipFinalizer))
	})
})

var _ = Describe("Signal Project on Deletion", func() {
	var (
		ctrl                         *gomock.Controller
		mockProjectsClient           *MockProjectsClient
		mockProjectMembershipsClient *MockProjectMembershipsClient
		mockUsersClient              *MockUsersClient
		mockIdpClient                *idp.MockClientInterface
		ctx                          context.Context
		functionObj                  *function
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockProjectsClient = NewMockProjectsClient(ctrl)
		mockProjectMembershipsClient = NewMockProjectMembershipsClient(ctrl)
		mockUsersClient = NewMockUsersClient(ctrl)
		mockIdpClient = idp.NewMockClientInterface(ctrl)
		ctx = context.Background()

		functionObj = &function{
			logger:                   logger,
			projectMembershipsClient: mockProjectMembershipsClient,
			projectsClient:           mockProjectsClient,
			usersClient:              mockUsersClient,
			idpClient:                mockIdpClient,
		}
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	It("should not signal when project name is empty", func() {
		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Role: privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
		}.Build()

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.ProjectMembershipFinalizer))
	})

	It("should continue delete when project lookup fails during signaling", func() {
		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Tenant:     "acme",
				Project:    "my-project",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Role: privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(nil, status.Errorf(codes.Unavailable, "service unavailable"))

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.ProjectMembershipFinalizer))
	})

	It("should skip signaling when project is not found by name", func() {
		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Tenant:     "acme",
				Project:    "nonexistent-project",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Role: privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsListResponse{}, nil)

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.ProjectMembershipFinalizer))
	})

	It("should continue delete when Signal RPC fails", func() {
		project := projectFixture("my-project", "")
		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Tenant:     "acme",
				Project:    "my-project",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Role: privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
		}.Build()

		expectProject(mockProjectsClient, project)

		mockProjectsClient.EXPECT().
			Signal(gomock.Any(), gomock.Any()).
			Return(nil, status.Errorf(codes.Unavailable, "service unavailable"))

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.ProjectMembershipFinalizer))
	})
})

var _ = Describe("User List Change Handling", func() {
	var (
		ctrl                         *gomock.Controller
		mockProjectsClient           *MockProjectsClient
		mockProjectMembershipsClient *MockProjectMembershipsClient
		mockUsersClient              *MockUsersClient
		mockIdpClient                *idp.MockClientInterface
		ctx                          context.Context
		functionObj                  *function
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockProjectsClient = NewMockProjectsClient(ctrl)
		mockProjectMembershipsClient = NewMockProjectMembershipsClient(ctrl)
		mockUsersClient = NewMockUsersClient(ctrl)
		mockIdpClient = idp.NewMockClientInterface(ctrl)
		ctx = context.Background()

		functionObj = &function{
			logger:                   logger,
			projectMembershipsClient: mockProjectMembershipsClient,
			projectsClient:           mockProjectsClient,
			usersClient:              mockUsersClient,
			idpClient:                mockIdpClient,
		}
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	It("should add new users when spec.users grows", func() {
		newUser := privatev1.User_builder{
			Status: privatev1.UserStatus_builder{
				KeycloakUserId: "keycloak-new-id",
			}.Build(),
		}.Build()

		project := projectFixture("my-project", "")

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "acme", Project: "my-project",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: userRefs("existing-user", "new-user"),
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY,
				Users: userRefs("existing-user"),
			}.Build(),
		}.Build()

		expectProject(mockProjectsClient, project)

		mockIdpClient.EXPECT().
			GetGroupIDByPath(gomock.Any(), "acme", "/my-project/system:managers").
			Return("group-id", nil)

		mockUsersClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.UsersGetResponse{Object: newUser}, nil)

		mockIdpClient.EXPECT().
			AddUserToGroup(gomock.Any(), "acme", "keycloak-new-id", "group-id").
			Return(nil)

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.handleUserListChange(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetStatus().GetUsers()).To(Equal(userRefs("existing-user", "new-user")))
	})

	It("should remove users when spec.users shrinks", func() {
		removedUser := privatev1.User_builder{
			Status: privatev1.UserStatus_builder{
				KeycloakUserId: "keycloak-removed-id",
			}.Build(),
		}.Build()

		project := projectFixture("my-project", "")

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "acme", Project: "my-project",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: userRefs("remaining-user"),
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY,
				Users: userRefs("remaining-user", "removed-user"),
			}.Build(),
		}.Build()

		expectProject(mockProjectsClient, project)

		mockIdpClient.EXPECT().
			GetGroupIDByPath(gomock.Any(), "acme", "/my-project/system:managers").
			Return("group-id", nil)

		mockUsersClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.UsersGetResponse{Object: removedUser}, nil)

		mockIdpClient.EXPECT().
			RemoveUserFromGroup(gomock.Any(), "acme", "keycloak-removed-id", "group-id").
			Return(nil)

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.handleUserListChange(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetStatus().GetUsers()).To(Equal(userRefs("remaining-user")))
	})

	It("should add and remove users simultaneously", func() {
		newUser := privatev1.User_builder{
			Status: privatev1.UserStatus_builder{
				KeycloakUserId: "keycloak-new-id",
			}.Build(),
		}.Build()

		removedUser := privatev1.User_builder{
			Status: privatev1.UserStatus_builder{
				KeycloakUserId: "keycloak-removed-id",
			}.Build(),
		}.Build()

		project := projectFixture("my-project", "")

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "acme", Project: "my-project",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: userRefs("kept-user", "new-user"),
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_VIEWER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY,
				Users: userRefs("kept-user", "removed-user"),
			}.Build(),
		}.Build()

		expectProject(mockProjectsClient, project)

		mockIdpClient.EXPECT().
			GetGroupIDByPath(gomock.Any(), "acme", "/my-project/system:viewers").
			Return("group-id", nil)

		mockUsersClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.UsersGetResponse{Object: removedUser}, nil)

		mockIdpClient.EXPECT().
			RemoveUserFromGroup(gomock.Any(), "acme", "keycloak-removed-id", "group-id").
			Return(nil)

		mockUsersClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.UsersGetResponse{Object: newUser}, nil)

		mockIdpClient.EXPECT().
			AddUserToGroup(gomock.Any(), "acme", "keycloak-new-id", "group-id").
			Return(nil)

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.handleUserListChange(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetStatus().GetUsers()).To(Equal(userRefs("kept-user", "new-user")))
	})

	It("should be a no-op when user lists are identical", func() {
		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Project: "project-id",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: userRefs("user-a", "user-b"),
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY,
				Users: userRefs("user-a", "user-b"),
			}.Build(),
		}.Build()

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.handleUserListChange(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetStatus().GetState()).To(Equal(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY))
	})

	It("should set FAILED state when adding a user fails", func() {
		project := projectFixture("my-project", "")

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "acme", Project: "my-project",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: userRefs("existing-user", "failing-user"),
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY,
				Users: userRefs("existing-user"),
			}.Build(),
		}.Build()

		expectProject(mockProjectsClient, project)

		mockIdpClient.EXPECT().
			GetGroupIDByPath(gomock.Any(), "acme", "/my-project/system:managers").
			Return("group-id", nil)

		mockUsersClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(nil, status.Error(codes.NotFound, "user not found"))

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.handleUserListChange(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetStatus().GetState()).To(Equal(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED))
		Expect(membership.GetStatus().GetMessage()).To(ContainSubstring("Failed to sync user changes"))
	})
})
