package servers

import (
	"context"
	"errors"
	"fmt"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func (s *PrivateClustersServer) prepareAutomaticClusterNetworking(
	ctx context.Context,
	cluster *privatev1.Cluster,
) error {
	spec := cluster.GetSpec()
	if !spec.GetSupportsAutomaticCaasEndpoints() {
		if spec.GetIngressAllocatedAddress() != "" {
			return grpcstatus.Error(grpccodes.InvalidArgument,
				"spec.ingress_allocated_address is assigned by Fulfillment")
		}
		return nil
	}
	if spec.GetIngressAllocatedAddress() != "" {
		return grpcstatus.Error(grpccodes.InvalidArgument,
			"spec.ingress_allocated_address is assigned by Fulfillment")
	}
	attachment := spec.GetNetworkAttachment()
	if attachment == nil || refKey(attachment.GetSubnet()) == "" {
		if s.settings.ProvisioningEnabled {
			return grpcstatus.Error(grpccodes.FailedPrecondition,
				"spec.network_attachment.subnet is required for CaaS networking")
		}
		return nil
	}

	// validateNetworkAttachmentState already resolved the Subnet and acquired
	// its serialization lock after locking the parent VirtualNetwork. Re-read
	// through the same scoped resolver without taking the child lock first.
	subnet, err := resolveNetworkingReference(ctx, s.subnetsDao, cluster.GetMetadata(),
		attachment.GetSubnet(), "subnet", grpccodes.NotFound, false)
	if err != nil {
		return err
	}
	virtualNetworkID := refKey(subnet.GetSpec().GetVirtualNetwork())
	if virtualNetworkID == "" {
		return grpcstatus.Errorf(grpccodes.Internal,
			"Subnet '%s' has no VirtualNetwork reference", subnet.GetId())
	}
	virtualNetwork, err := s.virtualNetworksDao.Get().SetId(virtualNetworkID).Do(ctx)
	if err != nil {
		return fmt.Errorf("reading parent VirtualNetwork %s: %w", virtualNetworkID, err)
	}
	if virtualNetwork.GetObject().GetMetadata().HasDeletionTimestamp() ||
		virtualNetwork.GetObject().GetStatus().GetState() != privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"VirtualNetwork '%s' is not READY", virtualNetworkID)
	}

	if err := validateResolvedSubnetReady(subnet, subnet.GetId(), " in spec.network_attachment"); err != nil {
		return err
	}
	if refKey(subnet.GetSpec().GetVirtualNetwork()) != virtualNetworkID {
		return grpcstatus.Error(grpccodes.Aborted, "Subnet parent changed while preparing Cluster networking")
	}

	if s.settings.ProvisioningEnabled {
		used, err := s.clusterIngressAddressesInUse(ctx, subnet.GetId())
		if err != nil {
			return grpcstatus.Errorf(grpccodes.Internal,
				"failed to check existing guest ingress allocations for Subnet '%s'", subnet.GetId())
		}
		address, err := selectClusterIngressAddress(subnet.GetSpec().GetIpv4Cidr(),
			subnet.GetStatus().GetIpam(), used)
		if err != nil {
			if errors.Is(err, errClusterIngressAllocationExhausted) {
				return grpcstatus.Errorf(grpccodes.FailedPrecondition,
					"spec.network_attachment: no eligible guest ingress address remains in Subnet '%s'", subnet.GetId())
			}
			return grpcstatus.Errorf(grpccodes.FailedPrecondition,
				"spec.network_attachment: cannot allocate guest ingress address from Subnet '%s': %v",
				subnet.GetId(), err)
		}
		spec.SetIngressAllocatedAddress(address)
	}

	return s.createAutomaticClusterSecurityGroup(ctx, cluster, subnet, virtualNetworkID)
}

func (s *PrivateClustersServer) clusterIngressAddressesInUse(
	ctx context.Context,
	subnetID string,
) (map[string]struct{}, error) {
	filter := fmt.Sprintf("this.spec.network_attachment.subnet.id == %q", subnetID)
	const pageSize int32 = 500
	used := make(map[string]struct{})
	err := forEachDAOPage(ctx, pageSize, func(ctx context.Context, offset, limit int32) ([]*privatev1.Cluster, error) {
		clusters, err := s.generic.dao.List().SetFilter(filter).SetOffset(offset).SetLimit(limit).Do(ctx)
		if err != nil {
			return nil, err
		}
		return clusters.GetItems(), nil
	}, func(cluster *privatev1.Cluster) {
		address := cluster.GetSpec().GetIngressAllocatedAddress()
		if address != "" {
			used[address] = struct{}{}
		}
	})
	if err != nil {
		return nil, err
	}
	return used, nil
}

func (s *PrivateClustersServer) createAutomaticClusterSecurityGroup(
	ctx context.Context,
	cluster *privatev1.Cluster,
	subnet *privatev1.Subnet,
	virtualNetworkID string,
) error {
	subnetCIDR := subnet.GetSpec().GetIpv4Cidr()
	port80, port443 := int32(80), int32(443)
	allProtocolsFromSubnet := privatev1.SecurityRule_builder{
		Protocol: privatev1.Protocol_PROTOCOL_ALL,
		Ipv4Cidr: &subnetCIDR,
	}.Build()
	webFromAnywhere := func(port int32) *privatev1.SecurityRule {
		from, to := port, port
		cidr := "0.0.0.0/0"
		return privatev1.SecurityRule_builder{
			Protocol: privatev1.Protocol_PROTOCOL_TCP,
			PortFrom: &from,
			PortTo:   &to,
			Ipv4Cidr: &cidr,
		}.Build()
	}
	allEgress := "0.0.0.0/0"
	groupID := cluster.GetId()
	tenant := cluster.GetMetadata().GetTenant()
	group := privatev1.SecurityGroup_builder{
		Metadata: privatev1.Metadata_builder{
			Name:    automaticSecurityGroupPrefix + groupID,
			Tenant:  tenant,
			Project: cluster.GetMetadata().GetProject(),
			Creator: systemCreator,
			Labels: map[string]string{
				autoCreatedLabel:     "true",
				autoCreatedForLabel:  groupID,
				autoCreatedKindLabel: "cluster",
			},
			Annotations: map[string]string{
				tenantAnnotation:         tenant,
				ownerReferenceAnnotation: groupID,
			},
		}.Build(),
		Spec: privatev1.SecurityGroupSpec_builder{
			VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
			Ingress: []*privatev1.SecurityRule{
				allProtocolsFromSubnet,
				webFromAnywhere(port80),
				webFromAnywhere(port443),
			},
			Egress: []*privatev1.SecurityRule{privatev1.SecurityRule_builder{
				Protocol: privatev1.Protocol_PROTOCOL_ALL,
				Ipv4Cidr: &allEgress,
			}.Build()},
		}.Build(),
		Status: privatev1.SecurityGroupStatus_builder{
			State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING,
		}.Build(),
	}.Build()
	if err := validateSecurityRules(group.GetSpec()); err != nil {
		return grpcstatus.Errorf(grpccodes.Internal, "invalid automatic Cluster SecurityGroup rules: %v", err)
	}
	if _, err := s.securityGroupsDao.Create().SetObject(group).Do(ctx); err != nil {
		return fmt.Errorf("creating automatic SecurityGroup for Cluster %s: %w", groupID, err)
	}
	return nil
}
