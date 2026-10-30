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
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("ReconcileDiskRegistrations", func() {
	It("creates each disk from persisted inputs and atomically saves its ID and resolved state", func() {
		computeClient, volumesClient := registrationTestClients()
		createCalls := 0
		volumesClient.create = func(_ context.Context, request *privatev1.VolumesCreateRequest) (*privatev1.VolumesCreateResponse, error) {
			createCalls++
			Expect(registrationStates(computeClient.object)["boot"]).To(Equal(
				privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN))
			Expect(request.GetObject().GetMetadata().GetName()).To(Equal("ci-" + diskRegistrationTestID + "-boot"))
			Expect(request.GetObject().GetMetadata().GetTenant()).To(Equal("tenant-a"))
			Expect(request.GetObject().GetMetadata().GetProject()).To(BeEmpty())
			Expect(request.GetObject().GetSpec().GetStorageTier()).To(Equal("vm-boot"))
			Expect(request.GetObject().GetSpec().GetSizeGib()).To(Equal(int64(40)))
			Expect(request.GetObject().GetSpec().GetTopology().GetSegments()).To(Equal(map[string]string{
				"osac.io/node": "osac-volume-registration-pending",
			}))
			volume := registrationTestVolume(request, "volume-boot", "system")
			return privatev1.VolumesCreateResponse_builder{Object: volume}.Build(), nil
		}

		err := ReconcileDiskRegistrations(context.Background(), computeClient, volumesClient, diskRegistrationTestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(diskRegistrationCompleteForTest(computeClient.object)).To(BeTrue())
		Expect(createCalls).To(Equal(1))
		Expect(computeClient.object.GetStatus().GetVolumes()).To(HaveLen(1))
		Expect(computeClient.object.GetStatus().GetVolumes()[0].GetDiskKey()).To(Equal("boot"))
		Expect(computeClient.object.GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal("volume-boot"))
		Expect(registrationStates(computeClient.object)["boot"]).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED))
		Expect(computeClient.updateRequests).To(HaveLen(3))
		resolutionUpdate := computeClient.updateRequests[2]
		Expect(resolutionUpdate.GetLock()).To(BeTrue())
		Expect(resolutionUpdate.GetUpdateMask().GetPaths()).To(ConsistOf(
			"status.volumes", "status.volume_registration"))
		Expect(registrationStates(resolutionUpdate.GetObject())["boot"]).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED))
		Expect(resolutionUpdate.GetObject().GetStatus().GetVolumes()).To(HaveLen(1))
		Expect(resolutionUpdate.GetObject().GetStatus().GetVolumes()[0].GetDiskKey()).To(Equal("boot"))
		Expect(resolutionUpdate.GetObject().GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal("volume-boot"))
	})

	It("registers every persisted disk with its own request and preserves a named project", func() {
		computeClient := &registrationComputeInstancesClient{object: diskRegistrationTestInstance()}
		computeClient.object.GetMetadata().SetVersion(1)
		computeClient.object.GetMetadata().SetProject("project-a")
		volumesClient := &registrationVolumesClient{}
		volumesClient.create = func(_ context.Context, request *privatev1.VolumesCreateRequest) (*privatev1.VolumesCreateResponse, error) {
			Expect(request.GetObject().GetMetadata().GetProject()).To(Equal("project-a"))
			Expect(request.GetObject().GetSpec().GetTopology().GetSegments()).To(Equal(map[string]string{
				"osac.io/node": "osac-volume-registration-pending",
			}))
			switch request.GetObject().GetMetadata().GetName() {
			case "ci-" + diskRegistrationTestID + "-boot":
				Expect(request.GetObject().GetSpec().GetStorageTier()).To(Equal("vm-boot"))
				Expect(request.GetObject().GetSpec().GetSizeGib()).To(Equal(int64(40)))
			case "ci-" + diskRegistrationTestID + "-disk-1":
				Expect(request.GetObject().GetSpec().GetStorageTier()).To(Equal("vm-data-fast"))
				Expect(request.GetObject().GetSpec().GetSizeGib()).To(Equal(int64(100)))
			case "ci-" + diskRegistrationTestID + "-disk-2":
				Expect(request.GetObject().GetSpec().GetStorageTier()).To(Equal("vm-data-capacity"))
				Expect(request.GetObject().GetSpec().GetSizeGib()).To(Equal(int64(250)))
			default:
				Fail("unexpected Volume request name: " + request.GetObject().GetMetadata().GetName())
			}
			volumeID := "volume-" + request.GetObject().GetMetadata().GetName()
			return privatev1.VolumesCreateResponse_builder{
				Object: registrationTestVolume(request, volumeID, "system"),
			}.Build(), nil
		}

		err := ReconcileDiskRegistrations(context.Background(), computeClient, volumesClient, diskRegistrationTestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(diskRegistrationCompleteForTest(computeClient.object)).To(BeTrue())
		Expect(volumesClient.createRequests).To(HaveLen(3))
		Expect(computeClient.object.GetStatus().GetVolumes()).To(HaveLen(3))
		Expect(registrationStates(computeClient.object)).To(Equal(map[string]privatev1.ComputeInstanceVolumeRegistration_State{
			"boot":   privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED,
			"disk-1": privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED,
			"disk-2": privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED,
		}))
	})

	It("recovers a committed Create after a lost response and restart", func() {
		computeClient, volumesClient := registrationTestClients()
		var committed *privatev1.Volume
		createCalls := 0
		listCalls := 0
		volumesClient.create = func(_ context.Context, request *privatev1.VolumesCreateRequest) (*privatev1.VolumesCreateResponse, error) {
			createCalls++
			committed = registrationTestVolume(request, "volume-original", "system")
			return nil, status.Error(codes.Unavailable, "response lost")
		}
		volumesClient.list = func(_ context.Context, _ *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			listCalls++
			if listCalls == 1 {
				return registrationTestListResponse(nil, 0), nil
			}
			return registrationTestListResponse([]*privatev1.Volume{committed}, 1), nil
		}

		err := ReconcileDiskRegistrations(context.Background(), computeClient, volumesClient, diskRegistrationTestID)
		Expect(err).To(HaveOccurred())
		Expect(diskRegistrationCompleteForTest(computeClient.object)).To(BeFalse())
		Expect(registrationStates(computeClient.object)["boot"]).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN))

		err = ReconcileDiskRegistrations(context.Background(), computeClient, volumesClient, diskRegistrationTestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(diskRegistrationCompleteForTest(computeClient.object)).To(BeTrue())
		Expect(createCalls).To(Equal(1))
		Expect(listCalls).To(Equal(2))
		Expect(computeClient.object.GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal("volume-original"))
	})

	It("keeps an uncertain intent after an empty lookup and never retries Create", func() {
		computeClient, volumesClient := registrationTestClients()
		createCalls := 0
		volumesClient.create = func(_ context.Context, _ *privatev1.VolumesCreateRequest) (*privatev1.VolumesCreateResponse, error) {
			createCalls++
			return nil, status.Error(codes.DeadlineExceeded, "outcome unknown")
		}
		volumesClient.list = func(_ context.Context, _ *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			return registrationTestListResponse(nil, 0), nil
		}

		for attempt := range 2 {
			err := ReconcileDiskRegistrations(context.Background(), computeClient, volumesClient, diskRegistrationTestID)
			Expect(diskRegistrationCompleteForTest(computeClient.object)).To(BeFalse())
			if attempt == 0 {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		}
		Expect(createCalls).To(Equal(1))
		Expect(registrationStates(computeClient.object)["boot"]).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN))
	})

	It("marks only explicit pre-commit rejections retryable", func() {
		computeClient, volumesClient := registrationTestClients()
		createCalls := 0
		volumesClient.create = func(_ context.Context, request *privatev1.VolumesCreateRequest) (*privatev1.VolumesCreateResponse, error) {
			createCalls++
			if createCalls == 1 {
				return nil, status.Error(codes.InvalidArgument, "invalid request")
			}
			return privatev1.VolumesCreateResponse_builder{
				Object: registrationTestVolume(request, "volume-boot", "system"),
			}.Build(), nil
		}

		err := ReconcileDiskRegistrations(context.Background(), computeClient, volumesClient, diskRegistrationTestID)
		Expect(err).To(HaveOccurred())
		Expect(diskRegistrationCompleteForTest(computeClient.object)).To(BeFalse())
		Expect(registrationStates(computeClient.object)["boot"]).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_NON_COMMITTED_FAILURE))

		err = ReconcileDiskRegistrations(context.Background(), computeClient, volumesClient, diskRegistrationTestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(diskRegistrationCompleteForTest(computeClient.object)).To(BeTrue())
		Expect(createCalls).To(Equal(2))
	})

	It("recovers AlreadyExists through exact-scope List", func() {
		computeClient, volumesClient := registrationTestClients()
		var request *privatev1.VolumesCreateRequest
		volumesClient.create = func(_ context.Context, createRequest *privatev1.VolumesCreateRequest) (*privatev1.VolumesCreateResponse, error) {
			request = createRequest
			return nil, status.Error(codes.AlreadyExists, "name already exists")
		}
		volumesClient.list = func(_ context.Context, listRequest *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			Expect(listRequest.GetFilter()).To(Equal(registrationCELFilter(
				computeClient.object,
				preparedDiskRegistrationForTest().request,
			)))
			return registrationTestListResponse([]*privatev1.Volume{
				registrationTestVolume(request, "volume-existing", "system"),
			}, 1), nil
		}

		err := ReconcileDiskRegistrations(context.Background(), computeClient, volumesClient, diskRegistrationTestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(diskRegistrationCompleteForTest(computeClient.object)).To(BeTrue())
		Expect(computeClient.object.GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal("volume-existing"))
	})

	DescribeTable("fails closed on incomplete or conflicting List results",
		func(list func(*registrationVolumesClient) func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error), expectError bool) {
			computeClient, volumesClient := registrationTestClients()
			computeClient.object.GetStatus().SetVolumeRegistration([]*privatev1.ComputeInstanceVolumeRegistration{
				privatev1.ComputeInstanceVolumeRegistration_builder{
					DiskKey: "boot",
					State:   privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN,
				}.Build(),
			})
			volumesClient.list = list(volumesClient)

			err := ReconcileDiskRegistrations(context.Background(), computeClient, volumesClient, diskRegistrationTestID)
			Expect(diskRegistrationCompleteForTest(computeClient.object)).To(BeFalse())
			if expectError {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(volumesClient.createRequests).To(BeEmpty())
			Expect(volumesClient.listRequests).To(HaveLen(1))
			Expect(volumesClient.listRequests[0].GetLimit()).To(Equal(int32(1)))
			Expect(volumesClient.listRequests[0].GetOffset()).To(BeZero())
			Expect(registrationStates(computeClient.object)["boot"]).To(Equal(
				privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN))
		},
		Entry("empty result", func(_ *registrationVolumesClient) func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			return func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
				return registrationTestListResponse(nil, 0), nil
			}
		}, false),
		Entry("foreign scope", func(volumes *registrationVolumesClient) func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			return oneRegistrationVolumeResponse(func(volume *privatev1.Volume) {
				volume.GetMetadata().SetTenant("tenant-b")
			})
		}, false),
		Entry("mismatched name", func(_ *registrationVolumesClient) func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			return oneRegistrationVolumeResponse(func(volume *privatev1.Volume) {
				volume.GetMetadata().SetName("different-name")
			})
		}, false),
		Entry("mismatched project", func(_ *registrationVolumesClient) func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			return oneRegistrationVolumeResponse(func(volume *privatev1.Volume) {
				volume.GetMetadata().SetProject("project-b")
			})
		}, false),
		Entry("mismatched spec", func(volumes *registrationVolumesClient) func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			return oneRegistrationVolumeResponse(func(volume *privatev1.Volume) {
				volume.GetSpec().SetSizeGib(99)
			})
		}, false),
		Entry("mismatched topology", func(_ *registrationVolumesClient) func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			return oneRegistrationVolumeResponse(func(volume *privatev1.Volume) {
				volume.GetSpec().SetTopology(privatev1.VolumeTopology_builder{
					Segments: map[string]string{"osac.io/node": "different-node"},
				}.Build())
			})
		}, false),
		Entry("wrong creator", func(volumes *registrationVolumesClient) func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			return oneRegistrationVolumeResponse(func(volume *privatev1.Volume) {
				volume.GetMetadata().SetCreator("tenant-user")
			})
		}, false),
		Entry("deleting metadata", func(volumes *registrationVolumesClient) func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			return oneRegistrationVolumeResponse(func(volume *privatev1.Volume) {
				volume.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
			})
		}, false),
		Entry("deleting lifecycle state", func(volumes *registrationVolumesClient) func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			return oneRegistrationVolumeResponse(func(volume *privatev1.Volume) {
				volume.GetStatus().SetState(privatev1.VolumeState_VOLUME_STATE_DELETING)
			})
		}, false),
		Entry("archived lifecycle state", func(volumes *registrationVolumesClient) func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			return oneRegistrationVolumeResponse(func(volume *privatev1.Volume) {
				volume.GetStatus().SetState(privatev1.VolumeState_VOLUME_STATE_DELETED)
			})
		}, false),
		Entry("active result with another deletion-pending match", func(_ *registrationVolumesClient) func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			return func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
				matching := registrationTestVolume(requestFromListFixture(), "volume-active", "system")
				return registrationTestListResponse([]*privatev1.Volume{matching}, 2), nil
			}
		}, false),
		Entry("missing item despite positive total", func(_ *registrationVolumesClient) func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			return func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
				return registrationTestListResponse(nil, 1), nil
			}
		}, true),
		Entry("inconsistent page size", func(_ *registrationVolumesClient) func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			return func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
				return privatev1.VolumesListResponse_builder{Size: 1, Total: 1}.Build(), nil
			}
		}, true),
		Entry("item exceeds total", func(_ *registrationVolumesClient) func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			return func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
				item := registrationTestVolume(requestFromListFixture(), "volume-one", "system")
				return registrationTestListResponse([]*privatev1.Volume{item}, 0), nil
			}
		}, true),
		Entry("page exceeds requested limit", func(_ *registrationVolumesClient) func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			return func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
				item := registrationTestVolume(requestFromListFixture(), "volume-one", "system")
				return registrationTestListResponse([]*privatev1.Volume{item, item}, 2), nil
			}
		}, true),
		Entry("List error", func(_ *registrationVolumesClient) func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			return func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
				return nil, status.Error(codes.PermissionDenied, "not visible")
			}
		}, true),
	)

	It("uses CEL string escaping for every exact-scope filter value", func() {
		computeClient, volumesClient := registrationTestClients()
		computeClient.object.GetMetadata().SetTenant("tenant-\"quoted")
		computeClient.object.GetMetadata().SetProject("project\\path")
		computeClient.object.GetStatus().SetVolumeRegistration([]*privatev1.ComputeInstanceVolumeRegistration{
			privatev1.ComputeInstanceVolumeRegistration_builder{
				DiskKey: "boot",
				State:   privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN,
			}.Build(),
		})
		volumesClient.list = func(_ context.Context, request *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			Expect(request.GetFilter()).To(ContainSubstring(strconv.Quote("ci-" + diskRegistrationTestID + "-boot")))
			Expect(request.GetFilter()).To(ContainSubstring(strconv.Quote("tenant-\"quoted")))
			Expect(request.GetFilter()).To(ContainSubstring(strconv.Quote("project\\path")))
			return registrationTestListResponse(nil, 0), nil
		}

		err := ReconcileDiskRegistrations(context.Background(), computeClient, volumesClient, diskRegistrationTestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(diskRegistrationCompleteForTest(computeClient.object)).To(BeFalse())
	})

	It("can recover an uncertain disk during deletion but never creates a fresh disk", func() {
		computeClient, volumesClient := registrationTestClients()
		computeClient.object.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		computeClient.object.GetStatus().SetVolumeRegistration([]*privatev1.ComputeInstanceVolumeRegistration{
			privatev1.ComputeInstanceVolumeRegistration_builder{
				DiskKey: "boot",
				State:   privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN,
			}.Build(),
		})
		volumesClient.list = func(_ context.Context, _ *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			return registrationTestListResponse([]*privatev1.Volume{
				registrationTestVolume(
					privatev1.VolumesCreateRequest_builder{Object: preparedDiskRegistrationForTest().request.GetObject()}.Build(),
					"volume-recovered", "system"),
			}, 1), nil
		}

		err := ReconcileDiskRegistrations(context.Background(), computeClient, volumesClient, diskRegistrationTestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(diskRegistrationCompleteForTest(computeClient.object)).To(BeTrue())
		Expect(volumesClient.createRequests).To(BeEmpty())
		Expect(volumesClient.listRequests).To(HaveLen(1))
		Expect(computeClient.object.GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal("volume-recovered"))
	})

	It("does not create or look up a never-attempted disk after cancellation", func() {
		computeClient, volumesClient := registrationTestClients()
		computeClient.object.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		computeClient.object.GetStatus().SetVolumeRegistration([]*privatev1.ComputeInstanceVolumeRegistration{
			privatev1.ComputeInstanceVolumeRegistration_builder{
				DiskKey: "boot",
				State:   privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED,
			}.Build(),
		})

		err := ReconcileDiskRegistrations(context.Background(), computeClient, volumesClient, diskRegistrationTestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(diskRegistrationCompleteForTest(computeClient.object)).To(BeFalse())
		Expect(volumesClient.createRequests).To(BeEmpty())
		Expect(volumesClient.listRequests).To(BeEmpty())
		Expect(registrationStates(computeClient.object)["boot"]).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED))
	})

	It("releases a canceled parent when its claimed Create was never dispatched", func() {
		computeClient, volumesClient := registrationTestClients()
		computeClient.failBeforeUpdateCalls = map[int]error{
			3: status.Error(codes.Aborted, "another status update won the lock"),
		}
		computeClient.afterUpdate = func(call int) {
			if call == 2 { // The attempted marker committed; Create has not started.
				Expect(registrationStates(computeClient.object)["boot"]).To(Equal(
					privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN))
				computeClient.object.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
				computeClient.object.GetMetadata().SetVersion(computeClient.object.GetMetadata().GetVersion() + 1)
			}
		}

		err := ReconcileDiskRegistrations(context.Background(), computeClient, volumesClient, diskRegistrationTestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(diskRegistrationCompleteForTest(computeClient.object)).To(BeFalse())
		Expect(volumesClient.createRequests).To(BeEmpty())
		Expect(registrationStates(computeClient.object)["boot"]).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_NON_COMMITTED_FAILURE))
		Expect(computeClient.updateRequests).To(HaveLen(4))
		settlement := computeClient.updateRequests[3]
		Expect(settlement.GetLock()).To(BeTrue())
		Expect(settlement.GetUpdateMask().GetPaths()).To(ConsistOf("status.volume_registration"))

		deleting, _, cleanupVolumes := newVolumeCleanupTask(nil,
			cloneDiskRegistrations(computeClient.object.GetStatus().GetVolumeRegistration()))
		Expect(deleting.delete(context.Background())).To(Succeed())
		Expect(hasFinalizer(deleting.computeInstance)).To(BeFalse())
		Expect(cleanupVolumes.createCalls).To(BeZero())
	})

	It("preserves a newer Volume ID when cancellation settlement loses the lock", func() {
		computeClient, volumesClient := registrationTestClients()
		computeClient.afterUpdate = func(call int) {
			if call == 2 {
				computeClient.object.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
				computeClient.object.GetMetadata().SetVersion(computeClient.object.GetMetadata().GetVersion() + 1)
			}
		}
		computeClient.beforeUpdate = func(call int) {
			if call == 3 {
				computeClient.object.GetStatus().SetVolumes([]*privatev1.ComputeInstanceVolume{
					privatev1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "volume-newer"}.Build(),
				})
				computeClient.object.GetStatus().GetVolumeRegistration()[0].SetState(
					privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED)
				computeClient.object.GetMetadata().SetVersion(computeClient.object.GetMetadata().GetVersion() + 1)
			}
		}

		err := ReconcileDiskRegistrations(context.Background(), computeClient, volumesClient, diskRegistrationTestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(volumesClient.createRequests).To(BeEmpty())
		Expect(computeClient.object.GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal("volume-newer"))
		Expect(registrationStates(computeClient.object)["boot"]).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED))
		Expect(computeClient.updateRequests[2].GetLock()).To(BeTrue())
	})

	It("keeps UNKNOWN after cancellation when Volume Create may have committed", func() {
		computeClient, volumesClient := registrationTestClients()
		volumesClient.create = func(context.Context, *privatev1.VolumesCreateRequest) (*privatev1.VolumesCreateResponse, error) {
			return nil, status.Error(codes.Unavailable, "Create response lost")
		}

		err := ReconcileDiskRegistrations(context.Background(), computeClient, volumesClient, diskRegistrationTestID)
		Expect(err).To(HaveOccurred())
		Expect(diskRegistrationCompleteForTest(computeClient.object)).To(BeFalse())
		Expect(volumesClient.createRequests).To(HaveLen(1))
		computeClient.object.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		computeClient.object.GetMetadata().SetVersion(computeClient.object.GetMetadata().GetVersion() + 1)

		err = ReconcileDiskRegistrations(context.Background(), computeClient, volumesClient, diskRegistrationTestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(diskRegistrationCompleteForTest(computeClient.object)).To(BeFalse())
		Expect(volumesClient.createRequests).To(HaveLen(1))
		Expect(registrationStates(computeClient.object)["boot"]).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN))

		deleting, _, _ := newVolumeCleanupTask(nil,
			cloneDiskRegistrations(computeClient.object.GetStatus().GetVolumeRegistration()))
		Expect(deleting.delete(context.Background())).To(HaveOccurred())
		Expect(hasFinalizer(deleting.computeInstance)).To(BeTrue())
	})

	It("does no work when the parent finalizer is absent", func() {
		computeClient, volumesClient := registrationTestClients()
		computeClient.object.GetMetadata().SetFinalizers(nil)

		err := ReconcileDiskRegistrations(context.Background(), computeClient, volumesClient, diskRegistrationTestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(diskRegistrationCompleteForTest(computeClient.object)).To(BeFalse())
		Expect(computeClient.updateRequests).To(BeEmpty())
		Expect(volumesClient.createRequests).To(BeEmpty())
		Expect(volumesClient.listRequests).To(BeEmpty())
	})

	It("recovers the committed ID when the ID status update fails", func() {
		computeClient, volumesClient := registrationTestClients()
		computeClient.failBeforeUpdateCalls = map[int]error{
			3: status.Error(codes.Unavailable, "status write unavailable"),
		}
		var committed *privatev1.Volume
		createCalls := 0
		volumesClient.create = func(_ context.Context, request *privatev1.VolumesCreateRequest) (*privatev1.VolumesCreateResponse, error) {
			createCalls++
			committed = registrationTestVolume(request, "volume-committed", "system")
			return privatev1.VolumesCreateResponse_builder{Object: committed}.Build(), nil
		}
		volumesClient.list = func(_ context.Context, _ *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			return registrationTestListResponse([]*privatev1.Volume{committed}, 1), nil
		}

		err := ReconcileDiskRegistrations(context.Background(), computeClient, volumesClient, diskRegistrationTestID)
		Expect(err).To(HaveOccurred())
		Expect(diskRegistrationCompleteForTest(computeClient.object)).To(BeFalse())
		Expect(registrationStates(computeClient.object)["boot"]).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN))

		err = ReconcileDiskRegistrations(context.Background(), computeClient, volumesClient, diskRegistrationTestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(diskRegistrationCompleteForTest(computeClient.object)).To(BeTrue())
		Expect(createCalls).To(Equal(1))
		Expect(computeClient.object.GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal("volume-committed"))
	})

	It("never overwrites or recreates a persisted ID", func() {
		computeClient, volumesClient := registrationTestClients()
		computeClient.object.GetStatus().SetVolumes([]*privatev1.ComputeInstanceVolume{
			privatev1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "volume-original"}.Build(),
		})
		computeClient.object.GetStatus().SetVolumeRegistration([]*privatev1.ComputeInstanceVolumeRegistration{
			privatev1.ComputeInstanceVolumeRegistration_builder{
				DiskKey: "boot",
				State:   privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED,
			}.Build(),
		})
		volumesClient.create = func(context.Context, *privatev1.VolumesCreateRequest) (*privatev1.VolumesCreateResponse, error) {
			Fail("Create must not be called for a persisted ID")
			return nil, nil
		}

		err := ReconcileDiskRegistrations(context.Background(), computeClient, volumesClient, diskRegistrationTestID)
		Expect(err).NotTo(HaveOccurred())
		Expect(diskRegistrationCompleteForTest(computeClient.object)).To(BeTrue())
		Expect(computeClient.object.GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal("volume-original"))
		Expect(volumesClient.createRequests).To(BeEmpty())
	})
})

