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
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// MACResolver returns the allocated host NIC MACs for a BMI by its resource ID. The production
// implementation (Reconciler.resolveHostMACs) reads them from the BMI's status.hardware.nics
// field, populated by the inventory backend at allocation time (OSAC-4203). A BMI may report
// multiple NICs; correlation matches an Agent to it if any NIC MAC matches.
type MACResolver func(ctx context.Context, bmiID string) []string

// agentAssociationState classifies how one worker's Agent was resolved from a complete scoped
// observation. Only agentEstablished may authorize readiness or bound deletion; agentAbsent needs
// authoritative evidence before destructive work, and ambiguous/invalid must fail closed.
type agentAssociationState int

const (
	agentAbsent agentAssociationState = iota
	agentEstablished
	agentAmbiguous
	agentInvalid
)

// agentAssociation is the single association result shared by phase projection, late binding
// and cleanup. A nil Agent is only meaningful for agentAbsent.
type agentAssociation struct {
	state  agentAssociationState
	agent  *unstructured.Unstructured
	reason string
}

// err surfaces an invalid association as a returned error. Ambiguous and absent results are
// decisions, not errors; callers must still refuse to advance or delete on them.
func (a agentAssociation) err() error {
	if a.state != agentInvalid {
		return nil
	}
	if a.reason == "" {
		return fmt.Errorf("invalid agent association")
	}
	return fmt.Errorf("%s", a.reason)
}

// agentIdentityKey is the Kubernetes identity of an Agent. Production identity is the UID; a
// namespaced name only disambiguates fixtures lacking one, so a recreated same-name Agent is a
// different incarnation and never the object an earlier observation authorized.
func agentIdentityKey(agent *unstructured.Unstructured) string {
	if uid := agent.GetUID(); uid != "" {
		return string(uid)
	}
	return agent.GetNamespace() + "/" + agent.GetName()
}

// distinctAgents preserves the first observation of each identity, so one object matched by both
// supported selectors is not counted twice and never becomes ambiguous.
func distinctAgents(agents []*unstructured.Unstructured) []*unstructured.Unstructured {
	seen := make(map[string]bool, len(agents))
	unique := make([]*unstructured.Unstructured, 0, len(agents))
	for _, agent := range agents {
		key := agentIdentityKey(agent)
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, agent)
	}
	return unique
}

// associateEstablishedAgent resolves the unique compatible Agent carrying the worker's
// worker-name label. It never falls back to MAC correlation: only an established label may
// authorize readiness or bound deletion.
func associateEstablishedAgent(agents []unstructured.Unstructured, co *v1alpha1.ClusterOrder, workerName string) agentAssociation {
	var matches []*unstructured.Unstructured
	for i := range agents {
		agent := &agents[i]
		if agent.GetLabels()[workerNameLabel] != workerName {
			continue
		}
		matches = append(matches, agent)
	}
	switch unique := distinctAgents(matches); len(unique) {
	case 0:
		return agentAssociation{state: agentAbsent}
	case 1:
		if err := agentBindingConflict(unique[0], co, workerName); err != nil {
			return agentAssociation{state: agentInvalid, reason: err.Error()}
		}
		return agentAssociation{state: agentEstablished, agent: unique[0]}
	default:
		return agentAssociation{state: agentAmbiguous,
			reason: fmt.Sprintf("%d Agents claim worker %s", len(unique), workerName)}
	}
}

// matchUnboundAgents resolves initial discovery from the observed snapshot: for every eligible
// waiting worker it returns the unique unbound compatible Agent whose inventory MACs intersect
// the worker's BMI host NICs. Ambiguity is preserved in both directions — one Agent matching
// several workers and several Agents matching one worker are both ambiguous — and an
// already-labelled or already-bound Agent is never a MAC fallback. An incompatible candidate is
// invalid evidence, not absence.
func matchUnboundAgents(
	ctx context.Context, co *v1alpha1.ClusterOrder, agents []unstructured.Unstructured,
	workers []v1alpha1.WorkerStatus, hostMACs MACResolver,
) map[string]agentAssociation {
	result := make(map[string]agentAssociation)
	type candidate struct {
		agent  *unstructured.Unstructured
		worker string
	}
	var candidates []candidate
	for i := range workers {
		w := &workers[i]
		if !eligibleForAgentObservation(*w) || w.Phase != workerPhaseWaitingForAgent {
			continue
		}
		bmiMACs := hostMACs(ctx, w.BareMetalInstance.ID)
		if len(bmiMACs) == 0 {
			continue
		}
		var invalid error
		var matched []*unstructured.Unstructured
		for j := range agents {
			agent := &agents[j]
			if agent.GetLabels()[workerNameLabel] != "" {
				continue // already claimed by a worker; never a fallback candidate
			}
			if !macsIntersect(extractAgentMACs(agent), bmiMACs) {
				continue
			}
			if err := agentBindingConflict(agent, co, w.Name); err != nil {
				invalid = err
				continue
			}
			matched = append(matched, agent)
		}
		unique := distinctAgents(matched)
		switch {
		case invalid != nil:
			result[w.Name] = agentAssociation{state: agentInvalid, reason: invalid.Error()}
		case len(unique) == 1:
			candidates = append(candidates, candidate{agent: unique[0], worker: w.Name})
		case len(unique) > 1:
			result[w.Name] = agentAssociation{state: agentAmbiguous,
				reason: fmt.Sprintf("%d unbound Agents match worker %s", len(unique), w.Name)}
		}
	}
	// An Agent that is the unique candidate for more than one worker is ambiguous for all of
	// them: never choose a worker arbitrarily.
	claims := map[string][]string{}
	for _, c := range candidates {
		key := agentIdentityKey(c.agent)
		claims[key] = append(claims[key], c.worker)
	}
	for _, c := range candidates {
		key := agentIdentityKey(c.agent)
		if len(claims[key]) > 1 {
			result[c.worker] = agentAssociation{state: agentAmbiguous,
				reason: fmt.Sprintf("unbound Agent %s matches several workers", key)}
			continue
		}
		result[c.worker] = agentAssociation{state: agentEstablished, agent: c.agent}
	}
	return result
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
// Uninterpretable entries are omitted (unknown, not a match); destructive association applies the
// stricter cleanup inventory validation instead.
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
