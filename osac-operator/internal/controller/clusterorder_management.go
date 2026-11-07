/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	routev1 "github.com/openshift/api/route/v1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/controller/assistedservice"
)

const (
	httpsScheme                      = "https"
	managementPublicationPending     = "ManagementPublicationPending"
	managementPublicationFailed      = "ManagementPublicationFailed"
	managementPublicationProbeFailed = "ManagementPublicationProbeFailed"
	managementPublicationVIPChanged  = "ManagementPublicationVIPChanged"
	managementEndpointTimeout        = 10 * time.Second

	routeGroup    = "route.openshift.io"
	routeVersion  = "v1"
	routeKind     = "Route"
	routeListKind = "RouteList"

	oauthExternalPublicRouteName  = "oauth"
	oauthExternalPrivateRouteName = "oauth-private"
	oauthInternalRouteName        = "oauth-internal"
	konnectivityServerRouteName   = "konnectivity-server"
	ignitionServerRouteName       = "ignition-server"
	hcpRootCASecretName           = "root-ca"
	hcpKonnectivityServingCAName  = "konnectivity-cluster-serving-signer"
)

// managementRouteIdentities lists the stable HyperShift Route names for the
// management endpoints. OAuth uses one of three names depending on HCP
// visibility; names are ordered from public to private to internal so the first
// matching Route is preferred if HyperShift briefly publishes more than one.
// OIDC remains in the HostedCluster service list for compatibility but is
// deprecated and does not have a HyperShift-managed Route.
var managementRouteIdentities = []struct {
	service hypershiftv1beta1.ServiceType
	names   []string
}{
	{
		service: hypershiftv1beta1.OAuthServer,
		names: []string{
			oauthExternalPublicRouteName,
			oauthExternalPrivateRouteName,
			oauthInternalRouteName,
		},
	},
	{service: hypershiftv1beta1.Konnectivity, names: []string{konnectivityServerRouteName}},
	{service: hypershiftv1beta1.Ignition, names: []string{ignitionServerRouteName}},
}

type managementRouteEndpoint struct {
	service     hypershiftv1beta1.ServiceType
	host        string
	termination routev1.TLSTerminationType
}

type managementEndpointProbe func(
	ctx context.Context,
	serverName string,
	port int,
	connectIP string,
	rootCAs *x509.CertPool,
) error

type managementPublicationCheck struct {
	ready   bool
	reason  string
	message string
	apiVIP  string
}

func (r *ClusterOrderReconciler) reconcileManagementPublication(
	ctx context.Context,
	instance *v1alpha1.ClusterOrder,
	hc *hypershiftv1beta1.HostedCluster,
) (bool, error) {
	check, err := r.checkManagementPublication(ctx, instance, hc)
	if err != nil {
		return false, err
	}
	if !check.ready {
		instance.SetStatusCondition(string(v1alpha1.ClusterOrderConditionManagementPublicationReady),
			metav1.ConditionFalse, check.message, check.reason)
		return false, nil
	}
	if instance.Status.ApiEndpoint != "" && instance.Status.ApiEndpoint != check.apiVIP {
		message := fmt.Sprintf("management API VIP changed from %s to %s; restore the original VIP",
			instance.Status.ApiEndpoint, check.apiVIP)
		instance.SetStatusCondition(string(v1alpha1.ClusterOrderConditionManagementPublicationReady),
			metav1.ConditionFalse, message, managementPublicationVIPChanged)
		return false, nil
	}
	if instance.Status.ApiEndpoint == "" {
		instance.Status.ApiEndpoint = check.apiVIP
	}
	instance.SetStatusCondition(string(v1alpha1.ClusterOrderConditionManagementPublicationReady),
		metav1.ConditionTrue, "Management API publication checks passed", endpointReasonAsExpected)
	return true, nil
}

