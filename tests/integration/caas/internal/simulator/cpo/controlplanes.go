// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

// Package cpo simulates control-plane operator responses on HostedControlPlanes.
// Guest processes are represented by native status and non-functional access data.
package cpo

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"strconv"
	"time"

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

// placeholderGuestKubeconfig supplies parseable access data with an unreachable
// reserved endpoint and empty credentials. It grants no guest cluster access.
const placeholderGuestKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: simulated-guest
  cluster:
    server: https://guest.caas-it.invalid:6443
contexts:
- name: simulated-guest
  context:
    cluster: simulated-guest
    user: simulated-guest
current-context: simulated-guest
users:
- name: simulated-guest
  user: {}
`

// HostedControlPlaneReconciler creates the HCP's API and ignition Services, CA and
// kubeconfig Secrets, then publishes references and the selected availability response.
type HostedControlPlaneReconciler struct {
	Client       client.Client
	APIReader    client.Reader
	Scope        kube.Scope
	Availability func(context.Context, *hypershiftv1.HostedControlPlane) (available, configured bool, err error)
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

	if paused, duration := reconciliationPause(ctx, controlPlane.Spec.PausedUntil); paused {
		next := controlPlane.DeepCopy()
		meta.SetStatusCondition(
			&next.Status.Conditions,
			metav1.Condition{
				Type:               "ReconciliationActive",
				Status:             metav1.ConditionFalse,
				Reason:             hypershiftv1.ReconciliationPausedConditionReason,
				ObservedGeneration: controlPlane.Generation,
			},
		)

		var err error

		if !equality.Semantic.DeepEqual(controlPlane.Status, next.Status) {
			err = r.Client.Status().
				Patch(ctx, next, client.MergeFromWithOptions(controlPlane, client.MergeFromWithOptimisticLock{}))
		}

		return ctrl.Result{RequeueAfter: duration}, err
	}

	available, configured, err := r.Availability(ctx, controlPlane)
	if err != nil {
		return ctrl.Result{}, err
	}

	if !configured {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	service, err := r.ensureAPIService(ctx, controlPlane)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.ensureIgnition(ctx, controlPlane); err != nil {
		return ctrl.Result{}, err
	}

	secret, err := r.ensureKubeconfig(ctx, controlPlane)
	if err != nil {
		return ctrl.Result{}, err
	}

	next := controlPlaneStatus(controlPlane, service, secret.Name, available)
	if !equality.Semantic.DeepEqual(controlPlane.Status, next.Status) {
		if err := r.Client.Status().Patch(ctx, next,
			client.MergeFromWithOptions(controlPlane, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Poll the live response even after readiness; unchanged status writes are skipped.
	return ctrl.Result{RequeueAfter: time.Second}, nil
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

// controlPlaneStatus returns endpoint, access, readiness and controller health status.
// It keeps worker connection details available while control-plane availability is held.
func controlPlaneStatus(
	controlPlane *hypershiftv1.HostedControlPlane,
	service *corev1.Service,
	kubeconfigName string,
	available bool,
) *hypershiftv1.HostedControlPlane {
	address, port := apiEndpoint(controlPlane, service)

	ready := address != "" && available
	status, reason := metav1.ConditionFalse, "ControlPlaneStarting"
	if ready {
		status, reason = metav1.ConditionTrue, hypershiftv1.AsExpectedReason
	}

	next := controlPlane.DeepCopy()

	meta.SetStatusCondition(
		&next.Status.Conditions,
		metav1.Condition{
			Type:               "ReconciliationActive",
			Status:             metav1.ConditionTrue,
			Reason:             hypershiftv1.AsExpectedReason,
			ObservedGeneration: controlPlane.Generation,
		},
	)
	next.Status.Ready = ready

	// The simulated backend is healthy independently of control-plane availability.
	meta.SetStatusCondition(&next.Status.Conditions, metav1.Condition{
		Type: "Degraded", Status: metav1.ConditionFalse, Reason: hypershiftv1.AsExpectedReason,
		ObservedGeneration: controlPlane.Generation,
	})

	// Management-side inputs stand in for guest initialization; availability is observed separately.
	next.Status.Initialized = true
	next.Status.KubeConfig = &hypershiftv1.KubeconfigSecretRef{Name: kubeconfigName, Key: "kubeconfig"}
	if address != "" {
		next.Status.ControlPlaneEndpoint = hypershiftv1.APIEndpoint{Host: address, Port: port}
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

// apiStrategy finds the required API-server publication contract.
func apiStrategy(controlPlane *hypershiftv1.HostedControlPlane) *hypershiftv1.ServicePublishingStrategy {
	for i := range controlPlane.Spec.Services {
		if controlPlane.Spec.Services[i].Service == hypershiftv1.APIServer {
			return &controlPlane.Spec.Services[i].ServicePublishingStrategy
		}
	}

	return nil
}

// ensureAPIService converges CPO-owned Service fields while preserving allocated
// addresses, node ports and fields managed by the API server or other controllers.
func (r *HostedControlPlaneReconciler) ensureAPIService(
	ctx context.Context,
	controlPlane *hypershiftv1.HostedControlPlane,
) (*corev1.Service, error) {
	key := client.ObjectKey{Namespace: controlPlane.Namespace, Name: "kube-apiserver"}
	service := &corev1.Service{}

	err := r.APIReader.Get(ctx, key, service)
	create := apierrors.IsNotFound(err)
	if err != nil && !create {
		return nil, err
	}

	if create {
		service.Namespace, service.Name = key.Namespace, key.Name
		if err := controllerutil.SetControllerReference(controlPlane, service, r.Client.Scheme()); err != nil {
			return nil, err
		}
	} else if !kube.MatchesController(service, controlPlane, hypershiftv1.GroupVersion.WithKind("HostedControlPlane")) {
		return nil, fmt.Errorf("service %s/%s belongs to another control plane", key.Namespace, key.Name)
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

// ensureIgnition supplies the HCP-owned CA and ignition Services for NodePort allocation.
// Existing controllers retain their identity; ownerless Services can be adopted.
func (r *HostedControlPlaneReconciler) ensureIgnition(
	ctx context.Context, cp *hypershiftv1.HostedControlPlane,
) error {
	for _, mapping := range cp.Spec.Services {
		if mapping.Service != hypershiftv1.Ignition || mapping.Type != hypershiftv1.NodePort {
			continue
		}

		if mapping.NodePort == nil {
			return fmt.Errorf("ignition NodePort metadata is required")
		}

		for _, name := range []string{"ignition-server", "ignition-server-proxy"} {
			service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: cp.Namespace, Name: name}}

			_, err := controllerutil.CreateOrUpdate(ctx, r.Client, service, func() error {
				return r.configureIgnitionService(service, cp, mapping.NodePort)
			})
			if err != nil {
				return err
			}
		}
	}

	return r.ensureIgnitionCA(ctx, cp)
}

// configureIgnitionService validates HCP ownership before setting publishing fields.
// Assigned ports and unrelated Service fields retain their existing values.
func (r *HostedControlPlaneReconciler) configureIgnitionService(
	service *corev1.Service, cp *hypershiftv1.HostedControlPlane, nodePort *hypershiftv1.NodePortPublishingStrategy,
) error {
	if metav1.GetControllerOf(service) != nil &&
		!kube.MatchesController(service, cp, hypershiftv1.GroupVersion.WithKind("HostedControlPlane")) {
		return fmt.Errorf("service %s/%s belongs to another control plane", cp.Namespace, service.Name)
	}

	if kube.OwnerReference(service, "HostedControlPlane") != nil &&
		!kube.MatchesOwner(service, cp, hypershiftv1.GroupVersion.WithKind("HostedControlPlane")) {
		return fmt.Errorf("service %s/%s belongs to another control plane", cp.Namespace, service.Name)
	}

	if err := controllerutil.SetControllerReference(cp, service, r.Client.Scheme()); err != nil {
		return err
	}

	service.Spec.Selector = map[string]string{"app": service.Name}

	port := corev1.ServicePort{Name: "https", Port: 443, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt(9090)}
	service.Spec.Type = corev1.ServiceTypeClusterIP
	if service.Name == "ignition-server-proxy" {
		if len(service.Spec.Ports) > 0 {
			port = service.Spec.Ports[0]
		}

		port.Name, port.Port, port.Protocol, port.TargetPort = "https", 443, corev1.ProtocolTCP, intstr.FromString("https")
		if port.NodePort == 0 {
			port.NodePort = nodePort.Port
		}

		service.Spec.Type = corev1.ServiceTypeNodePort
	}

	if len(service.Spec.Ports) == 0 {
		service.Spec.Ports = []corev1.ServicePort{port}
	} else {
		service.Spec.Ports[0] = port
	}

	return nil
}

// apiEndpoint applies configured hostname precedence to the observed Service address.
func apiEndpoint(controlPlane *hypershiftv1.HostedControlPlane, service *corev1.Service) (string, int32) {
	port := int32(6443)
	if len(service.Spec.Ports) > 0 {
		port = service.Spec.Ports[0].Port
	}

	strategy := apiStrategy(controlPlane)
	if strategy == nil {
		return "", port
	}

	switch strategy.Type {
	case hypershiftv1.NodePort:
		if strategy.NodePort == nil || len(service.Spec.Ports) == 0 || service.Spec.Ports[0].NodePort == 0 {
			return "", port
		}

		return strategy.NodePort.Address, service.Spec.Ports[0].NodePort
	case hypershiftv1.LoadBalancer:
		if len(service.Status.LoadBalancer.Ingress) == 0 {
			return "", port
		}

		if strategy.LoadBalancer != nil && strategy.LoadBalancer.Hostname != "" {
			return strategy.LoadBalancer.Hostname, port
		}

		if service.Status.LoadBalancer.Ingress[0].Hostname != "" {
			return service.Status.LoadBalancer.Ingress[0].Hostname, port
		}

		return service.Status.LoadBalancer.Ingress[0].IP, port
	default:
		return "", port
	}
}

// apiServiceSpec writes only CPO's native publishing fields on a validated Service.
func apiServiceSpec(service *corev1.Service, controlPlane *hypershiftv1.HostedControlPlane) error {
	strategy := apiStrategy(controlPlane)
	if strategy == nil {
		return fmt.Errorf("API server publishing strategy is required")
	}

	metav1.SetMetaDataLabel(&service.ObjectMeta, kube.ManagedByLabel, kube.ManagerName)
	metav1.SetMetaDataLabel(&service.ObjectMeta, "app", "kube-apiserver")

	if service.Spec.Selector == nil {
		service.Spec.Selector = map[string]string{"app": "kube-apiserver"}
	}

	service.Spec.IPFamilyPolicy = new(corev1.IPFamilyPolicyPreferDualStack)
	if len(service.Spec.Ports) == 0 {
		service.Spec.Ports = []corev1.ServicePort{{}}
	}

	port := &service.Spec.Ports[0]

	port.Port, port.Protocol, port.TargetPort = 6443, corev1.ProtocolTCP, intstr.FromString("client")
	service.Spec.LoadBalancerSourceRanges = nil
	if networking := controlPlane.Spec.Networking.APIServer; networking != nil {
		if networking.Port != nil {
			port.Port = *networking.Port
		}

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

// ensureIgnitionCA retains the original HCP's signing material across reconciliation.
// Only native certificate fields are generated; no server or guest access is provided.
func (r *HostedControlPlaneReconciler) ensureIgnitionCA(
	ctx context.Context,
	cp *hypershiftv1.HostedControlPlane,
) error {
	desired := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: cp.Namespace,
		Name:      "ignition-server-ca-cert",
		Labels:    map[string]string{kube.ManagedByLabel: kube.ManagerName},
	}}
	if err := controllerutil.SetControllerReference(cp, desired, r.Client.Scheme()); err != nil {
		return err
	}

	current := &corev1.Secret{}

	err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(desired), current)
	create := apierrors.IsNotFound(err)
	if err != nil && !create {
		return err
	}

	if create {
		current = desired
	} else if !kube.MatchesController(current, cp, hypershiftv1.GroupVersion.WithKind("HostedControlPlane")) {
		return fmt.Errorf("secret %s/%s belongs to another control plane", current.Namespace, current.Name)
	}

	if current.DeletionTimestamp != nil {
		return fmt.Errorf("ignition CA is deleting")
	}

	if len(current.Data[corev1.TLSCertKey]) > 0 && len(current.Data[corev1.TLSPrivateKeyKey]) > 0 {
		return nil
	}

	before := current.DeepCopy()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}

	certificate := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "ignition-ca"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().AddDate(1, 0, 0),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}

	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		return err
	}

	privateKey, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}

	if current.Data == nil {
		current.Data = map[string][]byte{}
	}

	current.Type = corev1.SecretTypeTLS

	current.Data[corev1.TLSCertKey] = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	current.Data[corev1.TLSPrivateKeyKey] = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privateKey})
	if create {
		return r.Client.Create(ctx, current)
	}

	return r.Client.Patch(ctx, current, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

// ensureKubeconfig maintains the HCP-owned access Secret while preserving its
// other metadata and refusing an existing Secret from another control plane.
func (r *HostedControlPlaneReconciler) ensureKubeconfig(
	ctx context.Context,
	controlPlane *hypershiftv1.HostedControlPlane,
) (*corev1.Secret, error) {
	key := client.ObjectKey{Namespace: controlPlane.Namespace, Name: controlPlane.Name + "-admin-kubeconfig"}

	secret := &corev1.Secret{}
	err := r.APIReader.Get(ctx, key, secret)
	if apierrors.IsNotFound(err) {
		secret.Namespace, secret.Name = key.Namespace, key.Name

		secret.Labels = map[string]string{kube.ManagedByLabel: kube.ManagerName}
		secret.Data = map[string][]byte{"kubeconfig": []byte(placeholderGuestKubeconfig)}
		if err := controllerutil.SetControllerReference(controlPlane, secret, r.Client.Scheme()); err != nil {
			return nil, err
		}

		return secret, r.Client.Create(ctx, secret)
	}

	if err != nil {
		return nil, err
	}

	if !kube.MatchesController(secret, controlPlane, hypershiftv1.GroupVersion.WithKind("HostedControlPlane")) {
		return nil, fmt.Errorf("secret %s/%s belongs to another control plane", key.Namespace, key.Name)
	}

	before := secret.DeepCopy()

	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}

	secret.Data["kubeconfig"] = []byte(placeholderGuestKubeconfig)
	metav1.SetMetaDataLabel(&secret.ObjectMeta, kube.ManagedByLabel, kube.ManagerName)

	if !equality.Semantic.DeepEqual(before.Data, secret.Data) ||
		!equality.Semantic.DeepEqual(before.Labels, secret.Labels) {
		if err := r.Client.Patch(ctx, secret,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return nil, err
		}
	}

	return secret, nil
}

// SetupWithManager watches HostedControlPlanes and their owned Services and Secrets.
// Child updates let published addresses and access data wake their control plane.
func (r *HostedControlPlaneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).Named("simulated-cpo-hostedcontrolplane").
		For(&hypershiftv1.HostedControlPlane{}).
		Owns(&corev1.Service{}).Owns(&corev1.Secret{}).
		WithOptions(kube.ControllerOptions()).Complete(r)
}
