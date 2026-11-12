// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package capagent

import (
	"context"
	"fmt"
	"sort"
	"time"

	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

const agentMachineFinalizer = "agentmachine.agent-install.openshift.io/deprovision"

// AgentMachineReconciler claims Agents for Machines and releases them before deletion.
// Its Machine annotation holds deletion until Assisted reports the host as unbound.
type AgentMachineReconciler struct {
	Client     client.Client
	APIReader  client.Reader
	Scope      kube.Scope
	HoldDetach func(context.Context, metav1.Object) (bool, error)
}

// Reconcile validates the owning Machine and recovers the AgentMachine's Agent claim.
// It then releases a deleting Machine or AgentMachine, claims a host, or reports readiness.
// The owning Machine UID is checked before any claim or release mutation.
func (r *AgentMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	consumer := &agentv1.AgentMachine{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, consumer); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	owner := kube.Owner(consumer, "Machine")
	if owner == nil {
		if consumer.DeletionTimestamp != nil {
			return ctrl.Result{}, kube.RemoveFinalizer(ctx, r.Client, consumer, agentMachineFinalizer)
		}

		ctrl.LoggerFrom(ctx).V(4).Info("waiting for Machine controller reference", "requeueAfter", time.Second)

		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	machine := &capiv1.Machine{}
	if err := r.APIReader.Get(
		ctx,
		client.ObjectKey{Namespace: consumer.Namespace, Name: owner.Name},
		machine,
	); err != nil {
		if apierrors.IsNotFound(err) && consumer.DeletionTimestamp != nil {
			return ctrl.Result{}, kube.RemoveFinalizer(ctx, r.Client, consumer, agentMachineFinalizer)
		}

		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !kube.MatchesController(consumer, machine, capiv1.GroupVersion.WithKind("Machine")) {
		if consumer.DeletionTimestamp != nil {
			return ctrl.Result{}, kube.RemoveFinalizer(ctx, r.Client, consumer, agentMachineFinalizer)
		}

		ctrl.LoggerFrom(ctx).V(4).Info("ignoring AgentMachine without its original Machine", "expectedMachineUID", owner.UID)

		return ctrl.Result{}, nil
	}

	if consumer.Annotations[capiv1.PausedAnnotation] == "true" {
		if consumer.DeletionTimestamp != nil {
			return r.release(ctx, consumer, machine, nil, nil)
		}

		return ctrl.Result{}, nil
	}

	if machine.DeletionTimestamp != nil || consumer.DeletionTimestamp != nil {
		return r.deleteMachine(ctx, consumer, machine)
	}

	if consumer.Status.Ready {
		_, _, _, err := r.bootstrap(ctx, machine)
		return ctrl.Result{}, err
	}

	return r.reconcileClaim(ctx, consumer, machine)
}

// reconcileClaim recovers the original scoped Agent and publishes native acquisition status.
func (r *AgentMachineReconciler) reconcileClaim(
	ctx context.Context,
	consumer *agentv1.AgentMachine,
	machine *capiv1.Machine,
) (ctrl.Result, error) {
	// The HostedControlPlane identifies the served Agent namespace.
	cluster, cp, err := controlPlane(
		ctx,
		r.APIReader,
		consumer.GetNamespace(),
		machine.Spec.ClusterName,
	)
	if err != nil {
		return ctrl.Result{}, err
	}

	if cluster == nil || cp == nil {
		ctrl.LoggerFrom(ctx).V(4).Info("waiting for Cluster and control plane", "requeueAfter", time.Second)
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	if cp.Spec.Platform.Agent == nil {
		return ctrl.Result{}, nil
	}

	agentNamespace := cp.Spec.Platform.Agent.AgentNamespace
	if !r.Scope.Contains(agentNamespace) {
		return ctrl.Result{}, nil
	}

	// Recover persisted claims before deciding whether acquisition or release is needed.
	agent, err := r.associatedAgent(ctx, consumer, agentNamespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	if consumer.Status.AgentRef != nil && consumer.Status.AgentRef.Name != "" &&
		(agent == nil || !agentClaimMatches(agent, consumer)) {
		return ctrl.Result{}, fmt.Errorf("recorded Agent is outside this AgentMachine's current claim")
	}

	if err = r.ensureDeletionProtection(ctx, consumer, machine); err != nil {
		return ctrl.Result{}, err
	}

	if agent == nil {
		return r.claimAgent(ctx, consumer, machine, cluster, agentNamespace)
	}

	if _, _, _, err := r.bootstrap(ctx, machine); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, r.reportAgent(ctx, consumer, agent)
}

// ensureDeletionProtection establishes the Machine hook before holding provider deletion
// or claiming a host. The AgentMachine finalizer follows a successful hook write.
func (r *AgentMachineReconciler) ensureDeletionProtection(
	ctx context.Context,
	consumer *agentv1.AgentMachine, machine *capiv1.Machine,
) error {
	if _, exists := machine.GetAnnotations()[kube.MachineHook]; !exists {
		before := machine.DeepCopy()

		metav1.SetMetaDataAnnotation(&machine.ObjectMeta, kube.MachineHook, "")

		if err := r.Client.Patch(
			ctx,
			machine,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}),
		); err != nil {
			return err
		}
	}

	return kube.AddFinalizer(ctx, r.Client, consumer, agentMachineFinalizer)
}

// reportAgent publishes native CAP-Agent conditions for the original persisted claim.
func (r *AgentMachineReconciler) reportAgent(
	ctx context.Context,
	consumer *agentv1.AgentMachine,
	agent *assistedv1.Agent,
) error {
	if agent != nil {
		providerID := "agent://" + agent.Name
		if consumer.Spec.ProviderID == nil || *consumer.Spec.ProviderID != providerID {
			before := consumer.DeepCopy()
			consumer.Spec.ProviderID = &providerID
			if err := r.Client.Patch(
				ctx,
				consumer,
				client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}),
			); err != nil {
				return err
			}
		}
	}

	next := consumer.DeepCopy()
	if agent == nil {
		conditions.MarkFalse(
			next,
			agentv1.AgentReservedCondition,
			agentv1.NoSuitableAgentsReason,
			capiv1.ConditionSeverityWarning,
			"",
		)
	} else {
		next.Status.AgentRef = &agentv1.AgentReference{Name: agent.Name, Namespace: agent.Namespace}
		conditions.MarkTrue(next, agentv1.AgentReservedCondition)
	}

	for _, pair := range []struct {
		target capiv1.ConditionType
		source conditionsv1.ConditionType
	}{
		{agentv1.AgentSpecSyncedCondition, assistedv1.SpecSyncedCondition},
		{agentv1.AgentValidatedCondition, assistedv1.ValidatedCondition},
		{agentv1.AgentRequirementsMetCondition, assistedv1.RequirementsMetCondition},
		{agentv1.InstalledCondition, assistedv1.InstalledCondition},
	} {
		setAgentCondition(next, agent, pair.target, pair.source)
	}

	conditions.SetSummary(next, conditions.WithConditions(
		agentv1.AgentReservedCondition,
		agentv1.AgentSpecSyncedCondition,
		agentv1.AgentValidatedCondition,
		agentv1.AgentRequirementsMetCondition,
		agentv1.InstalledCondition,
	), conditions.WithStepCounter())

	next.Status.Ready = conditions.IsTrue(next, capiv1.ReadyCondition)
	if equality.Semantic.DeepEqual(consumer.Status, next.Status) {
		return nil
	}

	return r.Client.Status().Patch(ctx, next, client.MergeFromWithOptions(consumer, client.MergeFromWithOptimisticLock{}))
}

// setAgentCondition carries Assisted's failure details with CAP-Agent's native severity.
func setAgentCondition(
	target *agentv1.AgentMachine,
	agent *assistedv1.Agent,
	kind capiv1.ConditionType,
	source conditionsv1.ConditionType,
) {
	if agent == nil {
		conditions.MarkFalse(
			target,
			kind,
			agentv1.AgentNotYetFoundReason,
			capiv1.ConditionSeverityInfo,
			"Agent not yet reserved",
		)

		return
	}

	condition := conditionsv1.FindStatusCondition(agent.Status.Conditions, source)

	severity := capiv1.ConditionSeverityError
	if source == assistedv1.InstalledCondition {
		severity = capiv1.ConditionSeverityInfo
	}

	if condition == nil {
		conditions.MarkFalse(target, kind, "", severity, "")
		return
	}

	if condition.Status == corev1.ConditionTrue {
		conditions.MarkTrue(target, kind)
		return
	}

	if condition.Reason == assistedv1.InstallationFailedReason {
		severity = capiv1.ConditionSeverityError
	}

	conditions.MarkFalse(target, kind, condition.Reason, severity, "%s", condition.Message)
}

// deleteMachine recovers the scoped claim without requiring a live control plane.
func (r *AgentMachineReconciler) deleteMachine(
	ctx context.Context,
	consumer *agentv1.AgentMachine,
	machine *capiv1.Machine,
) (ctrl.Result, error) {
	condition := conditions.Get(machine, capiv1.PreTerminateDeleteHookSucceededCondition)
	// CAPI must reach the pre-terminate hook before CAP-Agent starts releasing the claim.
	if condition == nil {
		return ctrl.Result{}, nil
	}

	// A completed hook leaves only this provider's finalizer to remove.
	if condition.Status == corev1.ConditionTrue {
		return ctrl.Result{}, kube.RemoveFinalizer(ctx, r.Client, consumer, agentMachineFinalizer)
	}

	if ref := consumer.Status.AgentRef; ref != nil && ref.Name != "" && !r.Scope.Contains(ref.Namespace) {
		return ctrl.Result{}, nil
	}

	var agent *assistedv1.Agent

	for _, namespace := range r.Scope.AgentNamespaces {
		current, err := r.associatedAgent(ctx, consumer, namespace)
		if err != nil {
			return ctrl.Result{}, err
		}

		if current != nil {
			agent = current
			break
		}
	}

	cluster := &capiv1.Cluster{}

	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: machine.Namespace, Name: machine.Spec.ClusterName}, cluster)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	if apierrors.IsNotFound(err) {
		cluster = nil
	}

	return r.release(ctx, consumer, machine, agent, cluster)
}

