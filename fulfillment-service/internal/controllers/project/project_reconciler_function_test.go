/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package project

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/fulfillment-service/internal/idp"
)

var _ = Describe("Finalizer Management", func() {
	It("should add finalizer on first call", func() {
		project := privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{},
			}.Build(),
		}.Build()

		task := &task{
			project: project,
		}

		added := task.addFinalizer()
		Expect(added).To(BeTrue())
		Expect(project.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
	})

	It("should not add finalizer if already present", func() {
		project := privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
		}.Build()

		task := &task{
			project: project,
		}

		added := task.addFinalizer()
		Expect(added).To(BeFalse())
		Expect(project.GetMetadata().GetFinalizers()).To(HaveLen(1))
	})
})

var _ = Describe("Default Values", func() {
	It("should set default status if missing", func() {
		project := privatev1.Project_builder{}.Build()

		task := &task{
			project: project,
		}

		task.setDefaults()

		Expect(project.HasStatus()).To(BeTrue())
		Expect(project.GetStatus().GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_PENDING))
	})

	It("should set default state if unspecified", func() {
		project := privatev1.Project_builder{
			Status: privatev1.ProjectStatus_builder{
				State: privatev1.ProjectState_PROJECT_STATE_UNSPECIFIED,
			}.Build(),
		}.Build()

		task := &task{
			project: project,
		}

		task.setDefaults()

		Expect(project.GetStatus().GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_PENDING))
	})

	It("should not change existing non-unspecified state", func() {
		project := privatev1.Project_builder{
			Status: privatev1.ProjectStatus_builder{
				State: privatev1.ProjectState_PROJECT_STATE_ACTIVE,
			}.Build(),
		}.Build()

		task := &task{
			project: project,
		}

		task.setDefaults()

		Expect(project.GetStatus().GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_ACTIVE))
	})
})

var _ = Describe("Finalizer Removal", func() {
	It("should remove finalizer when present", func() {
		project := privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller, "other-finalizer"},
			}.Build(),
		}.Build()

		task := &task{
			project: project,
		}

		task.removeFinalizer()

		Expect(project.GetMetadata().GetFinalizers()).To(ConsistOf("other-finalizer"))
		Expect(project.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.Controller))
	})

	It("should not error when finalizer not present", func() {
		project := privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{"other-finalizer"},
			}.Build(),
		}.Build()

		task := &task{
			project: project,
		}

		task.removeFinalizer()

		Expect(project.GetMetadata().GetFinalizers()).To(ConsistOf("other-finalizer"))
	})

	It("should handle missing metadata", func() {
		project := privatev1.Project_builder{}.Build()

		task := &task{
			project: project,
		}

		// Should not panic
		task.removeFinalizer()
	})
})

