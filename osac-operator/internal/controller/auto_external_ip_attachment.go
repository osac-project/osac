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
	"fmt"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	autoCreatedKindLabel     = osacPrefix + "/auto-created-kind"
	autoCreatedEndpointLabel = osacPrefix + "/auto-created-endpoint"
	externalIPUUIDLabel      = osacPrefix + "/externalip-uuid"
	tenantAnnotation         = osacPrefix + "/tenant"
	ownerReferenceAnnotation = osacPrefix + "/owner-reference"

	autoCreatedEndpointAPI     = "api"
	autoCreatedEndpointIngress = "ingress"
	autoCreatedKindCluster     = "cluster"
	autoCreatedKindBareMetal   = "bare_metal_instance"
)

type automaticExternalIPAttachmentsClient interface {
	List(context.Context, *privatev1.ExternalIPAttachmentsListRequest, ...grpc.CallOption) (*privatev1.ExternalIPAttachmentsListResponse, error)
	Create(context.Context, *privatev1.ExternalIPAttachmentsCreateRequest, ...grpc.CallOption) (*privatev1.ExternalIPAttachmentsCreateResponse, error)
}

type automaticClustersGetter interface {
	Get(context.Context, *privatev1.ClustersGetRequest, ...grpc.CallOption) (*privatev1.ClustersGetResponse, error)
}

type automaticBareMetalInstancesGetter interface {
	Get(context.Context, *privatev1.BareMetalInstancesGetRequest, ...grpc.CallOption) (*privatev1.BareMetalInstancesGetResponse, error)
}

type automaticExternalIPAttachmentTarget struct {
	owner              autoExternalIPOwner
	kind               autoExternalIPOwnerKind
	kindLabel          string
	tenant             string
	endpoints          []string
	availableEndpoints map[string]bool
}

// reconcileAutomaticExternalIPAttachments is called from the workload's existing
// reconciler after it becomes ready. It asks Fulfillment to create the EIA record;
// the normal Fulfillment projector and ExternalIPAttachment controller then create
// and attach the Kubernetes resource.
func reconcileAutomaticExternalIPAttachments(
	ctx context.Context,
	k8sClient client.Client,
	networkingNamespace string,
	attachmentsClient automaticExternalIPAttachmentsClient,
	pollInterval time.Duration,
	target automaticExternalIPAttachmentTarget,
) (ctrl.Result, error) {
	if networkingNamespace == "" || attachmentsClient == nil {
		return ctrl.Result{}, nil
	}
	if err := validateAutomaticExternalIPAttachmentTarget(target); err != nil {
		return ctrl.Result{}, err
	}
	if pollInterval <= 0 {
		pollInterval = defaultPreconditionRequeueInterval
	}

	ipsByEndpoint, err := listAutomaticExternalIPs(ctx, k8sClient, networkingNamespace, target)
	if err != nil {
		return ctrl.Result{}, err
	}
	waiting := false
	for _, endpoint := range target.endpoints {
		if !target.availableEndpoints[endpoint] {
			waiting = true
			continue
		}
		ip := ipsByEndpoint[endpoint]
		if ip == nil || !ip.DeletionTimestamp.IsZero() {
			waiting = true
			continue
		}

		endpointWaiting, err := reconcileAutomaticExternalIPAttachment(ctx, attachmentsClient, ip, endpoint, target)
		if err != nil {
			return ctrl.Result{}, err
		}
		waiting = waiting || endpointWaiting
	}
	if waiting {
		return ctrl.Result{RequeueAfter: pollInterval}, nil
	}
	return ctrl.Result{}, nil
}

func validateAutomaticExternalIPAttachmentTarget(target automaticExternalIPAttachmentTarget) error {
	if target.owner.id == "" {
		return fmt.Errorf("automatic ExternalIPAttachment target has no ID")
	}
	if target.tenant == "" {
		return fmt.Errorf("automatic ExternalIPAttachment target %q has no tenant", target.owner.id)
	}
	if target.kindLabel == "" || target.kindLabel != autoExternalIPKindLabel(target.kind) || len(target.endpoints) == 0 {
		return fmt.Errorf("automatic ExternalIPAttachment target %q has an invalid kind or endpoint set", target.owner.id)
	}
	return nil
}

