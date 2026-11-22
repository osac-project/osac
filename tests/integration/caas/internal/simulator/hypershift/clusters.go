// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

// Package hypershift simulates control-plane and worker-pool lifecycle responses.
// Running a guest cluster is outside this model.
package hypershift

import (
	"context"
	"fmt"
	"strconv"
	"time"

	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/configrefs"
	"github.com/openshift/hypershift/support/infraid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

const hostedClusterFinalizer = "hypershift.openshift.io/finalizer"

// HostedClusterReconciler creates provider inputs and observes their status.
// Worker release precedes control-plane deletion.
type HostedClusterReconciler struct {
	Client    client.Client
	APIReader client.Reader
	Scope     kube.Scope
}

// Reconcile supplies inputs for a served Agent cluster, copies its published
// kubeconfig and observes control-plane availability and guest-version conditions.
func (r *HostedClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	cluster := &hypershiftv1.HostedCluster{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if cluster.Spec.Platform.Type != hypershiftv1.AgentPlatform || cluster.Spec.Platform.Agent == nil ||
		!r.Scope.Contains(cluster.Spec.Platform.Agent.AgentNamespace) {
		return ctrl.Result{}, nil
	}

	if cluster.DeletionTimestamp != nil {
		return r.delete(ctx, cluster)
	}

	if err := kube.AddFinalizer(ctx, r.Client, cluster, hostedClusterFinalizer); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.observeReconciliationActive(ctx, cluster); err != nil {
		return ctrl.Result{}, err
	}

	if paused, duration := reconciliationPaused(cluster.Spec.PausedUntil); paused {
		return ctrl.Result{RequeueAfter: duration}, r.pause(ctx, cluster)
	}

	if cluster.Spec.InfraID == "" {
		before := cluster.DeepCopy()
		cluster.Spec.InfraID = infraid.New(cluster.Name)
		if err := r.Client.Patch(ctx, cluster,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, err
		}
	}

	if err := r.observeUnmanagedEtcd(ctx, cluster); err != nil {
		return ctrl.Result{}, err
	}

	cp, err := r.inputs(ctx, cluster)
	if err != nil || cp == nil {
		return ctrl.Result{}, err
	}

	config, err := r.copyKubeconfig(ctx, cluster, cp)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.observeControlPlane(ctx, cluster, cp, config); err != nil {
		return ctrl.Result{}, err
	}

	if config == "" || !meta.IsStatusConditionTrue(cp.Status.Conditions, "Available") {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	return ctrl.Result{}, nil
}

// observeUnmanagedEtcd reports the native TLS input condition independently of
// control-plane availability. It validates Secret keys without contacting etcd.
func (r *HostedClusterReconciler) observeUnmanagedEtcd(ctx context.Context, hc *hypershiftv1.HostedCluster) error {
	if hc.Spec.Etcd.ManagementType != hypershiftv1.Unmanaged {
		return nil
	}

	var secret *corev1.Secret

	if etcd := hc.Spec.Etcd.Unmanaged; etcd != nil && etcd.TLS.ClientSecret.Name != "" {
		secret = &corev1.Secret{}

		err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: hc.Namespace, Name: etcd.TLS.ClientSecret.Name}, secret)
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}

		if apierrors.IsNotFound(err) {
			secret = nil
		}
	}

	before := hc.DeepCopy()
	meta.SetStatusCondition(&hc.Status.Conditions, unmanagedEtcdCondition(hc, secret))

	if equality.Semantic.DeepEqual(before.Status, hc.Status) {
		return nil
	}

	return r.Client.Status().Patch(ctx, hc, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

// unmanagedEtcdCondition mirrors the native metadata and TLS key availability contract.
func unmanagedEtcdCondition(hc *hypershiftv1.HostedCluster, secret *corev1.Secret) metav1.Condition {
	condition := metav1.Condition{
		Type:               string(hypershiftv1.UnmanagedEtcdAvailable),
		Status:             metav1.ConditionFalse,
		Reason:             hypershiftv1.UnmanagedEtcdMisconfiguredReason,
		ObservedGeneration: hc.Generation,
	}
	etcd := hc.Spec.Etcd.Unmanaged
	if etcd == nil || etcd.Endpoint == "" || etcd.TLS.ClientSecret.Name == "" {
		condition.Message = "etcd metadata not specified for unmanaged deployment"
		return condition
	}

	if secret == nil {
		condition.Message = "missing TLS client secret " + etcd.TLS.ClientSecret.Name
		return condition
	}

	for _, field := range [][2]string{
		{"etcd-client.crt", "client cert"},
		{"etcd-client.key", "client key"},
		{"etcd-client-ca.crt", "client ca"},
	} {
		if _, present := secret.Data[field[0]]; !present {
			condition.Message = fmt.Sprintf("etcd secret %s does not have %s", secret.Name, field[1])
			return condition
		}
	}

	condition.Status, condition.Reason = metav1.ConditionTrue, hypershiftv1.UnmanagedEtcdAsExpected

	return condition
}

// observeReconciliationActive reports pause diagnostics independently of input readiness.
func (r *HostedClusterReconciler) observeReconciliationActive(
	ctx context.Context, hc *hypershiftv1.HostedCluster,
) error {
	before := hc.DeepCopy()
	meta.SetStatusCondition(&hc.Status.Conditions, reconciliationActiveCondition(hc.Spec.PausedUntil, hc.Generation))

	if equality.Semantic.DeepEqual(before.Status, hc.Status) {
		return nil
	}

	return r.Client.Status().Patch(ctx, hc, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

// reconciliationPaused interprets HyperShift's boolean or RFC3339 pause value.
func reconciliationPaused(value *string) (bool, time.Duration) {
	if value == nil {
		return false, 0
	}

	if paused, err := strconv.ParseBool(*value); err == nil {
		return paused, 0
	}

	until, err := time.Parse(time.RFC3339, *value)
	if err != nil || !until.After(time.Now()) {
		return false, 0
	}

	return true, time.Until(until)
}

// reconciliationActiveCondition mirrors pinned HyperShift's pausedUntil status.
func reconciliationActiveCondition(value *string, generation int64) metav1.Condition {
	condition := metav1.Condition{
		Type: string(hypershiftv1.ReconciliationActive), Status: metav1.ConditionTrue,
		Reason: hypershiftv1.AsExpectedReason, Message: "Reconciliation active on resource",
		ObservedGeneration: generation,
	}
	if value == nil {
		return condition
	}

	paused, err := strconv.ParseBool(*value)
	if err == nil {
		if paused {
			condition.Status, condition.Reason = metav1.ConditionFalse, hypershiftv1.ReconciliationPausedConditionReason
			condition.Message = "Reconciliation paused until field removed"
		}

		return condition
	}

	until, err := time.Parse(time.RFC3339, *value)
	if err != nil {
		condition.Reason = hypershiftv1.ReconciliationInvalidPausedUntilConditionReason
		condition.Message = "Invalid value provided for PausedUntil field"
		return condition
	}

	if time.Now().Before(until) {
		condition.Status, condition.Reason = metav1.ConditionFalse, hypershiftv1.ReconciliationPausedConditionReason
		condition.Message = fmt.Sprintf("Reconciliation paused until: %s", *value)
	}

	return condition
}

// pause propagates an HC pause to existing native children without creating inputs.
func (r *HostedClusterReconciler) pause(ctx context.Context, hc *hypershiftv1.HostedCluster) error {
	if hc.Spec.InfraID == "" {
		return nil
	}

	desired := capiClusterOutput(hc)
	cluster := &capiv1.Cluster{}

	err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(desired), cluster)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	if err != nil || !hostedClusterOutput(cluster, hc) {
		cluster = nil
	} else if err := validateOutput(cluster, desired); err != nil {
		return err
	}

	if cluster != nil && !cluster.Spec.Paused {
		before := cluster.DeepCopy()
		cluster.Spec.Paused = true
		if err := r.Client.Patch(ctx, cluster,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return err
		}
	}

	cp := &hypershiftv1.HostedControlPlane{}

	err = r.APIReader.Get(ctx, client.ObjectKey{Namespace: desired.Namespace, Name: hc.Name}, cp)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	if err == nil && hostedClusterOutput(cp, hc) &&
		!equality.Semantic.DeepEqual(cp.Spec.PausedUntil, hc.Spec.PausedUntil) {
		if metav1.GetControllerOf(cp) != nil &&
			(cluster == nil || !kube.MatchesController(cp, cluster, capiv1.GroupVersion.WithKind("Cluster"))) {
			return fmt.Errorf("control plane belongs to another Cluster incarnation")
		}

		before := cp.DeepCopy()
		cp.Spec.PausedUntil = hc.Spec.PausedUntil
		if err := r.Client.Patch(
			ctx,
			cp,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}),
		); err != nil {
			return err
		}
	}

	return nil
}

// copyKubeconfig copies the original HCP's published Secret key. Both source and
// destination must retain their original owners; missing input keeps HC waiting.
func (r *HostedClusterReconciler) copyKubeconfig(
	ctx context.Context, cluster *hypershiftv1.HostedCluster, cp *hypershiftv1.HostedControlPlane,
) (string, error) {
	ref := cp.Status.KubeConfig
	if ref == nil || ref.Name == "" || ref.Key == "" {
		return "", nil
	}

	source := &corev1.Secret{}

	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: cp.Namespace, Name: ref.Name}, source)
	if apierrors.IsNotFound(err) {
		return "", nil
	}

	if err != nil {
		return "", err
	}

	if !kube.MatchesController(source, cp, hypershiftv1.GroupVersion.WithKind("HostedControlPlane")) {
		return "", fmt.Errorf("source kubeconfig Secret belongs to another control plane")
	}

	value := source.Data[ref.Key]
	if len(value) == 0 {
		return "", fmt.Errorf("source kubeconfig Secret has no published data key")
	}

	desired := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: cluster.Namespace, Name: cluster.Name + "-admin-kubeconfig",
	}}
	if err := controllerutil.SetControllerReference(cluster, desired, r.Client.Scheme()); err != nil {
		return "", err
	}

	current := &corev1.Secret{}

	err = r.APIReader.Get(ctx, client.ObjectKeyFromObject(desired), current)
	if apierrors.IsNotFound(err) {
		metav1.SetMetaDataAnnotation(&desired.ObjectMeta, kube.HostedClusterAnnotation, cluster.Namespace+"/"+cluster.Name)
		desired.Data = map[string][]byte{"kubeconfig": value}
		return desired.Name, r.Client.Create(ctx, desired)
	}

	if err != nil {
		return "", err
	}

	if err := validateOutput(current, desired); err != nil {
		return "", err
	}

	if current.DeletionTimestamp != nil {
		return current.Name, nil
	}

	next := current.DeepCopy()

	metav1.SetMetaDataAnnotation(&next.ObjectMeta, kube.HostedClusterAnnotation, cluster.Namespace+"/"+cluster.Name)

	if next.Data == nil {
		next.Data = map[string][]byte{}
	}

	next.Data["kubeconfig"] = value
	if equality.Semantic.DeepEqual(current.Data, next.Data) && equality.Semantic.DeepEqual(current.Labels, next.Labels) &&
		equality.Semantic.DeepEqual(current.Annotations, next.Annotations) {
		return current.Name, nil
	}

	return next.Name, r.Client.Patch(ctx, next,
		client.MergeFromWithOptions(current, client.MergeFromWithOptimisticLock{}))
}

