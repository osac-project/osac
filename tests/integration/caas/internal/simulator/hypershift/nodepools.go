// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package hypershift

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

const nodePoolFinalizer = "hypershift.openshift.io/finalizer"

var stepCounterMessage = regexp.MustCompile(`\d+ of \d+ completed`)

// NodePoolReconciler publishes worker demand and observes available Machines.
type NodePoolReconciler struct {
	Client    client.Client
	APIReader client.Reader
	Scope     kube.Scope
}

// Reconcile supplies Agent-platform worker inputs and observes available capacity.
// Failed reads and writes retain the last measured count and report Unknown readiness.
// A live parent's Agent namespace bounds cleanup; missing parents allow orphan cleanup.
func (r *NodePoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	pool := &hypershiftv1.NodePool{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, pool); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if pool.Spec.Platform.Type != hypershiftv1.AgentPlatform {
		return ctrl.Result{}, nil
	}

	hc := &hypershiftv1.HostedCluster{}

	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: pool.Namespace, Name: pool.Spec.ClusterName}, hc)
	if apierrors.IsNotFound(err) {
		hc = nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	if pool.DeletionTimestamp != nil {
		return r.delete(ctx, pool, hc)
	}

	if hc == nil {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	if hc.Spec.Platform.Agent == nil || !r.Scope.Contains(hc.Spec.Platform.Agent.AgentNamespace) {
		return ctrl.Result{}, nil
	}

	if err := r.adoptPool(ctx, pool, hc); err != nil {
		return ctrl.Result{}, err
	}

	if hc.Spec.InfraID == "" {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	cluster := &capiv1.Cluster{}
	key := client.ObjectKey{Namespace: hc.Namespace + "-" + hc.Name, Name: hc.Spec.InfraID}

	err = r.APIReader.Get(ctx, key, cluster)
	if apierrors.IsNotFound(err) {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	if err != nil {
		return ctrl.Result{}, r.unknown(ctx, pool, err)
	}

	if !hostedClusterOutput(cluster, hc) {
		return ctrl.Result{}, r.unknown(ctx, pool, fmt.Errorf("CAPI Cluster belongs to another HostedCluster"))
	}

	if paused, duration := reconciliationPaused(pool.Spec.PausedUntil); paused {
		return ctrl.Result{RequeueAfter: duration}, r.pause(ctx, pool, cluster)
	}

	worker, err := r.inputs(ctx, pool, cluster)
	if err != nil {
		return ctrl.Result{}, r.unknown(ctx, pool, err)
	}

	next := pool.DeepCopy()

	next.Status.Replicas = worker.Status.AvailableReplicas

	if condition := conditions.Get(worker, capiv1.ReadyCondition); condition != nil {
		reason := condition.Reason
		if reason == "" {
			reason = hypershiftv1.AsExpectedReason
		}

		setPoolCondition(next, "Ready", condition.Status, reason, condition.Message)
	}

	if err := r.observeMachines(ctx, next, worker); err != nil {
		return ctrl.Result{}, r.unknown(ctx, pool, err)
	}

	if equality.Semantic.DeepEqual(pool.Status, next.Status) {
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, r.Client.Status().Patch(ctx, next,
		client.MergeFromWithOptions(pool, client.MergeFromWithOptimisticLock{}))
}

// adoptPool establishes native parent ownership and protects the pool's teardown.
func (r *NodePoolReconciler) adoptPool(
	ctx context.Context, pool *hypershiftv1.NodePool, hc *hypershiftv1.HostedCluster,
) error {
	owner := kube.OwnerReference(pool, "HostedCluster")
	if owner != nil && !kube.MatchesOwner(pool, hc, hypershiftv1.GroupVersion.WithKind("HostedCluster")) {
		return fmt.Errorf("NodePool belongs to another HostedCluster incarnation")
	}

	if owner == nil {
		before := pool.DeepCopy()
		if err := controllerutil.SetOwnerReference(hc, pool, r.Client.Scheme()); err != nil {
			return err
		}

		if err := r.Client.Patch(ctx, pool,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return err
		}
	}

	return kube.AddFinalizer(ctx, r.Client, pool, nodePoolFinalizer)
}

// pause stops the original MachineSet while preserving demand and worker inputs.
func (r *NodePoolReconciler) pause(ctx context.Context, pool *hypershiftv1.NodePool, cluster *capiv1.Cluster) error {
	set := &capiv1.MachineSet{}
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: pool.Name}, set); err != nil {
		return client.IgnoreNotFound(err)
	}

	if !nodePoolOutput(set, pool) || !matchesWorkerCluster(set, cluster) {
		return fmt.Errorf("MachineSet belongs to another NodePool or Cluster")
	}

	if set.Annotations[capiv1.PausedAnnotation] == "true" {
		return nil
	}

	before := set.DeepCopy()
	metav1.SetMetaDataAnnotation(&set.ObjectMeta, capiv1.PausedAnnotation, "true")

	return r.Client.Patch(ctx, set, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

// inputs writes a template, bootstrap Secret and MachineSet for InPlace demand.
// Upgrades and rolling replacement are outside this model.
func (r *NodePoolReconciler) inputs(
	ctx context.Context, pool *hypershiftv1.NodePool, cluster *capiv1.Cluster,
) (*capiv1.MachineSet, error) {
	if pool.Spec.Management.UpgradeType != hypershiftv1.UpgradeTypeInPlace {
		return nil, fmt.Errorf("unsupported NodePool upgrade type %q", pool.Spec.Management.UpgradeType)
	}

	if err := r.ensureTemplate(ctx, pool, cluster); err != nil {
		return nil, err
	}

	if err := r.ensureBootstrap(ctx, pool, cluster); err != nil {
		return nil, err
	}

	return r.ensureWorkers(ctx, pool, cluster)
}

// ensureTemplate keeps the pool's host selector on its original Cluster's template.
func (r *NodePoolReconciler) ensureTemplate(
	ctx context.Context, pool *hypershiftv1.NodePool, cluster *capiv1.Cluster,
) error {
	desired := &agentv1.AgentMachineTemplate{ObjectMeta: metav1.ObjectMeta{
		Namespace: cluster.Namespace, Name: pool.Name,
		Annotations: map[string]string{kube.NodePoolAnnotation: pool.Namespace + "/" + pool.Name},
	}}
	if pool.Spec.Platform.Agent != nil {
		desired.Spec.Template.Spec.AgentLabelSelector = pool.Spec.Platform.Agent.AgentLabelSelector.DeepCopy()
	}

	if err := controllerutil.SetControllerReference(cluster, desired, r.Client.Scheme()); err != nil {
		return err
	}

	current := &agentv1.AgentMachineTemplate{}

	err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(desired), current)
	if apierrors.IsNotFound(err) {
		return r.Client.Create(ctx, desired)
	}

	if err != nil {
		return err
	}

	if err := validateOutput(current, desired); err != nil {
		return err
	}

	if current.DeletionTimestamp != nil {
		return nil
	}

	next := current.DeepCopy()

	next.Spec.Template.Spec.AgentLabelSelector = desired.Spec.Template.Spec.AgentLabelSelector
	if equality.Semantic.DeepEqual(current.Spec, next.Spec) && equality.Semantic.DeepEqual(current.Labels, next.Labels) {
		return nil
	}

	return r.Client.Patch(ctx, next,
		client.MergeFromWithOptions(current, client.MergeFromWithOptimisticLock{}))
}

// ensureBootstrap supplies the pool's ignition source while preserving other Secret keys.
func (r *NodePoolReconciler) ensureBootstrap(
	ctx context.Context, pool *hypershiftv1.NodePool, cluster *capiv1.Cluster,
) error {
	data, err := r.bootstrapData(ctx, pool, cluster)
	if err != nil {
		return err
	}

	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace, Name: pool.Name + "-bootstrap",
			Annotations: map[string]string{kube.NodePoolAnnotation: pool.Namespace + "/" + pool.Name},
		},
		Data: map[string][]byte{"value": data},
	}
	if err := controllerutil.SetControllerReference(cluster, desired, r.Client.Scheme()); err != nil {
		return err
	}

	current := &corev1.Secret{}

	err = r.APIReader.Get(ctx, client.ObjectKeyFromObject(desired), current)
	if apierrors.IsNotFound(err) {
		return r.Client.Create(ctx, desired)
	}

	if err != nil {
		return err
	}

	if err := validateOutput(current, desired); err != nil {
		return err
	}

	if current.DeletionTimestamp != nil {
		return nil
	}

	next := current.DeepCopy()

	if next.Data == nil {
		next.Data = map[string][]byte{}
	}

	next.Data["value"] = desired.Data["value"]
	if equality.Semantic.DeepEqual(current.Data, next.Data) && equality.Semantic.DeepEqual(current.Labels, next.Labels) {
		return nil
	}

	return r.Client.Patch(ctx, next,
		client.MergeFromWithOptions(current, client.MergeFromWithOptimisticLock{}))
}

// ensureWorkers publishes pool demand, selector and bootstrap references on the
// original Cluster's MachineSet, retaining status and unrelated metadata.
func (r *NodePoolReconciler) ensureWorkers(
	ctx context.Context, pool *hypershiftv1.NodePool, cluster *capiv1.Cluster,
) (*capiv1.MachineSet, error) {
	desired := &capiv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace, Name: pool.Name,
			Annotations: map[string]string{kube.NodePoolAnnotation: pool.Namespace + "/" + pool.Name},
			Labels:      map[string]string{kube.ClusterLabel: cluster.Name},
		},
		Spec: capiv1.MachineSetSpec{
			ClusterName: cluster.Name,
			Selector:    metav1.LabelSelector{MatchLabels: map[string]string{"hypershift.openshift.io/nodePool": pool.Name}},
			Template: capiv1.MachineTemplateSpec{
				ObjectMeta: capiv1.ObjectMeta{
					Labels: map[string]string{
						"hypershift.openshift.io/nodePool": pool.Name, kube.ClusterLabel: cluster.Name,
					},
					Annotations: map[string]string{kube.NodePoolAnnotation: pool.Namespace + "/" + pool.Name},
				},
				Spec: capiv1.MachineSpec{
					ClusterName: cluster.Name,
					Bootstrap:   capiv1.Bootstrap{DataSecretName: new(pool.Name + "-bootstrap")},
					InfrastructureRef: corev1.ObjectReference{
						APIVersion: agentv1.GroupVersion.String(),
						Kind:       "AgentMachineTemplate",
						Namespace:  cluster.Namespace,
						Name:       pool.Name,
					},
				},
			},
		},
	}
	current := &capiv1.MachineSet{}

	err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(desired), current)
	if apierrors.IsNotFound(err) {
		setWorkerReplicas(pool, desired)
		return desired, r.Client.Create(ctx, desired)
	}

	if err != nil {
		return nil, err
	}

	if !nodePoolOutput(current, pool) {
		return nil, fmt.Errorf("MachineSet belongs to another NodePool")
	}

	if !matchesWorkerCluster(current, cluster) {
		return nil, fmt.Errorf("MachineSet belongs to another Cluster incarnation")
	}

	if current.DeletionTimestamp != nil {
		return current, nil
	}

	next := current.DeepCopy()
	next.Spec.ClusterName = desired.Spec.ClusterName
	setWorkerReplicas(pool, next)

	if next.Spec.Selector.MatchLabels == nil {
		next.Spec.Selector.MatchLabels = map[string]string{}
	}

	next.Spec.Selector.MatchLabels["hypershift.openshift.io/nodePool"] = pool.Name
	next.Spec.Template = desired.Spec.Template

	metav1.SetMetaDataLabel(&next.ObjectMeta, kube.ClusterLabel, cluster.Name)
	delete(next.Annotations, capiv1.PausedAnnotation)

	if equality.Semantic.DeepEqual(current.Spec, next.Spec) && equality.Semantic.DeepEqual(current.Labels, next.Labels) &&
		equality.Semantic.DeepEqual(current.Annotations, next.Annotations) {
		return current, nil
	}

	return next, r.Client.Patch(ctx, next,
		client.MergeFromWithOptions(current, client.MergeFromWithOptimisticLock{}))
}

