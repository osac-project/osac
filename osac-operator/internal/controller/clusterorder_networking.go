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
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	clusterEndpointsJobTemplate  = "cluster-endpoints"
	serviceNetworkKubeconfig     = "service-network-admin-kubeconfig"
	fieldClusterOrder            = "cluster_order"
	fieldHostedClusterName       = "hosted_cluster_name"
	fieldHostedClusterBaseDomain = "hosted_cluster_base_domain"
	guestClusterAPITimeout       = 15 * time.Second

	endpointReasonProvisioningDisabled = "ProvisioningDisabled"
	endpointReasonAsExpected           = "AsExpected"
	endpointReasonProgressing          = "EndpointsProgressing"
	endpointReasonExternalAccessFailed = "ExternalAccessFailed"
	endpointReasonIngressReservation   = "IngressReservationFailed"
	endpointReasonGuestIngressFailed   = "GuestIngressFailed"
	endpointReasonGuestIngressProgress = "GuestIngressProgressing"
	endpointReasonDNSPublishFailed     = "DNSPublishFailed"
	endpointReasonEndpointsPublished   = "EndpointsPublished"
	clusterEndpointDNSFinalizer        = "osac.openshift.io/cluster-endpoint-dns-finalizer"
	endpointDNSPrewriteRejection       = "Cluster endpoint DNS publication rejected before DNS writes"
	endpointProbeGracePollIntervals    = 3
	endpointMessageWaitingForAPIIP     = "Waiting for the API ExternalIP attachment"
	endpointMessageWaitingForIngressIP = "Waiting for the ingress ExternalIP attachment"
	endpointMessageWaitingForGuestAPI  = "Waiting for the guest API to become usable"
)

var errClusterEndpointTenantMismatch = errors.New("cluster endpoint tenant mismatch")

// clusterOrderUsesEndpointLifecycle follows the resolved template capability
// copied to the ClusterOrder by Fulfillment. Templates without it keep their
// existing endpoint flows.
func clusterOrderUsesEndpointLifecycle(instance *v1alpha1.ClusterOrder) bool {
	return instance != nil && instance.Spec.SupportsAutomaticCaaSEndpoints
}

func clusterOrderUsesAutomaticEndpointLifecycle(instance *v1alpha1.ClusterOrder, requested bool) bool {
	return clusterOrderUsesEndpointLifecycle(instance) && requested
}

func clusterEndpointDNSPublicationEstablished(instance *v1alpha1.ClusterOrder) bool {
	if instance == nil || instance.Status.APIURL == "" || instance.Status.ConsoleURL == "" {
		return false
	}
	for _, spec := range clusterEndpointDNSJobSpecs() {
		job := networktask.FindJob(instance.Status.Jobs, spec.publishTask, v1alpha1.NetworkJobManagerNone)
		if job == nil || job.State != v1alpha1.NetworkJobStateSucceeded {
			return false
		}
	}
	return true
}

func clusterEndpointDNSPublicationMayHaveWritten(
	instance *v1alpha1.ClusterOrder,
	spec clusterEndpointDNSJobSpec,
) bool {
	if instance == nil {
		return false
	}
	job := networktask.FindJob(instance.Status.Jobs, spec.publishTask, v1alpha1.NetworkJobManagerNone)
	if job == nil {
		switch spec.endpoint {
		case autoCreatedEndpointAPI:
			return instance.Status.APIURL != "" || instance.Status.APIDNSRecordsPublishedTime != nil
		case autoCreatedEndpointIngress:
			return instance.Status.ConsoleURL != ""
		default:
			return false
		}
	}
	if job.State != v1alpha1.NetworkJobStateFailed || job.Attempt != 1 ||
		!strings.HasPrefix(job.Message, endpointDNSPrewriteRejection) {
		return true
	}
	// The marker is returned only by the supported-domain guard, before any
	// provider call. An attempt after the first may follow a partial write from
	// an earlier attempt, so it must still be cleaned up.
	switch spec.endpoint {
	case autoCreatedEndpointAPI:
		return instance.Status.APIURL != "" || instance.Status.APIDNSRecordsPublishedTime != nil
	case autoCreatedEndpointIngress:
		return instance.Status.ConsoleURL != ""
	default:
		return true
	}
}

func clusterOrderHasTransientEndpointProbeFailure(
	instance *v1alpha1.ClusterOrder,
	priorPhase v1alpha1.ClusterOrderPhaseType,
	now time.Time,
	grace time.Duration,
) bool {
	if instance == nil || priorPhase != v1alpha1.ClusterOrderPhaseReady ||
		!clusterEndpointDNSPublicationEstablished(instance) || grace <= 0 {
		return false
	}
	endpoints := apimeta.FindStatusCondition(instance.Status.Conditions,
		string(v1alpha1.ClusterOrderConditionEndpointsReady))
	if endpoints == nil || endpoints.Status != metav1.ConditionFalse {
		return false
	}
	management := apimeta.FindStatusCondition(instance.Status.Conditions,
		string(v1alpha1.ClusterOrderConditionManagementPublicationReady))
	if management != nil && management.Status == metav1.ConditionFalse {
		switch management.Reason {
		case managementPublicationProbeFailed:
			return conditionWithinEndpointProbeGrace(management, now, grace)
		case managementPublicationVIPChanged:
			return false
		}
	}
	return endpoints.Reason == endpointReasonGuestIngressProgress &&
		conditionWithinEndpointProbeGrace(endpoints, now, grace)
}

func conditionWithinEndpointProbeGrace(condition *metav1.Condition, now time.Time, grace time.Duration) bool {
	if condition == nil || condition.LastTransitionTime.IsZero() || grace <= 0 {
		return false
	}
	elapsed := now.Sub(condition.LastTransitionTime.Time)
	return elapsed >= 0 && elapsed <= grace
}

type fulfillmentClusterLookup struct {
	loaded  bool
	cluster *privatev1.Cluster
}

// lookupFulfillmentCluster returns the Cluster for the order and records the
// result so endpoint reconciliation and attachment reconciliation share one
// Fulfillment read during a reconcile.
func (r *ClusterOrderReconciler) lookupFulfillmentCluster(
	ctx context.Context,
	instance *v1alpha1.ClusterOrder,
	lookup *fulfillmentClusterLookup,
) (*privatev1.Cluster, error) {
	if lookup != nil && lookup.loaded {
		return lookup.cluster, nil
	}
	cluster, err := r.getFulfillmentCluster(ctx, instance)
	if err != nil {
		return nil, err
	}
	if lookup != nil {
		lookup.cluster = cluster
		lookup.loaded = true
	}
	return cluster, nil
}

