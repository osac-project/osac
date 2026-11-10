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

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type QuotaUsageServerBuilder struct {
	logger            *slog.Logger
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.QuotaUsageServer = (*QuotaUsageServer)(nil)

// QuotaUsageServer is the public face of the quota usage. It adds the tenant boundary the report
// itself doesn't have: no tenant reads another tenant's usage.
type QuotaUsageServer struct {
	publicv1.UnimplementedQuotaUsageServer

	logger       *slog.Logger
	tenancyLogic auth.TenancyLogic
	delegate     privatev1.QuotaUsageServer
	outMapper    *GenericMapper[*privatev1.QuotaUsageReport, *publicv1.QuotaUsageReport]
}

func NewQuotaUsageServer() *QuotaUsageServerBuilder {
	return &QuotaUsageServerBuilder{}
}

func (b *QuotaUsageServerBuilder) SetLogger(value *slog.Logger) *QuotaUsageServerBuilder {
	b.logger = value
	return b
}

func (b *QuotaUsageServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *QuotaUsageServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *QuotaUsageServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *QuotaUsageServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *QuotaUsageServerBuilder) Build() (result *QuotaUsageServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.QuotaUsageReport, *publicv1.QuotaUsageReport]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}
	delegate, err := NewPrivateQuotaUsageServer().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}
	result = &QuotaUsageServer{
		logger:       b.logger,
		tenancyLogic: b.tenancyLogic,
		delegate:     delegate,
		outMapper:    outMapper,
	}
	return
}

func (s *QuotaUsageServer) Get(ctx context.Context,
	request *publicv1.QuotaUsageGetRequest) (response *publicv1.QuotaUsageGetResponse, err error) {
	tenant := request.GetTenant()
	if tenant == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "tenant is mandatory")
		return
	}
	if err = s.checkTenantVisible(ctx, tenant); err != nil {
		return
	}
	privateRequest := &privatev1.QuotaUsageGetRequest{}
	privateRequest.SetTenant(tenant)
	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	report := &publicv1.QuotaUsageReport{}
	if err = s.outMapper.Copy(ctx, privateResponse.GetReport(), report); err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private quota usage report to public", slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process quota usage report")
	}
	response = &publicv1.QuotaUsageGetResponse{}
	response.SetReport(report)
	return
}

// checkTenantVisible refuses the usage of a tenant the caller can't see, so no tenant reads
// another tenant's usage.
func (s *QuotaUsageServer) checkTenantVisible(ctx context.Context, tenant string) error {
	visibility, err := s.tenancyLogic.DetermineVisibility(ctx)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.Internal, "failed to determine the visible tenants")
	}
	if visibility.IsTotal() || visibility.IsTenantVisible(tenant) {
		return nil
	}
	return grpcstatus.Errorf(grpccodes.PermissionDenied, "tenant '%s' doesn't exist", tenant)
}
