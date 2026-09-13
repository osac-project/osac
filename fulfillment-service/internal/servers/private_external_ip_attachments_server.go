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
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateExternalIPAttachmentsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.ExternalIPAttachmentsServer = (*PrivateExternalIPAttachmentsServer)(nil)

type PrivateExternalIPAttachmentsServer struct {
	privatev1.UnimplementedExternalIPAttachmentsServer

	logger                  *slog.Logger
	generic                 *GenericServer[*privatev1.ExternalIPAttachment]
	externalIPDao           *dao.GenericDAO[*privatev1.ExternalIP]
	computeInstanceDao      *dao.GenericDAO[*privatev1.ComputeInstance]
	clusterDao              *dao.GenericDAO[*privatev1.Cluster]
	bareMetalInstanceDao    *dao.GenericDAO[*privatev1.BareMetalInstance]
	externalIPAttachmentDao *dao.GenericDAO[*privatev1.ExternalIPAttachment]
	lifecycle               *externalIPLifecycle
}

func NewPrivateExternalIPAttachmentsServer() *PrivateExternalIPAttachmentsServerBuilder {
	return &PrivateExternalIPAttachmentsServerBuilder{}
}

func (b *PrivateExternalIPAttachmentsServerBuilder) SetLogger(value *slog.Logger) *PrivateExternalIPAttachmentsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateExternalIPAttachmentsServerBuilder) SetNotifier(value events.Notifier) *PrivateExternalIPAttachmentsServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateExternalIPAttachmentsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateExternalIPAttachmentsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateExternalIPAttachmentsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateExternalIPAttachmentsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateExternalIPAttachmentsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateExternalIPAttachmentsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateExternalIPAttachmentsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateExternalIPAttachmentsServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateExternalIPAttachmentsServerBuilder) Build() (*PrivateExternalIPAttachmentsServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.tenancyLogic == nil {
		return nil, errors.New("tenancy logic is mandatory")
	}

	externalIPDaoBuilder := dao.NewGenericDAO[*privatev1.ExternalIP]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer)
	addDAOEventCallback(externalIPDaoBuilder, b.notifier)
	externalIPDao, err := externalIPDaoBuilder.Build()
	if err != nil {
		return nil, err
	}

	computeInstanceDao, err := dao.NewGenericDAO[*privatev1.ComputeInstance]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return nil, err
	}

	clusterDao, err := dao.NewGenericDAO[*privatev1.Cluster]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return nil, err
	}

	bareMetalInstanceDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstance]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return nil, err
	}

	externalIPAttachmentDaoBuilder := dao.NewGenericDAO[*privatev1.ExternalIPAttachment]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer)
	addDAOEventCallback(externalIPAttachmentDaoBuilder, b.notifier)
	externalIPAttachmentDao, err := externalIPAttachmentDaoBuilder.Build()
	if err != nil {
		return nil, err
	}
	natGatewayDao, err := dao.NewGenericDAO[*privatev1.NATGateway]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return nil, err
	}

	generic, err := NewGenericServer[*privatev1.ExternalIPAttachment]().
		SetLogger(b.logger).
		SetService(privatev1.ExternalIPAttachments_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return nil, err
	}

	result := &PrivateExternalIPAttachmentsServer{
		logger:                  b.logger,
		generic:                 generic,
		externalIPDao:           externalIPDao,
		computeInstanceDao:      computeInstanceDao,
		clusterDao:              clusterDao,
		bareMetalInstanceDao:    bareMetalInstanceDao,
		externalIPAttachmentDao: externalIPAttachmentDao,
	}
	result.lifecycle = newExternalIPLifecycle(
		externalIPDao,
		externalIPAttachmentDao,
		natGatewayDao,
		nil,
		computeInstanceDao,
		clusterDao,
		bareMetalInstanceDao,
		nil,
	)
	return result, nil
}

