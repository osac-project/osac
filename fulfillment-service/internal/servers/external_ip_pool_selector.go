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
	"log/slog"
	"sort"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// poolExhaustedMessage is the message of a workload create that finds no pool for its automatic ExternalIPs.
const poolExhaustedMessage = "ExternalIPPool exhaustion: no available capacity in any READY pool for IPv4"

// SelectExternalIPPool selects and locks the pool for automatic ExternalIPs: a READY IPv4 pool, not being deleted,
// whose available capacity covers need addresses. It prefers the largest available capacity, then the lowest pool ID.
// It locks every pool that fits in one request, in ascending ID order (the lock order for records of one kind), and
// then chooses among the locked rows, so the choice uses current capacity and two concurrent selections never lock
// pools in opposite order. When no pool fits, it fails with FAILED_PRECONDITION.
func SelectExternalIPPool(
	ctx context.Context,
	logger *slog.Logger,
	poolDao *dao.GenericDAO[*privatev1.ExternalIPPool],
	need int64,
) (*privatev1.ExternalIPPool, error) {
	var candidates []string
	err := forEachRecord(ctx, poolDao, func(pool *privatev1.ExternalIPPool) {
		if poolFits(pool, need) {
			candidates = append(candidates, pool.GetId())
		}
	})
	if err != nil {
		return nil, grpcstatus.Error(grpccodes.Internal, "failed to find ExternalIPPools")
	}
	sort.Strings(candidates)
	var chosen *privatev1.ExternalIPPool
	if len(candidates) > 0 {
		locked, err := poolDao.Lock().AddIds(candidates...).SetReturnObjects(true).Do(ctx)
		if err != nil {
			return nil, translateLifecycleError(err)
		}
		for _, pool := range locked.GetObjects() {
			if !poolFits(pool, need) {
				continue
			}
			if chosen == nil || pool.GetStatus().GetAvailable() > chosen.GetStatus().GetAvailable() {
				chosen = pool
			}
		}
	}
	if chosen != nil {
		return chosen, nil
	}
	if logger != nil {
		logger.ErrorContext(ctx, "ExternalIPPoolExhausted", slog.Int64("needed", need))
	}
	return nil, grpcstatus.Error(grpccodes.FailedPrecondition, poolExhaustedMessage)
}

func poolFits(pool *privatev1.ExternalIPPool, need int64) bool {
	return pool.GetStatus().GetState() == privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY &&
		!pool.GetMetadata().HasDeletionTimestamp() &&
		pool.GetSpec().GetIpFamily() == privatev1.IPFamily_IP_FAMILY_IPV4 &&
		pool.GetStatus().GetAvailable() >= need
}

// UpdatePoolCapacity adjusts the allocated/available counters of a locked ExternalIPPool by the given delta (+N to
// allocate, -N to release).
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

// automaticExternalIPEndpoint is one endpoint of a workload that gets an automatic ExternalIP. A ComputeInstance or
// BareMetalInstance has one endpoint without a name; a Cluster has "api" and "ingress".
type automaticExternalIPEndpoint string

// createAutomaticExternalIPs creates the automatic ExternalIPs of a workload in the transaction that stores it, all
// from one pool, and reserves their pool slots. With provider networking disabled it creates nothing.
func createAutomaticExternalIPs(ctx context.Context, logger *slog.Logger, settings NetworkingSettings,
	poolDao *dao.GenericDAO[*privatev1.ExternalIPPool], externalIPDao *dao.GenericDAO[*privatev1.ExternalIP],
	owner *privatev1.Metadata, ownerID, kind string, endpoints ...automaticExternalIPEndpoint) error {
	if !settings.ProvisioningEnabled {
		return nil
	}
	if len(endpoints) == 0 {
		endpoints = []automaticExternalIPEndpoint{""}
	}
	pool, err := SelectExternalIPPool(ctx, logger, poolDao, int64(len(endpoints)))
	if err != nil {
		return err
	}
	tenant := owner.GetTenant()
	for _, endpoint := range endpoints {
		labels := map[string]string{
			autoCreatedLabel:     "true",
			autoCreatedForLabel:  ownerID,
			autoCreatedKindLabel: kind,
		}
		name := automaticExternalIPPrefix + ownerID
		if endpoint != "" {
			labels[autoCreatedEndpointLabel] = string(endpoint)
			name += "-" + string(endpoint)
		}
		externalIP := privatev1.ExternalIP_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   name,
				Tenant: tenant,
				Labels: labels,
				Annotations: map[string]string{
					tenantAnnotation:         tenant,
					ownerReferenceAnnotation: ownerID,
				},
				Creator: systemCreator,
			}.Build(),
			Spec: privatev1.ExternalIPSpec_builder{
				Pool: privatev1.ExternalIPPoolReference_builder{Id: pool.GetId()}.Build(),
			}.Build(),
			Status: privatev1.ExternalIPStatus_builder{
				State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING,
			}.Build(),
		}.Build()
		created, err := externalIPDao.Create().SetObject(externalIP).Do(ctx)
		if err != nil {
			return ConvertDAOErrorToGRPC(err, "create", "")
		}
		logger.InfoContext(ctx, "AutoProvisionedExternalIP",
			slog.String("external_ip_id", created.GetObject().GetId()),
			slog.String("pool_id", pool.GetId()),
			slog.String("owner_id", ownerID),
			slog.String("owner_kind", kind))
	}
	if err := UpdatePoolCapacity(ctx, poolDao, pool.GetId(), int64(len(endpoints))); err != nil {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "%s", err)
	}
	return nil
}
