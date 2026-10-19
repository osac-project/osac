/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	catalogPricingCacheTTL                = 5 * time.Minute
	catalogPricingCacheMaxEntries         = 256
	catalogPricingTimeout                 = 15 * time.Second
	catalogPricingRetryCount              = billing.ProviderReadRetryCount
	catalogPricingMaxConcurrency          = 4
	catalogPricingCircuitFailureThreshold = 5
	catalogPricingCircuitFailureWindow    = 30 * time.Second
	catalogPricingCircuitOpenDuration     = 30 * time.Second
	billingCoverageTimeout                = 30 * time.Second
)

type catalogPricingEnrichmentDisabledKey struct{}

// ErrBillingCoverageUnavailable identifies provider failures that must not
// prevent a resource launch. A missing assignment or rate is never this error.
var ErrBillingCoverageUnavailable = errors.New("billing coverage provider unavailable")

var errPricingCircuitOpen = errors.New("billing pricing circuit is open")

type billingCoverageProviderError struct {
	status *grpcstatus.Status
}

func (e *billingCoverageProviderError) Error() string                  { return e.status.Err().Error() }
func (e *billingCoverageProviderError) Unwrap() error                  { return ErrBillingCoverageUnavailable }
func (e *billingCoverageProviderError) GRPCStatus() *grpcstatus.Status { return e.status }

func coverageProviderError(code grpccodes.Code, message string) error {
	return &billingCoverageProviderError{status: grpcstatus.New(code, message)}
}

func coverageErrorForCreate(ctx context.Context, logger *slog.Logger, err error) error {
	if errors.Is(err, ErrBillingCoverageUnavailable) {
		if logger != nil {
			logger.WarnContext(ctx, "billing provider unavailable during coverage check; proceeding with launch")
		}
		return nil
	}
	return err
}

func withoutCatalogPricing(ctx context.Context) context.Context {
	return context.WithValue(ctx, catalogPricingEnrichmentDisabledKey{}, true)
}

func catalogPricingEnrichmentDisabled(ctx context.Context) bool {
	return ctx.Value(catalogPricingEnrichmentDisabledKey{}) == true
}

// CatalogPricingRequest describes one billable catalog or selected configuration.
type CatalogPricingRequest = billing.CatalogPricingRequest

type catalogPricingLookup struct {
	request      billing.CatalogPricingComponentRequest
	requestIndex int
}

type catalogPricingAccountStore interface {
	GetAssignedAccount(context.Context, string) (billing.AssignedAccount, error)
}

// CatalogPricingResolver applies provider rates to catalog default configurations.
type CatalogPricingResolver struct {
	providerKey   string
	provider      billing.RateCardProvider
	accountStatus billing.AccountStatusProvider
	accounts      catalogPricingAccountStore
	now           func() time.Time

	mu            sync.Mutex
	cache         map[catalogRateCardKey]catalogRateCardEntry
	inFlight      map[catalogRateCardKey]*catalogRateCardLoad
	rateCardSlots chan struct{}
	circuit       catalogPricingCircuitBreaker
	metrics       *catalogPricingMetrics
}

type catalogRateCardKey struct {
	providerKey string
	account     string
	currency    string
	service     string
}

type catalogRateCardEntry struct {
	items     []billing.RateCardItem
	fetchedAt time.Time
}

type catalogRateCardLoad struct {
	done      chan struct{}
	items     []billing.RateCardItem
	fetchedAt time.Time
	err       error
}

// NewCatalogPricingResolver creates a resolver for the configured billing provider.
func NewCatalogPricingResolver(
	providerKey string,
	provider billing.RateCardProvider,
	accounts catalogPricingAccountStore,
	registerers ...prometheus.Registerer,
) *CatalogPricingResolver {
	accountStatus, _ := provider.(billing.AccountStatusProvider)
	resolver := &CatalogPricingResolver{
		providerKey:   providerKey,
		provider:      provider,
		accountStatus: accountStatus,
		accounts:      accounts,
		now:           time.Now,
		cache:         make(map[catalogRateCardKey]catalogRateCardEntry),
		inFlight:      make(map[catalogRateCardKey]*catalogRateCardLoad),
		rateCardSlots: make(chan struct{}, catalogPricingMaxConcurrency),
	}
	if len(registerers) > 0 && registerers[0] != nil {
		resolver.metrics = newCatalogPricingMetrics(registerers[0])
	}
	return resolver
}

