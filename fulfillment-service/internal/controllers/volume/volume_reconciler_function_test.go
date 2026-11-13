/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package volume

import (
	"context"
	"errors"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/annotations"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func newVolumeCR(id, namespace, name string, deletionTimestamp *metav1.Time) *osacv1alpha1.Volume {
	obj := &osacv1alpha1.Volume{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				labels.VolumeUuid: id,
			},
		},
	}
	if deletionTimestamp != nil {
		obj.SetDeletionTimestamp(deletionTimestamp)
		obj.SetFinalizers([]string{"osac.openshift.io/volume"})
	}
	return obj
}

func hasFinalizer(volume *privatev1.Volume) bool {
	return slices.Contains(volume.GetMetadata().GetFinalizers(), finalizers.Controller)
}

type inventoryParentClient struct {
	privatev1.ComputeInstancesClient
	parent        *privatev1.ComputeInstance
	err           error
	calls         int
	emptyResponse bool
}

func (client *inventoryParentClient) Get(_ context.Context, request *privatev1.ComputeInstancesGetRequest, _ ...grpc.CallOption) (*privatev1.ComputeInstancesGetResponse, error) {
	client.calls++
	if client.err != nil {
		return nil, client.err
	}
	if client.emptyResponse {
		return nil, nil
	}
	if client.parent.GetId() != request.GetId() {
		return nil, status.Error(codes.NotFound, "parent not found")
	}
	return privatev1.ComputeInstancesGetResponse_builder{Object: client.parent}.Build(), nil
}

func expectDeletionRetry(err error) {
	var retryable interface{ RequeueAfter() time.Duration }
	Expect(errors.As(err, &retryable)).To(BeTrue())
	Expect(retryable.RequeueAfter()).To(Equal(time.Second))
}

func newTaskForDelete(volumeID, hubID string, hubCache controllers.HubCache) *task {
	volume := privatev1.Volume_builder{
		Id: volumeID,
		Metadata: privatev1.Metadata_builder{
			Finalizers: []string{finalizers.Controller},
		}.Build(),
		Status: privatev1.VolumeStatus_builder{
			Hub: hubID,
		}.Build(),
	}.Build()

	f := &function{
		logger:   logger,
		hubCache: hubCache,
	}

	return &task{
		r:      f,
		volume: volume,
	}
}

var _ = Describe("buildSpec", func() {
	It("maps CSI topology segments", func() {
		t := &task{
			volume: privatev1.Volume_builder{
				Id: "vol-buildspec-topology",
				Spec: privatev1.VolumeSpec_builder{
					StorageTier: "gold",
					SizeGib:     100,
					AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					Topology: privatev1.VolumeTopology_builder{
						Segments: map[string]string{
							"osac.io/node":                "worker-1",
							"topology.kubernetes.io/zone": "zone-a",
						},
					}.Build(),
				}.Build(),
			}.Build(),
		}

		spec := t.buildSpec()

		Expect(spec.Topology).To(Equal(&osacv1alpha1.VolumeTopology{
			Segments: map[string]string{
				"osac.io/node":                "worker-1",
				"topology.kubernetes.io/zone": "zone-a",
			},
		}))
	})

	It("maps all spec fields including access mode enum", func() {
		t := &task{
			volume: privatev1.Volume_builder{
				Id: "vol-buildspec-1",
				Spec: privatev1.VolumeSpec_builder{
					StorageTier: "gold",
					SizeGib:     100,
					AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
				}.Build(),
			}.Build(),
		}

		spec := t.buildSpec()

		Expect(spec.StorageTier).To(Equal("gold"))
		Expect(spec.SizeGiB).To(Equal(int64(100)))
		Expect(spec.AccessMode).To(Equal(osacv1alpha1.VolumeAccessModeReadWriteOnce))
	})

	It("maps ReadWriteMany access mode", func() {
		t := &task{
			volume: privatev1.Volume_builder{
				Id: "vol-buildspec-rwm",
				Spec: privatev1.VolumeSpec_builder{
					StorageTier: "silver",
					SizeGib:     50,
					AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_MANY,
				}.Build(),
			}.Build(),
		}

		spec := t.buildSpec()
		Expect(spec.AccessMode).To(Equal(osacv1alpha1.VolumeAccessModeReadWriteMany))
	})

	It("maps ReadWriteOncePod access mode", func() {
		t := &task{
			volume: privatev1.Volume_builder{
				Id: "vol-buildspec-rwop",
				Spec: privatev1.VolumeSpec_builder{
					StorageTier: "silver",
					SizeGib:     50,
					AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE_POD,
				}.Build(),
			}.Build(),
		}

		spec := t.buildSpec()
		Expect(spec.AccessMode).To(Equal(osacv1alpha1.VolumeAccessModeReadWriteOncePod))
	})

	It("maps ReadOnlyMany access mode", func() {
		t := &task{
			volume: privatev1.Volume_builder{
				Id: "vol-buildspec-rom",
				Spec: privatev1.VolumeSpec_builder{
					StorageTier: "silver",
					SizeGib:     50,
					AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_ONLY_MANY,
				}.Build(),
			}.Build(),
		}

		spec := t.buildSpec()
		Expect(spec.AccessMode).To(Equal(osacv1alpha1.VolumeAccessModeReadOnlyMany))
	})

	It("defaults unspecified access mode to ReadWriteOnce", func() {
		t := &task{
			volume: privatev1.Volume_builder{
				Id: "vol-buildspec-default",
				Spec: privatev1.VolumeSpec_builder{
					StorageTier: "gold",
					SizeGib:     100,
					AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_UNSPECIFIED,
				}.Build(),
			}.Build(),
		}

		spec := t.buildSpec()
		Expect(spec.AccessMode).To(Equal(osacv1alpha1.VolumeAccessModeReadWriteOnce))
	})
})

