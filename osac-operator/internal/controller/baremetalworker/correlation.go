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
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const (
	workerPhaseWaitingForAgent = "WaitingForAgent"
	workerPhaseBinding         = "Binding"
	workerPhaseReady           = "Ready"
	workerPhaseFailed          = "Failed"

	agentBareMetalRoleLabel = "agentBareMetal"
	workerNameLabel         = "osac.openshift.io/worker-name"

	agentRegistrationTimeout = 30 * time.Minute
	agentRequeueInterval     = 30 * time.Second

	eventReasonAgentCorrelated          = "AgentCorrelated"
	eventReasonAgentRegistrationTimeout = "AgentRegistrationTimeout"
	eventReasonWorkerReady              = "WorkerReady"
)

const agentInstallAPIVersion = "v1beta1"

var agentGVK = schema.GroupVersionKind{
	Group: "agent-install.openshift.io", Version: agentInstallAPIVersion, Kind: "Agent",
}

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

// reconcileAgent converges every eligible worker from the shared Agent snapshot,
// then matches and binds unbound Agents. Status persistence remains in the caller.
func (r *Reconciler) reconcileAgent(
	ctx context.Context, co *v1alpha1.ClusterOrder, workers []v1alpha1.WorkerStatus,
	agents *unstructured.UnstructuredList,
) ([]v1alpha1.WorkerStatus, ctrl.Result, error) {
	observed := projectAgentWorkerPhases(ctx, workers, agents, r.macResolver)
	r.observeAgentReadiness(ctx, co, workers, observed)
	workers = observed
	initializeReadySince(workers)
	r.matchAndBindAgents(ctx, co, agents, workers)
	workers = r.checkAgentRegistrationTimeout(ctx, co, workers)

	if countWorkersInPhase(workers, workerPhaseWaitingForAgent) > 0 {
		return workers, ctrl.Result{RequeueAfter: agentRequeueInterval}, nil
	}
	return workers, ctrl.Result{}, nil
}

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

func observeProvisioningDuration(tenant string, w v1alpha1.WorkerStatus) {
	workerProvisioningDuration.WithLabelValues(tenant, workerTypeBareMetal, w.InstanceType).
		Observe(time.Since(w.CreationTimestamp.Time).Seconds())
}

// projectAgentWorkerPhases is observation only: it neither checks BMI existence,
// patches Agents, nor modifies the input workers or their identity/retry fields.
func projectAgentWorkerPhases(
	ctx context.Context, workers []v1alpha1.WorkerStatus,
	agents *unstructured.UnstructuredList, hostMACs MACResolver,
) []v1alpha1.WorkerStatus {
	result := append([]v1alpha1.WorkerStatus(nil), workers...)
	for i := range result {
		w := &result[i]
		if !eligibleForAgentObservation(*w) {
			continue
		}
		w.Phase = deriveWorkerPhase(findAgentForWorker(ctx, agents, w.BareMetalInstance.ID, w.Name, hostMACs), w.Name)
	}
	return result
}

func eligibleForAgentObservation(w v1alpha1.WorkerStatus) bool {
	return w.Kind == workerKindBMI && w.BareMetalInstance.ID != "" &&
		w.Phase != workerPhaseFailed && w.Phase != workerPhaseUnbinding && w.Phase != workerPhaseDeleting
}

func initializeReadySince(workers []v1alpha1.WorkerStatus) {
	now := metav1.Now()
	for i := range workers {
		if eligibleForAgentObservation(workers[i]) && workers[i].Phase == workerPhaseReady && workers[i].ReadySince == nil {
			workers[i].ReadySince = &now
		}
	}
}

// Preserve the late stage's existing Binding -> Ready emissions. Early rebuild
// observation and interrupted-status recovery remain quiet, as before this refactor.
func (r *Reconciler) observeAgentReadiness(
	ctx context.Context, co *v1alpha1.ClusterOrder, previous, observed []v1alpha1.WorkerStatus,
) {
	for i := range observed {
		if previous[i].Phase != workerPhaseBinding || observed[i].Phase != workerPhaseReady {
			continue
		}
		ctrllog.FromContext(ctx).Info("worker ready", "worker", observed[i].Name)
		observeProvisioningDuration(tenantOf(co), observed[i])
		r.recorder.Eventf(co, nil, corev1.EventTypeNormal, eventReasonWorkerReady, "AdvanceWorker",
			"worker %s is ready", observed[i].Name)
	}
}