// Resolve returns catalog prices for one tenant's linked account. Failed or
// uncovered lookups produce UNAVAILABLE results so catalog browsing remains
// available.
func (r *CatalogPricingResolver) Resolve(
	ctx context.Context,
	tenantID string,
	requests []CatalogPricingRequest,
) []*publicv1.CatalogItemPricing {
	if len(requests) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, catalogPricingTimeout)
	defer cancel()

	results := make([]*publicv1.CatalogItemPricing, len(requests))
	if r != nil && r.metrics != nil {
		defer func() { r.metrics.recordResults(r.providerKey, results) }()
	}
	for i, request := range requests {
		results[i] = unavailableCatalogPricing("", request.PriceMayVary)
	}
	if r == nil || r.accounts == nil || r.providerKey == "" || tenantID == "" {
		return results
	}

	account, err := r.accounts.GetAssignedAccount(ctx, tenantID)
	if err != nil {
		return results
	}
	account.CurrencyCode = strings.ToUpper(strings.TrimSpace(account.CurrencyCode))
	for i, request := range requests {
		results[i] = unavailableCatalogPricing(account.CurrencyCode, request.PriceMayVary)
	}
	if r.provider == nil || !assignmentIsConnected(account.ConnectionState) || account.ProviderKey != r.providerKey || account.CurrencyCode == "" || account.ProviderAccountReference == "" {
		return results
	}

	lookupsByService, expected, failed := catalogPricingLookupsByService(requests)
	staleResults, asOfTimes, components := r.resolveCatalogPricingLookups(ctx, account, requests, lookupsByService, failed)
	return buildCatalogPricingResults(results, requests, account, expected, failed, staleResults, asOfTimes, components)
}

func catalogPricingLookupsByService(
	requests []CatalogPricingRequest,
) (map[string][]catalogPricingLookup, []int, []bool) {
	lookupsByService := make(map[string][]catalogPricingLookup)
	expected := make([]int, len(requests))
	failed := make([]bool, len(requests))
	for index, request := range requests {
		if !request.Supported || request.Service == "" {
			failed[index] = true
			continue
		}
		componentRequests := catalogPricingComponents(request)
		if len(componentRequests) == 0 {
			failed[index] = true
			continue
		}
		expected[index] = len(componentRequests)
		for _, componentRequest := range componentRequests {
			lookupsByService[request.Service] = append(lookupsByService[request.Service], catalogPricingLookup{
				request: componentRequest, requestIndex: index,
			})
		}
	}
	return lookupsByService, expected, failed
}

func sortedCatalogPricingLookupServices(lookupsByService map[string][]catalogPricingLookup) []string {
	services := make([]string, 0, len(lookupsByService))
	for service := range lookupsByService {
		services = append(services, service)
	}
	sort.Strings(services)
	return services
}

func (r *CatalogPricingResolver) resolveCatalogPricingLookups(
	ctx context.Context,
	account billing.AssignedAccount,
	requests []CatalogPricingRequest,
	lookupsByService map[string][]catalogPricingLookup,
	failed []bool,
) ([]bool, []time.Time, [][]*publicv1.CatalogPriceComponent) {
	staleResults := make([]bool, len(requests))
	asOfTimes := make([]time.Time, len(requests))
	components := make([][]*publicv1.CatalogPriceComponent, len(requests))
	for _, service := range sortedCatalogPricingLookupServices(lookupsByService) {
		rateItems, usedStale, fetchedAt := r.getCatalogRateCardItems(ctx, account, service)
		if rateItems == nil {
			for _, lookup := range lookupsByService[service] {
				failed[lookup.requestIndex] = true
			}
			continue
		}
		r.resolveCatalogPricingService(requests, account, service, lookupsByService[service], rateItems, usedStale, fetchedAt, failed, staleResults, asOfTimes, components)
	}
	return staleResults, asOfTimes, components
}

