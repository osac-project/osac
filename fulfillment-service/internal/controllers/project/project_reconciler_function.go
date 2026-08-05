/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

//go:generate mockgen -source=../../api/osac/private/v1/projects_service_grpc.pb.go -destination=projects_client_mock.go -package=project ProjectsClient
//go:generate mockgen -source=../../api/osac/private/v1/tenants_service_grpc.pb.go -destination=tenants_client_mock.go -package=project TenantsClient
//go:generate mockgen -source=../../api/osac/private/v1/users_service_grpc.pb.go -destination=users_client_mock.go -package=project UsersClient
//go:generate mockgen -source=../../api/osac/private/v1/project_memberships_service_grpc.pb.go -destination=project_memberships_client_mock.go -package=project ProjectMembershipsClient

package project

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
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
	logger              *slog.Logger
	connection          *grpc.ClientConn
	projectGroupManager *idp.ProjectGroupManager
	usersClient         privatev1.UsersClient
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

// SetProjectGroupManager sets the project group manager that the reconciler will use to manage project authorization groups.
func (b *FunctionBuilder) SetProjectGroupManager(value *idp.ProjectGroupManager) *FunctionBuilder {
	b.projectGroupManager = value
	return b
}

// SetUsersClient sets the users client that the reconciler will use to look up user information.
func (b *FunctionBuilder) SetUsersClient(value privatev1.UsersClient) *FunctionBuilder {
	b.usersClient = value
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
	if b.projectGroupManager == nil {
		err = errors.New("project group manager is mandatory")
		return
	}

	usersClient := b.usersClient
	if usersClient == nil {
		usersClient = privatev1.NewUsersClient(b.connection)
	}

	result = &function{
		logger:                   b.logger,
		projectsClient:           privatev1.NewProjectsClient(b.connection),
		tenantsClient:            privatev1.NewTenantsClient(b.connection),
		usersClient:              usersClient,
		projectMembershipsClient: privatev1.NewProjectMembershipsClient(b.connection),
		projectGroupManager:      b.projectGroupManager,
		maskCalculator:           masks.NewCalculator().Build(),
	}
	return
}

// function is the implementation of the reconciler function.
type function struct {
	logger                   *slog.Logger
	projectsClient           privatev1.ProjectsClient
	tenantsClient            privatev1.TenantsClient
	usersClient              privatev1.UsersClient
	projectMembershipsClient privatev1.ProjectMembershipsClient
	projectGroupManager      *idp.ProjectGroupManager
	maskCalculator           *masks.Calculator
}

// Run executes the reconciliation logic for the given project.
func (r *function) Run(ctx context.Context, project *privatev1.Project) error {
	oldProject := proto.Clone(project).(*privatev1.Project)

	task := &task{
		r:       r,
		project: project,
	}

	var err error
	if project.HasMetadata() && project.GetMetadata().HasDeletionTimestamp() {
		err = task.delete(ctx)
	} else {
		err = task.update(ctx)
	}
	if err != nil {
		return err
	}

	updateMask := r.maskCalculator.Calculate(oldProject, project)

	if len(updateMask.GetPaths()) > 0 {
		_, err = r.projectsClient.Update(ctx, privatev1.ProjectsUpdateRequest_builder{
			Object:     project,
			UpdateMask: updateMask,
		}.Build())
	}

	return err
}

// task contains the data needed to reconcile a single project.
type task struct {
	r       *function
	project *privatev1.Project
}

// update performs the reconciliation logic for creating or updating a project.
func (t *task) update(ctx context.Context) error {
	if t.addFinalizer() {
		return nil
	}

	t.setDefaults()

	state := t.project.GetStatus().GetState()

	// Skip reconciliation for terminal states to prevent infinite loops.
	if state == privatev1.ProjectState_PROJECT_STATE_FAILED ||
		state == privatev1.ProjectState_PROJECT_STATE_DELETE_FAILED {
		return nil
	}

	// For active projects, no re-validation needed (the parent project relationship, stored in
	// metadata.project, is immutable).
	if state == privatev1.ProjectState_PROJECT_STATE_ACTIVE {
		return nil
	}

	// Project is PENDING or UNSPECIFIED, perform validation
	return t.validateAndActivate(ctx)
}

