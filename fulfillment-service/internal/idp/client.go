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
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/osac-project/fulfillment-service/internal/apiclient"
	"github.com/osac-project/fulfillment-service/internal/auth"
)

// Client is a Keycloak admin client for managing identity provider resources.
//
// Architecture:
// - One Keycloak realm contains all OSAC (default: "osac" realm)
// - OSAC organizations map to Keycloak Organizations within that realm
// - Identity providers are realm-level resources assigned to organizations
type Client struct {
	logger     *slog.Logger
	httpClient *apiclient.Client

	// realmName is the single Keycloak realm that contains all OSAC organizations
	realmName               string
	realmManagementClientID string
}

// ClientBuilder builds a Keycloak client.
type ClientBuilder struct {
	logger      *slog.Logger
	baseURL     string
	tokenSource auth.TokenSource
	caPool      *x509.CertPool
	httpClient  *http.Client
	realmName   string
}

// NewClient creates a builder for a Keycloak admin client.
func NewClient() *ClientBuilder {
	return &ClientBuilder{}
}

// SetLogger sets the logger.
func (b *ClientBuilder) SetLogger(value *slog.Logger) *ClientBuilder {
	b.logger = value
	return b
}

// SetBaseURL sets the base URL of the Keycloak server.
func (b *ClientBuilder) SetBaseURL(value string) *ClientBuilder {
	b.baseURL = value
	return b
}

// SetTokenSource sets the token source for authentication.
func (b *ClientBuilder) SetTokenSource(value auth.TokenSource) *ClientBuilder {
	b.tokenSource = value
	return b
}

// SetRealmName sets the realm name.
// This is the single Keycloak realm that contains all OSAC organizations.
// If not set, the default realm name is "osac".
func (b *ClientBuilder) SetRealmName(value string) *ClientBuilder {
	b.realmName = value
	return b
}

// SetCaPool sets the CA certificate pool.
func (b *ClientBuilder) SetCaPool(value *x509.CertPool) *ClientBuilder {
	b.caPool = value
	return b
}

// SetHTTPClient sets a custom HTTP client.
func (b *ClientBuilder) SetHTTPClient(value *http.Client) *ClientBuilder {
	b.httpClient = value
	return b
}

// Build creates the Keycloak client.
func (b *ClientBuilder) Build() (result *Client, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.baseURL == "" {
		err = errors.New("base URL is mandatory")
		return
	}
	if b.tokenSource == nil {
		err = errors.New("token source is mandatory")
		return
	}
	if b.realmName == "" {
		b.realmName = "osac"
	}

	// Build the underlying HTTP client
	httpClientBuilder := apiclient.NewClient().
		SetLogger(b.logger).
		SetBaseURL(strings.TrimSuffix(b.baseURL, "/")).
		SetTokenSource(b.tokenSource)

	if b.caPool != nil {
		httpClientBuilder = httpClientBuilder.SetCaPool(b.caPool)
	}
	if b.httpClient != nil {
		httpClientBuilder = httpClientBuilder.SetHTTPClient(b.httpClient)
	}

	httpClient, err := httpClientBuilder.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build HTTP client: %w", err)
	}

	result = &Client{
		logger:     b.logger,
		httpClient: httpClient,
		realmName:  b.realmName,
	}
	return
}

func isConflictError(err error) bool {
	var apiErr *apiclient.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict
}

// CreateTenant creates a new tenant (Keycloak organization in the configured realm).
// Returns the created tenant with server-assigned ID and any server defaults.
func (c *Client) CreateTenant(ctx context.Context, tenant *Tenant) (*Tenant, error) {
	kcOrg := toKeycloakOrganization(tenant)
	response, err := c.httpClient.DoRequest(ctx, http.MethodPost, fmt.Sprintf("/admin/realms/%s/organizations", c.realmName), kcOrg)
	if err != nil {
		if isConflictError(err) {
			return c.GetTenant(ctx, tenant.Name)
		}
		return nil, fmt.Errorf("failed to create organization: %w", err)
	}
	response.Body.Close()

	// Keycloak's POST /admin/realms returns 201 with no body, so we fetch the created organization
	// to get the server-assigned ID and verify the organization was actually created
	return c.GetTenant(ctx, tenant.Name)
}

