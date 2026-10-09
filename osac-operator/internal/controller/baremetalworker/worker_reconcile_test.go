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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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
func workerReadHarness() (*Reconciler, *workerReadClient, *v1alpha1.ClusterOrder) {
	GinkgoHelper()
	r, fc, co := bmiStageHarness(workerPhaseWaitingForAgent, "recorded-id")
	Expect(corev1.AddToScheme(r.scheme)).To(Succeed())
	co.Finalizers = []string{bmWorkerFinalizer}
	co.Labels = map[string]string{clusterOrderIDLabel: "cluster"}
	co.Annotations = map[string]string{"osac.openshift.io/tenant": "tenant"}
	Expect(r.Update(context.Background(), co)).To(Succeed())
	provider := &workerReadClient{bmiObservationClient: fc}
	r = NewReconciler(r.Client, r.apiReader, r.scheme, provider, nil, r.recorder, co.Namespace)
	return r, provider, co
}

var _ = It("returns after persisting the finalizer before observing resources", func() {
	r, fc, co := workerReadHarness()
	co.Finalizers = nil
	Expect(r.Update(context.Background(), co)).To(Succeed())
	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
	Expect(err).NotTo(HaveOccurred(), "finalizer boundary: result=%+v err=%v", res, err)
	Expect(res.IsZero()).To(BeFalse(), "finalizer boundary: result=%+v err=%v", res, err)
	Expect(r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	Expect(co.Finalizers).To(HaveLen(1), "continued past finalizer persistence: finalizers=%v lists=%d creates=%v", co.Finalizers, fc.lists, fc.names)
	Expect(co.Finalizers[0]).To(Equal(bmWorkerFinalizer), "continued past finalizer persistence: finalizers=%v lists=%d creates=%v", co.Finalizers, fc.lists, fc.names)
	Expect(fc.lists).To(Equal(0), "continued past finalizer persistence: finalizers=%v lists=%d creates=%v", co.Finalizers, fc.lists, fc.names)
	Expect(fc.names).To(BeEmpty(), "continued past finalizer persistence: finalizers=%v lists=%d creates=%v", co.Finalizers, fc.lists, fc.names)
})

var _ = It("returns after persisting identity repair before resolving prerequisites", func() {
	r, fc, co := workerReadHarness()
	co.Status.Workers[0].BareMetalInstance.ID = ""
	co.Status.Workers[0].Phase = workerPhaseProvisioning
	Expect(r.Status().Update(context.Background(), co)).To(Succeed())
	fc.listed = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "created-id")}
	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
	Expect(err).NotTo(HaveOccurred(), "repair entered missing pull-secret gate: result=%+v err=%v", res, err)
	Expect(res.IsZero()).To(BeFalse(), "repair entered missing pull-secret gate: result=%+v err=%v", res, err)
	Expect(r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	Expect(co.Status.Workers[0].BareMetalInstance.ID).To(Equal("created-id"), "repair not persisted before returning")
	Expect(fc.names).To(BeEmpty(), "repair not persisted before returning")
})

