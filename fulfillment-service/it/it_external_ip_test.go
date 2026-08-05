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
	"math/rand/v2"
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

func uniqueCIDR() string {
	return fmt.Sprintf("10.%d.%d.0/28", rand.IntN(200)+20, rand.IntN(256))
}

var _ = Describe("Private ExternalIPPool CRUD", func() {
	var (
		ctx    context.Context
		client privatev1.ExternalIPPoolsClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		client = privatev1.NewExternalIPPoolsClient(tool.InternalView().AdminConn())
	})

	It("Can create, get, and list an ExternalIPPool", func() {
		cidr := uniqueCIDR()
		id := fmt.Sprintf("test-pool-%s", uuid.New())
		response, err := client.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Id: id,
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs:    []string{cidr},
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())

		pool := response.GetObject()
		Expect(pool).ToNot(BeNil())
		Expect(pool.GetId()).To(Equal(id))
		Expect(pool.GetSpec().GetCidrs()).To(Equal([]string{cidr}))
		Expect(pool.GetSpec().GetIpFamily()).To(Equal(privatev1.IPFamily_IP_FAMILY_IPV4))
		Expect(pool.GetStatus().GetTotal()).To(BeNumerically(">", int64(0)))
		Expect(pool.GetStatus().GetAvailable()).To(Equal(pool.GetStatus().GetTotal()))
		Expect(pool.GetStatus().GetAllocated()).To(Equal(int64(0)))

		getResponse, err := client.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{
			Id: id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetId()).To(Equal(id))
		Expect(getResponse.GetObject().GetSpec().GetCidrs()).To(Equal([]string{cidr}))
		Expect(getResponse.GetObject().GetSpec().GetIpFamily()).To(Equal(privatev1.IPFamily_IP_FAMILY_IPV4))

		listResponse, err := client.List(ctx, privatev1.ExternalIPPoolsListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetItems()).ToNot(BeEmpty())

		DeferCleanup(func() {
			_, _ = client.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{
				Id: id,
			}.Build())
		})
	})

	It("Can delete an ExternalIPPool with no allocated IPs", func() {
		id := fmt.Sprintf("test-pool-%s", uuid.New())
		_, err := client.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{"test"},
				}.Build(),
				Id: id,
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs:    []string{uniqueCIDR()},
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		_, err = client.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{
			Id: id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		getResponse, err := client.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{
			Id: id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeTrue())
	})

	It("Rejects immutable field changes on update", func() {
		id := fmt.Sprintf("test-pool-%s", uuid.New())
		_, err := client.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Id: id,
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs:    []string{uniqueCIDR()},
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		_, err = client.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Id: id,
				Spec: privatev1.ExternalIPPoolSpec_builder{
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV6,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))

		_, err = client.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Id: id,
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs: []string{uniqueCIDR()},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))

		DeferCleanup(func() {
			_, _ = client.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{
				Id: id,
			}.Build())
		})
	})

	It("Rejects overlapping CIDRs", func() {
		cidr := uniqueCIDR()
		id1 := fmt.Sprintf("test-pool-%s", uuid.New())
		_, err := client.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Id: id1,
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs:    []string{cidr},
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		id2 := fmt.Sprintf("test-pool-%s", uuid.New())
		_, err = client.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Id: id2,
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs:    []string{cidr},
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.AlreadyExists))

		DeferCleanup(func() {
			_, _ = client.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{
				Id: id1,
			}.Build())
		})
	})

	It("Rejects delete when IPs are allocated", func() {
		poolId := fmt.Sprintf("test-pool-%s", uuid.New())
		_, err := client.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
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
			resp, err := client.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{
				Id: poolId,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())

		getResp, err := client.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{
			Id: poolId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		pool := getResp.GetObject()
		pool.SetStatus(privatev1.ExternalIPPoolStatus_builder{
			State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
			Total:     pool.GetStatus().GetTotal(),
			Available: pool.GetStatus().GetAvailable(),
			Allocated: pool.GetStatus().GetAllocated(),
		}.Build())
		_, err = client.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
			Object:     pool,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		externalIPsClient := publicv1.NewExternalIPsClient(tool.ExternalView().UserConn())
		ipId := fmt.Sprintf("test-ip-%s", uuid.New())
		_, err = externalIPsClient.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
			Object: publicv1.ExternalIP_builder{
				Id: ipId,
				Spec: publicv1.ExternalIPSpec_builder{
					Pool: poolId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			privateIPsClient := privatev1.NewExternalIPsClient(tool.InternalView().AdminConn())
			ipGetResp, err := privateIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
				Id: ipId,
			}.Build())
			if err != nil {
				_, _ = client.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{
					Id: poolId,
				}.Build())
				return
			}
			ip := ipGetResp.GetObject()
			ip.SetStatus(privatev1.ExternalIPStatus_builder{
				State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
			}.Build())
			_, _ = privateIPsClient.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
				Object:     ip,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
			}.Build())
			_, _ = externalIPsClient.Delete(ctx, publicv1.ExternalIPsDeleteRequest_builder{
				Id: ipId,
			}.Build())
			_, _ = client.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{
				Id: poolId,
			}.Build())
		})

		_, err = client.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{
			Id: poolId,
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
	})
})

