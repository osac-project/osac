/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions under the License.
*/

package it

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/netip"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/service/start/mcpserver"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("MCP network resources", func() {
	var (
		ctx                   context.Context
		networkClassesClient  privatev1.NetworkClassesClient
		virtualNetworks       privatev1.VirtualNetworksClient
		subnets               privatev1.SubnetsClient
		securityGroups        privatev1.SecurityGroupsClient
		publicVirtualNetworks publicv1.VirtualNetworksClient

		virtualNetworkID string
		subnetID         string
		securityGroupID  string
		mcpHTTPServer    *httptest.Server
	)

	BeforeEach(func() {
		ctx = context.Background()
		virtualNetworkID, subnetID, securityGroupID = "", "", ""
		mcpHTTPServer = nil
		adminConn := tool.InternalView().AdminConn()
		networkClassesClient = privatev1.NewNetworkClassesClient(adminConn)
		virtualNetworks = privatev1.NewVirtualNetworksClient(adminConn)
		subnets = privatev1.NewSubnetsClient(adminConn)
		securityGroups = privatev1.NewSecurityGroupsClient(adminConn)
		publicVirtualNetworks = publicv1.NewVirtualNetworksClient(tool.ExternalView().UserConn())

		networkClass, err := sharedNetworkClass(ctx, networkClassesClient)
		Expect(err).ToNot(HaveOccurred())
		networkClassID := networkClass.GetObject().GetId()
		waitForNetworkClassReady(ctx, networkClassesClient, networkClassID)
		err = updateNetworkClassStatusWithRetry(ctx, networkClassesClient, networkClassID, privatev1.NetworkClassStatus_builder{
			State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			Hub:   hubId,
		}.Build(), &fieldmaskpb.FieldMask{Paths: []string{"status.state", "status.hub"}})
		Expect(err).ToNot(HaveOccurred())
		waitForNetworkClassReady(ctx, networkClassesClient, networkClassID)

		mcpHTTPServer = startInProcessMCPServer(ctx, "fulfillment-mcp-network-it")

		DeferCleanup(func(cleanupCtx context.Context) {
			if securityGroupID != "" {
				Expect(cleanupSecurityGroupFixture(cleanupCtx, securityGroups, securityGroupID)).To(Succeed())
			}
			if subnetID != "" {
				Expect(cleanupSubnetFixture(cleanupCtx, subnets, subnetID)).To(Succeed())
			}
			if virtualNetworkID != "" {
				Expect(cleanupVirtualNetworkFixture(cleanupCtx, virtualNetworks, virtualNetworkID)).To(Succeed())
			}
		})
	})

	It("creates and deletes tenant network prerequisites through the public API", func() {
		session := connectMCPCaller(ctx, mcpHTTPServer.URL, "it-mcp-network-client", userUsername)

		virtualNetworkCIDR := ndVirtualNetworkCIDR()
		virtualNetworkName := ndUniqueName("mcp-vn")
		createdVirtualNetwork, err := callMCPTool[mcpserver.CreateNetworkResourceOutput](
			ctx, session, "create_network_resource", mcpserver.CreateNetworkResourceInput{
				ResourceType: mcpserver.NetworkResourceTypeVirtualNetwork,
				Name:         virtualNetworkName,
				IPv4Cidr:     virtualNetworkCIDR,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(createdVirtualNetwork.ResourceType).To(Equal(mcpserver.NetworkResourceTypeVirtualNetwork))
		Expect(createdVirtualNetwork.ID).ToNot(BeEmpty())
		Expect(createdVirtualNetwork.Version).To(BeNumerically(">=", 0))
		Expect(createdVirtualNetwork.State).To(Equal("VIRTUAL_NETWORK_STATE_PENDING"))
		virtualNetworkID = createdVirtualNetwork.ID

		virtualNetworkResponse, err := publicVirtualNetworks.Get(ctx, publicv1.VirtualNetworksGetRequest_builder{
			Id: virtualNetworkID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		virtualNetwork := virtualNetworkResponse.GetObject()
		Expect(virtualNetwork.GetMetadata().GetName()).To(Equal(virtualNetworkName))
		Expect(virtualNetwork.GetMetadata().GetTenant()).To(Equal(usersGroup))
		Expect(virtualNetwork.GetSpec().GetIpv4Cidr()).To(Equal(virtualNetworkCIDR))

		_, err = callMCPTool[mcpserver.CreateNetworkResourceOutput](
			ctx, session, "create_network_resource", mcpserver.CreateNetworkResourceInput{
				ID:           virtualNetworkID,
				ResourceType: mcpserver.NetworkResourceTypeVirtualNetwork,
				Name:         virtualNetworkName,
				IPv4Cidr:     "10.201.0.0/16",
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unsupported_in_place"))
		Expect(err.Error()).To(ContainSubstring(`"category":"invalid_request"`))
		Expect(err.Error()).To(ContainSubstring(`"grpc_code":"InvalidArgument"`))
		virtualNetworkResponse, err = publicVirtualNetworks.Get(ctx, publicv1.VirtualNetworksGetRequest_builder{
			Id: virtualNetworkID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(virtualNetworkResponse.GetObject().GetSpec().GetIpv4Cidr()).To(Equal(virtualNetworkCIDR))
		Expect(virtualNetworkResponse.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeFalse())

		invalidName := ndUniqueName("mcp-vn-ipv6")
		_, err = callMCPTool[mcpserver.CreateNetworkResourceOutput](
			ctx, session, "create_network_resource", mcpserver.CreateNetworkResourceInput{
				ResourceType: mcpserver.NetworkResourceTypeVirtualNetwork,
				Name:         invalidName,
				IPv4Cidr:     "2001:db8::/32",
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`"category":"invalid_request"`))
		Expect(err.Error()).To(ContainSubstring("canonical IPv4 CIDR"))
		invalidNetworks, err := virtualNetworks.List(ctx, privatev1.VirtualNetworksListRequest_builder{
			Filter: proto.String(fmt.Sprintf("this.metadata.name == %q", invalidName)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(invalidNetworks.GetItems()).To(BeEmpty())

		setComputeInstanceFixtureVirtualNetworkReady(ctx, virtualNetworks, virtualNetworkID)
		parentPrefix := netip.MustParsePrefix(virtualNetworkCIDR)
		subnetCIDR := netip.PrefixFrom(parentPrefix.Addr(), 24).String()
		subnetName := ndUniqueName("mcp-subnet")
		createdSubnet, err := callMCPTool[mcpserver.CreateNetworkResourceOutput](
			ctx, session, "create_network_resource", mcpserver.CreateNetworkResourceInput{
				ResourceType:     mcpserver.NetworkResourceTypeSubnet,
				Name:             subnetName,
				VirtualNetworkID: virtualNetworkID,
				IPv4Cidr:         subnetCIDR,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(createdSubnet.State).To(Equal("SUBNET_STATE_PENDING"))
		subnetID = createdSubnet.ID
		subnetResponse, err := subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnetID}.Build())
		Expect(err).ToNot(HaveOccurred())
		subnet := subnetResponse.GetObject()
		Expect(subnet.GetMetadata().GetTenant()).To(Equal(usersGroup))
		Expect(subnet.GetMetadata().GetAnnotations()).To(HaveKeyWithValue(
			"osac.openshift.io/owner-reference", virtualNetworkID,
		))
		Expect(subnet.GetSpec().GetVirtualNetwork().GetId()).To(Equal(virtualNetworkID))
		Expect(subnet.GetSpec().GetIpv4Cidr()).To(Equal(subnetCIDR))

		securityGroupName := ndUniqueName("mcp-sg")
		port := int32(443)
		createdSecurityGroup, err := callMCPTool[mcpserver.CreateNetworkResourceOutput](
			ctx, session, "create_network_resource", mcpserver.CreateNetworkResourceInput{
				ResourceType:     mcpserver.NetworkResourceTypeSecurityGroup,
				Name:             securityGroupName,
				VirtualNetworkID: virtualNetworkID,
				IngressRules: []mcpserver.NetworkSecurityRuleInput{{
					Protocol: "tcp",
					PortFrom: &port,
					PortTo:   &port,
					IPv4Cidr: "0.0.0.0/0",
				}},
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(createdSecurityGroup.State).To(Equal("SECURITY_GROUP_STATE_PENDING"))
		securityGroupID = createdSecurityGroup.ID
		securityGroupResponse, err := securityGroups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{
			Id: securityGroupID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		securityGroup := securityGroupResponse.GetObject()
		Expect(securityGroup.GetMetadata().GetTenant()).To(Equal(usersGroup))
		Expect(securityGroup.GetMetadata().GetAnnotations()).To(HaveKeyWithValue(
			"osac.openshift.io/owner-reference", virtualNetworkID,
		))
		Expect(securityGroup.GetSpec().GetVirtualNetwork().GetId()).To(Equal(virtualNetworkID))
		Expect(securityGroup.GetSpec().GetIngress()).To(HaveLen(1))
		Expect(securityGroup.GetSpec().GetIngress()[0].GetPortFrom()).To(Equal(int32(443)))

		otherSession := connectMCPCaller(ctx, mcpHTTPServer.URL, "it-mcp-other-network", "ben")

		otherSubnetName := ndUniqueName("mcp-other-subnet")
		_, err = callMCPTool[mcpserver.CreateNetworkResourceOutput](
			ctx, otherSession, "create_network_resource", mcpserver.CreateNetworkResourceInput{
				ResourceType:     mcpserver.NetworkResourceTypeSubnet,
				Name:             otherSubnetName,
				VirtualNetworkID: virtualNetworkID,
				IPv4Cidr:         subnetCIDR,
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`"category":"not_found"`))
		Expect(err.Error()).To(ContainSubstring(fmt.Sprintf("VirtualNetwork %s is missing", virtualNetworkID)))
		otherSubnets, err := subnets.List(ctx, privatev1.SubnetsListRequest_builder{
			Filter: proto.String(fmt.Sprintf("this.metadata.name == %q", otherSubnetName)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(otherSubnets.GetItems()).To(BeEmpty())

		_, err = callMCPTool[mcpserver.DeleteNetworkResourceOutput](
			ctx, otherSession, "delete_network_resource", mcpserver.DeleteNetworkResourceInput{
				ResourceType: mcpserver.NetworkResourceTypeVirtualNetwork,
				ID:           virtualNetworkID,
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`"grpc_code":"NotFound"`))
		virtualNetworkResponse, err = publicVirtualNetworks.Get(ctx, publicv1.VirtualNetworksGetRequest_builder{
			Id: virtualNetworkID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(virtualNetworkResponse.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeFalse())

		_, err = callMCPTool[mcpserver.DeleteNetworkResourceOutput](
			ctx, session, "delete_network_resource", mcpserver.DeleteNetworkResourceInput{
				ResourceType: mcpserver.NetworkResourceTypeVirtualNetwork,
				ID:           virtualNetworkID,
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`"category":"conflict"`))
		Expect(err.Error()).To(ContainSubstring(`"grpc_code":"Aborted"`))
		Expect(err.Error()).To(ContainSubstring("active dependencies"))
		virtualNetworkResponse, err = publicVirtualNetworks.Get(ctx, publicv1.VirtualNetworksGetRequest_builder{
			Id: virtualNetworkID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(virtualNetworkResponse.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeFalse())

		deletedSecurityGroup, err := callMCPTool[mcpserver.DeleteNetworkResourceOutput](
			ctx, session, "delete_network_resource", mcpserver.DeleteNetworkResourceInput{
				ResourceType: mcpserver.NetworkResourceTypeSecurityGroup,
				ID:           securityGroupID,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(deletedSecurityGroup).To(Equal(mcpserver.DeleteNetworkResourceOutput{
			ResourceType: mcpserver.NetworkResourceTypeSecurityGroup,
			ID:           securityGroupID,
		}))
		Expect(cleanupSecurityGroupFixture(ctx, securityGroups, securityGroupID)).To(Succeed())
		securityGroupID = ""

		deletedSubnet, err := callMCPTool[mcpserver.DeleteNetworkResourceOutput](
			ctx, session, "delete_network_resource", mcpserver.DeleteNetworkResourceInput{
				ResourceType: mcpserver.NetworkResourceTypeSubnet,
				ID:           subnetID,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(deletedSubnet.ID).To(Equal(subnetID))
		Expect(cleanupSubnetFixture(ctx, subnets, subnetID)).To(Succeed())
		subnetID = ""

		deletedVirtualNetwork, err := callMCPTool[mcpserver.DeleteNetworkResourceOutput](
			ctx, session, "delete_network_resource", mcpserver.DeleteNetworkResourceInput{
				ResourceType: mcpserver.NetworkResourceTypeVirtualNetwork,
				ID:           virtualNetworkID,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(deletedVirtualNetwork.ID).To(Equal(virtualNetworkID))
		Expect(cleanupVirtualNetworkFixture(ctx, virtualNetworks, virtualNetworkID)).To(Succeed())
		virtualNetworkID = ""
	})

	It("rejects an oversized SecurityGroup rule request without mutation", func() {
		session := connectMCPCaller(ctx, mcpHTTPServer.URL, "it-mcp-network-limit", userUsername)
		securityGroupName := ndUniqueName("mcp-sg-limit")
		rule := mcpserver.NetworkSecurityRuleInput{
			Protocol: "all",
			IPv4Cidr: "0.0.0.0/0",
		}
		ingressRules := make([]mcpserver.NetworkSecurityRuleInput, 50)
		egressRules := make([]mcpserver.NetworkSecurityRuleInput, 51)
		for index := range ingressRules {
			ingressRules[index] = rule
		}
		for index := range egressRules {
			egressRules[index] = rule
		}

		_, err := callMCPTool[mcpserver.CreateNetworkResourceOutput](
			ctx, session, "create_network_resource", mcpserver.CreateNetworkResourceInput{
				ResourceType:     mcpserver.NetworkResourceTypeSecurityGroup,
				Name:             securityGroupName,
				VirtualNetworkID: "virtual-network-does-not-exist",
				IngressRules:     ingressRules,
				EgressRules:      egressRules,
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`"category":"invalid_request"`))
		Expect(err.Error()).To(ContainSubstring("more than 100 rules in total"))

		securityGroupList, err := securityGroups.List(ctx, privatev1.SecurityGroupsListRequest_builder{
			Filter: proto.String(fmt.Sprintf("this.metadata.name == %q", securityGroupName)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(securityGroupList.GetItems()).To(BeEmpty())
	})

	It("creates and deletes public exposure resources through the public API", func() {
		ndSkipUnlessProviderNetworkingEnabled(ctx, "MCP public exposure resources")
		session := connectMCPCaller(ctx, mcpHTTPServer.URL, "it-mcp-exposure-client", userUsername)
		networking := newNDClients()

		virtualNetworkCIDR := ndVirtualNetworkCIDR()
		createdVirtualNetwork, err := callMCPTool[mcpserver.CreateNetworkResourceOutput](
			ctx, session, "create_network_resource", mcpserver.CreateNetworkResourceInput{
				ResourceType: mcpserver.NetworkResourceTypeVirtualNetwork,
				Name:         ndUniqueName("mcp-exposure-vn"),
				IPv4Cidr:     virtualNetworkCIDR,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		virtualNetworkID = createdVirtualNetwork.ID
		setComputeInstanceFixtureVirtualNetworkReady(ctx, virtualNetworks, virtualNetworkID)

		pool := ndCreatePool(ctx, networking, ndPoolBase()+"/28")
		ndSetPoolState(ctx, networking, pool.GetId(), privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY)

		externalIPName := ndUniqueName("mcp-eip")
		createdExternalIP, err := callMCPTool[mcpserver.ExposeNetworkResourceOutput](
			ctx, session, "expose_network_resource", mcpserver.ExposeNetworkResourceInput{
				ResourceType:     mcpserver.NetworkResourceTypeExternalIP,
				Name:             externalIPName,
				ExternalIPPoolID: pool.GetId(),
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(createdExternalIP.ResourceType).To(Equal(mcpserver.NetworkResourceTypeExternalIP))
		Expect(createdExternalIP.ID).ToNot(BeEmpty())
		Expect(createdExternalIP.Version).To(BeNumerically(">=", 0))
		Expect(createdExternalIP.State).To(Equal("EXTERNAL_IP_STATE_PENDING"))
		externalIPID := createdExternalIP.ID

		externalIPResponse, err := networking.publicExternalIPs.Get(ctx, publicv1.ExternalIPsGetRequest_builder{
			Id: externalIPID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		externalIP := externalIPResponse.GetObject()
		Expect(externalIP.GetMetadata().GetName()).To(Equal(externalIPName))
		Expect(externalIP.GetMetadata().GetTenant()).To(Equal(usersGroup))
		Expect(externalIP.GetSpec().GetPool().GetId()).To(Equal(pool.GetId()))

		externalIPList, err := callMCPTool[mcpserver.ListResourcesOutput](
			ctx, session, "list_resources", mcpserver.ListResourcesInput{
				ResourceType: mcpserver.ResourceTypeExternalIP,
				Filter:       fmt.Sprintf("this.id == %q", externalIPID),
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(externalIPList.Items).To(ConsistOf(HaveField("ID", externalIPID)))
		externalIPResource, err := callMCPTool[mcpserver.GetResourceOutput](
			ctx, session, "get_resource", mcpserver.GetResourceInput{
				ResourceType: mcpserver.ResourceTypeExternalIP,
				ID:           externalIPID,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(externalIPResource.Resource).To(HaveKeyWithValue("id", externalIPID))

		Expect(setExternalIPFixtureState(
			ctx, networking.externalIPs, externalIPID, privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
		)).To(Succeed())

		natGatewayName := ndUniqueName("mcp-nat")
		createdNATGateway, err := callMCPTool[mcpserver.ExposeNetworkResourceOutput](
			ctx, session, "expose_network_resource", mcpserver.ExposeNetworkResourceInput{
				ResourceType:     mcpserver.NetworkResourceTypeNATGateway,
				Name:             natGatewayName,
				VirtualNetworkID: virtualNetworkID,
				ExternalIPID:     externalIPID,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(createdNATGateway.ResourceType).To(Equal(mcpserver.NetworkResourceTypeNATGateway))
		Expect(createdNATGateway.State).To(Equal("NAT_GATEWAY_STATE_PENDING"))
		natGatewayID := createdNATGateway.ID

		natGatewayResponse, err := networking.publicNATGateways.Get(ctx, publicv1.NATGatewaysGetRequest_builder{
			Id: natGatewayID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		natGateway := natGatewayResponse.GetObject()
		Expect(natGateway.GetMetadata().GetName()).To(Equal(natGatewayName))
		Expect(natGateway.GetMetadata().GetTenant()).To(Equal(usersGroup))
		Expect(natGateway.GetSpec().GetVirtualNetwork().GetId()).To(Equal(virtualNetworkID))
		Expect(natGateway.GetSpec().GetExternalIp().GetId()).To(Equal(externalIPID))

		natGatewayList, err := callMCPTool[mcpserver.ListResourcesOutput](
			ctx, session, "list_resources", mcpserver.ListResourcesInput{
				ResourceType: mcpserver.ResourceTypeNATGateway,
				Filter:       fmt.Sprintf("this.id == %q", natGatewayID),
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(natGatewayList.Items).To(ConsistOf(HaveField("ID", natGatewayID)))
		natGatewayResource, err := callMCPTool[mcpserver.GetResourceOutput](
			ctx, session, "get_resource", mcpserver.GetResourceInput{
				ResourceType: mcpserver.ResourceTypeNATGateway,
				ID:           natGatewayID,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(natGatewayResource.Resource).To(HaveKeyWithValue("id", natGatewayID))

		attachmentName := ndUniqueName("mcp-missing-eipa")
		_, err = callMCPTool[mcpserver.ExposeNetworkResourceOutput](
			ctx, session, "expose_network_resource", mcpserver.ExposeNetworkResourceInput{
				ResourceType:    mcpserver.NetworkResourceTypeExternalIPAttachment,
				Name:            attachmentName,
				ExternalIPID:    "mcp-missing-eip",
				ClusterID:       "mcp-missing-cluster",
				ClusterEndpoint: mcpserver.NetworkClusterEndpointAPI,
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`"category":"not_found"`))
		Expect(err.Error()).To(ContainSubstring(`"grpc_code":"NotFound"`))
		Expect(err.Error()).To(ContainSubstring("is missing"))
		attachments, err := networking.attachments.List(ctx, privatev1.ExternalIPAttachmentsListRequest_builder{
			Filter: proto.String(fmt.Sprintf("this.metadata.name == %q", attachmentName)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(attachments.GetItems()).To(BeEmpty())

		deletedNATGateway, err := callMCPTool[mcpserver.DeleteNetworkResourceOutput](
			ctx, session, "delete_network_resource", mcpserver.DeleteNetworkResourceInput{
				ResourceType: mcpserver.NetworkResourceTypeNATGateway,
				ID:           natGatewayID,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(deletedNATGateway.ID).To(Equal(natGatewayID))
		Expect(cleanupNATGatewayFixture(
			ctx, networking.publicNATGateways, networking.natGateways, natGatewayID,
		)).To(Succeed())

		deletedExternalIP, err := callMCPTool[mcpserver.DeleteNetworkResourceOutput](
			ctx, session, "delete_network_resource", mcpserver.DeleteNetworkResourceInput{
				ResourceType: mcpserver.NetworkResourceTypeExternalIP,
				ID:           externalIPID,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(deletedExternalIP.ID).To(Equal(externalIPID))
		Expect(cleanupExternalIPFixture(ctx, networking.externalIPs, externalIPID)).To(Succeed())
	})
})
