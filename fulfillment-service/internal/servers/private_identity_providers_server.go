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
	"google.golang.org/protobuf/reflect/protoreflect"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
)

type PrivateIdentityProvidersServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.IdentityProvidersServer = (*PrivateIdentityProvidersServer)(nil)

type PrivateIdentityProvidersServer struct {
	privatev1.UnimplementedIdentityProvidersServer
	logger  *slog.Logger
	generic *GenericServer[*privatev1.IdentityProvider]
	dao     *dao.GenericDAO[*privatev1.IdentityProvider]
}

func NewPrivateIdentityProvidersServer() *PrivateIdentityProvidersServerBuilder {
	return &PrivateIdentityProvidersServerBuilder{}
}

func (b *PrivateIdentityProvidersServerBuilder) SetLogger(value *slog.Logger) *PrivateIdentityProvidersServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateIdentityProvidersServerBuilder) SetNotifier(value events.Notifier) *PrivateIdentityProvidersServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateIdentityProvidersServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateIdentityProvidersServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateIdentityProvidersServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateIdentityProvidersServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateIdentityProvidersServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateIdentityProvidersServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateIdentityProvidersServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateIdentityProvidersServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateIdentityProvidersServerBuilder) Build() (result *PrivateIdentityProvidersServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Create the server early so that we can use its functions to set up other objects:
	s := &PrivateIdentityProvidersServer{
		logger: b.logger,
	}

	// Create the generic server:
	s.generic, err = NewGenericServer[*privatev1.IdentityProvider]().
		SetLogger(b.logger).
		SetService(privatev1.IdentityProviders_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetRedactFunc(s.redact).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}

	// Create the DAO:
	s.dao, err = dao.NewGenericDAO[*privatev1.IdentityProvider]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Return the server:
	result = s
	return
}

// redact clears sensitive fields from the identity provider before it is included in event notification payloads.
func (s *PrivateIdentityProvidersServer) redact(
	object *privatev1.IdentityProvider) *privatev1.IdentityProvider {
	spec := object.GetSpec()
	if spec != nil {
		oidc := spec.GetOidc()
		if oidc != nil {
			oidc.SetClientSecret("")
		}
	}
	return object
}

func (s *PrivateIdentityProvidersServer) Create(ctx context.Context,
	request *privatev1.IdentityProvidersCreateRequest) (response *privatev1.IdentityProvidersCreateResponse, err error) {
	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateIdentityProvidersServer) List(ctx context.Context,
	request *privatev1.IdentityProvidersListRequest) (response *privatev1.IdentityProvidersListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateIdentityProvidersServer) Get(ctx context.Context,
	request *privatev1.IdentityProvidersGetRequest) (response *privatev1.IdentityProvidersGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateIdentityProvidersServer) Update(ctx context.Context,
	request *privatev1.IdentityProvidersUpdateRequest) (response *privatev1.IdentityProvidersUpdateResponse, err error) {
	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateIdentityProvidersServer) Delete(ctx context.Context,
	request *privatev1.IdentityProvidersDeleteRequest) (response *privatev1.IdentityProvidersDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateIdentityProvidersServer) Signal(ctx context.Context,
	request *privatev1.IdentityProvidersSignalRequest) (response *privatev1.IdentityProvidersSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}
