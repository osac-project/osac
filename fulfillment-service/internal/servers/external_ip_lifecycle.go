/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	systemCreator               = "system"
	autoCreatedLabel            = "osac.openshift.io/auto-created"
	autoCreatedForLabel         = "osac.openshift.io/auto-created-for"
	autoProvisionedLabel        = "osac.openshift.io/auto-provisioned"
	autoProvisionedForLabel     = "osac.openshift.io/auto-provisioned-for"
	autoAttachmentDeferredLabel = "osac.openshift.io/auto-attachment-deferred"
	autoCreatedKindLabel        = "osac.openshift.io/auto-created-kind"
	autoCreatedEndpointLabel    = "osac.openshift.io/auto-created-endpoint"
	tenantAnnotation            = "osac.openshift.io/tenant"
)

var validExternalIPAttachmentTransitions = map[privatev1.ExternalIPAttachmentState][]privatev1.ExternalIPAttachmentState{
	privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING: {
		privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_READY,
		privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_FAILED,
		privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_DELETING,
	},
	privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_READY: {
		privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_FAILED,
		privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_DELETING,
	},
	privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_FAILED: {
		privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING,
		privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_DELETING,
	},
	privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_DELETING: {
		privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_FAILED,
	},
}

type externalIPLifecycle struct {
	externalIPDao           *dao.GenericDAO[*privatev1.ExternalIP]
	externalIPAttachmentDao *dao.GenericDAO[*privatev1.ExternalIPAttachment]
	natGatewayDao           *dao.GenericDAO[*privatev1.NATGateway]
	externalIPPoolDao       *dao.GenericDAO[*privatev1.ExternalIPPool]
	computeInstanceDao      *dao.GenericDAO[*privatev1.ComputeInstance]
	clusterDao              *dao.GenericDAO[*privatev1.Cluster]
	bareMetalInstanceDao    *dao.GenericDAO[*privatev1.BareMetalInstance]
	virtualNetworkDao       *dao.GenericDAO[*privatev1.VirtualNetwork]
}

func newExternalIPLifecycle(
	externalIPDao *dao.GenericDAO[*privatev1.ExternalIP],
	externalIPAttachmentDao *dao.GenericDAO[*privatev1.ExternalIPAttachment],
	natGatewayDao *dao.GenericDAO[*privatev1.NATGateway],
	externalIPPoolDao *dao.GenericDAO[*privatev1.ExternalIPPool],
	computeInstanceDao *dao.GenericDAO[*privatev1.ComputeInstance],
	clusterDao *dao.GenericDAO[*privatev1.Cluster],
	bareMetalInstanceDao *dao.GenericDAO[*privatev1.BareMetalInstance],
	virtualNetworkDao *dao.GenericDAO[*privatev1.VirtualNetwork],
) *externalIPLifecycle {
	return &externalIPLifecycle{
		externalIPDao:           externalIPDao,
		externalIPAttachmentDao: externalIPAttachmentDao,
		natGatewayDao:           natGatewayDao,
		externalIPPoolDao:       externalIPPoolDao,
		computeInstanceDao:      computeInstanceDao,
		clusterDao:              clusterDao,
		bareMetalInstanceDao:    bareMetalInstanceDao,
		virtualNetworkDao:       virtualNetworkDao,
	}
}

func validatePrivateLifecycleUpdateMask(mask *fieldmaskpb.FieldMask, allowedStatusPaths, rejectedStatusPaths []string) error {
	if mask == nil || len(mask.GetPaths()) == 0 {
		return grpcstatus.Error(grpccodes.InvalidArgument, "update_mask is mandatory for private lifecycle updates")
	}
	for _, path := range mask.GetPaths() {
		if path == "metadata" || path == "spec" || strings.HasPrefix(path, "metadata"+".") || strings.HasPrefix(path, "spec"+".") {
			continue
		}
		if path == "status" || strings.HasPrefix(path, "status"+".") {
			if slices.Contains(rejectedStatusPaths, path) || !slices.Contains(allowedStatusPaths, path) {
				return grpcstatus.Error(grpccodes.InvalidArgument, "status output field cannot be updated through this path")
			}
			continue
		}
		return grpcstatus.Error(grpccodes.InvalidArgument, "update_mask path is not allowed for this private lifecycle update")
	}
	return nil
}