func (r *ClusterOrderReconciler) getFulfillmentCluster(
	ctx context.Context, instance *v1alpha1.ClusterOrder,
) (*privatev1.Cluster, error) {
	if r.ClustersClient == nil || instance.Labels[osacClusterOrderIDLabel] == "" {
		return nil, nil
	}
	response, err := r.ClustersClient.Get(ctx, privatev1.ClustersGetRequest_builder{
		Id: instance.Labels[osacClusterOrderIDLabel],
	}.Build())
	if status.Code(err) == codes.NotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return response.GetObject(), nil
}

// reconcileClusterEndpointLifecycle owns endpoint discovery, ingress
// reservation and the manager-free endpoint/DNS jobs for standard CaaS.
func (r *ClusterOrderReconciler) reconcileClusterEndpointLifecycle(
	ctx context.Context,
	instance *v1alpha1.ClusterOrder,
	hc *hypershiftv1beta1.HostedCluster,
	autoExternalAccess bool,
) error {
	if hc == nil {
		instance.SetStatusCondition(string(v1alpha1.ClusterOrderConditionManagementPublicationReady),
			metav1.ConditionFalse, "Waiting for the HostedCluster", managementPublicationPending)
		if r.Engine == nil || !r.Engine.ProvisioningEnabled() {
			setClusterEndpointCondition(instance, metav1.ConditionTrue,
				endpointReasonProvisioningDisabled, "Provider networking is disabled")
		} else {
			setClusterEndpointCondition(instance, metav1.ConditionFalse,
				endpointReasonProgressing, "Waiting for the HostedCluster")
		}
		return nil
	}

	managementAPIReady, err := r.reconcileManagementPublication(ctx, instance, hc)
	if err != nil {
		return err
	}
	if r.Engine == nil || !r.Engine.ProvisioningEnabled() {
		setClusterEndpointCondition(instance, metav1.ConditionTrue,
			endpointReasonProvisioningDisabled, "Provider networking is disabled")
		return nil
	}
	if !managementAPIReady {
		condition := apimeta.FindStatusCondition(instance.Status.Conditions,
			string(v1alpha1.ClusterOrderConditionManagementPublicationReady))
		message := "Waiting for the management API publication checks"
		if condition != nil && condition.Message != "" {
			message = condition.Message
		}
		setClusterEndpointCondition(instance, metav1.ConditionFalse, endpointReasonProgressing, message)
		return nil
	}
	if !autoExternalAccess {
		setClusterEndpointCondition(instance, metav1.ConditionFalse, endpointReasonExternalAccessFailed,
			"Automatic API and ingress external access is required for CaaS with provider networking enabled")
		return nil
	}

	apiDNSReady, apiReason, apiMessage, err := r.publishClusterAPIEndpointDNS(ctx, instance, hc)
	if err != nil {
		return err
	}

	guestClient, guestReadyNode, guestAPIUsable, err := r.guestClusterClient(ctx, hc)
	if err != nil {
		return err
	}
	ingressConfigured := false
	ingressReason := endpointReasonGuestIngressProgress
	ingressMessage := endpointMessageWaitingForGuestAPI
	if guestAPIUsable {
		ingressConfigured, err = r.reconcileClusterGuestIngress(ctx, instance, hc, guestClient, guestReadyNode)
		if err != nil {
			return err
		}
		if !ingressConfigured {
			if condition := apimeta.FindStatusCondition(instance.Status.Conditions,
				string(v1alpha1.ClusterOrderConditionEndpointsReady)); condition != nil {
				ingressReason = condition.Reason
				ingressMessage = condition.Message
			}
		}
	} else {
		setClusterEndpointCondition(instance, metav1.ConditionFalse, ingressReason, ingressMessage)
	}

	ingressDNSReady := false
	if ingressConfigured {
		ingressDNSReady, ingressReason, ingressMessage, err = r.publishClusterIngressEndpointDNS(ctx, instance, hc)
		if err != nil {
			return err
		}
	}
	if apiDNSReady && ingressDNSReady {
		setClusterEndpointCondition(instance, metav1.ConditionTrue,
			endpointReasonEndpointsPublished, "API and ingress endpoints are attached and published")
		return nil
	}
	if !apiDNSReady {
		setClusterEndpointCondition(instance, metav1.ConditionFalse, apiReason, apiMessage)
	} else {
		setClusterEndpointCondition(instance, metav1.ConditionFalse, ingressReason, ingressMessage)
	}
	return nil
}

func (r *ClusterOrderReconciler) reconcileClusterGuestIngress(
	ctx context.Context,
	instance *v1alpha1.ClusterOrder,
	hc *hypershiftv1beta1.HostedCluster,
	guestClient kubernetes.Interface,
	guestReadyNode bool,
) (bool, error) {
	subnet, virtualNetwork, err := r.clusterEndpointSubnet(ctx, instance)
	if err != nil {
		if isClusterEndpointTenantMismatch(err) {
			setClusterEndpointCondition(instance, metav1.ConditionFalse,
				endpointReasonExternalAccessFailed, err.Error())
			return false, nil
		}
		return false, err
	}
	input, err := networktask.LoadInput(instance)
	if err != nil {
		return false, err
	}
	managerUnavailable := subnet == nil || subnet.Status.NetworkManagers == nil || subnet.Status.NetworkManagers.Fabric == nil
	if subnet == nil || virtualNetwork == nil || !isReady(subnet) || subnet.Status.IPAM == nil ||
		(managerUnavailable && input == nil) {
		setClusterEndpointCondition(instance, metav1.ConditionFalse,
			endpointReasonProgressing, "Waiting for the cluster Subnet Fabric Manager and IPAM")
		return false, nil
	}

	reserveOutcome, err := r.runReserveClusterIngress(ctx, instance, subnet, virtualNetwork)
	if err != nil {
		return false, err
	}
	switch {
	case reserveOutcome.Failed():
		setClusterEndpointCondition(instance, metav1.ConditionFalse,
			endpointReasonIngressReservation, reserveOutcome.Entry.Message)
		return false, nil
	case !reserveOutcome.Succeeded() || instance.Status.IngressAllocatedAddress == "":
		setClusterEndpointCondition(instance, metav1.ConditionFalse,
			endpointReasonProgressing, endpointJobMessage(reserveOutcome, "Reserving guest ingress address"))
		return false, nil
	}

	if !guestReadyNode {
		setClusterEndpointCondition(instance, metav1.ConditionFalse,
			endpointReasonGuestIngressProgress, "Waiting for a Ready guest node before configuring ingress")
		return false, nil
	}

	configureOutcome, err := r.runClusterEndpointJob(ctx, instance, "configure_guest_ingress", map[string]any{
		fieldClusterOrder:                clusterEndpointOrderVars(instance),
		"hosted_control_plane_namespace": hostedControlPlaneNamespace(hc),
		"ingress_allocated_address":      instance.Status.IngressAllocatedAddress,
	}, false)
	if err != nil {
		return false, err
	}
	if configureOutcome.Failed() {
		setClusterEndpointCondition(instance, metav1.ConditionFalse,
			endpointReasonGuestIngressFailed, configureOutcome.Entry.Message)
		return false, nil
	}
	if !configureOutcome.Succeeded() {
		setClusterEndpointCondition(instance, metav1.ConditionFalse,
			endpointReasonGuestIngressProgress, endpointJobMessage(configureOutcome, "Configuring guest ingress"))
		return false, nil
	}

	ingressAddress, err := guestIngressServiceAddress(ctx, guestClient)
	if err != nil {
		return false, err
	}
	if ingressAddress == "" || ingressAddress != instance.Status.IngressAllocatedAddress {
		instance.Status.IngressEndpoint = ""
		setClusterEndpointCondition(instance, metav1.ConditionFalse,
			endpointReasonGuestIngressProgress, "Waiting for the ingress Service to use its reserved address")
		return false, nil
	}
	instance.Status.IngressEndpoint = ingressAddress
	return true, nil
}

