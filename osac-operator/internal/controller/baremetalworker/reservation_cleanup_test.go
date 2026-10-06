// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// reservationStatusClient injects competing writes immediately around the
// optimistic transition, without adding production fault hooks.
type reservationStatusClient struct {
	client.Client
	before     func()
	after      func()
	stripState bool
}

func (c *reservationStatusClient) Status() client.SubResourceWriter {
	return &reservationStatusWriter{SubResourceWriter: c.Client.Status(), c: c}
}

type reservationStatusWriter struct {
	client.SubResourceWriter
	c *reservationStatusClient
}

func (w *reservationStatusWriter) Patch(ctx context.Context, obj client.Object, p client.Patch, opts ...client.SubResourcePatchOption) error {
	co, ok := obj.(*v1alpha1.ClusterOrder)
	if !ok || len(co.Status.Workers) == 0 || co.Status.Workers[0].BMICreateState != v1alpha1.WorkerBMICreateStateAttempted {
		return w.SubResourceWriter.Patch(ctx, obj, p, opts...)
	}
	if w.c.before != nil {
		hook := w.c.before
		w.c.before = nil
		hook()
	}
	if w.c.stripState {
		// Model an apiserver using a schema that prunes the new status field.
		co.Status.Workers[0].BMICreateState = ""
	}
	if err := w.SubResourceWriter.Patch(ctx, obj, p, opts...); err != nil {
		return err
	}
	if w.c.after != nil {
		hook := w.c.after
		w.c.after = nil
		hook()
	}
	return nil
}

func reserveCleanupSlot(t *testing.T, r *Reconciler, co *v1alpha1.ClusterOrder) {
	t.Helper()
	ctx := context.Background()
	co.Status.Workers = nil
	if err := r.Status().Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	if added, err := r.reserveWorkerSlots(ctx, co); err != nil || !added {
		t.Fatalf("reserve: added=%v err=%v", added, err)
	}
	if err := r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
}

func TestDeletionCancelsNeverAttemptedReservation(t *testing.T) {
	r, fc, co := workerReadHarness(t)
	ctx := context.Background()
	co.Status.Workers = nil
	if err := r.Status().Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	if added, err := r.reserveWorkerSlots(ctx, co); err != nil || !added {
		t.Fatalf("reserve: added=%v err=%v", added, err)
	}
	key := client.ObjectKeyFromObject(co)
	if err := r.apiReader.Get(ctx, key, co); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, co); err != nil {
		t.Fatal(err)
	}
	// Re-read each durable boundary, just as a restarted reconciler would.
	for range 3 {
		if err := r.apiReader.Get(ctx, key, co); err != nil {
			t.Fatal(err)
		}
		if _, err := r.handleClusterDeletion(ctx, co); err != nil {
			t.Fatal(err)
		}
		if apierrors.IsNotFound(r.apiReader.Get(ctx, key, &v1alpha1.ClusterOrder{})) {
			break
		}
	}
	latest := &v1alpha1.ClusterOrder{}
	err := r.apiReader.Get(ctx, key, latest)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("reservation blocked finalization: workers=%+v finalizers=%v err=%v", latest.Status.Workers, latest.Finalizers, err)
	}
	if len(fc.names) != 0 || fc.gets != 0 || fc.lists != 0 {
		t.Fatalf("never-attempted cancellation called provider: creates=%v gets=%d lists=%d", fc.names, fc.gets, fc.lists)
	}
}

func TestCancellationWinsCreateAttemptRace(t *testing.T) {
	r, fc, co := workerReadHarness(t)
	ctx := context.Background()
	co.Finalizers = append(co.Finalizers, "test.osac.openshift.io/hold")
	if err := r.Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	reserveCleanupSlot(t, r, co)
	kube := r.Client
	cleanup := *r
	r.Client = &reservationStatusClient{Client: kube, before: func() {
		latest := &v1alpha1.ClusterOrder{}
		key := client.ObjectKeyFromObject(co)
		if err := kube.Get(ctx, key, latest); err != nil {
			t.Fatal(err)
		}
		if err := kube.Delete(ctx, latest); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := kube.Get(ctx, key, latest); err != nil {
				t.Fatal(err)
			}
			if _, err := cleanup.handleClusterDeletion(ctx, latest); err != nil {
				t.Fatal(err)
			}
		}
	}}
	_, err := runWorkerCapacityStage(t, r, co)
	if !apierrors.IsConflict(err) {
		t.Fatalf("attempt did not lose optimistic race: %v", err)
	}
	if len(fc.names) != 0 {
		t.Fatalf("stale invocation created after cancellation: %v", fc.names)
	}
	latest := &v1alpha1.ClusterOrder{}
	if err := kube.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
		t.Fatal(err)
	}
	if len(latest.Status.Workers) != 0 {
		t.Fatalf("cancelled reservation resurrected: %+v", latest.Status.Workers)
	}
}

