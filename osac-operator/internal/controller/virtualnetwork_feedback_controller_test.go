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
	"fmt"
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

var _ = Describe("VirtualNetworkFeedbackController", func() {
	const (
		vnetName      = "test-vnet"
		vnetNamespace = "test-namespace"
		vnetID        = "vnet-123"
		testIPv4CIDR  = "10.0.0.0/16"
	)

	var (
		ctx        context.Context
		k8sClient  client.Client
		mockServer *mockVirtualNetworksServer
		reconciler *VirtualNetworkFeedbackReconciler
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
		mockServer = &mockVirtualNetworksServer{
			virtualNetworks: make(map[string]*privatev1.VirtualNetwork),
			updates:         make([]*privatev1.VirtualNetwork, 0),
			signals:         make([]string, 0),
		}
		listener = bufconn.Listen(1024 * 1024)
		grpcServer = grpc.NewServer()
		privatev1.RegisterVirtualNetworksServer(grpcServer, mockServer)

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
		reconciler = NewVirtualNetworkFeedbackReconciler(k8sClient, conn, vnetNamespace)
	})

	AfterEach(func() {
		if grpcServer != nil {
			grpcServer.Stop()
		}
		if listener != nil {
			_ = listener.Close()
		}
	})

	Context("when reconciling a VirtualNetwork CR", func() {
		It("should sync Phase=Ready to database state=READY", func() {
			// Create virtual network in mock database
			ipv4Cidr := testIPv4CIDR
			vnet := &privatev1.VirtualNetwork{
				Id: vnetID,
				Metadata: &privatev1.Metadata{
					Name: vnetName,
				},
				Spec: &privatev1.VirtualNetworkSpec{
					NetworkClass: "udn-net",
					Ipv4Cidr:     &ipv4Cidr,
				},
				Status: &privatev1.VirtualNetworkStatus{
					State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING,
				},
			}
			mockServer.addVirtualNetwork(vnet)

			// Create VirtualNetwork CR with Phase=Ready
			cr := &v1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      vnetName,
					Namespace: vnetNamespace,
					Labels: map[string]string{
						osacVirtualNetworkIDLabel: vnetID,
					},
				},
				Spec: v1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					NetworkClass: "udn-net",
					IPv4CIDR:     "10.0.0.0/16",
				},
				Status: v1alpha1.VirtualNetworkStatus{
					Phase: v1alpha1.VirtualNetworkPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Reconcile
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      vnetName,
					Namespace: vnetNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert gRPC Update called with state=READY
			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY))

			// Assert finalizer was added
			updated := &v1alpha1.VirtualNetwork{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: vnetName, Namespace: vnetNamespace}, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, osacVirtualNetworkFeedbackFinalizer)).To(BeTrue())
		})

		It("should sync Phase=Progressing to database state=PENDING", func() {
			ipv4Cidr := testIPv4CIDR
			vnet := &privatev1.VirtualNetwork{
				Id: vnetID,
				Metadata: &privatev1.Metadata{
					Name: vnetName,
				},
				Spec: &privatev1.VirtualNetworkSpec{
					NetworkClass: "udn-net",
					Ipv4Cidr:     &ipv4Cidr,
				},
				Status: &privatev1.VirtualNetworkStatus{
					State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
				},
			}
			mockServer.addVirtualNetwork(vnet)

			cr := &v1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      vnetName,
					Namespace: vnetNamespace,
					Labels: map[string]string{
						osacVirtualNetworkIDLabel: vnetID,
					},
				},
				Spec: v1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					NetworkClass: "udn-net",
					IPv4CIDR:     "10.0.0.0/16",
				},
				Status: v1alpha1.VirtualNetworkStatus{
					Phase: v1alpha1.VirtualNetworkPhaseProgressing,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      vnetName,
					Namespace: vnetNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))
		})

		It("should sync Phase=Failed to database state=FAILED", func() {
			ipv4Cidr := testIPv4CIDR
			vnet := &privatev1.VirtualNetwork{
				Id: vnetID,
				Metadata: &privatev1.Metadata{
					Name: vnetName,
				},
				Spec: &privatev1.VirtualNetworkSpec{
					NetworkClass: "udn-net",
					Ipv4Cidr:     &ipv4Cidr,
				},
				Status: &privatev1.VirtualNetworkStatus{
					State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING,
				},
			}
			mockServer.addVirtualNetwork(vnet)

			cr := &v1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      vnetName,
					Namespace: vnetNamespace,
					Labels: map[string]string{
						osacVirtualNetworkIDLabel: vnetID,
					},
				},
				Spec: v1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					NetworkClass: "udn-net",
					IPv4CIDR:     "10.0.0.0/16",
				},
				Status: v1alpha1.VirtualNetworkStatus{
					Phase: v1alpha1.VirtualNetworkPhaseFailed,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      vnetName,
					Namespace: vnetNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_FAILED))
		})

		It("should skip CRs without virtualnetwork-uuid label", func() {
			cr := &v1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      vnetName,
					Namespace: vnetNamespace,
					Labels:    map[string]string{},
				},
				Spec: v1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					NetworkClass: "udn-net",
					IPv4CIDR:     "10.0.0.0/16",
				},
				Status: v1alpha1.VirtualNetworkStatus{
					Phase: v1alpha1.VirtualNetworkPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      vnetName,
					Namespace: vnetNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert no Update RPC was called
			Expect(mockServer.updates).To(BeEmpty())
		})

		It("should sync Phase=Deleting during deletion and keep finalizer when others present", func() {
			ipv4Cidr := testIPv4CIDR
			vnet := &privatev1.VirtualNetwork{
				Id: vnetID,
				Metadata: &privatev1.Metadata{
					Name: vnetName,
				},
				Spec: &privatev1.VirtualNetworkSpec{
					NetworkClass: "udn-net",
					Ipv4Cidr:     &ipv4Cidr,
				},
				Status: &privatev1.VirtualNetworkStatus{
					State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
				},
			}
			mockServer.addVirtualNetwork(vnet)

			// Create CR with finalizer first (simulating a previously reconciled CR)
			cr := &v1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      vnetName,
					Namespace: vnetNamespace,
					Labels: map[string]string{
						osacVirtualNetworkIDLabel: vnetID,
					},
					Finalizers: []string{osacVirtualNetworkFeedbackFinalizer, osacVirtualNetworkFinalizer},
				},
				Spec: v1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					NetworkClass: "udn-net",
					IPv4CIDR:     "10.0.0.0/16",
				},
				Status: v1alpha1.VirtualNetworkStatus{
					Phase: v1alpha1.VirtualNetworkPhaseDeleting,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Mark for deletion
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      vnetName,
					Namespace: vnetNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert Update RPC was called with PENDING state (VN has no DELETING state)
			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))

			// Feedback finalizer should still be present (other finalizers remain)
			updated := &v1alpha1.VirtualNetwork{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: vnetName, Namespace: vnetNamespace}, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, osacVirtualNetworkFeedbackFinalizer)).To(BeTrue())
		})

		It("should sync Phase=Failed during deletion to database state=FAILED", func() {
			ipv4Cidr := testIPv4CIDR
			vnet := &privatev1.VirtualNetwork{
				Id: vnetID,
				Metadata: &privatev1.Metadata{
					Name: vnetName,
				},
				Spec: &privatev1.VirtualNetworkSpec{
					NetworkClass: "udn-net",
					Ipv4Cidr:     &ipv4Cidr,
				},
				Status: &privatev1.VirtualNetworkStatus{
					State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
				},
			}
			mockServer.addVirtualNetwork(vnet)

			cr := &v1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      vnetName,
					Namespace: vnetNamespace,
					Labels: map[string]string{
						osacVirtualNetworkIDLabel: vnetID,
					},
					Finalizers: []string{osacVirtualNetworkFeedbackFinalizer, osacVirtualNetworkFinalizer},
				},
				Spec: v1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					NetworkClass: "udn-net",
					IPv4CIDR:     "10.0.0.0/16",
				},
				Status: v1alpha1.VirtualNetworkStatus{
					Phase: v1alpha1.VirtualNetworkPhaseFailed,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      vnetName,
					Namespace: vnetNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_FAILED))
		})

		It("should still remove finalizer when signal fails", func() {
			ipv4Cidr := testIPv4CIDR
			vnet := &privatev1.VirtualNetwork{
				Id: vnetID,
				Metadata: &privatev1.Metadata{
					Name: vnetName,
				},
				Spec: &privatev1.VirtualNetworkSpec{
					NetworkClass: "udn-net",
					Ipv4Cidr:     &ipv4Cidr,
				},
				Status: &privatev1.VirtualNetworkStatus{
					State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
				},
			}
			mockServer.addVirtualNetwork(vnet)
			mockServer.signalErr = fmt.Errorf("signal unavailable")

			cr := &v1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      vnetName,
					Namespace: vnetNamespace,
					Labels: map[string]string{
						osacVirtualNetworkIDLabel: vnetID,
					},
					Finalizers: []string{osacVirtualNetworkFeedbackFinalizer},
				},
				Spec: v1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					NetworkClass: "udn-net",
					IPv4CIDR:     "10.0.0.0/16",
				},
				Status: v1alpha1.VirtualNetworkStatus{
					Phase: v1alpha1.VirtualNetworkPhaseDeleting,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      vnetName,
					Namespace: vnetNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			updated := &v1alpha1.VirtualNetwork{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: vnetName, Namespace: vnetNamespace}, updated)
			Expect(err).To(HaveOccurred())
		})

		It("should remove feedback finalizer and signal when it is the last finalizer", func() {
			ipv4Cidr := testIPv4CIDR
			vnet := &privatev1.VirtualNetwork{
				Id: vnetID,
				Metadata: &privatev1.Metadata{
					Name: vnetName,
				},
				Spec: &privatev1.VirtualNetworkSpec{
					NetworkClass: "udn-net",
					Ipv4Cidr:     &ipv4Cidr,
				},
				Status: &privatev1.VirtualNetworkStatus{
					State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
				},
			}
			mockServer.addVirtualNetwork(vnet)

			// Create CR with only feedback finalizer (simulating other finalizers already removed)
			cr := &v1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      vnetName,
					Namespace: vnetNamespace,
					Labels: map[string]string{
						osacVirtualNetworkIDLabel: vnetID,
					},
					Finalizers: []string{osacVirtualNetworkFeedbackFinalizer},
				},
				Spec: v1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					NetworkClass: "udn-net",
					IPv4CIDR:     "10.0.0.0/16",
				},
				Status: v1alpha1.VirtualNetworkStatus{
					Phase: v1alpha1.VirtualNetworkPhaseDeleting,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Mark for deletion
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      vnetName,
					Namespace: vnetNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert Update RPC was called with PENDING state (VN has no DELETING state)
			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))

			// Assert Signal RPC was called
			Expect(mockServer.signals).To(HaveLen(1))
			Expect(mockServer.signals[0]).To(Equal(vnetID))

			// Assert finalizer was removed (CR should be gone since it was the last finalizer)
			updated := &v1alpha1.VirtualNetwork{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: vnetName, Namespace: vnetNamespace}, updated)
			Expect(err).To(HaveOccurred()) // CR should be garbage collected
		})

		It("should remove feedback finalizer when VN record is NotFound during deletion", func() {
			cr := &v1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      vnetName,
					Namespace: vnetNamespace,
					Labels: map[string]string{
						osacVirtualNetworkIDLabel: vnetID,
					},
					Finalizers: []string{osacVirtualNetworkFeedbackFinalizer},
				},
				Spec: v1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					NetworkClass: "udn-net",
					IPv4CIDR:     "10.0.0.0/16",
				},
				Status: v1alpha1.VirtualNetworkStatus{
					Phase: v1alpha1.VirtualNetworkPhaseDeleting,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Mark for deletion
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      vnetName,
					Namespace: vnetNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// No gRPC Update or Signal should have been called
			Expect(mockServer.updates).To(BeEmpty())
			Expect(mockServer.signals).To(BeEmpty())

			// CR should be gone (finalizer removed, it was the last one)
			updated := &v1alpha1.VirtualNetwork{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: vnetName, Namespace: vnetNamespace}, updated)
			Expect(err).To(HaveOccurred())
		})

		It("should return error when VN record is NotFound but CR is not being deleted", func() {
			cr := &v1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      vnetName,
					Namespace: vnetNamespace,
					Labels: map[string]string{
						osacVirtualNetworkIDLabel: vnetID,
					},
					Finalizers: []string{osacVirtualNetworkFeedbackFinalizer},
				},
				Spec: v1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					NetworkClass: "udn-net",
					IPv4CIDR:     "10.0.0.0/16",
				},
				Status: v1alpha1.VirtualNetworkStatus{
					Phase: v1alpha1.VirtualNetworkPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      vnetName,
					Namespace: vnetNamespace,
				},
			})
			Expect(err).To(HaveOccurred())
		})

		It("should not update if status unchanged", func() {
			ipv4Cidr := testIPv4CIDR
			vnet := &privatev1.VirtualNetwork{
				Id: vnetID,
				Metadata: &privatev1.Metadata{
					Name: vnetName,
				},
				Spec: &privatev1.VirtualNetworkSpec{
					NetworkClass: "udn-net",
					Ipv4Cidr:     &ipv4Cidr,
				},
				Status: &privatev1.VirtualNetworkStatus{
					State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
				},
			}
			mockServer.addVirtualNetwork(vnet)

			cr := &v1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      vnetName,
					Namespace: vnetNamespace,
					Labels: map[string]string{
						osacVirtualNetworkIDLabel: vnetID,
					},
					Finalizers: []string{osacVirtualNetworkFeedbackFinalizer},
				},
				Spec: v1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					NetworkClass: "udn-net",
					IPv4CIDR:     "10.0.0.0/16",
				},
				Status: v1alpha1.VirtualNetworkStatus{
					Phase: v1alpha1.VirtualNetworkPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      vnetName,
					Namespace: vnetNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert no Update RPC was called (state already READY, finalizer pre-seeded)
			Expect(mockServer.updates).To(BeEmpty())
		})
	})
})

