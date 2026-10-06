// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"reflect"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/events"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// Migrated coverage from rebuild_test.go drives the real combined observation
// instead of retaining a second production existence/phase pipeline for tests.
type absentProjectionClient struct{ FulfillmentClient }

func (absentProjectionClient) GetBareMetalInstance(context.Context, string) (*privatev1.BareMetalInstance, error) {
	return nil, status.Error(codes.NotFound, "confirmed missing")
}
func observeWorkerFixture(t *testing.T, workers []v1alpha1.WorkerStatus, agents *unstructured.UnstructuredList, exists func(string) bool) ([]v1alpha1.WorkerStatus, []string) {
	t.Helper()
	// Match the Agent fixture (namespace osac, cluster-order label "order").
	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac"}, Status: v1alpha1.ClusterOrderStatus{Workers: append([]v1alpha1.WorkerStatus(nil), workers...)}}
	var bmis []*privatev1.BareMetalInstance
	for _, w := range workers {
		if w.Kind == workerKindBMI && w.BareMetalInstance.ID != "" && exists(w.BareMetalInstance.ID) {
			bmis = append(bmis, ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID))
		}
	}
	observed := indexWorkerBMIs(bmis)
	observed.agents = agents
	r := &Reconciler{fulfillment: absentProjectionClient{}, recorder: events.NewFakeRecorder(10)}
	workers, err := r.observeExistingWorkers(context.Background(), co, "tenant", observed)
	if err != nil {
		t.Fatal(err)
	}
	var removed []string
	for _, before := range co.Status.Workers {
		if workerByName(workers, before.Name) == nil {
			removed = append(removed, before.Name)
		}
	}
	return workers, removed
}
func TestEstablishedLabelPrecedesMACFallback(t *testing.T) {
	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac"}}
	worker := newWorkerStatus("standard", "standard", "worker", "bmi-0", workerPhaseWaitingForAgent)
	resolver := func(context.Context, string) []string { return []string{"aa"} }

	// An established worker-name label wins even when the inventory MAC differs.
	labeled := agentPhaseFixture("worker", false)
	labeled.SetUID("uid-labeled")
	_ = unstructured.SetNestedSlice(labeled.Object, []interface{}{
		map[string]interface{}{"macAddress": "ff"},
	}, "status", "inventory", "interfaces")
	association := associateEstablishedAgent([]unstructured.Unstructured{*labeled}, co, "worker")
	if association.state != agentEstablished || association.agent.GetUID() != "uid-labeled" {
		t.Fatalf("established label not preferred: %+v", association)
	}

	// An unbound Agent is not an established binding; MAC correlation is only
	// initial-discovery evidence and must never override another worker's label.
	unbound := agentPhaseFixture("", false)
	unbound.SetUID("uid-unbound")
	_ = unstructured.SetNestedSlice(unbound.Object, []interface{}{
		map[string]interface{}{"macAddress": "aa"},
	}, "status", "inventory", "interfaces")
	if association := associateEstablishedAgent([]unstructured.Unstructured{*unbound}, co, "worker"); association.state != agentAbsent {
		t.Fatalf("MAC fallback authorized an established binding: %+v", association)
	}
	other := agentPhaseFixture("other-worker", false)
	other.SetUID("uid-other")
	_ = unstructured.SetNestedSlice(other.Object, []interface{}{
		map[string]interface{}{"macAddress": "aa"},
	}, "status", "inventory", "interfaces")
	associations := matchUnboundAgents(context.Background(), co, []unstructured.Unstructured{*other}, []v1alpha1.WorkerStatus{worker}, resolver)
	if association, ok := associations["worker"]; ok {
		t.Fatalf("used another worker's Agent as a MAC fallback: %+v", association)
	}
}
func TestWorkerPhaseMapping(t *testing.T) {
	for _, tt := range []struct {
		name, label, condition string
		installed              bool
		want                   string
	}{{"nil", "", "", false, workerPhaseWaitingForAgent}, {"bound debug installed", "worker", "", true, workerPhaseReady}, {"bound condition installed", "worker", "True", false, workerPhaseReady}, {"bound installing", "worker", "", false, workerPhaseBinding}, {"False overrides debug", "worker", "False", true, workerPhaseBinding}, {"Unknown overrides debug", "worker", "Unknown", true, workerPhaseBinding}, {"unbound installed", "", "True", true, workerPhaseWaitingForAgent}} {
		t.Run(tt.name, func(t *testing.T) {
			var a *unstructured.Unstructured
			if tt.name != "nil" {
				a = agentPhaseFixture(tt.label, tt.installed)
				if tt.condition != "" {
					_ = unstructured.SetNestedSlice(a.Object, []interface{}{map[string]interface{}{"type": "Installed", "status": tt.condition}}, "status", "conditions")
				}
			}
			if got := deriveWorkerPhase(a, "worker"); got != tt.want {
				t.Fatalf("phase=%s, want %s", got, tt.want)
			}
		})
	}
}
func TestCombinedObservationMigratedPhaseAndHistoryCases(t *testing.T) {
	for _, tt := range []struct {
		name, phase, condition    string
		present, agent, installed bool
		want                      string
	}{
		{"Ready without Agent", workerPhaseReady, "", true, false, false, workerPhaseWaitingForAgent},
		{"interrupted Ready status", workerPhaseWaitingForAgent, "", true, true, true, workerPhaseReady},
		{"Installed True", workerPhaseWaitingForAgent, "True", true, true, false, workerPhaseReady},
		{"bound not installed", workerPhaseWaitingForAgent, "", true, true, false, workerPhaseBinding},
		{"Installed False", workerPhaseWaitingForAgent, "False", true, true, true, workerPhaseBinding},
		{"Installed Unknown", workerPhaseWaitingForAgent, "Unknown", true, true, true, workerPhaseBinding},
		{"active confirmed missing", workerPhaseReady, "", false, false, false, ""},
		{"Unbinding protected", workerPhaseUnbinding, "True", true, true, true, workerPhaseUnbinding},
		{"Deleting protected", workerPhaseDeleting, "True", true, true, true, workerPhaseDeleting},
		{"Failed protected", workerPhaseFailed, "True", true, true, true, workerPhaseFailed},
		{"Ready clock preserved", workerPhaseReady, "True", true, true, true, workerPhaseReady},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stamp := metav1.NewTime(time.Unix(100, 0))
			w := newWorkerStatus("standard", "standard", "worker", "id", tt.phase)
			w.AttemptCount = 3
			w.LastFailureReason = "previous"
			w.LastFailureMessage = "history"
			w.LastFailureTime = &stamp
			if tt.phase == workerPhaseReady {
				w.ReadySince = &stamp
			}
			agents := &unstructured.UnstructuredList{}
			if tt.agent {
				a := agentPhaseFixture("worker", tt.installed)
				if tt.condition != "" {
					_ = unstructured.SetNestedSlice(a.Object, []interface{}{map[string]interface{}{"type": "Installed", "status": tt.condition}}, "status", "conditions")
				}
				agents.Items = append(agents.Items, *a)
			}
			got, removed := observeWorkerFixture(t, []v1alpha1.WorkerStatus{w}, agents, func(string) bool { return tt.present })
			if tt.want == "" {
				if len(got) != 0 || !reflect.DeepEqual(removed, []string{"worker"}) {
					t.Fatalf("absence result=%v removed=%v", got, removed)
				}
				return
			}
			if len(got) != 1 || got[0].Phase != tt.want || len(removed) != 0 {
				t.Fatalf("result=%v removed=%v", got, removed)
			}
			got[0].Phase = w.Phase
			// AttemptStartedAt is a separate attempt clock owned by R09 and covered by
			// TestR09LegacyAttemptBackfill; normalize it so the identity/history and
			// ReadySince assertions below stay focused.
			w.AttemptStartedAt = nil
			got[0].AttemptStartedAt = nil
			// ReadySince describes a continuous interval: it is cleared on any demotion
			// and only compared when the observed phase is still Ready.
			if tt.want != workerPhaseReady {
				if got[0].ReadySince != nil {
					t.Fatalf("demoted worker retained the healthy interval: %+v", got[0].ReadySince)
				}
				w.ReadySince = nil
			} else if w.ReadySince == nil {
				got[0].ReadySince = nil
			}
			if !reflect.DeepEqual(got[0], w) {
				t.Fatalf("lost identity/history/clock: %+v", got[0])
			}
		})
	}
}