var _ = Describe("Worker observation read budget", func() {
	for _, omitted := range []bool{false, true} {
		It(map[bool]string{false: "listed", true: "omitted"}[omitted], func() {
			r, fc, co := workerReadHarness()
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
			Expect(r.Create(context.Background(), a)).To(Succeed())
			_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
			Expect(err).To(HaveOccurred(), "expected pull-secret gate for unchanged observation")
			wantGets := 0
			if omitted {
				wantGets = 1
			}
			Expect(fc.lists).To(Equal(1), "observation lists=%d gets=%d, want 1/%d", fc.lists, fc.gets, wantGets)
			Expect(fc.gets).To(Equal(wantGets), "observation lists=%d gets=%d, want 1/%d", fc.lists, fc.gets, wantGets)
			Expect(fc.names).To(BeEmpty(), "created before prerequisite gate")
		})
	}
})
var _ = Describe("Worker observation failures precede prerequisite resolution", func() {
	for _, err := range []error{status.Error(codes.Internal, "list outage"), ErrFulfillmentServiceUnavailable} {
		It(err.Error(), func() {
			r, fc, co := workerReadHarness()
			fc.bmis = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "recorded-id")}
			fc.listErr = err
			before := co.DeepCopy()
			res, gotErr := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
			if errors.Is(err, ErrFulfillmentServiceUnavailable) {
				Expect(gotErr).NotTo(HaveOccurred(), "result=%+v error=%v", res, gotErr)
				Expect(res.RequeueAfter).To(Equal(unavailableBackoff), "result=%+v error=%v", res, gotErr)
			} else {
				Expect(status.Code(gotErr)).To(Equal(codes.Internal), "expected List outage")
			}
			Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
			Expect(before.Status.Workers).To(Equal(co.Status.Workers), "outage changed workers")
			Expect(fc.lists).To(Equal(1), "outage lists=%d gets=%d", fc.lists, fc.gets)
			Expect(fc.gets).To(Equal(0), "outage lists=%d gets=%d", fc.lists, fc.gets)
		})
	}
})
var _ = It("BMI recovery rejects ambiguous names", func() {
	r, fc, co := bmiStageHarness(workerPhaseProvisioning, "")
	fc.bmis = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "first"), ownedBMIFixture(co, "recorded-bmi", "second")}
	if _, _, err := runBMIStage(context.Background(), r, co); err == nil {
		Fail("adopted ambiguous name")
	}
	Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	Expect(co.Status.Workers[0].BareMetalInstance.ID).To(Equal(""), "persisted ambiguous identity")
})
var _ = Describe("Early Agent observation rejects concurrent worker state changes", func() {
	for _, mutation := range []string{"appended", "failed", "history"} {
		It(mutation, func() {
			r, _, co := bmiStageHarness(workerPhaseWaitingForAgent, "id")
			kube := r.Client
			var concurrent v1alpha1.WorkerStatus
			conflict := &bmiConflictClient{Client: kube, beforePatch: func() {
				latest := &v1alpha1.ClusterOrder{}
				Expect(kube.Get(context.Background(), client.ObjectKeyFromObject(co), latest)).To(Succeed())
				switch mutation {
				case "appended":
					latest.Status.Workers = append(latest.Status.Workers, newWorkerStatus("standard", "standard", "new-slot", "new-id", workerPhaseBinding))
				case "failed":
					latest.Status.Workers[0].Phase = workerPhaseFailed
				case "history":
					latest.Status.Workers[0].AttemptCount = 7
				}
				concurrent = latest.Status.Workers[len(latest.Status.Workers)-1]
				Expect(kube.Status().Update(context.Background(), latest)).To(Succeed())
				Expect(kube.Get(context.Background(), client.ObjectKeyFromObject(co), latest)).To(Succeed())
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
			Expect(err).NotTo(HaveOccurred())
			err = r.updateWorkerStatus(context.Background(), co, workers)
			Expect(apierrors.IsConflict(err)).To(BeTrue(), "error=%v, want one-shot conflict", err)
			Expect(conflict.patches).To(Equal(1), "status patches=%d, want 1", conflict.patches)
			Expect(kube.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
			got := co.Status.Workers[len(co.Status.Workers)-1]
			Expect(got).To(Equal(concurrent), "overwrote concurrent worker: got=%+v want=%+v", got, concurrent)
		})
	}
})

var _ = It("propagates status conflicts without retrying in the invocation", func() {
	ctx := context.Background()
	r, _, co := bmiStageHarness(workerPhaseBinding, "id")
	kube := r.Client
	conflict := &bmiConflictClient{Client: kube, beforePatch: func() {
		latest := &v1alpha1.ClusterOrder{}
		Expect(kube.Get(ctx, client.ObjectKeyFromObject(co), latest)).To(Succeed())
		latest.Status.Workers = append(latest.Status.Workers,
			newWorkerStatus("standard", "standard", "concurrent", "concurrent-id", workerPhaseReady))
		Expect(kube.Status().Update(ctx, latest)).To(Succeed())
	}}
	r.Client = conflict
	next := append([]v1alpha1.WorkerStatus(nil), co.Status.Workers...)
	next[0].Phase = workerPhaseReady

	err := r.updateWorkerStatus(ctx, co, next)
	Expect(apierrors.IsConflict(err)).To(BeTrue(), "error=%v, want one-shot conflict", err)
	Expect(conflict.patches).To(Equal(1), "status patches=%d, want 1", conflict.patches)
	latest := &v1alpha1.ClusterOrder{}
	Expect(kube.Get(ctx, client.ObjectKeyFromObject(co), latest)).To(Succeed())
	Expect(latest.Status.Workers).To(HaveLen(2), "concurrent worker was not preserved: %+v", latest.Status.Workers)
	Expect(latest.Status.Workers[1].Name).To(Equal("concurrent"), "concurrent worker was not preserved: %+v", latest.Status.Workers)
	Expect(latest.Status.Workers[0].Phase).To(Equal(workerPhaseBinding), "stale worker update was applied after conflict: %+v", latest.Status.Workers)
})

var _ = It("stops on a conflict while persisting the final worker status", func() {
	ctx := context.Background()
	r, _, co := bmiStageHarness(workerPhaseBinding, "id")
	kube := r.Client
	conflict := &bmiConflictClient{Client: kube, beforePatch: func() {
		latest := &v1alpha1.ClusterOrder{}
		Expect(kube.Get(ctx, client.ObjectKeyFromObject(co), latest)).To(Succeed())
		latest.Status.Workers = append(latest.Status.Workers, newWorkerStatus("standard", "standard", "appended", "appended-id", workerPhaseReady))
		Expect(kube.Status().Update(ctx, latest)).To(Succeed())
	}}
	r.Client = conflict
	next := append([]v1alpha1.WorkerStatus(nil), co.Status.Workers...)
	next[0].Phase = workerPhaseReady
	if err := r.updateWorkerStatusWithAgent(ctx, co, next); !apierrors.IsConflict(err) {
		Fail(fmt.Sprintf("error=%v, want one-shot conflict", err))
	}
	Expect(conflict.patches).To(Equal(1), "status patches=%d, want 1", conflict.patches)
	Expect(kube.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
	Expect(co.Status.Workers).To(HaveLen(2), "lost appended worker: %+v", co.Status)
	Expect(co.Status.Workers[1].Name).To(Equal("appended"), "lost appended worker: %+v", co.Status)
	Expect(co.Status.ReadyWorkers).To(BeNil(), "aggregates changed after conflict: %+v", co.Status)
})

// R05-U1/U3: phase derivation happens exactly once, in the observation
// projection; the Agent action stage never re-derives phases from the snapshot.
var _ = It("projects worker phases once per invocation", func() {
	r, fc, co := workerReadHarness()
	fc.listed = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "recorded-id")}
	calls := 0
	r.SetMACResolver(func(context.Context, string) []string { calls++; return nil })
	observed, res, err := r.observeWorkerResources(context.Background(), co)
	Expect(err).NotTo(HaveOccurred(), "observation: %+v %v", res, err)
	Expect(res.IsZero()).To(BeTrue(), "observation: %+v %v", res, err)
	workers, err := r.observeExistingWorkers(context.Background(), co, "tenant", observed)
	Expect(err).NotTo(HaveOccurred())
	if !workerSlicesEqual(co.Status.Workers, workers) {
		if err := r.updateWorkerStatus(context.Background(), co, workers); err != nil {
			Fail(fmt.Sprintf("status: %v", err))
		}
		Expect(r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	}
	_, _, err = r.reconcileObservedAgents(context.Background(), co, co.Status.Workers, observed)
	Expect(err).NotTo(HaveOccurred())
	Expect(calls).To(Equal(1), "phase MAC resolutions=%d, want exactly one projection", calls)
})

var _ = It("isolates invocation-local MAC resolvers and honors configured overrides", func() {
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
			defer GinkgoRecover()

			defer wg.Done()
			resolve := r.workerMACResolver(tt.o)
			for range 100 {
				if got := resolve(ctx, "same-id"); !reflect.DeepEqual(got, []string{tt.want}) {
					Fail(fmt.Sprintf("cross-invocation MACs=%v want=%s", got, tt.want))
				}
			}
		}()
	}
	wg.Wait()
	Expect(r.macResolver).To(BeNil(), "observation mutated shared resolver")
	r.SetMACResolver(func(context.Context, string) []string { return []string{"override"} })
	if got := r.workerMACResolver(first)(ctx, "same-id"); !reflect.DeepEqual(got, []string{"override"}) {
		Fail(fmt.Sprintf("override ignored: %v", got))
	}
})
var _ = It("caches unknown evidence from the fallback BMI Get", func() {
	r, fc, co := bmiStageHarness(workerPhaseWaitingForAgent, "missing")
	fc.getErr = status.Error(codes.Internal, "unknown ownership")
	o := indexWorkerBMIs(nil)
	o.agents = &unstructured.UnstructuredList{}
	if _, err := r.observeExistingWorkers(context.Background(), co, "tenant", o); err == nil {
		Fail("unknown ownership permitted convergence")
	}
	r.macResolver = nil
	resolve := r.workerMACResolver(o)
	for range 3 {
		if got := resolve(context.Background(), "missing"); len(got) != 0 {
			Fail("unknown evidence produced MACs")
		}
	}
	Expect(fc.gets).To(Equal(1), "fallback Gets=%d, want 1", fc.gets)
})

func (f *workerReadClient) GetClusterVersion(context.Context, string) (*privatev1.ClusterVersion, error) {
	return privatev1.ClusterVersion_builder{Id: "cv"}.Build(), nil
}

var _ = It("cleans up workers without image or ignition prerequisites", func() {
	ctx := context.Background()
	r, base, co := workerReadHarness()
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
	Expect(r.Update(ctx, co)).To(Succeed())
	co.Status.Workers = append(co.Status.Workers, newWorkerStatus("standard", "standard", "excess", "excess-id", workerPhaseWaitingForAgent))
	Expect(r.Status().Update(ctx, co)).To(Succeed())
	res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
	Expect(err).NotTo(HaveOccurred(), "retirement boundary: result=%+v err=%v", res, err)
	Expect(res.IsZero()).To(BeFalse(), "retirement boundary: result=%+v err=%v", res, err)
	Expect(r.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
	if got := workerByName(co.Status.Workers, "excess"); got == nil || got.Phase != workerPhaseUnbinding {
		Fail(fmt.Sprintf("retirement intent was not persisted without prerequisites: %+v", co.Status.Workers))
	}
	Expect(blocked.names).To(BeEmpty(), "prerequisite-free retirement acted externally: creates=%v deletes=%v ignition=%d", blocked.names, blocked.deletes, ignition.calls)
	Expect(blocked.deletes).To(BeEmpty(), "prerequisite-free retirement acted externally: creates=%v deletes=%v ignition=%d", blocked.names, blocked.deletes, ignition.calls)
	Expect(ignition.calls).To(Equal(0), "prerequisite-free retirement acted externally: creates=%v deletes=%v ignition=%d", blocked.names, blocked.deletes, ignition.calls)

	// Cleanup proceeds on the next invocation even though the missing pull secret
	// is still reported, and it never fetches discovery ignition.
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); err == nil {
		Fail("missing pull secret was not reported")
	}
	Expect(blocked.deletes).To(HaveLen(1), "cleanup did not request BMI deletion: %v", blocked.deletes)
	Expect(blocked.deletes[0]).To(Equal("excess-id"), "cleanup did not request BMI deletion: %v", blocked.deletes)
	Expect(ignition.calls).To(Equal(0), "cleanup fetched ignition or created a BMI: ignition=%d creates=%v", ignition.calls, blocked.names)
	Expect(blocked.names).To(BeEmpty(), "cleanup fetched ignition or created a BMI: ignition=%d creates=%v", ignition.calls, blocked.names)
})

var _ = It("binds an Agent while another worker waits for its retry", func() {
	ctx := context.Background()
	r, base, co := workerReadHarness()
	fc := &r04PrereqClient{workerReadClient: base}
	r.fulfillment = fc
	future := metav1.NewTime(time.Now().Add(time.Hour))
	failed := newWorkerStatus("standard", "standard", "failed-bmi", "failed-id", workerPhaseFailed)
	failed.NextRetryTime = &future
	binding := newWorkerStatus("standard", "standard", "binding-bmi", "binding-id", workerPhaseBinding)
	co.Spec.NodeRequests[0].NumberOfNodes = 2
	co.Spec.PullSecret = `{"auths":{}}`
	Expect(r.Update(ctx, co)).To(Succeed())
	co.Status.Workers = []v1alpha1.WorkerStatus{failed, binding}
	Expect(r.Status().Update(ctx, co)).To(Succeed())
	fc.bmis = []*privatev1.BareMetalInstance{
		ownedBMIFixture(co, "failed-bmi", "failed-id"),
		ownedBMIFixture(co, "binding-bmi", "binding-id"),
	}
	fc.listed = fc.bmis
	agent := agentPhaseFixture("binding-bmi", true)
	agent.SetNamespace(co.Namespace)
	agent.SetLabels(map[string]string{workerNameLabel: "binding-bmi", clusterOrderLabel: co.Name})
	Expect(unstructured.SetNestedSlice(agent.Object, []interface{}{map[string]interface{}{"macAddress": "aa:bb:cc:dd:ee:01"}}, "status", "inventory", "interfaces")).To(Succeed())
	Expect(r.Create(ctx, agent)).To(Succeed())
	// The first invocation projects the labeled Agent to Ready while the other
	// worker still waits for its retry; the second performs its pending cleanup.
	// A finite trace: binding converges on the first invocation, while the other
	// worker's cleanup proceeds on the next one that is not preempted by the
	// InfraEnv creation boundary.
	for i := 0; i < 4 && len(fc.deletes) == 0; i++ {
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); err != nil {
			Expect(err).NotTo(HaveOccurred())
		}
	}
	Expect(r.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
	if got := workerByName(co.Status.Workers, "binding-bmi"); got == nil || got.Phase != workerPhaseReady {
		Fail(fmt.Sprintf("binding did not converge past the pending retry: %+v", co.Status.Workers))
	}
	if got := workerByName(co.Status.Workers, "failed-bmi"); got == nil || got.Phase != workerPhaseFailed || got.BareMetalInstance.ID != "failed-id" {
		Fail(fmt.Sprintf("pending retry lost its recorded incarnation: %+v", co.Status.Workers))
	}
	Expect(fc.deletes).To(Equal([]string{"failed-id"}), "pending cleanup deletes=%v, want the failed incarnation only", fc.deletes)
	if deadline := r.workerRecheckDeadline(co.Status.Workers, time.Now()); deadline.RequeueAfter <= 0 {
		Fail(fmt.Sprintf("pending cleanup did not contribute a bounded recheck: %+v", deadline))
	}
})

var _ = It("makes independent progress across mixed worker states", func() {
	ctx := context.Background()
	r, base, co := workerReadHarness()
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
	Expect(r.Update(ctx, co)).To(Succeed())
	co.Status.Workers = []v1alpha1.WorkerStatus{healthy, pending, waiting}
	Expect(r.Status().Update(ctx, co)).To(Succeed())
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
		Expect(unstructured.SetNestedSlice(agent.Object, []interface{}{map[string]interface{}{"macAddress": mac}}, "status", "inventory", "interfaces")).To(Succeed())
		Expect(r.Create(ctx, agent)).To(Succeed())
	}
	for range 4 {
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); err != nil {
			Expect(err).NotTo(HaveOccurred())
		}
	}
	Expect(r.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
	bound := workerByName(co.Status.Workers, "waiting-bmi")
	Expect(bound).NotTo(BeNil(), "actionable worker did not converge: %+v", co.Status.Workers)
	Expect(bound.Phase).To(Equal(workerPhaseReady), "actionable worker did not converge: %+v", co.Status.Workers)
	kept := workerByName(co.Status.Workers, "healthy-bmi")
	Expect(kept).NotTo(BeNil(), "healthy worker history was reset by an unrelated delay: %+v", kept)
	Expect(kept.AttemptCount).To(Equal(int32(2)), "healthy worker history was reset by an unrelated delay: %+v", kept)
	Expect(kept.LastFailureReason).To(Equal("previous"), "healthy worker history was reset by an unrelated delay: %+v", kept)
	Expect(kept.ReadySince).NotTo(BeNil(), "healthy worker history was reset by an unrelated delay: %+v", kept)
	Expect(kept.ReadySince.Equal(&recent)).To(BeTrue(), "healthy worker history was reset by an unrelated delay: %+v", kept)
	delayed := workerByName(co.Status.Workers, "pending-bmi")
	Expect(delayed).NotTo(BeNil(), "delayed worker lost its recorded incarnation: %+v", delayed)
	Expect(delayed.Phase).To(Equal(workerPhaseFailed), "delayed worker lost its recorded incarnation: %+v", delayed)
	Expect(delayed.BareMetalInstance.ID).To(Equal("pending-id"), "delayed worker lost its recorded incarnation: %+v", delayed)
	Expect(fc.deletes).ToNot(BeEmpty(), "pending cleanup was starved by unrelated progress")
	if deadline := r.workerRecheckDeadline(co.Status.Workers, time.Now()); deadline.RequeueAfter <= 0 {
		Fail(fmt.Sprintf("no bounded recheck for the delayed worker: %+v", deadline))
	}
})

