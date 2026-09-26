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

	"github.com/prometheus/client_golang/prometheus"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/computeinstancespec"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/utils"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateComputeInstancesServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
	secretStore       vault.SecretStore
}

var _ privatev1.ComputeInstancesServer = (*PrivateComputeInstancesServer)(nil)

type PrivateComputeInstancesServer struct {
	privatev1.UnimplementedComputeInstancesServer

	logger                  *slog.Logger
	tenancyLogic            auth.TenancyLogic
	generic                 *GenericServer[*privatev1.ComputeInstance]
	templatesDao            *dao.GenericDAO[*privatev1.ComputeInstanceTemplate]
	catalogItemsDao         *dao.GenericDAO[*privatev1.ComputeInstanceCatalogItem]
	subnetsDao              *dao.GenericDAO[*privatev1.Subnet]
	securityGroupsDao       *dao.GenericDAO[*privatev1.SecurityGroup]
	instanceTypesDao        *dao.GenericDAO[*privatev1.InstanceType]
	diskImagesDao           *dao.GenericDAO[*privatev1.DiskImage]
	externalIPPoolDao       *dao.GenericDAO[*privatev1.ExternalIPPool]
	externalIPDao           *dao.GenericDAO[*privatev1.ExternalIP]
	externalIPAttachmentDao *dao.GenericDAO[*privatev1.ExternalIPAttachment]
	lifecycle               *externalIPLifecycle
	secretsDao              *dao.GenericDAO[*privatev1.Secret]
	secretStore             vault.SecretStore
	storageTiersDao         *dao.GenericDAO[*privatev1.StorageTier]
}

func NewPrivateComputeInstancesServer() *PrivateComputeInstancesServerBuilder {
	return &PrivateComputeInstancesServerBuilder{}
}

func (b *PrivateComputeInstancesServerBuilder) SetLogger(value *slog.Logger) *PrivateComputeInstancesServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateComputeInstancesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateComputeInstancesServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateComputeInstancesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateComputeInstancesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateComputeInstancesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateComputeInstancesServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateComputeInstancesServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateComputeInstancesServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateComputeInstancesServerBuilder) SetSecretStore(value vault.SecretStore) *PrivateComputeInstancesServerBuilder {
	b.secretStore = value
	return b
}

func (b *PrivateComputeInstancesServerBuilder) Build() (result *PrivateComputeInstancesServer, err error) {
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
	templatesDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceTemplate]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the catalog items DAO:
	catalogItemsDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceCatalogItem]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the Subnets DAO for network validation:
	subnetsDao, err := dao.NewGenericDAO[*privatev1.Subnet]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the SecurityGroups DAO for network validation:
	securityGroupsDao, err := dao.NewGenericDAO[*privatev1.SecurityGroup]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the InstanceTypes DAO for instance type validation:
	instanceTypesDao, err := dao.NewGenericDAO[*privatev1.InstanceType]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the DiskImages DAO for disk image validation:
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

	// Create the generic server:
	generic, err := NewGenericServer[*privatev1.ComputeInstance]().
		SetLogger(b.logger).
		SetService(privatev1.ComputeInstances_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	storageTiersDao, err := dao.NewGenericDAO[*privatev1.StorageTier]().SetLogger(b.logger).SetTenancyLogic(b.tenancyLogic).SetMetricsRegisterer(b.metricsRegisterer).Build()
	if err != nil {
		return
	}
	result = &PrivateComputeInstancesServer{
		storageTiersDao:         storageTiersDao,
		logger:                  b.logger,
		tenancyLogic:            b.tenancyLogic,
		generic:                 generic,
		templatesDao:            templatesDao,
		catalogItemsDao:         catalogItemsDao,
		subnetsDao:              subnetsDao,
		securityGroupsDao:       securityGroupsDao,
		instanceTypesDao:        instanceTypesDao,
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
		generic.dao,
		nil,
		nil,
		nil,
	)
	return
}

