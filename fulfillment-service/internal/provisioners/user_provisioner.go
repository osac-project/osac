/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package provisioners

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
)

// UserProvisionerBuilder builds a UserProvisioner.
type UserProvisionerBuilder struct {
	logger      *slog.Logger
	usersServer privatev1.UsersServer
}

// UserProvisioner implements auth.UserProvisioner using the PrivateUsersServer.
// The server handles event generation, so user creation automatically triggers events
// for the user controller to reconcile and populate keycloak_user_id in status.
type UserProvisioner struct {
	logger      *slog.Logger
	usersServer privatev1.UsersServer
}

// NewUserProvisioner creates a new builder.
func NewUserProvisioner() *UserProvisionerBuilder {
	return &UserProvisionerBuilder{}
}

// SetLogger sets the logger.
func (b *UserProvisionerBuilder) SetLogger(value *slog.Logger) *UserProvisionerBuilder {
	b.logger = value
	return b
}

// SetUsersServer sets the users server (which handles event generation).
func (b *UserProvisionerBuilder) SetUsersServer(value privatev1.UsersServer) *UserProvisionerBuilder {
	b.usersServer = value
	return b
}

// Build creates the provisioner.
func (b *UserProvisionerBuilder) Build() (result *UserProvisioner, err error) {
	if b.logger == nil {
		return nil, fmt.Errorf("logger is mandatory")
	}
	if b.usersServer == nil {
		return nil, fmt.Errorf("users server is mandatory")
	}
	result = &UserProvisioner{
		logger:      b.logger,
		usersServer: b.usersServer,
	}
	return result, nil
}

// Provision creates a user record if it doesn't exist.
// The server handles event generation, so creating a user will trigger an event
// that the user controller watches. The controller will reconcile the user and
// populate the keycloak_user_id in the status.
func (p *UserProvisioner) Provision(ctx context.Context, username, tenant string, claims jwt.MapClaims) error {
	// Check if user exists
	filter := fmt.Sprintf("this.spec.username==%q", username)
	limit := int32(1)
	listResponse, err := p.usersServer.List(ctx, &privatev1.UsersListRequest{
		Filter: &filter,
		Limit:  &limit,
	})
	if err != nil {
		return fmt.Errorf("failed to check if user exists: %w", err)
	}

	// User already exists
	if listResponse.GetSize() > 0 {
		return nil
	}

	// Extract claims
	email, _ := claims["email"].(string)

	// Sanitize username for use as metadata.name (must be DNS-1123 compliant).
	// DNS-1123 requires: lowercase letters, digits, and hyphens only, with no
	// leading or trailing hyphens. The original username is preserved in spec.username.
	sanitizedName := sanitizeForDNS1123(username)

	p.logger.InfoContext(ctx, "Provisioning user",
		slog.String("username", username),
		slog.String("name", sanitizedName),
		slog.String("tenant", tenant),
	)

	// Create user via server (this will trigger events for the controller)
	user := privatev1.User_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   sanitizedName,
			Tenant: tenant,
		}.Build(),
		Spec: privatev1.UserSpec_builder{
			Username: username,
			Email:    email,
			Enabled:  true,
		}.Build(),
	}.Build()

	_, err = p.usersServer.Create(ctx, &privatev1.UsersCreateRequest{
		Object: user,
	})
	if err != nil {
		// If the user was created concurrently (race condition), treat as success
		if status.Code(err) == codes.AlreadyExists {
			return nil
		}
		return fmt.Errorf("failed to create user: %w", err)
	}

	return nil
}

// sanitizeForDNS1123 converts a username to a DNS-1123 compliant name.
// DNS-1123 names must:
// - Contain only lowercase letters (a-z), digits (0-9), and hyphens (-)
// - Not start or end with a hyphen
func sanitizeForDNS1123(username string) string {
	// Convert to lowercase
	result := strings.ToLower(username)

	// Replace any character that isn't [a-z0-9-] with a hyphen
	var sanitized strings.Builder
	for _, r := range result {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			sanitized.WriteRune(r)
		} else {
			sanitized.WriteRune('-')
		}
	}
	result = sanitized.String()

	// Trim leading and trailing hyphens
	result = strings.Trim(result, "-")

	return result
}

var _ auth.UserProvisioner = (*UserProvisioner)(nil)
