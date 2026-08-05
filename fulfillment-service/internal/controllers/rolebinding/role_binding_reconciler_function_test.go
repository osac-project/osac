/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package rolebinding

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/fulfillment-service/internal/idp"
	"github.com/osac-project/fulfillment-service/internal/masks"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var logger = slog.Default()

// mockRoleBindingsClient implements the minimal RoleBindingsClient interface for testing.
type mockRoleBindingsClient struct {
	privatev1.RoleBindingsClient
	updateCalled bool
	updateReq    *privatev1.RoleBindingsUpdateRequest
	updateErr    error
}

func (m *mockRoleBindingsClient) Update(
	_ context.Context,
	req *privatev1.RoleBindingsUpdateRequest,
	_ ...grpc.CallOption,
) (*privatev1.RoleBindingsUpdateResponse, error) {
	m.updateCalled = true
	m.updateReq = req
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	return &privatev1.RoleBindingsUpdateResponse{Object: req.GetObject()}, nil
}

// mockRolesClient implements the minimal RolesClient interface for testing.
type mockRolesClient struct {
	privatev1.RolesClient
	getResponse  *privatev1.RolesGetResponse
	getErr       error
	listResponse *privatev1.RolesListResponse
	listErr      error
}

func (m *mockRolesClient) Get(
	_ context.Context,
	_ *privatev1.RolesGetRequest,
	_ ...grpc.CallOption,
) (*privatev1.RolesGetResponse, error) {
	return m.getResponse, m.getErr
}

func (m *mockRolesClient) List(
	_ context.Context,
	_ *privatev1.RolesListRequest,
	_ ...grpc.CallOption,
) (*privatev1.RolesListResponse, error) {
	return m.listResponse, m.listErr
}

// mockUsersClient implements the minimal UsersClient interface for testing.
type mockUsersClient struct {
	privatev1.UsersClient
	getResponse *privatev1.UsersGetResponse
	getErr      error
	users       map[string]*privatev1.User
}

func (m *mockUsersClient) Get(
	_ context.Context,
	req *privatev1.UsersGetRequest,
	_ ...grpc.CallOption,
) (*privatev1.UsersGetResponse, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.getResponse != nil {
		return m.getResponse, nil
	}
	if m.users != nil {
		if user, ok := m.users[req.GetId()]; ok {
			return &privatev1.UsersGetResponse{Object: user}, nil
		}
	}
	return nil, fmt.Errorf("user %q not found", req.GetId())
}

// hasFinalizer checks if the controller finalizer is present.
func hasFinalizer(binding *privatev1.RoleBinding) bool {
	return slices.Contains(binding.GetMetadata().GetFinalizers(), finalizers.Controller)
}

// newMockUsersClient creates a mock users client that returns users with Keycloak IDs.
// userIDs should be a list of user IDs (e.g., "user-1", "user-2").
// The Keycloak ID for each user will be "keycloak-<userID>".
func newMockUsersClient(userIDs ...string) *mockUsersClient {
	users := make(map[string]*privatev1.User)
	for _, userID := range userIDs {
		users[userID] = privatev1.User_builder{
			Id: userID,
			Status: privatev1.UserStatus_builder{
				KeycloakUserId: "keycloak-" + userID,
			}.Build(),
		}.Build()
	}
	return &mockUsersClient{users: users}
}