// observeControlPlane owns HC status only. HCCO's ClusterVersionFailing condition
// is inverted into ClusterVersionSucceeding, independently of CPO availability.
func (r *HostedClusterReconciler) observeControlPlane(
	ctx context.Context, cluster *hypershiftv1.HostedCluster, cp *hypershiftv1.HostedControlPlane, config string,
) error {
	next := cluster.DeepCopy()
	next.Status.KubeConfig = nil
	if config != "" {
		next.Status.KubeConfig = &corev1.LocalObjectReference{Name: config}
	}

	endpoint, err := r.ignitionEndpoint(ctx, cluster)
	if err != nil {
		return err
	}

	next.Status.IgnitionEndpoint = endpoint
	if cp.Status.ControlPlaneEndpoint.Host != "" {
		next.Status.ControlPlaneEndpoint = cp.Status.ControlPlaneEndpoint
	}

	for _, pair := range [][2]string{
		{"Available", "Available"},
		{"KubeAPIServerAvailable", "KubeAPIServerAvailable"},
		{"InfrastructureReady", "InfrastructureReady"},
		{"ClusterVersionFailing", "ClusterVersionSucceeding"},
	} {
		status, reason := metav1.ConditionUnknown, "WaitingForControlPlane"
		if condition := meta.FindStatusCondition(cp.Status.Conditions, pair[0]); condition != nil {
			status, reason = condition.Status, condition.Reason
		}

		if pair[0] == "ClusterVersionFailing" {
			switch status {
			case metav1.ConditionFalse:
				status = metav1.ConditionTrue
			case metav1.ConditionTrue:
				status = metav1.ConditionFalse
			case metav1.ConditionUnknown:
			}
		}

		meta.SetStatusCondition(&next.Status.Conditions, metav1.Condition{
			Type: pair[1], Status: status, Reason: reason, ObservedGeneration: cluster.Generation,
		})
	}

	degraded := metav1.Condition{
		Type: "Degraded", Status: metav1.ConditionUnknown, Reason: hypershiftv1.StatusUnknownReason,
	}
	if condition := meta.FindStatusCondition(cp.Status.Conditions, "Degraded"); condition != nil {
		degraded = *condition
	}

	degraded.ObservedGeneration = cluster.Generation
	meta.SetStatusCondition(&next.Status.Conditions, degraded)

	if equality.Semantic.DeepEqual(cluster.Status, next.Status) {
		return nil
	}

	return r.Client.Status().Patch(ctx, next,
		client.MergeFromWithOptions(cluster, client.MergeFromWithOptimisticLock{}))
}

