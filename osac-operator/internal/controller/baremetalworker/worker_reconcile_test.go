// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// These public reconciles deliberately stop at the pull-secret gate. Identity and
// Agent repair must already be durable, without allocation or provisioning.
type workerReadClient struct {
	*bmiObservationClient
	listed []*privatev1.BareMetalInstance
	lists  int
}

func (f *workerReadClient) ListBareMetalInstances(context.Context, string) ([]*privatev1.BareMetalInstance, error) {
	f.lists++
	return f.listed, f.listErr
}
func (f *workerReadClient) GetCluster(_ context.Context, id string) (*privatev1.Cluster, error) {
	return privatev1.Cluster_builder{Id: id, Metadata: privatev1.Metadata_builder{Tenant: "tenant"}.Build(), Spec: privatev1.ClusterSpec_builder{Version: privatev1.ClusterVersionReference_builder{Id: "cv"}.Build()}.Build()}.Build(), nil
}
func workerReadHarness(t *testing.T) (*Reconciler, *workerReadClient, *v1alpha1.ClusterOrder) {
	t.Helper()
	r, fc, co := bmiStageHarness(t, workerPhaseWaitingForAgent, "recorded-id")
	if err := corev1.AddToScheme(r.scheme); err != nil {
		t.Fatal(err)
	}
	co.Finalizers = []string{bmWorkerFinalizer}
	co.Labels = map[string]string{clusterOrderIDLabel: "cluster"}
	co.Annotations = map[string]string{"osac.openshift.io/tenant": "tenant"}
	if err := r.Update(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	provider := &workerReadClient{bmiObservationClient: fc}
	r = NewReconciler(r.Client, r.apiReader, r.scheme, provider, nil, r.recorder, co.Namespace)
	return r, provider, co
}
func TestR01FinalizerReturnsBeforeObservation(t *testing.T) {
	r, fc, co := workerReadHarness(t)
	co.Finalizers = nil
	if err := r.Update(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
	if err != nil || res.IsZero() {
		t.Fatalf("finalizer boundary: result=%+v err=%v", res, err)
	}
	if err := r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if len(co.Finalizers) != 1 || co.Finalizers[0] != bmWorkerFinalizer || fc.lists != 0 || len(fc.names) != 0 {
		t.Fatalf("continued past finalizer persistence: finalizers=%v lists=%d creates=%v", co.Finalizers, fc.lists, fc.names)
	}
}

func TestR01RepairReturnsBeforePrerequisites(t *testing.T) {
	r, fc, co := workerReadHarness(t)
	co.Status.Workers[0].BareMetalInstance.ID = ""
	co.Status.Workers[0].Phase = workerPhaseProvisioning
	if err := r.Status().Update(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	fc.listed = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "created-id")}
	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
	if err != nil || res.IsZero() {
		t.Fatalf("repair entered missing pull-secret gate: result=%+v err=%v", res, err)
	}
	if err := r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if co.Status.Workers[0].BareMetalInstance.ID != "created-id" || len(fc.names) != 0 {
		t.Fatal("repair not persisted before returning")
	}
}

func TestWorkerObservationReadBudget(t *testing.T) {
	for _, omitted := range []bool{false, true} {
		t.Run(map[bool]string{false: "listed", true: "omitted"}[omitted], func(t *testing.T) {
			r, fc, co := workerReadHarness(t)
			bmi := ownedBMIFixture(co, "recorded-bmi", "recorded-id")
			bmi.SetStatus(privatev1.BareMetalInstanceStatus_builder{Hardware: privatev1.BareMetalHardware_builder{Nics: []*privatev1.BareMetalNICStatus{privatev1.BareMetalNICStatus_builder{Mac: "aa"}.Build()}}.Build()}.Build())
			fc.bmis = []*privatev1.BareMetalInstance{bmi}
			if !omitted {
				fc.listed = fc.bmis
			}
			a := agentPhaseFixture("", false)
			a.SetNamespace(co.Namespace)
			a.SetLabels(map[string]string{infraEnvAgentLabel: co.Name + infraEnvNameSuffix})
			_ = unstructured.SetNestedSlice(a.Object, []interface{}{map[string]interface{}{"macAddress": "aa"}}, "status", "inventory", "interfaces")
			if err := r.Create(context.Background(), a); err != nil {
				t.Fatal(err)
			}
			_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
			if err == nil {
				t.Fatal("expected pull-secret gate for unchanged observation")
			}
			wantGets := 0
			if omitted {
				wantGets = 1
			}
			if fc.lists != 1 || fc.gets != wantGets {
				t.Fatalf("observation lists=%d gets=%d, want 1/%d", fc.lists, fc.gets, wantGets)
			}
			if len(fc.names) != 0 {
				t.Fatal("created before prerequisite gate")
			}
		})
	}
}
func TestWorkerObservationOutageBeforePrerequisites(t *testing.T) {
	for _, err := range []error{status.Error(codes.Internal, "list outage"), ErrFulfillmentServiceUnavailable} {
		t.Run(err.Error(), func(t *testing.T) {
			r, fc, co := workerReadHarness(t)
			fc.bmis = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "recorded-id")}
			fc.listErr = err
			before := co.DeepCopy()
			res, gotErr := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
			if errors.Is(err, ErrFulfillmentServiceUnavailable) {
				if gotErr != nil || res.RequeueAfter != unavailableBackoff {
					t.Fatalf("result=%+v error=%v", res, gotErr)
				}
			} else if status.Code(gotErr) != codes.Internal {
				t.Fatalf("error=%v, want list outage", gotErr)
			}
			if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before.Status.Workers, co.Status.Workers) {
				t.Fatal("outage changed workers")
			}
			if fc.lists != 1 || fc.gets != 0 {
				t.Fatalf("outage lists=%d gets=%d", fc.lists, fc.gets)
			}
		})
	}
}
func TestBMIRecoveryRejectsAmbiguousNames(t *testing.T) {
	r, fc, co := bmiStageHarness(t, workerPhaseProvisioning, "")
	fc.bmis = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "first"), ownedBMIFixture(co, "recorded-bmi", "second")}
	if _, _, err := runBMIStage(context.Background(), r, co); err == nil {
		t.Fatal("adopted ambiguous name")
	}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if co.Status.Workers[0].BareMetalInstance.ID != "" {
		t.Fatal("persisted ambiguous identity")
	}
}
func TestEarlyAgentObservationStopsOnConcurrentWorkerState(t *testing.T) {
	for _, mutation := range []string{"appended", "failed", "history"} {
		t.Run(mutation, func(t *testing.T) {
			r, _, co := bmiStageHarness(t, workerPhaseWaitingForAgent, "id")
			kube := r.Client
			var concurrent v1alpha1.WorkerStatus
			conflict := &bmiConflictClient{Client: kube, beforePatch: func() {
				latest := &v1alpha1.ClusterOrder{}
				if err := kube.Get(context.Background(), client.ObjectKeyFromObject(co), latest); err != nil {
					t.Fatal(err)
				}
				switch mutation {
				case "appended":
					latest.Status.Workers = append(latest.Status.Workers, newWorkerStatus("standard", "standard", "new-slot", "new-id", workerPhaseBinding))
				case "failed":
					latest.Status.Workers[0].Phase = workerPhaseFailed
				case "history":
					latest.Status.Workers[0].AttemptCount = 7
				}
				concurrent = latest.Status.Workers[len(latest.Status.Workers)-1]
				if err := kube.Status().Update(context.Background(), latest); err != nil {
					t.Fatal(err)
				}
				if err := kube.Get(context.Background(), client.ObjectKeyFromObject(co), latest); err != nil {
					t.Fatal(err)
				}
				concurrent = latest.Status.Workers[len(latest.Status.Workers)-1]
			}}
			r.Client = conflict
			agentFixture := agentPhaseFixture("slot", true)
			agentFixture.SetNamespace(co.Namespace)
			labels := agentFixture.GetLabels()
			labels[clusterOrderLabel] = co.Name
			agentFixture.SetLabels(labels)
			agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*agentFixture}}
			observed := indexWorkerBMIs([]*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "id")})
			observed.agents = agents
			workers, err := r.observeExistingWorkers(context.Background(), co, "tenant", observed)
			if err != nil {
				t.Fatal(err)
			}
			err = r.updateWorkerStatus(context.Background(), co, workers)
			if !apierrors.IsConflict(err) {
				t.Fatalf("error=%v, want one-shot conflict", err)
			}
			if conflict.patches != 1 {
				t.Fatalf("status patches=%d, want 1", conflict.patches)
			}
			if err := kube.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
				t.Fatal(err)
			}
			got := co.Status.Workers[len(co.Status.Workers)-1]
			if !reflect.DeepEqual(got, concurrent) {
				t.Fatalf("overwrote concurrent worker: got=%+v want=%+v", got, concurrent)
			}
		})
	}
}

