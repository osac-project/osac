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

func desiredWorkerNames(co *v1alpha1.ClusterOrder) map[string]bool {
	names := make(map[string]bool)
	globalIndex := 0
	for i := range co.Spec.NodeRequests {
		nr := &co.Spec.NodeRequests[i]
		if !nr.IsBareMetal() {
			continue
		}
		for j := 0; j < nr.NumberOfNodes; j++ {
			names[fmt.Sprintf("%s-worker-%d", co.Name, globalIndex)] = true
			globalIndex++
		}
	}
	return names
}

// identifyExcessWorkers returns workers in status that are not in the desired set and are not
// already in a teardown phase (Unbinding/Deleting).
func (r *Reconciler) identifyExcessWorkers(
	co *v1alpha1.ClusterOrder, currentWorkers []v1alpha1.WorkerStatus,
) []v1alpha1.WorkerStatus {
	desired := desiredWorkerNames(co)
	var excess []v1alpha1.WorkerStatus
	for _, w := range co.Status.Workers {
		if desired[w.Name] {
			continue
		}
		alreadyTracked := false
		for _, cw := range currentWorkers {
			if cw.Name == w.Name {
				alreadyTracked = true
				break
			}
		}
		if alreadyTracked {
			continue
		}
		excess = append(excess, w)
	}
	return excess
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

	if w.ResourceID != "" {
		if err := r.fulfillment.DeleteBareMetalInstance(ctx, w.ResourceID); err != nil {
			log.Error(err, "deleting excess failed BMI", "worker", w.Name)
			return append(workers, w)
		}
		log.Info("deleted excess failed BMI", "worker", w.Name, "bmiID", w.ResourceID)
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
// worker-name label, waits for the Agent to enter unbinding-pending-user-action, then deletes
// the Agent CR. Transitions to Deleting after Agent deletion. Checks for unbinding timeout (30 min).
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
	if state != agentUnbindingState {
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
		if w.ResourceID == "" {
			log.Info("worker deletion confirmed (no resource ID)", "worker", w.Name)
			r.recorder.Eventf(co, nil, corev1.EventTypeNormal, eventReasonWorkerDeleted, "ConfirmDeletion",
				"worker %s removed after BMI deletion confirmed", w.Name)
			continue
		}
		_, err := r.fulfillment.GetBareMetalInstance(ctx, w.ResourceID)
		if err != nil && status.Code(err) == codes.NotFound {
			log.Info("worker BMI deletion confirmed", "worker", w.Name, "bmiID", w.ResourceID)
			r.recorder.Eventf(co, nil, corev1.EventTypeNormal, eventReasonWorkerDeleted, "ConfirmDeletion",
				"worker %s removed after BMI %s deletion confirmed", w.Name, w.ResourceID)
			continue
		}
		if err != nil {
			log.Error(err, "checking BMI existence for deleting worker", "worker", w.Name)
			kept = append(kept, *w)
			continue
		}
		if delErr := r.fulfillment.DeleteBareMetalInstance(ctx, w.ResourceID); delErr != nil {
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

func workerSlicesEqual(a, b []v1alpha1.WorkerStatus) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Phase != b[i].Phase {
			return false
		}
	}
	return true
}

func hasTeardownWorkers(workers []v1alpha1.WorkerStatus) bool {
	for _, w := range workers {
		if w.Phase == workerPhaseUnbinding || w.Phase == workerPhaseDeleting {
			return true
		}
	}
	return false
}