var _ = Describe("setDefaults", func() {
	It("sets CREATING state when status is unspecified", func() {
		t := &task{
			volume: privatev1.Volume_builder{
				Id: "vol-defaults-1",
			}.Build(),
		}

		t.setDefaults()

		Expect(t.volume.GetStatus().GetState()).To(
			Equal(privatev1.VolumeState_VOLUME_STATE_CREATING),
		)
	})

	It("does not overwrite existing state", func() {
		t := &task{
			volume: privatev1.Volume_builder{
				Id: "vol-defaults-existing",
				Status: privatev1.VolumeStatus_builder{
					State: privatev1.VolumeState_VOLUME_STATE_AVAILABLE,
				}.Build(),
			}.Build(),
		}

		t.setDefaults()

		Expect(t.volume.GetStatus().GetState()).To(
			Equal(privatev1.VolumeState_VOLUME_STATE_AVAILABLE),
		)
	})

	It("creates status if it doesn't exist", func() {
		t := &task{
			volume: privatev1.Volume_builder{
				Id: "vol-defaults-no-status",
			}.Build(),
		}

		Expect(t.volume.HasStatus()).To(BeFalse())

		t.setDefaults()

		Expect(t.volume.HasStatus()).To(BeTrue())
		Expect(t.volume.GetStatus().GetState()).To(
			Equal(privatev1.VolumeState_VOLUME_STATE_CREATING),
		)
	})
})

