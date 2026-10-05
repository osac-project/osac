/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package externalip

import (
	"context"
	"errors"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
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
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type fakeNetworkingHubReader struct {
	result controllers.NetworkingHubResolution
	err    error
	calls  int
}

func (f *fakeNetworkingHubReader) Resolve(context.Context) (controllers.NetworkingHubResolution, error) {
	f.calls++
	return f.result, f.err
}

func readyNetworkingHubReader(id, namespace string, client clnt.Client) *fakeNetworkingHubReader {
	return &fakeNetworkingHubReader{result: controllers.NetworkingHubResolution{
		NetworkingHub: controllers.NetworkingHub{ID: id, Namespace: namespace, Client: client},
		HubID:         id,
		State:         privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
	}}
}

var _ = Describe("buildSpec", func() {
	It("Includes pool in spec", func() {
		t := &task{
			externalIP: privatev1.ExternalIP_builder{
				Id: "eip-test-1",
				Spec: privatev1.ExternalIPSpec_builder{
					Pool: privatev1.ExternalIPPoolReference_builder{Id: "pool-abc123"}.Build(),
				}.Build(),
			}.Build(),
		}

		spec := t.buildSpec()

		Expect(spec.Pool).To(Equal("pool-abc123"))
	})

	It("Does not include status fields", func() {
		t := &task{
			externalIP: privatev1.ExternalIP_builder{
				Id: "eip-test-3",
				Spec: privatev1.ExternalIPSpec_builder{
					Pool: privatev1.ExternalIPPoolReference_builder{Id: "pool-abc789"}.Build(),
				}.Build(),
				Status: privatev1.ExternalIPStatus_builder{
					State:   privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
					Hub:     "hub-1",
					Address: "10.0.0.5",
				}.Build(),
			}.Build(),
		}

		spec := t.buildSpec()

		Expect(spec.Pool).To(Equal("pool-abc789"))
	})
})

// newExternalIPCR creates a typed ExternalIP CR for use with the fake client.
func newExternalIPCR(id, namespace, name string, deletionTimestamp *metav1.Time) *osacv1alpha1.ExternalIP {
	obj := &osacv1alpha1.ExternalIP{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				labels.ExternalIPUuid: id,
			},
		},
	}
	if deletionTimestamp != nil {
		obj.SetDeletionTimestamp(deletionTimestamp)
		obj.SetFinalizers([]string{"osac.openshift.io/externalip"})
	}
	return obj
}

// hasFinalizer checks if the fulfillment-controller finalizer is present on the external IP.
func hasFinalizer(externalIP *privatev1.ExternalIP) bool {
	return slices.Contains(externalIP.GetMetadata().GetFinalizers(), finalizers.Controller)
}

// newTaskForDelete creates a task configured for testing delete() with hub-dependent paths.
func newTaskForDelete(externalIPID, hubID string, hubCache controllers.HubCache) *task {
	externalIP := privatev1.ExternalIP_builder{
		Id: externalIPID,
		Metadata: privatev1.Metadata_builder{
			Finalizers: []string{finalizers.Controller},
		}.Build(),
		Status: privatev1.ExternalIPStatus_builder{
			Hub: hubID,
		}.Build(),
	}.Build()

	f := &function{
		logger:   logger,
		hubCache: hubCache,
	}

	return &task{
		r:          f,
		externalIP: externalIP,
	}
}