func rejectOutputStatusOnCreate(hasStatus bool) error {
	if hasStatus {
		return grpcstatus.Error(grpccodes.InvalidArgument, "status output fields cannot be set on create")
	}
	return nil
}

func translateLifecycleError(err error) error {
	if _, ok := errors.AsType[*dao.ErrNotFound](err); ok {
		return grpcstatus.Errorf(grpccodes.NotFound, "object not found")
	}
	if _, ok := errors.AsType[*dao.ErrDeadlock](err); ok {
		return grpcstatus.Errorf(grpccodes.Aborted, "concurrent modification detected, please retry")
	}
	if inUseErr, ok := errors.AsType[*dao.ErrInUse](err); ok {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "%s", inUseErr.Error())
	}
	return err
}

func (l *externalIPLifecycle) lockAttachment(ctx context.Context, id string) (*privatev1.ExternalIP, *privatev1.ExternalIPAttachment, error) {
	initialResponse, err := l.externalIPAttachmentDao.Get().SetId(id).Do(ctx)
	if err != nil {
		return nil, nil, err
	}
	initial := initialResponse.GetObject()
	externalIPID := refKey(initial.GetSpec().GetExternalIp())
	if externalIPID == "" {
		return nil, nil, errors.New("external IP attachment has no external IP reference")
	}

	parentResponse, err := l.externalIPDao.Get().SetId(externalIPID).SetLock(true).Do(ctx)
	if err != nil {
		return nil, nil, err
	}
	attachmentResponse, err := l.externalIPAttachmentDao.Get().SetId(id).SetLock(true).Do(ctx)
	if err != nil {
		return nil, nil, err
	}
	attachment := attachmentResponse.GetObject()
	if refKey(attachment.GetSpec().GetExternalIp()) != externalIPID {
		return nil, nil, errors.New("external IP attachment reference changed while acquiring lifecycle lock")
	}
	if err := l.lockAttachmentTarget(ctx, attachment); err != nil {
		return nil, nil, err
	}
	return parentResponse.GetObject(), attachment, nil
}

func (l *externalIPLifecycle) lockAttachmentTarget(ctx context.Context, attachment *privatev1.ExternalIPAttachment) error {
	spec := attachment.GetSpec()
	switch {
	case spec.HasComputeInstance():
		if l.computeInstanceDao == nil {
			return errors.New("compute instance DAO is not configured")
		}
		_, err := l.computeInstanceDao.Get().SetId(refKey(spec.GetComputeInstance())).SetLock(true).Do(ctx)
		return err
	case spec.HasCluster():
		if l.clusterDao == nil {
			return errors.New("cluster DAO is not configured")
		}
		_, err := l.clusterDao.Get().SetId(refKey(spec.GetCluster())).SetLock(true).Do(ctx)
		return err
	case spec.HasBaremetalInstance():
		if l.bareMetalInstanceDao == nil {
			return errors.New("bare metal instance DAO is not configured")
		}
		_, err := l.bareMetalInstanceDao.Get().SetId(refKey(spec.GetBaremetalInstance())).SetLock(true).Do(ctx)
		return err
	default:
		return errors.New("external IP attachment has no target reference")
	}
}

