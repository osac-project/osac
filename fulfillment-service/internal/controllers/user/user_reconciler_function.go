/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package user

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"google.golang.org/grpc"
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

// SetIdpClient sets the IDP client that the reconciler will use to look up users in the identity provider.
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
		logger:         b.logger,
		usersClient:    privatev1.NewUsersClient(b.connection),
		idpClient:      b.idpClient,
		maskCalculator: masks.NewCalculator().Build(),
	}
	return
}

// function is the implementation of the reconciler function.
type function struct {
	logger         *slog.Logger
	usersClient    privatev1.UsersClient
	idpClient      idp.ClientInterface
	maskCalculator *masks.Calculator
}

// Run executes the reconciliation logic for the given user.
func (r *function) Run(ctx context.Context, user *privatev1.User) error {
	oldUser := proto.Clone(user).(*privatev1.User)

	task := &task{
		r:    r,
		user: user,
	}

	var err error
	if user.HasMetadata() && user.GetMetadata().HasDeletionTimestamp() {
		err = task.delete(ctx)
	} else {
		err = task.reconcile(ctx)
	}
	if err != nil {
		return err
	}

	updateMask := r.maskCalculator.Calculate(oldUser, user)

	if len(updateMask.GetPaths()) > 0 {
		_, err = r.usersClient.Update(ctx, privatev1.UsersUpdateRequest_builder{
			Object:     user,
			UpdateMask: updateMask,
		}.Build())
	}

	return err
}

// task contains the data needed to reconcile a single user.
type task struct {
	r    *function
	user *privatev1.User
}

// reconcile performs the reconciliation logic for a user.
func (t *task) reconcile(ctx context.Context) error {
	if t.addFinalizer() {
		return nil
	}

	// Ensure status exists
	if !t.user.HasStatus() {
		t.user.SetStatus(&privatev1.UserStatus{})
	}

	// If keycloak_user_id is already set, nothing to do
	if t.user.GetStatus().GetKeycloakUserId() != "" {
		return nil
	}

	// Look up the user in Keycloak by username
	username := t.user.GetSpec().GetUsername()
	if username == "" {
		t.r.logger.WarnContext(ctx, "User has no username, cannot look up keycloak_user_id",
			slog.String("user_id", t.user.GetId()),
		)
		return nil
	}

	tenant := t.user.GetMetadata().GetTenant()
	if tenant == "" {
		t.r.logger.WarnContext(ctx, "User has no tenant, cannot look up keycloak_user_id",
			slog.String("user_id", t.user.GetId()),
			slog.String("username", username),
		)
		return nil
	}

	// Look up the user in the IDP
	keycloakUser, err := t.r.idpClient.GetUserByUsername(ctx, tenant, username)
	if err != nil {
		// Return error to trigger retry on transient failures (network errors, etc.)
		t.r.logger.ErrorContext(ctx, "Failed to look up user in IDP",
			slog.String("user_id", t.user.GetId()),
			slog.String("username", username),
			slog.String("tenant", tenant),
			slog.Any("error", err),
		)
		return err
	}

	// If user not found in IDP, nothing to do
	// This is not an error - the user might be created in IDP later
	if keycloakUser == nil {
		t.r.logger.DebugContext(ctx, "User not found in IDP",
			slog.String("user_id", t.user.GetId()),
			slog.String("username", username),
			slog.String("tenant", tenant),
		)
		return nil
	}

	// Set the keycloak_user_id in status
	t.user.GetStatus().SetKeycloakUserId(keycloakUser.ID)

	t.r.logger.InfoContext(ctx, "Set keycloak_user_id for user",
		slog.String("user_id", t.user.GetId()),
		slog.String("username", username),
		slog.String("keycloak_user_id", keycloakUser.ID),
	)

	return nil
}

// delete performs the deletion cleanup for a user.
func (t *task) delete(ctx context.Context) error {
	keycloakUserID := t.user.GetStatus().GetKeycloakUserId()
	tenant := t.user.GetMetadata().GetTenant()
	username := t.user.GetSpec().GetUsername()

	if tenant != "" {
		if keycloakUserID == "" && username != "" {
			keycloakUser, err := t.r.idpClient.GetUserByUsername(ctx, tenant, username)
			if err != nil {
				return fmt.Errorf("failed to look up user in IDP for deletion: %w", err)
			}
			if keycloakUser != nil {
				keycloakUserID = keycloakUser.ID
			}
		}

		if keycloakUserID != "" {
			err := t.r.idpClient.DeleteUser(ctx, tenant, keycloakUserID)
			if err != nil {
				return fmt.Errorf("failed to delete user from IDP: %w", err)
			}

			t.r.logger.DebugContext(ctx, "Deleted user from IDP",
				slog.String("!user_id", t.user.GetId()),
				slog.String("!keycloak_user_id", keycloakUserID),
				slog.String("!tenant", tenant),
			)
		}
	}

	t.removeFinalizer()
	return nil
}

// addFinalizer adds the controller finalizer to the user if not already present.
// Returns true if the finalizer was added (indicating the update should be saved immediately).
func (t *task) addFinalizer() bool {
	if !t.user.HasMetadata() {
		t.user.SetMetadata(&privatev1.Metadata{})
	}
	list := t.user.GetMetadata().GetFinalizers()
	if !slices.Contains(list, finalizers.Controller) {
		list = append(list, finalizers.Controller)
		t.user.GetMetadata().SetFinalizers(list)
		return true
	}
	return false
}

// removeFinalizer removes the controller finalizer from the user.
func (t *task) removeFinalizer() {
	if !t.user.HasMetadata() {
		return
	}
	list := t.user.GetMetadata().GetFinalizers()
	if slices.Contains(list, finalizers.Controller) {
		list = slices.DeleteFunc(list, func(item string) bool {
			return item == finalizers.Controller
		})
		t.user.GetMetadata().SetFinalizers(list)
	}
}
