/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
    10|"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// BMI automatic ExternalIP integration coverage (OSAC-4984).
//
// Prerequisites (installer Kind / SUITE=fulfillment):
//   - Fulfillment service with BMaaS APIs enabled
//   - Ability to create ExternalIPPool resources and mark them READY via private Update
//   - No fabric manager required for Pending reservation / fail-closed cases
//
// Async Ready / dataplane is covered by the lightweight e2e smoke, not this suite.
// Mid-sequence fault injection (IT-S3) is omitted here; unit coverage lives with OSAC-4982.

const (
	bmiAutoCreatedLabel    = "osac.openshift.io/auto-created"
	bmiAutoCreatedForLabel = "osac.openshift.io/auto-created-for"
	bmiOwnerReferenceAnno  = "osac.openshift.io/owner-reference"
)

var _ = Describe("BMI auto ExternalIP", Ordered, Serial, Label("bmaas", "networking"), func() {
	var (
		ctx                                 context.Context
		bareMetalInstancesClient            publicv1.BareMetalInstancesClient
		privateBareMetalInstancesClient     privatev1.BareMetalInstancesClient
		bareMetalInstanceTemplatesClient    privatev1.BareMetalInstanceTemplatesClient
		bareMetalInstanceCatalogItemsClient privatev1.BareMetalInstanceCatalogItemsClient
		bareMetalInstanceTypesClient        privatev1.BareMetalInstanceTypesClient
		diskImagesClient                    privatev1.DiskImagesClient
		poolsClient                         privatev1.ExternalIPPoolsClient
		externalIPsClient                   publicv1.ExternalIPsClient
		privateExternalIPsClient            privatev1.ExternalIPsClient
		attachmentsClient                   publicv1.ExternalIPAttachmentsClient
		virtualNetworksClient               publicv1.VirtualNetworksClient

		templateId         string
		catalogItemId      string
		instanceTypeId     string
		defaultDiskImageId string
	)

	BeforeAll(func() {
		ctx = context.Background()
		bareMetalInstancesClient = publicv1.NewBareMetalInstancesClient(tool.ExternalView().UserConn())
		privateBareMetalInstancesClient = privatev1.NewBareMetalInstancesClient(tool.InternalView().AdminConn())
		bareMetalInstanceTemplatesClient = privatev1.NewBareMetalInstanceTemplatesClient(tool.InternalView().AdminConn())
		bareMetalInstanceCatalogItemsClient = privatev1.NewBareMetalInstanceCatalogItemsClient(tool.InternalView().AdminConn())
		bareMetalInstanceTypesClient = privatev1.NewBareMetalInstanceTypesClient(tool.InternalView().AdminConn())
		diskImagesClient = privatev1.NewDiskImagesClient(tool.InternalView().AdminConn())
		poolsClient = privatev1.NewExternalIPPoolsClient(tool.InternalView().AdminConn())
		externalIPsClient = publicv1.NewExternalIPsClient(tool.ExternalView().UserConn())
		privateExternalIPsClient = privatev1.NewExternalIPsClient(tool.InternalView().AdminConn())
		attachmentsClient = publicv1.NewExternalIPAttachmentsClient(tool.ExternalView().UserConn())
		virtualNetworksClient = publicv1.NewVirtualNetworksClient(tool.ExternalView().UserConn())

		templateResp, err := bareMetalInstanceTemplatesClient.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Id:          fmt.Sprintf("test_autoeip_tpl_%s", strings.ReplaceAll(uuid.New(), "-", "_")),
				Title:       "BMI auto ExternalIP template",
				Description: "Template for BMI auto ExternalIP IT.",
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-autoeip-tpl-%s", uuid.New()[24:32]),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		templateId = templateResp.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, _ = bareMetalInstanceTemplatesClient.Delete(ctx, privatev1.BareMetalInstanceTemplatesDeleteRequest_builder{
				Id: templateId,
			}.Build())
		})

		diskImageResp, err := diskImagesClient.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
			Object: privatev1.DiskImage_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-autoeip-di-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: privatev1.DiskImageSpec_builder{
					SourceType:    privatev1.SourceType_SOURCE_TYPE_REGISTRY,
					SourceRef:     "quay.io/test/rhel9:latest",
					GuestOsFamily: privatev1.GuestOSFamily_GUEST_OS_FAMILY_LINUX,
					Architecture: []privatev1.Architecture{
						privatev1.Architecture_ARCHITECTURE_AMD64,
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		defaultDiskImageId = diskImageResp.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, _ = diskImagesClient.Delete(ctx, privatev1.DiskImagesDeleteRequest_builder{
				Id: defaultDiskImageId,
			}.Build())
		})

		catalogResp, err := bareMetalInstanceCatalogItemsClient.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
			Object: privatev1.BareMetalInstanceCatalogItem_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-autoeip-ci-%s", uuid.New()[24:32]),
				}.Build(),
				Title:     "BMI auto ExternalIP catalog item",
				Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: templateId}.Build(),
				Published: true,
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		catalogItemId = catalogResp.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, _ = bareMetalInstanceCatalogItemsClient.Delete(ctx, privatev1.BareMetalInstanceCatalogItemsDeleteRequest_builder{
				Id: catalogItemId,
			}.Build())
		})

		instanceTypeResp, err := bareMetalInstanceTypesClient.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceType_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-autoeip-it-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.BareMetalInstanceTypeSpec_builder{
					Hardware: privatev1.BareMetalHardwareSpec_builder{
						Cpu: privatev1.BareMetalCPUSpec_builder{
							Cores:          4,
							Architecture:   "x86_64",
							ThreadsPerCore: 2,
						}.Build(),
						Memory: privatev1.BareMetalMemorySpec_builder{
							TotalGb: 16,
						}.Build(),
					}.Build(),
					HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
						MatchLabels: map[string]string{
							"osac.openshift.io/host-type": "compute",
						},
					}.Build(),
					Description: "BMI auto ExternalIP instance type.",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		instanceTypeId = instanceTypeResp.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, _ = bareMetalInstanceTypesClient.Delete(ctx, privatev1.BareMetalInstanceTypesDeleteRequest_builder{
				Id: instanceTypeId,
			}.Build())
		})
	})

	createReadyPool := func(available int64) string {
		GinkgoHelper()
		poolId := fmt.Sprintf("test-autoeip-pool-%s", uuid.New())
		_, err := poolsClient.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Id: poolId,
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-autoeip-pool-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs:    []string{uniqueCIDR()},
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		Eventually(func(g Gomega) {
			resp, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolId}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())

		getResp, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolId}.Build())
		Expect(err).ToNot(HaveOccurred())
		pool := getResp.GetObject()
		total := pool.GetStatus().GetTotal()
		if total < available {
			total = available
		}
		pool.SetStatus(privatev1.ExternalIPPoolStatus_builder{
			State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
			Total:     total,
			Available: available,
			Allocated: total - available,
		}.Build())
		_, err = poolsClient.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
			Object:     pool,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		DeferCleanup(func(ctx context.Context) {
			_, _ = poolsClient.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{Id: poolId}.Build())
		})
		return poolId
	}

	// zeroOtherReadyPools sets available=0 on every READY pool except keepPoolID,
	// restoring prior counters on cleanup. Makes SelectExternalIPPool deterministic.
	zeroOtherReadyPools := func(keepPoolID string) {
		GinkgoHelper()
		listResp, err := poolsClient.List(ctx, privatev1.ExternalIPPoolsListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())

		type snapshot struct {
			id        string
			available int64
			allocated int64
			total     int64
			state     privatev1.ExternalIPPoolState
		}
		var restored []snapshot

		for _, pool := range listResp.GetItems() {
			if pool.GetId() == keepPoolID {
				continue
			}
			if pool.GetStatus().GetState() != privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY {
				continue
			}
			restored = append(restored, snapshot{
				id:        pool.GetId(),
				available: pool.GetStatus().GetAvailable(),
				allocated: pool.GetStatus().GetAllocated(),
				total:     pool.GetStatus().GetTotal(),
				state:     pool.GetStatus().GetState(),
			})
			pool.SetStatus(privatev1.ExternalIPPoolStatus_builder{
				State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
				Total:     pool.GetStatus().GetTotal(),
				Available: 0,
				Allocated: pool.GetStatus().GetTotal(),
			}.Build())
			_, err = poolsClient.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
				Object:     pool,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		}

		DeferCleanup(func(ctx context.Context) {
			for _, snap := range restored {
				getResp, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: snap.id}.Build())
				if err != nil {
					continue
				}
				p := getResp.GetObject()
				p.SetStatus(privatev1.ExternalIPPoolStatus_builder{
					State:     snap.state,
					Total:     snap.total,
					Available: snap.available,
					Allocated: snap.allocated,
				}.Build())
				_, _ = poolsClient.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
					Object:     p,
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status"}},
				}.Build())
			}
		})
	}

	createBMIWithAutoEIP := func(nameSuffix string) (*publicv1.BareMetalInstancesCreateResponse, error) {
		GinkgoHelper()
		return bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-autoeip-bmi-%s", nameSuffix),
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem:              publicv1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemId}.Build(),
					InstanceType:             publicv1.BareMetalInstanceTypeLocalReference_builder{Id: instanceTypeId}.Build(),
					SshPublicKey:             new(bmiTestSSHPublicKey),
					DiskImage:                publicv1.DiskImageReference_builder{Id: defaultDiskImageId}.Build(),
					AutoExternalIpAttachment: proto.Bool(true),
				}.Build(),
			}.Build(),
		}.Build())
	}

	listAutoEIPs := func(bmiID string) []*publicv1.ExternalIP {
		GinkgoHelper()
		filter := fmt.Sprintf("this.metadata.labels['%s'] == '%s'", bmiAutoCreatedForLabel, bmiID)
		resp, err := externalIPsClient.List(ctx, publicv1.ExternalIPsListRequest_builder{
			Filter: &filter,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		return resp.GetItems()
	}

	listAutoAttachments := func(bmiID string) []*publicv1.ExternalIPAttachment {
		GinkgoHelper()
		filter := fmt.Sprintf("this.metadata.labels['%s'] == '%s'", bmiAutoCreatedForLabel, bmiID)
		resp, err := attachmentsClient.List(ctx, publicv1.ExternalIPAttachmentsListRequest_builder{
			Filter: &filter,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		return resp.GetItems()
	}

	deleteBMIAndWaitGone := func(bmiID string) {
		GinkgoHelper()
		_, err := privateBareMetalInstancesClient.Delete(ctx, privatev1.BareMetalInstancesDeleteRequest_builder{
			Id: bmiID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Eventually(func(g Gomega) {
			_, err := privateBareMetalInstancesClient.Get(ctx, privatev1.BareMetalInstancesGetRequest_builder{
				Id: bmiID,
			}.Build())
			g.Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			g.Expect(ok).To(BeTrue())
			g.Expect(status.Code()).To(Equal(grpccodes.NotFound))
		}, 2*time.Minute, time.Second).Should(Succeed())
	}

	It("creates Pending ExternalIP and ExternalIPAttachment with correlation metadata", func() {
		poolID := createReadyPool(64)
		zeroOtherReadyPools(poolID)

		poolBefore, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolID}.Build())
		Expect(err).ToNot(HaveOccurred())
		availableBefore := poolBefore.GetObject().GetStatus().GetAvailable()

		createResp, err := createBMIWithAutoEIP(uuid.New()[24:32])
		Expect(err).ToNot(HaveOccurred())
		bmi := createResp.GetObject()
		bmiID := bmi.GetId()
		Expect(bmi.GetSpec().GetAutoExternalIpAttachment()).To(BeTrue())
		DeferCleanup(func() {
			_, _ = privateBareMetalInstancesClient.Delete(ctx, privatev1.BareMetalInstancesDeleteRequest_builder{
				Id: bmiID,
			}.Build())
		})

		eips := listAutoEIPs(bmiID)
		Expect(eips).To(HaveLen(1))
		eip := eips[0]
		Expect(eip.GetStatus().GetState()).To(Equal(publicv1.ExternalIPState_EXTERNAL_IP_STATE_PENDING))
		Expect(eip.GetMetadata().GetLabels()[bmiAutoCreatedLabel]).To(Equal("true"))
		Expect(eip.GetMetadata().GetLabels()[bmiAutoCreatedForLabel]).To(Equal(bmiID))
		Expect(eip.GetMetadata().GetAnnotations()[bmiOwnerReferenceAnno]).To(Equal(bmiID))
		Expect(eip.GetMetadata().GetTenant()).To(Equal(bmi.GetMetadata().GetTenant()))
		Expect(eip.GetSpec().GetPool().GetId()).To(Equal(poolID))

		atts := listAutoAttachments(bmiID)
		Expect(atts).To(HaveLen(1))
		att := atts[0]
		Expect(att.GetStatus().GetState()).To(
			Equal(publicv1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING))
		Expect(att.GetMetadata().GetLabels()[bmiAutoCreatedLabel]).To(Equal("true"))
		Expect(att.GetMetadata().GetLabels()[bmiAutoCreatedForLabel]).To(Equal(bmiID))
		Expect(att.GetMetadata().GetAnnotations()[bmiOwnerReferenceAnno]).To(Equal(bmiID))
		Expect(att.GetSpec().GetExternalIp().GetId()).To(Equal(eip.GetId()))
		Expect(att.GetSpec().GetBaremetalInstance().GetId()).To(Equal(bmiID))

		bmiFilter := fmt.Sprintf("this.spec.baremetal_instance.id == '%s'", bmiID)
		listByBMI, err := attachmentsClient.List(ctx, publicv1.ExternalIPAttachmentsListRequest_builder{
			Filter: &bmiFilter,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listByBMI.GetItems()).ToNot(BeEmpty())
		found := false
		for _, item := range listByBMI.GetItems() {
			if item.GetMetadata().GetLabels()[bmiAutoCreatedLabel] == "true" &&
				item.GetSpec().GetBaremetalInstance().GetId() == bmiID {
				found = true
				break
			}
		}
		Expect(found).To(BeTrue(), "auto attachment should be listable by baremetal_instance.id")

		poolAfter, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(poolAfter.GetObject().GetStatus().GetAvailable()).To(Equal(availableBefore - 1))
	})

	It("cascade-deletes auto children, restores capacity, and preserves manual ExternalIP", func() {
		poolID := createReadyPool(64)
		zeroOtherReadyPools(poolID)

		createResp, err := createBMIWithAutoEIP(uuid.New()[24:32])
		Expect(err).ToNot(HaveOccurred())
		bmiID := createResp.GetObject().GetId()
		eips := listAutoEIPs(bmiID)
		Expect(eips).To(HaveLen(1))
		autoEIPID := eips[0].GetId()
		atts := listAutoAttachments(bmiID)
		Expect(atts).To(HaveLen(1))
		autoAttID := atts[0].GetId()

		poolMid, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolID}.Build())
		Expect(err).ToNot(HaveOccurred())
		availableAfterCreate := poolMid.GetObject().GetStatus().GetAvailable()

		manualIPID := fmt.Sprintf("test-manual-eip-%s", uuid.New())
		_, err = externalIPsClient.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
			Object: publicv1.ExternalIP_builder{
				Id: manualIPID,
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-manual-eip-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ExternalIPSpec_builder{
					Pool: publicv1.ExternalIPPoolReference_builder{Id: poolID}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(ctx context.Context) {
			_, _ = externalIPsClient.Delete(ctx, publicv1.ExternalIPsDeleteRequest_builder{Id: manualIPID}.Build())
		})

		var defaultVNID string
		vnList, err := virtualNetworksClient.List(ctx, publicv1.VirtualNetworksListRequest_builder{}.Build())
		if err == nil && len(vnList.GetItems()) > 0 {
			defaultVNID = vnList.GetItems()[0].GetId()
		}

		deleteBMIAndWaitGone(bmiID)

		Eventually(func(g Gomega) {
			g.Expect(listAutoEIPs(bmiID)).To(BeEmpty())
			g.Expect(listAutoAttachments(bmiID)).To(BeEmpty())
		}, time.Minute, time.Second).Should(Succeed())

		_, err = externalIPsClient.Get(ctx, publicv1.ExternalIPsGetRequest_builder{Id: autoEIPID}.Build())
		Expect(err).To(HaveOccurred())
		_, err = attachmentsClient.Get(ctx, publicv1.ExternalIPAttachmentsGetRequest_builder{Id: autoAttID}.Build())
		Expect(err).To(HaveOccurred())

		Eventually(func(g Gomega) {
			poolAfter, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolID}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			// Manual EIP still holds one slot; releasing auto children restores to post-auto-create level.
			g.Expect(poolAfter.GetObject().GetStatus().GetAvailable()).To(Equal(availableAfterCreate))
		}, time.Minute, time.Second).Should(Succeed())

		manualGet, err := externalIPsClient.Get(ctx, publicv1.ExternalIPsGetRequest_builder{Id: manualIPID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(manualGet.GetObject().GetId()).To(Equal(manualIPID))

		if defaultVNID != "" {
			_, err = virtualNetworksClient.Get(ctx, publicv1.VirtualNetworksGetRequest_builder{Id: defaultVNID}.Build())
			Expect(err).ToNot(HaveOccurred(), "default/tenant VirtualNetwork must survive BMI delete")
		}
	})

	It("rejects create when no READY pool has capacity and leaves no leaked BMI", func() {
		poolID := createReadyPool(0)
		zeroOtherReadyPools(poolID)

		bmiListBefore, err := bareMetalInstancesClient.List(ctx, publicv1.BareMetalInstancesListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		beforeCount := len(bmiListBefore.GetItems())
		beforeIDs := map[string]struct{}{}
		for _, item := range bmiListBefore.GetItems() {
			beforeIDs[item.GetId()] = struct{}{}
		}

		poolBefore, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolID}.Build())
		Expect(err).ToNot(HaveOccurred())

		_, err = createBMIWithAutoEIP(uuid.New()[24:32])
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
		Expect(status.Message()).To(ContainSubstring("auto_external_ip_attachment"))

		bmiListAfter, err := bareMetalInstancesClient.List(ctx, publicv1.BareMetalInstancesListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(bmiListAfter.GetItems()).To(HaveLen(beforeCount),
			"BMI must not leak when auto ExternalIP provisioning fails (requires OSAC-4982 rollback)")
		for _, item := range bmiListAfter.GetItems() {
			_, known := beforeIDs[item.GetId()]
			Expect(known).To(BeTrue(), "unexpected BMI %s after failed auto ExternalIP create", item.GetId())
		}

		poolAfter, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(poolAfter.GetObject().GetStatus().GetAvailable()).To(
			Equal(poolBefore.GetObject().GetStatus().GetAvailable()))
		Expect(poolAfter.GetObject().GetStatus().GetAllocated()).To(
			Equal(poolBefore.GetObject().GetStatus().GetAllocated()))
	})

	It("allows exactly one concurrent winner when pool available=1", func() {
		poolID := createReadyPool(1)
		zeroOtherReadyPools(poolID)

		var (
			wg        sync.WaitGroup
			mu        sync.Mutex
			successes []*publicv1.BareMetalInstance
			failures  []error
		)
		wg.Add(2)
		for i := 0; i < 2; i++ {
			go func(i int) {
				defer wg.Done()
				defer GinkgoRecover()
				resp, err := createBMIWithAutoEIP(fmt.Sprintf("%s-%d", uuid.New()[24:32], i))
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					failures = append(failures, err)
					return
				}
				successes = append(successes, resp.GetObject())
			}(i)
		}
		wg.Wait()

		Expect(successes).To(HaveLen(1), "exactly one create should succeed")
		Expect(failures).To(HaveLen(1), "exactly one create should fail")
		Expect(grpcstatus.Code(failures[0])).To(Equal(grpccodes.FailedPrecondition))

		winnerID := successes[0].GetId()
		DeferCleanup(func() {
			deleteBMIAndWaitGone(winnerID)
		})

		Expect(listAutoEIPs(winnerID)).To(HaveLen(1))
		Expect(listAutoAttachments(winnerID)).To(HaveLen(1))

		poolAfter, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(poolAfter.GetObject().GetStatus().GetAvailable()).To(Equal(int64(0)))
		Expect(poolAfter.GetObject().GetStatus().GetAllocated()).To(BeNumerically("<=", poolAfter.GetObject().GetStatus().GetTotal()))
	})

	It("surfaces Failed ExternalIP without deleting the BMI", func() {
		poolID := createReadyPool(64)
		zeroOtherReadyPools(poolID)

		createResp, err := createBMIWithAutoEIP(uuid.New()[24:32])
		Expect(err).ToNot(HaveOccurred())
		bmiID := createResp.GetObject().GetId()
		DeferCleanup(func() {
			deleteBMIAndWaitGone(bmiID)
		})

		eips := listAutoEIPs(bmiID)
		Expect(eips).To(HaveLen(1))
		eipID := eips[0].GetId()

		privEIP, err := privateExternalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{Id: eipID}.Build())
		Expect(err).ToNot(HaveOccurred())
		eip := privEIP.GetObject()
		msg := "fabric allocate failed (IT injected)"
		eip.SetStatus(privatev1.ExternalIPStatus_builder{
			State:   privatev1.ExternalIPState_EXTERNAL_IP_STATE_FAILED,
			Message: &msg,
		}.Build())
		_, err = privateExternalIPsClient.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
			Object:     eip,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		_, err = bareMetalInstancesClient.Get(ctx, publicv1.BareMetalInstancesGetRequest_builder{Id: bmiID}.Build())
		Expect(err).ToNot(HaveOccurred())

		pubEIP, err := externalIPsClient.Get(ctx, publicv1.ExternalIPsGetRequest_builder{Id: eipID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(pubEIP.GetObject().GetStatus().GetState()).To(Equal(publicv1.ExternalIPState_EXTERNAL_IP_STATE_FAILED))
		Expect(pubEIP.GetObject().GetStatus().GetMessage()).ToNot(BeEmpty())

		atts := listAutoAttachments(bmiID)
		Expect(atts).To(HaveLen(1))
		Expect(atts[0].GetStatus().GetState()).ToNot(
			Equal(publicv1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_READY))
	})
})
