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
	"fmt"
	"log/slog"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// validateAndCanonicalizeBareMetalInstanceCatalogItemPolicies checks the locked values and editable
// defaults of the Catalog Item being saved. It resolves instance type, image, and network
// references using each field's full or local reference rules, then stores target IDs and names
// in the policy. The request transaction holds dependency locks until the save ends.
// On error, the caller discards this copy of the item. Deprecated images produce warnings.
func validateAndCanonicalizeBareMetalInstanceCatalogItemPolicies(
	ctx context.Context,
	logger *slog.Logger,
	item *privatev1.BareMetalInstanceCatalogItem,
	template *privatev1.BareMetalInstanceTemplate,
	bareMetalInstanceTypesDao *dao.GenericDAO[*privatev1.BareMetalInstanceType],
	diskImagesDao *dao.GenericDAO[*privatev1.DiskImage],
	hostTypesDao *dao.GenericDAO[*privatev1.HostType],
	subnetsDao *dao.GenericDAO[*privatev1.Subnet],
	virtualNetworksDao *dao.GenericDAO[*privatev1.VirtualNetwork],
	networkClassesDao *dao.GenericDAO[*privatev1.NetworkClass],
	securityGroupsDao *dao.GenericDAO[*privatev1.SecurityGroup],
) ([]string, error) {
	if item == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "catalog item is mandatory")
	}
	fields := item.GetFields()
	if fields == nil {
		return nil, nil
	}
	if err := validateBareMetalInstanceCatalogItemScalarPolicies(fields); err != nil {
		return nil, err
	}

	scope := catalogItemScope(item)
	if err := validateBareMetalInstanceCatalogItemInstanceTypePolicy(ctx, scope, fields.GetInstanceType(), bareMetalInstanceTypesDao); err != nil {
		return nil, err
	}

	warnings, err := validateCatalogItemDiskImagePolicy(ctx, scope, fields.GetDiskImage(), diskImagesDao)
	if err != nil {
		return nil, err
	}

	if err := validateBareMetalInstanceCatalogItemNetworkPolicy(ctx, logger, scope, fields.GetNetworkAttachments(), template,
		hostTypesDao, subnetsDao, virtualNetworksDao, networkClassesDao, securityGroupsDao); err != nil {
		return nil, err
	}
	return warnings, nil
}

// applyBareMetalInstanceCatalogItemPolicies merges the offering's field rules into a new bare
// metal instance spec. It rejects caller values for locked fields, keeps caller values for
// editable fields, and copies locked/default values into omitted fields. Explicit zero, false,
// and empty strings count as supplied; empty collections follow their existing omitted-input
// behavior. The caller discards this spec on error and resolves copied references afterward.
func applyBareMetalInstanceCatalogItemPolicies(
	spec *privatev1.BareMetalInstanceSpec,
	fields *privatev1.BareMetalInstanceCatalogItemFields,
) error {
	if spec == nil || fields == nil {
		return nil
	}
	if err := applyPolicy(fields.GetSshPublicKey(), spec.HasSshPublicKey(), spec.SetSshPublicKey, decodeStringPolicy, identity[string]); err != nil {
		return fmt.Errorf("ssh_public_key: %w", err)
	}
	if err := applyPolicy(fields.GetUserData(), spec.HasUserData(), spec.SetUserData, decodeStringPolicy, identity[string]); err != nil {
		return fmt.Errorf("user_data: %w", err)
	}
	if err := applyPolicy(fields.GetRunStrategy(), spec.HasRunStrategy(), spec.SetRunStrategy, decodeBareMetalInstanceRunStrategyPolicy, identity[privatev1.BareMetalInstanceRunStrategy]); err != nil {
		return fmt.Errorf("run_strategy: %w", err)
	}
	if err := applyPolicy(fields.GetNetworkAttachments(), len(spec.GetNetworkAttachments()) > 0, spec.SetNetworkAttachments, decodeBareMetalInstanceNetworkAttachmentListPolicy, cloneBareMetalInstanceNetworkAttachments); err != nil {
		return fmt.Errorf("network_attachments: %w", err)
	}
	if err := applyPolicy(fields.GetAutoExternalIpAttachment(), spec.HasAutoExternalIpAttachment(), spec.SetAutoExternalIpAttachment, decodeBoolPolicy, identity[bool]); err != nil {
		return fmt.Errorf("auto_external_ip_attachment: %w", err)
	}
	if err := applyPolicy(fields.GetInstanceType(), spec.GetInstanceType() != nil, spec.SetInstanceType, decodeBareMetalInstanceTypeReferencePolicy, cloneMessage[*privatev1.BareMetalInstanceTypeLocalReference]); err != nil {
		return fmt.Errorf("instance_type: %w", err)
	}
	if err := applyPolicy(fields.GetDiskImage(), spec.GetDiskImage() != nil, spec.SetDiskImage, decodeDiskImageReferencePolicy, cloneMessage[*privatev1.DiskImageReference]); err != nil {
		return fmt.Errorf("disk_image: %w", err)
	}
	return nil
}