var _ = Describe("update", func() {
	DescribeTable("preserves inventory only with persisted parent proof",
		func(scenario string, ordinary, expectError bool) {
			parent := privatev1.ComputeInstance_builder{
				Id:       "parent-1",
				Metadata: privatev1.Metadata_builder{Tenant: "tenant-1", Project: "project-1", Finalizers: []string{finalizers.Controller}}.Build(),
				Spec: privatev1.ComputeInstanceSpec_builder{
					BootDisk: privatev1.ComputeInstanceDisk_builder{SizeGib: proto.Int32(10), StorageTier: privatev1.StorageTierReference_builder{Name: "gold"}.Build()}.Build(),
				}.Build(),
				Status: privatev1.ComputeInstanceStatus_builder{VolumeRegistration: []*privatev1.ComputeInstanceVolumeRegistration{
					privatev1.ComputeInstanceVolumeRegistration_builder{DiskKey: "boot", State: privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN}.Build(),
				}}.Build(),
			}.Build()
			volume := privatev1.Volume_builder{
				Id:       "inventory-volume",
				Metadata: privatev1.Metadata_builder{Name: "ci-parent-1-boot", Tenant: "tenant-1", Project: "project-1", Creator: "system", Finalizers: []string{finalizers.Controller}}.Build(),
				Spec: privatev1.VolumeSpec_builder{StorageTier: "gold", SizeGib: 10, AccessMode: privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					Topology: privatev1.VolumeTopology_builder{Segments: map[string]string{"osac.io/node": "osac-volume-registration-pending"}}.Build(),
				}.Build(),
				Status: privatev1.VolumeStatus_builder{State: privatev1.VolumeState_VOLUME_STATE_AVAILABLE, StateTransitionTime: controllers.NowTruncated(), Provider: "lvms", Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK}.Build(),
			}.Build()
			client := &inventoryParentClient{parent: parent}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "wrong creator":
				volume.GetMetadata().SetCreator("caller")
			case "extra topology":
				volume.GetSpec().GetTopology().SetSegments(map[string]string{"osac.io/node": "osac-volume-registration-pending", "zone": "a"})
			case "near topology":
				volume.GetSpec().GetTopology().SetSegments(map[string]string{"osac.io/node": "osac-volume-registration-pending-other"})
			case "empty topology":
				volume.GetSpec().GetTopology().SetSegments(nil)
			case "never attempted":
				parent.GetStatus().GetVolumeRegistration()[0].SetState(privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED)
			case "noncommitted":
				parent.GetStatus().GetVolumeRegistration()[0].SetState(privatev1.ComputeInstanceVolumeRegistration_STATE_NON_COMMITTED_FAILURE)
			case "resolved":
				parent.GetStatus().GetVolumeRegistration()[0].SetState(privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED)
				parent.GetStatus().SetVolumes([]*privatev1.ComputeInstanceVolume{privatev1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: volume.GetId()}.Build()})
			case "ordinary":
				volume.GetMetadata().SetName("pvc-collision")
			case "unrelated project", "unrelated spec":
				parent.GetStatus().SetVolumes([]*privatev1.ComputeInstanceVolume{privatev1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "another-volume"}.Build()})
				if scenario == "unrelated project" {
					volume.GetMetadata().SetProject("another-project")
				} else {
					volume.GetSpec().SetSizeGib(41)
				}
			case "missing parent":
				client.parent = nil
			case "missing intent":
				parent.GetStatus().SetVolumeRegistration(nil)
			case "lookup unavailable":
				client.err = status.Error(codes.Unavailable, "parent unavailable")
			case "lookup denied":
				client.err = status.Error(codes.PermissionDenied, "parent denied")
			case "empty response":
				client.emptyResponse = true
			case "canceled":
				cancel()
			case "conflicting mapping":
				parent.GetStatus().SetVolumes([]*privatev1.ComputeInstanceVolume{privatev1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "another-volume"}.Build()})
			case "deleting parent":
				parent.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
			}
			scheme := runtime.NewScheme()
			Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
			hubClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&osacv1alpha1.Volume{}).Build()
			ctrl := gomock.NewController(GinkgoT())
			hubs := controllers.NewMockHubCache(ctrl)
			if ordinary {
				volume.GetStatus().SetState(privatev1.VolumeState_VOLUME_STATE_CREATING)
				volume.GetStatus().SetHub("hub-1")
				hubs.EXPECT().Get(gomock.Any(), "hub-1").Return(&controllers.HubEntry{Namespace: "test-ns", Client: hubClient}, nil)
			}
			original := proto.Clone(volume)
			task := &task{r: &function{logger: logger, hubCache: hubs, computeInstancesClient: client}, volume: volume}
			err := task.update(ctx)
			if expectError {
				Expect(err).To(HaveOccurred())
				task.r.volumesClient = NewMockVolumesClient(ctrl)
				Expect(task.r.run(ctx, volume)).To(HaveOccurred())
				Expect(proto.Equal(volume, original)).To(BeTrue())
				if scenario == "lookup unavailable" {
					Expect(status.Code(err)).To(Equal(codes.Unavailable))
				}
				if scenario == "lookup denied" {
					Expect(status.Code(err)).To(Equal(codes.PermissionDenied))
				}
				if scenario == "canceled" {
					Expect(err).To(MatchError(context.Canceled))
				}
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
			created := &osacv1alpha1.VolumeList{}
			Expect(hubClient.List(context.Background(), created)).To(Succeed())
			if ordinary {
				Expect(created.Items).To(HaveLen(1))
				Expect(created.Items[0].Spec.Topology.Segments).To(Equal(volume.GetSpec().GetTopology().GetSegments()))
				Expect(volume.GetStatus().GetState()).To(Equal(privatev1.VolumeState_VOLUME_STATE_CREATING))
			} else {
				Expect(created.Items).To(BeEmpty())
				Expect(proto.Equal(volume, original)).To(BeTrue())
			}
			if scenario == "UNKNOWN" {
				volume.GetStatus().SetHub("")
				Expect(task.delete(context.Background())).To(Succeed())
				Expect(hasFinalizer(volume)).To(BeFalse())
				Expect(client.calls).To(Equal(1))
			}
		},
		Entry("UNKNOWN before ID persistence", "UNKNOWN", false, false),
		Entry("resolved mapping", "resolved", false, false),
		Entry("deleting parent", "deleting parent", false, false),
		Entry("ordinary caller attribution", "wrong creator", true, false),
		Entry("ordinary extra topology", "extra topology", true, false),
		Entry("ordinary near topology", "near topology", true, false),
		Entry("ordinary empty topology", "empty topology", true, false),
		Entry("ordinary CSI placeholder node", "ordinary", true, false),
		Entry("ordinary CSI never attempted", "never attempted", true, false),
		Entry("ordinary CSI noncommitted", "noncommitted", true, false),
		Entry("ordinary CSI CI-shaped name with unrelated project", "unrelated project", true, false),
		Entry("ordinary CSI CI-shaped name with unrelated spec", "unrelated spec", true, false),
		Entry("generated name alone", "missing parent", true, false),
		Entry("no persisted intent", "missing intent", true, false),
		Entry("lookup failure", "lookup unavailable", false, true),
		Entry("lookup authorization failure", "lookup denied", false, true),
		Entry("indeterminate lookup response", "empty response", false, true),
		Entry("cancellation", "canceled", false, true),
		Entry("conflicting mapped ID", "conflicting mapping", false, true),
	)

	DescribeTable("does not bypass candidate topologies without persisted inventory proof",
		func(segments map[string]string) {
			ctrl := gomock.NewController(GinkgoT())
			hubs := controllers.NewMockHubCache(ctrl)
			hubs.EXPECT().Get(gomock.Any(), "hub-1").Return(nil, errors.New("standalone hub lookup"))
			volume := privatev1.Volume_builder{
				Metadata: privatev1.Metadata_builder{Tenant: "tenant-1", Finalizers: []string{finalizers.Controller}}.Build(),
				Spec: privatev1.VolumeSpec_builder{
					Topology: privatev1.VolumeTopology_builder{Segments: segments}.Build(),
				}.Build(),
				Status: privatev1.VolumeStatus_builder{Hub: "hub-1"}.Build(),
			}.Build()
			task := &task{r: &function{logger: logger, hubCache: hubs}, volume: volume}
			err := task.update(context.Background())
			Expect(err).To(MatchError("standalone hub lookup"))
			Expect(volume.GetStatus().GetState()).To(Equal(privatev1.VolumeState_VOLUME_STATE_CREATING))
			Expect(volume.GetStatus().GetStateTransitionTime()).NotTo(BeNil())
		},
		Entry("legitimate placeholder node", map[string]string{"osac.io/node": "osac-volume-registration-pending"}),
		Entry("standalone node", map[string]string{"osac.io/node": "worker-1"}),
		Entry("missing topology", nil),
		Entry("empty node", map[string]string{"osac.io/node": ""}),
		Entry("not a prefix", map[string]string{"osac.io/node": "osac-volume-registration-pending-other"}),
		Entry("not a superset", map[string]string{"osac.io/node": "osac-volume-registration-pending", "zone": "a"}),
	)

	It("preserves inventory finalizers, tenant validation and no-hub deletion", func() {
		parent := privatev1.ComputeInstance_builder{
			Id:       "parent-1",
			Metadata: privatev1.Metadata_builder{Tenant: "tenant-1", Finalizers: []string{finalizers.Controller}}.Build(),
			Spec: privatev1.ComputeInstanceSpec_builder{BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib: proto.Int32(10), StorageTier: privatev1.StorageTierReference_builder{Name: "gold"}.Build(),
			}.Build()}.Build(),
			Status: privatev1.ComputeInstanceStatus_builder{VolumeRegistration: []*privatev1.ComputeInstanceVolumeRegistration{
				privatev1.ComputeInstanceVolumeRegistration_builder{DiskKey: "boot", State: privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN}.Build(),
			}}.Build(),
		}.Build()
		volume := privatev1.Volume_builder{
			Id:       "inventory-volume",
			Metadata: privatev1.Metadata_builder{Name: "ci-parent-1-boot", Creator: "system", Tenant: "tenant-1"}.Build(),
			Spec: privatev1.VolumeSpec_builder{StorageTier: "gold", SizeGib: 10, AccessMode: privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
				Topology: privatev1.VolumeTopology_builder{Segments: map[string]string{"osac.io/node": "osac-volume-registration-pending"}}.Build(),
			}.Build(),
			Status: privatev1.VolumeStatus_builder{
				State:               privatev1.VolumeState_VOLUME_STATE_AVAILABLE,
				StateTransitionTime: controllers.NowTruncated(),
			}.Build(),
		}.Build()
		transitionTime := volume.GetStatus().GetStateTransitionTime()
		task := &task{r: &function{logger: logger, computeInstancesClient: &inventoryParentClient{parent: parent}}, volume: volume}
		Expect(task.update(context.Background())).To(Succeed())
		Expect(hasFinalizer(volume)).To(BeTrue())
		Expect(task.update(context.Background())).To(Succeed())
		Expect(volume.GetStatus().GetHub()).To(BeEmpty())
		Expect(volume.GetStatus().GetState()).To(Equal(privatev1.VolumeState_VOLUME_STATE_AVAILABLE))
		Expect(volume.GetStatus().GetStateTransitionTime()).To(Equal(transitionTime))
		volume.GetMetadata().SetTenant("")
		Expect(task.update(context.Background())).To(Succeed())
		Expect(volume.GetStatus().GetState()).To(Equal(privatev1.VolumeState_VOLUME_STATE_FAILED))
		Expect(task.delete(context.Background())).To(Succeed())
		Expect(hasFinalizer(volume)).To(BeFalse())
	})

	It("sets tenant and project annotations when creating the hub Volume", func() {
		ctx := context.Background()
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&osacv1alpha1.Volume{}).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), "hub-1").
			Return(&controllers.HubEntry{Namespace: "test-ns", Client: fakeClient}, nil)

		volume := privatev1.Volume_builder{
			Id: "vol-project-annotations",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     "tenant-1",
				Project:    "project-1",
			}.Build(),
			Spec: privatev1.VolumeSpec_builder{
				StorageTier: "gold",
				SizeGib:     10,
				AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
			}.Build(),
			Status: privatev1.VolumeStatus_builder{
				State:    privatev1.VolumeState_VOLUME_STATE_CREATING,
				Hub:      "hub-1",
				Provider: "test-provider",
				Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
			}.Build(),
		}.Build()

		t := &task{
			r:      &function{logger: logger, hubCache: hubCache},
			volume: volume,
		}

		Expect(t.update(ctx)).To(Succeed())

		created := &osacv1alpha1.VolumeList{}
		Expect(fakeClient.List(ctx, created, clnt.MatchingLabels{
			labels.VolumeUuid: volume.GetId(),
		})).To(Succeed())
		Expect(created.Items).To(HaveLen(1))
		Expect(created.Items[0].GetAnnotations()).To(
			HaveKeyWithValue(annotations.Tenant, "tenant-1"))
		Expect(created.Items[0].GetAnnotations()).To(
			HaveKeyWithValue(annotations.Project, "project-1"))
	})

	It("sets tenant and project annotations when updating an existing hub Volume", func() {
		ctx := context.Background()
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		existing := &osacv1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "vol-existing",
				Namespace: "test-ns",
				Labels: map[string]string{
					labels.VolumeUuid: "vol-project-annotations-update",
				},
			},
		}
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(existing).
			WithStatusSubresource(&osacv1alpha1.Volume{}).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), "hub-1").
			Return(&controllers.HubEntry{Namespace: "test-ns", Client: fakeClient}, nil)

		volume := privatev1.Volume_builder{
			Id: "vol-project-annotations-update",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     "tenant-1",
				Project:    "project-1",
			}.Build(),
			Spec: privatev1.VolumeSpec_builder{
				StorageTier: "gold",
				SizeGib:     10,
				AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
			}.Build(),
			Status: privatev1.VolumeStatus_builder{
				State:    privatev1.VolumeState_VOLUME_STATE_CREATING,
				Hub:      "hub-1",
				Provider: "test-provider",
				Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
			}.Build(),
		}.Build()

		t := &task{
			r:      &function{logger: logger, hubCache: hubCache},
			volume: volume,
		}

		Expect(t.update(ctx)).To(Succeed())

		updated := &osacv1alpha1.Volume{}
		Expect(fakeClient.Get(ctx, clnt.ObjectKey{Name: existing.Name, Namespace: existing.Namespace}, updated)).To(Succeed())
		Expect(updated.GetAnnotations()).To(HaveKeyWithValue(annotations.Tenant, "tenant-1"))
		Expect(updated.GetAnnotations()).To(HaveKeyWithValue(annotations.Project, "project-1"))
	})
})

