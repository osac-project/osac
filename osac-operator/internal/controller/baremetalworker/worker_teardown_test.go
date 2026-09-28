// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"testing"

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
func TestTeardownUsesOneFreshOwnedReadWithoutInventingAbsence(t *testing.T) {
	for _, state := range []string{"present", "absent", "error", "foreign", "wrong-id"} {
		t.Run(state, func(t *testing.T) {
			r, fc, co := workerReadHarness(t)
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
			if provider.gets != 1 || provider.deletes != wantDeletes {
				t.Fatalf("gets=%d deletes=%d, want 1/%d", provider.gets, provider.deletes, wantDeletes)
			}
			if (len(kept) == 0) != (state == "absent") {
				t.Fatalf("worker removal without confirmed NotFound: %v", kept)
			}
		})
	}
}

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
func TestFinalizationRetainsFinalizerForConcurrentAppendedWorker(t *testing.T) {
	r, fc, co := workerReadHarness(t)
	fc.getErr = status.Error(codes.NotFound, "confirmed deleted")
	co.Finalizers = []string{bmWorkerFinalizer}
	if err := r.Update(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	kube := r.Client
	appended := newWorkerStatus("standard", "standard", "concurrent", "", workerPhaseProvisioning)
	c := &bmiConflictClient{Client: kube, beforePatch: func() {
		latest := co.DeepCopy()
		if err := kube.Get(context.Background(), client.ObjectKeyFromObject(co), latest); err != nil {
			t.Fatal(err)
		}
		latest.Status.Workers = append(latest.Status.Workers, appended)
		if err := kube.Status().Update(context.Background(), latest); err != nil {
			t.Fatal(err)
		}
	}}
	r.Client = c
	o := indexWorkerBMIs(nil)
	o.agents = &unstructured.UnstructuredList{}
	res, err := r.handleClusterDeletion(context.Background(), co)
	if !apierrors.IsConflict(err) || !res.IsZero() {
		t.Fatalf("result=%+v err=%v, want one-shot conflict", res, err)
	}
	latest := co.DeepCopy()
	if err := kube.Get(context.Background(), client.ObjectKeyFromObject(co), latest); err != nil {
		t.Fatal(err)
	}
	if len(latest.Finalizers) != 1 || latest.Finalizers[0] != bmWorkerFinalizer || !res.IsZero() {
		t.Fatalf("removed finalizer with appended worker: finalizers=%v result=%v", latest.Finalizers, res)
	}
	if len(latest.Status.Workers) != 2 || latest.Status.Workers[1].Name != appended.Name {
		t.Fatalf("workers=%v", latest.Status.Workers)
	}
}

func TestR03FailedScaleDownKeepsSlot(t *testing.T) {
	r, base, co := workerReadHarness(t)
	fc := &teardownReadClient{workerReadClient: base}
	r.fulfillment = fc
	w := co.Status.Workers[0]
	w.Phase = workerPhaseFailed
	fc.returned = ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID)
	kept := r.handleScaleDown(context.Background(), co, nil, []v1alpha1.WorkerStatus{w})
	if len(kept) != 1 || kept[0].BareMetalInstance != w.BareMetalInstance || kept[0].Phase != workerPhaseUnbinding || fc.deletes != 0 {
		t.Fatalf("retirement must precede external cleanup: workers=%+v deletes=%d", kept, fc.deletes)
	}
}

func TestR03UnknownCleanupDoesNotRelease(t *testing.T) {
	for _, scenario := range []string{"outage", "denied", "foreign-tenant", "foreign-owner", "idless"} {
		t.Run(scenario, func(t *testing.T) {
			r, base, co := workerReadHarness(t)
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
			if len(kept) != 1 || kept[0].BareMetalInstance != w.BareMetalInstance || fc.deletes != 0 {
				t.Fatalf("unknown released: workers=%+v deletes=%d", kept, fc.deletes)
			}
		})
	}
}

func TestStableConvergenceDoesNotRelistAgentsForTeardown(t *testing.T) {
	r, _, co := workerReadHarness(t)
	c := &agentListCountingClient{Client: r.Client}
	r.Client = c
	_, err := r.reconcileWorkerTeardown(context.Background(), co)
	if err != nil {
		t.Fatal(err)
	}
	if c.agentLists != 0 {
		t.Fatalf("unnecessary teardown Agent lists=%d", c.agentLists)
	}
}