// setWorkerReplicas hands native autoscaling bounds to the MachineSet while preserving
// demand within those bounds. Fixed pools reset the autoscaler annotations.
func setWorkerReplicas(pool *hypershiftv1.NodePool, set *capiv1.MachineSet) {
	minimum, maximum := int32(0), int32(0)
	if bounds := pool.Spec.AutoScaling; bounds != nil {
		minimum, maximum = bounds.Min, bounds.Max

		replicas := minimum
		if set.Spec.Replicas != nil {
			replicas = *set.Spec.Replicas
		}

		set.Spec.Replicas = new(min(max(replicas, minimum), maximum))
	} else {
		set.Spec.Replicas = new(int32(0))
		if pool.Spec.Replicas != nil {
			set.Spec.Replicas = new(*pool.Spec.Replicas)
		}
	}

	metav1.SetMetaDataAnnotation(&set.ObjectMeta, "cluster.x-k8s.io/cluster-api-autoscaler-node-group-min-size",
		strconv.Itoa(int(minimum)))
	metav1.SetMetaDataAnnotation(&set.ObjectMeta, "cluster.x-k8s.io/cluster-api-autoscaler-node-group-max-size",
		strconv.Itoa(int(maximum)))
}

// delete requests worker teardown and keeps bootstrap inputs until associated Machines disappear.
// A live Cluster incarnation must still match each input's original owner.
// A surviving control plane bounds orphan scope when the HostedCluster is missing.
func (r *NodePoolReconciler) delete(
	ctx context.Context, pool *hypershiftv1.NodePool, hc *hypershiftv1.HostedCluster,
) (ctrl.Result, error) {
	if hc != nil {
		if hc.Spec.Platform.Agent == nil || !r.Scope.Contains(hc.Spec.Platform.Agent.AgentNamespace) {
			return ctrl.Result{}, nil
		}
	} else {
		if allowed, err := r.orphanCleanupAllowed(ctx, pool); err != nil || !allowed {
			return ctrl.Result{}, err
		}
	}

	// Use the same Cluster observation for worker exclusion and input deletion in this pass.
	clusters := make(map[client.ObjectKey]*capiv1.Cluster)

	namespace := pool.Namespace + "-" + pool.Spec.ClusterName
	template := &agentv1.AgentMachineTemplate{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: pool.Name}}
	if _, err := r.deleteInput(ctx, pool, template, clusters); err != nil {
		return ctrl.Result{}, err
	}

	set := &capiv1.MachineSet{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: pool.Name}}

	association, err := r.deleteInput(ctx, pool, set, clusters)
	if err != nil {
		return ctrl.Result{}, err
	}

	pending, err := r.machinesRemain(ctx, pool, set, association)
	if err != nil {
		return ctrl.Result{}, err
	}

	if pending {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	for _, name := range []string{pool.Name + "-bootstrap", pool.Name + "-token"} {
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
		if _, err := r.deleteInput(ctx, pool, secret, clusters); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, kube.RemoveFinalizer(ctx, r.Client, pool, nodePoolFinalizer)
}

// orphanCleanupAllowed checks scope from a surviving HCP and its current Cluster owner.
// Missing or unassociated parents leave ownership-qualified cleanup available.
// Native source annotations identify a HostedCluster by namespace and name.
func (r *NodePoolReconciler) orphanCleanupAllowed(
	ctx context.Context, pool *hypershiftv1.NodePool,
) (bool, error) {
	cp := &hypershiftv1.HostedControlPlane{}
	namespace := pool.Namespace + "-" + pool.Spec.ClusterName

	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: pool.Spec.ClusterName}, cp)
	if apierrors.IsNotFound(err) {
		return true, nil
	}

	if err != nil {
		return false, err
	}

	source := pool.Namespace + "/" + pool.Spec.ClusterName
	owner := kube.Owner(cp, "Cluster")
	if cp.UID == "" || cp.Annotations[kube.HostedClusterAnnotation] != source || owner == nil ||
		owner.APIVersion != capiv1.GroupVersion.String() {
		return true, nil
	}

	cluster := &capiv1.Cluster{}

	err = r.APIReader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: owner.Name}, cluster)
	if apierrors.IsNotFound(err) {
		return true, nil
	}

	if err != nil {
		return false, err
	}

	if cluster.Annotations[kube.HostedClusterAnnotation] != source ||
		!kube.MatchesController(cp, cluster, capiv1.GroupVersion.WithKind("Cluster")) {
		return true, nil
	}

	ref := cluster.Spec.ControlPlaneRef
	if ref == nil || ref.APIVersion != hypershiftv1.GroupVersion.String() || ref.Kind != "HostedControlPlane" ||
		ref.Name != cp.Name || (ref.Namespace != "" && ref.Namespace != namespace) ||
		(ref.UID != "" && ref.UID != cp.UID) {
		return false, fmt.Errorf("surviving Cluster requires its associated HostedControlPlane reference")
	}

	return cp.Spec.Platform.Agent != nil && r.Scope.Contains(cp.Spec.Platform.Agent.AgentNamespace), nil
}