// capiClusterOutput describes the unowned Cluster identified by the HostedCluster.
func capiClusterOutput(hc *hypershiftv1.HostedCluster) *capiv1.Cluster {
	return &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{
		Namespace:   hc.Namespace + "-" + hc.Name,
		Name:        hc.Spec.InfraID,
		Annotations: map[string]string{kube.HostedClusterAnnotation: hc.Namespace + "/" + hc.Name},
	}}
}

// inputs supplies the namespace, CAPI Cluster and HCP. Worker installation inputs
// follow the published API endpoint and do not depend on HCP availability.
// A nil HCP with no error means encryption inputs are incomplete.
// HostedCluster and Secret events resume reconciliation.
func (r *HostedClusterReconciler) inputs(
	ctx context.Context, hc *hypershiftv1.HostedCluster,
) (*hypershiftv1.HostedControlPlane, error) {
	if hc.Spec.AdditionalTrustBundle != nil {
		return nil, fmt.Errorf("additional trust bundles are outside the simulated control-plane inputs")
	}

	if cfg := hc.Spec.Configuration; cfg != nil &&
		(len(configrefs.SecretRefs(cfg)) != 0 || len(configrefs.ConfigMapRefs(cfg)) != 0) {
		return nil, fmt.Errorf("configuration Secret and ConfigMap references are outside the simulated control-plane inputs")
	}

	desired := capiClusterOutput(hc)
	namespace := desired.Namespace
	if err := r.ensureNamespace(ctx, namespace, desired.Annotations); err != nil {
		return nil, err
	}

	cluster, err := r.ensureCluster(ctx, hc, desired)
	if err != nil {
		return nil, err
	}

	if err := r.ensureSSHKey(ctx, hc, namespace); err != nil {
		return nil, err
	}

	if ready, err := r.ensureControlPlaneSecrets(ctx, hc, namespace); err != nil || !ready {
		return nil, err
	}

	ready, err := r.copyControlPlaneSecret(ctx, hc, namespace, hc.Spec.PullSecret.Name, controlPlanePullSecretName,
		corev1.SecretTypeDockerConfigJson, corev1.DockerConfigJsonKey)
	if err != nil {
		return nil, err
	}

	if !ready {
		return nil, fmt.Errorf("pull Secret %s/%s must contain %s",
			hc.Namespace, hc.Spec.PullSecret.Name, corev1.DockerConfigJsonKey)
	}

	cp, err := r.ensureControlPlane(ctx, hc, cluster)
	if err != nil {
		return nil, err
	}

	// Resume CAPI after the control-plane inputs have been applied.
	if cluster.Spec.Paused && cluster.DeletionTimestamp == nil && cp.DeletionTimestamp == nil {
		before := cluster.DeepCopy()
		cluster.Spec.Paused = false
		if err := r.Client.Patch(ctx, cluster,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return nil, err
		}
	}

	if cp.Status.ControlPlaneEndpoint.Host != "" {
		if err := r.ensureAgentCluster(ctx, hc, cluster, cp); err != nil {
			return nil, err
		}
	}

	return cp, nil
}