// GetTenant retrieves a tenant (Keycloak organization in the configured realm) by name.
func (c *Client) GetTenant(ctx context.Context, name string) (*Tenant, error) {
	query := url.Values{}
	query.Add("search", name)
	query.Add("exact", "true")
	path := fmt.Sprintf("/admin/realms/%s/organizations?%s", c.realmName, query.Encode())
	response, err := c.httpClient.DoRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get organization: %w", err)
	}
	defer response.Body.Close()

	var kcOrgs []keycloakOrganization
	if err = json.NewDecoder(response.Body).Decode(&kcOrgs); err != nil {
		return nil, fmt.Errorf("failed to decode organization response: %w", err)
	}
	if len(kcOrgs) == 0 {
		return nil, fmt.Errorf("organization %q not found", name)
	}
	kcOrg := kcOrgs[0]
	return fromKeycloakOrganization(&kcOrg), nil
}

// UpdateTenant updates an existing tenant (Keycloak organization in the configured realm).
// The tenant must have a non-empty ID.
func (c *Client) UpdateTenant(ctx context.Context, tenant *Tenant) (*Tenant, error) {
	if tenant == nil {
		return nil, fmt.Errorf("organization is required for update")
	}
	if tenant.ID == "" {
		return nil, fmt.Errorf("organization ID is required for update")
	}
	kcOrg := toKeycloakOrganization(tenant)
	path := fmt.Sprintf("/admin/realms/%s/organizations/%s", c.realmName, url.PathEscape(tenant.ID))
	response, err := c.httpClient.DoRequest(ctx, http.MethodPut, path, kcOrg)
	if err != nil {
		return nil, fmt.Errorf("failed to update organization: %w", err)
	}
	response.Body.Close()

	return c.GetTenant(ctx, tenant.Name)
}

// DeleteTenant deletes a tenant (Keycloak organization in the configured realm) by name.
func (c *Client) DeleteTenant(ctx context.Context, tenantName string) error {
	// Delete the break-glass account first (Keycloak-specific: it belongs to realm, not organization)
	breakGlassUsername := fmt.Sprintf("%s-osac-break-glass", tenantName)
	if err := c.deleteBreakGlassAccount(ctx, tenantName, breakGlassUsername); err != nil {
		return fmt.Errorf("failed to delete break-glass account: %w", err)
	}

	org, err := c.GetTenant(ctx, tenantName)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil
		}
		return fmt.Errorf("failed to get organization: %w", err)
	}
	response, err := c.httpClient.DoRequest(ctx, http.MethodDelete, fmt.Sprintf("/admin/realms/%s/organizations/%s", c.realmName, org.ID), nil)
	if err != nil {
		return fmt.Errorf("failed to delete organization: %w", err)
	}
	defer response.Body.Close()

	return nil
}

func (c *Client) AddUserToOrganization(ctx context.Context, tenantName string, userID string) error {
	org, err := c.GetTenant(ctx, tenantName)
	if err != nil {
		return fmt.Errorf("failed to get organization: %w", err)
	}
	path := fmt.Sprintf("/admin/realms/%s/organizations/%s/members", c.realmName, org.ID)
	response, err := c.httpClient.DoRequest(ctx, http.MethodPost, path, userID)
	if err != nil {
		if isConflictError(err) {
			return nil
		}
		return fmt.Errorf("failed to add user to organization: %w", err)
	}
	defer response.Body.Close()
	return nil
}

func (c *Client) CreateUserInRealm(ctx context.Context, user *User) (*User, error) {
	kcUser := toKeycloakUser(user)
	response, err := c.httpClient.DoRequest(ctx, http.MethodPost, fmt.Sprintf("/admin/realms/%s/users", c.realmName), kcUser)
	if err != nil {
		if isConflictError(err) {
			existing, lookupErr := c.getUserByUsername(ctx, user.Username)
			if lookupErr != nil || existing == nil {
				return nil, fmt.Errorf("user %q already exists but failed to look up: %w", user.Username, errors.Join(err, lookupErr))
			}
			return existing, nil
		}
		return nil, fmt.Errorf("failed to create user: %w", err)
	}
	defer response.Body.Close()

	location := response.Header.Get("Location")
	if location == "" {
		return nil, fmt.Errorf("Location header not present in create user response") //nolint:staticcheck // ST1005: Location is an HTTP header name
	}

	// Extract the user ID from the Location header (e.g., "/admin/realms/osac/users/user-123" -> "user-123")
	parts := strings.Split(strings.TrimSuffix(location, "/"), "/")
	userID := parts[len(parts)-1]
	kcUser.ID = userID
	return fromKeycloakUser(kcUser), nil
}

