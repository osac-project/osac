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
	"fmt"
	"log/slog"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
	"github.com/osac-project/osac/fulfillment-service/internal/password"
	"github.com/osac-project/osac/fulfillment-service/internal/references"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateTenantsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	defaultNetworking *DefaultNetworkingProvisioner
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.TenantsServer = (*PrivateTenantsServer)(nil)

type PrivateTenantsServer struct {
	privatev1.UnimplementedTenantsServer
	logger            *slog.Logger
	generic           *GenericServer[*privatev1.Tenant]
	dao               *dao.GenericDAO[*privatev1.Tenant]
	secretsDao        *dao.GenericDAO[*privatev1.Secret]
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

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateTenantsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateTenantsServerBuilder {
	b.filterDesc = value
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
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}

	// Create the DAO:
	tenantsDao, err := dao.NewGenericDAO[*privatev1.Tenant]().
		SetLogger(b.logger).
		SetTableName("tenants").
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	secretsDao, err := dao.NewGenericDAO[*privatev1.Secret]().
		SetLogger(b.logger).
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
		dao:               tenantsDao,
		secretsDao:        secretsDao,
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

	if err = s.validateBreakGlassCredentialsSecret(ctx, object, false); err != nil {
		return
	}

	// Only generate break-glass credentials when the caller did NOT supply a secret reference.
	// When a reference is provided the reconciler reads the password from the Secret directly.
	if object.GetSpec().GetBreakGlassCredentialsSecret() == nil {
		pw, genErr := password.Generate()
		if genErr != nil {
			err = grpcstatus.Errorf(grpccodes.Internal, "failed to generate break-glass password: %v", genErr)
			return
		}
		if !object.HasStatus() {
			object.SetStatus(&privatev1.TenantStatus{})
		}
		creds := privatev1.BreakGlassCredentials_builder{
			Username: fmt.Sprintf("%s-osac-break-glass", name),
			Password: pw,
		}.Build()
		object.GetStatus().SetBreakGlassCredentials(creds)
	}

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
	// Only validate the break-glass Secret reference when the update actually
	// touches it. Running the lookup for partial updates that omit the field
	// would perform a needless Secret resolution and mutate the request object,
	// diverging from the mask-aware behavior of the storage backends server.
	if updateIncludesField(request.GetUpdateMask(), breakGlassCredentialsSecretField) {
		if err = s.validateBreakGlassCredentialsSecret(ctx, request.GetObject(), true); err != nil {
			return
		}
	}
	// Domain validation is now handled by protovalidate after update_mask merge in generic server
	// Delegate to the generic server:
	err = s.generic.Update(ctx, request, &response)
	if err != nil {
		return
	}
	stripBreakGlassCredentials(response.GetObject())
	if err = s.clearStaleBreakGlassCredentialsSecretRef(ctx, response.GetObject()); err != nil {
		return
	}
	return
}

func (s *PrivateTenantsServer) Delete(ctx context.Context,
	request *privatev1.TenantsDeleteRequest) (response *privatev1.TenantsDeleteResponse, err error) {
	getResponse, getErr := s.dao.Get().SetId(request.GetId()).Do(ctx)
	// Delete the break-glass secret before generic.Delete so its project and
	// tenant FKs cannot deadlock teardown (projects cannot be removed while
	// the secret exists, and the tenant cannot be removed while projects exist).
	if getErr == nil {
		tenant := getResponse.GetObject()
		s.deleteBreakGlassSecret(ctx, tenant)
		if err = s.persistBreakGlassCredentialsSecretRefClear(ctx, tenant); err != nil {
			return
		}
	}
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateTenantsServer) Signal(ctx context.Context,
	request *privatev1.TenantsSignalRequest) (response *privatev1.TenantsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

func stripBreakGlassCredentials(tenant *privatev1.Tenant) {
	if tenant.HasStatus() && tenant.GetStatus().HasBreakGlassCredentials() {
		tenant.GetStatus().ClearBreakGlassCredentials()
	}
}

const breakGlassCredentialsSecretName = "break-glass-credentials"

// breakGlassCredentialsSecretField is the update-mask path guarding break-glass
// Secret reference validation on partial updates.
const breakGlassCredentialsSecretField = "spec.break_glass_credentials_secret"

func (s *PrivateTenantsServer) deleteBreakGlassSecret(ctx context.Context, tenant *privatev1.Tenant) {
	if tenant == nil {
		return
	}
	deleted := map[string]struct{}{}
	if id := tenant.GetSpec().GetBreakGlassCredentialsSecret().GetId(); id != "" {
		s.deleteSecretByID(ctx, tenant, id)
		deleted[id] = struct{}{}
	}
	tenantName := tenant.GetMetadata().GetName()
	if tenantName == "" {
		return
	}
	filter := fmt.Sprintf("this.metadata.tenant == %s && this.metadata.name == %s",
		strconv.Quote(tenantName), strconv.Quote(breakGlassCredentialsSecretName))
	listResp, err := s.secretsDao.List().SetFilter(filter).Do(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to list break-glass credentials secrets",
			slog.String("tenant", tenantName),
			slog.Any("error", err))
		return
	}
	for _, secret := range listResp.GetItems() {
		id := secret.GetId()
		if _, ok := deleted[id]; ok {
			continue
		}
		s.deleteSecretByID(ctx, tenant, id)
	}
}