func (s *PrivateComputeInstancesServer) List(ctx context.Context,
	request *privatev1.ComputeInstancesListRequest) (response *privatev1.ComputeInstancesListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateComputeInstancesServer) Get(ctx context.Context,
	request *privatev1.ComputeInstancesGetRequest) (response *privatev1.ComputeInstancesGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateComputeInstancesServer) injectDefaultNetworkAttachments(ctx context.Context,
	vm *privatev1.ComputeInstance) error {
	tenant := vm.GetMetadata().GetTenant()
	if tenant == "" {
		var tenantErr error
		tenant, tenantErr = s.tenancyLogic.DetermineDefaultTenant(ctx)
		if tenantErr != nil {
			s.logger.ErrorContext(ctx, "failed to determine target tenant", slog.Any("error", tenantErr))
			return grpcstatus.Errorf(grpccodes.Internal, "failed to determine target tenant")
		}
	}

	spec := vm.GetSpec()
	subnet, err := findDefaultSubnet(ctx, s.logger, s.subnetsDao, tenant, vm.GetMetadata().GetProject())
	if err != nil {
		return grpcstatus.Errorf(grpccodes.Internal, "failed to look up default subnet: %v", err)
	}
	if subnet == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"spec.network_attachments: at least one network attachment is required for new compute instances")
	}

	attachment := privatev1.ComputeNetworkAttachment_builder{
		Subnet: privatev1.SubnetLocalReference_builder{Id: subnet.GetId()}.Build(),
	}.Build()

	virtualNetworkID := refKey(subnet.GetSpec().GetVirtualNetwork())
	sg, err := findDefaultSecurityGroup(ctx, s.logger, s.securityGroupsDao, virtualNetworkID, tenant, vm.GetMetadata().GetProject())
	if err != nil {
		return grpcstatus.Errorf(grpccodes.Internal, "failed to look up default security group: %v", err)
	}
	if sg != nil {
		attachment.SetSecurityGroups([]*privatev1.SecurityGroupLocalReference{
			privatev1.SecurityGroupLocalReference_builder{Id: sg.GetId()}.Build(),
		})
	}

	spec.SetNetworkAttachments([]*privatev1.ComputeNetworkAttachment{attachment})

	attrs := []slog.Attr{
		slog.String("subnet_id", subnet.GetId()),
	}
	if sg != nil {
		attrs = append(attrs, slog.String("security_group_id", sg.GetId()))
	}
	s.logger.LogAttrs(ctx, slog.LevelInfo, "auto-injected default network attachments", attrs...)
	return nil
}

func (s *PrivateComputeInstancesServer) Create(ctx context.Context, request *privatev1.ComputeInstancesCreateRequest) (response *privatev1.ComputeInstancesCreateResponse, err error) {
	var warnings []string
	err = s.generic.CreateWithCandidatePreparation(ctx, request, &response, func(ctx context.Context, _ *privatev1.ComputeInstance, candidate *privatev1.ComputeInstance) error {
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

// prepareCreate fills the new VM before it is stored. It selects either a published Catalog
// Item or a direct Template, applies Catalog rules and Template defaults, then checks the
// resulting image, instance type, network, and other required inputs.
func (s *PrivateComputeInstancesServer) prepareCreate(ctx context.Context, candidate *privatev1.ComputeInstance) (warnings []string, err error) {
	spec := candidate.GetSpec()
	template, err := s.resolveCreationSource(ctx, candidate)
	if err != nil {
		return
	}

	err = s.applyComputeTemplate(candidate, template)
	if err != nil {
		return
	}
	if err = s.validateAndResolveUserDataSecret(ctx, spec, true); err != nil {
		return
	}

	// Apply Catalog rules before adding the tenant's default network. Otherwise a locked
	// network field could mistake the server-provided attachment for a caller override.
	if len(spec.GetNetworkAttachments()) == 0 {
		err = s.injectDefaultNetworkAttachments(ctx, candidate)
		if err != nil {
			return
		}
	}

	// Validate and resolve the final network, including Catalog and Template defaults.
	if err = s.validateNetworkReferencesState(ctx, candidate); err != nil {
		return
	}

	// Validate instance type and disk image existence and lifecycle after defaults are applied.
	warnings, err = s.validateInstanceType(ctx, candidate)
	if err != nil {
		return
	}
	diskImage, diskImageWarnings, err := s.validateDiskImage(ctx, candidate)
	if err != nil {
		return
	}
	warnings = append(warnings, diskImageWarnings...)
	if err = s.validateSshPublicKey(ctx, candidate, diskImage); err != nil {
		return
	}
	err = s.validateStorageTiers(ctx, candidate)
	return
}

// validateStorageTiers checks all disk tiers after Catalog policies and Template defaults
// have been applied. It stores each tier's actual ID and name and rejects inactive
// tiers; volume provisioning chooses the backend later.
func (s *PrivateComputeInstancesServer) validateStorageTiers(ctx context.Context, instance *privatev1.ComputeInstance) error {
	spec := instance.GetSpec()
	disks := append([]*privatev1.ComputeInstanceDisk{spec.GetBootDisk()}, spec.GetAdditionalDisks()...)
	for _, disk := range disks {
		ref := disk.GetStorageTier()
		if ref == nil {
			continue
		}
		tier, err := resolveResourceInScope(ctx, s.storageTiersDao, referenceScope{tenant: auth.SharedTenant}, ref.GetId(), ref.GetName(), "storage tier", " in disks", grpccodes.NotFound)
		if err != nil {
			return err
		}
		if err := validateResolvedStorageTier(tier, " in disks"); err != nil {
			return err
		}
		disk.SetStorageTier(canonicalStorageTierReference(tier))
	}
	return nil
}

// resolveCreationSource accepts exactly one provisioning source: spec.catalog_item or
// spec.template. For a Catalog Item it finds the item's Template and applies its field rules;
// for a direct Template it resolves that reference under the VM's assigned tenant/project.
func (s *PrivateComputeInstancesServer) resolveCreationSource(ctx context.Context,
	candidate *privatev1.ComputeInstance) (*privatev1.ComputeInstanceTemplate, error) {
	spec := candidate.GetSpec()
	if spec.GetCatalogItem() != nil && spec.GetTemplate() != nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
			"catalog_item and template are mutually exclusive")
	}
	if spec.GetCatalogItem() != nil {
		return s.resolveCatalogItem(ctx, candidate)
	}
	if spec.GetTemplate() == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "template is mandatory")
	}
	return resolveAndCanonicalizeReference(ctx, s.templatesDao, candidate.GetMetadata(), spec.GetTemplate(),
		"template", grpccodes.InvalidArgument)
}