// ensureNamespace preserves the source annotation used across namespace boundaries.
func (r *HostedClusterReconciler) ensureNamespace(
	ctx context.Context,
	name string,
	annotations map[string]string,
) error {
	desired := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: name, Annotations: annotations,
	}}
	current := &corev1.Namespace{}

	err := r.APIReader.Get(ctx, client.ObjectKey{Name: name}, current)
	if apierrors.IsNotFound(err) {
		return r.Client.Create(ctx, desired)
	}

	if err != nil {
		return err
	}

	return validateOutput(current, desired)
}

// ensureCluster validates the native CAPI Cluster and sets its provider references on creation.
func (r *HostedClusterReconciler) ensureCluster(
	ctx context.Context, hc *hypershiftv1.HostedCluster, desired *capiv1.Cluster,
) (*capiv1.Cluster, error) {
	current := &capiv1.Cluster{}

	err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(desired), current)
	if apierrors.IsNotFound(err) {
		desired.Spec.ControlPlaneRef = &corev1.ObjectReference{
			APIVersion: hypershiftv1.GroupVersion.String(), Kind: "HostedControlPlane",
			Namespace: desired.Namespace, Name: hc.Name,
		}
		desired.Spec.InfrastructureRef = &corev1.ObjectReference{
			APIVersion: agentv1.GroupVersion.String(), Kind: "AgentCluster", Namespace: desired.Namespace, Name: hc.Name,
		}

		return desired, r.Client.Create(ctx, desired)
	}

	if err != nil {
		return nil, err
	}

	if err := validateOutput(current, desired); err != nil {
		return nil, err
	}

	return current, nil
}