var _ = Describe("validateTenant", func() {
	It("succeeds when a tenant is assigned", func() {
		t := &task{
			volume: privatev1.Volume_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: "tenant-1",
				}.Build(),
			}.Build(),
		}

		err := t.validateTenant()
		Expect(err).ToNot(HaveOccurred())
	})

	It("fails when tenant is empty", func() {
		t := &task{
			volume: privatev1.Volume_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: "",
				}.Build(),
			}.Build(),
		}

		err := t.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant"))
	})

	It("fails when metadata is missing", func() {
		t := &task{
			volume: privatev1.Volume_builder{}.Build(),
		}

		err := t.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant"))
	})
})

var _ = Describe("addFinalizer", func() {
	It("adds finalizer when not present", func() {
		t := &task{
			volume: privatev1.Volume_builder{
				Id: "vol-add-fin",
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{},
				}.Build(),
			}.Build(),
		}

		added := t.addFinalizer()

		Expect(added).To(BeTrue())
		Expect(hasFinalizer(t.volume)).To(BeTrue())
	})

	It("does not add finalizer when already present", func() {
		t := &task{
			volume: privatev1.Volume_builder{
				Id: "vol-has-fin",
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.Controller},
				}.Build(),
			}.Build(),
		}

		added := t.addFinalizer()

		Expect(added).To(BeFalse())
		Expect(hasFinalizer(t.volume)).To(BeTrue())
		Expect(t.volume.GetMetadata().GetFinalizers()).To(HaveLen(1))
	})

	It("creates metadata if it doesn't exist", func() {
		t := &task{
			volume: privatev1.Volume_builder{
				Id: "vol-no-meta",
			}.Build(),
		}

		Expect(t.volume.HasMetadata()).To(BeFalse())

		added := t.addFinalizer()

		Expect(added).To(BeTrue())
		Expect(t.volume.HasMetadata()).To(BeTrue())
		Expect(hasFinalizer(t.volume)).To(BeTrue())
	})
})

var _ = Describe("removeFinalizer", func() {
	It("removes finalizer when present", func() {
		t := &task{
			volume: privatev1.Volume_builder{
				Id: "vol-rm-fin",
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.Controller, "other-finalizer"},
				}.Build(),
			}.Build(),
		}

		Expect(hasFinalizer(t.volume)).To(BeTrue())

		t.removeFinalizer()

		Expect(hasFinalizer(t.volume)).To(BeFalse())
		Expect(t.volume.GetMetadata().GetFinalizers()).To(ContainElement("other-finalizer"))
	})

	It("does nothing when finalizer not present", func() {
		t := &task{
			volume: privatev1.Volume_builder{
				Id: "vol-no-fin",
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{"other-finalizer"},
				}.Build(),
			}.Build(),
		}

		t.removeFinalizer()

		Expect(hasFinalizer(t.volume)).To(BeFalse())
		Expect(t.volume.GetMetadata().GetFinalizers()).To(ContainElement("other-finalizer"))
	})

	It("does nothing when metadata doesn't exist", func() {
		t := &task{
			volume: privatev1.Volume_builder{
				Id: "vol-no-meta-rm",
			}.Build(),
		}

		t.removeFinalizer()

		Expect(t.volume.HasMetadata()).To(BeFalse())
	})
})

