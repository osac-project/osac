// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

// Package assisted reports host discovery and installation through Assisted API resources.
package assisted

import (
	"context"
	"errors"
	"fmt"
	"time"

	installv1 "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

const agentFinalizer = "agent.agent-install.openshift.io/ai-deprovision"

// InstallationResponse selects the host installation observation. Empty means unconfigured.
type InstallationResponse string

const (
	InstallationComplete InstallationResponse = "complete"
	InstallationFailed   InstallationResponse = "failed"
	InstallationHeld     InstallationResponse = "held"
)

// AgentReconciler reports discovery from supplied host inventory and advances installation.
// Hosts arrive through native Agent registration; CAP-Agent claims them for Machines.
type AgentReconciler struct {
	Client       client.Client
	APIReader    client.Reader
	Scope        kube.Scope
	Installation func(context.Context, *assistedv1.InfraEnv) (InstallationResponse, error)
}

// Reconcile reports discovery for one Agent and advances installation after approval
// and a valid deployment reference. Its installation input can finish, fail or hold progress.
// Registrations whose original InfraEnv is absent or deleting are left to native garbage collection.
func (r *AgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	agent := &assistedv1.Agent{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, agent); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !r.Scope.Contains(agent.GetNamespace()) {
		ctrl.LoggerFrom(ctx).V(4).Info("ignoring Agent outside active scope", "deleting", agent.GetDeletionTimestamp() != nil)
		return ctrl.Result{}, nil
	}

	if agent.DeletionTimestamp != nil {
		return ctrl.Result{}, kube.RemoveFinalizer(ctx, r.Client, agent, agentFinalizer)
	}

	// Resolve the discovery environment that owns this registration.
	infra, err := r.infraEnv(ctx, agent)
	if err != nil {
		return ctrl.Result{}, err
	}

	if infra == nil {
		return ctrl.Result{}, nil
	}

	response, err := r.Installation(ctx, infra)
	if err != nil {
		return ctrl.Result{}, err
	}

	if response == "" {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	if response != InstallationComplete && response != InstallationFailed && response != InstallationHeld {
		return ctrl.Result{}, fmt.Errorf("unsupported installation response %q", response)
	}

	if err := kube.AddFinalizer(ctx, r.Client, agent, agentFinalizer); err != nil {
		return ctrl.Result{}, err
	}

	next := agent.DeepCopy()

	installing, err := r.advanceInstallation(ctx, next, response)
	setAgentConditions(next)

	if err != nil {
		conditionsv1.SetStatusConditionNoHeartbeat(&next.Status.Conditions, conditionsv1.Condition{
			Type:    assistedv1.SpecSyncedCondition,
			Status:  corev1.ConditionFalse,
			Reason:  assistedv1.InputErrorReason,
			Message: err.Error(),
		})
	}

	// Save independent observations together while preserving the input failure.
	if !equality.Semantic.DeepEqual(agent.Status, next.Status) {
		err = errors.Join(err, r.Client.Status().Patch(ctx, next,
			client.MergeFromWithOptions(agent, client.MergeFromWithOptimisticLock{})))
	}

	if installing {
		ctrl.LoggerFrom(ctx).V(4).Info("installation remains active",
			"requeueAfter", time.Second,
			"statusWriteFailed", err != nil,
		)

		return ctrl.Result{RequeueAfter: time.Second}, err
	}

	return ctrl.Result{}, err
}

// setAgentConditions reports discovery, eligibility, binding and installation
// from supplied inventory and backend state.
// CPU, disk and network validation and heartbeat timing are outside this hardware model.
// Approval, connection and synchronization remain independent observations.
func setAgentConditions(next *assistedv1.Agent) {
	connected, reason, message := corev1.ConditionTrue, assistedv1.AgentConnectedReason, assistedv1.AgentConnectedMsg
	state := next.Status.DebugInfo.State
	if state == "disconnected" || state == "disconnected-unbound" {
		connected, reason, message = corev1.ConditionFalse, assistedv1.AgentDisconnectedReason, assistedv1.AgentDisonnectedMsg
	}

	conditionsv1.SetStatusConditionNoHeartbeat(
		&next.Status.Conditions,
		conditionsv1.Condition{Type: assistedv1.ConnectedCondition, Status: connected, Reason: reason, Message: message},
	)

	validated := conditionsv1.Condition{
		Type:    assistedv1.ValidatedCondition,
		Status:  corev1.ConditionUnknown,
		Reason:  assistedv1.ValidationsUnknownReason,
		Message: assistedv1.AgentValidationsUnknownMsg,
	}
	if len(next.Status.Inventory.Interfaces) > 0 {
		validated.Status = corev1.ConditionTrue
		validated.Reason = assistedv1.ValidationsPassingReason
		validated.Message = assistedv1.AgentValidationsPassingMsg
	}

	switch state {
	case "binding":
		validated.Status = corev1.ConditionFalse
		validated.Reason = assistedv1.BindingReason
		validated.Message = assistedv1.BindingMsg
	case "unbinding", "unbinding-pending-user-action":
		validated.Status = corev1.ConditionFalse
		validated.Reason = assistedv1.UnbindingReason
		validated.Message = assistedv1.UnbindingMsg
	case "insufficient", "insufficient-unbound":
		validated.Status = corev1.ConditionFalse
		validated.Reason = assistedv1.ValidationsFailingReason
		validated.Message = assistedv1.AgentValidationsFailingMsg
	}

	conditionsv1.SetStatusConditionNoHeartbeat(&next.Status.Conditions, validated)
	conditionsv1.SetStatusConditionNoHeartbeat(
		&next.Status.Conditions,
		conditionsv1.Condition{
			Type:    assistedv1.SpecSyncedCondition,
			Status:  corev1.ConditionTrue,
			Reason:  assistedv1.SyncedOkReason,
			Message: assistedv1.SyncedOkMsg,
		},
	)
	requirementsStatus(next)
	installationConditions(next)
}

// requirementsStatus applies native eligibility reasons independently of validation and binding.
func requirementsStatus(agent *assistedv1.Agent) {
	condition := conditionsv1.Condition{
		Type:    assistedv1.RequirementsMetCondition,
		Status:  corev1.ConditionUnknown,
		Reason:  assistedv1.UnknownStatusReason,
		Message: assistedv1.UnknownStatusMsg,
	}
	switch agent.Status.DebugInfo.State {
	case "known", "known-unbound":
		condition.Status = corev1.ConditionFalse

		condition.Reason = assistedv1.AgentIsNotApprovedReason
		condition.Message = assistedv1.AgentIsNotApprovedMsg
		if agent.Spec.Approved {
			condition.Status = corev1.ConditionTrue
			condition.Reason = assistedv1.AgentReadyReason
			condition.Message = assistedv1.AgentReadyMsg
		}
	case "insufficient",
		"insufficient-unbound",
		"disconnected",
		"disconnected-unbound",
		"discovering",
		"discovering-unbound",
		"pending-for-input":
		condition.Status = corev1.ConditionFalse
		condition.Reason = assistedv1.AgentNotReadyReason
		condition.Message = assistedv1.AgentNotReadyMsg
	case "preparing-for-installation",
		"preparing-successful",
		"installing",
		"installing-in-progress",
		"installing-pending-user-action":
		condition.Status = corev1.ConditionTrue
		condition.Reason = assistedv1.AgentAlreadyInstallingReason
		condition.Message = assistedv1.AgentAlreadyInstallingMsg
	case "installed", "error", "added-to-existing-cluster":
		condition.Status = corev1.ConditionTrue
		condition.Reason = assistedv1.AgentInstallationStoppedReason
		condition.Message = assistedv1.AgentInstallationStoppedMsg
	case "binding":
		condition.Status = corev1.ConditionFalse
		condition.Reason = assistedv1.BindingReason
		condition.Message = assistedv1.BindingMsg
	case "unbinding", "unbinding-pending-user-action":
		condition.Status = corev1.ConditionFalse
		condition.Reason = assistedv1.UnbindingReason
		condition.Message = assistedv1.UnbindingMsg
	}

	conditionsv1.SetStatusConditionNoHeartbeat(&agent.Status.Conditions, condition)
}

// advanceInstallation advances native binding with an installed deployment.
// Approval gates installation while discovery and binding remain independent.
func (r *AgentReconciler) advanceInstallation(
	ctx context.Context,
	agent *assistedv1.Agent,
	response InstallationResponse,
) (bool, error) {
	previousState := agent.Status.DebugInfo.State
	state := previousState
	interfaceCount := len(agent.Status.Inventory.Interfaces)

	if state == "" || state == "discovering-unbound" {
		state = "known-unbound"
		if interfaceCount == 0 {
			state = "discovering-unbound"
		}
	}

	deployment := ""
	if agent.Spec.ClusterDeploymentName != nil {
		deployment = agent.Spec.ClusterDeploymentName.Name
	}

	bound := false

	agent.Status.DebugInfo.State = state

	if interfaceCount > 0 && deployment != "" {
		var err error

		bound, err = r.deploymentReady(ctx, agent)
		if err != nil {
			return false, err
		}
	}

	ctrl.LoggerFrom(ctx).V(4).Info("evaluated Agent installation prerequisites",
		"agentUID", agent.GetUID(),
		"inventoryInterfaces", interfaceCount,
		"approved", agent.Spec.Approved,
		"deployment", deployment,
		"deploymentReady", bound,
	)

	if !bound {
		unbindingStatus(agent, state, deployment)
		return agent.Status.DebugInfo.State == "unbinding", nil
	}

	// Advance binding before installation; installed and failed states remain terminal.
	switch state {
	case "known-unbound", "unbinding":
		state = "binding"
	case "binding":
		state = "known"
	case "known":
		if agent.Spec.Approved {
			state = "installing"
		}
	case "installing":
		switch response {
		case InstallationHeld:
		case InstallationFailed:
			state = "error"
		case InstallationComplete:
			state = "installed"
		}
	}

	agent.Status.DebugInfo.State = state
	ctrl.LoggerFrom(ctx).V(4).Info("selected Agent installation response",
		"previousState", previousState,
		"desiredState", state,
		"response", response,
	)

	return state == "installing" || state == "binding" || state == "known" && agent.Spec.Approved, nil
}

// unbindingStatus exposes the native reclaim barrier across separate observations.
// Hardware reprovisioning remains a user action after a completed installation.
func unbindingStatus(next *assistedv1.Agent, state, deployment string) {
	switch state {
	case "installed", "installing", "error":
		if deployment == "" {
			state = "unbinding-pending-user-action"
		}
	case "known", "binding":
		if deployment == "" {
			state = "unbinding"
		}
	case "unbinding":
		state = "known-unbound"
	}

	next.Status.DebugInfo.State = state
}

// installationConditions reports native binding and installation observations from backend state.
func installationConditions(agent *assistedv1.Agent) {
	state := agent.Status.DebugInfo.State
	bound := conditionsv1.Condition{Type: assistedv1.BoundCondition, Status: corev1.ConditionFalse}

	switch state {
	case "binding":
		bound.Reason, bound.Message = assistedv1.BindingReason, assistedv1.BindingMsg
	case "unbinding":
		bound.Reason, bound.Message = assistedv1.UnbindingReason, assistedv1.UnbindingMsg
	case "unbinding-pending-user-action":
		bound.Reason, bound.Message = assistedv1.UnbindingPendingUserActionReason, assistedv1.UnbindingPendingUserActionMsg
	case "disconnected-unbound", "known-unbound", "insufficient-unbound", "disabled-unbound", "discovering-unbound":
		bound.Reason, bound.Message = assistedv1.UnboundReason, assistedv1.UnboundMsg
	default:
		bound.Status = corev1.ConditionTrue
		bound.Reason, bound.Message = assistedv1.BoundReason, assistedv1.BoundMsg
	}

	conditionsv1.SetStatusConditionNoHeartbeat(&agent.Status.Conditions, bound)
	installed := conditionsv1.Condition{Type: assistedv1.InstalledCondition, Status: corev1.ConditionFalse}

	switch state {
	case "installed", "added-to-existing-cluster":
		installed.Status = corev1.ConditionTrue
		installed.Reason, installed.Message = assistedv1.InstalledReason, assistedv1.InstalledMsg
	case "error":
		installed.Reason, installed.Message = assistedv1.InstallationFailedReason, assistedv1.InstallationFailedMsg
	case "insufficient",
		"insufficient-unbound",
		"disconnected",
		"disconnected-unbound",
		"discovering",
		"discovering-unbound",
		"known",
		"known-unbound",
		"pending-for-input":
		installed.Reason, installed.Message = assistedv1.InstallationNotStartedReason, assistedv1.InstallationNotStartedMsg
	case "preparing-for-installation",
		"preparing-successful",
		"installing",
		"installing-in-progress",
		"installing-pending-user-action":
		installed.Reason, installed.Message = assistedv1.InstallationInProgressReason, assistedv1.InstallationInProgressMsg
	case "binding":
		installed.Reason, installed.Message = assistedv1.BindingReason, assistedv1.BindingMsg
	case "unbinding", "unbinding-pending-user-action":
		installed.Reason, installed.Message = assistedv1.UnbindingReason, assistedv1.UnbindingMsg
	default:
		installed.Status = corev1.ConditionUnknown
		installed.Reason, installed.Message = assistedv1.UnknownStatusReason, assistedv1.UnknownStatusMsg+" "+state
	}

	conditionsv1.SetStatusConditionNoHeartbeat(&agent.Status.Conditions, installed)
}

// infraEnv resolves the original active discovery environment, accepting native
// non-controller owners. A replacement never supplies responses for an older registration.
func (r *AgentReconciler) infraEnv(
	ctx context.Context,
	agent *assistedv1.Agent,
) (*assistedv1.InfraEnv, error) {
	infra := &assistedv1.InfraEnv{}

	err := r.APIReader.Get(ctx,
		client.ObjectKey{Namespace: agent.Namespace, Name: agent.Labels[kube.InfraEnvLabel]}, infra)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	if infra.GetDeletionTimestamp() != nil {
		return nil, nil
	}

	if !kube.MatchesOwner(agent, infra, assistedv1.GroupVersion.WithKind("InfraEnv")) {
		return nil, nil
	}

	return infra, nil
}

// deploymentReady requires an installed ClusterDeployment and its original live
// AgentClusterInstall. Workers join only after the installation inputs are valid.
// Read failures return errors; missing or deleting inputs return false.
func (r *AgentReconciler) deploymentReady(ctx context.Context, agent *assistedv1.Agent) (bool, error) {
	ref := agent.Spec.ClusterDeploymentName
	if ref == nil {
		return false, nil
	}

	cd := &hivev1.ClusterDeployment{}

	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, cd)
	if apierrors.IsNotFound(err) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	installation := cd.Spec.ClusterInstallRef
	if cd.DeletionTimestamp != nil || !cd.Spec.Installed || installation == nil || installation.Name == "" ||
		installation.Group != installv1.GroupVersion.Group || installation.Kind != "AgentClusterInstall" {
		return false, nil
	}

	aci := &installv1.AgentClusterInstall{}

	err = r.APIReader.Get(ctx, client.ObjectKey{Namespace: cd.Namespace, Name: cd.Spec.ClusterInstallRef.Name}, aci)
	if apierrors.IsNotFound(err) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	if aci.DeletionTimestamp != nil {
		return false, nil
	}

	if err := validateInstallation(cd, aci); err != nil {
		return false, err
	}

	if err := r.validateDayTwoInputs(ctx, cd, aci, agent); err != nil {
		return false, err
	}

	return true, nil
}