// CreateUser creates a new user in the OSAC realm and adds them to an organization.
// Returns the created user with ID populated.
// If adding to the organization fails, the user is still created in the realm and can be
// added to the organization later using AddUserToOrganization.
func (c *Client) CreateUser(ctx context.Context, tenantName string, user *User) (*User, error) {
	// Step 1: Create user in the OSAC realm
	createdUser, err := c.CreateUserInRealm(ctx, user)
	if err != nil {
		return nil, err
	}

	// Step 2: Add user to the organization
	err = c.AddUserToOrganization(ctx, tenantName, createdUser.ID)
	if err != nil {
		c.logger.WarnContext(ctx, "User created but failed to add to organization",
			slog.String("user_id", createdUser.ID),
			slog.String("organization", tenantName),
			slog.Any("error", err),
		)
		return createdUser, fmt.Errorf("failed to add user to organization (user %s created in realm): %w", createdUser.ID, err)
	}

	return createdUser, nil
}

// GetUser retrieves a user by ID from the realm.
func (c *Client) GetUser(ctx context.Context, tenantName, userID string) (*User, error) {
	response, err := c.httpClient.DoRequest(ctx, http.MethodGet, fmt.Sprintf("/admin/realms/%s/users/%s", c.realmName, url.PathEscape(userID)), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	defer response.Body.Close()

	var kcUser keycloakUser
	if err = json.NewDecoder(response.Body).Decode(&kcUser); err != nil {
		return nil, fmt.Errorf("failed to decode user response: %w", err)
	}
	return fromKeycloakUser(&kcUser), nil
}

// ListUsers lists all users (members) in an organization.
func (c *Client) ListUsers(ctx context.Context, tenantName string) ([]*User, error) {
	var allUsers []*User
	const maxPerPage = 100
	first := 0

	// Fetches all pages to ensure no users are missed due to Keycloak's pagination.
	for {
		// Check if context is cancelled before making the next API call
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		// Fetch one page of organization members
		path := fmt.Sprintf("/admin/realms/%s/organizations/%s/members?first=%d&max=%d",
			c.realmName,
			url.PathEscape(tenantName), first, maxPerPage)

		response, err := c.httpClient.DoRequest(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to list organization members: %w", err)
		}

		var kcUsers []keycloakUser
		err = json.NewDecoder(response.Body).Decode(&kcUsers)
		response.Body.Close()

		if err != nil {
			return nil, fmt.Errorf("failed to decode organization members response: %w", err)
		}

		// Convert and append this page
		for _, kcUser := range kcUsers {
			allUsers = append(allUsers, fromKeycloakUser(&kcUser))
		}

		// If we got fewer than max, we've reached the last page
		if len(kcUsers) < maxPerPage {
			break
		}

		// Move to next page
		first += maxPerPage
	}

	return allUsers, nil
}

// DeleteUserFromOrganization removes a user (member) from an organization.
func (c *Client) DeleteUserFromOrganization(ctx context.Context, tenantName, userID string) error {
	response, err := c.httpClient.DoRequest(ctx, http.MethodDelete, fmt.Sprintf("/admin/realms/%s/organizations/%s/members/%s", c.realmName, url.PathEscape(tenantName), url.PathEscape(userID)), nil)
	if err != nil {
		var apiErr *apiclient.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return fmt.Errorf("user %q not found in organization %q: %w", userID, tenantName, err)
		}
		return fmt.Errorf("failed to remove user %q from organization %q: %w", userID, tenantName, err)
	}
	defer response.Body.Close()
	return nil
}

func (c *Client) DeleteUserFromRealm(ctx context.Context, userID string) error {
	response, err := c.httpClient.DoRequest(ctx, http.MethodDelete, fmt.Sprintf("/admin/realms/%s/users/%s", c.realmName, url.PathEscape(userID)), nil)
	if err != nil {
		return fmt.Errorf("failed to delete user %q from realm: %w", userID, err)
	}
	defer response.Body.Close()
	return nil
}

