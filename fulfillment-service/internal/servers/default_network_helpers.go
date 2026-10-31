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
	"log/slog"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// findDefaultSubnet returns the tenant's READY default Subnet with an IPv4 CIDR, or nil. Tenant defaults are
// tenant-wide (Default Networking design), so the project isn't used.
func findDefaultSubnet(
	ctx context.Context,
	_ *slog.Logger,
	subnetsDao *dao.GenericDAO[*privatev1.Subnet],
	tenant, _ string,
) (*privatev1.Subnet, error) {
	return findTenantDefault(ctx, subnetsDao, tenant, func(subnet *privatev1.Subnet) bool {
		return subnet.GetSpec().GetIpv4Cidr() != "" &&
			subnet.GetStatus().GetState() == privatev1.SubnetState_SUBNET_STATE_READY
	})
}

// findDefaultSecurityGroup returns the tenant's READY default SecurityGroup of the given VirtualNetwork, or nil.
// Tenant defaults are tenant-wide (Default Networking design), so the project isn't used.
func findDefaultSecurityGroup(
	ctx context.Context,
	_ *slog.Logger,
	securityGroupsDao *dao.GenericDAO[*privatev1.SecurityGroup],
	virtualNetworkID string,
	tenant, _ string,
) (*privatev1.SecurityGroup, error) {
	return findTenantDefault(ctx, securityGroupsDao, tenant, func(group *privatev1.SecurityGroup) bool {
		return refKey(group.GetSpec().GetVirtualNetwork()) == virtualNetworkID &&
			group.GetStatus().GetState() == privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY
	})
}

// resolveNetworkingReference resolves a Subnet or SecurityGroup reference of a BareMetalInstance or Cluster in the
// owner's tenant and project. The tenant defaults are tenant-wide (Default Networking design), so a reference to a
// tenant default of the owner's tenant resolves from any project. The target is locked when locked is true.
func resolveNetworkingReference[O referenceResource](
	ctx context.Context,
	resourceDao *dao.GenericDAO[O],
	owner *privatev1.Metadata,
	reference resourceReference,
	kind string,
	notFoundCode grpccodes.Code,
	locked bool,
) (O, error) {
	resolve := resolveAndCanonicalizeReference[O]
	if locked {
		resolve = resolveAndCanonicalizeLockedReference[O]
	}
	object, err := resolve(ctx, resourceDao, owner, reference, kind, notFoundCode)
	if err == nil || grpcstatus.Code(err) != notFoundCode {
		return object, err
	}
	key := reference.GetId()
	if key == "" {
		key = reference.GetName()
	}
	found, findErr := findInScope(ctx, resourceDao, owner.GetTenant(), owner.GetProject(), key)
	if findErr != nil {
		return object, findErr
	}
	if found.GetId() == "" || found.GetMetadata().GetLabels()[defaultLabel] != "true" {
		return object, err
	}
	if locked {
		if found, err = lockRecord(ctx, resourceDao, found.GetId()); err != nil {
			return found, err
		}
	}
	if err := validateResourceNotDeleted(kind, found.GetId(), "", found.GetMetadata()); err != nil {
		return found, err
	}
	canonicalizeResourceReference(reference, found)
	return found, nil
}