func (r *ClusterOrderReconciler) publishClusterAPIEndpointDNS(
	ctx context.Context,
	instance *v1alpha1.ClusterOrder,
	hc *hypershiftv1beta1.HostedCluster,
) (bool, string, string, error) {
	apiReady, apiIP, apiFailure, err := r.externalEndpointReady(ctx, instance, autoCreatedEndpointAPI)
	if err != nil {
		return false, endpointReasonProgressing, endpointMessageWaitingForAPIIP, err
	}
	if apiFailure != "" {
		return false, endpointReasonExternalAccessFailed, apiFailure, nil
	}
	if !apiReady {
		return false, endpointReasonProgressing, endpointMessageWaitingForAPIIP, nil
	}
	held, err := r.ensureAutomaticEndpointDNSHold(ctx, instance, autoCreatedEndpointAPI)
	if err != nil {
		return false, endpointReasonProgressing, "Holding the API ExternalIP for DNS cleanup", err
	}
	if !held {
		return false, endpointReasonProgressing, endpointMessageWaitingForAPIIP, nil
	}
	hostedClusterName, nameError := endpointDNSHostedClusterNameForPublication(
		instance, hc, autoCreatedEndpointAPI)
	if nameError != "" {
		return false, endpointReasonDNSPublishFailed, nameError, nil
	}
	r.markEndpointDNSRemovalForNextPublication(instance, autoCreatedEndpointAPI)
	instance.SetClusterReferenceHostedClusterName(hc.Name)
	apiDNS, err := r.runClusterEndpointJob(ctx, instance, "publish_public_dns_api", map[string]any{
		fieldClusterOrder:            clusterEndpointOrderVars(instance),
		fieldHostedClusterName:       hostedClusterName,
		"external_ip":                map[string]any{fieldAddress: apiIP},
		fieldHostedClusterBaseDomain: hc.Spec.DNS.BaseDomain,
	}, false)
	if err != nil {
		return false, endpointReasonProgressing, "Publishing API DNS records", err
	}
	if apiDNS.Failed() {
		return false, endpointReasonDNSPublishFailed, apiDNS.Entry.Message, nil
	}
	if !apiDNS.Succeeded() {
		return false, endpointReasonProgressing, endpointJobMessage(apiDNS, "Publishing API DNS records"), nil
	}
	if instance.Status.APIDNSRecordsPublishedTime == nil {
		now := metav1.NewTime(time.Now())
		instance.Status.APIDNSRecordsPublishedTime = &now
	}
	if hc.Spec.DNS.BaseDomain != "" {
		instance.Status.APIURL = fmt.Sprintf("https://api.%s.%s:%d",
			hostedClusterName, hc.Spec.DNS.BaseDomain, hc.Status.ControlPlaneEndpoint.Port)
	}

	return true, endpointReasonAsExpected, "API DNS records are published", nil
}

func (r *ClusterOrderReconciler) publishClusterIngressEndpointDNS(
	ctx context.Context,
	instance *v1alpha1.ClusterOrder,
	hc *hypershiftv1beta1.HostedCluster,
) (bool, string, string, error) {
	ingressReady, ingressIP, ingressFailure, err := r.externalEndpointReady(ctx, instance, autoCreatedEndpointIngress)
	if err != nil {
		return false, endpointReasonProgressing, endpointMessageWaitingForIngressIP, err
	}
	if ingressFailure != "" {
		return false, endpointReasonExternalAccessFailed, ingressFailure, nil
	}
	if !ingressReady {
		return false, endpointReasonProgressing, endpointMessageWaitingForIngressIP, nil
	}
	held, err := r.ensureAutomaticEndpointDNSHold(ctx, instance, autoCreatedEndpointIngress)
	if err != nil {
		return false, endpointReasonProgressing, "Holding the ingress ExternalIP for DNS cleanup", err
	}
	if !held {
		return false, endpointReasonProgressing, endpointMessageWaitingForIngressIP, nil
	}
	hostedClusterName, nameError := endpointDNSHostedClusterNameForPublication(
		instance, hc, autoCreatedEndpointIngress)
	if nameError != "" {
		return false, endpointReasonDNSPublishFailed, nameError, nil
	}
	r.markEndpointDNSRemovalForNextPublication(instance, autoCreatedEndpointIngress)
	instance.SetClusterReferenceHostedClusterName(hc.Name)
	ingressDNS, err := r.runClusterEndpointJob(ctx, instance, "publish_public_dns_ingress", map[string]any{
		fieldClusterOrder:            clusterEndpointOrderVars(instance),
		fieldHostedClusterName:       hostedClusterName,
		"external_ip":                map[string]any{fieldAddress: ingressIP},
		fieldHostedClusterBaseDomain: hc.Spec.DNS.BaseDomain,
	}, false)
	if err != nil {
		return false, endpointReasonProgressing, "Publishing ingress DNS record", err
	}
	if ingressDNS.Failed() {
		return false, endpointReasonDNSPublishFailed, ingressDNS.Entry.Message, nil
	}
	if !ingressDNS.Succeeded() {
		return false, endpointReasonProgressing, endpointJobMessage(ingressDNS, "Publishing ingress DNS record"), nil
	}
	if hc.Spec.DNS.BaseDomain != "" {
		instance.Status.ConsoleURL = fmt.Sprintf("https://console-openshift-console.apps.%s.%s",
			hostedClusterName, hc.Spec.DNS.BaseDomain)
	}
	return true, endpointReasonAsExpected, "Ingress DNS record is published", nil
}

func (r *ClusterOrderReconciler) hostedControlPlaneAPIServerService(
	ctx context.Context, hc *hypershiftv1beta1.HostedCluster,
) (*corev1.Service, error) {
	service := &corev1.Service{}
	err := r.Get(ctx, client.ObjectKey{Namespace: hostedControlPlaneNamespace(hc), Name: "kube-apiserver"}, service)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get HostedControlPlane kube-apiserver Service: %w", err)
	}
	return service, nil
}

func hostedControlPlaneNamespace(hc *hypershiftv1beta1.HostedCluster) string {
	return hc.Namespace + "-" + hc.Name
}