// matchAndBindAgents correlates unbound Agents to BMIs by MAC and performs late binding.
// Returns the number of newly bound workers.
func (r *Reconciler) matchAndBindAgents(
	ctx context.Context, co *v1alpha1.ClusterOrder,
	agents *unstructured.UnstructuredList, workers []v1alpha1.WorkerStatus,
) int {
	bound := 0
	for idx := range agents.Items {
		agent := &agents.Items[idx]
		if agent.GetLabels()[workerNameLabel] != "" {
			continue
		}
		if r.matchAndBindAgent(ctx, co, agent, workers) {
			bound++
		}
	}
	return bound
}

func observeCorrelationDuration(tenant string, w v1alpha1.WorkerStatus) {
	workerCorrelationDuration.WithLabelValues(tenant, workerTypeBareMetal, w.InstanceType).
		Observe(time.Since(w.CreationTimestamp.Time).Seconds())
}

// matchAndBindAgent matches a single Agent to a BMI by MAC, then binds it.
// Returns true if a worker was successfully bound.
func (r *Reconciler) matchAndBindAgent(
	ctx context.Context, co *v1alpha1.ClusterOrder,
	agent *unstructured.Unstructured, workers []v1alpha1.WorkerStatus,
) bool {
	log := ctrllog.FromContext(ctx)

	workerName, isAmbiguous := matchAgentToBMI(ctx, agent, workers, r.macResolver)
	if isAmbiguous {
		log.Error(nil, "multiple BMIs match agent MAC, skipping bind", "agent", agent.GetName())
		return false
	}
	if workerName == "" {
		return false
	}

	worker := workerByName(workers, workerName)
	if err := r.bindAgent(ctx, co, agent, worker); err != nil {
		log.Error(err, "binding agent failed", "agent", agent.GetName(), "worker", workerName)
		return false
	}

	setWorkerPhase(workers, workerName, workerPhaseBinding)
	if worker != nil {
		observeCorrelationDuration(tenantOf(co), *worker)
	}
	log.Info("agent correlated", "agent", agent.GetName(), "worker", workerName)
	r.recorder.Eventf(co, nil, corev1.EventTypeNormal, eventReasonAgentCorrelated, "CorrelateAgent",
		"agent %s correlated to worker %s", agent.GetName(), workerName)
	return true
}