// DeleteUser deletes a user by ID from the realm.
// Note: Deleting a user from the realm automatically removes them from all organizations,
// so there's no need to explicitly remove them from the organization first.
func (c *Client) DeleteUser(ctx context.Context, tenantName, userID string) error {
	return c.DeleteUserFromRealm(ctx, userID)
}

// ListTenantRoles lists all tenant-level roles.
// Note: Organizations in Keycloak don't have their own roles - they use realm roles.
func (c *Client) ListTenantRoles(ctx context.Context, tenantName string) ([]*Role, error) {
	// TODO: implement function
	return nil, nil
}

// ListClientRoles lists all roles for a specific client.
//
// The clientID parameter accepts either format for convenience:
//   - Human-readable clientId: "realm-management", "account", "my-app"
//   - Internal UUID: "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
func (c *Client) ListClientRoles(ctx context.Context, tenantName, clientID string) ([]*Role, error) {
	// Resolve to internal UUID
	internalID, err := c.GetRealmClientByClientID(ctx, clientID, c.realmName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve client ID: %w", err)
	}

	response, err := c.httpClient.DoRequest(ctx, http.MethodGet, fmt.Sprintf("/admin/realms/%s/clients/%s/roles", c.realmName, url.PathEscape(internalID)), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list client roles: %w", err)
	}
	defer response.Body.Close()

	var kcRoles []keycloakRole
	if err = json.NewDecoder(response.Body).Decode(&kcRoles); err != nil {
		return nil, fmt.Errorf("failed to decode client roles response: %w", err)
	}

	roles := make([]*Role, len(kcRoles))
	for i, kcRole := range kcRoles {
		roles[i] = fromKeycloakRole(&kcRole)
	}
	return roles, nil
}

// AssignTenantRolesToUser adds tenant-level roles to a user.
func (c *Client) AssignTenantRolesToUser(ctx context.Context, tenantName, userID string, roles []*Role) error {
	// Fetch full role objects from Keycloak to get their IDs
	kcRoles, err := c.fetchAndConvertRealmRoles(ctx, roles)
	if err != nil {
		return err
	}

	response, err := c.httpClient.DoRequest(ctx, http.MethodPost, fmt.Sprintf("/admin/realms/%s/users/%s/role-mappings/realm", c.realmName, url.PathEscape(userID)), kcRoles)
	if err != nil {
		return fmt.Errorf("failed to assign realm roles to user: %w", err)
	}
	defer response.Body.Close()
	return nil
}

// AssignClientRolesToUser adds client-level roles to a user.
//
// The clientID parameter accepts either format:
//   - Human-readable clientId: "realm-management", "account", "my-app"
//   - Internal UUID: "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
func (c *Client) AssignClientRolesToUser(ctx context.Context, tenantName, userID, clientID string, roles []*Role) error {
	// Resolve to internal UUID
	internalID, err := c.GetRealmClientByClientID(ctx, clientID, c.realmName)
	if err != nil {
		return fmt.Errorf("failed to resolve client ID: %w", err)
	}

	kcRoles := c.convertRolesToKeycloak(roles)

	response, err := c.httpClient.DoRequest(ctx, http.MethodPost, fmt.Sprintf("/admin/realms/%s/users/%s/role-mappings/clients/%s", c.realmName, url.PathEscape(userID), url.PathEscape(internalID)), kcRoles)
	if err != nil {
		return fmt.Errorf("failed to assign client roles to user: %w", err)
	}
	defer response.Body.Close()
	return nil
}

// RemoveTenantRolesFromUser removes tenant-level roles from a user.
func (c *Client) RemoveTenantRolesFromUser(ctx context.Context, tenantName, userID string, roles []*Role) error {
	// Fetch full role objects from Keycloak to get their IDs
	kcRoles, err := c.fetchAndConvertRealmRoles(ctx, roles)
	if err != nil {
		return err
	}

	response, err := c.httpClient.DoRequest(ctx, http.MethodDelete, fmt.Sprintf("/admin/realms/%s/users/%s/role-mappings/realm", c.realmName, url.PathEscape(userID)), kcRoles)
	if err != nil {
		return fmt.Errorf("failed to remove realm roles from user: %w", err)
	}
	defer response.Body.Close()
	return nil
}