func (r *ClusterOrderReconciler) guestClusterClient(
	ctx context.Context, hc *hypershiftv1beta1.HostedCluster,
) (kubernetes.Interface, bool, bool, error) {
	if hc == nil || !hostedClusterControlPlaneIsAvailable(hc) {
		return nil, false, false, nil
	}
	secret := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Namespace: hostedControlPlaneNamespace(hc), Name: serviceNetworkKubeconfig}, secret)
	if apierrors.IsNotFound(err) {
		return nil, false, false, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("get HostedControlPlane service-network kubeconfig: %w", err)
	}
	data := secret.Data["kubeconfig"]
	if len(data) == 0 {
		return nil, false, false, fmt.Errorf("secret %s/%s has no kubeconfig data", secret.Namespace, secret.Name)
	}
	config, err := clientcmd.RESTConfigFromKubeConfig(data)
	if err != nil {
		return nil, false, false, fmt.Errorf("parse HostedControlPlane service-network kubeconfig: %w", err)
	}
	config.Timeout = guestClusterAPITimeout
	guestClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, false, false, fmt.Errorf("create guest API client: %w", err)
	}
	nodes, err := guestClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 100})
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, false, ctx.Err()
		}
		if apierrors.IsServiceUnavailable(err) || apierrors.IsTimeout(err) ||
			apierrors.IsTooManyRequests(err) || isGuestAPITransportError(err) {
			return guestClient, false, false, nil
		}
		return nil, false, false, fmt.Errorf("query guest API through service network: %w", err)
	}
	ready := false
	for _, node := range nodes.Items {
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
				ready = true
				break
			}
		}
		if ready {
			break
		}
	}
	return guestClient, ready, true, nil
}

func isGuestAPITransportError(err error) bool {
	var urlErr *url.Error
	var netErr net.Error
	return errors.As(err, &urlErr) || errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded)
}

func guestIngressServiceAddress(ctx context.Context, guestClient kubernetes.Interface) (string, error) {
	if guestClient == nil {
		return "", nil
	}
	service, err := guestClient.CoreV1().Services("openshift-ingress").Get(ctx, "external-ingress", metav1.GetOptions{})
	if err != nil && ctx.Err() != nil {
		return "", ctx.Err()
	}
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		if apierrors.IsServiceUnavailable(err) || apierrors.IsTimeout(err) ||
			apierrors.IsTooManyRequests(err) || isGuestAPITransportError(err) {
			return "", nil
		}
		return "", fmt.Errorf("get guest external-ingress Service: %w", err)
	}
	for _, ingress := range service.Status.LoadBalancer.Ingress {
		address, parseErr := netip.ParseAddr(ingress.IP)
		if parseErr == nil && address.Is4() {
			return address.String(), nil
		}
	}
	return "", nil
}

func (r *ClusterOrderReconciler) clusterEndpointSubnet(
	ctx context.Context, instance *v1alpha1.ClusterOrder,
) (*v1alpha1.Subnet, *v1alpha1.VirtualNetwork, error) {
	if instance.Spec.NetworkAttachment == nil || instance.Spec.NetworkAttachment.SubnetRef == "" || r.NetworkingNamespace == "" {
		return nil, nil, nil
	}
	subnet := &v1alpha1.Subnet{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.NetworkingNamespace, Name: instance.Spec.NetworkAttachment.SubnetRef}, subnet); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("get cluster endpoint Subnet %q: %w", instance.Spec.NetworkAttachment.SubnetRef, err)
	}
	virtualNetwork, err := findVirtualNetwork(ctx, r.Client, r.NetworkingNamespace, subnet.Spec.VirtualNetwork)
	if err != nil {
		return nil, nil, err
	}
	if virtualNetwork != nil {
		tenant := instance.GetAnnotations()[networktask.TenantAnnotation]
		if tenant == "" || subnet.GetAnnotations()[networktask.TenantAnnotation] != tenant ||
			virtualNetwork.GetAnnotations()[networktask.TenantAnnotation] != tenant {
			return nil, nil, fmt.Errorf("%w: ClusterOrder %q, Subnet %q and VirtualNetwork %q must belong to the same tenant",
				errClusterEndpointTenantMismatch,
				instance.Name, subnet.Name, virtualNetwork.Name)
		}
	}
	return subnet, virtualNetwork, nil
}

func isClusterEndpointTenantMismatch(err error) bool {
	return errors.Is(err, errClusterEndpointTenantMismatch)
}

func (r *ClusterOrderReconciler) runReserveClusterIngress(
	ctx context.Context,
	instance *v1alpha1.ClusterOrder,
	subnet *v1alpha1.Subnet,
	virtualNetwork *v1alpha1.VirtualNetwork,
) (networktask.Outcome, error) {
	input, err := networktask.LoadInput(instance)
	if err != nil {
		return networktask.Outcome{}, err
	}
	var managerRef *v1alpha1.NetworkManagerRef
	var beforeLaunch func() error
	if input == nil {
		if subnet.Status.NetworkManagers == nil || subnet.Status.NetworkManagers.Fabric == nil {
			return networktask.Outcome{}, fmt.Errorf("subnet %q has no recorded Fabric Manager", subnet.Name)
		}
		managerRef = subnet.Status.NetworkManagers.Fabric.DeepCopy()
		resource, err := networktask.Snapshot(instance, kindClusterOrder, instance.Spec, "networkAttachment")
		if err != nil {
			return networktask.Outcome{}, err
		}
		vnSnapshot, err := networktask.Snapshot(virtualNetwork, kindVirtualNetwork, virtualNetwork.Spec, "ipv4Cidr")
		if err != nil {
			return networktask.Outcome{}, err
		}
		subnetSnapshot, err := networktask.Snapshot(subnet, kindSubnet, subnet.Spec, "ipv4Cidr", "addressPlan")
		if err != nil {
			return networktask.Outcome{}, err
		}
		input = networktask.NewInput(instance, kindClusterOrder, resource)
		input["virtual_network"] = vnSnapshot
		input["subnet"] = subnetSnapshot
		input["ipam"] = networktask.IPAMVars(subnet.Status.IPAM)
		clusterID := instance.Labels[osacClusterOrderIDLabel]
		if clusterID == "" {
			return networktask.Outcome{}, fmt.Errorf("ClusterOrder %q has no Cluster API ID", instance.Name)
		}
		input["cluster_id"] = clusterID
		input["fabric_manager"] = map[string]any{fieldName: managerRef.Name, "ansible_role": managerRef.AnsibleRole}
		beforeLaunch = func() error { return networktask.StoreInput(instance, input) }
	} else {
		// Retry the same manager selected for the first reservation attempt even
		// if NetworkClass resolution later changes the Subnet's live status.
		storedManager, ok := input["fabric_manager"].(map[string]any)
		if !ok {
			return networktask.Outcome{}, fmt.Errorf("ClusterOrder %q ingress reservation input has no recorded Fabric Manager", instance.Name)
		}
		name, nameOK := storedManager[fieldName].(string)
		role, roleOK := storedManager["ansible_role"].(string)
		if !nameOK || name == "" || !roleOK || role == "" {
			return networktask.Outcome{}, fmt.Errorf("ClusterOrder %q ingress reservation input has an invalid Fabric Manager", instance.Name)
		}
		managerRef = &v1alpha1.NetworkManagerRef{Name: name, AnsibleRole: role}
	}
	out, err := r.Engine.Run(ctx, instance, networktask.Request{
		Task:         networktask.ReserveClusterIngressIP,
		Manager:      v1alpha1.NetworkJobManagerFabric,
		ManagerRef:   managerRef,
		Kind:         kindClusterOrder,
		Vars:         clusterIngressManagerVars(input),
		BeforeLaunch: beforeLaunch,
		Accept: func(_ context.Context, result networktask.Result) error {
			address, err := networktask.ParseClusterIngress(result, subnet.Spec.IPv4CIDR,
				subnet.Status.IPAM, instance.Status.IngressAllocatedAddress)
			if err != nil {
				return err
			}
			instance.Status.IngressAllocatedAddress = address
			return nil
		},
	})
	return out, err
}