var _ = It("persists the worker summary before checking Create prerequisites", func() {
	ctx := context.Background()
	r, base, co := workerReadHarness()
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
	Expect(r.Update(ctx, co)).To(Succeed())
	co.Status.ReadyWorkers = &stale
	Expect(r.Status().Update(ctx, co)).To(Succeed())
	// The fixture InfraEnv is the object production creates: owned by this order.
	r07CreateInfraEnv(r, r07InfraEnv(r, co, "infra-uid", "http://ignition.test"))
	co.SetStatusCondition(v1alpha1.ConditionInfraEnvReady, metav1.ConditionTrue, "ready", reasonInfraEnvReady)
	Expect(r.Status().Update(ctx, co)).To(Succeed())
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
	Expect(errors.Is(err, imageErr)).To(BeTrue(), "error=%v, want the blocked image lookup", err)
	Expect(r.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
	Expect(co.Status.ReadyWorkers).NotTo(BeNil(), "stale ready summary was not demoted before the create gate: %+v", co.Status)
	Expect(*co.Status.ReadyWorkers).To(Equal(int32(0)), "stale ready summary was not demoted before the create gate: %+v", co.Status)
	Expect(co.Status.DesiredWorkers).NotTo(BeNil(), "aggregate counts were not persisted: %+v", co.Status)
	Expect(*co.Status.DesiredWorkers).To(Equal(int32(2)), "aggregate counts were not persisted: %+v", co.Status)
	Expect(co.Status.CurrentWorkers).NotTo(BeNil(), "aggregate counts were not persisted: %+v", co.Status)
	Expect(*co.Status.CurrentWorkers).To(Equal(int32(1)), "aggregate counts were not persisted: %+v", co.Status)
	Expect(blocked.names).To(BeEmpty(), "created a BMI under a blocked image lookup: %v", blocked.names)
})

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
func r07Harness(phase string) (*Reconciler, *workerReadClient, *v1alpha1.ClusterOrder) {
	GinkgoHelper()
	ctx := context.Background()
	r, fc, co := workerReadHarness()
	co.Spec.PullSecret = `{"auths":{}}`
	Expect(r.Update(ctx, co)).To(Succeed())
	co.Status.Workers[0].Phase = phase
	Expect(r.Status().Update(ctx, co)).To(Succeed())
	fc.listed = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "recorded-id")}
	fc.bmis = fc.listed
	return r, fc, co
}

