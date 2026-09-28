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
	"reflect"

	"testing"
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

func TestAgentConvergence(t *testing.T) {
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
		t.Run(tt.name, func(t *testing.T) {
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
			if err != nil {
				t.Fatal(err)
			}
			got, result, err := reconcileAgentStage(r, co, workers, agents)
			if err != nil {
				t.Fatal(err)
			}
			if got[0].Phase != tt.want {
				t.Errorf("phase = %s, want %s", got[0].Phase, tt.want)
			}
			got[0].Phase = before.Phase
			if tt.want != workerPhaseReady {
				// A demotion clears the healthy interval; only a still-Ready worker
				// keeps the ReadySince captured before the projection.
				got[0].ReadySince = nil
				before.ReadySince = nil
			}
			if !reflect.DeepEqual(got[0], before) {
				t.Errorf("observation changed worker identity/history: %+v", got[0])
			}
			if tt.want == workerPhaseWaitingForAgent && result.RequeueAfter != agentRequeueInterval {
				t.Errorf("requeue = %v, want %v", result.RequeueAfter, agentRequeueInterval)
			}
		})
	}
}

func TestAgentBindingRefusesExistingAssignment(t *testing.T) {
	for _, assignment := range []string{"cluster", "namespace", "worker", "label"} {
		t.Run(assignment, func(t *testing.T) {
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
				t.Error("bound an Agent assigned elsewhere")
			}
			got := agentPhaseFixture("", false)
			if err := c.Get(context.Background(), client.ObjectKeyFromObject(a), got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, got) {
				t.Error("modified an Agent assigned elsewhere")
			}
		})
	}
}

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

func TestAgentMatchingAndTimeoutProtectNonBMIAndReservations(t *testing.T) {
	ctx := context.Background()
	r := &Reconciler{recorder: events.NewFakeRecorder(10)}
	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac", CreationTimestamp: metav1.NewTime(time.Unix(100, 0))}}
	for _, kind := range []string{"Other", workerKindBMI} {
		t.Run(kind, func(t *testing.T) {
			id := "id"
			if kind == workerKindBMI {
				id = ""
			}
			w := newWorkerStatus("standard", "standard", "worker", id, workerPhaseWaitingForAgent)
			w.Kind = kind
			co.Status.Workers = []v1alpha1.WorkerStatus{w}
			got := r.checkAgentRegistrationTimeout(ctx, co, []v1alpha1.WorkerStatus{w}, time.Now())
			if !reflect.DeepEqual(got[0], w) {
				t.Error("timeout changed a protected worker")
			}
			a := agentPhaseFixture("", false)
			_ = unstructured.SetNestedSlice(a.Object, []interface{}{map[string]interface{}{"macAddress": "aa"}}, "status", "inventory", "interfaces")
			associations := matchUnboundAgents(ctx, co, []unstructured.Unstructured{*a}, []v1alpha1.WorkerStatus{w},
				func(context.Context, string) []string { return []string{"aa"} })
			if association, ok := associations[w.Name]; ok {
				t.Errorf("associated a protected worker: %+v", association)
			}
		})
	}
}

func TestAgentReconcileBindingFailure(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "patch failure", true: "exhausted conflicts"}[conflict], func(t *testing.T) {
			a := agentPhaseFixture("", false)
			_ = unstructured.SetNestedSlice(a.Object, []interface{}{map[string]interface{}{"macAddress": "aa:bb:cc:dd:ee:ff"}}, "status", "inventory", "interfaces")
			c := &failingAgentPatchClient{Client: clientfake.NewClientBuilder().WithObjects(a).Build(), conflict: conflict}
			recorder := events.NewFakeRecorder(10)
			r := &Reconciler{Client: c, recorder: recorder, macResolver: func(context.Context, string) []string { return []string{"aa:bb:cc:dd:ee:ff"} }}
			co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac", CreationTimestamp: metav1.Now()}}
			w := newWorkerStatus("standard", "standard", "worker", "id", workerPhaseWaitingForAgent)
			got, res, err := reconcileAgentStage(r, co, []v1alpha1.WorkerStatus{w}, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*a}})
			if err == nil {
				t.Fatal("binding failure was hidden")
			}
			if len(got) != 0 || !res.IsZero() {
				t.Fatalf("failed bind returned stale worker state: %+v, %+v", got, res)
			}
			if c.patches != 1 {
				t.Fatalf("bind patches=%d, want one", c.patches)
			}
			if len(recorder.Events) != 0 {
				t.Fatalf("failed bind emitted %d events", len(recorder.Events))
			}
		})
	}
}

