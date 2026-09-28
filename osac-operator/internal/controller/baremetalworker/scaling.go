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
	"fmt"
	"sort"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// workerSlotPlan partitions existing workers and records how many slots each
// requested NodeSet still needs. It is derived from the spec, not persisted.
type workerSlotPlan struct {
	selected         []v1alpha1.WorkerStatus
	excess           []v1alpha1.WorkerStatus
	missingByNodeSet map[string]int
}

// planWorkerSlots calculates scale-up and scale-down together by logical NodeSet,
// independently of request order and hardware profiles. Teardown entries never
// satisfy capacity. Prefer retaining healthy, older workers.
func planWorkerSlots(co *v1alpha1.ClusterOrder) workerSlotPlan {
	plan := workerSlotPlan{missingByNodeSet: make(map[string]int)}
	for _, nr := range co.Spec.NodeRequests {
		if nr.IsBareMetal() {
			plan.missingByNodeSet[nr.NodeSet] = nr.NumberOfNodes
		}
	}
	candidates := append([]v1alpha1.WorkerStatus(nil), co.Status.Workers...)
	sortByDeletionPriority(candidates)
	for _, w := range candidates {
		if isExcessWorkerSlot(w, plan.missingByNodeSet[w.NodeSet]) {
			plan.excess = append(plan.excess, w)
			continue
		}
		plan.missingByNodeSet[w.NodeSet]--
		plan.selected = append(plan.selected, w)
	}
	return plan
}

func isExcessWorkerSlot(w v1alpha1.WorkerStatus, remainingSlots int) bool {
	return w.Kind != workerKindBMI ||
		w.Phase == workerPhaseUnbinding ||
		w.Phase == workerPhaseDeleting ||
		remainingSlots == 0
}

// sortByDeletionPriority orders workers for retention: higher deletion priorities
// first, then older workers. Equal priorities and timestamps preserve input order.
func sortByDeletionPriority(workers []v1alpha1.WorkerStatus) {
	sort.SliceStable(workers, func(i, j int) bool {
		pi, pj := deletionPriority(workers[i].Phase), deletionPriority(workers[j].Phase)
		if pi != pj {
			return pi > pj
		}
		return workers[i].CreationTimestamp.Before(&workers[j].CreationTimestamp)
	})
}

// handleScaleDown processes excess workers by deletion priority (CAPI-aligned):
// Failed first, then not-yet-bound, then newest Ready. Failed workers have their
// BMIs deleted immediately; others are marked Unbinding.
func (r *Reconciler) handleScaleDown(
	ctx context.Context, co *v1alpha1.ClusterOrder,
	workers []v1alpha1.WorkerStatus, excess []v1alpha1.WorkerStatus,
) []v1alpha1.WorkerStatus {
	log := ctrllog.FromContext(ctx)

	sort.SliceStable(excess, func(i, j int) bool {
		pi, pj := deletionPriority(excess[i].Phase), deletionPriority(excess[j].Phase)
		if pi != pj {
			return pi < pj
		}
		return excess[j].CreationTimestamp.Before(&excess[i].CreationTimestamp)
	})

	now := metav1.Now()
	for _, w := range excess {
		if w.Phase == workerPhaseFailed {
			workers = r.removeFailedExcess(ctx, co, workers, w)
			continue
		}
		if w.Phase == workerPhaseUnbinding || w.Phase == workerPhaseDeleting {
			workers = append(workers, w)
			continue
		}
		log.Info("marking worker for scale-down", "worker", w.Name, "previousPhase", w.Phase)
		w.Phase = workerPhaseUnbinding
		w.LastFailureTime = &now
		r.recorder.Eventf(co, nil, corev1.EventTypeNormal, eventReasonWorkerDeleted, "ScaleDown",
			"worker %s marked for unbinding during scale-down", w.Name)
		workers = append(workers, w)
	}

	return workers
}

func (r *Reconciler) removeFailedExcess(
	ctx context.Context, co *v1alpha1.ClusterOrder,
	workers []v1alpha1.WorkerStatus, w v1alpha1.WorkerStatus,
) []v1alpha1.WorkerStatus {
	log := ctrllog.FromContext(ctx)

	if w.BareMetalInstance.ID != "" {
		if err := r.checkedDeleteBMI(ctx, co, w); err != nil {
			log.Error(err, "deleting excess failed BMI", "worker", w.Name)
			return append(workers, w)
		}
		log.Info("deleted excess failed BMI", "worker", w.Name, "bmiID", w.BareMetalInstance.ID)
	}
	r.recorder.Eventf(co, nil, corev1.EventTypeNormal, eventReasonWorkerDeleted, "ScaleDown",
		"removed failed worker %s during scale-down", w.Name)
	return workers
}

func deletionPriority(phase string) int {
	switch phase {
	case workerPhaseFailed:
		return 0
	case workerPhaseProvisioning, workerPhaseWaitingForAgent, workerPhaseBinding:
		return 1
	default:
		return 2
	}
}

