// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/controller/assistedservice"
)

const (
	agentInstallAPIVersion = assistedservice.AgentInstallAPIVersion

	agentBareMetalRoleLabel = "agentBareMetal"
	workerNameLabel         = "osac.openshift.io/worker-name"
	infraEnvAgentLabel      = "infraenvs.agent-install.openshift.io"
	agentInstanceTypeLabel  = "osac.openshift.io/instance_type"
	agentNodeSetLabel       = "osac.openshift.io/node-set"
	agentUIDAnnotation      = "osac.openshift.io/agent-uid"

	agentRegistrationTimeout = 30 * time.Minute
	agentRequeueInterval     = 30 * time.Second

	eventReasonAgentCorrelated          = "AgentCorrelated"
	eventReasonAgentRegistrationTimeout = "AgentRegistrationTimeout"
	eventReasonRegistryAccessFailed     = "RegistryAccessFailed"
	eventReasonRegistryAccessTimeout    = "RegistryAccessTimeout"
	eventReasonWorkerReady              = "WorkerReady"
)

var errAgentLeaseChanged = errors.New("agent lease changed before binding")

var agentGVK = schema.GroupVersionKind{
	Group: assistedservice.AgentInstallAPIGroup, Version: agentInstallAPIVersion, Kind: "Agent",
}

// reconcileObservedAgents performs Agent actions against the invocation snapshot:
// it matches and binds unbound Agents, then enforces the registration timeout.
// Phase projection already happened once in observeExistingWorkers; this stage
// never re-derives phases from the Agent list, and after a bind or delete it
// leaves further observation to the next explicit invocation. Status persistence
// remains in the caller.
func (r *Reconciler) reconcileObservedAgents(
	ctx context.Context, co *v1alpha1.ClusterOrder, workers []v1alpha1.WorkerStatus, o *workerObservation,
) ([]v1alpha1.WorkerStatus, ctrl.Result, error) {
	workers = append([]v1alpha1.WorkerStatus(nil), workers...)
	if co.Spec.SupportsAutomaticCaaSEndpoints {
		changed := false
		managementReady, managementOK := workerManagementPublicationReady(co)
		var apiRecordsTime *time.Time
		if r.NetworkingProvisioningEnabled && workerAPIRecordsPublished(co) {
			published := co.Status.APIDNSRecordsPublishedTime.Time
			apiRecordsTime = &published
		}
		for i := range workers {
			w := &workers[i]
			if !eligibleForAgentObservation(*w) || w.Phase != workerPhaseWaitingForAgent ||
				w.BareMetalInstance.ID == "" || !managementOK {
				continue
			}
			proof, ready, err := r.workerNetworkProof(ctx, co, *w, o)
			if err != nil {
				return nil, ctrl.Result{}, err
			}
			if !ready {
				continue
			}
			deadline := registrationDeadline(*w, proof, managementReady, apiRecordsTime,
				r.NetworkingProvisioningEnabled)
			if deadline != nil && (w.RegistrationDeadline == nil || deadline.After(w.RegistrationDeadline.Time)) {
				w.RegistrationDeadline = deadline
				changed = true
			}
		}
		if changed {
			return workers, workerBoundaryRequeue(), nil
		}
	}
	// Expire a waiting worker before considering a newly arrived Agent. This
	// prevents a late registration from being durably recorded and approved
	// after the registration deadline has already elapsed.
	workers = r.checkAgentRegistrationTimeout(ctx, co, workers, time.Now())
	if o.agents != nil {
		before := append([]v1alpha1.WorkerStatus(nil), workers...)
		if _, err := r.matchAndBindAgents(ctx, co, o.agents, workers, r.workerMACResolver(o), o); err != nil {
			return nil, ctrl.Result{}, err
		}
		if !workerSlicesEqual(before, workers) {
			return workers, workerBoundaryRequeue(), nil
		}
	}
	workers = r.checkAgentRegistrationTimeout(ctx, co, workers, time.Now())

	if countWorkersInPhase(workers, workerPhaseWaitingForAgent) > 0 {
		return workers, ctrl.Result{RequeueAfter: agentRequeueInterval}, nil
	}
	return workers, ctrl.Result{}, nil
}

