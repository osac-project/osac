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
	"maps"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/utils"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const bareMetalInstanceUserDataMaxBytes = 64 * 1024

func validateBareMetalUserData(userData []byte) error {
	if len(userData) > bareMetalInstanceUserDataMaxBytes {
		return fmt.Errorf("size %d exceeds the maximum of %d bytes", len(userData), bareMetalInstanceUserDataMaxBytes)
	}
	return nil
}

type PrivateBareMetalInstancesServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
	secretStore       vault.SecretStore
}

var _ privatev1.BareMetalInstancesServer = (*PrivateBareMetalInstancesServer)(nil)

type PrivateBareMetalInstancesServer struct {
	privatev1.UnimplementedBareMetalInstancesServer
	logger                  *slog.Logger
	tenancyLogic            auth.TenancyLogic
	generic                 *GenericServer[*privatev1.BareMetalInstance]
	catalogItemsDao         *dao.GenericDAO[*privatev1.BareMetalInstanceCatalogItem]
	templatesDao            *dao.GenericDAO[*privatev1.BareMetalInstanceTemplate]
	hostTypesDao            *dao.GenericDAO[*privatev1.HostType]
	instanceTypesDao        *dao.GenericDAO[*privatev1.BareMetalInstanceType]
	subnetsDao              *dao.GenericDAO[*privatev1.Subnet]
	virtualNetworksDao      *dao.GenericDAO[*privatev1.VirtualNetwork]
	networkClassesDao       *dao.GenericDAO[*privatev1.NetworkClass]
	securityGroupsDao       *dao.GenericDAO[*privatev1.SecurityGroup]
	diskImagesDao           *dao.GenericDAO[*privatev1.DiskImage]
	externalIPPoolDao       *dao.GenericDAO[*privatev1.ExternalIPPool]
	externalIPDao           *dao.GenericDAO[*privatev1.ExternalIP]
	externalIPAttachmentDao *dao.GenericDAO[*privatev1.ExternalIPAttachment]
	secretsDao              *dao.GenericDAO[*privatev1.Secret]
	secretStore             vault.SecretStore
	lifecycle               *externalIPLifecycle
}

func NewPrivateBareMetalInstancesServer() *PrivateBareMetalInstancesServerBuilder {
	return &PrivateBareMetalInstancesServerBuilder{}
}

func (b *PrivateBareMetalInstancesServerBuilder) SetLogger(value *slog.Logger) *PrivateBareMetalInstancesServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateBareMetalInstancesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateBareMetalInstancesServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateBareMetalInstancesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateBareMetalInstancesServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateBareMetalInstancesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateBareMetalInstancesServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateBareMetalInstancesServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateBareMetalInstancesServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateBareMetalInstancesServerBuilder) SetSecretStore(value vault.SecretStore) *PrivateBareMetalInstancesServerBuilder {
	b.secretStore = value
	return b
}

func (b *PrivateBareMetalInstancesServerBuilder) Build() (result *PrivateBareMetalInstancesServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	if b.attributionLogic == nil {
		err = errors.New("attribution logic is mandatory")
		return
	}

	catalogItemsDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstanceCatalogItem]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	templatesDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstanceTemplate]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	instanceTypesDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstanceType]().SetLogger(b.logger).SetTenancyLogic(b.tenancyLogic).SetMetricsRegisterer(b.metricsRegisterer).Build()
	if err != nil {
		return
	}

	hostTypesDao, err := dao.NewGenericDAO[*privatev1.HostType]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	subnetsDao, err := dao.NewGenericDAO[*privatev1.Subnet]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	virtualNetworksDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	networkClassesDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	securityGroupsDao, err := dao.NewGenericDAO[*privatev1.SecurityGroup]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	diskImagesDao, err := dao.NewGenericDAO[*privatev1.DiskImage]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	externalIPPoolDaoBuilder := dao.NewGenericDAO[*privatev1.ExternalIPPool]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer)
	externalIPPoolDao, err := externalIPPoolDaoBuilder.Build()
	if err != nil {
		return
	}

	externalIPDaoBuilder := dao.NewGenericDAO[*privatev1.ExternalIP]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer)
	externalIPDao, err := externalIPDaoBuilder.Build()
	if err != nil {
		return
	}

	externalIPAttachmentDaoBuilder := dao.NewGenericDAO[*privatev1.ExternalIPAttachment]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer)
	externalIPAttachmentDao, err := externalIPAttachmentDaoBuilder.Build()
	if err != nil {
		return
	}

	secretsDao, err := dao.NewGenericDAO[*privatev1.Secret]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	generic, err := NewGenericServer[*privatev1.BareMetalInstance]().
		SetLogger(b.logger).
		SetService(privatev1.BareMetalInstances_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}

	result = &PrivateBareMetalInstancesServer{
		logger:                  b.logger,
		tenancyLogic:            b.tenancyLogic,
		generic:                 generic,
		catalogItemsDao:         catalogItemsDao,
		templatesDao:            templatesDao,
		hostTypesDao:            hostTypesDao,
		instanceTypesDao:        instanceTypesDao,
		subnetsDao:              subnetsDao,
		virtualNetworksDao:      virtualNetworksDao,
		networkClassesDao:       networkClassesDao,
		securityGroupsDao:       securityGroupsDao,
		diskImagesDao:           diskImagesDao,
		externalIPPoolDao:       externalIPPoolDao,
		externalIPDao:           externalIPDao,
		externalIPAttachmentDao: externalIPAttachmentDao,
		secretsDao:              secretsDao,
		secretStore:             b.secretStore,
	}
	result.lifecycle = newExternalIPLifecycle(
		externalIPDao,
		externalIPAttachmentDao,
		nil,
		externalIPPoolDao,
		nil,
		nil,
		generic.dao,
		nil,
	)
	return
}

