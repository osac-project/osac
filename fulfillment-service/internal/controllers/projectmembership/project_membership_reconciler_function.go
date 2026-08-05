/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

//go:generate mockgen -source=../../api/osac/private/v1/project_memberships_service_grpc.pb.go -destination=project_memberships_client_mock.go -package=projectmembership ProjectMembershipsClient
//go:generate mockgen -source=../../api/osac/private/v1/projects_service_grpc.pb.go -destination=projects_client_mock.go -package=projectmembership ProjectsClient
//go:generate mockgen -source=../../api/osac/private/v1/users_service_grpc.pb.go -destination=users_client_mock.go -package=projectmembership UsersClient

package projectmembership

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

// SetIdpClient sets the IDP client that the reconciler will use to manage project membership roles.
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
		logger:                   b.logger,
		projectMembershipsClient: privatev1.NewProjectMembershipsClient(b.connection),
		projectsClient:           privatev1.NewProjectsClient(b.connection),
		usersClient:              privatev1.NewUsersClient(b.connection),
		idpClient:                b.idpClient,
		maskCalculator:           masks.NewCalculator().Build(),
	}
	return
}

// function is the implementation of the reconciler function.
type function struct {
	logger                   *slog.Logger
	projectMembershipsClient privatev1.ProjectMembershipsClient
	projectsClient           privatev1.ProjectsClient
	usersClient              privatev1.UsersClient
	idpClient                idp.ClientInterface
	maskCalculator           *masks.Calculator
}

// Run executes the reconciliation logic for the given project membership.
func (r *function) Run(ctx context.Context, membership *privatev1.ProjectMembership) error {
	oldMembership := proto.Clone(membership).(*privatev1.ProjectMembership)

	task := &task{
		r:          r,
		membership: membership,
	}

	var err error
	if membership.HasMetadata() && membership.GetMetadata().HasDeletionTimestamp() {
		err = task.delete(ctx)
	} else {
		err = task.update(ctx)
	}
	if err != nil {
		return err
	}

	updateMask := r.maskCalculator.Calculate(oldMembership, membership)

	if len(updateMask.GetPaths()) > 0 {
		_, err = r.projectMembershipsClient.Update(ctx, privatev1.ProjectMembershipsUpdateRequest_builder{
			Object:     membership,
			UpdateMask: updateMask,
		}.Build())
	}

	return err
}

// task contains the data needed to reconcile a single project membership.
type task struct {
	r          *function
	membership *privatev1.ProjectMembership
}

func (t *task) update(ctx context.Context) error {
	if t.addFinalizer() {
		return nil
	}

	t.setDefaults()

	state := t.membership.GetStatus().GetState()

	// For READY memberships, detect user list changes and sync accordingly
	if state == privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY {
		return t.handleUserListChange(ctx, nil)
	}

	var existingProject *privatev1.Project

	// For FAILED memberships, check if the failure is terminal before retrying.
	// Terminal failures (deleted project, invalid role, missing tenant) will not
	// resolve on their own, so we skip the retry to avoid unnecessary API calls.
	if state == privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED {
		terminal, reason, project := t.isTerminalFailure(ctx)
		if terminal {
			t.r.logger.DebugContext(ctx, "Skipping retry for terminal failure",
				slog.String("!reason", reason),
			)
			t.membership.GetStatus().SetMessage(reason)
			return nil
		}
		existingProject = project
		if len(t.membership.GetStatus().GetUsers()) > 0 {
			t.membership.GetStatus().SetState(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY)
			t.membership.GetStatus().SetMessage("")
			return t.handleUserListChange(ctx, existingProject)
		}
		t.membership.GetStatus().SetState(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_PENDING)
		t.membership.GetStatus().SetMessage("")
	}

	// Project membership is PENDING, perform initial synchronization
	return t.syncProjectMembership(ctx, existingProject)
}

