// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

func (r *Reconciler) listAgents(ctx context.Context, co *v1alpha1.ClusterOrder) (*unstructured.UnstructuredList, error) {
	agentList := &unstructured.UnstructuredList{}
	agentList.SetGroupVersionKind(schema.GroupVersionKind{
		Group: agentGVK.Group, Version: agentGVK.Version, Kind: agentGVK.Kind + "List",
	})
	infraEnvName := co.Name + infraEnvNameSuffix
	if err := r.List(ctx, agentList,
		client.InNamespace(co.Namespace),
		client.MatchingLabels{infraEnvAgentLabel: infraEnvName},
	); err != nil {
		return nil, fmt.Errorf("listing agents for %s by infraenv: %w", co.Name, err)
	}
	if len(agentList.Items) == 0 {
		if err := r.List(ctx, agentList,
			client.InNamespace(co.Namespace),
			client.MatchingLabels{clusterOrderLabel: co.Name},
		); err != nil {
			return nil, fmt.Errorf("listing agents for %s by clusterOrderLabel: %w", co.Name, err)
		}
	}
	return agentList, nil
}

// projectAgentWorkerPhases is observation only: it neither checks BMI existence,
// patches Agents, nor modifies the input workers or their identity/retry fields.
func projectAgentWorkerPhases(
	ctx context.Context, workers []v1alpha1.WorkerStatus,
	agents *unstructured.UnstructuredList, hostMACs MACResolver, knownPhases ...map[string]v1alpha1.WorkerStatus,
) []v1alpha1.WorkerStatus {
	result := append([]v1alpha1.WorkerStatus(nil), workers...)
	for i := range result {
		w := &result[i]
		if !eligibleForAgentObservation(*w) {
			continue
		}
		if len(knownPhases) > 0 {
			if previous, ok := knownPhases[0][w.Name]; ok && previous.Kind == w.Kind && previous.BareMetalInstance == w.BareMetalInstance && previous.Phase == w.Phase {
				continue
			}
		}
		w.Phase = deriveWorkerPhase(findAgentForWorker(ctx, agents, w.BareMetalInstance.ID, w.Name, hostMACs), w.Name)
	}
	return result
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
