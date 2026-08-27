/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package natgateway

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

// fakeHubsClient implements the HubsClient interface for testing selectHub.
type fakeHubsClient struct {
	privatev1.HubsClient
	listResponse *privatev1.HubsListResponse
	listErr      error
}

func (f *fakeHubsClient) List(
	_ context.Context,
	_ *privatev1.HubsListRequest,
	_ ...grpc.CallOption,
) (*privatev1.HubsListResponse, error) {
	return f.listResponse, f.listErr
}

// newNATGatewayCR creates a typed NATGateway CR for use with the fake client.
func newNATGatewayCR(id, namespace, name string, deletionTimestamp *metav1.Time) *osacv1alpha1.NATGateway {
	obj := &osacv1alpha1.NATGateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				labels.NATGatewayUuid: id,
			},
		},
	}
	if deletionTimestamp != nil {
		obj.SetDeletionTimestamp(deletionTimestamp)
		obj.SetFinalizers([]string{"osac.openshift.io/natgateway"})
	}
	return obj
}

// hasFinalizer checks if the fulfillment-controller finalizer is present on the NAT gateway.
func hasFinalizer(natGateway *privatev1.NATGateway) bool {
	return slices.Contains(natGateway.GetMetadata().GetFinalizers(), finalizers.Controller)
}

// newTaskForDelete creates a task configured for testing delete() with hub-dependent paths.
func newTaskForDelete(gatewayID, hubID string, hubCache controllers.HubCache) *task {
	natGateway := privatev1.NATGateway_builder{
		Id: gatewayID,
		Metadata: privatev1.Metadata_builder{
			Finalizers: []string{finalizers.Controller},
		}.Build(),
		Status: privatev1.NATGatewayStatus_builder{
			Hub: hubID,
		}.Build(),
	}.Build()

	f := &function{
		logger:   logger,
		hubCache: hubCache,
	}

	return &task{
		r:          f,
		natGateway: natGateway,
	}
}

var _ = Describe("buildSpec", func() {
	It("Includes virtualNetwork and externalIP in spec", func() {
		t := &task{
			natGateway: privatev1.NATGateway_builder{
				Id: "natgw-uuid-test-1",
				Spec: privatev1.NATGatewaySpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vn-uuid-abc123"}.Build(),
					ExternalIp:     privatev1.ExternalIPLocalReference_builder{Id: "eip-uuid-abc123"}.Build(),
				}.Build(),
			}.Build(),
		}

		spec := t.buildSpec()

		Expect(spec.VirtualNetwork).To(Equal("vn-uuid-abc123"))
		Expect(spec.ExternalIP).To(Equal("eip-uuid-abc123"))
	})

	It("Does not include status fields", func() {
		t := &task{
			natGateway: privatev1.NATGateway_builder{
				Id: "natgw-uuid-test-2",
				Spec: privatev1.NATGatewaySpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vn-uuid-abc456"}.Build(),
					ExternalIp:     privatev1.ExternalIPLocalReference_builder{Id: "eip-uuid-abc456"}.Build(),
				}.Build(),
				Status: privatev1.NATGatewayStatus_builder{
					State: privatev1.NATGatewayState_NAT_GATEWAY_STATE_READY,
					Hub:   "hub-1",
				}.Build(),
			}.Build(),
		}

		spec := t.buildSpec()

		Expect(spec.VirtualNetwork).To(Equal("vn-uuid-abc456"))
		Expect(spec.ExternalIP).To(Equal("eip-uuid-abc456"))
	})
})

var _ = Describe("delete", func() {
	const (
		gatewayID    = "natgw-uuid-delete-id"
		hubID        = "test-hub"
		hubNamespace = "test-ns"
		crName       = "natgateway-test"
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

		t := newTaskForDelete(gatewayID, hubID, hubCache)
		Expect(hasFinalizer(t.natGateway)).To(BeTrue())

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasFinalizer(t.natGateway)).To(BeFalse())
	})

	It("should call hubClient.Delete when K8s object exists without DeletionTimestamp", func() {
		cr := newNATGatewayCR(gatewayID, hubNamespace, crName, nil)

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

		t := newTaskForDelete(gatewayID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteCalled).To(BeTrue())
		Expect(hasFinalizer(t.natGateway)).To(BeTrue())
	})

	It("should not call hubClient.Delete when K8s object has DeletionTimestamp", func() {
		now := metav1.Now()
		cr := newNATGatewayCR(gatewayID, hubNamespace, crName, &now)

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

		t := newTaskForDelete(gatewayID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteCalled).To(BeFalse())
		Expect(hasFinalizer(t.natGateway)).To(BeTrue())
	})

	It("should propagate error when hub cache returns error", func() {
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(nil, errors.New("hub not found"))

		t := newTaskForDelete(gatewayID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("hub not found"))
		Expect(hasFinalizer(t.natGateway)).To(BeTrue())
	})

	It("should remove finalizer when hub cache returns ErrHubNotFound", func() {
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(nil, controllers.ErrHubNotFound)

		t := newTaskForDelete(gatewayID, hubID, hubCache)
		Expect(hasFinalizer(t.natGateway)).To(BeTrue())

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasFinalizer(t.natGateway)).To(BeFalse())
	})

	It("should remove finalizer when no hub is assigned", func() {
		natGateway := privatev1.NATGateway_builder{
			Id: gatewayID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Status: privatev1.NATGatewayStatus_builder{}.Build(),
		}.Build()

		f := &function{
			logger: logger,
		}

		t := &task{
			r:          f,
			natGateway: natGateway,
		}

		Expect(hasFinalizer(t.natGateway)).To(BeTrue())

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasFinalizer(t.natGateway)).To(BeFalse())
	})
})