func (s *PrivateBareMetalInstancesServer) List(ctx context.Context,
	request *privatev1.BareMetalInstancesListRequest) (response *privatev1.BareMetalInstancesListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateBareMetalInstancesServer) Get(ctx context.Context,
	request *privatev1.BareMetalInstancesGetRequest) (response *privatev1.BareMetalInstancesGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateBareMetalInstancesServer) Create(ctx context.Context, request *privatev1.BareMetalInstancesCreateRequest) (response *privatev1.BareMetalInstancesCreateResponse, err error) {
	var warnings []string
	err = s.generic.CreateWithCandidatePreparation(ctx, request, &response, func(ctx context.Context, _ *privatev1.BareMetalInstance, candidate *privatev1.BareMetalInstance) error {
		warnings, err = s.prepareCreate(ctx, candidate)
		return err
	})
	if err != nil {
		return
	}
	if !isDryRun(ctx) && response.GetObject().GetSpec().GetAutoExternalIpAttachment() {
		err = s.autoProvisionExternalIP(ctx, response.GetObject())
		if err != nil {
			if tx, txErr := database.TxFromContext(ctx); txErr == nil {
				tx.ReportError(&err)
			}
			return
		}
	}
	response.SetWarnings(warnings)
	return
}

// prepareCreate fills the new bare metal instance before it is stored. It selects either a
// published Catalog Item or a direct Template, applies Catalog rules and Template defaults,
// then checks the resulting image, network, and other required inputs.
func (s *PrivateBareMetalInstancesServer) prepareCreate(ctx context.Context, candidate *privatev1.BareMetalInstance) (warnings []string, err error) {
	template, err := s.resolveCreationSource(ctx, candidate)
	if err != nil {
		return nil, err
	}
	if err = s.applyBareMetalTemplate(candidate, template); err != nil {
		return
	}
	if err = s.validateAndResolveUserDataSecret(ctx, candidate.GetSpec(), true); err != nil {
		return
	}

	if err = s.validateSpec(candidate); err != nil {
		return
	}
	hostType, err := s.loadHostTypeForNetworkAttachments(ctx, template)
	if err != nil {
		return
	}
	if err = s.applyDefaultNetworkAttachments(ctx, candidate, hostType); err != nil {
		return
	}
	if err = s.validateNetworkAttachments(ctx, candidate, hostType); err != nil {
		return
	}
	normalizeSoleBareMetalAttachmentPrimary(candidate.GetSpec().GetNetworkAttachments())
	if ref := candidate.GetSpec().GetInstanceType(); ref != nil {
		if _, err = resolveAndCanonicalizeReference(ctx, s.instanceTypesDao, candidate.GetMetadata(), ref, "bare metal instance type", grpccodes.InvalidArgument); err != nil {
			return
		}
	}
	for i, attachment := range candidate.GetSpec().GetNetworkAttachments() {
		source := fmt.Sprintf(" in spec.network_attachments[%d]", i)
		subnet, resolveErr := resolveAndCanonicalizeReference(ctx, s.subnetsDao, candidate.GetMetadata(), attachment.GetSubnet(), "subnet", grpccodes.InvalidArgument)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if err = validateResolvedSubnetReady(subnet, refKey(attachment.GetSubnet()), source); err != nil {
			return
		}
		for _, ref := range attachment.GetSecurityGroups() {
			group, resolveErr := resolveAndCanonicalizeReference(ctx, s.securityGroupsDao, candidate.GetMetadata(), ref, "security group", grpccodes.InvalidArgument)
			if resolveErr != nil {
				return nil, resolveErr
			}
			if err = validateResolvedSecurityGroup(group, refKey(ref), source, refKey(subnet.GetSpec().GetVirtualNetwork())); err != nil {
				return
			}
		}
		if err = validateBareMetalSubnetFabricManager(ctx, subnet, fmt.Sprintf("network_attachments[%d]", i), s.virtualNetworksDao, s.networkClassesDao, s.logger); err != nil {
			return
		}
	}

	if ref := candidate.GetSpec().GetDiskImage(); ref != nil {
		if refKey(ref) == "" {
			return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "disk_image is mandatory")
		}
		resolved, err := resolveLockedDiskImageReference(ctx, s.diskImagesDao, referenceScope{tenant: candidate.GetMetadata().GetTenant(), project: candidate.GetMetadata().GetProject()}, ref, "")
		if err != nil {
			return nil, err
		}
		candidate.GetSpec().SetDiskImage(canonicalDiskImageReference(resolved))
		return validateResolvedDiskImage(resolved, refKey(ref), "")
	}

	return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "disk_image is mandatory")
}

