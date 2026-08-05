/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package subnet

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

var _ = Describe("buildSpec", func() {
	It("Includes all fields when IPv4 and IPv6 are present", func() {
		ipv4 := "10.0.1.0/24"
		ipv6 := "2001:db8::/64"
		vnetID := "vnet-123"

		task := &task{
			subnet: privatev1.Subnet_builder{
				Id: "subnet-test-123",
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: vnetID,
					Ipv4Cidr:       &ipv4,
					Ipv6Cidr:       &ipv6,
				}.Build(),
			}.Build(),
		}

		spec := task.buildSpec()

		Expect(spec.VirtualNetwork).To(Equal(vnetID))
		Expect(spec.IPv4CIDR).To(Equal(ipv4))
		Expect(spec.IPv6CIDR).To(Equal(ipv6))
	})

	It("Includes only IPv4 when IPv6 is not present", func() {
		ipv4 := "192.168.1.0/24"
		vnetID := "vnet-456"

		task := &task{
			subnet: privatev1.Subnet_builder{
				Id: "subnet-test-456",
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: vnetID,
					Ipv4Cidr:       &ipv4,
				}.Build(),
			}.Build(),
		}

		spec := task.buildSpec()

		Expect(spec.VirtualNetwork).To(Equal(vnetID))
		Expect(spec.IPv4CIDR).To(Equal(ipv4))
		Expect(spec.IPv6CIDR).To(BeEmpty())
	})

	It("Includes only IPv6 when IPv4 is not present", func() {
		ipv6 := "fd00:1234::/64"
		vnetID := "vnet-789"

		task := &task{
			subnet: privatev1.Subnet_builder{
				Id: "subnet-test-789",
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: vnetID,
					Ipv6Cidr:       &ipv6,
				}.Build(),
			}.Build(),
		}

		spec := task.buildSpec()

		Expect(spec.VirtualNetwork).To(Equal(vnetID))
		Expect(spec.IPv4CIDR).To(BeEmpty())
		Expect(spec.IPv6CIDR).To(Equal(ipv6))
	})
})

// newSubnetCR creates a typed Subnet CR for use with the fake client.
func newSubnetCR(id, namespace, name string, deletionTimestamp *metav1.Time) *osacv1alpha1.Subnet {
	obj := &osacv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				labels.SubnetUuid: id,
			},
		},
	}
	if deletionTimestamp != nil {
		obj.SetDeletionTimestamp(deletionTimestamp)
		obj.SetFinalizers([]string{"osac.openshift.io/subnet"})
	}
	return obj
}

// hasFinalizer checks if the fulfillment-controller finalizer is present on the subnet.
func hasFinalizer(subnet *privatev1.Subnet) bool {
	return slices.Contains(subnet.GetMetadata().GetFinalizers(), finalizers.Controller)
}

// newTaskForDelete creates a task configured for testing delete() with hub-dependent paths.
func newTaskForDelete(subnetID, hubID string, hubCache controllers.HubCache) *task {
	subnet := privatev1.Subnet_builder{
		Id: subnetID,
		Metadata: privatev1.Metadata_builder{
			Finalizers: []string{finalizers.Controller},
		}.Build(),
		Status: privatev1.SubnetStatus_builder{
			Hub: hubID,
		}.Build(),
	}.Build()

	f := &function{
		logger:   logger,
		hubCache: hubCache,
	}

	return &task{
		r:      f,
		subnet: subnet,
	}
}