var _ = Describe("delete", func() {
	const (
		externalIPID = "eip-delete-id"
		hubID        = "test-hub"
		hubNamespace = "test-ns"
		crName       = "externalip-test"
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

	It("should remove finalizer when K8s object doesn't exist", func() {
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

		t := newTaskForDelete(externalIPID, hubID, hubCache)
		Expect(hasFinalizer(t.externalIP)).To(BeTrue())

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasFinalizer(t.externalIP)).To(BeFalse())
	})

	It("should call hubClient.Delete when K8s object exists without DeletionTimestamp", func() {
		cr := newExternalIPCR(externalIPID, hubNamespace, crName, nil)

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

		t := newTaskForDelete(externalIPID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteCalled).To(BeTrue())
		// Finalizer should NOT be removed: K8s object still exists
		Expect(hasFinalizer(t.externalIP)).To(BeTrue())
	})

	It("should not call hubClient.Delete when K8s object has DeletionTimestamp", func() {
		now := metav1.Now()
		cr := newExternalIPCR(externalIPID, hubNamespace, crName, &now)

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

		t := newTaskForDelete(externalIPID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteCalled).To(BeFalse())
		// Finalizer should NOT be removed: K8s object still being deleted
		Expect(hasFinalizer(t.externalIP)).To(BeTrue())
	})

	It("should propagate error when hub cache returns error", func() {
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(nil, errors.New("hub not found"))

		t := newTaskForDelete(externalIPID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("hub not found"))
		// Finalizer should NOT be removed on error
		Expect(hasFinalizer(t.externalIP)).To(BeTrue())
	})

	It("should remove finalizer when hub cache returns ErrHubNotFound", func() {
		// This test verifies the core behavior: when a hub is decommissioned/deleted,
		// the reconciler removes its finalizer to allow the external IP to be archived.
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(nil, controllers.ErrHubNotFound)

		t := newTaskForDelete(externalIPID, hubID, hubCache)
		Expect(hasFinalizer(t.externalIP)).To(BeTrue())

		err := t.delete(ctx)
		// Should return nil (not propagate the error)
		Expect(err).ToNot(HaveOccurred())
		// Finalizer should be removed to allow archiving
		Expect(hasFinalizer(t.externalIP)).To(BeFalse())
	})

	It("should remove finalizer when no hub is assigned", func() {
		externalIP := privatev1.ExternalIP_builder{
			Id: externalIPID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Status: privatev1.ExternalIPStatus_builder{
				// No hub assigned
			}.Build(),
		}.Build()

		f := &function{
			logger: logger,
		}

		t := &task{
			r:          f,
			externalIP: externalIP,
		}

		Expect(hasFinalizer(t.externalIP)).To(BeTrue())

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasFinalizer(t.externalIP)).To(BeFalse())
	})
})

var _ = Describe("validateTenant", func() {
	It("should succeed when a tenant is assigned", func() {
		externalIP := privatev1.ExternalIP_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "tenant-1",
			}.Build(),
		}.Build()

		t := &task{
			externalIP: externalIP,
		}

		err := t.validateTenant()
		Expect(err).ToNot(HaveOccurred())
	})

	It("should fail when tenant is empty", func() {
		externalIP := privatev1.ExternalIP_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "",
			}.Build(),
		}.Build()

		t := &task{
			externalIP: externalIP,
		}

		err := t.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant"))
	})

	It("should fail when metadata is missing", func() {
		externalIP := privatev1.ExternalIP_builder{}.Build()

		t := &task{
			externalIP: externalIP,
		}

		err := t.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant"))
	})
})

var _ = Describe("setDefaults", func() {
	It("should set PENDING state when status is unspecified", func() {
		externalIP := privatev1.ExternalIP_builder{
			Id: "eip-defaults",
		}.Build()

		t := &task{
			externalIP: externalIP,
		}

		t.setDefaults()

		Expect(t.externalIP.GetStatus().GetState()).To(Equal(privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING))
	})

	It("should not overwrite existing state", func() {
		externalIP := privatev1.ExternalIP_builder{
			Id: "eip-existing-state",
			Status: privatev1.ExternalIPStatus_builder{
				State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
			}.Build(),
		}.Build()

		t := &task{
			externalIP: externalIP,
		}

		t.setDefaults()

		Expect(t.externalIP.GetStatus().GetState()).To(Equal(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED))
	})

	It("should create status if it doesn't exist", func() {
		externalIP := privatev1.ExternalIP_builder{
			Id: "eip-no-status",
		}.Build()

		t := &task{
			externalIP: externalIP,
		}

		Expect(t.externalIP.HasStatus()).To(BeFalse())

		t.setDefaults()

		Expect(t.externalIP.HasStatus()).To(BeTrue())
		Expect(t.externalIP.GetStatus().GetState()).To(Equal(privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING))
	})
})

