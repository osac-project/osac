/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package projection_test

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/projection"
)

var (
	dbContainer *database.Container
	logger      logr.Logger
)

var _ = BeforeSuite(func() {
	if os.Getenv("SKIP_DB_TESTS") != "" {
		Skip("SKIP_DB_TESTS is set")
	}

	zapLog, err := zap.NewDevelopment()
	Expect(err).ToNot(HaveOccurred())
	logger = zapr.NewLogger(zapLog)

	dbContainer, err = database.NewContainer(logger)
	Expect(err).ToNot(HaveOccurred())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	Expect(dbContainer.Start(ctx)).To(Succeed())
})

var _ = AfterSuite(func() {
	if dbContainer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dbContainer.Stop(ctx)
	}
})

func newTestStore() (*projection.PostgresStore, *pgxpool.Pool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	inst := dbContainer.NewInstance()
	pool, err := inst.Pool(ctx)
	Expect(err).ToNot(HaveOccurred())
	return projection.NewPostgresStore(pool), pool
}

func makeState(resourceID string, version int32) projection.ResourceState {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return projection.ResourceState{
		ResourceID:         resourceID,
		ResourceType:       "ComputeInstance",
		TenantID:           "tenant-1",
		ProjectID:          "project-1",
		CurrentState:       "RUNNING",
		IsBillable:         true,
		BillableSince:      &now,
		TransitionTime:     now,
		FulfillmentVersion: version,
		BillingDimensions: map[string]any{
			"instance_type":      "m5.large",
			"image_ref":          "rhel-9",
			"boot_disk_size_gib": "50",
		},
	}
}