var _ = Describe("ExternalIP lifecycle", func() {
	var (
		ctx                      context.Context
		poolsClient              privatev1.ExternalIPPoolsClient
		externalIPsClient        publicv1.ExternalIPsClient
		privateExternalIPsClient privatev1.ExternalIPsClient
		publicPoolsClient        publicv1.ExternalIPPoolsClient
		poolId                   string
	)

	BeforeEach(func() {
		ctx = context.Background()
		poolsClient = privatev1.NewExternalIPPoolsClient(tool.InternalView().AdminConn())
		externalIPsClient = publicv1.NewExternalIPsClient(tool.ExternalView().UserConn())
		privateExternalIPsClient = privatev1.NewExternalIPsClient(tool.InternalView().AdminConn())
		publicPoolsClient = publicv1.NewExternalIPPoolsClient(tool.ExternalView().UserConn())

		poolId = fmt.Sprintf("test-pool-%s", uuid.New())
		_, err := poolsClient.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
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

		getResp, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{
			Id: poolId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		pool := getResp.GetObject()
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
	})

	It("Can create, get, and list an ExternalIP", func() {
		ipId := fmt.Sprintf("test-ip-%s", uuid.New())
		response, err := externalIPsClient.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
			Object: publicv1.ExternalIP_builder{
				Id: ipId,
				Spec: publicv1.ExternalIPSpec_builder{
					Pool: poolId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())

		ip := response.GetObject()
		Expect(ip.GetId()).To(Equal(ipId))
		Expect(ip.GetSpec().GetPool()).To(Equal(poolId))
		Expect(ip.GetStatus().GetState()).To(Equal(publicv1.ExternalIPState_EXTERNAL_IP_STATE_PENDING))

		poolResp, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{
			Id: poolId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(poolResp.GetObject().GetStatus().GetAllocated()).To(Equal(int64(1)))

		getResponse, err := externalIPsClient.Get(ctx, publicv1.ExternalIPsGetRequest_builder{
			Id: ipId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetId()).To(Equal(ipId))
		Expect(getResponse.GetObject().GetSpec().GetPool()).To(Equal(poolId))

		listResponse, err := externalIPsClient.List(ctx, publicv1.ExternalIPsListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetItems()).ToNot(BeEmpty())
	})

	It("Rejects changing immutable pool field", func() {
		ipId := fmt.Sprintf("test-ip-%s", uuid.New())
		_, err := externalIPsClient.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
			Object: publicv1.ExternalIP_builder{
				Id: ipId,
				Spec: publicv1.ExternalIPSpec_builder{
					Pool: poolId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		_, err = externalIPsClient.Update(ctx, publicv1.ExternalIPsUpdateRequest_builder{
			Object: publicv1.ExternalIP_builder{
				Id: ipId,
				Spec: publicv1.ExternalIPSpec_builder{
					Pool: "different-pool",
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.pool"}},
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
	})

	It("Can delete an ExternalIP in ALLOCATED state", func() {
		ipId := fmt.Sprintf("test-ip-%s", uuid.New())
		_, err := externalIPsClient.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
			Object: publicv1.ExternalIP_builder{
				Id: ipId,
				Spec: publicv1.ExternalIPSpec_builder{
					Pool: poolId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// State promotion requires private API (admin-only)
		ipGetResp, err := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
			Id: ipId,
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

		_, err = externalIPsClient.Delete(ctx, publicv1.ExternalIPsDeleteRequest_builder{
			Id: ipId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		poolResp, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{
			Id: poolId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(poolResp.GetObject().GetStatus().GetAllocated()).To(Equal(int64(0)))
	})

	It("Rejects delete of ExternalIP not in ALLOCATED state", func() {
		ipId := fmt.Sprintf("test-ip-%s", uuid.New())
		_, err := externalIPsClient.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
			Object: publicv1.ExternalIP_builder{
				Id: ipId,
				Spec: publicv1.ExternalIPSpec_builder{
					Pool: poolId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		_, err = externalIPsClient.Delete(ctx, publicv1.ExternalIPsDeleteRequest_builder{
			Id: ipId,
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
	})

	It("Rejects create with non-existent pool", func() {
		ipId := fmt.Sprintf("test-ip-%s", uuid.New())
		_, err := externalIPsClient.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
			Object: publicv1.ExternalIP_builder{
				Id: ipId,
				Spec: publicv1.ExternalIPSpec_builder{
					Pool: "nonexistent-pool",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
	})

	It("Rejects create with non-READY pool", func() {
		nonReadyPoolId := fmt.Sprintf("test-pool-%s", uuid.New())
		_, err := poolsClient.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Id: nonReadyPoolId,
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs:    []string{uniqueCIDR()},
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		ipId := fmt.Sprintf("test-ip-%s", uuid.New())
		_, err = externalIPsClient.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
			Object: publicv1.ExternalIP_builder{
				Id: ipId,
				Spec: publicv1.ExternalIPSpec_builder{
					Pool: nonReadyPoolId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
	})

	It("Public pool visibility requires READY state and available capacity", func() {
		listResponse, err := publicPoolsClient.List(ctx, publicv1.ExternalIPPoolsListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())

		found := false
		for _, p := range listResponse.GetItems() {
			if p.GetId() == poolId {
				found = true
				break
			}
		}
		Expect(found).To(BeTrue(), "READY pool with available capacity should be visible via public API")

		getResponse, err := publicPoolsClient.Get(ctx, publicv1.ExternalIPPoolsGetRequest_builder{
			Id: poolId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetId()).To(Equal(poolId))
	})
})

var _ = Describe("ExternalIPAttachment cross-resource validation", func() {
	var (
		ctx                      context.Context
		poolsClient              privatev1.ExternalIPPoolsClient
		externalIPsClient        publicv1.ExternalIPsClient
		privateExternalIPsClient privatev1.ExternalIPsClient
		attachmentsClient        publicv1.ExternalIPAttachmentsClient
		clustersClient           publicv1.ClustersClient
		hostTypesClient          privatev1.HostTypesClient
		clusterTemplatesClient   privatev1.ClusterTemplatesClient

		poolId       string
		externalIPId string
		clusterId    string
		hostTypeId   string
		templateId   string
	)

	BeforeEach(func() {
		ctx = context.Background()
		poolsClient = privatev1.NewExternalIPPoolsClient(tool.InternalView().AdminConn())
		externalIPsClient = publicv1.NewExternalIPsClient(tool.ExternalView().UserConn())
		privateExternalIPsClient = privatev1.NewExternalIPsClient(tool.InternalView().AdminConn())
		attachmentsClient = publicv1.NewExternalIPAttachmentsClient(tool.ExternalView().UserConn())
		clustersClient = publicv1.NewClustersClient(tool.ExternalView().UserConn())
		hostTypesClient = privatev1.NewHostTypesClient(tool.InternalView().AdminConn())
		clusterTemplatesClient = privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())

		poolId = fmt.Sprintf("test-pool-%s", uuid.New())
		_, err := poolsClient.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
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

		// State promotion requires private API (admin-only)
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

		hostTypeId = fmt.Sprintf("test-ht-%s", uuid.New())
		_, err = hostTypesClient.Create(ctx, privatev1.HostTypesCreateRequest_builder{
			Object: privatev1.HostType_builder{
				Id: hostTypeId,
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		templateId = fmt.Sprintf("test-tmpl-%s", uuid.New())
		_, err = clusterTemplatesClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:    templateId,
				Title: "Test Template",
				NodeSets: map[string]*privatev1.ClusterTemplateNodeSet{
					"workers": privatev1.ClusterTemplateNodeSet_builder{
						HostType: hostTypeId,
						Size:     1,
					}.Build(),
				},
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		createClusterResp, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Spec: publicv1.ClusterSpec_builder{
					Template: templateId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		clusterId = createClusterResp.GetObject().GetId()
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
		if clusterId != "" {
			clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{
				Id: clusterId,
			}.Build())
		}
		if templateId != "" {
			clusterTemplatesClient.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
				Id: templateId,
			}.Build())
		}
		if hostTypeId != "" {
			hostTypesClient.Delete(ctx, privatev1.HostTypesDeleteRequest_builder{
				Id: hostTypeId,
			}.Build())
		}
	})

	It("Can create, get, and list an ExternalIPAttachment with cluster target", func() {
		attachmentId := fmt.Sprintf("test-att-%s", uuid.New())
		response, err := attachmentsClient.Create(ctx, publicv1.ExternalIPAttachmentsCreateRequest_builder{
			Object: publicv1.ExternalIPAttachment_builder{
				Id: attachmentId,
				Spec: publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp:     externalIPId,
					Cluster:        &clusterId,
					TargetEndpoint: publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		DeferCleanup(func() {
			attachmentsClient.Delete(ctx, publicv1.ExternalIPAttachmentsDeleteRequest_builder{
				Id: attachmentId,
			}.Build())
		})

		attachment := response.GetObject()
		Expect(attachment.GetId()).To(Equal(attachmentId))
		Expect(attachment.GetSpec().GetExternalIp()).To(Equal(externalIPId))
		Expect(attachment.GetSpec().GetCluster()).To(Equal(clusterId))
		Expect(attachment.GetStatus().GetState()).To(
			Equal(publicv1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING))

		ipResp, err := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
			Id: externalIPId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(ipResp.GetObject().GetStatus().GetAttached()).To(BeTrue())

		getResponse, err := attachmentsClient.Get(ctx, publicv1.ExternalIPAttachmentsGetRequest_builder{
			Id: attachmentId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetId()).To(Equal(attachmentId))
		Expect(getResponse.GetObject().GetSpec().GetExternalIp()).To(Equal(externalIPId))
		Expect(getResponse.GetObject().GetSpec().GetCluster()).To(Equal(clusterId))

		listResponse, err := attachmentsClient.List(ctx,
			publicv1.ExternalIPAttachmentsListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetItems()).ToNot(BeEmpty())
	})

	It("Rejects duplicate attachment for same ExternalIP", func() {
		attachmentId1 := fmt.Sprintf("test-att-%s", uuid.New())
		_, err := attachmentsClient.Create(ctx, publicv1.ExternalIPAttachmentsCreateRequest_builder{
			Object: publicv1.ExternalIPAttachment_builder{
				Id: attachmentId1,
				Spec: publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp:     externalIPId,
					Cluster:        &clusterId,
					TargetEndpoint: publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			attachmentsClient.Delete(ctx, publicv1.ExternalIPAttachmentsDeleteRequest_builder{
				Id: attachmentId1,
			}.Build())
		})

		attachmentId2 := fmt.Sprintf("test-att-%s", uuid.New())
		_, err = attachmentsClient.Create(ctx, publicv1.ExternalIPAttachmentsCreateRequest_builder{
			Object: publicv1.ExternalIPAttachment_builder{
				Id: attachmentId2,
				Spec: publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp:     externalIPId,
					Cluster:        &clusterId,
					TargetEndpoint: publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
	})

	It("Rejects attachment with non-existent ExternalIP", func() {
		attachmentId := fmt.Sprintf("test-att-%s", uuid.New())
		_, err := attachmentsClient.Create(ctx, publicv1.ExternalIPAttachmentsCreateRequest_builder{
			Object: publicv1.ExternalIPAttachment_builder{
				Id: attachmentId,
				Spec: publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp:     "nonexistent-ip",
					Cluster:        &clusterId,
					TargetEndpoint: publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
	})

	It("Rejects attachment with non-ALLOCATED ExternalIP", func() {
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

		attachmentId := fmt.Sprintf("test-att-%s", uuid.New())
		_, err = attachmentsClient.Create(ctx, publicv1.ExternalIPAttachmentsCreateRequest_builder{
			Object: publicv1.ExternalIPAttachment_builder{
				Id: attachmentId,
				Spec: publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp:     pendingIPId,
					Cluster:        &clusterId,
					TargetEndpoint: publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
	})

	It("Rejects attachment with no target", func() {
		attachmentId := fmt.Sprintf("test-att-%s", uuid.New())
		_, err := attachmentsClient.Create(ctx, publicv1.ExternalIPAttachmentsCreateRequest_builder{
			Object: publicv1.ExternalIPAttachment_builder{
				Id: attachmentId,
				Spec: publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp: externalIPId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
	})

	It("Rejects cluster target without endpoint", func() {
		attachmentId := fmt.Sprintf("test-att-%s", uuid.New())
		_, err := attachmentsClient.Create(ctx, publicv1.ExternalIPAttachmentsCreateRequest_builder{
			Object: publicv1.ExternalIPAttachment_builder{
				Id: attachmentId,
				Spec: publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp: externalIPId,
					Cluster:    &clusterId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
	})

	It("Rejects immutable fields on update", func() {
		attachmentId := fmt.Sprintf("test-att-%s", uuid.New())
		_, err := attachmentsClient.Create(ctx, publicv1.ExternalIPAttachmentsCreateRequest_builder{
			Object: publicv1.ExternalIPAttachment_builder{
				Id: attachmentId,
				Spec: publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp:     externalIPId,
					Cluster:        &clusterId,
					TargetEndpoint: publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			attachmentsClient.Delete(ctx, publicv1.ExternalIPAttachmentsDeleteRequest_builder{
				Id: attachmentId,
			}.Build())
		})

		_, err = attachmentsClient.Update(ctx, publicv1.ExternalIPAttachmentsUpdateRequest_builder{
			Object: publicv1.ExternalIPAttachment_builder{
				Id: attachmentId,
				Spec: publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp: "different-ip",
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.external_ip"}},
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
	})

	It("Deleting attachment resets ExternalIP attached flag", func() {
		attachmentId := fmt.Sprintf("test-att-%s", uuid.New())
		_, err := attachmentsClient.Create(ctx, publicv1.ExternalIPAttachmentsCreateRequest_builder{
			Object: publicv1.ExternalIPAttachment_builder{
				Id: attachmentId,
				Spec: publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp:     externalIPId,
					Cluster:        &clusterId,
					TargetEndpoint: publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		ipResp, err := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
			Id: externalIPId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(ipResp.GetObject().GetStatus().GetAttached()).To(BeTrue())

		_, err = attachmentsClient.Delete(ctx, publicv1.ExternalIPAttachmentsDeleteRequest_builder{
			Id: attachmentId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		ipResp, err = privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
			Id: externalIPId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(ipResp.GetObject().GetStatus().GetAttached()).To(BeFalse())
	})
})
