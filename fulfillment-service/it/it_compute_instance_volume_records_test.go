/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package it

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers/computeinstance"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	diskRegistrationVolumeHoldFinalizer = "osac-test.osac.io/hold-volume-record-cleanup"
	diskRegistrationPlaceholderNode     = "osac-volume-registration-pending"
	diskRegistrationIsolationHubID      = "00000000-0000-4000-8000-000000000000"
)

var _ = Describe("ComputeInstance Volume registration API contract", func() {
	var (
		network             catalogItemNetworkFixture
		templateID          string
		computeInstances    privatev1.ComputeInstancesClient
		volumes             privatev1.VolumesClient
		storageTierName     string
		computeInstanceSpec *publicv1.ComputeInstanceSpec
	)

	// Ginkgo cancels this context when BeforeEach returns; each It uses its own.
	BeforeEach(func(ctx context.Context) {
		network = createCatalogItemNetworkFixture(ctx, usersGroup, "")
		instanceTypeID := createCatalogItemComputeInstanceTypeFixture(ctx)
		diskImage := createCatalogItemDiskImageFixture(ctx, "shared", catalogItemFixtureName())
		storageTierID := createCatalogItemStorageTierFixture(ctx)
		storageTiers := privatev1.NewStorageTiersClient(tool.InternalView().AdminConn())
		tierResponse, err := storageTiers.Get(ctx, privatev1.StorageTiersGetRequest_builder{Id: storageTierID}.Build())
		Expect(err).NotTo(HaveOccurred())
		storageTierName = tierResponse.GetObject().GetMetadata().GetName()
		templateID = createCatalogItemComputeInstanceTemplateFixture(ctx, privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			InstanceType: privatev1.InstanceTypeReference_builder{Id: instanceTypeID}.Build(),
			DiskImage:    privatev1.DiskImageReference_builder{Id: diskImage.GetId()}.Build(),
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib:     new(int32(30)),
				StorageTier: privatev1.StorageTierReference_builder{Id: storageTierID}.Build(),
			}.Build(),
			RunStrategy: new(privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS),
		}.Build(), nil)
		computeInstances = privatev1.NewComputeInstancesClient(tool.InternalView().ControllerConn())
		volumes = privatev1.NewVolumesClient(tool.InternalView().ControllerConn())
		computeInstanceSpec = publicv1.ComputeInstanceSpec_builder{
			Template:           publicv1.ComputeInstanceTemplateReference_builder{Id: templateID}.Build(),
			NetworkAttachments: []*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()},
		}.Build()
	})

	It("recovers the original record after a committed Create response is lost", func(ctx context.Context) {
		instance := createIsolatedDiskRegistrationInstance(ctx, templateID, network)
		parent, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())

		volumeName := "ci-" + instance.GetId() + "-boot"
		filter := diskRegistrationAPIFilter(volumeName, parent.GetMetadata().GetTenant(), parent.GetMetadata().GetProject())
		before, err := volumes.List(ctx, privatev1.VolumesListRequest_builder{Filter: &filter}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(before.GetTotal()).To(BeZero())

		var committedID string
		DeferCleanup(func(cleanupCtx context.Context) {
			if committedID == "" {
				return
			}
			_, cleanupErr := volumes.Delete(cleanupCtx,
				privatev1.VolumesDeleteRequest_builder{Id: committedID}.Build())
			Expect(cleanupErr == nil || status.Code(cleanupErr) == codes.NotFound).To(BeTrue())
		})
		faultingVolumes := &diskRegistrationLostResponseVolumesClient{
			VolumesClient: volumes,
			hideFirstList: true,
			afterCreate: func(response *privatev1.VolumesCreateResponse) {
				Expect(response.GetObject().GetMetadata().GetCreator()).To(Equal("system"))
				committedID = response.GetObject().GetId()
				observed, getErr := volumes.Get(ctx, privatev1.VolumesGetRequest_builder{Id: committedID}.Build())
				Expect(getErr).NotTo(HaveOccurred())
				Expect(observed.GetObject().GetMetadata().GetCreator()).To(Equal("system"))
				Expect(observed.GetObject().GetMetadata().GetName()).To(Equal(volumeName))
				Expect(observed.GetObject().GetMetadata().GetTenant()).To(Equal(parent.GetMetadata().GetTenant()))
				Expect(observed.GetObject().GetMetadata().GetProject()).To(Equal(parent.GetMetadata().GetProject()))
				Expect(observed.GetObject().GetSpec().GetStorageTier()).To(Equal(storageTierName))
				Expect(observed.GetObject().GetSpec().GetSizeGib()).To(Equal(int64(30)))
				Expect(observed.GetObject().GetSpec().GetTopology().GetSegments()).To(Equal(
					map[string]string{"osac.io/node": diskRegistrationPlaceholderNode}))
			},
		}
		err = computeinstance.ReconcileDiskRegistrations(ctx, computeInstances, faultingVolumes, instance.GetId())
		Expect(err).To(HaveOccurred())
		Expect(committedID).NotTo(BeEmpty())
		Expect(faultingVolumes.createCalls).To(Equal(1))
		Expect(faultingVolumes.listCalls).To(Equal(1))

		uncertain, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(diskRegistrationState(uncertain, "boot")).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN))
		Expect(uncertain.GetStatus().GetVolumes()).To(BeEmpty())
		Expect(uncertain.GetStatus().GetConditions()).To(ContainElement(WithTransform(
			func(condition *privatev1.ComputeInstanceCondition) string { return condition.GetReason() },
			Equal("VolumeRegistrationPending"))))

		By("restarting from the persisted parent ID and recovering through the real private List API")
		err = computeinstance.ReconcileDiskRegistrations(ctx, computeInstances, volumes, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		resolved, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(diskRegistrationState(resolved, "boot")).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED))
		Expect(resolved.GetStatus().GetVolumes()).To(HaveLen(1))
		Expect(resolved.GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal(committedID))

		after, err := volumes.List(ctx, privatev1.VolumesListRequest_builder{Filter: &filter}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(after.GetTotal()).To(Equal(int32(1)))
		Expect(after.GetItems()[0].GetId()).To(Equal(committedID))
	})

	It("recovers a matching same-name record through real API uniqueness", func(ctx context.Context) {
		instance := createIsolatedDiskRegistrationInstance(ctx, templateID, network)
		parent, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		name := "ci-" + instance.GetId() + "-boot"
		filter := diskRegistrationAPIFilter(name, parent.GetMetadata().GetTenant(), parent.GetMetadata().GetProject())

		existing, err := volumes.Create(ctx, diskRegistrationVolumeRequest(parent, name, storageTierName, 30))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			_, cleanupErr := volumes.Delete(cleanupCtx,
				privatev1.VolumesDeleteRequest_builder{Id: existing.GetObject().GetId()}.Build())
			Expect(cleanupErr == nil || status.Code(cleanupErr) == codes.NotFound).To(BeTrue())
		})

		countedVolumes := &diskRegistrationCountingVolumesClient{VolumesClient: volumes}
		err = computeinstance.ReconcileDiskRegistrations(ctx, computeInstances, countedVolumes, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(countedVolumes.createCalls).To(Equal(1))
		Expect(countedVolumes.listCalls).To(Equal(1))

		persisted, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.GetStatus().GetVolumes()).To(HaveLen(1))
		Expect(persisted.GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal(existing.GetObject().GetId()))
		Expect(diskRegistrationState(persisted, "boot")).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED))
		all, err := volumes.List(ctx, privatev1.VolumesListRequest_builder{Filter: &filter}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(all.GetTotal()).To(Equal(int32(1)))
	})

	It("does not adopt a conflicting same-name record from the real API", func(ctx context.Context) {
		instance := createIsolatedDiskRegistrationInstance(ctx, templateID, network)
		parent, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		name := "ci-" + instance.GetId() + "-boot"
		filter := diskRegistrationAPIFilter(name, parent.GetMetadata().GetTenant(), parent.GetMetadata().GetProject())

		existing, err := volumes.Create(ctx, diskRegistrationVolumeRequest(parent, name, storageTierName, 31))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			seedID := existing.GetObject().GetId()
			_, cleanupErr := volumes.Delete(cleanupCtx,
				privatev1.VolumesDeleteRequest_builder{Id: seedID}.Build())
			Expect(cleanupErr == nil || status.Code(cleanupErr) == codes.NotFound).To(BeTrue())
			Eventually(func(g Gomega) {
				_, getErr := volumes.Get(cleanupCtx, privatev1.VolumesGetRequest_builder{Id: seedID}.Build())
				g.Expect(status.Code(getErr)).To(Equal(codes.NotFound))
			}, 2*time.Minute, time.Second).Should(Succeed())

			// The test reconciler has returned. Verify its conflicting Create left
			// no saved ID or other same-name record before settling this fixture.
			remaining, listErr := volumes.List(cleanupCtx,
				privatev1.VolumesListRequest_builder{Filter: &filter}.Build())
			Expect(listErr).NotTo(HaveOccurred())
			Expect(remaining.GetTotal()).To(BeZero())
			current, getErr := getDiskRegistrationComputeInstance(cleanupCtx, computeInstances, instance.GetId())
			if status.Code(getErr) == codes.NotFound {
				return
			}
			Expect(getErr).NotTo(HaveOccurred())
			Expect(current.GetStatus().GetHub()).To(Equal(diskRegistrationIsolationHubID))
			unknownBoot := diskRegistrationState(current, "boot") ==
				privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN
			if unknownBoot {
				Expect(current.GetStatus().GetVolumes()).To(BeEmpty())
				Expect(current.GetStatus().GetVolumeRegistration()).To(HaveLen(1))
				assertNoDiskRegistrationComputeInstanceCR(cleanupCtx, instance.GetId())
			}

			publicInstances := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
			_, deleteErr := publicInstances.Delete(cleanupCtx,
				publicv1.ComputeInstancesDeleteRequest_builder{Id: instance.GetId()}.Build())
			Expect(deleteErr == nil || status.Code(deleteErr) == codes.NotFound).To(BeTrue())
			if !unknownBoot {
				return
			}

			Eventually(func(g Gomega) {
				deleting, getErr := getDiskRegistrationComputeInstance(cleanupCtx, computeInstances, instance.GetId())
				g.Expect(getErr).NotTo(HaveOccurred())
				g.Expect(deleting.GetMetadata().HasDeletionTimestamp()).To(BeTrue())
				g.Expect(deleting.GetStatus().GetHub()).To(Equal(diskRegistrationIsolationHubID))
				g.Expect(deleting.GetStatus().GetVolumes()).To(BeEmpty())
				registrations := deleting.GetStatus().GetVolumeRegistration()
				g.Expect(registrations).To(HaveLen(1))
				g.Expect(registrations[0].GetDiskKey()).To(Equal("boot"))
				g.Expect(registrations[0].GetState()).To(Equal(
					privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN))
				registrations[0].SetState(privatev1.ComputeInstanceVolumeRegistration_STATE_NON_COMMITTED_FAILURE)
				_, updateErr := computeInstances.Update(cleanupCtx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object:     deleting,
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.volume_registration"}},
					Lock:       true,
				}.Build())
				g.Expect(updateErr).NotTo(HaveOccurred())
			}, time.Minute, time.Second).Should(Succeed())
			_, signalErr := computeInstances.Signal(cleanupCtx,
				privatev1.ComputeInstancesSignalRequest_builder{Id: instance.GetId()}.Build())
			Expect(signalErr == nil || status.Code(signalErr) == codes.NotFound).To(BeTrue())
			Eventually(func(g Gomega) {
				_, getErr := getDiskRegistrationComputeInstance(cleanupCtx, computeInstances, instance.GetId())
				g.Expect(status.Code(getErr)).To(Equal(codes.NotFound))
			}, 2*time.Minute, time.Second).Should(Succeed())
		})

		countedVolumes := &diskRegistrationCountingVolumesClient{VolumesClient: volumes}
		err = computeinstance.ReconcileDiskRegistrations(ctx, computeInstances, countedVolumes, instance.GetId())
		Expect(err).To(HaveOccurred())
		Expect(countedVolumes.createCalls).To(Equal(1))
		Expect(countedVolumes.listCalls).To(Equal(1))

		persisted, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.GetStatus().GetVolumes()).To(BeEmpty())
		Expect(diskRegistrationState(persisted, "boot")).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN))
		all, err := volumes.List(ctx, privatev1.VolumesListRequest_builder{Filter: &filter}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(all.GetTotal()).To(Equal(int32(1)))
		Expect(all.GetItems()[0].GetId()).To(Equal(existing.GetObject().GetId()))
	})

	It("allows only one concurrent reconciler to dispatch a same-name Create", func(ctx context.Context) {
		instance := createIsolatedDiskRegistrationInstance(ctx, templateID, network)
		parent, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		name := "ci-" + instance.GetId() + "-boot"
		filter := diskRegistrationAPIFilter(name, parent.GetMetadata().GetTenant(), parent.GetMetadata().GetProject())
		barrier := &diskRegistrationCreateBarrierVolumesClient{
			VolumesClient: volumes,
			entered:       make(chan struct{}, 1),
			release:       make(chan struct{}),
		}
		var releaseOnce sync.Once
		releaseCreate := func() { releaseOnce.Do(func() { close(barrier.release) }) }
		var createdID string
		barrier.afterCreate = func(response *privatev1.VolumesCreateResponse) {
			createdID = response.GetObject().GetId()
		}
		firstDone := make(chan struct{})
		DeferCleanup(func(cleanupCtx context.Context) {
			releaseCreate()
			select {
			case <-firstDone:
			case <-cleanupCtx.Done():
				Fail("timed out waiting for the concurrent disk registration to finish")
			}
			cleanupIDs := []string{createdID}
			if createdID == "" {
				response, listErr := volumes.List(cleanupCtx,
					privatev1.VolumesListRequest_builder{Filter: &filter}.Build())
				Expect(listErr).NotTo(HaveOccurred())
				for _, object := range response.GetItems() {
					metadata := object.GetMetadata()
					if metadata.GetName() == name &&
						metadata.GetTenant() == parent.GetMetadata().GetTenant() &&
						metadata.GetProject() == parent.GetMetadata().GetProject() {
						cleanupIDs = append(cleanupIDs, object.GetId())
					}
				}
			}
			for _, id := range cleanupIDs {
				if id == "" {
					continue
				}
				_, cleanupErr := volumes.Delete(cleanupCtx,
					privatev1.VolumesDeleteRequest_builder{Id: id}.Build())
				Expect(cleanupErr == nil || status.Code(cleanupErr) == codes.NotFound).To(BeTrue())
			}
		})

		firstResult := make(chan error, 1)
		go func() {
			defer close(firstDone)
			reconcileErr := computeinstance.ReconcileDiskRegistrations(ctx,
				computeInstances, barrier, instance.GetId())
			firstResult <- reconcileErr
		}()
		Eventually(barrier.entered, time.Minute).Should(Receive())

		secondErr := computeinstance.ReconcileDiskRegistrations(ctx,
			computeInstances, barrier, instance.GetId())
		Expect(secondErr).NotTo(HaveOccurred())
		pending, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(diskRegistrationState(pending, "boot")).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN))
		Expect(pending.GetStatus().GetVolumes()).To(BeEmpty())
		releaseCreate()

		var firstErr error
		Eventually(firstResult, time.Minute).Should(Receive(&firstErr))
		Expect(firstErr).NotTo(HaveOccurred())
		Expect(barrier.createCalls.Load()).To(Equal(int32(1)))
		all, err := volumes.List(ctx, privatev1.VolumesListRequest_builder{Filter: &filter}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(all.GetTotal()).To(Equal(int32(1)))
		persisted, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.GetStatus().GetVolumes()).To(HaveLen(1))
		Expect(diskRegistrationState(persisted, "boot")).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED))
		Expect(persisted.GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal(createdID))
	})

	It("keeps a committed attempted marker blocked after a pre-call interruption", func(ctx context.Context) {
		instance := createIsolatedDiskRegistrationInstance(ctx, templateID, network)
		parent, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		volumeName := "ci-" + instance.GetId() + "-boot"
		filter := diskRegistrationAPIFilter(volumeName, parent.GetMetadata().GetTenant(), parent.GetMetadata().GetProject())

		faultingComputeInstances := &diskRegistrationMarkerResponseLossClient{
			ComputeInstancesClient: computeInstances,
			afterMarker: func() {
				observed, getErr := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
				Expect(getErr).NotTo(HaveOccurred())
				Expect(diskRegistrationState(observed, "boot")).To(Equal(
					privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN))
				Expect(observed.GetStatus().GetConditions()).To(ContainElement(WithTransform(
					func(condition *privatev1.ComputeInstanceCondition) string { return condition.GetReason() },
					Equal("VolumeRegistrationPending"))))
				found, listErr := volumes.List(ctx, privatev1.VolumesListRequest_builder{Filter: &filter}.Build())
				Expect(listErr).NotTo(HaveOccurred())
				Expect(found.GetTotal()).To(BeZero())
			},
		}
		countedVolumes := &diskRegistrationCountingVolumesClient{VolumesClient: volumes}
		err = computeinstance.ReconcileDiskRegistrations(ctx, faultingComputeInstances, countedVolumes, instance.GetId())
		Expect(err).To(HaveOccurred())
		Expect(faultingComputeInstances.interrupted).To(BeTrue())
		Expect(countedVolumes.createCalls).To(BeZero())

		By("restarting from persisted state and allowing repeated complete empty List responses")
		for range 3 {
			err = computeinstance.ReconcileDiskRegistrations(ctx, computeInstances, countedVolumes, instance.GetId())
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(countedVolumes.createCalls).To(BeZero())
		Expect(countedVolumes.listCalls).To(Equal(3))
		retained, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(slices.Contains(retained.GetMetadata().GetFinalizers(), finalizers.Controller)).To(BeTrue())
		Expect(diskRegistrationState(retained, "boot")).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN))

		By("keeping the managed workload absent while the attempt is unresolved")
		assertNoDiskRegistrationComputeInstanceCR(ctx, instance.GetId())

		By("canceling before any Volume ID or managed workload exists")
		DeferCleanup(func(cleanupCtx context.Context) {
			// The uncertain marker deliberately retains the parent. This fixture
			// knows that Create was never dispatched, so it can clear that marker
			// after the assertions and let normal cancellation cleanup finish.
			released := false
			Eventually(func(g Gomega) {
				current, getErr := getDiskRegistrationComputeInstance(cleanupCtx, computeInstances, instance.GetId())
				if status.Code(getErr) == codes.NotFound {
					return
				}
				g.Expect(getErr).NotTo(HaveOccurred())
				if !current.GetMetadata().HasDeletionTimestamp() {
					return
				}
				for _, registration := range current.GetStatus().GetVolumeRegistration() {
					if registration.GetDiskKey() == "boot" {
						registration.SetState(privatev1.ComputeInstanceVolumeRegistration_STATE_NON_COMMITTED_FAILURE)
					}
				}
				_, updateErr := computeInstances.Update(cleanupCtx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object:     current,
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.volume_registration"}},
					Lock:       true,
				}.Build())
				g.Expect(updateErr).NotTo(HaveOccurred())
				released = true
			}, time.Minute, time.Second).Should(Succeed())
			if released {
				_, signalErr := computeInstances.Signal(cleanupCtx,
					privatev1.ComputeInstancesSignalRequest_builder{Id: instance.GetId()}.Build())
				Expect(signalErr == nil || status.Code(signalErr) == codes.NotFound).To(BeTrue())
				Eventually(func(g Gomega) {
					_, getErr := getDiskRegistrationComputeInstance(cleanupCtx, computeInstances, instance.GetId())
					g.Expect(status.Code(getErr)).To(Equal(codes.NotFound))
				}, 2*time.Minute, time.Second).Should(Succeed())
			}
		})
		publicInstances := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
		_, err = publicInstances.Delete(ctx, publicv1.ComputeInstancesDeleteRequest_builder{Id: instance.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			deleting, getErr := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(deleting.GetMetadata().HasDeletionTimestamp()).To(BeTrue())
			g.Expect(deleting.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
		}, time.Minute, time.Second).Should(Succeed())

		By("retaining the uncertain intent during repeated cancellation reconciles")
		for range 3 {
			err = computeinstance.ReconcileDiskRegistrations(ctx, computeInstances, countedVolumes, instance.GetId())
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(countedVolumes.createCalls).To(BeZero())
		Expect(countedVolumes.listCalls).To(Equal(6))
		remaining, err := volumes.List(ctx, privatev1.VolumesListRequest_builder{Filter: &filter}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(remaining.GetTotal()).To(BeZero())
		cancelled, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(cancelled.GetMetadata().HasDeletionTimestamp()).To(BeTrue())
		Expect(cancelled.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
		Expect(diskRegistrationState(cancelled, "boot")).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN))
		assertNoDiskRegistrationComputeInstanceCR(ctx, instance.GetId())
	})

	It("retains the parent until its saved Volume ID is confirmed absent", func(ctx context.Context) {
		instance := createDiskRegistrationAPIContractInstance(ctx, computeInstanceSpec)
		waitForDiskRegistrationFinalizer(ctx, computeInstances, instance.GetId())

		var volumeID string
		Eventually(func(g Gomega) {
			parent, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(parent.GetStatus().GetVolumes()).To(HaveLen(1))
			g.Expect(diskRegistrationState(parent, "boot")).To(Equal(
				privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED))
			volumeID = parent.GetStatus().GetVolumes()[0].GetVolumeId()
			g.Expect(volumeID).NotTo(BeEmpty())
		}, time.Minute, time.Second).Should(Succeed())

		volumeResponse, err := volumes.Get(ctx, privatev1.VolumesGetRequest_builder{Id: volumeID}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(volumeResponse.GetObject().GetMetadata().GetCreator()).To(Equal("system"))
		DeferCleanup(func(cleanupCtx context.Context) {
			setDiskRegistrationVolumeHoldFinalizer(cleanupCtx, volumes, volumeID, false)
		})
		setDiskRegistrationVolumeHoldFinalizer(ctx, volumes, volumeID, true)

		publicInstances := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
		_, err = publicInstances.Delete(ctx, publicv1.ComputeInstancesDeleteRequest_builder{Id: instance.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())

		By("holding the Volume record visible after the cleanup controller requests Delete")
		assertParentAndVolumePresent := func(g Gomega) {
			parent, getErr := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(parent.GetMetadata().HasDeletionTimestamp()).To(BeTrue())
			g.Expect(parent.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
			g.Expect(parent.GetStatus().GetVolumes()).To(HaveLen(1))
			g.Expect(parent.GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal(volumeID))

			visible, volumeErr := volumes.Get(ctx, privatev1.VolumesGetRequest_builder{Id: volumeID}.Build())
			g.Expect(volumeErr).NotTo(HaveOccurred())
			g.Expect(visible.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeTrue())
			g.Expect(visible.GetObject().GetMetadata().GetFinalizers()).To(
				ContainElement(diskRegistrationVolumeHoldFinalizer))
		}
		Eventually(assertParentAndVolumePresent, 2*time.Minute, time.Second).Should(Succeed())
		Consistently(assertParentAndVolumePresent, 5*time.Second, 250*time.Millisecond).Should(Succeed())

		By("releasing the fixture finalizer and waiting for record and parent removal")
		setDiskRegistrationVolumeHoldFinalizer(ctx, volumes, volumeID, false)
		Eventually(func(g Gomega) {
			_, getErr := volumes.Get(ctx, privatev1.VolumesGetRequest_builder{Id: volumeID}.Build())
			g.Expect(status.Code(getErr)).To(Equal(codes.NotFound))
		}, 2*time.Minute, time.Second).Should(Succeed())
		Eventually(func(g Gomega) {
			_, getErr := computeInstances.Get(ctx,
				privatev1.ComputeInstancesGetRequest_builder{Id: instance.GetId()}.Build())
			g.Expect(status.Code(getErr)).To(Equal(codes.NotFound))
		}, 2*time.Minute, time.Second).Should(Succeed())
	})

	DescribeTable("recovers provider-backed disk records after a lost Create response", func(ctx context.Context, provider string) {
		tierID := createCatalogItemStorageTierFixtureForProvider(ctx, provider)
		storageTiers := privatev1.NewStorageTiersClient(tool.InternalView().AdminConn())
		tier, err := storageTiers.Get(ctx, privatev1.StorageTiersGetRequest_builder{Id: tierID}.Build())
		Expect(err).NotTo(HaveOccurred())
		tierName := tier.GetObject().GetMetadata().GetName()
		diskSpec := privatev1.ComputeInstanceSpec_builder{
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib:     new(int32(30)),
				StorageTier: privatev1.StorageTierReference_builder{Id: tierID}.Build(),
			}.Build(),
			AdditionalDisks: privatev1.ComputeInstanceDiskList_builder{Items: []*privatev1.ComputeInstanceDisk{
				privatev1.ComputeInstanceDisk_builder{
					SizeGib:     new(int32(12)),
					StorageTier: privatev1.StorageTierReference_builder{Id: tierID}.Build(),
				}.Build(),
			}}.Build(),
		}.Build()
		instance := createIsolatedDiskRegistrationInstance(ctx, templateID, network, diskSpec)
		parent, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(parent.GetSpec().GetBootDisk().GetStorageTier().GetName()).To(Equal(tierName))
		Expect(parent.GetSpec().GetAdditionalDisks().GetItems()).To(HaveLen(1))
		Expect(parent.GetSpec().GetAdditionalDisks().GetItems()[0].GetStorageTier().GetName()).To(Equal(tierName))

		var committedBootID string
		DeferCleanup(func(cleanupCtx context.Context) {
			if committedBootID == "" {
				return
			}
			current, getErr := getDiskRegistrationComputeInstance(cleanupCtx, computeInstances, instance.GetId())
			if status.Code(getErr) == codes.NotFound {
				return
			}
			Expect(getErr).NotTo(HaveOccurred())
			if diskRegistrationState(current, "boot") == privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN {
				recoverErr := computeinstance.ReconcileDiskRegistrations(cleanupCtx,
					computeInstances, volumes, instance.GetId())
				Expect(recoverErr).NotTo(HaveOccurred())
			}
		})
		faultingVolumes := &diskRegistrationLostResponseVolumesClient{
			VolumesClient: volumes,
			hideFirstList: true,
			afterCreate: func(response *privatev1.VolumesCreateResponse) {
				committedBootID = response.GetObject().GetId()
			},
		}
		err = computeinstance.ReconcileDiskRegistrations(ctx, computeInstances, faultingVolumes, instance.GetId())
		Expect(err).To(HaveOccurred())
		Expect(committedBootID).NotTo(BeEmpty())
		Expect(faultingVolumes.createCalls).To(Equal(1))
		uncertain, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(diskRegistrationState(uncertain, "boot")).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN))
		Expect(uncertain.GetStatus().GetVolumes()).To(BeEmpty())

		err = computeinstance.ReconcileDiskRegistrations(ctx, computeInstances, volumes, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		resolved, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved.GetStatus().GetVolumes()).To(HaveLen(2))
		Expect(diskRegistrationState(resolved, "boot")).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED))
		Expect(diskRegistrationState(resolved, "disk-1")).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED))
		ids := make(map[string]string, 2)
		for _, saved := range resolved.GetStatus().GetVolumes() {
			ids[saved.GetDiskKey()] = saved.GetVolumeId()
		}
		Expect(ids).To(HaveKeyWithValue("boot", committedBootID))
		Expect(ids).To(HaveKey("disk-1"))
		Expect(ids["disk-1"]).NotTo(BeEmpty())
		Expect(ids["disk-1"]).NotTo(Equal(committedBootID))
		for _, id := range ids {
			observed, getErr := volumes.Get(ctx, privatev1.VolumesGetRequest_builder{Id: id}.Build())
			Expect(getErr).NotTo(HaveOccurred())
			Expect(observed.GetObject().GetSpec().GetStorageTier()).To(Equal(tierName))
			Expect(observed.GetObject().GetStatus().GetProvider()).To(Equal(provider))
		}
		assertNoDiskRegistrationComputeInstanceCR(ctx, instance.GetId())

		publicInstances := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
		_, err = publicInstances.Delete(ctx, publicv1.ComputeInstancesDeleteRequest_builder{Id: instance.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		for _, id := range ids {
			Eventually(func(g Gomega) {
				_, getErr := volumes.Get(ctx, privatev1.VolumesGetRequest_builder{Id: id}.Build())
				g.Expect(status.Code(getErr)).To(Equal(codes.NotFound))
			}, 2*time.Minute, time.Second).Should(Succeed())
		}
		Eventually(func(g Gomega) {
			_, getErr := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
			g.Expect(status.Code(getErr)).To(Equal(codes.NotFound))
		}, 2*time.Minute, time.Second).Should(Succeed())
	},
		Entry("LVMS", "lvms"),
		Entry("VAST API record", "vast"),
	)

	DescribeTable("registers and removes disk records through a supported storage tier", func(ctx context.Context, provider string) {
		tierID := createCatalogItemStorageTierFixtureForProvider(ctx, provider)
		storageTiers := privatev1.NewStorageTiersClient(tool.InternalView().AdminConn())
		tier, err := storageTiers.Get(ctx, privatev1.StorageTiersGetRequest_builder{Id: tierID}.Build())
		Expect(err).NotTo(HaveOccurred())
		tierName := tier.GetObject().GetMetadata().GetName()

		spec := publicv1.ComputeInstanceSpec_builder{
			Template:           publicv1.ComputeInstanceTemplateReference_builder{Id: templateID}.Build(),
			NetworkAttachments: []*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()},
			BootDisk: publicv1.ComputeInstanceDisk_builder{
				SizeGib:     new(int32(30)),
				StorageTier: publicv1.StorageTierReference_builder{Id: tierID}.Build(),
			}.Build(),
			AdditionalDisks: publicv1.ComputeInstanceDiskList_builder{Items: []*publicv1.ComputeInstanceDisk{
				publicv1.ComputeInstanceDisk_builder{
					SizeGib:     new(int32(12)),
					StorageTier: publicv1.StorageTierReference_builder{Id: tierID}.Build(),
				}.Build(),
			}}.Build(),
		}.Build()
		instance := createDiskRegistrationAPIContractInstance(ctx, spec)
		var parent *privatev1.ComputeInstance
		var ids map[string]string
		Eventually(func(g Gomega) {
			current, getErr := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(current.GetSpec().GetBootDisk().GetStorageTier().GetName()).To(Equal(tierName))
			g.Expect(current.GetSpec().GetAdditionalDisks().GetItems()).To(HaveLen(1))
			g.Expect(current.GetSpec().GetAdditionalDisks().GetItems()[0].GetStorageTier().GetName()).To(Equal(tierName))
			g.Expect(current.GetStatus().GetVolumes()).To(HaveLen(2))
			g.Expect(diskRegistrationState(current, "boot")).To(Equal(
				privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED))
			g.Expect(diskRegistrationState(current, "disk-1")).To(Equal(
				privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED))
			currentIDs := make(map[string]string, 2)
			for _, saved := range current.GetStatus().GetVolumes() {
				currentIDs[saved.GetDiskKey()] = saved.GetVolumeId()
			}
			g.Expect(currentIDs).To(HaveKey("boot"))
			g.Expect(currentIDs).To(HaveKey("disk-1"))
			g.Expect(currentIDs["boot"]).NotTo(BeEmpty())
			g.Expect(currentIDs["disk-1"]).NotTo(BeEmpty())
			g.Expect(currentIDs["disk-1"]).NotTo(Equal(currentIDs["boot"]))
			parent, ids = current, currentIDs
		}, time.Minute, time.Second).Should(Succeed())

		for key, size := range map[string]int64{"boot": 30, "disk-1": 12} {
			id := ids[key]
			DeferCleanup(func(cleanupCtx context.Context) {
				setDiskRegistrationVolumeHoldFinalizer(cleanupCtx, volumes, id, false)
			})
			name := "ci-" + instance.GetId() + "-" + key
			filter := diskRegistrationAPIFilter(name, parent.GetMetadata().GetTenant(), parent.GetMetadata().GetProject())
			found, listErr := volumes.List(ctx, privatev1.VolumesListRequest_builder{
				Filter: &filter,
			}.Build())
			Expect(listErr).NotTo(HaveOccurred())
			Expect(found.GetTotal()).To(Equal(int32(1)))
			Expect(found.GetItems()[0].GetId()).To(Equal(id))
			observed, getErr := volumes.Get(ctx, privatev1.VolumesGetRequest_builder{Id: id}.Build())
			Expect(getErr).NotTo(HaveOccurred())
			Expect(observed.GetObject().GetMetadata().GetName()).To(Equal(name))
			Expect(observed.GetObject().GetMetadata().GetTenant()).To(Equal(parent.GetMetadata().GetTenant()))
			Expect(observed.GetObject().GetMetadata().GetProject()).To(Equal(parent.GetMetadata().GetProject()))
			Expect(observed.GetObject().GetMetadata().GetCreator()).To(Equal("system"))
			Expect(observed.GetObject().GetSpec().GetStorageTier()).To(Equal(tierName))
			Expect(observed.GetObject().GetSpec().GetSizeGib()).To(Equal(size))
			Expect(observed.GetObject().GetStatus().GetProvider()).To(Equal(provider))
			Expect(observed.GetObject().GetSpec().GetTopology().GetSegments()).To(Equal(
				map[string]string{"osac.io/node": diskRegistrationPlaceholderNode}))
		}

		By("checking the managed CR receives both durable IDs")
		_, err = computeInstances.Signal(ctx,
			privatev1.ComputeInstancesSignalRequest_builder{Id: instance.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			objects := &osacv1alpha1.ComputeInstanceList{}
			g.Expect(tool.KubeClient().List(ctx, objects,
				crclient.MatchingLabels{labels.ComputeInstanceUuid: instance.GetId()},
			)).To(Succeed())
			g.Expect(objects.Items).To(HaveLen(1))
			g.Expect(objects.Items[0].Spec.DiskVolumeIDs).To(Equal(ids))
		}, time.Minute, time.Second).Should(Succeed())

		for _, id := range ids {
			setDiskRegistrationVolumeHoldFinalizer(ctx, volumes, id, true)
		}
		publicInstances := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
		_, err = publicInstances.Delete(ctx, publicv1.ComputeInstancesDeleteRequest_builder{Id: instance.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			deleting, getErr := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(deleting.GetMetadata().HasDeletionTimestamp()).To(BeTrue())
			g.Expect(deleting.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
			g.Expect(deleting.GetStatus().GetVolumes()).To(HaveLen(2))
			for _, id := range ids {
				visible, volumeErr := volumes.Get(ctx, privatev1.VolumesGetRequest_builder{Id: id}.Build())
				g.Expect(volumeErr).NotTo(HaveOccurred())
				g.Expect(visible.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeTrue())
			}
		}, 2*time.Minute, time.Second).Should(Succeed())
		for _, id := range ids {
			setDiskRegistrationVolumeHoldFinalizer(ctx, volumes, id, false)
		}
		for _, id := range ids {
			Eventually(func(g Gomega) {
				_, getErr := volumes.Get(ctx, privatev1.VolumesGetRequest_builder{Id: id}.Build())
				g.Expect(status.Code(getErr)).To(Equal(codes.NotFound))
			}, 2*time.Minute, time.Second).Should(Succeed())
		}
		Eventually(func(g Gomega) {
			_, getErr := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
			g.Expect(status.Code(getErr)).To(Equal(codes.NotFound))
		}, 2*time.Minute, time.Second).Should(Succeed())
	},
		Entry("LVMS", "lvms"),
		Entry("VAST API record", "vast"),
	)
})

