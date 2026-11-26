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
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateQuotaDefaultLimitsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
	scanner           *QuotaDefaultLimitScanner
}

var _ privatev1.QuotaDefaultLimitsServer = (*PrivateQuotaDefaultLimitsServer)(nil)

// PrivateQuotaDefaultLimitsServer serves the default limits, which belong to no tenant, through
// the generic machinery. One default exists per dimension: creating one for a dimension that
// already has one fails, an update changes the value, and deleting clears the default.
type PrivateQuotaDefaultLimitsServer struct {
	privatev1.UnimplementedQuotaDefaultLimitsServer

	logger  *slog.Logger
	generic *GenericServer[*privatev1.QuotaDefaultLimit]
	scanner *QuotaDefaultLimitScanner
}

func NewPrivateQuotaDefaultLimitsServer() *PrivateQuotaDefaultLimitsServerBuilder {
	return &PrivateQuotaDefaultLimitsServerBuilder{}
}

func (b *PrivateQuotaDefaultLimitsServerBuilder) SetLogger(value *slog.Logger) *PrivateQuotaDefaultLimitsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateQuotaDefaultLimitsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateQuotaDefaultLimitsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateQuotaDefaultLimitsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateQuotaDefaultLimitsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateQuotaDefaultLimitsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateQuotaDefaultLimitsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateQuotaDefaultLimitsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateQuotaDefaultLimitsServerBuilder {
	b.filterDesc = value
	return b
}

// SetScanner sets the worker that writes the warnings a default limit change leaves behind. It
// is optional: without a scanner the server still changes defaults, but schedules no scan, as
// the unit specs that only exercise the API do.
func (b *PrivateQuotaDefaultLimitsServerBuilder) SetScanner(value *QuotaDefaultLimitScanner) *PrivateQuotaDefaultLimitsServerBuilder {
	b.scanner = value
	return b
}

func (b *PrivateQuotaDefaultLimitsServerBuilder) Build() (result *PrivateQuotaDefaultLimitsServer, err error) {
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
	generic, err := NewGenericServer[*privatev1.QuotaDefaultLimit]().
		SetLogger(b.logger).
		SetService(privatev1.QuotaDefaultLimits_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		// A default belongs to the shared tenant, the way every platform-scoped kind does:
		AddAllowedTenants(auth.SharedTenant).
		Build()
	if err != nil {
		return
	}
	result = &PrivateQuotaDefaultLimitsServer{
		logger:  b.logger,
		generic: generic,
		scanner: b.scanner,
	}
	return
}

func (s *PrivateQuotaDefaultLimitsServer) List(ctx context.Context,
	request *privatev1.QuotaDefaultLimitsListRequest) (response *privatev1.QuotaDefaultLimitsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateQuotaDefaultLimitsServer) Get(ctx context.Context,
	request *privatev1.QuotaDefaultLimitsGetRequest) (response *privatev1.QuotaDefaultLimitsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateQuotaDefaultLimitsServer) Create(ctx context.Context,
	request *privatev1.QuotaDefaultLimitsCreateRequest) (response *privatev1.QuotaDefaultLimitsCreateResponse, err error) {
	limit := request.GetObject()
	if err = validateQuotaDimension(limit.GetSpec().GetDimension()); err != nil {
		return
	}
	// The plain create path runs no protovalidate in-process, so the value's presence and
	// sign are checked here as the gateway's interceptor would check them: an omitted value
	// must not silently become the zero that refuses everything, and a negative limit would
	// refuse nothing.
	if !limit.GetSpec().HasValue() {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "limit value is mandatory")
		return
	}
	if limit.GetSpec().GetValue() < 0 {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "limit value can't be negative")
		return
	}
	fillQuotaName(limit, limit.GetSpec().GetDimension())
	// A default belongs to the shared tenant, the way every platform-scoped kind does, and its
	// project is always empty:
	if limit.GetMetadata() == nil {
		limit.SetMetadata(&privatev1.Metadata{})
	}
	limit.GetMetadata().SetTenant(auth.SharedTenant)
	limit.GetMetadata().SetProject("")
	err = s.generic.Create(ctx, request, &response)
	// The generic create returns before the write on a dry run, so the scan is the only thing
	// left to skip there; an update commits, so it refuses a dry run instead. No default
	// existed for the dimension before, so the scan compares against none.
	if err == nil && !isDryRun(ctx) {
		s.scheduleDefaultScan(ctx, response.GetObject().GetSpec().GetDimension(), 0)
	}
	return
}

func (s *PrivateQuotaDefaultLimitsServer) Update(ctx context.Context,
	request *privatev1.QuotaDefaultLimitsUpdateRequest) (response *privatev1.QuotaDefaultLimitsUpdateResponse, err error) {
	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}
	if err = rejectUpdateDryRun(ctx); err != nil {
		return
	}
	// Read the old value with the row locked, so the scan sees the value this update
	// replaces:
	getResponse, err := s.generic.dao.Get().SetId(id).SetLock(true).Do(ctx)
	if err != nil {
		err = ConvertDAOErrorToGRPC(err, "get", id)
		return
	}
	oldValue := getResponse.GetObject().GetSpec().GetValue()
	err = s.generic.UpdateWithValidation(ctx, request, &response, validateQuotaDefaultLimitUpdate)
	if err == nil {
		s.scheduleDefaultScan(ctx, response.GetObject().GetSpec().GetDimension(), oldValue)
	}
	return
}

func (s *PrivateQuotaDefaultLimitsServer) Delete(ctx context.Context,
	request *privatev1.QuotaDefaultLimitsDeleteRequest) (response *privatev1.QuotaDefaultLimitsDeleteResponse, err error) {
	var getResponse *privatev1.QuotaDefaultLimitsGetResponse
	err = s.generic.Get(ctx, privatev1.QuotaDefaultLimitsGetRequest_builder{
		Id: request.GetId(),
	}.Build(), &getResponse)
	if err != nil {
		return
	}
	limit := getResponse.GetObject()
	err = s.generic.Delete(ctx, request, &response)
	if err == nil {
		s.scheduleDefaultScan(ctx, limit.GetSpec().GetDimension(), limit.GetSpec().GetValue())
	}
	return
}

// scheduleDefaultScan queues the threshold warnings of every tenant the change tightened, after
// the change commits: a default belongs to every tenant, and no request transaction should scan
// them all. The shared worker coalesces rapid changes of one dimension and serializes the walks.
func (s *PrivateQuotaDefaultLimitsServer) scheduleDefaultScan(ctx context.Context, dimension string, oldDefault int64) {
	if s.scanner == nil {
		return
	}
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return
	}
	tx.OnCommit(func() {
		// The commit has already happened, so the scan still sees the change. The worker is
		// detached from this request, so the RPC's deadline can't kill the scan and the
		// response doesn't wait for it:
		s.scanner.schedule(s.generic.quota, dimension, oldDefault)
	})
}

func (s *PrivateQuotaDefaultLimitsServer) Signal(ctx context.Context,
	request *privatev1.QuotaDefaultLimitsSignalRequest) (response *privatev1.QuotaDefaultLimitsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

// validateQuotaDefaultLimitUpdate rejects a change to a default's identity: the dimension, tenant
// and name never change, only the value does.
func validateQuotaDefaultLimitUpdate(
	_ context.Context, candidate, current *privatev1.QuotaDefaultLimit) error {
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
	return nil
}