var _ = Describe("fallback state timestamps", func() {
	It("timestamps the initial pending default and preserves same-state time", func() {
		t := &task{externalIP: privatev1.ExternalIP_builder{Id: "eip-state-time"}.Build()}
		before := time.Now().UTC()

		t.setDefaults()
		pendingAt := t.externalIP.GetStatus().GetStateTransitionTime().AsTime()
		Expect(t.externalIP.GetStatus().GetState()).To(Equal(privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING))
		Expect(pendingAt).To(BeTemporally(">=", before))

		t.setDefaults()
		Expect(t.externalIP.GetStatus().GetStateTransitionTime().AsTime()).To(Equal(pendingAt))
	})

	It("timestamps fallback pending and failure transitions without resetting repeated states", func() {
		oldTime := timestamppb.New(time.Date(2026, time.January, 1, 9, 0, 0, 0, time.UTC))
		t := &task{externalIP: privatev1.ExternalIP_builder{
			Id: "eip-fallback-failure-time",
			Status: privatev1.ExternalIPStatus_builder{
				State:               privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
				StateTransitionTime: oldTime,
			}.Build(),
		}.Build()}

		beforePending := time.Now().UTC()
		t.setPending(errors.New("temporary reconciliation error"))
		pendingAt := t.externalIP.GetStatus().GetStateTransitionTime().AsTime()
		Expect(t.externalIP.GetStatus().GetState()).To(Equal(privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING))
		Expect(pendingAt).To(BeTemporally(">=", beforePending))
		t.setPending(errors.New("still pending"))
		Expect(t.externalIP.GetStatus().GetStateTransitionTime().AsTime()).To(Equal(pendingAt))

		beforeFailure := time.Now().UTC()
		t.setFailed(errors.New("permanent reconciliation error"))
		failedAt := t.externalIP.GetStatus().GetStateTransitionTime().AsTime()
		Expect(t.externalIP.GetStatus().GetState()).To(Equal(privatev1.ExternalIPState_EXTERNAL_IP_STATE_FAILED))
		Expect(failedAt).To(BeTemporally(">=", beforeFailure))
		t.setFailed(errors.New("still failed"))
		Expect(t.externalIP.GetStatus().GetStateTransitionTime().AsTime()).To(Equal(failedAt))
	})
})

var _ = Describe("addFinalizer", func() {
	It("should add finalizer when not present", func() {
		externalIP := privatev1.ExternalIP_builder{
			Id: "eip-no-finalizer",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{},
			}.Build(),
		}.Build()

		t := &task{
			externalIP: externalIP,
		}

		added := t.addFinalizer()

		Expect(added).To(BeTrue())
		Expect(hasFinalizer(t.externalIP)).To(BeTrue())
	})

	It("should not add finalizer when already present", func() {
		externalIP := privatev1.ExternalIP_builder{
			Id: "eip-has-finalizer",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
		}.Build()

		t := &task{
			externalIP: externalIP,
		}

		added := t.addFinalizer()

		Expect(added).To(BeFalse())
		Expect(hasFinalizer(t.externalIP)).To(BeTrue())
		// Should not duplicate
		Expect(t.externalIP.GetMetadata().GetFinalizers()).To(HaveLen(1))
	})

	It("should create metadata if it doesn't exist", func() {
		externalIP := privatev1.ExternalIP_builder{
			Id: "eip-no-metadata",
		}.Build()

		t := &task{
			externalIP: externalIP,
		}

		Expect(t.externalIP.HasMetadata()).To(BeFalse())

		added := t.addFinalizer()

		Expect(added).To(BeTrue())
		Expect(t.externalIP.HasMetadata()).To(BeTrue())
		Expect(hasFinalizer(t.externalIP)).To(BeTrue())
	})
})