var _ = Describe("delete", func() {
	const (
		subnetID     = "subnet-delete-id"
		hubID        = "test-hub"
		hubNamespace = "test-ns"
		crName       = "subnet-test"
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

		t := newTaskForDelete(subnetID, hubID, hubCache)
		Expect(hasFinalizer(t.subnet)).To(BeTrue())

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasFinalizer(t.subnet)).To(BeFalse())
	})

	It("should call hubClient.Delete when K8s object exists without DeletionTimestamp", func() {
		cr := newSubnetCR(subnetID, hubNamespace, crName, nil)

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

		t := newTaskForDelete(subnetID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteCalled).To(BeTrue())
		// Finalizer should NOT be removed — K8s object still exists
		Expect(hasFinalizer(t.subnet)).To(BeTrue())
	})

	It("should not call hubClient.Delete when K8s object has DeletionTimestamp", func() {
		now := metav1.Now()
		cr := newSubnetCR(subnetID, hubNamespace, crName, &now)

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

		t := newTaskForDelete(subnetID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteCalled).To(BeFalse())
		// Finalizer should NOT be removed — K8s object still being deleted
		Expect(hasFinalizer(t.subnet)).To(BeTrue())
	})

	It("should propagate error when hub cache returns error", func() {
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(nil, errors.New("hub not found"))

		t := newTaskForDelete(subnetID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("hub not found"))
		// Finalizer should NOT be removed on error
		Expect(hasFinalizer(t.subnet)).To(BeTrue())
	})

	It("should remove finalizer when hub cache returns ErrHubNotFound", func() {
		// This test verifies the core behavior: when a hub is decommissioned/deleted,
		// the reconciler removes its finalizer to allow the subnet to be archived.
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(nil, controllers.ErrHubNotFound)

		t := newTaskForDelete(subnetID, hubID, hubCache)
		Expect(hasFinalizer(t.subnet)).To(BeTrue())

		err := t.delete(ctx)
		// Should return nil (not propagate the error)
		Expect(err).ToNot(HaveOccurred())
		// Finalizer should be removed to allow archiving
		Expect(hasFinalizer(t.subnet)).To(BeFalse())
	})

	It("should remove finalizer when no hub is assigned", func() {
		subnet := privatev1.Subnet_builder{
			Id: subnetID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Status: privatev1.SubnetStatus_builder{
				// No hub assigned
			}.Build(),
		}.Build()

		f := &function{
			logger: logger,
		}

		t := &task{
			r:      f,
			subnet: subnet,
		}

		Expect(hasFinalizer(t.subnet)).To(BeTrue())

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasFinalizer(t.subnet)).To(BeFalse())
	})
})

var _ = Describe("validateTenant", func() {
	It("should succeed when a tenant is assigned", func() {
		subnet := privatev1.Subnet_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "tenant-1",
			}.Build(),
		}.Build()

		t := &task{
			subnet: subnet,
		}

		err := t.validateTenant()
		Expect(err).ToNot(HaveOccurred())
	})

	It("should fail when tenant is empty", func() {
		subnet := privatev1.Subnet_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "",
			}.Build(),
		}.Build()

		t := &task{
			subnet: subnet,
		}

		err := t.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant"))
	})

	It("should fail when metadata is missing", func() {
		subnet := privatev1.Subnet_builder{}.Build()

		t := &task{
			subnet: subnet,
		}

		err := t.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant"))
	})
})

var _ = Describe("setDefaults", func() {
	It("should set PENDING state when status is unspecified", func() {
		subnet := privatev1.Subnet_builder{
			Id: "subnet-defaults",
		}.Build()

		t := &task{
			subnet: subnet,
		}

		t.setDefaults()

		Expect(t.subnet.GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_PENDING))
	})

	It("should not overwrite existing state", func() {
		subnet := privatev1.Subnet_builder{
			Id: "subnet-existing-state",
			Status: privatev1.SubnetStatus_builder{
				State: privatev1.SubnetState_SUBNET_STATE_READY,
			}.Build(),
		}.Build()

		t := &task{
			subnet: subnet,
		}

		t.setDefaults()

		Expect(t.subnet.GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_READY))
	})

	It("should create status if it doesn't exist", func() {
		subnet := privatev1.Subnet_builder{
			Id: "subnet-no-status",
		}.Build()

		t := &task{
			subnet: subnet,
		}

		Expect(t.subnet.HasStatus()).To(BeFalse())

		t.setDefaults()

		Expect(t.subnet.HasStatus()).To(BeTrue())
		Expect(t.subnet.GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_PENDING))
	})
})

