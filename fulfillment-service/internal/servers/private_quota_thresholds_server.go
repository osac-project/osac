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
	"fmt"
	"log/slog"
	"strconv"

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

// PrivateQuotaThresholdsServer serves the warning thresholds. A threshold is always a project's
// threshold, and it requires a limit on the same dimension in the same project, so a warning
// always has a limit to be measured against.
type PrivateQuotaThresholdsServer struct {
	privatev1.UnimplementedQuotaThresholdsServer

	logger    *slog.Logger
	generic   *GenericServer[*privatev1.QuotaThreshold]
	limitsDao *dao.GenericDAO[*privatev1.QuotaLimit]
	quota     *quotaEnforcer
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
		logger:    b.logger,
		generic:   generic,
		limitsDao: limitsDao,
	}
	result.quota, err = newQuotaEnforcer(b.logger, b.metricsRegisterer)
	if err != nil {
		return
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
	if threshold.GetMetadata().GetProject() == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'metadata.project' is required: a threshold is a project's threshold")
		return
	}
	fillQuotaName(threshold, threshold.GetSpec().GetDimension())
	err = s.generic.CreateWithCandidatePreparation(ctx, request, &response,
		func(ctx context.Context, _, candidate *privatev1.QuotaThreshold) error {
			return s.checkProjectLimit(ctx, candidate)
		})
	if err == nil {
		created := response.GetObject()
		err = s.warnThresholdChange(ctx, created, 0)
	}
	return
}

func (s *PrivateQuotaThresholdsServer) Update(ctx context.Context,
	request *privatev1.QuotaThresholdsUpdateRequest) (response *privatev1.QuotaThresholdsUpdateResponse, err error) {
	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}
	// Read the old value with the row locked, so a concurrent update can't change it between
	// this read and the update below:
	getResponse, err := s.generic.dao.Get().SetId(id).SetLock(true).Do(ctx)
	if err != nil {
		err = ConvertDAOErrorToGRPC(err, "get", id)
		return
	}
	oldValue := getResponse.GetObject().GetSpec().GetValue()
	err = s.generic.UpdateWithValidation(ctx, request, &response, s.validateThresholdUpdate)
	if err == nil {
		err = s.warnThresholdChange(ctx, response.GetObject(), oldValue)
	}
	return
}

// warnThresholdChange writes a warning when setting the threshold puts its project at or above
// it: the usage reached the new value and was below the old one.
func (s *PrivateQuotaThresholdsServer) warnThresholdChange(
	ctx context.Context, threshold *privatev1.QuotaThreshold, oldValue int64) error {
	metadata := threshold.GetMetadata()
	warnErr := s.quota.warnThresholdChange(ctx, metadata.GetTenant(), metadata.GetProject(),
		threshold.GetSpec().GetDimension(), oldValue, threshold.GetSpec().GetValue())
	if warnErr != nil {
		s.logger.ErrorContext(ctx, "Failed to write the quota warning of a threshold change",
			slog.Any("error", warnErr))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to write the quota warning")
	}
	return nil
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

// checkProjectLimit refuses a threshold on a dimension whose project has no limit, so a warning
// always has a limit to be measured against.
func (s *PrivateQuotaThresholdsServer) checkProjectLimit(ctx context.Context,
	threshold *privatev1.QuotaThreshold) error {
	metadata := threshold.GetMetadata()
	filter := fmt.Sprintf(
		"this.spec.dimension == %s && this.metadata.tenant == %s && this.metadata.project == %s",
		strconv.Quote(threshold.GetSpec().GetDimension()),
		strconv.Quote(metadata.GetTenant()),
		strconv.Quote(metadata.GetProject()),
	)
	response, err := s.limitsDao.List().
		SetFilter(filter).
		SetLimit(1).
		Do(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to query the quota limits of the project",
			slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to validate the threshold")
	}
	if len(response.GetItems()) == 0 {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"project '%s' has no limit of dimension '%s', so it can hold no threshold of it",
			metadata.GetProject(), threshold.GetSpec().GetDimension())
	}
	return nil
}

// validateThresholdUpdate rejects a change to a threshold's identity, and a threshold whose
// project lost its limit.
func (s *PrivateQuotaThresholdsServer) validateThresholdUpdate(
	ctx context.Context, candidate, current *privatev1.QuotaThreshold) error {
	if candidate.GetSpec().GetDimension() != current.GetSpec().GetDimension() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.dimension' is immutable and cannot be changed from '%s' to '%s'",
			current.GetSpec().GetDimension(), candidate.GetSpec().GetDimension())
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
	return s.checkProjectLimit(ctx, candidate)
}
