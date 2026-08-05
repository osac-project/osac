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
	"strconv"
	"strings"

	"maps"

	"github.com/prometheus/client_golang/prometheus"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/computeinstancespec"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
	"github.com/osac-project/fulfillment-service/internal/events"
	"github.com/osac-project/fulfillment-service/internal/utils"
)

type PrivateComputeInstancesServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.ComputeInstancesServer = (*PrivateComputeInstancesServer)(nil)

type PrivateComputeInstancesServer struct {
	privatev1.UnimplementedComputeInstancesServer

	logger            *slog.Logger
	generic           *GenericServer[*privatev1.ComputeInstance]
	templatesDao      *dao.GenericDAO[*privatev1.ComputeInstanceTemplate]
	catalogItemsDao   *dao.GenericDAO[*privatev1.ComputeInstanceCatalogItem]
	subnetsDao        *dao.GenericDAO[*privatev1.Subnet]
	securityGroupsDao *dao.GenericDAO[*privatev1.SecurityGroup]
	instanceTypesDao  *dao.GenericDAO[*privatev1.InstanceType]
}

func NewPrivateComputeInstancesServer() *PrivateComputeInstancesServerBuilder {
	return &PrivateComputeInstancesServerBuilder{}
}

func (b *PrivateComputeInstancesServerBuilder) SetLogger(value *slog.Logger) *PrivateComputeInstancesServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateComputeInstancesServerBuilder) SetNotifier(value events.Notifier) *PrivateComputeInstancesServerBuilder {
	b.notifier = value
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

	// Create the generic server:
	generic, err := NewGenericServer[*privatev1.ComputeInstance]().
		SetLogger(b.logger).
		SetService(privatev1.ComputeInstances_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &PrivateComputeInstancesServer{
		logger:            b.logger,
		generic:           generic,
		templatesDao:      templatesDao,
		catalogItemsDao:   catalogItemsDao,
		subnetsDao:        subnetsDao,
		securityGroupsDao: securityGroupsDao,
		instanceTypesDao:  instanceTypesDao,
	}
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

func (s *PrivateComputeInstancesServer) Create(ctx context.Context,
	request *privatev1.ComputeInstancesCreateRequest) (response *privatev1.ComputeInstancesCreateResponse, err error) {
	// Validate tenant isolation for network references:
	err = s.validateNetworkReferencesTenancy(ctx, request.GetObject())
	if err != nil {
		return
	}

	// Validate network references state (exists, READY):
	err = s.validateNetworkReferencesState(ctx, request.GetObject())
	if err != nil {
		return
	}

	// Require network_attachments for new VMs (no pod network for new VMs):
	if len(request.GetObject().GetSpec().GetNetworkAttachments()) == 0 {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument,
			"spec.network_attachments: at least one network attachment is required for new compute instances")
		return
	}

	// Dispatch between catalog item and template paths:
	spec := request.GetObject().GetSpec()
	catalogItemRef := spec.GetCatalogItem()
	templateRef := spec.GetTemplate()
	if catalogItemRef != "" && templateRef != "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument,
			"catalog_item and template are mutually exclusive")
		return
	}
	var template *privatev1.ComputeInstanceTemplate
	if catalogItemRef != "" {
		err = s.validateAndTransformCatalogItem(ctx, request.GetObject())
		if err != nil {
			return
		}
		template, err = s.fetchTemplate(ctx, spec.GetTemplate())
	} else {
		template, err = s.fetchAndValidateTemplate(ctx, request.GetObject())
	}
	if err != nil {
		return
	}

	// Apply the template's spec defaults and validate required fields, regardless
	// of whether the template was referenced directly or resolved via a catalog item.
	err = s.applySpecDefaults(spec, template)
	if err != nil {
		return
	}

	// Validate instance type existence and state (D-02: validate-only, no resolution).
	// Must run after template/catalog defaults are applied so instance_type defaults
	// from templates are visible.
	var warnings []string
	warnings, err = s.validateInstanceType(ctx, request.GetObject())
	if err != nil {
		return
	}

	err = s.generic.Create(ctx, request, &response)
	if err != nil {
		return
	}

	// Attach warnings to the response (deprecation notices for DEPRECATED instance types).
	if len(warnings) > 0 {
		response.SetWarnings(warnings)
	}
	return
}

