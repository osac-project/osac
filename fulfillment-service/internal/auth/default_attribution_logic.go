/*
Copyright (c) 2025 Red Hat Inc.

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
)

//go:generate mockgen -destination=user_id_resolver_mock.go -package=auth . UserIDResolver

// UserIDResolver defines an interface for resolving usernames to user IDs.
type UserIDResolver interface {
	GetID(ctx context.Context, username string) (string, error)
}

// DefaultAttributionLogicBuilder contains the data and logic needed to create default attribution logic.
type DefaultAttributionLogicBuilder struct {
	logger         *slog.Logger
	userIDResolver UserIDResolver
}

// DefaultAttributionLogic is the default implementation of AttributionLogic that looks up the user and returns their ID as the creator.
type DefaultAttributionLogic struct {
	logger         *slog.Logger
	userIDResolver UserIDResolver
}

// NewDefaultAttributionLogic creates a new builder for default attribution logic.
func NewDefaultAttributionLogic() *DefaultAttributionLogicBuilder {
	return &DefaultAttributionLogicBuilder{}
}

// SetLogger sets the logger that will be used by the attribution logic. This is mandatory.
func (b *DefaultAttributionLogicBuilder) SetLogger(value *slog.Logger) *DefaultAttributionLogicBuilder {
	b.logger = value
	return b
}

// SetUserIDResolver sets the user ID resolver that will be used to resolve usernames to IDs. This is optional.
// If not set, the attribution logic will fall back to using usernames as creator IDs.
func (b *DefaultAttributionLogicBuilder) SetUserIDResolver(value UserIDResolver) *DefaultAttributionLogicBuilder {
	b.userIDResolver = value
	return b
}

// Build creates the default attribution logic that looks up the user and returns their ID as the creator.
func (b *DefaultAttributionLogicBuilder) Build() (result *DefaultAttributionLogic, err error) {
	// Check that the logger has been set:
	if b.logger == nil {
		err = fmt.Errorf("logger is mandatory")
		return
	}

	// Create the attribution logic:
	result = &DefaultAttributionLogic{
		logger:         b.logger,
		userIDResolver: b.userIDResolver,
	}
	return
}

// DetermineAssignedCreator looks up the authenticated user and returns their ID as the creator.
// Falls back to returning the username if no user ID resolver is configured or if the resolution fails.
func (l *DefaultAttributionLogic) DetermineAssignedCreator(ctx context.Context) (result string, err error) {
	subject := SubjectFromContext(ctx)
	username := subject.User

	// If no user ID resolver is configured, fall back to username
	if l.userIDResolver == nil {
		l.logger.DebugContext(ctx, "No user ID resolver configured, using username as creator",
			slog.String("!username", username),
		)
		result = username
		return
	}

	// Try to resolve the username to a user ID
	// JIT provisioning runs before this, so regular users should already exist
	userID, resolveErr := l.userIDResolver.GetID(ctx, username)
	if resolveErr != nil {
		// Log the error but fall back to username
		// This handles service accounts and system users that don't have User objects
		l.logger.WarnContext(ctx, "Failed to get user ID for attribution, using username as creator",
			slog.String("!username", username),
			slog.Any("error", resolveErr),
		)
		result = username
		return
	}

	if userID == "" {
		// User doesn't exist - fall back to username
		// This can happen for service accounts or if JIT provisioning was skipped
		l.logger.DebugContext(ctx, "User not found for attribution, using username as creator",
			slog.String("!username", username),
		)
		result = username
		return
	}

	// Found the user - return their ID
	result = userID
	return
}