func (s *PrivateExternalIPAttachmentsServer) List(ctx context.Context,
	request *privatev1.ExternalIPAttachmentsListRequest) (response *privatev1.ExternalIPAttachmentsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateExternalIPAttachmentsServer) Get(ctx context.Context,
	request *privatev1.ExternalIPAttachmentsGetRequest) (response *privatev1.ExternalIPAttachmentsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateExternalIPAttachmentsServer) Create(ctx context.Context,
	request *privatev1.ExternalIPAttachmentsCreateRequest) (response *privatev1.ExternalIPAttachmentsCreateResponse, err error) {
	attachment := request.GetObject()
	if err = rejectOutputStatusOnCreate(attachment != nil && attachment.HasStatus()); err != nil {
		return
	}

	err = s.validateExternalIPAttachment(attachment)
	if err != nil {
		return
	}

	spec := attachment.GetSpec()
	externalIPRef := spec.GetExternalIp()
	externalIPKey := refKey(externalIPRef)

	err = s.validateExternalIPReference(ctx, externalIPKey)
	if err != nil {
		return
	}

	err = s.validateTargetReference(ctx, spec)
	if err != nil {
		return
	}

	targetID := s.getTargetID(spec)
	err = s.validateUniqueness(ctx, externalIPKey, targetID)
	if err != nil {
		return
	}

	if attachment.GetStatus() == nil {
		attachment.SetStatus(privatev1.ExternalIPAttachmentStatus_builder{
			State: privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING,
		}.Build())
	} else {
		attachment.GetStatus().SetState(
			privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING)
	}

	err = s.generic.Create(ctx, request, &response)
	if err != nil {
		return
	}

	return
}

func (s *PrivateExternalIPAttachmentsServer) Update(ctx context.Context,
	request *privatev1.ExternalIPAttachmentsUpdateRequest) (response *privatev1.ExternalIPAttachmentsUpdateResponse, err error) {
	tx, txErr := database.TxFromContext(ctx)
	if txErr != nil {
		return nil, txErr
	}
	defer tx.ReportError(&err)

	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	mask := request.GetUpdateMask()
	if err = validatePrivateLifecycleUpdateMask(mask,
		[]string{"status.state", "status.external_ip_address", "status.message", "status.hub", "status.state_transition_time"},
		nil); err != nil {
		return
	}

	parent, existingAttachment, lockErr := s.lifecycle.lockAttachment(ctx, id)
	if lockErr != nil {
		err = translateLifecycleError(lockErr)
		return
	}
	if updateIncludesField(mask,
		"spec.external_ip", "spec.compute_instance", "spec.cluster",
		"spec.baremetal_instance", "spec.target_endpoint") {
		err = validateImmutableFieldsExternalIPAttachment(request.GetObject(), existingAttachment)
		if err != nil {
			return
		}
	}
	if updateIncludesField(mask, "status.state") {
		oldState := existingAttachment.GetStatus().GetState()
		newState := request.GetObject().GetStatus().GetState()
		if oldState != newState && !containsAttachmentState(validExternalIPAttachmentTransitions[oldState], newState) {
			err = grpcstatus.Errorf(grpccodes.FailedPrecondition,
				"invalid ExternalIPAttachment state transition from %s to %s", oldState, newState)
			return
		}
	}

	err = s.generic.Update(ctx, request, &response)
	if err != nil {
		return
	}
	err = translateLifecycleError(s.lifecycle.settleAttachmentParent(ctx, parent, existingAttachment, response.GetObject()))
	return
}

func (s *PrivateExternalIPAttachmentsServer) Delete(ctx context.Context,
	request *privatev1.ExternalIPAttachmentsDeleteRequest) (response *privatev1.ExternalIPAttachmentsDeleteResponse, err error) {
	tx, txErr := database.TxFromContext(ctx)
	if txErr != nil {
		return nil, txErr
	}
	defer tx.ReportError(&err)

	id := request.GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	err = translateLifecycleError(s.lifecycle.deleteAttachment(ctx, id))
	if err == nil {
		response = &privatev1.ExternalIPAttachmentsDeleteResponse{}
	}
	return
}

