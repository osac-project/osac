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
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"slices"
	"time"
)

// TenantManager handles the lifecycle of tenants in Keycloak.
type TenantManager struct {
	logger *slog.Logger
	client ClientInterface
}

// TenantManagerBuilder builds the manager.
type TenantManagerBuilder struct {
	logger *slog.Logger
	client ClientInterface
}

// NewTenantManager creates a builder for the tenant manager.
func NewTenantManager() *TenantManagerBuilder {
	return &TenantManagerBuilder{}
}

// SetLogger sets the logger.
func (b *TenantManagerBuilder) SetLogger(value *slog.Logger) *TenantManagerBuilder {
	b.logger = value
	return b
}

// SetClient sets the Keycloak client.
func (b *TenantManagerBuilder) SetClient(value ClientInterface) *TenantManagerBuilder {
	b.client = value
	return b
}

// Build creates the manager.
func (b *TenantManagerBuilder) Build() (result *TenantManager, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.client == nil {
		err = errors.New("client is mandatory")
		return
	}

	result = &TenantManager{
		logger: b.logger,
		client: b.client,
	}
	return
}

// TenantConfig contains configuration for creating a tenant in the identity provider.
type TenantConfig struct {
	// Name is the unique identifier for the tenant
	Name string

	// DisplayName is the human-readable name
	DisplayName string

	// Enabled indicates whether the tenant should be enabled in the identity provider. Nil defaults to true.
	Enabled *bool

	// Domains is the list of e-mail domains associated with the tenant.
	Domains []string

	// BreakGlassUsername is the username for the break-glass account
	// If empty, defaults to "osac-break-glass"
	BreakGlassUsername string

	// BreakGlassEmail is the email for the break-glass account
	// If empty, defaults to "break-glass@{tenant-name}.osac.local"
	BreakGlassEmail string

	// BreakGlassPassword is the temporary password for the break-glass account.
	// The caller must generate and provide this; the IDP manager will not auto-generate one.
	BreakGlassPassword string
}

// BreakGlassCredentials contains the credentials for the break-glass account.
//
// SECURITY NOTES:
//   - Password is plaintext and MUST be handled securely
//   - DO NOT log the password
//   - Store in a secrets manager (Vault, Kubernetes Secrets, AWS Secrets Manager)
//   - Transmit only over TLS
//   - Clear from memory immediately after use
//   - Password is temporary and must be changed on first login
type BreakGlassCredentials struct {
	// UserID is the unique identifier for the break-glass user in the IdP
	UserID string

	// Username is the username for the break-glass account
	Username string

	// Email is the email address for the break-glass account
	Email string

	// Password is the temporary password that must be changed on first login.
	// This field is intentionally excluded from JSON marshaling to prevent
	// accidental logging or exposure.
	Password string `json:"-"`
}

// CreateTenant creates a complete IdP tenant setup with a break-glass account.
// Returns the break-glass account credentials and error.
func (m *TenantManager) CreateTenant(ctx context.Context, config *TenantConfig) (*BreakGlassCredentials, error) {
	if config == nil {
		return nil, errors.New("TenantConfig is mandatory")
	}

	m.logger.InfoContext(ctx, "Creating IdP tenant",
		slog.String("tenant", config.Name),
	)

	var (
		// Track if the tenant was created in case of error and rollback is needed
		tenantCreated bool
		credentials   *BreakGlassCredentials
		err           error
	)

	// Defer cleanup on error
	defer func() {
		if err != nil {
			m.logger.ErrorContext(ctx, "Error creating tenant in IdP",
				slog.String("tenant", config.Name),
				slog.Any("error", err),
			)
			m.rollback(ctx, config.Name, tenantCreated)
		}
	}()

	// Step 1: Create the tenant in the IdP
	enabled := true
	if config.Enabled != nil {
		enabled = *config.Enabled
	}
	tenant := &Tenant{
		Name:        config.Name,
		DisplayName: config.DisplayName,
		Enabled:     enabled,
		Domains:     config.Domains,
	}
	createdTenant, err := m.client.CreateTenant(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("failed to create tenant in IdP: %w", err)
	}
	tenantCreated = true
	m.logger.InfoContext(ctx, "Tenant created in IdP",
		slog.String("tenant", createdTenant.Name),
	)

	// Step 2: Create break-glass account
	credentials, err = m.createBreakGlassAccount(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create break-glass account: %w", err)
	}

	// Step 3: Assign IdP manager permissions to break-glass account
	err = m.assignIdpManagerPermissions(ctx, credentials.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to assign IdP manager permissions: %w", err)
	}

	m.logger.InfoContext(ctx, "IdP tenant created successfully",
		slog.String("tenant", createdTenant.Name),
	)
	return credentials, nil
}