func (r *CatalogPricingResolver) resolveCatalogPricingService(
	requests []CatalogPricingRequest,
	account billing.AssignedAccount,
	service string,
	lookups []catalogPricingLookup,
	rateItems []billing.RateCardItem,
	usedStale bool,
	fetchedAt time.Time,
	failed, staleResults []bool,
	asOfTimes []time.Time,
	components [][]*publicv1.CatalogPriceComponent,
) {
	for _, lookup := range lookups {
		if !usedStale {
			asOfTimes[lookup.requestIndex] = fetchedAt
		}
		matched, matchErr := r.resolveRateCardItem(rateItems, service, account.CurrencyCode, lookup.request.Dimensions)
		if matchErr != nil {
			failed[lookup.requestIndex] = true
			continue
		}
		amount, amountErr := billing.ComponentRateAmount(matched, lookup.request.Dimensions)
		if amountErr != nil {
			failed[lookup.requestIndex] = true
			continue
		}
		description := lookup.request.Description
		if description == "" {
			description = requests[lookup.requestIndex].Description
		}
		components[lookup.requestIndex] = append(components[lookup.requestIndex], publicv1.CatalogPriceComponent_builder{
			Amount: amount, Unit: string(matched.Unit), Description: description,
		}.Build())
		staleResults[lookup.requestIndex] = staleResults[lookup.requestIndex] || usedStale
	}
}

func buildCatalogPricingResults(
	results []*publicv1.CatalogItemPricing,
	requests []CatalogPricingRequest,
	account billing.AssignedAccount,
	expected []int,
	failed, staleResults []bool,
	asOfTimes []time.Time,
	components [][]*publicv1.CatalogPriceComponent,
) []*publicv1.CatalogItemPricing {
	for index, request := range requests {
		if failed[index] || expected[index] == 0 || len(components[index]) != expected[index] {
			continue
		}
		status := publicv1.CatalogPricingStatus_CATALOG_PRICING_STATUS_CURRENT
		if staleResults[index] {
			status = publicv1.CatalogPricingStatus_CATALOG_PRICING_STATUS_STALE
		}
		pricing := publicv1.CatalogItemPricing_builder{
			Status:       status,
			CurrencyCode: &account.CurrencyCode,
			PriceMayVary: request.PriceMayVary,
			Components:   components[index],
		}
		if status == publicv1.CatalogPricingStatus_CATALOG_PRICING_STATUS_CURRENT && !asOfTimes[index].IsZero() {
			pricing.AsOf = timestamppb.New(asOfTimes[index])
		}
		results[index] = pricing.Build()
	}
	return results
}

func catalogPricingComponents(request CatalogPricingRequest) []billing.CatalogPricingComponentRequest {
	components := make([]billing.CatalogPricingComponentRequest, 0, len(request.Components)+1)
	if request.Service != billing.ServiceCluster || len(request.Components) == 0 {
		components = append(components, billing.CatalogPricingComponentRequest{
			Dimensions: request.Dimensions, Description: request.Description,
		})
	}
	return append(components, request.Components...)
}

func (r *CatalogPricingResolver) resolveRateCardItem(
	items []billing.RateCardItem,
	service, currency string,
	dimensions map[string]string,
) (billing.RateCardItem, error) {
	item, err := billing.ResolveRateCardItem(items, service, currency, dimensions)
	if r == nil || r.metrics == nil || r.metrics.resolutionOutcomes == nil {
		return item, err
	}
	outcome := "resolved"
	switch {
	case errors.Is(err, billing.ErrRateCoverageGap):
		outcome = "coverage_gap"
	case errors.Is(err, billing.ErrAmbiguousRateCard):
		outcome = "ambiguous"
	case errors.Is(err, billing.ErrInvalidRateCard):
		outcome = "invalid_rate_card"
	case err != nil:
		outcome = "error"
	}
	r.metrics.resolutionOutcomes.WithLabelValues(r.providerKey, outcome).Inc()
	return item, err
}

