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
	"log/slog"
	"slices"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateExternalIPAttachmentsServerBuilder struct {
	logger             *slog.Logger
	attributionLogic   auth.AttributionLogic
	tenancyLogic       auth.TenancyLogic
	metricsRegisterer  prometheus.Registerer
	filterDesc         protoreflect.MessageDescriptor
	networkingSettings *NetworkingSettings
}

var _ privatev1.ExternalIPAttachmentsServer = (*PrivateExternalIPAttachmentsServer)(nil)

type PrivateExternalIPAttachmentsServer struct {
	privatev1.UnimplementedExternalIPAttachmentsServer

	logger                  *slog.Logger
	tenancyLogic            auth.TenancyLogic
	generic                 *GenericServer[*privatev1.ExternalIPAttachment]
	networkClassesDao       *dao.GenericDAO[*privatev1.NetworkClass]
	externalIPDao           *dao.GenericDAO[*privatev1.ExternalIP]
	computeInstanceDao      *dao.GenericDAO[*privatev1.ComputeInstance]
	clusterDao              *dao.GenericDAO[*privatev1.Cluster]
	bareMetalInstanceDao    *dao.GenericDAO[*privatev1.BareMetalInstance]
	externalIPAttachmentDao *dao.GenericDAO[*privatev1.ExternalIPAttachment]
	lifecycle               *externalIPLifecycle
	settings                NetworkingSettings
}

func NewPrivateExternalIPAttachmentsServer() *PrivateExternalIPAttachmentsServerBuilder {
	return &PrivateExternalIPAttachmentsServerBuilder{}
}

func (b *PrivateExternalIPAttachmentsServerBuilder) SetLogger(value *slog.Logger) *PrivateExternalIPAttachmentsServerBuilder {
	b.logger = value
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

// SetNetworkingSettings sets the networking installation settings. This is optional; the default is
// DefaultNetworkingSettings.
func (b *PrivateExternalIPAttachmentsServerBuilder) SetNetworkingSettings(value NetworkingSettings) *PrivateExternalIPAttachmentsServerBuilder {
	b.networkingSettings = &value
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

	networkClassesDao, err := newNetworkClassesDAO(b.logger, b.tenancyLogic, b.metricsRegisterer)
	if err != nil {
		return nil, err
	}

	externalIPAttachmentDaoBuilder := dao.NewGenericDAO[*privatev1.ExternalIPAttachment]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer)
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
		tenancyLogic:            b.tenancyLogic,
		generic:                 generic,
		networkClassesDao:       networkClassesDao,
		externalIPDao:           externalIPDao,
		computeInstanceDao:      computeInstanceDao,
		clusterDao:              clusterDao,
		bareMetalInstanceDao:    bareMetalInstanceDao,
		externalIPAttachmentDao: externalIPAttachmentDao,
		settings:                resolveNetworkingSettings(b.networkingSettings),
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

// automaticExternalIPAttachmentPrefix is the reserved name prefix of automatic ExternalIPAttachments.
const automaticExternalIPAttachmentPrefix = "auto-eipa-"

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
	err = s.generic.CreateWithCandidatePreparation(ctx, request, &response,
		func(ctx context.Context, _, candidate *privatev1.ExternalIPAttachment) error {
			if err := lockTenantForCreate(ctx, candidate.GetMetadata().GetTenant()); err != nil {
				return err
			}
			return s.prepareCreate(ctx, candidate)
		})
	return
}

