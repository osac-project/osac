// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package capi

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"time"

	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/controllers/external"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/labels/format"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

const (
	setFinalizer     = capiv1.MachineSetFinalizer
	machineFinalizer = capiv1.MachineFinalizer
)

var errWorkerNameOccupied = errors.New("worker name occupied")

// MachineSetReconciler converges worker Machines and counts ready infrastructure.
type MachineSetReconciler struct {
	Client    client.Client
	APIReader client.Reader
}

// Reconcile converges owned workers and reports
// counts from the start of this pass. Guest Node health is outside this model.
// HCP lacks CAPI spec.version, so version-based preflight checks do not apply.
func (r *MachineSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	set := &capiv1.MachineSet{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, set); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if set.DeletionTimestamp == nil {
		if err := kube.AddFinalizer(ctx, r.Client, set, setFinalizer); err != nil {
			return ctrl.Result{}, err
		}
	}

	cluster := &capiv1.Cluster{}

	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: set.Namespace, Name: set.Spec.ClusterName}, cluster)
	if apierrors.IsNotFound(err) {
		if set.DeletionTimestamp != nil {
			return r.delete(ctx, set)
		}

		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	if err != nil {
		return ctrl.Result{}, err
	}

	if annotations.IsPaused(cluster, set) {
		return ctrl.Result{}, nil
	}

	if set.DeletionTimestamp != nil {
		return r.delete(ctx, set)
	}

	if cluster.DeletionTimestamp != nil {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	ref := set.Spec.Template.Spec.InfrastructureRef
	if ref.Kind != "AgentMachineTemplate" || ref.APIVersion != agentv1.GroupVersion.String() {
		return ctrl.Result{}, fmt.Errorf("MachineSet requires an AgentMachineTemplate reference")
	}

	if err := r.adoptSet(ctx, set, cluster); err != nil {
		return ctrl.Result{}, err
	}

	if set.Spec.Replicas == nil {
		return ctrl.Result{}, fmt.Errorf("MachineSet spec.replicas is required for scaling")
	}

	desiredLabels := workerLabels(set)

	machines, err := r.liveMachines(ctx, set, desiredLabels)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.syncWorkerLabels(ctx, machines, desiredLabels); err != nil {
		return ctrl.Result{}, err
	}

	want := *set.Spec.Replicas

	err = r.reconcileReplicas(ctx, set, machines, want)

	return ctrl.Result{}, errors.Join(err, r.reportReplicas(ctx, set, machines, want, err))
}

// adoptSet normalizes Cluster labels and preserves native MachineDeployment ownership.
// Standalone sets use an ordinary Cluster owner.
func (r *MachineSetReconciler) adoptSet(ctx context.Context, set *capiv1.MachineSet, cluster *capiv1.Cluster) error {
	clusterOwned := false

	for _, owner := range set.OwnerReferences {
		if owner.Kind != "Cluster" {
			continue
		}

		if owner.APIVersion != capiv1.GroupVersion.String() || owner.Name != cluster.Name ||
			owner.UID == "" || owner.UID != cluster.UID || set.Namespace != cluster.Namespace {
			return fmt.Errorf("MachineSet belongs to another Cluster incarnation")
		}

		clusterOwned = true
	}

	if len(set.OwnerReferences) > 0 && !clusterOwned {
		owner := metav1.GetControllerOf(set)
		if owner == nil || owner.APIVersion != capiv1.GroupVersion.String() || owner.Kind != "MachineDeployment" ||
			owner.Name == "" || owner.UID == "" {
			return fmt.Errorf("MachineSet belongs to another Cluster incarnation")
		}
	}

	before := set.DeepCopy()
	if len(set.OwnerReferences) == 0 {
		if err := controllerutil.SetOwnerReference(cluster, set, r.Client.Scheme()); err != nil {
			return err
		}
	}

	metav1.SetMetaDataLabel(&set.ObjectMeta, capiv1.ClusterNameLabel, cluster.Name)

	if set.Spec.Selector.MatchLabels == nil {
		set.Spec.Selector.MatchLabels = map[string]string{}
	}

	set.Spec.Selector.MatchLabels[capiv1.ClusterNameLabel] = cluster.Name
	if set.Spec.Template.Labels == nil {
		set.Spec.Template.Labels = map[string]string{}
	}

	set.Spec.Template.Labels[capiv1.ClusterNameLabel] = cluster.Name
	if equality.Semantic.DeepEqual(before, set) {
		return nil
	}

	return r.Client.Patch(ctx, set, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

// reconcileReplicas creates numbered workers or deletes excess owned Machines.
func (r *MachineSetReconciler) reconcileReplicas(
	ctx context.Context, set *capiv1.MachineSet, machines []capiv1.Machine, want int32,
) error {
	if len(machines) > int(want) {
		selected, err := machinesToDelete(set, machines, len(machines)-max(0, int(want)))
		if err != nil {
			return err
		}

		var deleteErrors []error

		for i := range selected {
			if err := kube.Delete(ctx, r.Client, &selected[i]); err != nil {
				deleteErrors = append(deleteErrors, err)
			}
		}

		return errors.Join(deleteErrors...)
	}

	missing := int(want) - len(machines)
	if missing == 0 {
		return nil
	}

	if _, disabled := set.Annotations[capiv1.DisableMachineCreateAnnotation]; disabled {
		return nil
	}

	var occupied capiv1.MachineList

	if err := r.APIReader.List(ctx, &occupied, client.InNamespace(set.Namespace)); err != nil {
		return err
	}

	existing := map[string]bool{}
	for i := range occupied.Items {
		existing[occupied.Items[i].Name] = true
	}

	for i := 0; missing > 0; i++ {
		name := fmt.Sprintf("%s-%d", set.Name, i)
		if existing[name] {
			continue
		}

		err := r.createMachine(ctx, set, name)
		switch {
		case errors.Is(err, errWorkerNameOccupied):
			continue
		case err != nil:
			return err
		}

		missing--
	}

	return nil
}

// machinesToDelete applies CAPI v1.9.5's deletion priorities to observed Machines.
// Equal priorities use name order; deleting the complete set preserves its order.
func machinesToDelete(set *capiv1.MachineSet, machines []capiv1.Machine, count int) ([]capiv1.Machine, error) {
	policy := capiv1.MachineSetDeletePolicy(set.Spec.DeletePolicy)
	switch policy {
	case "", capiv1.RandomMachineSetDeletePolicy, capiv1.NewestMachineSetDeletePolicy, capiv1.OldestMachineSetDeletePolicy:
	default:
		//nolint:staticcheck // Preserve the pinned CAPI delete-policy validation error.
		return nil, fmt.Errorf("Unsupported delete policy %s. Must be one of 'Random', 'Newest', or 'Oldest'", policy)
	}

	if count >= len(machines) {
		return machines, nil
	}

	if count <= 0 {
		return nil, nil
	}

	sort.Slice(machines, func(i, j int) bool {
		left, right := machineDeletePriority(&machines[i], policy), machineDeletePriority(&machines[j], policy)
		if left == right {
			return machines[i].Name < machines[j].Name
		}

		return left > right
	})

	return machines[:count], nil
}

// machineDeletePriority ranks deletion, explicit requests and health before age.
// Age priorities use the pinned controller's ten-day exponential weighting.
func machineDeletePriority(machine *capiv1.Machine, policy capiv1.MachineSetDeletePolicy) float64 {
	if !machine.DeletionTimestamp.IsZero() {
		return 100
	}

	if _, requested := machine.Annotations[capiv1.DeleteMachineAnnotation]; requested {
		return 75
	}

	if !machineHealthy(machine) {
		return 50
	}

	if policy == "" || policy == capiv1.RandomMachineSetDeletePolicy {
		return 20
	}

	oldest := 0.0
	age := metav1.Now().Sub(machine.CreationTimestamp.Time)
	if !machine.CreationTimestamp.IsZero() && age >= 0 {
		oldest = 50 * (1 - math.Exp(-age.Seconds()/864000))
	}

	if policy == capiv1.NewestMachineSetDeletePolicy {
		return 50 - oldest
	}

	return oldest
}

// machineHealthy uses existing Machine observations without contacting a guest.
// Unknown or absent health conditions leave a Machine with a Node reference healthy.
func machineHealthy(machine *capiv1.Machine) bool {
	//nolint:staticcheck // The pinned CAPI deletion policy reads its v1beta1 failure fields.
	if machine.Status.NodeRef == nil || machine.Status.FailureReason != nil || machine.Status.FailureMessage != nil {
		return false
	}

	for _, kind := range []capiv1.ConditionType{
		capiv1.MachineNodeHealthyCondition, capiv1.MachineHealthCheckSucceededCondition,
	} {
		if conditions.IsFalse(machine, kind) {
			return false
		}
	}

	return true
}

// reportReplicas counts the Machines read at the start of the pass.
// Available capacity assumes management-ready Machines have healthy guest Nodes;
// the guestless model cannot observe Node readiness or minimum-ready timing.
func (r *MachineSetReconciler) reportReplicas(
	ctx context.Context, set *capiv1.MachineSet, machines []capiv1.Machine, want int32, replicaError error,
) error {
	var count, total, fullyLabeled int32

	getters := make([]conditions.Getter, 0, len(machines))
	selector := labels.Set(set.Spec.Template.Labels).AsSelectorPreValidated()

	for i := range machines {
		total++

		getters = append(getters, &machines[i])
		if selector.Matches(labels.Set(machines[i].Labels)) {
			fullyLabeled++
		}

		if machines[i].DeletionTimestamp == nil && conditions.IsTrue(&machines[i], capiv1.ReadyCondition) {
			count++
		}
	}

	ctrl.LoggerFrom(ctx).V(4).Info("measured Machine capacity", "observed", len(machines), "ready", count, "desired", want)

	next := set.DeepCopy()
	next.Status.Replicas = total
	//nolint:staticcheck // Match replica accounting in the pinned CAPI v1beta1 controller.
	next.Status.FullyLabeledReplicas = fullyLabeled

	next.Status.ReadyReplicas, next.Status.AvailableReplicas = count, count
	next.Status.ObservedGeneration = set.Generation
	if replicaError != nil && total < want {
		conditions.MarkFalse(next, capiv1.MachinesCreatedCondition, capiv1.MachineCreationFailedReason,
			capiv1.ConditionSeverityError, "%s", replicaError.Error())
	} else {
		conditions.MarkTrue(next, capiv1.MachinesCreatedCondition)
	}

	conditions.SetAggregate(next, capiv1.MachinesReadyCondition, getters, conditions.AddSourceRef())

	switch {
	case total < want:
		conditions.MarkFalse(
			next,
			capiv1.ResizedCondition,
			capiv1.ScalingUpReason,
			capiv1.ConditionSeverityWarning,
			"Scaling up MachineSet to %d replicas (actual %d)",
			want,
			total,
		)
	case total > want:
		conditions.MarkFalse(
			next,
			capiv1.ResizedCondition,
			capiv1.ScalingDownReason,
			capiv1.ConditionSeverityWarning,
			"Scaling down MachineSet to %d replicas (actual %d)",
			want,
			total,
		)
	case count == total:
		conditions.MarkTrue(next, capiv1.ResizedCondition)
	}

	conditions.SetSummary(
		next,
		conditions.WithConditions(capiv1.MachinesCreatedCondition, capiv1.ResizedCondition, capiv1.MachinesReadyCondition),
	)

	if equality.Semantic.DeepEqual(set.Status, next.Status) {
		return nil
	}

	return r.Client.Status().Patch(ctx, next,
		client.MergeFromWithOptions(set, client.MergeFromWithOptimisticLock{}))
}

// workerLabels derives the labels shared by new and existing workers.
func workerLabels(set *capiv1.MachineSet) map[string]string {
	result := make(map[string]string, len(set.Spec.Template.Labels)+2)
	maps.Copy(result, set.Spec.Template.Labels)
	result[capiv1.MachineSetNameLabel] = format.MustFormatValue(set.Name)

	if deployment, present := set.Labels[capiv1.MachineDeploymentNameLabel]; present {
		result[capiv1.MachineDeploymentNameLabel] = deployment
	}

	return result
}

// syncWorkerLabels updates selected live workers while retaining unrelated labels.
func (r *MachineSetReconciler) syncWorkerLabels(
	ctx context.Context, machines []capiv1.Machine, desired map[string]string,
) error {
	for i := range machines {
		machine := &machines[i]
		if machine.DeletionTimestamp != nil {
			continue
		}

		before := machine.DeepCopy()
		for key, value := range desired {
			metav1.SetMetaDataLabel(&machine.ObjectMeta, key, value)
		}

		if !maps.Equal(before.Labels, machine.Labels) {
			if err := r.Client.Patch(ctx, machine,
				client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
				return err
			}
		}
	}

	return nil
}

// createMachine persists a private infrastructure clone before its Machine.
// A Machine at the requested name must belong to the original set.
func (r *MachineSetReconciler) createMachine(ctx context.Context, set *capiv1.MachineSet, name string) error {
	current := &capiv1.Machine{}

	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: set.Namespace, Name: name}, current)
	if err == nil {
		if !kube.MatchesController(current, set, capiv1.GroupVersion.WithKind("MachineSet")) {
			return fmt.Errorf("machine %s belongs to another MachineSet incarnation", current.Name)
		}

		return nil
	}

	if !apierrors.IsNotFound(err) {
		return err
	}

	if set.Spec.Template.Spec.Bootstrap.ConfigRef != nil {
		return fmt.Errorf("MachineSet bootstrap ConfigRef is unsupported; use DataSecretName")
	}

	machine := &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: set.Namespace, Name: name, Finalizers: []string{machineFinalizer},
			Labels: workerLabels(set), Annotations: maps.Clone(set.Spec.Template.Annotations),
		},
		Spec: *set.Spec.Template.Spec.DeepCopy(),
	}
	machine.Spec.ClusterName = set.Spec.ClusterName

	if err := controllerutil.SetControllerReference(set, machine, r.Client.Scheme()); err != nil {
		return err
	}

	clone, err := r.cloneInfrastructure(ctx, set, machine)
	if err != nil {
		return err
	}

	machine.Spec.InfrastructureRef = corev1.ObjectReference{
		APIVersion: agentv1.GroupVersion.String(), Kind: "AgentMachine",
		Namespace: clone.Namespace, Name: clone.Name, UID: clone.UID,
	}

	return r.Client.Create(ctx, machine)
}