// CheckCoverage requires a fresh rate-card lookup for every billable service in
// the requested configuration. Cached catalog prices are deliberately ignored:
// a retained amount can help browsing, but cannot authorize a new launch.
func (r *CatalogPricingResolver) CheckCoverage(
	ctx context.Context,
	tenantID string,
	requests []CatalogPricingRequest,
) error {
	if len(requests) == 0 || r == nil {
		return nil
	}
	if tenantID == "" {
		return grpcstatus.Error(grpccodes.InvalidArgument, "tenant is required for billing coverage")
	}
	if r.provider == nil || r.accounts == nil || r.providerKey == "" {
		return grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_COVERAGE_UNAVAILABLE")
	}
	if r.accountStatus == nil {
		return grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_ACCOUNT_STATUS_UNAVAILABLE")
	}

	ctx, cancel := context.WithTimeout(ctx, billingCoverageTimeout)
	defer cancel()
	account, err := r.getCoverageAccount(ctx, tenantID)
	if err != nil {
		return err
	}

	byService, err := catalogPricingCoverageRequestsByService(requests)
	if err != nil {
		return err
	}
	services := make([]string, 0, len(byService))
	for service := range byService {
		services = append(services, service)
	}
	sort.Strings(services)
	for _, service := range services {
		if err := r.checkCatalogPricingServiceCoverage(ctx, account, service, byService[service]); err != nil {
			return err
		}
	}
	return nil
}

func (r *CatalogPricingResolver) getCoverageAccount(ctx context.Context, tenantID string) (billing.AssignedAccount, error) {
	account, err := r.accounts.GetAssignedAccount(ctx, tenantID)
	if errors.Is(err, billing.ErrTenantBillingAssignmentNotFound) {
		return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_ACCOUNT_UNAVAILABLE")
	}
	if err != nil {
		if ctx.Err() != nil {
			return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.DeadlineExceeded, "BILLING_COVERAGE_TIMEOUT")
		}
		return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.Internal, "BILLING_ACCOUNT_LOOKUP_FAILED")
	}
	account.CurrencyCode = strings.ToUpper(strings.TrimSpace(account.CurrencyCode))
	if account.ProviderKey != r.providerKey || !assignmentIsConnected(account.ConnectionState) || account.ProviderAccountReference == "" || account.CurrencyCode == "" {
		return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_ACCOUNT_UNAVAILABLE")
	}
	if err := r.checkCoverageProviderAccount(ctx, account); err != nil {
		return billing.AssignedAccount{}, err
	}
	return account, nil
}

func (r *CatalogPricingResolver) checkCoverageProviderAccount(ctx context.Context, account billing.AssignedAccount) error {
	providerAccount, err := billing.RetryProviderRead(ctx, func(ctx context.Context) (billing.Account, error) {
		return r.accountStatus.GetAccount(ctx, account.ProviderAccountReference)
	})
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return grpcstatus.Error(grpccodes.Canceled, "BILLING_COVERAGE_CANCELED")
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return coverageProviderError(grpccodes.DeadlineExceeded, "BILLING_COVERAGE_TIMEOUT")
		}
		if errors.Is(err, billing.ErrAccountNotFound) {
			return grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_ACCOUNT_UNAVAILABLE")
		}
		if errors.Is(err, billing.ErrProviderUnavailable) {
			return coverageProviderError(grpccodes.Unavailable, "BILLING_ACCOUNT_STATUS_UNAVAILABLE")
		}
		return grpcstatus.Error(grpccodes.Internal, "BILLING_ACCOUNT_STATUS_FAILED")
	}
	if providerAccount.Reference != account.ProviderAccountReference || providerAccount.State != billing.AccountStateActive {
		return grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_ACCOUNT_UNAVAILABLE")
	}
	return nil
}

func catalogPricingCoverageRequestsByService(requests []CatalogPricingRequest) (map[string][]billing.CatalogPricingComponentRequest, error) {
	byService := make(map[string][]billing.CatalogPricingComponentRequest)
	for _, request := range requests {
		if !request.Supported || request.Service == "" {
			return nil, grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_CONFIGURATION_UNSUPPORTED")
		}
		components := catalogPricingComponents(request)
		if len(components) == 0 {
			return nil, grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_CONFIGURATION_UNSUPPORTED")
		}
		byService[request.Service] = append(byService[request.Service], components...)
	}
	return byService, nil
}

