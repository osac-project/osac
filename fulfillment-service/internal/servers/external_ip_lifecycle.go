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
	"sort"
	"strconv"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
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

func addDAOEventCallback[O dao.Object](builder *dao.GenericDAOBuilder[O], notifier events.Notifier) {
	if notifier != nil {
		builder.AddEventCallback(makeNotifyCallback[O](notifier))
	}
}

func validatePublicUpdateMask(mask *fieldmaskpb.FieldMask) error {
	if mask == nil || len(mask.GetPaths()) == 0 {
		return grpcInvalidArgument("update_mask must explicitly name metadata or spec fields")
	}
	for _, path := range mask.GetPaths() {
		if path == "status" || maskPathHasPrefix(path, "status") {
			return grpcInvalidArgument("status output fields cannot be updated")
		}
		if path != "metadata" && path != "spec" && !maskPathHasPrefix(path, "metadata") && !maskPathHasPrefix(path, "spec") {
			return grpcInvalidArgument("update_mask paths must name metadata or spec fields")
		}
	}
	return nil
}

func validatePublicMetadataUpdateMask(mask *fieldmaskpb.FieldMask) error {
	if err := validatePublicUpdateMask(mask); err != nil {
		return err
	}
	for _, path := range mask.GetPaths() {
		if path != "metadata" && !maskPathHasPrefix(path, "metadata") {
			return grpcInvalidArgument("public lifecycle updates may only name metadata fields")
		}
	}
	return nil
}

func validatePrivateLifecycleUpdateMask(mask *fieldmaskpb.FieldMask, allowedStatusPaths, rejectedStatusPaths []string) error {
	if mask == nil || len(mask.GetPaths()) == 0 {
		return grpcInvalidArgument("update_mask is mandatory for private lifecycle updates")
	}
	for _, path := range mask.GetPaths() {
		if path == "metadata" || path == "spec" || maskPathHasPrefix(path, "metadata") || maskPathHasPrefix(path, "spec") {
			continue
		}
		if path == "status" || maskPathHasPrefix(path, "status") {
			if containsString(rejectedStatusPaths, path) || !containsString(allowedStatusPaths, path) {
				return grpcInvalidArgument("status output field cannot be updated through this path")
			}
			continue
		}
		return grpcInvalidArgument("update_mask path is not allowed for this private lifecycle update")
	}
	return nil
}

