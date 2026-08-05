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
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/fulfillment-service/internal/idp"
	"github.com/osac-project/fulfillment-service/internal/masks"
)

// FunctionBuilder contains the data needed to build instances of the reconciler function.
type FunctionBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	idpClient  idp.ClientInterface
}

// NewFunction creates a builder that can be used to configure and create reconciler functions.
func NewFunction() *FunctionBuilder {
	return &FunctionBuilder{}
}

// SetLogger sets the logger that the reconciler will use to write log messages.
func (b *FunctionBuilder) SetLogger(value *slog.Logger) *FunctionBuilder {
	b.logger = value
	return b
}

// SetConnection sets the gRPC connection that the reconciler will use to communicate with the API server.
func (b *FunctionBuilder) SetConnection(value *grpc.ClientConn) *FunctionBuilder {
	b.connection = value
	return b
}

// SetIdpClient sets the IDP client that the reconciler will use to assign roles to users.
func (b *FunctionBuilder) SetIdpClient(value idp.ClientInterface) *FunctionBuilder {
	b.idpClient = value
	return b
}

// Build uses the data stored in the builder to create and configure a new reconciler function.
func (b *FunctionBuilder) Build() (result *function, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.connection == nil {
		err = errors.New("connection is mandatory")
		return
	}
	if b.idpClient == nil {
		err = errors.New("IDP client is mandatory")
		return
	}

	result = &function{
		logger:             b.logger,
		roleBindingsClient: privatev1.NewRoleBindingsClient(b.connection),
		rolesClient:        privatev1.NewRolesClient(b.connection),
		usersClient:        privatev1.NewUsersClient(b.connection),
		idpClient:          b.idpClient,
		maskCalculator: masks.NewCalculator().
			Build(),
	}
	return
}

// function is the implementation of the reconciler function.
type function struct {
	logger             *slog.Logger
	roleBindingsClient privatev1.RoleBindingsClient
	rolesClient        privatev1.RolesClient
	usersClient        privatev1.UsersClient
	idpClient          idp.ClientInterface
	maskCalculator     *masks.Calculator
}

// Run executes the reconciliation logic for the given role binding.
func (r *function) Run(ctx context.Context, binding *privatev1.RoleBinding) error {
	oldBinding := proto.Clone(binding).(*privatev1.RoleBinding)

	task := &task{
		r:       r,
		binding: binding,
	}

	var err error
	if binding.HasMetadata() && binding.GetMetadata().HasDeletionTimestamp() {
		err = task.delete(ctx)
	} else {
		err = task.update(ctx)
	}
	if err != nil {
		return err
	}

	updateMask := r.maskCalculator.Calculate(oldBinding, binding)

	if len(updateMask.GetPaths()) > 0 {
		_, err = r.roleBindingsClient.Update(ctx, privatev1.RoleBindingsUpdateRequest_builder{
			Object:     binding,
			UpdateMask: updateMask,
		}.Build())
	}

	return err
}

// task contains the data needed to reconcile a single role binding.
type task struct {
	r       *function
	binding *privatev1.RoleBinding
}

func (t *task) update(ctx context.Context) error {
	if t.addFinalizer() {
		return nil
	}

	t.setDefaults()

	state := t.binding.GetStatus().GetState()

	// Skip reconciliation for terminal error state
	if state == privatev1.RoleBindingState_ROLE_BINDING_STATE_FAILED {
		return nil
	}

	// For READY bindings, check if the user list has changed
	if state == privatev1.RoleBindingState_ROLE_BINDING_STATE_READY {
		return t.handleUserListChange(ctx)
	}

	// Role binding is PENDING, perform initial role assignment
	return t.syncRoleAssignments(ctx)
}

