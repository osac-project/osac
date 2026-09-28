// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// listAgents returns the union of both supported Agent selectors, scoped to the
// cluster namespace. One observation stage issues two Kubernetes Lists — the
// InfraEnv registration filter and the cluster-order watch filter — and unions
// them by UID, so a mixed population is never truncated to whichever selector
// matched first. If either List fails the observation is unknown: a partial list
// is never treated as complete absence evidence.
func (r *Reconciler) listAgents(ctx context.Context, co *v1alpha1.ClusterOrder) (*unstructured.UnstructuredList, error) {
	infraEnvName := co.Name + infraEnvNameSuffix
	selectors := []client.MatchingLabels{
		{infraEnvAgentLabel: infraEnvName},
		{clusterOrderLabel: co.Name},
	}
	union := &unstructured.UnstructuredList{}
	union.SetGroupVersionKind(agentGVK.GroupVersion().WithKind(agentGVK.Kind + "List"))
	seen := make(map[string]bool)
	for _, selector := range selectors {
		listed := &unstructured.UnstructuredList{}
		listed.SetGroupVersionKind(union.GroupVersionKind())
		if err := r.List(ctx, listed, client.InNamespace(co.Namespace), selector); err != nil {
			return nil, fmt.Errorf("listing agents for %s: %w", co.Name, err)
		}
		for i := range listed.Items {
			agent := listed.Items[i]
			// Production identity is the Kubernetes UID; a namespaced name only
			// disambiguates fixtures that predate UIDs, so it is never a substitute.
			key := string(agent.GetUID())
			if key == "" {
				key = agent.GetNamespace() + "/" + agent.GetName()
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			union.Items = append(union.Items, agent)
		}
	}
	return union, nil
}

// projectAgentWorkerPhases is the single pure Agent-phase projection: it neither
// checks BMI existence, patches Agents, nor modifies the input workers or their
// identity/retry fields. Readiness comes only from a unique compatible
// established binding; an absent, ambiguous or invalid association fails closed
// (ambiguous stays WaitingForAgent; invalid is returned as an error). Protected
// Failed/Unbinding/Deleting workers are never projected back into a normal phase.
func projectAgentWorkerPhases(
	co *v1alpha1.ClusterOrder, workers []v1alpha1.WorkerStatus, agents *unstructured.UnstructuredList,
) ([]v1alpha1.WorkerStatus, error) {
	result := append([]v1alpha1.WorkerStatus(nil), workers...)
	for i := range result {
		w := &result[i]
		if !eligibleForAgentObservation(*w) {
			continue
		}
		association := associateEstablishedAgent(agents.Items, co, w.Name)
		if err := association.err(); err != nil {
			return nil, err
		}
		if association.state == agentEstablished {
			w.Phase = deriveWorkerPhase(association.agent, w.Name)
			continue
		}
		// No established association (absent or ambiguous): never advance readiness.
		w.Phase = workerPhaseWaitingForAgent
	}
	return result, nil
}

func eligibleForAgentObservation(w v1alpha1.WorkerStatus) bool {
	return w.Kind == workerKindBMI && w.BareMetalInstance.ID != "" &&
		w.Phase != workerPhaseFailed && w.Phase != workerPhaseUnbinding && w.Phase != workerPhaseDeleting
}

func deriveWorkerPhase(agent *unstructured.Unstructured, workerName string) string {
	if agent == nil || agent.GetLabels()[workerNameLabel] != workerName {
		return workerPhaseWaitingForAgent
	}
	if agentInstalled(agent) {
		return workerPhaseReady
	}
	return workerPhaseBinding
}

// agentInstalled reports whether an Agent is installed. The Installed condition is authoritative
// when present; status.debugInfo.state is retained as a fallback for older Agent objects.
func agentInstalled(agent *unstructured.Unstructured) bool {
	if agent == nil {
		return false
	}

	conditions, _, _ := unstructured.NestedSlice(agent.Object, "status", "conditions")
	for _, rawCondition := range conditions {
		condition, ok := rawCondition.(map[string]interface{})
		if !ok {
			continue
		}
		typeName, _, _ := unstructured.NestedString(condition, "type")
		if typeName != "Installed" {
			continue
		}
		status, _, _ := unstructured.NestedString(condition, "status")
		return status == "True"
	}

	state, _, _ := unstructured.NestedString(agent.Object, "status", "debugInfo", "state")
	return state == "installed"
}