// prepareCreate checks the gates of an ExternalIPAttachment create, in the order of the design, and sets the initial
// status, including the ExternalIP address. The target is locked before the ExternalIP.
func (s *PrivateExternalIPAttachmentsServer) prepareCreate(ctx context.Context,
	attachment *privatev1.ExternalIPAttachment) error {
	if err := validateReservedCreate(ctx, attachment.GetMetadata(), automaticExternalIPAttachmentPrefix); err != nil {
		return err
	}
	spec := attachment.GetSpec()
	externalIPKey := refKey(spec.GetExternalIp())
	tenant := attachment.GetMetadata().GetTenant()

	// Reject cross-tenant references before checking target readiness. This read is intentionally unlocked; the
	// locked check below revalidates the tenant after taking the locks in the target-before-ExternalIP order.
	if err := s.validateExternalIPTenant(ctx, tenant, externalIPKey); err != nil {
		return err
	}
	if !s.settings.ProvisioningEnabled {
		return grpcstatus.Error(grpccodes.FailedPrecondition,
			"provider networking is disabled; workloads are not ready ExternalIPAttachment endpoints")
	}

	// Lock the target first. Workload deletion follows the same target-before-ExternalIP order.
	targetHubReference, err := s.validateTargetReference(ctx, spec, attachment.GetMetadata())
	if err != nil {
		return err
	}
	externalIP, err := s.lockExternalIP(ctx, externalIPKey, tenant)
	if err != nil {
		return err
	}
	spec.SetExternalIp(privatev1.ExternalIPLocalReference_builder{Id: externalIP.GetId()}.Build())

	// The ExternalIP is not a tenant default, and an automatic one is used only by the system's own create of
	// auto-eipa-<ExternalIP ID>. This runs before the consumer check.
	if externalIP.GetMetadata().GetLabels()[defaultLabel] == "true" {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"ExternalIP '%s' is a tenant default and can't be attached", externalIP.GetId())
	}
	if isAutomatic(externalIP.GetMetadata()) &&
		(!isSystemCreate(ctx, attachment.GetMetadata()) ||
			attachment.GetMetadata().GetName() != automaticExternalIPAttachmentPrefix+externalIP.GetId()) {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"ExternalIP '%s' is automatic; only the system attaches it", externalIP.GetId())
	}
	if err := s.lifecycle.ensureExternalIPAvailable(ctx, externalIP.GetId(),
		externalIP.GetMetadata().GetName()); err != nil {
		return err
	}
	if externalIP.GetStatus().GetState() != privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"ExternalIP '%s' is not in ALLOCATED state (current state: %s)",
			externalIP.GetId(), externalIP.GetStatus().GetState().String())
	}

	canonicalHubID, err := canonicalNetworkingHubID(ctx, s.logger, s.networkClassesDao)
	if err != nil {
		return err
	}
	if err = validateNetworkingHubReferences(canonicalHubID, networkingHubReference{
		resourceType: "ExternalIP",
		id:           externalIP.GetId(),
		hubID:        externalIP.GetStatus().GetHub(),
	}, targetHubReference); err != nil {
		return err
	}

	attachment.SetStatus(privatev1.ExternalIPAttachmentStatus_builder{
		State:             privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING,
		ExternalIpAddress: externalIP.GetStatus().GetAddress(),
	}.Build())
	return nil
}

// lockExternalIP locks the ExternalIP of an attachment create and checks it belongs to the tenant and isn't being
// deleted.
func (s *PrivateExternalIPAttachmentsServer) lockExternalIP(ctx context.Context, key, tenant string) (
	*privatev1.ExternalIP, error) {
	externalIP, err := s.validateExternalIPReference(ctx, key)
	if err != nil {
		return nil, err
	}
	if err = validateTenantMatch(tenant, externalIP, "ExternalIP", key); err != nil {
		return nil, err
	}
	return externalIP, nil
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
		[]string{"status.state", "status.message", "status.hub", "status.state_transition_time"},
		[]string{"status.external_ip_address"}); err != nil {
		return
	}

	parent, existingAttachment, targetMetadata, lockErr := s.lifecycle.lockAttachment(ctx, id)
	if lockErr != nil {
		err = translateLifecycleError(lockErr)
		return
	}
	if updateIncludesField(mask, "status.state") &&
		request.GetObject().GetStatus().GetState() == privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_READY {
		if existingAttachment.GetMetadata().HasDeletionTimestamp() {
			err = grpcstatus.Errorf(grpccodes.FailedPrecondition,
				"ExternalIPAttachment '%s' is being deleted; it cannot become READY", id)
			return
		}
		if targetMetadata != nil && targetMetadata.HasDeletionTimestamp() {
			targetKind, targetID := externalIPAttachmentTarget(existingAttachment)
			err = grpcstatus.Errorf(grpccodes.FailedPrecondition,
				"%s '%s' is being deleted; ExternalIPAttachment cannot become READY", targetKind, targetID)
			return
		}
	}
	if updateIncludesField(mask, "status.state") {
		oldState := existingAttachment.GetStatus().GetState()
		newState := request.GetObject().GetStatus().GetState()
		if oldState != newState && !slices.Contains(validExternalIPAttachmentTransitions[oldState], newState) {
			err = grpcstatus.Errorf(grpccodes.FailedPrecondition,
				"invalid ExternalIPAttachment state transition from %s to %s", oldState, newState)
			return
		}
	}

	err = s.generic.UpdateWithCandidatePreparation(ctx, request, &response,
		func(_ context.Context, current, candidate *privatev1.ExternalIPAttachment) error {
			if err := validateNetworkingStatusOnlyUpdate("ExternalIPAttachment", current, candidate); err != nil {
				return err
			}
			return rejectStateChangeWhileDeleting(current, candidate, current.GetMetadata())
		})
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
	_, attachment, _, lockErr := s.lifecycle.lockAttachment(ctx, id)
	if lockErr != nil {
		err = translateLifecycleError(lockErr)
		return
	}
	if deleteAlreadyAccepted(attachment.GetMetadata()) {
		response = &privatev1.ExternalIPAttachmentsDeleteResponse{}
		return
	}
	if isAutomatic(attachment.GetMetadata()) && !auth.IsControllerServiceAccount(ctx) {
		err = grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"cannot delete automatic ExternalIPAttachment '%s': it is deleted with its workload", id)
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

