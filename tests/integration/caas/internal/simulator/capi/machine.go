// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package capi

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	//nolint:staticcheck // CAPI v1.9.5 uses this failure reason in its v1beta1 status.
	capierrors "sigs.k8s.io/cluster-api/errors"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// MachineReconciler adopts its concrete AgentMachine, reports provider readiness
// and waits for CAP-Agent to release a host before deleting its provider.
type MachineReconciler struct {
	Client    client.Client
	APIReader client.Reader
}

var errInfrastructureReplaced = errors.New("referenced AgentMachine was replaced")

// Reconcile normalizes Machine associations, adopts concrete infrastructure and
// mirrors readiness. Guest Node readiness and drain are outside this model.
func (r *MachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	machine := &capiv1.Machine{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, machine); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if machine.DeletionTimestamp == nil {
		if err := kube.AddFinalizer(ctx, r.Client, machine, machineFinalizer); err != nil {
			return ctrl.Result{}, err
		}
	}

	cluster := &capiv1.Cluster{}
	if err := r.APIReader.Get(
		ctx,
		client.ObjectKey{Namespace: machine.Namespace, Name: machine.Spec.ClusterName},
		cluster,
	); err != nil {
		return ctrl.Result{}, err
	}

	if annotations.IsPaused(cluster, machine) {
		return ctrl.Result{}, nil
	}

	if machine.DeletionTimestamp != nil {
		return r.delete(ctx, machine)
	}

	if err := r.normalizeMachineMetadata(ctx, machine, cluster); err != nil {
		return ctrl.Result{}, err
	}

	if !hasAgentMachineRef(machine) {
		return ctrl.Result{}, fmt.Errorf("machine requires a concrete AgentMachine reference")
	}

	infra, err := r.readInfrastructure(ctx, machine)
	if apierrors.IsNotFound(err) || (machine.Status.InfrastructureReady && errors.Is(err, errInfrastructureReplaced)) {
		if machine.Status.InfrastructureReady {
			ref := machine.Spec.InfrastructureRef
			next := machine.DeepCopy()
			//nolint:staticcheck // Match the pinned CAPI v1beta1 infrastructure-loss status.
			next.Status.FailureReason = new(capierrors.InvalidConfigurationMachineError)
			//nolint:staticcheck // Match the pinned CAPI v1beta1 infrastructure-loss status.
			next.Status.FailureMessage = new(fmt.Sprintf(
				"Machine infrastructure resource %v with name %q has been deleted after being ready",
				ref.GroupVersionKind(), ref.Name,
			))

			return ctrl.Result{}, errors.Join(err, r.Client.Status().Patch(ctx, next,
				client.MergeFromWithOptions(machine, client.MergeFromWithOptimisticLock{})))
		}

		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.adoptInfrastructure(ctx, machine, infra); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, r.reportMachine(ctx, machine, infra)
}

// normalizeMachineMetadata assigns standalone Machines their ordinary Cluster
// owner and normalizes the Cluster label while preserving controller ownership.
func (r *MachineReconciler) normalizeMachineMetadata(
	ctx context.Context, machine *capiv1.Machine, cluster *capiv1.Cluster,
) error {
	owner := kube.OwnerReference(machine, "Cluster")
	if owner != nil && !kube.MatchesOwner(machine, cluster, capiv1.GroupVersion.WithKind("Cluster")) {
		return fmt.Errorf("machine belongs to another Cluster incarnation")
	}

	_, fromSet := machine.Labels[capiv1.MachineSetNameLabel]
	_, fromPool := machine.Labels[capiv1.MachinePoolNameLabel]
	_, fromControlPlane := machine.Labels[capiv1.MachineControlPlaneNameLabel]
	before := machine.DeepCopy()

	if owner == nil && metav1.GetControllerOf(machine) == nil && !fromSet && !fromPool && !fromControlPlane {
		if err := controllerutil.SetOwnerReference(cluster, machine, r.Client.Scheme()); err != nil {
			return err
		}
	}

	metav1.SetMetaDataLabel(&machine.ObjectMeta, capiv1.ClusterNameLabel, machine.Spec.ClusterName)

	if equality.Semantic.DeepEqual(before.ObjectMeta, machine.ObjectMeta) {
		return nil
	}

	return r.Client.Patch(ctx, machine, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

// adoptInfrastructure hands validated infrastructure to its Machine and removes
// the temporary MachineSet owner while preserving unrelated ownership.
func (r *MachineReconciler) adoptInfrastructure(
	ctx context.Context, machine *capiv1.Machine, infra *agentv1.AgentMachine,
) error {
	before := infra.DeepCopy()
	if !controllerutil.HasControllerReference(infra) {
		if err := controllerutil.SetControllerReference(machine, infra, r.Client.Scheme()); err != nil {
			return err
		}
	}

	if set := metav1.GetControllerOf(machine); set != nil &&
		set.APIVersion == capiv1.GroupVersion.String() && set.Kind == "MachineSet" && set.UID != "" {
		infra.OwnerReferences = slices.DeleteFunc(infra.OwnerReferences, func(owner metav1.OwnerReference) bool {
			return owner.APIVersion == set.APIVersion && owner.Kind == set.Kind &&
				owner.Name == set.Name && owner.UID == set.UID
		})
	}

	if equality.Semantic.DeepEqual(before.OwnerReferences, infra.OwnerReferences) {
		return nil
	}

	return r.Client.Patch(ctx, infra, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

// reportMachine publishes bootstrap, provider identity and current conditions.
// Infrastructure provisioning is sticky independently of its mirrored condition.
func (r *MachineReconciler) reportMachine(
	ctx context.Context,
	machine *capiv1.Machine,
	infra *agentv1.AgentMachine,
) error {
	next := machine.DeepCopy()

	if infra.DeletionTimestamp == nil && (infra.Status.Ready || machine.Status.InfrastructureReady) {
		if infra.Spec.ProviderID == nil || *infra.Spec.ProviderID == "" {
			return fmt.Errorf("ready AgentMachine has no providerID")
		}

		next.Spec.ProviderID = new(*infra.Spec.ProviderID)
		next.Status.InfrastructureReady = true
	}

	if machine.Spec.Bootstrap.DataSecretName != nil {
		next.Status.BootstrapReady = true
		conditions.MarkTrue(next, capiv1.BootstrapReadyCondition)
	} else {
		conditions.MarkFalse(
			next,
			capiv1.BootstrapReadyCondition,
			"WaitingForBootstrapData",
			capiv1.ConditionSeverityInfo,
			"Waiting for bootstrap data",
		)
	}

	conditions.SetMirror(
		next,
		capiv1.InfrastructureReadyCondition,
		infra,
		conditions.WithFallbackValue(
			infra.Status.Ready,
			capiv1.WaitingForInfrastructureFallbackReason,
			capiv1.ConditionSeverityInfo,
			"",
		),
	)
	// Without a guest Node, aggregate readiness must wait for initial provisioning.
	if infra.DeletionTimestamp != nil && !next.Status.InfrastructureReady {
		conditions.MarkFalse(next, capiv1.ReadyCondition, capiv1.DeletingReason, capiv1.ConditionSeverityInfo,
			"Infrastructure is terminating before provisioning completed")
	} else {
		conditions.SetSummary(
			next,
			conditions.WithConditions(capiv1.InfrastructureReadyCondition, capiv1.BootstrapReadyCondition),
		)
	}

	if !equality.Semantic.DeepEqual(machine.Spec, next.Spec) {
		// The spec response contains stored status; retain these new observations separately.
		spec := next.DeepCopy()
		if err := r.Client.Patch(
			ctx,
			spec,
			client.MergeFromWithOptions(machine, client.MergeFromWithOptimisticLock{}),
		); err != nil {
			return err
		}

		machine.Spec, machine.ResourceVersion = spec.Spec, spec.ResourceVersion
		next.ResourceVersion = spec.ResourceVersion
	}

	if equality.Semantic.DeepEqual(machine.Status, next.Status) {
		return nil
	}

	return r.Client.Status().Patch(ctx, next,
		client.MergeFromWithOptions(machine, client.MergeFromWithOptimisticLock{}))
}

// hasAgentMachineRef recognizes the concrete, local provider that this simulator manages.
func hasAgentMachineRef(machine *capiv1.Machine) bool {
	ref := machine.Spec.InfrastructureRef
	return ref.APIVersion == agentv1.GroupVersion.String() && ref.Kind == "AgentMachine" && ref.Name != "" &&
		(ref.Namespace == "" || ref.Namespace == machine.Namespace)
}

// readInfrastructure resolves the supported local provider and verifies its original identity.
func (r *MachineReconciler) readInfrastructure(
	ctx context.Context, machine *capiv1.Machine,
) (*agentv1.AgentMachine, error) {
	ref := machine.Spec.InfrastructureRef
	infra := &agentv1.AgentMachine{}
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: machine.Namespace, Name: ref.Name}, infra); err != nil {
		return nil, err
	}

	if ref.UID != "" && ref.UID != infra.UID {
		if infra.UID == "" {
			return nil, fmt.Errorf("AgentMachine %s/%s has no observed UID", infra.Namespace, infra.Name)
		}

		return nil, fmt.Errorf("%w: %s/%s", errInfrastructureReplaced, infra.Namespace, infra.Name)
	}

	return infra, validateInfrastructureOwner(machine, infra)
}

// validateInfrastructureOwner accepts unowned infrastructure, this Machine's provider
// or its MachineSet's pending clone while preserving foreign resource associations.
func validateInfrastructureOwner(machine *capiv1.Machine, infrastructure *agentv1.AgentMachine) error {
	if machine.UID == "" || machine.Namespace != infrastructure.Namespace {
		return fmt.Errorf("AgentMachine %s requires an identified Machine in the same namespace", infrastructure.Name)
	}

	if len(infrastructure.OwnerReferences) == 0 {
		return nil
	}

	if hasConflictingMachineOwner(machine, infrastructure) {
		return fmt.Errorf("AgentMachine %s has a conflicting Machine owner", infrastructure.Name)
	}

	if controllerutil.HasControllerReference(infrastructure) {
		if kube.MatchesController(infrastructure, machine, capiv1.GroupVersion.WithKind("Machine")) {
			return nil
		}

		return fmt.Errorf("AgentMachine %s belongs to another controller", infrastructure.Name)
	}

	if kube.MatchesOwner(infrastructure, machine, capiv1.GroupVersion.WithKind("Machine")) {
		return nil
	}

	set := metav1.GetControllerOf(machine)
	if set != nil && set.APIVersion == capiv1.GroupVersion.String() && set.Kind == "MachineSet" && set.UID != "" {
		for _, owner := range infrastructure.OwnerReferences {
			if owner.APIVersion == set.APIVersion && owner.Kind == set.Kind && owner.Name == set.Name && owner.UID == set.UID {
				return nil
			}
		}
	}

	return fmt.Errorf("AgentMachine %s belongs to another MachineSet incarnation", infrastructure.Name)
}

// hasConflictingMachineOwner detects references the controller setter could overwrite.
// That setter matches group, kind and name even when the version or UID differs.
func hasConflictingMachineOwner(machine *capiv1.Machine, infra *agentv1.AgentMachine) bool {
	return slices.ContainsFunc(infra.OwnerReferences, func(owner metav1.OwnerReference) bool {
		return owner.Kind == "Machine" && owner.Name == machine.Name &&
			strings.HasPrefix(owner.APIVersion, capiv1.GroupVersion.Group+"/") &&
			(machine.UID == "" || owner.APIVersion != capiv1.GroupVersion.String() || owner.UID != machine.UID)
	})
}

// delete waits for all pre-terminate hooks, then deletes the private provider and waits
// for absence before removing the Machine finalizer. Shared templates remain.
func (r *MachineReconciler) delete(
	ctx context.Context, machine *capiv1.Machine,
) (ctrl.Result, error) {
	next := machine.DeepCopy()

	var (
		result ctrl.Result
		err    error
	)

	if annotations.HasWithPrefix(capiv1.PreTerminateDeleteHookAnnotationPrefix, machine.Annotations) {
		conditions.MarkFalse(next, capiv1.PreTerminateDeleteHookSucceededCondition, capiv1.WaitingExternalHookReason,
			capiv1.ConditionSeverityInfo, "")

		result.RequeueAfter = time.Second
	} else {
		conditions.MarkTrue(next, capiv1.PreTerminateDeleteHookSucceededCondition)

		result, err = r.deleteInfrastructure(ctx, machine)
	}

	if equality.Semantic.DeepEqual(machine.Status, next.Status) {
		return result, err
	}

	next.ObjectMeta = machine.ObjectMeta

	return result, errors.Join(err, client.IgnoreNotFound(r.Client.Status().Patch(ctx, next,
		client.MergeFromWithOptions(machine, client.MergeFromWithOptimisticLock{}))))
}

// deleteInfrastructure waits for the referenced AgentMachine to disappear before
// releasing the Machine finalizer. Shared or replacement providers stay untouched.
func (r *MachineReconciler) deleteInfrastructure(ctx context.Context, machine *capiv1.Machine) (ctrl.Result, error) {
	if !hasAgentMachineRef(machine) {
		return ctrl.Result{}, kube.RemoveFinalizer(ctx, r.Client, machine, machineFinalizer)
	}

	infra, err := r.readInfrastructure(ctx, machine)
	if apierrors.IsNotFound(err) || errors.Is(err, errInfrastructureReplaced) {
		return ctrl.Result{}, kube.RemoveFinalizer(ctx, r.Client, machine, machineFinalizer)
	}

	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: time.Second}, kube.Delete(ctx, r.Client, infra)
}