// handleUnbindingWorkers processes workers in Unbinding phase: finds matching Agents via the
// worker-name label, waits for unbinding-pending-user-action or a fully detached known-unbound
// Agent, then deletes the Agent CR. Transitions to Deleting after Agent deletion. Checks for
// unbinding timeout (30 min).
func (r *Reconciler) handleUnbindingWorkers(
	ctx context.Context, co *v1alpha1.ClusterOrder,
	workers []v1alpha1.WorkerStatus,
) []v1alpha1.WorkerStatus {
	log := ctrllog.FromContext(ctx)
	agents, err := r.listAgents(ctx, co)
	if err != nil {
		log.Error(err, "listing agents for unbinding check")
		return workers
	}

	now := time.Now()
	for i := range workers {
		w := &workers[i]
		if w.Phase != workerPhaseUnbinding {
			continue
		}
		r.processUnbindingWorker(ctx, co, w, agents, now)
	}
	return workers
}

func (r *Reconciler) processUnbindingWorker(
	ctx context.Context, co *v1alpha1.ClusterOrder,
	w *v1alpha1.WorkerStatus, agents *unstructured.UnstructuredList, now time.Time,
) {
	log := ctrllog.FromContext(ctx)

	agent := findAgentByWorkerName(agents, w.Name)
	if agent == nil {
		log.Info("no agent found for unbinding worker, transitioning to Deleting", "worker", w.Name)
		w.Phase = workerPhaseDeleting
		return
	}

	state, _, _ := unstructured.NestedString(agent.Object, "status", "debugInfo", "state")
	// CAP-Agent may reclaim a worker straight to known-unbound without visiting
	// unbinding-pending-user-action. Only treat that state as safe once both the
	// ClusterDeployment and AgentMachine binding references are gone.
	if state != agentUnbindingState && !isDetachedKnownUnbound(agent, state) {
		r.checkUnbindingTimeout(co, w, agent, now)
		return
	}

	if err := r.Delete(ctx, agent); err != nil {
		log.Error(err, "deleting agent CR", "agent", agent.GetName())
		return
	}
	log.Info("deleted agent CR", "worker", w.Name, "agent", agent.GetName())
	w.Phase = workerPhaseDeleting
}

func isDetachedKnownUnbound(agent *unstructured.Unstructured, state string) bool {
	if state != "known-unbound" {
		return false
	}
	_, boundToCluster, err := unstructured.NestedFieldNoCopy(agent.Object, "spec", "clusterDeploymentName")
	if err != nil || boundToCluster {
		return false
	}
	_, boundToMachine := agent.GetLabels()["agentMachineRef"]
	return !boundToMachine
}

func (r *Reconciler) checkUnbindingTimeout(
	co *v1alpha1.ClusterOrder, w *v1alpha1.WorkerStatus,
	agent *unstructured.Unstructured, now time.Time,
) {
	if w.LastFailureTime == nil || now.Sub(w.LastFailureTime.Time) <= agentUnbindingTimeout {
		return
	}
	w.LastFailureReason = eventReasonAgentUnbindingTimeout
	w.LastFailureMessage = fmt.Sprintf("agent %s stuck unbinding for > %s", agent.GetName(), agentUnbindingTimeout)
	r.recorder.Eventf(co, nil, corev1.EventTypeWarning, eventReasonAgentUnbindingTimeout, "UnbindTimeout",
		"worker %s: agent %s stuck unbinding", w.Name, agent.GetName())
}

// handleDeletingWorkers processes workers in Deleting phase: checks if the BMI is confirmed
// deleted (GetBareMetalInstance returns NotFound), and removes the entry. Retries Delete if
// the BMI still exists.
func (r *Reconciler) handleDeletingWorkers(
	ctx context.Context, co *v1alpha1.ClusterOrder,
	workers []v1alpha1.WorkerStatus,
) []v1alpha1.WorkerStatus {
	log := ctrllog.FromContext(ctx)
	var kept []v1alpha1.WorkerStatus
	for i := range workers {
		w := &workers[i]
		if w.Phase != workerPhaseDeleting {
			kept = append(kept, *w)
			continue
		}
		if w.BareMetalInstance.ID == "" {
			log.Info("worker deletion confirmed (no resource ID)", "worker", w.Name)
			r.recorder.Eventf(co, nil, corev1.EventTypeNormal, eventReasonWorkerDeleted, "ConfirmDeletion",
				"worker %s removed after BMI deletion confirmed", w.Name)
			continue
		}
		_, err := r.fulfillment.GetBareMetalInstance(ctx, w.BareMetalInstance.ID)
		if err != nil && status.Code(err) == codes.NotFound {
			log.Info("worker BMI deletion confirmed", "worker", w.Name, "bmiID", w.BareMetalInstance.ID)
			r.recorder.Eventf(co, nil, corev1.EventTypeNormal, eventReasonWorkerDeleted, "ConfirmDeletion",
				"worker %s removed after BMI %s deletion confirmed", w.Name, w.BareMetalInstance.ID)
			continue
		}
		if err != nil {
			log.Error(err, "checking BMI existence for deleting worker", "worker", w.Name)
			kept = append(kept, *w)
			continue
		}
		if delErr := r.checkedDeleteBMI(ctx, co, *w); delErr != nil {
			log.Error(delErr, "retrying BMI deletion", "worker", w.Name)
		}
		kept = append(kept, *w)
	}
	return kept
}

func findAgentByWorkerName(agents *unstructured.UnstructuredList, workerName string) *unstructured.Unstructured {
	for idx := range agents.Items {
		agent := &agents.Items[idx]
		if agent.GetLabels()[workerNameLabel] == workerName {
			return agent
		}
	}
	return nil
}
