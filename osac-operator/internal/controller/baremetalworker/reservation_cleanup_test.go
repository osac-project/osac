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

var _ = It("retains the reservation when Create intent wins the cancellation race", func() {
	r, fc, co := workerReadHarness()
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
		for range 2 {
			Expect(kube.Get(ctx, key, latest)).To(Succeed())
			if _, err := cleanup.handleClusterDeletion(ctx, latest); err != nil {
				Expect(err).NotTo(HaveOccurred())
			}
		}
		Expect(kube.Get(ctx, key, latest)).To(Succeed())
		Expect(latest.Status.Workers).To(HaveLen(1), "attempted reservation released before Create")
	}}
	_, err := runWorkerCapacityStage(r, co)
	Expect(apierrors.IsConflict(err)).To(BeTrue(), "identity write after retirement should conflict: %v", err)
	Expect(fc.names).To(HaveLen(1), "creates=%v bmis=%v", fc.names, fc.bmis)
	Expect(fc.bmis).To(HaveLen(1), "creates=%v bmis=%v", fc.names, fc.bmis)
	Expect(kube.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
	fc.listed = fc.bmis
	w := co.Status.Workers[0]
	gone, err := cleanup.cleanupWorker(ctx, co, &w)
	Expect(err).NotTo(HaveOccurred(), "lost identity not recovered: worker=%+v gone=%v err=%v", w, gone, err)
	Expect(gone).To(BeFalse(), "lost identity not recovered: worker=%+v gone=%v err=%v", w, gone, err)
	Expect(w.BareMetalInstance.ID).To(Equal(fc.bmis[0].GetId()), "lost identity not recovered: worker=%+v gone=%v err=%v", w, gone, err)
})

var _ = Describe("Conservative cleanup of ID-less workers", func() {
	for _, state := range []string{"", v1alpha1.WorkerBMICreateStateAttempted, "unknown"} {
		It("state="+state, func() {
			r, fc, co := workerReadHarness()
			co.Status.Workers[0].BareMetalInstance.ID = ""
			co.Status.Workers[0].BMICreateState = state
			co.Status.Workers[0].Phase = workerPhaseUnbinding
			Expect(r.Status().Update(context.Background(), co)).To(Succeed())
			w := co.Status.Workers[0]
			for range 2 {
				gone, err := r.cleanupWorker(context.Background(), co, &w)
				Expect(err).To(HaveOccurred(), "empty List released unknown outcome: gone=%v err=%v worker=%+v", gone, err, w)
				Expect(gone).To(BeFalse(), "empty List released unknown outcome: gone=%v err=%v worker=%+v", gone, err, w)
				Expect(w.BareMetalInstance.ID).To(Equal(""), "empty List released unknown outcome: gone=%v err=%v worker=%+v", gone, err, w)
			}
			Expect(fc.lists).To(Equal(2), "lists=%d creates=%v", fc.lists, fc.names)
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

var _ = It("retains and recovers identity after a lost Create acknowledgement", func() {
	r, fc, co := workerReadHarness()
	reserveCleanupSlot(r, co)
	r.fulfillment = &lostReservationCreateClient{workerReadClient: fc}
	ctx := context.Background()
	if _, err := runWorkerCapacityStage(r, co); err == nil {
		Fail("expected lost acknowledgement")
	}
	Expect(r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
	w := co.Status.Workers[0]
	Expect(w.BMICreateState).To(Equal(v1alpha1.WorkerBMICreateStateAttempted), "missing durable unknown outcome: %+v", w)
	Expect(w.BareMetalInstance.ID).To(Equal(""), "missing durable unknown outcome: %+v", w)
	// Restart after a lost response; delayed visibility cannot authorize release.
	for range 2 {
		gone, err := r.cleanupWorker(ctx, co, &w)
		Expect(err).To(HaveOccurred(), "delayed List released attempt: gone=%v err=%v", gone, err)
		Expect(gone).To(BeFalse(), "delayed List released attempt: gone=%v err=%v", gone, err)
	}
	fc.listed = fc.bmis
	gone, err := r.cleanupWorker(ctx, co, &w)
	Expect(err).NotTo(HaveOccurred(), "owned recovery: gone=%v err=%v worker=%+v", gone, err, w)
	Expect(gone).To(BeFalse(), "owned recovery: gone=%v err=%v worker=%+v", gone, err, w)
	Expect(w.BareMetalInstance.ID).To(Equal(fc.bmis[0].GetId()), "owned recovery: gone=%v err=%v worker=%+v", gone, err, w)
	Expect(fc.names).To(HaveLen(1), "cleanup created another incarnation: %v", fc.names)
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