func createDiskRegistrationAPIContractInstance(ctx context.Context, spec *publicv1.ComputeInstanceSpec) *publicv1.ComputeInstance {
	GinkgoHelper()
	instance, err := createComputeInstanceFixture(ctx, tool.ExternalView().UserConn(), spec)
	Expect(err).NotTo(HaveOccurred())
	return instance
}

// The deployed controller sees the noncanonical Hub and stops before disk
// registration. These cases can then drive the production registration helper
// through real private APIs without a competing reconciler.
func createIsolatedDiskRegistrationInstance(
	ctx context.Context,
	templateID string,
	network catalogItemNetworkFixture,
	diskSpec ...*privatev1.ComputeInstanceSpec,
) *privatev1.ComputeInstance {
	GinkgoHelper()
	attachment := privatev1.ComputeNetworkAttachment_builder{
		Subnet: privatev1.SubnetLocalReference_builder{Id: network.subnetID}.Build(),
	}.Build()
	if network.securityGroupID != "" {
		attachment.SetSecurityGroups([]*privatev1.SecurityGroupLocalReference{
			privatev1.SecurityGroupLocalReference_builder{Id: network.securityGroupID}.Build(),
		})
	}
	spec := privatev1.ComputeInstanceSpec_builder{
		Template:           privatev1.ComputeInstanceTemplateReference_builder{Id: templateID}.Build(),
		NetworkAttachments: []*privatev1.ComputeNetworkAttachment{attachment},
	}.Build()
	if len(diskSpec) > 0 {
		spec.SetBootDisk(diskSpec[0].GetBootDisk())
		spec.SetAdditionalDisks(diskSpec[0].GetAdditionalDisks())
	}
	client := privatev1.NewComputeInstancesClient(tool.InternalView().AdminConn())
	response, err := client.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
		Object: privatev1.ComputeInstance_builder{
			Metadata: privatev1.Metadata_builder{
				Name:       catalogItemFixtureName(),
				Tenant:     usersGroup,
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Spec:   spec,
			Status: privatev1.ComputeInstanceStatus_builder{Hub: diskRegistrationIsolationHubID}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	instance := response.GetObject()
	Expect(instance.GetStatus().GetHub()).To(Equal(diskRegistrationIsolationHubID))
	Expect(instance.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
	deferComputeInstanceDeletion(instance.GetId(), tool.ExternalView().AdminConn())
	Eventually(func(g Gomega) {
		current, getErr := client.Get(ctx,
			privatev1.ComputeInstancesGetRequest_builder{Id: instance.GetId()}.Build())
		g.Expect(getErr).NotTo(HaveOccurred())
		g.Expect(current.GetObject().GetStatus().GetHub()).To(Equal(diskRegistrationIsolationHubID))
		g.Expect(current.GetObject().GetStatus().GetState()).To(Equal(
			privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED))
		g.Expect(current.GetObject().GetStatus().GetVolumes()).To(BeEmpty())
		g.Expect(current.GetObject().GetStatus().GetVolumeRegistration()).To(BeEmpty())
	}, time.Minute, time.Second).Should(Succeed())
	return instance
}

func waitForDiskRegistrationFinalizer(ctx context.Context, client privatev1.ComputeInstancesClient, id string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		response, err := client.Get(probeCtx, privatev1.ComputeInstancesGetRequest_builder{Id: id}.Build())
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(slices.Contains(response.GetObject().GetMetadata().GetFinalizers(), finalizers.Controller)).To(BeTrue())
	}, time.Minute, time.Second).Should(Succeed())
}

func assertNoDiskRegistrationComputeInstanceCR(ctx context.Context, id string) {
	GinkgoHelper()
	Consistently(func(g Gomega) {
		objects := &osacv1alpha1.ComputeInstanceList{}
		g.Expect(tool.KubeClient().List(ctx, objects,
			crclient.MatchingLabels{labels.ComputeInstanceUuid: id},
		)).To(Succeed())
		g.Expect(objects.Items).To(BeEmpty())
	}, 3*time.Second, 250*time.Millisecond).Should(Succeed())
}

func getDiskRegistrationComputeInstance(ctx context.Context, client privatev1.ComputeInstancesClient, id string) (*privatev1.ComputeInstance, error) {
	response, err := client.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: id}.Build())
	if err != nil {
		return nil, err
	}
	return response.GetObject(), nil
}