// r07InfraEnv builds the object production creates: deterministic name, controller
// owner reference to the ClusterOrder, and optional boot-artifact evidence.
func r07InfraEnv(r *Reconciler, owner *v1alpha1.ClusterOrder, uid, ignitionURL string) *unstructured.Unstructured {
	GinkgoHelper()
	return r07InfraEnvNamed(r, owner, owner.Name+infraEnvNameSuffix, uid, ignitionURL)
}

// r07InfraEnvNamed builds a controlled InfraEnv under an explicit name, so a
// same-name object controlled by a different ClusterOrder can be forged.
func r07InfraEnvNamed(r *Reconciler, owner *v1alpha1.ClusterOrder, name, uid, ignitionURL string) *unstructured.Unstructured {
	GinkgoHelper()
	infra := &unstructured.Unstructured{}
	infra.SetGroupVersionKind(infraEnvGVK)
	infra.SetName(name)
	infra.SetNamespace(owner.Namespace)
	if uid != "" {
		infra.SetUID(types.UID(uid))
	}
	if err := controllerutil.SetControllerReference(owner, infra, r.scheme); err != nil {
		Fail(fmt.Sprintf("setting infraenv owner reference: %v", err))
	}
	if ignitionURL != "" {
		Expect(unstructured.SetNestedField(infra.Object, ignitionURL, "status", "bootArtifacts", "discoveryIgnitionURL")).To(Succeed())
	}
	return infra
}