var _ = Describe("PostgresStore", func() {
	var store *projection.PostgresStore
	var pool *pgxpool.Pool

	BeforeEach(func() {
		store, pool = newTestStore()
	})

	AfterEach(func() {
		pool.Close()
	})

	Describe("Upsert and Get", func() {
		It("Inserts a new resource and reads it back", func() {
			ctx := context.Background()
			state := makeState("vm-1", 1)

			Expect(store.Upsert(ctx, state)).To(Succeed())

			got, err := store.Get(ctx, "vm-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(got).ToNot(BeNil())
			Expect(got.ResourceID).To(Equal("vm-1"))
			Expect(got.CurrentState).To(Equal("RUNNING"))
			Expect(got.IsBillable).To(BeTrue())
			Expect(got.FulfillmentVersion).To(Equal(int32(1)))
			Expect(got.BillingDimensions["instance_type"]).To(Equal("m5.large"))
			Expect(got.TenantID).To(Equal("tenant-1"))
			Expect(got.ProjectID).To(Equal("project-1"))
		})

		It("Updates an existing resource with a newer version", func() {
			ctx := context.Background()
			state := makeState("vm-2", 1)
			Expect(store.Upsert(ctx, state)).To(Succeed())

			state.CurrentState = "STOPPED"
			state.PreviousState = "RUNNING"
			state.IsBillable = false
			state.BillableSince = nil
			state.FulfillmentVersion = 2
			Expect(store.Upsert(ctx, state)).To(Succeed())

			got, err := store.Get(ctx, "vm-2")
			Expect(err).ToNot(HaveOccurred())
			Expect(got.CurrentState).To(Equal("STOPPED"))
			Expect(got.PreviousState).To(Equal("RUNNING"))
			Expect(got.IsBillable).To(BeFalse())
			Expect(got.FulfillmentVersion).To(Equal(int32(2)))
		})

		It("Returns nil for a non-existent resource", func() {
			ctx := context.Background()
			got, err := store.Get(ctx, "nonexistent")
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(BeNil())
		})
	})

	Describe("Stale version rejection", func() {
		It("Allows idempotent upsert with same version", func() {
			ctx := context.Background()
			state := makeState("vm-stale-1", 5)
			Expect(store.Upsert(ctx, state)).To(Succeed())

			state.CurrentState = "STOPPED"
			Expect(store.Upsert(ctx, state)).To(Succeed())

			got, err := store.Get(ctx, "vm-stale-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(got.CurrentState).To(Equal("STOPPED"))
		})

		It("Rejects upsert with older version", func() {
			ctx := context.Background()
			state := makeState("vm-stale-2", 10)
			Expect(store.Upsert(ctx, state)).To(Succeed())

			state.FulfillmentVersion = 5
			state.CurrentState = "STOPPED"
			err := store.Upsert(ctx, state)
			Expect(err).To(MatchError(projection.ErrStaleVersion))
		})
	})

	Describe("Delete", func() {
		It("Deletes an existing resource", func() {
			ctx := context.Background()
			state := makeState("vm-del", 1)
			Expect(store.Upsert(ctx, state)).To(Succeed())

			Expect(store.Delete(ctx, "vm-del")).To(Succeed())

			got, err := store.Get(ctx, "vm-del")
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(BeNil())
		})

		It("Returns nil for non-existent resource", func() {
			ctx := context.Background()
			err := store.Delete(ctx, "nonexistent")
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("ListBillable", func() {
		It("Returns only billable resources", func() {
			ctx := context.Background()
			billable := makeState("vm-bill", 1)
			billable.IsBillable = true
			Expect(store.Upsert(ctx, billable)).To(Succeed())

			notBillable := makeState("vm-nobill", 1)
			notBillable.IsBillable = false
			notBillable.CurrentState = "STOPPED"
			notBillable.BillableSince = nil
			Expect(store.Upsert(ctx, notBillable)).To(Succeed())

			results, err := store.ListBillable(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(results).To(HaveLen(1))
			Expect(results[0].ResourceID).To(Equal("vm-bill"))
		})

		It("Returns empty slice when no billable resources exist", func() {
			ctx := context.Background()
			results, err := store.ListBillable(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(results).To(BeEmpty())
		})
	})

	Describe("ListAll", func() {
		It("Returns all resources regardless of billable status", func() {
			ctx := context.Background()
			Expect(store.Upsert(ctx, makeState("vm-a", 1))).To(Succeed())

			stopped := makeState("vm-b", 1)
			stopped.IsBillable = false
			stopped.CurrentState = "STOPPED"
			stopped.BillableSince = nil
			Expect(store.Upsert(ctx, stopped)).To(Succeed())

			results, err := store.ListAll(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(results).To(HaveLen(2))
		})
	})

	Describe("UpdateLastHeartbeat", func() {
		It("Updates last_heartbeat_at for specified resources", func() {
			ctx := context.Background()
			Expect(store.Upsert(ctx, makeState("vm-hb1", 1))).To(Succeed())
			Expect(store.Upsert(ctx, makeState("vm-hb2", 1))).To(Succeed())

			hbTime := time.Now().UTC().Truncate(time.Microsecond)
			Expect(store.UpdateLastHeartbeat(ctx, []string{"vm-hb1", "vm-hb2"}, hbTime)).To(Succeed())

			got1, err := store.Get(ctx, "vm-hb1")
			Expect(err).ToNot(HaveOccurred())
			Expect(got1.LastHeartbeatAt).ToNot(BeNil())
			Expect(*got1.LastHeartbeatAt).To(BeTemporally("~", hbTime, time.Second))

			got2, err := store.Get(ctx, "vm-hb2")
			Expect(err).ToNot(HaveOccurred())
			Expect(got2.LastHeartbeatAt).ToNot(BeNil())
		})

		It("Does nothing with empty resource list", func() {
			ctx := context.Background()
			Expect(store.UpdateLastHeartbeat(ctx, []string{}, time.Now())).To(Succeed())
		})
	})

	Describe("Concurrent access", func() {
		It("Handles concurrent upserts to the same resource", func() {
			ctx := context.Background()
			state := makeState("vm-concurrent", 1)
			Expect(store.Upsert(ctx, state)).To(Succeed())

			var wg sync.WaitGroup
			results := make([]error, 2)

			wg.Add(2)
			go func() {
				defer wg.Done()
				s := makeState("vm-concurrent", 2)
				s.CurrentState = "STOPPED"
				results[0] = store.Upsert(ctx, s)
			}()
			go func() {
				defer wg.Done()
				s := makeState("vm-concurrent", 3)
				s.CurrentState = "PAUSED"
				results[1] = store.Upsert(ctx, s)
			}()
			wg.Wait()

			successes := 0
			for _, err := range results {
				if err == nil {
					successes++
				}
			}
			Expect(successes).To(BeNumerically(">=", 1))

			got, err := store.Get(ctx, "vm-concurrent")
			Expect(err).ToNot(HaveOccurred())
			Expect(got.FulfillmentVersion).To(Equal(int32(3)))
		})
	})
})