func TestR02ConflictDoesNotRetry(t *testing.T) {
	ctx := context.Background()
	r, _, co := bmiStageHarness(t, workerPhaseBinding, "id")
	kube := r.Client
	conflict := &bmiConflictClient{Client: kube, beforePatch: func() {
		latest := &v1alpha1.ClusterOrder{}
		if err := kube.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
			t.Fatal(err)
		}
		latest.Status.Workers = append(latest.Status.Workers,
			newWorkerStatus("standard", "standard", "concurrent", "concurrent-id", workerPhaseReady))
		if err := kube.Status().Update(ctx, latest); err != nil {
			t.Fatal(err)
		}
	}}
	r.Client = conflict
	next := append([]v1alpha1.WorkerStatus(nil), co.Status.Workers...)
	next[0].Phase = workerPhaseReady

	err := r.updateWorkerStatus(ctx, co, next)
	if !apierrors.IsConflict(err) {
		t.Fatalf("error=%v, want one-shot conflict", err)
	}
	if conflict.patches != 1 {
		t.Fatalf("status patches=%d, want 1", conflict.patches)
	}
	latest := &v1alpha1.ClusterOrder{}
	if err := kube.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
		t.Fatal(err)
	}
	if len(latest.Status.Workers) != 2 || latest.Status.Workers[1].Name != "concurrent" {
		t.Fatalf("concurrent worker was not preserved: %+v", latest.Status.Workers)
	}
	if latest.Status.Workers[0].Phase != workerPhaseBinding {
		t.Fatalf("stale worker update was applied after conflict: %+v", latest.Status.Workers)
	}
}

func TestFinalWorkerStatusStopsOnConflict(t *testing.T) {
	ctx := context.Background()
	r, _, co := bmiStageHarness(t, workerPhaseBinding, "id")
	kube := r.Client
	conflict := &bmiConflictClient{Client: kube, beforePatch: func() {
		latest := &v1alpha1.ClusterOrder{}
		if err := kube.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
			t.Fatal(err)
		}
		latest.Status.Workers = append(latest.Status.Workers, newWorkerStatus("standard", "standard", "appended", "appended-id", workerPhaseReady))
		if err := kube.Status().Update(ctx, latest); err != nil {
			t.Fatal(err)
		}
	}}
	r.Client = conflict
	next := append([]v1alpha1.WorkerStatus(nil), co.Status.Workers...)
	next[0].Phase = workerPhaseReady
	if err := r.updateWorkerStatusWithAgent(ctx, co, next); !apierrors.IsConflict(err) {
		t.Fatalf("error=%v, want one-shot conflict", err)
	}
	if conflict.patches != 1 {
		t.Fatalf("status patches=%d, want 1", conflict.patches)
	}
	if err := kube.Get(ctx, client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if len(co.Status.Workers) != 2 || co.Status.Workers[1].Name != "appended" {
		t.Fatalf("lost appended worker: %+v", co.Status)
	}
	if co.Status.ReadyWorkers != nil {
		t.Fatalf("aggregates changed after conflict: %+v", co.Status)
	}
}

// R05-U1/U3: phase derivation happens exactly once, in the observation
// projection; the Agent action stage never re-derives phases from the snapshot.
func TestR05SingleProjectionStage(t *testing.T) {
	r, fc, co := workerReadHarness(t)
	fc.listed = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "recorded-id")}
	calls := 0
	r.SetMACResolver(func(context.Context, string) []string { calls++; return nil })
	observed, res, err := r.observeWorkerResources(context.Background(), co)
	if err != nil || !res.IsZero() {
		t.Fatalf("observation: %+v %v", res, err)
	}
	workers, err := r.observeExistingWorkers(context.Background(), co, "tenant", observed)
	if err != nil {
		t.Fatal(err)
	}
	if !workerSlicesEqual(co.Status.Workers, workers) {
		if err := r.updateWorkerStatus(context.Background(), co, workers); err != nil {
			t.Fatalf("status: %v", err)
		}
		if err := r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err = r.reconcileObservedAgents(context.Background(), co, co.Status.Workers, observed)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("phase MAC resolutions=%d, want exactly one projection", calls)
	}
}

func TestWorkerLocalMACResolverIsolationAndOverrides(t *testing.T) {
	ctx := context.Background()
	r := &Reconciler{}
	bmi := func(mac string) *privatev1.BareMetalInstance {
		return privatev1.BareMetalInstance_builder{Id: "same-id", Status: privatev1.BareMetalInstanceStatus_builder{Hardware: privatev1.BareMetalHardware_builder{Nics: []*privatev1.BareMetalNICStatus{privatev1.BareMetalNICStatus_builder{Mac: mac}.Build()}}.Build()}.Build()}.Build()
	}
	first := indexWorkerBMIs([]*privatev1.BareMetalInstance{bmi("first")})
	second := indexWorkerBMIs([]*privatev1.BareMetalInstance{bmi("second")})
	var wg sync.WaitGroup
	for _, tt := range []struct {
		o    *workerObservation
		want string
	}{{first, "first"}, {second, "second"}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resolve := r.workerMACResolver(tt.o)
			for range 100 {
				if got := resolve(ctx, "same-id"); !reflect.DeepEqual(got, []string{tt.want}) {
					t.Errorf("cross-invocation MACs=%v want=%s", got, tt.want)
				}
			}
		}()
	}
	wg.Wait()
	if r.macResolver != nil {
		t.Fatal("observation mutated shared resolver")
	}
	r.SetMACResolver(func(context.Context, string) []string { return []string{"override"} })
	if got := r.workerMACResolver(first)(ctx, "same-id"); !reflect.DeepEqual(got, []string{"override"}) {
		t.Fatalf("override ignored: %v", got)
	}
}
func TestWorkerFallbackGetMemoizesUnknownEvidence(t *testing.T) {
	r, fc, co := bmiStageHarness(t, workerPhaseWaitingForAgent, "missing")
	fc.getErr = status.Error(codes.Internal, "unknown ownership")
	o := indexWorkerBMIs(nil)
	o.agents = &unstructured.UnstructuredList{}
	if _, err := r.observeExistingWorkers(context.Background(), co, "tenant", o); err == nil {
		t.Fatal("unknown ownership permitted convergence")
	}
	r.macResolver = nil
	resolve := r.workerMACResolver(o)
	for range 3 {
		if got := resolve(context.Background(), "missing"); len(got) != 0 {
			t.Fatal("unknown evidence produced MACs")
		}
	}
	if fc.gets != 1 {
		t.Fatalf("fallback Gets=%d, want 1", fc.gets)
	}
}

