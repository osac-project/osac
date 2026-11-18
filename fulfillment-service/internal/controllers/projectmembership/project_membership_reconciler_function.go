/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

//go:generate mockgen -destination=project_memberships_client_mock.go -package=projectmembership github.com/osac-project/osac/proto/gen/osac/private/v1 ProjectMembershipsClient
//go:generate mockgen -destination=projects_client_mock.go -package=projectmembership github.com/osac-project/osac/proto/gen/osac/private/v1 ProjectsClient
//go:generate mockgen -destination=users_client_mock.go -package=projectmembership github.com/osac-project/osac/proto/gen/osac/private/v1 UsersClient

package projectmembership

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/idp"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
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

// Both membership targets and project parents are stored as (tenant, full project name)
// foreign keys. A project's opaque ID is not part of either reference.
func (t *task) getProjectByName(ctx context.Context, tenant, name string) (*privatev1.Project, error) {
	if tenant == "" {
		return nil, status.Error(codes.FailedPrecondition, "project reference has no organization tenant")
	}
	if name == "" {
		return nil, status.Error(codes.FailedPrecondition, "project reference is required")
	}
	filter := fmt.Sprintf("this.metadata.tenant == %q && this.metadata.name == %q", tenant, name)
	listResponse, err := t.r.projectsClient.List(ctx, privatev1.ProjectsListRequest_builder{
		Filter: &filter,
		Limit:  new(int32(2)),
	}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to list projects by name: %w", err)
	}

	projects := listResponse.GetItems()
	if len(projects) == 0 {
		return nil, status.Errorf(codes.NotFound, "project %q not found", name)
	}
	if len(projects) > 1 {
		return nil, status.Errorf(codes.FailedPrecondition, "multiple projects found with name %q", name)
	}

	project := projects[0]
	if project.GetMetadata().GetTenant() != tenant || project.GetMetadata().GetName() != name || project.GetId() == "" {
		return nil, status.Error(codes.FailedPrecondition, "project lookup did not match the stored tenant and project reference")
	}
	return project, nil
}

