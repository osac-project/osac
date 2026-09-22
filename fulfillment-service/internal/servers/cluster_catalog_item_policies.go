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

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// validateAndCanonicalizeClusterCatalogItemPolicies checks the locked values and editable defaults
// of the Cluster Catalog Item being saved. It resolves version, secret, network, and HostType
// references using each field's full or local reference rules, then stores target IDs and names
// in the policy. The request transaction holds dependency locks until the save ends.
// On error, the caller discards this copy of the item; Template parameters are checked separately.
func validateAndCanonicalizeClusterCatalogItemPolicies(
	ctx context.Context,
	item *privatev1.ClusterCatalogItem,
	template *privatev1.ClusterTemplate,
	hostTypesDao *dao.GenericDAO[*privatev1.HostType],
	clusterVersionsDao *dao.GenericDAO[*privatev1.ClusterVersion],
	secretsDao *dao.GenericDAO[*privatev1.Secret],
	subnetsDao *dao.GenericDAO[*privatev1.Subnet],
	securityGroupsDao *dao.GenericDAO[*privatev1.SecurityGroup],
) error {
	if item == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "catalog item is mandatory")
	}
	fields := item.GetFields()
	if fields == nil {
		return nil
	}
	scope := catalogItemScope(item)

	if err := validateClusterCatalogItemVersionPolicy(ctx, scope, fields.GetVersion(), clusterVersionsDao); err != nil {
		return err
	}

	if err := validateClusterCatalogItemPullSecretPolicy(ctx, scope, fields.GetPullSecretSecret(), secretsDao); err != nil {
		return err
	}

	if err := validateClusterCatalogItemNetworkCIDRPolicies(fields.GetNetwork()); err != nil {
		return err
	}

	if err := validateClusterCatalogItemScalarPolicies(fields); err != nil {
		return err
	}

	if err := validateClusterCatalogItemNetworkAttachmentPolicy(ctx, scope, fields.GetNetworkAttachment(), subnetsDao, securityGroupsDao); err != nil {
		return err
	}
	return validateClusterCatalogItemNodeSetPolicy(ctx, item, template, hostTypesDao)
}

// applyClusterCatalogItemPolicies merges the offering's field rules into a new Cluster spec.
// It rejects caller values for locked fields, keeps caller values for editable fields, and copies
// locked/default values into omitted fields. Explicit zero, false, and empty strings count as
// supplied; empty collections follow their existing omitted-input behavior. The caller discards
// this spec on error and resolves copied references and Template defaults afterward.
func applyClusterCatalogItemPolicies(spec *privatev1.ClusterSpec, fields *privatev1.ClusterCatalogItemFields) error {
	if spec == nil || fields == nil {
		return nil
	}
	if err := applyPolicy(fields.GetVersion(), spec.GetVersion() != nil, spec.SetVersion, decodeClusterVersionReferencePolicy, cloneMessage[*privatev1.ClusterVersionReference]); err != nil {
		return fmt.Errorf("version: %w", err)
	}
	if err := applyPolicy(fields.GetSshPublicKey(), spec.HasSshPublicKey(), spec.SetSshPublicKey, decodeStringPolicy, identity[string]); err != nil {
		return fmt.Errorf("ssh_public_key: %w", err)
	}
	if err := applyPolicy(fields.GetPullSecretSecret(), spec.GetPullSecretSecret() != nil, spec.SetPullSecretSecret, decodeSecretReferencePolicy, cloneMessage[*privatev1.SecretLocalReference]); err != nil {
		return fmt.Errorf("pull_secret_secret: %w", err)
	}
	if err := applyPolicy(fields.GetAutoExternalIpAttachment(), spec.HasAutoExternalIpAttachment(), spec.SetAutoExternalIpAttachment, decodeBoolPolicy, identity[bool]); err != nil {
		return fmt.Errorf("auto_external_ip_attachment: %w", err)
	}
	if err := applyPolicy(fields.GetNetworkAttachment(), spec.GetNetworkAttachment() != nil, spec.SetNetworkAttachment, decodeClusterNetworkAttachmentPolicy, cloneMessage[*privatev1.ClusterNetworkAttachment]); err != nil {
		return fmt.Errorf("network_attachment: %w", err)
	}
	if err := applyPolicy(fields.GetNodeSets(), len(spec.GetNodeSets()) > 0, spec.SetNodeSets, decodeClusterNodeSetMapPolicy, cloneClusterNodeSets); err != nil {
		return fmt.Errorf("node_sets: %w", err)
	}
	return applyClusterCatalogItemNetworkPolicies(spec, fields.GetNetwork())
}