func r07CreateInfraEnv(r *Reconciler, infra *unstructured.Unstructured) {
	GinkgoHelper()
	Expect(r.Create(context.Background(), infra)).To(Succeed())
}

// r07InfraEnvObjects returns the deterministic-name InfraEnv objects in the order's
// namespace, so duplication and replacement are observable.
func r07InfraEnvObjects(r *Reconciler, co *v1alpha1.ClusterOrder) []unstructured.Unstructured {
	GinkgoHelper()
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(infraEnvGVK)
	Expect(r.List(context.Background(), list, client.InNamespace(co.Namespace))).To(Succeed())
	var found []unstructured.Unstructured
	for i := range list.Items {
		if list.Items[i].GetName() == co.Name+infraEnvNameSuffix {
			found = append(found, list.Items[i])
		}
	}
	return found
}

// InfraEnv absence and presence are decided
// by the resource, not by the previous InfraEnvReady condition. Every prior
// condition state creates exactly one object on authoritative absence, and a
// present object whose discovery ignition URL is missing replaces a stale Ready
// claim with current pending evidence.
var _ = It("derives InfraEnv lookup from resource evidence rather than the Ready condition", func() {
	ctx := context.Background()
	for _, present := range []bool{true, false} {
		for _, prior := range []metav1.ConditionStatus{"", metav1.ConditionTrue, metav1.ConditionFalse} {
			label := "absent"
			if present {
				label = "present"
			}
			By(fmt.Sprintf("%s-prior-%q", label, string(prior)))
			func() {
				r, fc, co := r07Harness(workerPhaseReady)
				if prior != "" {
					co.SetStatusCondition(v1alpha1.ConditionInfraEnvReady, prior, "PriorEvidence", "prior evidence")
					Expect(r.Status().Update(ctx, co)).To(Succeed())
				}
				var created []unstructured.Unstructured
				if present {
					r07CreateInfraEnv(r, r07InfraEnv(r, co, "infra-uid", ""))
					created = r07InfraEnvObjects(r, co)
					Expect(created).To(HaveLen(1), "fixture InfraEnv objects=%d", len(created))
				}
				// A finite trace: an evidence-condition boundary may consume the first
				// invocation and the UID evidence is recorded by a later one.
				for range 3 {
					if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); err != nil {
						Fail(fmt.Sprintf("reconcile: %v", err))
					}
				}
				found := r07InfraEnvObjects(r, co)
				Expect(found).To(HaveLen(1), "InfraEnv objects=%d, want exactly one for present=%t prior=%q", len(found), present, prior)
				Expect(r.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
				cond := apimeta.FindStatusCondition(co.Status.Conditions, v1alpha1.ConditionInfraEnvReady)
				Expect(cond).NotTo(BeNil(), "InfraEnv without boot artifacts reported %+v, want current pending evidence", cond)
				Expect(cond.Status).To(Equal(metav1.ConditionFalse), "InfraEnv without boot artifacts reported %+v, want current pending evidence", cond)
				Expect(cond.Reason).To(Equal(reasonIgnitionPending), "InfraEnv without boot artifacts reported %+v, want current pending evidence", cond)
				if present {
					Expect(found[0].GetUID()).To(Equal(created[0].GetUID()), "present InfraEnv was replaced: uid=%q want %q", found[0].GetUID(), created[0].GetUID())
					Expect(co.Annotations[infraEnvUIDAnnotation]).To(Equal(string(found[0].GetUID())), "present InfraEnv was not observed and recorded: annotations=%v uid=%q", co.Annotations, found[0].GetUID())
				} else {
					Expect(co.Annotations[infraEnvUIDAnnotation]).To(BeEmpty(), "absent InfraEnv recorded a UID")
				}
				Expect(fc.names).To(BeEmpty(), "InfraEnv lookup created a BMI: %v", fc.names)
			}()
		}
	}
})

// A same-name InfraEnv this
// ClusterOrder does not control is reported as an error and is never adopted,
// replaced, or consumed as ignition evidence.
var _ = It("rejects an existing InfraEnv owned by another cluster", func() {
	ctx := context.Background()
	for name, build := range map[string]func(r *Reconciler, co *v1alpha1.ClusterOrder) *unstructured.Unstructured{
		"ownerless": func(_ *Reconciler, co *v1alpha1.ClusterOrder) *unstructured.Unstructured {
			infra := &unstructured.Unstructured{}
			infra.SetGroupVersionKind(infraEnvGVK)
			infra.SetName(co.Name + infraEnvNameSuffix)
			infra.SetNamespace(co.Namespace)
			infra.SetUID("foreign-uid")
			return infra
		},
		"other ClusterOrder": func(r *Reconciler, co *v1alpha1.ClusterOrder) *unstructured.Unstructured {
			foreign := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "other-order", Namespace: co.Namespace, UID: "other-uid"}}
			return r07InfraEnvNamed(r, foreign, co.Name+infraEnvNameSuffix, "foreign-uid", "http://ignition.test")
		},
	} {
		By(name)
		func() {
			r, fc, co := r07Harness(workerPhaseWaitingForAgent)
			ignition := &countingIgnition{}
			r.ignition = ignition
			foreign := build(r, co)
			r07CreateInfraEnv(r, foreign)
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
			Expect(err).To(HaveOccurred(), "err=%v, want a foreign-owner error", err)
			Expect(strings.Contains(err.Error(), "not controlled by ClusterOrder")).To(BeTrue(), "err=%v, want a foreign-owner error", err)
			found := r07InfraEnvObjects(r, co)
			Expect(found).To(HaveLen(1), "foreign InfraEnv was replaced: %d object(s) %v", len(found), found)
			Expect(found[0].GetUID()).To(Equal(foreign.GetUID()), "foreign InfraEnv was replaced: %d object(s) %v", len(found), found)
			Expect(r.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
			Expect(co.Annotations[infraEnvUIDAnnotation]).To(Equal(""), "foreign InfraEnv UID was recorded: %v", co.Annotations)
			Expect(fc.names).To(BeEmpty(), "foreign InfraEnv authorized work: creates=%v ignition=%d", fc.names, ignition.calls)
			Expect(ignition.calls).To(Equal(0), "foreign InfraEnv authorized work: creates=%v ignition=%d", fc.names, ignition.calls)
		}()
	}
})

