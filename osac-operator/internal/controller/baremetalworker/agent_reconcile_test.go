/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalworker

import (
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// reconcileAgentStage exercises the production observation projection and Agent
// action stages against a supplied worker slice and Agent snapshot. Standalone
// Agent tests arrange an observation instead of requiring a second production
// entry point.
func reconcileAgentStage(
	r *Reconciler, co *v1alpha1.ClusterOrder, workers []v1alpha1.WorkerStatus, agents *unstructured.UnstructuredList,
) ([]v1alpha1.WorkerStatus, ctrl.Result, error) {
	o := indexWorkerBMIs(nil)
	o.agents = agents
	projected, err := projectAgentWorkerPhases(co, workers, agents)
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	projectReadySince(projected, time.Now())
	r.observeAgentReadiness(context.Background(), co, workers, projected)
	return r.reconcileObservedAgents(context.Background(), co, projected, o)
}

func agentPhaseFixture(worker string, installed bool) *unstructured.Unstructured {
	a := &unstructured.Unstructured{Object: map[string]interface{}{}}
	a.SetGroupVersionKind(agentGVK)
	a.SetName("agent")
	a.SetNamespace("osac")
	a.SetLabels(map[string]string{workerNameLabel: worker, clusterOrderLabel: "order"})
	state := "installing"
	if installed {
		state = "installed"
	}
	_ = unstructured.SetNestedField(a.Object, state, "status", "debugInfo", "state")
	return a
}

var _ = Describe("Agent convergence", func() {
	tests := []struct {
		name, phase, kind, id string
		agent, installed      bool
		want                  string
	}{
		{"waiting to binding", workerPhaseWaitingForAgent, workerKindBMI, "id", true, false, workerPhaseBinding},
		{"waiting to ready repairs interrupted status", workerPhaseWaitingForAgent, workerKindBMI, "id", true, true, workerPhaseReady},
		{"binding to ready", workerPhaseBinding, workerKindBMI, "id", true, true, workerPhaseReady},
		{"ready to binding", workerPhaseReady, workerKindBMI, "id", true, false, workerPhaseBinding},
		{"ready to waiting", workerPhaseReady, workerKindBMI, "id", false, false, workerPhaseWaitingForAgent},
		{"binding to waiting", workerPhaseBinding, workerKindBMI, "id", false, false, workerPhaseWaitingForAgent},
		{"failed protected", workerPhaseFailed, workerKindBMI, "id", true, true, workerPhaseFailed},
		{"unbinding protected", workerPhaseUnbinding, workerKindBMI, "id", true, true, workerPhaseUnbinding},
		{"deleting protected", workerPhaseDeleting, workerKindBMI, "id", true, true, workerPhaseDeleting},
		{"reservation protected", workerPhaseProvisioning, workerKindBMI, "", true, true, workerPhaseProvisioning},
		{"non BMI protected", workerPhaseBinding, "Other", "id", true, true, workerPhaseBinding},
	}
	for _, tt := range tests {
		It(tt.name, func() {
			co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac", CreationTimestamp: metav1.Now()}}
			readySince := metav1.NewTime(time.Unix(100, 0))
			workers := []v1alpha1.WorkerStatus{{Name: "worker", Kind: tt.kind, Phase: tt.phase, BareMetalInstance: v1alpha1.BareMetalInstanceReference{Name: "bmi", ID: tt.id}, AttemptCount: 2, LastFailureReason: "previous", ReadySince: &readySince}}
			before := workers[0]
			s := runtime.NewScheme()
			c := clientfake.NewClientBuilder().WithScheme(s)
			if tt.agent {
				c = c.WithObjects(agentPhaseFixture("worker", tt.installed))
			}
			r := &Reconciler{Client: c.Build(), recorder: events.NewFakeRecorder(10), macResolver: func(context.Context, string) []string { return nil }}
			agents, err := r.listAgents(context.Background(), co)
			Expect(err).NotTo(HaveOccurred())
			got, result, err := reconcileAgentStage(r, co, workers, agents)
			Expect(err).NotTo(HaveOccurred())
			Expect(got[0].Phase).To(Equal(tt.want), "phase = %s, want %s", got[0].Phase, tt.want)
			got[0].Phase = before.Phase
			if tt.want != workerPhaseReady {
				// A demotion clears the healthy interval; only a still-Ready worker
				// keeps the ReadySince captured before the projection.
				got[0].ReadySince = nil
				before.ReadySince = nil
			}
			Expect(got[0]).To(Equal(before), "observation changed worker identity/history: %+v", got[0])
			Expect(tt.want == workerPhaseWaitingForAgent && result.RequeueAfter != agentRequeueInterval).To(BeFalse(), "requeue = %v, want %v", result.RequeueAfter, agentRequeueInterval)
		})
	}
})

var _ = Describe("Agent binding refuses existing assignments", func() {
	for _, assignment := range []string{"cluster", "namespace", "worker", "label"} {
		It(assignment, func() {
			a := agentPhaseFixture("", false)
			labels := a.GetLabels()
			delete(labels, workerNameLabel)
			if assignment == "worker" {
				labels[workerNameLabel] = "other-worker"
			}
			if assignment == "label" {
				labels[clusterOrderLabel] = "other-cluster"
			}
			a.SetLabels(labels)
			if assignment == "cluster" || assignment == "namespace" {
				name, namespace := "order", "osac"
				if assignment == "cluster" {
					name = "other-cluster"
				} else {
					namespace = "other-namespace"
				}
				_ = unstructured.SetNestedMap(a.Object, map[string]interface{}{"name": name, "namespace": namespace}, "spec", "clusterDeploymentName")
			}
			c := clientfake.NewClientBuilder().WithObjects(a).Build()
			r := &Reconciler{Client: c, apiReader: c}
			co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac"}}
			w := newWorkerStatus("standard", "standard", "worker", "id", workerPhaseWaitingForAgent)
			// The supplied snapshot is stale; rejection must use the authoritative read.
			stale := agentPhaseFixture("", false)
			before := a.DeepCopy()
			if err := r.bindAgent(context.Background(), co, stale, &w); err == nil {
				Fail("bound an Agent assigned elsewhere")
			}
			got := agentPhaseFixture("", false)
			Expect(c.Get(context.Background(), client.ObjectKeyFromObject(a), got)).To(Succeed())
			Expect(before).To(Equal(got), "modified an Agent assigned elsewhere")
		})
	}
})

type failingAgentPatchClient struct {
	client.Client
	patches  int
	conflict bool
	succeed  bool
}

func (c *failingAgentPatchClient) Patch(ctx context.Context, obj client.Object, p client.Patch, opts ...client.PatchOption) error {
	c.patches++
	if c.succeed {
		return c.Client.Patch(ctx, obj, p, opts...)
	}
	if c.conflict {
		return apierrors.NewConflict(schema.GroupResource{Group: agentGVK.Group, Resource: "agents"}, obj.GetName(), errors.New("test conflict"))
	}
	return errors.New("test patch failure")
}

var _ = It("protects non-BMI workers and reservations from Agent matching and timeouts", func() {
	ctx := context.Background()
	r := &Reconciler{recorder: events.NewFakeRecorder(10)}
	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac", CreationTimestamp: metav1.NewTime(time.Unix(100, 0))}}
	for _, kind := range []string{"Other", workerKindBMI} {
		By(kind)
		func() {
			id := "id"
			if kind == workerKindBMI {
				id = ""
			}
			w := newWorkerStatus("standard", "standard", "worker", id, workerPhaseWaitingForAgent)
			w.Kind = kind
			co.Status.Workers = []v1alpha1.WorkerStatus{w}
			got := r.checkAgentRegistrationTimeout(ctx, co, []v1alpha1.WorkerStatus{w}, time.Now())
			Expect(got[0]).To(Equal(w), "timeout changed a protected worker")
			a := agentPhaseFixture("", false)
			_ = unstructured.SetNestedSlice(a.Object, []interface{}{map[string]interface{}{"macAddress": "aa"}}, "status", "inventory", "interfaces")
			associations := matchUnboundAgents(ctx, co, []unstructured.Unstructured{*a}, []v1alpha1.WorkerStatus{w},
				func(context.Context, string) []string { return []string{"aa"} })
			if association, ok := associations[w.Name]; ok {
				Fail(fmt.Sprintf("associated a protected worker: %+v", association))
			}
		}()
	}
})

var _ = Describe("Agent binding failure handling", func() {
	for _, conflict := range []bool{false, true} {
		It(map[bool]string{false: "patch failure", true: "exhausted conflicts"}[conflict], func() {
			a := agentPhaseFixture("", false)
			_ = unstructured.SetNestedSlice(a.Object, []interface{}{map[string]interface{}{"macAddress": "aa:bb:cc:dd:ee:ff"}}, "status", "inventory", "interfaces")
			c := &failingAgentPatchClient{Client: clientfake.NewClientBuilder().WithObjects(a).Build(), conflict: conflict}
			recorder := events.NewFakeRecorder(10)
			r := &Reconciler{Client: c, recorder: recorder, macResolver: func(context.Context, string) []string { return []string{"aa:bb:cc:dd:ee:ff"} }}
			co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac", CreationTimestamp: metav1.Now()}}
			w := newWorkerStatus("standard", "standard", "worker", "id", workerPhaseWaitingForAgent)
			got, res, err := reconcileAgentStage(r, co, []v1alpha1.WorkerStatus{w}, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*a}})
			Expect(err).To(HaveOccurred(), "binding failure was hidden")
			Expect(got).To(BeEmpty(), "failed bind returned stale worker state: %+v, %+v", got, res)
			Expect(res.IsZero()).To(BeTrue(), "failed bind returned stale worker state: %+v, %+v", got, res)
			Expect(c.patches).To(Equal(1), "bind patches=%d, want one", c.patches)
			Expect(recorder.Events).To(BeEmpty(), "failed bind emitted %d events", len(recorder.Events))
		})
	}
})