func (f *workerReadClient) GetClusterVersion(context.Context, string) (*privatev1.ClusterVersion, error) {
	return privatev1.ClusterVersion_builder{Id: "cv"}.Build(), nil
}

func TestR04CleanupWithoutImageOrIgnition(t *testing.T) {
	ctx := context.Background()
	r, base, co := workerReadHarness(t)
	blocked := &r04PrereqClient{workerReadClient: base}
	r.fulfillment = blocked
	ignition := &countingIgnition{}
	r.ignition = ignition
	base.listed = []*privatev1.BareMetalInstance{
		ownedBMIFixture(co, "recorded-bmi", "recorded-id"),
		ownedBMIFixture(co, "excess", "excess-id"),
	}
	base.bmis = base.listed
	// One extra slot retires while every creation prerequisite is unavailable:
	// the pull secret is absent, the InfraEnv is gone and the image chain fails.
	co.Spec.NodeRequests[0].NumberOfNodes = 1
	if err := r.Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	co.Status.Workers = append(co.Status.Workers, newWorkerStatus("standard", "standard", "excess", "excess-id", workerPhaseWaitingForAgent))
	if err := r.Status().Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
	if err != nil || res.IsZero() {
		t.Fatalf("retirement boundary: result=%+v err=%v", res, err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if got := workerByName(co.Status.Workers, "excess"); got == nil || got.Phase != workerPhaseUnbinding {
		t.Fatalf("retirement intent was not persisted without prerequisites: %+v", co.Status.Workers)
	}
	if len(blocked.names) != 0 || len(blocked.deletes) != 0 || ignition.calls != 0 {
		t.Fatalf("prerequisite-free retirement acted externally: creates=%v deletes=%v ignition=%d", blocked.names, blocked.deletes, ignition.calls)
	}

	// Cleanup proceeds on the next invocation even though the missing pull secret
	// is still reported, and it never fetches discovery ignition.
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); err == nil {
		t.Fatal("missing pull secret was not reported")
	}
	if len(blocked.deletes) != 1 || blocked.deletes[0] != "excess-id" {
		t.Fatalf("cleanup did not request BMI deletion: %v", blocked.deletes)
	}
	if ignition.calls != 0 || len(blocked.names) != 0 {
		t.Fatalf("cleanup fetched ignition or created a BMI: ignition=%d creates=%v", ignition.calls, blocked.names)
	}
}

func TestR04PendingRetryDoesNotBlockBinding(t *testing.T) {
	ctx := context.Background()
	r, base, co := workerReadHarness(t)
	fc := &r04PrereqClient{workerReadClient: base}
	r.fulfillment = fc
	future := metav1.NewTime(time.Now().Add(time.Hour))
	failed := newWorkerStatus("standard", "standard", "failed-bmi", "failed-id", workerPhaseFailed)
	failed.NextRetryTime = &future
	binding := newWorkerStatus("standard", "standard", "binding-bmi", "binding-id", workerPhaseBinding)
	co.Spec.NodeRequests[0].NumberOfNodes = 2
	co.Spec.PullSecret = `{"auths":{}}`
	if err := r.Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	co.Status.Workers = []v1alpha1.WorkerStatus{failed, binding}
	if err := r.Status().Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	fc.bmis = []*privatev1.BareMetalInstance{
		ownedBMIFixture(co, "failed-bmi", "failed-id"),
		ownedBMIFixture(co, "binding-bmi", "binding-id"),
	}
	fc.listed = fc.bmis
	agent := agentPhaseFixture("binding-bmi", true)
	agent.SetNamespace(co.Namespace)
	agent.SetLabels(map[string]string{workerNameLabel: "binding-bmi", clusterOrderLabel: co.Name})
	if err := unstructured.SetNestedSlice(agent.Object, []interface{}{map[string]interface{}{"macAddress": "aa:bb:cc:dd:ee:01"}}, "status", "inventory", "interfaces"); err != nil {
		t.Fatal(err)
	}
	if err := r.Create(ctx, agent); err != nil {
		t.Fatal(err)
	}
	// The first invocation projects the labeled Agent to Ready while the other
	// worker still waits for its retry; the second performs its pending cleanup.
	// A finite trace: binding converges on the first invocation, while the other
	// worker's cleanup proceeds on the next one that is not preempted by the
	// InfraEnv creation boundary.
	for i := 0; i < 4 && len(fc.deletes) == 0; i++ {
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if got := workerByName(co.Status.Workers, "binding-bmi"); got == nil || got.Phase != workerPhaseReady {
		t.Fatalf("binding did not converge past the pending retry: %+v", co.Status.Workers)
	}
	if got := workerByName(co.Status.Workers, "failed-bmi"); got == nil || got.Phase != workerPhaseFailed || got.BareMetalInstance.ID != "failed-id" {
		t.Fatalf("pending retry lost its recorded incarnation: %+v", co.Status.Workers)
	}
	if !reflect.DeepEqual(fc.deletes, []string{"failed-id"}) {
		t.Fatalf("pending cleanup deletes=%v, want the failed incarnation only", fc.deletes)
	}
	if deadline := r.workerRecheckDeadline(co.Status.Workers, time.Now()); deadline.RequeueAfter <= 0 {
		t.Fatalf("pending cleanup did not contribute a bounded recheck: %+v", deadline)
	}
}

func TestR04FairnessTraceAcrossWorkerStates(t *testing.T) {
	ctx := context.Background()
	r, base, co := workerReadHarness(t)
	fc := &r04PrereqClient{workerReadClient: base}
	r.fulfillment = fc
	// A ready worker with recent retry history must not be reset merely because
	// another worker is delayed.
	healthy := newWorkerStatus("standard", "standard", "healthy-bmi", "healthy-id", workerPhaseReady)
	healthy.AttemptCount = 2
	healthy.LastFailureReason = "previous"
	// metav1.Time persists at second precision, so compare at that precision.
	recent := metav1.NewTime(time.Now().Add(-time.Minute).Truncate(time.Second))
	healthy.ReadySince = &recent
	future := metav1.NewTime(time.Now().Add(time.Hour))
	pending := newWorkerStatus("standard", "standard", "pending-bmi", "pending-id", workerPhaseFailed)
	pending.NextRetryTime = &future
	waiting := newWorkerStatus("standard", "standard", "waiting-bmi", "waiting-id", workerPhaseWaitingForAgent)
	co.Spec.NodeRequests[0].NumberOfNodes = 3
	co.Spec.PullSecret = `{"auths":{}}`
	if err := r.Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	co.Status.Workers = []v1alpha1.WorkerStatus{healthy, pending, waiting}
	if err := r.Status().Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	fc.bmis = []*privatev1.BareMetalInstance{
		ownedBMIFixture(co, "healthy-bmi", "healthy-id"),
		ownedBMIFixture(co, "pending-bmi", "pending-id"),
		ownedBMIFixture(co, "waiting-bmi", "waiting-id"),
	}
	fc.listed = fc.bmis
	for i, name := range []string{"healthy-bmi", "waiting-bmi"} {
		agent := agentPhaseFixture(name, true)
		agent.SetName(name + "-agent")
		agent.SetNamespace(co.Namespace)
		agent.SetLabels(map[string]string{workerNameLabel: name, clusterOrderLabel: co.Name})
		mac := "aa:bb:cc:dd:ee:0" + string(rune('2'+i))
		if err := unstructured.SetNestedSlice(agent.Object, []interface{}{map[string]interface{}{"macAddress": mac}}, "status", "inventory", "interfaces"); err != nil {
			t.Fatal(err)
		}
		if err := r.Create(ctx, agent); err != nil {
			t.Fatal(err)
		}
	}
	for range 4 {
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	bound := workerByName(co.Status.Workers, "waiting-bmi")
	if bound == nil || bound.Phase != workerPhaseReady {
		t.Fatalf("actionable worker did not converge: %+v", co.Status.Workers)
	}
	kept := workerByName(co.Status.Workers, "healthy-bmi")
	if kept == nil || kept.AttemptCount != 2 || kept.LastFailureReason != "previous" || kept.ReadySince == nil || !kept.ReadySince.Equal(&recent) {
		t.Fatalf("healthy worker history was reset by an unrelated delay: %+v", kept)
	}
	delayed := workerByName(co.Status.Workers, "pending-bmi")
	if delayed == nil || delayed.Phase != workerPhaseFailed || delayed.BareMetalInstance.ID != "pending-id" {
		t.Fatalf("delayed worker lost its recorded incarnation: %+v", delayed)
	}
	if len(fc.deletes) == 0 {
		t.Fatal("pending cleanup was starved by unrelated progress")
	}
	if deadline := r.workerRecheckDeadline(co.Status.Workers, time.Now()); deadline.RequeueAfter <= 0 {
		t.Fatalf("no bounded recheck for the delayed worker: %+v", deadline)
	}
}

func TestR04SummaryBeforeCreateGate(t *testing.T) {
	ctx := context.Background()
	r, base, co := workerReadHarness(t)
	imageErr := status.Error(codes.NotFound, "disk image unavailable")
	blocked := &r04PrereqClient{workerReadClient: base, imageErr: imageErr}
	r.fulfillment = blocked
	r.ignition = &countingIgnition{}
	blocked.listed = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "recorded-id")}
	blocked.bmis = blocked.listed
	// The worker was already demoted, but its aggregate summary is stale. A
	// scale-up makes the create gate due even though no slot needs a create yet.
	stale := int32(1)
	co.Spec.PullSecret = `{"auths":{}}`
	co.Spec.NodeRequests[0].NumberOfNodes = 2
	if err := r.Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	co.Status.ReadyWorkers = &stale
	if err := r.Status().Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	// The fixture InfraEnv is the object production creates: owned by this order.
	r07CreateInfraEnv(t, r, r07InfraEnv(t, r, co, "infra-uid", "http://ignition.test"))
	co.SetStatusCondition(v1alpha1.ConditionInfraEnvReady, metav1.ConditionTrue, "ready", reasonInfraEnvReady)
	if err := r.Status().Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
	if !errors.Is(err, imageErr) {
		t.Fatalf("error=%v, want the blocked image lookup", err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if co.Status.ReadyWorkers == nil || *co.Status.ReadyWorkers != 0 {
		t.Fatalf("stale ready summary was not demoted before the create gate: %+v", co.Status)
	}
	if co.Status.DesiredWorkers == nil || *co.Status.DesiredWorkers != 2 || co.Status.CurrentWorkers == nil || *co.Status.CurrentWorkers != 1 {
		t.Fatalf("aggregate counts were not persisted: %+v", co.Status)
	}
	if len(blocked.names) != 0 {
		t.Fatalf("created a BMI under a blocked image lookup: %v", blocked.names)
	}
}

// r04PrereqClient records destructive actions and blocks the creation input
// chain, so prerequisite-free work can be asserted without resolving an image.
type r04PrereqClient struct {
	*workerReadClient
	deletes  []string
	imageErr error
}

func (f *r04PrereqClient) DeleteBareMetalInstance(_ context.Context, id string) error {
	f.deletes = append(f.deletes, id)
	return nil
}

// GetClusterVersion resolves a reference to an unusable disk image so image
// resolution fails after the tenant/version lookups succeed.
func (f *r04PrereqClient) GetClusterVersion(_ context.Context, id string) (*privatev1.ClusterVersion, error) {
	return privatev1.ClusterVersion_builder{
		Id:   id,
		Spec: privatev1.ClusterVersionSpec_builder{DiskImage: privatev1.DiskImageReference_builder{Id: "blocked"}.Build()}.Build(),
	}.Build(), nil
}

func (f *r04PrereqClient) GetDiskImage(context.Context, string) (*privatev1.DiskImage, error) {
	if f.imageErr != nil {
		return nil, f.imageErr
	}
	return nil, status.Error(codes.NotFound, "disk image unavailable")
}

type countingIgnition struct{ calls int }

func (c *countingIgnition) FetchIgnition(context.Context, string) ([]byte, error) {
	c.calls++
	return []byte(`{}`), nil
}

type workerIgnition struct{}

func (workerIgnition) FetchIgnition(context.Context, string) ([]byte, error) {
	return []byte(`{}`), nil
}

type staleFailureClient struct {
	client.Client
	err error
}

func (c *staleFailureClient) Status() client.SubResourceWriter {
	return &staleFailureWriter{SubResourceWriter: c.Client.Status(), err: c.err}
}

type staleFailureWriter struct {
	client.SubResourceWriter
	err error
}

func (w *staleFailureWriter) Patch(ctx context.Context, obj client.Object, p client.Patch, opts ...client.SubResourcePatchOption) error {
	co := obj.(*v1alpha1.ClusterOrder)
	if len(co.Status.Workers) > 0 && co.Status.Workers[0].Phase == workerPhaseFailed {
		return w.err
	}
	return w.SubResourceWriter.Patch(ctx, obj, p, opts...)
}

// R07 observes the cluster InfraEnv as one resource-driven path: InfraEnvReady is
// evidence output, never the switch that decides whether the resource is looked
// up, and the recorded UID stays the recovery checkpoint for stale workers.

// r07Harness builds an order whose single worker already holds a recorded BMI, so
// no create is due and the InfraEnv evidence is the only thing in play.
func r07Harness(t *testing.T, phase string) (*Reconciler, *workerReadClient, *v1alpha1.ClusterOrder) {
	t.Helper()
	ctx := context.Background()
	r, fc, co := workerReadHarness(t)
	co.Spec.PullSecret = `{"auths":{}}`
	if err := r.Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	co.Status.Workers[0].Phase = phase
	if err := r.Status().Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	fc.listed = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "recorded-id")}
	fc.bmis = fc.listed
	return r, fc, co
}

// r07InfraEnv builds the object production creates: deterministic name, controller
// owner reference to the ClusterOrder, and optional boot-artifact evidence.
func r07InfraEnv(t *testing.T, r *Reconciler, owner *v1alpha1.ClusterOrder, uid, ignitionURL string) *unstructured.Unstructured {
	t.Helper()
	return r07InfraEnvNamed(t, r, owner, owner.Name+infraEnvNameSuffix, uid, ignitionURL)
}

// r07InfraEnvNamed builds a controlled InfraEnv under an explicit name, so a
// same-name object controlled by a different ClusterOrder can be forged.
func r07InfraEnvNamed(t *testing.T, r *Reconciler, owner *v1alpha1.ClusterOrder, name, uid, ignitionURL string) *unstructured.Unstructured {
	t.Helper()
	infra := &unstructured.Unstructured{}
	infra.SetGroupVersionKind(infraEnvGVK)
	infra.SetName(name)
	infra.SetNamespace(owner.Namespace)
	if uid != "" {
		infra.SetUID(types.UID(uid))
	}
	if err := controllerutil.SetControllerReference(owner, infra, r.scheme); err != nil {
		t.Fatalf("setting infraenv owner reference: %v", err)
	}
	if ignitionURL != "" {
		if err := unstructured.SetNestedField(infra.Object, ignitionURL, "status", "bootArtifacts", "discoveryIgnitionURL"); err != nil {
			t.Fatal(err)
		}
	}
	return infra
}

func r07CreateInfraEnv(t *testing.T, r *Reconciler, infra *unstructured.Unstructured) {
	t.Helper()
	if err := r.Create(context.Background(), infra); err != nil {
		t.Fatal(err)
	}
}

// r07InfraEnvObjects returns the deterministic-name InfraEnv objects in the order's
// namespace, so duplication and replacement are observable.
func r07InfraEnvObjects(t *testing.T, r *Reconciler, co *v1alpha1.ClusterOrder) []unstructured.Unstructured {
	t.Helper()
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(infraEnvGVK)
	if err := r.List(context.Background(), list, client.InNamespace(co.Namespace)); err != nil {
		t.Fatal(err)
	}
	var found []unstructured.Unstructured
	for i := range list.Items {
		if list.Items[i].GetName() == co.Name+infraEnvNameSuffix {
			found = append(found, list.Items[i])
		}
	}
	return found
}

// TestR07ConditionDoesNotChooseLookup is R07-U1: absence and presence are decided
// by the resource, not by the previous InfraEnvReady condition. Every prior
// condition state creates exactly one object on authoritative absence, and a
// present object whose discovery ignition URL is missing replaces a stale Ready
// claim with current pending evidence.
func TestR07ConditionDoesNotChooseLookup(t *testing.T) {
	ctx := context.Background()
	for _, present := range []bool{true, false} {
		for _, prior := range []metav1.ConditionStatus{"", metav1.ConditionTrue, metav1.ConditionFalse} {
			label := "absent"
			if present {
				label = "present"
			}
			t.Run(fmt.Sprintf("%s-prior-%q", label, string(prior)), func(t *testing.T) {
				r, fc, co := r07Harness(t, workerPhaseReady)
				if prior != "" {
					co.SetStatusCondition(v1alpha1.ConditionInfraEnvReady, prior, "PriorEvidence", "prior evidence")
					if err := r.Status().Update(ctx, co); err != nil {
						t.Fatal(err)
					}
				}
				var created []unstructured.Unstructured
				if present {
					r07CreateInfraEnv(t, r, r07InfraEnv(t, r, co, "infra-uid", ""))
					created = r07InfraEnvObjects(t, r, co)
					if len(created) != 1 {
						t.Fatalf("fixture InfraEnv objects=%d", len(created))
					}
				}
				// A finite trace: an evidence-condition boundary may consume the first
				// invocation and the UID evidence is recorded by a later one.
				for range 3 {
					if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); err != nil {
						t.Fatalf("reconcile: %v", err)
					}
				}
				found := r07InfraEnvObjects(t, r, co)
				if len(found) != 1 {
					t.Fatalf("InfraEnv objects=%d, want exactly one for present=%t prior=%q", len(found), present, prior)
				}
				if err := r.Get(ctx, client.ObjectKeyFromObject(co), co); err != nil {
					t.Fatal(err)
				}
				cond := apimeta.FindStatusCondition(co.Status.Conditions, v1alpha1.ConditionInfraEnvReady)
				if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != reasonIgnitionPending {
					t.Fatalf("InfraEnv without boot artifacts reported %+v, want current pending evidence", cond)
				}
				if present {
					if found[0].GetUID() != created[0].GetUID() {
						t.Fatalf("present InfraEnv was replaced: uid=%q want %q", found[0].GetUID(), created[0].GetUID())
					}
					if co.Annotations[infraEnvUIDAnnotation] != string(found[0].GetUID()) {
						t.Fatalf("present InfraEnv was not observed and recorded: annotations=%v uid=%q", co.Annotations, found[0].GetUID())
					}
				} else if co.Annotations[infraEnvUIDAnnotation] != "" {
					t.Fatalf("absent InfraEnv recorded a UID: annotations=%v", co.Annotations)
				}
				if len(fc.names) != 0 {
					t.Fatalf("InfraEnv lookup created a BMI: %v", fc.names)
				}
			})
		}
	}
}