func (r *CatalogPricingResolver) checkCatalogPricingServiceCoverage(
	ctx context.Context,
	account billing.AssignedAccount,
	service string,
	requests []billing.CatalogPricingComponentRequest,
) error {
	rateItems, err := r.getFreshRateCardItems(ctx, account, service)
	if err != nil {
		return catalogPricingCoverageLookupError(ctx, err)
	}
	for _, request := range requests {
		_, err := r.resolveRateCardItem(rateItems, service, account.CurrencyCode, request.Dimensions)
		if err != nil {
			return catalogPricingCoverageResolutionError(err)
		}
	}
	return nil
}

func catalogPricingCoverageLookupError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return grpcstatus.Error(grpccodes.Canceled, "BILLING_COVERAGE_CANCELED")
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return coverageProviderError(grpccodes.DeadlineExceeded, "BILLING_COVERAGE_TIMEOUT")
	}
	if errors.Is(err, billing.ErrProviderUnavailable) || errors.Is(err, errPricingCircuitOpen) {
		return coverageProviderError(grpccodes.Unavailable, "BILLING_COVERAGE_UNAVAILABLE")
	}
	return grpcstatus.Error(grpccodes.Internal, "BILLING_COVERAGE_LOOKUP_FAILED")
}

func catalogPricingCoverageResolutionError(err error) error {
	switch {
	case errors.Is(err, billing.ErrRateCoverageGap):
		return grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_COVERAGE_GAP")
	case errors.Is(err, billing.ErrAmbiguousRateCard):
		return grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_RATE_AMBIGUOUS")
	case errors.Is(err, billing.ErrInvalidRateCard):
		return grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_RATE_INVALID")
	default:
		return grpcstatus.Error(grpccodes.Internal, "BILLING_COVERAGE_CHECK_FAILED")
	}
}

// checkCatalogItemBillingCoverage requires current coverage when a tenant-owned
// catalog item is offered for provisioning. Shared and system catalog items have
// no single tenant account to check; their launches are checked for each tenant.
func checkCatalogItemBillingCoverage(
	ctx context.Context,
	resolver *CatalogPricingResolver,
	tenantID string,
	published bool,
	requests []CatalogPricingRequest,
) error {
	if !catalogItemNeedsBillingCoverage(resolver, tenantID, published) {
		return nil
	}
	return resolver.CheckCoverage(ctx, tenantID, requests)
}

func catalogItemNeedsBillingCoverage(resolver *CatalogPricingResolver, tenantID string, published bool) bool {
	return published && resolver != nil && tenantID != auth.SharedTenant && tenantID != auth.SystemTenant
}

func (r *CatalogPricingResolver) getFreshRateCardItems(
	ctx context.Context,
	account billing.AssignedAccount,
	service string,
) ([]billing.RateCardItem, error) {
	return r.listFreshRateCardItems(ctx, billing.RateCardQuery{
		AccountReference: account.ProviderAccountReference,
		Service:          service,
		CurrencyCode:     account.CurrencyCode,
	})
}

func (r *CatalogPricingResolver) getRateCardItems(
	ctx context.Context,
	account billing.AssignedAccount,
	service string,
) ([]billing.RateCardItem, bool) {
	items, stale, _ := r.getCatalogRateCardItems(ctx, account, service)
	return items, stale
}

