/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package grpcserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/servers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Storage tier resolver", func() {
	var (
		testCtx      context.Context
		pool         *pgxpool.Pool
		manager      database.TxManager
		resolver     servers.TierResolverFunc
		volumeServer *servers.PrivateVolumesServer
		tierServer   *servers.PrivateStorageTiersServer
	)

	begin := func() (context.Context, database.Tx) {
		GinkgoHelper()
		tx, err := manager.Begin(testCtx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			rollback := errors.New("test cleanup")
			tx.ReportError(&rollback)
			err := tx.End(cleanupCtx)
			Expect(err == nil || errors.Is(err, pgx.ErrTxClosed)).To(BeTrue(), "transaction cleanup: %v", err)
		})
		return database.TxIntoContext(testCtx, tx), tx
	}

	createNamedVolume := func(ctx context.Context, name string) error {
		_, err := volumeServer.Create(ctx, privatev1.VolumesCreateRequest_builder{
			Object: privatev1.Volume_builder{
				Metadata: privatev1.Metadata_builder{Name: name, Tenant: "volume-tenant"}.Build(),
				Spec: privatev1.VolumeSpec_builder{
					StorageTier: "tier",
					SizeGib:     1,
					AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
				}.Build(),
			}.Build(),
		}.Build())
		return err
	}
	createVolume := func(ctx context.Context) error {
		return createNamedVolume(ctx, "volume")
	}

	deleteTier := func(ctx context.Context) error {
		_, err := tierServer.Delete(ctx, privatev1.StorageTiersDeleteRequest_builder{Id: "tier"}.Build())
		return err
	}

	BeforeEach(func() {
		var cancel context.CancelFunc
		testCtx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		DeferCleanup(cancel)
		db, err := dbContainer.NewInstance().Build()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)
		pool, err = db.Pool(testCtx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(pool.Close)
		manager, err = database.NewTxManager().SetLogger(logger).SetPool(pool).Build()
		Expect(err).NotTo(HaveOccurred())
		tiersDAO, err := dao.NewGenericDAO[*privatev1.StorageTier]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		backendsDAO, err := dao.NewGenericDAO[*privatev1.StorageBackend]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		resolver = newDAOTierResolver(tiersDAO, backendsDAO)
		volumeServer, err = servers.NewPrivateVolumesServer().SetLogger(logger).
			SetAttributionLogic(attribution).SetTenancyLogic(tenancy).SetTierResolver(resolver).Build()
		Expect(err).NotTo(HaveOccurred())
		tierServer, err = servers.NewPrivateStorageTiersServer().SetLogger(logger).
			SetAttributionLogic(attribution).SetTenancyLogic(tenancy).SetStorageBackendsDAO(backendsDAO).Build()
		Expect(err).NotTo(HaveOccurred())
		_, err = pool.Exec(testCtx, `insert into tenants (id, name, tenant, data)
			values ('volume-tenant', 'volume-tenant', 'volume-tenant', '{}')`)
		Expect(err).NotTo(HaveOccurred())
		_, err = pool.Exec(testCtx, `insert into storage_backends (id, name, tenant, data)
			values ('backend', 'backend', 'shared', '{"spec":{"provider":"vast"}}')`)
		Expect(err).NotTo(HaveOccurred())
		_, err = pool.Exec(testCtx, `insert into storage_tiers (id, name, tenant, data)
			values ('tier', 'tier', 'shared', '{"spec":{"protocol":"STORAGE_PROTOCOL_BLOCK",
			"backends":[{"backend_id":"backend"}]},"status":{"state":"STORAGE_TIER_STATE_ACTIVE"}}')`)
		Expect(err).NotTo(HaveOccurred())
	})

	It("preserves provider and protocol resolution", func() {
		requestCtx, tx := begin()
		resolved, err := resolver(requestCtx, "tier")
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved.Provider).To(Equal("vast"))
		Expect(resolved.Protocol).To(Equal(privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK))
		Expect(tx.End(requestCtx)).To(Succeed())
	})

	DescribeTable("preserves resolution errors and rejects deleting tiers", func(sql string, code grpccodes.Code, message string) {
		_, err := pool.Exec(testCtx, sql)
		Expect(err).NotTo(HaveOccurred())
		requestCtx, _ := begin()
		_, err = resolver(requestCtx, "tier")
		Expect(grpcstatus.Code(err)).To(Equal(code))
		Expect(err.Error()).To(ContainSubstring(message))
	},
		Entry("missing tier", `delete from storage_tiers where id = 'tier'`, grpccodes.NotFound, "not found"),
		Entry("inactive tier", `update storage_tiers set data['status']['state'] = '"STORAGE_TIER_STATE_UNSPECIFIED"'`, grpccodes.FailedPrecondition, "not active"),
		Entry("no associations", `update storage_tiers set data['spec']['backends'] = '[]'`, grpccodes.FailedPrecondition, "no backend associations"),
		Entry("missing backend", `update storage_tiers set data['spec']['backends'] = '[{"backend_id":"missing"}]'`, grpccodes.Unknown, "failed to look up storage backend"),
		Entry("deleting tier", `update storage_tiers set finalizers = array['test']; update storage_tiers set deletion_timestamp = now()`, grpccodes.FailedPrecondition, "being deleted"),
	)

	// Observe an actual database lock wait, rather than assuming goroutine scheduling
	// or a sleep has put the second request at the intended point in the race.
	waitForTierLock := func(pid int, statement string) {
		GinkgoHelper()
		Eventually(func() string {
			var waitEvent, query string
			err := pool.QueryRow(testCtx, `select coalesce(wait_event_type, ''), query from pg_stat_activity where pid = $1`, pid).
				Scan(&waitEvent, &query)
			Expect(err).NotTo(HaveOccurred())
			return strings.ToLower(waitEvent + " " + query)
		}, 5*time.Second, 10*time.Millisecond).Should(SatisfyAll(
			HavePrefix("lock "), ContainSubstring("storage_tiers"), ContainSubstring(statement),
		))
	}

	// Cancellation and joining happen before transaction and database cleanup.
	startRequest := func(requestCtx context.Context, operation func(context.Context) error) <-chan error {
		GinkgoHelper()
		workerCtx, cancel := context.WithCancel(requestCtx)
		result := make(chan error, 1)
		done := make(chan struct{})
		DeferCleanup(func() {
			cancel()
			Eventually(done, 5*time.Second).Should(BeClosed())
		})
		go func() {
			defer close(done)
			result <- operation(workerCtx)
		}()
		return result
	}

	It("allows concurrent Volume creates on the same tier while blocking deletion", func() {
		firstCtx, firstTx := begin()
		Expect(createNamedVolume(firstCtx, "first")).To(Succeed())
		secondCtx, secondTx := begin()
		result := startRequest(secondCtx, func(ctx context.Context) error {
			return createNamedVolume(ctx, "second")
		})
		var createErr error
		Eventually(result, 5*time.Second).Should(Receive(&createErr))
		Expect(createErr).NotTo(HaveOccurred())

		deleteCtx, deleteTx := begin()
		var pid int
		Expect(deleteTx.QueryRow(deleteCtx, "select pg_backend_pid()").Scan(&pid)).To(Succeed())
		deletion := startRequest(deleteCtx, deleteTier)
		waitForTierLock(pid, "update")
		Expect(firstTx.End(firstCtx)).To(Succeed())
		waitForTierLock(pid, "update")
		Expect(secondTx.End(secondCtx)).To(Succeed())
		var deleteErr error
		Eventually(deletion, 5*time.Second).Should(Receive(&deleteErr))
		Expect(grpcstatus.Code(deleteErr)).To(Equal(grpccodes.FailedPrecondition))
		Expect(deleteTx.End(deleteCtx)).To(Succeed())

		var active, count int
		Expect(pool.QueryRow(testCtx, `select count(*) from storage_tiers where id = 'tier' and deletion_timestamp = 'epoch'`).Scan(&active)).To(Succeed())
		Expect(active).To(Equal(1))
		Expect(pool.QueryRow(testCtx, `select count(*) from volumes where data->'spec'->>'storage_tier' = 'tier'`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(2))
	})

	DescribeTable("holds the tier lock until Volume creation ends", func(commit bool) {
		createCtx, createTx := begin()
		Expect(createVolume(createCtx)).To(Succeed())
		deleteCtx, deleteTx := begin()
		var pid int
		Expect(deleteTx.QueryRow(deleteCtx, "select pg_backend_pid()").Scan(&pid)).To(Succeed())
		result := startRequest(deleteCtx, deleteTier)
		waitForTierLock(pid, "update")
		if !commit {
			abort := errors.New("cancel Volume creation")
			createTx.ReportError(&abort)
		}
		Expect(createTx.End(createCtx)).To(Succeed())
		var deleteErr error
		Eventually(result, 5*time.Second).Should(Receive(&deleteErr))
		if commit {
			Expect(grpcstatus.Code(deleteErr)).To(Equal(grpccodes.FailedPrecondition))
		} else {
			Expect(deleteErr).NotTo(HaveOccurred())
		}
		Expect(deleteTx.End(deleteCtx)).To(Succeed())
		var active int
		Expect(pool.QueryRow(testCtx, `select count(*) from storage_tiers where id = 'tier' and deletion_timestamp = 'epoch'`).Scan(&active)).To(Succeed())
		var count int
		Expect(pool.QueryRow(testCtx, `select count(*) from volumes where data->'spec'->>'storage_tier' = 'tier'`).Scan(&count)).To(Succeed())
		if commit {
			Expect(active).To(Equal(1))
			Expect(count).To(Equal(1))
		} else {
			Expect(active).To(BeZero())
			Expect(count).To(BeZero())
		}
	},
		Entry("committed Volume blocks deletion", true),
		Entry("rolled-back Volume allows deletion", false),
	)

	It("validates the current tier state after waiting for its lock", func() {
		updateCtx, updateTx := begin()
		_, err := updateTx.Exec(updateCtx, `update storage_tiers set data['status']['state'] = '"STORAGE_TIER_STATE_UNSPECIFIED"' where id = 'tier'`)
		Expect(err).NotTo(HaveOccurred())
		createCtx, createTx := begin()
		var pid int
		Expect(createTx.QueryRow(createCtx, "select pg_backend_pid()").Scan(&pid)).To(Succeed())
		result := startRequest(createCtx, createVolume)
		waitForTierLock(pid, "for share")
		Expect(updateTx.End(updateCtx)).To(Succeed())
		var createErr error
		Eventually(result, 5*time.Second).Should(Receive(&createErr))
		Expect(grpcstatus.Code(createErr)).To(Equal(grpccodes.FailedPrecondition))
		Expect(createErr.Error()).To(ContainSubstring("not active"))
		Expect(createTx.End(createCtx)).To(Succeed())
		var count int
		Expect(pool.QueryRow(testCtx, `select count(*) from volumes`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
	})

	DescribeTable("rejects creation when deletion wins the row lock", func(retain bool, expected grpccodes.Code) {
		if retain {
			_, err := pool.Exec(testCtx, `update storage_tiers set finalizers = array['test']`)
			Expect(err).NotTo(HaveOccurred())
		}
		deleteCtx, deleteTx := begin()
		Expect(deleteTier(deleteCtx)).To(Succeed())
		createCtx, createTx := begin()
		var pid int
		Expect(createTx.QueryRow(createCtx, "select pg_backend_pid()").Scan(&pid)).To(Succeed())
		result := startRequest(createCtx, createVolume)
		waitForTierLock(pid, "for share")
		Expect(deleteTx.End(deleteCtx)).To(Succeed())
		var createErr error
		Eventually(result, 5*time.Second).Should(Receive(&createErr))
		Expect(grpcstatus.Code(createErr)).To(Equal(expected))
		Expect(createTx.End(createCtx)).To(Succeed())
		var count int
		Expect(pool.QueryRow(testCtx, `select count(*) from volumes`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
	},
		Entry("archived tier", false, grpccodes.NotFound),
		Entry("tier retained by finalizer", true, grpccodes.FailedPrecondition),
	)
})
