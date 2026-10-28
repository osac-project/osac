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
	"slices"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// cidrCounter and cidrOffset provide a process-wide sequence with a different starting
// subnet on each run, avoiding pool CIDR collisions within and across repeated test runs.
var cidrCounter atomic.Int64
var cidrOffset = time.Now().UnixNano() % (200 * 256)

const externalIPTestFinalizer = "integration-test-external-ip-capacity"

func setExternalIPTestFinalizer(
	ctx context.Context,
	client privatev1.ExternalIPsClient,
	id string,
	add bool,
) {
	GinkgoHelper()
	if add {
		Eventually(func(g Gomega) {
			response, err := client.Get(ctx, privatev1.ExternalIPsGetRequest_builder{Id: id}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(response.GetObject().GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
		}, time.Minute, 100*time.Millisecond).Should(Succeed())
	}
	timeout := 10 * time.Second
	if !add {
		timeout = 2 * time.Minute
	}
	Eventually(func(g Gomega) bool {
		response, err := client.Get(ctx, privatev1.ExternalIPsGetRequest_builder{Id: id}.Build())
		if !add && grpcstatus.Code(err) == grpccodes.NotFound {
			return true
		}
		g.Expect(err).ToNot(HaveOccurred())
		object := response.GetObject()
		finalizers := slices.DeleteFunc(object.GetMetadata().GetFinalizers(), func(value string) bool {
			return value == externalIPTestFinalizer
		})
		if add {
			finalizers = append(finalizers, externalIPTestFinalizer)
		}
		object.GetMetadata().SetFinalizers(finalizers)
		_, err = client.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
			Object:     object,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
			Lock:       true,
		}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		if add {
			return true
		}
		_, err = client.Signal(ctx, privatev1.ExternalIPsSignalRequest_builder{Id: id}.Build())
		if grpcstatus.Code(err) == grpccodes.NotFound {
			return true
		}
		g.Expect(err).ToNot(HaveOccurred())
		return false
	}, timeout, 100*time.Millisecond).Should(BeTrue())
}

func deleteAndWaitForExternalIPFixture(
	ctx context.Context,
	deleteResource func(context.Context) error,
	getResource func(context.Context) error,
	signalResource func(context.Context) error,
) {
	GinkgoHelper()
	deleteCtx, cancel := context.WithTimeout(ctx, computeInstanceFixtureProbeTimeout)
	err := deleteResource(deleteCtx)
	cancel()
	if !expectFixtureDelete(err) || err != nil {
		return
	}

	Eventually(func(g Gomega) bool {
		probeCtx, cancel := context.WithTimeout(ctx, computeInstanceFixtureProbeTimeout)
		defer cancel()
		err := getResource(probeCtx)
		if grpcstatus.Code(err) == grpccodes.NotFound {
			return true
		}
		g.Expect(err).ToNot(HaveOccurred())
		err = signalResource(probeCtx)
		if grpcstatus.Code(err) == grpccodes.NotFound {
			return true
		}
		g.Expect(err).ToNot(HaveOccurred())
		return false
	}, time.Minute, time.Second).Should(BeTrue())
}

func deleteExternalIPFixtureAndWait(
	ctx context.Context,
	publicClient publicv1.ExternalIPsClient,
	privateClient privatev1.ExternalIPsClient,
	id string,
) {
	GinkgoHelper()
	deleteAndWaitForExternalIPFixture(
		ctx,
		func(deleteCtx context.Context) error {
			_, err := publicClient.Delete(deleteCtx, publicv1.ExternalIPsDeleteRequest_builder{Id: id}.Build())
			return err
		},
		func(getCtx context.Context) error {
			_, err := privateClient.Get(getCtx, privatev1.ExternalIPsGetRequest_builder{Id: id}.Build())
			return err
		},
		func(signalCtx context.Context) error {
			_, err := privateClient.Signal(signalCtx, privatev1.ExternalIPsSignalRequest_builder{Id: id}.Build())
			return err
		},
	)
}

func deleteExternalIPAttachmentFixtureAndWait(
	ctx context.Context,
	publicClient publicv1.ExternalIPAttachmentsClient,
	privateClient privatev1.ExternalIPAttachmentsClient,
	id string,
) {
	GinkgoHelper()
	deleteAndWaitForExternalIPFixture(
		ctx,
		func(deleteCtx context.Context) error {
			_, err := publicClient.Delete(deleteCtx, publicv1.ExternalIPAttachmentsDeleteRequest_builder{Id: id}.Build())
			return err
		},
		func(getCtx context.Context) error {
			_, err := privateClient.Get(getCtx, privatev1.ExternalIPAttachmentsGetRequest_builder{Id: id}.Build())
			return err
		},
		func(signalCtx context.Context) error {
			_, err := privateClient.Signal(signalCtx, privatev1.ExternalIPAttachmentsSignalRequest_builder{Id: id}.Build())
			return err
		},
	)
}

func uniqueCIDR() string {
	subnet := (cidrOffset + cidrCounter.Add(1) - 1) % (200 * 256)
	return fmt.Sprintf("10.%d.%d.0/28", 20+subnet/256, subnet%256)
}

func createReadyExternalIPNetworkClass(ctx context.Context, client privatev1.NetworkClassesClient) string {
	id := createDefaultNetworkClass(
		ctx,
		client,
		"test-external-ip-nc",
		"External IP Test Network Class",
		"10.230.0.0/16",
		"10.230.0.0/20",
	)
	DeferCleanup(func(cleanupCtx context.Context) {
		Expect(cleanupDefaultNetworkingForAllTenants(cleanupCtx, id)).To(Succeed())
		Expect(cleanupNetworkClassFixture(cleanupCtx, client, id)).To(Succeed())
	})

	Eventually(func(g Gomega) {
		response, err := client.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: id}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(response.GetObject().GetStatus().GetHub()).ToNot(BeEmpty())
		g.Expect(response.GetObject().GetStatus().GetState()).To(
			Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY))
	}, time.Minute, time.Second).Should(Succeed())
	setNetworkClassCanonicalHub(ctx, client, id, hubId)
	expectNetworkClassStatus(
		ctx,
		client,
		id,
		privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
		hubId,
		"",
	)
	return id
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
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-pool-%s", uuid.New()[24:32]),
				}.Build(),
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
					Name:       fmt.Sprintf("test-pool-%s", uuid.New()[24:32]),
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
		response, err := client.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Id: id,
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
		name := response.GetObject().GetMetadata().GetName()

		_, err = client.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Id: id,
				Metadata: privatev1.Metadata_builder{
					Name: name,
				}.Build(),
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
				Metadata: privatev1.Metadata_builder{
					Name: name,
				}.Build(),
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
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-pool-%s", uuid.New()[24:32]),
				}.Build(),
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
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-pool-%s", uuid.New()[24:32]),
				}.Build(),
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
		networkClassesClient := privatev1.NewNetworkClassesClient(tool.InternalView().AdminConn())
		createReadyExternalIPNetworkClass(ctx, networkClassesClient)

		poolId := fmt.Sprintf("test-pool-%s", uuid.New())
		ipId := fmt.Sprintf("test-ip-%s", uuid.New())
		externalIPsClient := publicv1.NewExternalIPsClient(tool.ExternalView().UserConn())
		privateIPsClient := privatev1.NewExternalIPsClient(tool.InternalView().AdminConn())
		_, err := client.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Id: poolId,
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-pool-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs:    []string{uniqueCIDR()},
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
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
				State:               privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
				StateTransitionTime: timestamppb.New(time.Now()),
			}.Build())
			_, _ = privateIPsClient.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
				Object: ip,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
					"status.state", "status.state_transition_time",
				}},
			}.Build())
			_, _ = externalIPsClient.Delete(ctx, publicv1.ExternalIPsDeleteRequest_builder{
				Id: ipId,
			}.Build())
			_, _ = client.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{
				Id: poolId,
			}.Build())
		})

		Eventually(func(g Gomega) {
			resp, err := client.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{
				Id: poolId,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_PENDING))
			g.Expect(resp.GetObject().GetStatus().GetHub()).To(Equal(hubId))
		}, time.Minute, time.Second).Should(Succeed())

		getResp, err := client.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{
			Id: poolId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		pool := getResp.GetObject()
		pool.GetStatus().SetState(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY)
		pool.GetStatus().SetHub(hubId)
		_, err = client.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
			Object:     pool,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state", "status.hub"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Eventually(func(g Gomega) {
			callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			resp, err := client.Get(callCtx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolId}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY))
		}, time.Minute, time.Second).Should(Succeed())

		_, err = externalIPsClient.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
			Object: publicv1.ExternalIP_builder{
				Id: ipId,
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-ip-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ExternalIPSpec_builder{
					Pool: publicv1.ExternalIPPoolReference_builder{Id: poolId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

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
		networkClassesClient     privatev1.NetworkClassesClient
		poolsClient              privatev1.ExternalIPPoolsClient
		externalIPsClient        publicv1.ExternalIPsClient
		privateExternalIPsClient privatev1.ExternalIPsClient
		publicPoolsClient        publicv1.ExternalIPPoolsClient
		poolId                   string
	)

	BeforeEach(func() {
		ctx = context.Background()
		networkClassesClient = privatev1.NewNetworkClassesClient(tool.InternalView().AdminConn())
		poolsClient = privatev1.NewExternalIPPoolsClient(tool.InternalView().AdminConn())
		externalIPsClient = publicv1.NewExternalIPsClient(tool.ExternalView().UserConn())
		privateExternalIPsClient = privatev1.NewExternalIPsClient(tool.InternalView().AdminConn())
		publicPoolsClient = publicv1.NewExternalIPPoolsClient(tool.ExternalView().UserConn())
		createReadyExternalIPNetworkClass(ctx, networkClassesClient)

		poolId = fmt.Sprintf("test-pool-%s", uuid.New())
		_, err := poolsClient.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
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

		Eventually(func(g Gomega) {
			resp, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{
				Id: poolId,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_PENDING))
			g.Expect(resp.GetObject().GetStatus().GetHub()).ToNot(BeEmpty())
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

		Eventually(func(g Gomega) {
			resp, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{
				Id: poolId,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY))
		}, time.Minute, time.Second).Should(Succeed())
	})

	It("Can create, get, and list an ExternalIP", func() {
		ipId := fmt.Sprintf("test-ip-%s", uuid.New())
		response, err := externalIPsClient.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
			Object: publicv1.ExternalIP_builder{
				Id: ipId,
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-ip-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ExternalIPSpec_builder{
					Pool: publicv1.ExternalIPPoolReference_builder{Id: poolId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		DeferCleanup(func(cleanupCtx context.Context) {
			deleteExternalIPFixtureAndWait(cleanupCtx, externalIPsClient, privateExternalIPsClient, ipId)
		})

		ip := response.GetObject()
		Expect(ip.GetId()).To(Equal(ipId))
		Expect(ip.GetSpec().GetPool().GetId()).To(Equal(poolId))
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
		Expect(getResponse.GetObject().GetSpec().GetPool().GetId()).To(Equal(poolId))

		listResponse, err := externalIPsClient.List(ctx, publicv1.ExternalIPsListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetItems()).ToNot(BeEmpty())
	})

	It("retains ALLOCATED ExternalIP capacity until the ExternalIP is archived", func() {
		ipId := fmt.Sprintf("test-ip-%s", uuid.New())
		poolBefore, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolId}.Build())
		Expect(err).ToNot(HaveOccurred())
		_, err = externalIPsClient.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
			Object: publicv1.ExternalIP_builder{
				Id: ipId,
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-ip-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ExternalIPSpec_builder{
					Pool: publicv1.ExternalIPPoolReference_builder{Id: poolId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			setExternalIPTestFinalizer(cleanupCtx, privateExternalIPsClient, ipId, false)
			deleteExternalIPFixtureAndWait(cleanupCtx, externalIPsClient, privateExternalIPsClient, ipId)
		})
		setExternalIPTestFinalizer(ctx, privateExternalIPsClient, ipId, true)

		// State promotion requires private API (admin-only)
		ipGetResp, err := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
			Id: ipId,
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

		_, err = externalIPsClient.Delete(ctx, publicv1.ExternalIPsDeleteRequest_builder{
			Id: ipId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		ipResp, err := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{Id: ipId}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(ipResp.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeTrue())

		poolWhileDeleting, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolId}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(poolWhileDeleting.GetObject().GetStatus().GetAllocated()).To(
			Equal(poolBefore.GetObject().GetStatus().GetAllocated() + 1))
		Expect(poolWhileDeleting.GetObject().GetStatus().GetAvailable()).To(
			Equal(poolBefore.GetObject().GetStatus().GetAvailable() - 1))

		_, err = poolsClient.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{Id: poolId}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))

		// Repeating Delete while the test finalizer holds the row is idempotent.
		_, err = externalIPsClient.Delete(ctx, publicv1.ExternalIPsDeleteRequest_builder{Id: ipId}.Build())
		Expect(err).ToNot(HaveOccurred())
		poolAfterRetry, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolId}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(poolAfterRetry.GetObject().GetStatus().GetAllocated()).To(
			Equal(poolBefore.GetObject().GetStatus().GetAllocated() + 1))

		setExternalIPTestFinalizer(ctx, privateExternalIPsClient, ipId, false)
		Eventually(func(g Gomega) {
			_, getErr := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{Id: ipId}.Build())
			g.Expect(grpcstatus.Code(getErr)).To(Equal(grpccodes.NotFound))
		}, time.Minute, time.Second).Should(Succeed())
		poolAfterArchive, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolId}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(poolAfterArchive.GetObject().GetStatus().GetAllocated()).To(
			Equal(poolBefore.GetObject().GetStatus().GetAllocated()))
		Expect(poolAfterArchive.GetObject().GetStatus().GetAvailable()).To(
			Equal(poolBefore.GetObject().GetStatus().GetAvailable()))
	})

	It("retains PENDING ExternalIP capacity until the ExternalIP is archived", func() {
		ipId := fmt.Sprintf("test-ip-%s", uuid.New())
		poolBefore, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolId}.Build())
		Expect(err).ToNot(HaveOccurred())
		_, err = externalIPsClient.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
			Object: publicv1.ExternalIP_builder{
				Id: ipId,
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-ip-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ExternalIPSpec_builder{
					Pool: publicv1.ExternalIPPoolReference_builder{Id: poolId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			setExternalIPTestFinalizer(cleanupCtx, privateExternalIPsClient, ipId, false)
			deleteExternalIPFixtureAndWait(cleanupCtx, externalIPsClient, privateExternalIPsClient, ipId)
		})
		setExternalIPTestFinalizer(ctx, privateExternalIPsClient, ipId, true)

		_, err = externalIPsClient.Delete(ctx, publicv1.ExternalIPsDeleteRequest_builder{
			Id: ipId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		poolWhileDeleting, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolId}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(poolWhileDeleting.GetObject().GetStatus().GetAllocated()).To(
			Equal(poolBefore.GetObject().GetStatus().GetAllocated() + 1))
		Expect(poolWhileDeleting.GetObject().GetStatus().GetAvailable()).To(
			Equal(poolBefore.GetObject().GetStatus().GetAvailable() - 1))

		setExternalIPTestFinalizer(ctx, privateExternalIPsClient, ipId, false)
		Eventually(func(g Gomega) {
			_, getErr := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{Id: ipId}.Build())
			g.Expect(grpcstatus.Code(getErr)).To(Equal(grpccodes.NotFound))
		}, time.Minute, time.Second).Should(Succeed())
		poolAfterArchive, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolId}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(poolAfterArchive.GetObject().GetStatus().GetAllocated()).To(Equal(poolBefore.GetObject().GetStatus().GetAllocated()))
		Expect(poolAfterArchive.GetObject().GetStatus().GetAvailable()).To(Equal(poolBefore.GetObject().GetStatus().GetAvailable()))
	})

	It("Rejects create with non-existent pool", func() {
		ipId := fmt.Sprintf("test-ip-%s", uuid.New())
		_, err := externalIPsClient.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
			Object: publicv1.ExternalIP_builder{
				Id: ipId,
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-ip-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ExternalIPSpec_builder{
					Pool: publicv1.ExternalIPPoolReference_builder{Name: "nonexistent-pool"}.Build(),
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

		ipId := fmt.Sprintf("test-ip-%s", uuid.New())
		_, err = externalIPsClient.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
			Object: publicv1.ExternalIP_builder{
				Id: ipId,
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-ip-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ExternalIPSpec_builder{
					Pool: publicv1.ExternalIPPoolReference_builder{Id: nonReadyPoolId}.Build(),
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

// The installer integration target disables operator and fulfillment sync; delete
// acknowledgements do not guarantee immediate NotFound responses in that mode.
var _ = Describe("ExternalIPAttachment cross-resource validation", func() {
	var (
		ctx                      context.Context
		networkClassesClient     privatev1.NetworkClassesClient
		poolsClient              privatev1.ExternalIPPoolsClient
		virtualNetworksClient    privatev1.VirtualNetworksClient
		subnetsClient            privatev1.SubnetsClient
		externalIPsClient        publicv1.ExternalIPsClient
		privateExternalIPsClient privatev1.ExternalIPsClient
		attachmentsClient        publicv1.ExternalIPAttachmentsClient
		privateAttachmentsClient privatev1.ExternalIPAttachmentsClient
		clustersClient           publicv1.ClustersClient
		privateClustersClient    privatev1.ClustersClient
		instanceTypesClient      privatev1.BareMetalInstanceTypesClient
		clusterTemplatesClient   privatev1.ClusterTemplatesClient

		poolId                  string
		externalIPId            string
		clusterId               string
		racingClusterID         string
		subnetID                string
		bmitName                string
		templateId              string
		attachmentIDs           []string
		additionalExternalIPIDs []string
		networkClassHubID       string
	)

	BeforeEach(func() {
		ctx = context.Background()
		attachmentIDs = nil
		additionalExternalIPIDs = nil
		racingClusterID = ""
		networkClassesClient = privatev1.NewNetworkClassesClient(tool.InternalView().AdminConn())
		poolsClient = privatev1.NewExternalIPPoolsClient(tool.InternalView().AdminConn())
		virtualNetworksClient = privatev1.NewVirtualNetworksClient(tool.InternalView().AdminConn())
		subnetsClient = privatev1.NewSubnetsClient(tool.InternalView().AdminConn())
		externalIPsClient = publicv1.NewExternalIPsClient(tool.ExternalView().UserConn())
		privateExternalIPsClient = privatev1.NewExternalIPsClient(tool.InternalView().AdminConn())
		attachmentsClient = publicv1.NewExternalIPAttachmentsClient(tool.ExternalView().UserConn())
		privateAttachmentsClient = privatev1.NewExternalIPAttachmentsClient(tool.InternalView().AdminConn())
		clustersClient = publicv1.NewClustersClient(tool.ExternalView().UserConn())
		privateClustersClient = privatev1.NewClustersClient(tool.InternalView().AdminConn())
		instanceTypesClient = privatev1.NewBareMetalInstanceTypesClient(tool.InternalView().AdminConn())
		clusterTemplatesClient = privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())
		networkClassID := createReadyExternalIPNetworkClass(ctx, networkClassesClient)
		networkClassResponse, err := networkClassesClient.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
			Id: networkClassID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		networkClassHubID = networkClassResponse.GetObject().GetStatus().GetHub()
		Expect(networkClassHubID).ToNot(BeEmpty())

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
		DeferCleanup(func(cleanupCtx context.Context) {
			deleteComputeInstanceFixtureResource(cleanupCtx,
				func(deleteCtx context.Context) error {
					_, deleteErr := poolsClient.Delete(deleteCtx, privatev1.ExternalIPPoolsDeleteRequest_builder{Id: poolId}.Build())
					return deleteErr
				})
		})

		Eventually(func(g Gomega) {
			resp, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{
				Id: poolId,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_PENDING))
			g.Expect(resp.GetObject().GetStatus().GetHub()).ToNot(BeEmpty())
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

		Eventually(func(g Gomega) {
			resp, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{
				Id: poolId,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY))
		}, time.Minute, time.Second).Should(Succeed())

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
		DeferCleanup(func(cleanupCtx context.Context) {
			deleteExternalIPFixtureAndWait(cleanupCtx, externalIPsClient, privateExternalIPsClient, externalIPId)
		})
		Eventually(func(g Gomega) {
			resp, err := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
				Id: externalIPId,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetHub()).To(Equal(networkClassHubID))
		}, time.Minute, time.Second).Should(Succeed())

		// State promotion requires private API (admin-only)
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

		bmitName = fmt.Sprintf("test-bmit-%s", uuid.New()[24:32])
		_, err = instanceTypesClient.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceType_builder{
				Metadata: privatev1.Metadata_builder{
					Name: bmitName,
				}.Build(),
				Spec: privatev1.BareMetalInstanceTypeSpec_builder{
					Hardware: privatev1.BareMetalHardwareSpec_builder{
						Cpu:    privatev1.BareMetalCPUSpec_builder{Cores: 32, Architecture: "amd64", ThreadsPerCore: 2}.Build(),
						Memory: privatev1.BareMetalMemorySpec_builder{TotalGb: 128}.Build(),
						NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
							privatev1.BareMetalNetworkPortSpec_builder{
								Name:  "eth0",
								Role:  "fabric",
								Type:  "Ethernet",
								Speed: "10Gbps",
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
		DeferCleanup(func(cleanupCtx context.Context) {
			deleteAndWaitForComputeInstanceFixtureResource(cleanupCtx,
				func(deleteCtx context.Context) error {
					_, deleteErr := instanceTypesClient.Delete(deleteCtx, privatev1.BareMetalInstanceTypesDeleteRequest_builder{Id: bmitName}.Build())
					return deleteErr
				},
				func(getCtx context.Context) error {
					_, getErr := instanceTypesClient.Get(getCtx, privatev1.BareMetalInstanceTypesGetRequest_builder{Id: bmitName}.Build())
					return getErr
				})
		})

		templateId = fmt.Sprintf("test-tmpl-%s", uuid.New())
		_, err = clusterTemplatesClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:    templateId,
				Title: "Test Template",
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-tmpl-%s", uuid.New()[24:32]),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			deleteAndWaitForComputeInstanceFixtureResource(cleanupCtx,
				func(deleteCtx context.Context) error {
					_, deleteErr := clusterTemplatesClient.Delete(deleteCtx, privatev1.ClusterTemplatesDeleteRequest_builder{Id: templateId}.Build())
					return deleteErr
				},
				func(getCtx context.Context) error {
					_, getErr := clusterTemplatesClient.Get(getCtx, privatev1.ClusterTemplatesGetRequest_builder{Id: templateId}.Build())
					return getErr
				})
		})

		// This Cluster is a networked target, so select its Hub from a real subnet
		// attachment and wait until that Hub matches the ExternalIP's canonical Hub.
		virtualNetworkID := fmt.Sprintf("test-vnet-%s", uuid.New())
		_, err = virtualNetworksClient.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Id: virtualNetworkID,
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-vnet-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					NetworkClass: privatev1.NetworkClassReference_builder{Id: networkClassID}.Build(),
					Region:       "us-east-1",
					Ipv4Cidr:     new("10.230.0.0/16"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			Expect(cleanupVirtualNetworkFixture(cleanupCtx, virtualNetworksClient, virtualNetworkID)).To(Succeed())
		})
		expectNetworkingResourceHub(ctx, networkClassHubID, func(getCtx context.Context) (string, error) {
			resp, getErr := virtualNetworksClient.Get(getCtx, privatev1.VirtualNetworksGetRequest_builder{
				Id: virtualNetworkID,
			}.Build())
			if getErr != nil {
				return "", getErr
			}
			return resp.GetObject().GetStatus().GetHub(), nil
		})
		setRoutingVirtualNetworkReady(ctx, virtualNetworksClient, virtualNetworkID)

		subnetID = fmt.Sprintf("test-subnet-%s", uuid.New())
		_, err = subnetsClient.Create(ctx, privatev1.SubnetsCreateRequest_builder{
			Object: privatev1.Subnet_builder{
				Id: subnetID,
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-subnet-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
					Ipv4Cidr:       new("10.230.1.0/24"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			Expect(cleanupSubnetFixture(cleanupCtx, subnetsClient, subnetID)).To(Succeed())
		})
		expectNetworkingResourceHub(ctx, networkClassHubID, func(getCtx context.Context) (string, error) {
			resp, getErr := subnetsClient.Get(getCtx, privatev1.SubnetsGetRequest_builder{
				Id: subnetID,
			}.Build())
			if getErr != nil {
				return "", getErr
			}
			return resp.GetObject().GetStatus().GetHub(), nil
		})
		setRoutingSubnetReady(ctx, subnetsClient, subnetID)

		createClusterResp, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					NodeSets: map[string]*publicv1.ClusterNodeSet{"workers": publicv1.ClusterNodeSet_builder{
						Size: new(int32(1)), BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: bmitName, Shared: true}.Build(),
					}.Build()},
					NetworkAttachment: publicv1.ClusterNetworkAttachment_builder{
						Subnet: publicv1.SubnetLocalReference_builder{Id: subnetID}.Build(),
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		clusterId = createClusterResp.GetObject().GetId()
		Expect(setClusterFixtureReadiness(ctx, privateClustersClient, clusterId, true)).To(Succeed())
		expectNetworkingResourceHub(ctx, networkClassHubID, func(getCtx context.Context) (string, error) {
			response, getErr := privateClustersClient.Get(getCtx, privatev1.ClustersGetRequest_builder{Id: clusterId}.Build())
			if getErr != nil {
				return "", getErr
			}
			return response.GetObject().GetStatus().GetHub(), nil
		})
	})

	AfterEach(func() {
		if clusterId != "" {
			Expect(setClusterFixtureReadiness(ctx, privateClustersClient, clusterId, false)).To(Succeed())
		}
		for _, id := range attachmentIDs {
			Expect(cleanupExternalIPAttachmentFixture(ctx, privateAttachmentsClient, id)).To(Succeed())
		}
		for _, id := range additionalExternalIPIDs {
			filter := fmt.Sprintf("this.spec.external_ip.id == %q && !has(this.metadata.deletion_timestamp)", id)
			response, err := privateAttachmentsClient.List(ctx, privatev1.ExternalIPAttachmentsListRequest_builder{
				Filter: &filter,
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			for _, attachment := range response.GetItems() {
				Expect(cleanupExternalIPAttachmentFixture(ctx, privateAttachmentsClient, attachment.GetId())).To(Succeed())
			}
		}
		if racingClusterID != "" {
			ownerFilter := fmt.Sprintf("this.metadata.labels['osac.openshift.io/auto-created-for'] == %q", racingClusterID)
			attachments, err := privateAttachmentsClient.List(ctx, privatev1.ExternalIPAttachmentsListRequest_builder{
				Filter: &ownerFilter,
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			for _, attachment := range attachments.GetItems() {
				Expect(cleanupExternalIPAttachmentFixture(ctx, privateAttachmentsClient, attachment.GetId())).To(Succeed())
			}
			Expect(cleanupClusterFixture(ctx, clustersClient, privateClustersClient, racingClusterID)).To(Succeed())
			externalIPs, err := privateExternalIPsClient.List(ctx, privatev1.ExternalIPsListRequest_builder{
				Filter: &ownerFilter,
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			for _, externalIP := range externalIPs.GetItems() {
				Expect(cleanupExternalIPFixture(ctx, privateExternalIPsClient, externalIP.GetId())).To(Succeed())
			}
		}
		if clusterId != "" {
			Expect(cleanupClusterFixture(ctx, clustersClient, privateClustersClient, clusterId)).To(Succeed())
		}
		for _, id := range additionalExternalIPIDs {
			Expect(cleanupExternalIPFixture(ctx, privateExternalIPsClient, id)).To(Succeed())
		}
		if externalIPId != "" {
			Expect(cleanupExternalIPFixture(ctx, privateExternalIPsClient, externalIPId)).To(Succeed())
		}
		if poolId != "" {
			Expect(cleanupExternalIPPoolFixture(ctx, poolsClient, poolId)).To(Succeed())
		}
		if templateId != "" {
			clusterTemplatesClient.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
				Id: templateId,
			}.Build())
		}
		if bmitName != "" {
			instanceTypesClient.Delete(ctx, privatev1.BareMetalInstanceTypesDeleteRequest_builder{
				Id: bmitName,
			}.Build())
		}
	})

	It("Can create, get, and list an ExternalIPAttachment with cluster target", func() {
		attachmentId := fmt.Sprintf("test-att-%s", uuid.New())
		response, err := attachmentsClient.Create(ctx, publicv1.ExternalIPAttachmentsCreateRequest_builder{
			Object: publicv1.ExternalIPAttachment_builder{
				Id: attachmentId,
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-att-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp:     publicv1.ExternalIPLocalReference_builder{Id: externalIPId}.Build(),
					Cluster:        publicv1.ClusterLocalReference_builder{Id: clusterId}.Build(),
					TargetEndpoint: publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		attachmentIDs = append(attachmentIDs, attachmentId)
		Expect(response).ToNot(BeNil())
		DeferCleanup(func(cleanupCtx context.Context) {
			deleteExternalIPAttachmentFixtureAndWait(cleanupCtx, attachmentsClient, privateAttachmentsClient, attachmentId)
		})

		attachment := response.GetObject()
		Expect(attachment.GetId()).To(Equal(attachmentId))
		Expect(attachment.GetSpec().GetExternalIp().GetId()).To(Equal(externalIPId))
		Expect(attachment.GetSpec().GetCluster().GetId()).To(Equal(clusterId))
		Expect(attachment.GetStatus().GetState()).To(
			Equal(publicv1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING))

		ipResp, err := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
			Id: externalIPId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(ipResp.GetObject().GetStatus().GetAttached()).To(BeFalse())

		getResponse, err := attachmentsClient.Get(ctx, publicv1.ExternalIPAttachmentsGetRequest_builder{
			Id: attachmentId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetId()).To(Equal(attachmentId))
		Expect(getResponse.GetObject().GetSpec().GetExternalIp().GetId()).To(Equal(externalIPId))
		Expect(getResponse.GetObject().GetSpec().GetCluster().GetId()).To(Equal(clusterId))

		listResponse, err := attachmentsClient.List(ctx,
			publicv1.ExternalIPAttachmentsListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetItems()).ToNot(BeEmpty())
	})

	It("creates one deferred automatic attachment after its ExternalIP and Cluster endpoint are ready", func() {
		clusterResponse, err := privateClustersClient.Get(ctx, privatev1.ClustersGetRequest_builder{Id: clusterId}.Build())
		Expect(err).NotTo(HaveOccurred())
		cluster := clusterResponse.GetObject()
		tenant := cluster.GetMetadata().GetTenant()
		Expect(setClusterFixtureReadiness(ctx, privateClustersClient, clusterId, false)).To(Succeed())
		deferredIPID := fmt.Sprintf("deferred-auto-ip-%s", uuid.New())
		deferredAttachmentFilter := fmt.Sprintf("this.spec.external_ip.id == %q && !has(this.metadata.deletion_timestamp)", deferredIPID)
		listAttachments := func() []*privatev1.ExternalIPAttachment {
			limit := int32(10)
			response, listErr := privateAttachmentsClient.List(ctx, privatev1.ExternalIPAttachmentsListRequest_builder{
				Filter: &deferredAttachmentFilter,
				Limit:  &limit,
			}.Build())
			Expect(listErr).NotTo(HaveOccurred())
			return response.GetItems()
		}

		additionalExternalIPIDs = append(additionalExternalIPIDs, deferredIPID)
		_, err = privateExternalIPsClient.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{
			Object: privatev1.ExternalIP_builder{
				Id: deferredIPID,
				Metadata: privatev1.Metadata_builder{
					Name:    deferredIPID,
					Tenant:  tenant,
					Creator: "system",
					Labels: map[string]string{
						"osac.openshift.io/auto-created":             "true",
						"osac.openshift.io/auto-created-for":         clusterId,
						"osac.openshift.io/auto-attachment-deferred": "true",
						"osac.openshift.io/auto-created-kind":        "cluster",
						"osac.openshift.io/auto-created-endpoint":    "api",
					},
					Annotations: map[string]string{
						"osac.openshift.io/tenant":          tenant,
						"osac.openshift.io/owner-reference": clusterId,
					},
				}.Build(),
				Spec: privatev1.ExternalIPSpec_builder{
					Pool: privatev1.ExternalIPPoolReference_builder{Id: poolId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		Consistently(listAttachments, 2*time.Second, 100*time.Millisecond).Should(BeEmpty(),
			"a deferred EIA must not be created while its ExternalIP is pending")

		Expect(setExternalIPFixtureState(ctx, privateExternalIPsClient, deferredIPID,
			privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)).To(Succeed())
		Consistently(listAttachments, 2*time.Second, 100*time.Millisecond).Should(BeEmpty(),
			"an allocated ExternalIP must wait for the Cluster endpoint")

		Expect(setClusterFixtureReadiness(ctx, privateClustersClient, clusterId, true)).To(Succeed())

		var created []*privatev1.ExternalIPAttachment
		Eventually(func(g Gomega) {
			created = listAttachments()
			g.Expect(created).To(HaveLen(1))
		}, time.Minute, time.Second).Should(Succeed())
		Expect(created[0].GetMetadata().GetLabels()["osac.openshift.io/auto-created"]).To(Equal("true"))
		Expect(created[0].GetMetadata().GetAnnotations()["osac.openshift.io/owner-reference"]).To(Equal(clusterId))
		Expect(created[0].GetSpec().GetCluster().GetId()).To(Equal(clusterId))
		Expect(created[0].GetSpec().GetTargetEndpoint()).To(Equal(privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API))
		attachmentIDs = append(attachmentIDs, created[0].GetId())

		Consistently(listAttachments, 2*time.Second, 100*time.Millisecond).Should(HaveLen(1),
			"reconciliation must keep exactly one automatic attachment for this ExternalIP")
	})

	It("does not recreate a deferred attachment when Cluster deletion races readiness", func() {
		autoAttach := true
		createdCluster, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{Name: fmt.Sprintf("test-race-cluster-%s", uuid.New()[24:32])}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					NodeSets: map[string]*publicv1.ClusterNodeSet{"workers": publicv1.ClusterNodeSet_builder{
						Size: new(int32(1)), BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: bmitName}.Build(),
					}.Build()},
					NetworkAttachment: publicv1.ClusterNetworkAttachment_builder{
						Subnet: publicv1.SubnetLocalReference_builder{Id: subnetID}.Build(),
					}.Build(),
					AutoExternalIpAttachment: &autoAttach,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		racingClusterID = createdCluster.GetObject().GetId()
		Expect(setClusterFixtureReadiness(ctx, privateClustersClient, racingClusterID, false)).To(Succeed())
		clusterResponse, err := privateClustersClient.Get(ctx,
			privatev1.ClustersGetRequest_builder{Id: racingClusterID}.Build())
		Expect(err).NotTo(HaveOccurred())
		tenant := clusterResponse.GetObject().GetMetadata().GetTenant()

		deferredIPID := fmt.Sprintf("deferred-race-ip-%s", uuid.New())
		additionalExternalIPIDs = append(additionalExternalIPIDs, deferredIPID)
		_, err = privateExternalIPsClient.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{
			Object: privatev1.ExternalIP_builder{
				Id: deferredIPID,
				Metadata: privatev1.Metadata_builder{
					Name:    deferredIPID,
					Tenant:  tenant,
					Creator: "system",
					Labels: map[string]string{
						"osac.openshift.io/auto-created":             "true",
						"osac.openshift.io/auto-created-for":         racingClusterID,
						"osac.openshift.io/auto-attachment-deferred": "true",
						"osac.openshift.io/auto-created-kind":        "cluster",
						"osac.openshift.io/auto-created-endpoint":    "api",
					},
					Annotations: map[string]string{
						"osac.openshift.io/tenant":          tenant,
						"osac.openshift.io/owner-reference": racingClusterID,
					},
				}.Build(),
				Spec: privatev1.ExternalIPSpec_builder{
					Pool: privatev1.ExternalIPPoolReference_builder{Id: poolId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		getRaceIP := func() (*privatev1.ExternalIP, error) {
			response, getErr := privateExternalIPsClient.Get(ctx,
				privatev1.ExternalIPsGetRequest_builder{Id: deferredIPID}.Build())
			if getErr != nil {
				return nil, getErr
			}
			return response.GetObject(), nil
		}
		Eventually(func(g Gomega) {
			ip, getErr := getRaceIP()
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(ip.GetStatus().GetHub()).To(Equal(networkClassHubID))
		}, time.Minute, time.Second).Should(Succeed())
		Expect(setExternalIPFixtureState(ctx, privateExternalIPsClient, deferredIPID,
			privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)).To(Succeed())

		ownerFilter := fmt.Sprintf("this.metadata.labels['osac.openshift.io/auto-created-for'] == %q", racingClusterID)
		listOwnedAttachments := func() []*privatev1.ExternalIPAttachment {
			response, listErr := privateAttachmentsClient.List(ctx, privatev1.ExternalIPAttachmentsListRequest_builder{
				Filter: &ownerFilter,
			}.Build())
			Expect(listErr).NotTo(HaveOccurred())
			return response.GetItems()
		}
		Consistently(listOwnedAttachments, 500*time.Millisecond, 100*time.Millisecond).Should(BeEmpty())

		// The deferred reconciler and the public workload delete share a start gate.
		// Whichever acquires the target/IP lifecycle locks first must leave cleanup
		// resumable and prevent a retry from creating a late attachment.
		start := make(chan struct{})
		ready := make(chan struct{}, 2)
		readinessResult := make(chan error, 1)
		deletionResult := make(chan error, 1)
		go func() {
			ready <- struct{}{}
			<-start
			readinessResult <- setClusterFixtureReadiness(ctx, privateClustersClient, racingClusterID, true)
		}()
		go func() {
			ready <- struct{}{}
			<-start
			_, deleteErr := clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{Id: racingClusterID}.Build())
			deletionResult <- deleteErr
		}()
		<-ready
		<-ready
		close(start)
		readyErr := <-readinessResult
		deleteErr := <-deletionResult
		if grpcstatus.Code(deleteErr) == grpccodes.Aborted {
			_, deleteErr = clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{Id: racingClusterID}.Build())
		}
		Expect(deleteErr).NotTo(HaveOccurred())
		if readyErr != nil {
			Expect(grpcstatus.Code(readyErr)).To(Or(Equal(grpccodes.NotFound), Equal(grpccodes.FailedPrecondition)))
		}

		Eventually(func(g Gomega) {
			cluster, getErr := privateClustersClient.Get(ctx,
				privatev1.ClustersGetRequest_builder{Id: racingClusterID}.Build())
			if grpcstatus.Code(getErr) == grpccodes.NotFound {
				return
			}
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(cluster.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeTrue())
		}, time.Minute, time.Second).Should(Succeed())
		Consistently(func(g Gomega) {
			for _, attachment := range listOwnedAttachments() {
				g.Expect(attachment.GetMetadata().HasDeletionTimestamp()).To(BeTrue(),
					"a deferred attachment must not remain active after its Cluster is deleting")
			}
		}, 2*time.Second, 100*time.Millisecond).Should(Succeed())

		for _, attachment := range listOwnedAttachments() {
			attachmentIDs = append(attachmentIDs, attachment.GetId())
			Expect(cleanupExternalIPAttachmentFixture(ctx, privateAttachmentsClient, attachment.GetId())).To(Succeed())
		}
		ownedIPs, err := privateExternalIPsClient.List(ctx, privatev1.ExternalIPsListRequest_builder{
			Filter: &ownerFilter,
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		for _, externalIP := range ownedIPs.GetItems() {
			Expect(cleanupExternalIPFixture(ctx, privateExternalIPsClient, externalIP.GetId())).To(Succeed())
		}
		Eventually(func(g Gomega) {
			g.Expect(listOwnedAttachments()).To(BeEmpty())
			response, listErr := privateExternalIPsClient.List(ctx, privatev1.ExternalIPsListRequest_builder{
				Filter: &ownerFilter,
			}.Build())
			g.Expect(listErr).NotTo(HaveOccurred())
			g.Expect(response.GetItems()).To(BeEmpty())
		}, time.Minute, time.Second).Should(Succeed())
	})

	It("does not release a deferred attachment IP while that attachment is terminating", func() {
		autoAttach := true
		createdCluster, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{Name: fmt.Sprintf("test-held-cluster-%s", uuid.New()[24:32])}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					NodeSets: map[string]*publicv1.ClusterNodeSet{"workers": publicv1.ClusterNodeSet_builder{
						Size: new(int32(1)), BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: bmitName}.Build(),
					}.Build()},
					NetworkAttachment: publicv1.ClusterNetworkAttachment_builder{
						Subnet: publicv1.SubnetLocalReference_builder{Id: subnetID}.Build(),
					}.Build(),
					AutoExternalIpAttachment: &autoAttach,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		racingClusterID = createdCluster.GetObject().GetId()
		Expect(setClusterFixtureReadiness(ctx, privateClustersClient, racingClusterID, false)).To(Succeed())
		clusterResponse, err := privateClustersClient.Get(ctx,
			privatev1.ClustersGetRequest_builder{Id: racingClusterID}.Build())
		Expect(err).NotTo(HaveOccurred())
		tenant := clusterResponse.GetObject().GetMetadata().GetTenant()

		deferredIPID := fmt.Sprintf("deferred-held-ip-%s", uuid.New())
		additionalExternalIPIDs = append(additionalExternalIPIDs, deferredIPID)
		_, err = privateExternalIPsClient.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{
			Object: privatev1.ExternalIP_builder{
				Id: deferredIPID,
				Metadata: privatev1.Metadata_builder{
					Name:    deferredIPID,
					Tenant:  tenant,
					Creator: "system",
					Labels: map[string]string{
						"osac.openshift.io/auto-created":             "true",
						"osac.openshift.io/auto-created-for":         racingClusterID,
						"osac.openshift.io/auto-attachment-deferred": "true",
						"osac.openshift.io/auto-created-kind":        "cluster",
						"osac.openshift.io/auto-created-endpoint":    "api",
					},
					Annotations: map[string]string{
						"osac.openshift.io/tenant":          tenant,
						"osac.openshift.io/owner-reference": racingClusterID,
					},
				}.Build(),
				Spec: privatev1.ExternalIPSpec_builder{
					Pool: privatev1.ExternalIPPoolReference_builder{Id: poolId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(setExternalIPFixtureState(ctx, privateExternalIPsClient, deferredIPID,
			privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)).To(Succeed())
		Expect(setClusterFixtureReadiness(ctx, privateClustersClient, racingClusterID, true)).To(Succeed())

		attachmentFilter := fmt.Sprintf("this.spec.external_ip.id == %q", deferredIPID)
		listDeferredAttachments := func() []*privatev1.ExternalIPAttachment {
			response, listErr := privateAttachmentsClient.List(ctx, privatev1.ExternalIPAttachmentsListRequest_builder{
				Filter: &attachmentFilter,
			}.Build())
			Expect(listErr).NotTo(HaveOccurred())
			return response.GetItems()
		}
		var deferredAttachment *privatev1.ExternalIPAttachment
		Eventually(func(g Gomega) {
			items := listDeferredAttachments()
			g.Expect(items).To(HaveLen(1))
			deferredAttachment = items[0]
		}, time.Minute, time.Second).Should(Succeed())
		attachmentIDs = append(attachmentIDs, deferredAttachment.GetId())

		Expect(addExternalIPAttachmentFinalizerWithRetry(ctx, privateAttachmentsClient,
			deferredAttachment.GetId(), "integration-test/hold-deferred-detach")).To(Succeed())
		_, err = attachmentsClient.Delete(ctx,
			publicv1.ExternalIPAttachmentsDeleteRequest_builder{Id: deferredAttachment.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			response, getErr := privateAttachmentsClient.Get(ctx,
				privatev1.ExternalIPAttachmentsGetRequest_builder{Id: deferredAttachment.GetId()}.Build())
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(response.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeTrue())
		}, time.Minute, time.Second).Should(Succeed())

		_, err = clustersClient.Delete(ctx,
			publicv1.ClustersDeleteRequest_builder{Id: racingClusterID}.Build())
		Expect(err).NotTo(HaveOccurred())
		clusterResponse, err = privateClustersClient.Get(ctx,
			privatev1.ClustersGetRequest_builder{Id: racingClusterID}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(clusterResponse.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeTrue())
		ipResponse, err := privateExternalIPsClient.Get(ctx,
			privatev1.ExternalIPsGetRequest_builder{Id: deferredIPID}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(ipResponse.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeFalse(),
			"a terminating attachment must retain its ExternalIP until detach completes")
		Consistently(func(g Gomega) {
			currentCluster, getClusterErr := privateClustersClient.Get(ctx,
				privatev1.ClustersGetRequest_builder{Id: racingClusterID}.Build())
			g.Expect(getClusterErr).NotTo(HaveOccurred(),
				"the Cluster must remain available to attachment lifecycle updates while detach is pending")
			g.Expect(currentCluster.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeTrue())
			g.Expect(listDeferredAttachments()).To(HaveLen(1),
				"the deferred reconciler must not replace an attachment that is already terminating")
		}, time.Second, 100*time.Millisecond).Should(Succeed())

		err = removeExternalIPAttachmentFinalizerWithRetry(ctx, privateAttachmentsClient,
			deferredAttachment.GetId(), "integration-test/hold-deferred-detach")
		// Removing the last finalizer can delete the terminating attachment in
		// the same transaction, in which case the update reports NotFound. Let the
		// assertions below verify that workload cleanup also removes its IPs.
		if grpcstatus.Code(err) == grpccodes.NotFound {
			_, attachmentGetErr := privateAttachmentsClient.Get(ctx,
				privatev1.ExternalIPAttachmentsGetRequest_builder{Id: deferredAttachment.GetId()}.Build())
			Expect(grpcstatus.Code(attachmentGetErr)).To(Equal(grpccodes.NotFound),
				"NotFound while releasing the finalizer must mean the attachment is gone, not that its target disappeared")
		} else {
			Expect(err).NotTo(HaveOccurred())
			Expect(cleanupExternalIPAttachmentFixture(ctx, privateAttachmentsClient, deferredAttachment.GetId())).To(Succeed())
		}
		ownerFilter := fmt.Sprintf("this.metadata.labels['osac.openshift.io/auto-created-for'] == %q", racingClusterID)
		Eventually(func(g Gomega) {
			_, clusterErr := privateClustersClient.Get(ctx,
				privatev1.ClustersGetRequest_builder{Id: racingClusterID}.Build())
			g.Expect(grpcstatus.Code(clusterErr)).To(Equal(grpccodes.NotFound),
				"the Cluster should finish deletion after its terminating attachment is released")
			g.Expect(listDeferredAttachments()).To(BeEmpty())
			ips, listErr := privateExternalIPsClient.List(ctx, privatev1.ExternalIPsListRequest_builder{
				Filter: &ownerFilter,
			}.Build())
			g.Expect(listErr).NotTo(HaveOccurred())
			g.Expect(ips.GetItems()).To(BeEmpty())
		}, time.Minute, time.Second).Should(Succeed())
	})

	It("serializes ExternalIPAttachment creation against ExternalIP deletion", func() {
		raceIPID := fmt.Sprintf("test-race-ip-%s", uuid.New())
		additionalExternalIPIDs = append(additionalExternalIPIDs, raceIPID)
		_, err := externalIPsClient.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
			Object: publicv1.ExternalIP_builder{
				Id:       raceIPID,
				Metadata: publicv1.Metadata_builder{Name: raceIPID}.Build(),
				Spec: publicv1.ExternalIPSpec_builder{
					Pool: publicv1.ExternalIPPoolReference_builder{Id: poolId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		getRaceIP := func() (*privatev1.ExternalIP, error) {
			response, getErr := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{Id: raceIPID}.Build())
			if getErr != nil {
				return nil, getErr
			}
			return response.GetObject(), nil
		}
		Eventually(func(g Gomega) {
			ip, getErr := getRaceIP()
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(ip.GetStatus().GetHub()).To(Equal(networkClassHubID))
		}, time.Minute, time.Second).Should(Succeed())
		Expect(setExternalIPFixtureState(ctx, privateExternalIPsClient, raceIPID,
			privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)).To(Succeed())

		attachmentID := fmt.Sprintf("test-race-attachment-%s", uuid.New())
		attachmentRequest := publicv1.ExternalIPAttachmentsCreateRequest_builder{
			Object: publicv1.ExternalIPAttachment_builder{
				Id:       attachmentID,
				Metadata: publicv1.Metadata_builder{Name: attachmentID}.Build(),
				Spec: publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp:     publicv1.ExternalIPLocalReference_builder{Id: raceIPID}.Build(),
					Cluster:        publicv1.ClusterLocalReference_builder{Id: clusterId}.Build(),
					TargetEndpoint: publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
				}.Build(),
			}.Build(),
		}.Build()

		raceCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		start := make(chan struct{})
		ready := make(chan struct{}, 2)
		type operationResult struct{ err error }
		createResult := make(chan operationResult, 1)
		deleteResult := make(chan operationResult, 1)
		go func() {
			ready <- struct{}{}
			<-start
			_, createErr := attachmentsClient.Create(raceCtx, attachmentRequest)
			createResult <- operationResult{err: createErr}
		}()
		go func() {
			ready <- struct{}{}
			<-start
			_, deleteErr := externalIPsClient.Delete(raceCtx, publicv1.ExternalIPsDeleteRequest_builder{Id: raceIPID}.Build())
			deleteResult <- operationResult{err: deleteErr}
		}()
		<-ready
		<-ready
		close(start)
		created := <-createResult
		deleted := <-deleteResult

		// A database deadlock is surfaced as Aborted. Retry that operation once so
		// the assertion checks the settled resource state after the concurrent race.
		if grpcstatus.Code(created.err) == grpccodes.Aborted {
			_, created.err = attachmentsClient.Create(ctx, attachmentRequest)
		}
		if grpcstatus.Code(deleted.err) == grpccodes.Aborted {
			_, deleted.err = externalIPsClient.Delete(ctx, publicv1.ExternalIPsDeleteRequest_builder{Id: raceIPID}.Build())
		}

		attachmentResponse, attachmentErr := privateAttachmentsClient.Get(ctx,
			privatev1.ExternalIPAttachmentsGetRequest_builder{Id: attachmentID}.Build())
		ipResponse, ipErr := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{Id: raceIPID}.Build())
		if attachmentErr == nil {
			attachmentIDs = append(attachmentIDs, attachmentID)
			Expect(ipErr).NotTo(HaveOccurred(), "a committed attachment must retain its ExternalIP")
			Expect(ipResponse.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeFalse(),
				"ExternalIP deletion must not pass an attachment created in the same race")
			Expect(deleted.err).To(HaveOccurred())
			Expect(attachmentResponse.GetObject().GetSpec().GetExternalIp().GetId()).To(Equal(raceIPID))
		} else {
			Expect(grpcstatus.Code(attachmentErr)).To(Equal(grpccodes.NotFound))
			Expect(deleted.err).NotTo(HaveOccurred(), "when no attachment commits, ExternalIP deletion must win")
			if ipErr == nil {
				Expect(ipResponse.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeTrue(),
					"a deleted ExternalIP may remain until its finalizer completes")
			} else {
				Expect(grpcstatus.Code(ipErr)).To(Equal(grpccodes.NotFound))
			}
		}
	})

	It("keeps an ExternalIP in use while its attachment is deleting behind a finalizer", func() {
		const heldFinalizer = "integration-test/hold-detach"
		attachmentID := fmt.Sprintf("test-held-attachment-%s", uuid.New())
		_, err := attachmentsClient.Create(ctx, publicv1.ExternalIPAttachmentsCreateRequest_builder{
			Object: publicv1.ExternalIPAttachment_builder{
				Id:       attachmentID,
				Metadata: publicv1.Metadata_builder{Name: attachmentID}.Build(),
				Spec: publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp:     publicv1.ExternalIPLocalReference_builder{Id: externalIPId}.Build(),
					Cluster:        publicv1.ClusterLocalReference_builder{Id: clusterId}.Build(),
					TargetEndpoint: publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		attachmentIDs = append(attachmentIDs, attachmentID)

		// The event-driven Fulfillment controller adds its own finalizer after
		// Create. Wait for that first reconcile to finish before adding the test
		// finalizer, otherwise its stale update can replace our marker.
		Eventually(func(g Gomega) {
			response, getErr := privateAttachmentsClient.Get(ctx,
				privatev1.ExternalIPAttachmentsGetRequest_builder{Id: attachmentID}.Build())
			g.Expect(getErr).NotTo(HaveOccurred())
			metadata := response.GetObject().GetMetadata()
			g.Expect(metadata.GetFinalizers()).To(ContainElement(finalizers.Controller))
			g.Expect(response.GetObject().GetStatus().GetHub()).NotTo(BeEmpty())
		}, time.Minute, time.Second).Should(Succeed())

		Expect(addExternalIPAttachmentFinalizerWithRetry(ctx, privateAttachmentsClient,
			attachmentID, heldFinalizer)).To(Succeed())
		attachmentBeforeDelete, err := privateAttachmentsClient.Get(ctx,
			privatev1.ExternalIPAttachmentsGetRequest_builder{Id: attachmentID}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(attachmentBeforeDelete.GetObject().GetMetadata().GetFinalizers()).To(ContainElement(heldFinalizer),
			"the test finalizer must be persisted before requesting deletion")
		_, err = attachmentsClient.Delete(ctx, publicv1.ExternalIPAttachmentsDeleteRequest_builder{Id: attachmentID}.Build())
		Expect(err).NotTo(HaveOccurred())
		attachmentAfterDelete, err := privateAttachmentsClient.Get(ctx,
			privatev1.ExternalIPAttachmentsGetRequest_builder{Id: attachmentID}.Build())
		Expect(err).NotTo(HaveOccurred())
		metadata := attachmentAfterDelete.GetObject().GetMetadata()
		Expect(metadata.HasDeletionTimestamp()).To(BeTrue(),
			"a finalizer must keep the attachment available after the synchronous delete request")
		Expect(metadata.GetFinalizers()).To(ContainElement(heldFinalizer),
			"the test finalizer must remain until the delete guard is checked")

		_, err = externalIPsClient.Delete(ctx, publicv1.ExternalIPsDeleteRequest_builder{Id: externalIPId}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
		ipResponse, err := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{Id: externalIPId}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(ipResponse.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeFalse())

		err = removeExternalIPAttachmentFinalizerWithRetry(ctx, privateAttachmentsClient, attachmentID, heldFinalizer)
		if grpcstatus.Code(err) == grpccodes.NotFound {
			_, attachmentGetErr := privateAttachmentsClient.Get(ctx,
				privatev1.ExternalIPAttachmentsGetRequest_builder{Id: attachmentID}.Build())
			Expect(grpcstatus.Code(attachmentGetErr)).To(Equal(grpccodes.NotFound),
				"NotFound while releasing the finalizer must mean the attachment is gone, not that another resource disappeared")
		} else {
			Expect(err).NotTo(HaveOccurred())
			Expect(cleanupExternalIPAttachmentFixture(ctx, privateAttachmentsClient, attachmentID)).To(Succeed())
		}

		_, err = externalIPsClient.Delete(ctx, publicv1.ExternalIPsDeleteRequest_builder{Id: externalIPId}.Build())
		Expect(err).NotTo(HaveOccurred())
	})

	It("Rejects duplicate attachment for same ExternalIP", func() {
		attachmentId1 := fmt.Sprintf("test-att-%s", uuid.New())
		_, err := attachmentsClient.Create(ctx, publicv1.ExternalIPAttachmentsCreateRequest_builder{
			Object: publicv1.ExternalIPAttachment_builder{
				Id: attachmentId1,
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-att-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp:     publicv1.ExternalIPLocalReference_builder{Id: externalIPId}.Build(),
					Cluster:        publicv1.ClusterLocalReference_builder{Id: clusterId}.Build(),
					TargetEndpoint: publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		attachmentIDs = append(attachmentIDs, attachmentId1)
		DeferCleanup(func(cleanupCtx context.Context) {
			deleteExternalIPAttachmentFixtureAndWait(cleanupCtx, attachmentsClient, privateAttachmentsClient, attachmentId1)
		})

		attachmentId2 := fmt.Sprintf("test-att-%s", uuid.New())
		_, err = attachmentsClient.Create(ctx, publicv1.ExternalIPAttachmentsCreateRequest_builder{
			Object: publicv1.ExternalIPAttachment_builder{
				Id: attachmentId2,
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-att-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp:     publicv1.ExternalIPLocalReference_builder{Id: externalIPId}.Build(),
					Cluster:        publicv1.ClusterLocalReference_builder{Id: clusterId}.Build(),
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
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-att-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp:     publicv1.ExternalIPLocalReference_builder{Id: "nonexistent-ip"}.Build(),
					Cluster:        publicv1.ClusterLocalReference_builder{Id: clusterId}.Build(),
					TargetEndpoint: publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
	})

	It("Rejects attachment with non-ALLOCATED ExternalIP", func() {
		pendingIPId := fmt.Sprintf("test-ip-%s", uuid.New())
		additionalExternalIPIDs = append(additionalExternalIPIDs, pendingIPId)
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
		DeferCleanup(func(cleanupCtx context.Context) {
			deleteExternalIPFixtureAndWait(cleanupCtx, externalIPsClient, privateExternalIPsClient, pendingIPId)
		})
		Eventually(func(g Gomega) {
			resp, err := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
				Id: pendingIPId,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetHub()).To(Equal(networkClassHubID))
		}, time.Minute, time.Second).Should(Succeed())

		attachmentId := fmt.Sprintf("test-att-%s", uuid.New())
		_, err = attachmentsClient.Create(ctx, publicv1.ExternalIPAttachmentsCreateRequest_builder{
			Object: publicv1.ExternalIPAttachment_builder{
				Id: attachmentId,
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-att-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp:     publicv1.ExternalIPLocalReference_builder{Id: pendingIPId}.Build(),
					Cluster:        publicv1.ClusterLocalReference_builder{Id: clusterId}.Build(),
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
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-att-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp: publicv1.ExternalIPLocalReference_builder{Id: externalIPId}.Build(),
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
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-att-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp: publicv1.ExternalIPLocalReference_builder{Id: externalIPId}.Build(),
					Cluster:    publicv1.ClusterLocalReference_builder{Id: clusterId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
	})

	It("settles and clears ExternalIP attachment output through child feedback", func() {
		attachmentId := fmt.Sprintf("test-att-%s", uuid.New())
		_, err := attachmentsClient.Create(ctx, publicv1.ExternalIPAttachmentsCreateRequest_builder{
			Object: publicv1.ExternalIPAttachment_builder{
				Id: attachmentId,
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-att-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp:     publicv1.ExternalIPLocalReference_builder{Id: externalIPId}.Build(),
					Cluster:        publicv1.ClusterLocalReference_builder{Id: clusterId}.Build(),
					TargetEndpoint: publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		attachmentIDs = append(attachmentIDs, attachmentId)
		DeferCleanup(func() {
			Expect(cleanupExternalIPAttachmentFixture(ctx, privateAttachmentsClient, attachmentId)).To(Succeed())
		})
		attachmentResponse, err := privateAttachmentsClient.Get(ctx, privatev1.ExternalIPAttachmentsGetRequest_builder{Id: attachmentId}.Build())
		Expect(err).ToNot(HaveOccurred())
		attachment := attachmentResponse.GetObject()
		attachment.GetStatus().SetState(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_READY)
		attachment.GetStatus().SetStateTransitionTime(timestamppb.Now())
		_, err = privateAttachmentsClient.Update(ctx, privatev1.ExternalIPAttachmentsUpdateRequest_builder{
			Object: attachment,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
				"status.state", "status.state_transition_time",
			}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		ipResp, err := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
			Id: externalIPId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(ipResp.GetObject().GetStatus().GetAttached()).To(BeTrue())
		Expect(ipResp.GetObject().GetStatus().GetAttribution().GetCluster().GetId()).To(Equal(clusterId))
		Expect(ipResp.GetObject().GetStatus().GetAttachmentTransitionTime()).ToNot(BeNil())

		deleteExternalIPAttachmentFixtureAndWait(ctx, attachmentsClient, privateAttachmentsClient, attachmentId)

		ipResp, err = privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
			Id: externalIPId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(ipResp.GetObject().GetStatus().GetAttached()).To(BeFalse())
	})
})
