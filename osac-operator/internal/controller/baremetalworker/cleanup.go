// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// cleanupWorker retains the incarnation until both its old Agent and BMI are
// authoritatively absent. A successful Delete is pending, never completion.
// Failed callers keep their disposition; retiring callers advance to Deleting.
func (r *Reconciler) cleanupWorker(ctx context.Context, co *v1alpha1.ClusterOrder, w *v1alpha1.WorkerStatus) (bool, error) {
	if err := r.checkCurrentCapacityPlan(ctx, co); err != nil {
		return false, err
	}
	tenant, err := r.authoritativeWorkerTenant(ctx, co)
	if err != nil {
		return false, err
	}
	if w.BareMetalInstance.ID == "" {
		if w.BMICreateState == v1alpha1.WorkerBMICreateStateReserved {
			// Only the authoritative journal can prove no Create was authorized.
			// The caller's optimistic removal competes with the Attempted write;
			// a conflict retains the reservation and ends the invocation.
			recorded := workerByName(co.Status.Workers, w.Name)
			if recorded == nil || recorded.BMICreateState != v1alpha1.WorkerBMICreateStateReserved ||
				recorded.BareMetalInstance != w.BareMetalInstance || w.BareMetalInstance.Name == "" {
				return false, errWorkerObservationChanged
			}
			return true, nil
		}
		return false, r.recoverCleanupBMI(ctx, co, tenant, w)
	}
	state, err := r.readCleanupBMI(ctx, co, tenant, *w)
	if err != nil {
		return false, err
	}
	agent, err := r.cleanupAgent(ctx, co, *w, state.bmi)
	if err != nil {
		return false, err
	}
	if agent != nil {
		return false, r.requestCleanupAgent(ctx, co, w, agent)
	}
	if w.Phase == workerPhaseUnbinding {
		w.Phase = workerPhaseDeleting
	}
	if state.absent {
		return true, nil
	}
	if bmiDeleting(state.bmi) {
		return false, nil
	}
	return false, r.fulfillment.DeleteBareMetalInstance(ctx, w.BareMetalInstance.ID)
}

// ID-less reservations may hide an accepted Create with a lost response. Even
// empty List is not proof of absence. Recover the exact owned name and persist
// its ID at a separate boundary before making any external mutation.
func (r *Reconciler) recoverCleanupBMI(ctx context.Context, co *v1alpha1.ClusterOrder, tenant string, w *v1alpha1.WorkerStatus) error {
	if w.BareMetalInstance.Name == "" {
		return fmt.Errorf("worker %s has no recorded BMI name", w.Name)
	}
	filter := fmt.Sprintf(`this.metadata.labels["%s"] == "%s"`, clusterOrderLabel, co.Name)
	bmis, err := r.fulfillment.ListBareMetalInstances(ctx, filter)
	if err != nil {
		return err
	}
	bmi, err := indexWorkerBMIs(bmis).exactOwnedName(co, tenant, w.BareMetalInstance.Name)
	if err != nil {
		return r.rejectWorkerIdentity(co, err.Error())
	}
	if bmi == nil {
		return fmt.Errorf("worker %s BMI reservation absence is unconfirmed", w.Name)
	}
	candidate := *w
	candidate.BareMetalInstance.ID = bmi.GetId()
	state, err := r.readCleanupBMI(ctx, co, tenant, candidate)
	if err != nil {
		return err
	}
	if state.absent {
		return fmt.Errorf("worker %s BMI vanished during name recovery", w.Name)
	}
	w.BareMetalInstance.ID = candidate.BareMetalInstance.ID
	return nil
}