// validateBareMetalInstanceCatalogItemScalarPolicies checks supported scalar values and returns the first invalid policy.
func validateBareMetalInstanceCatalogItemScalarPolicies(fields *privatev1.BareMetalInstanceCatalogItemFields) error {
	if err := validateCatalogItemStringPolicy(fields.GetSshPublicKey(), "fields.ssh_public_key", validateOpenSSHPublicKey); err != nil {
		return err
	}
	if err := validateCatalogItemStringPolicy(fields.GetUserData(), "fields.user_data", func(value string) error {
		return validateBareMetalUserData([]byte(value))
	}); err != nil {
		return err
	}
	if _, err := decodeBareMetalInstanceRunStrategyPolicy(fields.GetRunStrategy()); err != nil {
		return catalogItemPolicyError("fields.run_strategy", err.Error())
	}
	return validateCatalogItemBoolPolicy(fields.GetAutoExternalIpAttachment(), "fields.auto_external_ip_attachment")
}

// validateBareMetalInstanceCatalogItemInstanceTypePolicy checks a locked instance type or
// editable default in the Catalog Item's exact tenant/project and stores its ID/name. A shared
// offering cannot fix a tenant-local type.
func validateBareMetalInstanceCatalogItemInstanceTypePolicy(
	ctx context.Context,
	scope referenceScope,
	policy *privatev1.BareMetalInstanceTypeLocalReferenceFieldPolicy,
	resourceDao *dao.GenericDAO[*privatev1.BareMetalInstanceType],
) error {
	if policy == nil {
		return nil
	}
	state, err := decodeBareMetalInstanceTypeReferencePolicy(policy)
	if err != nil {
		return catalogItemPolicyError("fields.instance_type", err.Error())
	}
	if err := validateSharedCatalogItemLocalReferencePolicy(scope, "fields.instance_type", state.hasLocked, state.hasDefault); err != nil {
		return err
	}
	resolve := func(ref *privatev1.BareMetalInstanceTypeLocalReference) (*privatev1.BareMetalInstanceTypeLocalReference, error) {
		if ref == nil {
			return nil, nil
		}
		resolved, resolveErr := resolveLockedResourceInScope(ctx, resourceDao, scope, ref.GetId(), ref.GetName(),
			"bare metal instance type", " in fields.instance_type", grpccodes.NotFound)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if err := validateResourceNotDeleted("bare metal instance type", refKey(ref), " in fields.instance_type", resolved.GetMetadata()); err != nil {
			return nil, err
		}
		return canonicalBareMetalInstanceTypeLocalReference(resolved), nil
	}
	if state.hasLocked {
		canonical, resolveErr := resolve(state.lockedValue)
		if resolveErr != nil {
			return resolveErr
		}
		policy.SetLocked(canonical)
	}
	if state.hasDefault {
		canonical, resolveErr := resolve(state.defaultValue)
		if resolveErr != nil {
			return resolveErr
		}
		policy.GetEditable().SetDefaultValue(canonical)
	}
	return nil
}