var _ = Describe("validateTenant", func() {
	It("should succeed when a tenant is assigned", func() {
		t := &task{
			natGateway: privatev1.NATGateway_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: "tenant-1",
				}.Build(),
			}.Build(),
		}

		err := t.validateTenant()
		Expect(err).ToNot(HaveOccurred())
	})

	It("should fail when tenant is empty", func() {
		t := &task{
			natGateway: privatev1.NATGateway_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: "",
				}.Build(),
			}.Build(),
		}

		err := t.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant"))
	})

	It("should fail when metadata is missing", func() {
		t := &task{
			natGateway: privatev1.NATGateway_builder{}.Build(),
		}

		err := t.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant"))
	})
})

var _ = Describe("setDefaults", func() {
	It("should set PENDING state when status is unspecified", func() {
		t := &task{
			natGateway: privatev1.NATGateway_builder{
				Id: "natgw-uuid-defaults",
			}.Build(),
		}

		t.setDefaults()

		Expect(t.natGateway.GetStatus().GetState()).To(
			Equal(privatev1.NATGatewayState_NAT_GATEWAY_STATE_PENDING),
		)
	})

	It("should not overwrite existing state", func() {
		t := &task{
			natGateway: privatev1.NATGateway_builder{
				Id: "natgw-uuid-existing-state",
				Status: privatev1.NATGatewayStatus_builder{
					State: privatev1.NATGatewayState_NAT_GATEWAY_STATE_READY,
				}.Build(),
			}.Build(),
		}

		t.setDefaults()

		Expect(t.natGateway.GetStatus().GetState()).To(
			Equal(privatev1.NATGatewayState_NAT_GATEWAY_STATE_READY),
		)
	})

	It("should create status if it doesn't exist", func() {
		t := &task{
			natGateway: privatev1.NATGateway_builder{
				Id: "natgw-uuid-no-status",
			}.Build(),
		}

		Expect(t.natGateway.HasStatus()).To(BeFalse())

		t.setDefaults()

		Expect(t.natGateway.HasStatus()).To(BeTrue())
		Expect(t.natGateway.GetStatus().GetState()).To(
			Equal(privatev1.NATGatewayState_NAT_GATEWAY_STATE_PENDING),
		)
	})
})

var _ = Describe("addFinalizer", func() {
	It("should add finalizer when not present", func() {
		t := &task{
			natGateway: privatev1.NATGateway_builder{
				Id: "natgw-uuid-no-finalizer",
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{},
				}.Build(),
			}.Build(),
		}

		added := t.addFinalizer()

		Expect(added).To(BeTrue())
		Expect(hasFinalizer(t.natGateway)).To(BeTrue())
	})

	It("should not add finalizer when already present", func() {
		t := &task{
			natGateway: privatev1.NATGateway_builder{
				Id: "natgw-uuid-has-finalizer",
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.Controller},
				}.Build(),
			}.Build(),
		}

		added := t.addFinalizer()

		Expect(added).To(BeFalse())
		Expect(hasFinalizer(t.natGateway)).To(BeTrue())
		Expect(t.natGateway.GetMetadata().GetFinalizers()).To(HaveLen(1))
	})

	It("should create metadata if it doesn't exist", func() {
		t := &task{
			natGateway: privatev1.NATGateway_builder{
				Id: "natgw-uuid-no-metadata",
			}.Build(),
		}

		Expect(t.natGateway.HasMetadata()).To(BeFalse())

		added := t.addFinalizer()

		Expect(added).To(BeTrue())
		Expect(t.natGateway.HasMetadata()).To(BeTrue())
		Expect(hasFinalizer(t.natGateway)).To(BeTrue())
	})
})