func (s *PrivateComputeInstancesServer) Update(ctx context.Context,
	request *privatev1.ComputeInstancesUpdateRequest) (response *privatev1.ComputeInstancesUpdateResponse, err error) {
	computeInstance := request.GetObject()
	if computeInstance == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "compute instance is mandatory")
		return
	}
	if computeInstance.GetId() == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "compute instance id is mandatory")
		return
	}

	var warnings []string
	var resizeNoOp bool
	err = s.generic.UpdateWithCandidatePreparation(ctx, request, &response, func(ctx context.Context, current, candidate *privatev1.ComputeInstance) error {
		if updateIncludesField(request.GetUpdateMask(), "spec.instance_type") {
			warnings, resizeNoOp, err = s.validateInstanceTypeResize(ctx, current, candidate)
			if err != nil {
				return err
			}
		}
		if resizeNoOp && onlyInstanceTypeMask(request.GetUpdateMask()) {
			candidate.GetSpec().SetInstanceType(current.GetSpec().GetInstanceType())
			return nil
		}
		if err := validateComputeInstanceImmutability(current, candidate, request.GetUpdateMask()); err != nil {
			return err
		}
		if err := s.validateAndResolveUserDataSecret(
			ctx,
			candidate.GetSpec(),
			updateIncludesField(request.GetUpdateMask(), "spec.user_data_secret"),
		); err != nil {
			return err
		}
		if updateIncludesField(request.GetUpdateMask(), "spec.network_attachments") {
			// During deletion, keep the existing visibility check without requiring dependencies
			// to remain present or ready. Otherwise resolve and validate the final network once.
			if current.GetMetadata().HasDeletionTimestamp() {
				return s.validateNetworkReferencesTenancy(ctx, candidate)
			}
			return s.validateNetworkReferencesState(ctx, candidate)
		}
		return nil
	})
	if err != nil {
		return
	}
	if len(warnings) > 0 {
		response.SetWarnings(warnings)
	}
	return
}

func onlyInstanceTypeMask(mask *fieldmaskpb.FieldMask) bool {
	paths := mask.GetPaths()
	if len(paths) == 0 {
		return false
	}
	for _, path := range paths {
		if path != "spec.instance_type" {
			return false
		}
	}
	return true
}

