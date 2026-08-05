/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package virtualnetwork

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
	"github.com/osac-project/fulfillment-service/internal/masks"
)

var _ = Describe("buildSpec", func() {
	It("Includes all fields when IPv4 and IPv6 are present with capabilities", func() {
		ipv4 := "10.0.0.0/16"
		ipv6 := "2001:db8::/48"
		region := "us-east-1"
		networkClass := "cudn-net"
		implementationStrategy := "cudn"

		task := &task{
			virtualNetwork: privatev1.VirtualNetwork_builder{
				Id: "vnet-test-123",
				Spec: privatev1.VirtualNetworkSpec_builder{
					Region:                 region,
					NetworkClass:           networkClass,
					ImplementationStrategy: implementationStrategy,
					Ipv4Cidr:               &ipv4,
					Ipv6Cidr:               &ipv6,
					Capabilities: privatev1.VirtualNetworkCapabilities_builder{
						EnableIpv4:      true,
						EnableIpv6:      true,
						EnableDualStack: true,
					}.Build(),
				}.Build(),
			}.Build(),
		}

		spec := task.buildSpec()

		Expect(spec.Region).To(Equal(region))
		Expect(spec.NetworkClass).To(Equal(networkClass))
		Expect(spec.ImplementationStrategy).To(Equal(implementationStrategy))
		Expect(spec.IPv4CIDR).To(Equal(ipv4))
		Expect(spec.IPv6CIDR).To(Equal(ipv6))
	})

	It("Includes only IPv4 when IPv6 is not present", func() {
		ipv4 := "192.168.0.0/16"
		region := "eu-west-1"
		networkClass := "phys-net"
		implementationStrategy := "physnet"

		task := &task{
			virtualNetwork: privatev1.VirtualNetwork_builder{
				Id: "vnet-test-456",
				Spec: privatev1.VirtualNetworkSpec_builder{
					Region:                 region,
					NetworkClass:           networkClass,
					ImplementationStrategy: implementationStrategy,
					Ipv4Cidr:               &ipv4,
					Capabilities: privatev1.VirtualNetworkCapabilities_builder{
						EnableIpv4: true,
					}.Build(),
				}.Build(),
			}.Build(),
		}

		spec := task.buildSpec()

		Expect(spec.Region).To(Equal(region))
		Expect(spec.NetworkClass).To(Equal(networkClass))
		Expect(spec.ImplementationStrategy).To(Equal(implementationStrategy))
		Expect(spec.IPv4CIDR).To(Equal(ipv4))
		Expect(spec.IPv6CIDR).To(BeEmpty())
	})

	It("Includes only IPv6 when IPv4 is not present", func() {
		ipv6 := "fd00:1234::/32"
		region := "ap-south-1"
		networkClass := "ovn-kubernetes"
		implementationStrategy := "ovn"

		task := &task{
			virtualNetwork: privatev1.VirtualNetwork_builder{
				Id: "vnet-test-789",
				Spec: privatev1.VirtualNetworkSpec_builder{
					Region:                 region,
					NetworkClass:           networkClass,
					ImplementationStrategy: implementationStrategy,
					Ipv6Cidr:               &ipv6,
					Capabilities: privatev1.VirtualNetworkCapabilities_builder{
						EnableIpv6: true,
					}.Build(),
				}.Build(),
			}.Build(),
		}

		spec := task.buildSpec()

		Expect(spec.Region).To(Equal(region))
		Expect(spec.NetworkClass).To(Equal(networkClass))
		Expect(spec.ImplementationStrategy).To(Equal(implementationStrategy))
		Expect(spec.IPv4CIDR).To(BeEmpty())
		Expect(spec.IPv6CIDR).To(Equal(ipv6))
	})

	It("Handles missing capabilities field", func() {
		ipv4 := "172.16.0.0/12"
		region := "us-west-2"
		networkClass := "cudn-net"
		implementationStrategy := "cudn"

		task := &task{
			virtualNetwork: privatev1.VirtualNetwork_builder{
				Id: "vnet-test-no-caps",
				Spec: privatev1.VirtualNetworkSpec_builder{
					Region:                 region,
					NetworkClass:           networkClass,
					ImplementationStrategy: implementationStrategy,
					Ipv4Cidr:               &ipv4,
				}.Build(),
			}.Build(),
		}

		spec := task.buildSpec()

		Expect(spec.Region).To(Equal(region))
		Expect(spec.NetworkClass).To(Equal(networkClass))
		Expect(spec.ImplementationStrategy).To(Equal(implementationStrategy))
		Expect(spec.IPv4CIDR).To(Equal(ipv4))
	})
})