var _ = Describe("removeFinalizer", func() {
	It("should remove finalizer when present", func() {
		externalIP := privatev1.ExternalIP_builder{
			Id: "eip-has-finalizer",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller, "other-finalizer"},
			}.Build(),
		}.Build()

		t := &task{
			externalIP: externalIP,
		}

		Expect(hasFinalizer(t.externalIP)).To(BeTrue())

		t.removeFinalizer()

		Expect(hasFinalizer(t.externalIP)).To(BeFalse())
		// Other finalizers should remain
		Expect(t.externalIP.GetMetadata().GetFinalizers()).To(ContainElement("other-finalizer"))
	})

	It("should do nothing when finalizer not present", func() {
		externalIP := privatev1.ExternalIP_builder{
			Id: "eip-no-finalizer",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{"other-finalizer"},
			}.Build(),
		}.Build()

		t := &task{
			externalIP: externalIP,
		}

		Expect(hasFinalizer(t.externalIP)).To(BeFalse())

		t.removeFinalizer()

		Expect(hasFinalizer(t.externalIP)).To(BeFalse())
		Expect(t.externalIP.GetMetadata().GetFinalizers()).To(ContainElement("other-finalizer"))
	})

	It("should do nothing when metadata doesn't exist", func() {
		externalIP := privatev1.ExternalIP_builder{
			Id: "eip-no-metadata",
		}.Build()

		t := &task{
			externalIP: externalIP,
		}

		// Should not panic
		t.removeFinalizer()

		Expect(t.externalIP.HasMetadata()).To(BeFalse())
	})
})

var _ = Describe("selectHub", func() {
	It("uses the canonical Hub for empty and sticky assignments and never falls back", func() {
		ctx := context.Background()
		kubeClient := fake.NewClientBuilder().Build()
		resolver := readyNetworkingHubReader("hub-a", "hub-a-ns", kubeClient)
		f := &function{logger: logger, networkingHubReader: resolver}
		newExternalIP := func(hubID string) *privatev1.ExternalIP {
			return privatev1.ExternalIP_builder{
				Id: "eip-canonical-hub",
				Spec: privatev1.ExternalIPSpec_builder{
					Pool: privatev1.ExternalIPPoolReference_builder{Id: "pool-1"}.Build(),
				}.Build(),
				Status: privatev1.ExternalIPStatus_builder{Hub: hubID}.Build(),
			}.Build()
		}

		for _, assignedHubID := range []string{"", "hub-a"} {
			t := &task{r: f, externalIP: newExternalIP(assignedHubID)}
			Expect(t.selectHub(ctx)).To(Succeed())
			Expect(t.hubId).To(Equal("hub-a"))
			Expect(t.hubNamespace).To(Equal("hub-a-ns"))
			Expect(t.hubClient).To(BeIdenticalTo(kubeClient))
		}
		Expect(resolver.calls).To(Equal(2), "existing assignments must be checked against the canonical Hub")

		conflict := &task{r: f, externalIP: newExternalIP("hub-b")}
		Expect(conflict.selectHub(ctx)).To(MatchError(ContainSubstring(controllers.ErrResourceHubConflict.Error())))
		Expect(conflict.hubClient).To(BeNil())

		for _, resolutionErr := range []error{
			controllers.ErrNoNetworkingHubs,
			controllers.ErrMultipleNetworkingHubs,
			controllers.ErrCanonicalHubUnavailable,
		} {
			resolver.err = resolutionErr
			t := &task{r: f, externalIP: newExternalIP("hub-a")}
			Expect(t.selectHub(ctx)).To(MatchError(resolutionErr))
			Expect(t.hubClient).To(BeNil())
		}
	})
})