// resolveCreationSource accepts exactly one provisioning source: spec.catalog_item or
// spec.template. For a Catalog Item it finds the item's Template and applies its field rules;
// for a direct Template it resolves that reference under the instance's assigned tenant/project.
func (s *PrivateBareMetalInstancesServer) resolveCreationSource(ctx context.Context,
	candidate *privatev1.BareMetalInstance) (*privatev1.BareMetalInstanceTemplate, error) {
	spec := candidate.GetSpec()
	if spec.GetCatalogItem() != nil && spec.GetTemplate() != nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
			"catalog_item and template are mutually exclusive")
	}
	if spec.GetCatalogItem() != nil {
		return s.resolveCatalogItem(ctx, candidate)
	}
	if spec.GetTemplate() == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
			"either catalog_item or template is required")
	}
	return resolveAndCanonicalizeReference(ctx, s.templatesDao, candidate.GetMetadata(), spec.GetTemplate(),
		"template", grpccodes.NotFound)
}

func (s *PrivateBareMetalInstancesServer) Update(ctx context.Context,
	request *privatev1.BareMetalInstancesUpdateRequest) (response *privatev1.BareMetalInstancesUpdateResponse, err error) {
	err = s.generic.UpdateWithCandidatePreparation(ctx, request, &response, func(ctx context.Context, current, candidate *privatev1.BareMetalInstance) error {
		if err := validateBareMetalImmutability(current, candidate, request.GetUpdateMask()); err != nil {
			return err
		}
		if err := s.validateAndResolveUserDataSecret(
			ctx,
			candidate.GetSpec(),
			updateIncludesField(request.GetUpdateMask(), "spec.user_data_secret"),
		); err != nil {
			return err
		}
		return nil
	})
	return
}

func (s *PrivateBareMetalInstancesServer) validateAndResolveUserDataSecret(
	ctx context.Context,
	spec *privatev1.BareMetalInstanceSpec,
	resolve bool,
) error {
	if spec.HasUserData() && spec.GetUserDataSecret() != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"user_data and user_data_secret are mutually exclusive")
	}
	if !resolve || spec.GetUserDataSecret() == nil {
		return nil
	}
	resolved, err := validateUserDataSecret(
		ctx, s.logger, s.secretsDao, s.secretStore, spec.GetUserDataSecret(),
	)
	if err != nil {
		return err
	}
	spec.SetUserDataSecret(resolved)
	return nil
}

func (s *PrivateBareMetalInstancesServer) Delete(ctx context.Context,
	request *privatev1.BareMetalInstancesDeleteRequest) (response *privatev1.BareMetalInstancesDeleteResponse, err error) {
	id := request.GetId()
	if id != "" {
		getResponse, getErr := s.generic.dao.Get().SetId(id).Do(ctx)
		if getErr != nil {
			var notFoundErr *dao.ErrNotFound
			if !errors.As(getErr, &notFoundErr) {
				err = getErr
				return
			}
			s.logger.DebugContext(ctx, "BMI not found during delete, skipping auto-EIP cleanup",
				slog.String("bmi_id", id))
		} else if getResponse.GetObject().GetSpec().GetAutoExternalIpAttachment() {
			s.logger.InfoContext(ctx, "BMI has auto_external_ip_attachment, running cascade cleanup",
				slog.String("bmi_id", id))
			err = s.autoCleanupExternalIP(ctx, id)
			if err != nil {
				s.logger.ErrorContext(ctx, "Auto-EIP cascade cleanup failed",
					slog.String("bmi_id", id), slog.Any("error", err))
				return
			}
		} else {
			s.logger.DebugContext(ctx, "BMI does not have auto_external_ip_attachment",
				slog.String("bmi_id", id))
		}
	}
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateBareMetalInstancesServer) autoCleanupExternalIP(ctx context.Context, bmiID string) error {
	filter := fmt.Sprintf(
		"this.metadata.labels['%s'] == '%s'",
		autoCreatedForLabel, bmiID,
	)
	s.logger.InfoContext(ctx, "Auto-EIP cleanup: listing attachments",
		slog.String("bmi_id", bmiID), slog.String("filter", filter))

	listResp, err := s.externalIPAttachmentDao.List().SetFilter(filter).Do(ctx)
	if err != nil {
		return fmt.Errorf("auto_external_ip_attachment cleanup: failed to list attachments: %w", err)
	}

	items := listResp.GetItems()
	s.logger.InfoContext(ctx, "Auto-EIP cleanup: found attachments",
		slog.String("bmi_id", bmiID), slog.Int("count", len(items)))

	for _, attachment := range items {
		attachmentID := attachment.GetId()
		eipRef := attachment.GetSpec().GetExternalIp()
		eipID := refKey(eipRef)
		s.logger.InfoContext(ctx, "Auto-EIP cleanup: deleting attachment",
			slog.String("attachment_id", attachmentID), slog.String("eip_id", eipID))

		if eipID != "" {
			err = s.lifecycle.deleteAttachmentAndExternalIP(ctx, attachmentID, eipID)
			if err != nil {
				return fmt.Errorf("auto_external_ip_attachment cleanup: %w", err)
			}
			s.logger.InfoContext(ctx, "Auto-EIP cleanup: deleted attachment and EIP",
				slog.String("attachment_id", attachmentID), slog.String("eip_id", eipID))
		} else {
			if err = s.lifecycle.deleteAttachment(ctx, attachmentID); err != nil {
				return fmt.Errorf("auto_external_ip_attachment cleanup: %w", err)
			}
		}
	}

	if len(items) == 0 {
		s.logger.WarnContext(ctx, "Auto-EIP cleanup: no attachments found for BMI",
			slog.String("bmi_id", bmiID), slog.String("label", autoCreatedForLabel))
	}

	return nil
}