func (s *PrivateComputeInstancesServer) Update(ctx context.Context,
	request *privatev1.ComputeInstancesUpdateRequest) (response *privatev1.ComputeInstancesUpdateResponse, err error) {
	// Only validate fields affected by the update mask. With a field mask the object
	// is sparse so validating fields absent from it would fail incorrectly.
	mask := request.GetUpdateMask()
	isBeingDeleted := request.GetObject().GetMetadata().GetDeletionTimestamp() != nil

	// ALWAYS validate tenant isolation for network references, even during deletion.
	// This prevents cross-tenant updates on ComputeInstances being deleted.
	if hasMaskPrefix(mask, "spec.network_attachments") {
		err = s.validateNetworkReferencesTenancy(ctx, request.GetObject())
		if err != nil {
			return
		}
	}

	// Only validate resource state (exists, READY) if NOT being deleted.
	// Referenced resources (subnets, security groups) may already be deleted during cleanup.
	if !isBeingDeleted && hasMaskPrefix(mask, "spec.network_attachments") {
		err = s.validateNetworkReferencesState(ctx, request.GetObject())
		if err != nil {
			return
		}
	}

	err = s.validateTemplateImmutability(ctx, request)
	if err != nil {
		return
	}

	err = s.validateNetworkAttachmentsImmutability(ctx, request)
	if err != nil {
		return
	}

	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateComputeInstancesServer) Delete(ctx context.Context,
	request *privatev1.ComputeInstancesDeleteRequest) (response *privatev1.ComputeInstancesDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateComputeInstancesServer) Signal(ctx context.Context,
	request *privatev1.ComputeInstancesSignalRequest) (response *privatev1.ComputeInstancesSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

// fetchAndValidateTemplate fetches the template, validates parameters in the compute instance spec,
// applies template parameter defaults, and returns the template.
func (s *PrivateComputeInstancesServer) fetchAndValidateTemplate(ctx context.Context, vm *privatev1.ComputeInstance) (*privatev1.ComputeInstanceTemplate, error) {
	if vm == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "compute instance is mandatory")
	}

	spec := vm.GetSpec()
	if spec == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "compute instance spec is mandatory")
	}

	template, err := s.fetchTemplate(ctx, spec.GetTemplate())
	if err != nil {
		return nil, err
	}

	// Validate template parameters:
	vmParameters := spec.GetTemplateParameters()
	err = utils.ValidateComputeInstanceTemplateParameters(template, vmParameters)
	if err != nil {
		return nil, err
	}

	// Set default values for template parameters:
	actualVmParameters := utils.ProcessTemplateParametersWithDefaults(
		utils.ComputeInstanceTemplateAdapter{ComputeInstanceTemplate: template},
		vmParameters,
	)
	spec.SetTemplateParameters(actualVmParameters)

	return template, nil
}

// fetchTemplate fetches a compute instance template
func (s *PrivateComputeInstancesServer) fetchTemplate(ctx context.Context, templateID string) (*privatev1.ComputeInstanceTemplate, error) {
	if templateID == "" {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "template ID is mandatory")
	}

	getTemplateResponse, err := s.templatesDao.Get().
		SetId(templateID).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
				"template '%s' does not exist", templateID)
		}
		s.logger.ErrorContext(
			ctx,
			"Template retrieval failed",
			slog.String("template_id", templateID),
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(
			grpccodes.Internal,
			"failed to retrieve template '%s'",
			templateID,
		)
	}

	template := getTemplateResponse.GetObject()
	if template == nil {
		return nil, grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"template '%s' does not exist",
			templateID,
		)
	}
	return template, nil
}

