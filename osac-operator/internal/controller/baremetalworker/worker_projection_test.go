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
func observeWorkerFixture(t *testing.T, workers []v1alpha1.WorkerStatus, agents *unstructured.UnstructuredList, macs MACResolver, exists func(string) bool) ([]v1alpha1.WorkerStatus, []string) {
	t.Helper()
	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "fixture"}, Status: v1alpha1.ClusterOrderStatus{Workers: append([]v1alpha1.WorkerStatus(nil), workers...)}}
	var bmis []*privatev1.BareMetalInstance
	for _, w := range workers {
		if w.Kind == workerKindBMI && w.BareMetalInstance.ID != "" && exists(w.BareMetalInstance.ID) {
			bmis = append(bmis, ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID))
		}
	}
	observed := indexWorkerBMIs(bmis)
	observed.agents = agents
	r := &Reconciler{fulfillment: absentProjectionClient{}, macResolver: macs, recorder: events.NewFakeRecorder(10)}
	changes, err := r.observeExistingWorkers(context.Background(), co, "tenant", observed)
	if err != nil {
		t.Fatal(err)
	}
	var removed []string
	for _, change := range changes {
		if change.replacement == nil {
			removed = append(removed, change.observed.Name)
		}
		if !applyWorkerChange(co, change) {
			t.Fatal("fixture changes did not match original slot")
		}
	}
	return co.Status.Workers, removed
}
func TestFindAgentForWorkerNameAndNICFallback(t *testing.T) {
	for _, tt := range []struct {
		name, mac, label string
		want             bool
	}{{"matching MAC", "aa", "", true}, {"case insensitive", "AA", "", true}, {"name takes precedence", "different", "worker", true}, {"no match", "different", "", false}} {
		t.Run(tt.name, func(t *testing.T) {
			a := agentPhaseFixture(tt.label, false)
			_ = unstructured.SetNestedSlice(a.Object, []interface{}{map[string]interface{}{"macAddress": tt.mac}}, "status", "inventory", "interfaces")
			agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*a}}
			resolver := func(context.Context, string) []string {
				if tt.label != "" {
					return nil
				}
				return []string{"aa"}
			}
			got := findAgentForWorker(context.Background(), agents, "id", "worker", resolver)
			if (got != nil) != tt.want {
				t.Fatalf("agent=%v, want match=%v", got, tt.want)
			}
		})
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
			got, removed := observeWorkerFixture(t, []v1alpha1.WorkerStatus{w}, agents, func(context.Context, string) []string { return nil }, func(string) bool { return tt.present })
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
			if w.ReadySince == nil {
				got[0].ReadySince = nil
			}
			if !reflect.DeepEqual(got[0], w) {
				t.Fatalf("lost identity/history/clock: %+v", got[0])
			}
		})
	}
}
func TestCombinedObservationMixedAndEmptyWorkers(t *testing.T) {
	workers := []v1alpha1.WorkerStatus{newWorkerStatus("standard", "standard", "waiting", "id-0", workerPhaseProvisioning), newWorkerStatus("standard", "standard", "installed", "id-1", workerPhaseProvisioning), newWorkerStatus("standard", "standard", "missing", "id-2", workerPhaseReady), {Name: "vm", Kind: "VirtualMachine", Phase: "Running"}}
	agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*agentPhaseFixture("installed", true)}}
	got, removed := observeWorkerFixture(t, workers, agents, func(context.Context, string) []string { return nil }, func(id string) bool { return id != "id-2" })
	if len(got) != 3 || got[0].Phase != workerPhaseWaitingForAgent || got[1].Phase != workerPhaseReady || !reflect.DeepEqual(got[2], workers[3]) || !reflect.DeepEqual(removed, []string{"missing"}) {
		t.Fatalf("mixed workers=%v removed=%v", got, removed)
	}
	got, removed = observeWorkerFixture(t, nil, agents, func(context.Context, string) []string { return nil }, func(string) bool { return true })
	if got != nil || len(removed) != 0 {
		t.Fatal("empty input changed")
	}
}
