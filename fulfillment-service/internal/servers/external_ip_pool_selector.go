/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"fmt"
	"sort"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// SelectExternalIPPool selects the best ExternalIPPool for allocation.
//
// Selection criteria:
//   - Pool must be in READY state
//   - Pool must have available capacity (available > 0)
//   - Pool must match the requested IP family (skipped if IP_FAMILY_UNSPECIFIED)
//   - Among matching pools, the one with the most available capacity is selected
//   - Ties are broken deterministically by pool ID (lexicographic ascending)
func SelectExternalIPPool(
	ctx context.Context,
	poolDao *dao.GenericDAO[*privatev1.ExternalIPPool],
	ipFamily privatev1.IPFamily,
) (*privatev1.ExternalIPPool, error) {
	listResponse, err := poolDao.List().Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query ExternalIP pools: %w", err)
	}

	var candidates []*privatev1.ExternalIPPool
	for _, pool := range listResponse.GetItems() {
		if pool.GetStatus().GetState() != privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY {
			continue
		}
		if pool.GetStatus().GetAvailable() <= 0 {
			continue
		}
		if ipFamily != privatev1.IPFamily_IP_FAMILY_UNSPECIFIED &&
			pool.GetSpec().GetIpFamily() != ipFamily {
			continue
		}
		candidates = append(candidates, pool)
	}

	if len(candidates) == 0 {
		if ipFamily != privatev1.IPFamily_IP_FAMILY_UNSPECIFIED {
			return nil, fmt.Errorf("no READY ExternalIP pool with available capacity found for IP family %s", ipFamily)
		}
		return nil, fmt.Errorf("no READY ExternalIP pool with available capacity found")
	}

	sort.Slice(candidates, func(i, j int) bool {
		ai := candidates[i].GetStatus().GetAvailable()
		aj := candidates[j].GetStatus().GetAvailable()
		if ai != aj {
			return ai > aj
		}
		return candidates[i].GetId() < candidates[j].GetId()
	})

	return candidates[0], nil
}

// UpdatePoolCapacity adjusts the allocated/available counters of an ExternalIPPool
// by the given delta (+N to allocate, -N to release). The pool row is locked with
// SELECT FOR UPDATE to serialize concurrent capacity changes.
func UpdatePoolCapacity(
	ctx context.Context,
	poolDao *dao.GenericDAO[*privatev1.ExternalIPPool],
	poolID string,
	delta int64,
) error {
	getResponse, err := poolDao.Get().
		SetId(poolID).
		SetLock(true).
		Do(ctx)
	if err != nil {
		return fmt.Errorf("failed to get ExternalIPPool for capacity update: %w", err)
	}

	pool := getResponse.GetObject()
	newAllocated := pool.GetStatus().GetAllocated() + delta
	newAvailable := pool.GetStatus().GetAvailable() - delta
	if newAvailable < 0 {
		return fmt.Errorf("ExternalIP pool '%s' has no available capacity", poolID)
	}
	pool.GetStatus().SetAllocated(newAllocated)
	pool.GetStatus().SetAvailable(newAvailable)

	_, err = poolDao.Update().SetObject(pool).Do(ctx)
	if err != nil {
		return fmt.Errorf("failed to update ExternalIPPool capacity: %w", err)
	}
	return nil
}