func (t *task) getMembershipProject(ctx context.Context) (*privatev1.Project, error) {
	metadata := t.membership.GetMetadata()
	return t.getProjectByName(ctx, metadata.GetTenant(), metadata.GetProject())
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
	projectName := t.membership.GetMetadata().GetProject()
	if projectName == "" {
		return true, "Project reference is required", nil
	}

	role := t.membership.GetSpec().GetRole()
	if t.mapRoleToGroupSuffix(role) == "" {
		return true, fmt.Sprintf("Unknown project membership role: %v", role), nil
	}

	project, err := t.getMembershipProject(ctx)
	if err != nil {
		code := status.Code(err)
		if code == codes.NotFound {
			return true, fmt.Sprintf("Project '%s' no longer exists", projectName), nil
		}
		if code == codes.FailedPrecondition {
			return true, fmt.Sprintf("Failed to fetch project: %v", err), nil
		}
		return false, "", nil
	}

	if project.GetMetadata().HasDeletionTimestamp() {
		return true, fmt.Sprintf("Project '%s' is being deleted", projectName), nil
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

	var successfulUsers []*privatev1.UserReference
	var assignmentErrors []string
	for _, userRef := range users {
		userKey := controllers.RefKeyStr(userRef)
		if err := t.addUserToGroup(ctx, userKey, organizationName, groupID); err != nil {
			assignmentErrors = append(assignmentErrors, fmt.Sprintf("user %s: %v", userKey, err))
		} else {
			successfulUsers = append(successfulUsers, userRef)
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
		desiredSet[controllers.RefKeyStr(u)] = true
	}
	syncedSet := make(map[string]bool)
	for _, u := range syncedUsers {
		syncedSet[controllers.RefKeyStr(u)] = true
	}

	var usersToAdd []string
	for _, u := range desiredUsers {
		if !syncedSet[controllers.RefKeyStr(u)] {
			usersToAdd = append(usersToAdd, controllers.RefKeyStr(u))
		}
	}

	var usersToRemove []string
	for _, u := range syncedUsers {
		if !desiredSet[controllers.RefKeyStr(u)] {
			usersToRemove = append(usersToRemove, controllers.RefKeyStr(u))
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
	actualUsers := make(map[string]*privatev1.UserReference)
	for _, u := range syncedUsers {
		actualUsers[controllers.RefKeyStr(u)] = u
	}

	var syncErrors []string

	for _, userID := range usersToRemove {
		if err := t.removeUserFromGroup(ctx, userID, organizationName, groupID); err != nil {
			syncErrors = append(syncErrors, fmt.Sprintf("remove user %s: %v", userID, err))
		} else {
			delete(actualUsers, userID)
		}
	}

	for _, u := range desiredUsers {
		key := controllers.RefKeyStr(u)
		if !syncedSet[key] {
			if err := t.addUserToGroup(ctx, key, organizationName, groupID); err != nil {
				syncErrors = append(syncErrors, fmt.Sprintf("add user %s: %v", key, err))
			} else {
				actualUsers[key] = u
			}
		}
	}

	if len(syncErrors) > 0 {
		currentUsers := make([]*privatev1.UserReference, 0, len(actualUsers))
		for _, ref := range actualUsers {
			currentUsers = append(currentUsers, ref)
		}
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
		project, err = t.getMembershipProject(ctx)
		if err != nil {
			t.membership.GetStatus().SetState(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED)
			t.membership.GetStatus().SetMessage(fmt.Sprintf("Failed to fetch project: %v", err))
			return
		}
	}
	if project.GetMetadata().HasDeletionTimestamp() {
		t.membership.GetStatus().SetState(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED)
		err = fmt.Errorf("project %q is being deleted", project.GetMetadata().GetName())
		t.membership.GetStatus().SetMessage(err.Error())
		return
	}

	role := t.membership.GetSpec().GetRole()
	groupSuffix := t.mapRoleToGroupSuffix(role)
	if groupSuffix == "" {
		t.membership.GetStatus().SetState(privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED)
		t.membership.GetStatus().SetMessage(fmt.Sprintf("Unknown project membership role: %v", role))
		err = fmt.Errorf("unknown role")
		return
	}

	organizationName = t.membership.GetMetadata().GetTenant()

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
	// This bounds the number of lookups even for malformed stored data.
	MaxProjectHierarchyDepth = 100
)

// buildProjectGroupPath constructs the full hierarchical authorization group path for a project.
// For a nested project like project1 -> project2 -> project3 with role "managers",
// this returns "/project1/project2/project3/managers".
func (t *task) buildProjectGroupPath(ctx context.Context, project *privatev1.Project, groupSuffix string) (string, error) {
	tenant := project.GetMetadata().GetTenant()
	currentProject := project

	for i := 0; i < MaxProjectHierarchyDepth; i++ {
		parentName := currentProject.GetMetadata().GetProject()
		if parentName == "" {
			// Match the group path created by the project controller: the name already
			// contains the full hierarchy, even for pre-hierarchy rows without a parent.
			return "/" + strings.ReplaceAll(project.GetMetadata().GetName(), ".", "/") + "/" + groupSuffix, nil
		}

		currentName := currentProject.GetMetadata().GetName()
		if sep := strings.LastIndexByte(currentName, '.'); sep < 0 || parentName != currentName[:sep] {
			return "", status.Error(codes.FailedPrecondition, "parent project does not match the project name prefix")
		}
		var err error
		currentProject, err = t.getProjectByName(ctx, tenant, parentName)
		if err != nil {
			return "", fmt.Errorf("failed to fetch parent project %s: %w", parentName, err)
		}
	}
	return "", fmt.Errorf("project hierarchy exceeded maximum depth of %d", MaxProjectHierarchyDepth)
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
		t.signalProject(ctx)
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

	if t.membership.GetMetadata().GetProject() == "" {
		return nil
	}

	project, err := t.getMembershipProject(ctx)
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

	organizationName := t.membership.GetMetadata().GetTenant()

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

	for _, userRef := range users {
		userKey := controllers.RefKeyStr(userRef)
		if err := t.removeUserFromGroup(ctx, userKey, organizationName, groupID); err != nil {
			return fmt.Errorf("failed to remove user %s during cleanup: %w", userKey, err)
		}
	}

	return nil
}

// signalProject looks up the membership's project and signals it so that the
// project reconciler re-runs. This is used when the project membership is deleted to
// unblock the project's own deletion.
func (t *task) signalProject(ctx context.Context) {
	projectName := t.membership.GetMetadata().GetProject()
	if projectName == "" {
		return
	}
	project, err := t.getMembershipProject(ctx)
	if err != nil {
		t.r.logger.WarnContext(ctx, "Failed to look up project for signaling",
			slog.String("project_name", projectName),
			slog.Any("error", err),
		)
		return
	}

	_, err = t.r.projectsClient.Signal(ctx, privatev1.ProjectsSignalRequest_builder{
		Id: project.GetId(),
	}.Build())
	if err != nil {
		t.r.logger.WarnContext(ctx, "Failed to signal project for membership deletion",
			slog.String("project_name", projectName),
			slog.Any("error", err),
		)
	}
}