// validateBareMetalInstanceCatalogItemNetworkPolicy checks each configured subnet and security
// group in the Catalog Item's tenant and project, then stores their IDs and names. It also checks
// that the Template's HostType supports the selected interfaces and that each subnet uses a
// NetworkClass with a fabric manager. A shared Catalog Item cannot fix tenant-local attachments.
func validateBareMetalInstanceCatalogItemNetworkPolicy(
	ctx context.Context,
	logger *slog.Logger,
	scope referenceScope,
	policy *privatev1.BareMetalNetworkAttachmentListFieldPolicy,
	template *privatev1.BareMetalInstanceTemplate,
	hostTypesDao *dao.GenericDAO[*privatev1.HostType],
	subnetsDao *dao.GenericDAO[*privatev1.Subnet],
	virtualNetworksDao *dao.GenericDAO[*privatev1.VirtualNetwork],
	networkClassesDao *dao.GenericDAO[*privatev1.NetworkClass],
	securityGroupsDao *dao.GenericDAO[*privatev1.SecurityGroup],
) error {
	if policy == nil {
		return nil
	}
	state, err := decodeBareMetalInstanceNetworkAttachmentListPolicy(policy)
	if err != nil {
		return catalogItemPolicyError("fields.network_attachments", err.Error())
	}
	if err := validateCatalogItemNetworkAttachmentsNotEmpty("fields.network_attachments", state); err != nil {
		return err
	}
	if err := validateSharedCatalogItemLocalReferencePolicy(scope, "fields.network_attachments", state.hasLocked, state.hasDefault); err != nil {
		return err
	}
	// Validate each concrete policy value with the same attachment rules used by the resource server.
	validateAttachments := func(attachments []*privatev1.BareMetalNetworkAttachment) error {
		if err := validateBareMetalNetworkAttachmentStructure("fields.network_attachments", attachments); err != nil {
			return err
		}
		// Resolve references before checking SecurityGroup ownership and the subnet's fabric configuration.
		for i, attachment := range attachments {
			subnetRef := attachment.GetSubnet()
			resolvedSubnet, resolveErr := resolveCatalogItemSubnet(ctx, subnetsDao, scope, subnetRef,
				fmt.Sprintf(" in fields.network_attachments[%d].subnet", i), " in fields.network_attachments", fmt.Sprintf(" in fields.network_attachments[%d]", i))
			if resolveErr != nil {
				return resolveErr
			}
			attachment.SetSubnet(canonicalSubnetLocalReference(resolvedSubnet))
			virtualNetworkID := refKey(resolvedSubnet.GetSpec().GetVirtualNetwork())
			for j, securityGroupRef := range attachment.GetSecurityGroups() {
				if securityGroupRef == nil {
					return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'fields.network_attachments[%d].security_groups[%d]' must not be null", i, j)
				}
				resolvedSecurityGroup, resolveErr := resolveCatalogItemSecurityGroup(ctx, securityGroupsDao, scope, securityGroupRef,
					fmt.Sprintf(" in fields.network_attachments[%d].security_groups[%d]", i, j), " in fields.network_attachments", fmt.Sprintf(" in fields.network_attachments[%d]", i), virtualNetworkID)
				if resolveErr != nil {
					return resolveErr
				}
				attachment.GetSecurityGroups()[j] = canonicalSecurityGroupLocalReference(resolvedSecurityGroup)
			}
			if err := validateBareMetalSubnetFabricManager(ctx, resolvedSubnet, fmt.Sprintf("fields.network_attachments[%d]", i), virtualNetworksDao, networkClassesDao, logger); err != nil {
				return err
			}
		}
		// The Template selects the HostType whose physical interfaces must support the attachment list.
		if hostTypeID := template.GetHostType(); hostTypeID != "" {
			ref := privatev1.HostTypeReference_builder{Id: hostTypeID}.Build()
			hostType, err := resolveAndCanonicalizeLockedReference(ctx, hostTypesDao,
				template.GetMetadata(), ref, "host type", grpccodes.InvalidArgument)
			if err != nil {
				return err
			}
			if err := validateBareMetalAttachmentsForHostType("fields.network_attachments", attachments, hostType); err != nil {
				return err
			}
		}
		return nil
	}
	if state.hasLocked {
		if err := validateAttachments(state.lockedValue); err != nil {
			return err
		}
	}
	if state.hasDefault {
		if err := validateAttachments(state.defaultValue); err != nil {
			return err
		}
	}
	return nil
}

// decodeBareMetalInstanceRunStrategyPolicy decodes the selected locked/default policy value without mutating the policy.
// An absent policy yields no governed value; malformed behavior returns an error.
// Presence is retained even for explicit scalar zero values.
func decodeBareMetalInstanceRunStrategyPolicy(
	policy *privatev1.BareMetalInstanceRunStrategyFieldPolicy,
) (policyState[privatev1.BareMetalInstanceRunStrategy], error) {
	if policy == nil {
		return policyState[privatev1.BareMetalInstanceRunStrategy]{}, nil
	}
	if policy.HasLocked() {
		return policyState[privatev1.BareMetalInstanceRunStrategy]{hasLocked: true, lockedValue: policy.GetLocked()}, nil
	}
	if policy.HasEditable() {
		editable := policy.GetEditable()
		if editable == nil {
			return policyState[privatev1.BareMetalInstanceRunStrategy]{}, fmt.Errorf("editable bare metal run strategy policy is empty")
		}
		return policyState[privatev1.BareMetalInstanceRunStrategy]{
			hasDefault:   editable.HasDefaultValue(),
			defaultValue: editable.GetDefaultValue(),
		}, nil
	}
	return policyState[privatev1.BareMetalInstanceRunStrategy]{}, fmt.Errorf("bare metal run strategy policy has no behavior")
}

