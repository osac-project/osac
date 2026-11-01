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
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	autoCreatedLabel         = "osac.openshift.io/auto-created"
	autoCreatedForLabel      = "osac.openshift.io/auto-created-for"
	autoCreatedKindLabel     = "osac.openshift.io/auto-created-kind"
	tenantAnnotation         = "osac.openshift.io/tenant"
	ownerReferenceAnnotation = "osac.openshift.io/owner-reference"
	systemCreator            = "system"

	computeInstanceKind = "compute_instance"

	// recheckInterval is how often an automatic ExternalIP without its ExternalIPAttachment is checked again, while
	// neither the ExternalIP nor the workload is FAILED or being deleted.
	recheckInterval = 30 * time.Second

	autoAttachmentRetryDelay = time.Second
)

type externalIPAttachmentsClient interface {
	List(context.Context, *privatev1.ExternalIPAttachmentsListRequest, ...grpc.CallOption) (*privatev1.ExternalIPAttachmentsListResponse, error)
	Create(context.Context, *privatev1.ExternalIPAttachmentsCreateRequest, ...grpc.CallOption) (*privatev1.ExternalIPAttachmentsCreateResponse, error)
}

type computeInstancesClient interface {
	Get(context.Context, *privatev1.ComputeInstancesGetRequest, ...grpc.CallOption) (*privatev1.ComputeInstancesGetResponse, error)
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
}

// NewFunction creates a builder for the automatic ExternalIPAttachment reconciler. It creates the automatic
// ExternalIPAttachment of a ComputeInstance once its automatic ExternalIP is ALLOCATED and the VM is a ready endpoint.
// The OSAC operator requests the attachments of Clusters and BareMetalInstances.
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
	}
	return r.run, nil
}

// errWaiting tells the reconciler to check the automatic ExternalIP again later.
var errWaiting = errors.New("automatic ExternalIPAttachment waits for its ExternalIP and ComputeInstance")

func (r *function) run(ctx context.Context, externalIP *privatev1.ExternalIP) error {
	if externalIP == nil || !externalIP.HasMetadata() || externalIP.GetMetadata().HasDeletionTimestamp() {
		return nil
	}
	metadata := externalIP.GetMetadata()
	labels := metadata.GetLabels()
	// Only resources with creator system and both auto-created labels are automatic; labels that a tenant sets never
	// make a resource automatic.
	if metadata.GetCreator() != systemCreator || labels[autoCreatedLabel] != "true" ||
		labels[autoCreatedForLabel] == "" || labels[autoCreatedKindLabel] != computeInstanceKind {
		return nil
	}
	switch externalIP.GetStatus().GetState() {
	case privatev1.ExternalIPState_EXTERNAL_IP_STATE_FAILED:
		return nil
	case privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED:
	default:
		return controllers.RequeueAtInterval(errWaiting, recheckInterval)
	}

	tenant := metadata.GetTenant()
	ownerID := labels[autoCreatedForLabel]
	existing, err := r.findAttachment(ctx, externalIP.GetId())
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}

	response, err := r.computeInstancesClient.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: ownerID}.Build())
	if status.Code(err) == codes.NotFound {
		return nil
	}
	if err != nil {
		return retryOnTransientError(fmt.Errorf("failed to get ComputeInstance %q: %w", ownerID, err))
	}
	instance := response.GetObject()
	if instance.GetMetadata().GetTenant() != tenant || instance.GetMetadata().HasDeletionTimestamp() ||
		instance.GetStatus().GetState() == privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED {
		return nil
	}
	if !isReadyEndpoint(instance) {
		return controllers.RequeueAtInterval(errWaiting, recheckInterval)
	}

	attachment := privatev1.ExternalIPAttachment_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   "auto-eipa-" + externalIP.GetId(),
			Tenant: tenant,
			Labels: map[string]string{
				autoCreatedLabel:    "true",
				autoCreatedForLabel: ownerID,
			},
			Annotations: map[string]string{
				tenantAnnotation:         tenant,
				ownerReferenceAnnotation: ownerID,
			},
		}.Build(),
		Spec: privatev1.ExternalIPAttachmentSpec_builder{
			ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: externalIP.GetId()}.Build(),
			ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ownerID}.Build(),
		}.Build(),
	}.Build()
	_, err = r.externalIPAttachmentsClient.Create(ctx,
		privatev1.ExternalIPAttachmentsCreateRequest_builder{Object: attachment}.Build())
	switch {
	case err == nil:
		r.logger.InfoContext(ctx, "Created automatic ExternalIPAttachment",
			slog.String("external_ip_id", externalIP.GetId()), slog.String("compute_instance_id", ownerID))
		return nil
	case status.Code(err) == codes.AlreadyExists:
		// The create is idempotent by name.
		return nil
	case status.Code(err) == codes.FailedPrecondition:
		// A gate isn't met yet, for example the VM became not ready meanwhile.
		return controllers.RequeueAtInterval(err, recheckInterval)
	default:
		return retryOnTransientError(fmt.Errorf(
			"failed to create automatic ExternalIPAttachment for ExternalIP %q: %w", externalIP.GetId(), err))
	}
}

// isReadyEndpoint reports whether a VM is a ready ExternalIPAttachment endpoint: RUNNING, condition READY true, and
// the address of its network attachment reported.
func isReadyEndpoint(instance *privatev1.ComputeInstance) bool {
	if instance.GetStatus().GetState() != privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING {
		return false
	}
	statuses := instance.GetStatus().GetComputeNetworkAttachmentStatuses()
	if len(statuses) == 0 || statuses[0].GetIpAddress() == "" {
		return false
	}
	for _, condition := range instance.GetStatus().GetConditions() {
		if condition.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_READY &&
			condition.GetStatus() == privatev1.ConditionStatus_CONDITION_STATUS_TRUE {
			return true
		}
	}
	return false
}

func (r *function) findAttachment(ctx context.Context, externalIPID string) (*privatev1.ExternalIPAttachment, error) {
	filter := fmt.Sprintf("this.spec.external_ip.id == %s", strconv.Quote(externalIPID))
	response, err := r.externalIPAttachmentsClient.List(ctx, privatev1.ExternalIPAttachmentsListRequest_builder{
		Filter: proto.String(filter),
		Limit:  proto.Int32(1),
	}.Build())
	if err != nil {
		return nil, retryOnTransientError(fmt.Errorf("failed to list ExternalIPAttachments for ExternalIP %q: %w", externalIPID, err))
	}
	if len(response.GetItems()) == 0 {
		return nil, nil
	}
	return response.GetItems()[0], nil
}

func retryOnTransientError(err error) error {
	switch status.Code(err) {
	case codes.Aborted, codes.DeadlineExceeded, codes.Internal, codes.ResourceExhausted, codes.Unavailable, codes.Unknown:
		return controllers.RequeueAfter(err, autoAttachmentRetryDelay)
	default:
		return err
	}
}