// RemoveRealmRolesFromUser removes realm-level roles from a user.
func (c *Client) RemoveRealmRolesFromUser(ctx context.Context, userID string, roles []*Role) error {
	kcRoles := c.convertRolesToKeycloak(roles)

	response, err := c.httpClient.DoRequest(ctx, http.MethodDelete, fmt.Sprintf("/admin/realms/%s/users/%s/role-mappings/realm", c.realmName, url.PathEscape(userID)), kcRoles)
	if err != nil {
		return fmt.Errorf("failed to remove realm roles from user: %w", err)
	}
	defer response.Body.Close()
	return nil
}

// RemoveClientRolesFromUser removes client-level roles from a user.
//
// The clientID parameter accepts either format:
//   - Human-readable clientId: "realm-management", "account", "my-app"
//   - Internal UUID: "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
func (c *Client) RemoveClientRolesFromUser(ctx context.Context, tenantName, userID, clientID string, roles []*Role) error {
	// Resolve to internal UUID
	internalID, err := c.GetRealmClientByClientID(ctx, clientID, c.realmName)
	if err != nil {
		return fmt.Errorf("failed to resolve client ID: %w", err)
	}

	kcRoles := c.convertRolesToKeycloak(roles)

	response, err := c.httpClient.DoRequest(ctx, http.MethodDelete, fmt.Sprintf("/admin/realms/%s/users/%s/role-mappings/clients/%s", c.realmName, url.PathEscape(userID), url.PathEscape(internalID)), kcRoles)
	if err != nil {
		return fmt.Errorf("failed to remove client roles from user: %w", err)
	}
	defer response.Body.Close()
	return nil
}

// GetUserTenantRoles gets the tenant-level roles assigned to a user.
func (c *Client) GetUserTenantRoles(ctx context.Context, tenantName, userID string) ([]*Role, error) {
	// TODO: implement function
	return nil, nil
}

// GetUserClientRoles gets the client-level roles assigned to a user.
//
// The clientID parameter accepts either format:
//   - Human-readable clientId: "realm-management", "account", "my-app"
//   - Internal UUID: "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
func (c *Client) GetUserClientRoles(ctx context.Context, tenantName, userID, clientID string) ([]*Role, error) {
	// Resolve to internal UUID
	internalID, err := c.GetRealmClientByClientID(ctx, clientID, c.realmName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve client ID: %w", err)
	}

	response, err := c.httpClient.DoRequest(ctx, http.MethodGet, fmt.Sprintf("/admin/realms/%s/users/%s/role-mappings/clients/%s", c.realmName, url.PathEscape(userID), url.PathEscape(internalID)), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get user client roles: %w", err)
	}
	defer response.Body.Close()

	var kcRoles []keycloakRole
	if err = json.NewDecoder(response.Body).Decode(&kcRoles); err != nil {
		return nil, fmt.Errorf("failed to decode user client roles response: %w", err)
	}

	roles := make([]*Role, len(kcRoles))
	for i, kcRole := range kcRoles {
		roles[i] = fromKeycloakRole(&kcRole)
	}
	return roles, nil
}

// GetRealmClientByClientID resolves a client identifier to its internal UUID.
//
// The clientID parameter accepts either format:
//   - Human-readable clientId: "realm-management", "account", "my-app"
//   - Internal UUID: "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
//
// The method first checks if clientID is a valid UUID. If so, it returns it immediately
// (no API call needed).
//
// This is needed because Keycloak's role-mapping API endpoints require the internal UUID,
// but we use the human-readable clientId "realm-management".
//
// Example:
//
//	uuid, err := client.GetRealmClientByClientID(ctx, "realm-management", "osac")
//
// "realm-management" is the human-readable clientId
// "osac" is the realm name
//
//	// Returns: "a1b2c3d4-e5f6-7890-..." (internal UUID)
func (c *Client) GetRealmClientByClientID(ctx context.Context, clientID, realmName string) (string, error) {
	// Check if clientID is already a valid UUID (internal ID)
	// If so, return it immediately without making an API call
	if _, err := uuid.Parse(clientID); err == nil {
		return clientID, nil
	}

	// For realm-management client, use the cached client ID if available
	if clientID == realmManagementClientID {
		if c.realmManagementClientID != "" {
			return c.realmManagementClientID, nil
		}
	}

	// Look up the client UUID via API
	response, err := c.httpClient.DoRequest(ctx, http.MethodGet, fmt.Sprintf("/admin/realms/%s/clients?clientId=%s", realmName, url.QueryEscape(clientID)), nil)
	if err != nil {
		return "", fmt.Errorf("failed to get client by clientId: %w", err)
	}
	defer response.Body.Close()

	var kcClients []keycloakClient
	if err = json.NewDecoder(response.Body).Decode(&kcClients); err != nil {
		return "", fmt.Errorf("failed to decode clients response: %w", err)
	}

	if len(kcClients) == 0 {
		return "", fmt.Errorf("client %q not found", clientID)
	}

	internalUUID := kcClients[0].ID
	// For realm-management client, cache the client ID if not already cached
	if clientID == realmManagementClientID {
		c.realmManagementClientID = internalUUID
	}
	return internalUUID, nil
}

