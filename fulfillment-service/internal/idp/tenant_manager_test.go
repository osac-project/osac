/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package idp

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mockClient is a mock Keycloak client for testing.
type mockClient struct {
	createdTenant       *Tenant
	createdUsers        []*User
	deletedTenant       string
	deletedUsers        []string                      // Track deleted user IDs
	userRoleAssignments map[string]map[string][]*Role // userID -> clientID -> roles
	failUserCreation    bool                          // Trigger user creation failure
	failRoleAssignment  bool                          // Trigger role assignment failure
	failTenantDeletion  bool                          // Trigger tenant deletion failure
	failTenantGet       bool                          // Trigger tenant get failure
	failTenantUpdate    bool                          // Trigger tenant update failure
	returnNilTenant     bool                          // GetTenant returns nil without error
	tenantUpdateCalled  bool                          // Track whether UpdateTenant was called
}

func (m *mockClient) CreateTenant(ctx context.Context, tenant *Tenant) (*Tenant, error) {
	// Create a copy to avoid mutation
	createdTenant := &Tenant{
		ID:          tenant.ID,
		Name:        tenant.Name,
		DisplayName: tenant.DisplayName,
		Enabled:     tenant.Enabled,
		Attributes:  tenant.Attributes,
		Domains:     tenant.Domains,
	}
	m.createdTenant = createdTenant
	return createdTenant, nil
}

func (m *mockClient) GetTenant(ctx context.Context, name string) (*Tenant, error) {
	if m.failTenantGet {
		return nil, fmt.Errorf("simulated get tenant failure")
	}
	if m.returnNilTenant {
		return nil, nil
	}
	return m.createdTenant, nil
}

func (m *mockClient) UpdateTenant(ctx context.Context, tenant *Tenant) (*Tenant, error) {
	m.tenantUpdateCalled = true
	if m.failTenantUpdate {
		return nil, fmt.Errorf("simulated update tenant failure")
	}
	if m.createdTenant != nil {
		m.createdTenant.Domains = tenant.Domains
		m.createdTenant.Enabled = tenant.Enabled
	}
	return m.createdTenant, nil
}

func (m *mockClient) DeleteTenant(ctx context.Context, name string) error {
	if m.failTenantDeletion {
		return fmt.Errorf("simulated tenant deletion failure")
	}

	// Simulate Keycloak behavior: delete break-glass account first
	breakGlassUsername := fmt.Sprintf("%s-osac-break-glass", name)
	for _, user := range m.createdUsers {
		if user.Username == breakGlassUsername {
			m.deletedUsers = append(m.deletedUsers, user.ID)
			break
		}
	}

	m.deletedTenant = name
	return nil
}

func (m *mockClient) CreateUser(ctx context.Context, tenantName string, user *User) (*User, error) {
	if m.failUserCreation {
		return nil, fmt.Errorf("simulated user creation failure")
	}
	// Create a copy with ID populated
	userID := fmt.Sprintf("user-%d", len(m.createdUsers)+1)
	createdUser := &User{
		ID:              userID,
		Username:        user.Username,
		Email:           user.Email,
		EmailVerified:   user.EmailVerified,
		Enabled:         user.Enabled,
		FirstName:       user.FirstName,
		LastName:        user.LastName,
		Attributes:      user.Attributes,
		Groups:          user.Groups,
		Credentials:     user.Credentials,
		RequiredActions: user.RequiredActions,
	}
	m.createdUsers = append(m.createdUsers, createdUser)
	return createdUser, nil
}

func (m *mockClient) GetUser(ctx context.Context, tenantName, userID string) (*User, error) {
	for _, user := range m.createdUsers {
		if user.ID == userID {
			return user, nil
		}
	}
	return nil, nil
}

func (m *mockClient) GetUserByUsername(ctx context.Context, tenantName, username string) (*User, error) {
	for _, user := range m.createdUsers {
		if user.Username == username {
			return user, nil
		}
	}
	return nil, nil
}

func (m *mockClient) ListUsers(ctx context.Context, tenantName string) ([]*User, error) {
	return m.createdUsers, nil
}

func (m *mockClient) DeleteUser(ctx context.Context, tenantName, userID string) error {
	m.deletedUsers = append(m.deletedUsers, userID)
	return nil
}