var _ = Describe("delete", func() {
	const (
		volumeID     = "vol-delete-id"
		hubID        = "test-hub"
		hubNamespace = "test-ns"
		crName       = "vol-test"
	)

	var (
		ctx  context.Context
		ctrl *gomock.Controller
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	It("removes finalizer when no hub is assigned", func() {
		volume := privatev1.Volume_builder{
			Id: volumeID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Status: privatev1.VolumeStatus_builder{}.Build(),
		}.Build()

		f := &function{logger: logger}
		t := &task{r: f, volume: volume}

		Expect(hasFinalizer(t.volume)).To(BeTrue())

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasFinalizer(t.volume)).To(BeFalse())
	})

	It("removes finalizer when K8s object doesn't exist", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil)

		t := newTaskForDelete(volumeID, hubID, hubCache)
		Expect(hasFinalizer(t.volume)).To(BeTrue())

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasFinalizer(t.volume)).To(BeFalse())
	})

	It("calls hubClient.Delete when K8s object exists without DeletionTimestamp", func() {
		cr := newVolumeCR(volumeID, hubNamespace, crName, nil)

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		deleteCalled := false
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cr).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.DeleteOption) error {
					deleteCalled = true
					return nil
				},
			}).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil)

		t := newTaskForDelete(volumeID, hubID, hubCache)

		err := t.delete(ctx)
		expectDeletionRetry(err)
		Expect(deleteCalled).To(BeTrue())
		Expect(hasFinalizer(t.volume)).To(BeTrue())
	})

	It("does not call hubClient.Delete when K8s object has DeletionTimestamp", func() {
		now := metav1.Now()
		cr := newVolumeCR(volumeID, hubNamespace, crName, &now)

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		deleteCalled := false
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cr).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.DeleteOption) error {
					deleteCalled = true
					return nil
				},
			}).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil)

		t := newTaskForDelete(volumeID, hubID, hubCache)

		err := t.delete(ctx)
		expectDeletionRetry(err)
		Expect(deleteCalled).To(BeFalse())
		Expect(hasFinalizer(t.volume)).To(BeTrue())
	})

	It("removes finalizer when hub is decommissioned", func() {
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(nil, controllers.ErrHubNotFound)

		t := newTaskForDelete(volumeID, hubID, hubCache)
		Expect(hasFinalizer(t.volume)).To(BeTrue())

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasFinalizer(t.volume)).To(BeFalse())
	})

	It("propagates error when hub cache returns transient error", func() {
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(nil, errors.New("connection refused"))

		t := newTaskForDelete(volumeID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("connection refused"))
		Expect(hasFinalizer(t.volume)).To(BeTrue())
	})
})