var _ = It("restarts Agent reconciliation after an optimistic binding conflict", func() {
	ctx := context.Background()
	a := agentPhaseFixture("", false)
	a.SetUID("agent-v1")
	_ = unstructured.SetNestedSlice(a.Object, []interface{}{map[string]interface{}{"macAddress": "aa"}}, "status", "inventory", "interfaces")
	base := clientfake.NewClientBuilder().WithObjects(a).Build()
	patchClient := &failingAgentPatchClient{Client: base, conflict: true}
	r := &Reconciler{Client: patchClient, apiReader: base, recorder: events.NewFakeRecorder(10), macResolver: func(context.Context, string) []string { return []string{"aa"} }}
	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac"}}
	w := newWorkerStatus("standard", "standard", "worker", "id", workerPhaseWaitingForAgent)
	observed := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*a}}

	workers, result, err := reconcileAgentStage(r, co, []v1alpha1.WorkerStatus{w}, observed)
	Expect(apierrors.IsConflict(err)).To(BeTrue(), "first invocation: workers=%+v result=%+v err=%v", workers, result, err)
	Expect(result.IsZero()).To(BeTrue(), "first invocation: workers=%+v result=%+v err=%v", workers, result, err)
	Expect(workers).To(BeNil(), "first invocation: workers=%+v result=%+v err=%v", workers, result, err)
	Expect(patchClient.patches).To(Equal(1), "agent patches=%d, want 1", patchClient.patches)
	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(agentGVK)
	Expect(base.Get(ctx, client.ObjectKeyFromObject(a), current)).To(Succeed())
	Expect(current.GetLabels()[workerNameLabel]).To(Equal(""), "conflicted invocation took over the Agent")

	patchClient.conflict = false
	patchClient.succeed = true
	workers, result, err = reconcileAgentStage(r, co, []v1alpha1.WorkerStatus{w}, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*current}})
	Expect(err).NotTo(HaveOccurred(), "fresh invocation: workers=%+v result=%+v err=%v", workers, result, err)
	Expect(result.IsZero()).To(BeTrue(), "fresh invocation: workers=%+v result=%+v err=%v", workers, result, err)
	Expect(workers).To(HaveLen(1), "fresh invocation: workers=%+v result=%+v err=%v", workers, result, err)
	Expect(workers[0].Phase).To(Equal(workerPhaseBinding), "fresh invocation: workers=%+v result=%+v err=%v", workers, result, err)
	Expect(patchClient.patches).To(Equal(2), "agent patches=%d after restart, want 2", patchClient.patches)
})

