// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package capi

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"time"

	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

const (
	setFinalizer     = capiv1.MachineSetFinalizer
	machineFinalizer = "machine.cluster.x-k8s.io"
)

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

	if err := kube.AddFinalizer(ctx, r.Client, set, setFinalizer); err != nil {
		return ctrl.Result{}, err
	}

	machines, err := children(ctx, r.APIReader, set)
	if err != nil {
		return ctrl.Result{}, err
	}

	if set.Spec.Replicas == nil {
		return ctrl.Result{}, fmt.Errorf("MachineSet spec.replicas is required for scaling")
	}

	want := *set.Spec.Replicas

	err = r.reconcileReplicas(ctx, set, machines, want)

	return ctrl.Result{}, errors.Join(err, r.reportReplicas(ctx, set, machines, want, err))
}

// adoptSet establishes the native ordinary Cluster owner and normalizes Cluster labels.
// An existing owner must match before the owner setter can change metadata.
func (r *MachineSetReconciler) adoptSet(ctx context.Context, set *capiv1.MachineSet, cluster *capiv1.Cluster) error {
	if len(set.OwnerReferences) > 0 && !kube.MatchesOwner(set, cluster, capiv1.GroupVersion.WithKind("Cluster")) {
		return fmt.Errorf("MachineSet belongs to another Cluster incarnation")
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
	// Keep lower names when removing excess Machines so the choice is repeatable.
	sort.Slice(machines, func(i, j int) bool { return machines[i].Name < machines[j].Name })

	if len(machines) > int(want) {
		for i := max(0, int(want)); i < len(machines); i++ {
			if err := kube.Delete(ctx, r.Client, &machines[i]); err != nil {
				return err
			}
		}

		return nil
	}

	existing := map[string]bool{}
	for i := range machines {
		existing[machines[i].Name] = true
	}

	missing := int(want) - len(machines)
	for i := 0; missing > 0; i++ {
		name := fmt.Sprintf("%s-%d", set.Name, i)
		if existing[name] {
			continue
		}

		if err := r.createMachine(ctx, set, name); err != nil {
			return err
		}

		missing--
	}

	return nil
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

// createMachine persists a private infrastructure clone before its Machine.
// Existing Machines stay unchanged and must belong to the original set.
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

	machine := &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: set.Namespace, Name: name, Finalizers: []string{machineFinalizer},
			Labels: maps.Clone(set.Spec.Template.Labels), Annotations: set.Spec.Template.Annotations,
		},
		Spec: *set.Spec.Template.Spec.DeepCopy(),
	}
	machine.Spec.ClusterName = set.Spec.ClusterName
	if machine.Labels == nil {
		machine.Labels = map[string]string{}
	}

	machine.Labels[kube.ManagedByLabel] = kube.ManagerName

	if err := controllerutil.SetControllerReference(set, machine, r.Client.Scheme()); err != nil {
		return err
	}

	clone, err := r.cloneInfrastructure(ctx, set, machine)
	if err != nil {
		return err
	}

	machine.Spec.InfrastructureRef = corev1.ObjectReference{
		APIVersion: agentv1.GroupVersion.String(), Kind: "AgentMachine", Namespace: clone.Namespace, Name: clone.Name,
	}

	return r.Client.Create(ctx, machine)
}

// cloneInfrastructure creates a provider with a non-controller MachineSet owner.
// An unchanged pending clone is reused; an orphaned stale clone is deleted with its UID.
func (r *MachineSetReconciler) cloneInfrastructure(
	ctx context.Context, set *capiv1.MachineSet, machine *capiv1.Machine,
) (*agentv1.AgentMachine, error) {
	ref := machine.Spec.InfrastructureRef
	if ref.Namespace == "" {
		ref.Namespace = set.Namespace
	}

	template := &agentv1.AgentMachineTemplate{}
	if err := r.APIReader.Get(ctx, client.ObjectKey{
		Namespace: ref.Namespace, Name: ref.Name,
	}, template); err != nil {
		return nil, err
	}

	clone := &agentv1.AgentMachine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: set.Namespace, Name: machine.Name,
			Labels: map[string]string{kube.ManagedByLabel: kube.ManagerName, kube.ClusterLabel: machine.Spec.ClusterName},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: capiv1.GroupVersion.String(), Kind: "MachineSet", Name: set.Name, UID: set.UID,
			}},
		},
		Spec: *template.Spec.Template.Spec.DeepCopy(),
	}
	current := &agentv1.AgentMachine{}

	err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(clone), current)
	if apierrors.IsNotFound(err) {
		return clone, r.Client.Create(ctx, clone)
	}

	if err != nil {
		return nil, err
	}

	if err := validateInfrastructureOwner(machine, current); err != nil {
		return nil, err
	}

	if controllerutil.HasControllerReference(current) || current.DeletionTimestamp != nil {
		return nil, fmt.Errorf("AgentMachine %s is outside pending clone ownership", current.Name)
	}

	if equality.Semantic.DeepEqual(current.Spec, clone.Spec) {
		return current, nil
	}

	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(machine), &capiv1.Machine{}); !apierrors.IsNotFound(err) {
		if err != nil {
			return nil, err
		}

		return nil, fmt.Errorf("pending AgentMachine %s has a Machine", current.Name)
	}

	if err := kube.Delete(ctx, r.Client, current); err != nil {
		return nil, err
	}

	return nil, fmt.Errorf("stale AgentMachine %s is being deleted", current.Name)
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