var _ = Describe("Validation and Activation", func() {
	var (
		ctrl                *gomock.Controller
		mockClient          *MockProjectsClient
		mockUsersClient     *MockUsersClient
		mockIdpClient       *idp.MockClientInterface
		projectGroupManager *idp.ProjectGroupManager
		ctx                 context.Context
		functionObj         *function
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockClient = NewMockProjectsClient(ctrl)
		mockUsersClient = NewMockUsersClient(ctrl)
		mockIdpClient = idp.NewMockClientInterface(ctrl)
		ctx = context.Background()

		var err error
		projectGroupManager, err = idp.NewProjectGroupManager().
			SetLogger(logger).
			SetClient(mockIdpClient).
			Build()
		Expect(err).ToNot(HaveOccurred())

		functionObj = &function{
			logger:              logger,
			projectsClient:      mockClient,
			usersClient:         mockUsersClient,
			projectGroupManager: projectGroupManager,
		}
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	Context("Top-level projects (no parent)", func() {
		It("should transition to ACTIVE state", func() {
			project := privatev1.Project_builder{
				Id: "project-1",
				Metadata: privatev1.Metadata_builder{
					Name:   "test-project",
					Tenant: "acme",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Test Project",
				}.Build(),
				Status: privatev1.ProjectStatus_builder{
					State: privatev1.ProjectState_PROJECT_STATE_PENDING,
				}.Build(),
			}.Build()

			// Expect viewers group creation
			mockIdpClient.EXPECT().
				CreateGroup(gomock.Any(), "acme", "/test-project/system:viewers").
				Return("viewers-id", nil)

			// Expect managers group creation
			mockIdpClient.EXPECT().
				CreateGroup(gomock.Any(), "acme", "/test-project/system:managers").
				Return("managers-id", nil)

			task := &task{
				r:       functionObj,
				project: project,
			}

			err := task.validateAndActivate(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(project.GetStatus().GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_ACTIVE))
			Expect(project.GetStatus().HasMessage()).To(BeFalse())
		})

		It("should set Keycloak sync condition to true on success", func() {
			project := privatev1.Project_builder{
				Id: "project-1",
				Metadata: privatev1.Metadata_builder{
					Name:   "test-project",
					Tenant: "acme",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Test Project",
				}.Build(),
				Status: privatev1.ProjectStatus_builder{
					State: privatev1.ProjectState_PROJECT_STATE_PENDING,
				}.Build(),
			}.Build()

			// Expect viewers group creation
			mockIdpClient.EXPECT().
				CreateGroup(gomock.Any(), "acme", "/test-project/system:viewers").
				Return("viewers-id", nil)

			// Expect managers group creation
			mockIdpClient.EXPECT().
				CreateGroup(gomock.Any(), "acme", "/test-project/system:managers").
				Return("managers-id", nil)

			task := &task{
				r:       functionObj,
				project: project,
			}

			err := task.validateAndActivate(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify Keycloak sync condition is set to true
			var syncCondition *privatev1.ProjectCondition
			for _, cond := range project.GetStatus().GetConditions() {
				if cond.GetType() == privatev1.ProjectConditionType_PROJECT_CONDITION_TYPE_KEYCLOAK_SYNC {
					syncCondition = cond
					break
				}
			}
			Expect(syncCondition).ToNot(BeNil())
			Expect(syncCondition.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
			Expect(syncCondition.GetReason()).To(Equal("GroupsCreated"))
		})
	})

	Context("Projects with valid parent", func() {
		It("should transition to ACTIVE when parent exists and is ACTIVE", func() {
			parentProject := privatev1.Project_builder{
				Id: "parent-1",
				Metadata: privatev1.Metadata_builder{
					Name: "parent-project",
				}.Build(),
				Status: privatev1.ProjectStatus_builder{
					State: privatev1.ProjectState_PROJECT_STATE_ACTIVE,
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Parent Project",
				}.Build(),
			}.Build()

			project := privatev1.Project_builder{
				Id: "project-1",
				Metadata: privatev1.Metadata_builder{
					Name:    "parent-project.child-project",
					Project: "parent-project",
					Tenant:  "acme",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Child Project",
				}.Build(),
				Status: privatev1.ProjectStatus_builder{
					State: privatev1.ProjectState_PROJECT_STATE_PENDING,
				}.Build(),
			}.Build()

			mockClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsListResponse{
					Items: []*privatev1.Project{parentProject},
					Size:  1,
				}, nil)

			// Expect viewers group creation
			mockIdpClient.EXPECT().
				CreateGroup(gomock.Any(), "acme", "/parent-project/child-project/system:viewers").
				Return("viewers-id", nil)

			// Expect managers group creation
			mockIdpClient.EXPECT().
				CreateGroup(gomock.Any(), "acme", "/parent-project/child-project/system:managers").
				Return("managers-id", nil)

			task := &task{
				r:       functionObj,
				project: project,
			}

			err := task.validateAndActivate(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(project.GetStatus().GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_ACTIVE))
		})

		It("should handle multi-level hierarchy", func() {
			parentProject := privatev1.Project_builder{
				Id: "parent-id",
				Metadata: privatev1.Metadata_builder{
					Name:    "root.parent",
					Project: "root",
				}.Build(),
				Status: privatev1.ProjectStatus_builder{
					State: privatev1.ProjectState_PROJECT_STATE_ACTIVE,
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Parent",
				}.Build(),
			}.Build()

			project := privatev1.Project_builder{
				Id: "child-id",
				Metadata: privatev1.Metadata_builder{
					Name:    "parent.child",
					Project: "parent",
					Tenant:  "acme",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Child",
				}.Build(),
				Status: privatev1.ProjectStatus_builder{
					State: privatev1.ProjectState_PROJECT_STATE_PENDING,
				}.Build(),
			}.Build()

			// The parent project will be fetched in order to check if it is active:
			mockClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsListResponse{
					Items: []*privatev1.Project{parentProject},
					Size:  1,
				}, nil)

			// Expect viewers group creation
			mockIdpClient.EXPECT().
				CreateGroup(
					gomock.Any(),
					"acme",
					"/parent/child/system:viewers",
				).
				Return("viewers-id", nil)

			// Expect managers group creation (new tenant groups API)
			mockIdpClient.EXPECT().
				CreateGroup(
					gomock.Any(),
					"acme",
					"/parent/child/system:managers",
				).
				Return("managers-id", nil)

			task := &task{
				r:       functionObj,
				project: project,
			}

			err := task.validateAndActivate(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(project.GetStatus().GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_ACTIVE))
		})
	})

	Context("Parent not found", func() {
		It("should fail when parent does not exist", func() {
			project := privatev1.Project_builder{
				Id: "project-1",
				Metadata: privatev1.Metadata_builder{
					Name:    "nonexistent-parent.child",
					Project: "nonexistent-parent",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Orphaned Project",
				}.Build(),
				Status: privatev1.ProjectStatus_builder{
					State: privatev1.ProjectState_PROJECT_STATE_PENDING,
				}.Build(),
			}.Build()

			mockClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsListResponse{
					Items: []*privatev1.Project{},
					Size:  0,
				}, nil)

			task := &task{
				r:       functionObj,
				project: project,
			}

			err := task.validateAndActivate(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(project.GetStatus().GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_FAILED))
			Expect(project.GetStatus().GetMessage()).To(ContainSubstring("Parent project not found"))
		})
	})

	Context("Parent state validation", func() {
		It("should fail when parent is in PENDING state", func() {
			parentProject := privatev1.Project_builder{
				Id: "parent-1",
				Metadata: privatev1.Metadata_builder{
					Name: "my-parent",
				}.Build(),
				Status: privatev1.ProjectStatus_builder{
					State: privatev1.ProjectState_PROJECT_STATE_PENDING,
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Pending Parent",
				}.Build(),
			}.Build()

			project := privatev1.Project_builder{
				Id: "project-1",
				Metadata: privatev1.Metadata_builder{
					Name:    "my-parent.child",
					Project: "my-parent",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Child",
				}.Build(),
				Status: privatev1.ProjectStatus_builder{
					State: privatev1.ProjectState_PROJECT_STATE_PENDING,
				}.Build(),
			}.Build()

			mockClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsListResponse{
					Items: []*privatev1.Project{parentProject},
					Size:  1,
				}, nil)

			task := &task{
				r:       functionObj,
				project: project,
			}

			err := task.validateAndActivate(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(project.GetStatus().GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_FAILED))
			Expect(project.GetStatus().GetMessage()).To(ContainSubstring(
				"Parent project 'my-parent' is not in ACTIVE state (current state: PROJECT_STATE_PENDING)",
			))
		})

		It("should fail when parent is in FAILED state", func() {
			parentProject := privatev1.Project_builder{
				Id: "parent-1",
				Metadata: privatev1.Metadata_builder{
					Name: "my-parent",
				}.Build(),
				Status: privatev1.ProjectStatus_builder{
					State: privatev1.ProjectState_PROJECT_STATE_FAILED,
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Failed Parent",
				}.Build(),
			}.Build()

			project := privatev1.Project_builder{
				Id: "project-1",
				Metadata: privatev1.Metadata_builder{
					Name:    "my-parent.child",
					Project: "my-parent",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Child",
				}.Build(),
				Status: privatev1.ProjectStatus_builder{
					State: privatev1.ProjectState_PROJECT_STATE_PENDING,
				}.Build(),
			}.Build()

			mockClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsListResponse{
					Items: []*privatev1.Project{parentProject},
					Size:  1,
				}, nil)

			task := &task{
				r:       functionObj,
				project: project,
			}

			err := task.validateAndActivate(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(project.GetStatus().GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_FAILED))
			Expect(project.GetStatus().GetMessage()).To(ContainSubstring(
				"Parent project 'my-parent' is not in ACTIVE state (current state: PROJECT_STATE_FAILED)",
			))
		})
	})

	Context("Creator assignment", func() {
		It("should add creator to managers group when user exists with Keycloak ID", func() {
			project := privatev1.Project_builder{
				Id: "project-1",
				Metadata: privatev1.Metadata_builder{
					Name:    "test-project",
					Tenant:  "acme",
					Creator: "alice",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Test Project",
				}.Build(),
				Status: privatev1.ProjectStatus_builder{
					State: privatev1.ProjectState_PROJECT_STATE_PENDING,
				}.Build(),
			}.Build()

			// Expect viewers group creation
			mockIdpClient.EXPECT().
				CreateGroup(gomock.Any(), "acme", "/test-project/system:viewers").
				Return("viewers-id", nil)

			// Expect managers group creation
			mockIdpClient.EXPECT().
				CreateGroup(gomock.Any(), "acme", "/test-project/system:managers").
				Return("managers-id", nil)

			// Expect user lookup to get Keycloak ID
			mockUsersClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *privatev1.UsersGetRequest, _ ...grpc.CallOption) (*privatev1.UsersGetResponse, error) {
					Expect(req.GetId()).To(Equal("alice"))
					return &privatev1.UsersGetResponse{
						Object: privatev1.User_builder{
							Id: "alice",
							Status: privatev1.UserStatus_builder{
								KeycloakUserId: "keycloak-user-123",
							}.Build(),
						}.Build(),
					}, nil
				})

			// Expect adding creator to managers group
			mockIdpClient.EXPECT().
				AddUserToGroup(gomock.Any(), "acme", "keycloak-user-123", "managers-id").
				Return(nil)

			task := &task{
				r:       functionObj,
				project: project,
			}

			err := task.validateAndActivate(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(project.GetStatus().GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_ACTIVE))
		})

		It("should not fail activation when user lookup fails with NotFound", func() {
			project := privatev1.Project_builder{
				Id: "project-1",
				Metadata: privatev1.Metadata_builder{
					Name:    "test-project",
					Tenant:  "acme",
					Creator: "nonexistent-user",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Test Project",
				}.Build(),
				Status: privatev1.ProjectStatus_builder{
					State: privatev1.ProjectState_PROJECT_STATE_PENDING,
				}.Build(),
			}.Build()

			// Expect viewers group creation
			mockIdpClient.EXPECT().
				CreateGroup(gomock.Any(), "acme", "/test-project/system:viewers").
				Return("viewers-id", nil)

			// Expect managers group creation
			mockIdpClient.EXPECT().
				CreateGroup(gomock.Any(), "acme", "/test-project/system:managers").
				Return("managers-id", nil)

			// User not found
			mockUsersClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(nil, status.Error(codes.NotFound, "user not found"))

			// Should not attempt to add user to group

			task := &task{
				r:       functionObj,
				project: project,
			}

			err := task.validateAndActivate(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(project.GetStatus().GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_ACTIVE))
		})

		It("should not fail activation when user lookup fails with other error", func() {
			project := privatev1.Project_builder{
				Id: "project-1",
				Metadata: privatev1.Metadata_builder{
					Name:    "test-project",
					Tenant:  "acme",
					Creator: "alice",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Test Project",
				}.Build(),
				Status: privatev1.ProjectStatus_builder{
					State: privatev1.ProjectState_PROJECT_STATE_PENDING,
				}.Build(),
			}.Build()

			// Expect viewers group creation
			mockIdpClient.EXPECT().
				CreateGroup(gomock.Any(), "acme", "/test-project/system:viewers").
				Return("viewers-id", nil)

			// Expect managers group creation
			mockIdpClient.EXPECT().
				CreateGroup(gomock.Any(), "acme", "/test-project/system:managers").
				Return("managers-id", nil)

			// User lookup fails with internal error
			mockUsersClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(nil, status.Error(codes.Internal, "database error"))

			// Should not attempt to add user to group

			task := &task{
				r:       functionObj,
				project: project,
			}

			err := task.validateAndActivate(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(project.GetStatus().GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_ACTIVE))
		})

		It("should not fail activation when user has no Keycloak ID", func() {
			project := privatev1.Project_builder{
				Id: "project-1",
				Metadata: privatev1.Metadata_builder{
					Name:    "test-project",
					Tenant:  "acme",
					Creator: "alice",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Test Project",
				}.Build(),
				Status: privatev1.ProjectStatus_builder{
					State: privatev1.ProjectState_PROJECT_STATE_PENDING,
				}.Build(),
			}.Build()

			// Expect viewers group creation
			mockIdpClient.EXPECT().
				CreateGroup(gomock.Any(), "acme", "/test-project/system:viewers").
				Return("viewers-id", nil)

			// Expect managers group creation
			mockIdpClient.EXPECT().
				CreateGroup(gomock.Any(), "acme", "/test-project/system:managers").
				Return("managers-id", nil)

			// User found but no Keycloak ID
			mockUsersClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.UsersGetResponse{
					Object: privatev1.User_builder{
						Id:     "alice",
						Status: privatev1.UserStatus_builder{
							// No KeycloakUserId set
						}.Build(),
					}.Build(),
				}, nil)

			// Should not attempt to add user to group

			task := &task{
				r:       functionObj,
				project: project,
			}

			err := task.validateAndActivate(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(project.GetStatus().GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_ACTIVE))
		})

		It("should not fail activation when AddUserToGroup fails", func() {
			project := privatev1.Project_builder{
				Id: "project-1",
				Metadata: privatev1.Metadata_builder{
					Name:    "test-project",
					Tenant:  "acme",
					Creator: "alice",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Test Project",
				}.Build(),
				Status: privatev1.ProjectStatus_builder{
					State: privatev1.ProjectState_PROJECT_STATE_PENDING,
				}.Build(),
			}.Build()

			// Expect viewers group creation
			mockIdpClient.EXPECT().
				CreateGroup(gomock.Any(), "acme", "/test-project/system:viewers").
				Return("viewers-id", nil)

			// Expect managers group creation
			mockIdpClient.EXPECT().
				CreateGroup(gomock.Any(), "acme", "/test-project/system:managers").
				Return("managers-id", nil)

			// Expect user lookup
			mockUsersClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(&privatev1.UsersGetResponse{
					Object: privatev1.User_builder{
						Id: "alice",
						Status: privatev1.UserStatus_builder{
							KeycloakUserId: "keycloak-user-123",
						}.Build(),
					}.Build(),
				}, nil)

			// Adding to group fails
			mockIdpClient.EXPECT().
				AddUserToGroup(gomock.Any(), "acme", "keycloak-user-123", "managers-id").
				Return(status.Error(codes.Internal, "keycloak error"))

			task := &task{
				r:       functionObj,
				project: project,
			}

			err := task.validateAndActivate(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(project.GetStatus().GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_ACTIVE))
		})

		It("should not attempt to add creator when creator is empty", func() {
			project := privatev1.Project_builder{
				Id: "project-1",
				Metadata: privatev1.Metadata_builder{
					Name:   "test-project",
					Tenant: "acme",
					// No creator
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Test Project",
				}.Build(),
				Status: privatev1.ProjectStatus_builder{
					State: privatev1.ProjectState_PROJECT_STATE_PENDING,
				}.Build(),
			}.Build()

			// Expect viewers group creation
			mockIdpClient.EXPECT().
				CreateGroup(gomock.Any(), "acme", "/test-project/system:viewers").
				Return("viewers-id", nil)

			// Expect managers group creation
			mockIdpClient.EXPECT().
				CreateGroup(gomock.Any(), "acme", "/test-project/system:managers").
				Return("managers-id", nil)

			// Should not attempt to look up user or add to group

			task := &task{
				r:       functionObj,
				project: project,
			}

			err := task.validateAndActivate(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(project.GetStatus().GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_ACTIVE))
		})
	})

	Context("Update skips validation", func() {
		It("should skip validation when project is already ACTIVE", func() {
			project := privatev1.Project_builder{
				Id: "project-1",
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.Controller},
				}.Build(),
				Status: privatev1.ProjectStatus_builder{
					State: privatev1.ProjectState_PROJECT_STATE_ACTIVE,
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Active Project",
				}.Build(),
			}.Build()

			task := &task{
				r:       functionObj,
				project: project,
			}

			// Should not call any client methods since validation is skipped
			err := task.update(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(project.GetStatus().GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_ACTIVE))
		})

		It("should skip validation when project is in FAILED state", func() {
			project := privatev1.Project_builder{
				Id: "project-1",
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.Controller},
				}.Build(),
				Status: privatev1.ProjectStatus_builder{
					State:   privatev1.ProjectState_PROJECT_STATE_FAILED,
					Message: new("Some error"),
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Failed Project",
				}.Build(),
			}.Build()

			task := &task{
				r:       functionObj,
				project: project,
			}

			// Should not call any client methods since validation is skipped
			err := task.update(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(project.GetStatus().GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_FAILED))
		})
	})
})