// applyClusterCatalogItemNetworkPolicies applies pod and service CIDR policies to a detached spec.
// It preserves supplied-field presence and allocates network settings only when assigning a policy value.
func applyClusterCatalogItemNetworkPolicies(spec *privatev1.ClusterSpec, networkPolicies *privatev1.ClusterNetworkFieldPolicies) error {
	network := spec.GetNetwork()
	ensureNetwork := func() *privatev1.ClusterNetwork {
		if spec.GetNetwork() == nil {
			spec.SetNetwork(&privatev1.ClusterNetwork{})
		}
		return spec.GetNetwork()
	}
	if err := applyPolicy(networkPolicies.GetPodCidr(), network != nil && network.HasPodCidr(), func(value string) {
		ensureNetwork().SetPodCidr(value)
	}, decodeStringPolicy, identity[string]); err != nil {
		return fmt.Errorf("pod_cidr: %w", err)
	}
	if err := applyPolicy(networkPolicies.GetServiceCidr(), network != nil && network.HasServiceCidr(), func(value string) {
		ensureNetwork().SetServiceCidr(value)
	}, decodeStringPolicy, identity[string]); err != nil {
		return fmt.Errorf("service_cidr: %w", err)
	}
	return nil
}

// validateClusterCatalogItemVersionPolicy resolves and canonicalizes a locked/default version and checks its lifecycle.
func validateClusterCatalogItemVersionPolicy(
	ctx context.Context,
	scope referenceScope,
	policy *privatev1.ClusterVersionReferenceFieldPolicy,
	clusterVersionsDao *dao.GenericDAO[*privatev1.ClusterVersion],
) error {
	if policy == nil {
		return nil
	}
	state, err := decodeClusterVersionReferencePolicy(policy)
	if err != nil {
		return catalogItemPolicyError("fields.version", err.Error())
	}
	resolve := func(ref *privatev1.ClusterVersionReference) (*privatev1.ClusterVersionReference, error) {
		if ref == nil {
			return nil, nil
		}
		resolved, resolveErr := resolveLockedFullResourceReference(ctx, clusterVersionsDao, scope, ref,
			"cluster version", " in fields.version", grpccodes.InvalidArgument)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if err := validateResolvedClusterVersion(resolved, refKey(ref), " in fields.version"); err != nil {
			return nil, err
		}
		return buildClusterVersionReference(resolved), nil
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

// validateClusterCatalogItemPullSecretPolicy checks a locked pull secret or editable default
// in the Catalog Item's exact tenant/project, verifies its type, and stores its ID/name.
// A shared offering cannot set a tenant-local secret for every tenant, so it may only leave
// this field editable.
func validateClusterCatalogItemPullSecretPolicy(
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
		return catalogItemPolicyError("fields.pull_secret_secret", err.Error())
	}
	if err := validateSharedCatalogItemLocalReferencePolicy(scope, "fields.pull_secret_secret", state.hasLocked, state.hasDefault); err != nil {
		return err
	}
	resolve := func(ref *privatev1.SecretLocalReference) (*privatev1.SecretLocalReference, error) {
		if ref == nil {
			return nil, nil
		}
		resolved, resolveErr := resolveLockedResourceInScope(ctx, secretsDao, scope, ref.GetId(), ref.GetName(),
			"secret", " in fields.pull_secret_secret", grpccodes.InvalidArgument)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if err := validateResolvedSecretLifecycleAndType(resolved, refKey(ref), "fields.pull_secret_secret",
			privatev1.SecretType_SECRET_TYPE_PULL_SECRET); err != nil {
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

// validateClusterCatalogItemNetworkCIDRPolicies validates and canonicalizes configured pod and service CIDRs.
func validateClusterCatalogItemNetworkCIDRPolicies(network *privatev1.ClusterNetworkFieldPolicies) error {
	if network == nil {
		return nil
	}
	if err := canonicalizeCatalogItemCIDRPolicy(network.GetPodCidr(), "fields.network.pod_cidr"); err != nil {
		return err
	}
	return canonicalizeCatalogItemCIDRPolicy(network.GetServiceCidr(), "fields.network.service_cidr")
}

// validateClusterCatalogItemScalarPolicies checks SSH-key and automatic-external-IP policies without changing them.
func validateClusterCatalogItemScalarPolicies(fields *privatev1.ClusterCatalogItemFields) error {
	if err := validateCatalogItemStringPolicy(fields.GetSshPublicKey(), "fields.ssh_public_key", validateOpenSSHPublicKey); err != nil {
		return err
	}
	if err := validateCatalogItemBoolPolicy(fields.GetAutoExternalIpAttachment(), "fields.auto_external_ip_attachment"); err != nil {
		return err
	}

	return nil
}

// validateClusterCatalogItemNetworkAttachmentPolicy checks the governed subnet and security
// groups in the Catalog Item's exact tenant/project. It stores their IDs and names only after
// readiness and virtual-network compatibility checks pass.
func validateClusterCatalogItemNetworkAttachmentPolicy(
	ctx context.Context,
	scope referenceScope,
	policy *privatev1.ClusterNetworkAttachmentFieldPolicy,
	subnetsDao *dao.GenericDAO[*privatev1.Subnet],
	securityGroupsDao *dao.GenericDAO[*privatev1.SecurityGroup],
) error {
	if policy == nil {
		return nil
	}
	state, err := decodeClusterNetworkAttachmentPolicy(policy)
	if err != nil {
		return catalogItemPolicyError("fields.network_attachment", err.Error())
	}
	if err := validateSharedCatalogItemLocalReferencePolicy(scope, "fields.network_attachment", state.hasLocked, state.hasDefault); err != nil {
		return err
	}
	resolveAttachment := func(attachment *privatev1.ClusterNetworkAttachment) error {
		if attachment == nil || attachment.GetSubnet() == nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'fields.network_attachment.subnet' is required")
		}
		subnetRef := attachment.GetSubnet()
		resolvedSubnet, resolveErr := resolveCatalogItemSubnet(ctx, subnetsDao, scope, subnetRef,
			" in fields.network_attachment.subnet", " in fields.network_attachment.subnet", " in fields.network_attachment")
		if resolveErr != nil {
			return resolveErr
		}
		attachment.SetSubnet(canonicalSubnetLocalReference(resolvedSubnet))
		virtualNetworkID := refKey(resolvedSubnet.GetSpec().GetVirtualNetwork())
		for i, securityGroupRef := range attachment.GetSecurityGroups() {
			if securityGroupRef == nil {
				return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'fields.network_attachment.security_groups[%d]' must not be null", i)
			}
			resolvedSecurityGroup, resolveErr := resolveCatalogItemSecurityGroup(ctx, securityGroupsDao, scope, securityGroupRef,
				fmt.Sprintf(" in fields.network_attachment.security_groups[%d]", i), " in fields.network_attachment", " in fields.network_attachment", virtualNetworkID)
			if resolveErr != nil {
				return resolveErr
			}
			attachment.GetSecurityGroups()[i] = canonicalSecurityGroupLocalReference(resolvedSecurityGroup)
		}
		return nil
	}
	if state.hasLocked {
		if err := resolveAttachment(state.lockedValue); err != nil {
			return err
		}
	}
	if state.hasDefault {
		if err := resolveAttachment(state.defaultValue); err != nil {
			return err
		}
	}
	return nil
}

// validateClusterCatalogItemNodeSetPolicy checks HostType references supplied by the Catalog Item
// or inherited from its Template. A supplied name is looked up from the Catalog Item's
// tenant/project or explicit shared scope. An omitted HostType inherits the corresponding
// Template node set's reference, and a concrete network policy requires each effective HostType
// to provide a fabric interface. Resolved references are stored in the Catalog Item, while the
// Template remains unchanged. Dependency locks are held until the request transaction finishes.
func validateClusterCatalogItemNodeSetPolicy(
	ctx context.Context,
	item *privatev1.ClusterCatalogItem,
	template *privatev1.ClusterTemplate,
	hostTypes *dao.GenericDAO[*privatev1.HostType],
) error {
	policy := item.GetFields().GetNodeSets()
	state, err := decodeClusterNodeSetMapPolicy(policy)
	if err != nil {
		return catalogItemPolicyError("fields.node_sets", err.Error())
	}
	networkState, err := decodeClusterNetworkAttachmentPolicy(item.GetFields().GetNetworkAttachment())
	if err != nil {
		return catalogItemPolicyError("fields.network_attachment", err.Error())
	}
	// Defer fabric validation when callers must supply the editable network attachment themselves.
	requiresFabricInterface := networkState.hasLocked || networkState.hasDefault
	var nodeMap *privatev1.ClusterNodeSetMap
	// Select the concrete policy branch; otherwise the Template's node sets remain effective.
	switch {
	case state.hasLocked:
		nodeMap = policy.GetLocked()
	case state.hasDefault:
		nodeMap = policy.GetEditable().GetDefaultValue()
	default:
		if requiresFabricInterface {
			// A concrete network policy also requires inherited Template HostTypes to expose fabric interfaces.
			for name, node := range template.GetNodeSets() {
				ref := cloneMessage(node.GetHostType())
				if ref == nil {
					return catalogItemPolicyError("fields.node_sets."+name, "host type is required")
				}
				hostType, err := resolveAndCanonicalizeLockedReference(ctx, hostTypes, template.GetMetadata(), ref, "host type", grpccodes.InvalidArgument)
				if err != nil {
					return err
				}
				if _, err := selectClusterFabricInterface(hostType); err != nil {
					return catalogItemPolicyError("fields.node_sets."+name, err.Error())
				}
			}
		}
		return nil
	}
	// Resolve explicit or inherited HostTypes and enforce compatibility with matching Template node sets.
	for name, node := range nodeMap.GetItems() {
		if node == nil {
			return catalogItemPolicyError("fields.node_sets", "node set is required")
		}
		ref := node.GetHostType()
		templateNode := template.GetNodeSets()[name]
		if ref == nil && templateNode != nil {
			ref = cloneMessage(templateNode.GetHostType())
			if ref != nil {
				inheritReferenceScope(ref, template.GetMetadata())
			}
		}
		if ref == nil {
			return catalogItemPolicyError("fields.node_sets."+name, "host type is required")
		}
		resolved, err := resolveAndCanonicalizeLockedReference(ctx, hostTypes, item.GetMetadata(), ref, "host type", grpccodes.InvalidArgument)
		if err != nil {
			return err
		}
		if templateNode != nil && templateNode.GetHostType() != nil {
			expected := cloneMessage(templateNode.GetHostType())
			templateHost, err := resolveAndCanonicalizeLockedReference(ctx, hostTypes, template.GetMetadata(), expected, "host type", grpccodes.InvalidArgument)
			if err != nil {
				return err
			}
			if templateHost.GetId() != resolved.GetId() {
				return catalogItemPolicyError("fields.node_sets."+name, "host type conflicts with the template")
			}
		}
		if requiresFabricInterface {
			if _, err := selectClusterFabricInterface(resolved); err != nil {
				return catalogItemPolicyError("fields.node_sets."+name, err.Error())
			}
		}
		node.SetHostType(ref)
	}
	if err := validateClusterNodeSetMap(convertTemplateNodeSets(nodeMap.GetItems())); err != nil {
		return catalogItemPolicyError("fields.node_sets", err.Error())
	}
	return nil
}

// decodeSecretReferencePolicy decodes the selected locked/default policy value without mutating the policy.
// An absent policy yields no governed value; malformed behavior returns an error.
// Returned message/list values may alias the policy and must be copied before resource assignment.
func decodeSecretReferencePolicy(policy *privatev1.SecretReferenceFieldPolicy) (policyState[*privatev1.SecretLocalReference], error) {
	if policy == nil {
		return policyState[*privatev1.SecretLocalReference]{}, nil
	}
	if policy.HasLocked() {
		locked := policy.GetLocked()
		if locked == nil {
			return policyState[*privatev1.SecretLocalReference]{}, fmt.Errorf("locked secret policy is empty")
		}
		return policyState[*privatev1.SecretLocalReference]{hasLocked: true, lockedValue: locked}, nil
	}
	if policy.HasEditable() {
		editable := policy.GetEditable()
		if editable == nil {
			return policyState[*privatev1.SecretLocalReference]{}, fmt.Errorf("editable secret policy is empty")
		}
		return policyState[*privatev1.SecretLocalReference]{
			hasDefault:   editable.GetDefaultValue() != nil,
			defaultValue: editable.GetDefaultValue(),
		}, nil
	}
	return policyState[*privatev1.SecretLocalReference]{}, fmt.Errorf("secret policy has no behavior")
}

// decodeClusterVersionReferencePolicy decodes the selected locked/default policy value without mutating the policy.
// An absent policy yields no governed value; malformed behavior returns an error.
// Returned message/list values may alias the policy and must be copied before resource assignment.
func decodeClusterVersionReferencePolicy(
	policy *privatev1.ClusterVersionReferenceFieldPolicy,
) (policyState[*privatev1.ClusterVersionReference], error) {
	if policy == nil {
		return policyState[*privatev1.ClusterVersionReference]{}, nil
	}
	if policy.HasLocked() {
		locked := policy.GetLocked()
		if locked == nil {
			return policyState[*privatev1.ClusterVersionReference]{}, fmt.Errorf("locked cluster version policy is empty")
		}
		return policyState[*privatev1.ClusterVersionReference]{hasLocked: true, lockedValue: locked}, nil
	}
	if policy.HasEditable() {
		editable := policy.GetEditable()
		if editable == nil {
			return policyState[*privatev1.ClusterVersionReference]{}, fmt.Errorf("editable cluster version policy is empty")
		}
		return policyState[*privatev1.ClusterVersionReference]{
			hasDefault:   editable.GetDefaultValue() != nil,
			defaultValue: editable.GetDefaultValue(),
		}, nil
	}
	return policyState[*privatev1.ClusterVersionReference]{}, fmt.Errorf("cluster version policy has no behavior")
}

// decodeClusterNetworkAttachmentPolicy decodes the selected locked/default policy value without mutating the policy.
// An absent policy yields no governed value; malformed behavior returns an error.
// Returned message/list values may alias the policy and must be copied before resource assignment.
func decodeClusterNetworkAttachmentPolicy(
	policy *privatev1.ClusterNetworkAttachmentFieldPolicy,
) (policyState[*privatev1.ClusterNetworkAttachment], error) {
	if policy == nil {
		return policyState[*privatev1.ClusterNetworkAttachment]{}, nil
	}
	if policy.HasLocked() {
		locked := policy.GetLocked()
		if locked == nil {
			return policyState[*privatev1.ClusterNetworkAttachment]{}, fmt.Errorf("locked cluster network attachment policy is empty")
		}
		return policyState[*privatev1.ClusterNetworkAttachment]{hasLocked: true, lockedValue: locked}, nil
	}
	if policy.HasEditable() {
		editable := policy.GetEditable()
		if editable == nil {
			return policyState[*privatev1.ClusterNetworkAttachment]{}, fmt.Errorf("editable cluster network attachment policy is empty")
		}
		return policyState[*privatev1.ClusterNetworkAttachment]{
			hasDefault:   editable.GetDefaultValue() != nil,
			defaultValue: editable.GetDefaultValue(),
		}, nil
	}
	return policyState[*privatev1.ClusterNetworkAttachment]{}, fmt.Errorf("cluster network attachment policy has no behavior")
}

// decodeClusterNodeSetMapPolicy decodes the selected locked/default policy value without mutating the policy.
// An absent policy yields no governed value; malformed behavior returns an error.
// Node sets are converted to resource values with copied HostType references and explicit sizes.
func decodeClusterNodeSetMapPolicy(policy *privatev1.ClusterNodeSetMapPolicy) (policyState[map[string]*privatev1.ClusterNodeSet], error) {
	if policy == nil {
		return policyState[map[string]*privatev1.ClusterNodeSet]{}, nil
	}
	if policy.HasLocked() {
		locked := policy.GetLocked()
		if locked == nil {
			return policyState[map[string]*privatev1.ClusterNodeSet]{}, fmt.Errorf("locked node sets policy is empty")
		}
		return policyState[map[string]*privatev1.ClusterNodeSet]{hasLocked: true, lockedValue: convertTemplateNodeSets(locked.GetItems())}, nil
	}
	if policy.HasEditable() {
		editable := policy.GetEditable()
		if editable == nil {
			return policyState[map[string]*privatev1.ClusterNodeSet]{}, fmt.Errorf("editable node sets policy is empty")
		}
		defaultValue := editable.GetDefaultValue()
		if defaultValue == nil {
			return policyState[map[string]*privatev1.ClusterNodeSet]{}, nil
		}
		return policyState[map[string]*privatev1.ClusterNodeSet]{hasDefault: true, defaultValue: convertTemplateNodeSets(defaultValue.GetItems())}, nil
	}
	return policyState[map[string]*privatev1.ClusterNodeSet]{}, fmt.Errorf("node sets policy has no behavior")
}

// cloneClusterNodeSets copies the collection and its protobuf values, retaining nil entries.
// The result can be modified without changing the source policy.
func cloneClusterNodeSets(value map[string]*privatev1.ClusterNodeSet) map[string]*privatev1.ClusterNodeSet {
	if value == nil {
		return nil
	}
	result := make(map[string]*privatev1.ClusterNodeSet, len(value))
	for name, nodeSet := range value {
		if nodeSet != nil {
			result[name] = cloneMessage(nodeSet)
		} else {
			result[name] = nil
		}
	}
	return result
}
