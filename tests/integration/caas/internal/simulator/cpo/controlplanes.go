// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

// Package cpo simulates control-plane operator responses on HostedControlPlanes.
// Guest processes are represented by native status and non-functional access data.
package cpo

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	routev1 "github.com/openshift/api/route/v1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

const controlPlaneFinalizer = "hypershift.openshift.io/finalizer"

// apiServicePort is pinned HyperShift's KASSVCPort for the None platform.
const apiServicePort int32 = 6443

// HostedControlPlaneReconciler creates the HCP's API and ignition Services, CA and
// kubeconfig Secrets, then publishes references and the selected availability response.
type HostedControlPlaneReconciler struct {
	Client               client.Client
	APIReader            client.Reader
	Scope                kube.Scope
	DefaultIngressDomain string
	Availability         func(context.Context, *hypershiftv1.HostedControlPlane) (bool, error)
}

// Reconcile serves None-platform HCPs created for Agent hosted clusters in scope.
// Endpoint and kubeconfig inputs remain available during an availability hold so
// worker installation can progress independently of control-plane readiness.
func (r *HostedControlPlaneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	controlPlane := &hypershiftv1.HostedControlPlane{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, controlPlane); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if controlPlane.Spec.Platform.Type != hypershiftv1.NonePlatform || controlPlane.Spec.Platform.Agent == nil ||
		!r.Scope.Contains(controlPlane.Spec.Platform.Agent.AgentNamespace) {
		return ctrl.Result{}, nil
	}

	if controlPlane.DeletionTimestamp != nil {
		// None-platform cleanup finishes without cloud-provider work.
		return ctrl.Result{}, kube.RemoveFinalizer(ctx, r.Client, controlPlane, controlPlaneFinalizer)
	}

	if err := kube.AddFinalizer(ctx, r.Client, controlPlane, controlPlaneFinalizer); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.observeReconciliationActive(ctx, controlPlane); err != nil {
		return ctrl.Result{}, err
	}

	paused, requeue := reconciliationPause(ctx, controlPlane.Spec.PausedUntil)

	service, err := r.readAPIService(ctx, controlPlane)
	if err != nil {
		return ctrl.Result{}, err
	}

	var kubeconfig *hypershiftv1.KubeconfigSecretRef

	if !paused {
		service, kubeconfig, err = r.ensureOutputs(ctx, controlPlane, service)
		if err != nil {
			return ctrl.Result{}, err
		}

		// Poll live availability while output reconciliation is active.
		requeue = time.Second
	}

	available, observationError := r.Availability(ctx, controlPlane)

	next := controlPlaneStatus(controlPlane, service, kubeconfig, available, observationError)

	if !equality.Semantic.DeepEqual(controlPlane.Status, next.Status) {
		if err := r.Client.Status().Patch(ctx, next,
			client.MergeFromWithOptions(controlPlane, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, errors.Join(observationError, err)
		}
	}

	return ctrl.Result{RequeueAfter: requeue}, observationError
}

// ensureOutputs reconciles API and ignition endpoints before publishing guest access data.
func (r *HostedControlPlaneReconciler) ensureOutputs(
	ctx context.Context, cp *hypershiftv1.HostedControlPlane, service *corev1.Service,
) (*corev1.Service, *hypershiftv1.KubeconfigSecretRef, error) {
	service, err := r.ensureAPIService(ctx, cp, service)
	if err != nil {
		return nil, nil, err
	}

	if err := r.ensureIgnition(ctx, cp); err != nil {
		return nil, nil, err
	}

	kubeconfig, err := r.ensureKubeconfig(ctx, cp, service)

	return service, kubeconfig, err
}

// observeReconciliationActive reports pause diagnostics independently of output readiness.
func (r *HostedControlPlaneReconciler) observeReconciliationActive(
	ctx context.Context, cp *hypershiftv1.HostedControlPlane,
) error {
	before := cp.DeepCopy()
	meta.SetStatusCondition(&cp.Status.Conditions, reconciliationActiveCondition(cp.Spec.PausedUntil, cp.Generation))

	if equality.Semantic.DeepEqual(before.Status, cp.Status) {
		return nil
	}

	return r.Client.Status().Patch(ctx, cp, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

// reconciliationPause parses native Boolean and timestamp values, logging malformed input as unpaused.
func reconciliationPause(ctx context.Context, value *string) (bool, time.Duration) {
	if value == nil {
		return false, 0
	}

	if paused, err := strconv.ParseBool(*value); err == nil {
		return paused, 0
	}

	until, err := time.Parse(time.RFC3339, *value)
	if err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "invalid pausedUntil; considering control plane unpaused")

		return false, 0
	}

	duration := time.Until(until)

	return duration > 0, duration
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

// controlPlaneStatus returns endpoint, access, readiness and controller health status.
// It keeps worker connection details available while control-plane availability is held.
func controlPlaneStatus(
	controlPlane *hypershiftv1.HostedControlPlane,
	service *corev1.Service,
	kubeconfig *hypershiftv1.KubeconfigSecretRef,
	available bool,
	observationError error,
) *hypershiftv1.HostedControlPlane {
	address, port := apiEndpoint(controlPlane, service)
	endpoint := hypershiftv1.APIEndpoint{Host: address, Port: port}
	// A read-only pass can confirm the published endpoint but cannot publish a new one.
	published := controlPlane.Status.KubeConfig != nil && endpoint == controlPlane.Status.ControlPlaneEndpoint
	if kubeconfig == nil && !published {
		address = ""
	}

	ready := address != "" && available && observationError == nil
	status, reason := metav1.ConditionFalse, "ControlPlaneStarting"
	if ready {
		status, reason = metav1.ConditionTrue, hypershiftv1.AsExpectedReason
	}

	next := controlPlane.DeepCopy()
	next.Status.Ready = ready

	// Observation errors indicate degraded controller health independently of availability.
	degraded := metav1.Condition{
		Type: "Degraded", Status: metav1.ConditionFalse, Reason: hypershiftv1.AsExpectedReason,
		ObservedGeneration: controlPlane.Generation,
	}
	if observationError != nil {
		degraded.Status = metav1.ConditionTrue
		degraded.Reason, degraded.Message = "ReconciliationError", observationError.Error()
	}

	meta.SetStatusCondition(&next.Status.Conditions, degraded)

	// Management-side inputs stand in for guest initialization; availability is observed separately.
	if kubeconfig != nil {
		next.Status.Initialized = true
		next.Status.KubeConfig = kubeconfig
	}

	if address != "" {
		next.Status.ControlPlaneEndpoint = endpoint
	}

	for _, kind := range []string{"Available", "KubeAPIServerAvailable"} {
		meta.SetStatusCondition(&next.Status.Conditions, metav1.Condition{
			Type: kind, Status: status, Reason: reason,
			Message: "Simulated provider observation", ObservedGeneration: controlPlane.Generation,
		})
	}

	infrastructureStatus := metav1.ConditionFalse
	infrastructureReason := hypershiftv1.WaitingOnInfrastructureReadyReason
	message := "Cluster infrastructure is still provisioning"

	if address != "" {
		infrastructureStatus = metav1.ConditionTrue
		infrastructureReason, message = hypershiftv1.AsExpectedReason, hypershiftv1.AllIsWellMessage
	}

	meta.SetStatusCondition(&next.Status.Conditions, metav1.Condition{
		Type: "InfrastructureReady", Status: infrastructureStatus, Reason: infrastructureReason,
		Message: message, ObservedGeneration: controlPlane.Generation,
	})

	return next
}

// publishingStrategy returns the first native publishing mapping for a service.
func publishingStrategy(
	controlPlane *hypershiftv1.HostedControlPlane, service hypershiftv1.ServiceType,
) *hypershiftv1.ServicePublishingStrategy {
	for i := range controlPlane.Spec.Services {
		if controlPlane.Spec.Services[i].Service == service {
			return &controlPlane.Spec.Services[i].ServicePublishingStrategy
		}
	}

	return nil
}

const apiServiceName = "kube-apiserver"

// readAPIService observes the Service for this control plane's incarnation, or nil while absent.
func (r *HostedControlPlaneReconciler) readAPIService(
	ctx context.Context, controlPlane *hypershiftv1.HostedControlPlane,
) (*corev1.Service, error) {
	key := client.ObjectKey{Namespace: controlPlane.Namespace, Name: apiServiceName}
	service := &corev1.Service{}

	err := r.APIReader.Get(ctx, key, service)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	if !kube.MatchesController(service, controlPlane, hypershiftv1.GroupVersion.WithKind("HostedControlPlane")) {
		return nil, fmt.Errorf("service %s/%s belongs to another control plane", key.Namespace, key.Name)
	}

	return service, nil
}

// ensureAPIService converges the validated Service while preserving API-server allocations.
func (r *HostedControlPlaneReconciler) ensureAPIService(
	ctx context.Context, controlPlane *hypershiftv1.HostedControlPlane, service *corev1.Service,
) (*corev1.Service, error) {
	create := service == nil
	if create {
		service = &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: controlPlane.Namespace, Name: apiServiceName}}
		if err := controllerutil.SetControllerReference(controlPlane, service, r.Client.Scheme()); err != nil {
			return nil, err
		}
	}

	if service.DeletionTimestamp != nil {
		return service, nil
	}

	before := service.DeepCopy()
	if err := apiServiceSpec(service, controlPlane); err != nil {
		return nil, err
	}

	if create {
		return service, r.Client.Create(ctx, service)
	}

	if equality.Semantic.DeepEqual(before.Spec, service.Spec) &&
		equality.Semantic.DeepEqual(before.Labels, service.Labels) &&
		equality.Semantic.DeepEqual(before.Annotations, service.Annotations) {
		return service, nil
	}

	return service, r.Client.Patch(ctx, service, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

// apiEndpoint applies configured hostname precedence to the observed Service address.
func apiEndpoint(controlPlane *hypershiftv1.HostedControlPlane, service *corev1.Service) (string, int32) {
	if service == nil || service.DeletionTimestamp != nil {
		return "", apiServicePort
	}

	strategy := publishingStrategy(controlPlane, hypershiftv1.APIServer)
	if strategy == nil {
		return "", apiServicePort
	}

	switch strategy.Type {
	case hypershiftv1.NodePort:
		if strategy.NodePort == nil || len(service.Spec.Ports) == 0 || service.Spec.Ports[0].NodePort == 0 {
			return "", apiServicePort
		}

		return strategy.NodePort.Address, service.Spec.Ports[0].NodePort
	case hypershiftv1.LoadBalancer:
		if len(service.Status.LoadBalancer.Ingress) == 0 {
			return "", apiServicePort
		}

		if strategy.LoadBalancer != nil && strategy.LoadBalancer.Hostname != "" {
			return strategy.LoadBalancer.Hostname, apiServicePort
		}

		if service.Status.LoadBalancer.Ingress[0].Hostname != "" {
			return service.Status.LoadBalancer.Ingress[0].Hostname, apiServicePort
		}

		return service.Status.LoadBalancer.Ingress[0].IP, apiServicePort
	default:
		return "", apiServicePort
	}
}

// apiServiceSpec writes only CPO's native publishing fields on a validated Service.
func apiServiceSpec(service *corev1.Service, controlPlane *hypershiftv1.HostedControlPlane) error {
	strategy := publishingStrategy(controlPlane, hypershiftv1.APIServer)
	if strategy == nil {
		return fmt.Errorf("API server publishing strategy is required")
	}

	metav1.SetMetaDataLabel(&service.ObjectMeta, "app", "kube-apiserver")

	if service.Spec.Selector == nil {
		service.Spec.Selector = map[string]string{"app": "kube-apiserver"}
	}

	service.Spec.IPFamilyPolicy = new(corev1.IPFamilyPolicyPreferDualStack)
	if len(service.Spec.Ports) == 0 {
		service.Spec.Ports = []corev1.ServicePort{{}}
	}

	port := &service.Spec.Ports[0]

	port.Port, port.Protocol, port.TargetPort = apiServicePort, corev1.ProtocolTCP, intstr.FromString("client")

	service.Spec.LoadBalancerSourceRanges = nil
	if networking := controlPlane.Spec.Networking.APIServer; networking != nil {
		for _, cidr := range networking.AllowedCIDRBlocks {
			service.Spec.LoadBalancerSourceRanges = append(service.Spec.LoadBalancerSourceRanges, string(cidr))
		}
	}

	switch strategy.Type {
	case hypershiftv1.LoadBalancer:
		service.Spec.Type = corev1.ServiceTypeLoadBalancer
		if strategy.LoadBalancer != nil && strategy.LoadBalancer.Hostname != "" {
			metav1.SetMetaDataAnnotation(
				&service.ObjectMeta,
				hypershiftv1.ExternalDNSHostnameAnnotation,
				strategy.LoadBalancer.Hostname,
			)
		}
	case hypershiftv1.NodePort:
		service.Spec.Type = corev1.ServiceTypeNodePort

		if port.NodePort == 0 && strategy.NodePort != nil {
			port.NodePort = strategy.NodePort.Port
		}
	default:
		return fmt.Errorf("unsupported API server publishing strategy %q", strategy.Type)
	}

	return nil
}

// SetupWithManager watches HostedControlPlanes and their owned Services, Secrets and Routes.
// Child updates let published addresses and access data wake their control plane.
func (r *HostedControlPlaneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).Named("simulated-cpo-hostedcontrolplane").
		For(&hypershiftv1.HostedControlPlane{}).
		Owns(&corev1.Service{}).Owns(&corev1.Secret{}).Owns(&routev1.Route{}).
		WithOptions(kube.ControllerOptions()).Complete(r)
}
