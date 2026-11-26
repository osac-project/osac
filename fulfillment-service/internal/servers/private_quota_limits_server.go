/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateQuotaLimitsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.QuotaLimitsServer = (*PrivateQuotaLimitsServer)(nil)

// PrivateQuotaLimitsServer serves the tenant overrides and project limits through the generic
// machinery.
type PrivateQuotaLimitsServer struct {
	privatev1.UnimplementedQuotaLimitsServer

	logger           *slog.Logger
	generic          *GenericServer[*privatev1.QuotaLimit]
	attributionLogic auth.AttributionLogic
}

func NewPrivateQuotaLimitsServer() *PrivateQuotaLimitsServerBuilder {
	return &PrivateQuotaLimitsServerBuilder{}
}

func (b *PrivateQuotaLimitsServerBuilder) SetLogger(value *slog.Logger) *PrivateQuotaLimitsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateQuotaLimitsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateQuotaLimitsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateQuotaLimitsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateQuotaLimitsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateQuotaLimitsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateQuotaLimitsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateQuotaLimitsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateQuotaLimitsServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateQuotaLimitsServerBuilder) Build() (result *PrivateQuotaLimitsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	if b.attributionLogic == nil {
		err = errors.New("attribution logic is mandatory")
		return
	}
	generic, err := NewGenericServer[*privatev1.QuotaLimit]().
		SetLogger(b.logger).
		SetService(privatev1.QuotaLimits_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}
	result = &PrivateQuotaLimitsServer{
		logger:           b.logger,
		generic:          generic,
		attributionLogic: b.attributionLogic,
	}
	return
}

func (s *PrivateQuotaLimitsServer) List(ctx context.Context,
	request *privatev1.QuotaLimitsListRequest) (response *privatev1.QuotaLimitsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateQuotaLimitsServer) Get(ctx context.Context,
	request *privatev1.QuotaLimitsGetRequest) (response *privatev1.QuotaLimitsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateQuotaLimitsServer) Create(ctx context.Context,
	request *privatev1.QuotaLimitsCreateRequest) (response *privatev1.QuotaLimitsCreateResponse, err error) {
	limit := request.GetObject()
	if err = validateQuotaDimension(limit.GetSpec().GetDimension()); err != nil {
		return
	}
	// The plain create path runs no protovalidate in-process, so the value's presence is
	// checked here as the gateway's interceptor would check it.
	if !limit.GetSpec().HasValue() {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "limit value is mandatory")
		return
	}
	if limit.GetSpec().GetValue() < 0 {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "limit value can't be negative")
		return
	}
	if err = validateQuotaScope(limit.GetSpec(), limit.GetMetadata().GetProject()); err != nil {
		return
	}
	fillQuotaName(limit, limit.GetSpec().GetDimension())
	err = s.generic.CreateWithCandidatePreparation(ctx, request, &response,
		func(ctx context.Context, _, candidate *privatev1.QuotaLimit) error {
			// The warning this create may write counts usage, so the tenant lock comes first,
			// before the write and the count, and serializes with admission:
			return s.generic.quota.lockTenant(ctx, candidate.GetMetadata().GetTenant())
		})
	// The generic create returns before the write on a dry run, so the warning is the only
	// thing left to skip there; an update commits, so it refuses a dry run instead. A dry run
	// saves nothing, so it leaves no warning: the change it would warn of never happened.
	if err == nil && !isDryRun(ctx) {
		// The stored object, not the request: the generic server assigns the tenant on its
		// own clone of the request, so only the created object reliably carries it.
		created := response.GetObject()
		err = s.warnCreatedLimit(ctx, created.GetMetadata().GetTenant(),
			created.GetMetadata().GetProject(), created.GetSpec().GetDimension())
	}
	return
}