// validateAndActivate validates the project hierarchy and transitions to ACTIVE or FAILED state.
func (t *task) validateAndActivate(ctx context.Context) error {
	t.project.GetStatus().SetState(privatev1.ProjectState_PROJECT_STATE_PENDING)

	// Validate parent project if specified via metadata.project
	parentName := t.project.GetMetadata().GetProject()
	if parentName != "" {
		// Find parent project by name
		parentProject, err := t.findProjectByName(ctx, parentName)
		if err != nil {
			return fmt.Errorf("failed to fetch parent project: %w", err)
		}
		if parentProject == nil {
			t.project.GetStatus().SetState(privatev1.ProjectState_PROJECT_STATE_FAILED)
			t.project.GetStatus().SetMessage(fmt.Sprintf(
				"Parent project not found: %s", parentName,
			))
			return nil
		}

		// Validate parent is in ACTIVE state
		parentState := parentProject.GetStatus().GetState()
		if parentState != privatev1.ProjectState_PROJECT_STATE_ACTIVE {
			t.project.GetStatus().SetState(privatev1.ProjectState_PROJECT_STATE_FAILED)
			t.project.GetStatus().SetMessage(fmt.Sprintf(
				"Parent project '%s' is not in ACTIVE state (current state: %s)",
				parentName, parentState,
			))
			return nil
		}
	}

	// Project names are dot-separated label sequences (for example 'parent.child') where an empty string represents
	// the tenant's default project. Keycloak expects a hierarchical path, so we convert dots to slashes to
	// produce the group path. For example, 'parent.child' becomes 'parent/child').
	projectTenant := t.project.GetMetadata().GetTenant()
	projectName := t.project.GetMetadata().GetName()
	projectPath := strings.ReplaceAll(projectName, ".", "/")

	// Create Keycloak groups for project authorization
	// Returns the system:managers group ID to avoid timing issues with group lookup
	managersGroupID, err := t.r.projectGroupManager.CreateProjectGroups(ctx, projectTenant, projectPath)
	if err != nil {
		t.updateCondition(
			privatev1.ProjectConditionType_PROJECT_CONDITION_TYPE_KEYCLOAK_SYNC,
			privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
			"GroupCreationFailed",
			fmt.Sprintf("Failed to create Keycloak groups: %v", err),
		)
		// Persist the condition update before returning the error
		updateMask := t.r.maskCalculator.Calculate(&privatev1.Project{}, t.project)
		if len(updateMask.GetPaths()) > 0 {
			if _, updateErr := t.r.projectsClient.Update(ctx, privatev1.ProjectsUpdateRequest_builder{
				Object:     t.project,
				UpdateMask: updateMask,
			}.Build()); updateErr != nil {
				// Log update error but return the original group creation error
				t.r.logger.ErrorContext(ctx, "Failed to persist Keycloak sync condition",
					slog.String("project", t.project.GetMetadata().GetName()),
					slog.Any("update_error", updateErr),
				)
			}
		}
		return fmt.Errorf("failed to create Keycloak groups: %w", err)
	}

	// Add the project creator to the system:managers group using the ID from creation
	// This avoids timing issues where the group isn't immediately visible in searches
	creatorUsername := t.project.GetMetadata().GetCreator()
	if creatorUsername != "" {
		// Look up the user to get their Keycloak user ID
		userResponse, err := t.r.usersClient.Get(ctx, privatev1.UsersGetRequest_builder{
			Id: creatorUsername,
		}.Build())
		if err != nil {
			if status.Code(err) == codes.NotFound {
				t.r.logger.WarnContext(ctx, "Creator user not found, cannot add to managers group",
					slog.String("project", t.project.GetMetadata().GetName()),
					slog.String("!creator", creatorUsername),
				)
			} else {
				t.r.logger.WarnContext(ctx, "Failed to look up creator user",
					slog.String("project", t.project.GetMetadata().GetName()),
					slog.String("!creator", creatorUsername),
					slog.Any("error", err),
				)
			}
			// Don't fail the reconciliation if this fails - the groups are still created
			// The user can be added manually later
		} else {
			user := userResponse.GetObject()
			keycloakUserID := user.GetStatus().GetKeycloakUserId()
			if keycloakUserID == "" {
				t.r.logger.WarnContext(ctx, "Creator user has no Keycloak user ID, cannot add to managers group",
					slog.String("project", t.project.GetMetadata().GetName()),
					slog.String("!creator", creatorUsername),
				)
			} else {
				if err := t.r.projectGroupManager.AddUserToGroupByID(ctx,
					t.project.GetMetadata().GetTenant(),
					keycloakUserID,
					managersGroupID); err != nil {
					t.r.logger.WarnContext(ctx, "Failed to add creator to managers group",
						slog.String("project", t.project.GetMetadata().GetName()),
						slog.String("!creator", creatorUsername),
						slog.String("managers_group_id", managersGroupID),
						slog.Any("error", err),
					)
					// Don't fail the reconciliation if this fails - the groups are still created
					// The user can be added manually later
				} else {
					t.r.logger.InfoContext(ctx, "Added creator to project managers group",
						slog.String("project", t.project.GetMetadata().GetName()),
						slog.String("!creator", creatorUsername),
						slog.String("managers_group_id", managersGroupID),
					)
				}
			}
		}
	}

	// Update condition with success
	t.updateCondition(
		privatev1.ProjectConditionType_PROJECT_CONDITION_TYPE_KEYCLOAK_SYNC,
		privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
		"GroupsCreated",
		"Keycloak groups created successfully",
	)

	// All validations passed
	t.project.GetStatus().SetState(privatev1.ProjectState_PROJECT_STATE_ACTIVE)
	t.project.GetStatus().ClearMessage()

	t.r.logger.DebugContext(ctx, "Project activated",
		slog.String("project_id", t.project.GetId()),
		slog.String("project_name", t.project.GetMetadata().GetName()),
	)

	return nil
}

