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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// rebuildWorkerState re-derives worker phases from live BMI and Agent state. For each
// status.workers[] entry with kind BareMetalInstance, it looks up the BMI via
// GetBareMetalInstance. If the BMI is gone (NotFound), the entry is removed. If present,
// the phase is re-derived from Agent correlation state. AttemptCount and failure history
// are preserved from the persisted status.
// Returns true if any changes were made (caller should re-read the ClusterOrder).
func (r *Reconciler) rebuildWorkerState(ctx context.Context, co *v1alpha1.ClusterOrder) (bool, error) {
	if len(co.Status.Workers) == 0 {
		return false, nil
	}

	hasBMWorkers := false
	for i := range co.Status.Workers {
		if co.Status.Workers[i].Kind == workerKindBMI {
			hasBMWorkers = true
			break
		}
	}
	if !hasBMWorkers {
		return false, nil
	}

	log := ctrllog.FromContext(ctx)

	bmiExists := func(resourceID string) bool {
		_, err := r.fulfillment.GetBareMetalInstance(ctx, resourceID)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return false
			}
			log.Error(err, "checking BMI existence during rebuild", "resourceID", resourceID)
			return true
		}
		return true
	}

	agents, err := r.listAgents(ctx, co)
	if err != nil {
		return false, fmt.Errorf("listing agents during rebuild: %w", err)
	}

	rebuilt, removed := rebuildWorkerPhases(ctx, co.Status.Workers, agents, r.macResolver, bmiExists)

	if len(removed) == 0 && workerPhasesUnchanged(co.Status.Workers, rebuilt) {
		return false, nil
	}

	now := metav1.Now()
	for i := range rebuilt {
		if rebuilt[i].Phase == workerPhaseReady && rebuilt[i].ReadySince == nil {
			rebuilt[i].ReadySince = &now
		}
	}

	for _, name := range removed {
		log.Info("removed stale worker entry", "worker", name)
	}

	if err := r.updateWorkerStatus(ctx, co, rebuilt); err != nil {
		return false, fmt.Errorf("updating worker status after rebuild: %w", err)
	}
	return true, nil
}

// rebuildWorkerPhases is the pure logic for state rebuild. It iterates workers, checks BMI
// existence, and re-derives phases from Agent state. Workers in teardown phases (Unbinding,
// Deleting) are left untouched. Non-BMI workers pass through unchanged.
// Returns the rebuilt worker list and names of removed stale entries.
func rebuildWorkerPhases(
	ctx context.Context,
	workers []v1alpha1.WorkerStatus,
	agents *unstructured.UnstructuredList,
	hostMACs MACResolver,
	bmiExists func(resourceID string) bool,
) ([]v1alpha1.WorkerStatus, []string) {
	if workers == nil {
		return nil, nil
	}

	var result []v1alpha1.WorkerStatus
	var removed []string

	for i := range workers {
		w := workers[i]
		if w.Kind != workerKindBMI {
			result = append(result, w)
			continue
		}

		if w.Phase == workerPhaseUnbinding || w.Phase == workerPhaseDeleting || w.Phase == workerPhaseFailed {
			result = append(result, w)
			continue
		}

		if !bmiExists(w.ResourceID) {
			removed = append(removed, w.Name)
			continue
		}

		agent := findAgentForWorker(ctx, agents, w.ResourceID, w.Name, hostMACs)
		newPhase := deriveWorkerPhase(agent, w.Name)
		w.Phase = newPhase
		result = append(result, w)
	}

	return result, removed
}

// findAgentForWorker searches the agent list for an Agent that matches a worker. It first
// checks for an agent already labeled with the worker's name (cached binding), then falls
// back to MAC-based lookup via MACResolver. Unlike matchAgentToBMI, this does not filter
// by worker phase — it matches any worker.
func findAgentForWorker(
	ctx context.Context,
	agents *unstructured.UnstructuredList,
	bmiID string,
	workerName string,
	hostMACs MACResolver,
) *unstructured.Unstructured {
	for idx := range agents.Items {
		agent := &agents.Items[idx]
		if agent.GetLabels()[workerNameLabel] == workerName {
			return agent
		}
	}

	bmiMACs := hostMACs(ctx, bmiID)
	if len(bmiMACs) == 0 {
		return nil
	}

	for idx := range agents.Items {
		agent := &agents.Items[idx]
		if macsIntersect(extractAgentMACs(agent), bmiMACs) {
			return agent
		}
	}
	return nil
}

// deriveWorkerPhase determines the correct phase for a worker based on live Agent state.
// If no agent is found, returns WaitingForAgent. If an agent is bound to this worker
// (has the workerNameLabel) and installed, returns Ready. If bound but not installed,
// returns Binding. If the agent exists but is not bound to this worker, returns
// WaitingForAgent (correlation hasn't happened yet).
func deriveWorkerPhase(agent *unstructured.Unstructured, workerName string) string {
	if agent == nil {
		return workerPhaseWaitingForAgent
	}

	labels := agent.GetLabels()
	if labels[workerNameLabel] != workerName {
		return workerPhaseWaitingForAgent
	}

	if agentInstalled(agent) {
		return workerPhaseReady
	}
	return workerPhaseBinding
}

func workerPhasesUnchanged(old, new []v1alpha1.WorkerStatus) bool {
	if len(old) != len(new) {
		return false
	}
	for i := range old {
		if old[i].Phase != new[i].Phase {
			return false
		}
	}
	return true
}