// The ownership check
// validates namespace, controller kind, name and (when recorded) the ClusterOrder
// incarnation UID, so only this order's own object is consumed as evidence.
var _ = It("rejects foreign InfraEnv owner references", func() {
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
		By(name)
		func() {
			err := validateInfraEnvOwner(order, tc.infra)
			Expect(tc.wantErr).To(Equal((err != nil)), "err=%v, wantErr=%t", err, tc.wantErr)
		}()
	}
})

// r07StaleOrder prepares an order whose recorded InfraEnv UID is stale: the
// recorded value is "old", the observed replacement is "new", and the waiting
// worker must be failed before the replacement UID is acknowledged.
func r07StaleOrder() (*Reconciler, *workerReadClient, *v1alpha1.ClusterOrder) {
	GinkgoHelper()
	ctx := context.Background()
	r, fc, co := r07Harness(workerPhaseWaitingForAgent)
	co.Annotations[infraEnvUIDAnnotation] = "old"
	Expect(r.Update(ctx, co)).To(Succeed())
	co.SetStatusCondition(v1alpha1.ConditionInfraEnvReady, metav1.ConditionTrue, "ready", reasonInfraEnvReady)
	Expect(r.Status().Update(ctx, co)).To(Succeed())
	r07CreateInfraEnv(r, r07InfraEnv(r, co, "new", "http://ignition.test"))
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
func r07CreateHarness() (*Reconciler, *workerReadClient, *v1alpha1.ClusterOrder) {
	GinkgoHelper()
	ctx := context.Background()
	r, fc, co := workerReadHarness()
	co.Spec.PullSecret = `{"auths":{}}`
	Expect(r.Update(ctx, co)).To(Succeed())
	co.Status.Workers = nil
	Expect(r.Status().Update(ctx, co)).To(Succeed())
	fc.listed = nil
	fc.bmis = nil
	// The unit harness has no ignition fetcher; default to a valid empty artifact so
	// an invocation never depends on a case's scripted fetcher.
	r.ignition = &r07IgnitionScripted{body: []byte(`{}`)}
	return r, fc, co
}

// Only fetched, JSON-valid
// discovery ignition authorizes a BMI create. A missing URL waits, and a fetch or
// validation failure is reported without creating anything.
var _ = It("does not create a BMI from invalid ignition", func() {
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
		By(name)
		func() {
			r, fc, co := r07CreateHarness()
			r07CreateInfraEnv(r, r07InfraEnv(r, co, "infra-uid", tc.ignitionURL))
			ignition := &r07IgnitionScripted{body: tc.body, err: tc.fetchErr}
			r.ignition = ignition
			// A create is due (one requested node, no reserved slot): only validated
			// ignition bytes may produce one, and the missing URL holds instead.
			for i := range 2 {
				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
				switch {
				case tc.wantErr == "":
					Expect(err).ToNot(HaveOccurred(), "invocation %d: err=%v, want a bounded wait for the missing URL", i, err)
				case err == nil || !strings.Contains(err.Error(), tc.wantErr):
					Fail(fmt.Sprintf("invocation %d: err=%v, want %q", i, err, tc.wantErr))
				}
			}
			Expect(fc.names).To(BeEmpty(), "unenforceable ignition authorized a BMI create: %v", fc.names)
			Expect(tc.ignitionURL == "" && ignition.calls != 0).To(BeFalse(), "missing URL was fetched anyway: %d call(s)", ignition.calls)
		}()
	}
})

// A same-name object this
// ClusterOrder does not control never becomes creation input, even when a BMI
// create is due.
var _ = It("does not create a BMI from a foreign InfraEnv", func() {
	ctx := context.Background()
	r, fc, co := r07CreateHarness()
	foreign := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "other-order", Namespace: co.Namespace, UID: "other-uid"}}
	r07CreateInfraEnv(r, r07InfraEnvNamed(r, foreign, co.Name+infraEnvNameSuffix, "foreign-uid", "http://ignition.test"))
	ignition := &r07IgnitionScripted{body: []byte(`{}`)}
	r.ignition = ignition
	for range 2 {
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
		Expect(err).To(HaveOccurred(), "err=%v, want a foreign-owner error", err)
		Expect(strings.Contains(err.Error(), "not controlled by ClusterOrder")).To(BeTrue(), "err=%v, want a foreign-owner error", err)
	}
	Expect(fc.names).To(BeEmpty(), "foreign InfraEnv authorized work: creates=%v ignition=%d", fc.names, ignition.calls)
	Expect(ignition.calls).To(Equal(0), "foreign InfraEnv authorized work: creates=%v ignition=%d", fc.names, ignition.calls)
})

// A converged order whose
// InfraEnv publishes an artifact performs neither a discovery-ignition request nor
// a status patch, so condition reporting cannot create a hot loop.
var _ = It("avoids status writes for a stable Ready order", func() {
	ctx := context.Background()
	r, _, co := r07Harness(workerPhaseReady)
	r07CreateInfraEnv(r, r07InfraEnv(r, co, "infra-uid", "http://ignition.test"))
	co.Annotations[infraEnvUIDAnnotation] = "infra-uid"
	Expect(r.Update(ctx, co)).To(Succeed())
	co.SetStatusCondition(v1alpha1.ConditionInfraEnvReady, metav1.ConditionTrue, "ready", reasonInfraEnvReady)
	Expect(r.Status().Update(ctx, co)).To(Succeed())
	ignition := &countingIgnition{}
	r.ignition = ignition
	// Two invocations settle the worker projection (Agent observation and its
	// ReadySince initialization); the order is stable from then on.
	for range 2 {
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); err != nil {
			Fail(fmt.Sprintf("settling invocation: %v", err))
		}
	}
	counter := &r07CountingStatusClient{Client: r.Client}
	r.Client = counter
	for i := range 2 {
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); err != nil {
			Fail(fmt.Sprintf("invocation %d: %v", i, err))
		}
	}
	Expect(counter.patches).To(BeZero(), "stable order wrote %d status patch(es)", counter.patches)
	Expect(ignition.calls).To(BeZero(), "stable order fetched discovery ignition %d time(s)", ignition.calls)
	Expect(r.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
	Expect(co.Annotations[infraEnvUIDAnnotation]).To(Equal("infra-uid"), "stable evidence drifted: annotations=%v conditions=%v", co.Annotations, co.Status.Conditions)
	Expect(apimeta.IsStatusConditionTrue(co.Status.Conditions, v1alpha1.ConditionInfraEnvReady)).To(BeTrue(), "stable evidence drifted: annotations=%v conditions=%v", co.Annotations, co.Status.Conditions)
})