var _ = Describe("removeFinalizer", func() {
	It("should remove finalizer when present", func() {
		t := &task{
			natGateway: privatev1.NATGateway_builder{
				Id: "natgw-uuid-has-finalizer",
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.Controller, "other-finalizer"},
				}.Build(),
			}.Build(),
		}

		Expect(hasFinalizer(t.natGateway)).To(BeTrue())

		t.removeFinalizer()

		Expect(hasFinalizer(t.natGateway)).To(BeFalse())
		Expect(t.natGateway.GetMetadata().GetFinalizers()).To(ContainElement("other-finalizer"))
	})

	It("should do nothing when finalizer not present", func() {
		t := &task{
			natGateway: privatev1.NATGateway_builder{
				Id: "natgw-uuid-no-finalizer",
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{"other-finalizer"},
				}.Build(),
			}.Build(),
		}

		Expect(hasFinalizer(t.natGateway)).To(BeFalse())

		t.removeFinalizer()

		Expect(hasFinalizer(t.natGateway)).To(BeFalse())
		Expect(t.natGateway.GetMetadata().GetFinalizers()).To(ContainElement("other-finalizer"))
	})

	It("should do nothing when metadata doesn't exist", func() {
		t := &task{
			natGateway: privatev1.NATGateway_builder{
				Id: "natgw-uuid-no-metadata",
			}.Build(),
		}

		t.removeFinalizer()

		Expect(t.natGateway.HasMetadata()).To(BeFalse())
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

	It("should use existing hub from status", func() {
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
			natGateway: privatev1.NATGateway_builder{
				Id: "natgw-uuid-existing-hub",
				Spec: privatev1.NATGatewaySpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vn-uuid-1"}.Build(),
				}.Build(),
				Status: privatev1.NATGatewayStatus_builder{
					Hub: "hub-1",
				}.Build(),
			}.Build(),
		}

		err := t.selectHub(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(t.hubId).To(Equal("hub-1"))
		Expect(t.hubNamespace).To(Equal("hub-ns"))
	})

	It("should select hub randomly when status hub is empty", func() {
		hubsClient := &fakeHubsClient{
			listResponse: &privatev1.HubsListResponse{
				Items: []*privatev1.Hub{privatev1.Hub_builder{Id: "hub-random-1"}.Build()},
			},
		}

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), "hub-random-1").
			Return(&controllers.HubEntry{
				Namespace: "hub-random-ns",
				Client:    fake.NewClientBuilder().Build(),
			}, nil)

		t := &task{
			r: &function{
				logger:     logger,
				hubCache:   hubCache,
				hubsClient: hubsClient,
			},
			natGateway: privatev1.NATGateway_builder{
				Id: "natgw-uuid-random-hub",
			}.Build(),
		}

		err := t.selectHub(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(t.hubId).To(Equal("hub-random-1"))
		Expect(t.hubNamespace).To(Equal("hub-random-ns"))
	})

	It("should return error when no hubs are available", func() {
		hubsClient := &fakeHubsClient{
			listResponse: &privatev1.HubsListResponse{},
		}

		t := &task{
			r: &function{
				logger:     logger,
				hubsClient: hubsClient,
			},
			natGateway: privatev1.NATGateway_builder{
				Id: "natgw-uuid-no-hubs",
			}.Build(),
		}

		err := t.selectHub(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("there are no hubs"))
	})

	It("should return error when hub listing fails", func() {
		hubsClient := &fakeHubsClient{
			listErr: errors.New("hub listing failed"),
		}

		t := &task{
			r: &function{
				logger:     logger,
				hubsClient: hubsClient,
			},
			natGateway: privatev1.NATGateway_builder{
				Id: "natgw-uuid-hub-error",
			}.Build(),
		}

		err := t.selectHub(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("hub listing failed"))
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
						schema.GroupKind{Group: "osac.openshift.io", Kind: "NATGateway"},
						"natgw-test",
						field.ErrorList{
							field.Invalid(
								field.NewPath("spec", "virtualNetwork"),
								"invalid-value",
								"spec.virtualNetwork is invalid",
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

		natGatewaysClient := NewMockNATGatewaysClient(ctrl)
		natGatewaysClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.NATGatewaysUpdateRequest, opts ...grpc.CallOption) (*privatev1.NATGatewaysUpdateResponse, error) {
				return &privatev1.NATGatewaysUpdateResponse{Object: req.GetObject()}, nil
			}).
			MinTimes(1)

		natGateway := privatev1.NATGateway_builder{
			Id: "natgw-validation-test",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     "test-tenant",
			}.Build(),
			Spec: privatev1.NATGatewaySpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vn-123"}.Build(),
				ExternalIp:     privatev1.ExternalIPLocalReference_builder{Id: "eip-123"}.Build(),
			}.Build(),
			Status: privatev1.NATGatewayStatus_builder{
				State: privatev1.NATGatewayState_NAT_GATEWAY_STATE_PENDING,
				Hub:   "hub-1",
			}.Build(),
		}.Build()

		f := &function{
			logger:            logger,
			hubCache:          hubCache,
			natGatewaysClient: natGatewaysClient,
			maskCalculator:    masks.NewCalculator().Build(),
		}

		err := f.run(ctx, natGateway)
		Expect(err).ToNot(HaveOccurred())

		Expect(natGateway.GetStatus().GetState()).To(
			Equal(privatev1.NATGatewayState_NAT_GATEWAY_STATE_FAILED),
		)
		Expect(natGateway.GetStatus().GetMessage()).To(ContainSubstring("spec.virtualNetwork"))
	})
})