func (s *PrivateExternalIPAttachmentsServer) Signal(ctx context.Context,
	request *privatev1.ExternalIPAttachmentsSignalRequest) (response *privatev1.ExternalIPAttachmentsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

func (s *PrivateExternalIPAttachmentsServer) validateExternalIPAttachment(
	attachment *privatev1.ExternalIPAttachment) error {
	if attachment == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "external IP attachment is mandatory")
	}
	spec := attachment.GetSpec()
	if spec == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "external IP attachment spec is mandatory")
	}
	if spec.GetExternalIp() == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.external_ip' is required")
	}
	if !spec.HasComputeInstance() && !spec.HasCluster() && !spec.HasBaremetalInstance() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"exactly one target must be set (compute_instance, cluster, or baremetal_instance)")
	}

	targetCount := 0
	if spec.HasComputeInstance() {
		targetCount++
	}
	if spec.HasCluster() {
		targetCount++
	}
	if spec.HasBaremetalInstance() {
		targetCount++
	}
	if targetCount > 1 {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"exactly one target must be set (compute_instance, cluster, or baremetal_instance)")
	}

	if spec.HasCluster() {
		if spec.GetTargetEndpoint() == privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_UNSPECIFIED {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"field 'spec.target_endpoint' is required when target is cluster")
		}
	} else {
		if spec.GetTargetEndpoint() != privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_UNSPECIFIED {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"field 'spec.target_endpoint' must be UNSPECIFIED for non-cluster targets")
		}
	}

	return nil
}

func validateImmutableFieldsExternalIPAttachment(
	newAttachment, existingAttachment *privatev1.ExternalIPAttachment) error {
	newSpec := newAttachment.GetSpec()
	existingSpec := existingAttachment.GetSpec()

	if refKey(newSpec.GetExternalIp()) != refKey(existingSpec.GetExternalIp()) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.external_ip' is immutable and cannot be changed from '%s' to '%s'",
			refKey(existingSpec.GetExternalIp()), refKey(newSpec.GetExternalIp()))
	}

	if refKey(newSpec.GetComputeInstance()) != refKey(existingSpec.GetComputeInstance()) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.compute_instance' is immutable and cannot be changed from '%s' to '%s'",
			refKey(existingSpec.GetComputeInstance()), refKey(newSpec.GetComputeInstance()))
	}

	if refKey(newSpec.GetCluster()) != refKey(existingSpec.GetCluster()) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.cluster' is immutable and cannot be changed from '%s' to '%s'",
			refKey(existingSpec.GetCluster()), refKey(newSpec.GetCluster()))
	}

	if refKey(newSpec.GetBaremetalInstance()) != refKey(existingSpec.GetBaremetalInstance()) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.baremetal_instance' is immutable and cannot be changed from '%s' to '%s'",
			refKey(existingSpec.GetBaremetalInstance()), refKey(newSpec.GetBaremetalInstance()))
	}

	if newSpec.GetTargetEndpoint() != existingSpec.GetTargetEndpoint() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.target_endpoint' is immutable and cannot be changed from '%s' to '%s'",
			existingSpec.GetTargetEndpoint().String(), newSpec.GetTargetEndpoint().String())
	}

	return nil
}

func (s *PrivateExternalIPAttachmentsServer) validateExternalIPReference(
	ctx context.Context, externalIPID string) error {
	getResponse, err := s.externalIPDao.Get().
		SetId(externalIPID).
		SetLock(true).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"ExternalIP '%s' does not exist", externalIPID)
		}
		s.logger.ErrorContext(ctx, "Failed to query ExternalIP",
			slog.String("external_ip_id", externalIPID),
			slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to validate external_ip")
	}

	externalIP := getResponse.GetObject()

	if externalIP.GetStatus().GetState() != privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"ExternalIP '%s' is not in ALLOCATED state (current state: %s)",
			externalIPID, externalIP.GetStatus().GetState().String())
	}

	return nil
}