// AssignTenantAdminPermissions grants administrative access to a tenant for the specified user.
//
// For Keycloak, this assigns organization-level admin roles to the user.
func (c *Client) AssignTenantAdminPermissions(ctx context.Context, tenantName, userID string) error {
	// TODO: implement function
	return nil
}

func (c *Client) GetRealmRole(ctx context.Context, roleName string) (*Role, error) {
	response, err := c.httpClient.DoRequest(ctx, http.MethodGet, fmt.Sprintf("/admin/realms/%s/roles/%s", c.realmName, url.PathEscape(roleName)), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get role: %w", err)
	}
	defer response.Body.Close()
	var kcRole keycloakRole
	if err = json.NewDecoder(response.Body).Decode(&kcRole); err != nil {
		return nil, fmt.Errorf("failed to decode role response: %w", err)
	}
	return fromKeycloakRole(&kcRole), nil
}

// AssignIdpManagerPermissions grants limited IdP management permissions to the specified user.
//
// For Keycloak, this assigns a tenant-idp-manager role to the user.
// Intended for the break-glass account which can manage user roles and identity providers but cannot modify critical
// organization settings, realm settings, or authorization policies.
func (c *Client) AssignIdpManagerPermissions(ctx context.Context, userID string) error {
	domainRole, err := c.GetRealmRole(ctx, "tenant-idp-manager")
	if err != nil {
		return fmt.Errorf("failed to get tenant-idp-manager role from Keycloak: %w", err)
	}
	// Keycloak role assignment API expects an array of roles
	response, err := c.httpClient.DoRequest(ctx, http.MethodPost, fmt.Sprintf("/admin/realms/%s/users/%s/role-mappings/realm", c.realmName, url.PathEscape(userID)), []keycloakRole{*toKeycloakRole(domainRole)})
	if err != nil {
		return fmt.Errorf("failed to assign role to user: %w", err)
	}
	defer response.Body.Close()
	return nil
}

// GetUserByUsername implements the Client interface method.
// Note: tenantName is accepted for interface compatibility but not used in the Keycloak implementation
// since users in Keycloak are realm-scoped, not organization-scoped.
func (c *Client) GetUserByUsername(ctx context.Context, tenantName, username string) (*User, error) {
	return c.getUserByUsername(ctx, username)
}

// getUserByUsername retrieves a user by username from the realm.
// Returns nil if the user is not found.
func (c *Client) getUserByUsername(ctx context.Context, username string) (*User, error) {
	query := url.Values{}
	query.Add("username", username)
	query.Add("exact", "true")
	path := fmt.Sprintf("/admin/realms/%s/users?%s", c.realmName, query.Encode())

	response, err := c.httpClient.DoRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to query user by username: %w", err)
	}
	defer response.Body.Close()

	var kcUsers []keycloakUser
	if err = json.NewDecoder(response.Body).Decode(&kcUsers); err != nil {
		return nil, fmt.Errorf("failed to decode user query response: %w", err)
	}

	if len(kcUsers) == 0 {
		// User not found - return nil without error
		return nil, nil
	}

	return fromKeycloakUser(&kcUsers[0]), nil
}

// deleteBreakGlassAccount is a Keycloak-specific helper that deletes the break-glass account.
// In Keycloak, the break-glass account belongs to the realm (not the organization),
// so it must be explicitly deleted and won't be cascade-deleted with the organization.
func (c *Client) deleteBreakGlassAccount(ctx context.Context, tenantName, breakGlassUsername string) error {
	// Query for the break-glass user by username
	user, err := c.getUserByUsername(ctx, breakGlassUsername)
	if err != nil {
		return fmt.Errorf("failed to get user by username: %w", err)
	}

	if user == nil {
		// Break-glass account not found - may have been already deleted
		// This is not an error, just return success
		return nil
	}

	// Delete the break-glass user
	err = c.DeleteUser(ctx, tenantName, user.ID)
	if err != nil {
		return fmt.Errorf("failed to delete break-glass user: %w", err)
	}

	return nil
}