var _ = It("keeps the old InfraEnv UID when stale-ignition persistence fails", func() {
	ctx := context.Background()
	r, fc, co := workerReadHarness()
	co.Spec.PullSecret = `{"auths":{}}`
	co.Annotations[infraEnvUIDAnnotation] = "old"
	Expect(r.Update(ctx, co)).To(Succeed())
	co.SetStatusCondition(v1alpha1.ConditionInfraEnvReady, metav1.ConditionTrue, "ready", reasonInfraEnvReady)
	Expect(r.Status().Update(ctx, co)).To(Succeed())
	// The replacement models the object the reconciler itself recreates: same
	// deterministic name, owned by this order, new UID.
	r07CreateInfraEnv(r, r07InfraEnv(r, co, "new", "http://ignition.test"))
	fc.listed = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "recorded-id")}
	fc.bmis = fc.listed
	r.ignition = workerIgnition{}
	injected := errors.New("stale failure persistence interrupted")
	base := r.Client
	r.Client = &staleFailureClient{Client: base, err: injected}
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
	Expect(errors.Is(err, injected)).To(BeTrue(), "error=%v, want persistence error", err)
	Expect(r.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
	Expect(co.Annotations[infraEnvUIDAnnotation]).To(Equal("old"), "advanced past failed stale-ignition persistence: %+v", co)
	Expect(co.Status.Workers[0].Phase).To(Equal(workerPhaseWaitingForAgent), "advanced past failed stale-ignition persistence: %+v", co)
	Expect(fc.names).To(BeEmpty(), "advanced past failed stale-ignition persistence: %+v", co)

	// The next explicit invocation retries the same classification and persists it.
	// The recorded UID still does not advance in that invocation.
	r.Client = base
	r.fulfillment = &r04PrereqClient{workerReadClient: fc}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); err != nil {
		Fail(fmt.Sprintf("retry: %v", err))
	}
	Expect(r.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
	failed := co.Status.Workers[0]
	Expect(failed.Phase).To(Equal(workerPhaseFailed), "retry did not persist the stale classification: %+v", failed)
	Expect(failed.LastFailureReason).To(Equal(eventReasonAgentRegistrationTimeout), "retry did not persist the stale classification: %+v", failed)
	Expect(co.Annotations[infraEnvUIDAnnotation]).To(Equal("old"), "UID advanced in the same invocation as the repair: %v", co.Annotations)

	// A later invocation records the replacement UID without re-accounting the
	// already persisted failure.
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); err != nil {
		Fail(fmt.Sprintf("uid evidence: %v", err))
	}
	Expect(r.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
	after := co.Status.Workers[0]
	Expect(co.Annotations[infraEnvUIDAnnotation]).To(Equal("new"), "replacement UID was not recorded: %v", co.Annotations)
	Expect(after.LastFailureReason).To(Equal(failed.LastFailureReason), "UID recording re-emitted failure accounting: before=%+v after=%+v", failed, after)
	Expect(after.LastFailureMessage).To(Equal(failed.LastFailureMessage), "UID recording re-emitted failure accounting: before=%+v after=%+v", failed, after)
	Expect(after.LastFailureTime.Equal(failed.LastFailureTime)).To(BeTrue(), "UID recording re-emitted failure accounting: before=%+v after=%+v", failed, after)
})

// When the
// stale-worker failure is already durable and only the replacement-UID patch is
// lost, the next invocation records the UID without re-emitting failure
// accounting or changing the worker's protected state.
var _ = It("does not count a failure twice after losing InfraEnv UID evidence", func() {
	ctx := context.Background()
	r, fc, co := r07StaleOrder()
	// Deletion support keeps the failed incarnation's cleanup observable instead of
	// panicking in the shared fake.
	r.fulfillment = &r04PrereqClient{workerReadClient: fc}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); err != nil {
		Fail(fmt.Sprintf("persisting the stale failure: %v", err))
	}
	Expect(r.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
	failed := co.Status.Workers[0]
	Expect(failed.Phase).To(Equal(workerPhaseFailed), "fixture did not persist the repair before the UID: %+v annotations=%v", failed, co.Annotations)
	Expect(co.Annotations[infraEnvUIDAnnotation]).To(Equal("old"), "fixture did not persist the repair before the UID: %+v annotations=%v", failed, co.Annotations)

	lost := errors.New("replacement UID patch lost")
	base := r.Client
	r.Client = &r07AnnotationPatchFault{Client: base, err: lost}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); !errors.Is(err, lost) {
		Fail(fmt.Sprintf("error=%v, want the lost UID patch", err))
	}
	r.Client = base
	Expect(r.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
	Expect(co.Annotations[infraEnvUIDAnnotation]).To(Equal("old"), "lost UID patch changed durable state: annotations=%v workers=%+v", co.Annotations, co.Status.Workers)
	Expect(co.Status.Workers[0].LastFailureReason).To(Equal(failed.LastFailureReason), "lost UID patch changed durable state: annotations=%v workers=%+v", co.Annotations, co.Status.Workers)
	Expect(co.Status.Workers[0].LastFailureTime.Equal(failed.LastFailureTime)).To(BeTrue(), "lost UID patch changed durable state: annotations=%v workers=%+v", co.Annotations, co.Status.Workers)

	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)}); err != nil {
		Fail(fmt.Sprintf("recording the UID: %v", err))
	}
	Expect(r.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
	Expect(co.Annotations[infraEnvUIDAnnotation]).To(Equal("new"), "replacement UID was not recorded: %v", co.Annotations)
	Expect(co.Status.Workers[0].LastFailureReason).To(Equal(failed.LastFailureReason), "UID recording re-emitted failure accounting: before=%+v after=%+v", failed, co.Status.Workers[0])
	Expect(co.Status.Workers[0].LastFailureMessage).To(Equal(failed.LastFailureMessage), "UID recording re-emitted failure accounting: before=%+v after=%+v", failed, co.Status.Workers[0])
	Expect(co.Status.Workers[0].LastFailureTime.Equal(failed.LastFailureTime)).To(BeTrue(), "UID recording re-emitted failure accounting: before=%+v after=%+v", failed, co.Status.Workers[0])
})