// getRoleByNameOrID fetches a role by ID or name. If the provided value is not found as an ID,
// it attempts to find the role by name.
func (t *task) getRoleByNameOrID(ctx context.Context, nameOrID string) (*privatev1.Role, error) {
	// Try fetching by ID first
	roleResponse, err := t.r.rolesClient.Get(ctx, privatev1.RolesGetRequest_builder{
		Id: nameOrID,
	}.Build())
	if err == nil {
		return roleResponse.GetObject(), nil
	}

	// If not found by ID, try listing by name
	t.r.logger.DebugContext(ctx, "Role not found by ID, trying by name",
		slog.String("name_or_id", nameOrID),
	)

	filter := fmt.Sprintf("this.metadata.name == '%s'", nameOrID)
	listResponse, err := t.r.rolesClient.List(ctx, privatev1.RolesListRequest_builder{
		Filter: &filter,
	}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to list roles by name: %w", err)
	}

	roles := listResponse.GetItems()
	if len(roles) == 0 {
		return nil, fmt.Errorf("role with name or ID '%s' not found", nameOrID)
	}
	if len(roles) > 1 {
		return nil, fmt.Errorf("multiple roles found with name '%s'", nameOrID)
	}

	return roles[0], nil
}

// handleUserListChange reconciles a READY binding when the user list changes.
// It computes the difference between desired users (spec.users) and synced users (status.synced_users),
// then adds roles to new users and removes roles from removed users.
func (t *task) handleUserListChange(ctx context.Context) error {
	desiredUsers := t.binding.GetSpec().GetUsers()
	syncedUsers := t.binding.GetStatus().GetUsers()

	// Convert to sets for efficient comparison
	desiredSet := make(map[string]bool)
	for _, u := range desiredUsers {
		desiredSet[u] = true
	}
	syncedSet := make(map[string]bool)
	for _, u := range syncedUsers {
		syncedSet[u] = true
	}

	// Find users to add (in desired but not in synced)
	var usersToAdd []string
	for _, u := range desiredUsers {
		if !syncedSet[u] {
			usersToAdd = append(usersToAdd, u)
		}
	}

	// Find users to remove (in synced but not in desired)
	var usersToRemove []string
	for _, u := range syncedUsers {
		if !desiredSet[u] {
			usersToRemove = append(usersToRemove, u)
		}
	}

	// If no changes, nothing to do
	if len(usersToAdd) == 0 && len(usersToRemove) == 0 {
		return nil
	}

	// Fetch the Role by name or ID
	role, err := t.getRoleByNameOrID(ctx, t.binding.GetSpec().GetRole())
	if err != nil {
		t.binding.GetStatus().SetState(privatev1.RoleBindingState_ROLE_BINDING_STATE_FAILED)
		t.binding.GetStatus().SetMessage(fmt.Sprintf("Failed to fetch role: %v", err))
		return nil
	}

	roleName := role.GetMetadata().GetName()
	tenantName := t.binding.GetMetadata().GetTenant()

	// Map OSAC role to Keycloak roles
	keycloakRoles, clientID := t.mapRoleToKeycloak(roleName)
	if len(keycloakRoles) == 0 {
		t.binding.GetStatus().SetState(privatev1.RoleBindingState_ROLE_BINDING_STATE_FAILED)
		t.binding.GetStatus().SetMessage(fmt.Sprintf("Role %s has no Keycloak mapping", roleName))
		return nil
	}

	// Remove roles from users that were removed from the binding
	var removalErrors []string
	for _, userID := range usersToRemove {
		// Fetch the user to get their Keycloak ID
		userResp, err := t.r.usersClient.Get(ctx, privatev1.UsersGetRequest_builder{
			Id: userID,
		}.Build())
		if err != nil {
			removalErrors = append(removalErrors, fmt.Sprintf("user %s: failed to fetch user: %v", userID, err))
			t.r.logger.ErrorContext(ctx, "Failed to fetch user for role removal",
				slog.String("role_binding_id", t.binding.GetId()),
				slog.String("user_id", userID),
				slog.Any("error", err),
			)
			continue
		}

		idpUserID := userResp.GetObject().GetStatus().GetKeycloakUserId()
		if idpUserID == "" {
			removalErrors = append(removalErrors, fmt.Sprintf("user %s: no IDP user ID", userID))
			t.r.logger.ErrorContext(ctx, "User has no IDP user ID",
				slog.String("role_binding_id", t.binding.GetId()),
				slog.String("user_id", userID),
			)
			continue
		}

		if clientID != "" {
			err = t.r.idpClient.RemoveClientRolesFromUser(ctx, tenantName, idpUserID, clientID, keycloakRoles)
		} else {
			err = t.r.idpClient.RemoveTenantRolesFromUser(ctx, tenantName, idpUserID, keycloakRoles)
		}

		if err != nil {
			removalErrors = append(removalErrors, fmt.Sprintf("user %s: %v", userID, err))
			t.r.logger.ErrorContext(ctx, "Failed to remove role from user",
				slog.String("role_binding_id", t.binding.GetId()),
				slog.String("user_id", userID),
				slog.String("idp_user_id", idpUserID),
				slog.String("role", roleName),
				slog.Any("error", err),
			)
		} else {
			t.r.logger.InfoContext(ctx, "Role removed from user during update",
				slog.String("role_binding_id", t.binding.GetId()),
				slog.String("user_id", userID),
				slog.String("idp_user_id", idpUserID),
				slog.String("role", roleName),
			)
		}
	}

	// Assign roles to users that were added to the binding
	var assignmentErrors []string
	for _, userID := range usersToAdd {
		// Fetch the user to get their Keycloak ID
		userResp, err := t.r.usersClient.Get(ctx, privatev1.UsersGetRequest_builder{
			Id: userID,
		}.Build())
		if err != nil {
			assignmentErrors = append(assignmentErrors, fmt.Sprintf("user %s: failed to fetch user: %v", userID, err))
			t.r.logger.ErrorContext(ctx, "Failed to fetch user for role assignment",
				slog.String("role_binding_id", t.binding.GetId()),
				slog.String("user_id", userID),
				slog.Any("error", err),
			)
			continue
		}

		idpUserID := userResp.GetObject().GetStatus().GetKeycloakUserId()
		if idpUserID == "" {
			assignmentErrors = append(assignmentErrors, fmt.Sprintf("user %s: no IDP user ID", userID))
			t.r.logger.ErrorContext(ctx, "User has no IDP user ID",
				slog.String("role_binding_id", t.binding.GetId()),
				slog.String("user_id", userID),
			)
			continue
		}

		if clientID != "" {
			err = t.r.idpClient.AssignClientRolesToUser(ctx, tenantName, idpUserID, clientID, keycloakRoles)
		} else {
			err = t.r.idpClient.AssignTenantRolesToUser(ctx, tenantName, idpUserID, keycloakRoles)
		}

		if err != nil {
			assignmentErrors = append(assignmentErrors, fmt.Sprintf("user %s: %v", userID, err))
			t.r.logger.ErrorContext(ctx, "Failed to assign role to user",
				slog.String("role_binding_id", t.binding.GetId()),
				slog.String("user_id", userID),
				slog.String("idp_user_id", idpUserID),
				slog.String("role", roleName),
				slog.Any("error", err),
			)
		}
	}

	// Update status based on results
	if len(assignmentErrors) > 0 || len(removalErrors) > 0 {
		t.binding.GetStatus().SetState(privatev1.RoleBindingState_ROLE_BINDING_STATE_FAILED)
		var errorMsg string
		if len(assignmentErrors) > 0 {
			errorMsg += fmt.Sprintf("Failed to assign role to some users: %v. ", assignmentErrors)
		}
		if len(removalErrors) > 0 {
			errorMsg += fmt.Sprintf("Failed to remove role from some users: %v", removalErrors)
		}
		t.binding.GetStatus().SetMessage(errorMsg)
	} else {
		// Update synced_users to reflect current desired state
		t.binding.GetStatus().SetUsers(desiredUsers)
		t.binding.GetStatus().SetMessage(fmt.Sprintf("Role %s synced: added %d user(s), removed %d user(s)", roleName, len(usersToAdd), len(usersToRemove)))
	}

	t.r.logger.InfoContext(ctx, "Role binding user list changed",
		slog.String("role_binding_id", t.binding.GetId()),
		slog.String("role", roleName),
		slog.Int("users_added", len(usersToAdd)),
		slog.Int("users_removed", len(usersToRemove)),
		slog.Int("assignment_errors", len(assignmentErrors)),
		slog.Int("removal_errors", len(removalErrors)),
	)

	return nil
}