// TestR07ExistingForeignInfraEnvRejected is R07-U2: a same-name InfraEnv this
// ClusterOrder does not control is reported as an error and is never adopted,
// replaced, or consumed as ignition evidence.
func TestR07ExistingForeignInfraEnvRejected(t *testing.T) {
	ctx := context.Background()
	for name, build := range map[string]func(t *testing.T, r *Reconciler, co *v1alpha1.ClusterOrder) *unstructured.Unstructured{
		"ownerless": func(_ *testing.T, _ *Reconciler, co *v1alpha1.ClusterOrder) *unstructured.Unstructured {
			infra := &unstructured.Unstructured{}
			infra.SetGroupVersionKind(infraEnvGVK)
			infra.SetName(co.Name + infraEnvNameSuffix)
			infra.SetNamespace(co.Namespace)
			infra.SetUID("foreign-uid")
			return infra
		},
		"other ClusterOrder": func(t *testing.T, r *Reconciler, co *v1alpha1.ClusterOrder) *unstructured.Unstructured {
			foreign := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "other-order", Namespace: co.Namespace, UID: "other-uid"}}
			return r07InfraEnvNamed(t, r, foreign, co.Name+infraEnvNameSuffix, "foreign-uid", "http://ignition.test")
		},
	} {
		t.Run(name, func(t *testing.T) {
			r, fc, co := r07Harness(t, workerPhaseWaitingForAgent)
			ignition := &countingIgnition{}
			r.ignition = ignition
			foreign := build(t, r, co)
			r07CreateInfraEnv(t, r, foreign)
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
			if err == nil || !strings.Contains(err.Error(), "not controlled by ClusterOrder") {
				t.Fatalf("err=%v, want a foreign-owner error", err)
			}
			found := r07InfraEnvObjects(t, r, co)
			if len(found) != 1 || found[0].GetUID() != foreign.GetUID() {
				t.Fatalf("foreign InfraEnv was replaced: %d object(s) %v", len(found), found)
			}
			if err := r.Get(ctx, client.ObjectKeyFromObject(co), co); err != nil {
				t.Fatal(err)
			}
			if co.Annotations[infraEnvUIDAnnotation] != "" {
				t.Fatalf("foreign InfraEnv UID was recorded: %v", co.Annotations)
			}
			if len(fc.names) != 0 || ignition.calls != 0 {
				t.Fatalf("foreign InfraEnv authorized work: creates=%v ignition=%d", fc.names, ignition.calls)
			}
		})
	}
}