func (s *PrivateExternalIPAttachmentsServer) validateTargetReference(
	ctx context.Context, spec *privatev1.ExternalIPAttachmentSpec) error {
	switch {
	case spec.HasComputeInstance():
		return s.validateComputeInstanceReference(ctx, spec.GetComputeInstance())
	case spec.HasCluster():
		return s.validateClusterReference(ctx, spec.GetCluster())
	case spec.HasBaremetalInstance():
		return s.validateBareMetalInstanceReference(ctx, spec.GetBaremetalInstance())
	default:
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"exactly one target must be set (compute_instance, cluster, or baremetal_instance)")
	}
}

func (s *PrivateExternalIPAttachmentsServer) validateComputeInstanceReference(
	ctx context.Context, ref *privatev1.ComputeInstanceLocalReference) error {
	key := refKey(ref)
	_, err := s.computeInstanceDao.Get().
		SetId(key).
		SetLock(true).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"ComputeInstance '%s' does not exist", key)
		}
		s.logger.ErrorContext(ctx, "Failed to query ComputeInstance",
			slog.String("compute_instance_id", key),
			slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to validate compute_instance")
	}
	return nil
}

func (s *PrivateExternalIPAttachmentsServer) validateClusterReference(
	ctx context.Context, ref *privatev1.ClusterLocalReference) error {
	key := refKey(ref)
	_, err := s.clusterDao.Get().
		SetId(key).
		SetLock(true).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"Cluster '%s' does not exist", key)
		}
		s.logger.ErrorContext(ctx, "Failed to query Cluster",
			slog.String("cluster_id", key),
			slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to validate cluster")
	}
	return nil
}

func (s *PrivateExternalIPAttachmentsServer) validateBareMetalInstanceReference(
	ctx context.Context, ref *privatev1.BareMetalInstanceLocalReference) error {
	key := refKey(ref)
	_, err := s.bareMetalInstanceDao.Get().
		SetId(key).
		SetLock(true).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"BareMetalInstance '%s' does not exist", key)
		}
		s.logger.ErrorContext(ctx, "Failed to query BareMetalInstance",
			slog.String("baremetal_instance_id", key),
			slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to validate baremetal_instance")
	}
	return nil
}

func (s *PrivateExternalIPAttachmentsServer) getTargetID(
	spec *privatev1.ExternalIPAttachmentSpec) string {
	switch {
	case spec.HasComputeInstance():
		return refKey(spec.GetComputeInstance())
	case spec.HasCluster():
		return refKey(spec.GetCluster())
	case spec.HasBaremetalInstance():
		return refKey(spec.GetBaremetalInstance())
	default:
		return ""
	}
}

func (s *PrivateExternalIPAttachmentsServer) validateUniqueness(
	ctx context.Context, externalIPID string, targetID string) error {
	eipFilter := fmt.Sprintf("(this.spec.external_ip.id == %[1]q || this.spec.external_ip.name == %[1]q) && !has(this.metadata.deletion_timestamp)", externalIPID)
	eipResp, err := s.externalIPAttachmentDao.List().
		SetFilter(eipFilter).
		SetLimit(1).
		Do(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to list attachments for uniqueness check",
			slog.String("external_ip_id", externalIPID),
			slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to validate uniqueness")
	}
	if eipResp.GetTotal() > 0 {
		return grpcstatus.Errorf(grpccodes.AlreadyExists,
			"an ExternalIPAttachment already exists for ExternalIP '%s'", externalIPID)
	}
	if err := s.lifecycle.ensureExternalIPAvailable(ctx, externalIPID); err != nil {
		return err
	}

	return nil
}

func containsAttachmentState(states []privatev1.ExternalIPAttachmentState, state privatev1.ExternalIPAttachmentState) bool {
	for _, candidate := range states {
		if candidate == state {
			return true
		}
	}
	return false
}