// getProjectByNameOrID fetches a project by ID or name. If the provided value is not found as an ID,
// it attempts to find the project by name.
func (t *task) getProjectByNameOrID(ctx context.Context, nameOrID string) (*privatev1.Project, error) {
	// Try fetching by ID first
	projectResponse, err := t.r.projectsClient.Get(ctx, privatev1.ProjectsGetRequest_builder{
		Id: nameOrID,
	}.Build())
	if err == nil {
		return projectResponse.GetObject(), nil
	}

	// Only retry by name if the error was NotFound
	if status.Code(err) != codes.NotFound {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}

	// If not found by ID, try listing by name
	t.r.logger.DebugContext(ctx, "Project not found by ID, trying by name",
		slog.String("!name_or_id", nameOrID),
	)

	// Escape single quotes in the name to prevent CEL injection
	escapedName := strings.ReplaceAll(nameOrID, "'", "\\'")
	filter := fmt.Sprintf("this.metadata.name == '%s'", escapedName)
	listResponse, err := t.r.projectsClient.List(ctx, privatev1.ProjectsListRequest_builder{
		Filter: &filter,
	}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to list projects by name: %w", err)
	}

	projects := listResponse.GetItems()
	if len(projects) == 0 {
		return nil, status.Errorf(codes.NotFound, "project with name or ID '%s' not found", nameOrID)
	}
	if len(projects) > 1 {
		return nil, status.Errorf(codes.FailedPrecondition, "multiple projects found with name '%s'", nameOrID)
	}

	return projects[0], nil
}

func (t *task) addFinalizer() bool {
	metadata := t.membership.GetMetadata()
	finalizerName := finalizers.ProjectMembershipFinalizer
	for _, f := range metadata.GetFinalizers() {
		if f == finalizerName {
			return false
		}
	}
	metadata.SetFinalizers(append(metadata.GetFinalizers(), finalizerName))
	return true
}

func (t *task) setDefaults() {
	if !t.membership.HasStatus() {
		t.membership.SetStatus(privatev1.ProjectMembershipStatus_builder{}.Build())
	}
	status := t.membership.GetStatus()
	if status.GetState() == privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_UNSPECIFIED {
		status.SetState(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_PENDING)
	}
}

// isTerminalFailure validates preconditions that must hold for a retry to
// succeed. Returns true with a human-readable reason when the failure cannot
// resolve on its own (e.g. the project was deleted).
func (t *task) isTerminalFailure(ctx context.Context) (bool, string, *privatev1.Project) {
	projectNameOrID := t.membership.GetMetadata().GetProject()
	if projectNameOrID == "" {
		return true, "Project reference is required", nil
	}

	role := t.membership.GetSpec().GetRole()
	if t.mapRoleToGroupSuffix(role) == "" {
		return true, fmt.Sprintf("Unknown project membership role: %v", role), nil
	}

	project, err := t.getProjectByNameOrID(ctx, projectNameOrID)
	if err != nil {
		code := status.Code(err)
		if code == codes.NotFound || code == codes.FailedPrecondition {
			return true, fmt.Sprintf("Project '%s' no longer exists", projectNameOrID), nil
		}
		return false, "", nil
	}

	if project.GetMetadata().HasDeletionTimestamp() {
		return true, fmt.Sprintf("Project '%s' is being deleted", projectNameOrID), nil
	}

	if project.GetMetadata().GetTenant() == "" {
		return true, "Project has no organization tenant", nil
	}

	return false, "", project
}

