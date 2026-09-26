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

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/computeinstancespec"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/utils"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// validateAndCanonicalizeComputeInstanceCatalogItemPolicies checks the locked values and editable
// defaults of the Catalog Item being saved. It resolves image, instance type, storage, and network
// references using each field's full or local reference rules, then stores target IDs and names
// in the policy. The request transaction holds dependency locks until the save ends.
// On error, the caller discards this copy of the item. Deprecated targets produce warnings.
func validateAndCanonicalizeComputeInstanceCatalogItemPolicies(
	ctx context.Context,
	item *privatev1.ComputeInstanceCatalogItem,
	instanceTypesDao *dao.GenericDAO[*privatev1.InstanceType],
	diskImagesDao *dao.GenericDAO[*privatev1.DiskImage],
	secretsDao *dao.GenericDAO[*privatev1.Secret],
	storageTiersDao *dao.GenericDAO[*privatev1.StorageTier],
	subnetsDao *dao.GenericDAO[*privatev1.Subnet],
	securityGroupsDao *dao.GenericDAO[*privatev1.SecurityGroup],
) ([]string, error) {
	scope := catalogItemScope(item)
	fields := item.GetFields()
	if fields == nil {
		return nil, nil
	}

	warnings, err := validateComputeInstanceCatalogItemInstanceTypePolicy(ctx, scope, fields.GetInstanceType(), instanceTypesDao)
	if err != nil {
		return nil, err
	}
	imageWarnings, err := validateCatalogItemDiskImagePolicy(ctx, scope, fields.GetDiskImage(), diskImagesDao)
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, imageWarnings...)
	if err := validateComputeInstanceCatalogItemSSHKeyPolicy(ctx, scope, fields.GetSshKey(), secretsDao); err != nil {
		return nil, err
	}

	if err := validateComputeInstanceCatalogItemBootDiskPolicy(ctx, fields.GetBootDisk(), storageTiersDao); err != nil {
		return nil, err
	}

	if err := validateComputeInstanceCatalogItemAdditionalDisksPolicy(ctx, fields.GetAdditionalDisks(), storageTiersDao); err != nil {
		return nil, err
	}

	if err := validateComputeInstanceCatalogItemNetworkAttachmentsPolicy(ctx, scope, fields.GetNetworkAttachments(), subnetsDao, securityGroupsDao); err != nil {
		return nil, err
	}

	return warnings, validateComputeInstanceCatalogItemScalarPolicies(fields)
}