func (s *PrivateExternalIPAttachmentsServer) validateExternalIPReference(
	ctx context.Context, externalIPID string) (*privatev1.ExternalIP, error) {
	getResponse, err := s.externalIPDao.Get().
		SetId(externalIPID).
		SetLock(true).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
				"ExternalIP '%s' does not exist", externalIPID)
		}
		return nil, ConvertDAOErrorToGRPC(err, "get", externalIPID)
	}

	externalIP := getResponse.GetObject()
	if externalIP.HasMetadata() && externalIP.GetMetadata().HasDeletionTimestamp() {
		return nil, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"ExternalIP '%s' is being deleted", externalIPID)
	}

	if externalIP.GetStatus().GetState() != privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED {
		return nil, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"ExternalIP '%s' is not in ALLOCATED state (current state: %s)",
			externalIPID, externalIP.GetStatus().GetState().String())
	}

	return externalIP, nil
}

func (s *PrivateExternalIPAttachmentsServer) validateExternalIPTenant(
	ctx context.Context, tenant, externalIPID string) error {
	getResponse, err := s.externalIPDao.Get().
		SetId(externalIPID).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			// Preserve the existing target-before-reference error order when the
			// ExternalIP does not exist. The locked validation below reports it.
			return nil
		}
		return ConvertDAOErrorToGRPC(err, "get", externalIPID)
	}

	return validateTenantMatch(tenant, getResponse.GetObject(), "ExternalIP", externalIPID)
}

func (s *PrivateExternalIPAttachmentsServer) validateTargetReference(ctx context.Context,
	spec *privatev1.ExternalIPAttachmentSpec, metadata *privatev1.Metadata) (networkingHubReference, error) {
	switch {
	case spec.HasComputeInstance():
		return s.validateComputeInstanceReference(ctx, spec)
	case spec.HasCluster():
		return s.validateClusterReference(ctx, spec, isAutomatic(metadata))
	case spec.HasBaremetalInstance():
		return s.validateBareMetalInstanceReference(ctx, spec)
	default:
		return networkingHubReference{}, grpcstatus.Errorf(grpccodes.InvalidArgument,
			"exactly one target must be set (compute_instance, cluster, or baremetal_instance)")
	}
}

// validateComputeInstanceReference locks the target VM and checks its endpoint readiness: RUNNING, condition READY
// true, and the address of its network attachment reported.
func (s *PrivateExternalIPAttachmentsServer) validateComputeInstanceReference(
	ctx context.Context, spec *privatev1.ExternalIPAttachmentSpec) (networkingHubReference, error) {
	key := refKey(spec.GetComputeInstance())
	response, err := s.computeInstanceDao.Get().
		SetId(key).
		SetLock(true).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return networkingHubReference{}, grpcstatus.Errorf(grpccodes.InvalidArgument,
				"ComputeInstance '%s' does not exist", key)
		}
		return networkingHubReference{}, ConvertDAOErrorToGRPC(err, "get", key)
	}
	computeInstance := response.GetObject()
	spec.SetComputeInstance(privatev1.ComputeInstanceLocalReference_builder{Id: computeInstance.GetId()}.Build())
	if computeInstance.GetMetadata().HasDeletionTimestamp() {
		return networkingHubReference{}, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"ComputeInstance '%s' is being deleted", key)
	}
	statuses := computeInstance.GetStatus().GetComputeNetworkAttachmentStatuses()
	if computeInstance.GetStatus().GetState() != privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING ||
		!hasReadyComputeInstanceCondition(computeInstance) || len(statuses) == 0 || statuses[0].GetIpAddress() == "" {
		return networkingHubReference{}, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"ComputeInstance '%s' must be RUNNING and READY with its network attachment address before "+
				"creating an ExternalIPAttachment", key)
	}
	return networkingHubReference{resourceType: "ComputeInstance", id: computeInstance.GetId(),
		hubID: computeInstance.GetStatus().GetHub()}, nil
}