// getCatalogRateCardItems returns the OSAC fetch time for a successful lookup.
// A stale fallback retains the previous fetch time but callers must not present
// it as a current observation.
func (r *CatalogPricingResolver) getCatalogRateCardItems(
	ctx context.Context,
	account billing.AssignedAccount,
	service string,
) ([]billing.RateCardItem, bool, time.Time) {
	key := catalogRateCardKey{
		providerKey: r.providerKey,
		account:     account.ProviderAccountReference,
		currency:    account.CurrencyCode,
		service:     service,
	}
	now := r.now()

	r.mu.Lock()
	entry, hasEntry := r.cache[key]
	if hasEntry && now.Sub(entry.fetchedAt) < catalogPricingCacheTTL {
		r.mu.Unlock()
		return entry.items, false, entry.fetchedAt
	}
	if loading, ok := r.inFlight[key]; ok {
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			if hasEntry {
				return entry.items, true, entry.fetchedAt
			}
			return nil, false, time.Time{}
		case <-loading.done:
			if loading.err == nil {
				return loading.items, false, loading.fetchedAt
			}
			if hasEntry {
				return entry.items, true, entry.fetchedAt
			}
			return nil, false, time.Time{}
		}
	}
	loading := &catalogRateCardLoad{done: make(chan struct{})}
	r.inFlight[key] = loading
	r.mu.Unlock()

	query := billing.RateCardQuery{
		AccountReference: account.ProviderAccountReference,
		Service:          service,
		CurrencyCode:     account.CurrencyCode,
	}
	items, err := r.listFreshRateCardItems(ctx, query)
	if err == nil && items == nil {
		// A provider may represent an empty rate card with a nil slice. Keep that
		// distinct from nil returned after a failed lookup, which means unavailable.
		items = []billing.RateCardItem{}
	}
	fetchedAt := time.Time{}
	if err == nil {
		fetchedAt = r.now()
	}
	return r.finishRateCardLoad(key, loading, items, fetchedAt, err)
}

func (r *CatalogPricingResolver) finishRateCardLoad(
	key catalogRateCardKey,
	loading *catalogRateCardLoad,
	items []billing.RateCardItem,
	fetchedAt time.Time,
	loadErr error,
) ([]billing.RateCardItem, bool, time.Time) {
	r.mu.Lock()
	loading.items = items
	loading.fetchedAt = fetchedAt
	loading.err = loadErr
	if loadErr == nil {
		r.cache[key] = catalogRateCardEntry{items: items, fetchedAt: fetchedAt}
		r.evictOldestCatalogRateCardEntriesLocked()
	}
	delete(r.inFlight, key)
	close(loading.done)
	if loadErr == nil {
		r.mu.Unlock()
		return items, false, fetchedAt
	}
	entry, hasEntry := r.cache[key]
	r.mu.Unlock()
	if hasEntry {
		return entry.items, true, entry.fetchedAt
	}
	return nil, false, time.Time{}
}

// evictOldestCatalogRateCardEntriesLocked bounds memory without removing
// expired entries during normal reads. Retaining those entries allows a failed
// refresh to return their rates as STALE; they are only evicted under capacity
// pressure, oldest fetched entry first.
func (r *CatalogPricingResolver) evictOldestCatalogRateCardEntriesLocked() {
	for len(r.cache) > catalogPricingCacheMaxEntries {
		var oldestKey catalogRateCardKey
		var oldestFetchedAt time.Time
		first := true
		for key, entry := range r.cache {
			if first || entry.fetchedAt.Before(oldestFetchedAt) {
				oldestKey = key
				oldestFetchedAt = entry.fetchedAt
				first = false
			}
		}
		delete(r.cache, oldestKey)
	}
}

func listRateCardsWithRetries(ctx context.Context, provider billing.RateCardProvider, query billing.RateCardQuery) ([]billing.RateCardItem, error) {
	return billing.RetryProviderRead(ctx, func(ctx context.Context) ([]billing.RateCardItem, error) {
		return provider.ListRateCardItems(ctx, query)
	})
}

func (r *CatalogPricingResolver) listFreshRateCardItems(ctx context.Context, query billing.RateCardQuery) ([]billing.RateCardItem, error) {
	release, err := r.acquireRateCardSlot(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	allowed, probe := r.circuit.allow(r.now())
	if !allowed {
		if r.metrics != nil && r.metrics.lookupOutcomes != nil {
			r.metrics.lookupOutcomes.WithLabelValues(r.providerKey, "circuit_open").Inc()
		}
		return nil, errPricingCircuitOpen
	}
	startedAt := time.Now()
	items, err := listRateCardsWithRetries(ctx, r.provider, query)
	if r.metrics != nil && r.metrics.lookupOutcomes != nil {
		outcome := "success"
		if err != nil {
			outcome = "error"
		}
		r.metrics.lookupOutcomes.WithLabelValues(r.providerKey, outcome).Inc()
	}
	if r.metrics != nil && r.metrics.lookupDuration != nil {
		r.metrics.lookupDuration.WithLabelValues(r.providerKey).Observe(time.Since(startedAt).Seconds())
	}
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
			r.circuit.recordNeutral(probe)
		} else if errors.Is(err, billing.ErrProviderUnavailable) || errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			r.circuit.recordFailure(r.now(), probe)
		} else {
			// The provider responded. A rejected or malformed response must never
			// open the circuit.
			r.circuit.recordSuccess(probe)
		}
		return nil, err
	}
	r.circuit.recordSuccess(probe)
	return items, nil
}