// observeAgentReadiness reports an actual Binding -> Ready transition from the
// single projection pass. Workers are matched by name because the projection may
// have removed entries; a missing previous entry is not a transition.
func (r *Reconciler) observeAgentReadiness(
	ctx context.Context, co *v1alpha1.ClusterOrder, previous, observed []v1alpha1.WorkerStatus,
) {
	for i := range observed {
		if observed[i].Phase != workerPhaseReady {
			continue
		}
		before := workerByName(previous, observed[i].Name)
		if before == nil || before.Phase != workerPhaseBinding {
			continue
		}
		ctrllog.FromContext(ctx).Info("worker ready", "worker", observed[i].Name)
		observeProvisioningDuration(tenantOf(co), observed[i])
		r.recorder.Eventf(co, nil, corev1.EventTypeNormal, eventReasonWorkerReady, "AdvanceWorker",
			"worker %s is ready", observed[i].Name)
	}
}

// matchAndBindAgents correlates unbound Agents to BMIs by MAC and performs late binding.
// It requires a unique unbound compatible candidate in both directions before patching: an
// Agent matching several workers and several Agents matching one worker are both ambiguous and
// bind nothing. Incompatible candidates are returned as an error so the blocker is observable.
// Returns the number of newly bound workers.
func (r *Reconciler) matchAndBindAgents(
	ctx context.Context, co *v1alpha1.ClusterOrder,
	agents *unstructured.UnstructuredList, workers []v1alpha1.WorkerStatus, macs MACResolver,
	o *workerObservation,
) (int, error) {
	log := ctrllog.FromContext(ctx)
	associations := matchUnboundAgents(ctx, co, agents.Items, workers, macs)
	bound := 0
	for i := range workers {
		w := &workers[i]
		association, ok := associations[w.Name]
		if !ok || association.state == agentAbsent {
			continue
		}
		if err := association.err(); err != nil {
			return bound, err
		}
		if association.state == agentAmbiguous {
			log.Info("ambiguous Agent association, skipping bind", "worker", w.Name, "reason", association.reason)
			continue
		}
		if co.Spec.SupportsAutomaticCaaSEndpoints {
			ready, err := r.caasAgentReadyForBinding(ctx, co, w, association.agent, o)
			if err != nil {
				if w.Phase == workerPhaseFailed && w.LastFailureReason == eventReasonRegistryAccessFailed {
					// The terminal probe result is already recorded on the worker.
					// Persist that transition before retrying guest-side cleanup.
					log.Info("deferring registry probe cleanup after terminal failure", "worker", w.Name)
					continue
				}
				return bound, err
			}
			if !ready {
				continue
			}
		}
		if co.Spec.SupportsAutomaticCaaSEndpoints && w.Agent == nil {
			uid := string(association.agent.GetUID())
			if uid == "" {
				return bound, fmt.Errorf("agent %s has no UID for durable worker correlation", association.agent.GetName())
			}
			w.Agent = &v1alpha1.WorkerAgentReference{Name: association.agent.GetName(), UID: uid}
			continue
		}
		if w.Agent != nil && (w.Agent.Name != association.agent.GetName() || w.Agent.UID != string(association.agent.GetUID())) {
			log.Info("recorded Agent identity differs from candidate; skipping bind",
				"worker", w.Name, "recordedAgent", w.Agent.Name, "candidateAgent", association.agent.GetName())
			continue
		}
		if err := r.bindAgent(ctx, co, association.agent, w, o); err != nil {
			if errors.Is(err, errAgentLeaseChanged) {
				log.Info("Agent lease changed before binding, waiting for a fresh observation",
					"agent", association.agent.GetName(), "worker", w.Name)
				continue
			}
			return bound, err
		}
		w.Phase = workerPhaseBinding
		observeCorrelationDuration(tenantOf(co), *w)
		log.Info("agent correlated", "agent", association.agent.GetName(), "worker", w.Name)
		r.recorder.Eventf(co, nil, corev1.EventTypeNormal, eventReasonAgentCorrelated, "CorrelateAgent",
			"agent %s correlated to worker %s", association.agent.GetName(), w.Name)
		bound++
	}
	return bound, nil
}