func (s *PrivateComputeInstancesServer) validateAndResolveUserDataSecret(
	ctx context.Context,
	spec *privatev1.ComputeInstanceSpec,
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

func (s *PrivateComputeInstancesServer) Delete(ctx context.Context,
	request *privatev1.ComputeInstancesDeleteRequest) (response *privatev1.ComputeInstancesDeleteResponse, err error) {
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

func (s *PrivateComputeInstancesServer) Signal(ctx context.Context,
	request *privatev1.ComputeInstancesSignalRequest) (response *privatev1.ComputeInstancesSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

// applyComputeTemplate validates the VM's Template parameters, fills omitted spec fields from
// the Template, and stores the Template's actual ID, name, and scope on the VM. References
// copied from a shared Template retain that scope when the VM belongs to a tenant.
func (s *PrivateComputeInstancesServer) applyComputeTemplate(
	instance *privatev1.ComputeInstance,
	template *privatev1.ComputeInstanceTemplate,
) error {
	spec := instance.GetSpec()
	parameters, err := utils.ApplyTemplateParameterDefaultsAndValidate(
		utils.ComputeInstanceTemplateAdapter{ComputeInstanceTemplate: template}, spec.GetTemplateParameters())
	if err != nil {
		return err
	}
	spec.SetTemplateParameters(parameters)

	inheritInstanceType := spec.GetInstanceType() == nil
	inheritDiskImage := spec.GetDiskImage() == nil
	utils.ApplySpecDefaults(spec, template.GetSpecDefaults())
	if inheritInstanceType && spec.GetInstanceType() != nil {
		inheritReferenceScope(spec.GetInstanceType(), template.GetMetadata())
	}
	if inheritDiskImage && spec.GetDiskImage() != nil {
		inheritReferenceScope(spec.GetDiskImage(), template.GetMetadata())
	}
	spec.SetTemplate(canonicalComputeInstanceTemplateReference(template))
	return utils.ValidateRequiredSpecFields(spec)
}

// validateInstanceType checks the instance type selected by the caller, Catalog policy, or
// Template and fills its stored ID/name/scope. The VM keeps the reference; the controller
// reads vCPUs and memory from the type later.
func (s *PrivateComputeInstancesServer) validateInstanceType(
	ctx context.Context,
	ci *privatev1.ComputeInstance,
) ([]string, error) {
	spec := ci.GetSpec()
	instanceTypeRef := spec.GetInstanceType()
	if instanceTypeRef == nil || refKey(instanceTypeRef) == "" {
		return nil, nil
	}
	identifier := refKey(instanceTypeRef)

	resolved, err := resolveAndCanonicalizeReference(
		ctx, s.instanceTypesDao, ci.GetMetadata(), instanceTypeRef, "instance type", grpccodes.NotFound,
	)
	if err != nil {
		return nil, err
	}
	return validateResolvedInstanceType(resolved, identifier, "")
}

func (s *PrivateComputeInstancesServer) validateInstanceTypeResize(
	ctx context.Context,
	current, candidate *privatev1.ComputeInstance,
) (warnings []string, noOp bool, err error) {
	currentRef := current.GetSpec().GetInstanceType()
	targetRef := candidate.GetSpec().GetInstanceType()
	if currentRef == nil || targetRef == nil {
		return nil, false, grpcstatus.Errorf(grpccodes.InvalidArgument, "instance type is mandatory")
	}
	targetName := refKey(targetRef)
	currentType, err := resolveAndCanonicalizeReference(
		ctx, s.instanceTypesDao, current.GetMetadata(), currentRef, "instance type", grpccodes.NotFound,
	)
	if err != nil {
		return nil, false, err
	}
	targetType, err := resolveAndCanonicalizeReference(
		ctx, s.instanceTypesDao, current.GetMetadata(), targetRef, "instance type", grpccodes.NotFound,
	)
	if err != nil {
		if grpcstatus.Code(err) == grpccodes.NotFound {
			return nil, false, grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"instance type '%s' not found",
				targetName,
			)
		}
		return nil, false, err
	}
	if currentType.GetId() == targetType.GetId() {
		return nil, true, nil
	}

	targetName = targetType.GetMetadata().GetName()
	warnings, err = validateResolvedInstanceType(targetType, targetName, "")
	if err != nil {
		return nil, false, err
	}
	if !proto.Equal(
		currentType.GetSpec().GetGpu(),
		targetType.GetSpec().GetGpu(),
	) {
		return nil, false, grpcstatus.Errorf(
			grpccodes.FailedPrecondition,
			"cannot change GPU configuration when resizing from instance type '%s' to '%s'",
			currentType.GetMetadata().GetName(),
			targetName,
		)
	}

	return warnings, false, nil
}

// validateDiskImage checks the image selected by the caller, Catalog policy, or Template and
// stores its actual ID/name/scope. Deprecated images produce a warning; obsolete ones fail.
func (s *PrivateComputeInstancesServer) validateDiskImage(
	ctx context.Context,
	ci *privatev1.ComputeInstance,
) (*privatev1.DiskImage, []string, error) {
	spec := ci.GetSpec()
	diskImageRef := spec.GetDiskImage()
	if diskImageRef == nil {
		return nil, nil, nil
	}

	key := refKey(diskImageRef)
	if key == "" {
		return nil, nil, nil
	}

	diskImage, err := resolveDiskImageReference(ctx, s.diskImagesDao, referenceScope{tenant: ci.GetMetadata().GetTenant(), project: ci.GetMetadata().GetProject()}, diskImageRef, "")
	if err != nil {
		return nil, nil, err
	}
	warnings, err := validateResolvedDiskImage(diskImage, key, "")
	if err != nil {
		return nil, nil, err
	}
	spec.SetDiskImage(canonicalDiskImageReference(diskImage))

	return diskImage, warnings, nil
}

// validateSshPublicKey resolves the tenant-scoped Secret reference and checks that the
// selected guest can consume the key through cloud-init.
func (s *PrivateComputeInstancesServer) validateSshPublicKey(
	ctx context.Context,
	ci *privatev1.ComputeInstance,
	diskImage *privatev1.DiskImage,
) error {
	spec := ci.GetSpec()
	ref := spec.GetSshKey()
	if ref == nil {
		return nil
	}
	metadata := ci.GetMetadata()
	if metadata == nil {
		return grpcstatus.Error(grpccodes.InvalidArgument, "cannot resolve ssh key reference without instance metadata")
	}

	secret, err := resolveResourceInScope(
		ctx,
		s.secretsDao,
		referenceScope{tenant: metadata.GetTenant()},
		ref.GetId(),
		ref.GetName(),
		"secret",
		"",
		grpccodes.NotFound,
	)
	if err != nil {
		return err
	}

	if secret.GetType() != privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"secret '%s' referenced by ssh_key has type %s; expected %s",
			refKey(ref),
			secret.GetType(),
			privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
		)
	}
	data := secret.GetData()
	if len(data) == 0 && secret.GetBackend() == privatev1.SecretBackend_SECRET_BACKEND_VAULT {
		if s.secretStore == nil {
			s.logger.ErrorContext(ctx, "Failed to load SSH key Secret: secret store isn't configured")
			return grpcstatus.Errorf(grpccodes.Internal, "failed to resolve ssh key reference")
		}
		secretMetadata := secret.GetMetadata()
		data, err = s.secretStore.Fetch(
			ctx,
			secretMetadata.GetTenant(),
			secretMetadata.GetProject(),
			secretMetadata.GetName(),
		)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to load SSH key Secret from store", "error", err)
			return grpcstatus.Errorf(grpccodes.Internal, "failed to resolve ssh key reference")
		}
	}
	if len(data["public_key"]) == 0 {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"secret '%s' referenced by ssh_key must contain a non-empty 'public_key' entry",
			refKey(ref),
		)
	}

	spec.SetSshKey(privatev1.SecretLocalReference_builder{
		Id:   secret.GetId(),
		Name: secret.GetMetadata().GetName(),
	}.Build())

	if diskImage != nil && diskImage.GetSpec().GetGuestOsFamily() == privatev1.GuestOSFamily_GUEST_OS_FAMILY_WINDOWS {
		return grpcstatus.Error(grpccodes.InvalidArgument, "SSH key injection is not supported for Windows instances")
	}
	return nil
}

