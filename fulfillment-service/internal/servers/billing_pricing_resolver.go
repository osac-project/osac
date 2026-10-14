/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

type resolvedPricingComponent struct {
	Request CatalogPricingRequest
	Rate    *billing.RateCardItem
	Stale   bool
	Gap     bool
}

// ResolveSelectedConfiguration resolves rates for explicit provisioning
// configurations. It shares the catalog cache and provider retry/circuit logic,
// but never turns a failed lookup into an unpriced successful result.
func (r *CatalogPricingResolver) ResolveSelectedConfiguration(
	ctx context.Context,
	tenantID string,
	requests []CatalogPricingRequest,
) (string, []resolvedPricingComponent, error) {
	if len(requests) == 0 {
		return "", nil, grpcstatus.Error(grpccodes.InvalidArgument, "pricing configuration is empty")
	}
	if r == nil || r.accounts == nil || r.providerKey == "" {
		return "", nil, grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_NOT_CONFIGURED")
	}
	if r.provider == nil {
		return "", nil, grpcstatus.Error(grpccodes.Unimplemented, "PROVIDER_CAPABILITY_UNSUPPORTED")
	}
	if tenantID == "" {
		return "", nil, grpcstatus.Error(grpccodes.InvalidArgument, "tenant is required for pricing")
	}
	account, err := r.accounts.GetAssignedAccount(ctx, tenantID)
	if errors.Is(err, billing.ErrTenantBillingAssignmentNotFound) {
		return "", nil, grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_ACCOUNT_UNAVAILABLE")
	}
	if err != nil {
		if ctx.Err() != nil {
			return "", nil, grpcstatus.Error(grpccodes.DeadlineExceeded, "BILLING_PRICING_TIMEOUT")
		}
		return "", nil, grpcstatus.Error(grpccodes.Internal, "BILLING_ACCOUNT_LOOKUP_FAILED")
	}
	account.CurrencyCode = strings.ToUpper(strings.TrimSpace(account.CurrencyCode))
	if account.ProviderKey != r.providerKey || !assignmentIsConnected(account.ConnectionState) || account.ProviderAccountReference == "" || account.CurrencyCode == "" {
		return "", nil, grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_ACCOUNT_UNAVAILABLE")
	}

	components := make([]resolvedPricingComponent, len(requests))
	indicesByService := make(map[string][]int)
	for i, request := range requests {
		components[i].Request = request
		if !request.Supported {
			continue
		}
		if request.Service == "" {
			return "", nil, grpcstatus.Error(grpccodes.InvalidArgument, "pricing service is required")
		}
		indicesByService[request.Service] = append(indicesByService[request.Service], i)
	}
	services := make([]string, 0, len(indicesByService))
	for service := range indicesByService {
		services = append(services, service)
	}
	sort.Strings(services)
	for _, service := range services {
		rateItems, stale := r.getRateCardItems(ctx, account, service)
		if rateItems == nil {
			if ctx.Err() != nil {
				return "", nil, grpcstatus.Error(grpccodes.DeadlineExceeded, "BILLING_PRICING_TIMEOUT")
			}
			return "", nil, grpcstatus.Error(grpccodes.Unavailable, "BILLING_PRICING_UNAVAILABLE")
		}
		for _, index := range indicesByService[service] {
			component := &components[index]
			match, matchErr := r.resolveRateCardItem(rateItems, service, account.CurrencyCode, component.Request.Dimensions)
			switch {
			case matchErr == nil:
				component.Rate = &match
				component.Stale = stale
			case errors.Is(matchErr, billing.ErrRateCoverageGap):
				component.Gap = true
			case errors.Is(matchErr, billing.ErrAmbiguousRateCard):
				return "", nil, grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_RATE_AMBIGUOUS")
			default:
				return "", nil, grpcstatus.Error(grpccodes.Unavailable, "BILLING_PRICING_UNAVAILABLE")
			}
		}
	}
	return account.CurrencyCode, components, nil
}