func (r *Reconciler) caasAgentReadyForBinding(
	ctx context.Context, co *v1alpha1.ClusterOrder, worker *v1alpha1.WorkerStatus,
	agent *unstructured.Unstructured, observation *workerObservation,
) (bool, error) {
	log := ctrllog.FromContext(ctx)
	_, managementReady := workerManagementPublicationReady(co)
	if !managementReady || (r.NetworkingProvisioningEnabled && !workerAPIRecordsPublished(co)) {
		return false, nil
	}
	proof, networkReady, err := r.workerNetworkProof(ctx, co, *worker, observation)
	if err != nil {
		return false, err
	}
	if !networkReady || !proof.bmiReady {
		return false, nil
	}
	if r.NetworkingProvisioningEnabled &&
		(!proof.bindingReady || !agentMatchesWorkerLease(agent, proof.mac, proof.leaseAddress)) {
		return false, nil
	}
	if r.WorkerNetworkConfigGate {
		ready, err := r.workerNetworkConfigReady(ctx, co, agent)
		if err != nil {
			return false, err
		}
		if !ready {
			log.Info("waiting for MAC-driven worker NMStateConfig before Agent approval",
				"worker", worker.Name, "agent", agent.GetName())
			return false, nil
		}
	}
	if r.NetworkingProvisioningEnabled && worker.RegistryProbePrepared {
		image := workerRegistryProbeImage(co, worker)
		if image == "" {
			return false, fmt.Errorf("ClusterOrder has no ClusterVersion release image for worker %s registry probe", worker.Name)
		}
		registryReady, err := r.reconcileWorkerRegistryProbeResult(ctx, co, worker, image)
		if err != nil {
			return false, err
		}
		if !registryReady {
			return false, nil
		}
	}
	return true, nil
}

var nmStateConfigListGVK = schema.GroupVersionKind{
	Group: assistedservice.AgentInstallAPIGroup, Version: agentInstallAPIVersion, Kind: "NMStateConfigList",
}

func (r *Reconciler) workerNetworkConfigReady(
	ctx context.Context, co *v1alpha1.ClusterOrder, agent *unstructured.Unstructured,
) (bool, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(nmStateConfigListGVK)
	reader := r.apiReader
	if reader == nil {
		reader = r.Client
	}
	err := reader.List(ctx, list, client.InNamespace(co.Namespace), client.MatchingLabels{
		legacyClusterOrderLabel: co.Name,
	})
	if err != nil {
		// Older installations may not have the NMStateConfig CRD. Preserve the
		// legacy approval path there; CaaS deployments install the CRD and use
		// the gate.
		if meta.IsNoMatchError(err) {
			return true, nil
		}
		return false, err
	}
	wanted := co.Name + "-" + agent.GetName() + "-network"
	if agent.GetUID() == "" {
		return false, nil
	}
	for i := range list.Items {
		if list.Items[i].GetName() == wanted &&
			list.Items[i].GetAnnotations()[agentUIDAnnotation] == string(agent.GetUID()) {
			return true, nil
		}
	}
	return false, nil
}