func (r *ClusterOrderReconciler) checkManagementPublication(
	ctx context.Context,
	instance *v1alpha1.ClusterOrder,
	hc *hypershiftv1beta1.HostedCluster,
) (managementPublicationCheck, error) {
	if hc == nil {
		return managementPending("Waiting for the HostedCluster"), nil
	}
	if hc.Status.ControlPlaneEndpoint.Host == "" || hc.Status.ControlPlaneEndpoint.Port == 0 {
		return managementPending("Waiting for the HostedCluster controlPlaneEndpoint"), nil
	}
	if hc.Spec.DNS.BaseDomain == "" {
		return managementPending("Waiting for the HostedCluster DNS base domain"), nil
	}
	apiHostname := fmt.Sprintf("api.%s.%s", hc.Name, hc.Spec.DNS.BaseDomain)
	if !strings.EqualFold(hc.Status.ControlPlaneEndpoint.Host, apiHostname) {
		return managementFailed(fmt.Sprintf("HostedCluster controlPlaneEndpoint host %q does not match %q",
			hc.Status.ControlPlaneEndpoint.Host, apiHostname)), nil
	}

	hcpNamespace := hostedControlPlaneNamespace(hc)
	hcp := &hypershiftv1beta1.HostedControlPlane{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: hcpNamespace, Name: hc.Name}, hcp); err != nil {
		if errors.IsNotFound(err) {
			return managementPending("Waiting for the HostedControlPlane"), nil
		}
		return managementPublicationCheck{}, fmt.Errorf("get HostedControlPlane for management publication: %w", err)
	}

	apiService, err := r.hostedControlPlaneAPIServerService(ctx, hc)
	if err != nil {
		return managementPublicationCheck{}, err
	}
	if apiService == nil {
		return managementPending("Waiting for the kube-apiserver Service"), nil
	}
	if !serviceOwnedByHostedControlPlane(apiService, hcp) {
		return managementFailed("kube-apiserver Service is not owned by the HostedControlPlane"), nil
	}
	apiVIP, apiPort, check := managementAPIVIP(apiService, hc.Status.ControlPlaneEndpoint.Port)
	if !check.ready {
		return check, nil
	}

	clusterCIDRs, check, err := r.managementClusterCIDRs(ctx, instance, hc)
	if err != nil || !check.ready {
		return check, err
	}
	for _, cidr := range clusterCIDRs {
		if cidr.Contains(apiVIP) {
			return managementFailed(fmt.Sprintf("management API VIP %s is inside cluster CIDR %s",
				apiVIP, cidr)), nil
		}
	}

	apiRoots, check := r.checkHostedClusterKubeconfig(ctx, hc)
	if !check.ready {
		return check, nil
	}

	if err := r.probeManagementEndpoint(ctx, apiHostname, int(apiPort), apiVIP.String(), apiRoots); err != nil {
		return managementProbeFailed(fmt.Sprintf("management API endpoint %s:%d is not reachable with a valid TLS certificate: %v",
			apiHostname, apiPort, err)), nil
	}

	routesReady, err := r.probeManagementRoutes(ctx, hc)
	if err != nil || !routesReady.ready {
		return routesReady, err
	}

	ignitionURL, check, err := r.infraEnvDiscoveryIgnitionURL(ctx, instance)
	if err != nil || !check.ready {
		return check, err
	}
	if ignitionURL != nil {
		port := 443
		if ignitionURL.Port() != "" {
			port, err = strconv.Atoi(ignitionURL.Port())
			if err != nil || port < 1 || port > 65535 {
				return managementFailed("InfraEnv discovery ignition URL has an invalid port"), nil
			}
		}
		if err := r.probeManagementEndpoint(ctx, ignitionURL.Hostname(), port, "", r.ManagementRootCAs); err != nil {
			return managementProbeFailed(fmt.Sprintf("assisted-service endpoint %s:%d is not reachable with a valid TLS certificate: %v",
				ignitionURL.Hostname(), port, err)), nil
		}
	}

	return managementPublicationCheck{ready: true, apiVIP: apiVIP.String()}, nil
}

func (r *ClusterOrderReconciler) probeManagementRoutes(
	ctx context.Context,
	hc *hypershiftv1beta1.HostedCluster,
) (managementPublicationCheck, error) {
	endpoints, check, err := r.managementRouteEndpoints(ctx, hc)
	if err != nil || !check.ready {
		return check, err
	}
	for _, endpoint := range endpoints {
		rootCAs, check, err := r.managementRouteRootCAs(ctx, hc, endpoint)
		if err != nil || !check.ready {
			return check, err
		}
		if err := r.probeManagementEndpoint(ctx, endpoint.host, 443, "", rootCAs); err != nil {
			return managementProbeFailed(fmt.Sprintf("management Route endpoint %s:443 is not reachable with a valid TLS certificate: %v",
				endpoint.host, err)), nil
		}
	}
	return managementPublicationCheck{ready: true}, nil
}