func (t *task) syncProjectMembership(ctx context.Context, existingProject *privatev1.Project) error {
	users := t.membership.GetSpec().GetUsers()
	if len(users) == 0 {
		t.membership.GetStatus().SetState(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY)
		t.membership.GetStatus().SetUsers(nil)
		t.membership.GetStatus().SetMessage("")
		return nil
	}

	_, organizationName, groupID, err := t.resolveProjectGroup(ctx, existingProject)
	if err != nil {
		return nil
	}

	var successfulUsers []string
	var assignmentErrors []string
	for _, userID := range users {
		if err := t.addUserToGroup(ctx, userID, organizationName, groupID); err != nil {
			assignmentErrors = append(assignmentErrors, fmt.Sprintf("user %s: %v", userID, err))
		} else {
			successfulUsers = append(successfulUsers, userID)
		}
	}

	if len(assignmentErrors) > 0 {
		t.membership.GetStatus().SetState(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED)
		t.membership.GetStatus().SetUsers(successfulUsers)
		t.membership.GetStatus().SetMessage(fmt.Sprintf(
			"Failed to sync %d user(s): %s", len(assignmentErrors), strings.Join(assignmentErrors, "; "),
		))
		return nil
	}

	t.membership.GetStatus().SetState(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY)
	t.membership.GetStatus().SetUsers(users)
	t.membership.GetStatus().SetMessage("")
	return nil
}

func (t *task) handleUserListChange(ctx context.Context, existingProject *privatev1.Project) error {
	desiredUsers := t.membership.GetSpec().GetUsers()
	syncedUsers := t.membership.GetStatus().GetUsers()

	desiredSet := make(map[string]bool)
	for _, u := range desiredUsers {
		desiredSet[u] = true
	}
	syncedSet := make(map[string]bool)
	for _, u := range syncedUsers {
		syncedSet[u] = true
	}

	var usersToAdd []string
	for _, u := range desiredUsers {
		if !syncedSet[u] {
			usersToAdd = append(usersToAdd, u)
		}
	}

	var usersToRemove []string
	for _, u := range syncedUsers {
		if !desiredSet[u] {
			usersToRemove = append(usersToRemove, u)
		}
	}

	if len(usersToAdd) == 0 && len(usersToRemove) == 0 {
		return nil
	}

	_, organizationName, groupID, err := t.resolveProjectGroup(ctx, existingProject)
	if err != nil {
		return nil
	}

	// Track the actual synced user set so partial progress is preserved on failure.
	actualUsers := make(map[string]bool)
	for _, u := range syncedUsers {
		actualUsers[u] = true
	}

	var syncErrors []string

	for _, userID := range usersToRemove {
		if err := t.removeUserFromGroup(ctx, userID, organizationName, groupID); err != nil {
			syncErrors = append(syncErrors, fmt.Sprintf("remove user %s: %v", userID, err))
		} else {
			delete(actualUsers, userID)
		}
	}

	for _, userID := range usersToAdd {
		if err := t.addUserToGroup(ctx, userID, organizationName, groupID); err != nil {
			syncErrors = append(syncErrors, fmt.Sprintf("add user %s: %v", userID, err))
		} else {
			actualUsers[userID] = true
		}
	}

	if len(syncErrors) > 0 {
		currentUsers := make([]string, 0, len(actualUsers))
		for u := range actualUsers {
			currentUsers = append(currentUsers, u)
		}
		sort.Strings(currentUsers)
		t.membership.GetStatus().SetState(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED)
		t.membership.GetStatus().SetUsers(currentUsers)
		t.membership.GetStatus().SetMessage(fmt.Sprintf(
			"Failed to sync user changes: %s", strings.Join(syncErrors, "; "),
		))
		return nil
	}

	t.membership.GetStatus().SetUsers(desiredUsers)
	t.membership.GetStatus().SetMessage(fmt.Sprintf(
		"Synced: added %d user(s), removed %d user(s)", len(usersToAdd), len(usersToRemove),
	))
	return nil
}

