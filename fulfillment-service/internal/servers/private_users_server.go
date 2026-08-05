/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/events"
)

type PrivateUsersServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.UsersServer = (*PrivateUsersServer)(nil)

type PrivateUsersServer struct {
	privatev1.UnimplementedUsersServer
	logger  *slog.Logger
	generic *GenericServer[*privatev1.User]
}

func NewPrivateUsersServer() *PrivateUsersServerBuilder {
	return &PrivateUsersServerBuilder{}
}

func (b *PrivateUsersServerBuilder) SetLogger(value *slog.Logger) *PrivateUsersServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateUsersServerBuilder) SetNotifier(value events.Notifier) *PrivateUsersServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateUsersServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateUsersServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateUsersServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateUsersServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateUsersServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateUsersServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateUsersServerBuilder) Build() (result *PrivateUsersServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Create the generic server:
	generic, err := NewGenericServer[*privatev1.User]().
		SetLogger(b.logger).
		SetService(privatev1.Users_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &PrivateUsersServer{
		logger:  b.logger,
		generic: generic,
	}
	return
}

func (s *PrivateUsersServer) List(ctx context.Context,
	request *privatev1.UsersListRequest) (response *privatev1.UsersListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	if err != nil {
		return
	}
	for _, item := range response.GetItems() {
		s.pruneCredentials(item)
	}
	return
}

func (s *PrivateUsersServer) Get(ctx context.Context,
	request *privatev1.UsersGetRequest) (response *privatev1.UsersGetResponse, err error) {
	// Note that credentials are not pruned here because controllers need them to sync users with the identity
	// provider. The public server prunes credentials independently before returning them to clients.
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateUsersServer) Create(ctx context.Context,
	request *privatev1.UsersCreateRequest) (response *privatev1.UsersCreateResponse, err error) {
	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateUsersServer) Update(ctx context.Context,
	request *privatev1.UsersUpdateRequest) (response *privatev1.UsersUpdateResponse, err error) {
	// Note that credentials are not pruned here because controllers need them to sync users with the identity
	// provider. The public server prunes credentials independently before returning them to clients.
	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateUsersServer) Delete(ctx context.Context,
	request *privatev1.UsersDeleteRequest) (response *privatev1.UsersDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateUsersServer) Signal(ctx context.Context,
	request *privatev1.UsersSignalRequest) (response *privatev1.UsersSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

// pruneCredentials removes credential data from a user object so that sensitive information like passwords is never
// returned in responses.
func (s *PrivateUsersServer) pruneCredentials(user *privatev1.User) {
	if user.GetSpec() != nil {
		user.GetSpec().ClearCredentials()
	}
}