// applySpecDefaults applies template spec defaults to the spec in place and validates
// that all required fields are present. User-provided values are never overridden.
func (s *PrivateComputeInstancesServer) applySpecDefaults(
	spec *privatev1.ComputeInstanceSpec,
	template *privatev1.ComputeInstanceTemplate,
) error {
	utils.ApplySpecDefaults(spec, template.GetSpecDefaults())
	return utils.ValidateRequiredSpecFields(spec)
}

// validateInstanceType validates the instance_type field on a ComputeInstance during creation.
// Per D-01, the API stores only the instance_type name and does NOT expand cores/memory_gib.
// Per D-02, the API validates existence and state but resolution happens in the reconciler.
func (s *PrivateComputeInstancesServer) validateInstanceType(
	ctx context.Context,
	ci *privatev1.ComputeInstance,
) ([]string, error) {
	spec := ci.GetSpec()
	instanceTypeName := spec.GetInstanceType()
	var warnings []string

	if instanceTypeName == "" {
		// instance_type not on the spec directly. If a template is referenced
		// (e.g. via catalog item), check whether its spec_defaults provide one.
		if templateRef := spec.GetTemplate(); templateRef != "" {
			template, fetchErr := s.fetchTemplate(ctx, templateRef)
			if fetchErr == nil && template.GetSpecDefaults().HasInstanceType() {
				instanceTypeName = template.GetSpecDefaults().GetInstanceType()
			}
		}
	}

	if instanceTypeName == "" {
		return warnings, nil
	}

	// Look up the instance type and validate its state.
	stateWarnings, err := validateInstanceTypeState(ctx, s.instanceTypesDao, instanceTypeName, "")
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, stateWarnings...)

	return warnings, nil
}

// validateTemplateImmutability ensures that the template and template_parameters fields
// cannot be changed after compute instance creation.
func (s *PrivateComputeInstancesServer) validateTemplateImmutability(ctx context.Context,
	request *privatev1.ComputeInstancesUpdateRequest) error {
	updateMask := request.GetUpdateMask()
	updatingTemplate := hasMaskPrefix(updateMask, "spec.template")
	updatingTemplateParams := hasMaskPrefix(updateMask, "spec.template_parameters")
	updatingCatalogItem := hasMaskPrefix(updateMask, "spec.catalog_item")
	updatingInstanceType := hasMaskPrefix(updateMask, "spec.instance_type")

	if !updatingTemplate && !updatingTemplateParams && !updatingCatalogItem && !updatingInstanceType {
		return nil
	}

	ci := request.GetObject()
	if ci == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "compute instance is mandatory")
	}
	id := ci.GetId()
	if id == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "compute instance id is mandatory")
	}

	getResponse, err := s.generic.dao.Get().SetId(id).Do(ctx)
	if err != nil {
		return err
	}
	existingCI := getResponse.GetObject()

	existingSpec := existingCI.GetSpec()
	newSpec := request.GetObject().GetSpec()

	if updatingTemplate && existingSpec.GetTemplate() != newSpec.GetTemplate() {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"cannot change spec.template from '%s' to '%s': template is immutable",
			existingSpec.GetTemplate(),
			newSpec.GetTemplate(),
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

	if updatingCatalogItem && existingSpec.GetCatalogItem() != newSpec.GetCatalogItem() {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"cannot change spec.catalog_item from '%s' to '%s': catalog item is immutable",
			existingSpec.GetCatalogItem(),
			newSpec.GetCatalogItem(),
		)
	}

	if updatingInstanceType && existingSpec.GetInstanceType() != newSpec.GetInstanceType() {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"cannot change spec.instance_type from '%s' to '%s': instance type is immutable",
			existingSpec.GetInstanceType(),
			newSpec.GetInstanceType(),
		)
	}

	return nil
}