// validateComputeInstanceCatalogItemSSHKeyPolicy checks a locked SSH key or editable default
// in the Catalog Item's exact tenant/project, verifies its type, and stores its ID and name.
// Shared offerings may leave the key editable but cannot choose a tenant-local key.
func validateComputeInstanceCatalogItemSSHKeyPolicy(
	ctx context.Context,
	scope referenceScope,
	policy *privatev1.SecretReferenceFieldPolicy,
	secretsDao *dao.GenericDAO[*privatev1.Secret],
) error {
	if policy == nil {
		return nil
	}
	state, err := decodeSecretReferencePolicy(policy)
	if err != nil {
		return catalogItemPolicyError("fields.ssh_key", err.Error())
	}
	if err := validateSharedCatalogItemLocalReferencePolicy(scope, "fields.ssh_key", state.hasLocked, state.hasDefault); err != nil {
		return err
	}
	resolve := func(ref *privatev1.SecretLocalReference) (*privatev1.SecretLocalReference, error) {
		if ref == nil {
			return nil, nil
		}
		resolved, resolveErr := resolveLockedResourceInScope(ctx, secretsDao, scope, ref.GetId(), ref.GetName(),
			"secret", " in fields.ssh_key", grpccodes.InvalidArgument)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if err := validateResolvedSecretLifecycleAndType(resolved, refKey(ref), "fields.ssh_key",
			privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY); err != nil {
			return nil, err
		}
		return canonicalSecretLocalReference(resolved), nil
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

// applyComputeInstanceCatalogItemPolicies merges the offering's field rules into a new VM spec.
// It rejects caller values for locked fields, keeps caller values for editable fields, and copies
// locked/default values into omitted fields. Explicit zero, false, and empty strings count as
// supplied; empty collections follow their existing omitted-input behavior. The caller discards
// this spec on error and resolves copied references and Template defaults afterward.
func applyComputeInstanceCatalogItemPolicies(spec *privatev1.ComputeInstanceSpec, fields *privatev1.ComputeInstanceCatalogItemFields) error {
	if spec == nil || fields == nil {
		return nil
	}
	if err := applyPolicy(fields.GetDiskImage(), spec.HasDiskImage(), spec.SetDiskImage, decodeDiskImageReferencePolicy, cloneMessage[*privatev1.DiskImageReference]); err != nil {
		return fmt.Errorf("disk_image: %w", err)
	}
	if err := applyPolicy(fields.GetSshKey(), spec.GetSshKey() != nil, spec.SetSshKey, decodeSecretReferencePolicy, cloneMessage[*privatev1.SecretLocalReference]); err != nil {
		return fmt.Errorf("ssh_key: %w", err)
	}
	if err := applyPolicy(fields.GetInstanceType(), spec.HasInstanceType(), spec.SetInstanceType, decodeInstanceTypeReferencePolicy, cloneMessage[*privatev1.InstanceTypeReference]); err != nil {
		return fmt.Errorf("instance_type: %w", err)
	}
	if err := applyPolicy(fields.GetRunStrategy(), spec.HasRunStrategy(), spec.SetRunStrategy, decodeComputeInstanceRunStrategyPolicy, identity[privatev1.ComputeInstanceRunStrategy]); err != nil {
		return fmt.Errorf("run_strategy: %w", err)
	}
	if err := applyPolicy(fields.GetUserData(), spec.HasUserData(), spec.SetUserData, decodeStringPolicy, identity[string]); err != nil {
		return fmt.Errorf("user_data: %w", err)
	}
	if err := applyPolicy(fields.GetAutoExternalIpAttachment(), spec.HasAutoExternalIpAttachment(), spec.SetAutoExternalIpAttachment, decodeBoolPolicy, identity[bool]); err != nil {
		return fmt.Errorf("auto_external_ip_attachment: %w", err)
	}
	if err := applyPolicy(fields.GetNetworkAttachments(), len(spec.GetNetworkAttachments()) > 0, spec.SetNetworkAttachments, decodeComputeInstanceNetworkAttachmentListPolicy, cloneComputeInstanceNetworkAttachments); err != nil {
		return fmt.Errorf("network_attachments: %w", err)
	}
	if err := applyPolicy(fields.GetAdditionalDisks(), len(spec.GetAdditionalDisks()) > 0, spec.SetAdditionalDisks, decodeComputeInstanceDiskListPolicy, cloneComputeInstanceDisks); err != nil {
		return fmt.Errorf("additional_disks: %w", err)
	}
	return applyComputeInstanceCatalogItemBootDiskPolicies(spec, fields.GetBootDisk())
}

// applyComputeInstanceCatalogItemBootDiskPolicies applies size and tier policies to the detached spec.
// Presence is captured before applying defaults; nested disk allocation is deferred until a value is assigned.
func applyComputeInstanceCatalogItemBootDiskPolicies(
	spec *privatev1.ComputeInstanceSpec,
	bootFields *privatev1.ComputeInstanceBootDiskFieldPolicies,
) error {
	bootDisk := spec.GetBootDisk()
	if bootFields != nil {
		if err := applyPolicy(bootFields.GetSizeGib(), bootDisk != nil && bootDisk.HasSizeGib(), func(value int32) {
			if spec.GetBootDisk() == nil {
				spec.SetBootDisk(&privatev1.ComputeInstanceDisk{})
			}
			spec.GetBootDisk().SetSizeGib(value)
		}, decodeInt32Policy, identity[int32]); err != nil {
			return fmt.Errorf("size_gib: %w", err)
		}
		if err := applyPolicy(bootFields.GetStorageTier(), bootDisk != nil && bootDisk.GetStorageTier() != nil, func(value *privatev1.StorageTierReference) {
			if spec.GetBootDisk() == nil {
				spec.SetBootDisk(&privatev1.ComputeInstanceDisk{})
			}
			spec.GetBootDisk().SetStorageTier(value)
		}, decodeStorageTierReferencePolicy, cloneMessage[*privatev1.StorageTierReference]); err != nil {
			return fmt.Errorf("storage_tier: %w", err)
		}
	}
	return nil
}

// validateComputeInstanceCatalogItemInstanceTypePolicy checks a locked instance type or editable
// default when the offering is saved. It stores the resolved ID/name/scope and warns if the type
// is deprecated.
func validateComputeInstanceCatalogItemInstanceTypePolicy(
	ctx context.Context,
	scope referenceScope,
	policy *privatev1.InstanceTypeReferenceFieldPolicy,
	resourceDao *dao.GenericDAO[*privatev1.InstanceType],
) ([]string, error) {
	state, err := decodeInstanceTypeReferencePolicy(policy)
	if err != nil {
		return nil, catalogItemPolicyError("fields.instance_type", err.Error())
	}
	if !state.hasLocked && !state.hasDefault {
		return nil, nil
	}
	ref := state.defaultValue
	if state.hasLocked {
		ref = state.lockedValue
	}
	resolved, err := resolveLockedFullResourceReference(ctx, resourceDao, scope, ref,
		"instance type", " in fields.instance_type", grpccodes.NotFound)
	if err != nil {
		return nil, err
	}
	warnings, err := validateResolvedInstanceType(resolved, refKey(ref), " in fields.instance_type")
	if err != nil {
		return nil, err
	}
	if state.hasLocked {
		policy.SetLocked(canonicalInstanceTypeReference(resolved))
	} else {
		policy.GetEditable().SetDefaultValue(canonicalInstanceTypeReference(resolved))
	}
	return warnings, nil
}

// validateComputeInstanceCatalogItemBootDiskPolicy checks boot-disk scalar policies and canonicalizes their shared storage-tier references.
func validateComputeInstanceCatalogItemBootDiskPolicy(
	ctx context.Context,
	policy *privatev1.ComputeInstanceBootDiskFieldPolicies,
	storageTiersDao *dao.GenericDAO[*privatev1.StorageTier],
) error {
	if policy == nil {
		return nil
	}
	if err := validateCatalogItemInt32Policy(policy.GetSizeGib(), "fields.boot_disk.size_gib", func(value int32) error {
		if value <= 0 {
			return fmt.Errorf("must be greater than zero")
		}
		return nil
	}); err != nil {
		return err
	}
	storagePolicy := policy.GetStorageTier()
	if storagePolicy == nil {
		return nil
	}
	state, err := decodeStorageTierReferencePolicy(storagePolicy)
	if err != nil {
		return catalogItemPolicyError("fields.boot_disk.storage_tier", err.Error())
	}
	resolve := func(ref *privatev1.StorageTierReference) (*privatev1.StorageTierReference, error) {
		if ref == nil {
			return nil, nil
		}
		resolved, resolveErr := resolveLockedResourceInScope(ctx, storageTiersDao, referenceScope{tenant: auth.SharedTenant}, ref.GetId(), ref.GetName(),
			"storage tier", " in fields.boot_disk.storage_tier", grpccodes.NotFound)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if err := validateResolvedStorageTier(resolved, " in fields.boot_disk.storage_tier"); err != nil {
			return nil, err
		}
		return canonicalStorageTierReference(resolved), nil
	}
	if state.hasLocked {
		canonical, resolveErr := resolve(state.lockedValue)
		if resolveErr != nil {
			return resolveErr
		}
		storagePolicy.SetLocked(canonical)
	}
	if state.hasDefault {
		canonical, resolveErr := resolve(state.defaultValue)
		if resolveErr != nil {
			return resolveErr
		}
		storagePolicy.GetEditable().SetDefaultValue(canonical)
	}
	return nil
}

// validateComputeInstanceCatalogItemAdditionalDisksPolicy validates each locked/default disk and canonicalizes its shared storage tier.
func validateComputeInstanceCatalogItemAdditionalDisksPolicy(
	ctx context.Context,
	policy *privatev1.ComputeInstanceDiskListFieldPolicy,
	storageTiersDao *dao.GenericDAO[*privatev1.StorageTier],
) error {
	if policy == nil {
		return nil
	}
	state, err := decodeComputeInstanceDiskListPolicy(policy)
	if err != nil {
		return catalogItemPolicyError("fields.additional_disks", err.Error())
	}
	// Additional-disk entries must be complete because Template defaults do not merge into this list.
	resolveDisks := func(disks []*privatev1.ComputeInstanceDisk) error {
		for i, disk := range disks {
			if err := utils.ValidateCompleteComputeInstanceDisk(disk); err != nil {
				return grpcstatus.Errorf(grpccodes.InvalidArgument,
					"field 'fields.additional_disks[%d]' is incomplete: %s", i, err)
			}
			ref := disk.GetStorageTier()
			resolved, resolveErr := resolveLockedResourceInScope(ctx, storageTiersDao, referenceScope{tenant: auth.SharedTenant}, ref.GetId(), ref.GetName(),
				"storage tier", fmt.Sprintf(" in fields.additional_disks[%d].storage_tier", i), grpccodes.NotFound)
			if resolveErr != nil {
				return resolveErr
			}
			if err := validateResolvedStorageTier(resolved, fmt.Sprintf(" in fields.additional_disks[%d].storage_tier", i)); err != nil {
				return err
			}
			disk.SetStorageTier(canonicalStorageTierReference(resolved))
		}
		return nil
	}
	if state.hasLocked {
		if err := resolveDisks(state.lockedValue); err != nil {
			return err
		}
	}
	if state.hasDefault {
		if err := resolveDisks(state.defaultValue); err != nil {
			return err
		}
	}
	return nil
}

// validateComputeInstanceCatalogItemNetworkAttachmentsPolicy checks each governed subnet and
// security group in the Catalog Item's exact tenant/project. It stores their IDs and names only
// after readiness and virtual-network compatibility checks pass.
func validateComputeInstanceCatalogItemNetworkAttachmentsPolicy(
	ctx context.Context,
	scope referenceScope,
	policy *privatev1.ComputeNetworkAttachmentListFieldPolicy,
	subnetsDao *dao.GenericDAO[*privatev1.Subnet],
	securityGroupsDao *dao.GenericDAO[*privatev1.SecurityGroup],
) error {
	if policy == nil {
		return nil
	}
	state, err := decodeComputeInstanceNetworkAttachmentListPolicy(policy)
	if err != nil {
		return catalogItemPolicyError("fields.network_attachments", err.Error())
	}
	if err := validateCatalogItemNetworkAttachmentsNotEmpty("fields.network_attachments", state); err != nil {
		return err
	}
	if err := validateSharedCatalogItemLocalReferencePolicy(scope, "fields.network_attachments", state.hasLocked, state.hasDefault); err != nil {
		return err
	}
	// Validate list structure before resolving and canonicalizing each attachment dependency.
	validateAttachments := func(attachments []*privatev1.ComputeNetworkAttachment) error {
		if err := computeinstancespec.ValidateNetworkAttachments(attachments); err != nil {
			return catalogItemPolicyError("fields.network_attachments", err.Error())
		}
		for i, attachment := range attachments {
			subnetRef := attachment.GetSubnet()
			resolvedSubnet, resolveErr := resolveCatalogItemSubnet(ctx, subnetsDao, scope, subnetRef,
				fmt.Sprintf(" in fields.network_attachments[%d].subnet", i), fmt.Sprintf(" in fields.network_attachments[%d].subnet", i), fmt.Sprintf(" in fields.network_attachments[%d]", i))
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
					fmt.Sprintf(" in fields.network_attachments[%d].security_groups[%d]", i, j), fmt.Sprintf(" in fields.network_attachments[%d].security_groups[%d]", i, j), fmt.Sprintf(" in fields.network_attachments[%d]", i), virtualNetworkID)
				if resolveErr != nil {
					return resolveErr
				}
				attachment.GetSecurityGroups()[j] = canonicalSecurityGroupLocalReference(resolvedSecurityGroup)
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

// validateComputeInstanceCatalogItemScalarPolicies checks the supported scalar policies and returns the first invalid value.
func validateComputeInstanceCatalogItemScalarPolicies(fields *privatev1.ComputeInstanceCatalogItemFields) error {
	if _, err := decodeComputeInstanceRunStrategyPolicy(fields.GetRunStrategy()); err != nil {
		return catalogItemPolicyError("fields.run_strategy", err.Error())
	}
	if _, err := decodeStringPolicy(fields.GetUserData()); err != nil {
		return catalogItemPolicyError("fields.user_data", err.Error())
	}
	return validateCatalogItemBoolPolicy(fields.GetAutoExternalIpAttachment(), "fields.auto_external_ip_attachment")
}

// decodeStorageTierReferencePolicy decodes the selected locked/default policy value without mutating the policy.
// An absent policy yields no governed value; malformed behavior returns an error.
// Returned message/list values may alias the policy and must be copied before resource assignment.
func decodeStorageTierReferencePolicy(policy *privatev1.StorageTierReferenceFieldPolicy) (policyState[*privatev1.StorageTierReference], error) {
	if policy == nil {
		return policyState[*privatev1.StorageTierReference]{}, nil
	}
	if policy.HasLocked() {
		locked := policy.GetLocked()
		if locked == nil {
			return policyState[*privatev1.StorageTierReference]{}, fmt.Errorf("locked storage tier policy is empty")
		}
		return policyState[*privatev1.StorageTierReference]{hasLocked: true, lockedValue: locked}, nil
	}
	if policy.HasEditable() {
		editable := policy.GetEditable()
		if editable == nil {
			return policyState[*privatev1.StorageTierReference]{}, fmt.Errorf("editable storage tier policy is empty")
		}
		return policyState[*privatev1.StorageTierReference]{
			hasDefault:   editable.GetDefaultValue() != nil,
			defaultValue: editable.GetDefaultValue(),
		}, nil
	}
	return policyState[*privatev1.StorageTierReference]{}, fmt.Errorf("storage tier policy has no behavior")
}

// decodeComputeInstanceRunStrategyPolicy decodes the selected locked/default policy value without mutating the policy.
// An absent policy yields no governed value; malformed behavior returns an error.
// Presence is retained even for explicit scalar zero values.
func decodeComputeInstanceRunStrategyPolicy(
	policy *privatev1.ComputeInstanceRunStrategyFieldPolicy,
) (policyState[privatev1.ComputeInstanceRunStrategy], error) {
	if policy == nil {
		return policyState[privatev1.ComputeInstanceRunStrategy]{}, nil
	}
	if policy.HasLocked() {
		return policyState[privatev1.ComputeInstanceRunStrategy]{hasLocked: true, lockedValue: policy.GetLocked()}, nil
	}
	if policy.HasEditable() {
		editable := policy.GetEditable()
		if editable == nil {
			return policyState[privatev1.ComputeInstanceRunStrategy]{}, fmt.Errorf("editable compute run strategy policy is empty")
		}
		return policyState[privatev1.ComputeInstanceRunStrategy]{
			hasDefault:   editable.HasDefaultValue(),
			defaultValue: editable.GetDefaultValue(),
		}, nil
	}
	return policyState[privatev1.ComputeInstanceRunStrategy]{}, fmt.Errorf("compute run strategy policy has no behavior")
}

// decodeInstanceTypeReferencePolicy decodes the selected locked/default policy value without mutating the policy.
// An absent policy yields no governed value; malformed behavior returns an error.
// Returned message/list values may alias the policy and must be copied before resource assignment.
func decodeInstanceTypeReferencePolicy(
	policy *privatev1.InstanceTypeReferenceFieldPolicy,
) (policyState[*privatev1.InstanceTypeReference], error) {
	if policy == nil {
		return policyState[*privatev1.InstanceTypeReference]{}, nil
	}
	if policy.HasLocked() {
		locked := policy.GetLocked()
		if locked == nil {
			return policyState[*privatev1.InstanceTypeReference]{}, fmt.Errorf("locked instance type policy is empty")
		}
		return policyState[*privatev1.InstanceTypeReference]{hasLocked: true, lockedValue: locked}, nil
	}
	if policy.HasEditable() {
		editable := policy.GetEditable()
		if editable == nil {
			return policyState[*privatev1.InstanceTypeReference]{}, fmt.Errorf("editable instance type policy is empty")
		}
		return policyState[*privatev1.InstanceTypeReference]{
			hasDefault:   editable.GetDefaultValue() != nil,
			defaultValue: editable.GetDefaultValue(),
		}, nil
	}
	return policyState[*privatev1.InstanceTypeReference]{}, fmt.Errorf("instance type policy has no behavior")
}

// decodeComputeInstanceDiskListPolicy decodes the selected locked/default policy value without mutating the policy.
// An absent policy yields no governed value; malformed behavior returns an error.
// Returned message/list values may alias the policy and must be copied before resource assignment.
func decodeComputeInstanceDiskListPolicy(
	policy *privatev1.ComputeInstanceDiskListFieldPolicy,
) (policyState[[]*privatev1.ComputeInstanceDisk], error) {
	if policy == nil {
		return policyState[[]*privatev1.ComputeInstanceDisk]{}, nil
	}
	if policy.HasLocked() {
		locked := policy.GetLocked()
		if locked == nil {
			return policyState[[]*privatev1.ComputeInstanceDisk]{}, fmt.Errorf("locked additional disks policy is empty")
		}
		return policyState[[]*privatev1.ComputeInstanceDisk]{hasLocked: true, lockedValue: locked.GetItems()}, nil
	}
	if policy.HasEditable() {
		editable := policy.GetEditable()
		if editable == nil {
			return policyState[[]*privatev1.ComputeInstanceDisk]{}, fmt.Errorf("editable additional disks policy is empty")
		}
		defaultValue := editable.GetDefaultValue()
		if defaultValue == nil {
			return policyState[[]*privatev1.ComputeInstanceDisk]{}, nil
		}
		return policyState[[]*privatev1.ComputeInstanceDisk]{hasDefault: true, defaultValue: defaultValue.GetItems()}, nil
	}
	return policyState[[]*privatev1.ComputeInstanceDisk]{}, fmt.Errorf("additional disks policy has no behavior")
}

// decodeComputeInstanceNetworkAttachmentListPolicy decodes the selected locked/default policy value without mutating the policy.
// An absent policy yields no governed value; malformed behavior returns an error.
// Returned message/list values may alias the policy and must be copied before resource assignment.
func decodeComputeInstanceNetworkAttachmentListPolicy(
	policy *privatev1.ComputeNetworkAttachmentListFieldPolicy,
) (policyState[[]*privatev1.ComputeNetworkAttachment], error) {
	if policy == nil {
		return policyState[[]*privatev1.ComputeNetworkAttachment]{}, nil
	}
	if policy.HasLocked() {
		locked := policy.GetLocked()
		if locked == nil {
			return policyState[[]*privatev1.ComputeNetworkAttachment]{}, fmt.Errorf("locked network attachments policy is empty")
		}
		return policyState[[]*privatev1.ComputeNetworkAttachment]{hasLocked: true, lockedValue: locked.GetItems()}, nil
	}
	if policy.HasEditable() {
		editable := policy.GetEditable()
		if editable == nil {
			return policyState[[]*privatev1.ComputeNetworkAttachment]{}, fmt.Errorf("editable network attachments policy is empty")
		}
		defaultValue := editable.GetDefaultValue()
		if defaultValue == nil {
			return policyState[[]*privatev1.ComputeNetworkAttachment]{}, nil
		}
		return policyState[[]*privatev1.ComputeNetworkAttachment]{hasDefault: true, defaultValue: defaultValue.GetItems()}, nil
	}
	return policyState[[]*privatev1.ComputeNetworkAttachment]{}, fmt.Errorf("network attachments policy has no behavior")
}

// cloneComputeInstanceDisks copies the collection and its protobuf values, retaining nil entries.
// The result can be modified without changing the source policy.
func cloneComputeInstanceDisks(value []*privatev1.ComputeInstanceDisk) []*privatev1.ComputeInstanceDisk {
	if value == nil {
		return nil
	}
	result := make([]*privatev1.ComputeInstanceDisk, len(value))
	for i, disk := range value {
		if disk != nil {
			result[i] = cloneMessage(disk)
		}
	}
	return result
}

// cloneComputeInstanceNetworkAttachments copies the collection and its protobuf values, retaining nil entries.
// The result can be modified without changing the source policy.
func cloneComputeInstanceNetworkAttachments(value []*privatev1.ComputeNetworkAttachment) []*privatev1.ComputeNetworkAttachment {
	if value == nil {
		return nil
	}
	result := make([]*privatev1.ComputeNetworkAttachment, len(value))
	for i, attachment := range value {
		if attachment != nil {
			result[i] = cloneMessage(attachment)
		}
	}
	return result
}
