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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
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
		Expect(err).ToNot(HaveOccurred())
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
		Expect(err).ToNot(HaveOccurred())
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

	It("persists FAILED state when tenant validation fails", func() {
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
		Expect(err).To(HaveOccurred())
		Expect(volume.GetStatus().GetState()).To(
			Equal(privatev1.VolumeState_VOLUME_STATE_FAILED))
		Expect(volume.GetStatus().GetMessage()).To(ContainSubstring("tenant"))
	})
})