// CreateIdentityProvider creates an identity provider for a specific organization.
// In Keycloak, this creates the IdP at realm level and links it to the organization.
func (c *Client) CreateIdentityProvider(ctx context.Context, tenantName string, idpProvider *IdentityProvider) (*IdentityProvider, error) {
	if idpProvider == nil {
		return nil, fmt.Errorf("identity provider is nil")
	}
	c.logger.InfoContext(ctx, "Creating identity provider",
		slog.String("realm", c.realmName),
		slog.String("organization", tenantName),
		slog.String("alias", idpProvider.Alias),
		slog.String("type", idpProvider.Type),
	)

	// Step 1: Create at realm level
	path := fmt.Sprintf("/admin/realms/%s/identity-provider/instances", url.PathEscape(c.realmName))
	kcIdp := toKeycloakIdentityProvider(idpProvider)

	response, err := c.httpClient.DoRequest(ctx, http.MethodPost, path, kcIdp)
	if err != nil {
		return nil, fmt.Errorf("failed to create identity provider: %w", err)
	}
	defer response.Body.Close()

	// Step 2: Link to organization
	err = c.linkIdentityProviderToOrganization(ctx, tenantName, idpProvider.Alias)
	if err != nil {
		// Try to clean up the realm-level IdP if linking fails
		if cleanupErr := c.deleteIdentityProviderFromRealm(ctx, idpProvider.Alias); cleanupErr != nil {
			return nil, fmt.Errorf("failed to link identity provider to organization: %w (cleanup also failed: %w)", err, cleanupErr)
		}
		return nil, fmt.Errorf("failed to link identity provider to organization: %w", err)
	}

	// Step 3: Fetch and return (Keycloak returns empty body on creation)
	result, err := c.GetIdentityProvider(ctx, tenantName, idpProvider.Alias)
	if err != nil {
		// IdP was successfully created and linked - treat read failure as non-fatal
		c.logger.WarnContext(ctx, "Created identity provider but failed to fetch it back",
			slog.String("organization", tenantName),
			slog.String("alias", idpProvider.Alias),
			slog.String("error", err.Error()),
		)
		// Return a sanitized copy without sensitive Config data
		return &IdentityProvider{
			Alias:       idpProvider.Alias,
			DisplayName: idpProvider.DisplayName,
			Type:        idpProvider.Type,
			Enabled:     idpProvider.Enabled,
			Config:      nil, // Secrets are automatically filtered in GET responses
		}, nil
	}
	return result, nil
}

// GetIdentityProvider retrieves an identity provider for a specific organization.
func (c *Client) GetIdentityProvider(ctx context.Context, tenantName, alias string) (*IdentityProvider, error) {
	c.logger.InfoContext(ctx, "Getting identity provider",
		slog.String("realm", c.realmName),
		slog.String("organization", tenantName),
		slog.String("alias", alias),
	)

	// Get the organization to obtain its ID
	org, err := c.GetTenant(ctx, tenantName)
	if err != nil {
		return nil, fmt.Errorf("failed to get organization: %w", err)
	}

	// Get the IdP from the organization endpoint
	path := fmt.Sprintf("/admin/realms/%s/organizations/%s/identity-providers/%s",
		url.PathEscape(c.realmName),
		url.PathEscape(org.ID),
		url.PathEscape(alias),
	)

	response, err := c.httpClient.DoRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get identity provider: %w", err)
	}
	defer response.Body.Close()

	var kcIdp keycloakIdentityProvider
	if err := json.NewDecoder(response.Body).Decode(&kcIdp); err != nil {
		return nil, fmt.Errorf("failed to decode identity provider response: %w", err)
	}

	return fromKeycloakIdentityProvider(&kcIdp), nil
}