func (s *PrivateQuotaLimitsServer) Update(ctx context.Context,
	request *privatev1.QuotaLimitsUpdateRequest) (response *privatev1.QuotaLimitsUpdateResponse, err error) {
	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}
	if err = rejectUpdateDryRun(ctx); err != nil {
		return
	}
	// Read the stored tenant before any row lock, then take the tenant lock, then lock the
	// limit's row: the tenant lock comes first, so this warning-bearing write serializes with
	// admission the same way the check does. The stored tenant never changes.
	unlocked, err := s.generic.dao.Get().SetId(id).Do(ctx)
	if err != nil {
		err = ConvertDAOErrorToGRPC(err, "get", id)
		return
	}
	if err = s.generic.quota.lockTenant(ctx, unlocked.GetObject().GetMetadata().GetTenant()); err != nil {
		return
	}
	// Read the old value with the row locked, so the warning sees the value this update
	// replaces:
	getResponse, err := s.generic.dao.Get().SetId(id).SetLock(true).Do(ctx)
	if err != nil {
		err = ConvertDAOErrorToGRPC(err, "get", id)
		return
	}
	oldValue := getResponse.GetObject().GetSpec().GetValue()
	err = s.generic.UpdateWithValidation(ctx, request, &response, validateQuotaLimitUpdate)
	if err == nil {
		updated := response.GetObject()
		err = s.warnLimitChange(ctx, updated.GetMetadata().GetTenant(),
			updated.GetMetadata().GetProject(), updated.GetSpec().GetDimension(), oldValue)
	}
	return
}

func (s *PrivateQuotaLimitsServer) Delete(ctx context.Context,
	request *privatev1.QuotaLimitsDeleteRequest) (response *privatev1.QuotaLimitsDeleteResponse, err error) {
	id := request.GetId()
	// Read the stored tenant before any row lock, then take the tenant lock, then lock the
	// limit's row, the way Update does: the tenant lock comes first, so this warning-bearing
	// write serializes with admission, and the warning reads the value the delete replaces
	// rather than one a concurrent update already changed.
	unlocked, err := s.generic.dao.Get().SetId(id).Do(ctx)
	if err != nil {
		err = ConvertDAOErrorToGRPC(err, "get", id)
		return
	}
	// The delete may leave a warning behind, which counts usage, so the tenant lock comes
	// before the delete and the count. A tenant whose deletion was accepted still removes its
	// rows here, the way its teardown runs, so this lock tolerates the deletion.
	if err = lockTenantRowForCleanup(ctx, s.generic.quota.metrics,
		unlocked.GetObject().GetMetadata().GetTenant()); err != nil {
		return
	}
	// Read the old value with the row locked, so the warning sees the value this delete
	// replaces:
	getResponse, err := s.generic.dao.Get().SetId(id).SetLock(true).Do(ctx)
	if err != nil {
		err = ConvertDAOErrorToGRPC(err, "get", id)
		return
	}
	limit := getResponse.GetObject()
	err = s.generic.Delete(ctx, request, &response)
	if err == nil {
		err = s.warnLimitChange(ctx, limit.GetMetadata().GetTenant(),
			limit.GetMetadata().GetProject(), limit.GetSpec().GetDimension(),
			limit.GetSpec().GetValue())
	}
	return
}

// warnLimitChange writes the warnings a limit change leaves behind: one for each threshold of
// the dimension, in the changed scope, whose usage is at or above it. Only a change that
// tightens the scope's limit warns. The failure is reported to the transaction, so the limit
// change it follows rolls back with it.
func (s *PrivateQuotaLimitsServer) warnLimitChange(
	ctx context.Context, tenant, project, dimension string, oldLimit int64,
) error {
	return warnOrRollback(ctx, s.logger,
		"Failed to write the quota warnings of a limit change",
		"failed to write the quota warnings",
		func() error {
			return s.generic.quota.warnScopeAtOrAbove(ctx, tenant, project, dimension, oldLimit)
		})
}

// warnCreatedLimit writes the warnings a newly created limit leaves behind. The scope's old
// limit is what it falls back to without a row: the tenant's limit for a project, the default
// for the tenant. The failure is reported to the transaction, so the created limit rolls back
// with it.
func (s *PrivateQuotaLimitsServer) warnCreatedLimit(
	ctx context.Context, tenant, project, dimension string,
) error {
	return warnOrRollback(ctx, s.logger,
		"Failed to write the quota warnings of a limit change",
		"failed to write the quota warnings",
		func() error {
			return s.generic.quota.warnCreatedLimit(ctx, tenant, project, dimension)
		})
}