func maskPathHasPrefix(path, prefix string) bool {
	return len(path) > len(prefix) && path[:len(prefix)] == prefix && path[len(prefix)] == '.'
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func grpcInvalidArgument(message string) error {
	return grpcstatus.Error(grpccodes.InvalidArgument, message)
}

func rejectOutputStatusOnCreate(hasStatus bool) error {
	if hasStatus {
		return grpcInvalidArgument("status output fields cannot be set on create")
	}
	return nil
}

func translateLifecycleError(err error) error {
	var notFoundErr *dao.ErrNotFound
	if errors.As(err, &notFoundErr) {
		return grpcstatus.Errorf(grpccodes.NotFound, "object not found")
	}
	var deadlockErr *dao.ErrDeadlock
	if errors.As(err, &deadlockErr) {
		return grpcstatus.Errorf(grpccodes.Aborted, "concurrent modification detected, please retry")
	}
	var inUseErr *dao.ErrInUse
	if errors.As(err, &inUseErr) {
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

func (l *externalIPLifecycle) lockNewAttachmentReferences(ctx context.Context, externalIPID string, targetID string, targetDAO *dao.GenericDAO[*privatev1.ComputeInstance]) error {
	if _, err := l.externalIPDao.Get().SetId(externalIPID).SetLock(true).Do(ctx); err != nil {
		return err
	}
	if targetDAO == nil {
		return errors.New("attachment target DAO is not configured")
	}
	_, err := targetDAO.Get().SetId(targetID).SetLock(true).Do(ctx)
	return err
}

func (l *externalIPLifecycle) lockNewClusterAttachmentReferences(ctx context.Context, externalIPID, targetID string) error {
	if _, err := l.externalIPDao.Get().SetId(externalIPID).SetLock(true).Do(ctx); err != nil {
		return err
	}
	if l.clusterDao == nil {
		return errors.New("cluster DAO is not configured")
	}
	_, err := l.clusterDao.Get().SetId(targetID).SetLock(true).Do(ctx)
	return err
}

func (l *externalIPLifecycle) lockNewBareMetalAttachmentReferences(ctx context.Context, externalIPID, targetID string) error {
	if _, err := l.externalIPDao.Get().SetId(externalIPID).SetLock(true).Do(ctx); err != nil {
		return err
	}
	if l.bareMetalInstanceDao == nil {
		return errors.New("bare metal instance DAO is not configured")
	}
	_, err := l.bareMetalInstanceDao.Get().SetId(targetID).SetLock(true).Do(ctx)
	return err
}

func (l *externalIPLifecycle) lockNewNATGatewayReferences(ctx context.Context, externalIPID, virtualNetworkID string) error {
	if _, err := l.externalIPDao.Get().SetId(externalIPID).SetLock(true).Do(ctx); err != nil {
		return err
	}
	if l.virtualNetworkDao == nil {
		return errors.New("virtual network DAO is not configured")
	}
	_, err := l.virtualNetworkDao.Get().SetId(virtualNetworkID).SetLock(true).Do(ctx)
	return err
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

func (l *externalIPLifecycle) lockExternalIPConsumers(ctx context.Context, externalIPID string) error {
	if l.externalIPAttachmentDao != nil {
		response, err := l.externalIPAttachmentDao.List().SetFilter(fmt.Sprintf(
			"(this.spec.external_ip.id == %s || this.spec.external_ip.name == %s) && !has(this.metadata.deletion_timestamp)",
			strconv.Quote(externalIPID), strconv.Quote(externalIPID))).SetLimit(1000).Do(ctx)
		if err != nil {
			return err
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
			for _, id := range ids {
				attachmentResponse, getErr := l.externalIPAttachmentDao.Get().SetId(id).SetLock(true).Do(ctx)
				if getErr != nil {
					return getErr
				}
				if err = l.lockAttachmentTarget(ctx, attachmentResponse.GetObject()); err != nil {
					return err
				}
			}
		}
	}
	if l.natGatewayDao != nil {
		response, err := l.natGatewayDao.List().SetFilter(fmt.Sprintf(
			"(this.spec.external_ip.id == %s || this.spec.external_ip.name == %s) && !has(this.metadata.deletion_timestamp)",
			strconv.Quote(externalIPID), strconv.Quote(externalIPID))).SetLimit(1000).Do(ctx)
		if err != nil {
			return err
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
			for _, id := range ids {
				gatewayResponse, getErr := l.natGatewayDao.Get().SetId(id).SetLock(true).Do(ctx)
				if getErr != nil {
					return getErr
				}
				gateway := gatewayResponse.GetObject()
				if l.virtualNetworkDao == nil {
					return errors.New("virtual network DAO is not configured")
				}
				if _, getErr = l.virtualNetworkDao.Get().SetId(refKey(gateway.GetSpec().GetVirtualNetwork())).SetLock(true).Do(ctx); getErr != nil {
					return getErr
				}
			}
		}
	}
	return nil
}

func (l *externalIPLifecycle) ensureExternalIPAvailable(ctx context.Context, externalIPID string) error {
	filter := fmt.Sprintf(
		"(this.spec.external_ip.id == %s || this.spec.external_ip.name == %s) && !has(this.metadata.deletion_timestamp)",
		strconv.Quote(externalIPID), strconv.Quote(externalIPID))
	if l.externalIPAttachmentDao != nil {
		response, err := l.externalIPAttachmentDao.List().SetFilter(filter).SetLimit(1).Do(ctx)
		if err != nil {
			return err
		}
		if response.GetTotal() > 0 {
			return grpcstatus.Errorf(grpccodes.FailedPrecondition, "ExternalIP '%s' is already in use by an ExternalIPAttachment", externalIPID)
		}
	}
	if l.natGatewayDao != nil {
		response, err := l.natGatewayDao.List().SetFilter(filter).SetLimit(1).Do(ctx)
		if err != nil {
			return err
		}
		if response.GetTotal() > 0 {
			return grpcstatus.Errorf(grpccodes.FailedPrecondition, "ExternalIP '%s' is already in use by a NATGateway", externalIPID)
		}
	}
	return nil
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
		return grpcInvalidArgument("settled ExternalIPAttachment transition requires status.state_transition_time")
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
	return l.deleteLockedExternalIP(ctx, parent)
}

func (l *externalIPLifecycle) deleteExternalIP(ctx context.Context, id string) error {
	parentResponse, err := l.externalIPDao.Get().SetId(id).SetLock(true).Do(ctx)
	if err != nil {
		return err
	}
	if err = l.lockExternalIPConsumers(ctx, id); err != nil {
		return err
	}
	return l.deleteLockedExternalIP(ctx, parentResponse.GetObject())
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

func (l *externalIPLifecycle) deleteNATGateway(ctx context.Context, id string) error {
	_, natGateway, err := l.lockNATGateway(ctx, id)
	if err != nil {
		return err
	}
	_, err = l.natGatewayDao.Delete().SetId(natGateway.GetId()).Do(ctx)
	return err
}

func (l *externalIPLifecycle) deleteNATGatewayAndExternalIP(ctx context.Context, id string) error {
	parent, natGateway, err := l.lockNATGateway(ctx, id)
	if err != nil {
		return err
	}
	if _, err = l.natGatewayDao.Delete().SetId(natGateway.GetId()).Do(ctx); err != nil {
		return err
	}
	return l.deleteLockedExternalIP(ctx, parent)
}