// cloneInfrastructure creates a provider with a non-controller MachineSet owner.
// A pending clone must match the requested spec, template lineage and pause input.
func (r *MachineSetReconciler) cloneInfrastructure(
	ctx context.Context, set *capiv1.MachineSet, machine *capiv1.Machine,
) (*agentv1.AgentMachine, error) {
	ref := machine.Spec.InfrastructureRef
	if ref.Namespace == "" {
		ref.Namespace = set.Namespace
	}

	template, err := external.Get(ctx, r.APIReader, &ref)
	if err != nil {
		return nil, err
	}

	generated, err := external.GenerateTemplate(&external.GenerateTemplateInput{
		Template: template, TemplateRef: &ref, Namespace: set.Namespace, Name: machine.Name,
		ClusterName: machine.Spec.ClusterName, Labels: machine.Labels, Annotations: machine.Annotations,
		OwnerRef: &metav1.OwnerReference{
			APIVersion: capiv1.GroupVersion.String(), Kind: "MachineSet", Name: set.Name, UID: set.UID,
		},
	})
	if err != nil {
		return nil, err
	}

	clone := &agentv1.AgentMachine{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(generated.Object, clone); err != nil {
		return nil, err
	}

	current := &agentv1.AgentMachine{}

	err = r.APIReader.Get(ctx, client.ObjectKeyFromObject(clone), current)
	if apierrors.IsNotFound(err) {
		return clone, r.Client.Create(ctx, clone)
	}

	if err != nil {
		return nil, err
	}

	if !kube.MatchesOwner(current, set, capiv1.GroupVersion.WithKind("MachineSet")) {
		return nil, fmt.Errorf("%w: AgentMachine %s belongs to another MachineSet incarnation",
			errWorkerNameOccupied, current.Name)
	}

	if controllerutil.HasControllerReference(current) || current.DeletionTimestamp != nil {
		return nil, fmt.Errorf("AgentMachine %s is outside pending clone ownership", current.Name)
	}

	if hasConflictingMachineOwner(machine, current) {
		return nil, fmt.Errorf("%w: AgentMachine %s has a conflicting Machine owner", errWorkerNameOccupied, current.Name)
	}

	const nameKey, groupKindKey = capiv1.TemplateClonedFromNameAnnotation, capiv1.TemplateClonedFromGroupKindAnnotation

	currentPause, currentHasPause := current.Annotations[capiv1.PausedAnnotation]
	clonePause, cloneHasPause := clone.Annotations[capiv1.PausedAnnotation]

	if !equality.Semantic.DeepEqual(current.Spec, clone.Spec) ||
		current.Annotations[nameKey] != clone.Annotations[nameKey] ||
		current.Annotations[groupKindKey] != clone.Annotations[groupKindKey] ||
		currentPause != clonePause || currentHasPause != cloneHasPause {
		return nil, fmt.Errorf("%w: pending AgentMachine %s differs from the template", errWorkerNameOccupied, current.Name)
	}

	return current, nil
}

// liveMachines selects workers for replica management and validates that new workers can join them.
func (r *MachineSetReconciler) liveMachines(
	ctx context.Context, set *capiv1.MachineSet, desiredLabels map[string]string,
) ([]capiv1.Machine, error) {
	match, err := metav1.LabelSelectorAsMap(&set.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("invalid MachineSet selector: %w", err)
	}

	selector := labels.SelectorFromSet(match)
	if !selector.Matches(labels.Set(desiredLabels)) {
		return nil, fmt.Errorf("MachineSet worker labels do not match its selector")
	}

	machines, err := children(ctx, r.APIReader, set)
	if err != nil {
		return nil, err
	}

	return slices.DeleteFunc(machines, func(machine capiv1.Machine) bool {
		return !selector.Matches(labels.Set(machine.Labels))
	}), nil
}

// children finds Machines controlled by the current MachineSet, excluding old UIDs.
func children(ctx context.Context, r client.Reader, set *capiv1.MachineSet) ([]capiv1.Machine, error) {
	var all capiv1.MachineList

	if err := r.List(ctx, &all, client.InNamespace(set.Namespace)); err != nil {
		return nil, err
	}

	var result []capiv1.Machine

	for i := range all.Items {
		if kube.MatchesController(&all.Items[i], set, capiv1.GroupVersion.WithKind("MachineSet")) {
			result = append(result, all.Items[i])
		}
	}

	return result, nil
}

// delete waits for all owned Machines to disappear before releasing this set's finalizer.
func (r *MachineSetReconciler) delete(ctx context.Context, set *capiv1.MachineSet) (ctrl.Result, error) {
	machines, err := children(ctx, r.APIReader, set)
	if err != nil {
		return ctrl.Result{}, err
	}

	for i := range machines {
		if err := kube.Delete(ctx, r.Client, &machines[i]); err != nil {
			return ctrl.Result{}, err
		}
	}

	if len(machines) > 0 {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	return ctrl.Result{}, kube.RemoveFinalizer(ctx, r.Client, set, setFinalizer)
}