func TestAttemptWinsCancellationRace(t *testing.T) {
	r, fc, co := workerReadHarness(t)
	reserveCleanupSlot(t, r, co)
	ctx := context.Background()
	kube := r.Client
	cleanup := *r
	r.Client = &reservationStatusClient{Client: kube, after: func() {
		latest := &v1alpha1.ClusterOrder{}
		key := client.ObjectKeyFromObject(co)
		if err := kube.Get(ctx, key, latest); err != nil {
			t.Fatal(err)
		}
		if latest.Status.Workers[0].BMICreateState != v1alpha1.WorkerBMICreateStateAttempted {
			t.Fatal("attempt not durable before Create")
		}
		if err := kube.Delete(ctx, latest); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := kube.Get(ctx, key, latest); err != nil {
				t.Fatal(err)
			}
			if _, err := cleanup.handleClusterDeletion(ctx, latest); err != nil {
				t.Fatal(err)
			}
		}
		if err := kube.Get(ctx, key, latest); err != nil {
			t.Fatal(err)
		}
		if len(latest.Status.Workers) != 1 {
			t.Fatal("attempted reservation released before Create")
		}
	}}
	_, err := runWorkerCapacityStage(t, r, co)
	if !apierrors.IsConflict(err) {
		t.Fatalf("identity write after retirement should conflict: %v", err)
	}
	if len(fc.names) != 1 || len(fc.bmis) != 1 {
		t.Fatalf("creates=%v bmis=%v", fc.names, fc.bmis)
	}
	if err := kube.Get(ctx, client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	fc.listed = fc.bmis
	w := co.Status.Workers[0]
	gone, err := cleanup.cleanupWorker(ctx, co, &w)
	if err != nil || gone || w.BareMetalInstance.ID != fc.bmis[0].GetId() {
		t.Fatalf("lost identity not recovered: worker=%+v gone=%v err=%v", w, gone, err)
	}
}

func TestIDLessCleanupStateIsConservative(t *testing.T) {
	for _, state := range []string{"", v1alpha1.WorkerBMICreateStateAttempted, "unknown"} {
		t.Run("state="+state, func(t *testing.T) {
			r, fc, co := workerReadHarness(t)
			co.Status.Workers[0].BareMetalInstance.ID = ""
			co.Status.Workers[0].BMICreateState = state
			co.Status.Workers[0].Phase = workerPhaseUnbinding
			if err := r.Status().Update(context.Background(), co); err != nil {
				t.Fatal(err)
			}
			w := co.Status.Workers[0]
			for range 2 {
				gone, err := r.cleanupWorker(context.Background(), co, &w)
				if err == nil || gone || w.BareMetalInstance.ID != "" {
					t.Fatalf("empty List released unknown outcome: gone=%v err=%v worker=%+v", gone, err, w)
				}
			}
			if fc.lists != 2 || len(fc.names) != 0 {
				t.Fatalf("lists=%d creates=%v", fc.lists, fc.names)
			}
		})
	}
}

type lostReservationCreateClient struct{ *workerReadClient }

func (f *lostReservationCreateClient) CreateBareMetalInstance(ctx context.Context, bmi *privatev1.BareMetalInstance) (*privatev1.BareMetalInstance, error) {
	co := &v1alpha1.ClusterOrder{}
	if err := f.kube.Get(ctx, f.order, co); err != nil {
		return nil, err
	}
	if co.Status.Workers[0].BMICreateState != v1alpha1.WorkerBMICreateStateAttempted {
		return nil, errors.New("Create without durable authorization")
	}
	if _, err := f.nodeSetClient.CreateBareMetalInstance(ctx, bmi); err != nil {
		return nil, err
	}
	return nil, errors.New("lost successful Create acknowledgement")
}

func TestAttemptedLostCreateAcknowledgementRetainsAndRecoversIdentity(t *testing.T) {
	r, fc, co := workerReadHarness(t)
	reserveCleanupSlot(t, r, co)
	r.fulfillment = &lostReservationCreateClient{workerReadClient: fc}
	ctx := context.Background()
	if _, err := runWorkerCapacityStage(t, r, co); err == nil {
		t.Fatal("expected lost acknowledgement")
	}
	if err := r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	w := co.Status.Workers[0]
	if w.BMICreateState != v1alpha1.WorkerBMICreateStateAttempted || w.BareMetalInstance.ID != "" {
		t.Fatalf("missing durable unknown outcome: %+v", w)
	}
	// Restart after a lost response; delayed visibility cannot authorize release.
	for range 2 {
		gone, err := r.cleanupWorker(ctx, co, &w)
		if err == nil || gone {
			t.Fatalf("delayed List released attempt: gone=%v err=%v", gone, err)
		}
	}
	fc.listed = fc.bmis
	gone, err := r.cleanupWorker(ctx, co, &w)
	if err != nil || gone || w.BareMetalInstance.ID != fc.bmis[0].GetId() {
		t.Fatalf("owned recovery: gone=%v err=%v worker=%+v", gone, err, w)
	}
	if len(fc.names) != 1 {
		t.Fatalf("cleanup created another incarnation: %v", fc.names)
	}
}

func TestCreateRequiresPersistedIntentInPatchResponse(t *testing.T) {
	r, fc, co := workerReadHarness(t)
	reserveCleanupSlot(t, r, co)
	r.Client = &reservationStatusClient{Client: r.Client, stripState: true}
	if _, err := runWorkerCapacityStage(t, r, co); err == nil {
		t.Fatal("Create accepted a pruned intent write")
	}
	if len(fc.names) != 0 {
		t.Fatalf("Create issued without durable intent: %v", fc.names)
	}
}

func TestRetryCleanupResetsCreateAuthorization(t *testing.T) {
	r, fc, co := workerReadHarness(t)
	co.Status.Workers[0].BMICreateState = v1alpha1.WorkerBMICreateStateAttempted
	co.Status.Workers[0].Phase = workerPhaseFailed
	if err := r.Status().Update(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	fc.getErr = status.Error(codes.NotFound, "old incarnation gone")
	if err := r.handleFailedWorkers(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	w := co.Status.Workers[0]
	if w.BMICreateState != v1alpha1.WorkerBMICreateStateReserved || w.BareMetalInstance.ID != "" || w.NextRetryTime == nil {
		t.Fatalf("retry not safely reserved: %+v", w)
	}
}
