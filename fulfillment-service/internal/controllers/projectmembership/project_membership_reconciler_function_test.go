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

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/fulfillment-service/internal/idp"
)

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

		project := privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   "my-project",
				Tenant: "acme",
			}.Build(),
			Spec: privatev1.ProjectSpec_builder{}.Build(),
		}.Build()

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Project:    "project-id",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: []string{"existing-user", "new-user"},
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State:   privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
				Message: proto.String("previous error"),
				Users:   []string{"existing-user"},
			}.Build(),
		}.Build()

		// isTerminalFailure fetches the project, then passes it to handleUserListChange→resolveProjectGroup
		mockProjectsClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsGetResponse{Object: project}, nil)

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
		Expect(membership.GetStatus().GetUsers()).To(Equal([]string{"existing-user", "new-user"}))
		Expect(membership.GetStatus().GetMessage()).ToNot(ContainSubstring("previous error"))
	})

	It("should fall through to full sync when FAILED with no previous users", func() {
		user := privatev1.User_builder{
			Status: privatev1.UserStatus_builder{
				KeycloakUserId: "keycloak-alice-id",
			}.Build(),
		}.Build()

		project := privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   "my-project",
				Tenant: "acme",
			}.Build(),
			Spec: privatev1.ProjectSpec_builder{}.Build(),
		}.Build()

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Project:    "project-id",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: []string{"user-id"},
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State:   privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
				Message: proto.String("previous error"),
			}.Build(),
		}.Build()

		// isTerminalFailure fetches the project, then passes it to syncProjectMembership→resolveProjectGroup
		mockProjectsClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsGetResponse{Object: project}, nil)

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
		Expect(membership.GetStatus().GetUsers()).To(Equal([]string{"user-id"}))
		Expect(membership.GetStatus().GetMessage()).To(Equal(""))
	})

	It("should not retry when project is deleted (terminal failure)", func() {
		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Project:    "deleted-project-id",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: []string{"user-id"},
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State:   privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
				Message: proto.String("previous transient error"),
			}.Build(),
		}.Build()

		// Get returns NotFound, then getProjectByNameOrID falls back to List by name
		mockProjectsClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(nil, status.Errorf(codes.NotFound, "not found"))
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
		project := privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{
				Name:              "my-project",
				Tenant:            "acme",
				DeletionTimestamp: timestamppb.Now(),
			}.Build(),
			Spec: privatev1.ProjectSpec_builder{}.Build(),
		}.Build()

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Project:    "project-id",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: []string{"user-id"},
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State:   privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
				Message: proto.String("previous error"),
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsGetResponse{Object: project}, nil)

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
				Users: []string{"user-id"},
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

	It("should not retry when project has no tenant (terminal failure)", func() {
		project := privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{
				Name: "my-project",
			}.Build(),
			Spec: privatev1.ProjectSpec_builder{}.Build(),
		}.Build()

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Project:    "project-id",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: []string{"user-id"},
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State:   privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
				Message: proto.String("previous error"),
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsGetResponse{Object: project}, nil)

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

		project := privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   "my-project",
				Tenant: "acme",
			}.Build(),
			Spec: privatev1.ProjectSpec_builder{}.Build(),
		}.Build()

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Project:    "project-id",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: []string{"user-id"},
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State:   privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
				Message: proto.String("previous error"),
			}.Build(),
		}.Build()

		// First Get call is for isTerminalFailure — returns transient error
		mockProjectsClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(nil, status.Errorf(codes.Unavailable, "temporarily unavailable"))

		// isTerminalFailure sees transient error and returns nil project, so retry re-fetches.
		// Full sync calls getProjectByNameOrID again, which succeeds this time.
		mockProjectsClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsGetResponse{Object: project}, nil)

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
		project := privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   "my-project",
				Tenant: "acme",
			}.Build(),
			Spec: privatev1.ProjectSpec_builder{}.Build(),
		}.Build()

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Project:    "project-id",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: []string{"user-a"},
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State:   privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
				Message: proto.String("Failed to sync user changes: add user user-a: failed to fetch user"),
				Users:   []string{"user-a"},
			}.Build(),
		}.Build()

		// isTerminalFailure fetches the project to verify it still exists
		mockProjectsClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsGetResponse{Object: project}, nil)

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

			project := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "my-project",
					Tenant: "acme",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			membership := privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.ProjectMembershipFinalizer},
					Project:    "project-id",
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Users: []string{"user-a", "user-b"},
					Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
				}.Build(),
				Status: privatev1.ProjectMembershipStatus_builder{
					State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_PENDING,
				}.Build(),
			}.Build()

			mockProjectsClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsGetResponse{Object: project}, nil)

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
			Expect(membership.GetStatus().GetUsers()).To(Equal([]string{"user-a"}))
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

			project := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "my-project",
					Tenant: "acme",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			membership := privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.ProjectMembershipFinalizer},
					Project:    "project-id",
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Users: []string{"user-a", "user-b", "user-c"},
					Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
				}.Build(),
				Status: privatev1.ProjectMembershipStatus_builder{
					State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY,
					Users: []string{"user-a"},
				}.Build(),
			}.Build()

			mockProjectsClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsGetResponse{Object: project}, nil)

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
			Expect(membership.GetStatus().GetUsers()).To(ConsistOf("user-a", "user-b"))
			Expect(membership.GetStatus().GetMessage()).To(ContainSubstring("user-c"))
		})

		It("should track partial remove progress on failure", func() {
			userB := privatev1.User_builder{
				Status: privatev1.UserStatus_builder{
					KeycloakUserId: "keycloak-b",
				}.Build(),
			}.Build()

			project := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "my-project",
					Tenant: "acme",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			membership := privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.ProjectMembershipFinalizer},
					Project:    "project-id",
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Users: []string{"user-a"},
					Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
				}.Build(),
				Status: privatev1.ProjectMembershipStatus_builder{
					State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY,
					Users: []string{"user-a", "user-b", "user-c"},
				}.Build(),
			}.Build()

			mockProjectsClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsGetResponse{Object: project}, nil)

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
			Expect(membership.GetStatus().GetUsers()).To(ConsistOf("user-a", "user-c"))
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

			project := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "my-project",
					Tenant: "acme",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			membership := privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.ProjectMembershipFinalizer},
					Project:    "project-id",
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Users: []string{"user-a", "user-b"},
					Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
				}.Build(),
				Status: privatev1.ProjectMembershipStatus_builder{
					State:   privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
					Message: proto.String("Failed to sync 1 user(s): user user-b: failed to fetch user"),
					Users:   []string{"user-a"},
				}.Build(),
			}.Build()

			// isTerminalFailure fetches the project, then passes it to handleUserListChange→resolveProjectGroup
			mockProjectsClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsGetResponse{Object: project}, nil)

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
			Expect(membership.GetStatus().GetUsers()).To(Equal([]string{"user-a", "user-b"}))
			Expect(membership.GetStatus().GetMessage()).ToNot(ContainSubstring("Failed"))
		})

		It("should preserve user-a and stay FAILED when user-b still fails on retry", func() {
			project := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "my-project",
					Tenant: "acme",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			membership := privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.ProjectMembershipFinalizer},
					Project:    "project-id",
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Users: []string{"user-a", "user-b"},
					Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
				}.Build(),
				Status: privatev1.ProjectMembershipStatus_builder{
					State:   privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
					Message: proto.String("Failed to sync 1 user(s): user user-b: failed to fetch user"),
					Users:   []string{"user-a"},
				}.Build(),
			}.Build()

			// isTerminalFailure fetches the project — non-terminal, passes it downstream
			mockProjectsClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsGetResponse{Object: project}, nil)

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
			Expect(membership.GetStatus().GetUsers()).To(Equal([]string{"user-a"}))
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
		It("should build hierarchical path for one-level nesting", func() {
			parentProject := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "parent",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			childProject := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:    "child",
					Project: "parent",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			mockProjectsClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsGetResponse{Object: parentProject}, nil)

			task := &task{
				r: functionObj,
			}

			path, err := task.buildProjectGroupPath(ctx, childProject, "system:managers")
			Expect(err).ToNot(HaveOccurred())
			Expect(path).To(Equal("/parent/child/system:managers"))
		})

		It("should build hierarchical path for two-level nesting", func() {
			rootProject := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "root",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			parentProject := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:    "parent",
					Project: "root",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			childProject := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:    "child",
					Project: "parent",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			// First call: get parent project
			mockProjectsClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsGetResponse{Object: parentProject}, nil)

			// Second call: get root project
			mockProjectsClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsGetResponse{Object: rootProject}, nil)

			task := &task{
				r: functionObj,
			}

			path, err := task.buildProjectGroupPath(ctx, childProject, "system:viewers")
			Expect(err).ToNot(HaveOccurred())
			Expect(path).To(Equal("/root/parent/child/system:viewers"))
		})

		It("should build hierarchical path for three-level nesting", func() {
			orgProject := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "org",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			teamProject := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:    "team",
					Project: "org",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			productProject := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:    "product",
					Project: "team",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			componentProject := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:    "component",
					Project: "product",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			// First call: get product project
			mockProjectsClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsGetResponse{Object: productProject}, nil)

			// Second call: get team project
			mockProjectsClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsGetResponse{Object: teamProject}, nil)

			// Third call: get org project
			mockProjectsClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsGetResponse{Object: orgProject}, nil)

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
			childProject := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:    "child",
					Project: "parent",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			// getProjectByNameOrID will try Get first, then List
			mockProjectsClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(nil, status.Error(codes.NotFound, "parent not found"))

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

		It("should detect circular reference and return error", func() {
			// Create a scenario where we hit max depth
			// This simulates a circular reference by creating a deep chain
			deepProject := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:    "level",
					Project: "parent",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			// Mock calls up to max depth
			for i := 0; i < MaxProjectHierarchyDepth; i++ {
				mockProjectsClient.EXPECT().
					Get(gomock.Any(), gomock.Any()).
					Return(&privatev1.ProjectsGetResponse{Object: deepProject}, nil)
			}

			task := &task{
				r: functionObj,
			}

			path, err := task.buildProjectGroupPath(ctx, deepProject, "system:managers")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("exceeded maximum depth"))
			Expect(err.Error()).To(ContainSubstring("circular reference"))
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

			project := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "my-project",
					Tenant: "acme",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			membership := privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Project: "project-id",
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Users: []string{"user-id"},
					Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
				}.Build(),
				Status: privatev1.ProjectMembershipStatus_builder{
					State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_PENDING,
				}.Build(),
			}.Build()

			mockUsersClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.UsersGetResponse{Object: user}, nil)

			mockProjectsClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsGetResponse{Object: project}, nil)

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
			Expect(membership.GetStatus().GetUsers()).To(Equal([]string{"user-id"}))
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

			parentProject := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "parent",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			childProject := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:    "child",
					Tenant:  "acme",
					Project: "parent",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			membership := privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Project: "child-project-id",
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Users: []string{"user-id"},
					Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_VIEWER,
				}.Build(),
				Status: privatev1.ProjectMembershipStatus_builder{
					State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_PENDING,
				}.Build(),
			}.Build()

			mockUsersClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.UsersGetResponse{Object: user}, nil)

			mockProjectsClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsGetResponse{Object: childProject}, nil)

			// buildProjectGroupPath will fetch parent
			mockProjectsClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsGetResponse{Object: parentProject}, nil)

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
			Expect(membership.GetStatus().GetUsers()).To(Equal([]string{"user-id"}))
		})
	})

	Context("Error handling", func() {
		It("should fail when user does not exist", func() {
			project := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "my-project",
					Tenant: "acme",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			membership := privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Project: "project-id",
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Users: []string{"nonexistent-user"},
					Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
				}.Build(),
				Status: privatev1.ProjectMembershipStatus_builder{
					State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_PENDING,
				}.Build(),
			}.Build()

			mockProjectsClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsGetResponse{Object: project}, nil)

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
					Project: "nonexistent-project",
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Users: []string{"user-id"},
					Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
				}.Build(),
				Status: privatev1.ProjectMembershipStatus_builder{
					State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_PENDING,
				}.Build(),
			}.Build()

			mockProjectsClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(nil, status.Error(codes.NotFound, "project not found"))

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
			project := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "my-project",
					Tenant: "acme",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{}.Build(),
			}.Build()

			membership := privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Project: "project-id",
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Users: []string{"user-id"},
					Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
				}.Build(),
				Status: privatev1.ProjectMembershipStatus_builder{
					State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_PENDING,
				}.Build(),
			}.Build()

			mockProjectsClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsGetResponse{Object: project}, nil)

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

		project := privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   "my-project",
				Tenant: "acme",
			}.Build(),
			Spec: privatev1.ProjectSpec_builder{}.Build(),
		}.Build()

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Project:    "project-id",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: []string{"user-id"},
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				Users: []string{"user-id"},
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsGetResponse{Object: project}, nil)

		mockIdpClient.EXPECT().
			GetGroupIDByPath(gomock.Any(), "acme", "/my-project/system:managers").
			Return("group-id", nil)

		mockUsersClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.UsersGetResponse{Object: user}, nil)

		mockIdpClient.EXPECT().
			RemoveUserFromGroup(gomock.Any(), "acme", "keycloak-alice-id", "group-id").
			Return(nil)

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

		parentProject := privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{
				Name: "parent",
			}.Build(),
			Spec: privatev1.ProjectSpec_builder{}.Build(),
		}.Build()

		childProject := privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{
				Name:    "child",
				Tenant:  "acme",
				Project: "parent",
			}.Build(),
			Spec: privatev1.ProjectSpec_builder{}.Build(),
		}.Build()

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Project:    "child-project-id",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: []string{"user-id"},
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_VIEWER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				Users: []string{"user-id"},
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsGetResponse{Object: childProject}, nil)

		mockProjectsClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsGetResponse{Object: parentProject}, nil)

		mockIdpClient.EXPECT().
			GetGroupIDByPath(gomock.Any(), "acme", "/parent/child/system:viewers").
			Return("group-id", nil)

		mockUsersClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.UsersGetResponse{Object: user}, nil)

		mockIdpClient.EXPECT().
			RemoveUserFromGroup(gomock.Any(), "acme", "keycloak-alice-id", "group-id").
			Return(nil)

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
				Project:    "project-id",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: []string{"user-id"},
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				Users: []string{"user-id"},
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(nil, status.Error(codes.NotFound, "project not found"))

		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsListResponse{Items: []*privatev1.Project{}}, nil)

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.ProjectMembershipFinalizer))
	})

	It("should handle group not found during cleanup with gRPC status code", func() {
		project := privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   "my-project",
				Tenant: "acme",
			}.Build(),
			Spec: privatev1.ProjectSpec_builder{}.Build(),
		}.Build()

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Project:    "project-id",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: []string{"user-id"},
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				Users: []string{"user-id"},
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsGetResponse{Object: project}, nil)

		mockIdpClient.EXPECT().
			GetGroupIDByPath(gomock.Any(), "acme", "/my-project/system:managers").
			Return("", status.Error(codes.NotFound, "group not found"))

		task := &task{
			r:          functionObj,
			membership: membership,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(membership.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.ProjectMembershipFinalizer))
	})

	It("should handle group not found during cleanup with wrapped error message", func() {
		project := privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   "my-project",
				Tenant: "acme",
			}.Build(),
			Spec: privatev1.ProjectSpec_builder{}.Build(),
		}.Build()

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.ProjectMembershipFinalizer},
				Project:    "project-id",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: []string{"user-id"},
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_VIEWER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				Users: []string{"user-id"},
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsGetResponse{Object: project}, nil)

		mockIdpClient.EXPECT().
			GetGroupIDByPath(gomock.Any(), "acme", "/my-project/system:viewers").
			Return("", fmt.Errorf("wrapped error: group not found in keycloak"))

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

		project := privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   "my-project",
				Tenant: "acme",
			}.Build(),
			Spec: privatev1.ProjectSpec_builder{}.Build(),
		}.Build()

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Project: "project-id",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: []string{"existing-user", "new-user"},
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY,
				Users: []string{"existing-user"},
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsGetResponse{Object: project}, nil)

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
		Expect(membership.GetStatus().GetUsers()).To(Equal([]string{"existing-user", "new-user"}))
	})

	It("should remove users when spec.users shrinks", func() {
		removedUser := privatev1.User_builder{
			Status: privatev1.UserStatus_builder{
				KeycloakUserId: "keycloak-removed-id",
			}.Build(),
		}.Build()

		project := privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   "my-project",
				Tenant: "acme",
			}.Build(),
			Spec: privatev1.ProjectSpec_builder{}.Build(),
		}.Build()

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Project: "project-id",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: []string{"remaining-user"},
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY,
				Users: []string{"remaining-user", "removed-user"},
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsGetResponse{Object: project}, nil)

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
		Expect(membership.GetStatus().GetUsers()).To(Equal([]string{"remaining-user"}))
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

		project := privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   "my-project",
				Tenant: "acme",
			}.Build(),
			Spec: privatev1.ProjectSpec_builder{}.Build(),
		}.Build()

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Project: "project-id",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: []string{"kept-user", "new-user"},
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_VIEWER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY,
				Users: []string{"kept-user", "removed-user"},
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsGetResponse{Object: project}, nil)

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
		Expect(membership.GetStatus().GetUsers()).To(Equal([]string{"kept-user", "new-user"}))
	})

	It("should be a no-op when user lists are identical", func() {
		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Project: "project-id",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: []string{"user-a", "user-b"},
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY,
				Users: []string{"user-a", "user-b"},
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
		project := privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   "my-project",
				Tenant: "acme",
			}.Build(),
			Spec: privatev1.ProjectSpec_builder{}.Build(),
		}.Build()

		membership := privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Project: "project-id",
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Users: []string{"existing-user", "failing-user"},
				Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
			}.Build(),
			Status: privatev1.ProjectMembershipStatus_builder{
				State: privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY,
				Users: []string{"existing-user"},
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsGetResponse{Object: project}, nil)

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
