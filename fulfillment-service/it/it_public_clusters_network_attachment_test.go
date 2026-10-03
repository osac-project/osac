/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Cluster with ClusterNetworkAttachment", func() {
	var (
		ctx context.Context

		// Public API client for cluster operations.
		clustersClient publicv1.ClustersClient

		// Private API clients for fixture setup (networking resources, host types, templates).
		hostTypesClient       privatev1.HostTypesClient
		templatesClient       privatev1.ClusterTemplatesClient
		networkClassesClient  privatev1.NetworkClassesClient
		virtualNetworksClient privatev1.VirtualNetworksClient
		subnetsClient         privatev1.SubnetsClient
		securityGroupsClient  privatev1.SecurityGroupsClient

		// Shared fixture identifiers.
		hostTypeId       string
		templateId       string
		networkClassId   string
		virtualNetworkId string
		subnetId         string
		securityGroupId  string
	)

	BeforeEach(func() {
		ctx = context.Background()

		// Create the clients:
		clustersClient = publicv1.NewClustersClient(tool.ExternalView().UserConn())
		hostTypesClient = privatev1.NewHostTypesClient(tool.InternalView().AdminConn())
		templatesClient = privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())
		networkClassesClient = privatev1.NewNetworkClassesClient(tool.InternalView().AdminConn())
		virtualNetworksClient = privatev1.NewVirtualNetworksClient(tool.InternalView().AdminConn())
		subnetsClient = privatev1.NewSubnetsClient(tool.InternalView().AdminConn())
		securityGroupsClient = privatev1.NewSecurityGroupsClient(tool.InternalView().AdminConn())

		// Create a host type for testing:
		hostTypeId = fmt.Sprintf("my-host-type-%s", uuid.New())
		_, err := hostTypesClient.Create(ctx, privatev1.HostTypesCreateRequest_builder{
			Object: privatev1.HostType_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-ht-%s", uuid.New()[24:32]),
				}.Build(),
				Id: hostTypeId,
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = hostTypesClient.Delete(ctx, privatev1.HostTypesDeleteRequest_builder{
				Id: hostTypeId,
			}.Build())
		})

		// Create a template for testing:
		templateId = fmt.Sprintf("my-template-%s", uuid.New())
		_, err = templatesClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-tmpl-%s", uuid.New()[24:32]),
				}.Build(),
				Id:          templateId,
				Title:       "My template",
				Description: "My template.",
				NodeSets: map[string]*privatev1.ClusterTemplateNodeSet{
					"my-node-set": privatev1.ClusterTemplateNodeSet_builder{
						HostType: privatev1.HostTypeReference_builder{Id: hostTypeId}.Build(),
						Size:     3,
					}.Build(),
				},
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = templatesClient.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
				Id: templateId,
			}.Build())
		})

		// Create a NetworkClass:
		ncResp, err := networkClassesClient.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
			Object: privatev1.NetworkClass_builder{
				Metadata:      privatev1.Metadata_builder{Name: fmt.Sprintf("test-nc-%s", uuid.New())}.Build(),
				Title:         "Test Network Class for cluster attachment",
				FabricManager: new("netris"),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		networkClassId = ncResp.GetObject().GetId()
		DeferCleanup(func() {
			_, _ = networkClassesClient.Delete(ctx, privatev1.NetworkClassesDeleteRequest_builder{
				Id: networkClassId,
			}.Build())
		})

		// Create a VirtualNetwork:
		virtualNetworkId = fmt.Sprintf("test-vnet-%s", uuid.New())
		_, err = virtualNetworksClient.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-vnet-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Id: virtualNetworkId,
				Spec: privatev1.VirtualNetworkSpec_builder{
					NetworkClass: privatev1.NetworkClassReference_builder{Id: networkClassId}.Build(),
					Region:       "us-east-1",
					Ipv4Cidr:     new("10.100.0.0/16"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = virtualNetworksClient.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{
				Id: virtualNetworkId,
			}.Build())
		})

		// Set VirtualNetwork to READY state. In IT there is no osac-operator feedback controller.
		setComputeInstanceFixtureVirtualNetworkReady(ctx, virtualNetworksClient, virtualNetworkId)

		// Create a Subnet in the VirtualNetwork:
		subnetId = fmt.Sprintf("test-subnet-%s", uuid.New())
		_, err = subnetsClient.Create(ctx, privatev1.SubnetsCreateRequest_builder{
			Object: privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-subnet-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Id: subnetId,
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkId}.Build(),
					Ipv4Cidr:       new("10.100.1.0/24"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = subnetsClient.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{
				Id: subnetId,
			}.Build())
		})

		// Wait for the subnet reconciler to reach PENDING, then set to READY:
		setSubnetReady(ctx, subnetsClient, subnetId)

		// Create a SecurityGroup in the same VirtualNetwork:
		sgResp, err := securityGroupsClient.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
			Object: privatev1.SecurityGroup_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-sg-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		securityGroupId = sgResp.GetObject().GetId()
		DeferCleanup(func() {
			_, _ = securityGroupsClient.Delete(ctx, privatev1.SecurityGroupsDeleteRequest_builder{
				Id: securityGroupId,
			}.Build())
		})

		// Wait for the security group reconciler to reach PENDING, then set to READY:
		setSecurityGroupReady(ctx, securityGroupsClient, securityGroupId)
	})

	It("Can create a cluster with network attachment referencing a READY subnet and security group", func() {
		// Create the cluster with an explicit network attachment:
		createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					NetworkAttachment: publicv1.ClusterNetworkAttachment_builder{
						Subnet: publicv1.SubnetLocalReference_builder{Id: subnetId}.Build(),
						SecurityGroups: []*publicv1.SecurityGroupLocalReference{
							publicv1.SecurityGroupLocalReference_builder{Id: securityGroupId}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()
		DeferCleanup(func() {
			_, _ = clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
		})

		// Verify the network attachment is set correctly via Get:
		getResponse, err := clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{
			Id: object.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		spec := getResponse.GetObject().GetSpec()
		Expect(spec.GetNetworkAttachment()).ToNot(BeNil())
		Expect(spec.GetNetworkAttachment().GetSubnet().GetId()).To(Equal(subnetId))
		Expect(spec.GetNetworkAttachment().GetSecurityGroups()).To(HaveLen(1))
		Expect(spec.GetNetworkAttachment().GetSecurityGroups()[0].GetId()).To(Equal(securityGroupId))
	})

	It("Rejects cluster creation when the referenced subnet is not in READY state", func() {
		// Create a second subnet in the same VirtualNetwork but leave it in PENDING state:
		pendingSubnetId := fmt.Sprintf("test-pending-subnet-%s", uuid.New())
		_, err := subnetsClient.Create(ctx, privatev1.SubnetsCreateRequest_builder{
			Object: privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-pending-sub-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Id: pendingSubnetId,
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkId}.Build(),
					Ipv4Cidr:       new("10.100.2.0/24"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = subnetsClient.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{
				Id: pendingSubnetId,
			}.Build())
		})

		// Wait for the subnet to reach PENDING so the server can find it:
		Eventually(func(g Gomega) {
			probeCtx, cancel := context.WithTimeout(ctx, computeInstanceFixtureProbeTimeout)
			defer cancel()
			resp, err := subnetsClient.Get(probeCtx, privatev1.SubnetsGetRequest_builder{Id: pendingSubnetId}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.SubnetState_SUBNET_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())

		// Attempt to create a cluster referencing the non-READY subnet:
		_, err = clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					NetworkAttachment: publicv1.ClusterNetworkAttachment_builder{
						Subnet: publicv1.SubnetLocalReference_builder{Id: pendingSubnetId}.Build(),
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
		Expect(status.Message()).To(ContainSubstring("not in READY state"))
	})

	It("Rejects cluster creation when the referenced subnet does not exist", func() {
		_, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					NetworkAttachment: publicv1.ClusterNetworkAttachment_builder{
						Subnet: publicv1.SubnetLocalReference_builder{Id: "nonexistent-subnet-id"}.Build(),
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("not found"))
	})

	It("Rejects cluster creation when a security group belongs to a different virtual network", func() {
		// Create a second VirtualNetwork:
		crossVnId := fmt.Sprintf("test-cross-vnet-%s", uuid.New())
		_, err := virtualNetworksClient.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-cross-vnet-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Id: crossVnId,
				Spec: privatev1.VirtualNetworkSpec_builder{
					NetworkClass: privatev1.NetworkClassReference_builder{Id: networkClassId}.Build(),
					Region:       "us-east-1",
					Ipv4Cidr:     new("10.200.0.0/16"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = virtualNetworksClient.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{
				Id: crossVnId,
			}.Build())
		})

		// Set the second VirtualNetwork to READY:
		setComputeInstanceFixtureVirtualNetworkReady(ctx, virtualNetworksClient, crossVnId)

		// Create a SecurityGroup in the second VirtualNetwork:
		crossSgResp, err := securityGroupsClient.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
			Object: privatev1.SecurityGroup_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-cross-sg-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: crossVnId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		crossSgId := crossSgResp.GetObject().GetId()
		DeferCleanup(func() {
			_, _ = securityGroupsClient.Delete(ctx, privatev1.SecurityGroupsDeleteRequest_builder{
				Id: crossSgId,
			}.Build())
		})

		// Wait for the cross-VN security group to reach PENDING, then set to READY:
		setSecurityGroupReady(ctx, securityGroupsClient, crossSgId)

		// Attempt to create a cluster with subnet from VN-A and security group from VN-B:
		_, err = clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					NetworkAttachment: publicv1.ClusterNetworkAttachment_builder{
						Subnet: publicv1.SubnetLocalReference_builder{Id: subnetId}.Build(),
						SecurityGroups: []*publicv1.SecurityGroupLocalReference{
							publicv1.SecurityGroupLocalReference_builder{Id: crossSgId}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("different virtual network"))
	})

	It("Rejects updating the subnet in network attachment after cluster creation", func() {
		// Create a cluster with the shared subnet:
		createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					NetworkAttachment: publicv1.ClusterNetworkAttachment_builder{
						Subnet: publicv1.SubnetLocalReference_builder{Id: subnetId}.Build(),
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()
		DeferCleanup(func() {
			_, _ = clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
		})

		// Create a second READY subnet to attempt switching to:
		otherSubnetId := fmt.Sprintf("test-other-subnet-%s", uuid.New())
		_, err = subnetsClient.Create(ctx, privatev1.SubnetsCreateRequest_builder{
			Object: privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-other-sub-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Id: otherSubnetId,
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkId}.Build(),
					Ipv4Cidr:       new("10.100.3.0/24"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = subnetsClient.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{
				Id: otherSubnetId,
			}.Build())
		})

		// Wait for the second subnet to reach PENDING, then set to READY:
		setSubnetReady(ctx, subnetsClient, otherSubnetId)

		// Attempt to update the cluster's network attachment subnet:
		_, err = clustersClient.Update(ctx, publicv1.ClustersUpdateRequest_builder{
			Object: publicv1.Cluster_builder{
				Id: object.GetId(),
				Metadata: publicv1.Metadata_builder{
					Name: object.GetMetadata().GetName(),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					NetworkAttachment: publicv1.ClusterNetworkAttachment_builder{
						Subnet: publicv1.SubnetLocalReference_builder{Id: otherSubnetId}.Build(),
					}.Build(),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.network_attachment.subnet"}},
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("subnet is immutable"))
	})
})
