// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
func TestEarlyAgentObservationPreservesConcurrentWorkerState(t *testing.T) {
	for _, mutation := range []string{"appended", "failed", "history"} {
		t.Run(mutation, func(t *testing.T) {
			r, _, co := bmiStageHarness(t, workerPhaseWaitingForAgent, "id")
			kube := r.Client
			var concurrent v1alpha1.WorkerStatus
			r.Client = &bmiConflictClient{Client: kube, beforePatch: func() {
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
			agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*agentPhaseFixture("slot", true)}}
			observed := indexWorkerBMIs([]*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "id")})
			observed.agents = agents
			changes, err := r.observeExistingWorkers(context.Background(), co, "tenant", observed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.persistWorkerChangesAndRefresh(context.Background(), co, changes); err != nil {
				t.Fatal(err)
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

func TestFinalWorkerStatusPreservesAppendedWorkersAndAggregates(t *testing.T) {
	ctx := context.Background()
	r, _, co := bmiStageHarness(t, workerPhaseBinding, "id")
	kube := r.Client
	r.Client = &bmiConflictClient{Client: kube, beforePatch: func() {
		latest := &v1alpha1.ClusterOrder{}
		if err := kube.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
			t.Fatal(err)
		}
		latest.Status.Workers = append(latest.Status.Workers, newWorkerStatus("standard", "standard", "appended", "appended-id", workerPhaseReady))
		if err := kube.Status().Update(ctx, latest); err != nil {
			t.Fatal(err)
		}
	}}
	next := append([]v1alpha1.WorkerStatus(nil), co.Status.Workers...)
	next[0].Phase = workerPhaseReady
	if err := r.updateWorkerStatusWithAgent(ctx, co, next); err != nil {
		t.Fatal(err)
	}
	if err := kube.Get(ctx, client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if len(co.Status.Workers) != 2 || co.Status.ReadyWorkers == nil || *co.Status.ReadyWorkers != 2 {
		t.Fatalf("lost appended worker or stale aggregate: %+v", co.Status)
	}
}

func TestFinalAgentStageReusesEarlyPhaseProjection(t *testing.T) {
	r, fc, co := workerReadHarness(t)
	fc.listed = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "recorded-id")}
	calls := 0
	r.SetMACResolver(func(context.Context, string) []string { calls++; return nil })
	observed, res, err := r.observeWorkerResources(context.Background(), co)
	if err != nil || !res.IsZero() {
		t.Fatalf("observation: %+v %v", res, err)
	}
	changes, err := r.observeExistingWorkers(context.Background(), co, "tenant", observed)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := r.persistWorkerChangesAndRefresh(context.Background(), co, changes); err != nil || !res.IsZero() {
		t.Fatalf("status: %+v %v", res, err)
	}
	_, _, err = r.reconcileObservedAgents(context.Background(), co, observed)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("phase MAC resolutions=%d, want only early observation", calls)
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
	infra := &unstructured.Unstructured{}
	infra.SetGroupVersionKind(infraEnvGVK)
	infra.SetName(co.Name + infraEnvNameSuffix)
	infra.SetNamespace(co.Namespace)
	infra.SetUID("new")
	_ = unstructured.SetNestedField(infra.Object, "http://ignition.test", "status", "bootArtifacts", "discoveryIgnitionURL")
	if err := r.Create(ctx, infra); err != nil {
		t.Fatal(err)
	}
	fc.listed = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "recorded-id")}
	r.ignition = workerIgnition{}
	injected := errors.New("stale failure persistence interrupted")
	r.Client = &staleFailureClient{Client: r.Client, err: injected}
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
}