func validateComputeInstanceImmutability(
	current, candidate *privatev1.ComputeInstance,
	updateMask *fieldmaskpb.FieldMask,
) error {
	if err := validateComputeTemplateImmutability(current, candidate, updateMask); err != nil {
		return err
	}
	if err := validateComputeNetworkAttachmentsImmutability(current, candidate, updateMask); err != nil {
		return err
	}
	return validateComputeDiskImmutability(current, candidate, updateMask)
}

// validateComputeTemplateImmutability ensures that template-derived fields cannot be changed after creation.
func validateComputeTemplateImmutability(
	current, candidate *privatev1.ComputeInstance,
	updateMask *fieldmaskpb.FieldMask,
) error {
	updatingTemplate := updateIncludesField(updateMask, "spec.template")
	updatingTemplateParams := updateIncludesField(updateMask, "spec.template_parameters")
	updatingCatalogItem := updateIncludesField(updateMask, "spec.catalog_item")
	updatingDiskImage := updateIncludesField(updateMask, "spec.disk_image")
	updatingSshKey := updateIncludesField(updateMask, "spec.ssh_key")
	updatingAutoExternalIP := updateIncludesField(updateMask, "spec.auto_external_ip_attachment")
	updatingUserDataSecret := updateIncludesField(updateMask, "spec.user_data_secret")

	if !updatingTemplate && !updatingTemplateParams && !updatingCatalogItem &&
		!updatingDiskImage && !updatingSshKey && !updatingAutoExternalIP && !updatingUserDataSecret {
		return nil
	}

	existingSpec := current.GetSpec()
	newSpec := candidate.GetSpec()

	if updatingTemplate && refKey(existingSpec.GetTemplate()) != refKey(newSpec.GetTemplate()) {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"cannot change spec.template from '%s' to '%s': template is immutable",
			refKey(existingSpec.GetTemplate()),
			refKey(newSpec.GetTemplate()),
		)
	}

	if updatingTemplateParams {
		templateParamsEqual := func(first, second *anypb.Any) bool {
			return proto.Equal(first, second)
		}
		if !maps.EqualFunc(existingSpec.GetTemplateParameters(), newSpec.GetTemplateParameters(), templateParamsEqual) {
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"cannot change spec.template_parameters: template parameters are immutable",
			)
		}
	}

	if updatingCatalogItem {
		ref, err := preserveCatalogItemProvenance(existingSpec.GetCatalogItem(), newSpec.GetCatalogItem(), updateMask)
		if err != nil {
			return err
		}
		newSpec.SetCatalogItem(ref)
	}

	if updatingDiskImage && refKey(existingSpec.GetDiskImage()) != refKey(newSpec.GetDiskImage()) {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"cannot change spec.disk_image from '%s' to '%s': disk image is immutable",
			refKey(existingSpec.GetDiskImage()),
			refKey(newSpec.GetDiskImage()),
		)
	}

	if updatingSshKey {
		existingKey := existingSpec.GetSshKey()
		newKey := newSpec.GetSshKey()
		sameKey := proto.Equal(existingKey, newKey)
		if existingKey != nil && newKey != nil {
			if newKey.GetId() != "" {
				sameKey = existingKey.GetId() == newKey.GetId()
			} else {
				sameKey = existingKey.GetName() == newKey.GetName()
			}
		}
		if !sameKey {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"cannot change spec.ssh_key: ssh_key is immutable after creation")
		}
		if existingKey != nil {
			newSpec.SetSshKey(privatev1.SecretLocalReference_builder{
				Id:   existingKey.GetId(),
				Name: existingKey.GetName(),
			}.Build())
		}
	}

	if updatingAutoExternalIP && existingSpec.GetAutoExternalIpAttachment() != newSpec.GetAutoExternalIpAttachment() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"cannot change spec.auto_external_ip_attachment: auto_external_ip_attachment is immutable after creation")
	}

	// The user_data_secret reference is immutable once set. Setting it for the first time is
	// allowed, including migration from inline user_data, and it cannot be changed or cleared
	// afterwards. Mutual exclusion with inline user_data is enforced separately.
	if updatingUserDataSecret && existingSpec.GetUserDataSecret() != nil &&
		!proto.Equal(existingSpec.GetUserDataSecret(), newSpec.GetUserDataSecret()) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"cannot change spec.user_data_secret: user_data_secret is immutable after creation")
	}

	return nil
}