var _ = It("rejects ambiguous MAC associations in both directions", func() {
	ctx := context.Background()
	makeAgent := func(name, mac, assigned string) *unstructured.Unstructured {
		agent := agentPhaseFixture(assigned, false)
		agent.SetName(name)
		agent.SetUID(types.UID(name + "-uid"))
		_ = unstructured.SetNestedSlice(agent.Object, []interface{}{
			map[string]interface{}{"macAddress": mac},
		}, "status", "inventory", "interfaces")
		return agent
	}
	worker := func(name, bmiID string) v1alpha1.WorkerStatus {
		return newWorkerStatus("standard", "standard", name, bmiID, workerPhaseWaitingForAgent)
	}
	for _, tc := range []struct {
		name    string
		agents  []*unstructured.Unstructured
		workers []v1alpha1.WorkerStatus
		macs    map[string][]string
	}{
		{
			name:    "one Agent matches several workers",
			agents:  []*unstructured.Unstructured{makeAgent("agent-0", "aa", "")},
			workers: []v1alpha1.WorkerStatus{worker("w-0", "bmi-0"), worker("w-1", "bmi-1")},
			macs:    map[string][]string{"bmi-0": {"aa"}, "bmi-1": {"aa"}},
		},
		{
			name:    "several Agents match one worker",
			agents:  []*unstructured.Unstructured{makeAgent("agent-0", "aa", ""), makeAgent("agent-1", "aa", "")},
			workers: []v1alpha1.WorkerStatus{worker("w-0", "bmi-0")},
			macs:    map[string][]string{"bmi-0": {"aa"}},
		},
		{
			name:    "assigned Agent is never a MAC fallback",
			agents:  []*unstructured.Unstructured{makeAgent("agent-0", "aa", "other-worker")},
			workers: []v1alpha1.WorkerStatus{worker("w-0", "bmi-0")},
			macs:    map[string][]string{"bmi-0": {"aa"}},
		},
	} {
		By(tc.name)
		func() {
			objects := make([]client.Object, 0, len(tc.agents))
			for _, agent := range tc.agents {
				objects = append(objects, agent)
			}
			c := clientfake.NewClientBuilder().WithObjects(objects...).Build()
			r := &Reconciler{
				Client: c, apiReader: c, recorder: events.NewFakeRecorder(10),
				macResolver: func(_ context.Context, id string) []string { return tc.macs[id] },
			}
			co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac"}}
			observed := &unstructured.UnstructuredList{}
			for _, agent := range tc.agents {
				observed.Items = append(observed.Items, *agent)
			}
			workers := append([]v1alpha1.WorkerStatus(nil), tc.workers...)

			bound, err := r.matchAndBindAgents(ctx, co, observed, workers, r.macResolver)
			Expect(err).ToNot(HaveOccurred(), "unexpected error: %v", err)
			Expect(bound).To(BeZero(), "ambiguous MAC evidence bound %d workers, want none", bound)
			for _, w := range workers {
				Expect(w.Phase).To(Equal(workerPhaseWaitingForAgent), "worker %s advanced to %s under ambiguous evidence", w.Name, w.Phase)
			}
			for _, agent := range tc.agents {
				got := &unstructured.Unstructured{}
				got.SetGroupVersionKind(agentGVK)
				Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), got)).To(Succeed())
				if assigned := got.GetLabels()[workerNameLabel]; assigned != agent.GetLabels()[workerNameLabel] {
					Fail(fmt.Sprintf("Agent %s was reassigned to %q", agent.GetName(), assigned))
				}
			}
		}()
	}
})