var _ = Describe("Deletion Cleanup", func() {
	var (
		ctrl                         *gomock.Controller
		mockClient                   *MockProjectsClient
		mockTenantsClient            *MockTenantsClient
		mockProjectMembershipsClient *MockProjectMembershipsClient
		mockIdpClient                *idp.MockClientInterface
		resourceManager              *idp.ProjectGroupManager
		ctx                          context.Context
		functionObj                  *function
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockClient = NewMockProjectsClient(ctrl)
		mockTenantsClient = NewMockTenantsClient(ctrl)
		mockProjectMembershipsClient = NewMockProjectMembershipsClient(ctrl)
		mockIdpClient = idp.NewMockClientInterface(ctrl)
		ctx = context.Background()

		var err error
		resourceManager, err = idp.NewProjectGroupManager().
			SetLogger(logger).
			SetClient(mockIdpClient).
			Build()
		Expect(err).ToNot(HaveOccurred())

		functionObj = &function{
			logger:                   logger,
			projectsClient:           mockClient,
			tenantsClient:            mockTenantsClient,
			projectMembershipsClient: mockProjectMembershipsClient,
			projectGroupManager:      resourceManager,
		}
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	It("should block deletion when child projects exist", func() {
		project := privatev1.Project_builder{
			Id: "parent-1",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Status: privatev1.ProjectStatus_builder{}.Build(),
		}.Build()

		// Expect query for children
		mockClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsListResponse{
				Total: 2, // Has 2 children
			}, nil)

		task := &task{
			r:       functionObj,
			project: project,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		// State should be DELETE_FAILED
		Expect(project.GetStatus().GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_DELETE_FAILED))
		Expect(project.GetStatus().GetMessage()).To(ContainSubstring("Cannot delete project with 2 child project(s)"))
		// Finalizer should NOT be removed
		Expect(project.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
	})

	It("should delete Keycloak groups when no children exist", func() {
		project := privatev1.Project_builder{
			Id: "project-1",
			Metadata: privatev1.Metadata_builder{
				Name:       "test-project",
				Tenant:     "acme",
				Finalizers: []string{finalizers.Controller},
			}.Build(),
		}.Build()

		// Expect query for children (returns 0)
		mockClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsListResponse{
				Size: 0,
			}, nil)

		// Expect query for project memberships (returns 0)
		mockProjectMembershipsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectMembershipsListResponse{}, nil)

		// Expect parent project group ID lookup
		mockIdpClient.EXPECT().
			GetGroupIDByPath(gomock.Any(), "acme", "/test-project").
			Return("project-group-id", nil)

		// Expect parent project group deletion (cascades to delete system:viewers and system:managers subgroups)
		mockIdpClient.EXPECT().
			DeleteGroup(gomock.Any(), "acme", "project-group-id").
			Return(nil)

		task := &task{
			r:       functionObj,
			project: project,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(project.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.Controller))
	})

	It("should remove finalizer when Keycloak group is already gone", func() {
		project := privatev1.Project_builder{
			Id: "project-1",
			Metadata: privatev1.Metadata_builder{
				Name:       "test-project",
				Tenant:     "acme",
				Finalizers: []string{finalizers.Controller},
			}.Build(),
		}.Build()

		// Expect query for children (returns 0)
		mockClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsListResponse{
				Size: 0,
			}, nil)

		// Expect query for project memberships (returns 0)
		mockProjectMembershipsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectMembershipsListResponse{}, nil)

		// Group already deleted — DeleteProjectGroups swallows "not found" internally
		mockIdpClient.EXPECT().
			GetGroupIDByPath(gomock.Any(), "acme", "/test-project").
			Return("", fmt.Errorf("organization group not found: /test-project"))

		task := &task{
			r:       functionObj,
			project: project,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(project.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.Controller))
	})

	It("should return error and keep finalizer on transient Keycloak failure", func() {
		project := privatev1.Project_builder{
			Id: "project-1",
			Metadata: privatev1.Metadata_builder{
				Name:       "test-project",
				Tenant:     "acme",
				Finalizers: []string{finalizers.Controller},
			}.Build(),
		}.Build()

		// Expect query for children (returns 0)
		mockClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsListResponse{
				Size: 0,
			}, nil)

		// Expect query for project memberships (returns 0)
		mockProjectMembershipsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectMembershipsListResponse{}, nil)

		// Transient failure — should NOT swallow
		mockIdpClient.EXPECT().
			GetGroupIDByPath(gomock.Any(), "acme", "/test-project").
			Return("", status.Error(codes.Unavailable, "connection refused"))

		task := &task{
			r:       functionObj,
			project: project,
		}

		err := task.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to delete Keycloak groups"))
		Expect(project.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
	})

	It("should return error when tenant is missing during deletion", func() {
		project := privatev1.Project_builder{
			Id: "project-1",
			Metadata: privatev1.Metadata_builder{
				Name:       "test-project",
				Finalizers: []string{finalizers.Controller},
				// Missing tenant
			}.Build(),
		}.Build()

		// Expect query for children (returns 0)
		mockClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsListResponse{
				Size: 0,
			}, nil)

		// Expect query for project memberships (returns 0)
		mockProjectMembershipsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectMembershipsListResponse{}, nil)

		task := &task{
			r:       functionObj,
			project: project,
		}

		err := task.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to delete Keycloak groups"))
		Expect(project.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
	})

	It("should skip Keycloak cleanup and signal tenant for root project deletion", func() {
		project := privatev1.Project_builder{
			Id: "project-1",
			Metadata: privatev1.Metadata_builder{
				Tenant:     "acme",
				Finalizers: []string{finalizers.Controller},
				// Empty name = root project
			}.Build(),
		}.Build()

		// Expect query for children (returns 0)
		mockClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectsListResponse{
				Size: 0,
			}, nil)

		// Expect query for project memberships (returns 0)
		mockProjectMembershipsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.ProjectMembershipsListResponse{}, nil)

		// Root project goes through Keycloak cleanup — group at "/" not found, swallowed
		mockIdpClient.EXPECT().
			GetGroupIDByPath(gomock.Any(), "acme", "/").
			Return("", fmt.Errorf("organization group not found: /"))

		// Root project triggers tenant signal after finalizer removal
		mockTenantsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(privatev1.TenantsListResponse_builder{
				Items: []*privatev1.Tenant{
					privatev1.Tenant_builder{Id: "tenant-id-1"}.Build(),
				},
				Size: 1,
			}.Build(), nil)

		mockTenantsClient.EXPECT().
			Signal(gomock.Any(), gomock.Any()).
			Return(privatev1.TenantsSignalResponse_builder{}.Build(), nil)

		task := &task{
			r:       functionObj,
			project: project,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(project.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.Controller))
	})

	It("should return error if querying for children fails", func() {
		project := privatev1.Project_builder{
			Id: "project-1",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
		}.Build()

		// Expect query for children to fail
		mockClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(nil, status.Error(codes.Unavailable, "database unavailable"))

		task := &task{
			r:       functionObj,
			project: project,
		}

		err := task.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to query for child projects"))
		// Finalizer should NOT be removed on error
		Expect(project.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
	})

	Context("Cascade deletion of ProjectMemberships", func() {
		It("should cascade-delete memberships and wait for removal", func() {
			project := privatev1.Project_builder{
				Id: "project-1",
				Metadata: privatev1.Metadata_builder{
					Name:       "test-project",
					Tenant:     "acme",
					Finalizers: []string{finalizers.Controller},
				}.Build(),
			}.Build()

			membership := privatev1.ProjectMembership_builder{
				Id: "membership-1",
				Metadata: privatev1.Metadata_builder{
					Project: "test-project",
					Tenant:  "acme",
				}.Build(),
			}.Build()

			// Expect query for children (returns 0)
			mockClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsListResponse{Size: 0}, nil)

			// Expect query for memberships (returns 1)
			mockProjectMembershipsClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectMembershipsListResponse{
					Items: []*privatev1.ProjectMembership{membership},
					Total: 1,
					Size:  1,
				}, nil)

			// Expect delete call for the membership
			mockProjectMembershipsClient.EXPECT().
				Delete(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *privatev1.ProjectMembershipsDeleteRequest, _ ...grpc.CallOption) (*privatev1.ProjectMembershipsDeleteResponse, error) {
					Expect(req.GetId()).To(Equal("membership-1"))
					return &privatev1.ProjectMembershipsDeleteResponse{}, nil
				})

			task := &task{
				r:       functionObj,
				project: project,
			}

			err := task.delete(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("project still has 1 project membership(s) pending deletion"))
			Expect(project.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
		})

		It("should proceed with deletion when all memberships are gone", func() {
			project := privatev1.Project_builder{
				Id: "project-1",
				Metadata: privatev1.Metadata_builder{
					Name:       "test-project",
					Tenant:     "acme",
					Finalizers: []string{finalizers.Controller},
				}.Build(),
			}.Build()

			// Expect query for children (returns 0)
			mockClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsListResponse{Size: 0}, nil)

			// Expect query for memberships (returns 0 — all previously deleted)
			mockProjectMembershipsClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectMembershipsListResponse{}, nil)

			// Expect Keycloak cleanup
			mockIdpClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "acme", "/test-project").
				Return("project-group-id", nil)
			mockIdpClient.EXPECT().
				DeleteGroup(gomock.Any(), "acme", "project-group-id").
				Return(nil)

			task := &task{
				r:       functionObj,
				project: project,
			}

			err := task.delete(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(project.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.Controller))
		})

		It("should skip already-deleting memberships", func() {
			project := privatev1.Project_builder{
				Id: "project-1",
				Metadata: privatev1.Metadata_builder{
					Name:       "test-project",
					Tenant:     "acme",
					Finalizers: []string{finalizers.Controller},
				}.Build(),
			}.Build()

			alreadyDeleting := privatev1.ProjectMembership_builder{
				Id: "membership-1",
				Metadata: privatev1.Metadata_builder{
					Project:           "test-project",
					Tenant:            "acme",
					DeletionTimestamp: timestamppb.Now(),
				}.Build(),
			}.Build()

			// Expect query for children (returns 0)
			mockClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsListResponse{Size: 0}, nil)

			// Expect query for memberships (returns 1 already-deleting)
			mockProjectMembershipsClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectMembershipsListResponse{
					Items: []*privatev1.ProjectMembership{alreadyDeleting},
					Total: 1,
					Size:  1,
				}, nil)

			// Should NOT call Delete (already deleting)

			task := &task{
				r:       functionObj,
				project: project,
			}

			err := task.delete(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("project still has 1 project membership(s) pending deletion"))
		})

		It("should return error when querying for memberships fails", func() {
			project := privatev1.Project_builder{
				Id: "project-1",
				Metadata: privatev1.Metadata_builder{
					Name:       "test-project",
					Tenant:     "acme",
					Finalizers: []string{finalizers.Controller},
				}.Build(),
			}.Build()

			// Expect query for children (returns 0)
			mockClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsListResponse{Size: 0}, nil)

			// Expect query for memberships to fail
			mockProjectMembershipsClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(nil, status.Error(codes.Unavailable, "database unavailable"))

			task := &task{
				r:       functionObj,
				project: project,
			}

			err := task.delete(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to query for project memberships"))
			Expect(project.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
		})

		It("should cascade-delete multiple memberships", func() {
			project := privatev1.Project_builder{
				Id: "project-1",
				Metadata: privatev1.Metadata_builder{
					Name:       "test-project",
					Tenant:     "acme",
					Finalizers: []string{finalizers.Controller},
				}.Build(),
			}.Build()

			membership1 := privatev1.ProjectMembership_builder{
				Id: "membership-1",
				Metadata: privatev1.Metadata_builder{
					Project: "test-project",
					Tenant:  "acme",
				}.Build(),
			}.Build()

			membership2 := privatev1.ProjectMembership_builder{
				Id: "membership-2",
				Metadata: privatev1.Metadata_builder{
					Project: "test-project",
					Tenant:  "acme",
				}.Build(),
			}.Build()

			// Expect query for children (returns 0)
			mockClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsListResponse{Size: 0}, nil)

			// Expect query for memberships (returns 2)
			mockProjectMembershipsClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectMembershipsListResponse{
					Items: []*privatev1.ProjectMembership{membership1, membership2},
					Total: 2,
					Size:  2,
				}, nil)

			// Expect delete calls for both memberships
			mockProjectMembershipsClient.EXPECT().
				Delete(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectMembershipsDeleteResponse{}, nil).
				Times(2)

			task := &task{
				r:       functionObj,
				project: project,
			}

			err := task.delete(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("project still has 2 project membership(s) pending deletion"))
		})

		It("should handle NotFound during membership deletion gracefully", func() {
			project := privatev1.Project_builder{
				Id: "project-1",
				Metadata: privatev1.Metadata_builder{
					Name:       "test-project",
					Tenant:     "acme",
					Finalizers: []string{finalizers.Controller},
				}.Build(),
			}.Build()

			membership := privatev1.ProjectMembership_builder{
				Id: "membership-1",
				Metadata: privatev1.Metadata_builder{
					Project: "test-project",
					Tenant:  "acme",
				}.Build(),
			}.Build()

			// Expect query for children (returns 0)
			mockClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectsListResponse{Size: 0}, nil)

			// Expect query for memberships (returns 1)
			mockProjectMembershipsClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.ProjectMembershipsListResponse{
					Items: []*privatev1.ProjectMembership{membership},
					Total: 1,
					Size:  1,
				}, nil)

			// Delete returns NotFound (already deleted between list and delete)
			mockProjectMembershipsClient.EXPECT().
				Delete(gomock.Any(), gomock.Any()).
				Return(nil, status.Error(codes.NotFound, "not found"))

			task := &task{
				r:       functionObj,
				project: project,
			}

			err := task.delete(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("project still has 1 project membership(s) pending deletion"))
		})
	})
})
