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

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type ambiguousDefaultResourceError struct {
	resource string
	count    int
}

func (e *ambiguousDefaultResourceError) Error() string {
	return fmt.Sprintf("multiple default %s resources found (count %d)", e.resource, e.count)
}

func defaultResourceLookupFailure(err error, resource string) error {
	var ambiguous *ambiguousDefaultResourceError
	if errors.As(err, &ambiguous) {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"tenant default networking is misconfigured: %s", ambiguous)
	}
	return grpcstatus.Errorf(grpccodes.Internal, "failed to look up default %s: %v", resource, err)
}

func findDefaultVirtualNetwork(
	ctx context.Context,
	logger *slog.Logger,
	virtualNetworksDao *dao.GenericDAO[*privatev1.VirtualNetwork],
	tenant, _ string,
) (*privatev1.VirtualNetwork, error) {
	return findUniqueTenantDefault(ctx, logger, virtualNetworksDao, tenant, "VirtualNetwork", func(*privatev1.VirtualNetwork) bool {
		return true
	})
}

func findUniqueTenantDefault[O lifecycleResource](
	ctx context.Context,
	logger *slog.Logger,
	objectDao *dao.GenericDAO[O],
	tenant, resource string,
	match func(O) bool,
) (O, error) {
	var zero O
	response, err := objectDao.List().SetFilter(fmt.Sprintf(
		`this.metadata.tenant == %q && this.metadata.labels[%q] == "true"`, tenant, defaultLabel,
	)).Do(ctx)
	if err != nil {
		return zero, grpcstatus.Error(grpccodes.Internal, "failed to find the tenant defaults")
	}
	var found O
	count := 0
	for _, item := range response.GetItems() {
		if !match(item) {
			continue
		}
		found = item
		count++
	}
	if count > 1 {
		logger.ErrorContext(ctx, "multiple default networking resources found",
			slog.String("resource", resource), slog.Int("count", count))
		return zero, &ambiguousDefaultResourceError{resource: resource, count: count}
	}
	if count == 0 {
		return zero, nil
	}
	return found, nil
}

// findDefaultSubnet returns the tenant's default Subnet with an IPv4 CIDR, or nil. Tenant defaults are
// tenant-wide (Default Networking design), so the project isn't used.
func findDefaultSubnet(
	ctx context.Context,
	logger *slog.Logger,
	subnetsDao *dao.GenericDAO[*privatev1.Subnet],
	tenant, _ string,
) (*privatev1.Subnet, error) {
	return findUniqueTenantDefault(ctx, logger, subnetsDao, tenant, "Subnet", func(subnet *privatev1.Subnet) bool {
		return subnet.GetSpec().GetIpv4Cidr() != ""
	})
}

// findDefaultSecurityGroup returns the tenant's default SecurityGroup of the given VirtualNetwork, or nil.
// Tenant defaults are tenant-wide (Default Networking design), so the project isn't used.
func findDefaultSecurityGroup(
	ctx context.Context,
	logger *slog.Logger,
	securityGroupsDao *dao.GenericDAO[*privatev1.SecurityGroup],
	virtualNetworkID string,
	tenant, _ string,
) (*privatev1.SecurityGroup, error) {
	return findUniqueTenantDefault(ctx, logger, securityGroupsDao, tenant, "SecurityGroup", func(group *privatev1.SecurityGroup) bool {
		return refKey(group.GetSpec().GetVirtualNetwork()) == virtualNetworkID
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
	found, findErr := findTenantDefaultInScope(ctx, resourceDao, owner.GetTenant(),
		reference.GetId(), reference.GetName())
	if findErr != nil {
		return object, findErr
	}
	if found.GetId() == "" {
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