// releaseComplete recognizes the native states that permit CAP-Agent's deletion hook to finish.
func releaseComplete(agent *assistedv1.Agent) bool {
	switch agent.Status.DebugInfo.State {
	case "discovering-unbound", "known-unbound", "disconnected-unbound", "insufficient-unbound",
		"disabled-unbound", "unbinding-pending-user-action", "error":
		return true
	default:
		return false
	}
}

// associatedAgent finds this AgentMachine's claim in the served namespace.
// It also returns a referenced unclaimed Agent while release is pending.
func (r *AgentMachineReconciler) associatedAgent(
	ctx context.Context,
	o *agentv1.AgentMachine,
	namespace string,
) (*assistedv1.Agent, error) {
	if ref := o.Status.AgentRef; ref != nil && ref.Name != "" && ref.Namespace == namespace {
		agent := &assistedv1.Agent{}

		err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, agent)
		if err != nil && !apierrors.IsNotFound(err) {
			return nil, err
		}

		if err == nil && (agentClaimMatches(agent, o) || agentUnclaimed(agent)) {
			return agent, nil
		}
	}

	var agents assistedv1.AgentList

	if err := r.APIReader.List(ctx, &agents, client.InNamespace(namespace),
		client.MatchingLabels{"agentMachineRef": o.Name}); err != nil {
		return nil, err
	}

	for i := range agents.Items {
		if agentClaimMatches(&agents.Items[i], o) {
			return &agents.Items[i], nil
		}
	}

	return nil, nil
}