var _ = It("recovers Agent binding after an interrupted status write", func() {
	ctx := context.Background()
	a := agentPhaseFixture("", false)
	_ = unstructured.SetNestedSlice(a.Object, []interface{}{map[string]interface{}{"macAddress": "aa"}}, "status", "inventory", "interfaces")
	c := clientfake.NewClientBuilder().WithObjects(a).Build()
	recorder := events.NewFakeRecorder(10)
	r := &Reconciler{Client: c, recorder: recorder, macResolver: func(context.Context, string) []string { return []string{"aa"} }}
	w := newWorkerStatus("standard", "standard", "worker", "id", workerPhaseWaitingForAgent)
	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac"}, Status: v1alpha1.ClusterOrderStatus{Workers: []v1alpha1.WorkerStatus{w}}}
	histogram := workerCorrelationDuration.WithLabelValues(tenantOf(co), workerTypeBareMetal, w.InstanceType)
	before := &dto.Metric{}
	Expect(histogram.(interface{ Write(*dto.Metric) error }).Write(before)).To(Succeed())
	agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*a}}
	bound, res, err := reconcileAgentStage(r, co, co.Status.Workers, agents)
	Expect(err).NotTo(HaveOccurred(), "binding: %+v %+v %v", bound, res, err)
	Expect(bound[0].Phase).To(Equal(workerPhaseBinding), "binding: %+v %+v %v", bound, res, err)
	Expect(res.IsZero()).To(BeTrue(), "binding: %+v %+v %v", bound, res, err)
	// Simulate a crash: discard bound worker status, retaining only the Agent patch.
	Expect(c.Get(ctx, client.ObjectKeyFromObject(a), a)).To(Succeed())
	agents.Items = []unstructured.Unstructured{*a}
	recovered, res, err := reconcileAgentStage(r, co, co.Status.Workers, agents)
	Expect(err).NotTo(HaveOccurred(), "status repair: %+v %+v %v", recovered, res, err)
	Expect(recovered[0].Phase).To(Equal(workerPhaseBinding), "status repair: %+v %+v %v", recovered, res, err)
	Expect(res.IsZero()).To(BeTrue(), "status repair: %+v %+v %v", recovered, res, err)
	Expect(recovered[0].BareMetalInstance).To(Equal(w.BareMetalInstance), "status repair changed BMI identity")
	after := &dto.Metric{}
	Expect(histogram.(interface{ Write(*dto.Metric) error }).Write(after)).To(Succeed())
	if delta := after.GetHistogram().GetSampleCount() - before.GetHistogram().GetSampleCount(); delta != 1 {
		Fail(fmt.Sprintf("correlation observations = %d, want 1", delta))
	}
	Expect(recorder.Events).To(HaveLen(1), "binding events = %d, want 1", len(recorder.Events))
})