func setDiskRegistrationVolumeHoldFinalizer(
	ctx context.Context,
	client privatev1.VolumesClient,
	id string,
	hold bool,
) {
	GinkgoHelper()
	Eventually(func(g Gomega) bool {
		response, err := client.Get(ctx, privatev1.VolumesGetRequest_builder{Id: id}.Build())
		if !hold && status.Code(err) == codes.NotFound {
			return true
		}
		g.Expect(err).NotTo(HaveOccurred())
		object := response.GetObject()
		current := object.GetMetadata().GetFinalizers()
		present := slices.Contains(current, diskRegistrationVolumeHoldFinalizer)
		if present == hold {
			if !hold {
				_, signalErr := client.Signal(ctx, privatev1.VolumesSignalRequest_builder{Id: id}.Build())
				g.Expect(signalErr == nil || status.Code(signalErr) == codes.NotFound).To(BeTrue())
			}
			return true
		}
		finalizers := slices.DeleteFunc(slices.Clone(current), func(value string) bool {
			return value == diskRegistrationVolumeHoldFinalizer
		})
		if hold {
			finalizers = append(finalizers, diskRegistrationVolumeHoldFinalizer)
		}
		object.GetMetadata().SetFinalizers(finalizers)
		_, err = client.Update(ctx, privatev1.VolumesUpdateRequest_builder{
			Object:     object,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
			Lock:       true,
		}.Build())
		g.Expect(err).NotTo(HaveOccurred())
		if !hold {
			_, signalErr := client.Signal(ctx, privatev1.VolumesSignalRequest_builder{Id: id}.Build())
			g.Expect(signalErr == nil || status.Code(signalErr) == codes.NotFound).To(BeTrue())
		}
		return true
	}, time.Minute, 100*time.Millisecond).Should(BeTrue())
}

