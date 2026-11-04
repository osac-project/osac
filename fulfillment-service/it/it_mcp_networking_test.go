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
	"time"

	. "github.com/onsi/ginkgo/v2"
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
		networkClassID   string
		mcpHTTPServer    *httptest.Server
	)

	BeforeEach(func() {
		ctx = context.Background()
		virtualNetworkID, subnetID, securityGroupID = "", "", ""
		networkClassID = ""
		mcpHTTPServer = nil
		adminConn := tool.InternalView().AdminConn()
		networkClassesClient = privatev1.NewNetworkClassesClient(adminConn)
		virtualNetworks = privatev1.NewVirtualNetworksClient(adminConn)
		subnets = privatev1.NewSubnetsClient(adminConn)
		securityGroups = privatev1.NewSecurityGroupsClient(adminConn)
		publicVirtualNetworks = publicv1.NewVirtualNetworksClient(tool.ExternalView().UserConn())

		networkClass, err := sharedNetworkClass(ctx, networkClassesClient)
		Expect(err).ToNot(HaveOccurred())
		networkClassID = networkClass.GetObject().GetId()
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

	It("rejects missing parent prerequisites through MCP without mutation", func() {
		session := connectMCPCaller(ctx, mcpHTTPServer.URL, "it-mcp-network-missing", userUsername)
		var err error

		By("creating a Subnet with a genuinely absent parent")
		subnetName := ndUniqueName("mcp-subnet-missing-parent")
		_, err = callMCPTool[mcpserver.CreateNetworkResourceOutput](
			ctx, session, "create_network_resource", mcpserver.CreateNetworkResourceInput{
				ResourceType:     mcpserver.NetworkResourceTypeSubnet,
				Name:             subnetName,
				VirtualNetworkID: "virtual-network-does-not-exist",
				IPv4Cidr:         "10.150.1.0/24",
			},
		)
		expectMCPSafeError(
			err, "not_found", "NotFound", "VirtualNetwork virtual-network-does-not-exist is missing",
		)
		expectNoSubnetNamed(ctx, subnets, subnetName)

		By("creating a SecurityGroup with a genuinely absent parent")
		securityGroupName := ndUniqueName("mcp-sg-missing-parent")
		_, err = callMCPTool[mcpserver.CreateNetworkResourceOutput](
			ctx, session, "create_network_resource", mcpserver.CreateNetworkResourceInput{
				ResourceType:     mcpserver.NetworkResourceTypeSecurityGroup,
				Name:             securityGroupName,
				VirtualNetworkID: "virtual-network-does-not-exist",
			},
		)
		expectMCPSafeError(
			err, "not_found", "NotFound", "VirtualNetwork virtual-network-does-not-exist is missing",
		)
		expectNoSecurityGroupNamed(ctx, securityGroups, securityGroupName)
	})

	It("enforces network mutation authorization through MCP", func() {
		session := connectMCPCaller(ctx, mcpHTTPServer.URL, "it-mcp-network-authz", userUsername)

		virtualNetworkCIDR := ndVirtualNetworkCIDR()
		virtualNetworkName := ndUniqueName("mcp-vn-authz")
		createdVirtualNetwork, err := callMCPTool[mcpserver.CreateNetworkResourceOutput](
			ctx, session, "create_network_resource", mcpserver.CreateNetworkResourceInput{
				ResourceType: mcpserver.NetworkResourceTypeVirtualNetwork,
				Name:         virtualNetworkName,
				IPv4Cidr:     virtualNetworkCIDR,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		virtualNetworkID = createdVirtualNetwork.ID
		setComputeInstanceFixtureVirtualNetworkReady(ctx, virtualNetworks, virtualNetworkID)

		securityGroupName := ndUniqueName("mcp-sg-authz")
		createdSecurityGroup, err := callMCPTool[mcpserver.CreateNetworkResourceOutput](
			ctx, session, "create_network_resource", mcpserver.CreateNetworkResourceInput{
				ResourceType:     mcpserver.NetworkResourceTypeSecurityGroup,
				Name:             securityGroupName,
				VirtualNetworkID: virtualNetworkID,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		securityGroupID = createdSecurityGroup.ID

		By("creating a same-tenant service account without a RoleBinding")
		serviceAccounts := publicv1.NewServiceAccountsClient(tool.InternalView().AdminConn())
		createdAccount, err := serviceAccounts.Create(ctx,
			serviceAccountRequest(usersGroup, ndUniqueName("mcp-network-unauthorized")))
		Expect(err).ToNot(HaveOccurred())
		accountID := createdAccount.GetObject().GetId()
		DeferCleanup(func(cleanupCtx context.Context) {
			deleteServiceAccount(cleanupCtx, serviceAccounts, accountID)
		})

		var account *publicv1.ServiceAccount
		Eventually(func(g Gomega) {
			response, getErr := serviceAccounts.Get(ctx,
				publicv1.ServiceAccountsGetRequest_builder{Id: accountID}.Build())
			g.Expect(getErr).ToNot(HaveOccurred())
			account = response.GetObject()
			g.Expect(account.GetStatus().GetClientSecret().GetId()).ToNot(BeEmpty())
		}, 2*time.Minute, time.Second).Should(Succeed())
		Expect(account.GetMetadata().GetTenant()).To(Equal(usersGroup))

		secret, err := publicv1.NewSecretsClient(tool.InternalView().AdminConn()).Get(ctx,
			publicv1.SecretsGetRequest_builder{Id: account.GetStatus().GetClientSecret().GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		clientID := string(secret.GetObject().GetData()["client_id"])
		clientSecret := string(secret.GetObject().GetData()["client_secret"])
		tokenSource, _ := serviceAccountTokenSource(ctx, clientID, clientSecret)
		unauthorizedSession := connectMCPCallerWithTokenSource(
			ctx, mcpHTTPServer.URL, "it-mcp-network-unauthorized", tokenSource,
		)

		By("rejecting same-tenant creates and deletes without a RoleBinding")
		unauthorizedSubnetName := ndUniqueName("mcp-subnet-unauthorized")
		_, err = callMCPTool[mcpserver.CreateNetworkResourceOutput](
			ctx, unauthorizedSession, "create_network_resource", mcpserver.CreateNetworkResourceInput{
				ResourceType:     mcpserver.NetworkResourceTypeSubnet,
				Name:             unauthorizedSubnetName,
				VirtualNetworkID: virtualNetworkID,
				IPv4Cidr:         netip.PrefixFrom(netip.MustParsePrefix(virtualNetworkCIDR).Addr(), 24).String(),
			},
		)
		expectMCPSafeError(err, "authorization", "PermissionDenied", "not authorized")
		expectNoSubnetNamed(ctx, subnets, unauthorizedSubnetName)

		_, err = callMCPTool[mcpserver.DeleteNetworkResourceOutput](
			ctx, unauthorizedSession, "delete_network_resource", mcpserver.DeleteNetworkResourceInput{
				ResourceType: mcpserver.NetworkResourceTypeSecurityGroup,
				ID:           securityGroupID,
			},
		)
		expectMCPSafeError(err, "authorization", "PermissionDenied", "not authorized")

		By("rejecting cross-tenant SecurityGroup creates and deletes")
		otherSession := connectMCPCaller(ctx, mcpHTTPServer.URL, "it-mcp-other-sg", "ben")
		crossTenantSecurityGroupName := ndUniqueName("mcp-other-sg")
		_, err = callMCPTool[mcpserver.CreateNetworkResourceOutput](
			ctx, otherSession, "create_network_resource", mcpserver.CreateNetworkResourceInput{
				ResourceType:     mcpserver.NetworkResourceTypeSecurityGroup,
				Name:             crossTenantSecurityGroupName,
				VirtualNetworkID: virtualNetworkID,
			},
		)
		expectMCPSafeError(
			err, "not_found", "NotFound", fmt.Sprintf("VirtualNetwork %s is missing", virtualNetworkID),
		)
		expectNoSecurityGroupNamed(ctx, securityGroups, crossTenantSecurityGroupName)

		_, err = callMCPTool[mcpserver.DeleteNetworkResourceOutput](
			ctx, otherSession, "delete_network_resource", mcpserver.DeleteNetworkResourceInput{
				ResourceType: mcpserver.NetworkResourceTypeSecurityGroup,
				ID:           securityGroupID,
			},
		)
		expectMCPSafeError(err, "not_found", "NotFound", "requested OSAC resource was not found")

		securityGroupResponse, err := securityGroups.Get(ctx,
			privatev1.SecurityGroupsGetRequest_builder{Id: securityGroupID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(securityGroupResponse.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeFalse())
	})

	It("rejects deletion of ComputeInstance-referenced network resources through MCP", func() {
		session := connectMCPCaller(ctx, mcpHTTPServer.URL, "it-mcp-network-dependency", userUsername)

		virtualNetworkCIDR := ndVirtualNetworkCIDR()
		createdVirtualNetwork, err := callMCPTool[mcpserver.CreateNetworkResourceOutput](
			ctx, session, "create_network_resource", mcpserver.CreateNetworkResourceInput{
				ResourceType: mcpserver.NetworkResourceTypeVirtualNetwork,
				Name:         ndUniqueName("mcp-vn-dependency"),
				IPv4Cidr:     virtualNetworkCIDR,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		virtualNetworkID = createdVirtualNetwork.ID
		setComputeInstanceFixtureVirtualNetworkReady(ctx, virtualNetworks, virtualNetworkID)

		subnetCIDR := netip.PrefixFrom(netip.MustParsePrefix(virtualNetworkCIDR).Addr(), 24).String()
		createdSubnet, err := callMCPTool[mcpserver.CreateNetworkResourceOutput](
			ctx, session, "create_network_resource", mcpserver.CreateNetworkResourceInput{
				ResourceType:     mcpserver.NetworkResourceTypeSubnet,
				Name:             ndUniqueName("mcp-subnet-dependency"),
				VirtualNetworkID: virtualNetworkID,
				IPv4Cidr:         subnetCIDR,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		subnetID = createdSubnet.ID
		setSubnetReady(ctx, subnets, subnetID)

		createdSecurityGroup, err := callMCPTool[mcpserver.CreateNetworkResourceOutput](
			ctx, session, "create_network_resource", mcpserver.CreateNetworkResourceInput{
				ResourceType:     mcpserver.NetworkResourceTypeSecurityGroup,
				Name:             ndUniqueName("mcp-sg-dependency"),
				VirtualNetworkID: virtualNetworkID,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		securityGroupID = createdSecurityGroup.ID
		setSecurityGroupReady(ctx, securityGroups, securityGroupID)

		templateID := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)
		_, err = createComputeInstanceFixture(ctx, tool.ExternalView().UserConn(),
			publicv1.ComputeInstanceSpec_builder{
				Template: publicv1.ComputeInstanceTemplateReference_builder{Id: templateID}.Build(),
				NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
					publicv1.ComputeNetworkAttachment_builder{
						Subnet: publicv1.SubnetLocalReference_builder{Id: subnetID}.Build(),
						SecurityGroups: []*publicv1.SecurityGroupLocalReference{
							publicv1.SecurityGroupLocalReference_builder{Id: securityGroupID}.Build(),
						},
					}.Build(),
				},
			}.Build())
		Expect(err).ToNot(HaveOccurred())

		_, err = callMCPTool[mcpserver.DeleteNetworkResourceOutput](
			ctx, session, "delete_network_resource", mcpserver.DeleteNetworkResourceInput{
				ResourceType: mcpserver.NetworkResourceTypeSubnet,
				ID:           subnetID,
			},
		)
		expectMCPSafeError(err, "conflict", "Aborted", "active dependencies")
		subnetResponse, err := subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnetID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(subnetResponse.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeFalse())

		_, err = callMCPTool[mcpserver.DeleteNetworkResourceOutput](
			ctx, session, "delete_network_resource", mcpserver.DeleteNetworkResourceInput{
				ResourceType: mcpserver.NetworkResourceTypeSecurityGroup,
				ID:           securityGroupID,
			},
		)
		expectMCPSafeError(err, "conflict", "Aborted", "active dependencies")
		securityGroupResponse, err := securityGroups.Get(ctx,
			privatev1.SecurityGroupsGetRequest_builder{Id: securityGroupID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(securityGroupResponse.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeFalse())
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
})