// TestR07OwnerValidationRejectsForeignOwners is R07-U3: the ownership check
// validates namespace, controller kind, name and (when recorded) the ClusterOrder
// incarnation UID, so only this order's own object is consumed as evidence.
func TestR07OwnerValidationRejectsForeignOwners(t *testing.T) {
	order := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "ns", UID: "order-uid"}}
	controller := true
	infraEnv := func(kind, name string, uid types.UID, namespace string) *unstructured.Unstructured {
		infra := &unstructured.Unstructured{}
		infra.SetGroupVersionKind(infraEnvGVK)
		infra.SetName(order.Name + infraEnvNameSuffix)
		infra.SetNamespace(namespace)
		if kind != "" {
			infra.SetOwnerReferences([]metav1.OwnerReference{{
				APIVersion: "osac.openshift.io/v1alpha1", Kind: kind, Name: name, UID: uid, Controller: &controller,
			}})
		}
		return infra
	}
	for name, tc := range map[string]struct {
		infra   *unstructured.Unstructured
		wantErr bool
	}{
		"own object":              {infra: infraEnv("ClusterOrder", "order", "order-uid", "ns")},
		"earlier incarnation":     {infra: infraEnv("ClusterOrder", "order", "previous-uid", "ns"), wantErr: true},
		"another order":           {infra: infraEnv("ClusterOrder", "other", "other-uid", "ns"), wantErr: true},
		"another controller kind": {infra: infraEnv("ClusterDeployment", "order", "order-uid", "ns"), wantErr: true},
		"ownerless":               {infra: infraEnv("", "", "", "ns"), wantErr: true},
		"another namespace":       {infra: infraEnv("ClusterOrder", "order", "order-uid", "other"), wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateInfraEnvOwner(order, tc.infra)
			if tc.wantErr != (err != nil) {
				t.Fatalf("err=%v, wantErr=%t", err, tc.wantErr)
			}
		})
	}
}

