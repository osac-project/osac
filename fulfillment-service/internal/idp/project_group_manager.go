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
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
)

// ProjectGroupManager handles Keycloak group operations for project authorization.
type ProjectGroupManager struct {
	logger *slog.Logger
	client ClientInterface
}

// ProjectGroupManagerBuilder builds the project group manager.
type ProjectGroupManagerBuilder struct {
	logger *slog.Logger
	client ClientInterface
}

// NewProjectGroupManager creates a builder for the project group manager.
func NewProjectGroupManager() *ProjectGroupManagerBuilder {
	return &ProjectGroupManagerBuilder{}
}

// SetLogger sets the logger.
func (b *ProjectGroupManagerBuilder) SetLogger(value *slog.Logger) *ProjectGroupManagerBuilder {
	b.logger = value
	return b
}

// SetClient sets the Keycloak client.
func (b *ProjectGroupManagerBuilder) SetClient(value ClientInterface) *ProjectGroupManagerBuilder {
	b.client = value
	return b
}

// Build creates the manager.
func (b *ProjectGroupManagerBuilder) Build() (result *ProjectGroupManager, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.client == nil {
		err = errors.New("client is mandatory")
		return
	}

	result = &ProjectGroupManager{
		logger: b.logger,
		client: b.client,
	}
	return
}

// DeleteProjectGroups deletes tenant authorization groups for a project.
func (m *ProjectGroupManager) DeleteProjectGroups(ctx context.Context, tenant, projectName string) error {
	if tenant == "" {
		return fmt.Errorf("tenant is required")
	}
	// Validate inputs to prevent path traversal attacks
	if strings.Contains(projectName, "..") {
		return fmt.Errorf("project name cannot contain '..' sequence")
	}
	if strings.Contains(projectName, "/") {
		return fmt.Errorf("project name cannot contain '/' character")
	}

	// Delete the parent project group, which will cascade delete the system:viewers and system:managers subgroups
	projectGroupPath := fmt.Sprintf("/%s", projectName)

	projectGroupID, err := m.getGroupIDByPath(ctx, tenant, projectGroupPath)
	if err != nil {
		// Only swallow "not found" errors - propagate other errors (network, auth, etc.) for retry
		if strings.Contains(err.Error(), "organization group not found") {
			m.logger.WarnContext(ctx, "Project group not found, skipping deletion",
				slog.String("group_path", projectGroupPath),
				slog.String("tenant", tenant),
			)
			return nil
		}
		m.logger.ErrorContext(ctx, "Failed to get project group ID",
			slog.String("group_path", projectGroupPath),
			slog.String("tenant", tenant),
			slog.Any("error", err),
		)
		return fmt.Errorf("failed to get project group ID: %w", err)
	}

	if err = m.client.DeleteGroup(ctx, tenant, projectGroupID); err != nil {
		m.logger.ErrorContext(ctx, "Failed to delete project group",
			slog.String("group_id", projectGroupID),
			slog.String("group_path", projectGroupPath),
			slog.Any("error", err),
		)
		return fmt.Errorf("failed to delete project group %s: %w", projectGroupPath, err)
	}

	m.logger.InfoContext(ctx, "Deleted project group and subgroups",
		slog.String("group_path", projectGroupPath),
		slog.String("project_name", projectName),
		slog.String("tenant", tenant),
	)

	return nil
}

// getGroupIDByPath is a helper to get the group ID from a group path.
func (m *ProjectGroupManager) getGroupIDByPath(ctx context.Context, tenantName, groupPath string) (string, error) {
	return m.client.GetGroupIDByPath(ctx, tenantName, groupPath)
}

// CreateProjectGroups creates Keycloak tenant groups for a project.
// Creates hierarchical groups: /{project-name}/system:viewers and /{project-name}/system:managers
// These groups are used by Authorino OPA policies for authorization.
// Returns the managers group ID for immediate use (avoids timing issues with group lookup).
func (m *ProjectGroupManager) CreateProjectGroups(ctx context.Context, tenant, projectPath string) (string, error) {
	if tenant == "" {
		return "", fmt.Errorf("tenant is required")
	}
	// Validate inputs to prevent path traversal attacks
	if strings.Contains(projectPath, "..") {
		return "", fmt.Errorf("project path cannot contain '..' sequence")
	}

	m.logger.DebugContext(ctx, "Creating project groups",
		slog.String("tenant", tenant),
		slog.String("project_path", projectPath),
	)

	// Make sure the project path starts with a slash:
	if !strings.HasPrefix(projectPath, "/") {
		projectPath = fmt.Sprintf("/%s", projectPath)
	}

	// Create the viewers group:
	viewersGroupPath := path.Join(projectPath, GroupNameViewers)
	viewersGroupID, err := m.client.CreateGroup(ctx, tenant, viewersGroupPath)
	if err != nil {
		return "", fmt.Errorf("failed to create viewers group: %w", err)
	}
	m.logger.InfoContext(
		ctx,
		"Created project viewers group",
		slog.String("group_path", viewersGroupPath),
		slog.String("group_id", viewersGroupID),
		slog.String("project_path", projectPath),
		slog.String("tenant", tenant),
	)

	// Create the managers group:
	managersGroupPath := path.Join(projectPath, GroupNameManagers)
	managersGroupID, err := m.client.CreateGroup(ctx, tenant, managersGroupPath)
	if err != nil {
		// Clean up viewers group on failure
		if cleanupErr := m.client.DeleteGroup(ctx, tenant, viewersGroupID); cleanupErr != nil {
			m.logger.ErrorContext(
				ctx,
				"Failed to cleanup viewers group during rollback",
				slog.String("group_id", viewersGroupID),
				slog.Any("cleanup_error", cleanupErr),
			)
			return "", fmt.Errorf("failed to create managers group: %w (rollback also failed: %w)", err, cleanupErr)
		}
		return "", fmt.Errorf("failed to create managers group: %w", err)
	}
	m.logger.InfoContext(
		ctx,
		"Created project managers group",
		slog.String("group_path", managersGroupPath),
		slog.String("group_id", managersGroupID),
		slog.String("project_path", projectPath),
		slog.String("tenant", tenant),
	)

	return managersGroupID, nil
}