func (m *mockClient) ListTenantRoles(ctx context.Context, tenantName string) ([]*Role, error) {
	return nil, nil
}

func (m *mockClient) ListClientRoles(ctx context.Context, tenantName, clientID string) ([]*Role, error) {
	// Return full set of realm-management roles (matching Keycloak's standard roles)
	if clientID == "realm-management" {
		return []*Role{
			{ID: "1", Name: "manage-realm", ClientRole: true},
			{ID: "2", Name: "manage-users", ClientRole: true},
			{ID: "3", Name: "manage-clients", ClientRole: true},
			{ID: "4", Name: "manage-identity-providers", ClientRole: true},
			{ID: "5", Name: "manage-authorization", ClientRole: true},
			{ID: "6", Name: "manage-events", ClientRole: true},
			{ID: "7", Name: "view-realm", ClientRole: true},
			{ID: "8", Name: "view-users", ClientRole: true},
			{ID: "9", Name: "view-clients", ClientRole: true},
			{ID: "10", Name: "view-identity-providers", ClientRole: true},
			{ID: "11", Name: "view-authorization", ClientRole: true},
			{ID: "12", Name: "view-events", ClientRole: true},
		}, nil
	}
	return nil, nil
}

func (m *mockClient) AssignTenantRolesToUser(ctx context.Context, tenantName, userID string, roles []*Role) error {
	if m.userRoleAssignments == nil {
		m.userRoleAssignments = make(map[string]map[string][]*Role)
	}
	if m.userRoleAssignments[userID] == nil {
		m.userRoleAssignments[userID] = make(map[string][]*Role)
	}
	m.userRoleAssignments[userID]["realm"] = roles
	return nil
}

func (m *mockClient) AssignClientRolesToUser(ctx context.Context, tenantName, userID, clientID string, roles []*Role) error {
	if m.userRoleAssignments == nil {
		m.userRoleAssignments = make(map[string]map[string][]*Role)
	}
	if m.userRoleAssignments[userID] == nil {
		m.userRoleAssignments[userID] = make(map[string][]*Role)
	}
	m.userRoleAssignments[userID][clientID] = roles
	return nil
}

func (m *mockClient) RemoveTenantRolesFromUser(ctx context.Context, tenantName, userID string, roles []*Role) error {
	return nil
}

func (m *mockClient) RemoveClientRolesFromUser(ctx context.Context, tenantName, userID, clientID string, roles []*Role) error {
	return nil
}

func (m *mockClient) GetUserTenantRoles(ctx context.Context, tenantName, userID string) ([]*Role, error) {
	if m.userRoleAssignments != nil && m.userRoleAssignments[userID] != nil {
		return m.userRoleAssignments[userID]["realm"], nil
	}
	return nil, nil
}

func (m *mockClient) GetUserClientRoles(ctx context.Context, tenantName, userID, clientID string) ([]*Role, error) {
	if m.userRoleAssignments != nil && m.userRoleAssignments[userID] != nil {
		return m.userRoleAssignments[userID][clientID], nil
	}
	return nil, nil
}

func (m *mockClient) AssignTenantAdminPermissions(ctx context.Context, tenantName, userID string) error {
	if m.failRoleAssignment {
		return fmt.Errorf("simulated role assignment failure")
	}
	// Simulate assigning full admin roles (matching keycloakRealmManagementRoles)
	roles := []*Role{
		{ID: "1", Name: "manage-realm", ClientRole: true},
		{ID: "2", Name: "manage-users", ClientRole: true},
		{ID: "3", Name: "manage-clients", ClientRole: true},
		{ID: "4", Name: "manage-identity-providers", ClientRole: true},
		{ID: "5", Name: "manage-authorization", ClientRole: true},
		{ID: "6", Name: "manage-events", ClientRole: true},
		{ID: "7", Name: "view-realm", ClientRole: true},
		{ID: "8", Name: "view-users", ClientRole: true},
		{ID: "9", Name: "view-clients", ClientRole: true},
		{ID: "10", Name: "view-identity-providers", ClientRole: true},
		{ID: "11", Name: "view-authorization", ClientRole: true},
		{ID: "12", Name: "view-events", ClientRole: true},
	}
	return m.AssignClientRolesToUser(ctx, tenantName, userID, "realm-management", roles)
}

