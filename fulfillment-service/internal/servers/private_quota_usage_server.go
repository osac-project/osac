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
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.QuotaUsageServer = (*PrivateQuotaUsageServer)(nil)

// PrivateQuotaUsageServer builds the usage report of one tenant: each limit and the usage counted
// from the database, for the tenant and each of its projects. The built-in tenants hold no quota
// state, so they have no report.
type PrivateQuotaUsageServer struct {
	privatev1.UnimplementedQuotaUsageServer

	logger   *slog.Logger
	defaults *dao.GenericDAO[*privatev1.QuotaDefaultLimit]
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

func (b *PrivateQuotaUsageServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateQuotaUsageServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateQuotaUsageServerBuilder) Build() (result *PrivateQuotaUsageServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	// The report reads the tenant's own rows, so the DAOs see every tenant and project; the
	// public server decides which tenant and projects the caller may see:
	limits, err := newGuardDAO[*privatev1.QuotaLimit](b.logger)
	if err != nil {
		return
	}
	projects, err := newGuardDAO[*privatev1.Project](b.logger)
	if err != nil {
		return
	}
	defaults, err := newGuardDAO[*privatev1.QuotaDefaultLimit](b.logger)
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

// buildReport assembles the tenant's usage report: the limits and the counted usage, for the
// tenant and each of its projects.
func (s *PrivateQuotaUsageServer) buildReport(
	ctx context.Context, tenant string) (report *privatev1.QuotaUsageReport, err error) {
	usage, err := s.counter.Count(ctx, tenant)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to count the quota usage",
			slog.String("tenant", tenant),
			slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to count the quota usage")
	}
	defaults, err := quotaDefaultValues(ctx, s.defaults)
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
	// Every tenant holds at least its default project, so a tenant with no projects is a
	// tenant that doesn't exist:
	if len(projectNames) == 0 {
		return nil, grpcstatus.Errorf(grpccodes.NotFound, "tenant '%s' doesn't exist", tenant)
	}

	// The tenant's usage is the sum over its projects. The report covers every dimension
	// that has a default, an override or usage, so the dimensions with a limit and no
	// usage enter the map at zero:
	tenantUsage := map[string]int64{}
	quotaUsageSeed(tenantUsage, defaults, limits)
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
		// Each project's entries cover every dimension that has the project's own limit
		// or usage in the project. A dimension no project limit names appears through its
		// usage, priced against the tightest limit above the project:
		projectUsage := map[string]int64{}
		quotaUsageSeed(projectUsage, projectLimits[project])
		for dimension, amount := range usage[project] {
			projectUsage[dimension] += amount
		}
		projects = append(projects, privatev1.QuotaUsageProject_builder{
			Project: project,
			Dimensions: quotaUsageDimensions(projectUsage, func(dimension string) int64 {
				if value, ok := resolveQuotaLimitAt(limits, defaults, projectLimits, project, dimension); ok {
					return value
				}
				return 0
			}),
		}.Build())
	}
	report.SetProjects(projects)
	return report, nil
}

// listAllRows reads every row the filter matches, in pages: the DAO's default page size
// would silently truncate a tenant that holds more rows than it, and a report that drops
// rows understates the usage or resolves a limit against a row it never saw. The drain
// refuses a listing whose rows move under it, so the report fails loudly instead of
// quietly understating the tenant.
func listAllRows[O dao.Object](
	ctx context.Context, list *dao.GenericDAO[O], filter string,
) ([]O, error) {
	return dao.DrainPages(ctx, func(ctx context.Context, limit, offset int32) ([]O, int32, error) {
		response, err := list.List().
			SetFilter(filter).
			SetOffset(offset).
			SetLimit(limit).
			Do(ctx)
		if err != nil {
			return nil, 0, err
		}
		return response.GetItems(), response.GetTotal(), nil
	})
}

func (s *PrivateQuotaUsageServer) readLimits(
	ctx context.Context, tenant string) (overrides map[string]int64, projects map[string]map[string]int64, err error) {
	return readQuotaLimits(ctx, s.limits, s.logger, tenant)
}

// readProjects returns the names of the tenant's projects, its default project included.
func (s *PrivateQuotaUsageServer) readProjects(ctx context.Context, tenant string) ([]string, error) {
	items, err := listAllRows(ctx, s.projects, fmt.Sprintf("this.metadata.tenant == %s", strconv.Quote(tenant)))
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to read the tenant's projects",
			slog.String("tenant", tenant),
			slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to read the tenant's projects")
	}
	names := make([]string, 0, len(items))
	for _, project := range items {
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
		}.Build())
	}
	return result
}

// quotaUsageSeed adds every dimension of the given limits to the usage map at zero, so a
// dimension with a limit and no usage still enters the report.
func quotaUsageSeed(usage map[string]int64, limited ...map[string]int64) {
	for _, dimensions := range limited {
		for dimension := range dimensions {
			usage[dimension] = 0
		}
	}
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