func TestR02AgentConflictRestarts(t *testing.T) {
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
	if !apierrors.IsConflict(err) || !result.IsZero() || workers != nil {
		t.Fatalf("first invocation: workers=%+v result=%+v err=%v", workers, result, err)
	}
	if patchClient.patches != 1 {
		t.Fatalf("agent patches=%d, want 1", patchClient.patches)
	}
	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(agentGVK)
	if err := base.Get(ctx, client.ObjectKeyFromObject(a), current); err != nil {
		t.Fatal(err)
	}
	if current.GetLabels()[workerNameLabel] != "" {
		t.Fatal("conflicted invocation took over the Agent")
	}

	patchClient.conflict = false
	patchClient.succeed = true
	workers, result, err = reconcileAgentStage(r, co, []v1alpha1.WorkerStatus{w}, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*current}})
	if err != nil || !result.IsZero() || len(workers) != 1 || workers[0].Phase != workerPhaseBinding {
		t.Fatalf("fresh invocation: workers=%+v result=%+v err=%v", workers, result, err)
	}
	if patchClient.patches != 2 {
		t.Fatalf("agent patches=%d after restart, want 2", patchClient.patches)
	}
}

func TestR06MACAmbiguityBothDirections(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
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
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if bound != 0 {
				t.Fatalf("ambiguous MAC evidence bound %d workers, want none", bound)
			}
			for _, w := range workers {
				if w.Phase != workerPhaseWaitingForAgent {
					t.Fatalf("worker %s advanced to %s under ambiguous evidence", w.Name, w.Phase)
				}
			}
			for _, agent := range tc.agents {
				got := &unstructured.Unstructured{}
				got.SetGroupVersionKind(agentGVK)
				if err := c.Get(ctx, client.ObjectKeyFromObject(agent), got); err != nil {
					t.Fatal(err)
				}
				if assigned := got.GetLabels()[workerNameLabel]; assigned != agent.GetLabels()[workerNameLabel] {
					t.Fatalf("Agent %s was reassigned to %q", agent.GetName(), assigned)
				}
			}
		})
	}
}

func TestAgentBindingCrashRecovery(t *testing.T) {
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
	if err := histogram.(interface{ Write(*dto.Metric) error }).Write(before); err != nil {
		t.Fatal(err)
	}
	agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*a}}
	bound, res, err := reconcileAgentStage(r, co, co.Status.Workers, agents)
	if err != nil || bound[0].Phase != workerPhaseBinding || !res.IsZero() {
		t.Fatalf("binding: %+v %+v %v", bound, res, err)
	}
	// Simulate a crash: discard bound worker status, retaining only the Agent patch.
	if err := c.Get(ctx, client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatal(err)
	}
	agents.Items = []unstructured.Unstructured{*a}
	recovered, res, err := reconcileAgentStage(r, co, co.Status.Workers, agents)
	if err != nil || recovered[0].Phase != workerPhaseBinding || !res.IsZero() {
		t.Fatalf("status repair: %+v %+v %v", recovered, res, err)
	}
	if recovered[0].BareMetalInstance != w.BareMetalInstance {
		t.Fatal("status repair changed BMI identity")
	}
	after := &dto.Metric{}
	if err := histogram.(interface{ Write(*dto.Metric) error }).Write(after); err != nil {
		t.Fatal(err)
	}
	if delta := after.GetHistogram().GetSampleCount() - before.GetHistogram().GetSampleCount(); delta != 1 {
		t.Fatalf("correlation observations = %d, want 1", delta)
	}
	if len(recorder.Events) != 1 {
		t.Fatalf("binding events = %d, want 1", len(recorder.Events))
	}
}

func TestR09NewWorkerOnOldOrder(t *testing.T) {
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
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Phase != workerPhaseWaitingForAgent || res.RequeueAfter != agentRequeueInterval {
		t.Fatalf("new worker on old order inherited parent age: %+v result=%+v", got[0], res)
	}
}

// TestR09RetryClockExcludesBackoff proves an old failure timestamp is not reused
// as the next attempt's registration origin, so retry backoff is not counted
// against the fresh attempt.
func TestR09RetryClockExcludesBackoff(t *testing.T) {
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
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Phase != workerPhaseWaitingForAgent || res.RequeueAfter != agentRequeueInterval {
		t.Fatalf("retry attempt inherited backoff failure age: %+v result=%+v", got[0], res)
	}
}

// TestR09PolicyClockBoundaries fixes the policy clock so exact, just-before and
// just-after boundaries are evaluated without sleeps.
func TestR09PolicyClockBoundaries(t *testing.T) {
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
		t.Run(tt.name, func(t *testing.T) {
			r := &Reconciler{recorder: events.NewFakeRecorder(10)}
			got := r.checkAgentRegistrationTimeout(context.Background(), co, []v1alpha1.WorkerStatus{timeoutWorker()}, tt.now)
			if got[0].Phase != tt.want {
				t.Fatalf("phase=%s want %s", got[0].Phase, tt.want)
			}
		})
	}

	due := metav1.NewTime(base.Add(time.Minute))
	retry := v1alpha1.WorkerStatus{Phase: workerPhaseFailed, NextRetryTime: &due}
	if isRetryDue(retry, base) || !isRetryDue(retry, due.Time) || !isRetryDue(retry, due.Time.Add(time.Second)) {
		t.Fatal("retry due boundary is not inclusive of the deadline")
	}
	if !isRetryDue(v1alpha1.WorkerStatus{Phase: workerPhaseFailed}, base) {
		t.Fatal("nil retry deadline must be due")
	}

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
		t.Run(tt.name, func(t *testing.T) {
			workers := []v1alpha1.WorkerStatus{ready}
			resetHealthyWorkers(ctrllog.FromContext(context.Background()), co, workers, tt.now)
			if workers[0].AttemptCount != tt.want {
				t.Fatalf("attemptCount=%d want %d", workers[0].AttemptCount, tt.want)
			}
		})
	}
}