var _ = Describe("selectHub", func() {
	var (
		ctx  context.Context
		ctrl *gomock.Controller
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	It("uses existing hub from status", func() {
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), "hub-1").
			Return(&controllers.HubEntry{
				Namespace: "hub-ns",
				Client:    fake.NewClientBuilder().Build(),
			}, nil)

		t := &task{
			r: &function{
				logger:   logger,
				hubCache: hubCache,
			},
			volume: privatev1.Volume_builder{
				Id: "vol-existing-hub",
				Status: privatev1.VolumeStatus_builder{
					Hub: "hub-1",
				}.Build(),
			}.Build(),
		}

		err := t.selectHub(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(t.hubId).To(Equal("hub-1"))
		Expect(t.hubNamespace).To(Equal("hub-ns"))
	})

	It("selects a hub randomly when status hub is empty", func() {
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(&controllers.HubEntry{
				Namespace: "selected-ns",
				Client:    fake.NewClientBuilder().Build(),
			}, nil)

		hubsClient := NewMockHubsClient(ctrl)
		hubsClient.EXPECT().
			List(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(privatev1.HubsListResponse_builder{
				Items: []*privatev1.Hub{
					privatev1.Hub_builder{Id: "hub-a"}.Build(),
				},
			}.Build(), nil)

		t := &task{
			r: &function{
				logger:     logger,
				hubCache:   hubCache,
				hubsClient: hubsClient,
			},
			volume: privatev1.Volume_builder{
				Id: "vol-no-hub",
			}.Build(),
		}

		err := t.selectHub(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(t.hubId).To(Equal("hub-a"))
		Expect(t.hubNamespace).To(Equal("selected-ns"))
	})

	It("returns error when no hubs are available", func() {
		hubsClient := NewMockHubsClient(ctrl)
		hubsClient.EXPECT().
			List(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(privatev1.HubsListResponse_builder{
				Items: []*privatev1.Hub{},
			}.Build(), nil)

		t := &task{
			r: &function{
				logger:     logger,
				hubsClient: hubsClient,
			},
			volume: privatev1.Volume_builder{
				Id: "vol-no-hubs",
			}.Build(),
		}

		err := t.selectHub(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no hubs available"))
	})
})

var _ = Describe("Kubernetes validation error handling", func() {
	It("sets state to FAILED when K8s Create returns Invalid error", func() {
		ctx := context.Background()
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.CreateOption) error {
					return apierrors.NewInvalid(
						schema.GroupKind{Group: "osac.openshift.io", Kind: "Volume"},
						"vol-test",
						field.ErrorList{
							field.Invalid(
								field.NewPath("spec", "storageTier"),
								"invalid-value",
								"spec.storageTier is invalid",
							),
						},
					)
				},
			}).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), "hub-1").
			Return(&controllers.HubEntry{Namespace: "test-ns", Client: fakeClient}, nil).
			AnyTimes()

		volumesClient := NewMockVolumesClient(ctrl)
		volumesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.VolumesUpdateRequest, opts ...grpc.CallOption) (*privatev1.VolumesUpdateResponse, error) {
				return &privatev1.VolumesUpdateResponse{Object: req.GetObject()}, nil
			}).
			MinTimes(1)

		volume := privatev1.Volume_builder{
			Id: "vol-validation-test",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     "test-tenant",
			}.Build(),
			Spec: privatev1.VolumeSpec_builder{
				StorageTier: "gold",
				SizeGib:     100,
				AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
			}.Build(),
			Status: privatev1.VolumeStatus_builder{
				State: privatev1.VolumeState_VOLUME_STATE_CREATING,
				Hub:   "hub-1",
			}.Build(),
		}.Build()

		f := &function{
			logger:         logger,
			hubCache:       hubCache,
			volumesClient:  volumesClient,
			maskCalculator: masks.NewCalculator().Build(),
		}

		err := f.run(ctx, volume)
		Expect(err).ToNot(HaveOccurred())

		Expect(volume.GetStatus().GetState()).To(
			Equal(privatev1.VolumeState_VOLUME_STATE_FAILED),
		)
		Expect(volume.GetStatus().GetMessage()).To(ContainSubstring("spec.storageTier"))
	})

	It("persists hub assignment and returns early on first reconciliation", func() {
		ctx := context.Background()
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), "hub-a").
			Return(&controllers.HubEntry{Namespace: "hub-ns", Client: fake.NewClientBuilder().Build()}, nil)

		hubsClient := NewMockHubsClient(ctrl)
		hubsClient.EXPECT().
			List(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(privatev1.HubsListResponse_builder{
				Items: []*privatev1.Hub{
					privatev1.Hub_builder{Id: "hub-a"}.Build(),
				},
			}.Build(), nil)

		volumesClient := NewMockVolumesClient(ctrl)
		volumesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.VolumesUpdateRequest, opts ...grpc.CallOption) (*privatev1.VolumesUpdateResponse, error) {
				Expect(req.GetObject().GetStatus().GetHub()).To(Equal("hub-a"))
				return &privatev1.VolumesUpdateResponse{Object: req.GetObject()}, nil
			}).
			Times(1)

		volume := privatev1.Volume_builder{
			Id: "vol-hub-select",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     "test-tenant",
			}.Build(),
			Spec: privatev1.VolumeSpec_builder{
				StorageTier: "gold",
				SizeGib:     100,
				AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
			}.Build(),
			Status: privatev1.VolumeStatus_builder{
				State: privatev1.VolumeState_VOLUME_STATE_CREATING,
			}.Build(),
		}.Build()

		f := &function{
			logger:         logger,
			hubCache:       hubCache,
			hubsClient:     hubsClient,
			volumesClient:  volumesClient,
			maskCalculator: masks.NewCalculator().Build(),
		}

		err := f.run(ctx, volume)
		Expect(err).ToNot(HaveOccurred())
		Expect(volume.GetStatus().GetHub()).To(Equal("hub-a"))
	})

	It("marks FAILED without requeue when tenant validation fails", func() {
		ctx := context.Background()
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		volumesClient := NewMockVolumesClient(ctrl)
		volumesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.VolumesUpdateRequest, opts ...grpc.CallOption) (*privatev1.VolumesUpdateResponse, error) {
				Expect(req.GetObject().GetStatus().GetState()).To(
					Equal(privatev1.VolumeState_VOLUME_STATE_FAILED))
				return &privatev1.VolumesUpdateResponse{Object: req.GetObject()}, nil
			}).
			Times(1)

		volume := privatev1.Volume_builder{
			Id: "vol-no-tenant",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Spec: privatev1.VolumeSpec_builder{
				StorageTier: "gold",
				SizeGib:     100,
				AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
			}.Build(),
			Status: privatev1.VolumeStatus_builder{
				State: privatev1.VolumeState_VOLUME_STATE_CREATING,
			}.Build(),
		}.Build()

		f := &function{
			logger:         logger,
			volumesClient:  volumesClient,
			maskCalculator: masks.NewCalculator().Build(),
		}

		err := f.run(ctx, volume)
		Expect(err).ToNot(HaveOccurred())
		Expect(volume.GetStatus().GetState()).To(
			Equal(privatev1.VolumeState_VOLUME_STATE_FAILED))
		Expect(volume.GetStatus().GetMessage()).To(ContainSubstring("tenant"))
	})

	It("requeues without marking FAILED on transient K8s Create error", func() {
		ctx := context.Background()
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.CreateOption) error {
					return errors.New("connection refused")
				},
			}).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), "hub-1").
			Return(&controllers.HubEntry{Namespace: "test-ns", Client: fakeClient}, nil).
			AnyTimes()

		volumesClient := NewMockVolumesClient(ctrl)
		volumesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			Times(0)

		volume := privatev1.Volume_builder{
			Id: "vol-transient-k8s",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     "test-tenant",
			}.Build(),
			Spec: privatev1.VolumeSpec_builder{
				StorageTier: "gold",
				SizeGib:     100,
				AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
			}.Build(),
			Status: privatev1.VolumeStatus_builder{
				State: privatev1.VolumeState_VOLUME_STATE_CREATING,
				Hub:   "hub-1",
			}.Build(),
		}.Build()

		f := &function{
			logger:         logger,
			hubCache:       hubCache,
			volumesClient:  volumesClient,
			maskCalculator: masks.NewCalculator().Build(),
		}

		err := f.run(ctx, volume)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("connection refused"))
		Expect(volume.GetStatus().GetState()).To(
			Equal(privatev1.VolumeState_VOLUME_STATE_CREATING))
	})

	It("requeues without marking FAILED on transient delete error", func() {
		ctx := context.Background()
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		cr := newVolumeCR("vol-delete-transient", "test-ns", "vol-test", nil)

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cr).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.DeleteOption) error {
					return errors.New("connection refused")
				},
			}).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), "hub-1").
			Return(&controllers.HubEntry{Namespace: "test-ns", Client: fakeClient}, nil).
			AnyTimes()

		volumesClient := NewMockVolumesClient(ctrl)
		volumesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			Times(0)

		volume := privatev1.Volume_builder{
			Id: "vol-delete-transient",
			Metadata: privatev1.Metadata_builder{
				Finalizers:        []string{finalizers.Controller},
				Tenant:            "test-tenant",
				DeletionTimestamp: timestamppb.Now(),
			}.Build(),
			Spec: privatev1.VolumeSpec_builder{
				StorageTier: "gold",
				SizeGib:     100,
				AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
			}.Build(),
			Status: privatev1.VolumeStatus_builder{
				State: privatev1.VolumeState_VOLUME_STATE_CREATING,
				Hub:   "hub-1",
			}.Build(),
		}.Build()

		f := &function{
			logger:         logger,
			hubCache:       hubCache,
			volumesClient:  volumesClient,
			maskCalculator: masks.NewCalculator().Build(),
		}

		err := f.run(ctx, volume)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("connection refused"))
		Expect(volume.GetStatus().GetState()).To(
			Equal(privatev1.VolumeState_VOLUME_STATE_CREATING))
	})
})

