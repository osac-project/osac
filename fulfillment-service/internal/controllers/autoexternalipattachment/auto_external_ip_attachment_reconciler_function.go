/*
Copyright (c) 2026 Red Hat Inc.

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

package autoexternalipattachment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	autoCreatedLabel            = "osac.openshift.io/auto-created"
	autoCreatedForLabel         = "osac.openshift.io/auto-created-for"
	autoAttachmentDeferredLabel = "osac.openshift.io/auto-attachment-deferred"
	autoCreatedKindLabel        = "osac.openshift.io/auto-created-kind"
	autoCreatedEndpointLabel    = "osac.openshift.io/auto-created-endpoint"
	tenantAnnotation            = "osac.openshift.io/tenant"
	ownerReferenceAnnotation    = "osac.openshift.io/owner-reference"
	systemCreator               = "system"

	computeInstanceKind   = "compute_instance"
	clusterKind           = "cluster"
	bareMetalInstanceKind = "bare_metal_instance"
)

type externalIPAttachmentsClient interface {
	List(context.Context, *privatev1.ExternalIPAttachmentsListRequest, ...grpc.CallOption) (*privatev1.ExternalIPAttachmentsListResponse, error)
	Create(context.Context, *privatev1.ExternalIPAttachmentsCreateRequest, ...grpc.CallOption) (*privatev1.ExternalIPAttachmentsCreateResponse, error)
}

type computeInstancesClient interface {
	Get(context.Context, *privatev1.ComputeInstancesGetRequest, ...grpc.CallOption) (*privatev1.ComputeInstancesGetResponse, error)
}

type clustersClient interface {
	Get(context.Context, *privatev1.ClustersGetRequest, ...grpc.CallOption) (*privatev1.ClustersGetResponse, error)
}

type bareMetalInstancesClient interface {
	Get(context.Context, *privatev1.BareMetalInstancesGetRequest, ...grpc.CallOption) (*privatev1.BareMetalInstancesGetResponse, error)
}

// FunctionBuilder holds the dependencies needed to create automatic ExternalIPAttachments.
type FunctionBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
}

type function struct {
	logger                      *slog.Logger
	externalIPAttachmentsClient externalIPAttachmentsClient
	computeInstancesClient      computeInstancesClient
	clustersClient              clustersClient
	bareMetalInstancesClient    bareMetalInstancesClient
}

// NewFunction creates a builder for the deferred automatic ExternalIPAttachment reconciler.
func NewFunction() *FunctionBuilder {
	return &FunctionBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *FunctionBuilder) SetLogger(value *slog.Logger) *FunctionBuilder {
	b.logger = value
	return b
}

// SetConnection sets the Fulfillment gRPC connection. This is mandatory.
func (b *FunctionBuilder) SetConnection(value *grpc.ClientConn) *FunctionBuilder {
	b.connection = value
	return b
}

// Build creates the reconciler function.
func (b *FunctionBuilder) Build() (controllers.ReconcilerFunction[*privatev1.ExternalIP], error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.connection == nil {
		return nil, errors.New("client is mandatory")
	}
	r := &function{
		logger:                      b.logger,
		externalIPAttachmentsClient: privatev1.NewExternalIPAttachmentsClient(b.connection),
		computeInstancesClient:      privatev1.NewComputeInstancesClient(b.connection),
		clustersClient:              privatev1.NewClustersClient(b.connection),
		bareMetalInstancesClient:    privatev1.NewBareMetalInstancesClient(b.connection),
	}
	return r.run, nil
}

func (r *function) run(ctx context.Context, externalIP *privatev1.ExternalIP) error {
	if externalIP == nil || !externalIP.HasMetadata() || externalIP.GetMetadata().HasDeletionTimestamp() {
		return nil
	}
	metadata := externalIP.GetMetadata()
	labels := metadata.GetLabels()
	if labels[autoCreatedLabel] != "true" || labels[autoAttachmentDeferredLabel] != "true" {
		return nil
	}
	// GenericServer assigns creator from the authenticated caller. Automatic
	// workload ExternalIPs are created directly by the server with creator=system,
	// so caller-supplied labels and annotations cannot opt a tenant IP into this
	// privileged reconciler.
	if metadata.GetCreator() != systemCreator {
		r.logger.DebugContext(ctx, "Skipping deferred automatic attachment for non-system ExternalIP")
		return nil
	}
	if externalIP.GetStatus().GetState() != privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED {
		return nil
	}
	if externalIP.GetId() == "" {
		return errors.New("deferred automatic ExternalIP has no ID")
	}

	tenant := metadata.GetTenant()
	ownerID := labels[autoCreatedForLabel]
	if tenant == "" {
		return fmt.Errorf("deferred automatic ExternalIP %q has no tenant", externalIP.GetId())
	}
	if ownerID == "" {
		return fmt.Errorf("deferred automatic ExternalIP %q has no owner reference", externalIP.GetId())
	}

	attachmentSpec, ready, err := r.targetSpec(ctx, labels[autoCreatedKindLabel], ownerID, tenant, labels[autoCreatedEndpointLabel])
	if err != nil || !ready {
		return err
	}
	attachmentSpec.ExternalIp = privatev1.ExternalIPLocalReference_builder{Id: externalIP.GetId()}.Build()
	expectedSpec := attachmentSpec.Build()

	existing, err := r.findAttachment(ctx, externalIP.GetId())
	if err != nil {
		return err
	}
	if existing != nil {
		return r.validateExisting(existing, ownerID, tenant, expectedSpec)
	}

	attachmentLabels := map[string]string{
		autoCreatedLabel:    "true",
		autoCreatedForLabel: ownerID,
	}
	if endpoint := labels[autoCreatedEndpointLabel]; endpoint != "" {
		attachmentLabels[autoCreatedEndpointLabel] = endpoint
	}
	attachment := privatev1.ExternalIPAttachment_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   fmt.Sprintf("auto-eipa-%s", externalIP.GetId()),
			Tenant: tenant,
			Labels: attachmentLabels,
			Annotations: map[string]string{
				tenantAnnotation:         tenant,
				ownerReferenceAnnotation: ownerID,
			},
		}.Build(),
		Spec: expectedSpec,
	}.Build()
	_, err = r.externalIPAttachmentsClient.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{Object: attachment}.Build())
	if status.Code(err) != codes.AlreadyExists {
		if err != nil {
			return fmt.Errorf("failed to create automatic ExternalIPAttachment for ExternalIP %q: %w", externalIP.GetId(), err)
		}
		r.logger.InfoContext(ctx, "Created deferred automatic ExternalIPAttachment")
		return nil
	}

	// A concurrent reconcile may have created the unique attachment after our lookup.
	existing, listErr := r.findAttachment(ctx, externalIP.GetId())
	if listErr != nil {
		return listErr
	}
	if existing == nil {
		return fmt.Errorf("ExternalIPAttachment for ExternalIP %q already exists but could not be found", externalIP.GetId())
	}
	return r.validateExisting(existing, ownerID, tenant, expectedSpec)
}

func (r *function) targetSpec(
	ctx context.Context,
	kind string,
	ownerID string,
	tenant string,
	endpoint string,
) (*privatev1.ExternalIPAttachmentSpec_builder, bool, error) {
	spec := &privatev1.ExternalIPAttachmentSpec_builder{}
	switch kind {
	case computeInstanceKind:
		response, err := r.computeInstancesClient.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: ownerID}.Build())
		if err != nil {
			return nil, false, fmt.Errorf("failed to get ComputeInstance %q: %w", ownerID, err)
		}
		instance := response.GetObject()
		if !sameTenant(instance.GetMetadata(), tenant) || instance.GetMetadata().HasDeletionTimestamp() {
			return nil, false, nil
		}
		if instance.GetStatus().GetState() != privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING ||
			!hasComputeInstanceReadyCondition(instance) || instance.GetStatus().GetInternalIpAddress() == "" {
			return nil, false, nil
		}
		spec.ComputeInstance = privatev1.ComputeInstanceLocalReference_builder{Id: ownerID}.Build()
	case clusterKind:
		response, err := r.clustersClient.Get(ctx, privatev1.ClustersGetRequest_builder{Id: ownerID}.Build())
		if err != nil {
			return nil, false, fmt.Errorf("failed to get Cluster %q: %w", ownerID, err)
		}
		cluster := response.GetObject()
		if !sameTenant(cluster.GetMetadata(), tenant) || cluster.GetMetadata().HasDeletionTimestamp() {
			return nil, false, nil
		}
		if cluster.GetStatus().GetState() != privatev1.ClusterState_CLUSTER_STATE_READY {
			return nil, false, nil
		}
		var targetEndpoint privatev1.ExternalIPAttachmentEndpoint
		switch endpoint {
		case "api":
			targetEndpoint = privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API
			if cluster.GetStatus().GetApiEndpoint() == "" {
				return nil, false, nil
			}
		case "ingress":
			targetEndpoint = privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS
			if cluster.GetStatus().GetIngressEndpoint() == "" {
				return nil, false, nil
			}
		default:
			return nil, false, fmt.Errorf("deferred Cluster ExternalIP has invalid endpoint %q", endpoint)
		}
		spec.Cluster = privatev1.ClusterLocalReference_builder{Id: ownerID}.Build()
		spec.TargetEndpoint = targetEndpoint
	case bareMetalInstanceKind:
		response, err := r.bareMetalInstancesClient.Get(ctx, privatev1.BareMetalInstancesGetRequest_builder{Id: ownerID}.Build())
		if err != nil {
			return nil, false, fmt.Errorf("failed to get BareMetalInstance %q: %w", ownerID, err)
		}
		instance := response.GetObject()
		if !sameTenant(instance.GetMetadata(), tenant) || instance.GetMetadata().HasDeletionTimestamp() {
			return nil, false, nil
		}
		if instance.GetStatus().GetState() != privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING ||
			!hasBareMetalReadyCondition(instance) || !hasPrimaryBareMetalAddress(instance) {
			return nil, false, nil
		}
		spec.BaremetalInstance = privatev1.BareMetalInstanceLocalReference_builder{Id: ownerID}.Build()
	default:
		return nil, false, fmt.Errorf("deferred automatic ExternalIP has unsupported owner kind %q", kind)
	}
	return spec, true, nil
}

func (r *function) findAttachment(ctx context.Context, externalIPID string) (*privatev1.ExternalIPAttachment, error) {
	filter := fmt.Sprintf("this.spec.external_ip.id == %s && !has(this.metadata.deletion_timestamp)", strconv.Quote(externalIPID))
	response, err := r.externalIPAttachmentsClient.List(ctx, privatev1.ExternalIPAttachmentsListRequest_builder{
		Filter: proto.String(filter),
		Limit:  proto.Int32(2),
	}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to list ExternalIPAttachments for ExternalIP %q: %w", externalIPID, err)
	}
	if len(response.GetItems()) > 1 {
		return nil, fmt.Errorf("multiple ExternalIPAttachments exist for ExternalIP %q", externalIPID)
	}
	if len(response.GetItems()) == 0 {
		return nil, nil
	}
	return response.GetItems()[0], nil
}

func (r *function) validateExisting(
	attachment *privatev1.ExternalIPAttachment,
	ownerID string,
	tenant string,
	expectedSpec *privatev1.ExternalIPAttachmentSpec,
) error {
	externalIPID := expectedSpec.GetExternalIp().GetId()
	metadata := attachment.GetMetadata()
	labels := metadata.GetLabels()
	if labels[autoCreatedLabel] != "true" || labels[autoCreatedForLabel] != ownerID ||
		metadata.GetTenant() != tenant || !proto.Equal(attachment.GetSpec(), expectedSpec) {
		return fmt.Errorf("ExternalIP %q is already attached by a different or inconsistent ExternalIPAttachment", externalIPID)
	}
	return nil
}

func sameTenant(metadata *privatev1.Metadata, tenant string) bool {
	return metadata != nil && metadata.GetTenant() == tenant
}

func hasComputeInstanceReadyCondition(instance *privatev1.ComputeInstance) bool {
	for _, condition := range instance.GetStatus().GetConditions() {
		if condition.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_READY &&
			condition.GetStatus() == privatev1.ConditionStatus_CONDITION_STATUS_TRUE {
			return true
		}
	}
	return false
}

func hasBareMetalReadyCondition(instance *privatev1.BareMetalInstance) bool {
	for _, condition := range instance.GetStatus().GetConditions() {
		if condition.GetType() == privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_READY &&
			condition.GetStatus() == privatev1.ConditionStatus_CONDITION_STATUS_TRUE {
			return true
		}
	}
	return false
}

func hasPrimaryBareMetalAddress(instance *privatev1.BareMetalInstance) bool {
	for _, attachment := range instance.GetStatus().GetNetworkAttachmentStatuses() {
		if attachment.GetPrimary() && attachment.GetIpAddress() != "" {
			return true
		}
	}
	return false
}