// validateComputeNetworkAttachmentsImmutability ensures subnet references cannot be changed
// in networkAttachments array after creation. Security groups can be modified.
func validateComputeNetworkAttachmentsImmutability(
	current, candidate *privatev1.ComputeInstance,
	updateMask *fieldmaskpb.FieldMask,
) error {
	updatingNetworkAttachments := updateIncludesField(updateMask, "spec.network_attachments")

	if !updatingNetworkAttachments {
		return nil
	}

	existingAttachments := current.GetSpec().GetNetworkAttachments()
	if err := computeinstancespec.ValidateNetworkAttachments(existingAttachments); err != nil {
		return grpcstatus.Errorf(grpccodes.Internal, "failed to parse existing network attachments configuration: %s", err.Error())
	}
	newAttachments := candidate.GetSpec().GetNetworkAttachments()
	if err := computeinstancespec.ValidateNetworkAttachments(newAttachments); err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "invalid network attachments configuration: %s", err.Error())
	}

	// Check that the number of attachments hasn't changed
	// (array size immutability - defense-in-depth with CRD validation)
	if len(existingAttachments) != len(newAttachments) {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"cannot change number of network attachments from %d to %d",
			len(existingAttachments),
			len(newAttachments),
		)
	}

	// Check that subnet references haven't changed within each attachment
	// Security groups can change freely (no validation)
	for i := range existingAttachments {
		existingSubnet := existingAttachments[i].GetSubnet()
		newSubnet := newAttachments[i].GetSubnet()
		if refKey(existingSubnet) != refKey(newSubnet) {
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"cannot change network_attachments[%d].subnet from '%s' to '%s': subnet is immutable",
				i, refKey(existingSubnet), refKey(newSubnet),
			)
		}
	}

	return nil
}

// validateComputeDiskImmutability ensures that boot_disk and additional_disks cannot be
// modified after creation. The entire DiskSpec is immutable (size_gib and storage_tier).
func validateComputeDiskImmutability(
	current, candidate *privatev1.ComputeInstance,
	updateMask *fieldmaskpb.FieldMask,
) error {
	updatingBootDisk := updateIncludesField(updateMask, "spec.boot_disk")
	updatingAdditionalDisks := updateIncludesField(updateMask, "spec.additional_disks")

	if !updatingBootDisk && !updatingAdditionalDisks {
		return nil
	}

	existingSpec := current.GetSpec()
	newSpec := candidate.GetSpec()

	// Validate boot_disk immutability
	if updatingBootDisk {
		existingBootDisk := existingSpec.GetBootDisk()
		newBootDisk := newSpec.GetBootDisk()

		if existingBootDisk.GetSizeGib() != newBootDisk.GetSizeGib() {
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"cannot change spec.boot_disk.size_gib from %d to %d: boot disk is immutable",
				existingBootDisk.GetSizeGib(), newBootDisk.GetSizeGib(),
			)
		}

		if !storageTierRefsEqual(existingBootDisk.GetStorageTier(), newBootDisk.GetStorageTier()) {
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"cannot change spec.boot_disk.storage_tier from %q to %q: boot disk is immutable",
				existingBootDisk.GetStorageTier().GetName(), newBootDisk.GetStorageTier().GetName(),
			)
		}
	}

	// Validate additional_disks immutability
	if updatingAdditionalDisks {
		existingDisks := existingSpec.GetAdditionalDisks()
		newDisks := newSpec.GetAdditionalDisks()

		if len(existingDisks) != len(newDisks) {
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"cannot change spec.additional_disks array length from %d to %d: additional disks are immutable",
				len(existingDisks), len(newDisks),
			)
		}

		for i := range existingDisks {
			existingDisk := existingDisks[i]
			newDisk := newDisks[i]

			if existingDisk.GetSizeGib() != newDisk.GetSizeGib() {
				return grpcstatus.Errorf(
					grpccodes.InvalidArgument,
					"cannot change spec.additional_disks[%d].size_gib from %d to %d: disk is immutable",
					i, existingDisk.GetSizeGib(), newDisk.GetSizeGib(),
				)
			}

			if !storageTierRefsEqual(existingDisk.GetStorageTier(), newDisk.GetStorageTier()) {
				return grpcstatus.Errorf(
					grpccodes.InvalidArgument,
					"cannot change spec.additional_disks[%d].storage_tier from %q to %q: disk is immutable",
					i, existingDisk.GetStorageTier().GetName(), newDisk.GetStorageTier().GetName(),
				)
			}
		}
	}

	return nil
}

// storageTierRefsEqual compares two StorageTierReference values for equality.
// If both have non-empty IDs, comparison is by ID. Otherwise falls back to name comparison.
func storageTierRefsEqual(a, b *privatev1.StorageTierReference) bool {
	if a.GetId() != "" && b.GetId() != "" {
		return a.GetId() == b.GetId()
	}
	return a.GetName() == b.GetName()
}