type poolInputAssociation uint8

const (
	unrelatedPoolInput poolInputAssociation = iota
	unconfirmedPoolInput
	associatedPoolInput
	previousClusterPoolInput
)

// deleteInput classifies the observed association and deletes only inputs of this pool.
func (r *NodePoolReconciler) deleteInput(
	ctx context.Context, pool *hypershiftv1.NodePool, current client.Object, clusters map[client.ObjectKey]*capiv1.Cluster,
) (poolInputAssociation, error) {
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(current), current); err != nil {
		return unrelatedPoolInput, client.IgnoreNotFound(err)
	}

	if !nodePoolOutput(current, pool) {
		return unrelatedPoolInput, nil
	}

	association, err := r.poolInputCluster(ctx, current, clusters)
	if err != nil || association != associatedPoolInput {
		return association, err
	}

	return association, kube.Delete(ctx, r.Client, current)
}

// machinesRemain counts workers by their associated MachineSet or native pool annotation.
// Only exact workers of a set proven to belong to a previous Cluster are excluded.
func (r *NodePoolReconciler) machinesRemain(
	ctx context.Context, pool *hypershiftv1.NodePool, set *capiv1.MachineSet, association poolInputAssociation,
) (bool, error) {
	var machines capiv1.MachineList

	if err := r.APIReader.List(ctx, &machines, client.InNamespace(pool.Namespace+"-"+pool.Spec.ClusterName)); err != nil {
		return false, err
	}

	for i := range machines.Items {
		machine := &machines.Items[i]
		if association != unrelatedPoolInput &&
			kube.MatchesController(machine, set, capiv1.GroupVersion.WithKind("MachineSet")) {
			if association == previousClusterPoolInput {
				continue
			}

			return true, nil
		}

		if nodePoolOutput(machine, pool) {
			return true, nil
		}
	}

	return false, nil
}