func diskRegistrationCompleteForTest(instance *privatev1.ComputeInstance) bool {
	prepared, err := prepareDiskRegistrations(instance)
	Expect(err).NotTo(HaveOccurred())
	complete, err := diskRegistrationsComplete(instance, prepared)
	Expect(err).NotTo(HaveOccurred())
	return complete
}

type registrationComputeInstancesClient struct {
	privatev1.ComputeInstancesClient
	object                *privatev1.ComputeInstance
	getErr                error
	getCalls              int
	beforeGet             func(int)
	updateRequests        []*privatev1.ComputeInstancesUpdateRequest
	failBeforeUpdateCalls map[int]error
	failAfterUpdateCalls  map[int]error
	beforeUpdate          func(int)
	afterUpdate           func(int)
}

func (c *registrationComputeInstancesClient) Get(
	_ context.Context,
	request *privatev1.ComputeInstancesGetRequest,
	_ ...grpc.CallOption,
) (*privatev1.ComputeInstancesGetResponse, error) {
	c.getCalls++
	if c.beforeGet != nil {
		c.beforeGet(c.getCalls)
	}
	if c.getErr != nil {
		return nil, c.getErr
	}
	if c.object.GetId() != request.GetId() {
		return nil, status.Error(codes.NotFound, "ComputeInstance not found")
	}
	return privatev1.ComputeInstancesGetResponse_builder{
		Object: proto.Clone(c.object).(*privatev1.ComputeInstance),
	}.Build(), nil
}

