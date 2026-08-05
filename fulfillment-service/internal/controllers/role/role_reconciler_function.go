/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package role

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/fulfillment-service/internal/masks"
)

// FunctionBuilder contains the data needed to build instances of the reconciler function.
type FunctionBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
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

	result = &function{
		logger:      b.logger,
		rolesClient: privatev1.NewRolesClient(b.connection),
		maskCalculator: masks.NewCalculator().
			Build(),
	}
	return
}

// function is the implementation of the reconciler function.
type function struct {
	logger         *slog.Logger
	rolesClient    privatev1.RolesClient
	maskCalculator *masks.Calculator
}

// Run executes the reconciliation logic for the given role.
func (r *function) Run(ctx context.Context, role *privatev1.Role) error {
	oldRole := proto.Clone(role).(*privatev1.Role)

	task := &task{
		r:    r,
		role: role,
	}

	var err error
	if role.HasMetadata() && role.GetMetadata().HasDeletionTimestamp() {
		err = task.delete(ctx)
	} else {
		err = task.update(ctx)
	}
	if err != nil {
		return err
	}

	updateMask := r.maskCalculator.Calculate(oldRole, role)

	if len(updateMask.GetPaths()) > 0 {
		_, err = r.rolesClient.Update(ctx, privatev1.RolesUpdateRequest_builder{
			Object:     role,
			UpdateMask: updateMask,
		}.Build())
	}

	return err
}

// task contains the data needed to reconcile a single role.
type task struct {
	r    *function
	role *privatev1.Role
}

func (t *task) update(ctx context.Context) error {
	if t.addFinalizer() {
		return nil
	}

	t.setDefaults()

	t.r.logger.InfoContext(
		ctx,
		"Reconciling role",
		slog.Any("role", t.role),
	)

	return nil
}

func (t *task) delete(ctx context.Context) error {
	t.r.logger.InfoContext(
		ctx,
		"Reconciling deleted role",
		slog.Any("role", t.role),
	)

	t.removeFinalizer()
	return nil
}

func (t *task) setDefaults() {
	if !t.role.HasStatus() {
		t.role.SetStatus(&privatev1.RoleStatus{})
	}
	if t.role.GetStatus().GetState() == privatev1.RoleState_ROLE_STATE_UNSPECIFIED {
		t.role.GetStatus().SetState(privatev1.RoleState_ROLE_STATE_PENDING)
	}
}

func (t *task) addFinalizer() bool {
	if !t.role.HasMetadata() {
		t.role.SetMetadata(&privatev1.Metadata{})
	}
	list := t.role.GetMetadata().GetFinalizers()
	if !slices.Contains(list, finalizers.Controller) {
		list = append(list, finalizers.Controller)
		t.role.GetMetadata().SetFinalizers(list)
		return true
	}
	return false
}

func (t *task) removeFinalizer() {
	if !t.role.HasMetadata() {
		return
	}
	list := t.role.GetMetadata().GetFinalizers()
	if slices.Contains(list, finalizers.Controller) {
		list = slices.DeleteFunc(list, func(item string) bool {
			return item == finalizers.Controller
		})
		t.role.GetMetadata().SetFinalizers(list)
	}
}