var _ = Describe("create status population", func() {
	It("populates status.provider and protocol from the resolved private volume", func() {
		ctx := context.Background()
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		var capturedStatus *osacv1alpha1.Volume
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&osacv1alpha1.Volume{}).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourceUpdate: func(ctx context.Context, client clnt.Client, subResourceName string, obj clnt.Object, opts ...clnt.SubResourceUpdateOption) error {
					if v, ok := obj.(*osacv1alpha1.Volume); ok {
						capturedStatus = v.DeepCopy()
					}
					return nil
				},
			}).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), "hub-1").
			Return(&controllers.HubEntry{Namespace: "test-ns", Client: fakeClient}, nil).
			AnyTimes()

		volumesClient := NewMockVolumesClient(ctrl)
		volumesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.VolumesUpdateRequest, opts ...grpc.CallOption) (*privatev1.VolumesUpdateResponse, error) {
				return &privatev1.VolumesUpdateResponse{Object: req.GetObject()}, nil
			}).
			AnyTimes()

		volume := privatev1.Volume_builder{
			Id: "vol-status-test",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     "test-tenant",
			}.Build(),
			Spec: privatev1.VolumeSpec_builder{
				StorageTier: "gold",
				SizeGib:     100,
				AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
			}.Build(),
			Status: privatev1.VolumeStatus_builder{
				State:    privatev1.VolumeState_VOLUME_STATE_CREATING,
				Hub:      "hub-1",
				Provider: "vast",
				Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
			}.Build(),
		}.Build()

		f := &function{
			logger:         logger,
			hubCache:       hubCache,
			volumesClient:  volumesClient,
			maskCalculator: masks.NewCalculator().Build(),
		}

		Expect(f.run(ctx, volume)).To(Succeed())
		Expect(capturedStatus).ToNot(BeNil())
		Expect(capturedStatus.Status.Provider).To(Equal("vast"))
		Expect(capturedStatus.Status.Protocol).To(Equal(osacv1alpha1.VolumeProtocolBlock))
	})

	It("retries status stamp on conflict and succeeds", func() {
		ctx := context.Background()
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		conflictCount := 0
		var capturedStatus *osacv1alpha1.Volume
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&osacv1alpha1.Volume{}).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourceUpdate: func(ctx context.Context, client clnt.Client, subResourceName string, obj clnt.Object, opts ...clnt.SubResourceUpdateOption) error {
					conflictCount++
					if conflictCount <= 2 {
						return apierrors.NewConflict(
							schema.GroupResource{Group: "osac.openshift.io", Resource: "volumes"},
							"vol-test",
							errors.New("the object has been modified"),
						)
					}
					if v, ok := obj.(*osacv1alpha1.Volume); ok {
						capturedStatus = v.DeepCopy()
					}
					return nil
				},
			}).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), "hub-1").
			Return(&controllers.HubEntry{Namespace: "test-ns", Client: fakeClient}, nil).
			AnyTimes()

		volumesClient := NewMockVolumesClient(ctrl)
		volumesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.VolumesUpdateRequest, opts ...grpc.CallOption) (*privatev1.VolumesUpdateResponse, error) {
				return &privatev1.VolumesUpdateResponse{Object: req.GetObject()}, nil
			}).
			AnyTimes()

		volume := privatev1.Volume_builder{
			Id: "vol-conflict-retry",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     "test-tenant",
			}.Build(),
			Spec: privatev1.VolumeSpec_builder{
				StorageTier: "gold",
				SizeGib:     100,
				AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
			}.Build(),
			Status: privatev1.VolumeStatus_builder{
				State:    privatev1.VolumeState_VOLUME_STATE_CREATING,
				Hub:      "hub-1",
				Provider: "vast",
				Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
			}.Build(),
		}.Build()

		f := &function{
			logger:         logger,
			hubCache:       hubCache,
			volumesClient:  volumesClient,
			maskCalculator: masks.NewCalculator().Build(),
		}

		Expect(f.run(ctx, volume)).To(Succeed())
		Expect(conflictCount).To(Equal(3))
		Expect(capturedStatus).ToNot(BeNil())
		Expect(capturedStatus.Status.Provider).To(Equal("vast"))
		Expect(capturedStatus.Status.Protocol).To(Equal(osacv1alpha1.VolumeProtocolBlock))
	})

	It("re-stamps status on patch-spec branch when CR exists without provider/protocol", func() {
		ctx := context.Background()
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		existingCR := &osacv1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test-ns",
				Name:      "vol-existing",
				Labels: map[string]string{
					labels.VolumeUuid: "vol-restamp",
				},
			},
			Spec: osacv1alpha1.VolumeSpec{
				StorageTier: "gold",
				SizeGiB:     100,
				AccessMode:  osacv1alpha1.VolumeAccessModeReadWriteOnce,
			},
		}

		var capturedStatus *osacv1alpha1.Volume
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(existingCR).
			WithStatusSubresource(&osacv1alpha1.Volume{}).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourceUpdate: func(ctx context.Context, client clnt.Client, subResourceName string, obj clnt.Object, opts ...clnt.SubResourceUpdateOption) error {
					if v, ok := obj.(*osacv1alpha1.Volume); ok {
						capturedStatus = v.DeepCopy()
					}
					return nil
				},
			}).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), "hub-1").
			Return(&controllers.HubEntry{Namespace: "test-ns", Client: fakeClient}, nil).
			AnyTimes()

		volumesClient := NewMockVolumesClient(ctrl)
		volumesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.VolumesUpdateRequest, opts ...grpc.CallOption) (*privatev1.VolumesUpdateResponse, error) {
				return &privatev1.VolumesUpdateResponse{Object: req.GetObject()}, nil
			}).
			AnyTimes()

		volume := privatev1.Volume_builder{
			Id: "vol-restamp",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     "test-tenant",
			}.Build(),
			Spec: privatev1.VolumeSpec_builder{
				StorageTier: "gold",
				SizeGib:     100,
				AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
			}.Build(),
			Status: privatev1.VolumeStatus_builder{
				State:    privatev1.VolumeState_VOLUME_STATE_CREATING,
				Hub:      "hub-1",
				Provider: "vast",
				Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
			}.Build(),
		}.Build()

		f := &function{
			logger:         logger,
			hubCache:       hubCache,
			volumesClient:  volumesClient,
			maskCalculator: masks.NewCalculator().Build(),
		}

		Expect(f.run(ctx, volume)).To(Succeed())
		Expect(capturedStatus).ToNot(BeNil())
		Expect(capturedStatus.Status.Provider).To(Equal("vast"))
		Expect(capturedStatus.Status.Protocol).To(Equal(osacv1alpha1.VolumeProtocolBlock))
	})

	It("skips status stamp when provider and protocol already match", func() {
		ctx := context.Background()
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		existingCR := &osacv1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test-ns",
				Name:      "vol-already-stamped",
				Labels: map[string]string{
					labels.VolumeUuid: "vol-noop-stamp",
				},
			},
			Spec: osacv1alpha1.VolumeSpec{
				StorageTier: "gold",
				SizeGiB:     100,
				AccessMode:  osacv1alpha1.VolumeAccessModeReadWriteOnce,
			},
			Status: osacv1alpha1.VolumeStatus{
				Provider: "vast",
				Protocol: osacv1alpha1.VolumeProtocolBlock,
			},
		}

		statusUpdateCalled := false
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(existingCR).
			WithStatusSubresource(&osacv1alpha1.Volume{}).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourceUpdate: func(ctx context.Context, client clnt.Client, subResourceName string, obj clnt.Object, opts ...clnt.SubResourceUpdateOption) error {
					statusUpdateCalled = true
					return nil
				},
			}).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), "hub-1").
			Return(&controllers.HubEntry{Namespace: "test-ns", Client: fakeClient}, nil).
			AnyTimes()

		volumesClient := NewMockVolumesClient(ctrl)
		volumesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.VolumesUpdateRequest, opts ...grpc.CallOption) (*privatev1.VolumesUpdateResponse, error) {
				return &privatev1.VolumesUpdateResponse{Object: req.GetObject()}, nil
			}).
			AnyTimes()

		volume := privatev1.Volume_builder{
			Id: "vol-noop-stamp",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     "test-tenant",
			}.Build(),
			Spec: privatev1.VolumeSpec_builder{
				StorageTier: "gold",
				SizeGib:     100,
				AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
			}.Build(),
			Status: privatev1.VolumeStatus_builder{
				State:    privatev1.VolumeState_VOLUME_STATE_CREATING,
				Hub:      "hub-1",
				Provider: "vast",
				Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
			}.Build(),
		}.Build()

		f := &function{
			logger:         logger,
			hubCache:       hubCache,
			volumesClient:  volumesClient,
			maskCalculator: masks.NewCalculator().Build(),
		}

		Expect(f.run(ctx, volume)).To(Succeed())
		Expect(statusUpdateCalled).To(BeFalse())
	})
})

