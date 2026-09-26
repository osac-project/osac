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
	"github.com/osac-project/osac-metering/schema"
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

func expectTimeInstant(actual, expected *time.Time) {
	if expected == nil {
		Expect(actual).To(BeNil())
		return
	}
	Expect(actual).ToNot(BeNil())
	Expect(actual.Equal(*expected)).To(BeTrue(), "timestamps should represent the same instant")
}

func expectMeterStateEqual(actual, expected projection.BMaaSMeterState) {
	expectTimeInstant(actual.Allocation.ActiveSince, expected.Allocation.ActiveSince)
	expectTimeInstant(actual.Allocation.FirstStartedAt, expected.Allocation.FirstStartedAt)
	expectTimeInstant(actual.Consumption.ActiveSince, expected.Consumption.ActiveSince)
	expectTimeInstant(actual.Consumption.FirstStartedAt, expected.Consumption.FirstStartedAt)
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

		It("derives is_billable from billable_since regardless of the caller's value", func() {
			ctx := context.Background()
			state := makeState("vm-generated", 1)
			state.IsBillable = false // wrong on purpose -- column ignores this
			Expect(store.Upsert(ctx, state)).To(Succeed())

			got, err := store.Get(ctx, "vm-generated")
			Expect(err).ToNot(HaveOccurred())
			Expect(got.IsBillable).To(BeTrue(),
				"is_billable is GENERATED from billable_since, not from the caller's IsBillable field")
		})

		It("self-heals ever_billable from billable_since even when the caller computes it wrong", func() {
			ctx := context.Background()
			state := makeState("vm-selfheal", 1)
			state.EverBillable = false // wrong on purpose -- this is the exact bug fixed in 60dc25a2
			Expect(store.Upsert(ctx, state)).To(Succeed())

			got, err := store.Get(ctx, "vm-selfheal")
			Expect(err).ToNot(HaveOccurred())
			Expect(got.EverBillable).To(BeTrue(),
				"ever_billable must derive from billable_since IS NOT NULL, not trust the caller's "+
					"already-OR'd value -- a caller that gets EverBillable wrong on a first write "+
					"(as the pre-fix reconciler state_drift branch did) must not need a second, "+
					"correct write to self-correct")
		})

		It("Returns nil for a non-existent resource", func() {
			ctx := context.Background()
			got, err := store.Get(ctx, "nonexistent")
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(BeNil())
		})
	})

	Describe("BMaaS billable_since invariant", func() {
		It("persists billable_since from allocation active_since", func() {
			ctx := context.Background()
			state := makeState("bmi-billable-since-diverge", 1)
			state.ResourceType = schema.ResourceTypeBareMetalInstance
			callerBillableSince := state.TransitionTime.Add(-2 * time.Hour)
			allocationSince := state.TransitionTime.Add(-time.Hour)
			state.BillableSince = &callerBillableSince
			state.BMaaSMeterState.Allocation.ActiveSince = &allocationSince

			Expect(store.Upsert(ctx, state)).To(Succeed())

			var billableSince, allocationActiveSince *time.Time
			var isBillable bool
			err := pool.QueryRow(ctx, `
				SELECT r.billable_since, r.is_billable, m.active_since
				FROM metering_resource_state AS r
				JOIN metering_resource_meter_state AS m
				  ON m.resource_id = r.resource_id AND m.meter_type = 'allocation'
				WHERE r.resource_id = $1`, state.ResourceID).
				Scan(&billableSince, &isBillable, &allocationActiveSince)
			Expect(err).ToNot(HaveOccurred())
			expectTimeInstant(billableSince, &allocationSince)
			expectTimeInstant(allocationActiveSince, &allocationSince)
			Expect(isBillable).To(Equal(allocationActiveSince != nil))
		})

		It("clears billable_since when allocation is inactive", func() {
			ctx := context.Background()
			state := makeState("bmi-parent-only", 1)
			state.ResourceType = schema.ResourceTypeBareMetalInstance
			state.IsBillable = true

			Expect(store.Upsert(ctx, state)).To(Succeed())

			var billableSince, allocationActiveSince *time.Time
			var isBillable bool
			err := pool.QueryRow(ctx, `
				SELECT r.billable_since, r.is_billable, m.active_since
				FROM metering_resource_state AS r
				JOIN metering_resource_meter_state AS m
				  ON m.resource_id = r.resource_id AND m.meter_type = 'allocation'
				WHERE r.resource_id = $1`, state.ResourceID).
				Scan(&billableSince, &isBillable, &allocationActiveSince)
			Expect(err).ToNot(HaveOccurred())
			Expect(billableSince).To(BeNil())
			Expect(allocationActiveSince).To(BeNil())
			Expect(isBillable).To(BeFalse())

			results, err := store.ListBillable(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(results).To(BeEmpty())
		})
	})

	It("round-trips independent meter first-use state", func() {
		ctx := context.Background()
		state := makeState("bmi-meter-state", 1)
		state.ResourceType = schema.ResourceTypeBareMetalInstance
		allocationSince := state.TransitionTime.Add(-time.Hour)
		allocationFirstStarted := state.TransitionTime.Add(-2 * time.Hour)
		consumptionSince := state.TransitionTime.Add(-30 * time.Minute)
		consumptionFirstStarted := state.TransitionTime.Add(-90 * time.Minute)
		state.BMaaSMeterState = projection.BMaaSMeterState{
			Allocation: projection.MeterState{
				ActiveSince:    &allocationSince,
				FirstStartedAt: &allocationFirstStarted,
			},
			Consumption: projection.MeterState{
				ActiveSince:    &consumptionSince,
				FirstStartedAt: &consumptionFirstStarted,
			},
		}
		state.BillableSince = &allocationSince

		Expect(store.Upsert(ctx, state)).To(Succeed())

		got, err := store.Get(ctx, state.ResourceID)
		Expect(err).NotTo(HaveOccurred())
		expectMeterStateEqual(got.BMaaSMeterState, state.BMaaSMeterState)
	})

	It("preserves first-started timestamps when a newer upsert omits one", func() {
		ctx := context.Background()
		state := makeState("bmi-ever-started-merge", 1)
		state.ResourceType = schema.ResourceTypeBareMetalInstance
		allocationFirstStarted := state.TransitionTime.Add(-time.Hour)
		state.BMaaSMeterState = projection.BMaaSMeterState{
			Allocation: projection.MeterState{FirstStartedAt: &allocationFirstStarted},
		}
		Expect(store.Upsert(ctx, state)).To(Succeed())

		state.FulfillmentVersion = 2
		consumptionFirstStarted := state.TransitionTime.Add(-30 * time.Minute)
		state.BMaaSMeterState = projection.BMaaSMeterState{
			Consumption: projection.MeterState{FirstStartedAt: &consumptionFirstStarted},
		}
		Expect(store.Upsert(ctx, state)).To(Succeed())

		got, err := store.Get(ctx, state.ResourceID)
		Expect(err).NotTo(HaveOccurred())
		expectMeterStateEqual(got.BMaaSMeterState, projection.BMaaSMeterState{
			Allocation:  projection.MeterState{FirstStartedAt: &allocationFirstStarted},
			Consumption: projection.MeterState{FirstStartedAt: &consumptionFirstStarted},
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
		It("does not delete after the projection advances and retains its watermark", func() {
			ctx := context.Background()
			state := makeState("vm-delete-version", 2)
			Expect(store.Upsert(ctx, state)).To(Succeed())

			newer := state
			newer.FulfillmentVersion = 3
			newer.CurrentState = "STOPPED"
			Expect(store.Upsert(ctx, newer)).To(Succeed())

			deleted, err := store.DeleteIfVersion(ctx, state.ResourceID, state.FulfillmentVersion)
			Expect(err).ToNot(HaveOccurred())
			Expect(deleted).To(BeFalse())
			got, err := store.Get(ctx, state.ResourceID)
			Expect(err).ToNot(HaveOccurred())
			Expect(got.Deleted).To(BeFalse())
			Expect(got.FulfillmentVersion).To(Equal(newer.FulfillmentVersion))

			deleted, err = store.DeleteIfVersion(ctx, state.ResourceID, newer.FulfillmentVersion)
			Expect(err).ToNot(HaveOccurred())
			Expect(deleted).To(BeTrue())

			got, err = store.Get(ctx, state.ResourceID)
			Expect(err).ToNot(HaveOccurred())
			Expect(got).ToNot(BeNil())
			Expect(got.FulfillmentVersion).To(Equal(newer.FulfillmentVersion))
			Expect(got.Deleted).To(BeTrue())

			results, err := store.ListAll(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(results).To(BeEmpty())

			Expect(store.Upsert(ctx, newer)).To(MatchError(projection.ErrStaleVersion))

			newest := newer
			newest.FulfillmentVersion++
			Expect(store.Upsert(ctx, newest)).To(MatchError(projection.ErrStaleVersion))
		})

		It("clears active BMaaS meter intervals when tombstoning", func() {
			ctx := context.Background()
			state := makeState("bmi-delete-meters", 4)
			state.ResourceType = schema.ResourceTypeBareMetalInstance
			allocationSince := state.TransitionTime.Add(-2 * time.Hour)
			consumptionSince := state.TransitionTime.Add(-time.Hour)
			state.BMaaSMeterState = projection.BMaaSMeterState{
				Allocation: projection.MeterState{
					ActiveSince:    &allocationSince,
					FirstStartedAt: &allocationSince,
				},
				Consumption: projection.MeterState{
					ActiveSince:    &consumptionSince,
					FirstStartedAt: &consumptionSince,
				},
			}
			Expect(store.Upsert(ctx, state)).To(Succeed())

			deleted, err := store.DeleteIfVersion(ctx, state.ResourceID, state.FulfillmentVersion)
			Expect(err).ToNot(HaveOccurred())
			Expect(deleted).To(BeTrue())

			got, err := store.Get(ctx, state.ResourceID)
			Expect(err).ToNot(HaveOccurred())
			Expect(got.Deleted).To(BeTrue())
			Expect(got.IsBillable).To(BeFalse())
			Expect(got.BillableSince).To(BeNil())
			Expect(got.BMaaSMeterState.Allocation.ActiveSince).To(BeNil())
			Expect(got.BMaaSMeterState.Consumption.ActiveSince).To(BeNil())
			Expect(got.BMaaSMeterState.Allocation.FirstStartedAt).ToNot(BeNil())
			Expect(got.BMaaSMeterState.Consumption.FirstStartedAt).ToNot(BeNil())
		})

		It("retains a deletion tombstone for an existing resource", func() {
			ctx := context.Background()
			state := makeState("vm-del", 1)
			Expect(store.Upsert(ctx, state)).To(Succeed())

			deleted, err := store.DeleteIfVersion(ctx, "vm-del", state.FulfillmentVersion)
			Expect(err).ToNot(HaveOccurred())
			Expect(deleted).To(BeTrue())

			got, err := store.Get(ctx, "vm-del")
			Expect(err).ToNot(HaveOccurred())
			Expect(got).ToNot(BeNil())
			Expect(got.FulfillmentVersion).To(Equal(int32(1)))
		})

		It("Returns nil for non-existent resource", func() {
			ctx := context.Background()
			deleted, err := store.DeleteIfVersion(ctx, "nonexistent", 1)
			Expect(err).NotTo(HaveOccurred())
			Expect(deleted).To(BeFalse())
		})
	})

	Describe("ListBillable", func() {
		It("uses normalized allocation activity as the BMaaS billable source", func() {
			ctx := context.Background()
			active := makeState("bmi-active-allocation", 1)
			active.ResourceType = schema.ResourceTypeBareMetalInstance
			active.IsBillable = false
			active.BillableSince = nil
			allocationSince := active.TransitionTime.Add(-time.Hour)
			active.BMaaSMeterState.Allocation.ActiveSince = &allocationSince
			Expect(store.Upsert(ctx, active)).To(Succeed())

			inactive := makeState("bmi-inactive-allocation", 1)
			inactive.ResourceType = schema.ResourceTypeBareMetalInstance
			inactive.BMaaSMeterState.Allocation.ActiveSince = nil
			Expect(store.Upsert(ctx, inactive)).To(Succeed())

			results, err := store.ListBillable(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(results).To(HaveLen(1))
			Expect(results[0].ResourceID).To(Equal("bmi-active-allocation"))
		})

		It("uses normalized allocation state and canonical lifecycle state for BMaaS", func() {
			ctx := context.Background()

			stoppedActive := makeState("bmi-stopped-active", 1)
			stoppedActive.ResourceType = schema.ResourceTypeBareMetalInstance
			stoppedActive.CurrentState = "BARE_METAL_INSTANCE_STATE_STOPPED"
			stoppedActive.IsBillable = false
			stoppedActive.BillableSince = nil
			allocationSince := stoppedActive.TransitionTime.Add(-time.Hour)
			stoppedActive.BMaaSMeterState.Allocation.ActiveSince = &allocationSince
			Expect(store.Upsert(ctx, stoppedActive)).To(Succeed())

			failedActive := makeState("bmi-failed-active", 1)
			failedActive.ResourceType = schema.ResourceTypeBareMetalInstance
			failedActive.CurrentState = "BARE_METAL_INSTANCE_STATE_FAILED"
			failedActive.BMaaSMeterState.Allocation.ActiveSince = &allocationSince
			Expect(store.Upsert(ctx, failedActive)).To(Succeed())

			failedShortActive := makeState("bmi-failed-short-active", 1)
			failedShortActive.ResourceType = schema.ResourceTypeBareMetalInstance
			failedShortActive.CurrentState = "FAILED"
			failedShortActive.BMaaSMeterState.Allocation.ActiveSince = &allocationSince
			Expect(store.Upsert(ctx, failedShortActive)).To(Succeed())

			parentOnly := makeState("bmi-parent-only", 1)
			parentOnly.ResourceType = schema.ResourceTypeBareMetalInstance
			parentOnly.BMaaSMeterState.Allocation.ActiveSince = nil
			parentOnly.IsBillable = true
			Expect(store.Upsert(ctx, parentOnly)).To(Succeed())

			got, err := store.Get(ctx, stoppedActive.ResourceID)
			Expect(err).ToNot(HaveOccurred())
			expectTimeInstant(got.BillableSince, &allocationSince)
			Expect(got.IsBillable).To(BeTrue())

			results, err := store.ListBillable(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(results).To(HaveLen(1))
			Expect(results[0].ResourceID).To(Equal(stoppedActive.ResourceID))
		})

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