// agentClaimMatches requires the AgentMachine name and namespace recorded on the Agent.
func agentClaimMatches(agent *assistedv1.Agent, consumer *agentv1.AgentMachine) bool {
	return agent.GetLabels()["agentMachineRef"] == consumer.GetName() &&
		agent.GetAnnotations()["agentMachineRefNamespace"] == consumer.GetNamespace()
}

// agentUnclaimed recognizes a host whose complete native claim has been cleared.
func agentUnclaimed(agent *assistedv1.Agent) bool {
	return agent.GetLabels()["agentMachineRef"] == "" && agent.GetAnnotations()["agentMachineRefNamespace"] == ""
}

// selectAgent returns the first matching host by name that is approved, connected,
// validated, unbound and unclaimed. Stable ordering makes the choice repeatable;
// the later claim update detects concurrent changes through the resource version.
func (r *AgentMachineReconciler) selectAgent(
	ctx context.Context,
	o *agentv1.AgentMachine,
	namespace string,
) (*assistedv1.Agent, error) {
	selection := o.Spec.AgentLabelSelector
	if selection == nil {
		selection = &metav1.LabelSelector{}
	}

	selector, err := metav1.LabelSelectorAsSelector(selection)
	if err != nil {
		return nil, err
	}

	var agents assistedv1.AgentList

	if err := r.APIReader.List(ctx, &agents, client.InNamespace(namespace)); err != nil {
		return nil, err
	}

	ctrl.LoggerFrom(ctx).V(4).Info("evaluating Agent candidates",
		"agentNamespace", namespace,
		"candidateCount", len(agents.Items),
	)
	sort.Slice(agents.Items, func(i, j int) bool { return agents.Items[i].Name < agents.Items[j].Name })

	for i := range agents.Items {
		a := &agents.Items[i]
		ctrl.LoggerFrom(ctx).V(4).Info("observed Agent candidate",
			"agent", a.GetName(),
			"uid", a.GetUID(),
			"deleting", a.GetDeletionTimestamp() != nil,
			"selectorMatches", selector.Matches(labels.Set(a.GetLabels())),
			"claimedBy", a.GetLabels()["agentMachineRef"],
			"approved", a.Spec.Approved,
			"connected", conditionsv1.IsStatusConditionTrue(a.Status.Conditions, "Connected"),
			"validated", conditionsv1.IsStatusConditionTrue(a.Status.Conditions, "Validated"),
			"unbound", conditionsv1.IsStatusConditionFalse(a.Status.Conditions, "Bound"),
		)

		if a.GetDeletionTimestamp() != nil || !selector.Matches(labels.Set(a.GetLabels())) {
			continue
		}

		if _, claimed := a.GetLabels()["agentMachineRef"]; claimed {
			continue
		}

		if a.Spec.Approved && conditionsv1.IsStatusConditionTrue(a.Status.Conditions, "Connected") &&
			conditionsv1.IsStatusConditionTrue(a.Status.Conditions, "Validated") &&
			conditionsv1.IsStatusConditionFalse(a.Status.Conditions, "Bound") {
			return a, nil
		}
	}

	return nil, nil
}