// The reconciler must write the finalizer list and version it read, so a concurrent change gets Aborted instead of
// being lost.
var _ = Describe("finalizer removal on delete", func() {
	const (
		foreignFinalizer = "test.osac.io/hold"
		readVersion      = int32(7)
	)

	var volumesClient *MockVolumesClient

	newDeletingVolume := func(list ...string) *privatev1.Volume {
		return privatev1.Volume_builder{
			Id: "vol-finalizer-guard",
			Metadata: privatev1.Metadata_builder{
				DeletionTimestamp: timestamppb.Now(),
				Finalizers:        list,
				Tenant:            "test-tenant",
				Version:           readVersion,
			}.Build(),
		}.Build()
	}

	newFunction := func() *function {
		return &function{
			logger:         logger,
			volumesClient:  volumesClient,
			maskCalculator: masks.NewCalculator().Build(),
		}
	}

	BeforeEach(func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		volumesClient = NewMockVolumesClient(ctrl)
	})

	It("preserves foreign finalizers and sends the version it read", func() {
		var requests []*privatev1.VolumesUpdateRequest
		volumesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.VolumesUpdateRequest, opts ...grpc.CallOption) (*privatev1.VolumesUpdateResponse, error) {
				requests = append(requests, proto.Clone(req).(*privatev1.VolumesUpdateRequest))
				return &privatev1.VolumesUpdateResponse{Object: req.GetObject()}, nil
			}).
			Times(1)

		volume := newDeletingVolume(finalizers.Controller, foreignFinalizer)
		Expect(newFunction().run(context.Background(), volume)).To(Succeed())

		Expect(requests).To(HaveLen(1))
		Expect(requests[0].GetObject().GetMetadata().GetFinalizers()).To(Equal([]string{foreignFinalizer}))
		Expect(requests[0].GetUpdateMask().GetPaths()).To(Equal([]string{"metadata.finalizers"}))
		Expect(requests[0].GetObject().GetMetadata().GetVersion()).To(Equal(readVersion))
	})

	It("returns a conflict error when the server rejects the write with Aborted", func() {
		volumesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, status.Error(codes.Aborted, "object has been modified")).
			Times(1)

		volume := newDeletingVolume(finalizers.Controller, foreignFinalizer)
		err := newFunction().run(context.Background(), volume)

		Expect(err).To(HaveOccurred())
		Expect(controllers.IsConflict(err)).To(BeTrue())
	})

	It("doesn't write when the controller finalizer is already absent", func() {
		volumesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			Times(0)

		volume := newDeletingVolume(foreignFinalizer)
		Expect(newFunction().run(context.Background(), volume)).To(Succeed())
	})
})