// newVirtualNetworkCR creates a typed VirtualNetwork CR for use with the fake client.
func newVirtualNetworkCR(id, namespace, name string, deletionTimestamp *metav1.Time) *osacv1alpha1.VirtualNetwork {
	obj := &osacv1alpha1.VirtualNetwork{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				labels.VirtualNetworkUuid: id,
			},
		},
	}
	if deletionTimestamp != nil {
		obj.SetDeletionTimestamp(deletionTimestamp)
		obj.SetFinalizers([]string{"osac.openshift.io/virtualnetwork"})
	}
	return obj
}

// hasFinalizer checks if the fulfillment-controller finalizer is present on the virtual network.
func hasFinalizer(virtualNetwork *privatev1.VirtualNetwork) bool {
	return slices.Contains(virtualNetwork.GetMetadata().GetFinalizers(), finalizers.Controller)
}

// newTaskForDelete creates a task configured for testing delete() with hub-dependent paths.
func newTaskForDelete(virtualNetworkID, hubID string, hubCache controllers.HubCache) *task {
	virtualNetwork := privatev1.VirtualNetwork_builder{
		Id: virtualNetworkID,
		Metadata: privatev1.Metadata_builder{
			Finalizers: []string{finalizers.Controller},
		}.Build(),
		Status: privatev1.VirtualNetworkStatus_builder{
			Hub: hubID,
		}.Build(),
	}.Build()

	f := &function{
		logger:   logger,
		hubCache: hubCache,
	}

	return &task{
		r:              f,
		virtualNetwork: virtualNetwork,
	}
}

var _ = Describe("delete", func() {
	var (
		ctx              context.Context
		ctrl             *gomock.Controller
		mockHubCache     *controllers.MockHubCache
		fakeClient       clnt.Client
		scheme           *runtime.Scheme
		virtualNetworkID string
		hubID            string
		namespace        string
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		mockHubCache = controllers.NewMockHubCache(ctrl)

		virtualNetworkID = "vnet-delete-123"
		hubID = "hub-xyz"
		namespace = "test-namespace"

		scheme = runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	When("hub is not yet assigned", func() {
		It("removes the finalizer immediately", func() {
			task := newTaskForDelete(virtualNetworkID, "", mockHubCache)
			task.virtualNetwork.GetStatus().SetHub("")

			err := task.delete(ctx)

			Expect(err).ToNot(HaveOccurred())
			Expect(hasFinalizer(task.virtualNetwork)).To(BeFalse(), "finalizer should be removed")
		})
	})

	When("hub cache fails", func() {
		It("returns error and does not remove finalizer", func() {
			expectedErr := errors.New("hub cache unavailable")
			mockHubCache.EXPECT().Get(gomock.Any(), hubID).Return(nil, expectedErr)

			task := newTaskForDelete(virtualNetworkID, hubID, mockHubCache)

			err := task.delete(ctx)

			Expect(err).To(MatchError(expectedErr))
			Expect(hasFinalizer(task.virtualNetwork)).To(BeTrue(), "finalizer should remain")
		})
	})

	When("hub cache returns ErrHubNotFound", func() {
		It("removes finalizer to allow archiving", func() {
			// This test verifies the core behavior: when a hub is decommissioned/deleted,
			// the reconciler removes its finalizer to allow the virtual network to be archived.
			mockHubCache.EXPECT().
				Get(gomock.Any(), hubID).
				Return(nil, controllers.ErrHubNotFound)

			task := newTaskForDelete(virtualNetworkID, hubID, mockHubCache)
			Expect(hasFinalizer(task.virtualNetwork)).To(BeTrue())

			err := task.delete(ctx)

			// Should return nil (not propagate the error)
			Expect(err).ToNot(HaveOccurred())
			// Finalizer should be removed to allow archiving
			Expect(hasFinalizer(task.virtualNetwork)).To(BeFalse())
		})
	})

	When("K8s object does not exist", func() {
		It("removes the finalizer", func() {
			fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()

			mockHubCache.EXPECT().
				Get(gomock.Any(), hubID).
				Return(&controllers.HubEntry{
					Namespace: namespace,
					Client:    fakeClient,
				}, nil)

			task := newTaskForDelete(virtualNetworkID, hubID, mockHubCache)

			err := task.delete(ctx)

			Expect(err).ToNot(HaveOccurred())
			Expect(hasFinalizer(task.virtualNetwork)).To(BeFalse(), "finalizer should be removed")
		})
	})

	When("K8s object exists without deletion timestamp", func() {
		It("deletes the K8s object and keeps the finalizer", func() {
			existingCR := newVirtualNetworkCR(virtualNetworkID, namespace, "vnet-123", nil)

			deleteCalled := false
			fakeClient = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(existingCR).
				WithInterceptorFuncs(interceptor.Funcs{
					Delete: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.DeleteOption) error {
						deleteCalled = true
						return nil
					},
				}).
				Build()

			mockHubCache.EXPECT().
				Get(gomock.Any(), hubID).
				Return(&controllers.HubEntry{
					Namespace: namespace,
					Client:    fakeClient,
				}, nil)

			task := newTaskForDelete(virtualNetworkID, hubID, mockHubCache)

			err := task.delete(ctx)

			Expect(err).ToNot(HaveOccurred())
			Expect(deleteCalled).To(BeTrue(), "Delete should have been called")
			Expect(hasFinalizer(task.virtualNetwork)).To(BeTrue(), "finalizer should remain until K8s object is fully deleted")
		})
	})

	When("K8s object exists with deletion timestamp (finalizers being processed)", func() {
		It("does not remove the finalizer and waits", func() {
			now := metav1.Now()
			existingCR := newVirtualNetworkCR(virtualNetworkID, namespace, "vnet-456", &now)
			fakeClient = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(existingCR).
				Build()

			mockHubCache.EXPECT().
				Get(gomock.Any(), hubID).
				Return(&controllers.HubEntry{
					Namespace: namespace,
					Client:    fakeClient,
				}, nil)

			task := newTaskForDelete(virtualNetworkID, hubID, mockHubCache)

			err := task.delete(ctx)

			Expect(err).ToNot(HaveOccurred())
			Expect(hasFinalizer(task.virtualNetwork)).To(BeTrue(), "finalizer should remain while K8s finalizers process")
		})
	})

	When("K8s List operation fails", func() {
		It("returns error and does not remove finalizer", func() {
			expectedErr := errors.New("list failed")
			fakeClient = fake.NewClientBuilder().
				WithScheme(scheme).
				WithInterceptorFuncs(interceptor.Funcs{
					List: func(ctx context.Context, client clnt.WithWatch, list clnt.ObjectList, opts ...clnt.ListOption) error {
						return expectedErr
					},
				}).
				Build()

			mockHubCache.EXPECT().
				Get(gomock.Any(), hubID).
				Return(&controllers.HubEntry{
					Namespace: namespace,
					Client:    fakeClient,
				}, nil)

			task := newTaskForDelete(virtualNetworkID, hubID, mockHubCache)

			err := task.delete(ctx)

			Expect(err).To(MatchError(expectedErr))
			Expect(hasFinalizer(task.virtualNetwork)).To(BeTrue(), "finalizer should remain on error")
		})
	})
})