// bindAgent sets the Agent's clusterDeploymentName (late binding), marks it approved,
// and applies labels for NodePool selection and role identification.
func (r *Reconciler) bindAgent(
	ctx context.Context, co *v1alpha1.ClusterOrder,
	agent *unstructured.Unstructured, worker *v1alpha1.WorkerStatus,
	observations ...*workerObservation,
) error {
	reader := r.apiReader
	if reader == nil {
		reader = r.Client
	}
	latest := &unstructured.Unstructured{}
	latest.SetGroupVersionKind(agentGVK)
	if err := reader.Get(ctx, client.ObjectKeyFromObject(agent), latest); err != nil {
		return err
	}
	if latest.GetUID() != agent.GetUID() {
		return fmt.Errorf("agent %s identity changed before binding", agent.GetName())
	}
	if err := verifyAgentBinding(latest, co, worker); err != nil {
		return err
	}
	if co.Spec.SupportsAutomaticCaaSEndpoints && r.NetworkingProvisioningEnabled && len(observations) > 0 {
		var proof workerNetworkProof
		var ready bool
		var err error
		if r.fulfillment == nil || r.apiReader == nil {
			proof, ready, err = r.workerNetworkProof(ctx, co, *worker, observations[0])
		} else {
			proof, ready, err = r.freshWorkerNetworkProof(ctx, co, *worker)
		}
		if err != nil {
			return err
		}
		if !ready || !proof.bindingReady || !agentMatchesWorkerLease(latest, proof.mac, proof.leaseAddress) {
			return errAgentLeaseChanged
		}
	}
	// CAP-Agent may have claimed the established worker since our discovery
	// snapshot. Its deployment/ignition binding is authoritative; do not undo it.
	if isCAPAgentWorkerBinding(latest, co, worker.Name) {
		return nil
	}
	base := latest.DeepCopy()

	// CAP-Agent owns the Agent -> ClusterDeployment handoff.  OSAC must not
	// pre-bind the Agent: CAP-Agent only selects approved, validated Agents that
	// are still unbound, then writes the HostedControlPlane ClusterDeployment
	// reference and AgentMachine ownership itself. Legacy (non-CAP) flows retain
	// the direct ClusterDeployment binding.
	if !co.Spec.SupportsAutomaticCaaSEndpoints {
		clusterDeploymentNamespace := co.Namespace
		if ref := co.Status.ClusterReference; ref != nil && ref.Namespace != "" && ref.HostedClusterName != "" {
			clusterDeploymentNamespace = ref.Namespace + "-" + ref.HostedClusterName
		}
		if err := unstructured.SetNestedMap(latest.Object, map[string]interface{}{
			jsonNameField: co.Name,
			"namespace":   clusterDeploymentNamespace,
		}, "spec", "clusterDeploymentName"); err != nil {
			return fmt.Errorf("setting agent clusterDeploymentName: %w", err)
		}
	}

	if err := unstructured.SetNestedField(latest.Object, true, "spec", "approved"); err != nil {
		return fmt.Errorf("setting agent approved: %w", err)
	}

	labels := latest.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	workerName := ""
	if worker != nil {
		workerName = worker.Name
		labels[agentNodeSetLabel] = worker.NodeSet
		if worker.InstanceType != "" {
			labels[agentInstanceTypeLabel] = worker.InstanceType
		}
	}
	labels[workerNameLabel] = workerName
	labels[agentBareMetalRoleLabel] = literalTrue
	labels[clusterOrderLabel] = co.Name
	labels[legacyClusterOrderLabel] = co.Name
	latest.SetLabels(labels)

	return r.Patch(ctx, latest, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

// verifyAgentBinding prevents reassigning an Agent, including when its binding
// changed after the reconcile snapshot or during an optimistic-lock conflict.
func verifyAgentBinding(agent *unstructured.Unstructured, co *v1alpha1.ClusterOrder, worker *v1alpha1.WorkerStatus) error {
	if worker == nil {
		return fmt.Errorf("agent %s has no eligible worker in the cluster namespace", agent.GetName())
	}
	if worker.Agent != nil && (worker.Agent.Name != agent.GetName() || worker.Agent.UID != string(agent.GetUID())) {
		return fmt.Errorf("agent %s does not match the recorded Agent identity for worker %s", agent.GetName(), worker.Name)
	}
	return agentBindingConflict(agent, co, worker.Name)
}

// agentBindingConflict reports why an Agent may not be associated with the named worker, or nil
// when the ownership/binding contract permits it. Observation, late binding and cleanup share it
// so every consumer applies the same namespace, cluster and binding scope rules.
func agentBindingConflict(agent *unstructured.Unstructured, co *v1alpha1.ClusterOrder, workerName string) error {
	if agent.GetNamespace() != co.Namespace {
		return fmt.Errorf("agent %s is outside cluster namespace %s", agent.GetName(), co.Namespace)
	}
	labels := agent.GetLabels()
	for _, key := range []string{clusterOrderLabel, legacyClusterOrderLabel} {
		if labels[key] != "" && labels[key] != co.Name {
			return fmt.Errorf("agent %s belongs to another cluster", agent.GetName())
		}
	}
	if labels[workerNameLabel] != "" && labels[workerNameLabel] != workerName {
		return fmt.Errorf("agent %s belongs to another worker", agent.GetName())
	}
	name, _, _ := unstructured.NestedString(agent.Object, "spec", "clusterDeploymentName", jsonNameField)
	namespace, _, _ := unstructured.NestedString(agent.Object, "spec", "clusterDeploymentName", "namespace")
	if (name != "" && name != co.Name) || (namespace != "" && namespace != co.Namespace) {
		if !isCAPAgentWorkerBinding(agent, co, workerName) {
			return fmt.Errorf("agent %s is bound to another cluster deployment", agent.GetName())
		}
	}
	return nil
}

// isCAPAgentWorkerBinding recognizes the exact HyperShift handoff of an already
// OSAC-associated worker. CAP-Agent's ClusterDeployment has the HostedCluster's
// name and lives in its hosted-control-plane namespace, not the Agent namespace.
// A reference alone is never ownership evidence for initial MAC discovery.
func isCAPAgentWorkerBinding(agent *unstructured.Unstructured, co *v1alpha1.ClusterOrder, workerName string) bool {
	ref := co.Status.ClusterReference
	if ref == nil || ref.Namespace == "" || ref.HostedClusterName == "" || workerName == "" {
		return false
	}
	labels := agent.GetLabels()
	if labels[workerNameLabel] != workerName ||
		(labels[clusterOrderLabel] != co.Name && labels[legacyClusterOrderLabel] != co.Name) {
		return false
	}
	name, _, _ := unstructured.NestedString(agent.Object, "spec", "clusterDeploymentName", jsonNameField)
	namespace, _, _ := unstructured.NestedString(agent.Object, "spec", "clusterDeploymentName", "namespace")
	return name == ref.HostedClusterName && namespace == ref.Namespace+"-"+ref.HostedClusterName
}

// checkAgentRegistrationTimeout transitions workers stuck in WaitingForAgent past the timeout
// to Failed with reason AgentRegistrationTimeout. The clock is the durable per-attempt origin,
// never the parent ClusterOrder age or an unrelated failure timestamp. Emitting a VM worker or
// an ID-less reservation is not eligible, so it is never timed out. now is captured at the
// orchestration boundary and passed in for deterministic policy evaluation.
func (r *Reconciler) checkAgentRegistrationTimeout(
	ctx context.Context, co *v1alpha1.ClusterOrder, workers []v1alpha1.WorkerStatus, now time.Time,
) []v1alpha1.WorkerStatus {
	log := ctrllog.FromContext(ctx)

	for i := range workers {
		w := &workers[i]
		if !eligibleForAgentObservation(*w) || w.Phase != workerPhaseWaitingForAgent {
			continue
		}
		if co.Spec.SupportsAutomaticCaaSEndpoints {
			if _, ready := workerManagementPublicationReady(co); !ready ||
				(r.NetworkingProvisioningEnabled && !workerAPIRecordsPublished(co)) {
				continue
			}
		}
		var deadline time.Time
		if co.Spec.SupportsAutomaticCaaSEndpoints {
			if w.RegistrationDeadline == nil {
				continue
			}
			deadline = w.RegistrationDeadline.Time
		} else {
			if w.AttemptStartedAt == nil {
				continue
			}
			deadline = w.AttemptStartedAt.Add(agentRegistrationTimeout)
		}
		if now.Before(deadline) {
			continue
		}
		w.Phase = workerPhaseFailed
		w.LastFailureReason = eventReasonAgentRegistrationTimeout
		w.LastFailureMessage = fmt.Sprintf("no agent registered within %s", agentRegistrationTimeout)
		if r.NetworkingProvisioningEnabled && w.RegistryAccessReady != nil && w.RegistryAccessReady.Status == metav1.ConditionFalse &&
			w.RegistryAccessReady.Reason == "ProbePending" {
			w.LastFailureReason = eventReasonRegistryAccessTimeout
			w.LastFailureMessage = fmt.Sprintf("worker registry probe result was unavailable before timeout: %s",
				w.RegistryAccessReady.Message)
		}
		failTime := metav1.NewTime(now)
		w.LastFailureTime = &failTime
		observeProvisioningFailure(tenantOf(co), *w)
		log.Info("worker registration gate timed out", "worker", w.Name, "reason", w.LastFailureReason)
		r.recorder.Eventf(co, nil, corev1.EventTypeWarning, w.LastFailureReason, "CheckTimeout",
			"worker %s: %s", w.Name, w.LastFailureMessage)
	}
	return workers
}