func (s *PrivateTenantsServer) deleteSecretByID(ctx context.Context, tenant *privatev1.Tenant, id string) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to get transaction for break-glass secret deletion",
			slog.String("tenant", tenant.GetMetadata().GetName()),
			slog.String("secret_id", id),
			slog.Any("error", err))
		return
	}
	// Use a savepoint so a missing/already-deleted secret does not mark the
	// outer Tenants/Delete transaction for rollback via DAO ReportError.
	err = tx.Savepoint(ctx, func(spCtx context.Context) error {
		_, err := s.secretsDao.Delete().SetId(id).Do(spCtx)
		if err != nil {
			var nf *dao.ErrNotFound
			if errors.As(err, &nf) {
				return nil
			}
			return err
		}
		return nil
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to delete break-glass credentials secret",
			slog.String("tenant", tenant.GetMetadata().GetName()),
			slog.String("secret_id", id),
			slog.Any("error", err))
	}
}

func (s *PrivateTenantsServer) clearStaleBreakGlassCredentialsSecretRef(ctx context.Context,
	tenant *privatev1.Tenant) error {
	ref := tenant.GetSpec().GetBreakGlassCredentialsSecret()
	if ref == nil {
		return nil
	}
	_, err := references.NewDAOLookupFunc(s.secretsDao)(ctx, "", "", ref.GetId(), ref.GetName())
	if err == nil {
		return nil
	}
	var nf interface{ IsNotFound() bool }
	if !errors.As(err, &nf) || !nf.IsNotFound() {
		return nil
	}
	if err = s.persistBreakGlassCredentialsSecretRefClear(ctx, tenant); err != nil {
		return err
	}
	tenant.GetSpec().ClearBreakGlassCredentialsSecret()
	return nil
}

func (s *PrivateTenantsServer) persistBreakGlassCredentialsSecretRefClear(ctx context.Context,
	tenant *privatev1.Tenant) error {
	if tenant.GetSpec().GetBreakGlassCredentialsSecret() == nil {
		return nil
	}
	updated := proto.Clone(tenant).(*privatev1.Tenant)
	updated.GetSpec().ClearBreakGlassCredentialsSecret()
	_, err := s.dao.Update().SetObject(updated).Do(ctx)
	if err != nil {
		return fmt.Errorf("failed to clear break-glass credentials secret reference: %w", err)
	}
	return nil
}

func (s *PrivateTenantsServer) validateBreakGlassCredentialsSecret(ctx context.Context,
	tenant *privatev1.Tenant, allowStaleRef bool) error {
	ref := tenant.GetSpec().GetBreakGlassCredentialsSecret()
	if ref == nil {
		return nil
	}
	if ref.GetId() == "" && ref.GetName() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"break_glass_credentials_secret must specify id or name")
	}
	resolved, err := references.NewDAOLookupFunc(s.secretsDao)(ctx, "", "", ref.GetId(), ref.GetName())
	if err != nil {
		var deniedErr *dao.ErrDenied
		if errors.As(err, &deniedErr) {
			return grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
		}
		var nf interface{ IsNotFound() bool }
		if errors.As(err, &nf) && nf.IsNotFound() {
			if allowStaleRef {
				if tenant.HasSpec() {
					tenant.GetSpec().ClearBreakGlassCredentialsSecret()
				}
				return nil
			}
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"there is no secret with identifier or name '%s'", refKey(ref))
		}
		s.logger.ErrorContext(ctx, "Failed to resolve break_glass_credentials_secret reference", "error", err)
		return grpcstatus.Errorf(grpccodes.Internal, "failed to resolve break_glass_credentials_secret reference")
	}
	if resolved.Tenant == auth.SharedTenant {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"shared secrets cannot be used as break_glass_credentials_secret references")
	}
	resolvedRef := &privatev1.SecretLocalReference{}
	resolvedRef.SetId(resolved.ID)
	resolvedRef.SetName(resolved.Name)
	if !tenant.HasSpec() {
		tenant.SetSpec(&privatev1.TenantSpec{})
	}
	tenant.GetSpec().SetBreakGlassCredentialsSecret(resolvedRef)
	return nil
}

// Domain validation has been migrated to protovalidate constraints in tenant_type.proto.
// The following validations are now handled declaratively:
// - Non-empty domains (min_len: 1)
// - Max length 253 chars (max_len: 253)
// - Not an IP address (CEL: not_ip_address)
// - At least two labels (CEL: min_two_labels, checks for '.')
// - Valid DNS labels (CEL: valid_dns_labels, validates each segment)
// - No duplicates (unique: true)