func managementAPIVIP(service *corev1.Service, expectedPort int32) (netip.Addr, int32, managementPublicationCheck) {
	if service == nil {
		return netip.Addr{}, 0, managementPending("Waiting for the kube-apiserver Service")
	}
	if service.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return netip.Addr{}, 0, managementPending("Waiting for kube-apiserver Service type LoadBalancer")
	}
	var vip netip.Addr
	for _, ingress := range service.Status.LoadBalancer.Ingress {
		if ingress.IP == "" {
			continue
		}
		address, err := netip.ParseAddr(ingress.IP)
		if err != nil || !address.Is4() {
			return netip.Addr{}, 0, managementFailed("kube-apiserver Service has an invalid or non-IPv4 load balancer address")
		}
		if vip.IsValid() && vip != address {
			return netip.Addr{}, 0, managementFailed("kube-apiserver Service has more than one management API VIP")
		}
		vip = address
	}
	if !vip.IsValid() {
		return netip.Addr{}, 0, managementPending("Waiting for one management API VIP")
	}

	var port int32
	for _, candidate := range service.Spec.Ports {
		if candidate.Port != expectedPort {
			continue
		}
		if port != 0 {
			return netip.Addr{}, 0, managementFailed("kube-apiserver Service has ambiguous frontend ports")
		}
		port = candidate.Port
	}
	if port == 0 {
		if len(service.Spec.Ports) == 0 {
			return netip.Addr{}, 0, managementPending("Waiting for kube-apiserver frontend port")
		}
		return netip.Addr{}, 0, managementFailed(fmt.Sprintf(
			"kube-apiserver Service frontend ports do not include controlPlaneEndpoint port %d", expectedPort))
	}
	return vip, port, managementPublicationCheck{ready: true}
}

func serviceOwnedByHostedControlPlane(service *corev1.Service, hcp *hypershiftv1beta1.HostedControlPlane) bool {
	if service == nil || hcp == nil || hcp.UID == "" {
		return false
	}
	for _, owner := range service.OwnerReferences {
		if owner.APIVersion == hypershiftv1beta1.GroupVersion.String() &&
			owner.Kind == "HostedControlPlane" && owner.Name == hcp.Name && owner.UID == hcp.UID {
			return true
		}
	}
	return false
}

func (r *ClusterOrderReconciler) managementClusterCIDRs(
	ctx context.Context,
	instance *v1alpha1.ClusterOrder,
	hc *hypershiftv1beta1.HostedCluster,
) ([]netip.Prefix, managementPublicationCheck, error) {
	var cidrs []netip.Prefix
	for _, network := range hc.Spec.Networking.ClusterNetwork {
		prefix, err := netip.ParsePrefix(network.CIDR.String())
		if err != nil {
			return nil, managementFailed(fmt.Sprintf("HostedCluster has an invalid pod CIDR %q", network.CIDR.String())), nil
		}
		cidrs = append(cidrs, prefix.Masked())
	}
	for _, network := range hc.Spec.Networking.ServiceNetwork {
		prefix, err := netip.ParsePrefix(network.CIDR.String())
		if err != nil {
			return nil, managementFailed(fmt.Sprintf("HostedCluster has an invalid service CIDR %q", network.CIDR.String())), nil
		}
		cidrs = append(cidrs, prefix.Masked())
	}
	if len(hc.Spec.Networking.ClusterNetwork) == 0 || len(hc.Spec.Networking.ServiceNetwork) == 0 {
		return nil, managementPending("Waiting for the HostedCluster pod and service CIDRs"), nil
	}

	if instance.Spec.NetworkAttachment == nil {
		return cidrs, managementPublicationCheck{ready: true}, nil
	}
	if instance.Spec.NetworkAttachment.SubnetRef == "" {
		return nil, managementFailed("ClusterOrder NetworkAttachment has no SubnetRef"), nil
	}
	if r.NetworkingNamespace == "" {
		return nil, managementPending("Waiting for the networking namespace to check the Cluster VirtualNetwork CIDR"), nil
	}
	subnet, virtualNetwork, err := r.clusterEndpointSubnet(ctx, instance)
	if err != nil {
		if isClusterEndpointTenantMismatch(err) {
			return nil, managementFailed(err.Error()), nil
		}
		return nil, managementPublicationCheck{}, err
	}
	if subnet == nil || virtualNetwork == nil {
		return nil, managementPending("Waiting for the Cluster Subnet and VirtualNetwork CIDRs"), nil
	}
	prefix, err := netip.ParsePrefix(virtualNetwork.Spec.IPv4CIDR)
	if err != nil {
		return nil, managementFailed(fmt.Sprintf("VirtualNetwork %q has an invalid IPv4 CIDR", virtualNetwork.Name)), nil
	}
	cidrs = append(cidrs, prefix.Masked())
	return cidrs, managementPublicationCheck{ready: true}, nil
}

