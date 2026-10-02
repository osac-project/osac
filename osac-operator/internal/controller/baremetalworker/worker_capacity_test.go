// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type capacityMutationClient struct {
	*nodeSetClient
	beforeType  func()
	afterCreate func()
}

func (f *capacityMutationClient) GetBareMetalInstanceType(ctx context.Context, name string) (*privatev1.BareMetalInstanceType, error) {
	if f.beforeType != nil {
		f.beforeType()
		f.beforeType = nil
	}
	return f.nodeSetClient.GetBareMetalInstanceType(ctx, name)
}
func (f *capacityMutationClient) CreateBareMetalInstance(ctx context.Context, bmi *privatev1.BareMetalInstance) (*privatev1.BareMetalInstance, error) {
	created, err := f.nodeSetClient.CreateBareMetalInstance(ctx, bmi)
	if err == nil && f.afterCreate != nil {
		f.afterCreate()
		f.afterCreate = nil
	}
	return created, err
}
func mutateCapacityOrder(t *testing.T, r *Reconciler, co *v1alpha1.ClusterOrder, mutate func(*v1alpha1.ClusterOrder)) {
	t.Helper()
	latest := &v1alpha1.ClusterOrder{}
	ctx := context.Background()
	if err := r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
		t.Fatal(err)
	}
	mutate(latest)
	desiredStatus := latest.Status
	if err := r.Update(ctx, latest); err != nil {
		t.Fatal(err)
	}
	latest.Status = desiredStatus
	if err := r.Status().Update(ctx, latest); err != nil {
		t.Fatal(err)
	}
}
func TestR01ReservationReturnsBeforeCreate(t *testing.T) {
	r, fc, co := nodeSetHarness(t, "r01-reserve", nodeRequest("standard", 2))
	res, err := r.reconcileWorkerCapacity(context.Background(), co, "tenant", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if len(co.Status.Workers) != 2 || len(fc.names) != 0 || res.IsZero() {
		t.Fatalf("reservation boundary: workers=%+v creates=%v result=%+v", co.Status.Workers, fc.names, res)
	}
	for _, w := range co.Status.Workers {
		if w.BareMetalInstance.Name == "" || w.BareMetalInstance.ID != "" {
			t.Fatalf("invalid reservation: %+v", w)
		}
	}
}

func TestR01SingleCreateBoundary(t *testing.T) {
	r, fc, co := nodeSetHarness(t, "r01-create", nodeRequest("standard", 2))
	if _, err := r.reserveWorkerSlots(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	res, err := r.reconcileWorkerCapacity(context.Background(), co, "tenant", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if len(fc.names) != 1 || res.IsZero() {
		t.Fatalf("create boundary: creates=%v result=%+v", fc.names, res)
	}
	if w := workerByName(co.Status.Workers, fc.names[0]); w == nil || w.BareMetalInstance.ID == "" {
		t.Fatalf("successful create identity not persisted: %+v", co.Status.Workers)
	}
}

func TestR01StaleCachedOrderUsesPersistedSlots(t *testing.T) {
	r, fc, co := nodeSetHarness(t, "r01-stale", nodeRequest("standard", 2))
	stale := co.DeepCopy()
	if _, err := r.reserveWorkerSlots(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	names := []string{co.Status.Workers[0].Name, co.Status.Workers[1].Name}
	for i := range 2 {
		if _, err := r.reconcileWorkerCapacity(context.Background(), stale.DeepCopy(), "tenant", nil, nil); err != nil {
			t.Fatal(err)
		}
		if len(fc.names) != i+1 {
			t.Fatalf("invocation %d creates=%v", i, fc.names)
		}
	}
	if err := r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if len(co.Status.Workers) != 2 || co.Status.Workers[0].Name != names[0] || co.Status.Workers[1].Name != names[1] || len(fc.bmis) != 2 {
		t.Fatalf("stale snapshot changed reservations or duplicated allocation: %+v", co.Status.Workers)
	}
}

func TestR01NoOpCapacityDoesNotRequeueForRetentionOrdering(t *testing.T) {
	r, _, co := nodeSetHarness(t, "r01-noop", nodeRequest("standard", 2))
	co.Status.Workers = []v1alpha1.WorkerStatus{
		newWorkerStatus("standard", "standard", "pending", "pending-id", workerPhaseWaitingForAgent),
		newWorkerStatus("standard", "standard", "ready", "ready-id", workerPhaseReady),
	}
	if err := r.Status().Update(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	res, err := r.reconcileWorkerCapacity(context.Background(), co, "tenant", nil, nil)
	if err != nil || !res.IsZero() {
		t.Fatalf("unchanged capacity requeued solely for sorted plan: result=%+v err=%v", res, err)
	}
}

func TestR01WaitingRetryDoesNotBlockAnotherReservation(t *testing.T) {
	r, fc, co := nodeSetHarness(t, "r01-retry", nodeRequest("standard", 2))
	future := metav1.NewTime(time.Now().Add(time.Hour))
	co.Status.Workers = []v1alpha1.WorkerStatus{
		newWorkerStatus("standard", "standard", "waiting", "", workerPhaseFailed),
		newWorkerStatus("standard", "standard", "actionable", "", workerPhaseFailed),
	}
	co.Status.Workers[0].NextRetryTime = &future
	if err := r.Status().Update(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	res, err := r.reconcileWorkerCapacity(context.Background(), co, "tenant", nil, nil)
	if err != nil || res.IsZero() || len(fc.names) != 1 || fc.names[0] != "actionable" {
		t.Fatalf("waiting slot blocked progress: result=%+v err=%v creates=%v", res, err, fc.names)
	}
}

type capacityRetryClient struct {
	*workerReadClient
	deletes   []string
	deleteErr error
}

func (f *capacityRetryClient) DeleteBareMetalInstance(_ context.Context, id string) error {
	f.deletes = append(f.deletes, id)
	return f.deleteErr
}

func TestR01FailedCapacityReturnsAfterOneRetryDelete(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{{"success", nil}, {"error", errors.New("provider deletion pending")}} {
		t.Run(tc.name, func(t *testing.T) {
			deleteErr := tc.err
			r, base, co := workerReadHarness(t)
			fc := &capacityRetryClient{workerReadClient: base, deleteErr: deleteErr}
			r.fulfillment = fc
			co.Spec.NodeRequests[0].NumberOfNodes = 2
			if err := r.Update(context.Background(), co); err != nil {
				t.Fatal(err)
			}
			co.Status.Workers[0].Phase = workerPhaseFailed
			co.Status.Workers = append(co.Status.Workers, newWorkerStatus("standard", "standard", "second", "second-id", workerPhaseFailed))
			if err := r.Status().Update(context.Background(), co); err != nil {
				t.Fatal(err)
			}
			fc.bmis = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "recorded-id"), ownedBMIFixture(co, "second", "second-id")}
			res, err := r.reconcileWorkerCapacity(context.Background(), co, "tenant", nil, nil)
			if !errors.Is(err, deleteErr) || res.IsZero() != (deleteErr != nil) || len(fc.deletes) != 1 || len(fc.names) != 0 {
				t.Fatalf("retry-delete boundary: result=%+v err=%v deletes=%v creates=%v", res, err, fc.deletes, fc.names)
			}
			if err := r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
				t.Fatal(err)
			}
			wantID := ""
			if deleteErr != nil {
				wantID = "recorded-id"
			}
			if co.Status.Workers[0].BareMetalInstance.ID != wantID || co.Status.Workers[1].BareMetalInstance.ID != "second-id" {
				t.Fatalf("retry changed wrong slots: %+v", co.Status.Workers)
			}
		})
	}
}

func TestCapacityActionRejectsChangedSpecOrSlot(t *testing.T) {
	for _, mutation := range []string{"spec", "deletion", "reference", "failed", "appended"} {
		t.Run(mutation, func(t *testing.T) {
			r, fc, co := nodeSetHarness(t, "guard-capacity", nodeRequest("standard", 1))
			if _, err := r.reserveWorkerSlots(context.Background(), co); err != nil {
				t.Fatal(err)
			}
			provider := &capacityMutationClient{nodeSetClient: fc}
			r.fulfillment = provider
			provider.beforeType = func() {
				mutateCapacityOrder(t, r, co, func(latest *v1alpha1.ClusterOrder) {
					switch mutation {
					case "spec":
						latest.Spec.NodeRequests[0].NumberOfNodes = 0
					case "deletion":
						latest.Finalizers = []string{bmWorkerFinalizer}
					case "reference":
						latest.Status.Workers[0].BareMetalInstance.Name = "replacement"
					case "failed":
						latest.Status.Workers[0].Phase = workerPhaseFailed
					case "appended":
						latest.Status.Workers = append(latest.Status.Workers, newWorkerStatus("standard", "standard", "appended", "appended-id", workerPhaseReady))
					}
				})
				if mutation == "deletion" {
					latest := &v1alpha1.ClusterOrder{}
					if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), latest); err != nil {
						t.Fatal(err)
					}
					if err := r.Delete(context.Background(), latest); err != nil {
						t.Fatal(err)
					}
				}
			}
			res, err := r.reconcileWorkerCapacity(context.Background(), co, "tenant", nil, nil)
			if err == nil && res.IsZero() {
				t.Fatal("capacity action accepted stale plan")
			}
			if len(fc.names) != 0 {
				t.Fatalf("created from stale plan: %v", fc.names)
			}
		})
	}
}
func TestCreatePersistencePreservesAppendedSlotAndStopsNextAction(t *testing.T) {
	r, fc, co := nodeSetHarness(t, "append-after-create", nodeRequest("standard", 2))
	if _, err := r.reserveWorkerSlots(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	provider := &capacityMutationClient{nodeSetClient: fc}
	r.fulfillment = provider
	provider.afterCreate = func() {
		mutateCapacityOrder(t, r, co, func(latest *v1alpha1.ClusterOrder) {
			latest.Status.Workers = append(latest.Status.Workers, newWorkerStatus("standard", "standard", "appended", "appended-id", workerPhaseReady))
		})
	}
	res, err := r.reconcileWorkerCapacity(context.Background(), co, "tenant", nil, nil)
	if err == nil && res.IsZero() {
		t.Fatal("did not stop after new capacity evidence")
	}
	if len(fc.names) != 1 {
		t.Fatalf("creates=%v, want only first action", fc.names)
	}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if len(co.Status.Workers) != 3 {
		t.Fatalf("lost appended slot: %+v", co.Status.Workers)
	}
	w := workerByName(co.Status.Workers, fc.names[0])
	if w == nil || w.BareMetalInstance.ID == "" {
		t.Fatal("lost successful create identity")
	}
}
func TestReservationRejectsReplacementOrder(t *testing.T) {
	r, _, co := nodeSetHarness(t, "replacement-order", nodeRequest("standard", 1))
	stale := co.DeepCopy()
	stale.UID = "old-uid"
	if _, err := r.reserveWorkerSlots(context.Background(), stale); !errors.Is(err, errWorkerObservationChanged) {
		t.Fatalf("error=%v, want identity guard", err)
	}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if len(co.Status.Workers) != 0 {
		t.Fatal("reserved capacity on replacement order")
	}
}
func TestConcurrentForeignReferenceStopsCapacity(t *testing.T) {
	r, fc, co := workerReadHarness(t)
	initial := co.DeepCopy()
	fc.listed = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "recorded-id")}
	observed, res, err := r.observeWorkerResources(context.Background(), co)
	if err != nil || !res.IsZero() {
		t.Fatalf("observe=%+v %v", res, err)
	}
	if _, err := r.observeExistingWorkers(context.Background(), co, "tenant", observed); err != nil {
		t.Fatal(err)
	}
	foreign := ownedBMIFixture(co, "foreign-bmi", "foreign-id")
	foreign.GetMetadata().SetTenant("foreign")
	fc.bmis = append(fc.bmis, foreign)
	mutateCapacityOrder(t, r, co, func(latest *v1alpha1.ClusterOrder) {
		latest.Status.Workers = append(latest.Status.Workers, v1alpha1.WorkerStatus{Name: "foreign", Kind: workerKindBMI, NodeSet: "standard", Phase: workerPhaseReady, BareMetalInstance: v1alpha1.BareMetalInstanceReference{Name: "foreign-bmi", ID: "foreign-id"}, CreationTimestamp: metav1.Now()})
	})
	if _, err := r.reconcileWorkerCapacity(context.Background(), initial, "tenant", nil, nil, observed); err == nil {
		t.Fatal("unverified refreshed reference permitted capacity actions")
	}
	if len(fc.names) != 0 {
		t.Fatal("provisioned after foreign reference refresh")
	}
}
