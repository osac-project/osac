// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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

func reserveCleanupSlot(r *Reconciler, co *v1alpha1.ClusterOrder) {
	GinkgoHelper()
	ctx := context.Background()
	co.Status.Workers = nil
	Expect(r.Status().Update(ctx, co)).To(Succeed())
	if added, err := r.reserveWorkerSlots(ctx, co); err != nil || !added {
		Fail(fmt.Sprintf("reserve: added=%v err=%v", added, err))
	}
	Expect(r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
}

var _ = It("cancels a never-attempted reservation during deletion", func() {
	r, fc, co := workerReadHarness()
	ctx := context.Background()
	co.Status.Workers = nil
	Expect(r.Status().Update(ctx, co)).To(Succeed())
	if added, err := r.reserveWorkerSlots(ctx, co); err != nil || !added {
		Fail(fmt.Sprintf("reserve: added=%v err=%v", added, err))
	}
	key := client.ObjectKeyFromObject(co)
	Expect(r.apiReader.Get(ctx, key, co)).To(Succeed())
	Expect(r.Delete(ctx, co)).To(Succeed())
	// Re-read each durable boundary, just as a restarted reconciler would.
	for range 3 {
		Expect(r.apiReader.Get(ctx, key, co)).To(Succeed())
		if _, err := r.handleClusterDeletion(ctx, co); err != nil {
			Expect(err).NotTo(HaveOccurred())
		}
		if apierrors.IsNotFound(r.apiReader.Get(ctx, key, &v1alpha1.ClusterOrder{})) {
			break
		}
	}
	latest := &v1alpha1.ClusterOrder{}
	err := r.apiReader.Get(ctx, key, latest)
	Expect(apierrors.IsNotFound(err)).To(BeTrue(), "reservation blocked finalization: workers=%+v finalizers=%v err=%v", latest.Status.Workers, latest.Finalizers, err)
	Expect(fc.names).To(BeEmpty(), "never-attempted cancellation called provider: creates=%v gets=%d lists=%d", fc.names, fc.gets, fc.lists)
	Expect(fc.gets).To(Equal(0), "never-attempted cancellation called provider: creates=%v gets=%d lists=%d", fc.names, fc.gets, fc.lists)
	Expect(fc.lists).To(Equal(0), "never-attempted cancellation called provider: creates=%v gets=%d lists=%d", fc.names, fc.gets, fc.lists)
})

var _ = It("rejects a stale Create when cancellation wins the intent race", func() {
	r, fc, co := workerReadHarness()
	ctx := context.Background()
	co.Finalizers = append(co.Finalizers, "test.osac.openshift.io/hold")
	Expect(r.Update(ctx, co)).To(Succeed())
	reserveCleanupSlot(r, co)
	kube := r.Client
	cleanup := *r
	r.Client = &reservationStatusClient{Client: kube, before: func() {
		latest := &v1alpha1.ClusterOrder{}
		key := client.ObjectKeyFromObject(co)
		Expect(kube.Get(ctx, key, latest)).To(Succeed())
		Expect(kube.Delete(ctx, latest)).To(Succeed())
		for range 2 {
			Expect(kube.Get(ctx, key, latest)).To(Succeed())
			if _, err := cleanup.handleClusterDeletion(ctx, latest); err != nil {
				Expect(err).NotTo(HaveOccurred())
			}
		}
	}}
	_, err := runWorkerCapacityStage(r, co)
	Expect(apierrors.IsConflict(err)).To(BeTrue(), "attempt did not lose optimistic race: %v", err)
	Expect(fc.names).To(BeEmpty(), "stale invocation created after cancellation: %v", fc.names)
	latest := &v1alpha1.ClusterOrder{}
	Expect(kube.Get(ctx, client.ObjectKeyFromObject(co), latest)).To(Succeed())
	Expect(latest.Status.Workers).To(BeEmpty(), "cancelled reservation resurrected: %+v", latest.Status.Workers)
})

var _ = It("releases the attempted reservation when deletion wins the race", func() {
	r, _, co := workerReadHarness()
	reserveCleanupSlot(r, co)
	ctx := context.Background()
	kube := r.Client
	cleanup := *r
	r.Client = &reservationStatusClient{Client: kube, after: func() {
		latest := &v1alpha1.ClusterOrder{}
		key := client.ObjectKeyFromObject(co)
		Expect(kube.Get(ctx, key, latest)).To(Succeed())
		Expect(latest.Status.Workers[0].BMICreateState).To(Equal(v1alpha1.WorkerBMICreateStateAttempted), "attempt not durable before Create")
		Expect(kube.Delete(ctx, latest)).To(Succeed())
		for range 3 {
			err := kube.Get(ctx, key, latest)
			if apierrors.IsNotFound(err) {
				break
			}
			Expect(err).NotTo(HaveOccurred())
			if _, err := cleanup.handleClusterDeletion(ctx, latest); err != nil {
				Expect(err).NotTo(HaveOccurred())
			}
		}
		// The confirmed-absent BMI allows the worker to be released and
		// the finalizer removed, completing the deletion.
		Expect(apierrors.IsNotFound(kube.Get(ctx, key, latest))).To(BeTrue(),
			"deletion should complete when BMI is confirmed absent: workers=%v finalizers=%v",
			latest.Status.Workers, latest.Finalizers)
	}}
	_, err := runWorkerCapacityStage(r, co)
	// The ClusterOrder is deleted within the hook, so the capacity
	// stage's follow-up write sees either a conflict or a NotFound.
	Expect(err).To(HaveOccurred(), "identity write after retirement should fail: %v", err)
})

var _ = Describe("Cleanup of ID-less workers with empty List", func() {
	for _, state := range []string{"", v1alpha1.WorkerBMICreateStateAttempted, "unknown"} {
		It("state="+state, func() {
			r, fc, co := workerReadHarness()
			co.Status.Workers[0].BareMetalInstance.ID = ""
			co.Status.Workers[0].BMICreateState = state
			co.Status.Workers[0].Phase = workerPhaseUnbinding
			Expect(r.Status().Update(context.Background(), co)).To(Succeed())
			w := co.Status.Workers[0]
			gone, err := r.cleanupWorker(context.Background(), co, &w)
			Expect(err).NotTo(HaveOccurred(), "confirmed-absent BMI blocked cleanup: gone=%v err=%v worker=%+v", gone, err, w)
			Expect(gone).To(BeTrue(), "confirmed-absent BMI blocked cleanup: gone=%v err=%v worker=%+v", gone, err, w)
			Expect(w.BareMetalInstance.ID).To(Equal(""), "confirmed-absent BMI blocked cleanup: gone=%v err=%v worker=%+v", gone, err, w)
			Expect(fc.lists).To(Equal(1), "lists=%d creates=%v", fc.lists, fc.names)
			Expect(fc.names).To(BeEmpty(), "lists=%d creates=%v", fc.lists, fc.names)
		})
	}
})

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

var _ = It("releases a lost-Create worker when the List confirms absence", func() {
	r, fc, co := workerReadHarness()
	reserveCleanupSlot(r, co)
	r.fulfillment = &lostReservationCreateClient{workerReadClient: fc}
	ctx := context.Background()
	if _, err := runWorkerCapacityStage(r, co); err == nil {
		Fail("expected lost acknowledgement")
	}
	// The nodeSetClient accepted the Create; clear its store so
	// the fixture is consistent with the empty List that follows.
	fc.bmis = nil
	Expect(r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
	w := co.Status.Workers[0]
	Expect(w.BMICreateState).To(Equal(v1alpha1.WorkerBMICreateStateAttempted), "missing durable unknown outcome: %+v", w)
	Expect(w.BareMetalInstance.ID).To(Equal(""), "missing durable unknown outcome: %+v", w)
	// The List succeeds but the BMI is not found — confirmed absent.
	gone, err := r.cleanupWorker(ctx, co, &w)
	Expect(err).NotTo(HaveOccurred(), "confirmed-absent BMI blocked cleanup: gone=%v err=%v", gone, err)
	Expect(gone).To(BeTrue(), "confirmed-absent BMI blocked cleanup: gone=%v err=%v", gone, err)
})

var _ = It("recovers identity when the List finds the lost-Create BMI", func() {
	r, base, co := workerReadHarness()
	fc := &teardownReadClient{workerReadClient: base}
	r.fulfillment = fc
	w := co.Status.Workers[0]
	w.Phase = workerPhaseUnbinding
	w.BMICreateState = v1alpha1.WorkerBMICreateStateAttempted
	fc.returned = ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID)
	base.listed = []*privatev1.BareMetalInstance{fc.returned}
	w.BareMetalInstance.ID = ""
	gone, err := r.cleanupWorker(context.Background(), co, &w)
	Expect(err).NotTo(HaveOccurred(), "owned recovery: gone=%v err=%v worker=%+v", gone, err, w)
	Expect(gone).To(BeFalse(), "owned recovery: gone=%v err=%v worker=%+v", gone, err, w)
	Expect(w.BareMetalInstance.ID).To(Equal(fc.returned.GetId()), "owned recovery: gone=%v err=%v worker=%+v", gone, err, w)
})

var _ = It("requires persisted Create intent in the status patch response", func() {
	r, fc, co := workerReadHarness()
	reserveCleanupSlot(r, co)
	r.Client = &reservationStatusClient{Client: r.Client, stripState: true}
	if _, err := runWorkerCapacityStage(r, co); err == nil {
		Fail("Create accepted a pruned intent write")
	}
	Expect(fc.names).To(BeEmpty(), "Create issued without durable intent: %v", fc.names)
})

var _ = It("resets Create authorization only after retry cleanup confirms absence", func() {
	r, fc, co := workerReadHarness()
	co.Status.Workers[0].BMICreateState = v1alpha1.WorkerBMICreateStateAttempted
	co.Status.Workers[0].Phase = workerPhaseFailed
	Expect(r.Status().Update(context.Background(), co)).To(Succeed())
	fc.getErr = status.Error(codes.NotFound, "old incarnation gone")
	Expect(r.handleFailedWorkers(context.Background(), co)).To(Succeed())
	w := co.Status.Workers[0]
	Expect(w.BMICreateState).To(Equal(v1alpha1.WorkerBMICreateStateReserved), "retry not safely reserved: %+v", w)
	Expect(w.BareMetalInstance.ID).To(Equal(""), "retry not safely reserved: %+v", w)
	Expect(w.NextRetryTime).NotTo(BeNil(), "retry not safely reserved: %+v", w)
})
