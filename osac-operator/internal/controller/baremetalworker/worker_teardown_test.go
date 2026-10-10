// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type teardownReadClient struct {
	*workerReadClient
	returned *privatev1.BareMetalInstance
	deletes  int
}

func (f *teardownReadClient) GetBareMetalInstance(ctx context.Context, id string) (*privatev1.BareMetalInstance, error) {
	if f.returned != nil {
		f.gets++
		return f.returned, nil
	}
	return f.bmiObservationClient.GetBareMetalInstance(ctx, id)
}
func (f *teardownReadClient) DeleteBareMetalInstance(context.Context, string) error {
	f.deletes++
	return nil
}

var _ = Describe("teardown uses one fresh owned read without inventing absence", func() {
	for _, state := range []string{"present", "absent", "error", "foreign", "wrong-id"} {
		It(state, func() {
			r, fc, co := workerReadHarness()
			provider := &teardownReadClient{workerReadClient: fc}
			r.fulfillment = provider
			w := co.Status.Workers[0]
			w.Phase = workerPhaseDeleting
			provider.returned = ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID)
			switch state {
			case "absent":
				provider.returned = nil
				fc.getErr = status.Error(codes.NotFound, "gone")
			case "error":
				provider.returned = nil
				fc.getErr = status.Error(codes.Internal, "unknown")
			case "foreign":
				provider.returned.GetMetadata().SetTenant("foreign")
			case "wrong-id":
				provider.returned.SetId("different-id")
			}
			observed := indexWorkerBMIs(nil)
			observed.agents = &unstructured.UnstructuredList{}
			kept := r.reconcileTeardownWorkers(context.Background(), co, []v1alpha1.WorkerStatus{w})
			wantDeletes := 0
			if state == "present" {
				wantDeletes = 1
			}
			Expect(provider.gets).To(Equal(1), "gets=%d deletes=%d, want 1/%d", provider.gets, provider.deletes, wantDeletes)
			Expect(provider.deletes).To(Equal(wantDeletes), "gets=%d deletes=%d, want 1/%d", provider.gets, provider.deletes, wantDeletes)
			Expect((len(kept) == 0)).To(Equal((state == "absent")), "worker removal without confirmed NotFound: %v", kept)
		})
	}
})

type agentListCountingClient struct {
	client.Client
	agentLists int
}

func (c *agentListCountingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if agents, ok := list.(*unstructured.UnstructuredList); ok && agents.GetKind() == "AgentList" {
		c.agentLists++
	}
	return c.Client.List(ctx, list, opts...)
}

var _ = It("retains the finalizer when another writer appends a worker", func() {
	r, fc, co := workerReadHarness()
	fc.getErr = status.Error(codes.NotFound, "confirmed deleted")
	co.Finalizers = []string{bmWorkerFinalizer}
	Expect(r.Update(context.Background(), co)).To(Succeed())
	Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	kube := r.Client
	appended := newWorkerStatus("standard", "standard", "concurrent", "", workerPhaseProvisioning)
	c := &bmiConflictClient{Client: kube, beforePatch: func() {
		latest := co.DeepCopy()
		Expect(kube.Get(context.Background(), client.ObjectKeyFromObject(co), latest)).To(Succeed())
		latest.Status.Workers = append(latest.Status.Workers, appended)
		Expect(kube.Status().Update(context.Background(), latest)).To(Succeed())
	}}
	r.Client = c
	o := indexWorkerBMIs(nil)
	o.agents = &unstructured.UnstructuredList{}
	res, err := r.handleClusterDeletion(context.Background(), co)
	Expect(apierrors.IsConflict(err)).To(BeTrue(), "result=%+v err=%v, want one-shot conflict", res, err)
	Expect(res.IsZero()).To(BeTrue(), "result=%+v err=%v, want one-shot conflict", res, err)
	latest := co.DeepCopy()
	Expect(kube.Get(context.Background(), client.ObjectKeyFromObject(co), latest)).To(Succeed())
	Expect(latest.Finalizers).To(HaveLen(1), "removed finalizer with appended worker: finalizers=%v result=%v", latest.Finalizers, res)
	Expect(latest.Finalizers[0]).To(Equal(bmWorkerFinalizer), "removed finalizer with appended worker: finalizers=%v result=%v", latest.Finalizers, res)
	Expect(res.IsZero()).To(BeTrue(), "removed finalizer with appended worker: finalizers=%v result=%v", latest.Finalizers, res)
	Expect(latest.Status.Workers).To(HaveLen(2), "workers=%v", latest.Status.Workers)
	Expect(latest.Status.Workers[1].Name).To(Equal(appended.Name), "workers=%v", latest.Status.Workers)
})