func (s *PrivateBareMetalInstancesServer) Signal(ctx context.Context,
	request *privatev1.BareMetalInstancesSignalRequest) (response *privatev1.BareMetalInstancesSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

// validateSpec validates fields on the bare metal instance spec that are checked at create time.
func (s *PrivateBareMetalInstancesServer) validateSpec(bmi *privatev1.BareMetalInstance) error {
	if bmi == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "bare metal instance is mandatory")
	}
	spec := bmi.GetSpec()
	if spec == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "bare metal instance spec is mandatory")
	}

	if key := spec.GetSshPublicKey(); key != "" {
		if err := validateOpenSSHPublicKey(key); err != nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument, "spec.ssh_public_key: %s", err)
		}
	}

	if spec.HasUserData() {
		if err := validateBareMetalUserData([]byte(spec.GetUserData())); err != nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument, "spec.user_data: %s", err)
		}
	}

	// If none of the authentication inputs are set, the server cannot be accessed after deployment.
	if spec.GetSshPublicKey() == "" && spec.GetUserData() == "" && spec.GetUserDataSecret() == nil {
		return grpcstatus.Error(grpccodes.InvalidArgument,
			"at least one authentication method must be provided: spec.ssh_public_key, spec.user_data or spec.user_data_secret")
	}

	return nil
}

// applyDefaultNetworkAttachments completes network_attachments at Create time.
// len==0 injects a sole tenant-default attachment; len==1 fills missing subnet /
// empty security_groups / interface without overwriting supplied values; len>1 is a no-op.
// hostType is the HostType for candidate's template when present (may be nil).
func (s *PrivateBareMetalInstancesServer) applyDefaultNetworkAttachments(
	ctx context.Context, bmi *privatev1.BareMetalInstance, hostType *privatev1.HostType) error {
	attachments := bmi.GetSpec().GetNetworkAttachments()
	if len(attachments) > 1 {
		return nil
	}
	if len(attachments) == 1 && attachments[0] != nil &&
		refKey(attachments[0].GetSubnet()) != "" &&
		!securityGroupsMissing(attachments[0]) &&
		attachments[0].GetInterface() != "" {
		return nil
	}

	tenantName := bmi.GetMetadata().GetTenant()
	if tenantName == "" {
		var err error
		tenantName, err = s.tenancyLogic.DetermineDefaultTenant(ctx)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to determine default tenant for network attachment defaults",
				slog.Any("error", err))
			return grpcstatus.Errorf(grpccodes.Internal, "failed to determine tenant")
		}
	}
	project := bmi.GetMetadata().GetProject()

	var defaultSubnet *privatev1.Subnet
	if len(attachments) == 0 || needsDefaultSubnetLookup(attachments[0]) {
		var err error
		defaultSubnet, err = findDefaultSubnet(ctx, s.logger, s.subnetsDao, tenantName, project)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to look up default subnet",
				slog.String("tenant", tenantName), slog.Any("error", err))
			return grpcstatus.Errorf(grpccodes.Internal, "failed to find default subnet")
		}
	}
	defaultVN := ""
	if defaultSubnet != nil {
		defaultVN = refKey(defaultSubnet.GetSpec().GetVirtualNetwork())
	}

	if len(attachments) == 0 {
		return s.injectFullDefaultNetworkAttachment(ctx, bmi, tenantName, project, defaultSubnet, hostType)
	}

	attachment := attachments[0]
	if attachment == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "network_attachments[0]: attachment cannot be null")
	}

	var resolvedSubnet *privatev1.Subnet
	var err error
	if attachment.GetSubnet() == nil || refKey(attachment.GetSubnet()) == "" {
		if defaultSubnet == nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"spec.network_attachments: subnet is required and no tenant default subnet is available")
		}
		attachment.SetSubnet(privatev1.SubnetLocalReference_builder{Id: defaultSubnet.GetId()}.Build())
		resolvedSubnet = defaultSubnet
	} else if securityGroupsMissing(attachment) {
		resolvedSubnet, err = resolveAndCanonicalizeReference(ctx, s.subnetsDao, bmi.GetMetadata(),
			attachment.GetSubnet(), "subnet", grpccodes.InvalidArgument)
		if err != nil {
			return err
		}
	}

	if securityGroupsMissing(attachment) {
		subnetVN := refKey(resolvedSubnet.GetSpec().GetVirtualNetwork())
		if !subnetOnDefaultVirtualNetwork(subnetVN, defaultVN) {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"spec.network_attachments[0]: security_groups are required when the subnet is not on the tenant default virtual network")
		}
		sg, sgErr := findDefaultSecurityGroup(ctx, s.logger, s.securityGroupsDao, subnetVN, tenantName, project)
		if sgErr != nil {
			s.logger.ErrorContext(ctx, "Failed to look up default security group",
				slog.String("tenant", tenantName), slog.Any("error", sgErr))
			return grpcstatus.Errorf(grpccodes.Internal, "failed to find default security group")
		}
		if sg == nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"spec.network_attachments[0]: security_groups are required and no tenant default security group is available")
		}
		attachment.SetSecurityGroups([]*privatev1.SecurityGroupLocalReference{
			privatev1.SecurityGroupLocalReference_builder{Id: sg.GetId()}.Build(),
		})
	}

	if attachment.GetInterface() == "" {
		ifaceName, ifaceErr := s.resolveDefaultInterface(hostType)
		if ifaceErr != nil {
			return ifaceErr
		}
		if ifaceName != "" {
			attachment.SetInterface(ifaceName)
		}
	}

	bmi.GetSpec().SetNetworkAttachments([]*privatev1.BareMetalNetworkAttachment{attachment})
	return nil
}