// ensureControlPlane copies HostedCluster settings into the original Cluster's
// HCP without replacing status or HCP-only settings supplied by other controllers.
func (r *HostedClusterReconciler) ensureControlPlane(
	ctx context.Context, hc *hypershiftv1.HostedCluster, cluster *capiv1.Cluster,
) (*hypershiftv1.HostedControlPlane, error) {
	desired := &hypershiftv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
		Namespace: cluster.Namespace, Name: hc.Name,
		Annotations: map[string]string{kube.HostedClusterAnnotation: hc.Namespace + "/" + hc.Name},
	}}

	current := &hypershiftv1.HostedControlPlane{}

	err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(desired), current)
	create := apierrors.IsNotFound(err)
	if err != nil && !create {
		return nil, err
	}

	if create {
		current = desired
	} else if err := validateProvider(current, cluster); err != nil {
		return nil, err
	}

	if current.DeletionTimestamp != nil {
		return current, nil
	}

	before := current.DeepCopy()

	source := hc.Spec.DeepCopy()
	current.Spec.ReleaseImage = source.Release.Image
	current.Spec.ClusterID, current.Spec.InfraID = source.ClusterID, source.InfraID
	current.Spec.UpdateService, current.Spec.Channel = source.UpdateService, source.Channel
	current.Spec.Platform, current.Spec.DNS = source.Platform, source.DNS
	current.Spec.Platform.Type = hypershiftv1.NonePlatform

	current.Spec.Networking = source.Networking
	current.Spec.PullSecret = corev1.LocalObjectReference{Name: controlPlanePullSecretName}
	if source.SSHKey.Name != "" {
		current.Spec.SSHKey = corev1.LocalObjectReference{Name: "ssh-key"}
	}

	current.Spec.IssuerURL, current.Spec.Etcd = source.IssuerURL, source.Etcd
	current.Spec.ServiceAccountSigningKey = source.ServiceAccountSigningKey
	current.Spec.ControllerAvailabilityPolicy = source.ControllerAvailabilityPolicy
	current.Spec.InfrastructureAvailabilityPolicy = source.InfrastructureAvailabilityPolicy
	current.Spec.FIPS, current.Spec.KubeAPIServerDNSName = source.FIPS, source.KubeAPIServerDNSName
	current.Spec.Services, current.Spec.AuditWebhook = source.Services, source.AuditWebhook
	current.Spec.Configuration, current.Spec.OperatorConfiguration = source.Configuration, source.OperatorConfiguration
	current.Spec.ImageContentSources = source.ImageContentSources
	current.Spec.SecretEncryption, current.Spec.PausedUntil = source.SecretEncryption, source.PausedUntil
	current.Spec.OLMCatalogPlacement, current.Spec.Autoscaling = source.OLMCatalogPlacement, source.Autoscaling

	current.Spec.AutoNode, current.Spec.NodeSelector = source.AutoNode, source.NodeSelector

	current.Spec.Tolerations, current.Spec.Labels = source.Tolerations, source.Labels
	current.Spec.Capabilities = source.Capabilities
	if current.Spec.IssuerURL == "" {
		current.Spec.IssuerURL = "https://kubernetes.default.svc"
	}

	if current.Spec.Etcd.ManagementType == "" {
		current.Spec.Etcd.ManagementType = hypershiftv1.Managed
	}

	if create {
		return current, r.Client.Create(ctx, current)
	}

	if equality.Semantic.DeepEqual(before.Spec, current.Spec) &&
		equality.Semantic.DeepEqual(before.Labels, current.Labels) {
		return current, nil
	}

	return current, r.Client.Patch(ctx, current,
		client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

// ensureAgentCluster forwards the published endpoint for worker installation.
func (r *HostedClusterReconciler) ensureAgentCluster(
	ctx context.Context, hc *hypershiftv1.HostedCluster, cluster *capiv1.Cluster, cp *hypershiftv1.HostedControlPlane,
) error {
	endpoint, err := r.ignitionEndpoint(ctx, hc)
	if err != nil || endpoint == "" {
		return err
	}

	desired := &agentv1.AgentCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace, Name: hc.Name,
			Annotations: map[string]string{kube.HostedClusterAnnotation: hc.Namespace + "/" + hc.Name},
		},
		Spec: agentv1.AgentClusterSpec{
			ControlPlaneEndpoint: capiv1.APIEndpoint{
				Host: cp.Status.ControlPlaneEndpoint.Host,
				Port: cp.Status.ControlPlaneEndpoint.Port,
			},
			IgnitionEndpoint: &agentv1.IgnitionEndpoint{
				Url: "https://" + endpoint + "/ignition",
				CaCertificateReference: &agentv1.CaCertificateReference{
					Namespace: cluster.Namespace,
					Name:      "ignition-server-ca-cert",
				},
			},
		},
	}
	current := &agentv1.AgentCluster{}

	err = r.APIReader.Get(ctx, client.ObjectKeyFromObject(desired), current)
	if apierrors.IsNotFound(err) {
		return r.Client.Create(ctx, desired)
	}

	if err != nil {
		return err
	}

	if err := validateProvider(current, cluster); err != nil {
		return err
	}

	if current.DeletionTimestamp != nil {
		return nil
	}

	next := current.DeepCopy()

	next.Spec.ControlPlaneEndpoint = desired.Spec.ControlPlaneEndpoint
	next.Spec.IgnitionEndpoint = desired.Spec.IgnitionEndpoint
	if equality.Semantic.DeepEqual(current.Spec, next.Spec) && equality.Semantic.DeepEqual(current.Labels, next.Labels) {
		return nil
	}

	return r.Client.Patch(ctx, next,
		client.MergeFromWithOptions(current, client.MergeFromWithOptimisticLock{}))
}

