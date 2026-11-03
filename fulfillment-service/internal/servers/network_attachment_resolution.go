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
	"math"
	"sort"
	"strings"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// networkAttachmentRequest is the network attachment field of a workload create, before defaulting.
type networkAttachmentRequest struct {
	// tenant and project are the scope of the Subnets and SecurityGroups the field references: the tenant and project
	// of the workload. For a CaaS worker BareMetalInstance (tenant system) they are the tenant and project of the
	// Cluster.
	tenant  string
	project string

	// subnetsDao and securityGroupsDao resolve the explicit references. A workload server passes its caller-scoped
	// DAOs, so a reference resolves only to a record the caller sees in the workload's tenant and project. The CaaS
	// worker path passes the guard DAOs. Tenant defaults are always found with the guard DAOs.
	subnetsDao        *dao.GenericDAO[*privatev1.Subnet]
	securityGroupsDao *dao.GenericDAO[*privatev1.SecurityGroup]

	// subnet is the requested Subnet; nil or empty selects the tenant's default Subnet.
	subnet *privatev1.SubnetLocalReference

	// securityGroups are the requested SecurityGroups; empty selects the tenant's default SecurityGroup when the
	// Subnet is in the tenant's default VirtualNetwork.
	securityGroups []*privatev1.SecurityGroupLocalReference

	// automaticGroupOwner is the ID of the Cluster whose automatic SecurityGroup the field may select (CaaS workers).
	// Empty means that no automatic SecurityGroup may be selected.
	automaticGroupOwner string

	// source names the field in error messages, for example "spec.network_attachments[0]".
	source string
}

// networkAttachmentResolution is the stored network attachment field: one Subnet and at least one SecurityGroup, all
// READY, locked, and in one VirtualNetwork.
type networkAttachmentResolution struct {
	subnet         *privatev1.Subnet
	virtualNetwork *privatev1.VirtualNetwork
	securityGroups []*privatev1.SecurityGroup
	usedDefaults   bool
}

func (r *networkAttachmentResolution) subnetReference() *privatev1.SubnetLocalReference {
	return privatev1.SubnetLocalReference_builder{
		Id:   r.subnet.GetId(),
		Name: r.subnet.GetMetadata().GetName(),
	}.Build()
}

func (r *networkAttachmentResolution) securityGroupReferences() []*privatev1.SecurityGroupLocalReference {
	result := make([]*privatev1.SecurityGroupLocalReference, len(r.securityGroups))
	for i, group := range r.securityGroups {
		result[i] = privatev1.SecurityGroupLocalReference_builder{
			Id:   group.GetId(),
			Name: group.GetMetadata().GetName(),
		}.Build()
	}
	return result
}

func (r *networkAttachmentResolution) securityGroupIDs() []string {
	result := make([]string, len(r.securityGroups))
	for i, group := range r.securityGroups {
		result[i] = group.GetId()
	}
	return result
}

func (r *networkAttachmentResolution) hubReferences() []networkingHubReference {
	result := []networkingHubReference{{
		resourceType: "Subnet",
		id:           r.subnet.GetId(),
		hubID:        r.subnet.GetStatus().GetHub(),
	}}
	for _, group := range r.securityGroups {
		result = append(result, networkingHubReference{
			resourceType: "SecurityGroup",
			id:           group.GetId(),
			hubID:        group.GetStatus().GetHub(),
		})
	}
	return result
}

// resolveNetworkAttachment resolves the network attachment field of a workload create with the defaulting rules of
// the unified networking design, and checks the workload gates. It locks the Subnet, then the SecurityGroups by ID.
// The caller has locked the Tenant.
func resolveNetworkAttachment(ctx context.Context, logger *slog.Logger, guards *networkingGuards,
	settings NetworkingSettings, request networkAttachmentRequest) (*networkAttachmentResolution, error) {
	result := &networkAttachmentResolution{}
	if err := resolveAttachmentSubnet(ctx, guards, request, result); err != nil {
		return nil, err
	}
	if err := resolveAttachmentSecurityGroups(ctx, guards, request, result); err != nil {
		return nil, err
	}
	if err := lockAttachmentSecurityGroups(ctx, guards, result); err != nil {
		return nil, err
	}
	if err := checkAttachmentResolution(request, result); err != nil {
		return nil, err
	}
	if result.usedDefaults {
		if settings.ProvisioningEnabled {
			if err := requireDefaultNetworkingReady(ctx, guards.tenants, request.tenant); err != nil {
				return nil, err
			}
		}
		logger.InfoContext(ctx, "PopulatedNetworkAttachmentsDefaults",
			slog.String("tenant", request.tenant),
			slog.String("subnet_id", result.subnet.GetId()),
			slog.Any("security_group_ids", result.securityGroupIDs()))
	}
	return result, nil
}