// Read the complete namespace through the uncached reader: selector/cache
// omission cannot authorize infrastructure deletion. Association is strict and
// includes unlabelled, not-yet-bound Agents matching the recorded BMI NICs.
func (r *Reconciler) cleanupAgent(ctx context.Context, co *v1alpha1.ClusterOrder, w v1alpha1.WorkerStatus, bmi *privatev1.BareMetalInstance) (*unstructured.Unstructured, error) {
	agents := &unstructured.UnstructuredList{}
	agents.SetGroupVersionKind(agentGVK.GroupVersion().WithKind("AgentList"))
	if err := r.apiReader.List(ctx, agents, client.InNamespace(co.Namespace)); err != nil {
		return nil, fmt.Errorf("observing cleanup Agents: %w", err)
	}
	var matched *unstructured.Unstructured
	for i := range agents.Items {
		agent := &agents.Items[i]
		associated := agent.GetLabels()[workerNameLabel] == w.Name
		if !associated && cleanupAgentInScope(agent, co) {
			if err := validateCleanupInventory(agent); err != nil {
				return nil, err
			}
		}
		if !associated && !macsIntersect(extractAgentMACs(agent), nicMACs(bmi)) {
			continue
		}
		if err := verifyAgentBinding(agent, co, &w); err != nil {
			return nil, err
		}
		if tenant := agent.GetAnnotations()[tenantAnnotationKey]; tenant != "" && tenant != tenantOf(co) {
			return nil, fmt.Errorf("agent %s belongs to another tenant", agent.GetName())
		}
		if matched != nil {
			return nil, fmt.Errorf("ambiguous cleanup Agents for worker %s", w.Name)
		}
		matched = agent
	}
	return matched, nil
}

// Scoped inventory that cannot be interpreted is unknown association, never
// evidence that no old Agent exists. Do not reuse the best-effort projection
// parser's silent omission rules for a destructive decision.
func validateCleanupInventory(agent *unstructured.Unstructured) error {
	interfaces, found, err := unstructured.NestedSlice(agent.Object, "status", "inventory", "interfaces")
	if err != nil || !found || len(interfaces) == 0 {
		return fmt.Errorf("cleanup Agent %s inventory is unavailable or malformed", agent.GetName())
	}
	for _, raw := range interfaces {
		iface, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("cleanup Agent %s has a malformed inventory interface", agent.GetName())
		}
		mac, ok := iface["macAddress"].(string)
		if !ok || mac == "" {
			return fmt.Errorf("cleanup Agent %s inventory interface has no MAC", agent.GetName())
		}
	}
	return nil
}

func cleanupAgentInScope(agent *unstructured.Unstructured, co *v1alpha1.ClusterOrder) bool {
	labels := agent.GetLabels()
	return labels[clusterOrderLabel] == co.Name || labels["osac.openshift.io/clusterorder"] == co.Name || labels[infraEnvAgentLabel] == co.Name+infraEnvNameSuffix
}

func (r *Reconciler) requestCleanupAgent(ctx context.Context, co *v1alpha1.ClusterOrder, w *v1alpha1.WorkerStatus, agent *unstructured.Unstructured) error {
	state, _, err := unstructured.NestedString(agent.Object, "status", "debugInfo", "state")
	if err != nil {
		return fmt.Errorf("reading Agent %s detach state: %w", agent.GetName(), err)
	}
	if (state != agentUnbindingState && state != "known-unbound") || !isDetachedKnownUnbound(agent, "known-unbound") {
		if w.Phase != workerPhaseFailed {
			r.checkUnbindingTimeout(co, w, agent, time.Now())
		}
		r.recorder.Eventf(co, nil, corev1.EventTypeNormal, "WorkerCleanupBlocked", "WaitForDetach", "worker %s waits for Agent %s owner-driven detach", w.Name, agent.GetName())
		return nil
	}
	if !agent.GetDeletionTimestamp().IsZero() {
		return nil
	}
	uid := agent.GetUID()
	if uid == "" {
		return fmt.Errorf("agent %s has no UID for safe deletion", agent.GetName())
	}
	// Also protect detach/binding changes between observation and deletion.
	rv := agent.GetResourceVersion()
	return r.Delete(ctx, agent, client.Preconditions{UID: &uid, ResourceVersion: &rv})
}
