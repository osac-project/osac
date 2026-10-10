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
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/utils"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// policyState records whether a field has a locked value or an editable default. It keeps
// explicit zero, false, and empty-string values distinct from an omitted default. Reference
// values may still point into the policy and must be copied before assignment to a resource.
type policyState[T any] struct {
	hasLocked    bool
	lockedValue  T
	hasDefault   bool
	defaultValue T
}

// applyPolicy applies one Catalog Item field rule to an object being created. A locked value
// rejects any caller-supplied value, even an equal one. An editable value keeps caller input or
// fills an omitted field from the policy default. The caller supplies the presence check and
// copy function; reference lookup and other defaulting happen outside this helper.
func applyPolicy[T any, P any](policy *P, present bool, set func(T), decode func(*P) (policyState[T], error), clone func(T) T) error {
	if policy == nil {
		return nil
	}
	state, err := decode(policy)
	if err != nil {
		return err
	}
	if present {
		if state.hasLocked {
			return fmt.Errorf("field is not editable")
		}
		return nil
	}
	if state.hasLocked {
		set(clone(state.lockedValue))
	} else if state.hasDefault {
		set(clone(state.defaultValue))
	}
	return nil
}

// cloneMessage copies a protobuf value so changing the destination cannot change its source.
func cloneMessage[T proto.Message](value T) T {
	return proto.Clone(value).(T)
}

// identity returns a scalar unchanged; scalar policy values need no deep copy.
func identity[T any](value T) T { return value }

// decodeBoolPolicy decodes the selected locked/default policy value without mutating the policy.
// An absent policy yields no governed value; malformed behavior returns an error.
// Presence is retained even for explicit scalar zero values.
func decodeBoolPolicy(policy *privatev1.BoolFieldPolicy) (policyState[bool], error) {
	if policy == nil {
		return policyState[bool]{}, nil
	}
	if policy.HasLocked() {
		return policyState[bool]{hasLocked: true, lockedValue: policy.GetLocked()}, nil
	}
	if policy.HasEditable() {
		editable := policy.GetEditable()
		if editable == nil {
			return policyState[bool]{}, fmt.Errorf("editable boolean policy is empty")
		}
		return policyState[bool]{
			hasDefault:   editable.HasDefaultValue(),
			defaultValue: editable.GetDefaultValue(),
		}, nil
	}
	return policyState[bool]{}, fmt.Errorf("boolean policy has no behavior")
}

// decodeInt32Policy decodes the selected locked/default policy value without mutating the policy.
// An absent policy yields no governed value; malformed behavior returns an error.
// Presence is retained even for explicit scalar zero values.
func decodeInt32Policy(policy *privatev1.Int32FieldPolicy) (policyState[int32], error) {
	if policy == nil {
		return policyState[int32]{}, nil
	}
	if policy.HasLocked() {
		return policyState[int32]{hasLocked: true, lockedValue: policy.GetLocked()}, nil
	}
	if policy.HasEditable() {
		editable := policy.GetEditable()
		if editable == nil {
			return policyState[int32]{}, fmt.Errorf("editable int32 policy is empty")
		}
		return policyState[int32]{
			hasDefault:   editable.HasDefaultValue(),
			defaultValue: editable.GetDefaultValue(),
		}, nil
	}
	return policyState[int32]{}, fmt.Errorf("int32 policy has no behavior")
}

// decodeStringPolicy decodes the selected locked/default policy value without mutating the policy.
// An absent policy yields no governed value; malformed behavior returns an error.
// Presence is retained even for explicit scalar zero values.
func decodeStringPolicy(policy *privatev1.StringFieldPolicy) (policyState[string], error) {
	if policy == nil {
		return policyState[string]{}, nil
	}
	if policy.HasLocked() {
		return policyState[string]{hasLocked: true, lockedValue: policy.GetLocked()}, nil
	}
	if policy.HasEditable() {
		editable := policy.GetEditable()
		if editable == nil {
			return policyState[string]{}, fmt.Errorf("editable string policy is empty")
		}
		return policyState[string]{
			hasDefault:   editable.HasDefaultValue(),
			defaultValue: editable.GetDefaultValue(),
		}, nil
	}
	return policyState[string]{}, fmt.Errorf("string policy has no behavior")
}

