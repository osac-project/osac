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

	"github.com/osac-project/osac/fulfillment-service/internal/controllers/computeinstance"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const diskRegistrationKubeHoldFinalizer = "osac-test.osac.io/hold-compute-instance-cleanup"

var _ = Describe("ComputeInstance Volume registration API contract", func() {
	var (
		ctx                 context.Context
		network             catalogItemNetworkFixture
		templateID          string
		computeInstances    privatev1.ComputeInstancesClient
		volumes             privatev1.VolumesClient
		storageTierName     string
		computeInstanceSpec *publicv1.ComputeInstanceSpec
	)

	BeforeEach(func(testCtx context.Context) {
		ctx = testCtx
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

	It("recovers the original record after a committed Create response is lost", func() {
		instance := createDiskRegistrationAPIContractInstance(ctx, computeInstanceSpec)
		waitForDiskRegistrationFinalizer(ctx, computeInstances, instance.GetId())
		parent, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(parent.GetMetadata().GetCreator()).NotTo(Equal("system"))

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
				Expect(observed.GetObject().GetSpec().GetTopology()).To(BeNil())
			},
		}
		complete, err := computeinstance.ReconcileDiskRegistrations(ctx, computeInstances, faultingVolumes, instance.GetId())
		Expect(err).To(HaveOccurred())
		Expect(complete).To(BeFalse())
		Expect(committedID).NotTo(BeEmpty())
		Expect(faultingVolumes.createCalls).To(Equal(1))
		Expect(faultingVolumes.listCalls).To(Equal(1))

		uncertain, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(diskRegistrationState(uncertain, "boot")).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_UNCERTAIN))
		Expect(uncertain.GetStatus().GetVolumes()).To(BeEmpty())

		By("restarting from the persisted parent ID and recovering through the real private List API")
		complete, err = computeinstance.ReconcileDiskRegistrations(ctx, computeInstances, volumes, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(complete).To(BeTrue())
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

	It("recovers a matching same-name record through real API uniqueness", func() {
		instance := createDiskRegistrationAPIContractInstance(ctx, computeInstanceSpec)
		waitForDiskRegistrationFinalizer(ctx, computeInstances, instance.GetId())
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
		complete, err := computeinstance.ReconcileDiskRegistrations(ctx, computeInstances, countedVolumes, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(complete).To(BeTrue())
		Expect(countedVolumes.createCalls).To(Equal(1))
		Expect(countedVolumes.listCalls).To(Equal(1))

		persisted, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.GetStatus().GetVolumes()).To(HaveLen(1))
		Expect(persisted.GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal(existing.GetObject().GetId()))
		all, err := volumes.List(ctx, privatev1.VolumesListRequest_builder{Filter: &filter}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(all.GetTotal()).To(Equal(int32(1)))
	})

	It("does not adopt a conflicting same-name record from the real API", func() {
		instance := createDiskRegistrationAPIContractInstance(ctx, computeInstanceSpec)
		waitForDiskRegistrationFinalizer(ctx, computeInstances, instance.GetId())
		parent, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		name := "ci-" + instance.GetId() + "-boot"
		filter := diskRegistrationAPIFilter(name, parent.GetMetadata().GetTenant(), parent.GetMetadata().GetProject())

		existing, err := volumes.Create(ctx, diskRegistrationVolumeRequest(parent, name, storageTierName, 31))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			_, cleanupErr := volumes.Delete(cleanupCtx,
				privatev1.VolumesDeleteRequest_builder{Id: existing.GetObject().GetId()}.Build())
			Expect(cleanupErr == nil || status.Code(cleanupErr) == codes.NotFound).To(BeTrue())
		})

		countedVolumes := &diskRegistrationCountingVolumesClient{VolumesClient: volumes}
		complete, err := computeinstance.ReconcileDiskRegistrations(ctx, computeInstances, countedVolumes, instance.GetId())
		Expect(err).To(HaveOccurred())
		Expect(complete).To(BeFalse())
		Expect(countedVolumes.createCalls).To(Equal(1))
		Expect(countedVolumes.listCalls).To(Equal(1))

		persisted, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.GetStatus().GetVolumes()).To(BeEmpty())
		Expect(diskRegistrationState(persisted, "boot")).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_UNCERTAIN))
		all, err := volumes.List(ctx, privatev1.VolumesListRequest_builder{Filter: &filter}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(all.GetTotal()).To(Equal(int32(1)))
		Expect(all.GetItems()[0].GetId()).To(Equal(existing.GetObject().GetId()))
	})

	It("allows only one concurrent reconciler to dispatch a same-name Create", func() {
		instance := createDiskRegistrationAPIContractInstance(ctx, computeInstanceSpec)
		waitForDiskRegistrationFinalizer(ctx, computeInstances, instance.GetId())
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

		type result struct {
			complete bool
			err      error
		}
		firstResult := make(chan result, 1)
		go func() {
			defer close(firstDone)
			complete, reconcileErr := computeinstance.ReconcileDiskRegistrations(ctx,
				computeInstances, barrier, instance.GetId())
			firstResult <- result{complete: complete, err: reconcileErr}
		}()
		Eventually(barrier.entered, time.Minute).Should(Receive())

		secondComplete, secondErr := computeinstance.ReconcileDiskRegistrations(ctx,
			computeInstances, barrier, instance.GetId())
		Expect(secondErr).NotTo(HaveOccurred())
		Expect(secondComplete).To(BeFalse())
		releaseCreate()

		var first result
		Eventually(firstResult, time.Minute).Should(Receive(&first))
		Expect(first.err).NotTo(HaveOccurred())
		Expect(first.complete).To(BeTrue())
		Expect(barrier.createCalls.Load()).To(Equal(int32(1)))
		all, err := volumes.List(ctx, privatev1.VolumesListRequest_builder{Filter: &filter}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(all.GetTotal()).To(Equal(int32(1)))
		persisted, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.GetStatus().GetVolumes()).To(HaveLen(1))
	})

	It("keeps a committed attempted marker blocked after a pre-call interruption", func() {
		instance := createDiskRegistrationAPIContractInstance(ctx, computeInstanceSpec)
		waitForDiskRegistrationFinalizer(ctx, computeInstances, instance.GetId())
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
					privatev1.ComputeInstanceVolumeRegistration_STATE_UNCERTAIN))
				found, listErr := volumes.List(ctx, privatev1.VolumesListRequest_builder{Filter: &filter}.Build())
				Expect(listErr).NotTo(HaveOccurred())
				Expect(found.GetTotal()).To(BeZero())
			},
		}
		countedVolumes := &diskRegistrationCountingVolumesClient{VolumesClient: volumes}
		complete, err := computeinstance.ReconcileDiskRegistrations(ctx, faultingComputeInstances, countedVolumes, instance.GetId())
		Expect(err).To(HaveOccurred())
		Expect(complete).To(BeFalse())
		Expect(faultingComputeInstances.interrupted).To(BeTrue())
		Expect(countedVolumes.createCalls).To(BeZero())

		By("restarting from persisted state and allowing repeated complete empty List responses")
		for range 3 {
			complete, err = computeinstance.ReconcileDiskRegistrations(ctx, computeInstances, countedVolumes, instance.GetId())
			Expect(err).NotTo(HaveOccurred())
			Expect(complete).To(BeFalse())
		}
		Expect(countedVolumes.createCalls).To(BeZero())
		Expect(countedVolumes.listCalls).To(Equal(3))
		retained, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(slices.Contains(retained.GetMetadata().GetFinalizers(), finalizers.Controller)).To(BeTrue())
		Expect(diskRegistrationState(retained, "boot")).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_UNCERTAIN))

		By("canceling while a fixture-owned Kubernetes finalizer keeps the parent cleanup path open")
		computeInstanceCR := waitForDiskRegistrationComputeInstanceCR(ctx, instance.GetId())
		DeferCleanup(func(cleanupCtx context.Context) {
			current := &osacv1alpha1.ComputeInstance{}
			getErr := tool.KubeClient().Get(cleanupCtx,
				crclient.ObjectKeyFromObject(computeInstanceCR), current)
			if getErr == nil && slices.Contains(current.GetFinalizers(), diskRegistrationKubeHoldFinalizer) {
				current.SetFinalizers(slices.DeleteFunc(current.GetFinalizers(), func(value string) bool {
					return value == diskRegistrationKubeHoldFinalizer
				}))
				Expect(tool.KubeClient().Update(cleanupCtx, current)).To(Succeed())
			}
			_, signalErr := computeInstances.Signal(cleanupCtx, privatev1.ComputeInstancesSignalRequest_builder{Id: instance.GetId()}.Build())
			Expect(signalErr == nil || status.Code(signalErr) == codes.NotFound).To(BeTrue())
		})
		if !slices.Contains(computeInstanceCR.GetFinalizers(), diskRegistrationKubeHoldFinalizer) {
			computeInstanceCR.SetFinalizers(append(computeInstanceCR.GetFinalizers(), diskRegistrationKubeHoldFinalizer))
			Expect(tool.KubeClient().Update(ctx, computeInstanceCR)).To(Succeed())
		}
		publicInstances := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
		_, err = publicInstances.Delete(ctx, publicv1.ComputeInstancesDeleteRequest_builder{Id: instance.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			deleting, getErr := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(deleting.GetMetadata().HasDeletionTimestamp()).To(BeTrue())
			g.Expect(deleting.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
		}, time.Minute, time.Second).Should(Succeed())

		By("keeping the uncertain request and finalizer through repeated cancellation reconciles")
		for range 3 {
			complete, err = computeinstance.ReconcileDiskRegistrations(ctx, computeInstances, countedVolumes, instance.GetId())
			Expect(err).NotTo(HaveOccurred())
			Expect(complete).To(BeFalse())
		}
		Expect(countedVolumes.createCalls).To(BeZero())
		Expect(countedVolumes.listCalls).To(Equal(6))
		cancelled, err := getDiskRegistrationComputeInstance(ctx, computeInstances, instance.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(cancelled.GetMetadata().HasDeletionTimestamp()).To(BeTrue())
		Expect(cancelled.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
		Expect(diskRegistrationState(cancelled, "boot")).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_UNCERTAIN))
	})
})