func (l *externalIPLifecycle) lockNATGateway(ctx context.Context, id string) (*privatev1.ExternalIP, *privatev1.NATGateway, error) {
	initialResponse, err := l.natGatewayDao.Get().SetId(id).Do(ctx)
	if err != nil {
		return nil, nil, err
	}
	initial := initialResponse.GetObject()
	externalIPID := refKey(initial.GetSpec().GetExternalIp())
	virtualNetworkID := refKey(initial.GetSpec().GetVirtualNetwork())
	if externalIPID == "" || virtualNetworkID == "" {
		return nil, nil, errors.New("NAT gateway has an incomplete reference")
	}

	parentResponse, err := l.externalIPDao.Get().SetId(externalIPID).SetLock(true).Do(ctx)
	if err != nil {
		return nil, nil, err
	}
	natResponse, err := l.natGatewayDao.Get().SetId(id).SetLock(true).Do(ctx)
	if err != nil {
		return nil, nil, err
	}
	natGateway := natResponse.GetObject()
	if refKey(natGateway.GetSpec().GetExternalIp()) != externalIPID || refKey(natGateway.GetSpec().GetVirtualNetwork()) != virtualNetworkID {
		return nil, nil, errors.New("NAT gateway reference changed while acquiring lifecycle lock")
	}
	if l.virtualNetworkDao == nil {
		return nil, nil, errors.New("virtual network DAO is not configured")
	}
	if _, err = l.virtualNetworkDao.Get().SetId(virtualNetworkID).SetLock(true).Do(ctx); err != nil {
		return nil, nil, err
	}
	return parentResponse.GetObject(), natGateway, nil
}

func (l *externalIPLifecycle) lockExternalIPConsumers(ctx context.Context, externalIP *privatev1.ExternalIP) error {
	externalIPID := externalIP.GetId()
	filter := externalIPConsumerFilter(externalIPID, externalIP.GetMetadata().GetName())
	if l.externalIPAttachmentDao != nil {
		response, err := l.externalIPAttachmentDao.List().SetFilter(filter).SetLimit(1000).Do(ctx)
		if err != nil {
			return err
		}
		if response.GetTotal() > 1000 {
			return fmt.Errorf("ExternalIP %s has more than 1000 live consumers", externalIPID)
		}
		ids := make([]string, 0, len(response.GetItems()))
		for _, attachment := range response.GetItems() {
			ids = append(ids, attachment.GetId())
		}
		sort.Strings(ids)
		if len(ids) > 0 {
			if _, err = l.externalIPAttachmentDao.Lock().AddIds(ids...).Do(ctx); err != nil {
				return err
			}
		}
	}
	if l.natGatewayDao != nil {
		response, err := l.natGatewayDao.List().SetFilter(filter).SetLimit(1000).Do(ctx)
		if err != nil {
			return err
		}
		if response.GetTotal() > 1000 {
			return fmt.Errorf("ExternalIP %s has more than 1000 live consumers", externalIPID)
		}
		ids := make([]string, 0, len(response.GetItems()))
		for _, gateway := range response.GetItems() {
			ids = append(ids, gateway.GetId())
		}
		sort.Strings(ids)
		if len(ids) > 0 {
			if _, err = l.natGatewayDao.Lock().AddIds(ids...).Do(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *externalIPLifecycle) ensureExternalIPAvailable(ctx context.Context, externalIPID, externalIPName string) error {
	return l.ensureExternalIPAvailableExcept(ctx, externalIPID, externalIPName, "")
}

func (l *externalIPLifecycle) ensureExternalIPAvailableExcept(
	ctx context.Context,
	externalIPID string,
	externalIPName string,
	exceptAttachmentID string,
) error {
	filter := externalIPConsumerFilter(externalIPID, externalIPName)
	if l.externalIPAttachmentDao != nil {
		response, err := l.externalIPAttachmentDao.List().SetFilter(filter).SetLimit(1000).Do(ctx)
		if err != nil {
			return err
		}
		if response.GetTotal() > 1000 {
			return fmt.Errorf("ExternalIP %s has more than 1000 live consumers", externalIPID)
		}
		for _, attachment := range response.GetItems() {
			if attachment.GetId() != exceptAttachmentID {
				return grpcstatus.Errorf(grpccodes.FailedPrecondition,
					"ExternalIP '%s' is already in use by an ExternalIPAttachment", externalIPID)
			}
		}
	}
	if l.natGatewayDao != nil {
		response, err := l.natGatewayDao.List().SetFilter(filter).SetLimit(1000).Do(ctx)
		if err != nil {
			return err
		}
		if response.GetTotal() > 0 {
			return grpcstatus.Errorf(grpccodes.FailedPrecondition, "ExternalIP '%s' is already in use by a NATGateway", externalIPID)
		}
	}
	return nil
}

func externalIPConsumerFilter(externalIPID, externalIPName string) string {
	keys := []string{externalIPID}
	if externalIPName != "" && externalIPName != externalIPID {
		keys = append(keys, externalIPName)
	}
	conditions := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		quotedKey := strconv.Quote(key)
		conditions = append(conditions,
			"this.spec.external_ip.id == "+quotedKey,
			"this.spec.external_ip.name == "+quotedKey)
	}
	return "(" + strings.Join(conditions, " || ") + ")"
}

func (l *externalIPLifecycle) settleAttachmentParent(
	ctx context.Context,
	parent *privatev1.ExternalIP,
	before, after *privatev1.ExternalIPAttachment,
) error {
	wasReady := before.GetStatus().GetState() == privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_READY
	isReady := after.GetStatus().GetState() == privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_READY
	if wasReady == isReady {
		return nil
	}
	transition := after.GetStatus().GetStateTransitionTime()
	if transition == nil {
		return grpcstatus.Error(grpccodes.InvalidArgument, "settled ExternalIPAttachment transition requires status.state_transition_time")
	}
	if !parent.HasStatus() {
		parent.SetStatus(&privatev1.ExternalIPStatus{})
	}
	if isReady {
		attribution, err := buildExternalIPAttribution(after)
		if err != nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument, "invalid ExternalIP attribution: %s", err)
		}
		parent.GetStatus().SetAttached(true)
		parent.GetStatus().SetAttribution(attribution)
		parent.GetStatus().SetAttachmentTransitionTime(proto.Clone(transition).(*timestamppb.Timestamp))
	} else {
		parent.GetStatus().SetAttached(false)
		parent.GetStatus().ClearAttribution()
		parent.GetStatus().SetAttachmentTransitionTime(proto.Clone(transition).(*timestamppb.Timestamp))
	}
	_, err := l.externalIPDao.Update().SetObject(parent).Do(ctx)
	return err
}

