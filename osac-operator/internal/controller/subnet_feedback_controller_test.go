/*
Copyright 2025.

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

package controller

import (
	"context"
	"net"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/osac-project/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac-operator/internal/api/osac/private/v1"
)

var _ = Describe("SubnetFeedbackController", func() {
	const (
		subnetName      = "test-subnet"
		subnetNamespace = "test-namespace"
		subnetID        = "subnet-123"
		testIPv4CIDR    = "10.0.1.0/24"
	)

	var (
		ctx        context.Context
		k8sClient  client.Client
		mockServer *mockSubnetsServer
		reconciler *SubnetFeedbackReconciler
		grpcServer *grpc.Server
		listener   *bufconn.Listener
	)

	BeforeEach(func() {
		ctx = context.Background()

		// Create fake Kubernetes client
		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		k8sClient = fake.NewClientBuilder().WithScheme(scheme).Build()

		// Create mock gRPC server
		mockServer = &mockSubnetsServer{
			subnets: make(map[string]*privatev1.Subnet),
			updates: make([]*privatev1.Subnet, 0),
			signals: make([]string, 0),
		}
		listener = bufconn.Listen(1024 * 1024)
		grpcServer = grpc.NewServer()
		privatev1.RegisterSubnetsServer(grpcServer, mockServer)

		go func() {
			_ = grpcServer.Serve(listener)
		}()

		// Create gRPC client connection
		conn, err := grpc.NewClient("passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
				return listener.Dial()
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		Expect(err).NotTo(HaveOccurred())

		// Create reconciler
		reconciler = NewSubnetFeedbackReconciler(k8sClient, conn, subnetNamespace)
	})

	AfterEach(func() {
		if grpcServer != nil {
			grpcServer.Stop()
		}
		if listener != nil {
			_ = listener.Close()
		}
	})

	Context("when reconciling a Subnet CR", func() {
		It("should sync Phase=Ready to database state=READY", func() {
			// Create subnet in mock database
			ipv4Cidr := testIPv4CIDR
			subnet := &privatev1.Subnet{
				Id: subnetID,
				Metadata: &privatev1.Metadata{
					Name: subnetName,
				},
				Spec: &privatev1.SubnetSpec{
					VirtualNetwork: "vnet-123",
					Ipv4Cidr:       &ipv4Cidr,
				},
				Status: &privatev1.SubnetStatus{
					State: privatev1.SubnetState_SUBNET_STATE_PENDING,
				},
			}
			mockServer.addSubnet(subnet)

			// Create Subnet CR with Phase=Ready
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      subnetName,
					Namespace: subnetNamespace,
					Labels: map[string]string{
						osacSubnetIDLabel: subnetID,
					},
				},
				Spec: v1alpha1.SubnetSpec{
					VirtualNetwork: "vnet-123",
					IPv4CIDR:       "10.0.1.0/24",
				},
				Status: v1alpha1.SubnetStatus{
					Phase: v1alpha1.SubnetPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Reconcile
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnetName,
					Namespace: subnetNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert gRPC Update called with state=READY
			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_READY))

			// Assert finalizer was added
			updated := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: subnetName, Namespace: subnetNamespace}, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, osacSubnetFeedbackFinalizer)).To(BeTrue())
		})

		It("should sync Phase=Progressing to database state=PENDING", func() {
			ipv4Cidr := testIPv4CIDR
			subnet := &privatev1.Subnet{
				Id: subnetID,
				Metadata: &privatev1.Metadata{
					Name: subnetName,
				},
				Spec: &privatev1.SubnetSpec{
					VirtualNetwork: "vnet-123",
					Ipv4Cidr:       &ipv4Cidr,
				},
				Status: &privatev1.SubnetStatus{
					State: privatev1.SubnetState_SUBNET_STATE_READY,
				},
			}
			mockServer.addSubnet(subnet)

			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      subnetName,
					Namespace: subnetNamespace,
					Labels: map[string]string{
						osacSubnetIDLabel: subnetID,
					},
				},
				Spec: v1alpha1.SubnetSpec{
					VirtualNetwork: "vnet-123",
					IPv4CIDR:       "10.0.1.0/24",
				},
				Status: v1alpha1.SubnetStatus{
					Phase: v1alpha1.SubnetPhaseProgressing,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnetName,
					Namespace: subnetNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_PENDING))
		})

		It("should sync Phase=Failed to database state=FAILED", func() {
			ipv4Cidr := testIPv4CIDR
			subnet := &privatev1.Subnet{
				Id: subnetID,
				Metadata: &privatev1.Metadata{
					Name: subnetName,
				},
				Spec: &privatev1.SubnetSpec{
					VirtualNetwork: "vnet-123",
					Ipv4Cidr:       &ipv4Cidr,
				},
				Status: &privatev1.SubnetStatus{
					State: privatev1.SubnetState_SUBNET_STATE_PENDING,
				},
			}
			mockServer.addSubnet(subnet)

			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      subnetName,
					Namespace: subnetNamespace,
					Labels: map[string]string{
						osacSubnetIDLabel: subnetID,
					},
				},
				Spec: v1alpha1.SubnetSpec{
					VirtualNetwork: "vnet-123",
					IPv4CIDR:       "10.0.1.0/24",
				},
				Status: v1alpha1.SubnetStatus{
					Phase: v1alpha1.SubnetPhaseFailed,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnetName,
					Namespace: subnetNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_FAILED))
		})

		It("should sync backendNetworkId to database message field", func() {
			ipv4Cidr := testIPv4CIDR
			subnet := &privatev1.Subnet{
				Id: subnetID,
				Metadata: &privatev1.Metadata{
					Name: subnetName,
				},
				Spec: &privatev1.SubnetSpec{
					VirtualNetwork: "vnet-123",
					Ipv4Cidr:       &ipv4Cidr,
				},
				Status: &privatev1.SubnetStatus{
					State: privatev1.SubnetState_SUBNET_STATE_PENDING,
				},
			}
			mockServer.addSubnet(subnet)

			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      subnetName,
					Namespace: subnetNamespace,
					Labels: map[string]string{
						osacSubnetIDLabel: subnetID,
					},
				},
				Spec: v1alpha1.SubnetSpec{
					VirtualNetwork: "vnet-123",
					IPv4CIDR:       "10.0.1.0/24",
				},
				Status: v1alpha1.SubnetStatus{
					Phase:            v1alpha1.SubnetPhaseReady,
					BackendNetworkID: "backend-net-456",
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnetName,
					Namespace: subnetNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetMessage()).To(Equal("backend-net-456"))
		})

		It("should skip CRs without subnet-uuid label", func() {
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      subnetName,
					Namespace: subnetNamespace,
					Labels:    map[string]string{},
				},
				Spec: v1alpha1.SubnetSpec{
					VirtualNetwork: "vnet-123",
					IPv4CIDR:       "10.0.1.0/24",
				},
				Status: v1alpha1.SubnetStatus{
					Phase: v1alpha1.SubnetPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnetName,
					Namespace: subnetNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert no Update RPC was called
			Expect(mockServer.updates).To(BeEmpty())
		})

		It("should sync Phase=Deleting to database state=DELETING during deletion", func() {
			ipv4Cidr := testIPv4CIDR
			subnet := &privatev1.Subnet{
				Id: subnetID,
				Metadata: &privatev1.Metadata{
					Name: subnetName,
				},
				Spec: &privatev1.SubnetSpec{
					VirtualNetwork: "vnet-123",
					Ipv4Cidr:       &ipv4Cidr,
				},
				Status: &privatev1.SubnetStatus{
					State: privatev1.SubnetState_SUBNET_STATE_READY,
				},
			}
			mockServer.addSubnet(subnet)

			// Create CR with finalizer first (simulating a previously reconciled CR)
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      subnetName,
					Namespace: subnetNamespace,
					Labels: map[string]string{
						osacSubnetIDLabel: subnetID,
					},
					Finalizers: []string{osacSubnetFeedbackFinalizer, osacSubnetFinalizer},
				},
				Spec: v1alpha1.SubnetSpec{
					VirtualNetwork: "vnet-123",
					IPv4CIDR:       "10.0.1.0/24",
				},
				Status: v1alpha1.SubnetStatus{
					Phase: v1alpha1.SubnetPhaseDeleting,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Mark for deletion
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnetName,
					Namespace: subnetNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert Update RPC was called with DELETING state
			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_DELETING))

			// Feedback finalizer should still be present (other finalizers remain)
			updated := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: subnetName, Namespace: subnetNamespace}, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, osacSubnetFeedbackFinalizer)).To(BeTrue())
		})

		It("should sync Phase=Failed during deletion to database state=DELETE_FAILED", func() {
			ipv4Cidr := testIPv4CIDR
			subnet := &privatev1.Subnet{
				Id: subnetID,
				Metadata: &privatev1.Metadata{
					Name: subnetName,
				},
				Spec: &privatev1.SubnetSpec{
					VirtualNetwork: "vnet-123",
					Ipv4Cidr:       &ipv4Cidr,
				},
				Status: &privatev1.SubnetStatus{
					State: privatev1.SubnetState_SUBNET_STATE_READY,
				},
			}
			mockServer.addSubnet(subnet)

			// Create CR with finalizers (simulating a previously reconciled CR)
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      subnetName,
					Namespace: subnetNamespace,
					Labels: map[string]string{
						osacSubnetIDLabel: subnetID,
					},
					Finalizers: []string{osacSubnetFeedbackFinalizer, osacSubnetFinalizer},
				},
				Spec: v1alpha1.SubnetSpec{
					VirtualNetwork: "vnet-123",
					IPv4CIDR:       "10.0.1.0/24",
				},
				Status: v1alpha1.SubnetStatus{
					Phase: v1alpha1.SubnetPhaseFailed,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Mark for deletion
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnetName,
					Namespace: subnetNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert Update RPC was called with DELETE_FAILED state
			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_DELETE_FAILED))
		})

		It("should remove feedback finalizer and signal when it is the last finalizer", func() {
			ipv4Cidr := testIPv4CIDR
			subnet := &privatev1.Subnet{
				Id: subnetID,
				Metadata: &privatev1.Metadata{
					Name: subnetName,
				},
				Spec: &privatev1.SubnetSpec{
					VirtualNetwork: "vnet-123",
					Ipv4Cidr:       &ipv4Cidr,
				},
				Status: &privatev1.SubnetStatus{
					State: privatev1.SubnetState_SUBNET_STATE_READY,
				},
			}
			mockServer.addSubnet(subnet)

			// Create CR with only feedback finalizer (simulating other finalizers already removed)
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      subnetName,
					Namespace: subnetNamespace,
					Labels: map[string]string{
						osacSubnetIDLabel: subnetID,
					},
					Finalizers: []string{osacSubnetFeedbackFinalizer},
				},
				Spec: v1alpha1.SubnetSpec{
					VirtualNetwork: "vnet-123",
					IPv4CIDR:       "10.0.1.0/24",
				},
				Status: v1alpha1.SubnetStatus{
					Phase: v1alpha1.SubnetPhaseDeleting,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Mark for deletion
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnetName,
					Namespace: subnetNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert Update RPC was called with DELETING state
			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_DELETING))

			// Assert Signal RPC was called
			Expect(mockServer.signals).To(HaveLen(1))
			Expect(mockServer.signals[0]).To(Equal(subnetID))

			// Assert finalizer was removed (CR should be gone since it was the last finalizer)
			updated := &v1alpha1.Subnet{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: subnetName, Namespace: subnetNamespace}, updated)
			Expect(err).To(HaveOccurred()) // CR should be garbage collected
		})

		It("should remove feedback finalizer when subnet record is NotFound during deletion", func() {
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      subnetName,
					Namespace: subnetNamespace,
					Labels: map[string]string{
						osacSubnetIDLabel: subnetID,
					},
					Finalizers: []string{osacSubnetFeedbackFinalizer},
				},
				Spec: v1alpha1.SubnetSpec{
					VirtualNetwork: "vnet-123",
					IPv4CIDR:       "10.0.1.0/24",
				},
				Status: v1alpha1.SubnetStatus{
					Phase: v1alpha1.SubnetPhaseDeleting,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Mark for deletion
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnetName,
					Namespace: subnetNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// No gRPC Update or Signal should have been called
			Expect(mockServer.updates).To(BeEmpty())
			Expect(mockServer.signals).To(BeEmpty())

			// CR should be gone (finalizer removed, it was the last one)
			updated := &v1alpha1.Subnet{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: subnetName, Namespace: subnetNamespace}, updated)
			Expect(err).To(HaveOccurred())
		})

		It("should return error when subnet record is NotFound but CR is not being deleted", func() {
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      subnetName,
					Namespace: subnetNamespace,
					Labels: map[string]string{
						osacSubnetIDLabel: subnetID,
					},
					Finalizers: []string{osacSubnetFeedbackFinalizer},
				},
				Spec: v1alpha1.SubnetSpec{
					VirtualNetwork: "vnet-123",
					IPv4CIDR:       "10.0.1.0/24",
				},
				Status: v1alpha1.SubnetStatus{
					Phase: v1alpha1.SubnetPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnetName,
					Namespace: subnetNamespace,
				},
			})
			Expect(err).To(HaveOccurred())
		})

		It("should not update if status unchanged", func() {
			ipv4Cidr := testIPv4CIDR
			subnet := &privatev1.Subnet{
				Id: subnetID,
				Metadata: &privatev1.Metadata{
					Name: subnetName,
				},
				Spec: &privatev1.SubnetSpec{
					VirtualNetwork: "vnet-123",
					Ipv4Cidr:       &ipv4Cidr,
				},
				Status: &privatev1.SubnetStatus{
					State: privatev1.SubnetState_SUBNET_STATE_READY,
				},
			}
			mockServer.addSubnet(subnet)

			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      subnetName,
					Namespace: subnetNamespace,
					Labels: map[string]string{
						osacSubnetIDLabel: subnetID,
					},
					Finalizers: []string{osacSubnetFeedbackFinalizer},
				},
				Spec: v1alpha1.SubnetSpec{
					VirtualNetwork: "vnet-123",
					IPv4CIDR:       "10.0.1.0/24",
				},
				Status: v1alpha1.SubnetStatus{
					Phase: v1alpha1.SubnetPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnetName,
					Namespace: subnetNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert no Update RPC was called (state already READY, finalizer pre-seeded)
			Expect(mockServer.updates).To(BeEmpty())
		})
	})
})

// mockSubnetsServer implements privatev1.SubnetsServer for testing
type mockSubnetsServer struct {
	privatev1.UnimplementedSubnetsServer
	mu      sync.Mutex
	subnets map[string]*privatev1.Subnet
	updates []*privatev1.Subnet
	signals []string
}

func (m *mockSubnetsServer) addSubnet(subnet *privatev1.Subnet) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subnets[subnet.GetId()] = subnet
}

func (m *mockSubnetsServer) Get(ctx context.Context, req *privatev1.SubnetsGetRequest) (*privatev1.SubnetsGetResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	subnet, ok := m.subnets[req.GetId()]
	if !ok {
		return nil, grpcstatus.Errorf(codes.NotFound, "object with identifier '%s' not found", req.GetId())
	}

	return &privatev1.SubnetsGetResponse{
		Object: subnet,
	}, nil
}

func (m *mockSubnetsServer) Update(ctx context.Context, req *privatev1.SubnetsUpdateRequest) (*privatev1.SubnetsUpdateResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	subnet := req.GetObject()
	m.subnets[subnet.GetId()] = subnet
	m.updates = append(m.updates, subnet)

	return &privatev1.SubnetsUpdateResponse{
		Object: subnet,
	}, nil
}

func (m *mockSubnetsServer) Signal(ctx context.Context, req *privatev1.SubnetsSignalRequest) (*privatev1.SubnetsSignalResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.signals = append(m.signals, req.GetId())

	return &privatev1.SubnetsSignalResponse{}, nil
}
