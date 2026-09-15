/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package maas

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc"

	privatev1 "github.com/osac-project/osac-metering/internal/api/osac/private/v1"
)

const missRefreshCooldown = 1 * time.Second

// TenantLister is the subset of TenantsClient needed for tenant resolution.
type TenantLister interface {
	List(ctx context.Context, in *privatev1.TenantsListRequest, opts ...grpc.CallOption) (*privatev1.TenantsListResponse, error)
}

// TenantCache validates organization_id against known OSAC tenants.
// Cache is a fast-path optimization; on miss it calls the API before rejecting.
type TenantCache struct {
	client          TenantLister
	logger          logr.Logger
	refreshInterval time.Duration

	mu              sync.RWMutex
	tenants         map[string]bool
	lastMissRefresh time.Time
	sfGroup         singleflight.Group
}

func NewTenantCache(client TenantLister, logger logr.Logger, refreshInterval time.Duration) *TenantCache {
	return &TenantCache{
		client:          client,
		logger:          logger.WithName("tenant-cache"),
		refreshInterval: refreshInterval,
		tenants:         map[string]bool{},
	}
}

// Load populates the cache from the Tenants List API.
func (tc *TenantCache) Load(ctx context.Context) error {
	tenants, err := tc.fetchTenants(ctx)
	if err != nil {
		return err
	}
	tc.mu.Lock()
	tc.tenants = tenants
	tc.mu.Unlock()
	tc.logger.Info("tenant cache loaded", "count", len(tenants))
	return nil
}

// RunPeriodicRefresh refreshes the cache on the configured interval.
func (tc *TenantCache) RunPeriodicRefresh(ctx context.Context) {
	ticker := time.NewTicker(tc.refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := tc.Load(ctx); err != nil {
				tc.logger.Error(err, "periodic tenant cache refresh failed")
			}
		}
	}
}

// Resolve validates that organizationID maps to a known tenant.
// Fast path: check cache. On miss: coalesced API refresh with cooldown, then reject if still not found.
func (tc *TenantCache) Resolve(ctx context.Context, organizationID string) (string, error) {
	tc.mu.RLock()
	found := tc.tenants[organizationID]
	tc.mu.RUnlock()

	if found {
		return organizationID, nil
	}

	tc.mu.RLock()
	coolingDown := time.Since(tc.lastMissRefresh) < missRefreshCooldown
	tc.mu.RUnlock()

	if !coolingDown {
		_, err, _ := tc.sfGroup.Do("refresh", func() (any, error) {
			tc.mu.Lock()
			tc.lastMissRefresh = time.Now()
			tc.mu.Unlock()
			return nil, tc.Load(ctx)
		})
		if err != nil {
			return "", fmt.Errorf("refreshing tenant cache: %w", err)
		}
	}

	tc.mu.RLock()
	found = tc.tenants[organizationID]
	tc.mu.RUnlock()

	if !found {
		return "", permanentError{fmt.Errorf("organization %q does not match any known tenant", organizationID)}
	}
	return organizationID, nil
}

const tenantPageSize = 500

func (tc *TenantCache) fetchTenants(ctx context.Context) (map[string]bool, error) {
	tenants := map[string]bool{}
	var offset int32

	for {
		limit := int32(tenantPageSize)
		resp, err := tc.client.List(ctx, &privatev1.TenantsListRequest{
			Offset: &offset,
			Limit:  &limit,
		})
		if err != nil {
			return nil, fmt.Errorf("listing tenants (offset=%d): %w", offset, err)
		}

		items := resp.GetItems()
		for _, t := range items {
			if md := t.GetMetadata(); md != nil {
				tenants[md.GetName()] = true
			}
		}

		if len(items) < tenantPageSize {
			break
		}
		offset += int32(len(items))
	}

	return tenants, nil
}
