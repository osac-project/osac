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
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const (
	eventReasonWorkerDeleted = "WorkerDeleted"
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
	return planWorkerSlotsFor(co, co.Status.Workers)
}

// planWorkerSlotsFor is planWorkerSlots against an explicit worker list, so the
// summary and metric consumers can classify a projected or persisted journal
// with the same NodeSet-safe retention rules before it is written.
func planWorkerSlotsFor(co *v1alpha1.ClusterOrder, workers []v1alpha1.WorkerStatus) workerSlotPlan {
	plan := workerSlotPlan{missingByNodeSet: make(map[string]int)}
	for _, nr := range co.Spec.NodeRequests {
		if nr.IsBareMetal() {
			plan.missingByNodeSet[nr.NodeSet] = nr.NumberOfNodes
		}
	}
	candidates := append([]v1alpha1.WorkerStatus(nil), workers...)
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
// Failed first, then not-yet-bound, then newest Ready. All slots persist their
// retirement intent as Unbinding before shared cleanup may touch infrastructure.
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
		if w.Phase == workerPhaseUnbinding || w.Phase == workerPhaseDeleting {
			workers = append(workers, w)
			continue
		}
		log.Info("marking worker for scale-down", "worker", w.Name, "previousPhase", w.Phase)
		w.Phase = workerPhaseUnbinding
		w.LastFailureTime = &now
		// Retirement is a lifecycle demotion: the continuous healthy interval ends.
		w.ReadySince = nil
		r.recorder.Eventf(co, nil, corev1.EventTypeNormal, eventReasonWorkerDeleted, "ScaleDown",
			"worker %s marked for unbinding during scale-down", w.Name)
		workers = append(workers, w)
	}

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