func (c *registrationComputeInstancesClient) Update(
	_ context.Context,
	request *privatev1.ComputeInstancesUpdateRequest,
	_ ...grpc.CallOption,
) (*privatev1.ComputeInstancesUpdateResponse, error) {
	c.updateRequests = append(c.updateRequests, proto.Clone(request).(*privatev1.ComputeInstancesUpdateRequest))
	call := len(c.updateRequests)
	if c.beforeUpdate != nil {
		c.beforeUpdate(call)
	}
	if err := c.failBeforeUpdateCalls[call]; err != nil {
		return nil, err
	}
	if request.GetLock() && request.GetObject().GetMetadata().GetVersion() != c.object.GetMetadata().GetVersion() {
		return nil, status.Error(codes.Aborted, "object version changed")
	}
	if !c.object.HasStatus() {
		c.object.SetStatus(&privatev1.ComputeInstanceStatus{})
	}
	paths := request.GetUpdateMask().GetPaths()
	if slices.Contains(paths, "status.volume_registration") {
		c.object.GetStatus().SetVolumeRegistration(cloneDiskRegistrations(request.GetObject().GetStatus().GetVolumeRegistration()))
	}
	if slices.Contains(paths, "status.volumes") {
		c.object.GetStatus().SetVolumes(cloneComputeInstanceVolumes(request.GetObject().GetStatus().GetVolumes()))
	}
	if slices.Contains(paths, "status.conditions") {
		status := proto.Clone(request.GetObject().GetStatus()).(*privatev1.ComputeInstanceStatus)
		c.object.GetStatus().SetConditions(status.GetConditions())
	}
	c.object.GetMetadata().SetVersion(c.object.GetMetadata().GetVersion() + 1)
	if err := c.failAfterUpdateCalls[call]; err != nil {
		return nil, err
	}
	response := privatev1.ComputeInstancesUpdateResponse_builder{
		Object: proto.Clone(c.object).(*privatev1.ComputeInstance),
	}.Build()
	if c.afterUpdate != nil {
		c.afterUpdate(call)
	}
	return response, nil
}