// resolveAttachmentSubnet finds the requested Subnet, or the tenant's default, locks it and reads its VirtualNetwork.
func resolveAttachmentSubnet(ctx context.Context, guards *networkingGuards, request networkAttachmentRequest,
	result *networkAttachmentResolution) error {
	var err error
	if request.subnet == nil || refKey(request.subnet) == "" {
		result.subnet, err = findTenantDefault(ctx, guards.subnets, request.tenant,
			func(subnet *privatev1.Subnet) bool { return subnet.GetMetadata().GetName() == defaultSubnetName })
		if err != nil {
			return err
		}
		if result.subnet == nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"%s.subnet: tenant '%s' has no default Subnet", request.source, request.tenant)
		}
		result.usedDefaults = true
	} else {
		result.subnet, err = resolveResourceInScope(ctx, request.subnetsDao,
			referenceScope{tenant: request.tenant, project: request.project},
			request.subnet.GetId(), request.subnet.GetName(), "Subnet", " in "+request.source+".subnet", grpccodes.NotFound)
		if err != nil {
			if grpcstatus.Code(err) == grpccodes.NotFound {
				return grpcstatus.Errorf(grpccodes.InvalidArgument,
					"%s.subnet: Subnet '%s' does not exist", request.source, refKey(request.subnet))
			}
			return err
		}
	}
	result.subnet, err = lockRecord(ctx, guards.subnets, result.subnet.GetId())
	if err != nil {
		return err
	}
	virtualNetworkID := refKey(result.subnet.GetSpec().GetVirtualNetwork())
	virtualNetworks, err := guards.virtualNetworks.List().
		SetFilter(fmt.Sprintf("this.id == %q", virtualNetworkID)).
		SetLimit(1).
		Do(ctx)
	if err != nil {
		return grpcstatus.Error(grpccodes.Internal, "failed to find the VirtualNetwork of the Subnet")
	}
	if len(virtualNetworks.GetItems()) == 0 {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"%s.subnet: the VirtualNetwork of Subnet '%s' does not exist", request.source, result.subnet.GetId())
	}
	result.virtualNetwork = virtualNetworks.GetItems()[0]
	return nil
}

// resolveAttachmentSecurityGroups finds the requested SecurityGroups, or the tenant's default SecurityGroup when the
// Subnet is in the tenant's default VirtualNetwork.
func resolveAttachmentSecurityGroups(ctx context.Context, guards *networkingGuards, request networkAttachmentRequest,
	result *networkAttachmentResolution) error {
	virtualNetworkID := result.virtualNetwork.GetId()
	if len(request.securityGroups) == 0 {
		if result.virtualNetwork.GetMetadata().GetLabels()[defaultLabel] != "true" {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"%s.security_groups: at least one SecurityGroup is required for a Subnet outside the tenant's "+
					"default VirtualNetwork", request.source)
		}
		group, err := findTenantDefault(ctx, guards.securityGroups, request.tenant,
			func(group *privatev1.SecurityGroup) bool {
				return refKey(group.GetSpec().GetVirtualNetwork()) == virtualNetworkID
			})
		if err != nil {
			return err
		}
		if group == nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"%s.security_groups: tenant '%s' has no default SecurityGroup", request.source, request.tenant)
		}
		result.securityGroups = []*privatev1.SecurityGroup{group}
		result.usedDefaults = true
		return nil
	}
	groups, err := resolveRequestedSecurityGroupsWithLookup(ctx, request, virtualNetworkID, result.subnet.GetId(),
		func(ctx context.Context, tenant string, ids, names []string) ([]*privatev1.SecurityGroup, error) {
			quote := func(values []string) string {
				quoted := make([]string, len(values))
				for i, value := range values {
					quoted[i] = fmt.Sprintf("%q", value)
				}
				return "[" + strings.Join(quoted, ", ") + "]"
			}
			// The DAO clamps this to its configured maximum (normally 1000).
			// Never resolve references from a truncated candidate set.
			response, err := request.securityGroupsDao.List().
				SetFilter(fmt.Sprintf("this.metadata.tenant == %q && this.metadata.project == %q && "+
					"(this.id in %s || this.metadata.name in %s)", tenant, request.project, quote(ids), quote(names))).
				SetLimit(math.MaxInt32).
				Do(ctx)
			if err != nil {
				return nil, err
			}
			if int(response.GetTotal()) > len(response.GetItems()) {
				return nil, errSecurityGroupLookupTruncated
			}
			return response.GetItems(), nil
		})
	if err != nil {
		return err
	}
	result.securityGroups = groups
	return nil
}

