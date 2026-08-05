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

//go:generate go run go.uber.org/mock/mockgen -destination=client_mock.go -package=idp . ClientInterface

import "context"

// ClientInterface defines the interface for interacting with the identity provider (Keycloak).
// This interface exists primarily to enable mocking in tests.
// Production code uses the concrete *Client type.
type ClientInterface interface {
	CreateTenant(ctx context.Context, tenant *Tenant) (*Tenant, error)
	GetTenant(ctx context.Context, name string) (*Tenant, error)
	UpdateTenant(ctx context.Context, tenant *Tenant) (*Tenant, error)
	DeleteTenant(ctx context.Context, tenantName string) error
	AddUserToOrganization(ctx context.Context, tenantName string, userID string) error
	CreateUserInRealm(ctx context.Context, user *User) (*User, error)
	CreateUser(ctx context.Context, tenantName string, user *User) (*User, error)
	GetUser(ctx context.Context, tenantName, userID string) (*User, error)
	ListUsers(ctx context.Context, tenantName string) ([]*User, error)
	DeleteUserFromOrganization(ctx context.Context, tenantName, userID string) error
	DeleteUserFromRealm(ctx context.Context, userID string) error
	DeleteUser(ctx context.Context, tenantName, userID string) error
	ListTenantRoles(ctx context.Context, tenantName string) ([]*Role, error)
	ListClientRoles(ctx context.Context, tenantName, clientID string) ([]*Role, error)
	AssignTenantRolesToUser(ctx context.Context, tenantName, userID string, roles []*Role) error
	AssignClientRolesToUser(ctx context.Context, tenantName, userID, clientID string, roles []*Role) error
	RemoveTenantRolesFromUser(ctx context.Context, tenantName, userID string, roles []*Role) error
	RemoveRealmRolesFromUser(ctx context.Context, userID string, roles []*Role) error
	RemoveClientRolesFromUser(ctx context.Context, tenantName, userID, clientID string, roles []*Role) error
	GetUserTenantRoles(ctx context.Context, tenantName, userID string) ([]*Role, error)
	GetUserClientRoles(ctx context.Context, tenantName, userID, clientID string) ([]*Role, error)
	GetRealmClientByClientID(ctx context.Context, clientID, realmName string) (string, error)
	AssignTenantAdminPermissions(ctx context.Context, tenantName, userID string) error
	GetRealmRole(ctx context.Context, roleName string) (*Role, error)
	AssignIdpManagerPermissions(ctx context.Context, userID string) error
	GetUserByUsername(ctx context.Context, tenantName, username string) (*User, error)
	CreateIdentityProvider(ctx context.Context, tenantName string, idpProvider *IdentityProvider) (*IdentityProvider, error)
	GetIdentityProvider(ctx context.Context, tenantName, alias string) (*IdentityProvider, error)
	DeleteIdentityProvider(ctx context.Context, tenantName, alias string) error
	ListIdentityProviders(ctx context.Context, tenantName string) ([]*IdentityProvider, error)
	CreateGroup(ctx context.Context, tenantName, groupPath string) (string, error)
	DeleteGroup(ctx context.Context, tenantName, groupID string) error
	GetGroupIDByPath(ctx context.Context, tenantName, groupPath string) (string, error)
	AddUserToGroup(ctx context.Context, tenantName, idpUserID, groupID string) error
	RemoveUserFromGroup(ctx context.Context, tenantName, idpUserID, groupID string) error
}

// Ensure Client implements ClientInterface at compile time.
var _ ClientInterface = (*Client)(nil)