func (r *ClusterOrderReconciler) runClusterEndpointJob(
	ctx context.Context,
	instance *v1alpha1.ClusterOrder,
	task string,
	vars map[string]any,
	targetDeleting bool,
) (networktask.Outcome, error) {
	resource, err := networktask.Snapshot(instance, kindClusterOrder, instance.Spec, "networkAttachment")
	if err != nil {
		return networktask.Outcome{}, err
	}
	vars["resource"] = resource
	if tenant := instance.GetAnnotations()[networktask.TenantAnnotation]; tenant != "" {
		vars["tenant"] = tenant
	}
	return r.Engine.Run(ctx, instance, networktask.Request{
		Task:           task,
		Manager:        v1alpha1.NetworkJobManagerNone,
		Kind:           kindClusterOrder,
		Template:       r.Engine.TemplateName(clusterEndpointsJobTemplate),
		Vars:           vars,
		TargetDeleting: targetDeleting,
	})
}

func (r *ClusterOrderReconciler) externalEndpointReady(
	ctx context.Context, instance *v1alpha1.ClusterOrder, endpoint string,
) (bool, string, string, error) {
	clusterID := instance.Labels[osacClusterOrderIDLabel]
	var ips v1alpha1.ExternalIPList
	if err := r.List(ctx, &ips, client.InNamespace(r.NetworkingNamespace), client.MatchingLabels{
		autoCreatedLabel:         labelValueTrue,
		autoCreatedForLabel:      clusterID,
		autoCreatedKindLabel:     autoCreatedKindCluster,
		autoCreatedEndpointLabel: endpoint,
	}); err != nil {
		return false, "", "", fmt.Errorf("list automatic ExternalIP for %s endpoint: %w", endpoint, err)
	}
	if len(ips.Items) > 1 {
		return false, "", "", fmt.Errorf("multiple automatic ExternalIPs for ClusterOrder %q endpoint %q", instance.Name, endpoint)
	}
	if len(ips.Items) == 0 {
		return false, "", "", nil
	}
	ip := &ips.Items[0]
	state, message := networkFeedback(ip, ip.Status.Conditions)
	if state == networkFeedbackFailed {
		return false, "", message, nil
	}
	if state != networkFeedbackReady || ip.Status.Address == "" {
		return false, "", "", nil
	}
	var attachments v1alpha1.ExternalIPAttachmentList
	if err := r.List(ctx, &attachments, client.InNamespace(r.NetworkingNamespace), client.MatchingLabels{
		autoCreatedLabel:         labelValueTrue,
		autoCreatedForLabel:      clusterID,
		autoCreatedEndpointLabel: endpoint,
	}); err != nil {
		return false, "", "", fmt.Errorf("list automatic ExternalIPAttachment for %s endpoint: %w", endpoint, err)
	}
	if len(attachments.Items) > 1 {
		return false, "", "", fmt.Errorf("multiple automatic ExternalIPAttachments for ClusterOrder %q endpoint %q", instance.Name, endpoint)
	}
	if len(attachments.Items) == 0 {
		return false, ip.Status.Address, "", nil
	}
	attachment := &attachments.Items[0]
	if attachmentState, attachmentMessage := networkFeedback(attachment, attachment.Status.Conditions); attachmentState == networkFeedbackFailed {
		return false, "", attachmentMessage, nil
	} else if attachmentState != networkFeedbackReady {
		return false, ip.Status.Address, "", nil
	}
	return true, ip.Status.Address, "", nil
}

func (r *ClusterOrderReconciler) ensureAutomaticEndpointDNSHold(
	ctx context.Context, instance *v1alpha1.ClusterOrder, endpoint string,
) (bool, error) {
	attachments, err := r.listAutomaticEndpointAttachments(ctx, instance)
	if err != nil {
		return false, err
	}
	var match *v1alpha1.ExternalIPAttachment
	for i := range attachments {
		if attachments[i].Labels[autoCreatedEndpointLabel] != endpoint {
			continue
		}
		if match != nil {
			return false, fmt.Errorf("multiple automatic ExternalIPAttachments for ClusterOrder %q endpoint %q", instance.Name, endpoint)
		}
		match = &attachments[i]
	}
	if match == nil || !match.DeletionTimestamp.IsZero() {
		return false, nil
	}
	if controllerutil.ContainsFinalizer(match, clusterEndpointDNSFinalizer) {
		return true, nil
	}
	base := match.DeepCopy()
	controllerutil.AddFinalizer(match, clusterEndpointDNSFinalizer)
	if err := r.Patch(ctx, match, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
		return false, fmt.Errorf("add DNS hold to automatic %s ExternalIPAttachment %q: %w", endpoint, match.Name, err)
	}
	return true, nil
}

func (r *ClusterOrderReconciler) listAutomaticEndpointAttachments(
	ctx context.Context, instance *v1alpha1.ClusterOrder,
) ([]v1alpha1.ExternalIPAttachment, error) {
	clusterID := instance.Labels[osacClusterOrderIDLabel]
	var list v1alpha1.ExternalIPAttachmentList
	if err := r.List(ctx, &list, client.InNamespace(r.NetworkingNamespace), client.MatchingLabels{
		autoCreatedLabel:    labelValueTrue,
		autoCreatedForLabel: clusterID,
	}); err != nil {
		return nil, fmt.Errorf("list automatic ExternalIPAttachments for ClusterOrder %q: %w", instance.Name, err)
	}
	attachments := make([]v1alpha1.ExternalIPAttachment, 0, len(list.Items))
	for i := range list.Items {
		endpoint := list.Items[i].Labels[autoCreatedEndpointLabel]
		if endpoint == autoCreatedEndpointAPI || endpoint == autoCreatedEndpointIngress {
			attachments = append(attachments, list.Items[i])
		}
	}
	return attachments, nil
}