type registrationVolumesClient struct {
	privatev1.VolumesClient
	create         func(context.Context, *privatev1.VolumesCreateRequest) (*privatev1.VolumesCreateResponse, error)
	list           func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error)
	createRequests []*privatev1.VolumesCreateRequest
	listRequests   []*privatev1.VolumesListRequest
}

func (c *registrationVolumesClient) Create(
	ctx context.Context,
	request *privatev1.VolumesCreateRequest,
	_ ...grpc.CallOption,
) (*privatev1.VolumesCreateResponse, error) {
	c.createRequests = append(c.createRequests, proto.Clone(request).(*privatev1.VolumesCreateRequest))
	if c.create == nil {
		return nil, status.Error(codes.Unimplemented, "Create not configured")
	}
	return c.create(ctx, request)
}

func (c *registrationVolumesClient) List(
	ctx context.Context,
	request *privatev1.VolumesListRequest,
	_ ...grpc.CallOption,
) (*privatev1.VolumesListResponse, error) {
	c.listRequests = append(c.listRequests, proto.Clone(request).(*privatev1.VolumesListRequest))
	if c.list == nil {
		return registrationTestListResponse(nil, 0), nil
	}
	return c.list(ctx, request)
}

func registrationTestClients() (*registrationComputeInstancesClient, *registrationVolumesClient) {
	instance := diskRegistrationTestInstance()
	instance.GetSpec().SetAdditionalDisks(nil)
	instance.GetMetadata().SetVersion(1)
	return &registrationComputeInstancesClient{object: instance}, &registrationVolumesClient{}
}