func (s *PrivateQuotaLimitsServer) Signal(ctx context.Context,
	request *privatev1.QuotaLimitsSignalRequest) (response *privatev1.QuotaLimitsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

// rejectUpdateDryRun refuses a dry run of an update. The generic server honors a dry run only
// on creates, so an update would commit while the warning it should leave is skipped: a dry run
// can't preview this change, so it refuses instead of saving half of it.
func rejectUpdateDryRun(ctx context.Context) error {
	if !isDryRun(ctx) {
		return nil
	}
	return grpcstatus.Errorf(grpccodes.FailedPrecondition,
		"the dry run is not supported for this operation")
}

// validateQuotaDimension rejects a quota object whose spec names no dimension.
func validateQuotaDimension(dimension string) error {
	if dimension == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.dimension' is required")
	}
	return nil
}

// quotaScopedSpec is the spec of a quota object whose scope says whether it covers the whole
// tenant or the project the metadata names.
type quotaScopedSpec interface {
	GetScope() privatev1.QuotaScope
	HasScope() bool
}

// validateQuotaScope rejects a quota object whose spec names no scope, one whose scope only a
// refusal reports, and one whose scope disagrees with the metadata: a tenant scope keeps the
// empty project name, the tenant's default project, and a project scope requires a named one.
func validateQuotaScope(spec quotaScopedSpec, project string) error {
	if !spec.HasScope() || spec.GetScope() == privatev1.QuotaScope_QUOTA_SCOPE_UNSPECIFIED {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.scope' is required")
	}
	if spec.GetScope() == privatev1.QuotaScope_QUOTA_SCOPE_ANCESTOR_PROJECT {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.scope' must be the whole tenant or one project")
	}
	if spec.GetScope() == privatev1.QuotaScope_QUOTA_SCOPE_TENANT && project != "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'metadata.project' must be empty when the scope is the whole tenant")
	}
	if spec.GetScope() == privatev1.QuotaScope_QUOTA_SCOPE_PROJECT && project == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'metadata.project' is required when the scope is a project")
	}
	return nil
}

// quotaRequestName derives a request's name from its dimension with a unique suffix, so a new
// request of a dimension doesn't collide with a reviewed one that still holds the plain name.
func quotaRequestName(dimension string) string {
	name := quotaNameFromDimension(dimension)
	if len(name) > 54 {
		name = name[:54]
	}
	return name + "-" + uuid.NewString()[:8]
}

// fillQuotaName gives the object the name derived from its dimension when the caller sent none.
func fillQuotaName(object quotaNamedObject, dimension string) {
	metadata := object.GetMetadata()
	if metadata == nil {
		metadata = &privatev1.Metadata{}
		object.SetMetadata(metadata)
	}
	if metadata.GetName() == "" {
		metadata.SetName(quotaNameFromDimension(dimension))
	}
}

// quotaNamedObject is any quota object the generic DAO stores whose metadata carries its name.
type quotaNamedObject interface {
	dao.Object
	GetMetadata() *privatev1.Metadata
	SetMetadata(*privatev1.Metadata)
}

// validateQuotaLimitUpdate rejects a change to a limit's identity: the dimension, scope, tenant,
// project and name never change, only the value does.
func validateQuotaLimitUpdate(_ context.Context, candidate, current *privatev1.QuotaLimit) error {
	if candidate.GetSpec().GetDimension() != current.GetSpec().GetDimension() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.dimension' is immutable and cannot be changed from '%s' to '%s'",
			current.GetSpec().GetDimension(), candidate.GetSpec().GetDimension())
	}
	if candidate.GetSpec().GetScope() != current.GetSpec().GetScope() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.scope' is immutable")
	}
	if err := validateQuotaScope(candidate.GetSpec(), candidate.GetMetadata().GetProject()); err != nil {
		return err
	}
	if candidate.GetMetadata().GetTenant() != current.GetMetadata().GetTenant() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'metadata.tenant' is immutable")
	}
	if candidate.GetMetadata().GetProject() != current.GetMetadata().GetProject() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'metadata.project' is immutable")
	}
	if candidate.GetMetadata().GetName() != current.GetMetadata().GetName() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'metadata.name' is immutable")
	}
	return nil
}