type clusterEndpointDNSJobSpec struct {
	endpoint    string
	publishTask string
	removeTask  string
}

func clusterEndpointDNSJobSpecs(endpoints ...string) []clusterEndpointDNSJobSpec {
	all := []clusterEndpointDNSJobSpec{
		{endpoint: autoCreatedEndpointAPI, publishTask: "publish_public_dns_api", removeTask: "remove_public_dns_api"},
		{endpoint: autoCreatedEndpointIngress, publishTask: "publish_public_dns_ingress", removeTask: "remove_public_dns_ingress"},
	}
	if len(endpoints) == 0 {
		return all
	}

	selected := make(map[string]struct{}, len(endpoints))
	for _, endpoint := range endpoints {
		selected[endpoint] = struct{}{}
	}
	filtered := make([]clusterEndpointDNSJobSpec, 0, len(selected))
	for _, spec := range all {
		if _, ok := selected[spec.endpoint]; ok {
			filtered = append(filtered, spec)
		}
	}
	return filtered
}

// markEndpointDNSRemovalForNextPublication makes a completed cleanup eligible
// again only when a new publish attempt is about to start. A successful remove
// must stay terminal if the subsequent attachment hold release fails.
func (r *ClusterOrderReconciler) markEndpointDNSRemovalForNextPublication(
	instance *v1alpha1.ClusterOrder,
	endpoint string,
) {
	if r.Engine == nil {
		return
	}
	for _, spec := range clusterEndpointDNSJobSpecs(endpoint) {
		if r.Engine.LaunchDue(instance, spec.publishTask, v1alpha1.NetworkJobManagerNone) {
			r.Engine.MarkForRerun(instance, spec.removeTask, v1alpha1.NetworkJobManagerNone)
		}
	}
}

func endpointDNSHostedClusterName(instance *v1alpha1.ClusterOrder, endpoint string) string {
	if instance == nil {
		return ""
	}
	switch endpoint {
	case autoCreatedEndpointAPI:
		return instance.Status.APIDNSHostedClusterName
	case autoCreatedEndpointIngress:
		return instance.Status.IngressDNSHostedClusterName
	default:
		return ""
	}
}

func setEndpointDNSHostedClusterName(instance *v1alpha1.ClusterOrder, endpoint, name string) {
	switch endpoint {
	case autoCreatedEndpointAPI:
		instance.Status.APIDNSHostedClusterName = name
	case autoCreatedEndpointIngress:
		instance.Status.IngressDNSHostedClusterName = name
	}
}

// endpointDNSHostedClusterNameForPublication pins each DNS publication to the
// HostedCluster identity used to create its records. If either endpoint may
// still have records under a different HostedCluster name, block publication
// until cleanup removes those records.
func endpointDNSHostedClusterNameForPublication(
	instance *v1alpha1.ClusterOrder,
	hc *hypershiftv1beta1.HostedCluster,
	endpoint string,
) (string, string) {
	if instance == nil || hc == nil || hc.Name == "" {
		return "", "Waiting for the HostedCluster name before publishing DNS"
	}
	targetFound := false
	for _, spec := range clusterEndpointDNSJobSpecs() {
		if spec.endpoint == endpoint {
			targetFound = true
		}
		storedName := endpointDNSHostedClusterName(instance, spec.endpoint)
		if storedName != "" && storedName != hc.Name &&
			clusterEndpointDNSPublicationMayHaveWritten(instance, spec) {
			return "", fmt.Sprintf(
				"HostedCluster name changed from %q to %q while %s DNS records may exist; remove published DNS before changing the HostedCluster name",
				storedName, hc.Name, spec.endpoint)
		}
	}
	if !targetFound {
		return "", fmt.Sprintf("unsupported ClusterOrder DNS endpoint %q", endpoint)
	}
	if endpointDNSHostedClusterName(instance, endpoint) != hc.Name {
		setEndpointDNSHostedClusterName(instance, endpoint, hc.Name)
	}
	return hc.Name, ""
}

// reconcileDeletingAutomaticEndpointDNS holds each deleting automatic endpoint
// attachment until that endpoint's published DNS records have been removed.
func (r *ClusterOrderReconciler) reconcileDeletingAutomaticEndpointDNS(
	ctx context.Context, instance *v1alpha1.ClusterOrder,
) (bool, ctrl.Result, error) {
	if !clusterOrderUsesEndpointLifecycle(instance) {
		return false, ctrl.Result{}, nil
	}
	attachments, err := r.listAutomaticEndpointAttachments(ctx, instance)
	if err != nil {
		return true, ctrl.Result{}, err
	}
	deleting := false
	deletingEndpoints := make(map[string]struct{})
	for i := range attachments {
		attachment := &attachments[i]
		if attachment.DeletionTimestamp.IsZero() {
			continue
		}
		deleting = true
		if controllerutil.ContainsFinalizer(attachment, clusterEndpointDNSFinalizer) {
			deletingEndpoints[attachment.Labels[autoCreatedEndpointLabel]] = struct{}{}
		}
	}
	if !deleting {
		return false, ctrl.Result{}, nil
	}
	endpoints := make([]string, 0, len(deletingEndpoints))
	for _, spec := range clusterEndpointDNSJobSpecs() {
		if _, ok := deletingEndpoints[spec.endpoint]; ok {
			endpoints = append(endpoints, spec.endpoint)
		}
	}
	if len(endpoints) == 0 {
		// DNS cleanup already completed. Wait for the provider finalizer to
		// remove the old attachment before reconciling a replacement.
		return true, ctrl.Result{RequeueAfter: r.StatusPollInterval}, nil
	}

	cancelled, retryAfter, err := r.cancelClusterEndpointJobsForDelete(ctx, instance, endpoints...)
	if err != nil {
		return true, ctrl.Result{}, err
	}
	if !cancelled {
		return true, ctrl.Result{RequeueAfter: requeueAfterOutcome(networktask.Outcome{RequeueAfter: retryAfter}, r.StatusPollInterval)}, nil
	}
	apiDNSHostedClusterName := instance.Status.APIDNSHostedClusterName
	ingressDNSHostedClusterName := instance.Status.IngressDNSHostedClusterName
	dnsRemoved, result, err := r.removePublishedClusterDNSForEndpoints(ctx, instance, endpoints...)
	if err != nil || !dnsRemoved {
		return true, result, err
	}
	statusBase := instance.DeepCopy()
	statusBase.Status.APIDNSHostedClusterName = apiDNSHostedClusterName
	statusBase.Status.IngressDNSHostedClusterName = ingressDNSHostedClusterName
	for _, endpoint := range endpoints {
		switch endpoint {
		case autoCreatedEndpointAPI:
			instance.Status.APIURL = ""
			instance.Status.APIDNSRecordsPublishedTime = nil
		case autoCreatedEndpointIngress:
			instance.Status.ConsoleURL = ""
		}
	}
	if instance.Status.Phase == v1alpha1.ClusterOrderPhaseReady {
		setClusterOrderPhase(&instance.Status, v1alpha1.ClusterOrderPhaseProgressing)
		instance.SetStatusCondition(v1alpha1.ConditionProgressing, metav1.ConditionTrue,
			humanizeConditionName(endpointReasonProgressing), endpointReasonProgressing)
	}
	setClusterEndpointCondition(instance, metav1.ConditionFalse, endpointReasonProgressing,
		"Replacing an automatic endpoint attachment")
	if err := r.markAutomaticEndpointDNSJobsForRerun(ctx, instance, statusBase, endpoints...); err != nil {
		return true, ctrl.Result{}, err
	}
	if err := r.releaseAutomaticEndpointDNSHolds(ctx, instance, endpoints...); err != nil {
		return true, ctrl.Result{}, err
	}
	return true, ctrl.Result{RequeueAfter: r.StatusPollInterval}, nil
}