// TestR09ContinuousHealthyInterval proves a Ready demotion clears ReadySince, so
// disjoint healthy intervals cannot accumulate into the healthy-reset threshold.
func TestR09ContinuousHealthyInterval(t *testing.T) {
	stale := metav1.NewTime(time.Now().Add(-2 * time.Hour).Truncate(time.Second))
	ready := newWorkerStatus("standard", "standard", "worker", "id", workerPhaseReady)
	ready.ReadySince = &stale
	got, _ := observeWorkerFixture(t, []v1alpha1.WorkerStatus{ready}, &unstructured.UnstructuredList{}, func(string) bool { return true })
	if len(got) != 1 || got[0].Phase != workerPhaseWaitingForAgent {
		t.Fatalf("expected a Ready demotion: %+v", got)
	}
	if got[0].ReadySince != nil {
		t.Fatalf("demotion retained the previous healthy interval: %+v", got[0].ReadySince)
	}
	// Re-entering Ready starts a new interval instead of inheriting the stale one.
	agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*agentPhaseFixture("worker", true)}}
	got, _ = observeWorkerFixture(t, []v1alpha1.WorkerStatus{got[0]}, agents, func(string) bool { return true })
	if len(got) != 1 || got[0].Phase != workerPhaseReady || got[0].ReadySince == nil {
		t.Fatalf("re-entry did not start a fresh healthy interval: %+v", got)
	}
	if !got[0].ReadySince.Time.After(stale.Time) {
		t.Fatalf("re-entry reused a disconnected interval: %+v", got[0].ReadySince)
	}
}