// decodeDiskImageReferencePolicy decodes the selected locked/default policy value without mutating the policy.
// An absent policy yields no governed value; malformed behavior returns an error.
// Returned message/list values may alias the policy and must be copied before resource assignment.
func decodeDiskImageReferencePolicy(policy *privatev1.DiskImageReferenceFieldPolicy) (policyState[*privatev1.DiskImageReference], error) {
	if policy == nil {
		return policyState[*privatev1.DiskImageReference]{}, nil
	}
	if policy.HasLocked() {
		locked := policy.GetLocked()
		if locked == nil {
			return policyState[*privatev1.DiskImageReference]{}, fmt.Errorf("locked disk image policy is empty")
		}
		return policyState[*privatev1.DiskImageReference]{hasLocked: true, lockedValue: locked}, nil
	}
	if policy.HasEditable() {
		editable := policy.GetEditable()
		if editable == nil {
			return policyState[*privatev1.DiskImageReference]{}, fmt.Errorf("editable disk image policy is empty")
		}
		return policyState[*privatev1.DiskImageReference]{
			hasDefault:   editable.GetDefaultValue() != nil,
			defaultValue: editable.GetDefaultValue(),
		}, nil
	}
	return policyState[*privatev1.DiskImageReference]{}, fmt.Errorf("disk image policy has no behavior")
}

// catalogItemPolicyError returns an InvalidArgument error identifying the invalid policy field and reason.
func catalogItemPolicyError(field, reason string) error {
	return grpcstatus.Errorf(grpccodes.InvalidArgument, "field '%s': %s", field, reason)
}

// validateCatalogItemStringPolicy checks the selected locked/default string with validate without changing the policy.
// Absent policies and editable policies without defaults have no value to validate.
func validateCatalogItemStringPolicy(policy *privatev1.StringFieldPolicy, field string, validate func(string) error) error {
	state, err := decodeStringPolicy(policy)
	if err != nil {
		return catalogItemPolicyError(field, err.Error())
	}
	if state.hasLocked {
		if err := validate(state.lockedValue); err != nil {
			return catalogItemPolicyError(field, err.Error())
		}
	}
	if state.hasDefault {
		if err := validate(state.defaultValue); err != nil {
			return catalogItemPolicyError(field, err.Error())
		}
	}
	return nil
}

// canonicalizeCatalogItemCIDRPolicy validates and replaces locked/default cluster CIDRs with their canonical form.
// It mutates the detached policy and returns a field-qualified error for invalid CIDRs.
func canonicalizeCatalogItemCIDRPolicy(policy *privatev1.StringFieldPolicy, field string) error {
	state, err := decodeStringPolicy(policy)
	if err != nil {
		return catalogItemPolicyError(field, err.Error())
	}
	if state.hasLocked {
		canonical, canonicalErr := utils.CanonicalizeCIDR(state.lockedValue)
		if canonicalErr != nil {
			return catalogItemPolicyError(field, canonicalErr.Error())
		}
		policy.SetLocked(canonical)
	}
	if state.hasDefault {
		canonical, canonicalErr := utils.CanonicalizeCIDR(state.defaultValue)
		if canonicalErr != nil {
			return catalogItemPolicyError(field, canonicalErr.Error())
		}
		policy.GetEditable().SetDefaultValue(canonical)
	}
	return nil
}

// validateCatalogItemInt32Policy checks the selected locked/default integer with validate, preserving explicit zero presence.
// It returns a field-qualified error without changing the policy.
func validateCatalogItemInt32Policy(policy *privatev1.Int32FieldPolicy, field string, validate func(int32) error) error {
	state, err := decodeInt32Policy(policy)
	if err != nil {
		return catalogItemPolicyError(field, err.Error())
	}
	if state.hasLocked {
		if err := validate(state.lockedValue); err != nil {
			return catalogItemPolicyError(field, err.Error())
		}
	}
	if state.hasDefault {
		if err := validate(state.defaultValue); err != nil {
			return catalogItemPolicyError(field, err.Error())
		}
	}
	return nil
}

// validateCatalogItemBoolPolicy checks that a boolean policy selects a valid behavior without changing its value.
func validateCatalogItemBoolPolicy(policy *privatev1.BoolFieldPolicy, field string) error {
	if _, err := decodeBoolPolicy(policy); err != nil {
		return catalogItemPolicyError(field, err.Error())
	}
	return nil
}

// validateSharedCatalogItemLocalReferencePolicy rejects a shared offering that fixes a
// tenant-local dependency. Other tenants could not use that locked value or default; an
// editable field without a default lets each caller supply its own local reference.
func validateSharedCatalogItemLocalReferencePolicy(scope referenceScope, field string, hasLocked, hasDefault bool) error {
	if scope.tenant == auth.SharedTenant && (hasLocked || hasDefault) {
		return catalogItemPolicyError(field, "shared catalog items cannot define a locked or default tenant-local reference")
	}
	return nil
}