// release clears this AgentMachine's claim and waits for Assisted to report
// an unbound or terminal debug state before removing the deletion holds.
// Detach settings apply only while this AgentMachine still holds the claim.
func (r *AgentMachineReconciler) release(
	ctx context.Context,
	consumer *agentv1.AgentMachine, machine *capiv1.Machine, agent *assistedv1.Agent, cluster *capiv1.Cluster,
) (ctrl.Result, error) {
	if agent != nil && agentClaimMatches(agent, consumer) {
		ref := &agentv1.AgentReference{Name: agent.Name, Namespace: agent.Namespace}
		if !equality.Semantic.DeepEqual(consumer.Status.AgentRef, ref) {
			// Retain the recovered identity before removing the Agent-side claim.
			before := consumer.DeepCopy()
			consumer.Status.AgentRef = ref
			if err := r.Client.Status().Patch(ctx, consumer,
				client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
				return ctrl.Result{}, err
			}
		}

		return r.releaseClaim(ctx, agent, cluster)
	}

	if agent != nil && !releaseComplete(agent) {
		ctrl.LoggerFrom(ctx).V(4).Info("waiting for Assisted to report unbound",
			"agent", agent.GetName(),
			"requeueAfter", time.Second,
		)

		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	// The host is absent or unbound, so allow both Machine and AgentMachine deletion.
	if _, exists := machine.GetAnnotations()[kube.MachineHook]; exists {
		before := machine.DeepCopy()
		delete(machine.Annotations, kube.MachineHook)

		if err := r.Client.Patch(
			ctx,
			machine,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}),
		); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, kube.RemoveFinalizer(ctx, r.Client, consumer, agentMachineFinalizer)
}