var _ = Describe("validateTenant", func() {
	It("succeeds when a tenant is assigned", func() {
		task := &task{
			virtualNetwork: privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: "tenant-abc",
				}.Build(),
			}.Build(),
		}

		err := task.validateTenant()

		Expect(err).ToNot(HaveOccurred())
	})

	It("fails when no metadata is present", func() {
		task := &task{
			virtualNetwork: privatev1.VirtualNetwork_builder{}.Build(),
		}

		err := task.validateTenant()

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant"))
	})

	It("fails when tenant is empty", func() {
		task := &task{
			virtualNetwork: privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: "",
				}.Build(),
			}.Build(),
		}

		err := task.validateTenant()

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant"))
	})
})

var _ = Describe("setDefaults", func() {
	It("sets status to PENDING when status is missing", func() {
		task := &task{
			virtualNetwork: privatev1.VirtualNetwork_builder{}.Build(),
		}

		task.setDefaults()

		Expect(task.virtualNetwork.HasStatus()).To(BeTrue())
		Expect(task.virtualNetwork.GetStatus().GetState()).To(Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))
	})

	It("sets state to PENDING when state is UNSPECIFIED", func() {
		task := &task{
			virtualNetwork: privatev1.VirtualNetwork_builder{
				Status: privatev1.VirtualNetworkStatus_builder{
					State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_UNSPECIFIED,
				}.Build(),
			}.Build(),
		}

		task.setDefaults()

		Expect(task.virtualNetwork.GetStatus().GetState()).To(Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))
	})

	It("does not change state when already set to READY", func() {
		task := &task{
			virtualNetwork: privatev1.VirtualNetwork_builder{
				Status: privatev1.VirtualNetworkStatus_builder{
					State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
				}.Build(),
			}.Build(),
		}

		task.setDefaults()

		Expect(task.virtualNetwork.GetStatus().GetState()).To(Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY))
	})

	It("does not change state when already set to FAILED", func() {
		task := &task{
			virtualNetwork: privatev1.VirtualNetwork_builder{
				Status: privatev1.VirtualNetworkStatus_builder{
					State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_FAILED,
				}.Build(),
			}.Build(),
		}

		task.setDefaults()

		Expect(task.virtualNetwork.GetStatus().GetState()).To(Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_FAILED))
	})
})