// mockVirtualNetworksServer implements privatev1.VirtualNetworksServer for testing
type mockVirtualNetworksServer struct {
	privatev1.UnimplementedVirtualNetworksServer
	mu              sync.Mutex
	virtualNetworks map[string]*privatev1.VirtualNetwork
	updates         []*privatev1.VirtualNetwork
	signals         []string
	signalErr       error
}

func (m *mockVirtualNetworksServer) addVirtualNetwork(vnet *privatev1.VirtualNetwork) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.virtualNetworks[vnet.GetId()] = vnet
}

func (m *mockVirtualNetworksServer) Get(ctx context.Context, req *privatev1.VirtualNetworksGetRequest) (*privatev1.VirtualNetworksGetResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	vnet, ok := m.virtualNetworks[req.GetId()]
	if !ok {
		return nil, grpcstatus.Errorf(codes.NotFound, "object with identifier '%s' not found", req.GetId())
	}

	return &privatev1.VirtualNetworksGetResponse{
		Object: vnet,
	}, nil
}

func (m *mockVirtualNetworksServer) Update(ctx context.Context, req *privatev1.VirtualNetworksUpdateRequest) (*privatev1.VirtualNetworksUpdateResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	vnet := req.GetObject()
	m.virtualNetworks[vnet.GetId()] = vnet
	m.updates = append(m.updates, vnet)

	return &privatev1.VirtualNetworksUpdateResponse{
		Object: vnet,
	}, nil
}

func (m *mockVirtualNetworksServer) Signal(ctx context.Context, req *privatev1.VirtualNetworksSignalRequest) (*privatev1.VirtualNetworksSignalResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.signals = append(m.signals, req.GetId())

	if m.signalErr != nil {
		return nil, m.signalErr
	}
	return &privatev1.VirtualNetworksSignalResponse{}, nil
}