var _ = Describe("hub persistence", func() {
	const (
		externalIPID = "test-externalip-hub"
		poolID       = "pool-123"
		tenantName   = "test-tenant"
		hubID        = "test-hub-123"
		hubNamespace = "hub-123-ns"
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

	It("should select hub and return without creating ExternalIP CR", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		resolver := readyNetworkingHubReader(hubID, hubNamespace, fakeClient)

		externalIPsClient := NewMockExternalIPsClient(ctrl)
		externalIPsClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.ExternalIPsUpdateRequest, opts ...grpc.CallOption) (*privatev1.ExternalIPsUpdateResponse, error) {
				return &privatev1.ExternalIPsUpdateResponse{Object: req.GetObject()}, nil
			}).AnyTimes()

		externalIP := privatev1.ExternalIP_builder{
			Id: externalIPID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ExternalIPSpec_builder{
				Pool: privatev1.ExternalIPPoolReference_builder{Id: poolID}.Build(),
			}.Build(),
			Status: privatev1.ExternalIPStatus_builder{
				State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING,
				Hub:   "",
			}.Build(),
		}.Build()

		f := &function{
			logger:              logger,
			externalIPsClient:   externalIPsClient,
			networkingHubReader: resolver,
			maskCalculator:      nil,
		}

		err := f.run(ctx, externalIP)
		Expect(err).ToNot(HaveOccurred())
		Expect(externalIP.GetStatus().GetHub()).To(Equal(hubID))

		list := &osacv1alpha1.ExternalIPList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(BeEmpty())
	})

	It("should not create CR when no hubs available", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		resolver := &fakeNetworkingHubReader{err: controllers.ErrNoNetworkingHubs}
		externalIPsClient := NewMockExternalIPsClient(ctrl)
		externalIPsClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.ExternalIPsUpdateRequest, opts ...grpc.CallOption) (*privatev1.ExternalIPsUpdateResponse, error) {
				return &privatev1.ExternalIPsUpdateResponse{Object: req.GetObject()}, nil
			})

		externalIP := privatev1.ExternalIP_builder{
			Id: externalIPID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ExternalIPSpec_builder{
				Pool: privatev1.ExternalIPPoolReference_builder{Id: poolID}.Build(),
			}.Build(),
			Status: privatev1.ExternalIPStatus_builder{
				State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING,
				Hub:   "",
			}.Build(),
		}.Build()

		f := &function{
			logger:              logger,
			externalIPsClient:   externalIPsClient,
			networkingHubReader: resolver,
			maskCalculator:      nil,
		}

		err := f.run(ctx, externalIP)
		Expect(errors.Is(err, controllers.ErrNoNetworkingHubs)).To(BeTrue())
		Expect(externalIP.GetStatus().GetState()).To(Equal(privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING))
		Expect(externalIP.GetStatus().GetHub()).To(BeEmpty())
		Expect(externalIP.GetStatus().GetMessage()).To(ContainSubstring(controllers.ErrNoNetworkingHubs.Error()))

		list := &osacv1alpha1.ExternalIPList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(BeEmpty())
	})

	It("should skip hub selection if already set", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		resolver := readyNetworkingHubReader(hubID, hubNamespace, fakeClient)

		externalIPsClient := NewMockExternalIPsClient(ctrl)
		externalIPsClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.ExternalIPsUpdateRequest, opts ...grpc.CallOption) (*privatev1.ExternalIPsUpdateResponse, error) {
				Expect(req.GetUpdateMask().GetPaths()).ToNot(ContainElement("status.hub"))
				return &privatev1.ExternalIPsUpdateResponse{Object: req.GetObject()}, nil
			}).AnyTimes()

		externalIP := privatev1.ExternalIP_builder{
			Id: externalIPID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ExternalIPSpec_builder{
				Pool: privatev1.ExternalIPPoolReference_builder{Id: poolID}.Build(),
			}.Build(),
			Status: privatev1.ExternalIPStatus_builder{
				State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING,
				Hub:   hubID,
			}.Build(),
		}.Build()

		f := &function{
			logger:              logger,
			externalIPsClient:   externalIPsClient,
			networkingHubReader: resolver,
			maskCalculator:      nil,
		}

		err := f.run(ctx, externalIP)
		Expect(err).ToNot(HaveOccurred())
		Expect(resolver.calls).To(Equal(1), "existing assignments must be checked against the canonical Hub")

		list := &osacv1alpha1.ExternalIPList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))
		Expect(list.Items[0].Namespace).To(Equal(hubNamespace))
	})

	It("should create CR on second reconcile after hub is persisted", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		resolver := readyNetworkingHubReader(hubID, hubNamespace, fakeClient)

		externalIPsClient := NewMockExternalIPsClient(ctrl)

		// First reconcile: persist hub succeeds
		externalIPsClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.ExternalIPsUpdateRequest, opts ...grpc.CallOption) (*privatev1.ExternalIPsUpdateResponse, error) {
				Expect(req.GetUpdateMask().GetPaths()).To(Equal([]string{"status.hub"}))
				return &privatev1.ExternalIPsUpdateResponse{Object: req.GetObject()}, nil
			})

		externalIP := privatev1.ExternalIP_builder{
			Id: externalIPID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ExternalIPSpec_builder{
				Pool: privatev1.ExternalIPPoolReference_builder{Id: poolID}.Build(),
			}.Build(),
			Status: privatev1.ExternalIPStatus_builder{
				State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING,
				Hub:   "",
			}.Build(),
		}.Build()

		f := &function{
			logger:              logger,
			externalIPsClient:   externalIPsClient,
			networkingHubReader: resolver,
			maskCalculator:      nil,
		}

		// First reconcile: hub="" -> selects hub, returns early, no CR
		err := f.run(ctx, externalIP)
		Expect(err).ToNot(HaveOccurred())

		list := &osacv1alpha1.ExternalIPList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(BeEmpty())

		// Second reconcile: hub already set -> CR created
		externalIP.GetStatus().SetHub(hubID)
		externalIPsClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&privatev1.ExternalIPsUpdateResponse{Object: externalIP}, nil).
			AnyTimes()

		err = f.run(ctx, externalIP)
		Expect(err).ToNot(HaveOccurred())
		Expect(resolver.calls).To(Equal(2), "each reconciliation must revalidate the stored Hub assignment")

		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))
		Expect(list.Items[0].Namespace).To(Equal(hubNamespace))
	})
})

