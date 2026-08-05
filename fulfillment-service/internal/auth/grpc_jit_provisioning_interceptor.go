/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"

	"github.com/osac-project/fulfillment-service/internal/util"
)

//go:generate mockgen -source=$GOFILE -destination=grpc_jit_provisioning_interceptor_mock.go -package=$GOPACKAGE UserProvisioner

// UserProvisioner provisions user records in the database.
type UserProvisioner interface {
	Provision(ctx context.Context, username, tenant string, claims jwt.MapClaims) error
}

// GrpcJitProvisioningInterceptorBuilder builds a GrpcJitProvisioningInterceptor.
type GrpcJitProvisioningInterceptorBuilder struct {
	logger      *slog.Logger
	provisioner UserProvisioner
}

// GrpcJitProvisioningInterceptor performs just-in-time user provisioning for authenticated requests.
// It runs after authentication and authorization, provisioning users who have been granted access.
type GrpcJitProvisioningInterceptor struct {
	logger      *slog.Logger
	provisioner UserProvisioner
}

// NewGrpcJitProvisioningInterceptor creates a new builder.
func NewGrpcJitProvisioningInterceptor() *GrpcJitProvisioningInterceptorBuilder {
	return &GrpcJitProvisioningInterceptorBuilder{}
}

// SetLogger sets the logger.
func (b *GrpcJitProvisioningInterceptorBuilder) SetLogger(value *slog.Logger) *GrpcJitProvisioningInterceptorBuilder {
	b.logger = value
	return b
}

// SetProvisioner sets the user provisioner.
func (b *GrpcJitProvisioningInterceptorBuilder) SetProvisioner(value UserProvisioner) *GrpcJitProvisioningInterceptorBuilder {
	b.provisioner = util.NormalizeNil(value)
	return b
}

// Build creates the interceptor.
func (b *GrpcJitProvisioningInterceptorBuilder) Build() (result *GrpcJitProvisioningInterceptor, err error) {
	if b.logger == nil {
		return nil, fmt.Errorf("logger is mandatory")
	}
	result = &GrpcJitProvisioningInterceptor{
		logger:      b.logger,
		provisioner: b.provisioner,
	}
	return result, nil
}

// UnaryServer is the unary server interceptor function.
func (i *GrpcJitProvisioningInterceptor) UnaryServer(ctx context.Context, req any,
	info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	err := i.provisionIfNeeded(ctx)
	if err != nil {
		return nil, err
	}
	return handler(ctx, req)
}

// StreamServer is the stream server interceptor function.
func (i *GrpcJitProvisioningInterceptor) StreamServer(srv any, stream grpc.ServerStream,
	info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	err := i.provisionIfNeeded(stream.Context())
	if err != nil {
		return err
	}
	return handler(srv, stream)
}

// provisionIfNeeded checks if user provisioning is needed and performs it.
// Returns nil if provisioning is not needed or succeeds, error if provisioning fails.
func (i *GrpcJitProvisioningInterceptor) provisionIfNeeded(ctx context.Context) error {
	// Skip if no provisioner configured
	if i.provisioner == nil {
		return nil
	}

	// Get subject from context (set by authz interceptor)
	subject := SubjectFromContext(ctx)
	if subject == nil {
		// No subject means anonymous or unauthenticated request - skip provisioning
		return nil
	}

	// Only provision for users with exactly one tenant
	// Users with zero tenants (unauthenticated), multiple tenants (cloud provider admins),
	// or special tenants (system/shared) should not have user records
	if !subject.Tenants.Finite() {
		return nil
	}

	tenants := subject.Tenants.Inclusions()
	if len(tenants) != 1 {
		// Zero or multiple tenants - not a regular user
		return nil
	}

	tenant := tenants[0]
	if tenant == SystemTenant || tenant == SharedTenant {
		// System and shared tenants don't have user records
		return nil
	}

	// Get username from subject
	username := subject.User

	// Get JWT token from context for additional claims
	token := TokenFromContext(ctx)
	if token == nil {
		// No token in context - skip provisioning
		return nil
	}

	// Extract JWT claims for email, name, etc.
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		i.logger.ErrorContext(ctx, "Failed to extract claims from JWT token for user provisioning")
		return nil
	}

	// Provision the user
	err := i.provisioner.Provision(ctx, username, tenant, claims)
	if err != nil {
		i.logger.ErrorContext(ctx, "Failed to provision user",
			slog.String("!username", username),
			slog.String("!tenant", tenant),
			slog.Any("error", err),
		)
		return fmt.Errorf("user provisioning failed: %w", err)
	}

	return nil
}
