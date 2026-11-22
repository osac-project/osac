// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package cpo

import (
	"context"
	"fmt"
	"hash/fnv"

	routev1 "github.com/openshift/api/route/v1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/certs"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// ensureIgnition publishes native ignition Services and observes Route ingress before PKI.
// Existing controllers retain their identity; ownerless Services can be adopted.
func (r *HostedControlPlaneReconciler) ensureIgnition(
	ctx context.Context, cp *hypershiftv1.HostedControlPlane,
) error {
	strategy := publishingStrategy(cp, hypershiftv1.Ignition)
	if strategy == nil {
		return fmt.Errorf("ignition publishing strategy is required")
	}

	if err := r.ensureIgnitionServices(ctx, cp, strategy); err != nil {
		return err
	}

	if strategy.Type == hypershiftv1.Route {
		route, err := r.ensureIgnitionRoute(ctx, cp, strategy.Route)
		if err != nil {
			return err
		}

		if len(route.Status.Ingress) == 0 || route.Status.Ingress[0].Host == "" {
			return nil
		}
	}

	if _, disabled := cp.Annotations[hypershiftv1.DisablePKIReconciliationAnnotation]; disabled {
		return nil
	}

	return r.ensureIgnitionCA(ctx, cp)
}

// ensureIgnitionServices publishes the backend and proxy Services from a native strategy.
func (r *HostedControlPlaneReconciler) ensureIgnitionServices(
	ctx context.Context, cp *hypershiftv1.HostedControlPlane, strategy *hypershiftv1.ServicePublishingStrategy,
) error {
	switch strategy.Type {
	case hypershiftv1.NodePort:
		if strategy.NodePort == nil {
			return fmt.Errorf("ignition NodePort metadata is required")
		}
	case hypershiftv1.Route:
	default:
		return fmt.Errorf("unsupported ignition publishing strategy %q", strategy.Type)
	}

	for _, name := range []string{"ignition-server", "ignition-server-proxy"} {
		service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: cp.Namespace, Name: name}}

		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, service, func() error {
			return r.configureIgnitionService(service, cp, strategy)
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// configureIgnitionService validates HCP ownership before setting publishing fields.
// Assigned ports and unrelated Service fields retain their existing values.
func (r *HostedControlPlaneReconciler) configureIgnitionService(
	service *corev1.Service, cp *hypershiftv1.HostedControlPlane, strategy *hypershiftv1.ServicePublishingStrategy,
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

	service.Spec.Type = corev1.ServiceTypeClusterIP
	if service.Name == "ignition-server" {
		service.Spec.Ports = []corev1.ServicePort{{
			Name: "https", Port: 443, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt(9090),
		}}

		return nil
	}

	port := corev1.ServicePort{}
	if len(service.Spec.Ports) > 0 {
		port = service.Spec.Ports[0]
	}

	port.Name, port.Port, port.Protocol, port.TargetPort = "https", 443, corev1.ProtocolTCP, intstr.FromString("https")
	if strategy.Type == hypershiftv1.NodePort {
		if port.NodePort == 0 {
			port.NodePort = strategy.NodePort.Port
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

// ensureIgnitionRoute converges the native external Route while preserving admission observations.
// Ordinary hostnames are assigned by OpenShift API defaulting; missing observations remain pending.
func (r *HostedControlPlaneReconciler) ensureIgnitionRoute(
	ctx context.Context, cp *hypershiftv1.HostedControlPlane, strategy *hypershiftv1.RoutePublishingStrategy,
) (*routev1.Route, error) {
	route := &routev1.Route{}
	key := client.ObjectKey{Namespace: cp.Namespace, Name: "ignition-server"}

	err := r.APIReader.Get(ctx, key, route)
	create := apierrors.IsNotFound(err)
	if err != nil && !create {
		return nil, err
	}

	if create {
		route.Namespace, route.Name = key.Namespace, key.Name
	} else if !kube.MatchesController(route, cp, hypershiftv1.GroupVersion.WithKind("HostedControlPlane")) {
		return nil, fmt.Errorf("route %s/%s belongs to another control plane", route.Namespace, route.Name)
	}

	if route.DeletionTimestamp != nil {
		return nil, fmt.Errorf("ignition Route is terminating")
	}

	before := route.DeepCopy()
	if err := controllerutil.SetControllerReference(cp, route, r.Client.Scheme()); err != nil {
		return nil, err
	}

	if strategy != nil && strategy.Hostname != "" {
		route.Spec.Host = strategy.Hostname
	} else if route.Spec.Host == "" {
		route.Spec.Host = shortenedRouteHostname(route.Name, route.Namespace, r.DefaultIngressDomain)
	}

	// Use the Route API default weight so the stored target compares equal on retry.
	route.Spec.To = routev1.RouteTargetReference{
		Kind: "Service", Name: "ignition-server-proxy", Weight: new(int32(100)),
	}
	route.Spec.TLS = &routev1.TLSConfig{
		Termination:                   routev1.TLSTerminationPassthrough,
		InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
	}
	delete(route.Annotations, hypershiftv1.ExternalDNSHostnameAnnotation)

	if create {
		return route, r.Client.Create(ctx, route)
	}

	if !equality.Semantic.DeepEqual(before.Spec, route.Spec) ||
		!equality.Semantic.DeepEqual(before.Annotations, route.Annotations) {
		return route, r.Client.Patch(ctx, route,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	}

	return route, nil
}

// shortenedRouteHostname ports HyperShift's ShortenRouteHostnameIfNeeded and its empty-suffix name shortening.
// Only overlong names use the ingress domain; ordinary names await native API defaulting.
func shortenedRouteHostname(name, namespace, domain string) string {
	if domain == "" || len(name)+len(namespace)+1 < 63 {
		return ""
	}

	base := name + "-" + namespace
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(base))

	return fmt.Sprintf("%s-%08x.%s", base[:53], hash.Sum32(), domain)
}

const ignitionCASecretName = "ignition-server-ca-cert"

// ensureIgnitionCA retains the original HCP's signing material across reconciliation.
// Only native certificate fields are generated; no server or guest access is provided.
func (r *HostedControlPlaneReconciler) ensureIgnitionCA(
	ctx context.Context,
	cp *hypershiftv1.HostedControlPlane,
) error {
	desired := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: cp.Namespace,
		Name:      ignitionCASecretName,
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

	before := current.DeepCopy()
	current.Type = corev1.SecretTypeTLS

	if err := certs.ReconcileSelfSignedCA(current, "ignition-root-ca", "openshift", func(o *certs.CAOpts) {
		o.CASignerCertMapKey = corev1.TLSCertKey
		o.CASignerKeyMapKey = corev1.TLSPrivateKeyKey
	}); err != nil {
		return err
	}

	if create {
		return r.Client.Create(ctx, current)
	}

	if equality.Semantic.DeepEqual(before, current) {
		return nil
	}

	return r.Client.Patch(ctx, current, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}