// poolInputCluster recognizes pending CAPI adoption, the original owner, and a confirmed Cluster replacement.
func (r *NodePoolReconciler) poolInputCluster(
	ctx context.Context, current client.Object, clusters map[client.ObjectKey]*capiv1.Cluster,
) (poolInputAssociation, error) {
	owner, valid := poolClusterOwner(current)
	if !valid {
		return unconfirmedPoolInput, nil
	}

	if owner == nil {
		return associatedPoolInput, nil
	}

	key := client.ObjectKey{Namespace: current.GetNamespace(), Name: owner.Name}

	cluster, observed := clusters[key]
	if !observed {
		cluster = &capiv1.Cluster{}

		err := r.APIReader.Get(ctx, key, cluster)
		if apierrors.IsNotFound(err) {
			cluster = nil
		} else if err != nil {
			return unconfirmedPoolInput, err
		}

		clusters[key] = cluster
	}

	switch {
	case cluster == nil:
		return associatedPoolInput, nil
	case cluster.UID == "":
		return unconfirmedPoolInput, nil
	case cluster.UID != owner.UID:
		return previousClusterPoolInput, nil
	default:
		return associatedPoolInput, nil
	}
}

// observeMachines keeps Machine health separate from the MachineSet's replica count.
func (r *NodePoolReconciler) observeMachines(
	ctx context.Context,
	pool *hypershiftv1.NodePool,
	set *capiv1.MachineSet,
) error {
	var machines capiv1.MachineList

	if err := r.APIReader.List(ctx, &machines, client.InNamespace(set.Namespace)); err != nil {
		return err
	}

	count, notReady := 0, 0
	messages := map[string][]string{}
	status, reason, message := corev1.ConditionTrue, hypershiftv1.AsExpectedReason, hypershiftv1.AllIsWellMessage

	for i := range machines.Items {
		machine := &machines.Items[i]
		if !kube.MatchesController(machine, set, capiv1.GroupVersion.WithKind("MachineSet")) {
			continue
		}

		count++

		condition := conditions.Get(machine, capiv1.ReadyCondition)
		if condition == nil || condition.Status == corev1.ConditionTrue {
			continue
		}

		notReady++

		failureReason, failureMessage := condition.Reason, fmt.Sprintf("Machine %s: %s\n", machine.Name, condition.Reason)
		if infra := conditions.Get(
			machine,
			capiv1.InfrastructureReadyCondition,
		); infra != nil && infra.Status != corev1.ConditionTrue &&
			!stepCounterMessage.MatchString(infra.Message) {
			failureReason, failureMessage = infra.Reason, fmt.Sprintf(
				"Machine %s: %s: %s\n",
				machine.Name,
				infra.Reason,
				infra.Message,
			)
		}

		messages[failureReason] = append(messages[failureReason], failureMessage)
	}

	if notReady > 0 {
		status = corev1.ConditionFalse
		reason, message = machineReadinessMessage(messages, count, notReady)
	}

	if count == 0 {
		status, reason, message = corev1.ConditionFalse, hypershiftv1.NodePoolNotFoundReason, "No Machines are created"
		if pool.Spec.Replicas != nil && *pool.Spec.Replicas == 0 {
			reason, message = hypershiftv1.AsExpectedReason, "NodePool set to no replicas"
		}
	}

	setPoolCondition(pool, "AllMachinesReady", status, reason, message)

	return nil
}