// r07StaleOrder prepares an order whose recorded InfraEnv UID is stale: the
// recorded value is "old", the observed replacement is "new", and the waiting
// worker must be failed before the replacement UID is acknowledged.
func r07StaleOrder(t *testing.T) (*Reconciler, *workerReadClient, *v1alpha1.ClusterOrder) {
	t.Helper()
	ctx := context.Background()
	r, fc, co := r07Harness(t, workerPhaseWaitingForAgent)
	co.Annotations[infraEnvUIDAnnotation] = "old"
	if err := r.Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	co.SetStatusCondition(v1alpha1.ConditionInfraEnvReady, metav1.ConditionTrue, "ready", reasonInfraEnvReady)
	if err := r.Status().Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	r07CreateInfraEnv(t, r, r07InfraEnv(t, r, co, "new", "http://ignition.test"))
	return r, fc, co
}

// r07AnnotationPatchFault fails ClusterOrder metadata patches carrying a recorded
// InfraEnv UID, leaving status writes untouched, so a lost UID patch can be
// injected without breaking failure persistence.
type r07AnnotationPatchFault struct {
	client.Client
	err error
}

func (c *r07AnnotationPatchFault) Patch(ctx context.Context, obj client.Object, p client.Patch, opts ...client.PatchOption) error {
	if co, ok := obj.(*v1alpha1.ClusterOrder); ok && co.Annotations[infraEnvUIDAnnotation] != "" {
		return c.err
	}
	return c.Client.Patch(ctx, obj, p, opts...)
}

// r07IgnitionScripted returns fixed ignition bytes and an optional fetch failure for
// the create stage's input path.
type r07IgnitionScripted struct {
	calls int
	body  []byte
	err   error
}

func (i *r07IgnitionScripted) FetchIgnition(context.Context, string) ([]byte, error) {
	i.calls++
	return i.body, i.err
}

// r07CountingStatusClient counts optimistic status patches so a stable order can
// prove it writes no status at all.
type r07CountingStatusClient struct {
	client.Client
	patches int
}

func (c *r07CountingStatusClient) Status() client.SubResourceWriter {
	return &r07CountingStatusWriter{SubResourceWriter: c.Client.Status(), c: c}
}

type r07CountingStatusWriter struct {
	client.SubResourceWriter
	c *r07CountingStatusClient
}

func (w *r07CountingStatusWriter) Patch(ctx context.Context, obj client.Object, p client.Patch, opts ...client.SubResourcePatchOption) error {
	w.c.patches++
	return w.SubResourceWriter.Patch(ctx, obj, p, opts...)
}

// r07CreateHarness builds an order with no workers and a pull secret, so a BMI
// create is due as soon as the InfraEnv's discovery ignition resolves. Callers
// place the InfraEnv evidence they want observed and then drive explicit
// invocations.
func r07CreateHarness(t *testing.T) (*Reconciler, *workerReadClient, *v1alpha1.ClusterOrder) {
	t.Helper()
	ctx := context.Background()
	r, fc, co := workerReadHarness(t)
	co.Spec.PullSecret = `{"auths":{}}`
	if err := r.Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	co.Status.Workers = nil
	if err := r.Status().Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	fc.listed = nil
	fc.bmis = nil
	// The unit harness has no ignition fetcher; default to a valid empty artifact so
	// an invocation never depends on a case's scripted fetcher.
	r.ignition = &r07IgnitionScripted{body: []byte(`{}`)}
	return r, fc, co
}