// securityGroupBatchLookup finds only the requested IDs and names in the tenant. The production callback also
// preserves the request's project and caller-scoped DAO visibility.
type securityGroupBatchLookup func(context.Context, string, []string, []string) ([]*privatev1.SecurityGroup, error)

var errSecurityGroupLookupTruncated = errors.New("matching SecurityGroups exceed the DAO query limit")

// resolveRequestedSecurityGroupsWithLookup resolves explicit references with one lookup, in request order. Defaults
// remain in resolveAttachmentSecurityGroups, and row locking follows complete reference validation.
func resolveRequestedSecurityGroupsWithLookup(ctx context.Context, request networkAttachmentRequest,
	virtualNetworkID, subnetID string, lookup securityGroupBatchLookup) ([]*privatev1.SecurityGroup, error) {
	keys := make([]string, 0, len(request.securityGroups))
	seenKeys := map[string]bool{}
	for _, ref := range request.securityGroups {
		key := refKey(ref)
		if !seenKeys[key] {
			keys = append(keys, key)
			seenKeys[key] = true
		}
	}
	// Every local reference retains its existing ID-first, then name fallback.
	rows, err := lookup(ctx, request.tenant, keys, keys)
	if err != nil {
		if errors.Is(err, errSecurityGroupLookupTruncated) {
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to resolve the network references: %s", err)
		}
		return nil, grpcstatus.Error(grpccodes.Internal, "failed to resolve the network references")
	}
	byID := make(map[string]*privatev1.SecurityGroup, len(rows))
	byName := make(map[string][]*privatev1.SecurityGroup)
	for _, group := range rows {
		byID[group.GetId()] = group
		name := group.GetMetadata().GetName()
		byName[name] = append(byName[name], group)
	}
	groups := make([]*privatev1.SecurityGroup, 0, len(request.securityGroups))
	seen := map[string]bool{}
	for i, ref := range request.securityGroups {
		key := refKey(ref)
		group := byID[key]
		if group == nil {
			matches := byName[key]
			if len(matches) > 1 {
				return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
					"name '%s' matches %d records; use the ID", key, len(matches))
			}
			if len(matches) == 1 {
				group = matches[0]
			}
		}
		if group == nil {
			return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
				"%s.security_groups[%d]: SecurityGroup '%s' does not exist", request.source, i, key)
		}
		if seen[group.GetId()] {
			return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
				"%s.security_groups[%d]: SecurityGroup '%s' is listed twice", request.source, i, group.GetId())
		}
		seen[group.GetId()] = true
		if refKey(group.GetSpec().GetVirtualNetwork()) != virtualNetworkID {
			return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
				"%s.security_groups[%d]: SecurityGroup '%s' belongs to another VirtualNetwork than Subnet '%s'",
				request.source, i, group.GetId(), subnetID)
		}
		groups = append(groups, group)
	}
	return groups, nil
}

// lockAttachmentSecurityGroups locks the resolved SecurityGroups by ID and replaces them with the locked rows.
func lockAttachmentSecurityGroups(ctx context.Context, guards *networkingGuards,
	result *networkAttachmentResolution) error {
	ids := result.securityGroupIDs()
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	response, err := guards.securityGroups.Lock().AddIds(ids...).SetReturnObjects(true).Do(ctx)
	if err != nil {
		return translateLifecycleError(err)
	}
	locked := make(map[string]*privatev1.SecurityGroup, len(ids))
	for _, group := range response.GetObjects() {
		locked[group.GetId()] = group
	}
	if len(locked) != len(ids) {
		return grpcstatus.Error(grpccodes.Internal, "failed to load locked SecurityGroups")
	}
	for i, group := range result.securityGroups {
		result.securityGroups[i] = locked[group.GetId()]
	}
	return nil
}