func diskRegistrationState(instance *privatev1.ComputeInstance, diskKey string) privatev1.ComputeInstanceVolumeRegistration_State {
	for _, registration := range instance.GetStatus().GetVolumeRegistration() {
		if registration.GetDiskKey() == diskKey {
			return registration.GetState()
		}
	}
	return privatev1.ComputeInstanceVolumeRegistration_STATE_UNSPECIFIED
}

func diskRegistrationAPIFilter(name, tenant, project string) string {
	return fmt.Sprintf("this.metadata.name == %s && this.metadata.tenant == %s && this.metadata.project == %s",
		strconv.Quote(name), strconv.Quote(tenant), strconv.Quote(project))
}

func diskRegistrationVolumeRequest(
	instance *privatev1.ComputeInstance,
	name string,
	storageTier string,
	sizeGib int64,
) *privatev1.VolumesCreateRequest {
	return privatev1.VolumesCreateRequest_builder{
		Object: privatev1.Volume_builder{
			Metadata: privatev1.Metadata_builder{
				Name:    name,
				Tenant:  instance.GetMetadata().GetTenant(),
				Project: instance.GetMetadata().GetProject(),
			}.Build(),
			Spec: privatev1.VolumeSpec_builder{
				StorageTier: storageTier,
				SizeGib:     sizeGib,
				AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
				Topology: privatev1.VolumeTopology_builder{
					Segments: map[string]string{"osac.io/node": diskRegistrationPlaceholderNode},
				}.Build(),
			}.Build(),
		}.Build(),
	}.Build()
}