func (r *CatalogPricingResolver) acquireRateCardSlot(ctx context.Context) (func(), error) {
	if r.rateCardSlots == nil {
		return func() {}, nil
	}
	select {
	case r.rateCardSlots <- struct{}{}:
		return func() { <-r.rateCardSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type catalogPricingCircuitBreaker struct {
	mu            sync.Mutex
	failures      []time.Time
	openUntil     time.Time
	probeInFlight bool
}

func (b *catalogPricingCircuitBreaker) allow(now time.Time) (allowed, probe bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openUntil.IsZero() {
		return true, false
	}
	if now.Before(b.openUntil) || b.probeInFlight {
		return false, false
	}
	b.probeInFlight = true
	return true, true
}

func (b *catalogPricingCircuitBreaker) recordSuccess(probe bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if probe {
		b.failures = nil
		b.openUntil = time.Time{}
		b.probeInFlight = false
	}
}

// recordNeutral releases an abandoned half-open probe without claiming that
// the provider recovered or failed.
func (b *catalogPricingCircuitBreaker) recordNeutral(probe bool) {
	if !probe {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probeInFlight = false
}

func (b *catalogPricingCircuitBreaker) recordFailure(now time.Time, probe bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if probe {
		b.probeInFlight = false
		b.failures = nil
		b.openUntil = now.Add(catalogPricingCircuitOpenDuration)
		return
	}
	cutoff := now.Add(-catalogPricingCircuitFailureWindow)
	recent := b.failures[:0]
	for _, failure := range b.failures {
		if failure.After(cutoff) {
			recent = append(recent, failure)
		}
	}
	b.failures = append(recent, now)
	if len(b.failures) >= catalogPricingCircuitFailureThreshold {
		b.failures = nil
		b.openUntil = now.Add(catalogPricingCircuitOpenDuration)
	}
}

func unavailableCatalogPricing(currency string, priceMayVary bool) *publicv1.CatalogItemPricing {
	var currencyCode *string
	if currency != "" {
		currencyCode = &currency
	}
	return publicv1.CatalogItemPricing_builder{
		Status:       publicv1.CatalogPricingStatus_CATALOG_PRICING_STATUS_UNAVAILABLE,
		CurrencyCode: currencyCode,
		PriceMayVary: priceMayVary,
	}.Build()
}

func resolveCatalogPricing(
	ctx context.Context,
	logger *slog.Logger,
	tenancyLogic auth.TenancyLogic,
	resolver *CatalogPricingResolver,
	requestedTenant string,
	requests []CatalogPricingRequest,
) ([]*publicv1.CatalogItemPricing, error) {
	if resolver == nil || catalogPricingEnrichmentDisabled(ctx) {
		return nil, nil
	}
	if tenancyLogic == nil {
		return unavailableCatalogPricingResults(requests), nil
	}

	assignableTenants, err := tenancyLogic.DetermineAssignableTenants(ctx)
	if err != nil {
		return unavailableCatalogPricingResults(requests), nil
	}

	tenantID := ""
	if assignableTenants.Universal() {
		// Platform administrators can preview pricing for a selected tenant, but
		// must not silently price against the shared or default tenant.
		tenantID = strings.TrimSpace(requestedTenant)
		if tenantID == "" {
			return unavailableCatalogPricingResults(requests), nil
		}
	} else {
		// Tenant callers always use their active tenant context. Ignore any
		// request tenant_id so it cannot switch pricing to another account.
		tenantID = strings.TrimSpace(config.TenantFromContext(ctx))
		if tenantID == "" {
			return unavailableCatalogPricingResults(requests), nil
		}
	}
	if tenantID == "" {
		return unavailableCatalogPricingResults(requests), nil
	}
	if err := auth.ValidateTenantAssignment(ctx, tenancyLogic, tenantID); err != nil {
		if logger == nil {
			logger = slog.Default()
		}
		return nil, convertTenantErrorToGRPC(ctx, err, logger, tenantID)
	}
	return resolver.Resolve(ctx, tenantID, requests), nil
}

func unavailableCatalogPricingResults(requests []CatalogPricingRequest) []*publicv1.CatalogItemPricing {
	results := make([]*publicv1.CatalogItemPricing, len(requests))
	for i, request := range requests {
		results[i] = unavailableCatalogPricing("", request.PriceMayVary)
	}
	return results
}

type catalogPricingMetrics struct {
	lookupOutcomes     *prometheus.CounterVec
	lookupDuration     *prometheus.HistogramVec
	resolutionOutcomes *prometheus.CounterVec
	results            *prometheus.CounterVec
}

func newCatalogPricingMetrics(registerer prometheus.Registerer) *catalogPricingMetrics {
	metrics := &catalogPricingMetrics{}
	lookupOutcomes := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "osac_billing_provider_pricing_lookups_total",
		Help: "Catalog pricing lookups by configured provider and outcome.",
	}, []string{"provider", "outcome"})
	lookupDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "osac_billing_provider_pricing_lookup_duration_seconds",
		Help:    "Time spent retrieving provider pricing for catalog reads.",
		Buckets: prometheus.DefBuckets,
	}, []string{"provider"})
	results := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "osac_billing_catalog_pricing_results_total",
		Help: "Catalog pricing results by configured provider and freshness state.",
	}, []string{"provider", "status"})
	resolutionOutcomes := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "osac_billing_rate_resolution_outcomes_total",
		Help: "Provider rate resolution outcomes by configured provider and result.",
	}, []string{"provider", "outcome"})
	metrics.lookupOutcomes = registerCatalogPricingCounter(registerer, lookupOutcomes)
	metrics.lookupDuration = registerCatalogPricingHistogram(registerer, lookupDuration)
	metrics.resolutionOutcomes = registerCatalogPricingCounter(registerer, resolutionOutcomes)
	metrics.results = registerCatalogPricingCounter(registerer, results)
	return metrics
}