// decodeBareMetalInstanceTypeReferencePolicy decodes the selected locked/default policy value without mutating the policy.
// An absent policy yields no governed value; malformed behavior returns an error.
// Returned message/list values may alias the policy and must be copied before resource assignment.
func decodeBareMetalInstanceTypeReferencePolicy(
	policy *privatev1.BareMetalInstanceTypeLocalReferenceFieldPolicy,
) (policyState[*privatev1.BareMetalInstanceTypeLocalReference], error) {
	if policy == nil {
		return policyState[*privatev1.BareMetalInstanceTypeLocalReference]{}, nil
	}
	if policy.HasLocked() {
		locked := policy.GetLocked()
		if locked == nil {
			return policyState[*privatev1.BareMetalInstanceTypeLocalReference]{}, fmt.Errorf("locked bare metal instance type policy is empty")
		}
		return policyState[*privatev1.BareMetalInstanceTypeLocalReference]{hasLocked: true, lockedValue: locked}, nil
	}
	if policy.HasEditable() {
		editable := policy.GetEditable()
		if editable == nil {
			return policyState[*privatev1.BareMetalInstanceTypeLocalReference]{}, fmt.Errorf("editable bare metal instance type policy is empty")
		}
		return policyState[*privatev1.BareMetalInstanceTypeLocalReference]{
			hasDefault:   editable.GetDefaultValue() != nil,
			defaultValue: editable.GetDefaultValue(),
		}, nil
	}
	return policyState[*privatev1.BareMetalInstanceTypeLocalReference]{}, fmt.Errorf("bare metal instance type policy has no behavior")
}

// decodeBareMetalInstanceNetworkAttachmentListPolicy decodes the selected locked/default policy value without mutating the policy.
// An absent policy yields no governed value; malformed behavior returns an error.
// Returned message/list values may alias the policy and must be copied before resource assignment.
func decodeBareMetalInstanceNetworkAttachmentListPolicy(
	policy *privatev1.BareMetalNetworkAttachmentListFieldPolicy,
) (policyState[[]*privatev1.BareMetalNetworkAttachment], error) {
	if policy == nil {
		return policyState[[]*privatev1.BareMetalNetworkAttachment]{}, nil
	}
	if policy.HasLocked() {
		locked := policy.GetLocked()
		if locked == nil {
			return policyState[[]*privatev1.BareMetalNetworkAttachment]{}, fmt.Errorf("locked network attachments policy is empty")
		}
		return policyState[[]*privatev1.BareMetalNetworkAttachment]{hasLocked: true, lockedValue: locked.GetItems()}, nil
	}
	if policy.HasEditable() {
		editable := policy.GetEditable()
		if editable == nil {
			return policyState[[]*privatev1.BareMetalNetworkAttachment]{}, fmt.Errorf("editable network attachments policy is empty")
		}
		defaultValue := editable.GetDefaultValue()
		if defaultValue == nil {
			return policyState[[]*privatev1.BareMetalNetworkAttachment]{}, nil
		}
		return policyState[[]*privatev1.BareMetalNetworkAttachment]{hasDefault: true, defaultValue: defaultValue.GetItems()}, nil
	}
	return policyState[[]*privatev1.BareMetalNetworkAttachment]{}, fmt.Errorf("network attachments policy has no behavior")
}

// cloneBareMetalInstanceNetworkAttachments copies the collection and its protobuf values, retaining nil entries.
// The result can be modified without changing the source policy.
func cloneBareMetalInstanceNetworkAttachments(value []*privatev1.BareMetalNetworkAttachment) []*privatev1.BareMetalNetworkAttachment {
	if value == nil {
		return nil
	}
	result := make([]*privatev1.BareMetalNetworkAttachment, len(value))
	for i, attachment := range value {
		if attachment != nil {
			result[i] = cloneMessage(attachment)
		}
	}
	return result
}