var _ = Describe("addFinalizer", func() {
	It("adds finalizer when not present and creates metadata", func() {
		task := &task{
			virtualNetwork: privatev1.VirtualNetwork_builder{}.Build(),
		}

		added := task.addFinalizer()

		Expect(added).To(BeTrue())
		Expect(hasFinalizer(task.virtualNetwork)).To(BeTrue())
	})

	It("adds finalizer when not present but metadata exists", func() {
		task := &task{
			virtualNetwork: privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{"other-finalizer"},
				}.Build(),
			}.Build(),
		}

		added := task.addFinalizer()

		Expect(added).To(BeTrue())
		Expect(hasFinalizer(task.virtualNetwork)).To(BeTrue())
		Expect(task.virtualNetwork.GetMetadata().GetFinalizers()).To(ContainElement("other-finalizer"))
	})

	It("does not add finalizer when already present", func() {
		task := &task{
			virtualNetwork: privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.Controller},
				}.Build(),
			}.Build(),
		}

		added := task.addFinalizer()

		Expect(added).To(BeFalse())
		finalizerList := task.virtualNetwork.GetMetadata().GetFinalizers()
		Expect(finalizerList).To(HaveLen(1))
		Expect(finalizerList[0]).To(Equal(finalizers.Controller))
	})
})

var _ = Describe("removeFinalizer", func() {
	It("removes the controller finalizer when present", func() {
		task := &task{
			virtualNetwork: privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.Controller, "other-finalizer"},
				}.Build(),
			}.Build(),
		}

		task.removeFinalizer()

		Expect(hasFinalizer(task.virtualNetwork)).To(BeFalse())
		Expect(task.virtualNetwork.GetMetadata().GetFinalizers()).To(ConsistOf("other-finalizer"))
	})

	It("does nothing when finalizer is not present", func() {
		task := &task{
			virtualNetwork: privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{"other-finalizer"},
				}.Build(),
			}.Build(),
		}

		task.removeFinalizer()

		Expect(task.virtualNetwork.GetMetadata().GetFinalizers()).To(ConsistOf("other-finalizer"))
	})

	It("does nothing when metadata is missing", func() {
		task := &task{
			virtualNetwork: privatev1.VirtualNetwork_builder{}.Build(),
		}

		task.removeFinalizer()

		Expect(task.virtualNetwork.HasMetadata()).To(BeFalse())
	})
})