// syncRoleAssignments assigns the role to all users in the binding.
func (t *task) syncRoleAssignments(ctx context.Context) error {
	// Fetch the Role by name or ID
	role, err := t.getRoleByNameOrID(ctx, t.binding.GetSpec().GetRole())
	if err != nil {
		t.binding.GetStatus().SetState(privatev1.RoleBindingState_ROLE_BINDING_STATE_FAILED)
		t.binding.GetStatus().SetMessage(fmt.Sprintf("Failed to fetch role: %v", err))
		return nil
	}

	roleName := role.GetMetadata().GetName()
	tenantName := t.binding.GetMetadata().GetTenant()

	// Map OSAC role to Keycloak roles
	keycloakRoles, clientID := t.mapRoleToKeycloak(roleName)
	if len(keycloakRoles) == 0 {
		t.binding.GetStatus().SetState(privatev1.RoleBindingState_ROLE_BINDING_STATE_FAILED)
		t.binding.GetStatus().SetMessage(fmt.Sprintf("Role %s has no Keycloak mapping", roleName))
		return nil
	}

	// Assign the roles to each user in the binding
	var assignmentErrors []string
	for _, userID := range t.binding.GetSpec().GetUsers() {
		// Fetch the user to get their Keycloak ID
		userResp, err := t.r.usersClient.Get(ctx, privatev1.UsersGetRequest_builder{
			Id: userID,
		}.Build())
		if err != nil {
			assignmentErrors = append(assignmentErrors, fmt.Sprintf("user %s: failed to fetch user: %v", userID, err))
			t.r.logger.ErrorContext(ctx, "Failed to fetch user for role assignment",
				slog.String("role_binding_id", t.binding.GetId()),
				slog.String("user_id", userID),
				slog.Any("error", err),
			)
			continue
		}

		idpUserID := userResp.GetObject().GetStatus().GetKeycloakUserId()
		if idpUserID == "" {
			assignmentErrors = append(assignmentErrors, fmt.Sprintf("user %s: no IDP user ID", userID))
			t.r.logger.ErrorContext(ctx, "User has no IDP user ID",
				slog.String("role_binding_id", t.binding.GetId()),
				slog.String("user_id", userID),
			)
			continue
		}

		if clientID != "" {
			// Client-level role (e.g., realm-management)
			err = t.r.idpClient.AssignClientRolesToUser(ctx, tenantName, idpUserID, clientID, keycloakRoles)
		} else {
			// Tenant-level role
			err = t.r.idpClient.AssignTenantRolesToUser(ctx, tenantName, idpUserID, keycloakRoles)
		}

		if err != nil {
			assignmentErrors = append(assignmentErrors, fmt.Sprintf("user %s: %v", userID, err))
			t.r.logger.ErrorContext(ctx, "Failed to assign role to user",
				slog.String("role_binding_id", t.binding.GetId()),
				slog.String("user_id", userID),
				slog.String("idp_user_id", idpUserID),
				slog.String("role", roleName),
				slog.Any("error", err),
			)
		}
	}

	// Update status based on assignment results
	if len(assignmentErrors) > 0 {
		t.binding.GetStatus().SetState(privatev1.RoleBindingState_ROLE_BINDING_STATE_FAILED)
		t.binding.GetStatus().SetMessage(fmt.Sprintf("Failed to assign role to some users: %v", assignmentErrors))
	} else {
		t.binding.GetStatus().SetState(privatev1.RoleBindingState_ROLE_BINDING_STATE_READY)
		t.binding.GetStatus().SetMessage(fmt.Sprintf("Role %s assigned to %d user(s)", roleName, len(t.binding.GetSpec().GetUsers())))
		// Track which users have been synced so we can detect changes later
		t.binding.GetStatus().SetUsers(t.binding.GetSpec().GetUsers())
	}

	t.r.logger.InfoContext(ctx, "Role binding synced",
		slog.String("role_binding_id", t.binding.GetId()),
		slog.String("role", roleName),
		slog.Int("users", len(t.binding.GetSpec().GetUsers())),
		slog.Int("errors", len(assignmentErrors)),
	)

	return nil
}

