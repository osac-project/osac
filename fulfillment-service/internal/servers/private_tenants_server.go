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
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
	"github.com/osac-project/fulfillment-service/internal/events"
)

type PrivateTenantsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	defaultNetworking *DefaultNetworkingProvisioner
}

var _ privatev1.TenantsServer = (*PrivateTenantsServer)(nil)

type PrivateTenantsServer struct {
	privatev1.UnimplementedTenantsServer
	logger            *slog.Logger
	generic           *GenericServer[*privatev1.Tenant]
	dao               *dao.GenericDAO[*privatev1.Tenant]
	defaultNetworking *DefaultNetworkingProvisioner
}

func NewPrivateTenantsServer() *PrivateTenantsServerBuilder {
	return &PrivateTenantsServerBuilder{}
}

func (b *PrivateTenantsServerBuilder) SetLogger(value *slog.Logger) *PrivateTenantsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateTenantsServerBuilder) SetNotifier(value events.Notifier) *PrivateTenantsServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateTenantsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateTenantsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateTenantsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateTenantsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateTenantsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateTenantsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateTenantsServerBuilder) SetDefaultNetworkingProvisioner(value *DefaultNetworkingProvisioner) *PrivateTenantsServerBuilder {
	b.defaultNetworking = value
	return b
}

func (b *PrivateTenantsServerBuilder) Build() (result *PrivateTenantsServer, err error) {
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
	generic, err := NewGenericServer[*privatev1.Tenant]().
		SetLogger(b.logger).
		SetService(privatev1.Tenants_ServiceDesc.ServiceName).
		SetTableName("tenants").
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the DAO:
	dao, err := dao.NewGenericDAO[*privatev1.Tenant]().
		SetLogger(b.logger).
		SetTableName("tenants").
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &PrivateTenantsServer{
		logger:            b.logger,
		generic:           generic,
		dao:               dao,
		defaultNetworking: b.defaultNetworking,
	}
	return
}

func (s *PrivateTenantsServer) List(ctx context.Context,
	request *privatev1.TenantsListRequest) (response *privatev1.TenantsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateTenantsServer) Get(ctx context.Context,
	request *privatev1.TenantsGetRequest) (response *privatev1.TenantsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateTenantsServer) Create(ctx context.Context,
	request *privatev1.TenantsCreateRequest) (response *privatev1.TenantsCreateResponse, err error) {
	// For tenants the name is mandatory:
	object := request.GetObject()
	metadata := object.GetMetadata()
	name := metadata.GetName()
	if name == "" {
		err = grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"field 'metadata.name' is mandatory",
		)
		return
	}

	// For tenants the identifier must be empty or equal to the name. If it is empty it will be set to the name.
	id := object.GetId()
	if id != "" && id != name {
		err = grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"field 'id' must be empty or equal to field 'metadata.name'",
		)
		return
	}
	if id == "" {
		object.SetId(name)
	}

	// The tenant of a tenant must be itself, so either empty or equal to the name. If it is empty it will be set to
	// the name.
	tenant := metadata.GetTenant()
	if tenant != "" && tenant != name {
		err = grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"field 'metadata.tenant' must be empty or equal to field 'metadata.name'",
		)
		return
	}
	if tenant == "" {
		metadata.SetTenant(name)
	}

	// Generate break-glass credentials so they are persisted temporarily and
	// returned in the Create response. The reconciler will read the password
	// when creating the Keycloak account and then clear it from the database.
	password, genErr := generatePassword()
	if genErr != nil {
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to generate break-glass password: %v", genErr)
		return
	}
	if !object.HasStatus() {
		object.SetStatus(&privatev1.TenantStatus{})
	}
	object.GetStatus().SetBreakGlassCredentials(
		privatev1.BreakGlassCredentials_builder{
			Username: fmt.Sprintf("%s-osac-break-glass", name),
			Password: password,
		}.Build(),
	)

	// Domain validation is now handled by protovalidate in the interceptor
	// Delegate to the generic server:
	err = s.generic.Create(ctx, request, &response)
	if err != nil {
		return
	}

	if s.defaultNetworking != nil {
		if provisionErr := s.defaultNetworking.Provision(ctx, name); provisionErr != nil {
			s.logger.ErrorContext(ctx, "Failed to provision default networking",
				slog.String("tenant", name),
				slog.Any("error", provisionErr))
			err = grpcstatus.Errorf(grpccodes.Internal,
				"failed to provision default networking resources")
			return
		}
	}

	return
}

func (s *PrivateTenantsServer) Update(ctx context.Context,
	request *privatev1.TenantsUpdateRequest) (response *privatev1.TenantsUpdateResponse, err error) {
	// Domain validation is now handled by protovalidate after update_mask merge in generic server
	// Delegate to the generic server:
	err = s.generic.Update(ctx, request, &response)
	if err == nil {
		stripBreakGlassCredentials(response.GetObject())
	}
	return
}

func (s *PrivateTenantsServer) Delete(ctx context.Context,
	request *privatev1.TenantsDeleteRequest) (response *privatev1.TenantsDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateTenantsServer) Signal(ctx context.Context,
	request *privatev1.TenantsSignalRequest) (response *privatev1.TenantsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
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

func stripBreakGlassCredentials(tenant *privatev1.Tenant) {
	if tenant.HasStatus() && tenant.GetStatus().HasBreakGlassCredentials() {
		tenant.GetStatus().ClearBreakGlassCredentials()
	}
}

// Domain validation has been migrated to protovalidate constraints in tenant_type.proto.
// The following validations are now handled declaratively:
// - Non-empty domains (min_len: 1)
// - Max length 253 chars (max_len: 253)
// - Not an IP address (CEL: not_ip_address)
// - At least two labels (CEL: min_two_labels, checks for '.')
// - Valid DNS labels (CEL: valid_dns_labels, validates each segment)
// - No duplicates (unique: true)