// TestR07InvalidIgnitionCannotCreateBMI is R07-U4: only fetched, JSON-valid
// discovery ignition authorizes a BMI create. A missing URL waits, and a fetch or
// validation failure is reported without creating anything.
func TestR07InvalidIgnitionCannotCreateBMI(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		ignitionURL string
		body        []byte
		fetchErr    error
		wantErr     string
	}{
		"missing URL":   {wantErr: ""},
		"invalid JSON":  {ignitionURL: "http://ignition.test", body: []byte("not-json"), wantErr: "not valid JSON"},
		"fetch failure": {ignitionURL: "http://ignition.test", fetchErr: errors.New("tls: bad certificate"), wantErr: "fetching discovery ignition"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r, fc, co := r07CreateHarness(t)
			r07CreateInfraEnv(t, r, r07InfraEnv(t, r, co, "infra-uid", tc.ignitionURL))
			ignition := &r07IgnitionScripted{body: tc.body, err: tc.fetchErr}
			r.ignition = ignition
			// A create is due (one requested node, no reserved slot): only validated
			// ignition bytes may produce one, and the missing URL holds instead.
			for i := range 2 {
				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
				switch {
				case tc.wantErr == "":
					if err != nil {
						t.Fatalf("invocation %d: err=%v, want a bounded wait for the missing URL", i, err)
					}
				case err == nil || !strings.Contains(err.Error(), tc.wantErr):
					t.Fatalf("invocation %d: err=%v, want %q", i, err, tc.wantErr)
				}
			}
			if len(fc.names) != 0 {
				t.Fatalf("unenforceable ignition authorized a BMI create: %v", fc.names)
			}
			if tc.ignitionURL == "" && ignition.calls != 0 {
				t.Fatalf("missing URL was fetched anyway: %d call(s)", ignition.calls)
			}
		})
	}
}

// TestR07ForeignInfraEnvCannotCreateBMI is R07-U5: a same-name object this
// ClusterOrder does not control never becomes creation input, even when a BMI
// create is due.
func TestR07ForeignInfraEnvCannotCreateBMI(t *testing.T) {
	ctx := context.Background()
	r, fc, co := r07CreateHarness(t)
	foreign := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "other-order", Namespace: co.Namespace, UID: "other-uid"}}
	r07CreateInfraEnv(t, r, r07InfraEnvNamed(t, r, foreign, co.Name+infraEnvNameSuffix, "foreign-uid", "http://ignition.test"))
	ignition := &r07IgnitionScripted{body: []byte(`{}`)}
	r.ignition = ignition
	for range 2 {
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
		if err == nil || !strings.Contains(err.Error(), "not controlled by ClusterOrder") {
			t.Fatalf("err=%v, want a foreign-owner error", err)
		}
	}
	if len(fc.names) != 0 || ignition.calls != 0 {
		t.Fatalf("foreign InfraEnv authorized work: creates=%v ignition=%d", fc.names, ignition.calls)
	}
}

// TestR07StableReadyOrderDoesNoStatusWork is R07-U6: a converged order whose
// InfraEnv publishes an artifact performs neither a discovery-ignition request nor
// a status patch, so condition reporting cannot create a hot loop.
func TestR07StableReadyOrderDoesNoStatusWork(t *testing.T) {
	ctx := context.Background()
	r, _, co := r07Harness(t, workerPhaseReady)
	r07CreateInfraEnv(t, r, r07InfraEnv(t, r, co, "infra-uid", "http://ignition.test"))
	co.Annotations[infraEnvUIDAnnotation] = "infra-uid"
	if err := r.Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	co.SetStatusCondition(v1alpha1.ConditionInfraEnvReady, metav1.ConditionTrue, "ready", reasonInfraEnvReady)
	if err := r.Status().Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	ignition := &countingIgnition{}
	r.ignition = ignition
	// Two invocations settle the worker projection (Agent observation and its
	// ReadySince initialization); the order is stable from then on.
	for range 2 {
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); err != nil {
			t.Fatalf("settling invocation: %v", err)
		}
	}
	counter := &r07CountingStatusClient{Client: r.Client}
	r.Client = counter
	for i := range 2 {
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); err != nil {
			t.Fatalf("invocation %d: %v", i, err)
		}
	}
	if counter.patches != 0 {
		t.Fatalf("stable order wrote %d status patch(es)", counter.patches)
	}
	if ignition.calls != 0 {
		t.Fatalf("stable order fetched discovery ignition %d time(s)", ignition.calls)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if co.Annotations[infraEnvUIDAnnotation] != "infra-uid" ||
		!apimeta.IsStatusConditionTrue(co.Status.Conditions, v1alpha1.ConditionInfraEnvReady) {
		t.Fatalf("stable evidence drifted: annotations=%v conditions=%v", co.Annotations, co.Status.Conditions)
	}
}

