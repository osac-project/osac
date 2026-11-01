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
	"log/slog"
	"math"
	"net/netip"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// networkingGuards holds DAOs with total visibility for the gates and guards of the networking and workload servers.
// They see every tenant and project, including the system tenant, and records being deleted.
type networkingGuards struct {
	logger                *slog.Logger
	networkClasses        *dao.GenericDAO[*privatev1.NetworkClass]
	virtualNetworks       *dao.GenericDAO[*privatev1.VirtualNetwork]
	subnets               *dao.GenericDAO[*privatev1.Subnet]
	securityGroups        *dao.GenericDAO[*privatev1.SecurityGroup]
	natGateways           *dao.GenericDAO[*privatev1.NATGateway]
	fabricDomains         *dao.GenericDAO[*privatev1.FabricDomain]
	externalIPPools       *dao.GenericDAO[*privatev1.ExternalIPPool]
	externalIPs           *dao.GenericDAO[*privatev1.ExternalIP]
	externalIPAttachments *dao.GenericDAO[*privatev1.ExternalIPAttachment]
	computeInstances      *dao.GenericDAO[*privatev1.ComputeInstance]
	bareMetalInstances    *dao.GenericDAO[*privatev1.BareMetalInstance]
	clusters              *dao.GenericDAO[*privatev1.Cluster]
	tenants               *dao.GenericDAO[*privatev1.Tenant]
}

func newNetworkingGuards(logger *slog.Logger) (result *networkingGuards, err error) {
	result = &networkingGuards{logger: logger}
	if result.networkClasses, err = newGuardDAO[*privatev1.NetworkClass](logger); err != nil {
		return
	}
	if result.virtualNetworks, err = newGuardDAO[*privatev1.VirtualNetwork](logger); err != nil {
		return
	}
	if result.subnets, err = newGuardDAO[*privatev1.Subnet](logger); err != nil {
		return
	}
	if result.securityGroups, err = newGuardDAO[*privatev1.SecurityGroup](logger); err != nil {
		return
	}
	if result.natGateways, err = newGuardDAO[*privatev1.NATGateway](logger); err != nil {
		return
	}
	if result.fabricDomains, err = newGuardDAO[*privatev1.FabricDomain](logger); err != nil {
		return
	}
	if result.externalIPPools, err = newGuardDAO[*privatev1.ExternalIPPool](logger); err != nil {
		return
	}
	if result.externalIPs, err = newGuardDAO[*privatev1.ExternalIP](logger); err != nil {
		return
	}
	if result.externalIPAttachments, err = newGuardDAO[*privatev1.ExternalIPAttachment](logger); err != nil {
		return
	}
	if result.computeInstances, err = newGuardDAO[*privatev1.ComputeInstance](logger); err != nil {
		return
	}
	if result.bareMetalInstances, err = newGuardDAO[*privatev1.BareMetalInstance](logger); err != nil {
		return
	}
	if result.clusters, err = newGuardDAO[*privatev1.Cluster](logger); err != nil {
		return
	}
	if result.tenants, err = newGuardDAO[*privatev1.Tenant](logger); err != nil {
		return
	}
	return
}

// firstPoolOverlap returns the ID of the first ExternalIPPool whose CIDR overlaps the prefix. Pool CIDRs are private,
// so errors name only the pool ID.
func (g *networkingGuards) firstPoolOverlap(ctx context.Context, prefix netip.Prefix,
	exceptID string) (string, error) {
	found := ""
	err := forEachRecord(ctx, g.externalIPPools, func(pool *privatev1.ExternalIPPool) {
		if found != "" || pool.GetId() == exceptID {
			return
		}
		for _, cidr := range pool.GetSpec().GetCidrs() {
			poolPrefix, err := netip.ParsePrefix(cidr)
			if err == nil && poolPrefix.Overlaps(prefix) {
				found = pool.GetId()
				return
			}
		}
	})
	if err != nil {
		return "", grpcstatus.Error(grpccodes.Internal, "failed to check ExternalIPPool ranges")
	}
	return found, nil
}

// rejectDeleteIfReferenced rejects a delete while a dependent of the given kind references the parent. It uses the
// guard DAO for that kind, so dependents in every tenant and project count.
func rejectGuardedDelete[O lifecycleResource](ctx context.Context, g *networkingGuards,
	dependentDao *dao.GenericDAO[O], dependentKind, parentKind string, parent lifecycleResource,
	references func(O) bool) error {
	return rejectDeleteIfReferenced(ctx, g.logger, dependentDao, dependentKind, parentKind, parent.GetId(),
		parent.GetMetadata(), references)
}