func registerCatalogPricingCounter(registerer prometheus.Registerer, collector *prometheus.CounterVec) *prometheus.CounterVec {
	if err := registerer.Register(collector); err != nil {
		var alreadyRegistered prometheus.AlreadyRegisteredError
		if errors.As(err, &alreadyRegistered) {
			if existing, ok := alreadyRegistered.ExistingCollector.(*prometheus.CounterVec); ok {
				return existing
			}
		}
		return nil
	}
	return collector
}

func registerCatalogPricingHistogram(registerer prometheus.Registerer, collector *prometheus.HistogramVec) *prometheus.HistogramVec {
	if err := registerer.Register(collector); err != nil {
		var alreadyRegistered prometheus.AlreadyRegisteredError
		if errors.As(err, &alreadyRegistered) {
			if existing, ok := alreadyRegistered.ExistingCollector.(*prometheus.HistogramVec); ok {
				return existing
			}
		}
		return nil
	}
	return collector
}

func (m *catalogPricingMetrics) recordResults(provider string, results []*publicv1.CatalogItemPricing) {
	if m.results == nil {
		return
	}
	for _, result := range results {
		if result == nil {
			continue
		}
		status := "UNAVAILABLE"
		switch result.GetStatus() {
		case publicv1.CatalogPricingStatus_CATALOG_PRICING_STATUS_CURRENT:
			status = "CURRENT"
		case publicv1.CatalogPricingStatus_CATALOG_PRICING_STATUS_STALE:
			status = "STALE"
		}
		m.results.WithLabelValues(provider, status).Inc()
	}
}

type catalogItemPricingClearer interface {
	ClearPricing()
}

func clearCatalogItemPricing(item catalogItemPricingClearer) {
	if item != nil {
		item.ClearPricing()
	}
}