func TestStaleIgnitionPersistenceFailureDoesNotAdvanceUID(t *testing.T) {
	ctx := context.Background()
	r, fc, co := workerReadHarness(t)
	co.Spec.PullSecret = `{"auths":{}}`
	co.Annotations[infraEnvUIDAnnotation] = "old"
	if err := r.Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	co.SetStatusCondition(v1alpha1.ConditionInfraEnvReady, metav1.ConditionTrue, "ready", reasonInfraEnvReady)
	if err := r.Status().Update(ctx, co); err != nil {
		t.Fatal(err)
	}
	// The replacement models the object the reconciler itself recreates: same
	// deterministic name, owned by this order, new UID.
	r07CreateInfraEnv(t, r, r07InfraEnv(t, r, co, "new", "http://ignition.test"))
	fc.listed = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "recorded-id")}
	fc.bmis = fc.listed
	r.ignition = workerIgnition{}
	injected := errors.New("stale failure persistence interrupted")
	base := r.Client
	r.Client = &staleFailureClient{Client: base, err: injected}
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
	if !errors.Is(err, injected) {
		t.Fatalf("error=%v, want persistence error", err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if co.Annotations[infraEnvUIDAnnotation] != "old" || co.Status.Workers[0].Phase != workerPhaseWaitingForAgent || len(fc.names) != 0 {
		t.Fatalf("advanced past failed stale-ignition persistence: %+v", co)
	}

	// The next explicit invocation retries the same classification and persists it.
	// The recorded UID still does not advance in that invocation.
	r.Client = base
	r.fulfillment = &r04PrereqClient{workerReadClient: fc}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	failed := co.Status.Workers[0]
	if failed.Phase != workerPhaseFailed || failed.LastFailureReason != eventReasonAgentRegistrationTimeout {
		t.Fatalf("retry did not persist the stale classification: %+v", failed)
	}
	if co.Annotations[infraEnvUIDAnnotation] != "old" {
		t.Fatalf("UID advanced in the same invocation as the repair: %v", co.Annotations)
	}

	// A later invocation records the replacement UID without re-accounting the
	// already persisted failure.
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); err != nil {
		t.Fatalf("uid evidence: %v", err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	after := co.Status.Workers[0]
	if co.Annotations[infraEnvUIDAnnotation] != "new" {
		t.Fatalf("replacement UID was not recorded: %v", co.Annotations)
	}
	if after.LastFailureReason != failed.LastFailureReason || after.LastFailureMessage != failed.LastFailureMessage ||
		!after.LastFailureTime.Equal(failed.LastFailureTime) {
		t.Fatalf("UID recording re-emitted failure accounting: before=%+v after=%+v", failed, after)
	}
}

// TestR07LostUIDEvidenceDoesNotDuplicateFailureAccounting is R07-U7: when the
// stale-worker failure is already durable and only the replacement-UID patch is
// lost, the next invocation records the UID without re-emitting failure
// accounting or changing the worker's protected state.
func TestR07LostUIDEvidenceDoesNotDuplicateFailureAccounting(t *testing.T) {
	ctx := context.Background()
	r, fc, co := r07StaleOrder(t)
	// Deletion support keeps the failed incarnation's cleanup observable instead of
	// panicking in the shared fake.
	r.fulfillment = &r04PrereqClient{workerReadClient: fc}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); err != nil {
		t.Fatalf("persisting the stale failure: %v", err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	failed := co.Status.Workers[0]
	if failed.Phase != workerPhaseFailed || co.Annotations[infraEnvUIDAnnotation] != "old" {
		t.Fatalf("fixture did not persist the repair before the UID: %+v annotations=%v", failed, co.Annotations)
	}

	lost := errors.New("replacement UID patch lost")
	base := r.Client
	r.Client = &r07AnnotationPatchFault{Client: base, err: lost}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); !errors.Is(err, lost) {
		t.Fatalf("error=%v, want the lost UID patch", err)
	}
	r.Client = base
	if err := r.Get(ctx, client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if co.Annotations[infraEnvUIDAnnotation] != "old" || co.Status.Workers[0].LastFailureReason != failed.LastFailureReason ||
		!co.Status.Workers[0].LastFailureTime.Equal(failed.LastFailureTime) {
		t.Fatalf("lost UID patch changed durable state: annotations=%v workers=%+v", co.Annotations, co.Status.Workers)
	}

	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); err != nil {
		t.Fatalf("recording the UID: %v", err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if co.Annotations[infraEnvUIDAnnotation] != "new" {
		t.Fatalf("replacement UID was not recorded: %v", co.Annotations)
	}
	if co.Status.Workers[0].LastFailureReason != failed.LastFailureReason ||
		co.Status.Workers[0].LastFailureMessage != failed.LastFailureMessage ||
		!co.Status.Workers[0].LastFailureTime.Equal(failed.LastFailureTime) {
		t.Fatalf("UID recording re-emitted failure accounting: before=%+v after=%+v", failed, co.Status.Workers[0])
	}
}

// --- R10: intent-based worker counts ---

// workerCountsHarness builds an order with a bare-metal intent and (optionally)
// a pre-existing worker journal, then reloads the persisted object so the
// summary write starts from an authoritative snapshot.
func workerCountsHarness(
	t *testing.T, name string, requests []v1alpha1.NodeRequest, workers []v1alpha1.WorkerStatus,
) (*Reconciler, *v1alpha1.ClusterOrder) {
	t.Helper()
	r, _, co := nodeSetHarness(t, name, requests...)
	if len(workers) > 0 {
		mutateCapacityOrder(t, r, co, func(o *v1alpha1.ClusterOrder) {
			o.Status.Workers = workers
		})
	}
	if err := r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	return r, co
}

// persistWorkerSummary runs the production summary write and reloads it.
func persistWorkerSummary(
	t *testing.T, r *Reconciler, co *v1alpha1.ClusterOrder, workers []v1alpha1.WorkerStatus,
) *v1alpha1.ClusterOrder {
	t.Helper()
	if err := r.updateWorkerStatusWithAgent(context.Background(), co, workers); err != nil {
		t.Fatal(err)
	}
	if err := r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	return co
}

func assertWorkerCounts(t *testing.T, co *v1alpha1.ClusterOrder, desired, current, ready int32) {
	t.Helper()
	if co.Status.DesiredWorkers == nil || co.Status.CurrentWorkers == nil || co.Status.ReadyWorkers == nil {
		t.Fatalf("summary not persisted: %+v", co.Status)
	}
	if *co.Status.DesiredWorkers != desired || *co.Status.CurrentWorkers != current || *co.Status.ReadyWorkers != ready {
		t.Fatalf("counts=(%d,%d,%d), want (%d,%d,%d)",
			*co.Status.DesiredWorkers, *co.Status.CurrentWorkers, *co.Status.ReadyWorkers,
			desired, current, ready)
	}
}

// R10-U1: requested capacity is visible before any reservation exists and is not
// derived from the length of the worker journal.
func TestR10DesiredBeforeReservation(t *testing.T) {
	r, co := workerCountsHarness(t, "r10-desired", []v1alpha1.NodeRequest{nodeRequest("standard", 2)}, nil)
	persistWorkerSummary(t, r, co, nil)
	assertWorkerCounts(t, co, 2, 0, 0)
}

// R10-U2: retiring and failed records do not inflate desired or active
// availability, and a durably retired failed record does not keep the order
// retrying while an actionable failure still reports.
func TestR10CleanupNotDesiredOrCurrent(t *testing.T) {
	r, co := workerCountsHarness(t, "r10-cleanup", []v1alpha1.NodeRequest{nodeRequest("standard", 1)}, nil)
	co.SetStatusCondition(v1alpha1.ConditionWorkersFailed, metav1.ConditionTrue, "retry 1", reasonWorkersFailed)
	workers := []v1alpha1.WorkerStatus{
		newWorkerStatus("standard", "standard", "ready-0", "id-ready", workerPhaseReady),
		newWorkerStatus("standard", "standard", "retiring-0", "id-retiring", workerPhaseUnbinding),
		newWorkerStatus("standard", "standard", "failed-0", "id-failed", workerPhaseUnbinding),
	}
	persistWorkerSummary(t, r, co, workers)
	assertWorkerCounts(t, co, 1, 1, 1)
	if apimeta.IsStatusConditionTrue(co.Status.Conditions, v1alpha1.ConditionWorkersFailed) {
		t.Fatalf("a retired failed record kept the order retrying: %+v", co.Status.Conditions)
	}

	// An actionable failure within the requested capacity still reports.
	actionable := []v1alpha1.WorkerStatus{
		newWorkerStatus("standard", "standard", "failed-1", "id-failed1", workerPhaseFailed),
	}
	persistWorkerSummary(t, r, co, actionable)
	assertWorkerCounts(t, co, 1, 0, 0)
	if !apimeta.IsStatusConditionTrue(co.Status.Conditions, v1alpha1.ConditionWorkersFailed) {
		t.Fatalf("an actionable failure did not report: %+v", co.Status.Conditions)
	}
}

// R10-U3: ready surplus in one NodeSet cannot compensate for a missing NodeSet,
// even when both share one instance type.
func TestR10LogicalNodeSetCoverage(t *testing.T) {
	requests := []v1alpha1.NodeRequest{
		{NodeSet: "a", NumberOfNodes: 1, BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: "shared"}},
		{NodeSet: "b", NumberOfNodes: 1, BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: "shared"}},
	}
	workers := []v1alpha1.WorkerStatus{
		newWorkerStatus("a", "shared", "a-0", "id-a0", workerPhaseReady),
		newWorkerStatus("a", "shared", "a-1", "id-a1", workerPhaseReady),
	}
	r, co := workerCountsHarness(t, "r10-nodesets", requests, workers)
	persistWorkerSummary(t, r, co, workers)
	assertWorkerCounts(t, co, 2, 1, 1)
}
