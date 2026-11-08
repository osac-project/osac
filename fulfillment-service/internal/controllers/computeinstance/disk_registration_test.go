/*
Copyright (c) 2026 Red Hat, Inc.

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

package computeinstance

import (
	"context"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const diskRegistrationTestID = "019a0112-3456-7890-abcd-0123456789ab"

var _ = Describe("ComputeInstance disk registration preparation", func() {
	It("maps every persisted disk to a stable Volume request", func() {
		prepared, err := prepareDiskRegistrations(diskRegistrationTestInstance())
		Expect(err).ToNot(HaveOccurred())
		Expect(prepared).To(HaveLen(3))

		expectRequest := func(index int, key, name, tier string, size int64) {
			request := prepared[index].request
			Expect(prepared[index].diskKey).To(Equal(key))
			Expect(request.GetObject().GetMetadata().GetName()).To(Equal(name))
			Expect(request.GetObject().GetMetadata().GetTenant()).To(Equal("tenant-a"))
			Expect(request.GetObject().GetMetadata().GetProject()).To(BeEmpty())
			Expect(request.GetObject().GetSpec().GetStorageTier()).To(Equal(tier))
			Expect(request.GetObject().GetSpec().GetSizeGib()).To(Equal(size))
			Expect(request.GetObject().GetSpec().GetAccessMode()).To(
				Equal(privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE))
			Expect(request.GetObject().GetSpec().GetTopology().GetSegments()).To(Equal(map[string]string{
				"osac.io/node": "osac-volume-registration-pending",
			}))
		}

		expectRequest(0, "boot", "ci-"+diskRegistrationTestID+"-boot", "vm-boot", 40)
		expectRequest(1, "disk-1", "ci-"+diskRegistrationTestID+"-disk-1", "vm-data-fast", 100)
		expectRequest(2, "disk-2", "ci-"+diskRegistrationTestID+"-disk-2", "vm-data-capacity", 250)
	})

	DescribeTable("rejects the complete disk set when any disk is invalid",
		func(mutate func(*privatev1.ComputeInstance)) {
			instance := diskRegistrationTestInstance()
			mutate(instance)

			prepared, err := prepareDiskRegistrations(instance)
			Expect(err).To(HaveOccurred())
			Expect(prepared).To(BeNil())
		},
		Entry("missing boot disk", func(instance *privatev1.ComputeInstance) {
			instance.GetSpec().SetBootDisk(nil)
		}),
		Entry("missing boot tier name", func(instance *privatev1.ComputeInstance) {
			instance.GetSpec().GetBootDisk().SetStorageTier(privatev1.StorageTierReference_builder{Id: "tier-id"}.Build())
		}),
		Entry("nonpositive boot size", func(instance *privatev1.ComputeInstance) {
			instance.GetSpec().GetBootDisk().SetSizeGib(0)
		}),
		Entry("missing additional disk", func(instance *privatev1.ComputeInstance) {
			instance.GetSpec().GetAdditionalDisks().SetItems([]*privatev1.ComputeInstanceDisk{nil})
		}),
		Entry("missing additional tier name", func(instance *privatev1.ComputeInstance) {
			instance.GetSpec().GetAdditionalDisks().GetItems()[1].SetStorageTier(
				privatev1.StorageTierReference_builder{Id: "tier-id"}.Build())
		}),
		Entry("nonpositive additional size", func(instance *privatev1.ComputeInstance) {
			instance.GetSpec().GetAdditionalDisks().GetItems()[1].SetSizeGib(-1)
		}),
		Entry("Volume name beyond the DNS label limit", func(instance *privatev1.ComputeInstance) {
			instance.SetId(strings.Repeat("a", 54))
		}),
	)

	It("uses the persisted additional-disk list as-is and derives identical names after restart", func() {
		instance := diskRegistrationTestInstance()
		instance.GetSpec().SetAdditionalDisks(nil)
		first, err := prepareDiskRegistrations(instance)
		Expect(err).ToNot(HaveOccurred())
		Expect(first).To(HaveLen(1))

		restarted := proto.Clone(instance).(*privatev1.ComputeInstance)
		second, err := prepareDiskRegistrations(restarted)
		Expect(err).ToNot(HaveOccurred())
		Expect(second[0].request.GetObject().GetMetadata().GetName()).To(Equal(
			first[0].request.GetObject().GetMetadata().GetName()))
		Expect(proto.Equal(second[0].request, first[0].request)).To(BeTrue())
	})

	It("reconstructs the complete boot and additional disk requests after restart", func() {
		instance := diskRegistrationTestInstance()
		first, err := prepareDiskRegistrations(instance)
		Expect(err).NotTo(HaveOccurred())

		restarted := proto.Clone(instance).(*privatev1.ComputeInstance)
		second, err := prepareDiskRegistrations(restarted)
		Expect(err).NotTo(HaveOccurred())
		Expect(second).To(HaveLen(len(first)))
		for index := range first {
			Expect(second[index].diskKey).To(Equal(first[index].diskKey))
			Expect(proto.Equal(second[index].request, first[index].request)).To(BeTrue())
		}
	})

	It("keeps an explicitly empty additional-disk list empty", func() {
		instance := diskRegistrationTestInstance()
		instance.GetSpec().SetAdditionalDisks(privatev1.ComputeInstanceDiskList_builder{
			Items: []*privatev1.ComputeInstanceDisk{},
		}.Build())

		prepared, err := prepareDiskRegistrations(instance)
		Expect(err).ToNot(HaveOccurred())
		Expect(prepared).To(HaveLen(1))
		Expect(prepared[0].diskKey).To(Equal("boot"))
	})
})

var _ = Describe("ComputeInstance disk registration claim", func() {
	It("commits all intents and the attempted marker before returning a request", func() {
		ctrl := gomock.NewController(GinkgoT())
		defer ctrl.Finish()
		client := NewMockComputeInstancesClient(ctrl)
		persisted := diskRegistrationTestInstance()
		persisted.GetMetadata().SetVersion(5)

		client.EXPECT().Get(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, _ *privatev1.ComputeInstancesGetRequest, _ ...grpc.CallOption) (*privatev1.ComputeInstancesGetResponse, error) {
				return privatev1.ComputeInstancesGetResponse_builder{Object: proto.Clone(persisted).(*privatev1.ComputeInstance)}.Build(), nil
			})
		client.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, request *privatev1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption) (*privatev1.ComputeInstancesUpdateResponse, error) {
				Expect(request.GetLock()).To(BeTrue())
				Expect(request.GetObject().GetMetadata().GetVersion()).To(Equal(int32(5)))
				Expect(request.GetUpdateMask().GetPaths()).To(Equal([]string{computeInstanceDiskRegistrationUpdateMask}))
				Expect(registrationStates(request.GetObject())).To(Equal(map[string]privatev1.ComputeInstanceVolumeRegistration_State{
					"boot":   privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED,
					"disk-1": privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED,
					"disk-2": privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED,
				}))
				persisted = commitDiskRegistrationUpdate(persisted, request)
				return privatev1.ComputeInstancesUpdateResponse_builder{Object: proto.Clone(persisted).(*privatev1.ComputeInstance)}.Build(), nil
			})
		client.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, request *privatev1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption) (*privatev1.ComputeInstancesUpdateResponse, error) {
				Expect(request.GetLock()).To(BeTrue())
				Expect(request.GetObject().GetMetadata().GetVersion()).To(Equal(int32(6)))
				Expect(request.GetUpdateMask().GetPaths()).To(Equal([]string{
					computeInstanceDiskRegistrationUpdateMask, "status.conditions"}))
				Expect(registrationStates(request.GetObject())).To(Equal(map[string]privatev1.ComputeInstanceVolumeRegistration_State{
					"boot":   privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN,
					"disk-1": privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED,
					"disk-2": privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED,
				}))
				Expect(request.GetObject().GetStatus().GetConditions()).To(ContainElement(WithTransform(
					func(condition *privatev1.ComputeInstanceCondition) string { return condition.GetReason() },
					Equal("VolumeRegistrationPending"))))
				persisted = commitDiskRegistrationUpdate(persisted, request)
				return privatev1.ComputeInstancesUpdateResponse_builder{Object: proto.Clone(persisted).(*privatev1.ComputeInstance)}.Build(), nil
			})

		t := &task{
			r:               &function{computeInstancesClient: client},
			computeInstance: privatev1.ComputeInstance_builder{Id: diskRegistrationTestID}.Build(),
		}
		request, claimed, err := t.prepareAndClaimDiskRegistration(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(claimed).To(BeTrue())
		Expect(request.GetObject().GetMetadata().GetName()).To(Equal("ci-" + diskRegistrationTestID + "-boot"))
		Expect(registrationStates(persisted)["boot"]).To(Equal(privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN))
	})

	DescribeTable("does not return a request when persistence fails",
		func(failOnUpdate int) {
			ctrl := gomock.NewController(GinkgoT())
			defer ctrl.Finish()
			client := NewMockComputeInstancesClient(ctrl)
			current := diskRegistrationTestInstance()
			current.GetMetadata().SetVersion(12)
			client.EXPECT().Get(gomock.Any(), gomock.Any()).Return(
				privatev1.ComputeInstancesGetResponse_builder{Object: current}.Build(), nil)

			updates := 0
			client.EXPECT().Update(gomock.Any(), gomock.Any()).Times(failOnUpdate).DoAndReturn(
				func(_ context.Context, request *privatev1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption) (*privatev1.ComputeInstancesUpdateResponse, error) {
					updates++
					if updates == failOnUpdate {
						return nil, status.Error(codes.Internal, "database write failed")
					}
					current = commitDiskRegistrationUpdate(current, request)
					return privatev1.ComputeInstancesUpdateResponse_builder{Object: current}.Build(), nil
				})

			t := &task{
				r:               &function{computeInstancesClient: client},
				computeInstance: privatev1.ComputeInstance_builder{Id: diskRegistrationTestID}.Build(),
			}
			request, claimed, err := t.prepareAndClaimDiskRegistration(context.Background())
			Expect(err).To(HaveOccurred())
			Expect(request).To(BeNil())
			Expect(claimed).To(BeFalse())
		},
		Entry("intent write", 1),
		Entry("attempted marker write", 2),
	)

	It("allows only one claimant when two reconcilers read the same version", func() {
		ctrl := gomock.NewController(GinkgoT())
		defer ctrl.Finish()
		client := NewMockComputeInstancesClient(ctrl)
		initial := diskRegistrationTestInstance()
		initial.GetMetadata().SetVersion(20)
		initial.GetStatus().SetVolumeRegistration([]*privatev1.ComputeInstanceVolumeRegistration{
			privatev1.ComputeInstanceVolumeRegistration_builder{
				DiskKey: "boot",
				State:   privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED,
			}.Build(),
			privatev1.ComputeInstanceVolumeRegistration_builder{
				DiskKey: "disk-1",
				State:   privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED,
			}.Build(),
			privatev1.ComputeInstanceVolumeRegistration_builder{
				DiskKey: "disk-2",
				State:   privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED,
			}.Build(),
		})
		client.EXPECT().Get(gomock.Any(), gomock.Any()).Return(
			privatev1.ComputeInstancesGetResponse_builder{Object: proto.Clone(initial).(*privatev1.ComputeInstance)}.Build(), nil).Times(2)
		updates := 0
		client.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, request *privatev1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption) (*privatev1.ComputeInstancesUpdateResponse, error) {
				updates++
				Expect(request.GetLock()).To(BeTrue())
				if updates == 1 {
					committed := commitDiskRegistrationUpdate(initial, request)
					return privatev1.ComputeInstancesUpdateResponse_builder{Object: committed}.Build(), nil
				}
				return nil, status.Error(codes.Aborted, "object version changed")
			}).Times(2)

		makeTask := func() *task {
			return &task{
				r:               &function{computeInstancesClient: client},
				computeInstance: privatev1.ComputeInstance_builder{Id: diskRegistrationTestID}.Build(),
			}
		}
		firstRequest, firstClaimed, firstErr := makeTask().prepareAndClaimDiskRegistration(context.Background())
		secondRequest, secondClaimed, secondErr := makeTask().prepareAndClaimDiskRegistration(context.Background())

		Expect(firstErr).ToNot(HaveOccurred())
		Expect(firstClaimed).To(BeTrue())
		Expect(firstRequest.GetObject().GetMetadata().GetName()).To(Equal("ci-" + diskRegistrationTestID + "-boot"))
		Expect(secondErr).ToNot(HaveOccurred())
		Expect(secondClaimed).To(BeFalse())
		Expect(secondRequest).To(BeNil())
	})

	It("does not reset an uncertain attempt when no Volume ID is present", func() {
		ctrl := gomock.NewController(GinkgoT())
		defer ctrl.Finish()
		client := NewMockComputeInstancesClient(ctrl)
		current := diskRegistrationTestInstance()
		current.GetSpec().SetAdditionalDisks(nil)
		current.GetStatus().SetVolumeRegistration([]*privatev1.ComputeInstanceVolumeRegistration{
			privatev1.ComputeInstanceVolumeRegistration_builder{
				DiskKey: "boot",
				State:   privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN,
			}.Build(),
		})
		client.EXPECT().Get(gomock.Any(), gomock.Any()).Return(
			privatev1.ComputeInstancesGetResponse_builder{Object: current}.Build(), nil)

		t := &task{
			r:               &function{computeInstancesClient: client},
			computeInstance: privatev1.ComputeInstance_builder{Id: diskRegistrationTestID}.Build(),
		}
		request, claimed, err := t.prepareAndClaimDiskRegistration(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(request).To(BeNil())
		Expect(claimed).To(BeFalse())
		Expect(registrationStates(current)["boot"]).To(Equal(privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN))
	})

	It("retains known IDs and unresolved intents while claiming another disk", func() {
		ctrl := gomock.NewController(GinkgoT())
		defer ctrl.Finish()
		client := NewMockComputeInstancesClient(ctrl)
		current := diskRegistrationTestInstance()
		current.GetStatus().SetVolumes([]*privatev1.ComputeInstanceVolume{
			privatev1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "volume-boot"}.Build(),
		})
		current.GetStatus().SetVolumeRegistration([]*privatev1.ComputeInstanceVolumeRegistration{
			privatev1.ComputeInstanceVolumeRegistration_builder{
				DiskKey: "boot",
				State:   privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED,
			}.Build(),
			privatev1.ComputeInstanceVolumeRegistration_builder{
				DiskKey: "disk-1",
				State:   privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN,
			}.Build(),
			privatev1.ComputeInstanceVolumeRegistration_builder{
				DiskKey: "disk-2",
				State:   privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED,
			}.Build(),
		})
		client.EXPECT().Get(gomock.Any(), gomock.Any()).Return(
			privatev1.ComputeInstancesGetResponse_builder{Object: current}.Build(), nil)
		client.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, request *privatev1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption) (*privatev1.ComputeInstancesUpdateResponse, error) {
				Expect(request.GetLock()).To(BeTrue())
				Expect(request.GetUpdateMask().GetPaths()).To(Equal([]string{
					computeInstanceDiskRegistrationUpdateMask, "status.conditions"}))
				Expect(request.GetObject().GetStatus().GetVolumes()).To(Equal(current.GetStatus().GetVolumes()))
				Expect(registrationStates(request.GetObject())).To(Equal(map[string]privatev1.ComputeInstanceVolumeRegistration_State{
					"boot":   privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED,
					"disk-1": privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN,
					"disk-2": privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN,
				}))
				updated := commitDiskRegistrationUpdate(current, request)
				return privatev1.ComputeInstancesUpdateResponse_builder{Object: updated}.Build(), nil
			})

		t := &task{
			r:               &function{computeInstancesClient: client},
			computeInstance: privatev1.ComputeInstance_builder{Id: diskRegistrationTestID}.Build(),
		}
		request, claimed, err := t.prepareAndClaimDiskRegistration(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(claimed).To(BeTrue())
		Expect(request.GetObject().GetMetadata().GetName()).To(Equal("ci-" + diskRegistrationTestID + "-disk-2"))
	})

	It("does not claim new work after cancellation or before the parent finalizer is persisted", func() {
		for _, canceled := range []bool{true, false} {
			ctrl := gomock.NewController(GinkgoT())
			client := NewMockComputeInstancesClient(ctrl)
			current := diskRegistrationTestInstance()
			if canceled {
				current.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
				current.GetStatus().SetVolumes([]*privatev1.ComputeInstanceVolume{
					privatev1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "volume-boot"}.Build(),
				})
				current.GetStatus().SetVolumeRegistration([]*privatev1.ComputeInstanceVolumeRegistration{
					privatev1.ComputeInstanceVolumeRegistration_builder{
						DiskKey: "boot",
						State:   privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN,
					}.Build(),
				})
			} else {
				current.GetMetadata().SetFinalizers(nil)
			}
			before := proto.Clone(current).(*privatev1.ComputeInstance)
			client.EXPECT().Get(gomock.Any(), gomock.Any()).Return(
				privatev1.ComputeInstancesGetResponse_builder{Object: current}.Build(), nil)

			t := &task{
				r:               &function{computeInstancesClient: client},
				computeInstance: privatev1.ComputeInstance_builder{Id: diskRegistrationTestID}.Build(),
			}
			request, claimed, err := t.prepareAndClaimDiskRegistration(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(request).To(BeNil())
			Expect(claimed).To(BeFalse())
			Expect(proto.Equal(current, before)).To(BeTrue())
			ctrl.Finish()
		}
	})
})

func diskRegistrationTestInstance() *privatev1.ComputeInstance {
	return privatev1.ComputeInstance_builder{
		Id: diskRegistrationTestID,
		Metadata: privatev1.Metadata_builder{
			Tenant:     "tenant-a",
			Project:    "",
			Finalizers: []string{finalizers.Controller},
		}.Build(),
		Spec: privatev1.ComputeInstanceSpec_builder{
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib:     proto.Int32(40),
				StorageTier: privatev1.StorageTierReference_builder{Id: "boot-tier-id", Name: "vm-boot"}.Build(),
			}.Build(),
			AdditionalDisks: privatev1.ComputeInstanceDiskList_builder{
				Items: []*privatev1.ComputeInstanceDisk{
					privatev1.ComputeInstanceDisk_builder{
						SizeGib:     proto.Int32(100),
						StorageTier: privatev1.StorageTierReference_builder{Id: "fast-tier-id", Name: "vm-data-fast"}.Build(),
					}.Build(),
					privatev1.ComputeInstanceDisk_builder{
						SizeGib:     proto.Int32(250),
						StorageTier: privatev1.StorageTierReference_builder{Id: "capacity-tier-id", Name: "vm-data-capacity"}.Build(),
					}.Build(),
				},
			}.Build(),
		}.Build(),
		Status: privatev1.ComputeInstanceStatus_builder{}.Build(),
	}.Build()
}

func registrationStates(instance *privatev1.ComputeInstance) map[string]privatev1.ComputeInstanceVolumeRegistration_State {
	states := make(map[string]privatev1.ComputeInstanceVolumeRegistration_State)
	for _, registration := range instance.GetStatus().GetVolumeRegistration() {
		states[registration.GetDiskKey()] = registration.GetState()
	}
	return states
}

func commitDiskRegistrationUpdate(
	current *privatev1.ComputeInstance,
	request *privatev1.ComputeInstancesUpdateRequest,
) *privatev1.ComputeInstance {
	updated := proto.Clone(current).(*privatev1.ComputeInstance)
	updated.GetStatus().SetVolumeRegistration(cloneDiskRegistrations(request.GetObject().GetStatus().GetVolumeRegistration()))
	if slices.Contains(request.GetUpdateMask().GetPaths(), "status.conditions") {
		updated.GetStatus().SetConditions(proto.Clone(request.GetObject().GetStatus()).(*privatev1.ComputeInstanceStatus).GetConditions())
	}
	updated.GetMetadata().SetVersion(current.GetMetadata().GetVersion() + 1)
	return updated
}