// validateNetworkReferencesTenancy validates that referenced Subnet and SecurityGroups
// belong to the same tenant as the ComputeInstance.
//
// This validation MUST run even during deletion to prevent cross-tenant updates.
// The DAO Get() calls enforce tenant isolation via TenancyLogic - cross-tenant resources
// are filtered out and appear as NotFound. During deletion, NotFound is allowed (resources
// may have been deleted during cleanup). This ensures tenant boundaries are always enforced
// while allowing graceful deletion.
//
// Implements requirement VAL-04 (tenant isolation).
func (s *PrivateComputeInstancesServer) validateNetworkReferencesTenancy(
	ctx context.Context,
	vm *privatev1.ComputeInstance,
) error {
	if vm == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "compute instance is mandatory")
	}

	spec := vm.GetSpec()
	if spec == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "compute instance spec is mandatory")
	}

	attachments := spec.GetNetworkAttachments()
	if err := computeinstancespec.ValidateNetworkAttachments(attachments); err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "invalid network attachments configuration: %s", err.Error())
	}
	if len(attachments) == 0 {
		return nil
	}

	for _, att := range attachments {
		subnetRef := att.GetSubnet()
		securityGroupRefs := att.GetSecurityGroups()

		// At this point, subnetRef is guaranteed to be non-nil because
		// ValidateNetworkAttachments ensures all attachments have non-empty subnet.
		subnetIDStr := refKey(subnetRef)

		// Validate tenant isolation for subnet.
		// TenancyLogic in DAO filters out cross-tenant resources, making them appear as NotFound.
		// We allow NotFound during deletion (resource may be deleted or cross-tenant).
		// The key is that we ALWAYS call DAO Get() so tenant filtering happens.
		_, getErr := s.subnetsDao.Get().SetId(subnetIDStr).Do(ctx)
		if getErr != nil {
			var notFoundErr *dao.ErrNotFound
			if errors.As(getErr, &notFoundErr) {
				// Resource doesn't exist OR belongs to different tenant (filtered by TenancyLogic).
				// During deletion this is allowed. During creation/normal update this is caught
				// by validateNetworkReferencesState.
				continue
			}
			// Other error - propagate
			s.logger.ErrorContext(ctx, "Failed to query Subnet for tenancy check",
				slog.String("subnet_id", subnetIDStr),
				slog.Any("error", getErr))
			return grpcstatus.Errorf(grpccodes.Internal, "failed to validate subnet")
		}

		// Validate tenant isolation for security groups.
		for _, sgRef := range securityGroupRefs {
			if sgRef == nil {
				continue
			}
			sgIDStr := refKey(sgRef)
			_, getErr := s.securityGroupsDao.Get().SetId(sgIDStr).Do(ctx)
			if getErr != nil {
				var notFoundErr *dao.ErrNotFound
				if errors.As(getErr, &notFoundErr) {
					// Resource doesn't exist OR belongs to different tenant (filtered by TenancyLogic).
					// During deletion this is allowed. During creation/normal update this is caught
					// by validateNetworkReferencesState.
					continue
				}
				// Other error - propagate
				s.logger.ErrorContext(ctx, "Failed to query SecurityGroup for tenancy check",
					slog.String("security_group_id", sgIDStr),
					slog.Any("error", getErr))
				return grpcstatus.Errorf(grpccodes.Internal, "failed to validate security group")
			}
		}
	}

	return nil
}

// validateNetworkReferencesState validates that referenced Subnet and SecurityGroups
// exist, are in READY state, and SecurityGroups belong to the same VirtualNetwork as their attachment's Subnet.
//
// This validation is SKIPPED during deletion because resources may already be deleted.
// Resolution checks tenant/project ownership and fills the final reference IDs. During deletion,
// validateNetworkReferencesTenancy still runs separately without requiring readiness.
//
// Implements requirements VAL-01, VAL-02, VAL-03.
func (s *PrivateComputeInstancesServer) validateNetworkReferencesState(
	ctx context.Context,
	vm *privatev1.ComputeInstance,
) error {
	if vm == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "compute instance is mandatory")
	}

	spec := vm.GetSpec()
	if spec == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "compute instance spec is mandatory")
	}

	attachments := spec.GetNetworkAttachments()
	if err := computeinstancespec.ValidateNetworkAttachments(attachments); err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "invalid network attachments configuration: %s", err.Error())
	}
	if len(attachments) == 0 {
		return nil
	}

	for i, att := range attachments {
		subnetRef := att.GetSubnet()
		securityGroupRefs := att.GetSecurityGroups()

		subnetKey := refKey(subnetRef)
		subnet, err := resolveAndCanonicalizeReference(ctx, s.subnetsDao, vm.GetMetadata(), subnetRef,
			"subnet", grpccodes.NotFound)
		if err != nil {
			if grpcstatus.Code(err) == grpccodes.NotFound {
				return grpcstatus.Errorf(grpccodes.InvalidArgument,
					"network_attachments[%d]: subnet '%s' does not exist", i, subnetKey)
			}
			return err
		}

		source := fmt.Sprintf(" in network_attachments[%d]", i)
		// VAL-02: Validate READY state
		if err := validateResolvedSubnetReady(subnet, subnetKey, source); err != nil {
			return err
		}

		virtualNetworkID := refKey(subnet.GetSpec().GetVirtualNetwork())

		// Use the subnet's VirtualNetwork as the expected owner for every SecurityGroup.
		for _, sgRef := range securityGroupRefs {
			if sgRef == nil {
				continue
			}
			sgKey := refKey(sgRef)

			sg, err := resolveAndCanonicalizeReference(ctx, s.securityGroupsDao, vm.GetMetadata(), sgRef,
				"security group", grpccodes.NotFound)
			if err != nil {
				if grpcstatus.Code(err) == grpccodes.NotFound {
					return grpcstatus.Errorf(grpccodes.InvalidArgument,
						"network_attachments[%d]: security group '%s' does not exist", i, sgKey)
				}
				return err
			}

			// VAL-02: Validate READY state
			// VAL-03: Validate SecurityGroup belongs to same VirtualNetwork as Subnet
			if err := validateResolvedSecurityGroup(sg, sgKey, source, virtualNetworkID); err != nil {
				return err
			}
		}
	}

	return nil
}