type diskRegistrationLostResponseVolumesClient struct {
	privatev1.VolumesClient
	hideFirstList bool
	afterCreate   func(*privatev1.VolumesCreateResponse)
	createCalls   int
	listCalls     int
}

func (c *diskRegistrationLostResponseVolumesClient) Create(
	ctx context.Context,
	request *privatev1.VolumesCreateRequest,
	options ...grpc.CallOption,
) (*privatev1.VolumesCreateResponse, error) {
	c.createCalls++
	response, err := c.VolumesClient.Create(ctx, request, options...)
	if err != nil {
		return response, err
	}
	if c.afterCreate != nil {
		c.afterCreate(response)
	}
	return nil, status.Error(codes.Unavailable, "simulated Create response loss after server commit")
}

func (c *diskRegistrationLostResponseVolumesClient) List(
	ctx context.Context,
	request *privatev1.VolumesListRequest,
	options ...grpc.CallOption,
) (*privatev1.VolumesListResponse, error) {
	c.listCalls++
	response, err := c.VolumesClient.List(ctx, request, options...)
	if err != nil || !c.hideFirstList {
		return response, err
	}
	c.hideFirstList = false
	Expect(response.GetTotal()).To(Equal(int32(1)))
	Expect(response.GetSize()).To(Equal(int32(1)))
	Expect(response.GetItems()).To(HaveLen(1))
	return privatev1.VolumesListResponse_builder{}.Build(), nil
}

