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

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/uuid"
)

var _ = Describe("NATGateway lifecycle", func() {
	var (
		ctx context.Context

		natGatewaysClient        publicv1.NATGatewaysClient
		privateExternalIPsClient privatev1.ExternalIPsClient
		externalIPsClient        publicv1.ExternalIPsClient
		poolsClient              privatev1.ExternalIPPoolsClient
		virtualNetworksClient    privatev1.VirtualNetworksClient
		networkClassesClient     privatev1.NetworkClassesClient

		networkClassId   string
		virtualNetworkId string
		poolId           string
		externalIPId     string
	)

	BeforeEach(func() {
		ctx = context.Background()

		natGatewaysClient = publicv1.NewNATGatewaysClient(tool.ExternalView().UserConn())
		privateExternalIPsClient = privatev1.NewExternalIPsClient(tool.InternalView().AdminConn())
		externalIPsClient = publicv1.NewExternalIPsClient(tool.ExternalView().UserConn())
		poolsClient = privatev1.NewExternalIPPoolsClient(tool.InternalView().AdminConn())
		virtualNetworksClient = privatev1.NewVirtualNetworksClient(tool.InternalView().AdminConn())
		networkClassesClient = privatev1.NewNetworkClassesClient(tool.InternalView().AdminConn())

		// Create NetworkClass
		ncResp, err := networkClassesClient.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
			Object: privatev1.NetworkClass_builder{
				Title:                  "Test CUDN Network Class",
				ImplementationStrategy: "cudn",
				FabricManager:          "netris",
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		networkClassId = ncResp.GetObject().GetId()

		// Create VirtualNetwork
		virtualNetworkId = fmt.Sprintf("test-vnet-%s", uuid.New())
		_, err = virtualNetworksClient.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Id: virtualNetworkId,
				Spec: privatev1.VirtualNetworkSpec_builder{
					NetworkClass: networkClassId,
					Region:       "us-east-1",
					Ipv4Cidr:     new("10.100.0.0/16"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		Eventually(func(g Gomega) {
			resp, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{
				Id: virtualNetworkId,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())

		vnGetResp, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{
			Id: virtualNetworkId,
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

		// Create ExternalIPPool
		poolId = fmt.Sprintf("test-pool-%s", uuid.New())
		_, err = poolsClient.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Id: poolId,
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs:    []string{uniqueCIDR()},
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		Eventually(func(g Gomega) {
			resp, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{
				Id: poolId,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())

		poolGetResp, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{
			Id: poolId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		pool := poolGetResp.GetObject()
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

		// Create ExternalIP
		externalIPId = fmt.Sprintf("test-ip-%s", uuid.New())
		_, err = externalIPsClient.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
			Object: publicv1.ExternalIP_builder{
				Id: externalIPId,
				Spec: publicv1.ExternalIPSpec_builder{
					Pool: poolId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Promote ExternalIP to ALLOCATED
		ipGetResp, err := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
			Id: externalIPId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		ip := ipGetResp.GetObject()
		ip.SetStatus(privatev1.ExternalIPStatus_builder{
			State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
		}.Build())
		_, err = privateExternalIPsClient.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
			Object:     ip,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		if externalIPId != "" {
			_, _ = externalIPsClient.Delete(ctx, publicv1.ExternalIPsDeleteRequest_builder{
				Id: externalIPId,
			}.Build())
		}
		if poolId != "" {
			_, _ = poolsClient.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{
				Id: poolId,
			}.Build())
		}
		if virtualNetworkId != "" {
			_, _ = virtualNetworksClient.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{
				Id: virtualNetworkId,
			}.Build())
		}
		if networkClassId != "" {
			_, _ = networkClassesClient.Delete(ctx, privatev1.NetworkClassesDeleteRequest_builder{
				Id: networkClassId,
			}.Build())
		}
	})

	It("Can create, get, and list a NATGateway", func() {
		ngId := fmt.Sprintf("test-ng-%s", uuid.New())
		response, err := natGatewaysClient.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{
			Object: publicv1.NATGateway_builder{
				Id: ngId,
				Spec: publicv1.NATGatewaySpec_builder{
					VirtualNetwork: virtualNetworkId,
					ExternalIp:     externalIPId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		DeferCleanup(func() {
			natGatewaysClient.Delete(ctx, publicv1.NATGatewaysDeleteRequest_builder{
				Id: ngId,
			}.Build())
		})

		ng := response.GetObject()
		Expect(ng.GetId()).To(Equal(ngId))
		Expect(ng.GetSpec().GetVirtualNetwork()).To(Equal(virtualNetworkId))
		Expect(ng.GetSpec().GetExternalIp()).To(Equal(externalIPId))
		Expect(ng.GetStatus().GetState()).To(Equal(publicv1.NATGatewayState_NAT_GATEWAY_STATE_PENDING))

		// Verify ExternalIP attached flag is set
		ipResp, err := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
			Id: externalIPId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(ipResp.GetObject().GetStatus().GetAttached()).To(BeTrue())

		// Get
		getResponse, err := natGatewaysClient.Get(ctx, publicv1.NATGatewaysGetRequest_builder{
			Id: ngId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetId()).To(Equal(ngId))
		Expect(getResponse.GetObject().GetSpec().GetVirtualNetwork()).To(Equal(virtualNetworkId))
		Expect(getResponse.GetObject().GetSpec().GetExternalIp()).To(Equal(externalIPId))

		// List
		listResponse, err := natGatewaysClient.List(ctx, publicv1.NATGatewaysListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetItems()).ToNot(BeEmpty())
	})

	It("Rejects create with non-existent ExternalIP", func() {
		ngId := fmt.Sprintf("test-ng-%s", uuid.New())
		_, err := natGatewaysClient.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{
			Object: publicv1.NATGateway_builder{
				Id: ngId,
				Spec: publicv1.NATGatewaySpec_builder{
					VirtualNetwork: virtualNetworkId,
					ExternalIp:     "nonexistent-ip",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
	})

	It("Rejects create with non-ALLOCATED ExternalIP", func() {
		pendingIPId := fmt.Sprintf("test-ip-%s", uuid.New())
		_, err := externalIPsClient.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
			Object: publicv1.ExternalIP_builder{
				Id: pendingIPId,
				Spec: publicv1.ExternalIPSpec_builder{
					Pool: poolId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			// Promote to ALLOCATED so delete succeeds
			ipGetResp, err := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
				Id: pendingIPId,
			}.Build())
			if err == nil {
				ip := ipGetResp.GetObject()
				ip.SetStatus(privatev1.ExternalIPStatus_builder{
					State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
				}.Build())
				_, _ = privateExternalIPsClient.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
					Object:     ip,
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
				}.Build())
			}
			_, _ = externalIPsClient.Delete(ctx, publicv1.ExternalIPsDeleteRequest_builder{
				Id: pendingIPId,
			}.Build())
		})

		ngId := fmt.Sprintf("test-ng-%s", uuid.New())
		_, err = natGatewaysClient.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{
			Object: publicv1.NATGateway_builder{
				Id: ngId,
				Spec: publicv1.NATGatewaySpec_builder{
					VirtualNetwork: virtualNetworkId,
					ExternalIp:     pendingIPId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
	})

	It("Rejects create with already-attached ExternalIP", func() {
		ngId1 := fmt.Sprintf("test-ng-%s", uuid.New())
		_, err := natGatewaysClient.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{
			Object: publicv1.NATGateway_builder{
				Id: ngId1,
				Spec: publicv1.NATGatewaySpec_builder{
					VirtualNetwork: virtualNetworkId,
					ExternalIp:     externalIPId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			natGatewaysClient.Delete(ctx, publicv1.NATGatewaysDeleteRequest_builder{
				Id: ngId1,
			}.Build())
		})

		// Create a second VirtualNetwork for the second NATGateway attempt
		vn2Id := fmt.Sprintf("test-vnet-%s", uuid.New())
		_, err = virtualNetworksClient.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Id: vn2Id,
				Spec: privatev1.VirtualNetworkSpec_builder{
					NetworkClass: networkClassId,
					Region:       "us-east-1",
					Ipv4Cidr:     new("10.200.0.0/16"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			virtualNetworksClient.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{
				Id: vn2Id,
			}.Build())
		})

		ngId2 := fmt.Sprintf("test-ng-%s", uuid.New())
		_, err = natGatewaysClient.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{
			Object: publicv1.NATGateway_builder{
				Id: ngId2,
				Spec: publicv1.NATGatewaySpec_builder{
					VirtualNetwork: vn2Id,
					ExternalIp:     externalIPId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
	})

	It("Rejects update of immutable spec.virtual_network", func() {
		ngId := fmt.Sprintf("test-ng-%s", uuid.New())
		_, err := natGatewaysClient.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{
			Object: publicv1.NATGateway_builder{
				Id: ngId,
				Spec: publicv1.NATGatewaySpec_builder{
					VirtualNetwork: virtualNetworkId,
					ExternalIp:     externalIPId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			natGatewaysClient.Delete(ctx, publicv1.NATGatewaysDeleteRequest_builder{
				Id: ngId,
			}.Build())
		})

		_, err = natGatewaysClient.Update(ctx, publicv1.NATGatewaysUpdateRequest_builder{
			Object: publicv1.NATGateway_builder{
				Id: ngId,
				Spec: publicv1.NATGatewaySpec_builder{
					VirtualNetwork: "different-vnet",
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.virtual_network"}},
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
	})

	It("Rejects update of immutable spec.external_ip", func() {
		ngId := fmt.Sprintf("test-ng-%s", uuid.New())
		_, err := natGatewaysClient.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{
			Object: publicv1.NATGateway_builder{
				Id: ngId,
				Spec: publicv1.NATGatewaySpec_builder{
					VirtualNetwork: virtualNetworkId,
					ExternalIp:     externalIPId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			natGatewaysClient.Delete(ctx, publicv1.NATGatewaysDeleteRequest_builder{
				Id: ngId,
			}.Build())
		})

		_, err = natGatewaysClient.Update(ctx, publicv1.NATGatewaysUpdateRequest_builder{
			Object: publicv1.NATGateway_builder{
				Id: ngId,
				Spec: publicv1.NATGatewaySpec_builder{
					ExternalIp: "different-ip",
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.external_ip"}},
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
	})

	It("Deleting NATGateway resets ExternalIP attached flag", func() {
		ngId := fmt.Sprintf("test-ng-%s", uuid.New())
		_, err := natGatewaysClient.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{
			Object: publicv1.NATGateway_builder{
				Id: ngId,
				Spec: publicv1.NATGatewaySpec_builder{
					VirtualNetwork: virtualNetworkId,
					ExternalIp:     externalIPId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		ipResp, err := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
			Id: externalIPId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(ipResp.GetObject().GetStatus().GetAttached()).To(BeTrue())

		_, err = natGatewaysClient.Delete(ctx, publicv1.NATGatewaysDeleteRequest_builder{
			Id: ngId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		ipResp, err = privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
			Id: externalIPId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(ipResp.GetObject().GetStatus().GetAttached()).To(BeFalse())
	})

	It("Can update metadata labels", func() {
		ngId := fmt.Sprintf("test-ng-%s", uuid.New())
		_, err := natGatewaysClient.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{
			Object: publicv1.NATGateway_builder{
				Id: ngId,
				Spec: publicv1.NATGatewaySpec_builder{
					VirtualNetwork: virtualNetworkId,
					ExternalIp:     externalIPId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			natGatewaysClient.Delete(ctx, publicv1.NATGatewaysDeleteRequest_builder{
				Id: ngId,
			}.Build())
		})

		_, err = natGatewaysClient.Update(ctx, publicv1.NATGatewaysUpdateRequest_builder{
			Object: publicv1.NATGateway_builder{
				Id: ngId,
				Metadata: publicv1.Metadata_builder{
					Labels: map[string]string{"env": "test"},
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		getResp, err := natGatewaysClient.Get(ctx, publicv1.NATGatewaysGetRequest_builder{
			Id: ngId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResp.GetObject().GetMetadata().GetLabels()).To(HaveKeyWithValue("env", "test"))
	})
})