// --- R10: intent-based worker counts ---

// workerCountsHarness builds an order with a bare-metal intent and (optionally)
// a pre-existing worker journal, then reloads the persisted object so the
// summary write starts from an authoritative snapshot.
func workerCountsHarness(
	name string, requests []v1alpha1.NodeRequest, workers []v1alpha1.WorkerStatus,
) (*Reconciler, *v1alpha1.ClusterOrder) {
	GinkgoHelper()
	r, _, co := nodeSetHarness(name, requests...)
	if len(workers) > 0 {
		mutateCapacityOrder(r, co, func(o *v1alpha1.ClusterOrder) {
			o.Status.Workers = workers
		})
	}
	Expect(r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	return r, co
}

// persistWorkerSummary runs the production summary write and reloads it.
func persistWorkerSummary(
	r *Reconciler, co *v1alpha1.ClusterOrder, workers []v1alpha1.WorkerStatus,
) *v1alpha1.ClusterOrder {
	GinkgoHelper()
	Expect(r.updateWorkerStatusWithAgent(context.Background(), co, workers)).To(Succeed())
	Expect(r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	return co
}

func assertWorkerCounts(co *v1alpha1.ClusterOrder, desired, current, ready int32) {
	GinkgoHelper()
	Expect(co.Status.DesiredWorkers).NotTo(BeNil(), "summary not persisted: %+v", co.Status)
	Expect(co.Status.CurrentWorkers).NotTo(BeNil(), "summary not persisted: %+v", co.Status)
	Expect(co.Status.ReadyWorkers).NotTo(BeNil(), "summary not persisted: %+v", co.Status)
	Expect(*co.Status.DesiredWorkers).To(Equal(desired), "counts=(%d,%d,%d), want (%d,%d,%d)", *co.Status.DesiredWorkers, *co.Status.CurrentWorkers, *co.Status.ReadyWorkers, desired, current, ready)
	Expect(*co.Status.CurrentWorkers).To(Equal(current), "counts=(%d,%d,%d), want (%d,%d,%d)", *co.Status.DesiredWorkers, *co.Status.CurrentWorkers, *co.Status.ReadyWorkers, desired, current, ready)
	Expect(*co.Status.ReadyWorkers).To(Equal(ready), "counts=(%d,%d,%d), want (%d,%d,%d)", *co.Status.DesiredWorkers, *co.Status.CurrentWorkers, *co.Status.ReadyWorkers, desired, current, ready)
}

// R10-U1: requested capacity is visible before any reservation exists and is not
// derived from the length of the worker journal.
var _ = It("reports desired capacity before persisting reservations", func() {
	r, co := workerCountsHarness("r10-desired", []v1alpha1.NodeRequest{nodeRequest("standard", 2)}, nil)
	persistWorkerSummary(r, co, nil)
	assertWorkerCounts(co, 2, 0, 0)
})

// R10-U2: retiring and failed records do not inflate desired or active
// availability, and a durably retired failed record does not keep the order
// retrying while an actionable failure still reports.
var _ = It("excludes retiring workers from desired and current capacity", func() {
	r, co := workerCountsHarness("r10-cleanup", []v1alpha1.NodeRequest{nodeRequest("standard", 1)}, nil)
	co.SetStatusCondition(v1alpha1.ConditionWorkersFailed, metav1.ConditionTrue, "retry 1", reasonWorkersFailed)
	workers := []v1alpha1.WorkerStatus{
		newWorkerStatus("standard", "standard", "ready-0", "id-ready", workerPhaseReady),
		newWorkerStatus("standard", "standard", "retiring-0", "id-retiring", workerPhaseUnbinding),
		newWorkerStatus("standard", "standard", "failed-0", "id-failed", workerPhaseUnbinding),
	}
	persistWorkerSummary(r, co, workers)
	assertWorkerCounts(co, 1, 1, 1)
	Expect(apimeta.IsStatusConditionTrue(co.Status.Conditions, v1alpha1.ConditionWorkersFailed)).To(BeFalse(), "a retired failed record kept the order retrying: %+v", co.Status.Conditions)

	// An actionable failure within the requested capacity still reports.
	actionable := []v1alpha1.WorkerStatus{
		newWorkerStatus("standard", "standard", "failed-1", "id-failed1", workerPhaseFailed),
	}
	persistWorkerSummary(r, co, actionable)
	assertWorkerCounts(co, 1, 0, 0)
	Expect(apimeta.IsStatusConditionTrue(co.Status.Conditions, v1alpha1.ConditionWorkersFailed)).To(BeTrue(), "an actionable failure did not report: %+v", co.Status.Conditions)
})

// R10-U3: ready surplus in one NodeSet cannot compensate for a missing NodeSet,
// even when both share one instance type.
var _ = It("counts capacity independently for each logical NodeSet", func() {
	requests := []v1alpha1.NodeRequest{
		{NodeSet: "a", NumberOfNodes: 1, BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: "shared"}},
		{NodeSet: "b", NumberOfNodes: 1, BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: "shared"}},
	}
	workers := []v1alpha1.WorkerStatus{
		newWorkerStatus("a", "shared", "a-0", "id-a0", workerPhaseReady),
		newWorkerStatus("a", "shared", "a-1", "id-a1", workerPhaseReady),
	}
	r, co := workerCountsHarness("r10-nodesets", requests, workers)
	persistWorkerSummary(r, co, workers)
	assertWorkerCounts(co, 2, 1, 1)
})