// mapRoleToKeycloak maps OSAC role names to Keycloak realm-level roles.
// All OSAC roles map 1:1 to Keycloak realm roles with the same name.
// Returns the list of Keycloak roles and an empty client ID (realm-level roles are not client-scoped).
func (t *task) mapRoleToKeycloak(roleName string) ([]*idp.Role, string) {
	return []*idp.Role{
		{Name: roleName, ClientRole: false},
	}, ""
}

func (t *task) delete(ctx context.Context) error {
	// Skip if not in ready state (roles not assigned yet)
	if t.binding.GetStatus().GetState() != privatev1.RoleBindingState_ROLE_BINDING_STATE_READY {
		t.removeFinalizer()
		return nil
	}

	// Fetch the Role by name or ID
	role, err := t.getRoleByNameOrID(ctx, t.binding.GetSpec().GetRole())
	if err != nil {
		t.r.logger.ErrorContext(ctx, "Failed to fetch role for deletion",
			slog.String("role_binding_id", t.binding.GetId()),
			slog.Any("error", err),
		)
		// Don't block deletion even if we can't fetch the role
		t.removeFinalizer()
		return nil
	}

	roleName := role.GetMetadata().GetName()
	tenantName := t.binding.GetMetadata().GetTenant()

	// Map OSAC role to Keycloak roles
	keycloakRoles, clientID := t.mapRoleToKeycloak(roleName)
	if len(keycloakRoles) == 0 {
		t.r.logger.WarnContext(ctx, "No Keycloak mapping for role during deletion",
			slog.String("role_binding_id", t.binding.GetId()),
			slog.String("role", roleName),
		)
		t.removeFinalizer()
		return nil
	}

	// Remove the roles from each user in the binding
	for _, userID := range t.binding.GetSpec().GetUsers() {
		// Fetch the user to get their Keycloak ID
		userResp, err := t.r.usersClient.Get(ctx, privatev1.UsersGetRequest_builder{
			Id: userID,
		}.Build())
		if err != nil {
			t.r.logger.ErrorContext(ctx, "Failed to fetch user for role removal during delete",
				slog.String("role_binding_id", t.binding.GetId()),
				slog.String("user_id", userID),
				slog.Any("error", err),
			)
			continue
		}

		idpUserID := userResp.GetObject().GetStatus().GetKeycloakUserId()
		if idpUserID == "" {
			t.r.logger.WarnContext(ctx, "User has no IDP user ID during delete",
				slog.String("role_binding_id", t.binding.GetId()),
				slog.String("user_id", userID),
			)
			continue
		}

		if clientID != "" {
			// Client-level role (e.g., realm-management)
			err = t.r.idpClient.RemoveClientRolesFromUser(ctx, tenantName, idpUserID, clientID, keycloakRoles)
		} else {
			// Tenant-level role
			err = t.r.idpClient.RemoveTenantRolesFromUser(ctx, tenantName, idpUserID, keycloakRoles)
		}

		if err != nil {
			t.r.logger.ErrorContext(ctx, "Failed to remove role from user",
				slog.String("role_binding_id", t.binding.GetId()),
				slog.String("user_id", userID),
				slog.String("idp_user_id", idpUserID),
				slog.String("role", roleName),
				slog.Any("error", err),
			)
			// Continue removing from other users even if one fails
		} else {
			t.r.logger.InfoContext(ctx, "Role removed from user",
				slog.String("role_binding_id", t.binding.GetId()),
				slog.String("user_id", userID),
				slog.String("idp_user_id", idpUserID),
				slog.String("role", roleName),
			)
		}
	}

	t.removeFinalizer()
	return nil
}

func (t *task) setDefaults() {
	if !t.binding.HasStatus() {
		t.binding.SetStatus(&privatev1.RoleBindingStatus{})
	}
	if t.binding.GetStatus().GetState() == privatev1.RoleBindingState_ROLE_BINDING_STATE_UNSPECIFIED {
		t.binding.GetStatus().SetState(privatev1.RoleBindingState_ROLE_BINDING_STATE_PENDING)
	}
}

func (t *task) addFinalizer() bool {
	if !t.binding.HasMetadata() {
		t.binding.SetMetadata(&privatev1.Metadata{})
	}
	list := t.binding.GetMetadata().GetFinalizers()
	if !slices.Contains(list, finalizers.Controller) {
		list = append(list, finalizers.Controller)
		t.binding.GetMetadata().SetFinalizers(list)
		return true
	}
	return false
}

func (t *task) removeFinalizer() {
	if !t.binding.HasMetadata() {
		return
	}
	list := t.binding.GetMetadata().GetFinalizers()
	if slices.Contains(list, finalizers.Controller) {
		list = slices.DeleteFunc(list, func(item string) bool {
			return item == finalizers.Controller
		})
		t.binding.GetMetadata().SetFinalizers(list)
	}
}