// resolveProjectGroup resolves the project, organization, and group ID for the membership.
// On failure, it sets the membership status to FAILED and returns a non-nil error.
func (t *task) resolveProjectGroup(ctx context.Context, existingProject *privatev1.Project) (
	project *privatev1.Project, organizationName string, groupID string, err error,
) {
	if existingProject != nil {
		project = existingProject
	} else {
		projectNameOrID := t.membership.GetMetadata().GetProject()
		if projectNameOrID == "" {
			t.membership.GetStatus().SetState(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED)
			t.membership.GetStatus().SetMessage("Project is required")
			err = fmt.Errorf("project is required")
			return
		}

		project, err = t.getProjectByNameOrID(ctx, projectNameOrID)
		if err != nil {
			t.membership.GetStatus().SetState(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED)
			t.membership.GetStatus().SetMessage(fmt.Sprintf("Failed to fetch project: %v", err))
			return
		}
	}

	role := t.membership.GetSpec().GetRole()
	groupSuffix := t.mapRoleToGroupSuffix(role)
	if groupSuffix == "" {
		t.membership.GetStatus().SetState(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED)
		t.membership.GetStatus().SetMessage(fmt.Sprintf("Unknown project membership role: %v", role))
		err = fmt.Errorf("unknown role")
		return
	}

	organizationName = project.GetMetadata().GetTenant()
	if organizationName == "" {
		t.membership.GetStatus().SetState(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED)
		t.membership.GetStatus().SetMessage("Project has no organization tenant")
		err = fmt.Errorf("no tenant")
		return
	}

	groupPath, pathErr := t.buildProjectGroupPath(ctx, project, groupSuffix)
	if pathErr != nil {
		t.membership.GetStatus().SetState(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED)
		t.membership.GetStatus().SetMessage(fmt.Sprintf("Failed to build project hierarchy path: %v", pathErr))
		err = pathErr
		return
	}

	groupID, err = t.r.idpClient.GetGroupIDByPath(ctx, organizationName, groupPath)
	if err != nil {
		t.membership.GetStatus().SetState(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED)
		t.membership.GetStatus().SetMessage(fmt.Sprintf("Failed to find authorization group %s: %v", groupPath, err))
		return
	}

	return
}

func (t *task) addUserToGroup(ctx context.Context, userID, organizationName, groupID string) error {
	userResponse, err := t.r.usersClient.Get(ctx, privatev1.UsersGetRequest_builder{
		Id: userID,
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to fetch user: %w", err)
	}
	user := userResponse.GetObject()

	idpUserID := user.GetStatus().GetKeycloakUserId()
	if idpUserID == "" {
		return fmt.Errorf("user IDP ID not yet populated")
	}

	if err := t.r.idpClient.AddUserToGroup(ctx, organizationName, idpUserID, groupID); err != nil {
		return fmt.Errorf("failed to add to group: %w", err)
	}
	return nil
}

func (t *task) removeUserFromGroup(ctx context.Context, userID, organizationName, groupID string) error {
	userResponse, err := t.r.usersClient.Get(ctx, privatev1.UsersGetRequest_builder{
		Id: userID,
	}.Build())
	if err != nil {
		if status.Code(err) == codes.NotFound {
			t.r.logger.DebugContext(ctx, "User not found during removal, skipping",
				slog.String("!user_id", userID))
			return nil
		}
		return fmt.Errorf("failed to fetch user: %w", err)
	}
	user := userResponse.GetObject()

	idpUserID := user.GetStatus().GetKeycloakUserId()
	if idpUserID == "" {
		t.r.logger.DebugContext(ctx, "User has no IDP user ID during removal, skipping",
			slog.String("!user_id", userID))
		return nil
	}

	if err := t.r.idpClient.RemoveUserFromGroup(ctx, organizationName, idpUserID, groupID); err != nil {
		code := status.Code(err)
		if code == codes.NotFound || code == codes.AlreadyExists {
			return nil
		}
		return fmt.Errorf("failed to remove from group: %w", err)
	}
	return nil
}

func (t *task) mapRoleToGroupSuffix(role privatev1.ProjectMembershipRole) string {
	switch role {
	case privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_VIEWER:
		return "system:viewers"
	case privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER:
		return "system:managers"
	default:
		return ""
	}
}

const (
	// MaxProjectHierarchyDepth is the maximum allowed depth of project nesting.
	// This prevents infinite loops in case of circular references in the project hierarchy.
	MaxProjectHierarchyDepth = 100
)

// buildProjectGroupPath constructs the full hierarchical authorization group path for a project.
// For a nested project like project1 -> project2 -> project3 with role "managers",
// this returns "/project1/project2/project3/managers".
func (t *task) buildProjectGroupPath(ctx context.Context, project *privatev1.Project, groupSuffix string) (string, error) {
	// Build the path by traversing up the parent chain
	var pathParts []string
	currentProject := project

	for i := 0; i < MaxProjectHierarchyDepth; i++ {
		// Prepend the current project name
		projectName := currentProject.GetMetadata().GetName()
		pathParts = append([]string{projectName}, pathParts...)

		// Check if there's a parent
		if currentProject.GetMetadata().GetProject() == "" {
			break
		}

		// Fetch the parent project
		parentNameOrID := currentProject.GetMetadata().GetProject()
		var err error
		currentProject, err = t.getProjectByNameOrID(ctx, parentNameOrID)
		if err != nil {
			return "", fmt.Errorf("failed to fetch parent project %s: %w", parentNameOrID, err)
		}
	}

	if len(pathParts) >= MaxProjectHierarchyDepth {
		return "", fmt.Errorf("project hierarchy exceeded maximum depth of %d (possible circular reference)", MaxProjectHierarchyDepth)
	}

	// Add the role suffix at the end
	pathParts = append(pathParts, groupSuffix)

	// Construct the full path with leading slash
	// For example: pathParts = ["project1", "project2", "project3", "managers"]
	// Result: "/project1/project2/project3/managers"
	return "/" + joinPath(pathParts...), nil
}

// joinPath joins path components with "/" separator
func joinPath(parts ...string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += "/" + parts[i]
	}
	return result
}