// findProjectByName looks up a project by its metadata.name. Returns nil if not found.
func (t *task) findProjectByName(ctx context.Context, name string) (*privatev1.Project, error) {
	listResp, err := t.r.projectsClient.List(ctx, privatev1.ProjectsListRequest_builder{
		Filter: new(fmt.Sprintf("this.metadata.name == %q", name)),
		Limit:  new(int32(1)),
	}.Build())
	if err != nil {
		return nil, err
	}
	items := listResp.GetItems()
	if len(items) == 0 {
		return nil, nil
	}
	return items[0], nil
}

// delete performs the deletion cleanup for a project.
func (t *task) delete(ctx context.Context) error {
	// Check for child projects before deletion
	listFilter := fmt.Sprintf(
		"this.metadata.tenant == %q && this.metadata.project == %q && this.metadata.name != %q",
		t.project.GetMetadata().GetTenant(), t.project.GetMetadata().GetName(), t.project.GetMetadata().GetName(),
	)
	listResp, err := t.r.projectsClient.List(ctx, privatev1.ProjectsListRequest_builder{
		Filter: new(listFilter),
		Limit:  new(int32(0)),
	}.Build())
	if err != nil {
		// Transient error - retry later
		return fmt.Errorf("failed to query for child projects: %w", err)
	}

	// Block deletion if children exist
	if listResp.GetTotal() > 0 {
		if !t.project.HasStatus() {
			t.project.SetStatus(&privatev1.ProjectStatus{})
		}
		t.project.GetStatus().SetState(privatev1.ProjectState_PROJECT_STATE_DELETE_FAILED)
		t.project.GetStatus().SetMessage(fmt.Sprintf("Cannot delete project with %d child project(s). Delete children first.", listResp.GetTotal()))
		t.r.logger.WarnContext(ctx, "Cannot delete project with children",
			slog.String("project_id", t.project.GetId()),
			slog.Int("child_count", int(listResp.GetTotal())),
		)
		// Don't remove finalizer - deletion is blocked
		return nil
	}

	// Cascade-delete ProjectMemberships scoped to this project
	if err := t.deleteProjectMemberships(ctx); err != nil {
		return err
	}

	// Clean up Keycloak groups
	err = t.r.projectGroupManager.DeleteProjectGroups(ctx,
		t.project.GetMetadata().GetTenant(),
		t.project.GetMetadata().GetName())
	if err != nil {
		t.r.logger.ErrorContext(ctx, "Failed to delete Keycloak groups",
			slog.String("project_id", t.project.GetId()),
			slog.String("tenant", t.project.GetMetadata().GetTenant()),
			slog.String("project_name", t.project.GetMetadata().GetName()),
			slog.Any("error", err),
		)
		return fmt.Errorf("failed to delete Keycloak groups: %w", err)
	}

	t.removeFinalizer()

	// When the root project (empty name) is deleted, signal the parent tenant
	// so it re-reconciles and can proceed with its own deletion.
	if t.project.GetMetadata().GetName() == "" {
		t.signalTenant(ctx)
	}

	return nil
}