// DeleteIdentityProvider deletes an identity provider for a specific organization.
// In Keycloak, this deletes the IdP at realm level (which auto-removes from all organizations).
func (c *Client) DeleteIdentityProvider(ctx context.Context, tenantName, alias string) error {
	c.logger.InfoContext(ctx, "Deleting identity provider",
		slog.String("realm", c.realmName),
		slog.String("organization", tenantName),
		slog.String("alias", alias),
	)

	// Delete at realm level - this automatically removes from all organizations
	return c.deleteIdentityProviderFromRealm(ctx, alias)
}

// deleteIdentityProviderFromRealm is an internal helper that deletes at realm level.
func (c *Client) deleteIdentityProviderFromRealm(ctx context.Context, alias string) error {
	path := fmt.Sprintf("/admin/realms/%s/identity-provider/instances/%s",
		url.PathEscape(c.realmName),
		url.PathEscape(alias),
	)

	response, err := c.httpClient.DoRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return fmt.Errorf("failed to delete identity provider: %w", err)
	}
	defer response.Body.Close()

	return nil
}

// linkIdentityProviderToOrganization is an internal helper that links an IdP to an organization.
func (c *Client) linkIdentityProviderToOrganization(ctx context.Context, tenantName, alias string) error {
	c.logger.InfoContext(ctx, "Linking identity provider to organization",
		slog.String("organization", tenantName),
		slog.String("alias", alias),
	)

	// Get the organization to obtain its ID
	org, err := c.GetTenant(ctx, tenantName)
	if err != nil {
		return fmt.Errorf("failed to get organization: %w", err)
	}

	// Link the IdP to the organization
	path := fmt.Sprintf("/admin/realms/%s/organizations/%s/identity-providers",
		url.PathEscape(c.realmName),
		url.PathEscape(org.ID),
	)

	// The body is just the alias as a JSON string (Keycloak expects "alias" not {"alias": "value"})
	response, err := c.httpClient.DoRequest(ctx, http.MethodPost, path, alias)
	if err != nil {
		return fmt.Errorf("failed to link identity provider to organization: %w", err)
	}
	defer response.Body.Close()

	return nil
}

// ListIdentityProviders lists all identity providers for a specific organization.
func (c *Client) ListIdentityProviders(ctx context.Context, tenantName string) ([]*IdentityProvider, error) {
	c.logger.InfoContext(ctx, "Listing organization identity providers",
		slog.String("organization", tenantName),
	)

	// Get the organization to obtain its ID
	org, err := c.GetTenant(ctx, tenantName)
	if err != nil {
		return nil, fmt.Errorf("failed to get organization: %w", err)
	}

	path := fmt.Sprintf("/admin/realms/%s/organizations/%s/identity-providers",
		url.PathEscape(c.realmName),
		url.PathEscape(org.ID),
	)

	response, err := c.httpClient.DoRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list organization identity providers: %w", err)
	}
	defer response.Body.Close()

	var kcIdps []keycloakIdentityProvider
	if err := json.NewDecoder(response.Body).Decode(&kcIdps); err != nil {
		return nil, fmt.Errorf("failed to decode organization identity providers response: %w", err)
	}

	idps := make([]*IdentityProvider, 0, len(kcIdps))
	for i := range kcIdps {
		idps = append(idps, fromKeycloakIdentityProvider(&kcIdps[i]))
	}

	c.logger.InfoContext(ctx, "Listed organization identity providers",
		slog.String("organization", tenantName),
		slog.Int("count", len(idps)),
	)

	return idps, nil
}

// Helper methods for role conversion

// fetchAndConvertRealmRoles fetches full realm role objects from Keycloak by name
// and converts them to keycloakRole format. This is needed when assigning/removing
// realm roles because Keycloak requires the full role representation including ID.
func (c *Client) fetchAndConvertRealmRoles(ctx context.Context, roles []*Role) ([]keycloakRole, error) {
	kcRoles := make([]keycloakRole, 0, len(roles))
	for _, role := range roles {
		domainRole, err := c.GetRealmRole(ctx, role.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get realm role %s: %w", role.Name, err)
		}
		kcRoles = append(kcRoles, *toKeycloakRole(domainRole))
	}
	return kcRoles, nil
}

// convertRolesToKeycloak converts domain Role objects to keycloakRole format.
// This is used when the caller already has complete role information (including ID).
func (c *Client) convertRolesToKeycloak(roles []*Role) []keycloakRole {
	kcRoles := make([]keycloakRole, len(roles))
	for i, role := range roles {
		kcRoles[i] = *toKeycloakRole(role)
	}
	return kcRoles
}
