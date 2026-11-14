package computeinstance

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Disk inventory proof", func() {
	It("does not look up a parent for an absent Volume", func() {
		matched, err := IsComputeInstanceVolumeInventory(context.Background(), nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(matched).To(BeFalse())
	})

	fixture := func(diskKey string) (*registrationComputeInstancesClient, *privatev1.Volume) {
		parent := diskRegistrationTestInstance()
		prepared, err := prepareDiskRegistrations(parent)
		Expect(err).NotTo(HaveOccurred())
		candidate, found := preparedDiskByKey(prepared, diskKey)
		Expect(found).To(BeTrue())
		parent.GetStatus().SetVolumeRegistration([]*privatev1.ComputeInstanceVolumeRegistration{
			privatev1.ComputeInstanceVolumeRegistration_builder{
				DiskKey: diskKey, State: privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN,
			}.Build(),
		})
		volume := registrationTestVolume(candidate.request, "inventory-volume", "system")
		return &registrationComputeInstancesClient{object: parent}, volume
	}

	DescribeTable("keeps proven boot and data records unchanged",
		func(diskKey string, resolved, deleting bool) {
			client, volume := fixture(diskKey)
			if resolved {
				client.object.GetStatus().GetVolumeRegistration()[0].SetState(privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED)
				client.object.GetStatus().SetVolumes([]*privatev1.ComputeInstanceVolume{
					privatev1.ComputeInstanceVolume_builder{DiskKey: diskKey, VolumeId: volume.GetId()}.Build(),
				})
			}
			if deleting {
				client.object.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
			}
			original := proto.Clone(volume)
			matched, err := IsComputeInstanceVolumeInventory(context.Background(), client.getInventoryParent, volume)
			Expect(err).NotTo(HaveOccurred())
			Expect(matched).To(BeTrue())
			Expect(client.getCalls).To(Equal(1))
			Expect(proto.Equal(volume, original)).To(BeTrue())
			Expect(client.updateRequests).To(BeEmpty())
		},
		Entry("UNKNOWN boot", "boot", false, false),
		Entry("UNKNOWN data", "disk-1", false, false),
		Entry("resolved boot", "boot", true, false),
		Entry("resolved data", "disk-2", true, false),
		Entry("deleting UNKNOWN parent", "boot", false, true),
		Entry("deleting resolved parent", "boot", true, true),
	)

	DescribeTable("does not adopt caller strings or mismatching records",
		func(mutate func(*registrationComputeInstancesClient, *privatev1.Volume)) {
			client, volume := fixture("boot")
			mutate(client, volume)
			matched, err := IsComputeInstanceVolumeInventory(context.Background(), client.getInventoryParent, volume)
			Expect(err).NotTo(HaveOccurred())
			Expect(matched).To(BeFalse())
		},
		Entry("prefix only", func(_ *registrationComputeInstancesClient, volume *privatev1.Volume) {
			volume.GetMetadata().SetName("ci-anything")
		}),
		Entry("missing matching parent", func(client *registrationComputeInstancesClient, _ *privatev1.Volume) {
			client.object.SetId("another-parent")
		}),
		Entry("wrong tenant", func(_ *registrationComputeInstancesClient, volume *privatev1.Volume) {
			volume.GetMetadata().SetTenant("another-tenant")
		}),
		Entry("wrong project", func(_ *registrationComputeInstancesClient, volume *privatev1.Volume) {
			volume.GetMetadata().SetProject("another-project")
		}),
		Entry("wrong creator", func(_ *registrationComputeInstancesClient, volume *privatev1.Volume) {
			volume.GetMetadata().SetCreator("caller")
		}),
		Entry("wrong size", func(_ *registrationComputeInstancesClient, volume *privatev1.Volume) { volume.GetSpec().SetSizeGib(41) }),
		Entry("wrong tier", func(_ *registrationComputeInstancesClient, volume *privatev1.Volume) {
			volume.GetSpec().SetStorageTier("another-tier")
		}),
		Entry("wrong access mode", func(_ *registrationComputeInstancesClient, volume *privatev1.Volume) {
			volume.GetSpec().SetAccessMode(privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_MANY)
		}),
		Entry("deleting record", func(_ *registrationComputeInstancesClient, volume *privatev1.Volume) {
			volume.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		}),
		Entry("DELETING state", func(_ *registrationComputeInstancesClient, volume *privatev1.Volume) {
			volume.GetStatus().SetState(privatev1.VolumeState_VOLUME_STATE_DELETING)
		}),
		Entry("DELETED state", func(_ *registrationComputeInstancesClient, volume *privatev1.Volume) {
			volume.GetStatus().SetState(privatev1.VolumeState_VOLUME_STATE_DELETED)
		}),
		Entry("missing intent", func(client *registrationComputeInstancesClient, _ *privatev1.Volume) {
			client.object.GetStatus().SetVolumeRegistration(nil)
		}),
		Entry("missing spec", func(_ *registrationComputeInstancesClient, volume *privatev1.Volume) { volume.SetSpec(nil) }),
		Entry("missing topology", func(_ *registrationComputeInstancesClient, volume *privatev1.Volume) {
			volume.GetSpec().SetTopology(nil)
		}),
		Entry("empty node", func(_ *registrationComputeInstancesClient, volume *privatev1.Volume) {
			volume.GetSpec().GetTopology().SetSegments(map[string]string{"osac.io/node": ""})
		}),
		Entry("wrong topology key", func(_ *registrationComputeInstancesClient, volume *privatev1.Volume) {
			volume.GetSpec().GetTopology().SetSegments(map[string]string{"node": diskRegistrationPlaceholderNode})
		}),
		Entry("near topology", func(_ *registrationComputeInstancesClient, volume *privatev1.Volume) {
			volume.GetSpec().GetTopology().SetSegments(map[string]string{"osac.io/node": diskRegistrationPlaceholderNode + "-other"})
		}),
		Entry("ordinary node", func(_ *registrationComputeInstancesClient, volume *privatev1.Volume) {
			volume.GetSpec().GetTopology().SetSegments(map[string]string{"osac.io/node": "worker-1"})
		}),
		Entry("extra topology", func(_ *registrationComputeInstancesClient, volume *privatev1.Volume) {
			volume.GetSpec().GetTopology().SetSegments(map[string]string{"osac.io/node": diskRegistrationPlaceholderNode, "zone": "a"})
		}),
	)

	DescribeTable("routes unrelated old-node requests normally despite another mapped inventory record",
		func(differentProject bool) {
			client, volume := fixture("boot")
			client.object.GetStatus().SetVolumes([]*privatev1.ComputeInstanceVolume{
				privatev1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "another-volume"}.Build(),
			})
			if differentProject {
				volume.GetMetadata().SetProject("another-project")
			} else {
				volume.GetSpec().SetSizeGib(41)
			}
			matched, err := IsComputeInstanceVolumeInventory(context.Background(), client.getInventoryParent, volume)
			Expect(err).NotTo(HaveOccurred())
			Expect(matched).To(BeFalse())
		},
		Entry("different project", true),
		Entry("different disk spec", false),
	)

	DescribeTable("recovers ambiguous records without changing their spec or status",
		func(diskKey string, state privatev1.VolumeState) {
			client, volume := fixture(diskKey)
			volume.GetStatus().SetState(state)
			original := proto.Clone(volume)
			volumes := &registrationVolumesClient{list: func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
				return registrationTestListResponse([]*privatev1.Volume{volume}, 1), nil
			}}
			Expect(reconcileDiskRegistrationCandidate(context.Background(), client, volumes, client.object.GetId(), diskKey)).To(Succeed())
			Expect(reconcileDiskRegistrationCandidate(context.Background(), client, volumes, client.object.GetId(), diskKey)).To(Succeed())
			Expect(volumes.createRequests).To(BeEmpty())
			Expect(volumes.listRequests).To(HaveLen(1))
			Expect(client.object.GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal(volume.GetId()))
			Expect(client.object.GetStatus().GetVolumeRegistration()[0].GetState()).To(Equal(privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED))
			matched, err := IsComputeInstanceVolumeInventory(context.Background(), client.getInventoryParent, volume)
			Expect(err).NotTo(HaveOccurred())
			Expect(matched).To(BeTrue())
			Expect(proto.Equal(volume, original)).To(BeTrue())
		},
		Entry("boot still CREATING", "boot", privatev1.VolumeState_VOLUME_STATE_CREATING),
		Entry("data AVAILABLE", "disk-1", privatev1.VolumeState_VOLUME_STATE_AVAILABLE),
		Entry("data FAILED is not repaired", "disk-2", privatev1.VolumeState_VOLUME_STATE_FAILED),
	)

	DescribeTable("does not adopt unmapped noncommitted records",
		func(diskKey string, state privatev1.ComputeInstanceVolumeRegistration_State) {
			client, volume := fixture(diskKey)
			client.object.GetStatus().GetVolumeRegistration()[0].SetState(state)
			originalVolume := proto.Clone(volume)
			originalParent := proto.Clone(client.object)
			matched, err := IsComputeInstanceVolumeInventory(context.Background(), client.getInventoryParent, volume)
			Expect(err).NotTo(HaveOccurred())
			Expect(matched).To(BeFalse())
			Expect(proto.Equal(volume, originalVolume)).To(BeTrue())
			Expect(proto.Equal(client.object, originalParent)).To(BeTrue())
			Expect(client.updateRequests).To(BeEmpty())
		},
		Entry("never attempted boot", "boot", privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED),
		Entry("never attempted data", "disk-1", privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED),
		Entry("noncommitted boot", "boot", privatev1.ComputeInstanceVolumeRegistration_STATE_NON_COMMITTED_FAILURE),
		Entry("noncommitted data", "disk-1", privatev1.ComputeInstanceVolumeRegistration_STATE_NON_COMMITTED_FAILURE),
	)

	DescribeTable("surfaces inconsistent persisted proof rather than provisioning",
		func(mutate func(*privatev1.ComputeInstance)) {
			client, volume := fixture("boot")
			mutate(client.object)
			matched, err := IsComputeInstanceVolumeInventory(context.Background(), client.getInventoryParent, volume)
			Expect(err).To(HaveOccurred())
			Expect(matched).To(BeFalse())
		},
		Entry("conflicting ID", func(parent *privatev1.ComputeInstance) {
			parent.GetStatus().SetVolumes([]*privatev1.ComputeInstanceVolume{privatev1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "another-volume"}.Build()})
		}),
		Entry("resolved without mapping", func(parent *privatev1.ComputeInstance) {
			parent.GetStatus().GetVolumeRegistration()[0].SetState(privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED)
		}),
		Entry("mapped never attempted", func(parent *privatev1.ComputeInstance) {
			parent.GetStatus().GetVolumeRegistration()[0].SetState(privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED)
			parent.GetStatus().SetVolumes([]*privatev1.ComputeInstanceVolume{privatev1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "inventory-volume"}.Build()})
		}),
		Entry("mapped noncommitted", func(parent *privatev1.ComputeInstance) {
			parent.GetStatus().GetVolumeRegistration()[0].SetState(privatev1.ComputeInstanceVolumeRegistration_STATE_NON_COMMITTED_FAILURE)
			parent.GetStatus().SetVolumes([]*privatev1.ComputeInstanceVolume{privatev1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "inventory-volume"}.Build()})
		}),
		Entry("invalid state", func(parent *privatev1.ComputeInstance) {
			parent.GetStatus().GetVolumeRegistration()[0].SetState(privatev1.ComputeInstanceVolumeRegistration_State(99))
		}),
		Entry("missing UNKNOWN finalizer", func(parent *privatev1.ComputeInstance) { parent.GetMetadata().SetFinalizers(nil) }),
		Entry("empty mapped ID", func(parent *privatev1.ComputeInstance) {
			parent.GetStatus().SetVolumes([]*privatev1.ComputeInstanceVolume{privatev1.ComputeInstanceVolume_builder{DiskKey: "boot"}.Build()})
		}),
		Entry("duplicate intent", func(parent *privatev1.ComputeInstance) {
			parent.GetStatus().SetVolumeRegistration(append(parent.GetStatus().GetVolumeRegistration(), proto.Clone(parent.GetStatus().GetVolumeRegistration()[0]).(*privatev1.ComputeInstanceVolumeRegistration)))
		}),
	)

	It("requires persisted UNKNOWN and system attribution even before a Create receives an ID", func() {
		client, volume := fixture("boot")
		volume.SetId("")
		matched, err := IsComputeInstanceVolumeInventory(context.Background(), client.getInventoryParent, volume)
		Expect(err).NotTo(HaveOccurred())
		Expect(matched).To(BeTrue())
		volume.GetMetadata().SetCreator("caller")
		matched, err = IsComputeInstanceVolumeInventory(context.Background(), client.getInventoryParent, volume)
		Expect(err).NotTo(HaveOccurred())
		Expect(matched).To(BeFalse())
		Expect(client.getCalls).To(Equal(1))
	})

	It("preserves proof lookup errors and cancellation", func() {
		client, volume := fixture("boot")
		client.getErr = status.Error(codes.Unavailable, "parent lookup unavailable")
		matched, err := IsComputeInstanceVolumeInventory(context.Background(), client.getInventoryParent, volume)
		Expect(matched).To(BeFalse())
		Expect(status.Code(err)).To(Equal(codes.Unavailable))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		matched, err = IsComputeInstanceVolumeInventory(ctx, client.getInventoryParent, volume)
		Expect(matched).To(BeFalse())
		Expect(err).To(MatchError(context.Canceled))
	})

	It("rejects a lookup response containing a different parent", func() {
		client, volume := fixture("boot")
		client.object.SetId("another-parent")
		matched, err := IsComputeInstanceVolumeInventory(context.Background(),
			func(context.Context, string) (*privatev1.ComputeInstance, error) { return client.object, nil }, volume)
		Expect(matched).To(BeFalse())
		Expect(err).To(HaveOccurred())
	})
})

func (client *registrationComputeInstancesClient) getInventoryParent(ctx context.Context, id string) (*privatev1.ComputeInstance, error) {
	response, err := client.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: id}.Build())
	return response.GetObject(), err
}