func listAutomaticExternalIPs(
	ctx context.Context,
	k8sClient client.Client,
	networkingNamespace string,
	target automaticExternalIPAttachmentTarget,
) (map[string]*v1alpha1.ExternalIP, error) {
	var externalIPs v1alpha1.ExternalIPList
	if err := k8sClient.List(ctx, &externalIPs,
		client.InNamespace(networkingNamespace),
		client.MatchingLabels{
			autoCreatedLabel:     labelValueTrue,
			autoCreatedForLabel:  target.owner.id,
			autoCreatedKindLabel: target.kindLabel,
		},
	); err != nil {
		return nil, fmt.Errorf("list automatic ExternalIPs for %s %q: %w", target.kindLabel, target.owner.id, err)
	}

	ipsByEndpoint := make(map[string]*v1alpha1.ExternalIP, len(externalIPs.Items))
	for i := range externalIPs.Items {
		ip := &externalIPs.Items[i]
		endpoint := ip.Labels[autoCreatedEndpointLabel]
		if target.kind == clusterOrderOwner {
			if endpoint != autoCreatedEndpointAPI && endpoint != autoCreatedEndpointIngress {
				return nil, fmt.Errorf("automatic Cluster ExternalIP %q has invalid endpoint %q", ip.Name, endpoint)
			}
		} else if endpoint != "" {
			return nil, fmt.Errorf("automatic ExternalIP %q for %s unexpectedly has endpoint %q", ip.Name, target.kindLabel, endpoint)
		}
		if _, exists := ipsByEndpoint[endpoint]; exists {
			return nil, fmt.Errorf("multiple automatic ExternalIPs exist for %s %q endpoint %q", target.kindLabel, target.owner.id, endpoint)
		}
		ipsByEndpoint[endpoint] = ip
	}
	return ipsByEndpoint, nil
}

func reconcileAutomaticExternalIPAttachment(
	ctx context.Context,
	attachmentsClient automaticExternalIPAttachmentsClient,
	ip *v1alpha1.ExternalIP,
	endpoint string,
	target automaticExternalIPAttachmentTarget,
) (bool, error) {
	if ip.Status.State == v1alpha1.ExternalIPStateFailed {
		return false, fmt.Errorf("automatic ExternalIP %q failed allocation", ip.Name)
	}
	if ip.Status.State != v1alpha1.ExternalIPStateAllocated || ip.Status.Address == "" {
		return true, nil
	}

	ipID := ip.Labels[externalIPUUIDLabel]
	if ipID == "" {
		return false, fmt.Errorf("allocated automatic ExternalIP %q has no Fulfillment ID label", ip.Name)
	}
	if ip.Annotations[tenantAnnotation] != target.tenant {
		return false, fmt.Errorf("automatic ExternalIP %q tenant does not match %s %q", ip.Name, target.kindLabel, target.owner.id)
	}

	expected := buildAutomaticExternalIPAttachment(ipID, endpoint, target)
	existing, err := findAutomaticExternalIPAttachment(ctx, attachmentsClient, ipID)
	if err != nil {
		return false, err
	}
	if existing != nil {
		return false, validateAutomaticExternalIPAttachment(existing, expected, target)
	}

	_, err = attachmentsClient.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{Object: expected}.Build())
	switch status.Code(err) {
	case codes.OK:
		return false, nil
	case codes.AlreadyExists:
		return validateConcurrentAutomaticExternalIPAttachment(ctx, attachmentsClient, ipID, expected, target)
	case codes.FailedPrecondition, codes.NotFound:
		// Fulfillment may not yet have observed the Ready/Allocated status that
		// this reconciler observed locally. Its create API remains authoritative.
		return true, nil
	default:
		return false, fmt.Errorf("create automatic ExternalIPAttachment for ExternalIP %q: %w", ipID, err)
	}
}

func validateConcurrentAutomaticExternalIPAttachment(
	ctx context.Context,
	attachmentsClient automaticExternalIPAttachmentsClient,
	ipID string,
	expected *privatev1.ExternalIPAttachment,
	target automaticExternalIPAttachmentTarget,
) (bool, error) {
	existing, err := findAutomaticExternalIPAttachment(ctx, attachmentsClient, ipID)
	if err != nil {
		return false, err
	}
	if existing == nil {
		return false, fmt.Errorf("ExternalIPAttachment for ExternalIP %q already exists but cannot be found", ipID)
	}
	return false, validateAutomaticExternalIPAttachment(existing, expected, target)
}

