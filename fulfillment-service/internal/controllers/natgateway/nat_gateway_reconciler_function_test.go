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
	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/controllers"
	"github.com/osac-project/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/fulfillment-service/internal/kubernetes/labels"
)

// fakeVirtualNetworksClient implements the VirtualNetworksClient interface for testing selectHub.
type fakeVirtualNetworksClient struct {
	privatev1.VirtualNetworksClient
	getResponse *privatev1.VirtualNetworksGetResponse
	getErr      error
}

func (f *fakeVirtualNetworksClient) Get(
	_ context.Context,
	_ *privatev1.VirtualNetworksGetRequest,
	_ ...grpc.CallOption,
) (*privatev1.VirtualNetworksGetResponse, error) {
	return f.getResponse, f.getErr
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
					VirtualNetwork: "vn-uuid-abc123",
					ExternalIp:     "eip-uuid-abc123",
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
					VirtualNetwork: "vn-uuid-abc456",
					ExternalIp:     "eip-uuid-abc456",
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
					VirtualNetwork: "vn-uuid-1",
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

	It("should derive hub from parent VirtualNetwork when status hub is empty", func() {
		vnClient := &fakeVirtualNetworksClient{
			getResponse: privatev1.VirtualNetworksGetResponse_builder{
				Object: privatev1.VirtualNetwork_builder{
					Id: "vn-uuid-1",
					Status: privatev1.VirtualNetworkStatus_builder{
						Hub: "vn-hub-1",
					}.Build(),
				}.Build(),
			}.Build(),
		}

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), "vn-hub-1").
			Return(&controllers.HubEntry{
				Namespace: "vn-hub-ns",
				Client:    fake.NewClientBuilder().Build(),
			}, nil)

		t := &task{
			r: &function{
				logger:                logger,
				hubCache:              hubCache,
				virtualNetworksClient: vnClient,
			},
			natGateway: privatev1.NATGateway_builder{
				Id: "natgw-uuid-derive-hub",
				Spec: privatev1.NATGatewaySpec_builder{
					VirtualNetwork: "vn-uuid-1",
				}.Build(),
			}.Build(),
		}

		err := t.selectHub(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(t.hubId).To(Equal("vn-hub-1"))
		Expect(t.hubNamespace).To(Equal("vn-hub-ns"))
	})

	It("should return error when parent VirtualNetwork has no hub", func() {
		vnClient := &fakeVirtualNetworksClient{
			getResponse: privatev1.VirtualNetworksGetResponse_builder{
				Object: privatev1.VirtualNetwork_builder{
					Id:     "vn-uuid-no-hub",
					Status: privatev1.VirtualNetworkStatus_builder{}.Build(),
				}.Build(),
			}.Build(),
		}

		t := &task{
			r: &function{
				logger:                logger,
				virtualNetworksClient: vnClient,
			},
			natGateway: privatev1.NATGateway_builder{
				Id: "natgw-uuid-vn-no-hub",
				Spec: privatev1.NATGatewaySpec_builder{
					VirtualNetwork: "vn-uuid-no-hub",
				}.Build(),
			}.Build(),
		}

		err := t.selectHub(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no hub assigned yet"))
	})

	It("should return error when VirtualNetwork lookup fails", func() {
		vnClient := &fakeVirtualNetworksClient{
			getErr: errors.New("virtual network not found"),
		}

		t := &task{
			r: &function{
				logger:                logger,
				virtualNetworksClient: vnClient,
			},
			natGateway: privatev1.NATGateway_builder{
				Id: "natgw-uuid-vn-error",
				Spec: privatev1.NATGatewaySpec_builder{
					VirtualNetwork: "vn-uuid-missing",
				}.Build(),
			}.Build(),
		}

		err := t.selectHub(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("virtual network not found"))
	})
})