func (r *ClusterOrderReconciler) checkHostedClusterKubeconfig(
	ctx context.Context,
	hc *hypershiftv1beta1.HostedCluster,
) (*x509.CertPool, managementPublicationCheck) {
	if hc.Status.KubeConfig == nil || hc.Status.KubeConfig.Name == "" {
		return nil, managementPending("Waiting for HostedCluster status.kubeconfig")
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: hc.Namespace, Name: hc.Status.KubeConfig.Name}, secret); err != nil {
		if errors.IsNotFound(err) {
			return nil, managementPending("Waiting for the HostedCluster kubeconfig Secret")
		}
		return nil, managementFailed(fmt.Sprintf("cannot read HostedCluster kubeconfig Secret: %v", err))
	}
	data := secret.Data["kubeconfig"]
	if len(data) == 0 {
		return nil, managementFailed("HostedCluster kubeconfig Secret has no kubeconfig data")
	}
	config, err := clientcmd.Load(data)
	if err != nil {
		return nil, managementFailed(fmt.Sprintf("HostedCluster kubeconfig is invalid: %v", err))
	}
	currentContext := config.Contexts[config.CurrentContext]
	if currentContext == nil || currentContext.Cluster == "" {
		return nil, managementFailed("HostedCluster kubeconfig has no current cluster")
	}
	cluster := config.Clusters[currentContext.Cluster]
	if cluster == nil {
		return nil, managementFailed("HostedCluster kubeconfig current context references a missing cluster")
	}
	serverURL, err := url.Parse(cluster.Server)
	if err != nil || serverURL.Scheme != httpsScheme || serverURL.Hostname() == "" {
		return nil, managementFailed("HostedCluster kubeconfig server is not a valid HTTPS endpoint")
	}
	port := int32(443)
	if serverURL.Port() != "" {
		parsedPort, parseErr := strconv.Atoi(serverURL.Port())
		if parseErr != nil || parsedPort < 1 || parsedPort > 65535 {
			return nil, managementFailed("HostedCluster kubeconfig server has an invalid port")
		}
		port = int32(parsedPort)
	}
	if !strings.EqualFold(serverURL.Hostname(), hc.Status.ControlPlaneEndpoint.Host) ||
		port != hc.Status.ControlPlaneEndpoint.Port {
		return nil, managementFailed("HostedCluster kubeconfig server does not match status.controlPlaneEndpoint")
	}
	if cluster.InsecureSkipTLSVerify {
		return nil, managementFailed("HostedCluster kubeconfig disables TLS verification")
	}
	var rootCAs *x509.CertPool
	if len(cluster.CertificateAuthorityData) > 0 {
		rootCAs = x509.NewCertPool()
		if !rootCAs.AppendCertsFromPEM(cluster.CertificateAuthorityData) {
			return nil, managementFailed("HostedCluster kubeconfig has invalid certificate-authority-data")
		}
	}
	return rootCAs, managementPublicationCheck{ready: true}
}

