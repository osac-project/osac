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
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("NATGateway lifecycle", func() {
	var (
		ctx context.Context

		natGatewaysClient        publicv1.NATGatewaysClient
		privateNATGatewaysClient privatev1.NATGatewaysClient
		privateExternalIPsClient privatev1.ExternalIPsClient
		externalIPsClient        publicv1.ExternalIPsClient
		poolsClient              privatev1.ExternalIPPoolsClient
		virtualNetworksClient    privatev1.VirtualNetworksClient
		networkClassesClient     privatev1.NetworkClassesClient

		networkClassId    string
		networkClassHubID string
		virtualNetworkId  string
		poolId            string
		externalIPId      string
		natGatewayIDs     []string
		networkClassIDs   []string
		virtualNetworkIDs []string
		poolIDs           []string
		externalIPIDs     []string
	)

	BeforeEach(func() {
		ctx = context.Background()
		natGatewayIDs = nil
		networkClassIDs = nil
		virtualNetworkIDs = nil
		poolIDs = nil
		externalIPIDs = nil

		natGatewaysClient = publicv1.NewNATGatewaysClient(tool.ExternalView().UserConn())
		privateNATGatewaysClient = privatev1.NewNATGatewaysClient(tool.InternalView().AdminConn())
		privateExternalIPsClient = privatev1.NewExternalIPsClient(tool.InternalView().AdminConn())
		externalIPsClient = publicv1.NewExternalIPsClient(tool.ExternalView().UserConn())
		poolsClient = privatev1.NewExternalIPPoolsClient(tool.InternalView().AdminConn())
		virtualNetworksClient = privatev1.NewVirtualNetworksClient(tool.InternalView().AdminConn())
		networkClassesClient = privatev1.NewNetworkClassesClient(tool.InternalView().AdminConn())

		// Create NetworkClass
		ncName := fmt.Sprintf("cudn-natgw-%s", uuid.New())
		ncResp, err := networkClassesClient.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
			Object: privatev1.NetworkClass_builder{
				Metadata:      privatev1.Metadata_builder{Name: ncName}.Build(),
				Title:         "Test CUDN Network Class",
				FabricManager: new("netris"),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		networkClassId = ncResp.GetObject().GetId()
		networkClassIDs = append(networkClassIDs, networkClassId)
		waitForNetworkClassReady(ctx, networkClassesClient, networkClassId)
		setNetworkClassCanonicalHub(ctx, networkClassesClient, networkClassId, hubId)
		expectNetworkClassStatus(
			ctx,
			networkClassesClient,
			networkClassId,
			privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			hubId,
			"",
		)
		networkClassResp, err := networkClassesClient.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
			Id: networkClassId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		networkClassHubID = networkClassResp.GetObject().GetStatus().GetHub()
		Expect(networkClassHubID).ToNot(BeEmpty())

		// Create VirtualNetwork
		virtualNetworkId = fmt.Sprintf("test-vnet-%s", uuid.New())
		_, err = virtualNetworksClient.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Id: virtualNetworkId,
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-vnet-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					NetworkClass: privatev1.NetworkClassReference_builder{Id: networkClassId}.Build(),
					Region:       "us-east-1",
					Ipv4Cidr:     new("10.100.0.0/16"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		virtualNetworkIDs = append(virtualNetworkIDs, virtualNetworkId)

		Eventually(func(g Gomega) {
			resp, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{
				Id: virtualNetworkId,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))
			g.Expect(resp.GetObject().GetStatus().GetHub()).To(Equal(networkClassHubID))
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
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-pool-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs:    []string{uniqueCIDR()},
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		poolIDs = append(poolIDs, poolId)

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
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-ip-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ExternalIPSpec_builder{
					Pool: publicv1.ExternalIPPoolReference_builder{Id: poolId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		externalIPIDs = append(externalIPIDs, externalIPId)
		Eventually(func(g Gomega) {
			resp, err := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
				Id: externalIPId,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetHub()).To(Equal(networkClassHubID))
		}, time.Minute, time.Second).Should(Succeed())

		// Promote ExternalIP to ALLOCATED
		ipGetResp, err := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
			Id: externalIPId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		ip := ipGetResp.GetObject()
		ip.SetStatus(privatev1.ExternalIPStatus_builder{
			State:               privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
			StateTransitionTime: timestamppb.New(time.Now()),
		}.Build())
		_, err = privateExternalIPsClient.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
			Object: ip,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
				"status.state", "status.state_transition_time",
			}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		for _, id := range natGatewayIDs {
			cleanupErr := cleanupNetworkFixtureResource(ctx,
				func(cleanupCtx context.Context) error {
					_, err := natGatewaysClient.Delete(cleanupCtx, publicv1.NATGatewaysDeleteRequest_builder{Id: id}.Build())
					return err
				},
				func(ctx context.Context) (*privatev1.Metadata, error) {
					response, err := privateNATGatewaysClient.Get(ctx, privatev1.NATGatewaysGetRequest_builder{Id: id}.Build())
					if err != nil {
						return nil, err
					}
					return response.GetObject().GetMetadata(), nil
				},
				func(ctx context.Context) error {
					response, err := privateNATGatewaysClient.Get(ctx, privatev1.NATGatewaysGetRequest_builder{Id: id}.Build())
					if err != nil {
						return err
					}
					gateway := response.GetObject()
					gateway.GetMetadata().SetFinalizers(nil)
					_, err = privateNATGatewaysClient.Update(ctx, privatev1.NATGatewaysUpdateRequest_builder{
						Object: gateway,
						UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
							"metadata.finalizers",
						}},
						Lock: true,
					}.Build())
					return err
				},
			)
			Expect(cleanupErr).To(Succeed())
		}
		for _, id := range externalIPIDs {
			cleanupErr := cleanupNetworkFixtureResource(ctx,
				func(cleanupCtx context.Context) error {
					_, err := privateExternalIPsClient.Get(cleanupCtx, privatev1.ExternalIPsGetRequest_builder{Id: id}.Build())
					if grpcstatus.Code(err) == grpccodes.NotFound {
						return nil
					}
					if err != nil {
						return err
					}
					if err := setExternalIPFixtureState(cleanupCtx, privateExternalIPsClient, id,
						privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED); err != nil {
						return err
					}
					_, err = externalIPsClient.Delete(cleanupCtx, publicv1.ExternalIPsDeleteRequest_builder{Id: id}.Build())
					return err
				},
				func(cleanupCtx context.Context) (*privatev1.Metadata, error) {
					response, err := privateExternalIPsClient.Get(cleanupCtx, privatev1.ExternalIPsGetRequest_builder{Id: id}.Build())
					if err != nil {
						return nil, err
					}
					return response.GetObject().GetMetadata(), nil
				},
				func(cleanupCtx context.Context) error {
					response, err := privateExternalIPsClient.Get(cleanupCtx, privatev1.ExternalIPsGetRequest_builder{Id: id}.Build())
					if err != nil {
						return err
					}
					ip := response.GetObject()
					ip.GetMetadata().SetFinalizers(nil)
					_, err = privateExternalIPsClient.Update(cleanupCtx, privatev1.ExternalIPsUpdateRequest_builder{
						Object: ip,
						UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
							"metadata.finalizers",
						}},
						Lock: true,
					}.Build())
					return err
				},
			)
			Expect(cleanupErr).To(Succeed())
		}
		for _, id := range poolIDs {
			cleanupErr := cleanupNetworkFixtureResource(ctx,
				func(cleanupCtx context.Context) error {
					_, err := poolsClient.Delete(cleanupCtx, privatev1.ExternalIPPoolsDeleteRequest_builder{Id: id}.Build())
					return err
				},
				func(cleanupCtx context.Context) (*privatev1.Metadata, error) {
					response, err := poolsClient.Get(cleanupCtx, privatev1.ExternalIPPoolsGetRequest_builder{Id: id}.Build())
					if err != nil {
						return nil, err
					}
					return response.GetObject().GetMetadata(), nil
				},
				func(cleanupCtx context.Context) error {
					response, err := poolsClient.Get(cleanupCtx, privatev1.ExternalIPPoolsGetRequest_builder{Id: id}.Build())
					if err != nil {
						return err
					}
					pool := response.GetObject()
					pool.GetMetadata().SetFinalizers(nil)
					_, err = poolsClient.Update(cleanupCtx, privatev1.ExternalIPPoolsUpdateRequest_builder{
						Object: pool,
						UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
							"metadata.finalizers",
						}},
						Lock: true,
					}.Build())
					return err
				},
			)
			Expect(cleanupErr).To(Succeed())
		}
		for _, id := range virtualNetworkIDs {
			Expect(cleanupVirtualNetworkFixture(ctx, virtualNetworksClient, id)).To(Succeed())
			waitForNotFound(func() error {
				_, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
				return err
			})
		}
		for _, id := range networkClassIDs {
			Expect(cleanupNetworkClassFixture(ctx, networkClassesClient, id)).To(Succeed())
			waitForNotFound(func() error {
				_, err := networkClassesClient.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: id}.Build())
				return err
			})
		}
	})

	It("Can create, get, and list a NATGateway", func() {
		ngId := fmt.Sprintf("test-ng-%s", uuid.New())
		response, err := natGatewaysClient.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{
			Object: publicv1.NATGateway_builder{
				Id: ngId,
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-ng-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.NATGatewaySpec_builder{
					VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkId}.Build(),
					ExternalIp:     publicv1.ExternalIPLocalReference_builder{Id: externalIPId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		natGatewayIDs = append(natGatewayIDs, ngId)

		ng := response.GetObject()
		Expect(ng.GetId()).To(Equal(ngId))
		Expect(ng.GetSpec().GetVirtualNetwork().GetId()).To(Equal(virtualNetworkId))
		Expect(ng.GetSpec().GetExternalIp().GetId()).To(Equal(externalIPId))
		Expect(ng.GetStatus().GetState()).To(Equal(publicv1.NATGatewayState_NAT_GATEWAY_STATE_PENDING))

		// NATGateway does not populate ExternalIPAttachment output fields.
		ipResp, err := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
			Id: externalIPId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(ipResp.GetObject().GetStatus().GetAttached()).To(BeFalse())

		// Get
		getResponse, err := natGatewaysClient.Get(ctx, publicv1.NATGatewaysGetRequest_builder{
			Id: ngId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetId()).To(Equal(ngId))
		Expect(getResponse.GetObject().GetSpec().GetVirtualNetwork().GetId()).To(Equal(virtualNetworkId))
		Expect(getResponse.GetObject().GetSpec().GetExternalIp().GetId()).To(Equal(externalIPId))

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
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-ng-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.NATGatewaySpec_builder{
					VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkId}.Build(),
					ExternalIp:     publicv1.ExternalIPLocalReference_builder{Name: "nonexistent-ip"}.Build(),
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
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-ip-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ExternalIPSpec_builder{
					Pool: publicv1.ExternalIPPoolReference_builder{Id: poolId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		expectNetworkingResourceHub(ctx, networkClassHubID, func(getCtx context.Context) (string, error) {
			resp, getErr := privateExternalIPsClient.Get(getCtx, privatev1.ExternalIPsGetRequest_builder{
				Id: pendingIPId,
			}.Build())
			if getErr != nil {
				return "", getErr
			}
			return resp.GetObject().GetStatus().GetHub(), nil
		})
		externalIPIDs = append(externalIPIDs, pendingIPId)

		ngId := fmt.Sprintf("test-ng-%s", uuid.New())
		_, err = natGatewaysClient.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{
			Object: publicv1.NATGateway_builder{
				Id: ngId,
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-ng-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.NATGatewaySpec_builder{
					VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkId}.Build(),
					ExternalIp:     publicv1.ExternalIPLocalReference_builder{Id: pendingIPId}.Build(),
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
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-ng-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.NATGatewaySpec_builder{
					VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkId}.Build(),
					ExternalIp:     publicv1.ExternalIPLocalReference_builder{Id: externalIPId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		natGatewayIDs = append(natGatewayIDs, ngId1)

		// Create a second VirtualNetwork for the second NATGateway attempt
		vn2Id := fmt.Sprintf("test-vnet-%s", uuid.New())
		_, err = virtualNetworksClient.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Id: vn2Id,
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-vnet-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					NetworkClass: privatev1.NetworkClassReference_builder{Id: networkClassId}.Build(),
					Region:       "us-east-1",
					Ipv4Cidr:     new("10.200.0.0/16"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		virtualNetworkIDs = append(virtualNetworkIDs, vn2Id)

		ngId2 := fmt.Sprintf("test-ng-%s", uuid.New())
		_, err = natGatewaysClient.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{
			Object: publicv1.NATGateway_builder{
				Id: ngId2,
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-ng-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.NATGatewaySpec_builder{
					VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: vn2Id}.Build(),
					ExternalIp:     publicv1.ExternalIPLocalReference_builder{Id: externalIPId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
	})

	It("Rejects create when the VirtualNetwork's NetworkClass has no fabric_manager", func() {
		// Only one NetworkClass may exist per deployment (OSAC-4073), so the fabric-manager
		// NetworkClass and VirtualNetwork created by the outer BeforeEach must be torn down
		// before creating the k8s-only NetworkClass this test needs.
		Expect(cleanupVirtualNetworkFixture(ctx, virtualNetworksClient, virtualNetworkId)).To(Succeed())
		waitForNotFound(func() error {
			_, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: virtualNetworkId}.Build())
			return err
		})
		Expect(cleanupNetworkClassFixture(ctx, networkClassesClient, networkClassId)).To(Succeed())
		waitForNotFound(func() error {
			_, err := networkClassesClient.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: networkClassId}.Build())
			return err
		})

		// Create a k8s-only NetworkClass (no fabric_manager):
		k8sOnlyNC, err := networkClassesClient.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
			Object: privatev1.NetworkClass_builder{
				Title:      "Test k8s-only Network Class",
				K8SManager: new("cudn_localnet"),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		networkClassIDs = append(networkClassIDs, k8sOnlyNC.GetObject().GetId())
		waitForNetworkClassReady(ctx, networkClassesClient, k8sOnlyNC.GetObject().GetId())

		k8sOnlyVNId := fmt.Sprintf("test-vnet-%s", uuid.New())
		_, err = virtualNetworksClient.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Id: k8sOnlyVNId,
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-vnet-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					NetworkClass: privatev1.NetworkClassReference_builder{Id: k8sOnlyNC.GetObject().GetId()}.Build(),
					Region:       "us-east-1",
					Ipv4Cidr:     new("10.150.0.0/16"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		virtualNetworkIDs = append(virtualNetworkIDs, k8sOnlyVNId)

		ngId := fmt.Sprintf("test-ng-%s", uuid.New())
		_, err = natGatewaysClient.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{
			Object: publicv1.NATGateway_builder{
				Id: ngId,
				Spec: publicv1.NATGatewaySpec_builder{
					VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: k8sOnlyVNId}.Build(),
					ExternalIp:     publicv1.ExternalIPLocalReference_builder{Id: externalIPId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
	})

	It("Deleting NATGateway leaves ExternalIP attachment output unset", func() {
		ngId := fmt.Sprintf("test-ng-%s", uuid.New())
		_, err := natGatewaysClient.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{
			Object: publicv1.NATGateway_builder{
				Id: ngId,
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-ng-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.NATGatewaySpec_builder{
					VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkId}.Build(),
					ExternalIp:     publicv1.ExternalIPLocalReference_builder{Id: externalIPId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		natGatewayIDs = append(natGatewayIDs, ngId)

		ipResp, err := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
			Id: externalIPId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(ipResp.GetObject().GetStatus().GetAttached()).To(BeFalse())

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

})

func deleteAndWaitForNotFound(deleteResource, getResource func() error) {
	deleteAndWaitForNotFoundWithTimeout(deleteResource, getResource, time.Minute)
}

func deleteAndWaitForNotFoundWithTimeout(deleteResource, getResource func() error, timeout time.Duration) {
	GinkgoHelper()
	err := deleteResource()
	Expect(err == nil || grpcstatus.Code(err) == grpccodes.NotFound).To(BeTrue(), "delete failed: %v", err)
	waitForNotFoundWithTimeout(getResource, timeout)
}

func waitForNotFound(getResource func() error) {
	waitForNotFoundWithTimeout(getResource, time.Minute)
}

func waitForNotFoundWithTimeout(getResource func() error, timeout time.Duration) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		err := getResource()
		g.Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound), "expected resource to be deleted, got: %v", err)
	}, timeout, time.Second).Should(Succeed())
}