var _ = Describe("hub persistence", func() {
	const (
		virtualNetworkID = "test-vn-hub"
		tenantName       = "test-tenant"
		hubID            = "test-hub-123"
		hubNamespace     = "hub-123-ns"
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

	It("should select hub and return without creating VirtualNetwork CR", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{Namespace: hubNamespace, Client: fakeClient}, nil).
			AnyTimes()

		hubsClient := controllers.NewMockHubsClient(ctrl)
		hubsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.HubsListResponse{
				Items: []*privatev1.Hub{privatev1.Hub_builder{Id: hubID}.Build()},
			}, nil)

		vnClient := NewMockVirtualNetworksClient(ctrl)
		vnClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.VirtualNetworksUpdateRequest, opts ...grpc.CallOption) (*privatev1.VirtualNetworksUpdateResponse, error) {
				return &privatev1.VirtualNetworksUpdateResponse{Object: req.GetObject()}, nil
			}).AnyTimes()

		vn := privatev1.VirtualNetwork_builder{
			Id: virtualNetworkID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.VirtualNetworkSpec_builder{
				Region:       "us-east-1",
				NetworkClass: "cudn-net",
			}.Build(),
			Status: privatev1.VirtualNetworkStatus_builder{
				State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING,
				Hub:   "",
			}.Build(),
		}.Build()

		f := &function{
			logger:                logger,
			hubCache:              hubCache,
			virtualNetworksClient: vnClient,
			hubsClient:            hubsClient,
			maskCalculator:        masks.NewCalculator().Build(),
		}

		err := f.run(ctx, vn)
		Expect(err).ToNot(HaveOccurred())
		Expect(vn.GetStatus().GetHub()).To(Equal(hubID))

		list := &osacv1alpha1.VirtualNetworkList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(BeEmpty())
	})

	It("should not create CR when no hubs available", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		hubCache := controllers.NewMockHubCache(ctrl)

		hubsClient := controllers.NewMockHubsClient(ctrl)
		hubsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.HubsListResponse{
				Items: []*privatev1.Hub{},
			}, nil)

		vnClient := NewMockVirtualNetworksClient(ctrl)

		vn := privatev1.VirtualNetwork_builder{
			Id: virtualNetworkID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.VirtualNetworkSpec_builder{
				Region:       "us-east-1",
				NetworkClass: "cudn-net",
			}.Build(),
			Status: privatev1.VirtualNetworkStatus_builder{
				State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING,
				Hub:   "",
			}.Build(),
		}.Build()

		f := &function{
			logger:                logger,
			hubCache:              hubCache,
			virtualNetworksClient: vnClient,
			hubsClient:            hubsClient,
			maskCalculator:        masks.NewCalculator().Build(),
		}

		err := f.run(ctx, vn)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("there are no hubs"))

		list := &osacv1alpha1.VirtualNetworkList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(BeEmpty())
	})

	It("should skip hub selection if already set", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{Namespace: hubNamespace, Client: fakeClient}, nil).
			AnyTimes()

		hubsClient := controllers.NewMockHubsClient(ctrl)

		vnClient := NewMockVirtualNetworksClient(ctrl)
		vnClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.VirtualNetworksUpdateRequest, opts ...grpc.CallOption) (*privatev1.VirtualNetworksUpdateResponse, error) {
				Expect(req.GetUpdateMask().GetPaths()).ToNot(ContainElement("status.hub"))
				return &privatev1.VirtualNetworksUpdateResponse{Object: req.GetObject()}, nil
			}).AnyTimes()

		vn := privatev1.VirtualNetwork_builder{
			Id: virtualNetworkID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.VirtualNetworkSpec_builder{
				Region:       "us-east-1",
				NetworkClass: "cudn-net",
			}.Build(),
			Status: privatev1.VirtualNetworkStatus_builder{
				State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING,
				Hub:   hubID,
			}.Build(),
		}.Build()

		f := &function{
			logger:                logger,
			hubCache:              hubCache,
			virtualNetworksClient: vnClient,
			hubsClient:            hubsClient,
			maskCalculator:        nil,
		}

		err := f.run(ctx, vn)
		Expect(err).ToNot(HaveOccurred())

		list := &osacv1alpha1.VirtualNetworkList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))
		Expect(list.Items[0].Namespace).To(Equal(hubNamespace))
	})

	It("should create CR on second reconcile after hub is persisted", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{Namespace: hubNamespace, Client: fakeClient}, nil).
			AnyTimes()

		hubsClient := controllers.NewMockHubsClient(ctrl)
		hubsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.HubsListResponse{
				Items: []*privatev1.Hub{privatev1.Hub_builder{Id: hubID}.Build()},
			}, nil)

		vnClient := NewMockVirtualNetworksClient(ctrl)
		vnClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.VirtualNetworksUpdateRequest, opts ...grpc.CallOption) (*privatev1.VirtualNetworksUpdateResponse, error) {
				return &privatev1.VirtualNetworksUpdateResponse{Object: req.GetObject()}, nil
			}).AnyTimes()

		vn := privatev1.VirtualNetwork_builder{
			Id: virtualNetworkID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.VirtualNetworkSpec_builder{
				Region:       "us-east-1",
				NetworkClass: "cudn-net",
			}.Build(),
			Status: privatev1.VirtualNetworkStatus_builder{
				State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING,
				Hub:   "",
			}.Build(),
		}.Build()

		f := &function{
			logger:                logger,
			hubCache:              hubCache,
			virtualNetworksClient: vnClient,
			hubsClient:            hubsClient,
			maskCalculator:        masks.NewCalculator().Build(),
		}

		// First reconcile: hub is empty, should select hub and return without creating CR
		err := f.run(ctx, vn)
		Expect(err).ToNot(HaveOccurred())

		list := &osacv1alpha1.VirtualNetworkList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(BeEmpty())

		// Second reconcile: hub already set, should create CR
		vn.GetStatus().SetHub(hubID)

		err = f.run(ctx, vn)
		Expect(err).ToNot(HaveOccurred())

		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))
		Expect(list.Items[0].Namespace).To(Equal(hubNamespace))
	})
})