// validateCatalogItemNetworkAttachmentsNotEmpty rejects an explicitly configured empty locked/default attachment list.
// An absent policy or an editable policy without a default imposes no list value.
func validateCatalogItemNetworkAttachmentsNotEmpty[T any](field string, state policyState[[]T]) error {
	if (state.hasLocked && len(state.lockedValue) == 0) || (state.hasDefault && len(state.defaultValue) == 0) {
		return catalogItemPolicyError(field, "locked/default network attachments must not be empty")
	}
	return nil
}

// validateCatalogItemDiskImagePolicy checks a locked image or editable image default when an
// offering is saved. A name-only reference prefers the item's tenant image, then a shared image;
// explicit project/shared selectors choose their scope. It stores the image's ID/name/scope in
// the policy and holds a dependency lock through the request transaction.
func validateCatalogItemDiskImagePolicy(
	ctx context.Context,
	scope referenceScope,
	policy *privatev1.DiskImageReferenceFieldPolicy,
	resourceDao *dao.GenericDAO[*privatev1.DiskImage],
) ([]string, error) {
	state, err := decodeDiskImageReferencePolicy(policy)
	if err != nil {
		return nil, catalogItemPolicyError("fields.disk_image", err.Error())
	}
	if !state.hasLocked && !state.hasDefault {
		return nil, nil
	}
	ref := state.defaultValue
	if state.hasLocked {
		ref = state.lockedValue
	}
	resolved, err := resolveLockedDiskImageReference(ctx, resourceDao, scope, ref, " in fields.disk_image")
	if err != nil {
		return nil, err
	}
	warnings, err := validateResolvedDiskImage(resolved, refKey(ref), " in fields.disk_image")
	if err != nil {
		return nil, err
	}
	if state.hasLocked {
		policy.SetLocked(canonicalDiskImageReference(resolved))
	} else {
		policy.GetEditable().SetDefaultValue(canonicalDiskImageReference(resolved))
	}
	return warnings, nil
}

// resolveCatalogItemSubnet finds a local subnet in the Catalog Item's exact tenant/project,
// then rejects it if deletion has started or it is not ready. The caller fills the policy
// reference from the returned subnet.
func resolveCatalogItemSubnet(
	ctx context.Context,
	resourceDao *dao.GenericDAO[*privatev1.Subnet],
	scope referenceScope,
	ref *privatev1.SubnetLocalReference,
	lookupSource, deletionSource, attachmentSource string,
) (*privatev1.Subnet, error) {
	resolved, err := resolveLockedResourceInScope(ctx, resourceDao, scope, ref.GetId(), ref.GetName(), "subnet", lookupSource, grpccodes.InvalidArgument)
	if err != nil {
		return nil, err
	}
	if err := validateResourceNotDeleted("subnet", refKey(ref), deletionSource, resolved.GetMetadata()); err != nil {
		return nil, err
	}
	if err := validateResolvedSubnetReady(resolved, refKey(ref), attachmentSource); err != nil {
		return nil, err
	}

	return resolved, nil
}

// resolveCatalogItemSecurityGroup finds a local security group in the Catalog Item's exact
// tenant/project. It checks deletion, readiness, and whether the group belongs to the selected
// subnet's virtual network. An empty virtualNetworkID skips the last check.
func resolveCatalogItemSecurityGroup(
	ctx context.Context,
	resourceDao *dao.GenericDAO[*privatev1.SecurityGroup],
	scope referenceScope,
	ref *privatev1.SecurityGroupLocalReference,
	lookupSource, deletionSource, attachmentSource, virtualNetworkID string,
) (*privatev1.SecurityGroup, error) {
	resolved, err := resolveLockedResourceInScope(ctx, resourceDao, scope, ref.GetId(), ref.GetName(), "security group", lookupSource, grpccodes.InvalidArgument)
	if err != nil {
		return nil, err
	}
	if err := validateResourceNotDeleted("security group", refKey(ref), deletionSource, resolved.GetMetadata()); err != nil {
		return nil, err
	}
	if err := validateResolvedSecurityGroup(resolved, refKey(ref), attachmentSource, virtualNetworkID); err != nil {
		return nil, err
	}

	return resolved, nil
}