func (r *ClusterOrderReconciler) managementRouteEndpoints(
	ctx context.Context,
	hc *hypershiftv1beta1.HostedCluster,
) ([]managementRouteEndpoint, managementPublicationCheck, error) {
	requiredServices := map[hypershiftv1beta1.ServiceType]struct{}{
		hypershiftv1beta1.OAuthServer:  {},
		hypershiftv1beta1.Konnectivity: {},
		hypershiftv1beta1.Ignition:     {},
	}
	configuredServices := make(map[hypershiftv1beta1.ServiceType]struct{}, len(requiredServices))
	configuredHosts := make(map[hypershiftv1beta1.ServiceType]string, len(requiredServices))
	for _, service := range hc.Spec.Services {
		if _, required := requiredServices[service.Service]; !required {
			continue
		}
		if _, duplicate := configuredServices[service.Service]; duplicate {
			return nil, managementFailed(fmt.Sprintf("HostedCluster has duplicate service publishing configuration for %s", service.Service)), nil
		}
		if service.ServicePublishingStrategy.Type != hypershiftv1beta1.Route {
			return nil, managementFailed(fmt.Sprintf("HostedCluster service %s is not published through a Route", service.Service)), nil
		}
		configuredServices[service.Service] = struct{}{}
		if service.ServicePublishingStrategy.Route != nil && service.ServicePublishingStrategy.Route.Hostname != "" {
			configuredHosts[service.Service] = service.ServicePublishingStrategy.Route.Hostname
		}
	}
	if len(configuredServices) != len(requiredServices) {
		return nil, managementPending("Waiting for the HostedCluster OAuth, Konnectivity, and Ignition Route configuration"), nil
	}

	routes := &unstructured.UnstructuredList{}
	routes.SetGroupVersionKind(schema.GroupVersionKind{Group: routeGroup, Version: routeVersion, Kind: routeListKind})
	if err := r.List(ctx, routes, client.InNamespace(hostedControlPlaneNamespace(hc))); err != nil {
		return nil, managementPublicationCheck{}, fmt.Errorf("list HostedControlPlane Routes: %w", err)
	}
	routesByName := make(map[string]*unstructured.Unstructured, len(routes.Items))
	for i := range routes.Items {
		route := &routes.Items[i]
		routesByName[route.GetName()] = route
	}

	endpoints := make([]managementRouteEndpoint, 0, len(managementRouteIdentities))
	for _, identity := range managementRouteIdentities {
		var route *unstructured.Unstructured
		for _, routeName := range identity.names {
			if candidate := routesByName[routeName]; candidate != nil {
				route = candidate
				break
			}
		}
		if route == nil {
			return nil, managementPending(fmt.Sprintf(
				"Waiting for HostedControlPlane Route for service %s", identity.service)), nil
		}

		host, found, err := unstructured.NestedString(route.Object, "spec", "host")
		if err != nil {
			return nil, managementFailed(fmt.Sprintf("Route %s has invalid spec.host: %v", route.GetName(), err)), nil
		}
		if !found || host == "" {
			return nil, managementPending(fmt.Sprintf("Waiting for Route %s spec.host", route.GetName())), nil
		}
		if expectedHost := configuredHosts[identity.service]; expectedHost != "" &&
			!strings.EqualFold(expectedHost, host) {
			return nil, managementPending(fmt.Sprintf(
				"Waiting for Route %s to use configured hostname %s", route.GetName(), expectedHost)), nil
		}
		if _, found, err := unstructured.NestedMap(route.Object, "spec", "tls"); err != nil || !found {
			return nil, managementFailed(fmt.Sprintf("Route %s does not publish TLS", route.GetName())), nil
		}
		termination, found, err := unstructured.NestedString(route.Object, "spec", "tls", "termination")
		if err != nil {
			return nil, managementFailed(fmt.Sprintf("Route %s has invalid TLS termination: %v", route.GetName(), err)), nil
		}
		if !found || termination == "" {
			return nil, managementFailed(fmt.Sprintf("Route %s has no TLS termination", route.GetName())), nil
		}
		tlsTermination := routev1.TLSTerminationType(termination)
		switch tlsTermination {
		case routev1.TLSTerminationEdge, routev1.TLSTerminationReencrypt, routev1.TLSTerminationPassthrough:
		default:
			return nil, managementFailed(fmt.Sprintf("Route %s has unsupported TLS termination %q", route.GetName(), termination)), nil
		}
		admitted, exists, err := routeIsAdmitted(route)
		if err != nil {
			return nil, managementFailed(fmt.Sprintf("Route %s has invalid admission status: %v", route.GetName(), err)), nil
		}
		if !exists {
			return nil, managementPending(fmt.Sprintf("Waiting for Route %s admission", route.GetName())), nil
		}
		if !admitted {
			return nil, managementFailed(fmt.Sprintf("Route %s is not admitted", route.GetName())), nil
		}
		endpoints = append(endpoints, managementRouteEndpoint{
			service:     identity.service,
			host:        host,
			termination: tlsTermination,
		})
	}
	return endpoints, managementPublicationCheck{ready: true}, nil
}

