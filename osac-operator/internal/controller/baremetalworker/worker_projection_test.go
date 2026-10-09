// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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
func observeWorkerFixture(workers []v1alpha1.WorkerStatus, agents *unstructured.UnstructuredList, exists func(string) bool) ([]v1alpha1.WorkerStatus, []string) {
	GinkgoHelper()
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
	Expect(err).NotTo(HaveOccurred())
	var removed []string
	for _, before := range co.Status.Workers {
		if workerByName(workers, before.Name) == nil {
			removed = append(removed, before.Name)
		}
	}
	return workers, removed
}

var _ = It("prefers an established worker label over MAC fallback", func() {
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
	Expect(association.state).To(Equal(agentEstablished), "established label not preferred: %+v", association)
	Expect(string(association.agent.GetUID())).To(Equal("uid-labeled"), "established label not preferred: %+v", association)

	// An unbound Agent is not an established binding; MAC correlation is only
	// initial-discovery evidence and must never override another worker's label.
	unbound := agentPhaseFixture("", false)
	unbound.SetUID("uid-unbound")
	_ = unstructured.SetNestedSlice(unbound.Object, []interface{}{
		map[string]interface{}{"macAddress": "aa"},
	}, "status", "inventory", "interfaces")
	if association := associateEstablishedAgent([]unstructured.Unstructured{*unbound}, co, "worker"); association.state != agentAbsent {
		Fail(fmt.Sprintf("MAC fallback authorized an established binding: %+v", association))
	}
	other := agentPhaseFixture("other-worker", false)
	other.SetUID("uid-other")
	_ = unstructured.SetNestedSlice(other.Object, []interface{}{
		map[string]interface{}{"macAddress": "aa"},
	}, "status", "inventory", "interfaces")
	associations := matchUnboundAgents(context.Background(), co, []unstructured.Unstructured{*other}, []v1alpha1.WorkerStatus{worker}, resolver)
	if association, ok := associations["worker"]; ok {
		Fail(fmt.Sprintf("used another worker's Agent as a MAC fallback: %+v", association))
	}
})
var _ = Describe("worker phase mapping", func() {
	for _, tt := range []struct {
		name, label, condition string
		installed              bool
		want                   string
	}{{"nil", "", "", false, workerPhaseWaitingForAgent}, {"bound debug installed", "worker", "", true, workerPhaseReady}, {"bound condition installed", "worker", "True", false, workerPhaseReady}, {"bound installing", "worker", "", false, workerPhaseBinding}, {"False overrides debug", "worker", "False", true, workerPhaseBinding}, {"Unknown overrides debug", "worker", "Unknown", true, workerPhaseBinding}, {"unbound installed", "", "True", true, workerPhaseWaitingForAgent}} {
		It(tt.name, func() {
			var a *unstructured.Unstructured
			if tt.name != "nil" {
				a = agentPhaseFixture(tt.label, tt.installed)
				if tt.condition != "" {
					_ = unstructured.SetNestedSlice(a.Object, []interface{}{map[string]interface{}{"type": "Installed", "status": tt.condition}}, "status", "conditions")
				}
			}
			if got := deriveWorkerPhase(a, "worker"); got != tt.want {
				Fail(fmt.Sprintf("phase=%s, want %s", got, tt.want))
			}
		})
	}
})
var _ = Describe("Worker phase and history preservation", func() {
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
		It(tt.name, func() {
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
			got, removed := observeWorkerFixture([]v1alpha1.WorkerStatus{w}, agents, func(string) bool { return tt.present })
			if tt.want == "" {
				Expect(got).To(BeEmpty(), "absence result=%v removed=%v", got, removed)
				Expect(removed).To(Equal([]string{"worker"}), "absence result=%v removed=%v", got, removed)
				return
			}
			Expect(got).To(HaveLen(1), "result=%v removed=%v", got, removed)
			Expect(got[0].Phase).To(Equal(tt.want), "result=%v removed=%v", got, removed)
			Expect(removed).To(BeEmpty(), "result=%v removed=%v", got, removed)
			got[0].Phase = w.Phase
			// AttemptStartedAt is covered by the legacy attempt backfill spec;
			// normalize it so the identity/history and
			// ReadySince assertions below stay focused.
			w.AttemptStartedAt = nil
			got[0].AttemptStartedAt = nil
			// ReadySince describes a continuous interval: it is cleared on any demotion
			// and only compared when the observed phase is still Ready.
			if tt.want != workerPhaseReady {
				Expect(got[0].ReadySince).To(BeNil(), "demoted worker retained the healthy interval: %+v", got[0].ReadySince)
				w.ReadySince = nil
			} else if w.ReadySince == nil {
				got[0].ReadySince = nil
			}
			Expect(got[0]).To(Equal(w), "lost identity/history/clock: %+v", got[0])
		})
	}
})