func TestAgentTimeoutAndReadinessObservations(t *testing.T) {
	recorder := events.NewFakeRecorder(10)
	r := &Reconciler{recorder: recorder, macResolver: func(context.Context, string) []string { return nil }}
	fixed := metav1.NewTime(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	w := newWorkerStatus("standard", "standard", "worker", "id", workerPhaseWaitingForAgent)
	w.AttemptStartedAt = &fixed
	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac", CreationTimestamp: fixed}, Status: v1alpha1.ClusterOrderStatus{Workers: []v1alpha1.WorkerStatus{w}}}
	failures := workerProvisioningFailures.WithLabelValues(tenantOf(co), workerTypeBareMetal, w.InstanceType)
	beforeFailures := testutil.ToFloat64(failures)
	got, res, err := reconcileAgentStage(r, co, co.Status.Workers, &unstructured.UnstructuredList{})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Phase != workerPhaseFailed || got[0].LastFailureReason != eventReasonAgentRegistrationTimeout || !res.IsZero() {
		t.Fatalf("incorrect timeout: %+v, %+v", got, res)
	}
	co.Status.Workers = got
	got, _, err = reconcileAgentStage(r, co, got, &unstructured.UnstructuredList{})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Phase != workerPhaseFailed {
		t.Fatal("repeated observation resurrected failed worker")
	}
	if len(recorder.Events) != 1 {
		t.Fatalf("timeout events = %d, want 1", len(recorder.Events))
	}
	if delta := testutil.ToFloat64(failures) - beforeFailures; delta != 1 {
		t.Fatalf("failure metric delta = %v, want 1", delta)
	}
	// A recent attempt origin must not time out even with stale failure history:
	// the failure timestamp is not the registration clock.
	recent := metav1.NewTime(time.Now().Add(-time.Minute).Truncate(time.Second))
	w.AttemptStartedAt = &recent
	w.LastFailureTime = &fixed
	co.Status.Workers = []v1alpha1.WorkerStatus{w}
	got, res, err = reconcileAgentStage(r, co, co.Status.Workers, &unstructured.UnstructuredList{})
	if err != nil || got[0].Phase != workerPhaseWaitingForAgent || res.RequeueAfter != agentRequeueInterval {
		t.Fatalf("recent retry timed out: %+v, %+v, %v", got, res, err)
	}
	// Newly installed Binding workers emit readiness once; ReadySince is initialized.
	w.Phase = workerPhaseBinding
	beforeMetric := &dto.Metric{}
	histogram := workerProvisioningDuration.WithLabelValues(tenantOf(co), workerTypeBareMetal, w.InstanceType)
	if err := histogram.(interface{ Write(*dto.Metric) error }).Write(beforeMetric); err != nil {
		t.Fatal(err)
	}
	agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*agentPhaseFixture(w.Name, true)}}
	got, _, err = reconcileAgentStage(r, co, []v1alpha1.WorkerStatus{w}, agents)
	if err != nil || got[0].ReadySince == nil {
		t.Fatalf("missing readiness: %+v, %v", got, err)
	}
	since := got[0].ReadySince.DeepCopy()
	got, _, err = reconcileAgentStage(r, co, got, agents)
	if err != nil || !got[0].ReadySince.Equal(since) || len(recorder.Events) != 2 {
		t.Fatalf("duplicate readiness or timestamp reset: %+v, %v, events=%d", got, err, len(recorder.Events))
	}
	afterMetric := &dto.Metric{}
	if err := histogram.(interface{ Write(*dto.Metric) error }).Write(afterMetric); err != nil {
		t.Fatal(err)
	}
	if delta := afterMetric.GetHistogram().GetSampleCount() - beforeMetric.GetHistogram().GetSampleCount(); delta != 1 {
		t.Fatalf("readiness observations = %d, want 1", delta)
	}
}

func TestAgentObservationBeforeSlotSelectionAndStaleIgnition(t *testing.T) {
	_, _, co := nodeSetHarness(t, "order", nodeRequest("standard", 1))
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
	if err != nil {
		t.Fatal(err)
	}
	co.Status.Workers = projected
	plan := planWorkerSlots(co)
	if len(plan.selected) != 1 || plan.selected[0].Name != "installed" {
		t.Fatalf("incorrect early retention: %+v", plan)
	}
	co.Status.Workers = classifyStaleIgnition(co, "new")
	if co.Status.Workers[0].Phase != workerPhaseReady || co.Status.Workers[1].Phase != workerPhaseFailed {
		t.Fatalf("incorrect stale ignition classification: %+v", co.Status.Workers)
	}
}