func needsDefaultSubnetLookup(a *privatev1.BareMetalNetworkAttachment) bool {
	if a == nil {
		return true
	}
	if a.GetSubnet() == nil || refKey(a.GetSubnet()) == "" {
		return true
	}
	return securityGroupsMissing(a)
}

func (s *PrivateBareMetalInstancesServer) injectFullDefaultNetworkAttachment(
	ctx context.Context, bmi *privatev1.BareMetalInstance, tenantName, project string,
	defaultSubnet *privatev1.Subnet, hostType *privatev1.HostType) error {
	if defaultSubnet == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"spec.network_attachments: at least one network attachment is required and no tenant default subnet is available")
	}
	subnetVN := refKey(defaultSubnet.GetSpec().GetVirtualNetwork())
	sg, err := findDefaultSecurityGroup(ctx, s.logger, s.securityGroupsDao, subnetVN, tenantName, project)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to look up default security group",
			slog.String("tenant", tenantName), slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to find default security group")
	}
	if sg == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"spec.network_attachments: no tenant default security group is available")
	}

	ifaceName, err := s.resolveDefaultInterface(hostType)
	if err != nil {
		return err
	}

	attachment := privatev1.BareMetalNetworkAttachment_builder{
		Subnet: privatev1.SubnetLocalReference_builder{Id: defaultSubnet.GetId()}.Build(),
		SecurityGroups: []*privatev1.SecurityGroupLocalReference{
			privatev1.SecurityGroupLocalReference_builder{Id: sg.GetId()}.Build(),
		},
	}.Build()
	if ifaceName != "" {
		attachment.SetInterface(ifaceName)
	}

	bmi.GetSpec().SetNetworkAttachments([]*privatev1.BareMetalNetworkAttachment{attachment})
	return nil
}

func securityGroupsMissing(a *privatev1.BareMetalNetworkAttachment) bool {
	return a == nil || len(a.GetSecurityGroups()) == 0
}

func subnetOnDefaultVirtualNetwork(subnetVN, defaultSubnetVN string) bool {
	return subnetVN != "" && defaultSubnetVN != "" && subnetVN == defaultSubnetVN
}

// loadHostTypeForNetworkAttachments returns the HostType referenced by template, or nil when
// the template has no host_type. Missing HostType rows yield InvalidArgument.
func (s *PrivateBareMetalInstancesServer) loadHostTypeForNetworkAttachments(
	ctx context.Context, template *privatev1.BareMetalInstanceTemplate) (*privatev1.HostType, error) {
	if template == nil {
		return nil, nil
	}
	hostTypeID := template.GetHostType()
	if hostTypeID == "" {
		return nil, nil
	}
	htResp, err := s.hostTypesDao.Get().SetId(hostTypeID).Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
				"host type '%s' referenced by template '%s' not found", hostTypeID, template.GetId())
		}
		s.logger.ErrorContext(ctx, "Failed to lookup host type",
			slog.String("host_type_id", hostTypeID), slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to lookup host type")
	}
	return htResp.GetObject(), nil
}

// resolveDefaultInterface returns the first fabric-role interface name from hostType.
// Returns ("", nil) when hostType is nil. Returns an error when hostType has no fabric-role interface.
func (s *PrivateBareMetalInstancesServer) resolveDefaultInterface(hostType *privatev1.HostType) (string, error) {
	if hostType == nil {
		return "", nil
	}
	for _, ni := range hostType.GetInterfaces() {
		if strings.EqualFold(ni.GetRole(), "fabric") {
			return ni.GetName(), nil
		}
	}
	return "", grpcstatus.Errorf(grpccodes.FailedPrecondition,
		"host type '%s' has no fabric-role interface for default network attachment", hostType.GetId())
}