func autoExternalIPKindLabel(kind autoExternalIPOwnerKind) string {
	switch kind {
	case clusterOrderOwner:
		return autoCreatedKindCluster
	case bareMetalInstanceOwner:
		return autoCreatedKindBareMetal
	default:
		return ""
	}
}

func buildAutomaticExternalIPAttachment(
	ipID string,
	endpoint string,
	target automaticExternalIPAttachmentTarget,
) *privatev1.ExternalIPAttachment {
	spec := &privatev1.ExternalIPAttachmentSpec_builder{
		ExternalIp: privatev1.ExternalIPLocalReference_builder{Id: ipID}.Build(),
	}
	switch target.kind {
	case clusterOrderOwner:
		spec.Cluster = privatev1.ClusterLocalReference_builder{Id: target.owner.id}.Build()
		switch endpoint {
		case autoCreatedEndpointAPI:
			spec.TargetEndpoint = privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API
		case autoCreatedEndpointIngress:
			spec.TargetEndpoint = privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS
		}
	case bareMetalInstanceOwner:
		spec.BaremetalInstance = privatev1.BareMetalInstanceLocalReference_builder{Id: target.owner.id}.Build()
	}

	labels := map[string]string{
		autoCreatedLabel:    labelValueTrue,
		autoCreatedForLabel: target.owner.id,
	}
	if endpoint != "" {
		labels[autoCreatedEndpointLabel] = endpoint
	}
	return privatev1.ExternalIPAttachment_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   "auto-eipa-" + ipID,
			Tenant: target.tenant,
			Labels: labels,
			Annotations: map[string]string{
				tenantAnnotation:         target.tenant,
				ownerReferenceAnnotation: target.owner.id,
			},
		}.Build(),
		Spec: spec.Build(),
	}.Build()
}

func findAutomaticExternalIPAttachment(
	ctx context.Context,
	attachmentsClient automaticExternalIPAttachmentsClient,
	ipID string,
) (*privatev1.ExternalIPAttachment, error) {
	filter := fmt.Sprintf("this.spec.external_ip.id == %s && !has(this.metadata.deletion_timestamp)", strconv.Quote(ipID))
	response, err := attachmentsClient.List(ctx, privatev1.ExternalIPAttachmentsListRequest_builder{
		Filter: proto.String(filter),
		Limit:  proto.Int32(2),
	}.Build())
	if err != nil {
		return nil, fmt.Errorf("list ExternalIPAttachments for ExternalIP %q: %w", ipID, err)
	}
	if len(response.GetItems()) > 1 {
		return nil, fmt.Errorf("multiple ExternalIPAttachments exist for ExternalIP %q", ipID)
	}
	if len(response.GetItems()) == 0 {
		return nil, nil
	}
	return response.GetItems()[0], nil
}

func validateAutomaticExternalIPAttachment(
	actual *privatev1.ExternalIPAttachment,
	expected *privatev1.ExternalIPAttachment,
	target automaticExternalIPAttachmentTarget,
) error {
	metadata := actual.GetMetadata()
	labels := metadata.GetLabels()
	if labels[autoCreatedLabel] != labelValueTrue || labels[autoCreatedForLabel] != target.owner.id ||
		metadata.GetTenant() != target.tenant || metadata.GetAnnotations()[tenantAnnotation] != target.tenant ||
		metadata.GetAnnotations()[ownerReferenceAnnotation] != target.owner.id ||
		!proto.Equal(actual.GetSpec(), expected.GetSpec()) {
		return fmt.Errorf("ExternalIP %q is already attached by a different or inconsistent ExternalIPAttachment",
			expected.GetSpec().GetExternalIp().GetId())
	}
	return nil
}

func mergeReconcileResult(current, additional ctrl.Result) ctrl.Result {
	if additional.RequeueAfter > 0 && (current.RequeueAfter == 0 || additional.RequeueAfter < current.RequeueAfter) {
		current.RequeueAfter = additional.RequeueAfter
	}
	return current
}
