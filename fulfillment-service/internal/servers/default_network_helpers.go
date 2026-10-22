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

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

type ambiguousDefaultResourceError struct {
	resource string
	count    int
}

func (e *ambiguousDefaultResourceError) Error() string {
	return fmt.Sprintf("expected exactly one READY default %s, found %d", e.resource, e.count)
}

func defaultResourceLookupFailure(err error, resource string) error {
	var ambiguous *ambiguousDefaultResourceError
	if errors.As(err, &ambiguous) {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"tenant default networking is misconfigured: %s", ambiguous)
	}
	return grpcstatus.Errorf(grpccodes.Internal, "failed to look up default %s: %v", resource, err)
}

// findDefaultVirtualNetwork returns the READY virtual network labeled as a tenant
// default, scoped to the given tenant and project. Returns nil if none exists and
// an error if more than one matches.
func findDefaultVirtualNetwork(
	ctx context.Context,
	logger *slog.Logger,
	virtualNetworksDao *dao.GenericDAO[*privatev1.VirtualNetwork],
	tenant, project string,
) (*privatev1.VirtualNetwork, error) {
	filter := fmt.Sprintf(
		"this.metadata.labels[%q] == \"true\" && this.metadata.tenant == %q && this.metadata.project == %q",
		defaultLabel, tenant, project,
	)
	listResponse, err := virtualNetworksDao.List().
		SetFilter(filter).
		Do(ctx)
	if err != nil {
		return nil, err
	}
	var items []*privatev1.VirtualNetwork
	for _, virtualNetwork := range listResponse.GetItems() {
		if virtualNetwork.GetMetadata().HasDeletionTimestamp() {
			continue
		}
		if virtualNetwork.GetStatus().GetState() != privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY {
			continue
		}
		items = append(items, virtualNetwork)
	}
	if len(items) == 0 {
		return nil, nil
	}
	if len(items) > 1 {
		logger.ErrorContext(ctx, "multiple default VirtualNetworks found",
			slog.Int("count", len(items)),
		)
		return nil, &ambiguousDefaultResourceError{resource: "VirtualNetwork", count: len(items)}
	}
	return items[0], nil
}

// findDefaultSubnet returns the READY subnet labeled as a tenant default with an
// IPv4 CIDR, scoped to the given tenant and project. Returns nil if none exists and
// an error if more than one matches.
func findDefaultSubnet(
	ctx context.Context,
	logger *slog.Logger,
	subnetsDao *dao.GenericDAO[*privatev1.Subnet],
	tenant, project string,
) (*privatev1.Subnet, error) {
	filter := fmt.Sprintf(
		"this.metadata.labels[\"%s\"] == \"true\" && has(this.spec.ipv4_cidr) && this.metadata.tenant == \"%s\"",
		defaultLabel, tenant,
	)
	filter += fmt.Sprintf(" && this.metadata.project == %q", project)
	listResponse, err := subnetsDao.List().
		SetFilter(filter).
		Do(ctx)
	if err != nil {
		return nil, err
	}
	var items []*privatev1.Subnet
	for _, subnet := range listResponse.GetItems() {
		if subnet.GetMetadata().HasDeletionTimestamp() {
			continue
		}
		if subnet.GetStatus().GetState() != privatev1.SubnetState_SUBNET_STATE_READY {
			continue
		}
		items = append(items, subnet)
	}
	if len(items) == 0 {
		return nil, nil
	}
	if len(items) > 1 {
		logger.ErrorContext(ctx, "multiple default Subnets found",
			slog.Int("count", len(items)),
		)
		return nil, &ambiguousDefaultResourceError{resource: "Subnet", count: len(items)}
	}
	return items[0], nil
}

// findDefaultSecurityGroup returns the READY security group labeled as a tenant
// default that belongs to the given virtual network, scoped to the given tenant
// and project. Returns nil if none exists and an error if more than one matches.
func findDefaultSecurityGroup(
	ctx context.Context,
	logger *slog.Logger,
	securityGroupsDao *dao.GenericDAO[*privatev1.SecurityGroup],
	virtualNetworkID string,
	tenant, project string,
) (*privatev1.SecurityGroup, error) {
	filter := fmt.Sprintf(
		"this.metadata.labels[\"%s\"] == \"true\" && this.metadata.tenant == \"%s\"",
		defaultLabel, tenant,
	)
	filter += fmt.Sprintf(" && this.metadata.project == %q", project)
	listResponse, err := securityGroupsDao.List().
		SetFilter(filter).
		Do(ctx)
	if err != nil {
		return nil, err
	}
	var items []*privatev1.SecurityGroup
	for _, sg := range listResponse.GetItems() {
		if sg.GetMetadata().HasDeletionTimestamp() {
			continue
		}
		if sg.GetStatus().GetState() != privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY {
			continue
		}
		if refKey(sg.GetSpec().GetVirtualNetwork()) != virtualNetworkID {
			continue
		}
		items = append(items, sg)
	}
	if len(items) == 0 {
		return nil, nil
	}
	if len(items) > 1 {
		logger.ErrorContext(ctx, "multiple default SecurityGroups found",
			slog.Int("count", len(items)),
		)
		return nil, &ambiguousDefaultResourceError{resource: "SecurityGroup", count: len(items)}
	}
	return items[0], nil
}