func createDiskRegistrationAPIContractInstance(ctx context.Context, spec *publicv1.ComputeInstanceSpec) *publicv1.ComputeInstance {
	GinkgoHelper()
	instance, err := createComputeInstanceFixture(ctx, tool.ExternalView().UserConn(), spec)
	Expect(err).NotTo(HaveOccurred())
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

func waitForDiskRegistrationComputeInstanceCR(ctx context.Context, id string) *osacv1alpha1.ComputeInstance {
	GinkgoHelper()
	var result *osacv1alpha1.ComputeInstance
	Eventually(func(g Gomega) {
		objects := &osacv1alpha1.ComputeInstanceList{}
		g.Expect(tool.KubeClient().List(ctx, objects,
			crclient.MatchingLabels{labels.ComputeInstanceUuid: id},
		)).To(Succeed())
		g.Expect(objects.Items).To(HaveLen(1))
		result = &objects.Items[0]
	}, time.Minute, time.Second).Should(Succeed())
	return result
}

func getDiskRegistrationComputeInstance(ctx context.Context, client privatev1.ComputeInstancesClient, id string) (*privatev1.ComputeInstance, error) {
	response, err := client.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: id}.Build())
	if err != nil {
		return nil, err
	}
	return response.GetObject(), nil
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
		if registration.GetState() == privatev1.ComputeInstanceVolumeRegistration_STATE_UNCERTAIN {
			return true
		}
	}
	return false
}

var _ privatev1.VolumesClient = (*diskRegistrationLostResponseVolumesClient)(nil)
var _ privatev1.VolumesClient = (*diskRegistrationCountingVolumesClient)(nil)
var _ privatev1.VolumesClient = (*diskRegistrationCreateBarrierVolumesClient)(nil)
var _ privatev1.ComputeInstancesClient = (*diskRegistrationMarkerResponseLossClient)(nil)