// resolveCatalogItem finds the VM's published Catalog Item in the VM's selected tenant/project
// or shared scope, then finds the item's Template under the item's ownership. It applies locked
// and editable field and parameter rules to the new VM and returns that Template for defaults.
func (s *PrivateComputeInstancesServer) resolveCatalogItem(
	ctx context.Context, ci *privatev1.ComputeInstance,
) (*privatev1.ComputeInstanceTemplate, error) {
	if ci == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
	}
	catalogItemRef := ci.GetSpec().GetCatalogItem()
	if catalogItemRef == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "catalog_item is mandatory")
	}
	catalogItemRefStr := refKey(catalogItemRef)

	catalogItem, err := resolveAndCanonicalizeLockedReference(ctx, s.catalogItemsDao, ci.GetMetadata(), catalogItemRef, "catalog item", grpccodes.NotFound)
	if err != nil {
		return nil, err
	}

	if err := validateCatalogItemForCreation(catalogItem, catalogItemRefStr); err != nil {
		return nil, err
	}

	templateRef := catalogItem.GetTemplate()
	if templateRef == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
			"catalog item '%s' does not reference a template", catalogItemRefStr)
	}
	templateRef = cloneMessage(templateRef)
	resolvedTemplate, resolveErr := resolveAndCanonicalizeLockedReference(ctx, s.templatesDao, catalogItem.GetMetadata(), templateRef, "template", grpccodes.InvalidArgument)
	if resolveErr != nil {
		return nil, resolveErr
	}
	if err := applyComputeInstanceCatalogItemPolicies(ci.GetSpec(), catalogItem.GetFields()); err != nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "%s", err)
	}
	parameters, parameterErr := applyCatalogItemTemplateParameterPolicies(
		utils.ComputeInstanceTemplateAdapter{ComputeInstanceTemplate: resolvedTemplate},
		catalogItem.GetTemplateParameters(), ci.GetSpec().GetTemplateParameters())
	if parameterErr != nil {
		return nil, parameterErr
	}
	ci.GetSpec().SetTemplateParameters(parameters)
	return resolvedTemplate, nil
}

const (
	autoCreatedLabel    = "osac.openshift.io/auto-created"
	autoCreatedForLabel = "osac.openshift.io/auto-created-for"
)

func (s *PrivateComputeInstancesServer) autoProvisionExternalIP(
	ctx context.Context, ci *privatev1.ComputeInstance,
) error {
	pool, err := SelectExternalIPPool(ctx, s.externalIPPoolDao, privatev1.IPFamily_IP_FAMILY_UNSPECIFIED)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "auto_external_ip_attachment: %s", err)
	}

	tenant := ci.GetMetadata().GetTenant()
	ciID := ci.GetId()
	shortID := ciID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	eip := privatev1.ExternalIP_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   fmt.Sprintf("auto-eip-%s", shortID),
			Tenant: tenant,
			Labels: map[string]string{
				autoCreatedLabel:    "true",
				autoCreatedForLabel: ciID,
			},
			Annotations: map[string]string{
				ownerReferenceAnnotation: ciID,
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

	err = s.lifecycle.lockNewAttachmentReferences(ctx, eipID, ciID, s.generic.dao)
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
				autoCreatedForLabel: ciID,
			},
			Annotations: map[string]string{
				ownerReferenceAnnotation: ciID,
			},
			Creator: "system",
		}.Build(),
		Spec: privatev1.ExternalIPAttachmentSpec_builder{
			ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eipID}.Build(),
			ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ciID}.Build(),
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

func (s *PrivateComputeInstancesServer) autoCleanupExternalIP(ctx context.Context, ciID string) error {
	filter := fmt.Sprintf(
		"this.metadata.labels['%s'] == '%s'",
		autoCreatedForLabel, ciID,
	)
	listResp, err := s.externalIPAttachmentDao.List().SetFilter(filter).Do(ctx)
	if err != nil {
		return fmt.Errorf("auto_external_ip_attachment cleanup: failed to list attachments: %w", err)
	}

	for _, attachment := range listResp.GetItems() {
		attachmentID := attachment.GetId()
		eipRef := attachment.GetSpec().GetExternalIp()
		eipID := refKey(eipRef)

		if eipID != "" {
			err = s.lifecycle.deleteAttachmentAndExternalIP(ctx, attachmentID, eipID)
		} else {
			err = s.lifecycle.deleteAttachment(ctx, attachmentID)
		}
		if err != nil {
			return fmt.Errorf("auto_external_ip_attachment cleanup: %w", err)
		}
	}

	return nil
}