// resolveCatalogItem finds the instance's published Catalog Item in the selected tenant/project
// or shared scope, then finds the item's Template under the item's ownership. It applies locked
// and editable field and parameter rules and returns that Template for defaults.
func (s *PrivateBareMetalInstancesServer) resolveCatalogItem(ctx context.Context,
	bmi *privatev1.BareMetalInstance) (*privatev1.BareMetalInstanceTemplate, error) {
	if bmi == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "bare metal instance is mandatory")
	}
	catalogItemRef := bmi.GetSpec().GetCatalogItem()
	if catalogItemRef == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "catalog_item is mandatory")
	}
	catalogItemRefStr := refKey(catalogItemRef)

	item, err := resolveAndCanonicalizeLockedReference(ctx, s.catalogItemsDao, bmi.GetMetadata(), catalogItemRef, "catalog item", grpccodes.NotFound)
	if err != nil {
		return nil, err
	}

	if err := validateCatalogItemForCreation(item, catalogItemRefStr); err != nil {
		return nil, err
	}

	templateRef := item.GetTemplate()
	if templateRef == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
			"catalog item '%s' does not reference a template", catalogItemRefStr)
	}
	templateRef = cloneMessage(templateRef)
	resolvedTemplate, resolveErr := resolveAndCanonicalizeLockedReference(ctx, s.templatesDao, item.GetMetadata(), templateRef, "template", grpccodes.NotFound)
	if resolveErr != nil {
		return nil, resolveErr
	}

	if err := applyBareMetalInstanceCatalogItemPolicies(bmi.GetSpec(), item.GetFields()); err != nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "%s", err)
	}
	parameters, parameterErr := applyCatalogItemTemplateParameterPolicies(
		utils.BareMetalInstanceTemplateAdapter{BareMetalInstanceTemplate: resolvedTemplate},
		item.GetTemplateParameters(), bmi.GetSpec().GetTemplateParameters())
	if parameterErr != nil {
		return nil, parameterErr
	}
	bmi.GetSpec().SetTemplateParameters(parameters)
	return resolvedTemplate, nil
}

// applyBareMetalTemplate validates the instance's Template parameters, fills omitted parameter
// values from the Template, and stores the Template's actual ID, name, and scope.
func (s *PrivateBareMetalInstancesServer) applyBareMetalTemplate(bmi *privatev1.BareMetalInstance, template *privatev1.BareMetalInstanceTemplate) error {
	providedParams := bmi.GetSpec().GetTemplateParameters()
	if len(template.GetParameters()) != 0 || len(providedParams) != 0 {
		actualParams, err := utils.ApplyTemplateParameterDefaultsAndValidate(
			utils.BareMetalInstanceTemplateAdapter{BareMetalInstanceTemplate: template}, providedParams,
		)
		if err != nil {
			return err
		}
		bmi.GetSpec().SetTemplateParameters(actualParams)
	}

	bmi.GetSpec().SetTemplate(canonicalBareMetalInstanceTemplateReference(template))
	return nil
}

// validateBareMetalImmutability ensures template, catalog_item, disk_image, ssh_public_key, user_data, template_parameters,
// and auto_external_ip_attachment cannot be changed after creation.
func validateBareMetalImmutability(
	current, candidate *privatev1.BareMetalInstance,
	mask *fieldmaskpb.FieldMask,
) error {
	updatingTemplate := updateIncludesField(mask, "spec.template")
	updatingCatalogItem := updateIncludesField(mask, "spec.catalog_item")
	updatingDiskImage := updateIncludesField(mask, "spec.disk_image")
	updatingSshKey := updateIncludesField(mask, "spec.ssh_public_key")
	updatingUserData := updateIncludesField(mask, "spec.user_data")
	updatingUserDataSecret := updateIncludesField(mask, "spec.user_data_secret")
	updatingTemplateParams := updateIncludesField(mask, "spec.template_parameters")
	updatingAutoExternalIP := updateIncludesField(mask, "spec.auto_external_ip_attachment")
	updatingNetworkAttachments := updateIncludesField(mask, "spec.network_attachments")

	newSpec := candidate.GetSpec()
	existingSpec := current.GetSpec()
	if existingSpec == nil {
		return grpcstatus.Errorf(grpccodes.Internal, "stored bare metal instance is missing spec")
	}

	if updatingTemplate && refKey(existingSpec.GetTemplate()) != refKey(newSpec.GetTemplate()) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"cannot change spec.template from '%s' to '%s': template is immutable",
			refKey(existingSpec.GetTemplate()), refKey(newSpec.GetTemplate()))
	}

	if updatingCatalogItem {
		ref, err := preserveCatalogItemProvenance(existingSpec.GetCatalogItem(), newSpec.GetCatalogItem(), mask)
		if err != nil {
			return err
		}
		newSpec.SetCatalogItem(ref)
	}
	if updatingDiskImage && !proto.Equal(existingSpec.GetDiskImage(), newSpec.GetDiskImage()) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"cannot change spec.disk_image from '%s' to '%s': disk image is immutable",
			refKey(existingSpec.GetDiskImage()), refKey(newSpec.GetDiskImage()))
	}

	if updatingSshKey && existingSpec.GetSshPublicKey() != newSpec.GetSshPublicKey() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"cannot change spec.ssh_public_key: ssh_public_key is immutable after creation")
	}

	if err := validateBareMetalUserDataImmutability(
		existingSpec, newSpec, updatingUserData, updatingUserDataSecret,
	); err != nil {
		return err
	}

	if updatingTemplateParams {
		templateParamsEqual := func(first, second *anypb.Any) bool {
			return proto.Equal(first, second)
		}
		if !maps.EqualFunc(existingSpec.GetTemplateParameters(), newSpec.GetTemplateParameters(), templateParamsEqual) {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"cannot change spec.template_parameters: template parameters are immutable")
		}
	}

	if updatingAutoExternalIP && existingSpec.GetAutoExternalIpAttachment() != newSpec.GetAutoExternalIpAttachment() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"cannot change spec.auto_external_ip_attachment: auto_external_ip_attachment is immutable after creation")
	}

	if updatingNetworkAttachments {
		if err := compareNetworkAttachmentsImmutability(existingSpec.GetNetworkAttachments(), newSpec.GetNetworkAttachments()); err != nil {
			return err
		}
	}

	return nil
}