var _ = It("retains a failed worker slot during scale-down", func() {
	r, base, co := workerReadHarness()
	fc := &teardownReadClient{workerReadClient: base}
	r.fulfillment = fc
	w := co.Status.Workers[0]
	w.Phase = workerPhaseFailed
	fc.returned = ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID)
	kept := r.handleScaleDown(context.Background(), co, nil, []v1alpha1.WorkerStatus{w})
	Expect(kept).To(HaveLen(1), "retirement must precede external cleanup: workers=%+v deletes=%d", kept, fc.deletes)
	Expect(kept[0].BareMetalInstance).To(Equal(w.BareMetalInstance), "retirement must precede external cleanup: workers=%+v deletes=%d", kept, fc.deletes)
	Expect(kept[0].Phase).To(Equal(workerPhaseUnbinding), "retirement must precede external cleanup: workers=%+v deletes=%d", kept, fc.deletes)
	Expect(fc.deletes).To(Equal(0), "retirement must precede external cleanup: workers=%+v deletes=%d", kept, fc.deletes)
})

var _ = Describe("Worker retention when cleanup evidence is unknown", func() {
	for _, scenario := range []string{"outage", "denied", "foreign-tenant", "foreign-owner", "idless"} {
		It(scenario, func() {
			r, base, co := workerReadHarness()
			fc := &teardownReadClient{workerReadClient: base}
			r.fulfillment = fc
			w := co.Status.Workers[0]
			w.Phase = workerPhaseDeleting
			switch scenario {
			case "outage":
				base.getErr = status.Error(codes.Unavailable, "outage")
			case "denied":
				base.getErr = status.Error(codes.PermissionDenied, "denied")
			case "foreign-tenant", "foreign-owner":
				fc.returned = ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID)
				if scenario == "foreign-tenant" {
					fc.returned.GetMetadata().SetTenant("foreign")
				} else {
					fc.returned.GetMetadata().SetAnnotations(map[string]string{ownerReferenceAnnotation: "ClusterOrder/foreign"})
				}
			case "idless":
				w.BareMetalInstance.ID = ""
			}
			o := indexWorkerBMIs(nil)
			o.agents = &unstructured.UnstructuredList{}
			kept := r.reconcileTeardownWorkers(context.Background(), co, []v1alpha1.WorkerStatus{w})
			Expect(kept).To(HaveLen(1), "unknown released: workers=%+v deletes=%d", kept, fc.deletes)
			Expect(kept[0].BareMetalInstance).To(Equal(w.BareMetalInstance), "unknown released: workers=%+v deletes=%d", kept, fc.deletes)
			Expect(fc.deletes).To(Equal(0), "unknown released: workers=%+v deletes=%d", kept, fc.deletes)
		})
	}
})

var _ = It("avoids another Agent List for teardown during stable convergence", func() {
	r, _, co := workerReadHarness()
	c := &agentListCountingClient{Client: r.Client}
	r.Client = c
	_, err := r.reconcileWorkerTeardown(context.Background(), co)
	Expect(err).NotTo(HaveOccurred())
	Expect(c.agentLists).To(BeZero(), "unnecessary teardown Agent lists=%d", c.agentLists)
})