var _ = Describe("RoleBinding Reconciler", func() {
	var (
		ctx  context.Context
		ctrl *gomock.Controller
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	Describe("FunctionBuilder", func() {
		It("should build function successfully with all dependencies", func() {
			idpClient := idp.NewMockClientInterface(ctrl)

			builder := NewFunction().
				SetLogger(logger).
				SetConnection(&grpc.ClientConn{}).
				SetIdpClient(idpClient)

			fn, err := builder.Build()

			Expect(err).ToNot(HaveOccurred())
			Expect(fn).ToNot(BeNil())
			Expect(fn.logger).To(Equal(logger))
			Expect(fn.idpClient).To(Equal(idpClient))
		})

		It("should return error when logger is missing", func() {
			builder := NewFunction()

			_, err := builder.Build()

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger is mandatory"))
		})

		It("should return error when connection is missing", func() {
			builder := NewFunction().SetLogger(logger)

			_, err := builder.Build()

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("connection is mandatory"))
		})

		It("should return error when IDP client is missing", func() {
			builder := NewFunction().
				SetLogger(logger).
				SetConnection(&grpc.ClientConn{})

			_, err := builder.Build()

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("IDP client is mandatory"))
		})
	})

	Describe("Run", func() {
		It("should call update and persist changes", func() {
			role := privatev1.Role_builder{
				Id:       "role-run",
				Metadata: privatev1.Metadata_builder{Name: "custom-role"}.Build(),
			}.Build()

			rolesClient := &mockRolesClient{
				getResponse: &privatev1.RolesGetResponse{Object: role},
			}

			usersClient := newMockUsersClient("user-1")

			bindingsClient := &mockRoleBindingsClient{}
			idpClient := idp.NewMockClientInterface(ctrl)
			// When binding already has finalizer, update will call syncRoleAssignments
			idpClient.EXPECT().
				AssignTenantRolesToUser(ctx, "test-org", "keycloak-user-1", gomock.Any()).
				Return(nil).
				Times(1)

			f := &function{
				logger:             logger,
				roleBindingsClient: bindingsClient,
				rolesClient:        rolesClient,
				usersClient:        usersClient,
				idpClient:          idpClient,
				maskCalculator:     masks.NewCalculator().Build(),
			}

			binding := privatev1.RoleBinding_builder{
				Id: "rb-run",
				Metadata: privatev1.Metadata_builder{
					Tenant:     "test-org",
					Finalizers: []string{finalizers.Controller},
				}.Build(),
				Spec: privatev1.RoleBindingSpec_builder{
					Role:  "custom-role",
					Users: []string{"user-1"},
				}.Build(),
				Status: privatev1.RoleBindingStatus_builder{
					State: privatev1.RoleBindingState_ROLE_BINDING_STATE_PENDING,
				}.Build(),
			}.Build()

			err := f.Run(ctx, binding)

			Expect(err).ToNot(HaveOccurred())
			Expect(bindingsClient.updateCalled).To(BeTrue())
		})

		It("should call delete for bindings with deletion timestamp", func() {
			role := privatev1.Role_builder{
				Id:       "role-delete-run",
				Metadata: privatev1.Metadata_builder{Name: "custom-role"}.Build(),
			}.Build()

			rolesClient := &mockRolesClient{
				getResponse: &privatev1.RolesGetResponse{Object: role},
			}

			usersClient := newMockUsersClient("user-1")

			bindingsClient := &mockRoleBindingsClient{}
			idpClient := idp.NewMockClientInterface(ctrl)
			idpClient.EXPECT().
				RemoveTenantRolesFromUser(ctx, "test-org", "keycloak-user-1", gomock.Any()).
				Return(nil)

			f := &function{
				logger:             logger,
				roleBindingsClient: bindingsClient,
				rolesClient:        rolesClient,
				usersClient:        usersClient,
				idpClient:          idpClient,
				maskCalculator:     masks.NewCalculator().Build(),
			}

			deletionTime := timestamppb.Now()
			binding := privatev1.RoleBinding_builder{
				Id: "rb-delete-run",
				Metadata: privatev1.Metadata_builder{
					Tenant:            "test-org",
					Finalizers:        []string{finalizers.Controller},
					DeletionTimestamp: deletionTime,
				}.Build(),
				Spec: privatev1.RoleBindingSpec_builder{
					Role:  "custom-role",
					Users: []string{"user-1"},
				}.Build(),
				Status: privatev1.RoleBindingStatus_builder{
					State: privatev1.RoleBindingState_ROLE_BINDING_STATE_READY,
				}.Build(),
			}.Build()

			err := f.Run(ctx, binding)

			Expect(err).ToNot(HaveOccurred())
			Expect(hasFinalizer(binding)).To(BeFalse())
		})
	})

	Describe("setDefaults", func() {
		It("should set status and state to PENDING when not set", func() {
			binding := privatev1.RoleBinding_builder{
				Id: "rb-test-1",
			}.Build()

			t := &task{binding: binding}
			t.setDefaults()

			Expect(binding.HasStatus()).To(BeTrue())
			Expect(binding.GetStatus().GetState()).To(Equal(privatev1.RoleBindingState_ROLE_BINDING_STATE_PENDING))
		})

		It("should preserve existing state", func() {
			binding := privatev1.RoleBinding_builder{
				Id: "rb-test-2",
				Status: privatev1.RoleBindingStatus_builder{
					State: privatev1.RoleBindingState_ROLE_BINDING_STATE_READY,
				}.Build(),
			}.Build()

			t := &task{binding: binding}
			t.setDefaults()

			Expect(binding.GetStatus().GetState()).To(Equal(privatev1.RoleBindingState_ROLE_BINDING_STATE_READY))
		})
	})

	Describe("addFinalizer", func() {
		It("should add finalizer when not present", func() {
			binding := privatev1.RoleBinding_builder{
				Id: "rb-test-3",
			}.Build()

			t := &task{binding: binding}
			added := t.addFinalizer()

			Expect(added).To(BeTrue())
			Expect(hasFinalizer(binding)).To(BeTrue())
		})

		It("should not add duplicate finalizer", func() {
			binding := privatev1.RoleBinding_builder{
				Id: "rb-test-4",
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.Controller},
				}.Build(),
			}.Build()

			t := &task{binding: binding}
			added := t.addFinalizer()

			Expect(added).To(BeFalse())
			Expect(hasFinalizer(binding)).To(BeTrue())
		})
	})

	Describe("removeFinalizer", func() {
		It("should remove finalizer when present", func() {
			binding := privatev1.RoleBinding_builder{
				Id: "rb-test-5",
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.Controller, "other-finalizer"},
				}.Build(),
			}.Build()

			t := &task{binding: binding}
			t.removeFinalizer()

			Expect(hasFinalizer(binding)).To(BeFalse())
			Expect(binding.GetMetadata().GetFinalizers()).To(Equal([]string{"other-finalizer"}))
		})

		It("should handle missing finalizer gracefully", func() {
			binding := privatev1.RoleBinding_builder{
				Id: "rb-test-6",
			}.Build()

			t := &task{binding: binding}
			t.removeFinalizer()

			Expect(hasFinalizer(binding)).To(BeFalse())
		})
	})

	Describe("mapRoleToKeycloak", func() {
		var t *task

		BeforeEach(func() {
			f := &function{logger: logger}
			t = &task{r: f}
		})

		It("should map tenant-admin to realm-level role", func() {
			roles, clientID := t.mapRoleToKeycloak("tenant-admin")

			Expect(clientID).To(Equal(""))
			Expect(roles).To(HaveLen(1))
			Expect(roles[0].Name).To(Equal("tenant-admin"))
			Expect(roles[0].ClientRole).To(BeFalse())
		})

		It("should map tenant-reader to realm-level role", func() {
			roles, clientID := t.mapRoleToKeycloak("tenant-reader")

			Expect(clientID).To(Equal(""))
			Expect(roles).To(HaveLen(1))
			Expect(roles[0].Name).To(Equal("tenant-reader"))
			Expect(roles[0].ClientRole).To(BeFalse())
		})

		It("should map tenant-user to realm-level role", func() {
			roles, clientID := t.mapRoleToKeycloak("tenant-user")

			Expect(clientID).To(Equal(""))
			Expect(roles).To(HaveLen(1))
			Expect(roles[0].Name).To(Equal("tenant-user"))
			Expect(roles[0].ClientRole).To(BeFalse())
		})

		It("should map any custom role to realm-level role", func() {
			roles, clientID := t.mapRoleToKeycloak("custom-role")

			Expect(clientID).To(Equal(""))
			Expect(roles).To(HaveLen(1))
			Expect(roles[0].Name).To(Equal("custom-role"))
			Expect(roles[0].ClientRole).To(BeFalse())
		})
	})

	Describe("getRoleByNameOrID", func() {
		It("should fetch role by ID successfully", func() {
			role := privatev1.Role_builder{
				Id: "role-123",
				Metadata: privatev1.Metadata_builder{
					Name: "tenant-admin",
				}.Build(),
			}.Build()

			rolesClient := &mockRolesClient{
				getResponse: &privatev1.RolesGetResponse{Object: role},
			}

			f := &function{
				logger:      logger,
				rolesClient: rolesClient,
			}
			t := &task{r: f}

			result, err := t.getRoleByNameOrID(ctx, "role-123")

			Expect(err).ToNot(HaveOccurred())
			Expect(result.GetId()).To(Equal("role-123"))
			Expect(result.GetMetadata().GetName()).To(Equal("tenant-admin"))
		})

		It("should fetch role by name when ID not found", func() {
			role := privatev1.Role_builder{
				Id: "role-456",
				Metadata: privatev1.Metadata_builder{
					Name: "tenant-reader",
				}.Build(),
			}.Build()

			rolesClient := &mockRolesClient{
				getErr: fmt.Errorf("not found"),
				listResponse: &privatev1.RolesListResponse{
					Items: []*privatev1.Role{role},
				},
			}

			f := &function{
				logger:      logger,
				rolesClient: rolesClient,
			}
			t := &task{r: f}

			result, err := t.getRoleByNameOrID(ctx, "tenant-reader")

			Expect(err).ToNot(HaveOccurred())
			Expect(result.GetId()).To(Equal("role-456"))
			Expect(result.GetMetadata().GetName()).To(Equal("tenant-reader"))
		})

		It("should return error when role not found by ID or name", func() {
			rolesClient := &mockRolesClient{
				getErr: fmt.Errorf("not found"),
				listResponse: &privatev1.RolesListResponse{
					Items: []*privatev1.Role{},
				},
			}

			f := &function{
				logger:      logger,
				rolesClient: rolesClient,
			}
			t := &task{r: f}

			_, err := t.getRoleByNameOrID(ctx, "nonexistent")

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})

		It("should return error when multiple roles found by name", func() {
			role1 := privatev1.Role_builder{
				Id:       "role-1",
				Metadata: privatev1.Metadata_builder{Name: "duplicate"}.Build(),
			}.Build()
			role2 := privatev1.Role_builder{
				Id:       "role-2",
				Metadata: privatev1.Metadata_builder{Name: "duplicate"}.Build(),
			}.Build()

			rolesClient := &mockRolesClient{
				getErr: fmt.Errorf("not found"),
				listResponse: &privatev1.RolesListResponse{
					Items: []*privatev1.Role{role1, role2},
				},
			}

			f := &function{
				logger:      logger,
				rolesClient: rolesClient,
			}
			t := &task{r: f}

			_, err := t.getRoleByNameOrID(ctx, "duplicate")

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("multiple roles found"))
		})
	})

	Describe("syncRoleAssignments", func() {
		It("should assign realm-level roles to all users", func() {
			role := privatev1.Role_builder{
				Id:       "role-789",
				Metadata: privatev1.Metadata_builder{Name: "custom-role"}.Build(),
			}.Build()

			rolesClient := &mockRolesClient{
				getResponse: &privatev1.RolesGetResponse{Object: role},
			}

			usersClient := newMockUsersClient("user-1", "user-2")

			idpClient := idp.NewMockClientInterface(ctrl)
			idpClient.EXPECT().
				AssignTenantRolesToUser(ctx, "test-org", "keycloak-user-1", gomock.Any()).
				Return(nil)
			idpClient.EXPECT().
				AssignTenantRolesToUser(ctx, "test-org", "keycloak-user-2", gomock.Any()).
				Return(nil)

			binding := privatev1.RoleBinding_builder{
				Id: "rb-sync-1",
				Metadata: privatev1.Metadata_builder{
					Tenant: "test-org",
				}.Build(),
				Spec: privatev1.RoleBindingSpec_builder{
					Role:  "custom-role",
					Users: []string{"user-1", "user-2"},
				}.Build(),
				Status: privatev1.RoleBindingStatus_builder{
					State: privatev1.RoleBindingState_ROLE_BINDING_STATE_PENDING,
				}.Build(),
			}.Build()

			f := &function{
				logger:      logger,
				rolesClient: rolesClient,
				usersClient: usersClient,
				idpClient:   idpClient,
			}
			t := &task{r: f, binding: binding}

			err := t.syncRoleAssignments(ctx)

			Expect(err).ToNot(HaveOccurred())
			Expect(binding.GetStatus().GetState()).To(Equal(privatev1.RoleBindingState_ROLE_BINDING_STATE_READY))
			Expect(binding.GetStatus().GetUsers()).To(Equal([]string{"user-1", "user-2"}))
			Expect(binding.GetStatus().GetMessage()).To(ContainSubstring("assigned to 2 user(s)"))
		})

		It("should assign tenant-admin role as realm-level role", func() {
			role := privatev1.Role_builder{
				Id:       "role-admin",
				Metadata: privatev1.Metadata_builder{Name: "tenant-admin"}.Build(),
			}.Build()

			rolesClient := &mockRolesClient{
				getResponse: &privatev1.RolesGetResponse{Object: role},
			}

			usersClient := newMockUsersClient("user-1")

			idpClient := idp.NewMockClientInterface(ctrl)
			idpClient.EXPECT().
				AssignTenantRolesToUser(ctx, "test-org", "keycloak-user-1", gomock.Any()).
				Return(nil)

			binding := privatev1.RoleBinding_builder{
				Id: "rb-sync-2",
				Metadata: privatev1.Metadata_builder{
					Tenant: "test-org",
				}.Build(),
				Spec: privatev1.RoleBindingSpec_builder{
					Role:  "tenant-admin",
					Users: []string{"user-1"},
				}.Build(),
				Status: privatev1.RoleBindingStatus_builder{
					State: privatev1.RoleBindingState_ROLE_BINDING_STATE_PENDING,
				}.Build(),
			}.Build()

			f := &function{
				logger:      logger,
				rolesClient: rolesClient,
				usersClient: usersClient,
				idpClient:   idpClient,
			}
			t := &task{r: f, binding: binding}

			err := t.syncRoleAssignments(ctx)

			Expect(err).ToNot(HaveOccurred())
			Expect(binding.GetStatus().GetState()).To(Equal(privatev1.RoleBindingState_ROLE_BINDING_STATE_READY))
			Expect(binding.GetStatus().GetUsers()).To(Equal([]string{"user-1"}))
		})

		It("should set FAILED state when role assignment fails", func() {
			role := privatev1.Role_builder{
				Id:       "role-fail",
				Metadata: privatev1.Metadata_builder{Name: "custom-role"}.Build(),
			}.Build()

			rolesClient := &mockRolesClient{
				getResponse: &privatev1.RolesGetResponse{Object: role},
			}

			usersClient := newMockUsersClient("user-1")

			idpClient := idp.NewMockClientInterface(ctrl)
			idpClient.EXPECT().
				AssignTenantRolesToUser(ctx, "test-org", "keycloak-user-1", gomock.Any()).
				Return(fmt.Errorf("IDP error"))

			binding := privatev1.RoleBinding_builder{
				Id: "rb-sync-fail",
				Metadata: privatev1.Metadata_builder{
					Tenant: "test-org",
				}.Build(),
				Spec: privatev1.RoleBindingSpec_builder{
					Role:  "custom-role",
					Users: []string{"user-1"},
				}.Build(),
				Status: privatev1.RoleBindingStatus_builder{
					State: privatev1.RoleBindingState_ROLE_BINDING_STATE_PENDING,
				}.Build(),
			}.Build()

			f := &function{
				logger:      logger,
				rolesClient: rolesClient,
				usersClient: usersClient,
				idpClient:   idpClient,
			}
			t := &task{r: f, binding: binding}

			err := t.syncRoleAssignments(ctx)

			Expect(err).ToNot(HaveOccurred())
			Expect(binding.GetStatus().GetState()).To(Equal(privatev1.RoleBindingState_ROLE_BINDING_STATE_FAILED))
			Expect(binding.GetStatus().GetMessage()).To(ContainSubstring("Failed to assign role"))
		})

		It("should set FAILED state when role not found", func() {
			rolesClient := &mockRolesClient{
				getErr: fmt.Errorf("role not found"),
				listResponse: &privatev1.RolesListResponse{
					Items: []*privatev1.Role{},
				},
			}

			binding := privatev1.RoleBinding_builder{
				Id: "rb-no-role",
				Spec: privatev1.RoleBindingSpec_builder{
					Role: "nonexistent",
				}.Build(),
				Status: privatev1.RoleBindingStatus_builder{
					State: privatev1.RoleBindingState_ROLE_BINDING_STATE_PENDING,
				}.Build(),
			}.Build()

			f := &function{
				logger:      logger,
				rolesClient: rolesClient,
			}
			t := &task{r: f, binding: binding}

			err := t.syncRoleAssignments(ctx)

			Expect(err).ToNot(HaveOccurred())
			Expect(binding.GetStatus().GetState()).To(Equal(privatev1.RoleBindingState_ROLE_BINDING_STATE_FAILED))
			Expect(binding.GetStatus().GetMessage()).To(ContainSubstring("Failed to fetch role"))
		})
	})

	Describe("handleUserListChange", func() {
		It("should add new users when users are added to spec", func() {
			role := privatev1.Role_builder{
				Id:       "role-change",
				Metadata: privatev1.Metadata_builder{Name: "custom-role"}.Build(),
			}.Build()

			rolesClient := &mockRolesClient{
				getResponse: &privatev1.RolesGetResponse{Object: role},
			}

			usersClient := newMockUsersClient("user-1", "user-2", "user-3")

			idpClient := idp.NewMockClientInterface(ctrl)
			idpClient.EXPECT().
				AssignTenantRolesToUser(ctx, "test-org", "keycloak-user-3", gomock.Any()).
				Return(nil)

			binding := privatev1.RoleBinding_builder{
				Id: "rb-add-user",
				Metadata: privatev1.Metadata_builder{
					Tenant: "test-org",
				}.Build(),
				Spec: privatev1.RoleBindingSpec_builder{
					Role:  "custom-role",
					Users: []string{"user-1", "user-2", "user-3"},
				}.Build(),
				Status: privatev1.RoleBindingStatus_builder{
					State: privatev1.RoleBindingState_ROLE_BINDING_STATE_READY,
					Users: []string{"user-1", "user-2"},
				}.Build(),
			}.Build()

			f := &function{
				logger:      logger,
				rolesClient: rolesClient,
				usersClient: usersClient,
				idpClient:   idpClient,
			}
			t := &task{r: f, binding: binding}

			err := t.handleUserListChange(ctx)

			Expect(err).ToNot(HaveOccurred())
			Expect(binding.GetStatus().GetState()).To(Equal(privatev1.RoleBindingState_ROLE_BINDING_STATE_READY))
			Expect(binding.GetStatus().GetUsers()).To(Equal([]string{"user-1", "user-2", "user-3"}))
			Expect(binding.GetStatus().GetMessage()).To(ContainSubstring("added 1 user(s)"))
		})

		It("should remove users when users are removed from spec", func() {
			role := privatev1.Role_builder{
				Id:       "role-remove",
				Metadata: privatev1.Metadata_builder{Name: "custom-role"}.Build(),
			}.Build()

			rolesClient := &mockRolesClient{
				getResponse: &privatev1.RolesGetResponse{Object: role},
			}

			usersClient := newMockUsersClient("user-1", "user-2", "user-3")

			idpClient := idp.NewMockClientInterface(ctrl)
			idpClient.EXPECT().
				RemoveTenantRolesFromUser(ctx, "test-org", "keycloak-user-3", gomock.Any()).
				Return(nil)

			binding := privatev1.RoleBinding_builder{
				Id: "rb-remove-user",
				Metadata: privatev1.Metadata_builder{
					Tenant: "test-org",
				}.Build(),
				Spec: privatev1.RoleBindingSpec_builder{
					Role:  "custom-role",
					Users: []string{"user-1", "user-2"},
				}.Build(),
				Status: privatev1.RoleBindingStatus_builder{
					State: privatev1.RoleBindingState_ROLE_BINDING_STATE_READY,
					Users: []string{"user-1", "user-2", "user-3"},
				}.Build(),
			}.Build()

			f := &function{
				logger:      logger,
				rolesClient: rolesClient,
				usersClient: usersClient,
				idpClient:   idpClient,
			}
			t := &task{r: f, binding: binding}

			err := t.handleUserListChange(ctx)

			Expect(err).ToNot(HaveOccurred())
			Expect(binding.GetStatus().GetState()).To(Equal(privatev1.RoleBindingState_ROLE_BINDING_STATE_READY))
			Expect(binding.GetStatus().GetUsers()).To(Equal([]string{"user-1", "user-2"}))
			Expect(binding.GetStatus().GetMessage()).To(ContainSubstring("removed 1 user(s)"))
		})

		It("should handle both additions and removals", func() {
			role := privatev1.Role_builder{
				Id:       "role-both",
				Metadata: privatev1.Metadata_builder{Name: "tenant-admin"}.Build(),
			}.Build()

			rolesClient := &mockRolesClient{
				getResponse: &privatev1.RolesGetResponse{Object: role},
			}

			usersClient := newMockUsersClient("user-1", "user-2", "user-3")

			idpClient := idp.NewMockClientInterface(ctrl)
			idpClient.EXPECT().
				RemoveTenantRolesFromUser(ctx, "test-org", "keycloak-user-2", gomock.Any()).
				Return(nil)
			idpClient.EXPECT().
				AssignTenantRolesToUser(ctx, "test-org", "keycloak-user-3", gomock.Any()).
				Return(nil)

			binding := privatev1.RoleBinding_builder{
				Id: "rb-both",
				Metadata: privatev1.Metadata_builder{
					Tenant: "test-org",
				}.Build(),
				Spec: privatev1.RoleBindingSpec_builder{
					Role:  "tenant-admin",
					Users: []string{"user-1", "user-3"},
				}.Build(),
				Status: privatev1.RoleBindingStatus_builder{
					State: privatev1.RoleBindingState_ROLE_BINDING_STATE_READY,
					Users: []string{"user-1", "user-2"},
				}.Build(),
			}.Build()

			f := &function{
				logger:      logger,
				rolesClient: rolesClient,
				usersClient: usersClient,
				idpClient:   idpClient,
			}
			t := &task{r: f, binding: binding}

			err := t.handleUserListChange(ctx)

			Expect(err).ToNot(HaveOccurred())
			Expect(binding.GetStatus().GetState()).To(Equal(privatev1.RoleBindingState_ROLE_BINDING_STATE_READY))
			Expect(binding.GetStatus().GetUsers()).To(Equal([]string{"user-1", "user-3"}))
		})

		It("should do nothing when user list unchanged", func() {
			binding := privatev1.RoleBinding_builder{
				Id: "rb-no-change",
				Spec: privatev1.RoleBindingSpec_builder{
					Users: []string{"user-1", "user-2"},
				}.Build(),
				Status: privatev1.RoleBindingStatus_builder{
					State: privatev1.RoleBindingState_ROLE_BINDING_STATE_READY,
					Users: []string{"user-1", "user-2"},
				}.Build(),
			}.Build()

			f := &function{logger: logger}
			t := &task{r: f, binding: binding}

			err := t.handleUserListChange(ctx)

			Expect(err).ToNot(HaveOccurred())
		})

		It("should set FAILED state when role fetch fails", func() {
			rolesClient := &mockRolesClient{
				getErr: fmt.Errorf("role not found"),
				listResponse: &privatev1.RolesListResponse{
					Items: []*privatev1.Role{},
				},
			}

			binding := privatev1.RoleBinding_builder{
				Id: "rb-change-no-role",
				Spec: privatev1.RoleBindingSpec_builder{
					Role:  "nonexistent",
					Users: []string{"user-1", "user-2"},
				}.Build(),
				Status: privatev1.RoleBindingStatus_builder{
					State: privatev1.RoleBindingState_ROLE_BINDING_STATE_READY,
					Users: []string{"user-1"},
				}.Build(),
			}.Build()

			f := &function{
				logger:      logger,
				rolesClient: rolesClient,
			}
			t := &task{r: f, binding: binding}

			err := t.handleUserListChange(ctx)

			Expect(err).ToNot(HaveOccurred())
			Expect(binding.GetStatus().GetState()).To(Equal(privatev1.RoleBindingState_ROLE_BINDING_STATE_FAILED))
			Expect(binding.GetStatus().GetMessage()).To(ContainSubstring("Failed to fetch role"))
		})

		It("should set FAILED state when assignment fails during update", func() {
			role := privatev1.Role_builder{
				Id:       "role-fail-update",
				Metadata: privatev1.Metadata_builder{Name: "custom-role"}.Build(),
			}.Build()

			rolesClient := &mockRolesClient{
				getResponse: &privatev1.RolesGetResponse{Object: role},
			}

			usersClient := newMockUsersClient("user-1", "user-2")

			idpClient := idp.NewMockClientInterface(ctrl)
			idpClient.EXPECT().
				AssignTenantRolesToUser(ctx, "test-org", "keycloak-user-2", gomock.Any()).
				Return(fmt.Errorf("IDP error"))

			binding := privatev1.RoleBinding_builder{
				Id: "rb-fail-add",
				Metadata: privatev1.Metadata_builder{
					Tenant: "test-org",
				}.Build(),
				Spec: privatev1.RoleBindingSpec_builder{
					Role:  "custom-role",
					Users: []string{"user-1", "user-2"},
				}.Build(),
				Status: privatev1.RoleBindingStatus_builder{
					State: privatev1.RoleBindingState_ROLE_BINDING_STATE_READY,
					Users: []string{"user-1"},
				}.Build(),
			}.Build()

			f := &function{
				logger:      logger,
				rolesClient: rolesClient,
				usersClient: usersClient,
				idpClient:   idpClient,
			}
			t := &task{r: f, binding: binding}

			err := t.handleUserListChange(ctx)

			Expect(err).ToNot(HaveOccurred())
			Expect(binding.GetStatus().GetState()).To(Equal(privatev1.RoleBindingState_ROLE_BINDING_STATE_FAILED))
			Expect(binding.GetStatus().GetMessage()).To(ContainSubstring("Failed to assign role"))
		})

		It("should set FAILED state when removal fails during update", func() {
			role := privatev1.Role_builder{
				Id:       "role-fail-remove",
				Metadata: privatev1.Metadata_builder{Name: "custom-role"}.Build(),
			}.Build()

			rolesClient := &mockRolesClient{
				getResponse: &privatev1.RolesGetResponse{Object: role},
			}

			usersClient := newMockUsersClient("user-1", "user-2")

			idpClient := idp.NewMockClientInterface(ctrl)
			idpClient.EXPECT().
				RemoveTenantRolesFromUser(ctx, "test-org", "keycloak-user-2", gomock.Any()).
				Return(fmt.Errorf("IDP error"))

			binding := privatev1.RoleBinding_builder{
				Id: "rb-fail-remove",
				Metadata: privatev1.Metadata_builder{
					Tenant: "test-org",
				}.Build(),
				Spec: privatev1.RoleBindingSpec_builder{
					Role:  "custom-role",
					Users: []string{"user-1"},
				}.Build(),
				Status: privatev1.RoleBindingStatus_builder{
					State: privatev1.RoleBindingState_ROLE_BINDING_STATE_READY,
					Users: []string{"user-1", "user-2"},
				}.Build(),
			}.Build()

			f := &function{
				logger:      logger,
				rolesClient: rolesClient,
				usersClient: usersClient,
				idpClient:   idpClient,
			}
			t := &task{r: f, binding: binding}

			err := t.handleUserListChange(ctx)

			Expect(err).ToNot(HaveOccurred())
			Expect(binding.GetStatus().GetState()).To(Equal(privatev1.RoleBindingState_ROLE_BINDING_STATE_FAILED))
			Expect(binding.GetStatus().GetMessage()).To(ContainSubstring("Failed to remove role"))
		})
	})

	Describe("delete", func() {
		It("should remove roles from all users and remove finalizer", func() {
			role := privatev1.Role_builder{
				Id:       "role-delete",
				Metadata: privatev1.Metadata_builder{Name: "custom-role"}.Build(),
			}.Build()

			rolesClient := &mockRolesClient{
				getResponse: &privatev1.RolesGetResponse{Object: role},
			}

			usersClient := newMockUsersClient("user-1", "user-2")

			idpClient := idp.NewMockClientInterface(ctrl)
			idpClient.EXPECT().
				RemoveTenantRolesFromUser(ctx, "test-org", "keycloak-user-1", gomock.Any()).
				Return(nil)
			idpClient.EXPECT().
				RemoveTenantRolesFromUser(ctx, "test-org", "keycloak-user-2", gomock.Any()).
				Return(nil)

			binding := privatev1.RoleBinding_builder{
				Id: "rb-delete",
				Metadata: privatev1.Metadata_builder{
					Tenant:     "test-org",
					Finalizers: []string{finalizers.Controller},
				}.Build(),
				Spec: privatev1.RoleBindingSpec_builder{
					Role:  "custom-role",
					Users: []string{"user-1", "user-2"},
				}.Build(),
				Status: privatev1.RoleBindingStatus_builder{
					State: privatev1.RoleBindingState_ROLE_BINDING_STATE_READY,
				}.Build(),
			}.Build()

			f := &function{
				logger:      logger,
				rolesClient: rolesClient,
				usersClient: usersClient,
				idpClient:   idpClient,
			}
			t := &task{r: f, binding: binding}

			err := t.delete(ctx)

			Expect(err).ToNot(HaveOccurred())
			Expect(hasFinalizer(binding)).To(BeFalse())
		})

		It("should remove finalizer immediately if state is not READY", func() {
			binding := privatev1.RoleBinding_builder{
				Id: "rb-delete-pending",
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.Controller},
				}.Build(),
				Status: privatev1.RoleBindingStatus_builder{
					State: privatev1.RoleBindingState_ROLE_BINDING_STATE_PENDING,
				}.Build(),
			}.Build()

			f := &function{logger: logger}
			t := &task{r: f, binding: binding}

			err := t.delete(ctx)

			Expect(err).ToNot(HaveOccurred())
			Expect(hasFinalizer(binding)).To(BeFalse())
		})

		It("should continue deletion even if role fetch fails", func() {
			rolesClient := &mockRolesClient{
				getErr: fmt.Errorf("role not found"),
				listResponse: &privatev1.RolesListResponse{
					Items: []*privatev1.Role{},
				},
			}

			binding := privatev1.RoleBinding_builder{
				Id: "rb-delete-no-role",
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.Controller},
				}.Build(),
				Spec: privatev1.RoleBindingSpec_builder{
					Role: "missing-role",
				}.Build(),
				Status: privatev1.RoleBindingStatus_builder{
					State: privatev1.RoleBindingState_ROLE_BINDING_STATE_READY,
				}.Build(),
			}.Build()

			f := &function{
				logger:      logger,
				rolesClient: rolesClient,
			}
			t := &task{r: f, binding: binding}

			err := t.delete(ctx)

			Expect(err).ToNot(HaveOccurred())
			Expect(hasFinalizer(binding)).To(BeFalse())
		})

		It("should continue removing from other users if one fails", func() {
			role := privatev1.Role_builder{
				Id:       "role-partial-delete",
				Metadata: privatev1.Metadata_builder{Name: "custom-role"}.Build(),
			}.Build()

			rolesClient := &mockRolesClient{
				getResponse: &privatev1.RolesGetResponse{Object: role},
			}

			usersClient := newMockUsersClient("user-1", "user-2")

			idpClient := idp.NewMockClientInterface(ctrl)
			idpClient.EXPECT().
				RemoveTenantRolesFromUser(ctx, "test-org", "keycloak-user-1", gomock.Any()).
				Return(fmt.Errorf("IDP error"))
			idpClient.EXPECT().
				RemoveTenantRolesFromUser(ctx, "test-org", "keycloak-user-2", gomock.Any()).
				Return(nil)

			binding := privatev1.RoleBinding_builder{
				Id: "rb-delete-partial",
				Metadata: privatev1.Metadata_builder{
					Tenant:     "test-org",
					Finalizers: []string{finalizers.Controller},
				}.Build(),
				Spec: privatev1.RoleBindingSpec_builder{
					Role:  "custom-role",
					Users: []string{"user-1", "user-2"},
				}.Build(),
				Status: privatev1.RoleBindingStatus_builder{
					State: privatev1.RoleBindingState_ROLE_BINDING_STATE_READY,
				}.Build(),
			}.Build()

			f := &function{
				logger:      logger,
				rolesClient: rolesClient,
				usersClient: usersClient,
				idpClient:   idpClient,
			}
			t := &task{r: f, binding: binding}

			err := t.delete(ctx)

			Expect(err).ToNot(HaveOccurred())
			Expect(hasFinalizer(binding)).To(BeFalse())
		})
	})

	Describe("update", func() {
		It("should skip reconciliation for FAILED state", func() {
			binding := privatev1.RoleBinding_builder{
				Id: "rb-failed",
				Status: privatev1.RoleBindingStatus_builder{
					State: privatev1.RoleBindingState_ROLE_BINDING_STATE_FAILED,
				}.Build(),
			}.Build()

			f := &function{logger: logger}
			t := &task{r: f, binding: binding}

			err := t.update(ctx)

			Expect(err).ToNot(HaveOccurred())
		})

		It("should call handleUserListChange for READY state", func() {
			binding := privatev1.RoleBinding_builder{
				Id: "rb-ready",
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.Controller},
				}.Build(),
				Spec: privatev1.RoleBindingSpec_builder{
					Users: []string{"user-1"},
				}.Build(),
				Status: privatev1.RoleBindingStatus_builder{
					State: privatev1.RoleBindingState_ROLE_BINDING_STATE_READY,
					Users: []string{"user-1"},
				}.Build(),
			}.Build()

			f := &function{logger: logger}
			t := &task{r: f, binding: binding}

			err := t.update(ctx)

			Expect(err).ToNot(HaveOccurred())
		})
	})
})