// rejectDeleteIfWorkloadsReference rejects a delete while any workload (ComputeInstance, BareMetalInstance or Cluster)
// whose stored network attachment field matches exists. For a CaaS worker BareMetalInstance (tenant system) the
// message names the owning Cluster, never the worker.
func (g *networkingGuards) rejectDeleteIfWorkloadsReference(ctx context.Context, parentKind string,
	parent lifecycleResource, matchSubnet func(*privatev1.SubnetLocalReference) bool,
	matchGroups func([]*privatev1.SecurityGroupLocalReference) bool) error {
	count := 0
	first := ""
	note := func(id string) {
		count++
		if first == "" {
			first = id
		}
	}
	err := forEachRecord(ctx, g.computeInstances, func(instance *privatev1.ComputeInstance) {
		for _, attachment := range instance.GetSpec().GetNetworkAttachments() {
			if matchSubnet(attachment.GetSubnet()) || matchGroups(attachment.GetSecurityGroups()) {
				note("ComputeInstance '" + instance.GetId() + "'")
				return
			}
		}
	})
	if err == nil {
		err = forEachRecord(ctx, g.bareMetalInstances, func(instance *privatev1.BareMetalInstance) {
			for _, attachment := range instance.GetSpec().GetNetworkAttachments() {
				if matchSubnet(attachment.GetSubnet()) || matchGroups(attachment.GetSecurityGroups()) {
					if clusterID := instance.GetStatus().GetCluster().GetId(); clusterID != "" &&
						instance.GetMetadata().GetTenant() == auth.SystemTenant {
						note("Cluster '" + clusterID + "'")
					} else {
						note("BareMetalInstance '" + instance.GetId() + "'")
					}
					return
				}
			}
		})
	}
	if err == nil {
		err = forEachRecord(ctx, g.clusters, func(cluster *privatev1.Cluster) {
			attachment := cluster.GetSpec().GetNetworkAttachment()
			if attachment == nil {
				return
			}
			if matchSubnet(attachment.GetSubnet()) || matchGroups(attachment.GetSecurityGroups()) {
				note("Cluster '" + cluster.GetId() + "'")
			}
		})
	}
	if err != nil {
		g.logger.ErrorContext(ctx, "Failed to check workload references", slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to check %s references", parentKind)
	}
	if count == 0 {
		return nil
	}
	return grpcstatus.Errorf(grpccodes.FailedPrecondition,
		"cannot delete %s '%s': %d workload(s) still reference it (including %s)",
		parentKind, parent.GetId(), count, first)
}

// forEachRecord calls the function for every record of the DAO, page by page.
func forEachRecord[O dao.Object](ctx context.Context, objectDao *dao.GenericDAO[O], fn func(O)) error {
	return forEachMatchingRecord(ctx, objectDao, "", fn)
}

// forEachRecordPageSize is the page size of forEachMatchingRecord. It is a variable so tests can make pages small.
var forEachRecordPageSize int32 = 1000

// forEachMatchingRecord calls the function for every record of the DAO that matches the filter (all records when the
// filter is empty), page by page, so no record is missed because of the DAO's default list limit.
func forEachMatchingRecord[O dao.Object](ctx context.Context, objectDao *dao.GenericDAO[O], filter string,
	fn func(O)) error {
	return forEachDAOPage(ctx, forEachRecordPageSize, func(ctx context.Context, offset, pageSize int32) ([]O, error) {
		request := objectDao.List().SetLimit(pageSize).SetOffset(offset)
		if filter != "" {
			request.SetFilter(filter)
		}
		page, err := request.Do(ctx)
		if err != nil {
			return nil, err
		}
		return page.GetItems(), nil
	}, fn)
}

// ErrDAOOffsetOverflow indicates that another full page would exceed the DAO's int32 offset range.
var ErrDAOOffsetOverflow = errors.New("DAO pagination offset overflow")

func nextDAOOffset(offset, pageSize int32) (int32, error) {
	if offset > math.MaxInt32-pageSize {
		return 0, ErrDAOOffsetOverflow
	}
	return offset + pageSize, nil
}

// forEachDAOPage visits records until a short page, preserving list errors and checking offset overflow before
// advancing. Callers supply the DAO filter, a positive page size, and their own error translation.
func forEachDAOPage[O any](ctx context.Context, pageSize int32,
	list func(context.Context, int32, int32) ([]O, error), fn func(O)) error {
	for offset := int32(0); ; {
		items, err := list(ctx, offset, pageSize)
		if err != nil {
			return err
		}
		for _, item := range items {
			fn(item)
		}
		if len(items) < int(pageSize) {
			return nil
		}
		offset, err = nextDAOOffset(offset, pageSize)
		if err != nil {
			return err
		}
	}
}

// Default Cluster pod and service networks, used when a Cluster doesn't set them.
const (
	defaultClusterPodCIDR     = "10.128.0.0/14"
	defaultClusterServiceCIDR = "172.30.0.0/16"
)

// clusterCIDRs returns the pod and service CIDRs of a Cluster, with the defaults for the ones it doesn't set.
func clusterCIDRs(cluster *privatev1.Cluster) []string {
	network := cluster.GetSpec().GetNetwork()
	pod := network.GetPodCidr()
	if pod == "" {
		pod = defaultClusterPodCIDR
	}
	service := network.GetServiceCidr()
	if service == "" {
		service = defaultClusterServiceCIDR
	}
	return []string{pod, service}
}

// firstOverlap returns a description of the first record whose range overlaps the prefix, among VirtualNetworks and
// Cluster pod and service networks, or an empty string.
func (g *networkingGuards) firstVirtualNetworkOrClusterOverlap(ctx context.Context, prefix netip.Prefix) (string,
	error) {
	found := ""
	err := forEachRecord(ctx, g.virtualNetworks, func(vn *privatev1.VirtualNetwork) {
		if found != "" {
			return
		}
		if other, err := netip.ParsePrefix(vn.GetSpec().GetIpv4Cidr()); err == nil && other.Overlaps(prefix) {
			found = "VirtualNetwork '" + vn.GetId() + "'"
		}
	})
	if err == nil && found == "" {
		err = forEachRecord(ctx, g.clusters, func(cluster *privatev1.Cluster) {
			if found != "" {
				return
			}
			for _, cidr := range clusterCIDRs(cluster) {
				if other, err := netip.ParsePrefix(cidr); err == nil && other.Overlaps(prefix) {
					found = "the pod or service network of Cluster '" + cluster.GetId() + "'"
					return
				}
			}
		})
	}
	if err != nil {
		return "", grpcstatus.Error(grpccodes.Internal, "failed to check network ranges")
	}
	return found, nil
}