// markAutomaticEndpointDNSJobsForRerun marks publication jobs for the
// replacement endpoint and persists their retry state with the cleared status.
func (r *ClusterOrderReconciler) markAutomaticEndpointDNSJobsForRerun(
	ctx context.Context,
	instance, base *v1alpha1.ClusterOrder,
	endpoints ...string,
) error {
	if r.Engine == nil {
		return nil
	}
	for _, spec := range clusterEndpointDNSJobSpecs(endpoints...) {
		r.Engine.MarkForRerun(instance, spec.publishTask, v1alpha1.NetworkJobManagerNone)
	}
	if equality.Semantic.DeepEqual(base.Status, instance.Status) {
		return nil
	}
	if err := r.Status().Patch(ctx, instance,
		client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("persist automatic endpoint DNS jobs for rerun: %w", err)
	}
	return nil
}

func (r *ClusterOrderReconciler) releaseAutomaticEndpointDNSHolds(
	ctx context.Context, instance *v1alpha1.ClusterOrder, endpoints ...string,
) error {
	attachments, err := r.listAutomaticEndpointAttachments(ctx, instance)
	if err != nil {
		return err
	}
	selected := make(map[string]struct{}, len(endpoints))
	for _, spec := range clusterEndpointDNSJobSpecs(endpoints...) {
		selected[spec.endpoint] = struct{}{}
	}
	for i := range attachments {
		attachment := &attachments[i]
		if _, ok := selected[attachment.Labels[autoCreatedEndpointLabel]]; !ok {
			continue
		}
		if !controllerutil.ContainsFinalizer(attachment, clusterEndpointDNSFinalizer) {
			continue
		}
		base := attachment.DeepCopy()
		controllerutil.RemoveFinalizer(attachment, clusterEndpointDNSFinalizer)
		if err := r.Patch(ctx, attachment, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
			return fmt.Errorf("release DNS hold from automatic ExternalIPAttachment %q: %w", attachment.Name, err)
		}
	}
	return nil
}

func clusterEndpointOrderVars(instance *v1alpha1.ClusterOrder) map[string]any {
	namespace := ""
	if instance.Status.ClusterReference != nil {
		namespace = instance.Status.ClusterReference.Namespace
	}
	return map[string]any{
		"metadata": map[string]any{fieldName: instance.Name, fieldNamespace: instance.Namespace},
		"status": map[string]any{"clusterReference": map[string]any{
			fieldNamespace:      namespace,
			"hostedClusterName": instance.GetClusterReferenceHostedClusterName(),
		}},
	}
}

func endpointJobMessage(out networktask.Outcome, fallback string) string {
	if out.Entry.Message != "" {
		return out.Entry.Message
	}
	return fallback
}

func setClusterEndpointCondition(instance *v1alpha1.ClusterOrder, status metav1.ConditionStatus, reason, message string) {
	instance.SetStatusCondition(string(v1alpha1.ClusterOrderConditionEndpointsReady), status, message, reason)
}

// reconcileVIPEndpoints preserves endpoint values published by legacy CI and
// NICo workflows. Standard CaaS discovers these values from the live services.
func reconcileVIPEndpoints(instance *v1alpha1.ClusterOrder) {
	annotations := instance.GetAnnotations()
	if v := annotations[osacAPIEndpointAnnotation]; v != "" {
		instance.Status.ApiEndpoint = v
	}
	if v := annotations[osacIngressEndpointAnnotation]; v != "" {
		instance.Status.IngressEndpoint = v
	}
}

func clusterAPIEndpointAvailableForAttachment(instance *v1alpha1.ClusterOrder) bool {
	if instance.Status.ApiEndpoint == "" {
		return false
	}
	return !clusterOrderUsesEndpointLifecycle(instance) ||
		apimeta.IsStatusConditionTrue(instance.Status.Conditions,
			string(v1alpha1.ClusterOrderConditionManagementPublicationReady))
}

func (r *ClusterOrderReconciler) cancelClusterEndpointJobsForDelete(
	ctx context.Context, instance *v1alpha1.ClusterOrder, endpoints ...string,
) (bool, time.Duration, error) {
	if r.Engine == nil {
		return true, 0, nil
	}
	if len(endpoints) == 0 {
		return r.Engine.CancelForDelete(ctx, instance, kindClusterOrder)
	}
	// TargetDeleting prevents publication jobs from launching. Cancellation only
	// polls or cancels recorded AAP jobs, so cleanup does not depend on the
	// HostedCluster DNS base domain still being available.
	vars := map[string]any{fieldClusterOrder: clusterEndpointOrderVars(instance)}
	specs := clusterEndpointDNSJobSpecs(endpoints...)
	for _, spec := range specs {
		if !networktask.Started(instance, spec.publishTask, v1alpha1.NetworkJobManagerNone) {
			continue
		}
		out, err := r.runClusterEndpointJob(ctx, instance, spec.publishTask, vars, true)
		if err != nil {
			return false, 0, err
		}
		switch out.Entry.State {
		case v1alpha1.NetworkJobStateSucceeded, v1alpha1.NetworkJobStateFailed, v1alpha1.NetworkJobStateCancelled:
			continue
		default:
			return false, requeueAfterOutcome(out, r.StatusPollInterval), nil
		}
	}
	return true, 0, nil
}

func (r *ClusterOrderReconciler) removePublishedClusterDNS(
	ctx context.Context, instance *v1alpha1.ClusterOrder,
) (bool, ctrl.Result, error) {
	return r.removePublishedClusterDNSForEndpoints(ctx, instance)
}