var _ = It("starts a new worker's timeout independently of the parent order's age", func() {
	oldOrder := metav1.NewTime(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	recent := metav1.NewTime(time.Now().Add(-time.Minute).Truncate(time.Second))
	w := newWorkerStatus("standard", "standard", "worker", "id", workerPhaseWaitingForAgent)
	w.AttemptStartedAt = &recent
	co := &v1alpha1.ClusterOrder{
		ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac", CreationTimestamp: oldOrder},
		Status:     v1alpha1.ClusterOrderStatus{Workers: []v1alpha1.WorkerStatus{w}},
	}
	r := &Reconciler{recorder: events.NewFakeRecorder(10), macResolver: func(context.Context, string) []string { return nil }}
	got, res, err := reconcileAgentStage(r, co, co.Status.Workers, &unstructured.UnstructuredList{})
	Expect(err).NotTo(HaveOccurred())
	Expect(got[0].Phase).To(Equal(workerPhaseWaitingForAgent), "new worker on old order inherited parent age: %+v result=%+v", got[0], res)
	Expect(res.RequeueAfter).To(Equal(agentRequeueInterval), "new worker on old order inherited parent age: %+v result=%+v", got[0], res)
})

// An old failure timestamp is not reused
// as the next attempt's registration origin, so retry backoff is not counted
// against the fresh attempt.
var _ = It("excludes retry backoff from the attempt timeout", func() {
	oldFailure := metav1.NewTime(time.Now().Add(-2 * time.Hour).Truncate(time.Second))
	recent := metav1.NewTime(time.Now().Add(-time.Minute).Truncate(time.Second))
	w := newWorkerStatus("standard", "standard", "worker", "id", workerPhaseWaitingForAgent)
	w.LastFailureTime = &oldFailure
	w.LastFailureReason = eventReasonAgentRegistrationTimeout
	w.AttemptStartedAt = &recent
	co := &v1alpha1.ClusterOrder{
		ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac"},
		Status:     v1alpha1.ClusterOrderStatus{Workers: []v1alpha1.WorkerStatus{w}},
	}
	r := &Reconciler{recorder: events.NewFakeRecorder(10), macResolver: func(context.Context, string) []string { return nil }}
	got, res, err := reconcileAgentStage(r, co, co.Status.Workers, &unstructured.UnstructuredList{})
	Expect(err).NotTo(HaveOccurred())
	Expect(got[0].Phase).To(Equal(workerPhaseWaitingForAgent), "retry attempt inherited backoff failure age: %+v result=%+v", got[0], res)
	Expect(res.RequeueAfter).To(Equal(agentRequeueInterval), "retry attempt inherited backoff failure age: %+v result=%+v", got[0], res)
})

// A fixed policy clock makes exact, just-before and
// just-after boundaries are evaluated without sleeps.
var _ = It("applies inclusive timeout, retry and healthy-reset clock boundaries", func() {
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	origin := metav1.NewTime(base)
	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac"}}
	timeoutWorker := func() v1alpha1.WorkerStatus {
		w := newWorkerStatus("standard", "standard", "worker", "id", workerPhaseWaitingForAgent)
		w.AttemptStartedAt = &origin
		return w
	}
	for _, tt := range []struct {
		name string
		now  time.Time
		want string
	}{
		{"just before timeout", base.Add(agentRegistrationTimeout - time.Second), workerPhaseWaitingForAgent},
		{"exact timeout", base.Add(agentRegistrationTimeout), workerPhaseFailed},
		{"just after timeout", base.Add(agentRegistrationTimeout + time.Second), workerPhaseFailed},
	} {
		By(tt.name)
		func() {
			r := &Reconciler{recorder: events.NewFakeRecorder(10)}
			got := r.checkAgentRegistrationTimeout(context.Background(), co, []v1alpha1.WorkerStatus{timeoutWorker()}, tt.now)
			Expect(got[0].Phase).To(Equal(tt.want), "phase=%s want %s", got[0].Phase, tt.want)
		}()
	}

	due := metav1.NewTime(base.Add(time.Minute))
	retry := v1alpha1.WorkerStatus{Phase: workerPhaseFailed, NextRetryTime: &due}
	Expect(isRetryDue(retry, base)).To(BeFalse(), "retry due boundary is not inclusive of the deadline")
	Expect(isRetryDue(retry, due.Time)).To(BeTrue(), "retry due boundary is not inclusive of the deadline")
	Expect(isRetryDue(retry, due.Time.Add(time.Second))).To(BeTrue(), "retry due boundary is not inclusive of the deadline")
	Expect(isRetryDue(v1alpha1.WorkerStatus{Phase: workerPhaseFailed}, base)).To(BeTrue(), "nil retry deadline must be due")

	ready := newWorkerStatus("standard", "standard", "ready", "ready-id", workerPhaseReady)
	ready.AttemptCount = 1
	ready.ReadySince = &origin
	for _, tt := range []struct {
		name string
		now  time.Time
		want int32
	}{
		{"just before healthy threshold", base.Add(minHealthyDuration - time.Second), 1},
		{"exact healthy threshold", base.Add(minHealthyDuration), 0},
		{"just after healthy threshold", base.Add(minHealthyDuration + time.Second), 0},
	} {
		By(tt.name)
		func() {
			workers := []v1alpha1.WorkerStatus{ready}
			resetHealthyWorkers(ctrllog.FromContext(context.Background()), co, workers, tt.now)
			Expect(workers[0].AttemptCount).To(Equal(tt.want), "attemptCount=%d want %d", workers[0].AttemptCount, tt.want)
		}()
	}
})

var _ = It("records Agent timeout and readiness observations once", func() {
	recorder := events.NewFakeRecorder(10)
	r := &Reconciler{recorder: recorder, macResolver: func(context.Context, string) []string { return nil }}
	fixed := metav1.NewTime(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	w := newWorkerStatus("standard", "standard", "worker", "id", workerPhaseWaitingForAgent)
	w.AttemptStartedAt = &fixed
	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac", CreationTimestamp: fixed}, Status: v1alpha1.ClusterOrderStatus{Workers: []v1alpha1.WorkerStatus{w}}}
	failures := workerProvisioningFailures.WithLabelValues(tenantOf(co), workerTypeBareMetal, w.InstanceType)
	beforeFailures := testutil.ToFloat64(failures)
	got, res, err := reconcileAgentStage(r, co, co.Status.Workers, &unstructured.UnstructuredList{})
	Expect(err).NotTo(HaveOccurred())
	Expect(got[0].Phase).To(Equal(workerPhaseFailed), "incorrect timeout: %+v, %+v", got, res)
	Expect(got[0].LastFailureReason).To(Equal(eventReasonAgentRegistrationTimeout), "incorrect timeout: %+v, %+v", got, res)
	Expect(res.IsZero()).To(BeTrue(), "incorrect timeout: %+v, %+v", got, res)
	co.Status.Workers = got
	got, _, err = reconcileAgentStage(r, co, got, &unstructured.UnstructuredList{})
	Expect(err).NotTo(HaveOccurred())
	Expect(got[0].Phase).To(Equal(workerPhaseFailed), "repeated observation resurrected failed worker")
	Expect(recorder.Events).To(HaveLen(1), "timeout events = %d, want 1", len(recorder.Events))
	if delta := testutil.ToFloat64(failures) - beforeFailures; delta != 1 {
		Fail(fmt.Sprintf("failure metric delta = %v, want 1", delta))
	}
	// A recent attempt origin must not time out even with stale failure history:
	// the failure timestamp is not the registration clock.
	recent := metav1.NewTime(time.Now().Add(-time.Minute).Truncate(time.Second))
	w.AttemptStartedAt = &recent
	w.LastFailureTime = &fixed
	co.Status.Workers = []v1alpha1.WorkerStatus{w}
	got, res, err = reconcileAgentStage(r, co, co.Status.Workers, &unstructured.UnstructuredList{})
	Expect(err).NotTo(HaveOccurred(), "recent retry timed out: %+v, %+v, %v", got, res, err)
	Expect(got[0].Phase).To(Equal(workerPhaseWaitingForAgent), "recent retry timed out: %+v, %+v, %v", got, res, err)
	Expect(res.RequeueAfter).To(Equal(agentRequeueInterval), "recent retry timed out: %+v, %+v, %v", got, res, err)
	// Newly installed Binding workers emit readiness once; ReadySince is initialized.
	w.Phase = workerPhaseBinding
	beforeMetric := &dto.Metric{}
	histogram := workerProvisioningDuration.WithLabelValues(tenantOf(co), workerTypeBareMetal, w.InstanceType)
	Expect(histogram.(interface{ Write(*dto.Metric) error }).Write(beforeMetric)).To(Succeed())
	agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*agentPhaseFixture(w.Name, true)}}
	got, _, err = reconcileAgentStage(r, co, []v1alpha1.WorkerStatus{w}, agents)
	Expect(err).NotTo(HaveOccurred(), "missing readiness: %+v, %v", got, err)
	Expect(got[0].ReadySince).NotTo(BeNil(), "missing readiness: %+v, %v", got, err)
	since := got[0].ReadySince.DeepCopy()
	got, _, err = reconcileAgentStage(r, co, got, agents)
	Expect(err).NotTo(HaveOccurred(), "duplicate readiness or timestamp reset: %+v, %v, events=%d", got, err, len(recorder.Events))
	Expect(got[0].ReadySince.Equal(since)).To(BeTrue(), "duplicate readiness or timestamp reset: %+v, %v, events=%d", got, err, len(recorder.Events))
	Expect(recorder.Events).To(HaveLen(2), "duplicate readiness or timestamp reset: %+v, %v, events=%d", got, err, len(recorder.Events))
	afterMetric := &dto.Metric{}
	Expect(histogram.(interface{ Write(*dto.Metric) error }).Write(afterMetric)).To(Succeed())
	if delta := afterMetric.GetHistogram().GetSampleCount() - beforeMetric.GetHistogram().GetSampleCount(); delta != 1 {
		Fail(fmt.Sprintf("readiness observations = %d, want 1", delta))
	}
})

