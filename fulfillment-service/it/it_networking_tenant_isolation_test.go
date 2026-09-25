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

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// Networking tenant isolation exercises the private API Create path with admin
// TotalVisibility. Cross-tenant reference rejection must not rely on DAO
// tenancy filtering (which is a no-op for private callers).
var _ = Describe("Networking tenant isolation", func() {
	var (
		ctx context.Context

		tenantsClient         privatev1.TenantsClient
		networkClassesClient  privatev1.NetworkClassesClient
		virtualNetworksClient privatev1.VirtualNetworksClient
		subnetsClient         privatev1.SubnetsClient
		securityGroupsClient  privatev1.SecurityGroupsClient
		poolsClient           privatev1.ExternalIPPoolsClient
		externalIPsClient     privatev1.ExternalIPsClient
		natGatewaysClient     privatev1.NATGatewaysClient
		attachmentsClient     privatev1.ExternalIPAttachmentsClient
		instanceTypesClient   privatev1.BareMetalInstanceTypesClient
		templatesClient       privatev1.ClusterTemplatesClient
		clustersClient        privatev1.ClustersClient

		tenantAName    string
		tenantBName    string
		networkClassId string
	)

	BeforeEach(func() {
		ctx = context.Background()

		tenantsClient = privatev1.NewTenantsClient(tool.InternalView().AdminConn())
		networkClassesClient = privatev1.NewNetworkClassesClient(tool.InternalView().AdminConn())
		virtualNetworksClient = privatev1.NewVirtualNetworksClient(tool.InternalView().AdminConn())
		subnetsClient = privatev1.NewSubnetsClient(tool.InternalView().AdminConn())
		securityGroupsClient = privatev1.NewSecurityGroupsClient(tool.InternalView().AdminConn())
		poolsClient = privatev1.NewExternalIPPoolsClient(tool.InternalView().AdminConn())
		externalIPsClient = privatev1.NewExternalIPsClient(tool.InternalView().AdminConn())
		natGatewaysClient = privatev1.NewNATGatewaysClient(tool.InternalView().AdminConn())
		attachmentsClient = privatev1.NewExternalIPAttachmentsClient(tool.InternalView().AdminConn())
		instanceTypesClient = privatev1.NewBareMetalInstanceTypesClient(tool.InternalView().AdminConn())
		templatesClient = privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())
		clustersClient = privatev1.NewClustersClient(tool.InternalView().AdminConn())

		tenantAName = fmt.Sprintf("net-iso-a-%s", uuid.New()[24:32])
		tenantBName = fmt.Sprintf("net-iso-b-%s", uuid.New()[24:32])
		_ = createTenant(ctx, tenantsClient, tenantAName)
		_ = createTenant(ctx, tenantsClient, tenantBName)

		ncResp, err := networkClassesClient.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
			Object: privatev1.NetworkClass_builder{
				Metadata:      privatev1.Metadata_builder{Name: fmt.Sprintf("nc-%s", uuid.New()[24:32])}.Build(),
				Title:         "Networking tenant isolation NetworkClass",
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
	})

	createReadyVirtualNetwork := func(tenant, ipv4Cidr string) string {
		vnID := fmt.Sprintf("vn-%s", uuid.New())
		_, err := virtualNetworksClient.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Id: vnID,
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("vn-%s", uuid.New()[24:32]),
					Tenant: tenant,
				}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					NetworkClass: privatev1.NetworkClassReference_builder{Id: networkClassId}.Build(),
					Region:       "us-east-1",
					Ipv4Cidr:     new(ipv4Cidr),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = virtualNetworksClient.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{
				Id: vnID,
			}.Build())
		})

		Eventually(func(g Gomega) {
			resp, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{
				Id: vnID,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())

		vnGetResp, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{
			Id: vnID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		vn := vnGetResp.GetObject()
		vn.SetStatus(privatev1.VirtualNetworkStatus_builder{
			State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
		}.Build())
		_, err = virtualNetworksClient.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
			Object:     vn,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		return vnID
	}

	createReadyPool := func(tenant string) string {
		poolID := fmt.Sprintf("pool-%s", uuid.New())
		metadata := privatev1.Metadata_builder{
			Name: fmt.Sprintf("pool-%s", uuid.New()[24:32]),
		}
		if tenant != "" {
			metadata.Tenant = tenant
		}
		_, err := poolsClient.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Id:       poolID,
				Metadata: metadata.Build(),
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs:    []string{uniqueCIDR()},
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = poolsClient.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{
				Id: poolID,
			}.Build())
		})

		Eventually(func(g Gomega) {
			resp, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{
				Id: poolID,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())

		poolGetResp, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{
			Id: poolID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		pool := poolGetResp.GetObject()
		if tenant == "" {
			Expect(pool.GetMetadata().GetTenant()).To(Equal(auth.SharedTenant))
		}
		pool.SetStatus(privatev1.ExternalIPPoolStatus_builder{
			State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
			Total:     pool.GetStatus().GetTotal(),
			Available: pool.GetStatus().GetAvailable(),
			Allocated: pool.GetStatus().GetAllocated(),
		}.Build())
		_, err = poolsClient.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
			Object:     pool,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		return poolID
	}

	createExternalIP := func(tenant, poolID string) string {
		eipID := fmt.Sprintf("eip-%s", uuid.New())
		_, err := externalIPsClient.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{
			Object: privatev1.ExternalIP_builder{
				Id: eipID,
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("eip-%s", uuid.New()[24:32]),
					Tenant: tenant,
				}.Build(),
				Spec: privatev1.ExternalIPSpec_builder{
					Pool: privatev1.ExternalIPPoolReference_builder{Id: poolID}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = externalIPsClient.Delete(ctx, privatev1.ExternalIPsDeleteRequest_builder{
				Id: eipID,
			}.Build())
		})
		return eipID
	}

	promoteExternalIPAllocated := func(eipID string) {
		ipGetResp, err := externalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
			Id: eipID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		ip := ipGetResp.GetObject()
		ip.SetStatus(privatev1.ExternalIPStatus_builder{
			State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
		}.Build())
		_, err = externalIPsClient.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
			Object:     ip,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
	}

	createCluster := func(tenant string) string {
		instanceTypeName := fmt.Sprintf("bmit-%s", uuid.New()[24:32])
		_, err := instanceTypesClient.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceType_builder{
				Metadata: privatev1.Metadata_builder{Name: instanceTypeName}.Build(),
				Spec: privatev1.BareMetalInstanceTypeSpec_builder{
					Hardware: privatev1.BareMetalHardwareSpec_builder{
						Cpu:    privatev1.BareMetalCPUSpec_builder{Cores: 32, Architecture: "x86_64", ThreadsPerCore: 2}.Build(),
						Memory: privatev1.BareMetalMemorySpec_builder{TotalGb: 128}.Build(),
						NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
							privatev1.BareMetalNetworkPortSpec_builder{
								Name: "eth0", Role: "fabric", Type: "Ethernet", Speed: "10Gbps",
							}.Build(),
						},
					}.Build(),
					HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
						MatchLabels: map[string]string{"hardware.profile": "compute"},
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = instanceTypesClient.Delete(ctx, privatev1.BareMetalInstanceTypesDeleteRequest_builder{
				Id: instanceTypeName,
			}.Build())
		})

		templateID := fmt.Sprintf("tmpl-%s", uuid.New())
		_, err = templatesClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:    templateID,
				Title: "Networking tenant isolation template",
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("tmpl-%s", uuid.New()[24:32]),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = templatesClient.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
				Id: templateID,
			}.Build())
		})

		clusterID := fmt.Sprintf("cluster-%s", uuid.New())
		_, err = clustersClient.Create(ctx, privatev1.ClustersCreateRequest_builder{
			Object: privatev1.Cluster_builder{
				Id: clusterID,
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("cluster-%s", uuid.New()[24:32]),
					Tenant: tenant,
				}.Build(),
				Spec: privatev1.ClusterSpec_builder{
					Template: privatev1.ClusterTemplateReference_builder{Id: templateID}.Build(),
					NodeSets: map[string]*privatev1.ClusterNodeSet{
						"workers": privatev1.ClusterNodeSet_builder{
							BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: instanceTypeName}.Build(),
							Size:                  new(int32(1)),
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = clustersClient.Delete(ctx, privatev1.ClustersDeleteRequest_builder{
				Id: clusterID,
			}.Build())
		})
		return clusterID
	}

	It("rejects Subnet referencing VirtualNetwork from a different tenant", func() {
		vnID := createReadyVirtualNetwork(tenantAName, "10.110.0.0/16")

		_, err := subnetsClient.Create(ctx, privatev1.SubnetsCreateRequest_builder{
			Object: privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("subnet-%s", uuid.New()[24:32]),
					Tenant: tenantBName,
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vnID}.Build(),
					Ipv4Cidr:       new("10.110.1.0/24"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err).To(MatchError(ContainSubstring("belongs to tenant")))
	})

	It("accepts Subnet referencing same-tenant VirtualNetwork", func() {
		vnID := createReadyVirtualNetwork(tenantAName, "10.111.0.0/16")

		resp, err := subnetsClient.Create(ctx, privatev1.SubnetsCreateRequest_builder{
			Object: privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("subnet-%s", uuid.New()[24:32]),
					Tenant: tenantAName,
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vnID}.Build(),
					Ipv4Cidr:       new("10.111.1.0/24"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = subnetsClient.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{
				Id: resp.GetObject().GetId(),
			}.Build())
		})
	})

	It("rejects SecurityGroup referencing VirtualNetwork from a different tenant", func() {
		vnID := createReadyVirtualNetwork(tenantAName, "10.112.0.0/16")

		_, err := securityGroupsClient.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
			Object: privatev1.SecurityGroup_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("sg-%s", uuid.New()[24:32]),
					Tenant: tenantBName,
				}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vnID}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err).To(MatchError(ContainSubstring("belongs to tenant")))
	})

	It("accepts ExternalIP referencing a shared ExternalIPPool", func() {
		poolID := createReadyPool("")
		_ = createExternalIP(tenantAName, poolID)
	})

	It("rejects ExternalIP referencing ExternalIPPool from an unrelated tenant", func() {
		poolID := createReadyPool(tenantBName)

		_, err := externalIPsClient.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{
			Object: privatev1.ExternalIP_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("eip-%s", uuid.New()[24:32]),
					Tenant: tenantAName,
				}.Build(),
				Spec: privatev1.ExternalIPSpec_builder{
					Pool: privatev1.ExternalIPPoolReference_builder{Id: poolID}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err).To(MatchError(ContainSubstring("belongs to tenant")))
	})

	It("rejects NATGateway referencing VirtualNetwork from a different tenant", func() {
		vnID := createReadyVirtualNetwork(tenantAName, "10.113.0.0/16")
		poolID := createReadyPool("")
		eipID := createExternalIP(tenantBName, poolID)
		promoteExternalIPAllocated(eipID)

		_, err := natGatewaysClient.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
			Object: privatev1.NATGateway_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("nat-%s", uuid.New()[24:32]),
					Tenant: tenantBName,
				}.Build(),
				Spec: privatev1.NATGatewaySpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vnID}.Build(),
					ExternalIp:     privatev1.ExternalIPLocalReference_builder{Id: eipID}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err).To(MatchError(ContainSubstring("belongs to tenant")))
	})

	It("rejects NATGateway referencing ExternalIP from a different tenant", func() {
		vnID := createReadyVirtualNetwork(tenantAName, "10.114.0.0/16")
		poolID := createReadyPool("")
		eipID := createExternalIP(tenantBName, poolID)
		promoteExternalIPAllocated(eipID)

		_, err := natGatewaysClient.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
			Object: privatev1.NATGateway_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("nat-%s", uuid.New()[24:32]),
					Tenant: tenantAName,
				}.Build(),
				Spec: privatev1.NATGatewaySpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vnID}.Build(),
					ExternalIp:     privatev1.ExternalIPLocalReference_builder{Id: eipID}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err).To(MatchError(ContainSubstring("belongs to tenant")))
	})

	It("rejects ExternalIPAttachment referencing ExternalIP from a different tenant", func() {
		poolID := createReadyPool("")
		eipID := createExternalIP(tenantAName, poolID)
		promoteExternalIPAllocated(eipID)
		clusterID := createCluster(tenantAName)

		_, err := attachmentsClient.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
			Object: privatev1.ExternalIPAttachment_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("att-%s", uuid.New()[24:32]),
					Tenant: tenantBName,
				}.Build(),
				Spec: privatev1.ExternalIPAttachmentSpec_builder{
					ExternalIp:     privatev1.ExternalIPLocalReference_builder{Id: eipID}.Build(),
					Cluster:        privatev1.ClusterLocalReference_builder{Id: clusterID}.Build(),
					TargetEndpoint: privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err).To(MatchError(ContainSubstring("belongs to tenant")))
	})
})