// machineReadinessMessage groups native Machine failures with stable reasons and
// the upstream per-reason message limit, keeping unchanged observations quiet.
func machineReadinessMessage(messages map[string][]string, total, notReady int) (string, string) {
	reasons := slices.Sorted(maps.Keys(messages))

	var message strings.Builder

	fmt.Fprintf(&message, "%d of %d machines are not ready\n", notReady, total)

	for _, reason := range reasons {
		size := 0
		for _, detail := range messages[reason] {
			if size+len(detail) > 1000 {
				message.WriteString("... too many similar errors\n")
				break
			}

			size += len(detail)
			message.WriteString(detail)
		}
	}

	return strings.Join(reasons, ","), message.String()
}

// setPoolCondition preserves transition times and unrelated observations.
func setPoolCondition(pool *hypershiftv1.NodePool, kind string, status corev1.ConditionStatus, reason, message string) {
	var condition *hypershiftv1.NodePoolCondition

	for i := range pool.Status.Conditions {
		if pool.Status.Conditions[i].Type == kind {
			condition = &pool.Status.Conditions[i]
			break
		}
	}

	if condition == nil {
		pool.Status.Conditions = append(pool.Status.Conditions, hypershiftv1.NodePoolCondition{Type: kind})
		condition = &pool.Status.Conditions[len(pool.Status.Conditions)-1]
	}

	if condition.Status != status {
		condition.LastTransitionTime = metav1.Now()
	}

	condition.Status = status
	condition.Reason = reason
	condition.Message = message
	condition.ObservedGeneration = pool.Generation
}

// unknown retains the measured worker count while marking both readiness
// conditions Unknown. The original failure remains visible if the status write fails.
func (r *NodePoolReconciler) unknown(ctx context.Context, pool *hypershiftv1.NodePool, cause error) error {
	next := pool.DeepCopy()
	setPoolCondition(next, "Ready", corev1.ConditionUnknown, "ObservationFailed", cause.Error())
	setPoolCondition(next, "AllMachinesReady", corev1.ConditionUnknown, "ObservationFailed", cause.Error())

	if equality.Semantic.DeepEqual(pool.Status, next.Status) {
		return cause
	}

	return errors.Join(cause, r.Client.Status().Patch(ctx, next,
		client.MergeFromWithOptions(pool, client.MergeFromWithOptimisticLock{})))
}