type diskRegistrationCountingVolumesClient struct {
	privatev1.VolumesClient
	createCalls int
	listCalls   int
}

func (c *diskRegistrationCountingVolumesClient) Create(
	ctx context.Context,
	request *privatev1.VolumesCreateRequest,
	options ...grpc.CallOption,
) (*privatev1.VolumesCreateResponse, error) {
	c.createCalls++
	return c.VolumesClient.Create(ctx, request, options...)
}

func (c *diskRegistrationCountingVolumesClient) List(
	ctx context.Context,
	request *privatev1.VolumesListRequest,
	options ...grpc.CallOption,
) (*privatev1.VolumesListResponse, error) {
	c.listCalls++
	return c.VolumesClient.List(ctx, request, options...)
}

type diskRegistrationCreateBarrierVolumesClient struct {
	privatev1.VolumesClient
	entered     chan struct{}
	release     chan struct{}
	createCalls atomic.Int32
	afterCreate func(*privatev1.VolumesCreateResponse)
}

func (c *diskRegistrationCreateBarrierVolumesClient) Create(
	ctx context.Context,
	request *privatev1.VolumesCreateRequest,
	options ...grpc.CallOption,
) (*privatev1.VolumesCreateResponse, error) {
	if c.createCalls.Add(1) == 1 {
		c.entered <- struct{}{}
		<-c.release
	}
	response, err := c.VolumesClient.Create(ctx, request, options...)
	if err == nil && response != nil && c.afterCreate != nil {
		c.afterCreate(response)
	}
	return response, err
}

