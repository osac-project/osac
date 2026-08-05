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

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
	"github.com/osac-project/fulfillment-service/internal/events"
)

type PrivateExternalIPAttachmentsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
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

func (b *PrivateExternalIPAttachmentsServerBuilder) Build() (*PrivateExternalIPAttachmentsServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.tenancyLogic == nil {
		return nil, errors.New("tenancy logic is mandatory")
	}

	externalIPDao, err := dao.NewGenericDAO[*privatev1.ExternalIP]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
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

	externalIPAttachmentDao, err := dao.NewGenericDAO[*privatev1.ExternalIPAttachment]().
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

	err = s.validateExternalIPAttachment(attachment)
	if err != nil {
		return
	}

	spec := attachment.GetSpec()
	externalIPID := spec.GetExternalIp()

	err = s.validateExternalIPReference(ctx, externalIPID)
	if err != nil {
		return
	}

	err = s.validateTargetReference(ctx, spec)
	if err != nil {
		return
	}

	targetID := s.getTargetID(spec)
	err = s.validateUniqueness(ctx, externalIPID, targetID)
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

	err = s.updateExternalIPAttachedFlag(ctx, externalIPID, true)
	if err != nil {
		return
	}

	return
}

func (s *PrivateExternalIPAttachmentsServer) Update(ctx context.Context,
	request *privatev1.ExternalIPAttachmentsUpdateRequest) (response *privatev1.ExternalIPAttachmentsUpdateResponse, err error) {
	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	mask := request.GetUpdateMask()
	if updateIncludesField(mask,
		"spec.external_ip", "spec.compute_instance", "spec.cluster",
		"spec.baremetal_instance", "spec.target_endpoint") {
		getRequest := &privatev1.ExternalIPAttachmentsGetRequest{}
		getRequest.SetId(id)
		var getResponse *privatev1.ExternalIPAttachmentsGetResponse
		err = s.generic.Get(ctx, getRequest, &getResponse)
		if err != nil {
			return
		}
		err = validateImmutableFieldsExternalIPAttachment(request.GetObject(), getResponse.GetObject())
		if err != nil {
			return
		}
	}

	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateExternalIPAttachmentsServer) Delete(ctx context.Context,
	request *privatev1.ExternalIPAttachmentsDeleteRequest) (response *privatev1.ExternalIPAttachmentsDeleteResponse, err error) {
	id := request.GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	getRequest := &privatev1.ExternalIPAttachmentsGetRequest{}
	getRequest.SetId(id)
	var getResponse *privatev1.ExternalIPAttachmentsGetResponse
	err = s.generic.Get(ctx, getRequest, &getResponse)
	if err != nil {
		return
	}

	externalIPID := getResponse.GetObject().GetSpec().GetExternalIp()

	err = s.generic.Delete(ctx, request, &response)
	if err != nil {
		return
	}

	if externalIPID != "" {
		err = s.updateExternalIPAttachedFlag(ctx, externalIPID, false)
		if err != nil {
			return
		}
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
	if spec.GetExternalIp() == "" {
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

	if newExternalIP := newSpec.GetExternalIp(); newExternalIP != existingSpec.GetExternalIp() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.external_ip' is immutable and cannot be changed from '%s' to '%s'",
			existingSpec.GetExternalIp(), newExternalIP)
	}

	if newSpec.GetComputeInstance() != existingSpec.GetComputeInstance() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.compute_instance' is immutable and cannot be changed from '%s' to '%s'",
			existingSpec.GetComputeInstance(), newSpec.GetComputeInstance())
	}

	if newSpec.GetCluster() != existingSpec.GetCluster() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.cluster' is immutable and cannot be changed from '%s' to '%s'",
			existingSpec.GetCluster(), newSpec.GetCluster())
	}

	if newSpec.GetBaremetalInstance() != existingSpec.GetBaremetalInstance() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.baremetal_instance' is immutable and cannot be changed from '%s' to '%s'",
			existingSpec.GetBaremetalInstance(), newSpec.GetBaremetalInstance())
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

	if externalIP.GetStatus().GetAttached() {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"ExternalIP '%s' is already attached", externalIPID)
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
	ctx context.Context, computeInstanceID string) error {
	_, err := s.computeInstanceDao.Get().
		SetId(computeInstanceID).
		SetLock(true).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"ComputeInstance '%s' does not exist", computeInstanceID)
		}
		s.logger.ErrorContext(ctx, "Failed to query ComputeInstance",
			slog.String("compute_instance_id", computeInstanceID),
			slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to validate compute_instance")
	}
	return nil
}

func (s *PrivateExternalIPAttachmentsServer) validateClusterReference(
	ctx context.Context, clusterID string) error {
	_, err := s.clusterDao.Get().
		SetId(clusterID).
		SetLock(true).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"Cluster '%s' does not exist", clusterID)
		}
		s.logger.ErrorContext(ctx, "Failed to query Cluster",
			slog.String("cluster_id", clusterID),
			slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to validate cluster")
	}
	return nil
}

func (s *PrivateExternalIPAttachmentsServer) validateBareMetalInstanceReference(
	ctx context.Context, bareMetalInstanceID string) error {
	_, err := s.bareMetalInstanceDao.Get().
		SetId(bareMetalInstanceID).
		SetLock(true).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"BareMetalInstance '%s' does not exist", bareMetalInstanceID)
		}
		s.logger.ErrorContext(ctx, "Failed to query BareMetalInstance",
			slog.String("baremetal_instance_id", bareMetalInstanceID),
			slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to validate baremetal_instance")
	}
	return nil
}

func (s *PrivateExternalIPAttachmentsServer) getTargetID(
	spec *privatev1.ExternalIPAttachmentSpec) string {
	switch {
	case spec.HasComputeInstance():
		return spec.GetComputeInstance()
	case spec.HasCluster():
		return spec.GetCluster()
	case spec.HasBaremetalInstance():
		return spec.GetBaremetalInstance()
	default:
		return ""
	}
}

func (s *PrivateExternalIPAttachmentsServer) validateUniqueness(
	ctx context.Context, externalIPID string, targetID string) error {
	eipFilter := fmt.Sprintf("this.spec.external_ip == %q", externalIPID)
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

	return nil
}

func (s *PrivateExternalIPAttachmentsServer) updateExternalIPAttachedFlag(
	ctx context.Context, externalIPID string, attached bool) error {
	getResponse, err := s.externalIPDao.Get().
		SetId(externalIPID).
		SetLock(true).
		Do(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to get ExternalIP for attached flag update",
			slog.String("external_ip_id", externalIPID),
			slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to update ExternalIP attached status")
	}

	externalIP := getResponse.GetObject()
	externalIP.GetStatus().SetAttached(attached)

	_, err = s.externalIPDao.Update().
		SetObject(externalIP).
		Do(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to update ExternalIP attached flag",
			slog.String("external_ip_id", externalIPID),
			slog.Bool("attached", attached),
			slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to update ExternalIP attached status")
	}

	return nil
}
