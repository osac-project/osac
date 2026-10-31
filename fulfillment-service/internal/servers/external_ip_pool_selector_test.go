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
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("ExternalIP pool selector", func() {
	var poolDao *dao.GenericDAO[*privatev1.ExternalIPPool]

	createPool := func(name string, ipFamily privatev1.IPFamily, state privatev1.ExternalIPPoolState, available int64) *privatev1.ExternalIPPool {
		pool := privatev1.ExternalIPPool_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   name,
				Tenant: "system",
			}.Build(),
			Spec: privatev1.ExternalIPPoolSpec_builder{
				IpFamily: ipFamily,
			}.Build(),
			Status: privatev1.ExternalIPPoolStatus_builder{
				State:     state,
				Total:     available,
				Available: available,
				Allocated: 0,
			}.Build(),
		}.Build()
		resp, err := poolDao.Create().SetObject(pool).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		return resp.GetObject()
	}

	BeforeEach(func() {
		var err error
		poolDao, err = dao.NewGenericDAO[*privatev1.ExternalIPPool]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	selectPool := func(need int64) (*privatev1.ExternalIPPool, error) {
		return SelectExternalIPPool(ctx, logger, poolDao, need)
	}

	It("selects the READY pool with the largest available capacity", func() {
		createPool("small", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 5)
		createPool("large", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 100)

		pool, err := selectPool(1)
		Expect(err).ToNot(HaveOccurred())
		Expect(pool.GetMetadata().GetName()).To(Equal("large"))
	})

	It("skips pools not in READY state", func() {
		createPool("pending", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_PENDING, 100)
		createPool("ready", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 10)

		pool, err := selectPool(1)
		Expect(err).ToNot(HaveOccurred())
		Expect(pool.GetMetadata().GetName()).To(Equal("ready"))
	})

	It("skips pools whose capacity doesn't cover the need", func() {
		createPool("one", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 1)
		createPool("two", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 2)

		pool, err := selectPool(2)
		Expect(err).ToNot(HaveOccurred())
		Expect(pool.GetMetadata().GetName()).To(Equal("two"))
	})

	It("skips IPv6 pools", func() {
		createPool("ipv6-pool", privatev1.IPFamily_IP_FAMILY_IPV6, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 100)
		createPool("ipv4-pool", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 10)

		pool, err := selectPool(1)
		Expect(err).ToNot(HaveOccurred())
		Expect(pool.GetMetadata().GetName()).To(Equal("ipv4-pool"))
	})

	It("breaks ties deterministically by pool ID", func() {
		poolA := createPool("pool-a", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 50)
		poolB := createPool("pool-b", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 50)

		pool, err := selectPool(1)
		Expect(err).ToNot(HaveOccurred())
		Expect(pool.GetId()).To(Equal(min(poolA.GetId(), poolB.GetId())))
	})

	It("fails with FAILED_PRECONDITION and the exhaustion message when no pool fits", func() {
		createPool("empty", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 0)

		_, err := selectPool(1)
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
		Expect(grpcstatus.Convert(err).Message()).To(Equal(poolExhaustedMessage))
	})

	It("reads every pool, not only the first page", func() {
		saved := forEachRecordPageSize
		forEachRecordPageSize = 1
		DeferCleanup(func() { forEachRecordPageSize = saved })
		createPool("pool-1", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 5)
		createPool("pool-2", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 7)
		createPool("pool-3", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 9)

		pool, err := selectPool(1)
		Expect(err).ToNot(HaveOccurred())
		Expect(pool.GetMetadata().GetName()).To(Equal("pool-3"))
	})

	It("finds a pool overlap on any page", func() {
		saved := forEachRecordPageSize
		forEachRecordPageSize = 1
		DeferCleanup(func() { forEachRecordPageSize = saved })
		var last *privatev1.ExternalIPPool
		for i := range 3 {
			response, err := poolDao.Create().SetObject(privatev1.ExternalIPPool_builder{
				Id:       fmt.Sprintf("pool-%d", i),
				Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("pool-%d", i), Tenant: "system"}.Build(),
				Spec: privatev1.ExternalIPPoolSpec_builder{
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
					Cidrs:    []string{fmt.Sprintf("198.51.100.%d/30", i*4)},
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			last = response.GetObject()
		}
		guards, err := newNetworkingGuards(logger)
		Expect(err).ToNot(HaveOccurred())
		found, err := guards.firstPoolOverlap(ctx, netip.MustParsePrefix("198.51.100.8/29"), "")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(Equal(last.GetId()))
	})

	It("locks every fitting pool, so concurrent selections lock in ID order", func() {
		createPool("pool-a", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 5)
		createPool("pool-b", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 9)
		full := createPool("pool-full", privatev1.IPFamily_IP_FAMILY_IPV4,
			privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 0)

		// Commit the fixtures so another connection sees the rows:
		Expect(suiteTx.End(ctx)).To(Succeed())
		suiteTx = nil
		selectTx, err := tm.Begin(context.Background())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			if selectTx != nil {
				_ = selectTx.End(context.Background())
			}
		})
		selectCtx := database.TxIntoContext(context.Background(), selectTx)
		pool, err := SelectExternalIPPool(selectCtx, logger, poolDao, 1)
		Expect(err).ToNot(HaveOccurred())
		Expect(pool.GetMetadata().GetName()).To(Equal("pool-b"))

		// Both fitting pools are locked by the selecting transaction; the full one isn't:
		locked := func(id string) bool {
			_, err := testPool.Exec(context.Background(),
				"select id from external_ip_pools where id = $1 for update nowait", id)
			var pgErr *pgconn.PgError
			return errors.As(err, &pgErr) && pgErr.Code == "55P03"
		}
		names := map[string]bool{}
		response, err := poolDao.List().Do(selectCtx)
		Expect(err).ToNot(HaveOccurred())
		for _, item := range response.GetItems() {
			names[item.GetMetadata().GetName()] = locked(item.GetId())
		}
		Expect(names).To(Equal(map[string]bool{"pool-a": true, "pool-b": true, "pool-full": false}))
		Expect(full.GetId()).ToNot(BeEmpty())
		Expect(selectTx.End(context.Background())).To(Succeed())
		selectTx = nil
	})

	It("locks all fitting pools with one ordered database request", func() {
		createPool("pool-a", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 5)
		createPool("pool-b", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 9)

		var output bytes.Buffer
		loggingDAO, err := dao.NewGenericDAO[*privatev1.ExternalIPPool]().
			SetLogger(slog.New(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		pool, err := SelectExternalIPPool(ctx, logger, loggingDAO, 1)
		Expect(err).ToNot(HaveOccurred())
		Expect(pool.GetMetadata().GetName()).To(Equal("pool-b"))
		Expect(strings.Count(output.String(), " for update")).To(Equal(1), output.String())
	})
})