// validateClusterReference locks the target Cluster and checks its endpoint readiness. The API endpoint needs the
// API address, the ingress endpoint needs the ingress address; a manual attachment also needs the Cluster READY.
func (s *PrivateExternalIPAttachmentsServer) validateClusterReference(ctx context.Context,
	spec *privatev1.ExternalIPAttachmentSpec, automatic bool) (networkingHubReference, error) {
	key := refKey(spec.GetCluster())
	response, err := s.clusterDao.Get().
		SetId(key).
		SetLock(true).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return networkingHubReference{}, grpcstatus.Errorf(grpccodes.InvalidArgument,
				"Cluster '%s' does not exist", key)
		}
		return networkingHubReference{}, ConvertDAOErrorToGRPC(err, "get", key)
	}
	cluster := response.GetObject()
	spec.SetCluster(privatev1.ClusterLocalReference_builder{Id: cluster.GetId()}.Build())
	if cluster.GetMetadata().HasDeletionTimestamp() {
		return networkingHubReference{}, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"Cluster '%s' is being deleted", key)
	}
	if !automatic && cluster.GetStatus().GetState() != privatev1.ClusterState_CLUSTER_STATE_READY {
		return networkingHubReference{}, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"Cluster '%s' must be READY before creating an ExternalIPAttachment", key)
	}
	switch spec.GetTargetEndpoint() {
	case privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API:
		if cluster.GetStatus().GetApiEndpoint() == "" {
			return networkingHubReference{}, grpcstatus.Errorf(grpccodes.FailedPrecondition,
				"Cluster '%s' API endpoint is not ready for an ExternalIPAttachment", key)
		}
	case privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS:
		if cluster.GetStatus().GetIngressEndpoint() == "" {
			return networkingHubReference{}, grpcstatus.Errorf(grpccodes.FailedPrecondition,
				"Cluster '%s' ingress endpoint is not ready for an ExternalIPAttachment", key)
		}
	default:
		return networkingHubReference{}, grpcstatus.Errorf(grpccodes.InvalidArgument,
			"Cluster ExternalIPAttachment requires an API or ingress endpoint")
	}
	return networkingHubReference{resourceType: "Cluster", id: cluster.GetId(), hubID: cluster.GetStatus().GetHub()}, nil
}

// validateBareMetalInstanceReference locks the target server and checks its endpoint readiness: RUNNING and the
// address of its network attachment reported.
func (s *PrivateExternalIPAttachmentsServer) validateBareMetalInstanceReference(
	ctx context.Context, spec *privatev1.ExternalIPAttachmentSpec) (networkingHubReference, error) {
	key := refKey(spec.GetBaremetalInstance())
	response, err := s.bareMetalInstanceDao.Get().
		SetId(key).
		SetLock(true).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return networkingHubReference{}, grpcstatus.Errorf(grpccodes.InvalidArgument,
				"BareMetalInstance '%s' does not exist", key)
		}
		return networkingHubReference{}, ConvertDAOErrorToGRPC(err, "get", key)
	}
	instance := response.GetObject()
	spec.SetBaremetalInstance(privatev1.BareMetalInstanceLocalReference_builder{Id: instance.GetId()}.Build())
	if instance.GetMetadata().HasDeletionTimestamp() ||
		instance.GetStatus().GetState() != privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING ||
		!hasBareMetalInstanceIP(instance) {
		return networkingHubReference{}, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"BareMetalInstance '%s' must be RUNNING with its network attachment address before creating an "+
				"ExternalIPAttachment", key)
	}
	return networkingHubReference{resourceType: "BareMetalInstance", id: instance.GetId(),
		hubID: instance.GetStatus().GetHub()}, nil
}

func hasReadyComputeInstanceCondition(instance *privatev1.ComputeInstance) bool {
	for _, condition := range instance.GetStatus().GetConditions() {
		if condition.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_READY &&
			condition.GetStatus() == privatev1.ConditionStatus_CONDITION_STATUS_TRUE {
			return true
		}
	}
	return false
}

func hasBareMetalInstanceIP(instance *privatev1.BareMetalInstance) bool {
	for _, attachment := range instance.GetStatus().GetNetworkAttachmentStatuses() {
		if attachment.GetIpAddress() != "" {
			return true
		}
	}
	return false
}
