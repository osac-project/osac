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

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateQuotaThresholdsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.QuotaThresholdsServer = (*PrivateQuotaThresholdsServer)(nil)

// PrivateQuotaThresholdsServer serves the warning thresholds. A threshold watches one dimension
// at one scope, a project or the whole tenant, and requires a limit on the same dimension at that
// scope or one above it, so a warning always has a limit to be measured against.
type PrivateQuotaThresholdsServer struct {
	privatev1.UnimplementedQuotaThresholdsServer

	logger      *slog.Logger
	generic     *GenericServer[*privatev1.QuotaThreshold]
	limitsDao   *dao.GenericDAO[*privatev1.QuotaLimit]
	defaultsDao *dao.GenericDAO[*privatev1.QuotaDefaultLimit]
}

func NewPrivateQuotaThresholdsServer() *PrivateQuotaThresholdsServerBuilder {
	return &PrivateQuotaThresholdsServerBuilder{}
}

func (b *PrivateQuotaThresholdsServerBuilder) SetLogger(value *slog.Logger) *PrivateQuotaThresholdsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateQuotaThresholdsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateQuotaThresholdsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateQuotaThresholdsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateQuotaThresholdsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateQuotaThresholdsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateQuotaThresholdsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateQuotaThresholdsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateQuotaThresholdsServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateQuotaThresholdsServerBuilder) Build() (result *PrivateQuotaThresholdsServer, err error) {
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
	limitsDao, err := dao.NewGenericDAO[*privatev1.QuotaLimit]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}
	defaultsDao, err := dao.NewGenericDAO[*privatev1.QuotaDefaultLimit]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}
	generic, err := NewGenericServer[*privatev1.QuotaThreshold]().
		SetLogger(b.logger).
		SetService(privatev1.QuotaThresholds_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}
	result = &PrivateQuotaThresholdsServer{
		logger:      b.logger,
		generic:     generic,
		limitsDao:   limitsDao,
		defaultsDao: defaultsDao,
	}
	return
}

func (s *PrivateQuotaThresholdsServer) List(ctx context.Context,
	request *privatev1.QuotaThresholdsListRequest) (response *privatev1.QuotaThresholdsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateQuotaThresholdsServer) Get(ctx context.Context,
	request *privatev1.QuotaThresholdsGetRequest) (response *privatev1.QuotaThresholdsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateQuotaThresholdsServer) Create(ctx context.Context,
	request *privatev1.QuotaThresholdsCreateRequest) (response *privatev1.QuotaThresholdsCreateResponse, err error) {
	threshold := request.GetObject()
	if err = validateQuotaDimension(threshold.GetSpec().GetDimension()); err != nil {
		return
	}
	// The plain create path runs no protovalidate in-process, so the percent's presence and
	// bounds are checked here as the gateway's interceptor would check them.
	if !threshold.GetSpec().HasPercent() ||
		threshold.GetSpec().GetPercent() < 1 || threshold.GetSpec().GetPercent() > 100 {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "threshold percent is mandatory and must be 1 to 100")
		return
	}
	if err = validateQuotaScope(threshold.GetSpec(), threshold.GetMetadata().GetProject()); err != nil {
		return
	}
	fillQuotaName(threshold, threshold.GetSpec().GetDimension())
	err = s.generic.CreateWithCandidatePreparation(ctx, request, &response,
		func(ctx context.Context, _, candidate *privatev1.QuotaThreshold) error {
			return s.checkScopeLimit(ctx, candidate)
		})
	return
}

func (s *PrivateQuotaThresholdsServer) Update(ctx context.Context,
	request *privatev1.QuotaThresholdsUpdateRequest) (response *privatev1.QuotaThresholdsUpdateResponse, err error) {
	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}
	err = s.generic.UpdateWithValidation(ctx, request, &response, s.validateThresholdUpdate)
	return
}

func (s *PrivateQuotaThresholdsServer) Delete(ctx context.Context,
	request *privatev1.QuotaThresholdsDeleteRequest) (response *privatev1.QuotaThresholdsDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateQuotaThresholdsServer) Signal(ctx context.Context,
	request *privatev1.QuotaThresholdsSignalRequest) (response *privatev1.QuotaThresholdsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

// checkScopeLimit refuses a threshold on a dimension whose scope resolves to no limit, so a
// warning always has a limit to be measured against. The scope resolves the way the usage report
// resolves it: the scope's own limit, the nearest ancestor project's, the tenant's override, the
// default.
func (s *PrivateQuotaThresholdsServer) checkScopeLimit(ctx context.Context,
	threshold *privatev1.QuotaThreshold) error {
	metadata := threshold.GetMetadata()
	overrides, projects, err := readQuotaLimits(ctx, s.limitsDao, s.logger, metadata.GetTenant())
	if err != nil {
		return err
	}
	defaults, err := quotaDefaultValues(ctx, s.defaultsDao)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to read the default quota limits",
			slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to validate the threshold")
	}
	dimension := threshold.GetSpec().GetDimension()
	if _, ok := resolveQuotaLimitAt(
		overrides, defaults, projects, metadata.GetProject(), dimension); ok {
		return nil
	}
	if metadata.GetProject() == "" {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"the tenant has no limit of dimension '%s', so it can hold no threshold of it", dimension)
	}
	return grpcstatus.Errorf(grpccodes.FailedPrecondition,
		"project '%s' has no limit of dimension '%s', so it can hold no threshold of it",
		metadata.GetProject(), dimension)
}

// validateThresholdUpdate rejects a change to a threshold's identity, a threshold whose scope
// lost its limit, and a threshold with no percent or one out of bounds.
func (s *PrivateQuotaThresholdsServer) validateThresholdUpdate(
	ctx context.Context, candidate, current *privatev1.QuotaThreshold) error {
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
	// The update path validates the merged candidate, so the percent's presence and bounds are
	// checked here as Create checks them: a mask that clears the percent must not silently
	// leave a threshold that never warns.
	if !candidate.GetSpec().HasPercent() ||
		candidate.GetSpec().GetPercent() < 1 || candidate.GetSpec().GetPercent() > 100 {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "threshold percent is mandatory and must be 1 to 100")
	}
	return s.checkScopeLimit(ctx, candidate)
}