var _ = Describe("Kubernetes validation error handling", func() {
	It("should set state to FAILED when K8s Create returns Invalid error", func() {
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
						schema.GroupKind{Group: "osac.openshift.io", Kind: "ExternalIP"},
						"",
						field.ErrorList{
							field.Invalid(field.NewPath("spec", "pool"), "bad-pool", "pool reference is invalid"),
						},
					)
				},
			}).
			Build()

		resolver := readyNetworkingHubReader("hub-validation", "hub-ns", fakeClient)

		externalIPsClient := NewMockExternalIPsClient(ctrl)
		externalIPsClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.ExternalIPsUpdateRequest, opts ...grpc.CallOption) (*privatev1.ExternalIPsUpdateResponse, error) {
				return &privatev1.ExternalIPsUpdateResponse{Object: req.GetObject()}, nil
			}).
			MinTimes(1)

		externalIP := privatev1.ExternalIP_builder{
			Id: "eip-validation-test",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     "test-tenant",
			}.Build(),
			Spec: privatev1.ExternalIPSpec_builder{
				Pool: privatev1.ExternalIPPoolReference_builder{Id: "pool-test"}.Build(),
			}.Build(),
			Status: privatev1.ExternalIPStatus_builder{
				State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING,
				Hub:   "hub-validation",
			}.Build(),
		}.Build()

		f := &function{
			logger:              logger,
			externalIPsClient:   externalIPsClient,
			networkingHubReader: resolver,
			maskCalculator:      nil,
		}

		err := f.run(ctx, externalIP)
		Expect(err).ToNot(HaveOccurred())

		Expect(externalIP.GetStatus().GetState()).To(
			Equal(privatev1.ExternalIPState_EXTERNAL_IP_STATE_FAILED),
		)
		Expect(externalIP.GetStatus().GetMessage()).To(ContainSubstring("pool reference is invalid"))
	})
})