func validateBareMetalUserDataImmutability(
	existingSpec, newSpec *privatev1.BareMetalInstanceSpec,
	updatingUserData, updatingUserDataSecret bool,
) error {
	isAtomicMigration := updatingUserData && updatingUserDataSecret &&
		existingSpec.HasUserData() && !newSpec.HasUserData() &&
		existingSpec.GetUserDataSecret() == nil && newSpec.GetUserDataSecret() != nil
	if updatingUserData && existingSpec.GetUserData() != newSpec.GetUserData() && !isAtomicMigration {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"cannot change spec.user_data: user_data is immutable after creation")
	}
	if updatingUserDataSecret && existingSpec.GetUserDataSecret() != nil &&
		!proto.Equal(existingSpec.GetUserDataSecret(), newSpec.GetUserDataSecret()) &&
		!isAtomicMigration {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"cannot change spec.user_data_secret: user_data_secret is immutable after creation")
	}
	return nil
}

func compareNetworkAttachmentsImmutability(existing, updated []*privatev1.BareMetalNetworkAttachment) error {
	if len(existing) != len(updated) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"cannot change number of network attachments from %d to %d: network_attachments structure is immutable",
			len(existing), len(updated))
	}
	for i := range existing {
		if refKey(existing[i].GetSubnet()) != refKey(updated[i].GetSubnet()) {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"cannot change network_attachments[%d].subnet from '%s' to '%s': subnet is immutable",
				i, refKey(existing[i].GetSubnet()), refKey(updated[i].GetSubnet()))
		}
		if existing[i].GetInterface() != updated[i].GetInterface() {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"cannot change network_attachments[%d].interface from '%s' to '%s': interface is immutable",
				i, existing[i].GetInterface(), updated[i].GetInterface())
		}
		if existing[i].GetPrimary() != updated[i].GetPrimary() {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"cannot change network_attachments[%d].primary: primary is immutable after creation", i)
		}
	}
	return nil
}

func (s *PrivateBareMetalInstancesServer) validateNetworkAttachments(
	ctx context.Context, bmi *privatev1.BareMetalInstance, hostType *privatev1.HostType) error {
	attachments := bmi.GetSpec().GetNetworkAttachments()
	if err := validateBareMetalNetworkAttachmentStructure("", attachments); err != nil {
		return err
	}
	if len(attachments) == 0 {
		return nil
	}
	if hostType == nil {
		if templateID := refKey(bmi.GetSpec().GetTemplate()); templateID != "" {
			s.logger.WarnContext(ctx, "Template has no host_type, skipping interface validation",
				slog.String("template_id", templateID))
		}
		return nil
	}
	return validateBareMetalAttachmentsForHostType("", attachments, hostType)
}

func validateBareMetalNetworkAttachmentStructure(source string, attachments []*privatev1.BareMetalNetworkAttachment) error {
	prefix := ""
	if source != "" {
		prefix = fmt.Sprintf("field '%s': ", source)
	}

	if len(attachments) > 1 {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"%sat most one network attachment is supported", prefix)
	}

	for i, a := range attachments {
		if a == nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument, "%snetwork_attachments[%d]: attachment cannot be null", prefix, i)
		}
		if a.GetSubnet() == nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument, "%snetwork_attachments[%d]: subnet is required", prefix, i)
		}
		if a.HasPrimary() && !a.GetPrimary() {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"%snetwork_attachments[%d]: primary: false is not supported; omit primary or set primary: true", prefix, i)
		}
	}
	return nil
}

func normalizeSoleBareMetalAttachmentPrimary(attachments []*privatev1.BareMetalNetworkAttachment) {
	if len(attachments) != 1 || attachments[0] == nil {
		return
	}
	attachments[0].SetPrimary(true)
}