func (r *ClusterOrderReconciler) managementRouteRootCAs(
	ctx context.Context,
	hc *hypershiftv1beta1.HostedCluster,
	endpoint managementRouteEndpoint,
) (*x509.CertPool, managementPublicationCheck, error) {
	switch endpoint.termination {
	case routev1.TLSTerminationEdge, routev1.TLSTerminationReencrypt:
		return r.ManagementRootCAs, managementPublicationCheck{ready: true}, nil
	case routev1.TLSTerminationPassthrough:
		secretName, ok := managementRouteServingCASecretName(endpoint.service)
		if !ok {
			return nil, managementFailed(fmt.Sprintf(
				"no serving CA is defined for passthrough Route service %s", endpoint.service)), nil
		}
		key := client.ObjectKey{Namespace: hostedControlPlaneNamespace(hc), Name: secretName}
		secret := &corev1.Secret{}
		if err := r.Get(ctx, key, secret); err != nil {
			if errors.IsNotFound(err) {
				return nil, managementPending(fmt.Sprintf(
					"Waiting for HostedControlPlane serving CA Secret %s", key)), nil
			}
			return nil, managementPublicationCheck{}, fmt.Errorf("get HostedControlPlane serving CA Secret %s: %w", key, err)
		}
		caBundle := secret.Data["ca.crt"]
		if len(caBundle) == 0 {
			return nil, managementFailed(fmt.Sprintf("HostedControlPlane serving CA Secret %s has no ca.crt", key)), nil
		}
		rootCAs := x509.NewCertPool()
		if !rootCAs.AppendCertsFromPEM(caBundle) {
			return nil, managementFailed(fmt.Sprintf("HostedControlPlane serving CA Secret %s has invalid ca.crt", key)), nil
		}
		return rootCAs, managementPublicationCheck{ready: true}, nil
	default:
		return nil, managementFailed(fmt.Sprintf(
			"Route service %s has unsupported TLS termination %q", endpoint.service, endpoint.termination)), nil
	}
}

func managementRouteServingCASecretName(service hypershiftv1beta1.ServiceType) (string, bool) {
	switch service {
	case hypershiftv1beta1.OAuthServer, hypershiftv1beta1.Ignition:
		// HyperShift signs these service certificates with the HCP root-ca Secret.
		return hcpRootCASecretName, true
	case hypershiftv1beta1.Konnectivity:
		// The Route targets the cluster-facing port 8091 certificate.
		return hcpKonnectivityServingCAName, true
	default:
		return "", false
	}
}

func routeIsAdmitted(route *unstructured.Unstructured) (bool, bool, error) {
	ingress, found, err := unstructured.NestedSlice(route.Object, "status", "ingress")
	if err != nil || !found || len(ingress) == 0 {
		return false, false, err
	}
	for _, entry := range ingress {
		status, ok := entry.(map[string]interface{})
		if !ok {
			return false, false, fmt.Errorf("ingress entry is not an object")
		}
		conditions, _, err := unstructured.NestedSlice(status, "conditions")
		if err != nil {
			return false, false, err
		}
		for _, condition := range conditions {
			fields, ok := condition.(map[string]interface{})
			if !ok {
				return false, false, fmt.Errorf("condition is not an object")
			}
			if fields["type"] == "Admitted" {
				admitted, _ := fields["status"].(string)
				if admitted == "True" {
					return true, true, nil
				}
			}
		}
	}
	return false, true, nil
}