// releaseClaim waits for detach permission, then clears
// the Agent's claim and installation inputs with an optimistic patch.
func (r *AgentMachineReconciler) releaseClaim(
	ctx context.Context,
	agent *assistedv1.Agent, cluster *capiv1.Cluster,
) (ctrl.Result, error) {
	hold := false

	if cluster != nil {
		var err error

		hold, err = r.HoldDetach(ctx, cluster)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	if hold {
		ctrl.LoggerFrom(ctx).V(4).Info("holding Agent detachment",
			"agent", agent.GetName(),
			"agentUID", agent.GetUID(),
			"requeueAfter", time.Second,
		)

		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	// Assisted reports Bound=False after observing the cleared inputs.
	before := agent.DeepCopy()
	delete(agent.Labels, "agentMachineRef")
	delete(agent.Annotations, "agentMachineRefNamespace")

	agent.Spec.ClusterDeploymentName = nil
	agent.Spec.MachineConfigPool = ""
	agent.Spec.IgnitionEndpointTokenReference = nil
	agent.Spec.IgnitionEndpointHTTPHeaders = nil

	ctrl.LoggerFrom(ctx).V(4).Info("clearing Agent claim and installation inputs",
		"agent", agent.GetName(),
		"agentUID", agent.GetUID(),
		"requeueAfter", time.Second,
	)

	err := r.Client.Patch(ctx, agent, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))

	return ctrl.Result{RequeueAfter: time.Second}, err
}

// claimAgent checks installation inputs, selects an eligible Agent and saves its
// claim and bootstrap configuration with a resourceVersion-checked update. Missing
// inputs requeue; a successful claim publishes its reference and requeues to observe Assisted.
func (r *AgentMachineReconciler) claimAgent(
	ctx context.Context,
	consumer *agentv1.AgentMachine, machine *capiv1.Machine, cluster *capiv1.Cluster,
	namespace string,
) (ctrl.Result, error) {
	ref := cluster.Spec.InfrastructureRef
	if ref == nil {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	if ref.APIVersion != agentv1.GroupVersion.String() || ref.Kind != "AgentCluster" ||
		(ref.Namespace != "" && ref.Namespace != cluster.Namespace) {
		return ctrl.Result{}, fmt.Errorf("cluster requires a same-namespace AgentCluster reference")
	}

	infrastructure := &agentv1.AgentCluster{}

	err := r.APIReader.Get(ctx, client.ObjectKey{
		Namespace: cluster.Namespace, Name: ref.Name,
	}, infrastructure)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	if apierrors.IsNotFound(err) {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	if !kube.MatchesController(infrastructure, cluster, capiv1.GroupVersion.WithKind("Cluster")) {
		return ctrl.Result{}, fmt.Errorf("AgentCluster belongs to another Cluster incarnation")
	}

	if infrastructure.Status.ClusterDeploymentRef.Name == "" {
		ctrl.LoggerFrom(ctx).V(4).Info("waiting for installed ClusterDeployment reference", "requeueAfter", time.Second)
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	agent, err := r.selectAgent(ctx, consumer, namespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	if agent == nil {
		ctrl.LoggerFrom(ctx).V(4).Info("waiting for eligible Agent", "agentNamespace", namespace, "requeueAfter", time.Second)
		return ctrl.Result{RequeueAfter: time.Second}, r.reportAgent(ctx, consumer, nil)
	}

	pool, token, headers, err := r.bootstrap(ctx, machine)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Save both parts of the AgentMachine identity so a restart can find this claim.
	metav1.SetMetaDataLabel(&agent.ObjectMeta, "agentMachineRef", consumer.Name)
	metav1.SetMetaDataAnnotation(&agent.ObjectMeta, "agentMachineRefNamespace", consumer.Namespace)

	// Assisted consumes the deployment and bootstrap inputs to install this worker.
	agent.Spec.ClusterDeploymentName = &assistedv1.ClusterReference{
		Name:      infrastructure.Status.ClusterDeploymentRef.Name,
		Namespace: infrastructure.Status.ClusterDeploymentRef.Namespace,
	}
	agent.Spec.MachineConfigPool = pool
	agent.Spec.IgnitionEndpointTokenReference = token
	agent.Spec.IgnitionEndpointHTTPHeaders = headers

	if err = r.Client.Update(ctx, agent); err != nil {
		return ctrl.Result{}, err
	}

	ctrl.LoggerFrom(ctx).V(4).Info("claimed Agent and published installation inputs",
		"agent", agent.GetName(),
		"agentNamespace", agent.GetNamespace(),
		"agentUID", agent.GetUID(),
		"agentMachine", consumer.GetName(),
		"agentMachineUID", consumer.GetUID(),
		"machineUID", machine.GetUID(),
	)

	return ctrl.Result{RequeueAfter: time.Second}, r.reportAgent(ctx, consumer, agent)
}