// TestR09LegacyAttemptBackfill proves the one-time legacy migration: a pre-existing
// attempt inherits the backing BMI's creation time when usable, otherwise a single
// observation-time origin. Neither is refreshed by later reconciles.
func TestR09LegacyAttemptBackfill(t *testing.T) {
	ctx := context.Background()
	r := &Reconciler{recorder: events.NewFakeRecorder(10)}
	created := time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)

	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac"}}
	bmi := ownedBMIFixture(co, "worker", "id")
	bmi.GetMetadata().SetCreationTimestamp(timestamppb.New(created))
	legacy := newWorkerStatus("standard", "standard", "worker", "id", workerPhaseWaitingForAgent)
	legacy.AttemptStartedAt = nil
	co.Status.Workers = []v1alpha1.WorkerStatus{legacy}
	observed := indexWorkerBMIs([]*privatev1.BareMetalInstance{bmi})
	observed.agents = &unstructured.UnstructuredList{}
	got, err := r.observeExistingWorkers(ctx, co, "tenant", observed)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].AttemptStartedAt == nil || !got[0].AttemptStartedAt.Time.Equal(created) {
		t.Fatalf("legacy worker did not inherit the BMI creation time: %+v", got[0].AttemptStartedAt)
	}

	// Persisted once: another observation with the field already set must keep it.
	co.Status.Workers = got
	observed = indexWorkerBMIs([]*privatev1.BareMetalInstance{bmi})
	observed.agents = &unstructured.UnstructuredList{}
	again, err := r.observeExistingWorkers(ctx, co, "tenant", observed)
	if err != nil {
		t.Fatal(err)
	}
	if again[0].AttemptStartedAt == nil || !again[0].AttemptStartedAt.Equal(got[0].AttemptStartedAt) {
		t.Fatal("backfill refreshed an already persisted attempt origin")
	}

	// No usable BMI clock: one observation-time origin is still durable.
	noclock := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac"}}
	noclockWorker := newWorkerStatus("standard", "standard", "worker", "id", workerPhaseWaitingForAgent)
	noclockWorker.AttemptStartedAt = nil
	noclock.Status.Workers = []v1alpha1.WorkerStatus{noclockWorker}
	fallbackObs := indexWorkerBMIs([]*privatev1.BareMetalInstance{ownedBMIFixture(noclock, "worker", "id")})
	fallbackObs.agents = &unstructured.UnstructuredList{}
	fallback, err := r.observeExistingWorkers(ctx, noclock, "tenant", fallbackObs)
	if err != nil {
		t.Fatal(err)
	}
	if fallback[0].AttemptStartedAt == nil {
		t.Fatal("missing-clock legacy worker was not given a one-time origin")
	}
}

func TestCombinedObservationMixedAndEmptyWorkers(t *testing.T) {
	workers := []v1alpha1.WorkerStatus{newWorkerStatus("standard", "standard", "waiting", "id-0", workerPhaseProvisioning), newWorkerStatus("standard", "standard", "installed", "id-1", workerPhaseProvisioning), newWorkerStatus("standard", "standard", "missing", "id-2", workerPhaseReady), {Name: "vm", Kind: "VirtualMachine", Phase: "Running"}}
	agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*agentPhaseFixture("installed", true)}}
	got, removed := observeWorkerFixture(t, workers, agents, func(id string) bool { return id != "id-2" })
	if len(got) != 3 || got[0].Phase != workerPhaseWaitingForAgent || got[1].Phase != workerPhaseReady || !reflect.DeepEqual(got[2], workers[3]) || !reflect.DeepEqual(removed, []string{"missing"}) {
		t.Fatalf("mixed workers=%v removed=%v", got, removed)
	}
	got, removed = observeWorkerFixture(t, nil, agents, func(string) bool { return true })
	if got != nil || len(removed) != 0 {
		t.Fatal("empty input changed")
	}
}