var _ = Describe("addFinalizer", func() {
	It("should add finalizer when not present", func() {
		subnet := privatev1.Subnet_builder{
			Id: "subnet-no-finalizer",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{},
			}.Build(),
		}.Build()

		t := &task{
			subnet: subnet,
		}

		added := t.addFinalizer()

		Expect(added).To(BeTrue())
		Expect(hasFinalizer(t.subnet)).To(BeTrue())
	})

	It("should not add finalizer when already present", func() {
		subnet := privatev1.Subnet_builder{
			Id: "subnet-has-finalizer",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
		}.Build()

		t := &task{
			subnet: subnet,
		}

		added := t.addFinalizer()

		Expect(added).To(BeFalse())
		Expect(hasFinalizer(t.subnet)).To(BeTrue())
		// Should not duplicate
		Expect(t.subnet.GetMetadata().GetFinalizers()).To(HaveLen(1))
	})

	It("should create metadata if it doesn't exist", func() {
		subnet := privatev1.Subnet_builder{
			Id: "subnet-no-metadata",
		}.Build()

		t := &task{
			subnet: subnet,
		}

		Expect(t.subnet.HasMetadata()).To(BeFalse())

		added := t.addFinalizer()

		Expect(added).To(BeTrue())
		Expect(t.subnet.HasMetadata()).To(BeTrue())
		Expect(hasFinalizer(t.subnet)).To(BeTrue())
	})
})

var _ = Describe("removeFinalizer", func() {
	It("should remove finalizer when present", func() {
		subnet := privatev1.Subnet_builder{
			Id: "subnet-has-finalizer",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller, "other-finalizer"},
			}.Build(),
		}.Build()

		t := &task{
			subnet: subnet,
		}

		Expect(hasFinalizer(t.subnet)).To(BeTrue())

		t.removeFinalizer()

		Expect(hasFinalizer(t.subnet)).To(BeFalse())
		// Other finalizers should remain
		Expect(t.subnet.GetMetadata().GetFinalizers()).To(ContainElement("other-finalizer"))
	})

	It("should do nothing when finalizer not present", func() {
		subnet := privatev1.Subnet_builder{
			Id: "subnet-no-finalizer",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{"other-finalizer"},
			}.Build(),
		}.Build()

		t := &task{
			subnet: subnet,
		}

		Expect(hasFinalizer(t.subnet)).To(BeFalse())

		t.removeFinalizer()

		Expect(hasFinalizer(t.subnet)).To(BeFalse())
		Expect(t.subnet.GetMetadata().GetFinalizers()).To(ContainElement("other-finalizer"))
	})

	It("should do nothing when metadata doesn't exist", func() {
		subnet := privatev1.Subnet_builder{
			Id: "subnet-no-metadata",
		}.Build()

		t := &task{
			subnet: subnet,
		}

		// Should not panic
		t.removeFinalizer()

		Expect(t.subnet.HasMetadata()).To(BeFalse())
	})
})