func (r *ClusterOrderReconciler) removePublishedClusterDNSForEndpoints(
	ctx context.Context, instance *v1alpha1.ClusterOrder, endpoints ...string,
) (bool, ctrl.Result, error) {
	if r.Engine == nil || !r.Engine.ProvisioningEnabled() {
		return true, ctrl.Result{}, nil
	}
	specs := clusterEndpointDNSJobSpecs(endpoints...)
	cleanupSpecs := make([]clusterEndpointDNSJobSpec, 0, len(specs))
	for _, spec := range specs {
		if !networktask.Started(instance, spec.publishTask, v1alpha1.NetworkJobManagerNone) ||
			!clusterEndpointDNSPublicationMayHaveWritten(instance, spec) {
			continue
		}
		removeJob := networktask.FindJob(instance.Status.Jobs, spec.removeTask, v1alpha1.NetworkJobManagerNone)
		if removeJob != nil && removeJob.State == v1alpha1.NetworkJobStateSucceeded {
			// A successful cleanup stays terminal until a new publish attempt marks
			// this remove task for rerun. Do not delete DNS again if hold release
			// retries after the completed cleanup.
			continue
		}
		cleanupSpecs = append(cleanupSpecs, spec)
	}
	if len(cleanupSpecs) == 0 {
		for _, spec := range specs {
			setEndpointDNSHostedClusterName(instance, spec.endpoint, "")
		}
		return true, ctrl.Result{}, nil
	}
	baseDomain, err := r.clusterEndpointDNSBaseDomain(ctx, instance)
	if err != nil {
		return false, ctrl.Result{}, err
	}
	for _, spec := range cleanupSpecs {
		// A publish job may have written only some records before failing or
		// being canceled. The manager's delete task is idempotent, so clean up
		// after any recorded attempt, not only a fully successful publish. The
		// explicit pre-write rejection is the sole exception.
		vars := map[string]any{
			fieldClusterOrder:            clusterEndpointOrderVars(instance),
			fieldHostedClusterName:       endpointDNSHostedClusterName(instance, spec.endpoint),
			fieldHostedClusterBaseDomain: baseDomain,
		}
		out, err := r.runClusterEndpointJob(ctx, instance, spec.removeTask, vars, true)
		if err != nil {
			return false, ctrl.Result{}, err
		}
		if !out.Succeeded() {
			return false, ctrl.Result{RequeueAfter: requeueAfterOutcome(out, r.StatusPollInterval)}, nil
		}
	}
	for _, spec := range specs {
		setEndpointDNSHostedClusterName(instance, spec.endpoint, "")
	}
	return true, ctrl.Result{}, nil
}

func (r *ClusterOrderReconciler) clusterEndpointDNSBaseDomain(
	ctx context.Context, instance *v1alpha1.ClusterOrder,
) (string, error) {
	if baseDomain := clusterEndpointDNSBaseDomainFromStatus(instance); baseDomain != "" {
		return baseDomain, nil
	}
	namespace, err := r.findNamespace(ctx, instance)
	if err != nil {
		return "", fmt.Errorf("find workload namespace for ClusterOrder DNS cleanup: %w", err)
	}
	if namespace == nil {
		return "", fmt.Errorf("workload namespace is unavailable for ClusterOrder %q DNS cleanup", instance.Name)
	}
	hc, err := r.findHostedCluster(ctx, instance, namespace.Name)
	if err != nil {
		return "", fmt.Errorf("find HostedCluster for ClusterOrder DNS cleanup: %w", err)
	}
	if hc == nil || hc.Spec.DNS.BaseDomain == "" {
		return "", fmt.Errorf("HostedCluster base domain is unavailable for ClusterOrder %q DNS cleanup", instance.Name)
	}
	return hc.Spec.DNS.BaseDomain, nil
}

func clusterEndpointDNSBaseDomainFromStatus(instance *v1alpha1.ClusterOrder) string {
	if instance == nil {
		return ""
	}
	for _, endpoint := range []struct {
		url    string
		prefix string
	}{
		{url: instance.Status.APIURL, prefix: "api."},
		{url: instance.Status.ConsoleURL, prefix: "console-openshift-console.apps."},
	} {
		parsed, err := url.Parse(endpoint.url)
		if err != nil || parsed.Hostname() == "" {
			continue
		}
		rest := strings.TrimPrefix(parsed.Hostname(), endpoint.prefix)
		if rest == parsed.Hostname() {
			continue
		}
		parts := strings.SplitN(rest, ".", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return parts[1]
		}
	}
	return ""
}

func (r *ClusterOrderReconciler) releaseClusterIngressReservation(
	ctx context.Context, instance *v1alpha1.ClusterOrder,
) (bool, ctrl.Result, error) {
	if !r.clusterIngressReservationRecorded(instance) {
		return true, ctrl.Result{}, nil
	}
	input, err := networktask.LoadInput(instance)
	if err != nil {
		return false, ctrl.Result{}, err
	}
	manager, ok := input["fabric_manager"].(map[string]any)
	if !ok {
		return false, ctrl.Result{}, fmt.Errorf("ClusterOrder %q ingress reservation input has no recorded Fabric Manager", instance.Name)
	}
	managerName, nameOK := manager[fieldName].(string)
	ansibleRole, roleOK := manager["ansible_role"].(string)
	if !nameOK || managerName == "" || !roleOK || ansibleRole == "" {
		return false, ctrl.Result{}, fmt.Errorf("ClusterOrder %q ingress reservation input has an invalid Fabric Manager", instance.Name)
	}
	out, err := r.Engine.Run(ctx, instance, networktask.Request{
		Task: networktask.ReleaseClusterIngressIP, Manager: v1alpha1.NetworkJobManagerFabric,
		ManagerRef: &v1alpha1.NetworkManagerRef{Name: managerName, AnsibleRole: ansibleRole},
		Kind:       kindClusterOrder, Vars: clusterIngressManagerVars(input),
	})
	if err != nil {
		return false, ctrl.Result{}, err
	}
	if !out.Succeeded() {
		return false, ctrl.Result{RequeueAfter: requeueAfterOutcome(out, r.StatusPollInterval)}, nil
	}
	return true, ctrl.Result{}, nil
}

func (r *ClusterOrderReconciler) clusterIngressReservationRecorded(instance *v1alpha1.ClusterOrder) bool {
	return r.Engine != nil && r.Engine.ProvisioningEnabled() &&
		networktask.Started(instance, networktask.ReserveClusterIngressIP, v1alpha1.NetworkJobManagerFabric)
}

// clusterIngressManagerVars removes operator-owned routing metadata from the
// manager task input. The selected manager is already in the Engine's manager
// envelope; the stored metadata exists only so deletion can recover that ref.
func clusterIngressManagerVars(input map[string]any) map[string]any {
	vars := make(map[string]any, len(input))
	for key, value := range input {
		if key != "fabric_manager" {
			vars[key] = value
		}
	}
	return vars
}

func requeueAfterOutcome(out networktask.Outcome, fallback time.Duration) time.Duration {
	if out.RequeueAfter > 0 {
		return out.RequeueAfter
	}
	if fallback > 0 {
		return fallback
	}
	return 30 * time.Second
}