type diskRegistrationMarkerResponseLossClient struct {
	privatev1.ComputeInstancesClient
	afterMarker func()
	interrupted bool
}

func (c *diskRegistrationMarkerResponseLossClient) Update(
	ctx context.Context,
	request *privatev1.ComputeInstancesUpdateRequest,
	options ...grpc.CallOption,
) (*privatev1.ComputeInstancesUpdateResponse, error) {
	response, err := c.ComputeInstancesClient.Update(ctx, request, options...)
	if err != nil || c.interrupted || !diskRegistrationRequestHasUncertainState(request) {
		return response, err
	}
	c.interrupted = true
	if c.afterMarker != nil {
		c.afterMarker()
	}
	return nil, status.Error(codes.Unavailable, "simulated caller interruption after attempted marker commit")
}

func diskRegistrationRequestHasUncertainState(request *privatev1.ComputeInstancesUpdateRequest) bool {
	for _, registration := range request.GetObject().GetStatus().GetVolumeRegistration() {
		if registration.GetState() == privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN {
			return true
		}
	}
	return false
}

var _ privatev1.VolumesClient = (*diskRegistrationLostResponseVolumesClient)(nil)
var _ privatev1.VolumesClient = (*diskRegistrationCountingVolumesClient)(nil)
var _ privatev1.VolumesClient = (*diskRegistrationCreateBarrierVolumesClient)(nil)
var _ privatev1.ComputeInstancesClient = (*diskRegistrationMarkerResponseLossClient)(nil)