var _ = It("observes Agents before selecting slots and classifying stale ignition", func() {
	_, _, co := nodeSetHarness("order", nodeRequest("standard", 1))
	co.Annotations = map[string]string{infraEnvUIDAnnotation: "old"}
	co.Status.Workers = []v1alpha1.WorkerStatus{
		newWorkerStatus("standard", "standard", "installed", "id-1", workerPhaseWaitingForAgent),
		newWorkerStatus("standard", "standard", "missing", "id-2", workerPhaseReady),
	}
	agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*agentPhaseFixture("installed", true)}}
	for i := range agents.Items {
		agents.Items[i].SetNamespace(co.Namespace)
	}
	projected, err := projectAgentWorkerPhases(co, co.Status.Workers, agents)
	Expect(err).NotTo(HaveOccurred())
	co.Status.Workers = projected
	plan := planWorkerSlots(co)
	Expect(plan.selected).To(HaveLen(1), "incorrect early retention: %+v", plan)
	Expect(plan.selected[0].Name).To(Equal("installed"), "incorrect early retention: %+v", plan)
	co.Status.Workers = classifyStaleIgnition(co, "new")
	Expect(co.Status.Workers[0].Phase).To(Equal(workerPhaseReady), "incorrect stale ignition classification: %+v", co.Status.Workers)
	Expect(co.Status.Workers[1].Phase).To(Equal(workerPhaseFailed), "incorrect stale ignition classification: %+v", co.Status.Workers)
})