// UpdateTenant updates an existing tenant in the identity provider. It fetches the current
// tenant by name, applies the updated domains, and sends the update to the IdP.
func (m *TenantManager) UpdateTenant(ctx context.Context, name string, domains []string) error {
	if name == "" {
		return errors.New("tenant name is mandatory")
	}

	m.logger.InfoContext(ctx, "Updating IdP tenant domains",
		slog.String("tenant", name),
	)

	tenant, err := m.client.GetTenant(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to get tenant from IdP for update: %w", err)
	}
	if tenant == nil {
		return fmt.Errorf("tenant '%s' not found in IdP", name)
	}

	currentDomains := slices.Clone(tenant.Domains)
	desiredDomains := slices.Clone(domains)
	slices.Sort(currentDomains)
	slices.Sort(desiredDomains)
	if slices.Equal(currentDomains, desiredDomains) {
		m.logger.DebugContext(ctx, "IdP tenant domains already up to date, skipping update",
			slog.String("tenant", name),
		)
		return nil
	}

	tenant.Domains = domains
	_, err = m.client.UpdateTenant(ctx, tenant)
	if err != nil {
		return fmt.Errorf("failed to update tenant in IdP: %w", err)
	}

	m.logger.InfoContext(ctx, "IdP tenant domains updated successfully",
		slog.String("tenant", name),
	)
	return nil
}

// rollback performs cleanup by deleting the tenant from the IdP.
// Deleting the IdP tenant will cascade-delete all resources within it (users, roles, etc.).
func (m *TenantManager) rollback(ctx context.Context, tenantName string, deleteTenant bool) {
	if !deleteTenant {
		return
	}

	// Use a fresh context for cleanup so rollback succeeds even if
	// the original context was cancelled or timed out.
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	m.logger.WarnContext(ctx, "Rolling back tenant creation in IdP",
		slog.String("tenant", tenantName),
	)

	// Delete tenant from IdP (cascade-deletes all users and resources within it)
	if err := m.client.DeleteTenant(cleanupCtx, tenantName); err != nil {
		m.logger.ErrorContext(ctx, "Failed to rollback tenant creation in IdP",
			slog.String("tenant", tenantName),
			slog.Any("error", err),
		)
	} else {
		m.logger.InfoContext(ctx, "Rolled back tenant creation in IdP",
			slog.String("tenant", tenantName),
		)
	}
}

// createBreakGlassAccount creates the break-glass account for a tenant.
// Returns the break-glass credentials and error.
// The break-glass account is a built-in OSAC user with limited privileges (idp-manager role)
// that can manage IdP configuration and roles.
func (m *TenantManager) createBreakGlassAccount(ctx context.Context, config *TenantConfig) (*BreakGlassCredentials, error) {
	// Set defaults if not provided
	username := config.BreakGlassUsername
	if username == "" {
		username = fmt.Sprintf("%s-osac-break-glass", config.Name)
	}

	email := config.BreakGlassEmail
	if email == "" {
		email = fmt.Sprintf("break-glass@%s.osac.local", config.Name)
	}
	password := config.BreakGlassPassword
	if password == "" {
		var err error
		password, err = generatePassword()
		if err != nil {
			return nil, fmt.Errorf("failed to generate break-glass password: %w", err)
		}
	}

	user := &User{
		Username:      username,
		Email:         email,
		EmailVerified: true,
		Enabled:       true,
		FirstName:     "OSAC",
		LastName:      "Break-Glass",
		Credentials: []*Credential{
			{
				Type:      "password",
				Value:     password,
				Temporary: true, // User must change password on first login
			},
		},
	}

	createdUser, err := m.client.CreateUser(ctx, config.Name, user)
	if err != nil {
		return nil, err
	}

	credentials := &BreakGlassCredentials{
		UserID:   createdUser.ID,
		Username: username,
		Email:    email,
		Password: password,
	}

	m.logger.InfoContext(ctx, "Break-glass account created for tenant",
		slog.String("tenant_name", config.Name),
		slog.String("username", username),
		slog.String("user_id", createdUser.ID),
	)

	return credentials, nil
}

// assignIdpManagerPermissions assigns limited IdP manager permissions to a user.
// This grants the user permissions to manage user roles and identity providers but not
// critical realm settings.
func (m *TenantManager) assignIdpManagerPermissions(ctx context.Context, userID string) error {
	m.logger.InfoContext(ctx, "Assigning IdP manager permissions to user",
		slog.String("user_id", userID),
	)

	err := m.client.AssignIdpManagerPermissions(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to assign IdP manager permissions: %w", err)
	}

	m.logger.InfoContext(ctx, "IdP manager permissions assigned",
		slog.String("user_id", userID),
	)
	return nil
}

// DeleteTenant deletes a tenant from the IdP and all of its resources.
func (m *TenantManager) DeleteTenant(ctx context.Context, tenantName string) error {
	m.logger.InfoContext(ctx, "Deleting tenant from IdP",
		slog.String("tenant", tenantName),
	)

	err := m.client.DeleteTenant(ctx, tenantName)
	if err != nil {
		return fmt.Errorf("failed to delete tenant from IdP: %w", err)
	}

	m.logger.InfoContext(ctx, "IdP tenant deleted successfully",
		slog.String("tenant", tenantName),
	)
	return nil
}

func generatePassword() (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%"
	const length = 24
	b := make([]byte, length)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		b[i] = charset[n.Int64()]
	}
	return string(b), nil
}