// A Ready demotion clears ReadySince, so
// disjoint healthy intervals cannot accumulate into the healthy-reset threshold.
var _ = It("starts a new continuous healthy interval after a Ready demotion", func() {
	stale := metav1.NewTime(time.Now().Add(-2 * time.Hour).Truncate(time.Second))
	ready := newWorkerStatus("standard", "standard", "worker", "id", workerPhaseReady)
	ready.ReadySince = &stale
	got, _ := observeWorkerFixture([]v1alpha1.WorkerStatus{ready}, &unstructured.UnstructuredList{}, func(string) bool { return true })
	Expect(got).To(HaveLen(1), "expected a Ready demotion: %+v", got)
	Expect(got[0].Phase).To(Equal(workerPhaseWaitingForAgent), "expected a Ready demotion: %+v", got)
	Expect(got[0].ReadySince).To(BeNil(), "demotion retained the previous healthy interval: %+v", got[0].ReadySince)
	// Re-entering Ready starts a new interval instead of inheriting the stale one.
	agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*agentPhaseFixture("worker", true)}}
	got, _ = observeWorkerFixture([]v1alpha1.WorkerStatus{got[0]}, agents, func(string) bool { return true })
	Expect(got).To(HaveLen(1), "re-entry did not start a fresh healthy interval: %+v", got)
	Expect(got[0].Phase).To(Equal(workerPhaseReady), "re-entry did not start a fresh healthy interval: %+v", got)
	Expect(got[0].ReadySince).NotTo(BeNil(), "re-entry did not start a fresh healthy interval: %+v", got)
	Expect(got[0].ReadySince.Time.After(stale.Time)).To(BeTrue(), "re-entry reused a disconnected interval: %+v", got[0].ReadySince)
})

// Legacy attempt clocks are migrated once: a pre-existing
// attempt inherits the backing BMI's creation time when usable, otherwise a single
// observation-time origin. Neither is refreshed by later reconciles.
var _ = It("backfills a legacy attempt clock once without extending its deadline", func() {
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
	Expect(err).NotTo(HaveOccurred())
	Expect(got[0].AttemptStartedAt).NotTo(BeNil(), "legacy worker did not inherit the BMI creation time: %+v", got[0].AttemptStartedAt)
	Expect(got[0].AttemptStartedAt.Time.Equal(created)).To(BeTrue(), "legacy worker did not inherit the BMI creation time: %+v", got[0].AttemptStartedAt)

	// Persisted once: another observation with the field already set must keep it.
	co.Status.Workers = got
	observed = indexWorkerBMIs([]*privatev1.BareMetalInstance{bmi})
	observed.agents = &unstructured.UnstructuredList{}
	again, err := r.observeExistingWorkers(ctx, co, "tenant", observed)
	Expect(err).NotTo(HaveOccurred())
	Expect(again[0].AttemptStartedAt).NotTo(BeNil(), "backfill refreshed an already persisted attempt origin")
	Expect(again[0].AttemptStartedAt.Equal(got[0].AttemptStartedAt)).To(BeTrue(), "backfill refreshed an already persisted attempt origin")

	// No usable BMI clock: one observation-time origin is still durable.
	noclock := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac"}}
	noclockWorker := newWorkerStatus("standard", "standard", "worker", "id", workerPhaseWaitingForAgent)
	noclockWorker.AttemptStartedAt = nil
	noclock.Status.Workers = []v1alpha1.WorkerStatus{noclockWorker}
	fallbackObs := indexWorkerBMIs([]*privatev1.BareMetalInstance{ownedBMIFixture(noclock, "worker", "id")})
	fallbackObs.agents = &unstructured.UnstructuredList{}
	fallback, err := r.observeExistingWorkers(ctx, noclock, "tenant", fallbackObs)
	Expect(err).NotTo(HaveOccurred())
	Expect(fallback[0].AttemptStartedAt).ToNot(BeNil(), "missing-clock legacy worker was not given a one-time origin")
})

var _ = It("observes mixed worker kinds and leaves empty input unchanged", func() {
	workers := []v1alpha1.WorkerStatus{newWorkerStatus("standard", "standard", "waiting", "id-0", workerPhaseProvisioning), newWorkerStatus("standard", "standard", "installed", "id-1", workerPhaseProvisioning), newWorkerStatus("standard", "standard", "missing", "id-2", workerPhaseReady), {Name: "vm", Kind: "VirtualMachine", Phase: "Running"}}
	agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*agentPhaseFixture("installed", true)}}
	got, removed := observeWorkerFixture(workers, agents, func(id string) bool { return id != "id-2" })
	Expect(got).To(HaveLen(3), "mixed workers=%v removed=%v", got, removed)
	Expect(got[0].Phase).To(Equal(workerPhaseWaitingForAgent), "mixed workers=%v removed=%v", got, removed)
	Expect(got[1].Phase).To(Equal(workerPhaseReady), "mixed workers=%v removed=%v", got, removed)
	Expect(got[2]).To(Equal(workers[3]), "mixed workers=%v removed=%v", got, removed)
	Expect(removed).To(Equal([]string{"missing"}), "mixed workers=%v removed=%v", got, removed)
	got, removed = observeWorkerFixture(nil, agents, func(string) bool { return true })
	Expect(got).To(BeNil(), "empty input changed")
	Expect(removed).To(BeEmpty(), "empty input changed")
})