func (t *task) delete(ctx context.Context) error {
	metadata := t.membership.GetMetadata()
	finalizerName := finalizers.ProjectMembershipFinalizer

	currentFinalizers := metadata.GetFinalizers()
	if len(currentFinalizers) == 0 {
		return nil
	}

	var updated []string
	for _, f := range currentFinalizers {
		if f != finalizerName {
			updated = append(updated, f)
		}
	}

	// If we're removing the finalizer, clean up the IDP assignment first
	if len(updated) < len(currentFinalizers) {
		if err := t.cleanupProjectMembership(ctx); err != nil {
			return err
		}
	}

	metadata.SetFinalizers(updated)
	return nil
}

func (t *task) cleanupProjectMembership(ctx context.Context) error {
	users := t.membership.GetStatus().GetUsers()
	if len(users) == 0 {
		users = t.membership.GetSpec().GetUsers()
	}
	if len(users) == 0 {
		return nil
	}

	projectNameOrID := t.membership.GetMetadata().GetProject()
	if projectNameOrID == "" {
		return nil
	}

	project, err := t.getProjectByNameOrID(ctx, projectNameOrID)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			t.r.logger.DebugContext(ctx, "Project not found during cleanup, skipping")
			return nil
		}
		return fmt.Errorf("failed to fetch project during cleanup: %w", err)
	}

	role := t.membership.GetSpec().GetRole()
	groupSuffix := t.mapRoleToGroupSuffix(role)
	if groupSuffix == "" {
		return nil
	}

	organizationName := project.GetMetadata().GetTenant()

	groupPath, err := t.buildProjectGroupPath(ctx, project, groupSuffix)
	if err != nil {
		return fmt.Errorf("failed to build project hierarchy path during cleanup: %w", err)
	}

	groupID, err := t.r.idpClient.GetGroupIDByPath(ctx, organizationName, groupPath)
	if err != nil {
		if status.Code(err) == codes.NotFound || strings.Contains(err.Error(), "not found") {
			t.r.logger.DebugContext(ctx, "Authorization group not found during cleanup, skipping",
				slog.String("group_path", groupPath))
			return nil
		}
		return fmt.Errorf("failed to find authorization group during cleanup: %w", err)
	}

	for _, userID := range users {
		if err := t.removeUserFromGroup(ctx, userID, organizationName, groupID); err != nil {
			return fmt.Errorf("failed to remove user %s during cleanup: %w", userID, err)
		}
	}

	return nil
}