func buildExternalIPAttribution(attachment *privatev1.ExternalIPAttachment) (*privatev1.ExternalIPAttribution, error) {
	spec := attachment.GetSpec()
	switch {
	case spec.HasComputeInstance():
		if spec.GetComputeInstance().GetId() == "" {
			return nil, errors.New("compute instance attribution requires a non-empty id")
		}
		if spec.GetTargetEndpoint() != privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_UNSPECIFIED {
			return nil, errors.New("compute instance attribution cannot have a target endpoint")
		}
		return privatev1.ExternalIPAttribution_builder{
			ComputeInstance: proto.Clone(spec.GetComputeInstance()).(*privatev1.ComputeInstanceLocalReference),
		}.Build(), nil
	case spec.HasCluster():
		if spec.GetCluster().GetId() == "" {
			return nil, errors.New("cluster attribution requires a non-empty id")
		}
		endpoint := spec.GetTargetEndpoint()
		if endpoint != privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API && endpoint != privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS {
			return nil, errors.New("cluster attribution requires a valid target endpoint")
		}
		return privatev1.ExternalIPAttribution_builder{
			Cluster:  proto.Clone(spec.GetCluster()).(*privatev1.ClusterLocalReference),
			Endpoint: endpoint,
		}.Build(), nil
	case spec.HasBaremetalInstance():
		if spec.GetBaremetalInstance().GetId() == "" {
			return nil, errors.New("bare metal attribution requires a non-empty id")
		}
		if spec.GetTargetEndpoint() != privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_UNSPECIFIED {
			return nil, errors.New("bare metal attribution cannot have a target endpoint")
		}
		return privatev1.ExternalIPAttribution_builder{
			BaremetalInstance: proto.Clone(spec.GetBaremetalInstance()).(*privatev1.BareMetalInstanceLocalReference),
		}.Build(), nil
	default:
		return nil, errors.New("ExternalIPAttachment has no attribution target")
	}
}