func registrationTestVolume(request *privatev1.VolumesCreateRequest, id, creator string) *privatev1.Volume {
	volume := proto.Clone(request.GetObject()).(*privatev1.Volume)
	volume.SetId(id)
	volume.GetMetadata().SetCreator(creator)
	volume.SetStatus(privatev1.VolumeStatus_builder{
		State: privatev1.VolumeState_VOLUME_STATE_CREATING,
	}.Build())
	return volume
}

func registrationTestListResponse(items []*privatev1.Volume, total int32) *privatev1.VolumesListResponse {
	return privatev1.VolumesListResponse_builder{
		Items: items,
		Size:  int32(len(items)),
		Total: total,
	}.Build()
}

func oneRegistrationVolumeResponse(
	mutate func(*privatev1.Volume),
) func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
	return func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
		volume := registrationTestVolume(requestFromListFixture(), "volume-candidate", "system")
		mutate(volume)
		return registrationTestListResponse([]*privatev1.Volume{volume}, 1), nil
	}
}

func preparedDiskRegistrationForTest() preparedDiskRegistration {
	registrations, err := prepareDiskRegistrations(diskRegistrationTestInstance())
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return registrations[0]
}

func requestFromListFixture() *privatev1.VolumesCreateRequest {
	return preparedDiskRegistrationForTest().request
}

func registrationCELFilter(instance *privatev1.ComputeInstance, request *privatev1.VolumesCreateRequest) string {
	return strings.Join([]string{
		"this.metadata.name == " + strconv.Quote(request.GetObject().GetMetadata().GetName()),
		"this.metadata.tenant == " + strconv.Quote(instance.GetMetadata().GetTenant()),
		"this.metadata.project == " + strconv.Quote(instance.GetMetadata().GetProject()),
	}, " && ")
}

var _ privatev1.ComputeInstancesClient = (*registrationComputeInstancesClient)(nil)
var _ privatev1.VolumesClient = (*registrationVolumesClient)(nil)