// validateNetworkAttachmentsImmutability ensures subnet references cannot be changed
// in networkAttachments array after creation. Security groups can be modified.
func (s *PrivateComputeInstancesServer) validateNetworkAttachmentsImmutability(
	ctx context.Context,
	request *privatev1.ComputeInstancesUpdateRequest,
) error {
	updateMask := request.GetUpdateMask()
	updatingNetworkAttachments := hasMaskPrefix(updateMask, "spec.network_attachments")

	if !updatingNetworkAttachments {
		return nil
	}

	ci := request.GetObject()
	if ci == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "compute instance is mandatory")
	}
	id := ci.GetId()
	if id == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "compute instance id is mandatory")
	}

	getResponse, err := s.generic.dao.Get().SetId(id).Do(ctx)
	if err != nil {
		return err
	}
	existingCI := getResponse.GetObject()

	existingAttachments := existingCI.GetSpec().GetNetworkAttachments()
	if err := computeinstancespec.ValidateNetworkAttachments(existingAttachments); err != nil {
		return grpcstatus.Errorf(grpccodes.Internal, "failed to parse existing network attachments configuration: %s", err.Error())
	}
	newAttachments := request.GetObject().GetSpec().GetNetworkAttachments()
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
		if existingSubnet != newSubnet {
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"cannot change network_attachments[%d].subnet from '%s' to '%s': subnet is immutable",
				i, existingSubnet, newSubnet,
			)
		}
	}

	return nil
}

func hasMaskPrefix(mask *fieldmaskpb.FieldMask, prefixes ...string) bool {
	if mask == nil || len(mask.GetPaths()) == 0 {
		return true
	}
	for _, path := range mask.GetPaths() {
		for _, prefix := range prefixes {
			if path == prefix || strings.HasPrefix(path, prefix+".") {
				return true
			}
		}
	}
	return false
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
		subnetID := att.GetSubnet()
		securityGroupIDs := att.GetSecurityGroups()

		// At this point, subnetID is guaranteed to be non-empty because
		// ValidateNetworkAttachments ensures all attachments have non-empty subnet.

		// Validate tenant isolation for subnet.
		// TenancyLogic in DAO filters out cross-tenant resources, making them appear as NotFound.
		// We allow NotFound during deletion (resource may be deleted or cross-tenant).
		// The key is that we ALWAYS call DAO Get() so tenant filtering happens.
		_, getErr := s.subnetsDao.Get().SetId(subnetID).Do(ctx)
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
				slog.String("subnet_id", subnetID),
				slog.Any("error", getErr))
			return grpcstatus.Errorf(grpccodes.Internal, "failed to validate subnet")
		}

		// Validate tenant isolation for security groups.
		for _, sgID := range securityGroupIDs {
			if sgID == "" {
				continue
			}
			_, getErr := s.securityGroupsDao.Get().SetId(sgID).Do(ctx)
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
					slog.String("security_group_id", sgID),
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
// Tenant isolation is validated separately by validateNetworkReferencesTenancy.
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
		subnetID := att.GetSubnet()
		securityGroupIDs := att.GetSecurityGroups()

		// At this point, subnetID is guaranteed to be non-empty because
		// ValidateNetworkAttachments ensures all attachments have non-empty subnet
		var subnet *privatev1.Subnet
		var virtualNetworkID string

		// VAL-01: Validate Subnet exists and is READY
		getSubnetResponse, getErr := s.subnetsDao.Get().
			SetId(subnetID).
			Do(ctx)
		if getErr != nil {
			var notFoundErr *dao.ErrNotFound
			if errors.As(getErr, &notFoundErr) {
				return grpcstatus.Errorf(grpccodes.InvalidArgument,
					"network_attachments[%d]: subnet '%s' does not exist", i, subnetID)
			}
			// Note: TenancyErr won't happen here because tenancy was already validated
			s.logger.ErrorContext(ctx, "Failed to query Subnet",
				slog.String("subnet_id", subnetID),
				slog.Any("error", getErr))
			return grpcstatus.Errorf(grpccodes.Internal, "failed to validate subnet")
		}

		subnet = getSubnetResponse.GetObject()
		if subnet == nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"network_attachments[%d]: subnet '%s' does not exist", i, subnetID)
		}

		// VAL-02: Validate READY state
		if subnet.GetStatus().GetState() != privatev1.SubnetState_SUBNET_STATE_READY {
			return grpcstatus.Errorf(grpccodes.FailedPrecondition,
				"network_attachments[%d]: subnet '%s' is not in READY state (current state: %s)",
				i, subnetID, subnet.GetStatus().GetState().String())
		}

		virtualNetworkID = subnet.GetSpec().GetVirtualNetwork()

		for _, sgID := range securityGroupIDs {
			if sgID == "" {
				continue
			}

			getSGResponse, getErr := s.securityGroupsDao.Get().
				SetId(sgID).
				Do(ctx)
			if getErr != nil {
				var notFoundErr *dao.ErrNotFound
				if errors.As(getErr, &notFoundErr) {
					return grpcstatus.Errorf(grpccodes.InvalidArgument,
						"network_attachments[%d]: security group '%s' does not exist", i, sgID)
				}
				// Note: TenancyErr won't happen here because tenancy was already validated
				s.logger.ErrorContext(ctx, "Failed to query SecurityGroup",
					slog.String("security_group_id", sgID),
					slog.Any("error", getErr))
				return grpcstatus.Errorf(grpccodes.Internal, "failed to validate security group")
			}

			sg := getSGResponse.GetObject()
			if sg == nil {
				return grpcstatus.Errorf(grpccodes.InvalidArgument,
					"network_attachments[%d]: security group '%s' does not exist", i, sgID)
			}

			// VAL-02: Validate READY state
			if sg.GetStatus().GetState() != privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY {
				return grpcstatus.Errorf(grpccodes.FailedPrecondition,
					"network_attachments[%d]: security group '%s' is not in READY state (current state: %s)",
					i, sgID, sg.GetStatus().GetState().String())
			}

			// VAL-03: Validate SecurityGroup belongs to same VirtualNetwork as Subnet
			if virtualNetworkID != "" {
				sgVirtualNetworkID := sg.GetSpec().GetVirtualNetwork()
				if sgVirtualNetworkID != virtualNetworkID {
					return grpcstatus.Errorf(grpccodes.InvalidArgument,
						"network_attachments[%d]: security group '%s' belongs to VirtualNetwork '%s', but subnet '%s' belongs to VirtualNetwork '%s'",
						i, sgID, sgVirtualNetworkID, subnetID, virtualNetworkID)
				}
			}
		}
	}

	return nil
}