func (m *mockClient) AssignIdpManagerPermissions(ctx context.Context, userID string) error {
	if m.failRoleAssignment {
		return fmt.Errorf("simulated role assignment failure")
	}
	// Simulate assigning limited IdP manager roles (matching keycloakIdpManagerRoles)
	roles := []*Role{
		{ID: "2", Name: "manage-users", ClientRole: true},
		{ID: "8", Name: "view-users", ClientRole: true},
		{ID: "4", Name: "manage-identity-providers", ClientRole: true},
		{ID: "10", Name: "view-identity-providers", ClientRole: true},
		{ID: "7", Name: "view-realm", ClientRole: true},
	}
	// Use empty tenant name since it's no longer a parameter
	return m.AssignClientRolesToUser(ctx, "", userID, "realm-management", roles)
}

// Identity Provider stub methods
func (m *mockClient) CreateIdentityProvider(ctx context.Context, tenantName string, idp *IdentityProvider) (*IdentityProvider, error) {
	return idp, nil
}

func (m *mockClient) GetIdentityProvider(ctx context.Context, tenantName, alias string) (*IdentityProvider, error) {
	return nil, nil
}

func (m *mockClient) ListIdentityProviders(ctx context.Context, tenantName string) ([]*IdentityProvider, error) {
	return nil, nil
}

func (m *mockClient) DeleteIdentityProvider(ctx context.Context, tenantName, alias string) error {
	return nil
}

func (m *mockClient) CreateGroup(ctx context.Context, tenantName, groupPath string) (string, error) {
	// Return a fake group ID
	return "test-group-id", nil
}

func (m *mockClient) DeleteGroup(ctx context.Context, tenantName, groupID string) error {
	return nil
}

func (m *mockClient) GetGroupIDByPath(ctx context.Context, tenantName, groupPath string) (string, error) {
	// Return a fake group ID for testing
	return "test-group-id", nil
}

func (m *mockClient) AddUserToGroup(ctx context.Context, tenantName, userID, groupID string) error {
	// Stub for testing - no-op
	return nil
}

func (m *mockClient) RemoveUserFromGroup(ctx context.Context, tenantName, username, groupID string) error {
	// Stub for testing - no-op
	return nil
}