func validateBareMetalAttachmentsForHostType(source string, attachments []*privatev1.BareMetalNetworkAttachment, hostType *privatev1.HostType) error {
	prefix := ""
	if source != "" {
		prefix = fmt.Sprintf("field '%s': ", source)
	}
	hostTypeID := hostType.GetId()
	// Keep lifecycle interfaces visible for precise errors but exclude them from tenant-network capacity.
	interfaceRoles := make(map[string]string)
	validInterfaces := make(map[string]bool)
	for _, ni := range hostType.GetInterfaces() {
		interfaceRoles[ni.GetName()] = ni.GetRole()
		if !strings.EqualFold(ni.GetRole(), "lifecycle") {
			validInterfaces[ni.GetName()] = true
		}
	}

	if len(attachments) > len(validInterfaces) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"%snumber of network attachments (%d) exceeds available interfaces (%d) on host type '%s'",
			prefix, len(attachments), len(validInterfaces), hostTypeID)
	}

	for i, a := range attachments {
		iface := a.GetInterface()
		if iface == "" {
			continue
		}
		role, found := interfaceRoles[iface]
		switch {
		case found && strings.EqualFold(role, "lifecycle"):
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"%snetwork_attachments[%d]: interface '%s' has role 'lifecycle' and cannot be used for tenant networking", prefix, i, iface)
		case !validInterfaces[iface]:
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"%snetwork_attachments[%d]: interface '%s' not found in host type '%s'", prefix, i, iface, hostTypeID)
		}
	}

	return nil
}

// validateBareMetalSubnetFabricManager checks that the resolved subnet's NetworkClass
// defines the fabric manager required to provision bare metal networking.
// BareMetalInstance provisioning is a fabric-level operation with no k8sManager fallback.
// Missing downstream dependencies are ignored for compatibility with existing resources and fixtures.
func validateBareMetalSubnetFabricManager(
	ctx context.Context,
	subnet *privatev1.Subnet,
	source string,
	virtualNetworksDao *dao.GenericDAO[*privatev1.VirtualNetwork],
	networkClassesDao *dao.GenericDAO[*privatev1.NetworkClass],
	logger *slog.Logger,
) error {
	// The fabric manager is configured on the NetworkClass reached through the subnet's VirtualNetwork.
	virtualNetworkKey := refKey(subnet.GetSpec().GetVirtualNetwork())
	vnResp, err := virtualNetworksDao.Get().SetId(virtualNetworkKey).Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return nil
		}
		logger.ErrorContext(ctx, "Failed to lookup virtual network for fabric manager validation",
			slog.String("virtual_network_id", virtualNetworkKey), slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to validate network_attachments")
	}

	networkClassKey := refKey(vnResp.GetObject().GetSpec().GetNetworkClass())
	ncResp, err := networkClassesDao.Get().SetId(networkClassKey).Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return nil
		}
		logger.ErrorContext(ctx, "Failed to lookup network class for fabric manager validation",
			slog.String("network_class_id", networkClassKey), slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to validate network_attachments")
	}

	if !ncResp.GetObject().HasFabricManager() {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"%s: subnet '%s' uses NetworkClass '%s' which has no 'fabric_manager'; "+
				"bare metal instances require a fabric manager", source, subnet.GetId(), networkClassKey)
	}
	return nil
}

func (s *PrivateBareMetalInstancesServer) autoProvisionExternalIP(
	ctx context.Context, bmi *privatev1.BareMetalInstance,
) error {
	pool, err := SelectExternalIPPool(ctx, s.externalIPPoolDao, privatev1.IPFamily_IP_FAMILY_UNSPECIFIED)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "auto_external_ip_attachment: %s", err)
	}

	tenant := bmi.GetMetadata().GetTenant()
	bmiID := bmi.GetId()
	shortID := bmiID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	eip := privatev1.ExternalIP_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   fmt.Sprintf("auto-eip-%s", shortID),
			Tenant: tenant,
			Labels: map[string]string{
				autoCreatedLabel:    "true",
				autoCreatedForLabel: bmiID,
			},
			Annotations: map[string]string{
				ownerReferenceAnnotation: bmiID,
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

	err = s.lifecycle.lockNewBareMetalAttachmentReferences(ctx, eipID, bmiID)
	if err != nil {
		return fmt.Errorf("auto_external_ip_attachment: failed to lock attachment references: %w", err)
	}

	err = UpdatePoolCapacity(ctx, s.externalIPPoolDao, pool.GetId(), 1)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "auto_external_ip_attachment: %s", err)
	}

	attachment := privatev1.ExternalIPAttachment_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   fmt.Sprintf("auto-eipa-%s", shortID),
			Tenant: tenant,
			Labels: map[string]string{
				autoCreatedLabel:    "true",
				autoCreatedForLabel: bmiID,
			},
			Annotations: map[string]string{
				ownerReferenceAnnotation: bmiID,
			},
			Creator: "system",
		}.Build(),
		Spec: privatev1.ExternalIPAttachmentSpec_builder{
			ExternalIp:        privatev1.ExternalIPLocalReference_builder{Id: eipID}.Build(),
			BaremetalInstance: privatev1.BareMetalInstanceLocalReference_builder{Id: bmiID}.Build(),
		}.Build(),
		Status: privatev1.ExternalIPAttachmentStatus_builder{
			State: privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING,
		}.Build(),
	}.Build()

	_, err = s.externalIPAttachmentDao.Create().SetObject(attachment).Do(ctx)
	if err != nil {
		return fmt.Errorf("auto_external_ip_attachment: failed to create ExternalIPAttachment: %w", err)
	}

	return nil
}