// validateAndTransformCatalogItem validates a catalog item reference, ensures it references
// a template, and applies its field definitions to the compute instance spec. Every catalog
// item must reference a template — it's the only source of the spec defaults (image,
// boot_disk, run_strategy, instance_type) that provisioning requires; a catalog item without
// one can never produce a valid compute instance. Callers fetch the template separately once
// this succeeds, using the template reference now set on the spec.
func (s *PrivateComputeInstancesServer) validateAndTransformCatalogItem(
	ctx context.Context, ci *privatev1.ComputeInstance,
) error {
	if ci == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
	}
	catalogItemRef := ci.GetSpec().GetCatalogItem()
	if catalogItemRef == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "catalog_item is mandatory")
	}

	catalogItem, err := s.lookupCatalogItem(ctx, catalogItemRef)
	if err != nil {
		return err
	}

	if err := validateCatalogItemAccess(catalogItem, catalogItemRef); err != nil {
		return err
	}

	templateRef := catalogItem.GetTemplate()
	if templateRef == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"catalog item '%s' does not reference a template", catalogItemRef)
	}
	ci.GetSpec().SetTemplate(templateRef)

	return applyFieldDefinitions(ci.GetSpec(), catalogItem.GetFieldDefinitions())
}

func (s *PrivateComputeInstancesServer) lookupCatalogItem(ctx context.Context,
	key string) (result *privatev1.ComputeInstanceCatalogItem, err error) {
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