var _ = Describe("TenantManager", func() {
	var (
		ctx     context.Context
		mock    *mockClient
		manager *TenantManager
	)

	BeforeEach(func() {
		var err error
		ctx = context.Background()
		mock = &mockClient{}

		manager, err = NewTenantManager().
			SetLogger(logger).
			SetClient(mock).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("CreateTenant", func() {
		It("creates a tenant with break-glass account", func() {
			config := &TenantConfig{
				Name:               "test-tenant",
				DisplayName:        "Test Tenant",
				Enabled:            new(true),
				BreakGlassPassword: "breakglass123",
			}

			credentials, err := manager.CreateTenant(ctx, config)
			Expect(err).ToNot(HaveOccurred())
			Expect(credentials).ToNot(BeNil())

			// Verify Tenant was created
			Expect(mock.createdTenant).ToNot(BeNil())
			Expect(mock.createdTenant.Name).To(Equal("test-tenant"))
			Expect(mock.createdTenant.Enabled).To(BeTrue())

			// Verify break-glass user was created
			Expect(mock.createdUsers).To(HaveLen(1))
			breakGlassUser := mock.createdUsers[0]
			Expect(breakGlassUser.Username).To(Equal("test-tenant-osac-break-glass"))
			Expect(breakGlassUser.Email).To(Equal("break-glass@test-tenant.osac.local"))
			Expect(breakGlassUser.FirstName).To(Equal("OSAC"))
			Expect(breakGlassUser.LastName).To(Equal("Break-Glass"))

			// Verify credentials were returned
			Expect(credentials.UserID).To(Equal(breakGlassUser.ID))
			Expect(credentials.Username).To(Equal("test-tenant-osac-break-glass"))
			Expect(credentials.Email).To(Equal("break-glass@test-tenant.osac.local"))
			Expect(credentials.Password).To(Equal("breakglass123"))

			// Verify password is temporary
			Expect(breakGlassUser.Credentials).To(HaveLen(1))
			Expect(breakGlassUser.Credentials[0].Temporary).To(BeTrue())
		})

		It("creates a disabled tenant when Enabled is false", func() {
			config := &TenantConfig{
				Name:               "test-tenant",
				DisplayName:        "Test Tenant",
				Enabled:            new(false),
				BreakGlassPassword: "breakglass123",
			}

			credentials, err := manager.CreateTenant(ctx, config)
			Expect(err).ToNot(HaveOccurred())
			Expect(credentials).ToNot(BeNil())

			Expect(mock.createdTenant).ToNot(BeNil())
			Expect(mock.createdTenant.Name).To(Equal("test-tenant"))
			Expect(mock.createdTenant.Enabled).To(BeFalse())
		})

		It("assigns IdP manager roles to break-glass account", func() {
			config := &TenantConfig{
				Name:               "test-tenant",
				DisplayName:        "Test Tenant",
				Enabled:            new(true),
				BreakGlassPassword: "breakglass123",
			}

			credentials, err := manager.CreateTenant(ctx, config)
			Expect(err).ToNot(HaveOccurred())
			Expect(credentials).ToNot(BeNil())
			Expect(credentials.Username).ToNot(BeEmpty())
			Expect(credentials.Email).ToNot(BeEmpty())
			Expect(credentials.Password).ToNot(BeEmpty())
			Expect(credentials.UserID).ToNot(BeEmpty())

			// Verify break-glass user was created
			Expect(mock.createdUsers).To(HaveLen(1))
			breakGlassUserID := mock.createdUsers[0].ID
			Expect(credentials.UserID).To(Equal(breakGlassUserID))

			// Verify IdP manager roles were assigned
			Expect(mock.userRoleAssignments).ToNot(BeNil())
			Expect(mock.userRoleAssignments[breakGlassUserID]).ToNot(BeNil())

			// Check for realm-management client role assignments (limited set)
			breakGlassRoles := mock.userRoleAssignments[breakGlassUserID]["realm-management"]
			Expect(breakGlassRoles).ToNot(BeEmpty())

			// Verify specific IdP manager roles were assigned
			roleNames := make([]string, len(breakGlassRoles))
			for i, role := range breakGlassRoles {
				roleNames[i] = role.Name
			}

			// Should contain all 5 IdP manager roles
			Expect(roleNames).To(ContainElement("manage-users"))
			Expect(roleNames).To(ContainElement("view-users"))
			Expect(roleNames).To(ContainElement("manage-identity-providers"))
			Expect(roleNames).To(ContainElement("view-identity-providers"))
			Expect(roleNames).To(ContainElement("view-realm"))
			// Should NOT contain full admin roles like manage-realm or manage-clients
			Expect(roleNames).ToNot(ContainElement("manage-realm"))
			Expect(roleNames).ToNot(ContainElement("manage-clients"))
		})

		It("uses custom break-glass username and email when provided", func() {
			config := &TenantConfig{
				Name:               "test-tenant",
				DisplayName:        "Test Tenant",
				Enabled:            new(true),
				BreakGlassUsername: "custom-break-glass",
				BreakGlassEmail:    "custom@example.com",
				BreakGlassPassword: "breakglass123",
			}

			credentials, err := manager.CreateTenant(ctx, config)
			Expect(err).ToNot(HaveOccurred())

			Expect(mock.createdUsers).To(HaveLen(1))
			breakGlassUser := mock.createdUsers[0]
			Expect(breakGlassUser.Username).To(Equal("custom-break-glass"))
			Expect(breakGlassUser.Email).To(Equal("custom@example.com"))

			Expect(credentials.Username).To(Equal("custom-break-glass"))
			Expect(credentials.Email).To(Equal("custom@example.com"))
			Expect(credentials.Password).To(Equal("breakglass123"))
			Expect(credentials.UserID).To(Equal(breakGlassUser.ID))
		})

		It("generates a password when not provided", func() {
			config := &TenantConfig{
				Name:        "test-tenant",
				DisplayName: "Test Tenant",
				Enabled:     new(true),
			}

			credentials, err := manager.CreateTenant(ctx, config)
			Expect(err).ToNot(HaveOccurred())
			Expect(credentials).ToNot(BeNil())
			Expect(credentials.Password).ToNot(BeEmpty())
			Expect(credentials.Password).To(HaveLen(24))
		})

		It("rolls back tenant on break-glass user creation failure", func() {
			// Create a mock that fails on user creation
			failingMock := &mockClient{
				failUserCreation: true,
			}

			failingManager, err := NewTenantManager().
				SetLogger(logger).
				SetClient(failingMock).
				Build()
			Expect(err).ToNot(HaveOccurred())

			config := &TenantConfig{
				Name:               "test-tenant",
				DisplayName:        "Test Tenant",
				Enabled:            new(true),
				BreakGlassPassword: "breakglass123",
			}

			credentials, err := failingManager.CreateTenant(ctx, config)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to create break-glass account"))
			Expect(credentials).To(BeNil())

			// Verify tenant was created then deleted (rollback)
			Expect(failingMock.createdTenant).ToNot(BeNil())
			Expect(failingMock.deletedTenant).To(Equal("test-tenant"))
		})

		It("rolls back tenant on role assignment failure", func() {
			// Create a mock that fails on role assignment
			failingMock := &mockClient{
				failRoleAssignment: true,
			}

			failingManager, err := NewTenantManager().
				SetLogger(logger).
				SetClient(failingMock).
				Build()
			Expect(err).ToNot(HaveOccurred())

			config := &TenantConfig{
				Name:               "test-tenant",
				DisplayName:        "Test Tenant",
				Enabled:            new(true),
				BreakGlassPassword: "breakglass123",
			}

			credentials, err := failingManager.CreateTenant(ctx, config)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to assign IdP manager permissions"))
			Expect(credentials).To(BeNil())

			// Verify user was created
			Expect(failingMock.createdUsers).To(HaveLen(1))

			// Verify tenant was created then deleted (rollback)
			// Deleting the tenant from the IdP cascade-deletes all users, so we don't
			// need to explicitly delete the user
			Expect(failingMock.createdTenant).ToNot(BeNil())
			Expect(failingMock.deletedTenant).To(Equal("test-tenant"))
		})

		It("rolls back tenant even when original context is cancelled", func() {
			// Create a mock that fails on user creation
			failingMock := &mockClient{
				failUserCreation: true,
			}

			failingManager, err := NewTenantManager().
				SetLogger(logger).
				SetClient(failingMock).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create a context that is already cancelled
			cancelledCtx, cancel := context.WithCancel(context.Background())
			cancel()

			config := &TenantConfig{
				Name:               "test-tenant",
				DisplayName:        "Test Tenant",
				Enabled:            new(true),
				BreakGlassPassword: "breakglass123",
			}

			credentials, err := failingManager.CreateTenant(cancelledCtx, config)
			Expect(err).To(HaveOccurred())
			Expect(credentials).To(BeNil())

			// Verify tenant was created then deleted (rollback)
			// Even though the original context was cancelled, rollback should succeed
			// because it uses a fresh context
			Expect(failingMock.createdTenant).ToNot(BeNil())
			Expect(failingMock.deletedTenant).To(Equal("test-tenant"))
		})
	})

	Describe("UpdateTenant", func() {
		It("skips the IDP update when domains already match", func() {
			config := &TenantConfig{
				Name:               "test-tenant",
				Enabled:            new(true),
				Domains:            []string{"a.example.com", "b.example.com"},
				BreakGlassPassword: "breakglass123",
			}
			_, err := manager.CreateTenant(ctx, config)
			Expect(err).ToNot(HaveOccurred())

			mock.tenantUpdateCalled = false
			err = manager.UpdateTenant(
				ctx, "test-tenant", []string{"b.example.com", "a.example.com"},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(mock.tenantUpdateCalled).To(BeFalse())
			Expect(mock.createdTenant.Domains).To(ConsistOf("a.example.com", "b.example.com"))
		})

		It("updates the tenant domains", func() {
			config := &TenantConfig{
				Name:               "test-tenant",
				Enabled:            new(true),
				Domains:            []string{"example.com"},
				BreakGlassPassword: "breakglass123",
			}
			_, err := manager.CreateTenant(ctx, config)
			Expect(err).ToNot(HaveOccurred())

			err = manager.UpdateTenant(ctx, "test-tenant", []string{"new.example.com", "corp.example.org"})
			Expect(err).ToNot(HaveOccurred())
			Expect(mock.createdTenant.Domains).To(ConsistOf("new.example.com", "corp.example.org"))
		})

		It("clears domains when given an empty list", func() {
			config := &TenantConfig{
				Name:               "test-tenant",
				Enabled:            new(true),
				Domains:            []string{"example.com"},
				BreakGlassPassword: "breakglass123",
			}
			_, err := manager.CreateTenant(ctx, config)
			Expect(err).ToNot(HaveOccurred())
			Expect(mock.createdTenant.Domains).To(ConsistOf("example.com"))

			err = manager.UpdateTenant(ctx, "test-tenant", []string{})
			Expect(err).ToNot(HaveOccurred())
			Expect(mock.createdTenant.Domains).To(BeEmpty())
		})

		It("clears domains when given nil", func() {
			config := &TenantConfig{
				Name:               "test-tenant",
				Enabled:            new(true),
				Domains:            []string{"example.com"},
				BreakGlassPassword: "breakglass123",
			}
			_, err := manager.CreateTenant(ctx, config)
			Expect(err).ToNot(HaveOccurred())

			err = manager.UpdateTenant(ctx, "test-tenant", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(mock.createdTenant.Domains).To(BeEmpty())
		})

		It("returns an error when the name is empty", func() {
			err := manager.UpdateTenant(ctx, "", []string{"example.com"})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("tenant name is mandatory"))
		})

		It("returns an error when getting the tenant from IdP fails", func() {
			failingMock := &mockClient{
				failTenantGet: true,
			}
			failingManager, err := NewTenantManager().
				SetLogger(logger).
				SetClient(failingMock).
				Build()
			Expect(err).ToNot(HaveOccurred())

			err = failingManager.UpdateTenant(ctx, "test-tenant", []string{"example.com"})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to get tenant from IdP for update"))
		})

		It("returns an error when the tenant does not exist in IdP", func() {
			failingMock := &mockClient{
				returnNilTenant: true,
			}
			failingManager, err := NewTenantManager().
				SetLogger(logger).
				SetClient(failingMock).
				Build()
			Expect(err).ToNot(HaveOccurred())

			err = failingManager.UpdateTenant(ctx, "missing-tenant", []string{"example.com"})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})

		It("returns an error when the client update fails", func() {
			failingMock := &mockClient{
				createdTenant:    &Tenant{Name: "test-tenant"},
				failTenantUpdate: true,
			}
			failingManager, err := NewTenantManager().
				SetLogger(logger).
				SetClient(failingMock).
				Build()
			Expect(err).ToNot(HaveOccurred())

			err = failingManager.UpdateTenant(ctx, "test-tenant", []string{"example.com"})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to update tenant in IdP"))
		})
	})

	Describe("DeleteTenant", func() {
		It("deletes the tenant from IdP", func() {
			err := manager.DeleteTenant(ctx, "test-tenant")
			Expect(err).ToNot(HaveOccurred())
			Expect(mock.deletedTenant).To(Equal("test-tenant"))
		})

		It("returns an error when deletion fails", func() {
			failingMock := &mockClient{
				failTenantDeletion: true,
			}

			failingManager, err := NewTenantManager().
				SetLogger(logger).
				SetClient(failingMock).
				Build()
			Expect(err).ToNot(HaveOccurred())

			err = failingManager.DeleteTenant(ctx, "test-tenant")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to delete tenant from IdP"))
		})
	})
})

// Stub methods to satisfy ClientInterface (not used in manager tests)

func (m *mockClient) AddUserToOrganization(ctx context.Context, tenantName string, userID string) error {
	return fmt.Errorf("not implemented in test mock")
}

func (m *mockClient) CreateUserInRealm(ctx context.Context, user *User) (*User, error) {
	return nil, fmt.Errorf("not implemented in test mock")
}

func (m *mockClient) DeleteUserFromOrganization(ctx context.Context, tenantName, userID string) error {
	return fmt.Errorf("not implemented in test mock")
}

func (m *mockClient) DeleteUserFromRealm(ctx context.Context, userID string) error {
	return fmt.Errorf("not implemented in test mock")
}

func (m *mockClient) GetRealmClientByClientID(ctx context.Context, clientID, realmName string) (string, error) {
	return "", fmt.Errorf("not implemented in test mock")
}

func (m *mockClient) GetRealmRole(ctx context.Context, roleName string) (*Role, error) {
	return nil, fmt.Errorf("not implemented in test mock")
}

func (m *mockClient) RemoveRealmRolesFromUser(ctx context.Context, userID string, roles []*Role) error {
	return fmt.Errorf("not implemented in test mock")
}
