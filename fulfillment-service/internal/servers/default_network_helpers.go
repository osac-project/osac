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
	"sort"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// findDefaultSubnet returns the newest READY subnet labeled as a tenant default
// with an IPv4 CIDR, scoped to the given tenant. Returns nil if none found.
func findDefaultSubnet(
	ctx context.Context,
	logger *slog.Logger,
	subnetsDao *dao.GenericDAO[*privatev1.Subnet],
	tenant string,
) (*privatev1.Subnet, error) {
	filter := fmt.Sprintf(
		"this.metadata.labels[\"%s\"] == \"true\" && has(this.spec.ipv4_cidr) && this.metadata.tenant == \"%s\"",
		defaultLabel, tenant,
	)
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
	sort.Slice(items, func(i, j int) bool {
		ti := items[i].GetMetadata().GetCreationTimestamp().AsTime()
		tj := items[j].GetMetadata().GetCreationTimestamp().AsTime()
		return ti.After(tj)
	})
	if len(items) > 1 {
		logger.WarnContext(ctx, "multiple default Subnets found, using newest",
			slog.Int("count", len(items)),
		)
	}
	return items[0], nil
}

// findDefaultSecurityGroup returns the newest READY security group labeled as a
// tenant default that belongs to the given virtual network, scoped to the given
// tenant. Returns nil if none found.
func findDefaultSecurityGroup(
	ctx context.Context,
	logger *slog.Logger,
	securityGroupsDao *dao.GenericDAO[*privatev1.SecurityGroup],
	virtualNetworkID string,
	tenant string,
) (*privatev1.SecurityGroup, error) {
	filter := fmt.Sprintf(
		"this.metadata.labels[\"%s\"] == \"true\" && this.metadata.tenant == \"%s\"",
		defaultLabel, tenant,
	)
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
	sort.Slice(items, func(i, j int) bool {
		ti := items[i].GetMetadata().GetCreationTimestamp().AsTime()
		tj := items[j].GetMetadata().GetCreationTimestamp().AsTime()
		return ti.After(tj)
	})
	if len(items) > 1 {
		logger.WarnContext(ctx, "multiple default SecurityGroups found, using newest",
			slog.Int("count", len(items)),
		)
	}
	return items[0], nil
}
