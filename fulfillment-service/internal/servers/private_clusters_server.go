/*
Copyright (c) 2025 Red Hat Inc.

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
	"sort"
	"strconv"
	"strings"

	"github.com/bits-and-blooms/bitset"
	"github.com/dustin/go-humanize/english"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/exp/maps"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
	"github.com/osac-project/osac/fulfillment-service/internal/references"
	"github.com/osac-project/osac/fulfillment-service/internal/utils"
)

type PrivateClustersServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.ClustersServer = (*PrivateClustersServer)(nil)

type PrivateClustersServer struct {
	privatev1.UnimplementedClustersServer
	logger                  *slog.Logger
	notifier                events.Notifier
	tenancyLogic            auth.TenancyLogic
	templatesDao            *dao.GenericDAO[*privatev1.ClusterTemplate]
	catalogItemsDao         *dao.GenericDAO[*privatev1.ClusterCatalogItem]
	hostTypesDao            *dao.GenericDAO[*privatev1.HostType]
	clusterVersionsDao      *dao.GenericDAO[*privatev1.ClusterVersion]
	subnetsDao              *dao.GenericDAO[*privatev1.Subnet]
	securityGroupsDao       *dao.GenericDAO[*privatev1.SecurityGroup]
	externalIPPoolDao       *dao.GenericDAO[*privatev1.ExternalIPPool]
	externalIPDao           *dao.GenericDAO[*privatev1.ExternalIP]
	externalIPAttachmentDao *dao.GenericDAO[*privatev1.ExternalIPAttachment]
	secretsDao              *dao.GenericDAO[*privatev1.Secret]
	generic                 *GenericServer[*privatev1.Cluster]
}

func NewPrivateClustersServer() *PrivateClustersServerBuilder {
	return &PrivateClustersServerBuilder{}
}

func (b *PrivateClustersServerBuilder) SetLogger(value *slog.Logger) *PrivateClustersServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateClustersServerBuilder) SetNotifier(value events.Notifier) *PrivateClustersServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateClustersServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateClustersServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateClustersServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateClustersServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateClustersServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateClustersServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateClustersServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateClustersServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateClustersServerBuilder) Build() (result *PrivateClustersServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	// Create the templates DAO:
	templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the catalog items DAO:
	catalogItemsDao, err := dao.NewGenericDAO[*privatev1.ClusterCatalogItem]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the host types DAO:
	hostTypesDao, err := dao.NewGenericDAO[*privatev1.HostType]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the cluster versions DAO:
	clusterVersionsDao, err := dao.NewGenericDAO[*privatev1.ClusterVersion]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the subnets DAO:
	subnetsDao, err := dao.NewGenericDAO[*privatev1.Subnet]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the ExternalIP DAOs:
	externalIPPoolDao, err := dao.NewGenericDAO[*privatev1.ExternalIPPool]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the security groups DAO:
	securityGroupsDao, err := dao.NewGenericDAO[*privatev1.SecurityGroup]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	externalIPDao, err := dao.NewGenericDAO[*privatev1.ExternalIP]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	externalIPAttachmentDao, err := dao.NewGenericDAO[*privatev1.ExternalIPAttachment]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the secrets DAO:
	secretsDao, err := dao.NewGenericDAO[*privatev1.Secret]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the generic server:
	// TODO(OSAC-1060): Remove AddAllowedTenants(auth.SharedTenant) once the osac-operator storage
	// controller handles CaaS cluster deletion independently of tenant-level storage provisioning.
	// CaaS e2e tests use SA auth → shared tenant to avoid cluster-storage finalizer blocking deletion
	// when AAP storage provisioning jobs fail in test environments without configured backends.
	generic, err := NewGenericServer[*privatev1.Cluster]().
		SetLogger(b.logger).
		SetService(privatev1.Clusters_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		AddAllowedTenants(auth.SharedTenant).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &PrivateClustersServer{
		logger:                  b.logger,
		notifier:                b.notifier,
		tenancyLogic:            b.tenancyLogic,
		templatesDao:            templatesDao,
		catalogItemsDao:         catalogItemsDao,
		hostTypesDao:            hostTypesDao,
		clusterVersionsDao:      clusterVersionsDao,
		subnetsDao:              subnetsDao,
		securityGroupsDao:       securityGroupsDao,
		externalIPPoolDao:       externalIPPoolDao,
		externalIPDao:           externalIPDao,
		externalIPAttachmentDao: externalIPAttachmentDao,
		secretsDao:              secretsDao,
		generic:                 generic,
	}
	return
}

func (s *PrivateClustersServer) List(ctx context.Context,
	request *privatev1.ClustersListRequest) (response *privatev1.ClustersListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateClustersServer) Get(ctx context.Context,
	request *privatev1.ClustersGetRequest) (response *privatev1.ClustersGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateClustersServer) Create(ctx context.Context,
	request *privatev1.ClustersCreateRequest) (response *privatev1.ClustersCreateResponse, err error) {
	// Ensure sane defaults:
	s.setDefaults(request.GetObject())

	// Get the spec:
	spec := request.GetObject().GetSpec()

	// The user may have specified the host types of the node sets by name, but we want to save the
	// identifiers, so we need to look them up:
	for _, nodeSet := range spec.GetNodeSets() {
		var hostType *privatev1.HostType
		hostType, err = s.lookupHostType(ctx, refKey(nodeSet.GetHostType()))
		if err != nil {
			return
		}
		if hostType != nil {
			hostTypeRef := &privatev1.HostTypeReference{}
			hostTypeRef.SetId(hostType.GetId())
			hostTypeRef.SetName(hostType.GetMetadata().GetName())
			nodeSet.SetHostType(hostTypeRef)
		}
	}

	// Validate duplicate conditions first:
	err = s.validateNoDuplicateConditions(request.GetObject())
	if err != nil {
		return
	}

	// Dispatch between catalog item and template paths:
	catalogItemRef := spec.GetCatalogItem()
	templateRef := spec.GetTemplate()
	if catalogItemRef != nil && templateRef != nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument,
			"catalog_item and template are mutually exclusive")
		return
	}
	if catalogItemRef != nil {
		err = s.validateAndTransformCatalogItem(ctx, request.GetObject())
		if err != nil {
			return
		}
	} else {
		err = s.validateAndTransformCluster(ctx, request.GetObject())
		if err != nil {
			return
		}
	}

	// Inject default network attachment from tenant defaults when not provided.
	// Injection errors are deferred — generic.Create validates the tenant and
	// produces a clearer error when the tenant itself doesn't exist.
	var networkAttachmentErr error
	if request.GetObject().GetSpec().GetNetworkAttachment() == nil {
		networkAttachmentErr = s.injectDefaultNetworkAttachment(ctx, request.GetObject())
	}

	// Validate network attachment references (subnet READY, SGs same-VN):
	if networkAttachmentErr == nil && request.GetObject().GetSpec().GetNetworkAttachment() != nil {
		networkAttachmentErr = s.validateNetworkAttachmentState(ctx, request.GetObject())
	}

	// Resolve fabric_interface for each node set when the cluster has a
	// network attachment. The HostType's interfaces list is searched for
	// the first interface with role "fabric".
	if spec.GetNetworkAttachment() != nil {
		if err = s.resolveFabricInterfaces(ctx, spec); err != nil {
			return
		}
	}

	// Attempt to persist — generic.Create validates tenant existence, so if the
	// tenant is invalid, the tenant error takes priority over a network error.
	err = s.generic.Create(ctx, request, &response)
	if err != nil {
		return
	}

	// If persist succeeded but we had a deferred network attachment error, roll
	// back by returning the error (the DB transaction will be rolled back by the
	// gRPC interceptor).
	if networkAttachmentErr != nil {
		err = networkAttachmentErr
		response = nil
	}

	if response.GetObject().GetSpec().GetAutoExternalIpAttachment() {
		if err = s.autoProvisionExternalIPs(ctx, response.GetObject()); err != nil {
			if tx, txErr := database.TxFromContext(ctx); txErr == nil {
				tx.ReportError(&err)
			}
			return
		}
	}
	return
}

func (s *PrivateClustersServer) Update(ctx context.Context,
	request *privatev1.ClustersUpdateRequest) (response *privatev1.ClustersUpdateResponse, err error) {
	err = s.validateNoDuplicateConditions(request.GetObject())
	if err != nil {
		return
	}
	err = s.validateClusterStateForSpecUpdate(ctx, request)
	if err != nil {
		return
	}
	err = s.validateTemplateImmutability(ctx, request)
	if err != nil {
		return
	}
	err = s.validateVersionUpdate(ctx, request)
	if err != nil {
		return
	}
	err = s.validateNodeSetsUpdate(ctx, request)
	if err != nil {
		return
	}
	err = s.validateNetworkAttachmentImmutability(ctx, request)
	if err != nil {
		return
	}
	// Validate network attachment state when security groups are being updated.
	// For sub-field masks (e.g. spec.network_attachment.security_groups), merge
	// the existing cluster's subnet into the update object before validation,
	// because the caller may only send the changed security_groups.
	mask := request.GetUpdateMask()
	if mask != nil && len(mask.GetPaths()) > 0 &&
		updateIncludesField(mask, "spec.network_attachment.security_groups") {
		obj := request.GetObject()
		if obj == nil || obj.GetSpec() == nil {
			err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object and spec are required")
			return
		}
		if obj.GetSpec().GetNetworkAttachment().GetSubnet() == nil {
			existing, found, lookupErr := s.getExistingCluster(ctx, request)
			if lookupErr != nil {
				err = lookupErr
				return
			}
			if found && existing.GetSpec().GetNetworkAttachment() != nil {
				att := obj.GetSpec().GetNetworkAttachment()
				if att == nil {
					att = privatev1.ClusterNetworkAttachment_builder{}.Build()
					obj.GetSpec().SetNetworkAttachment(att)
				}
				att.SetSubnet(existing.GetSpec().GetNetworkAttachment().GetSubnet())
			}
		}
		err = s.validateNetworkAttachmentState(ctx, request.GetObject())
		if err != nil {
			return
		}
	}
	err = s.validateAutoExternalIPImmutability(ctx, request)
	if err != nil {
		return
	}
	if err = s.validatePullSecretMutualExclusionForUpdate(ctx, request); err != nil {
		return
	}
	if err = s.validatePullSecretSecret(ctx, request.GetObject().GetSpec()); err != nil {
		return
	}
	if err = utils.ValidateClusterSpecFields(request.GetObject().GetSpec()); err != nil {
		return
	}
	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateClustersServer) Delete(ctx context.Context,
	request *privatev1.ClustersDeleteRequest) (response *privatev1.ClustersDeleteResponse, err error) {
	id := request.GetId()
	if id != "" {
		getResponse, getErr := s.generic.dao.Get().SetId(id).Do(ctx)
		if getErr != nil {
			var notFoundErr *dao.ErrNotFound
			if !errors.As(getErr, &notFoundErr) {
				err = getErr
				return
			}
		} else if getResponse.GetObject().GetSpec().GetAutoExternalIpAttachment() {
			err = s.autoCleanupExternalIP(ctx, id)
			if err != nil {
				return
			}
		}
	}
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateClustersServer) autoCleanupExternalIP(ctx context.Context, clusterID string) error {
	filter := fmt.Sprintf(
		"this.metadata.labels['%s'] == '%s'",
		autoCreatedForLabel, clusterID,
	)
	listResp, err := s.externalIPAttachmentDao.List().SetFilter(filter).Do(ctx)
	if err != nil {
		return fmt.Errorf("auto_external_ip_attachment cleanup: failed to list attachments: %w", err)
	}

	for _, attachment := range listResp.GetItems() {
		attachmentID := attachment.GetId()
		eipRef := attachment.GetSpec().GetExternalIp()
		eipID := refKey(eipRef)

		_, err = s.externalIPAttachmentDao.Delete().SetId(attachmentID).Do(ctx)
		if err != nil {
			return fmt.Errorf("auto_external_ip_attachment cleanup: failed to delete attachment: %w", err)
		}

		if s.notifier != nil {
			attResp, getErr := s.externalIPAttachmentDao.Get().SetId(attachmentID).Do(ctx)
			if getErr == nil {
				attEvent := privatev1.Event_builder{
					Type:                 privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
					ExternalIpAttachment: attResp.GetObject(),
				}.Build()
				if notifyErr := s.notifier.Notify(ctx, attEvent); notifyErr != nil {
					s.logger.WarnContext(ctx, "Failed to notify ExternalIPAttachment deletion", "error", notifyErr)
				}
			}
		}

		if eipID != "" {
			err = s.updateExternalIPAttachedFlag(ctx, eipID, false)
			if err != nil {
				return fmt.Errorf("auto_external_ip_attachment cleanup: %w", err)
			}

			eipResp, getErr := s.externalIPDao.Get().SetId(eipID).Do(ctx)
			if getErr != nil {
				return fmt.Errorf("auto_external_ip_attachment cleanup: failed to get ExternalIP: %w", getErr)
			}
			poolRef := eipResp.GetObject().GetSpec().GetPool()

			_, err = s.externalIPDao.Delete().SetId(eipID).Do(ctx)
			if err != nil {
				return fmt.Errorf("auto_external_ip_attachment cleanup: failed to delete ExternalIP: %w", err)
			}

			if s.notifier != nil {
				updatedEIP, getErr := s.externalIPDao.Get().SetId(eipID).Do(ctx)
				if getErr == nil {
					eipEvent := privatev1.Event_builder{
						Type:       privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
						ExternalIp: updatedEIP.GetObject(),
					}.Build()
					if notifyErr := s.notifier.Notify(ctx, eipEvent); notifyErr != nil {
						s.logger.WarnContext(ctx, "Failed to notify ExternalIP deletion", "error", notifyErr)
					}
				}
			}

			if poolRef != nil {
				err = UpdatePoolCapacity(ctx, s.externalIPPoolDao, refKey(poolRef), -1)
				if err != nil {
					return fmt.Errorf("auto_external_ip_attachment cleanup: %w", err)
				}
			}
		}
	}

	return nil
}

func (s *PrivateClustersServer) Signal(ctx context.Context,
	request *privatev1.ClustersSignalRequest) (response *privatev1.ClustersSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

func (s *PrivateClustersServer) setDefaults(cluster *privatev1.Cluster) {
	if !cluster.HasSpec() {
		cluster.SetSpec(&privatev1.ClusterSpec{})
	}
	if !cluster.HasStatus() {
		cluster.SetStatus(&privatev1.ClusterStatus{})
	}
}

func (s *PrivateClustersServer) lookupTemplate(ctx context.Context,
	key string) (result *privatev1.ClusterTemplate, err error) {
	if key == "" {
		return
	}
	response, err := s.templatesDao.List().
		SetFilter(fmt.Sprintf("this.id == %[1]s || this.metadata.name == %[1]s", strconv.Quote(key))).
		SetLimit(1).
		Do(ctx)
	if err != nil {
		var deniedErr *dao.ErrDenied
		if errors.As(err, &deniedErr) {
			err = grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
		}
		return
	}
	switch response.GetTotal() {
	case 0:
		err = grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"there is no template with identifier or name '%s'",
			key,
		)
	case 1:
		result = response.GetItems()[0]
	default:
		err = grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"there are multiple templates with identifier or name '%s'",
			key,
		)
	}
	return
}

// validatePullSecretMutualExclusion rejects specs that set both pull_secret and pull_secret_secret.
func (s *PrivateClustersServer) validatePullSecretMutualExclusion(spec *privatev1.ClusterSpec) error {
	if spec.HasPullSecret() && spec.GetPullSecretSecret() != nil {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"pull_secret and pull_secret_secret are mutually exclusive",
		)
	}
	return nil
}

// validatePullSecretMutualExclusionForUpdate checks for pull_secret / pull_secret_secret conflicts
// on Update, accounting for the update mask. When only one of the two fields is in the mask, the
// other retains its DB value, so a conflict can occur even if the request itself looks clean.
func (s *PrivateClustersServer) validatePullSecretMutualExclusionForUpdate(
	ctx context.Context, request *privatev1.ClustersUpdateRequest) error {
	spec := request.GetObject().GetSpec()
	mask := request.GetUpdateMask()

	if err := s.validatePullSecretMutualExclusion(spec); err != nil {
		return err
	}

	// With a nil/empty mask the entire object is replaced, so no DB state to consider.
	if mask == nil || len(mask.GetPaths()) == 0 {
		return nil
	}

	settingPullSecretSecret := spec.GetPullSecretSecret() != nil && updateIncludesField(mask, "spec.pull_secret_secret")
	settingPullSecret := spec.HasPullSecret() && updateIncludesField(mask, "spec.pull_secret")

	if !settingPullSecretSecret && !settingPullSecret {
		return nil
	}

	existing, found, err := s.getExistingCluster(ctx, request)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	existingSpec := existing.GetSpec()

	if settingPullSecretSecret && existingSpec.HasPullSecret() && !updateIncludesField(mask, "spec.pull_secret") {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "pull_secret and pull_secret_secret are mutually exclusive")
	}
	if settingPullSecret && existingSpec.GetPullSecretSecret() != nil && !updateIncludesField(mask, "spec.pull_secret_secret") {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "pull_secret and pull_secret_secret are mutually exclusive")
	}
	return nil
}

func (s *PrivateClustersServer) validatePullSecretSecret(ctx context.Context, spec *privatev1.ClusterSpec) error {
	ref := spec.GetPullSecretSecret()
	if ref == nil {
		return nil
	}
	if ref.GetId() == "" && ref.GetName() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "pull_secret_secret must specify id or name")
	}
	resolved, err := references.NewDAOLookupFunc(s.secretsDao)(ctx, "", "", ref.GetId(), ref.GetName())
	if err != nil {
		var deniedErr *dao.ErrDenied
		if errors.As(err, &deniedErr) {
			return grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
		}
		var nf interface{ IsNotFound() bool }
		if errors.As(err, &nf) && nf.IsNotFound() {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"there is no secret with identifier or name '%s'", refKey(ref))
		}
		s.logger.ErrorContext(ctx, "Failed to resolve pull_secret_secret reference", "error", err)
		return grpcstatus.Errorf(grpccodes.Internal, "failed to resolve pull_secret_secret reference")
	}
	resolvedRef := &privatev1.SecretLocalReference{}
	resolvedRef.SetId(resolved.ID)
	resolvedRef.SetName(resolved.Name)
	spec.SetPullSecretSecret(resolvedRef)
	return nil
}

func (s *PrivateClustersServer) lookupHostType(ctx context.Context,
	key string) (result *privatev1.HostType, err error) {
	if key == "" {
		return
	}
	response, err := s.hostTypesDao.List().
		SetFilter(fmt.Sprintf("this.id == %[1]s || this.metadata.name == %[1]s", strconv.Quote(key))).
		SetLimit(1).
		Do(ctx)
	if err != nil {
		var deniedErr *dao.ErrDenied
		if errors.As(err, &deniedErr) {
			err = grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
		}
		return
	}
	switch response.GetTotal() {
	case 0:
		err = grpcstatus.Errorf(
			grpccodes.NotFound,
			"there is no host type with identifier or name '%s'",
			key,
		)
	case 1:
		result = response.GetItems()[0]
	default:
		err = grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"there are multiple host types with identifier or name '%s'",
			key,
		)
	}
	return
}

// ensureClusterVersion makes sure the cluster spec has a usable version reference: if the user didn't provide one, it
// resolves the system default. Either way, it validates that the resulting ClusterVersion isn't deleted, disabled,
// or obsolete.
func (s *PrivateClustersServer) ensureClusterVersion(ctx context.Context, cluster *privatev1.Cluster) error {
	versionRef := cluster.GetSpec().GetVersion()
	if versionRef != nil && versionRef.GetName() != "" {
		return lookupAndValidateClusterVersion(ctx, s.logger, s.clusterVersionsDao, versionRef.GetName())
	}
	ref, err := resolveDefaultClusterVersion(ctx, s.logger, s.clusterVersionsDao)
	if err != nil {
		return err
	}
	cluster.GetSpec().SetVersion(ref)
	return nil
}

func (s *PrivateClustersServer) validateNoDuplicateConditions(object *privatev1.Cluster) error {
	conditions := object.GetStatus().GetConditions()
	if conditions == nil {
		return nil
	}
	conditionTypes := &bitset.BitSet{}
	for _, condition := range conditions {
		conditionType := condition.GetType()
		if conditionTypes.Test(uint(conditionType)) { // #nosec G115 -- proto enum, non-negative
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"condition '%s' is duplicated",
				conditionType.String(),
			)
		}
		conditionTypes.Set(uint(conditionType)) // #nosec G115 -- proto enum, non-negative
	}
	return nil
}

// validateNodeSetsUpdate validates that changes to node_sets are allowed.
// It delegates to specific validators for different aspects of the validation.
func (s *PrivateClustersServer) validateNodeSetsUpdate(ctx context.Context,
	request *privatev1.ClustersUpdateRequest) error {
	// Check if the update affects node_sets at all:
	if !s.updateAffectsNodeSets(request.GetUpdateMask()) {
		// Update doesn't touch node_sets, no validation needed
		return nil
	}

	// Check if only size fields are being updated - these are always allowed
	if s.isUpdatingOnlySizes(request.GetUpdateMask()) {
		return nil
	}

	// Fetch the existing cluster from the database:
	existingCluster, found, err := s.getExistingCluster(ctx, request)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	// Get the node sets from both clusters:
	existingNodeSets := existingCluster.GetSpec().GetNodeSets()
	newNodeSets := request.GetObject().GetSpec().GetNodeSets()

	// Run specific validations:
	if err := s.validateAtLeastOneNodeSet(newNodeSets); err != nil {
		return err
	}
	if err := s.validateNodeSetHostTypeImmutability(existingNodeSets, newNodeSets); err != nil {
		return err
	}

	return nil
}

// getExistingCluster fetches the existing cluster from the database.
// Returns the cluster, a boolean indicating if it was found, and any error that occurred.
func (s *PrivateClustersServer) getExistingCluster(ctx context.Context,
	request *privatev1.ClustersUpdateRequest) (*privatev1.Cluster, bool, error) {
	cluster := request.GetObject()
	if cluster == nil {
		return nil, false, nil
	}
	id := cluster.GetId()
	if id == "" {
		return nil, false, nil
	}
	getResponse, err := s.generic.dao.Get().
		SetId(id).
		Do(ctx)
	if err != nil {
		return nil, false, err
	}
	existingCluster := getResponse.GetObject()
	return existingCluster, true, nil
}

// validateClusterStateForSpecUpdate rejects spec modifications when the cluster is in a
// terminal or non-reconcilable state. The reconciler processes UNSPECIFIED (by transitioning
// to PROGRESSING via setDefaults), PROGRESSING, and READY clusters — it returns nil for
// every other state — so spec changes outside those states would be silently ignored.
//
// This uses SetLock(true) (SELECT ... FOR UPDATE) so the row lock is held for the
// remainder of the transaction, preventing a concurrent status transition from
// bypassing the check before GenericServer.Update writes the spec changes.
func (s *PrivateClustersServer) validateClusterStateForSpecUpdate(ctx context.Context,
	request *privatev1.ClustersUpdateRequest) error {
	if !updateIncludesField(request.GetUpdateMask(), "spec") {
		return nil
	}
	cluster := request.GetObject()
	if cluster == nil {
		return nil
	}
	id := cluster.GetId()
	if id == "" {
		return nil
	}
	getResponse, err := s.generic.dao.Get().
		SetId(id).
		SetLock(true).
		Do(ctx)
	if err != nil {
		return err
	}
	existingCluster := getResponse.GetObject()
	state := existingCluster.GetStatus().GetState()
	if state != privatev1.ClusterState_CLUSTER_STATE_UNSPECIFIED &&
		state != privatev1.ClusterState_CLUSTER_STATE_PROGRESSING &&
		state != privatev1.ClusterState_CLUSTER_STATE_READY {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"cannot update cluster spec when cluster state is %s",
			state,
		)
	}
	return nil
}

// updateAffectsNodeSets checks if the update mask indicates that node_sets are being modified.
func (s *PrivateClustersServer) updateAffectsNodeSets(updateMask *fieldmaskpb.FieldMask) bool {
	return updateIncludesField(updateMask, "spec.node_sets")
}

// isUpdatingOnlySizes checks if the update mask is only modifying size fields of node sets.
func (s *PrivateClustersServer) isUpdatingOnlySizes(updateMask *fieldmaskpb.FieldMask) bool {
	for _, path := range updateMask.GetPaths() {
		if strings.HasPrefix(path, "spec.node_sets") {
			if !strings.HasSuffix(path, ".size") {
				return false
			}
		}
	}
	return true
}

// validateAtLeastOneNodeSet ensures that clusters always have at least one node set.
func (s *PrivateClustersServer) validateAtLeastOneNodeSet(nodeSets map[string]*privatev1.ClusterNodeSet) error {
	if len(nodeSets) == 0 {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"cannot remove the last node set: clusters must have at least one node set",
		)
	}
	return nil
}

// validateNodeSetHostTypeImmutability ensures that the host_type field of existing node sets
// cannot be changed. This is an existing documented restriction in the API specification.
func (s *PrivateClustersServer) validateNodeSetHostTypeImmutability(
	existingNodeSets map[string]*privatev1.ClusterNodeSet,
	newNodeSets map[string]*privatev1.ClusterNodeSet) error {
	for nodeSetName, existingNodeSet := range existingNodeSets {
		newNodeSet, exists := newNodeSets[nodeSetName]
		if !exists {
			// Node set is being removed, which is allowed (if at least one remains)
			continue
		}
		existingHostType := existingNodeSet.GetHostType()
		newHostType := newNodeSet.GetHostType()
		if refKey(existingHostType) != refKey(newHostType) {
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"cannot change host_type for node set '%s' from '%s' to '%s': host_type is immutable",
				nodeSetName,
				refKey(existingHostType),
				refKey(newHostType),
			)
		}
	}
	return nil
}

// validateTemplateImmutability ensures that the template and template_parameters fields
// cannot be changed after cluster creation.
func (s *PrivateClustersServer) validateTemplateImmutability(ctx context.Context,
	request *privatev1.ClustersUpdateRequest) error {
	// Check if template, template_parameters, or catalog_item are being updated:
	updateMask := request.GetUpdateMask()
	updatingTemplate := updateIncludesField(updateMask, "spec.template")
	updatingTemplateParams := updateIncludesField(updateMask, "spec.template_parameters")
	updatingCatalogItem := updateIncludesField(updateMask, "spec.catalog_item")

	// If none of the immutable fields are being updated, no validation needed:
	if !updatingTemplate && !updatingTemplateParams && !updatingCatalogItem {
		return nil
	}

	// Fetch the existing cluster from the database:
	existingCluster, found, err := s.getExistingCluster(ctx, request)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	// Get the specs from both clusters:
	existingSpec := existingCluster.GetSpec()
	newSpec := request.GetObject().GetSpec()

	// Sparse updates can reach private validation with spec omitted entirely.
	// Without a spec message, this validator has no immutable spec fields to compare.
	if newSpec == nil {
		return nil
	}

	// These immutable non-optional proto3 fields have no presence tracking, so a
	// zero value is ambiguous. Treat zero as absent and normalize to the existing
	// value before comparing.
	if updatingTemplate && newSpec.GetTemplate().GetId() == "" && newSpec.GetTemplate().GetName() == "" {
		newSpec.SetTemplate(existingSpec.GetTemplate())
	}
	if updatingTemplateParams && len(newSpec.GetTemplateParameters()) == 0 {
		newSpec.SetTemplateParameters(existingSpec.GetTemplateParameters())
	}
	if updatingCatalogItem && newSpec.GetCatalogItem().GetId() == "" && newSpec.GetCatalogItem().GetName() == "" {
		newSpec.SetCatalogItem(existingSpec.GetCatalogItem())
	}

	// Check if template has changed. Compare by refKey (Id) rather than proto.Equal because
	// the reference validator interceptor may backfill additional fields on incoming requests.
	if updatingTemplate && refKey(existingSpec.GetTemplate()) != refKey(newSpec.GetTemplate()) {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"cannot change spec.template from '%s' to '%s': template is immutable",
			refKey(existingSpec.GetTemplate()),
			refKey(newSpec.GetTemplate()),
		)
	}

	// Check if template_parameters have changed:
	if updatingTemplateParams {
		templateParamsEqual := func(first, second *anypb.Any) bool {
			return proto.Equal(first, second)
		}
		if !maps.EqualFunc(existingSpec.GetTemplateParameters(), newSpec.GetTemplateParameters(), templateParamsEqual) { //nolint:govet // inline: Go compiler doesn't support type param inference for inlining yet
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"cannot change spec.template_parameters: template parameters are immutable",
			)
		}
	}

	if updatingCatalogItem && refKey(existingSpec.GetCatalogItem()) != refKey(newSpec.GetCatalogItem()) {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"cannot change spec.catalog_item from '%s' to '%s': catalog item is immutable",
			refKey(existingSpec.GetCatalogItem()),
			refKey(newSpec.GetCatalogItem()),
		)
	}

	return nil
}

// validateVersionUpdate ensures version stays valid on update. Absent
// version is normalized to the existing value (cannot be cleared once set).
// Changed values are validated the same way as Create via ensureClusterVersion.
func (s *PrivateClustersServer) validateVersionUpdate(ctx context.Context,
	request *privatev1.ClustersUpdateRequest) error {
	if !updateIncludesField(request.GetUpdateMask(), "spec.version") {
		return nil
	}
	newSpec := request.GetObject().GetSpec()
	if newSpec == nil {
		return nil
	}
	existing, found, err := s.getExistingCluster(ctx, request)
	if err != nil || !found {
		return err
	}
	newRef := newSpec.GetVersion()
	existingRef := existing.GetSpec().GetVersion()
	if newRef == nil || newRef.GetName() == "" {
		if existingRef != nil {
			newSpec.SetVersion(existingRef)
		}
		return nil
	}
	if existingRef != nil && newRef.GetName() == existingRef.GetName() {
		return nil
	}
	return s.ensureClusterVersion(ctx, request.GetObject())
}

// validateNetworkAttachmentImmutability ensures that the subnet field within
// network_attachment cannot be changed after cluster creation.
func (s *PrivateClustersServer) validateNetworkAttachmentImmutability(ctx context.Context,
	request *privatev1.ClustersUpdateRequest) error {
	updateMask := request.GetUpdateMask()

	if !updateIncludesField(updateMask, "spec.network_attachment.subnet") {
		return nil
	}

	existingCluster, found, err := s.getExistingCluster(ctx, request)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	existingSubnet := existingCluster.GetSpec().GetNetworkAttachment().GetSubnet()
	newSubnet := request.GetObject().GetSpec().GetNetworkAttachment().GetSubnet()

	if refKey(existingSubnet) != refKey(newSubnet) {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"cannot change spec.network_attachment.subnet from '%s' to '%s': subnet is immutable",
			refKey(existingSubnet), refKey(newSubnet),
		)
	}

	return nil
}

// resolveTargetTenant returns the tenant from the cluster metadata, falling
// back to the caller's default tenant when not explicitly set.
func (s *PrivateClustersServer) resolveTargetTenant(ctx context.Context, cluster *privatev1.Cluster) (string, error) {
	if t := cluster.GetMetadata().GetTenant(); t != "" {
		return t, nil
	}
	return s.tenancyLogic.DetermineDefaultTenant(ctx)
}

// injectDefaultNetworkAttachment populates spec.network_attachment from the
// tenant's default subnet and security group when the caller omits it.
func (s *PrivateClustersServer) injectDefaultNetworkAttachment(ctx context.Context,
	cluster *privatev1.Cluster) error {
	tenant, err := s.resolveTargetTenant(ctx, cluster)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to determine target tenant", slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to determine target tenant")
	}

	spec := cluster.GetSpec()
	subnet, err := findDefaultSubnet(ctx, s.logger, s.subnetsDao, tenant)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to look up default subnet", slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to look up default subnet")
	}
	if subnet == nil {
		return nil
	}

	attachment := privatev1.ClusterNetworkAttachment_builder{
		Subnet: privatev1.SubnetLocalReference_builder{Id: subnet.GetId()}.Build(),
	}.Build()

	virtualNetworkID := refKey(subnet.GetSpec().GetVirtualNetwork())
	sg, err := findDefaultSecurityGroup(ctx, s.logger, s.securityGroupsDao, virtualNetworkID, tenant)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to look up default security group", slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to look up default security group")
	}
	if sg != nil {
		attachment.SetSecurityGroups([]*privatev1.SecurityGroupLocalReference{
			privatev1.SecurityGroupLocalReference_builder{Id: sg.GetId()}.Build(),
		})
	}

	spec.SetNetworkAttachment(attachment)

	attrs := []slog.Attr{
		slog.String("subnet_id", subnet.GetId()),
	}
	if sg != nil {
		attrs = append(attrs, slog.String("security_group_id", sg.GetId()))
	}
	s.logger.LogAttrs(ctx, slog.LevelInfo, "auto-injected default network attachment", attrs...)
	return nil
}

// validateNetworkAttachmentState validates that the subnet referenced by the
// cluster's network attachment is READY, and that all security groups are READY
// and belong to the same virtual network as the subnet.
func (s *PrivateClustersServer) validateNetworkAttachmentState(ctx context.Context, cluster *privatev1.Cluster) error {
	if cluster == nil || cluster.GetSpec() == nil {
		return nil
	}
	att := cluster.GetSpec().GetNetworkAttachment()
	if att == nil {
		return nil
	}

	subnetRef := att.GetSubnet()
	subnetKey := refKey(subnetRef)
	if subnetKey == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"spec.network_attachment.subnet is required")
	}

	getResponse, getErr := s.subnetsDao.Get().SetId(subnetKey).Do(ctx)
	if getErr != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(getErr, &notFoundErr) {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"spec.network_attachment: subnet '%s' does not exist", subnetKey)
		}
		s.logger.ErrorContext(ctx, "failed to query subnet",
			slog.String("subnet_key", subnetKey), slog.Any("error", getErr))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to validate subnet")
	}
	subnet := getResponse.GetObject()
	if subnet == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"spec.network_attachment: subnet '%s' does not exist", subnetKey)
	}
	if subnet.GetStatus().GetState() != privatev1.SubnetState_SUBNET_STATE_READY {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"spec.network_attachment: subnet '%s' is not in READY state (current state: %s)",
			subnetKey, subnet.GetStatus().GetState().String())
	}

	virtualNetworkID := refKey(subnet.GetSpec().GetVirtualNetwork())
	if virtualNetworkID == "" {
		return grpcstatus.Errorf(grpccodes.Internal,
			"spec.network_attachment: subnet '%s' has no virtual network reference", subnetKey)
	}

	for i, sgRef := range att.GetSecurityGroups() {
		sgKey := refKey(sgRef)
		if sgKey == "" {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"spec.network_attachment.security_groups[%d]: reference is empty", i)
		}

		sgResponse, getErr := s.securityGroupsDao.Get().SetId(sgKey).Do(ctx)
		if getErr != nil {
			var notFoundErr *dao.ErrNotFound
			if errors.As(getErr, &notFoundErr) {
				return grpcstatus.Errorf(grpccodes.InvalidArgument,
					"spec.network_attachment.security_groups[%d]: security group '%s' does not exist",
					i, sgKey)
			}
			s.logger.ErrorContext(ctx, "failed to query security group",
				slog.String("security_group_key", sgKey), slog.Any("error", getErr))
			return grpcstatus.Errorf(grpccodes.Internal, "failed to validate security group")
		}
		sg := sgResponse.GetObject()
		if sg == nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"spec.network_attachment.security_groups[%d]: security group '%s' does not exist",
				i, sgKey)
		}
		if sg.GetStatus().GetState() != privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY {
			return grpcstatus.Errorf(grpccodes.FailedPrecondition,
				"spec.network_attachment.security_groups[%d]: security group '%s' is not in READY state (current state: %s)",
				i, sgKey, sg.GetStatus().GetState().String())
		}

		sgVirtualNetworkID := refKey(sg.GetSpec().GetVirtualNetwork())
		if sgVirtualNetworkID != virtualNetworkID {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"spec.network_attachment.security_groups[%d]: security group '%s' belongs to VirtualNetwork '%s', "+
					"but subnet '%s' belongs to VirtualNetwork '%s'",
				i, sgKey, sgVirtualNetworkID, subnetKey, virtualNetworkID)
		}
	}

	return nil
}

// validateAutoExternalIPImmutability prevents changing auto_external_ip_attachment after creation.
func (s *PrivateClustersServer) validateAutoExternalIPImmutability(ctx context.Context,
	request *privatev1.ClustersUpdateRequest) error {
	if !updateIncludesField(request.GetUpdateMask(), "spec.auto_external_ip_attachment") {
		return nil
	}

	existing, found, err := s.getExistingCluster(ctx, request)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	oldVal := existing.GetSpec().GetAutoExternalIpAttachment()
	newVal := request.GetObject().GetSpec().GetAutoExternalIpAttachment()
	if oldVal != newVal {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"cannot change spec.auto_external_ip_attachment: auto_external_ip_attachment is immutable after creation")
	}
	return nil
}

// resolveFabricInterfaces populates fabric_interface on each node set by
// looking up the HostType and selecting the first interface with role "fabric".
func (s *PrivateClustersServer) resolveFabricInterfaces(ctx context.Context, spec *privatev1.ClusterSpec) error {
	for name, nodeSet := range spec.GetNodeSets() {
		hostTypeKey := refKey(nodeSet.GetHostType())
		if hostTypeKey == "" {
			continue
		}
		hostType, err := s.lookupHostType(ctx, hostTypeKey)
		if err != nil {
			return err
		}
		if hostType == nil {
			continue
		}
		fabricInterface := ""
		for _, ni := range hostType.GetInterfaces() {
			if strings.EqualFold(ni.GetRole(), "fabric") {
				fabricInterface = ni.GetName()
				break
			}
		}
		if fabricInterface == "" {
			return grpcstatus.Errorf(grpccodes.FailedPrecondition,
				"node_sets[%s]: host type '%s' has no interface with role 'fabric'",
				name, hostTypeKey)
		}
		nodeSet.SetFabricInterface(fabricInterface)
	}
	return nil
}

// autoProvisionExternalIPs creates two ExternalIPs and two ExternalIPAttachments
// (one for API, one for ingress) from the best available pool.
func (s *PrivateClustersServer) autoProvisionExternalIPs(ctx context.Context, cluster *privatev1.Cluster) error {
	pool, err := SelectExternalIPPool(ctx, s.externalIPPoolDao, privatev1.IPFamily_IP_FAMILY_UNSPECIFIED)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "auto_external_ip_attachment: %s", err)
	}
	if pool.GetStatus().GetAvailable() < 2 {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"auto_external_ip_attachment: ExternalIP pool '%s' needs at least 2 available IPs, has %d",
			pool.GetId(), pool.GetStatus().GetAvailable())
	}

	tenant := cluster.GetMetadata().GetTenant()
	clusterID := cluster.GetId()

	endpoints := []privatev1.ExternalIPAttachmentEndpoint{
		privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
		privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
	}

	for _, endpoint := range endpoints {
		eip := privatev1.ExternalIP_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: tenant,
				Labels: map[string]string{
					autoCreatedLabel:    "true",
					autoCreatedForLabel: clusterID,
				},
				Annotations: map[string]string{
					ownerReferenceAnnotation: clusterID,
				},
				Creator: "system",
			}.Build(),
			Spec: privatev1.ExternalIPSpec_builder{
				Pool: privatev1.ExternalIPPoolReference_builder{Id: pool.GetId()}.Build(),
			}.Build(),
			Status: privatev1.ExternalIPStatus_builder{
				State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING,
			}.Build(),
		}.Build()

		eipResp, err := s.externalIPDao.Create().SetObject(eip).Do(ctx)
		if err != nil {
			return fmt.Errorf("auto_external_ip_attachment: failed to create ExternalIP: %w", err)
		}
		eipID := eipResp.GetObject().GetId()

		attachment := privatev1.ExternalIPAttachment_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: tenant,
				Labels: map[string]string{
					autoCreatedLabel:    "true",
					autoCreatedForLabel: clusterID,
				},
				Annotations: map[string]string{
					ownerReferenceAnnotation: clusterID,
				},
				Creator: "system",
			}.Build(),
			Spec: privatev1.ExternalIPAttachmentSpec_builder{
				ExternalIp:     privatev1.ExternalIPLocalReference_builder{Id: eipID}.Build(),
				Cluster:        privatev1.ClusterLocalReference_builder{Id: clusterID}.Build(),
				TargetEndpoint: endpoint,
			}.Build(),
			Status: privatev1.ExternalIPAttachmentStatus_builder{
				State: privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING,
			}.Build(),
		}.Build()

		_, err = s.externalIPAttachmentDao.Create().SetObject(attachment).Do(ctx)
		if err != nil {
			return fmt.Errorf("auto_external_ip_attachment: failed to create ExternalIPAttachment: %w", err)
		}

		eipResp.GetObject().GetStatus().SetAttached(true)
		_, err = s.externalIPDao.Update().SetObject(eipResp.GetObject()).Do(ctx)
		if err != nil {
			return fmt.Errorf("auto_external_ip_attachment: failed to update ExternalIP attached flag: %w", err)
		}
	}

	err = UpdatePoolCapacity(ctx, s.externalIPPoolDao, pool.GetId(), 2)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "auto_external_ip_attachment: %s", err)
	}

	s.logger.InfoContext(ctx, "auto-provisioned external IP attachments for cluster",
		slog.String("cluster_id", clusterID),
		slog.String("pool_id", pool.GetId()),
	)
	return nil
}

func (s *PrivateClustersServer) validateAndTransformCluster(ctx context.Context, cluster *privatev1.Cluster) error {
	// Check that the template is specified and that refers to a existing template. If the reference was a name
	// then we replace it with the identifier.
	if cluster == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
	}
	templateRef := cluster.GetSpec().GetTemplate()
	if templateRef == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "template is mandatory")
	}
	templateRefStr := refKey(templateRef)
	template, err := s.lookupTemplate(ctx, templateRefStr)
	if err != nil {
		return err
	}
	if template.GetMetadata().HasDeletionTimestamp() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "template '%s' has been deleted", templateRefStr)
	}

	// Apply spec defaults from the template (user values take precedence):
	utils.ApplyClusterSpecDefaults(cluster.GetSpec(), template.GetSpecDefaults())

	// Validate mutual exclusion of inline pull_secret and pull_secret_secret reference:
	if err = s.validatePullSecretMutualExclusion(cluster.GetSpec()); err != nil {
		return err
	}

	// Validate pull_secret_secret reference exists:
	if err = s.validatePullSecretSecret(ctx, cluster.GetSpec()); err != nil {
		return err
	}

	if err = s.ensureClusterVersion(ctx, cluster); err != nil {
		return err
	}

	// Validate cluster spec fields (CIDR format, etc.) after defaults have been applied:
	if err = utils.ValidateClusterSpecFields(cluster.GetSpec()); err != nil {
		return err
	}

	// Check that the host types given in the cluster and the template exist, and index them by identifier and
	// name, so that it will be easier to look them up later.
	hostTypes, err := s.lookupAndIndexHostTypes(ctx, template)
	if err != nil {
		return err
	}

	// Validate node sets against the template:
	templateNodeSets := template.GetNodeSets()
	clusterNodeSets := cluster.GetSpec().GetNodeSets()
	if err = s.validateNodeSets(clusterNodeSets, templateNodeSets, hostTypes, templateRefStr); err != nil {
		return err
	}

	// Replace the node sets given in the cluster with those from the template, taking only the size from cluster:
	mergeNodeSetsWithTemplate(cluster, templateNodeSets, clusterNodeSets)

	// Validate template parameters:
	clusterParameters := cluster.GetSpec().GetTemplateParameters()
	err = utils.ValidateClusterTemplateParameters(template, clusterParameters)
	if err != nil {
		return err
	}

	// Set default values for template parameters:
	actualClusterParameters := utils.ProcessTemplateParametersWithDefaults(
		utils.ClusterTemplateAdapter{ClusterTemplate: template},
		clusterParameters,
	)
	cluster.GetSpec().SetTemplateParameters(actualClusterParameters)

	// Make sure that the template and the host types of the node sets are referenced by their identifiers and
	// names, as that is what we want to save to the database. Both fields are needed: id for lookups, name for
	// display and billing dimensions (metering reads the name).
	resolvedTemplateRef := &privatev1.ClusterTemplateReference{}
	resolvedTemplateRef.SetId(template.GetId())
	resolvedTemplateRef.SetName(template.GetMetadata().GetName())
	cluster.GetSpec().SetTemplate(resolvedTemplateRef)
	for _, clusterNodeSet := range cluster.GetSpec().GetNodeSets() {
		hostType := hostTypes[refKey(clusterNodeSet.GetHostType())]
		if hostType != nil {
			resolvedHostTypeRef := &privatev1.HostTypeReference{}
			resolvedHostTypeRef.SetId(hostType.GetId())
			resolvedHostTypeRef.SetName(hostType.GetMetadata().GetName())
			clusterNodeSet.SetHostType(resolvedHostTypeRef)
		}
	}

	return nil
}

// lookupAndIndexHostTypes fetches host types referenced by the template's node sets and indexes them
// by both identifier and name.
func (s *PrivateClustersServer) lookupAndIndexHostTypes(
	ctx context.Context, template *privatev1.ClusterTemplate,
) (map[string]*privatev1.HostType, error) {
	hostTypes := map[string]*privatev1.HostType{}
	for _, nodeSet := range template.GetNodeSets() {
		hostTypeRef := nodeSet.GetHostType()
		if hostTypeRef == nil {
			continue
		}
		hostType, err := s.lookupHostType(ctx, refKey(hostTypeRef))
		if err != nil {
			return nil, err
		}
		hostTypeName := hostType.GetMetadata().GetName()
		if hostTypeName != "" {
			hostTypes[hostTypeName] = hostType
		}
		hostTypeId := hostType.GetId()
		hostTypes[hostTypeId] = hostType
	}
	return hostTypes, nil
}

// validateNodeSets checks membership, host-type consistency, and positive size for cluster node sets.
func (s *PrivateClustersServer) validateNodeSets(
	clusterNodeSets map[string]*privatev1.ClusterNodeSet,
	templateNodeSets map[string]*privatev1.ClusterTemplateNodeSet,
	hostTypes map[string]*privatev1.HostType,
	templateRef string,
) error {
	// Check that all the node sets given in the cluster correspond to node sets that exist in the template:
	for clusterNodeSetKey := range clusterNodeSets {
		templateNodeSet := templateNodeSets[clusterNodeSetKey]
		if templateNodeSet == nil {
			templateNodeSetKeys := maps.Keys(templateNodeSets)
			sort.Strings(templateNodeSetKeys)
			for i, templateNodeSetKey := range templateNodeSetKeys {
				templateNodeSetKeys[i] = fmt.Sprintf("'%s'", templateNodeSetKey)
			}
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"node set '%s' doesn't exist, valid values for template '%s' are %s",
				clusterNodeSetKey, templateRef, english.WordSeries(templateNodeSetKeys, "and"),
			)
		}
	}

	// Check that all the node sets given in the cluster specify the same host type that is specified in the
	// template:
	for clusterNodeSetKey, clusterNodeSet := range clusterNodeSets {
		clusterHostTypeRef := clusterNodeSet.GetHostType()
		clusterHostTypeKey := refKey(clusterHostTypeRef)
		if clusterHostTypeKey == "" {
			continue
		}
		templateNodeSet := templateNodeSets[clusterNodeSetKey]
		templateHostTypeRef := templateNodeSet.GetHostType()
		templateHostType := hostTypes[refKey(templateHostTypeRef)]
		templateHostTypeId := templateHostType.GetId()
		templateHostTypeName := templateHostType.GetMetadata().GetName()
		if templateHostTypeName != "" {
			if clusterHostTypeKey != templateHostTypeId && clusterHostTypeKey != templateHostTypeName {
				return grpcstatus.Errorf(
					grpccodes.InvalidArgument,
					"host type for node set '%s' should be empty, '%s' or '%s', like in template '%s', "+
						"but it is '%s'",
					clusterNodeSetKey,
					templateHostTypeName,
					templateHostTypeId,
					templateRef,
					clusterHostTypeKey,
				)
			}
		} else {
			if clusterHostTypeKey != templateHostTypeId {
				return grpcstatus.Errorf(
					grpccodes.InvalidArgument,
					"host type for node set '%s' should be empty or '%s', like in template '%s', "+
						"but it is '%s'",
					clusterNodeSetKey,
					templateHostTypeId,
					templateRef,
					clusterHostTypeKey,
				)
			}
		}
	}

	// Check that all the node sets given in the cluster have a positive size:
	for clusterNodeSetKey, clusterNodeSet := range clusterNodeSets {
		if !clusterNodeSet.HasSize() {
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"size for node set '%s' is required",
				clusterNodeSetKey,
			)
		}
		clusterNodeSetSize := clusterNodeSet.GetSize()
		if clusterNodeSetSize <= 0 {
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"size for node set '%s' should be greater than zero, but it is %d",
				clusterNodeSetKey, clusterNodeSetSize,
			)
		}
	}

	return nil
}

// mergeNodeSetsWithTemplate replaces the cluster's node sets with template-derived sets, keeping only
// the size from the cluster.
func mergeNodeSetsWithTemplate(
	cluster *privatev1.Cluster,
	templateNodeSets map[string]*privatev1.ClusterTemplateNodeSet,
	clusterNodeSets map[string]*privatev1.ClusterNodeSet,
) {
	actualNodeSets := map[string]*privatev1.ClusterNodeSet{}
	for templateNodeSetKey, templateNodeSet := range templateNodeSets {
		var actualNodeSetSize int32
		clusterNodeSet := clusterNodeSets[templateNodeSetKey]
		if clusterNodeSet != nil {
			actualNodeSetSize = clusterNodeSet.GetSize()
		} else {
			actualNodeSetSize = templateNodeSet.GetSize()
		}
		actualNodeSets[templateNodeSetKey] = privatev1.ClusterNodeSet_builder{
			HostType: templateNodeSet.GetHostType(),
			Size:     proto.Int32(actualNodeSetSize),
		}.Build()
	}
	cluster.GetSpec().SetNodeSets(actualNodeSets)
}

func (s *PrivateClustersServer) validateAndTransformCatalogItem(ctx context.Context, cluster *privatev1.Cluster) error {
	if cluster == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
	}
	catalogItemRef := cluster.GetSpec().GetCatalogItem()
	if catalogItemRef == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "catalog_item is mandatory")
	}
	catalogItemRefStr := refKey(catalogItemRef)

	catalogItem, err := s.lookupCatalogItem(ctx, catalogItemRefStr)
	if err != nil {
		return err
	}

	if err := validateCatalogItemAccess(catalogItem, catalogItemRefStr); err != nil {
		return err
	}

	templateRef := catalogItem.GetTemplate()
	if templateRef == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "catalog item '%s' has no template", catalogItemRefStr)
	}
	cluster.GetSpec().SetTemplate(templateRef)

	if err := applyFieldDefinitions(cluster.GetSpec(), catalogItem.GetFieldDefinitions()); err != nil {
		return err
	}

	// Look up the template to apply spec defaults, node sets, and parameter validation:
	templateRefStr := refKey(templateRef)
	template, err := s.lookupTemplate(ctx, templateRefStr)
	if err != nil {
		return err
	}
	if template.GetMetadata().HasDeletionTimestamp() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "template '%s' has been deleted", templateRefStr)
	}

	resolvedTemplateRef := &privatev1.ClusterTemplateReference{}
	resolvedTemplateRef.SetId(template.GetId())
	resolvedTemplateRef.SetName(template.GetMetadata().GetName())
	cluster.GetSpec().SetTemplate(resolvedTemplateRef)

	// Apply spec defaults from the template (user and field_definition values take precedence):
	utils.ApplyClusterSpecDefaults(cluster.GetSpec(), template.GetSpecDefaults())

	if err := s.validatePullSecretMutualExclusion(cluster.GetSpec()); err != nil {
		return err
	}
	if err := s.validatePullSecretSecret(ctx, cluster.GetSpec()); err != nil {
		return err
	}

	// Version resolution (catalog-item path):
	// user input > field_definition default > template spec_defaults > system default.
	if err := s.ensureClusterVersion(ctx, cluster); err != nil {
		return err
	}

	if err := utils.ValidateClusterSpecFields(cluster.GetSpec()); err != nil {
		return err
	}

	hostTypes, err := s.lookupAndIndexHostTypes(ctx, template)
	if err != nil {
		return err
	}

	templateNodeSets := template.GetNodeSets()
	clusterNodeSets := cluster.GetSpec().GetNodeSets()
	if err := s.validateNodeSets(clusterNodeSets, templateNodeSets, hostTypes, templateRefStr); err != nil {
		return err
	}

	mergeNodeSetsWithTemplate(cluster, templateNodeSets, clusterNodeSets)

	clusterParameters := cluster.GetSpec().GetTemplateParameters()
	if err := utils.ValidateClusterTemplateParameters(template, clusterParameters); err != nil {
		return err
	}

	actualClusterParameters := utils.ProcessTemplateParametersWithDefaults(
		utils.ClusterTemplateAdapter{ClusterTemplate: template},
		clusterParameters,
	)
	cluster.GetSpec().SetTemplateParameters(actualClusterParameters)

	for _, clusterNodeSet := range cluster.GetSpec().GetNodeSets() {
		hostType := hostTypes[refKey(clusterNodeSet.GetHostType())]
		if hostType != nil {
			resolvedHostTypeRef := &privatev1.HostTypeReference{}
			resolvedHostTypeRef.SetId(hostType.GetId())
			resolvedHostTypeRef.SetName(hostType.GetMetadata().GetName())
			clusterNodeSet.SetHostType(resolvedHostTypeRef)
		}
	}

	return nil
}

func (s *PrivateClustersServer) lookupCatalogItem(ctx context.Context,
	key string) (result *privatev1.ClusterCatalogItem, err error) {
	if key == "" {
		return
	}
	response, err := s.catalogItemsDao.List().
		SetFilter(fmt.Sprintf("this.id == %[1]s || this.metadata.name == %[1]s", strconv.Quote(key))).
		SetLimit(1).
		Do(ctx)
	if err != nil {
		var deniedErr *dao.ErrDenied
		if errors.As(err, &deniedErr) {
			err = grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
			return
		}
		s.logger.ErrorContext(ctx, "Failed to lookup catalog item",
			slog.String("key", key),
			slog.Any("error", err))
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to lookup catalog item")
		return
	}
	items := response.GetItems()
	if len(items) == 0 {
		err = grpcstatus.Errorf(grpccodes.NotFound,
			"there is no catalog item with identifier or name '%s'", key)
		return
	}
	result = items[0]
	return
}

func (s *PrivateClustersServer) updateExternalIPAttachedFlag(ctx context.Context, externalIPID string, attached bool) error {
	getResponse, err := s.externalIPDao.Get().
		SetId(externalIPID).
		SetLock(true).
		Do(ctx)
	if err != nil {
		return fmt.Errorf("failed to get ExternalIP for attached flag update: %w", err)
	}

	eip := getResponse.GetObject()
	eip.GetStatus().SetAttached(attached)

	_, err = s.externalIPDao.Update().SetObject(eip).Do(ctx)
	if err != nil {
		return fmt.Errorf("failed to update ExternalIP attached flag: %w", err)
	}

	return nil
}
