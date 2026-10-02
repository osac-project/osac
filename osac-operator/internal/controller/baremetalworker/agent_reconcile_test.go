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
	"k8s.io/client-go/tools/events"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

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
			got, result, err := r.reconcileAgent(context.Background(), co, workers, agents)
			if err != nil {
				t.Fatal(err)
			}
			if got[0].Phase != tt.want {
				t.Errorf("phase = %s, want %s", got[0].Phase, tt.want)
			}
			got[0].Phase = before.Phase
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
}

func (c *failingAgentPatchClient) Patch(ctx context.Context, obj client.Object, p client.Patch, opts ...client.PatchOption) error {
	c.patches++
	if c.conflict {
		return apierrors.NewConflict(schema.GroupResource{Group: agentGVK.Group, Resource: "agents"}, obj.GetName(), errors.New("test conflict"))
	}
	return errors.New("test patch failure")
}

func TestAgentMatchingAndTimeoutProtectNonBMIAndReservations(t *testing.T) {
	ctx := context.Background()
	r := &Reconciler{recorder: events.NewFakeRecorder(10)}
	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(time.Unix(100, 0))}}
	for _, kind := range []string{"Other", workerKindBMI} {
		t.Run(kind, func(t *testing.T) {
			id := "id"
			if kind == workerKindBMI {
				id = ""
			}
			w := newWorkerStatus("standard", "standard", "worker", id, workerPhaseWaitingForAgent)
			w.Kind = kind
			co.Status.Workers = []v1alpha1.WorkerStatus{w}
			got := r.checkAgentRegistrationTimeout(ctx, co, []v1alpha1.WorkerStatus{w})
			if !reflect.DeepEqual(got[0], w) {
				t.Error("timeout changed a protected worker")
			}
			a := agentPhaseFixture("", false)
			_ = unstructured.SetNestedSlice(a.Object, []interface{}{map[string]interface{}{"macAddress": "aa"}}, "status", "inventory", "interfaces")
			match, ambiguous := matchAgentToBMI(ctx, a, []v1alpha1.WorkerStatus{w}, func(context.Context, string) []string { return []string{"aa"} })
			if match != "" || ambiguous {
				t.Error("matched a protected worker")
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
			got, res, err := r.reconcileAgent(context.Background(), co, []v1alpha1.WorkerStatus{w}, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*a}})
			if err != nil {
				t.Fatal(err)
			}
			if got[0].Phase != workerPhaseWaitingForAgent || res.RequeueAfter != agentRequeueInterval {
				t.Fatalf("failed bind advanced worker: %+v, %+v", got, res)
			}
			if c.patches == 0 {
				t.Fatal("did not attempt bind")
			}
			if len(recorder.Events) != 0 {
				t.Fatalf("failed bind emitted %d events", len(recorder.Events))
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
	bound, res, err := r.reconcileAgent(ctx, co, co.Status.Workers, agents)
	if err != nil || bound[0].Phase != workerPhaseBinding || !res.IsZero() {
		t.Fatalf("binding: %+v %+v %v", bound, res, err)
	}
	// Simulate a crash: discard bound worker status, retaining only the Agent patch.
	if err := c.Get(ctx, client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatal(err)
	}
	agents.Items = []unstructured.Unstructured{*a}
	recovered, res, err := r.reconcileAgent(ctx, co, co.Status.Workers, agents)
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

func TestWorkerPhaseStartTimeBoundaries(t *testing.T) {
	created := metav1.NewTime(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	failure := metav1.NewTime(created.Add(agentRegistrationTimeout))
	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: created}}
	r := &Reconciler{}
	w := newWorkerStatus("standard", "standard", "worker", "id", workerPhaseWaitingForAgent)
	co.Status.Workers = []v1alpha1.WorkerStatus{w}
	if got := r.workerPhaseStartTime(co, w.Name); !got.Equal(created.Time) {
		t.Fatalf("start = %v, want creation time", got)
	}
	co.Status.Workers[0].LastFailureTime = &failure
	if got := r.workerPhaseStartTime(co, w.Name); !got.Equal(failure.Time) {
		t.Fatalf("start = %v, want failure time", got)
	}
	co.Status.Workers[0].Phase = workerPhaseBinding
	if got := r.workerPhaseStartTime(co, w.Name); !got.IsZero() {
		t.Fatalf("binding start = %v, want zero", got)
	}
	if got := r.workerPhaseStartTime(co, "new-worker"); !got.IsZero() {
		t.Fatalf("new worker start = %v, want zero", got)
	}
}

func TestAgentTimeoutAndReadinessObservations(t *testing.T) {
	ctx := context.Background()
	recorder := events.NewFakeRecorder(10)
	r := &Reconciler{recorder: recorder, macResolver: func(context.Context, string) []string { return nil }}
	fixed := metav1.NewTime(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	w := newWorkerStatus("standard", "standard", "worker", "id", workerPhaseWaitingForAgent)
	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", CreationTimestamp: fixed}, Status: v1alpha1.ClusterOrderStatus{Workers: []v1alpha1.WorkerStatus{w}}}
	failures := workerProvisioningFailures.WithLabelValues(tenantOf(co), workerTypeBareMetal, w.InstanceType)
	beforeFailures := testutil.ToFloat64(failures)
	got, res, err := r.reconcileAgent(ctx, co, co.Status.Workers, &unstructured.UnstructuredList{})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Phase != workerPhaseFailed || got[0].LastFailureReason != eventReasonAgentRegistrationTimeout || !res.IsZero() {
		t.Fatalf("incorrect timeout: %+v, %+v", got, res)
	}
	co.Status.Workers = got
	got, _, err = r.reconcileAgent(ctx, co, got, &unstructured.UnstructuredList{})
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
	// A recent failure time is the existing registration-clock approximation.
	w.LastFailureTime = new(metav1.Time)
	*w.LastFailureTime = metav1.Now()
	co.Status.Workers = []v1alpha1.WorkerStatus{w}
	got, res, err = r.reconcileAgent(ctx, co, co.Status.Workers, &unstructured.UnstructuredList{})
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
	got, _, err = r.reconcileAgent(ctx, co, []v1alpha1.WorkerStatus{w}, agents)
	if err != nil || got[0].ReadySince == nil {
		t.Fatalf("missing readiness: %+v, %v", got, err)
	}
	since := got[0].ReadySince.DeepCopy()
	got, _, err = r.reconcileAgent(ctx, co, got, agents)
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
	co.Status.Workers = projectAgentWorkerPhases(context.Background(), co.Status.Workers, agents, func(context.Context, string) []string { return nil })
	plan := planWorkerSlots(co)
	if len(plan.selected) != 1 || plan.selected[0].Name != "installed" {
		t.Fatalf("incorrect early retention: %+v", plan)
	}
	for _, change := range classifyStaleIgnition(co, "new") {
		if !applyWorkerChange(co, change) {
			t.Fatal("stale ignition classifier produced incompatible evidence")
		}
	}
	if co.Status.Workers[0].Phase != workerPhaseReady || co.Status.Workers[1].Phase != workerPhaseFailed {
		t.Fatalf("incorrect stale ignition classification: %+v", co.Status.Workers)
	}
}