// bindAgent sets the Agent's clusterDeploymentName (late binding), marks it approved,
// and applies labels for NodePool selection and role identification.
func (r *Reconciler) bindAgent(
	ctx context.Context, co *v1alpha1.ClusterOrder,
	agent *unstructured.Unstructured, worker *v1alpha1.WorkerStatus,
) error {
	reader := r.apiReader
	if reader == nil {
		reader = r.Client
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &unstructured.Unstructured{}
		latest.SetGroupVersionKind(agentGVK)
		if err := reader.Get(ctx, client.ObjectKeyFromObject(agent), latest); err != nil {
			return err
		}
		if err := verifyAgentBinding(latest, co, worker); err != nil {
			return err
		}
		base := latest.DeepCopy()

		if err := unstructured.SetNestedMap(latest.Object, map[string]interface{}{
			"name":      co.Name,
			"namespace": co.Namespace,
		}, "spec", "clusterDeploymentName"); err != nil {
			return fmt.Errorf("setting agent clusterDeploymentName: %w", err)
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
		labels[agentBareMetalRoleLabel] = "true"
		labels[clusterOrderLabel] = co.Name
		labels["osac.openshift.io/clusterorder"] = co.Name
		latest.SetLabels(labels)

		return r.Patch(ctx, latest, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	})
}

// verifyAgentBinding prevents reassigning an Agent, including when its binding
// changed after the reconcile snapshot or during an optimistic-lock conflict.
func verifyAgentBinding(agent *unstructured.Unstructured, co *v1alpha1.ClusterOrder, worker *v1alpha1.WorkerStatus) error {
	if worker == nil || agent.GetNamespace() != co.Namespace {
		return fmt.Errorf("agent %s has no eligible worker in the cluster namespace", agent.GetName())
	}
	labels := agent.GetLabels()
	for _, key := range []string{clusterOrderLabel, "osac.openshift.io/clusterorder"} {
		if labels[key] != "" && labels[key] != co.Name {
			return fmt.Errorf("agent %s belongs to another cluster", agent.GetName())
		}
	}
	if labels[workerNameLabel] != "" && labels[workerNameLabel] != worker.Name {
		return fmt.Errorf("agent %s belongs to another worker", agent.GetName())
	}
	name, _, _ := unstructured.NestedString(agent.Object, "spec", "clusterDeploymentName", "name")
	namespace, _, _ := unstructured.NestedString(agent.Object, "spec", "clusterDeploymentName", "namespace")
	if (name != "" && name != co.Name) || (namespace != "" && namespace != co.Namespace) {
		return fmt.Errorf("agent %s is bound to another cluster deployment", agent.GetName())
	}
	return nil
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

// checkAgentRegistrationTimeout transitions workers stuck in WaitingForAgent past the timeout
// to Failed with reason AgentRegistrationTimeout.
func (r *Reconciler) checkAgentRegistrationTimeout(
	ctx context.Context, co *v1alpha1.ClusterOrder, workers []v1alpha1.WorkerStatus,
) []v1alpha1.WorkerStatus {
	log := ctrllog.FromContext(ctx)
	now := time.Now()

	for i := range workers {
		w := &workers[i]
		if !eligibleForAgentObservation(*w) || w.Phase != workerPhaseWaitingForAgent {
			continue
		}
		phaseStart := r.workerPhaseStartTime(co, w.Name)
		if phaseStart.IsZero() {
			continue
		}
		if now.Sub(phaseStart) < agentRegistrationTimeout {
			continue
		}
		w.Phase = workerPhaseFailed
		w.LastFailureReason = eventReasonAgentRegistrationTimeout
		w.LastFailureMessage = fmt.Sprintf("no agent registered within %s", agentRegistrationTimeout)
		failTime := metav1.NewTime(now)
		w.LastFailureTime = &failTime
		observeProvisioningFailure(tenantOf(co), *w)
		log.Info("agent registration timeout", "worker", w.Name)
		r.recorder.Eventf(co, nil, corev1.EventTypeWarning, eventReasonAgentRegistrationTimeout, "CheckTimeout",
			"worker %s: no agent registered within %s", w.Name, agentRegistrationTimeout)
	}
	return workers
}

// workerPhaseStartTime returns when a worker entered its current phase by checking the
// ClusterOrder's existing status.workers. For WaitingForAgent workers that were just
// transitioned, uses the resource version change time approximation.
func (r *Reconciler) workerPhaseStartTime(co *v1alpha1.ClusterOrder, workerName string) time.Time {
	for _, w := range co.Status.Workers {
		if w.Name == workerName && w.Phase == workerPhaseWaitingForAgent {
			if w.LastFailureTime != nil {
				return w.LastFailureTime.Time
			}
			return co.CreationTimestamp.Time
		}
	}
	return time.Time{}
}

// reconcileNodePoolReplicas preserves the requested bare-metal capacity while Agents are
// still provisioning. NodePools are discovered by the same label selector the ClusterOrder
// controller uses.
func (r *Reconciler) reconcileNodePoolReplicas(
	ctx context.Context, co *v1alpha1.ClusterOrder,
) (ctrl.Result, error) {
	clusterRef := co.Status.ClusterReference
	if clusterRef == nil || clusterRef.Namespace == "" {
		return ctrl.Result{}, nil
	}

	nodePools, err := r.listNodePools(ctx, clusterRef.Namespace, co.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(nodePools.Items) == 0 {
		ctrllog.FromContext(ctx).Info("no NodePool found, requeuing")
		return ctrl.Result{RequeueAfter: agentRequeueInterval}, nil
	}

	replicas := requestedBareMetalWorkersByNodeSet(co)
	return ctrl.Result{}, r.patchNodePoolReplicas(ctx, nodePools, replicas)
}

// requestedBareMetalWorkersByNodeSet preserves each requested NodePool capacity while
// BMaaS workers are still provisioning and their Agents have not registered yet.
func requestedBareMetalWorkersByNodeSet(co *v1alpha1.ClusterOrder) map[string]int64 {
	replicas := make(map[string]int64)
	for _, request := range co.Spec.NodeRequests {
		if request.IsBareMetal() {
			replicas[request.NodeSet] = int64(request.NumberOfNodes)
		}
	}
	return replicas
}

func (r *Reconciler) listNodePools(ctx context.Context, namespace, clusterOrderName string) (*unstructured.UnstructuredList, error) {
	nodePoolList := &unstructured.UnstructuredList{}
	nodePoolList.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "hypershift.openshift.io", Version: "v1beta1", Kind: "NodePoolList",
	})
	if err := r.List(ctx, nodePoolList,
		client.InNamespace(namespace),
		client.MatchingLabels{"osac.openshift.io/clusterorder": clusterOrderName},
	); err != nil {
		return nil, fmt.Errorf("listing nodepools for %s: %w", clusterOrderName, err)
	}
	return nodePoolList, nil
}

func (r *Reconciler) patchNodePoolReplicas(
	ctx context.Context, nodePools *unstructured.UnstructuredList, replicasByNodeSet map[string]int64,
) error {
	log := ctrllog.FromContext(ctx)
	for idx := range nodePools.Items {
		np := &nodePools.Items[idx]
		nodeSet := np.GetLabels()[agentNodeSetLabel]
		replicas, ok := replicasByNodeSet[nodeSet]
		if !ok {
			continue
		}
		currentReplicas, _, _ := unstructured.NestedInt64(np.Object, "spec", "replicas")
		if currentReplicas == replicas {
			continue
		}
		patch := np.DeepCopy()
		if err := unstructured.SetNestedField(patch.Object, replicas, "spec", "replicas"); err != nil {
			return fmt.Errorf("setting nodepool replicas: %w", err)
		}
		if err := r.Patch(ctx, patch, client.MergeFrom(np)); err != nil {
			return fmt.Errorf("patching nodepool %s replicas: %w", np.GetName(), err)
		}
		log.Info("updated NodePool replicas", "nodepool", np.GetName(), "replicas", replicas)
	}
	return nil
}

// updateWorkerStatusWithAgent patches status.workers, aggregate counts, and the
// WorkersFailed condition on the ClusterOrder, re-reading and retrying on conflict.
// It also resets attemptCount for workers that have been Ready for MinHealthyDuration.
func (r *Reconciler) updateWorkerStatusWithAgent(
	ctx context.Context, co *v1alpha1.ClusterOrder, workers []v1alpha1.WorkerStatus,
) error {
	log := ctrllog.FromContext(ctx)
	resetHealthyWorkers(log, co, workers)
	return r.patchStatusWithRetry(ctx, co, func(latest *v1alpha1.ClusterOrder) {
		latest.Status.Workers = workers
		desired, current, ready := computeWorkerAggregates(workers)
		latest.Status.DesiredWorkers = &desired
		latest.Status.CurrentWorkers = &current
		latest.Status.ReadyWorkers = &ready

		failedMsg := FormatWorkersFailed(workers)
		switch {
		case failedMsg != "":
			latest.SetStatusCondition(v1alpha1.ConditionWorkersFailed,
				metav1.ConditionTrue, failedMsg, reasonWorkersFailed)
		case apimeta.IsStatusConditionTrue(latest.Status.Conditions, v1alpha1.ConditionWorkersFailed):
			latest.SetStatusCondition(v1alpha1.ConditionWorkersFailed,
				metav1.ConditionFalse, "all workers healthy", reasonWorkersFailedCleared)
		}
	})
}

// resetHealthyWorkers resets attemptCount for workers that have been Ready for at least
// MinHealthyDuration, clearing their failure history.
func resetHealthyWorkers(log logr.Logger, co *v1alpha1.ClusterOrder, workers []v1alpha1.WorkerStatus) {
	now := time.Now()
	for i := range workers {
		w := &workers[i]
		if w.Phase != workerPhaseReady || w.AttemptCount == 0 || w.ReadySince == nil {
			continue
		}
		if now.Sub(w.ReadySince.Time) < minHealthyDuration {
			continue
		}
		log.Info("worker healthy for MinHealthyDuration, resetting attemptCount",
			"worker", w.Name, "previousAttempts", w.AttemptCount)
		w.AttemptCount = 0
		w.LastFailureReason = ""
		w.LastFailureMessage = ""
		w.LastFailureTime = nil
		w.NextRetryTime = nil
	}
}

func computeWorkerAggregates(workers []v1alpha1.WorkerStatus) (desired, current, ready int32) {
	for _, w := range workers {
		desired++
		switch w.Phase {
		case workerPhaseProvisioning, workerPhaseWaitingForAgent, workerPhaseBinding, workerPhaseReady,
			workerPhaseUnbinding, workerPhaseDeleting:
			current++
		}
		if w.Phase == workerPhaseReady {
			ready++
		}
	}
	return
}

func countWorkersInPhase(workers []v1alpha1.WorkerStatus, phase string) int {
	n := 0
	for i := range workers {
		if workers[i].Phase == phase {
			n++
		}
	}
	return n
}

func setWorkerPhase(workers []v1alpha1.WorkerStatus, name, phase string) {
	for i := range workers {
		if workers[i].Name == name {
			workers[i].Phase = phase
			return
		}
	}
}

func workerByName(workers []v1alpha1.WorkerStatus, name string) *v1alpha1.WorkerStatus {
	for i := range workers {
		if workers[i].Name == name {
			return &workers[i]
		}
	}
	return nil
}