func (r *ClusterOrderReconciler) infraEnvDiscoveryIgnitionURL(
	ctx context.Context,
	instance *v1alpha1.ClusterOrder,
) (*url.URL, managementPublicationCheck, error) {
	if !clusterOrderRequestsBareMetalWorkers(instance) {
		return nil, managementPublicationCheck{ready: true}, nil
	}
	infraEnv := assistedservice.NewInfraEnvObject()
	key := client.ObjectKey{Namespace: instance.Namespace, Name: assistedservice.InfraEnvName(instance.Name)}
	if err := r.Get(ctx, key, infraEnv); err != nil {
		if errors.IsNotFound(err) {
			return nil, managementPending("Waiting for the Cluster InfraEnv"), nil
		}
		return nil, managementPublicationCheck{}, fmt.Errorf("get Cluster InfraEnv: %w", err)
	}
	owner := metav1.GetControllerOf(infraEnv)
	if owner == nil || owner.Kind != "ClusterOrder" || owner.Name != instance.Name ||
		(instance.UID != "" && owner.UID != instance.UID) {
		return nil, managementFailed("InfraEnv is not owned by this ClusterOrder"), nil
	}
	ignitionURL, found, err := assistedservice.DiscoveryIgnitionURL(infraEnv)
	if err != nil {
		return nil, managementFailed(fmt.Sprintf("InfraEnv discovery ignition URL is invalid: %v", err)), nil
	}
	if !found || ignitionURL == "" {
		return nil, managementPending("Waiting for the InfraEnv discovery ignition URL"), nil
	}
	parsed, err := url.Parse(ignitionURL)
	if err != nil || parsed.Scheme != httpsScheme || parsed.Hostname() == "" {
		return nil, managementFailed("InfraEnv discovery ignition URL is not a valid HTTPS URL"), nil
	}
	return parsed, managementPublicationCheck{ready: true}, nil
}

func clusterOrderRequestsBareMetalWorkers(instance *v1alpha1.ClusterOrder) bool {
	return instance != nil && instance.HasBareMetalNodeSet()
}

func (r *ClusterOrderReconciler) probeManagementEndpoint(
	ctx context.Context,
	serverName string,
	port int,
	connectIP string,
	rootCAs *x509.CertPool,
) error {
	probe := r.managementProbe
	if probe == nil {
		probe = probeManagementEndpoint
	}
	return probe(ctx, serverName, port, connectIP, rootCAs)
}

func probeManagementEndpoint(
	ctx context.Context,
	serverName string,
	port int,
	connectIP string,
	rootCAs *x509.CertPool,
) error {
	if serverName == "" || port < 1 || port > 65535 {
		return fmt.Errorf("invalid endpoint")
	}
	ctx, cancel := context.WithTimeout(ctx, managementEndpointTimeout)
	defer cancel()

	var addresses []net.IPAddr
	if connectIP != "" {
		address, err := netip.ParseAddr(connectIP)
		if err != nil {
			return fmt.Errorf("invalid connect address %q: %w", connectIP, err)
		}
		addresses = []net.IPAddr{{IP: net.ParseIP(address.String())}}
	} else {
		resolved, err := net.DefaultResolver.LookupIPAddr(ctx, serverName)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", serverName, err)
		}
		addresses = resolved
	}
	if len(addresses) == 0 {
		return fmt.Errorf("no addresses resolved")
	}

	var lastErr error
	for _, address := range addresses {
		connection, err := (&net.Dialer{}).DialContext(ctx, "tcp",
			net.JoinHostPort(address.IP.String(), strconv.Itoa(port)))
		if err != nil {
			lastErr = err
			continue
		}
		tlsConnection := tls.Client(connection, &tls.Config{
			ServerName: serverName,
			RootCAs:    rootCAs,
			MinVersion: tls.VersionTLS12,
		})
		err = handshakeAndClose(ctx, tlsConnection)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return lastErr
}

func handshakeAndClose(ctx context.Context, connection *tls.Conn) error {
	err := connection.HandshakeContext(ctx)
	_ = connection.Close()
	return err
}

func managementPending(message string) managementPublicationCheck {
	return managementPublicationCheck{reason: managementPublicationPending, message: message}
}

func managementFailed(message string) managementPublicationCheck {
	return managementPublicationCheck{reason: managementPublicationFailed, message: message}
}

func managementProbeFailed(message string) managementPublicationCheck {
	return managementPublicationCheck{reason: managementPublicationProbeFailed, message: message}
}