func (l *externalIPLifecycle) deleteAttachment(ctx context.Context, id string) error {
	parent, attachment, err := l.lockAttachment(ctx, id)
	if err != nil {
		return err
	}
	if attachment.GetStatus().GetState() != privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_DELETING {
		before := proto.Clone(attachment).(*privatev1.ExternalIPAttachment)
		after := proto.Clone(attachment).(*privatev1.ExternalIPAttachment)
		if !after.HasStatus() {
			after.SetStatus(&privatev1.ExternalIPAttachmentStatus{})
		}
		after.GetStatus().SetState(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_DELETING)
		after.GetStatus().SetStateTransitionTime(timestamppb.Now())
		if _, err = l.externalIPAttachmentDao.Update().SetObject(after).Do(ctx); err != nil {
			return err
		}
		if err = l.settleAttachmentParent(ctx, parent, before, after); err != nil {
			return err
		}
	}
	_, err = l.externalIPAttachmentDao.Delete().SetId(id).Do(ctx)
	return err
}

func (l *externalIPLifecycle) deleteAttachmentAndExternalIP(ctx context.Context, attachmentID, externalIPID string) error {
	parent, attachment, err := l.lockAttachment(ctx, attachmentID)
	if err != nil {
		return err
	}
	if refKey(attachment.GetSpec().GetExternalIp()) != externalIPID {
		return errors.New("external IP attachment reference does not match its parent")
	}
	if attachment.GetStatus().GetState() != privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_DELETING {
		before := proto.Clone(attachment).(*privatev1.ExternalIPAttachment)
		after := proto.Clone(attachment).(*privatev1.ExternalIPAttachment)
		if !after.HasStatus() {
			after.SetStatus(&privatev1.ExternalIPAttachmentStatus{})
		}
		after.GetStatus().SetState(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_DELETING)
		after.GetStatus().SetStateTransitionTime(timestamppb.Now())
		if _, err = l.externalIPAttachmentDao.Update().SetObject(after).Do(ctx); err != nil {
			return err
		}
		if err = l.settleAttachmentParent(ctx, parent, before, after); err != nil {
			return err
		}
	}
	if _, err = l.externalIPAttachmentDao.Delete().SetId(attachmentID).Do(ctx); err != nil {
		return err
	}
	if err = l.lockExternalIPConsumers(ctx, parent); err != nil {
		return err
	}
	if err = l.ensureExternalIPAvailableExcept(
		ctx, parent.GetId(), parent.GetMetadata().GetName(), attachmentID,
	); err != nil {
		return err
	}
	return l.deleteLockedExternalIP(ctx, parent)
}

func (l *externalIPLifecycle) deleteAutoCreatedExternalIPs(ctx context.Context, ownerID string) error {
	filter := autoCreatedExternalIPFilter(ownerID)
	eipList, err := l.externalIPDao.List().SetFilter(filter).Do(ctx)
	if err != nil {
		return fmt.Errorf("failed to list ExternalIPs: %w", err)
	}
	for _, externalIP := range eipList.GetItems() {
		// Only the workload servers create automatic ExternalIPs with the system
		// creator. Ignore tenant-created objects that copy the owner labels.
		if externalIP.GetMetadata().GetCreator() != systemCreator {
			continue
		}
		// The paired auto-created attachment path may already have started deleting this EIP.
		// Its finalizer keeps it visible until the operator completes cleanup.
		if externalIP.GetMetadata().HasDeletionTimestamp() {
			continue
		}
		if err := l.deleteExternalIP(ctx, externalIP.GetId()); err != nil {
			return err
		}
	}
	return nil
}