// checkAttachmentResolution checks the locked Subnet and SecurityGroups: READY, not being deleted, in the Subnet's
// VirtualNetwork, and an automatic SecurityGroup only for the workers of its Cluster.
func checkAttachmentResolution(request networkAttachmentRequest, result *networkAttachmentResolution) error {
	if err := validateResolvedSubnetReady(result.subnet, result.subnet.GetId(), " in "+request.source); err != nil {
		return err
	}
	for _, group := range result.securityGroups {
		if err := validateResolvedSecurityGroup(group, group.GetId(), " in "+request.source,
			result.virtualNetwork.GetId()); err != nil {
			return err
		}
		if isAutomatic(group.GetMetadata()) &&
			(request.automaticGroupOwner == "" ||
				group.GetMetadata().GetLabels()[autoCreatedForLabel] != request.automaticGroupOwner) {
			return grpcstatus.Errorf(grpccodes.FailedPrecondition,
				"%s.security_groups: SecurityGroup '%s' is automatic; only the workers of its Cluster use it",
				request.source, group.GetId())
		}
	}
	return nil
}

// requireDefaultNetworkingReady checks that the tenant's DefaultNetworkingReady condition is True.
func requireDefaultNetworkingReady(ctx context.Context, tenants *dao.GenericDAO[*privatev1.Tenant], tenant string) error {
	response, err := tenants.List().SetFilter(fmt.Sprintf("this.id == %q", tenant)).SetLimit(1).Do(ctx)
	if err != nil {
		return grpcstatus.Error(grpccodes.Internal, "failed to check the tenant default networking")
	}
	for _, item := range response.GetItems() {
		for _, condition := range item.GetStatus().GetConditions() {
			if condition.GetType() == privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY &&
				condition.GetStatus() == privatev1.ConditionStatus_CONDITION_STATUS_TRUE {
				return nil
			}
		}
	}
	return grpcstatus.Error(grpccodes.FailedPrecondition, "tenant default networking is not ready")
}

// findTenantDefault returns the tenant's one default-labeled record of a kind that the match function accepts, or nil.
// Records being deleted aren't defaults.
func findTenantDefault[O lifecycleResource](ctx context.Context, objectDao *dao.GenericDAO[O], tenant string,
	match func(O) bool) (O, error) {
	var zero O
	response, err := objectDao.List().
		SetFilter(fmt.Sprintf(`this.metadata.tenant == %q && this.metadata.labels["%s"] == "true"`,
			tenant, defaultLabel)).
		Do(ctx)
	if err != nil {
		return zero, grpcstatus.Error(grpccodes.Internal, "failed to find the tenant defaults")
	}
	for _, item := range response.GetItems() {
		if !item.GetMetadata().HasDeletionTimestamp() && match(item) {
			return item, nil
		}
	}
	return zero, nil
}

// findInScope finds a record of the tenant and project, or a tenant default of any project, by ID, or by name when no
// record has that ID. It returns nil when there is none, also when the record belongs to another tenant or project or
// the DAO doesn't let the caller see it.
func findInScope[O referenceResource](ctx context.Context, objectDao *dao.GenericDAO[O], tenant, project,
	key string) (O, error) {
	var zero O
	scope := referenceScope{tenant: tenant, project: project}
	object, err := resolveResourceInScope(ctx, objectDao, scope, key, "", "VirtualNetwork", "", grpccodes.NotFound)
	if err == nil || grpcstatus.Code(err) != grpccodes.NotFound {
		return object, err
	}
	object, err = resolveResourceInScope(ctx, objectDao, scope, "", key, "VirtualNetwork", "", grpccodes.NotFound)
	if grpcstatus.Code(err) != grpccodes.NotFound {
		return object, err
	}
	// The tenant defaults are tenant-wide, as when the field is omitted: a reference to one resolves in any project.
	object, err = findTenantDefault(ctx, objectDao, tenant, func(item O) bool {
		return item.GetId() == key || item.GetMetadata().GetName() == key
	})
	if err != nil || object.GetId() == "" {
		return zero, err
	}
	return object, nil
}

// lockRecord locks a record found by a guard DAO.
func lockRecord[O lifecycleResource](ctx context.Context, objectDao *dao.GenericDAO[O], id string) (O, error) {
	var zero O
	response, err := objectDao.Get().SetId(id).SetLock(true).Do(ctx)
	if err != nil {
		return zero, translateLifecycleError(err)
	}
	return response.GetObject(), nil
}
