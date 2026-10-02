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
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// MACResolver returns the allocated host NIC MACs for a BMI by its resource ID. The production
// implementation (Reconciler.resolveHostMACs) reads them from the BMI's status.hardware.nics
// field, populated by the inventory backend at allocation time (OSAC-4203). A BMI may report
// multiple NICs; correlation matches an Agent to it if any NIC MAC matches.
type MACResolver func(ctx context.Context, bmiID string) []string

// matchAgentToBMI performs the three-dimension match: the Agent must be in the correct namespace,
// carry the cluster-order label, and have an inventory MAC that uniquely matches one BMI's host
// NIC MACs. Returns the matched worker name, or empty string with ambiguous=true if multiple match.
func matchAgentToBMI(
	ctx context.Context,
	agent *unstructured.Unstructured,
	workers []v1alpha1.WorkerStatus,
	hostMACs MACResolver,
) (workerName string, ambiguous bool) {
	agentMACs := extractAgentMACs(agent)
	if len(agentMACs) == 0 {
		return "", false
	}

	var matched string
	for i := range workers {
		w := &workers[i]
		if !eligibleForAgentObservation(*w) || w.Phase != workerPhaseWaitingForAgent {
			continue
		}
		bmiMACs := hostMACs(ctx, w.BareMetalInstance.ID)
		if len(bmiMACs) == 0 {
			continue
		}
		if macsIntersect(agentMACs, bmiMACs) {
			if matched != "" {
				return "", true
			}
			matched = w.Name
		}
	}
	return matched, false
}

// macsIntersect reports whether any MAC in a matches any MAC in b, case-insensitively.
func macsIntersect(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if strings.EqualFold(x, y) {
				return true
			}
		}
	}
	return false
}

// extractAgentMACs reads all MAC addresses from the Agent's status.inventory.interfaces[].macAddress.
func extractAgentMACs(agent *unstructured.Unstructured) []string {
	interfaces, found, err := unstructured.NestedSlice(agent.Object, "status", "inventory", "interfaces")
	if err != nil || !found {
		return nil
	}
	macs := make([]string, 0, len(interfaces))
	for _, iface := range interfaces {
		m, ok := iface.(map[string]interface{})
		if !ok {
			continue
		}
		mac, ok := m["macAddress"].(string)
		if !ok || mac == "" {
			continue
		}
		macs = append(macs, mac)
	}
	return macs
}

// findAgentForWorker prefers the recorded worker-name binding, then uses the
// existing inventory NIC MAC correlation fallback. It does not query providers.
func findAgentForWorker(ctx context.Context, agents *unstructured.UnstructuredList, bmiID, workerName string, hostMACs MACResolver) *unstructured.Unstructured {
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

func findAgentByWorkerName(agents *unstructured.UnstructuredList, workerName string) *unstructured.Unstructured {
	for idx := range agents.Items {
		agent := &agents.Items[idx]
		if agent.GetLabels()[workerNameLabel] == workerName {
			return agent
		}
	}
	return nil
}