func autoCreatedExternalIPFilter(ownerID string) string {
	return fmt.Sprintf(
		"(this.metadata.labels['%s'] == 'true' && this.metadata.labels['%s'] == %s) || "+
			"(this.metadata.labels['%s'] == 'true' && this.metadata.labels['%s'] == %s)",
		autoCreatedLabel, autoCreatedForLabel, strconv.Quote(ownerID),
		autoProvisionedLabel, autoProvisionedForLabel, strconv.Quote(ownerID),
	)
}

func autoCreatedExternalIPAttachmentFilter(ownerID string) string {
	return fmt.Sprintf(
		"(this.metadata.labels['%s'] == 'true' && this.metadata.labels['%s'] == %s) || "+
			"(this.metadata.labels['%s'] == 'true' && this.metadata.labels['%s'] == %s)",
		autoCreatedLabel, autoCreatedForLabel, strconv.Quote(ownerID),
		autoProvisionedLabel, autoProvisionedForLabel, strconv.Quote(ownerID),
	)
}

func (l *externalIPLifecycle) rejectManualAttachmentDelete(
	ctx context.Context,
	targetKind string,
	targetID string,
	targetMetadata *privatev1.Metadata,
) error {
	return rejectDeleteIfReferenced(ctx, nil, l.externalIPAttachmentDao, "ExternalIPAttachment", targetKind, targetID,
		targetMetadata, func(attachment *privatev1.ExternalIPAttachment) bool {
			spec := attachment.GetSpec()
			var referenced bool
			switch targetKind {
			case "ComputeInstance":
				referenced = referenceMatches(spec.GetComputeInstance(), targetID, targetMetadata)
			case "Cluster":
				referenced = referenceMatches(spec.GetCluster(), targetID, targetMetadata)
			case "BareMetalInstance":
				referenced = referenceMatches(spec.GetBaremetalInstance(), targetID, targetMetadata)
			}
			if !referenced {
				return false
			}
			metadata := attachment.GetMetadata()
			labels := metadata.GetLabels()
			isCurrent := labels[autoCreatedLabel] == "true" && labels[autoCreatedForLabel] == targetID
			isLegacy := labels[autoProvisionedLabel] == "true" && labels[autoProvisionedForLabel] == targetID
			return metadata.GetCreator() != systemCreator || (!isCurrent && !isLegacy)
		})
}

func (l *externalIPLifecycle) deleteExternalIP(ctx context.Context, id string) error {
	parentResponse, err := l.externalIPDao.Get().SetId(id).SetLock(true).Do(ctx)
	if err != nil {
		return err
	}
	if err = l.lockExternalIPConsumers(ctx, parentResponse.GetObject()); err != nil {
		return err
	}
	externalIP := parentResponse.GetObject()
	if err = l.ensureExternalIPAvailable(ctx, id, externalIP.GetMetadata().GetName()); err != nil {
		return err
	}
	return l.deleteLockedExternalIP(ctx, externalIP)
}

func (l *externalIPLifecycle) deleteLockedExternalIP(ctx context.Context, externalIP *privatev1.ExternalIP) error {
	firstDeletionBoundary := !externalIP.HasMetadata() || !externalIP.GetMetadata().HasDeletionTimestamp()
	if _, err := l.externalIPDao.Delete().SetId(externalIP.GetId()).Do(ctx); err != nil {
		return err
	}
	if !firstDeletionBoundary {
		return nil
	}
	poolID := refKey(externalIP.GetSpec().GetPool())
	if poolID == "" {
		return nil
	}
	return UpdatePoolCapacity(ctx, l.externalIPPoolDao, poolID, -1)
}

func (l *externalIPLifecycle) deleteLockedNATGateway(ctx context.Context, natGateway *privatev1.NATGateway) error {
	_, err := l.natGatewayDao.Delete().SetId(natGateway.GetId()).Do(ctx)
	return err
}
