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
	"sort"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/quotausage"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateQuotaUsageServerBuilder struct {
	logger            *slog.Logger
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.QuotaUsageServer = (*PrivateQuotaUsageServer)(nil)

// PrivateQuotaUsageServer builds the usage report of one tenant: each limit, the usage counted
// from the database, the room left, and the dimensions over their limit, for the tenant and each
// of its projects. The built-in tenants hold no quota state, so they have no report.
type PrivateQuotaUsageServer struct {
	privatev1.UnimplementedQuotaUsageServer

	logger   *slog.Logger
	defaults *quotaDefaultLimitsStore
	limits   *dao.GenericDAO[*privatev1.QuotaLimit]
	projects *dao.GenericDAO[*privatev1.Project]
	counter  *quotausage.Counter
}

func NewPrivateQuotaUsageServer() *PrivateQuotaUsageServerBuilder {
	return &PrivateQuotaUsageServerBuilder{}
}

func (b *PrivateQuotaUsageServerBuilder) SetLogger(value *slog.Logger) *PrivateQuotaUsageServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateQuotaUsageServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateQuotaUsageServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateQuotaUsageServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateQuotaUsageServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateQuotaUsageServerBuilder) Build() (result *PrivateQuotaUsageServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	// The report reads the tenant's own rows, so the DAOs see every tenant and project and the
	// public server decides which tenant the caller may ask about:
	limits, err := newGuardDAO[*privatev1.QuotaLimit](b.logger)
	if err != nil {
		return
	}
	projects, err := newGuardDAO[*privatev1.Project](b.logger)
	if err != nil {
		return
	}
	defaults, err := newQuotaDefaultLimitsStore(b.logger)
	if err != nil {
		return
	}
	counter, err := quotausage.NewCounter().
		SetLogger(b.logger).
		Build()
	if err != nil {
		return
	}
	result = &PrivateQuotaUsageServer{
		logger:   b.logger,
		defaults: defaults,
		limits:   limits,
		projects: projects,
		counter:  counter,
	}
	return
}

func (s *PrivateQuotaUsageServer) Get(ctx context.Context,
	request *privatev1.QuotaUsageGetRequest) (response *privatev1.QuotaUsageGetResponse, err error) {
	tenant := request.GetTenant()
	if tenant == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "tenant is mandatory")
		return
	}
	if tenant == auth.SharedTenant || tenant == auth.SystemTenant {
		err = grpcstatus.Errorf(grpccodes.NotFound,
			"tenant '%s' holds no quota state, so it has no usage report", tenant)
		return
	}

	report, err := s.buildReport(ctx, tenant)
	if err != nil {
		return
	}
	response = &privatev1.QuotaUsageGetResponse{}
	response.SetReport(report)
	return
}

// buildReport assembles the tenant's usage report: the limits, the counted usage, the room left
// and the over-limit marks, for the tenant and each of its projects.
func (s *PrivateQuotaUsageServer) buildReport(
	ctx context.Context, tenant string) (report *privatev1.QuotaUsageReport, err error) {
	usage, err := s.counter.Count(ctx, tenant)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to count the quota usage",
			slog.String("tenant", tenant),
			slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to count the quota usage")
	}
	defaults, err := s.defaults.values(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to read the default quota limits",
			slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to read the quota limits")
	}
	limits, projectLimits, err := s.readLimits(ctx, tenant)
	if err != nil {
		return nil, err
	}
	projectNames, err := s.readProjects(ctx, tenant)
	if err != nil {
		return nil, err
	}

	// The tenant's usage is the sum over its projects:
	tenantUsage := map[string]int64{}
	for _, dimensions := range usage {
		for dimension, amount := range dimensions {
			tenantUsage[dimension] += amount
		}
	}

	report = &privatev1.QuotaUsageReport{}
	report.SetTenant(tenant)
	report.SetDimensions(quotaUsageDimensions(tenantUsage, func(dimension string) int64 {
		return resolveQuotaLimit(limits, defaults, dimension)
	}))
	var projects []*privatev1.QuotaUsageProject
	for _, project := range projectNames {
		projects = append(projects, privatev1.QuotaUsageProject_builder{
			Project: project,
			Dimensions: quotaUsageDimensions(usage[project], func(dimension string) int64 {
				if value, ok := projectLimits[project][dimension]; ok {
					return value
				}
				return resolveQuotaLimit(limits, defaults, dimension)
			}),
		}.Build())
	}
	report.SetProjects(projects)
	return report, nil
}

// readLimits returns the tenant's overrides and each project's limits.
func (s *PrivateQuotaUsageServer) readLimits(
	ctx context.Context, tenant string) (overrides map[string]int64, projects map[string]map[string]int64, err error) {
	response, err := s.limits.List().
		SetFilter(fmt.Sprintf("this.metadata.tenant == %s", strconv.Quote(tenant))).
		Do(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to read the quota limits",
			slog.String("tenant", tenant),
			slog.Any("error", err))
		return nil, nil, grpcstatus.Errorf(grpccodes.Internal, "failed to read the quota limits")
	}
	overrides = map[string]int64{}
	projects = map[string]map[string]int64{}
	for _, limit := range response.GetItems() {
		project := limit.GetMetadata().GetProject()
		if project == "" {
			overrides[limit.GetSpec().GetDimension()] = limit.GetSpec().GetValue()
			continue
		}
		if projects[project] == nil {
			projects[project] = map[string]int64{}
		}
		projects[project][limit.GetSpec().GetDimension()] = limit.GetSpec().GetValue()
	}
	return overrides, projects, nil
}

// readProjects returns the names of the tenant's projects, its default project included.
func (s *PrivateQuotaUsageServer) readProjects(ctx context.Context, tenant string) ([]string, error) {
	response, err := s.projects.List().
		SetFilter(fmt.Sprintf("this.metadata.tenant == %s", strconv.Quote(tenant))).
		Do(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to read the tenant's projects",
			slog.String("tenant", tenant),
			slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to read the tenant's projects")
	}
	names := make([]string, 0, len(response.GetItems()))
	for _, project := range response.GetItems() {
		names = append(names, project.GetMetadata().GetName())
	}
	sort.Strings(names)
	return names, nil
}

// quotaUsageDimensions turns one scope's usage into report entries, sorted by dimension. The
// limit of each entry comes from the given resolver.
func quotaUsageDimensions(usage map[string]int64, limit func(string) int64) []*privatev1.QuotaUsageDimension {
	dimensions := make([]string, 0, len(usage))
	for dimension := range usage {
		dimensions = append(dimensions, dimension)
	}
	sort.Strings(dimensions)
	result := make([]*privatev1.QuotaUsageDimension, 0, len(dimensions))
	for _, dimension := range dimensions {
		value := limit(dimension)
		amount := usage[dimension]
		result = append(result, privatev1.QuotaUsageDimension_builder{
			Dimension: dimension,
			Limit:     value,
			Usage:     amount,
			Room:      value - amount,
			OverLimit: amount > value,
		}.Build())
	}
	return result
}

// resolveQuotaLimit applies the resolution order: the tenant's override, else the default, else 0.
func resolveQuotaLimit(overrides, defaults map[string]int64, dimension string) int64 {
	if value, ok := overrides[dimension]; ok {
		return value
	}
	if value, ok := defaults[dimension]; ok {
		return value
	}
	return 0
}