var _ = Describe("hub persistence", func() {
	const (
		subnetID     = "test-subnet-hub"
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

	It("should select hub and return without creating Subnet CR", func() {
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

		subnetsClient := NewMockSubnetsClient(ctrl)
		subnetsClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.SubnetsUpdateRequest, opts ...grpc.CallOption) (*privatev1.SubnetsUpdateResponse, error) {
				return &privatev1.SubnetsUpdateResponse{Object: req.GetObject()}, nil
			}).AnyTimes()

		subnet := privatev1.Subnet_builder{
			Id: subnetID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.SubnetSpec_builder{
				VirtualNetwork: "vn-123",
			}.Build(),
			Status: privatev1.SubnetStatus_builder{
				State: privatev1.SubnetState_SUBNET_STATE_PENDING,
				Hub:   "",
			}.Build(),
		}.Build()

		f := &function{
			logger:         logger,
			hubCache:       hubCache,
			subnetsClient:  subnetsClient,
			hubsClient:     hubsClient,
			maskCalculator: nil,
		}

		err := f.run(ctx, subnet)
		Expect(err).ToNot(HaveOccurred())
		Expect(subnet.GetStatus().GetHub()).To(Equal(hubID))

		list := &osacv1alpha1.SubnetList{}
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

		subnetsClient := NewMockSubnetsClient(ctrl)

		subnet := privatev1.Subnet_builder{
			Id: subnetID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.SubnetSpec_builder{
				VirtualNetwork: "vn-123",
			}.Build(),
			Status: privatev1.SubnetStatus_builder{
				State: privatev1.SubnetState_SUBNET_STATE_PENDING,
				Hub:   "",
			}.Build(),
		}.Build()

		f := &function{
			logger:         logger,
			hubCache:       hubCache,
			subnetsClient:  subnetsClient,
			hubsClient:     hubsClient,
			maskCalculator: nil,
		}

		err := f.run(ctx, subnet)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("there are no hubs"))

		list := &osacv1alpha1.SubnetList{}
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

		subnetsClient := NewMockSubnetsClient(ctrl)
		subnetsClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.SubnetsUpdateRequest, opts ...grpc.CallOption) (*privatev1.SubnetsUpdateResponse, error) {
				Expect(req.GetUpdateMask().GetPaths()).ToNot(ContainElement("status.hub"))
				return &privatev1.SubnetsUpdateResponse{Object: req.GetObject()}, nil
			}).AnyTimes()

		subnet := privatev1.Subnet_builder{
			Id: subnetID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.SubnetSpec_builder{
				VirtualNetwork: "vn-123",
			}.Build(),
			Status: privatev1.SubnetStatus_builder{
				State: privatev1.SubnetState_SUBNET_STATE_PENDING,
				Hub:   hubID,
			}.Build(),
		}.Build()

		f := &function{
			logger:         logger,
			hubCache:       hubCache,
			subnetsClient:  subnetsClient,
			hubsClient:     hubsClient,
			maskCalculator: nil,
		}

		err := f.run(ctx, subnet)
		Expect(err).ToNot(HaveOccurred())

		list := &osacv1alpha1.SubnetList{}
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

		subnetsClient := NewMockSubnetsClient(ctrl)
		subnetsClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.SubnetsUpdateRequest, opts ...grpc.CallOption) (*privatev1.SubnetsUpdateResponse, error) {
				return &privatev1.SubnetsUpdateResponse{Object: req.GetObject()}, nil
			}).AnyTimes()

		subnet := privatev1.Subnet_builder{
			Id: subnetID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.SubnetSpec_builder{
				VirtualNetwork: "vn-123",
			}.Build(),
			Status: privatev1.SubnetStatus_builder{
				State: privatev1.SubnetState_SUBNET_STATE_PENDING,
				Hub:   "",
			}.Build(),
		}.Build()

		f := &function{
			logger:         logger,
			hubCache:       hubCache,
			subnetsClient:  subnetsClient,
			hubsClient:     hubsClient,
			maskCalculator: nil,
		}

		// First reconcile: hub is empty, gets selected and persisted, but no CR created
		err := f.run(ctx, subnet)
		Expect(err).ToNot(HaveOccurred())

		list := &osacv1alpha1.SubnetList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(BeEmpty())

		// Second reconcile: hub already set, CR should be created
		subnet.GetStatus().SetHub(hubID)

		err = f.run(ctx, subnet)
		Expect(err).ToNot(HaveOccurred())

		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))
		Expect(list.Items[0].Namespace).To(Equal(hubNamespace))
	})
})