// AddUserToProjectGroup adds a user to a project group (system:viewers or system:managers).
func (m *ProjectGroupManager) AddUserToProjectGroup(ctx context.Context, tenant, projectPath, username, groupType string) error {
	if tenant == "" {
		return fmt.Errorf("tenant is required")
	}
	if projectPath == "" {
		return fmt.Errorf("project name is required")
	}
	if username == "" {
		return fmt.Errorf("username is required")
	}
	if groupType != GroupNameViewers && groupType != GroupNameManagers {
		return fmt.Errorf("invalid group type %q, must be %q or %q", groupType, GroupNameViewers, GroupNameManagers)
	}
	// Validate inputs to prevent path traversal attacks
	if strings.Contains(projectPath, "..") {
		return fmt.Errorf("project name cannot contain '..' sequence")
	}

	groupPath := fmt.Sprintf("/%s/%s", projectPath, groupType)
	groupID, err := m.getGroupIDByPath(ctx, tenant, groupPath)
	if err != nil {
		return fmt.Errorf("failed to get group ID for %s: %w", groupPath, err)
	}

	if err = m.client.AddUserToGroup(ctx, tenant, username, groupID); err != nil {
		return fmt.Errorf("failed to add user to group %s: %w", groupPath, err)
	}

	m.logger.InfoContext(ctx, "Added user to project group",
		slog.String("group_path", groupPath),
		slog.String("group_type", groupType),
		slog.String("!username", username),
		slog.String("project_path", projectPath),
		slog.String("tenant", tenant),
	)

	return nil
}

// AddUserToGroupByID adds a user to a group using the group ID directly.
// This avoids timing issues with group lookup for recently created groups.
func (m *ProjectGroupManager) AddUserToGroupByID(ctx context.Context, tenant, username, groupID string) error {
	if tenant == "" {
		return fmt.Errorf("tenant is required")
	}
	if username == "" {
		return fmt.Errorf("username is required")
	}
	if groupID == "" {
		return fmt.Errorf("group ID is required")
	}

	if err := m.client.AddUserToGroup(ctx, tenant, username, groupID); err != nil {
		return fmt.Errorf("failed to add user to group: %w", err)
	}

	m.logger.InfoContext(ctx, "Added user to group",
		slog.String("group_id", groupID),
		slog.String("!username", username),
		slog.String("tenant", tenant),
	)

	return nil
}

// RemoveUserFromProjectGroup removes a user from a project group (system:viewers or system:managers).
func (m *ProjectGroupManager) RemoveUserFromProjectGroup(ctx context.Context, tenant, projectPath, username, groupType string) error {
	if tenant == "" {
		return fmt.Errorf("tenant is required")
	}
	if projectPath == "" {
		return fmt.Errorf("project name is required")
	}
	if username == "" {
		return fmt.Errorf("username is required")
	}
	if groupType != GroupNameViewers && groupType != GroupNameManagers {
		return fmt.Errorf("invalid group type %q, must be %q or %q", groupType, GroupNameViewers, GroupNameManagers)
	}
	// Validate inputs to prevent path traversal attacks
	if strings.Contains(projectPath, "..") {
		return fmt.Errorf("project name cannot contain '..' sequence")
	}

	groupPath := fmt.Sprintf("/%s/%s", projectPath, groupType)
	groupID, err := m.getGroupIDByPath(ctx, tenant, groupPath)
	if err != nil {
		return fmt.Errorf("failed to get group ID for %s: %w", groupPath, err)
	}

	if err = m.client.RemoveUserFromGroup(ctx, tenant, username, groupID); err != nil {
		return fmt.Errorf("failed to remove user from group %s: %w", groupPath, err)
	}

	m.logger.InfoContext(ctx, "Removed user from project group",
		slog.String("group_path", groupPath),
		slog.String("group_type", groupType),
		slog.String("!username", username),
		slog.String("project_path", projectPath),
		slog.String("tenant", tenant),
	)

	return nil
}