// deleteProjectMemberships lists all ProjectMemberships scoped to this project, marks each for
// deletion, and waits for them to be fully removed before returning. This prevents orphaned
// memberships and ensures the project can complete its own deletion.
func (t *task) deleteProjectMemberships(ctx context.Context) error {
	projectName := t.project.GetMetadata().GetName()
	tenant := t.project.GetMetadata().GetTenant()

	membershipFilter := fmt.Sprintf(
		"this.metadata.tenant == %q && this.metadata.project == %q",
		tenant, projectName,
	)
	listResp, err := t.r.projectMembershipsClient.List(ctx, privatev1.ProjectMembershipsListRequest_builder{
		Filter: new(membershipFilter),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to query for project memberships: %w", err)
	}

	for _, membership := range listResp.GetItems() {
		if membership.HasMetadata() && membership.GetMetadata().HasDeletionTimestamp() {
			continue
		}
		t.r.logger.InfoContext(ctx, "Cascade-deleting project membership",
			slog.String("project_id", t.project.GetId()),
			slog.String("membership_id", membership.GetId()),
		)
		_, err := t.r.projectMembershipsClient.Delete(ctx, privatev1.ProjectMembershipsDeleteRequest_builder{
			Id: membership.GetId(),
		}.Build())
		if err != nil && status.Code(err) != codes.NotFound {
			return fmt.Errorf("failed to delete project membership %s: %w", membership.GetId(), err)
		}
	}

	if listResp.GetTotal() > 0 {
		return fmt.Errorf("project still has %d project membership(s) pending deletion", listResp.GetTotal())
	}

	return nil
}

// setDefaults sets default values for the project.
func (t *task) setDefaults() {
	if !t.project.HasStatus() {
		t.project.SetStatus(&privatev1.ProjectStatus{})
	}
	if t.project.GetStatus().GetState() == privatev1.ProjectState_PROJECT_STATE_UNSPECIFIED {
		t.project.GetStatus().SetState(privatev1.ProjectState_PROJECT_STATE_PENDING)
	}
	// Initialize default conditions
	for value := range privatev1.ProjectConditionType_name {
		if value != 0 {
			t.setConditionDefaults(privatev1.ProjectConditionType(value))
		}
	}
}

// setConditionDefaults ensures a condition exists with a default state if not already present.
func (t *task) setConditionDefaults(conditionType privatev1.ProjectConditionType) {
	exists := false
	for _, current := range t.project.GetStatus().GetConditions() {
		if current.GetType() == conditionType {
			exists = true
			break
		}
	}
	if !exists {
		conditions := t.project.GetStatus().GetConditions()
		conditions = append(conditions, privatev1.ProjectCondition_builder{
			Type:   conditionType,
			Status: privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
		}.Build())
		t.project.GetStatus().SetConditions(conditions)
	}
}

// addFinalizer adds the controller finalizer to the project if not already present.
// Returns true if the finalizer was added (indicating the update should be saved immediately).
func (t *task) addFinalizer() bool {
	if !t.project.HasMetadata() {
		t.project.SetMetadata(&privatev1.Metadata{})
	}
	list := t.project.GetMetadata().GetFinalizers()
	if !slices.Contains(list, finalizers.Controller) {
		list = append(list, finalizers.Controller)
		t.project.GetMetadata().SetFinalizers(list)
		return true
	}
	return false
}

// removeFinalizer removes the controller finalizer from the project.
func (t *task) removeFinalizer() {
	if !t.project.HasMetadata() {
		return
	}
	list := t.project.GetMetadata().GetFinalizers()
	if slices.Contains(list, finalizers.Controller) {
		list = slices.DeleteFunc(list, func(item string) bool {
			return item == finalizers.Controller
		})
		t.project.GetMetadata().SetFinalizers(list)
	}
}

// signalTenant looks up the parent tenant by name and signals it so that the
// tenant reconciler re-runs. This is used when the root project is deleted to
// unblock the tenant's own deletion.
func (t *task) signalTenant(ctx context.Context) {
	tenantName := t.project.GetMetadata().GetTenant()
	listResp, err := t.r.tenantsClient.List(ctx, privatev1.TenantsListRequest_builder{
		Filter: new(fmt.Sprintf("this.metadata.name == %q", tenantName)),
		Limit:  new(int32(1)),
	}.Build())
	if err != nil {
		t.r.logger.WarnContext(ctx, "Failed to look up tenant for signaling",
			slog.String("tenant_name", tenantName),
			slog.Any("error", err),
		)
		return
	}
	items := listResp.GetItems()
	if len(items) == 0 {
		return
	}
	_, err = t.r.tenantsClient.Signal(ctx, privatev1.TenantsSignalRequest_builder{
		Id: items[0].GetId(),
	}.Build())
	if err != nil {
		t.r.logger.WarnContext(ctx, "Failed to signal tenant after root project deletion",
			slog.String("tenant_id", items[0].GetId()),
			slog.Any("error", err),
		)
	}
}

// updateCondition updates or creates a condition with the specified type, status, reason, and message.
func (t *task) updateCondition(conditionType privatev1.ProjectConditionType, status privatev1.ConditionStatus,
	reason string, message string) {
	conditions := t.project.GetStatus().GetConditions()
	updated := false
	for i, condition := range conditions {
		if condition.GetType() == conditionType {
			conditions[i] = privatev1.ProjectCondition_builder{
				Type:    conditionType,
				Status:  status,
				Reason:  &reason,
				Message: &message,
			}.Build()
			updated = true
			break
		}
	}
	if !updated {
		conditions = append(conditions, privatev1.ProjectCondition_builder{
			Type:    conditionType,
			Status:  status,
			Reason:  &reason,
			Message: &message,
		}.Build())
	}
	t.project.GetStatus().SetConditions(conditions)
}