// delete waits for NodePools, then the CAPI Cluster and finally namespace cleanup.
// It deletes only the original cluster's access copy and linked provider outputs.
func (r *HostedClusterReconciler) delete(ctx context.Context, hc *hypershiftv1.HostedCluster) (ctrl.Result, error) {
	pending, err := r.deletePools(ctx, hc)
	if err != nil {
		return ctrl.Result{}, err
	}

	if pending {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	namespace := &corev1.Namespace{}

	err = r.APIReader.Get(ctx, client.ObjectKey{Name: hc.Namespace + "-" + hc.Name}, namespace)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	if apierrors.IsNotFound(err) {
		namespace = nil
	}

	pending, err = r.deleteCAPICluster(ctx, hc, namespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	if pending {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	secret := &corev1.Secret{}

	err = r.APIReader.Get(ctx, client.ObjectKey{Namespace: hc.Namespace, Name: hc.Name + "-admin-kubeconfig"}, secret)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	if err == nil && kube.MatchesController(secret, hc, hypershiftv1.GroupVersion.WithKind("HostedCluster")) {
		if err := kube.Delete(ctx, r.Client, secret); err != nil {
			return ctrl.Result{}, err
		}
	}

	if namespace != nil && hostedClusterOutput(namespace, hc) {
		pending, err := r.cleanupControlPlaneNamespace(ctx, hc, namespace)
		if err != nil {
			return ctrl.Result{}, err
		}

		if pending {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
	}

	return ctrl.Result{}, kube.RemoveFinalizer(ctx, r.Client, hc, hostedClusterFinalizer)
}

// cleanupControlPlaneNamespace retires a retained namespace's unadopted HCP
// or deletes the namespace when retention is disabled.
func (r *HostedClusterReconciler) cleanupControlPlaneNamespace(
	ctx context.Context, hc *hypershiftv1.HostedCluster, namespace *corev1.Namespace,
) (bool, error) {
	if hc.Annotations[hypershiftv1.SkipControlPlaneNamespaceDeletionAnnotation] != "true" {
		if err := kube.Delete(ctx, r.Client, namespace); err != nil {
			return false, fmt.Errorf("delete control-plane namespace: %w", err)
		}

		return true, nil
	}

	cp := &hypershiftv1.HostedControlPlane{}
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: namespace.Name, Name: hc.Name}, cp); err != nil {
		return false, client.IgnoreNotFound(err)
	}

	if !hostedClusterOutput(cp, hc) {
		return false, nil
	}

	if len(cp.OwnerReferences) != 0 {
		return false, fmt.Errorf("control plane %s/%s still has owners", cp.Namespace, cp.Name)
	}

	return true, kube.Delete(ctx, r.Client, cp)
}

// deletePools requests deletion of this cluster's pools and waits for their
// controllers to finish releasing worker claims.
func (r *HostedClusterReconciler) deletePools(ctx context.Context, hc *hypershiftv1.HostedCluster) (bool, error) {
	var pools hypershiftv1.NodePoolList

	if err := r.APIReader.List(ctx, &pools, client.InNamespace(hc.Namespace)); err != nil {
		return false, err
	}

	pending := false

	for i := range pools.Items {
		if pools.Items[i].Spec.ClusterName == hc.Name {
			pending = true

			if err := kube.Delete(ctx, r.Client, &pools.Items[i]); err != nil {
				return false, err
			}
		}
	}

	return pending, nil
}

// deleteCAPICluster waits for the linked native Cluster to disappear before
// namespace cleanup. Foreign Clusters in that namespace are preserved.
func (r *HostedClusterReconciler) deleteCAPICluster(
	ctx context.Context, hc *hypershiftv1.HostedCluster, namespace *corev1.Namespace,
) (bool, error) {
	if hc.Spec.InfraID == "" || namespace == nil || !hostedClusterOutput(namespace, hc) {
		return false, nil
	}

	desired := capiClusterOutput(hc)
	cluster := &capiv1.Cluster{}

	err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(desired), cluster)
	if apierrors.IsNotFound(err) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	if err := validateOutput(cluster, desired); err != nil {
		return false, err
	}

	return true, kube.Delete(ctx, r.Client, cluster)
}
