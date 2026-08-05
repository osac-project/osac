/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controller

import (
	"context"
	"net"
	"sync"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
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

var _ = Describe("ExternalIPPoolFeedbackController", func() {
	const (
		poolName      = "test-pool"
		poolNamespace = "test-namespace"
		poolID        = "pool-123"
	)

	var (
		ctx        context.Context
		k8sClient  client.Client
		mockServer *mockExternalIPPoolsServer
		reconciler *ExternalIPPoolFeedbackReconciler
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
		mockServer = &mockExternalIPPoolsServer{
			publicIPPools: make(map[string]*privatev1.ExternalIPPool),
			updates:       make([]*privatev1.ExternalIPPool, 0),
			signals:       make([]string, 0),
		}
		listener = bufconn.Listen(1024 * 1024)
		grpcServer = grpc.NewServer()
		privatev1.RegisterExternalIPPoolsServer(grpcServer, mockServer)

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
		reconciler = NewExternalIPPoolFeedbackReconciler(k8sClient, conn, poolNamespace)
	})

	AfterEach(func() {
		if grpcServer != nil {
			grpcServer.Stop()
		}
		if listener != nil {
			_ = listener.Close()
		}
	})

	Context("when reconciling a ExternalIPPool CR", func() {
		It("should sync Phase=Ready to database state=READY", func() {
			pool := &privatev1.ExternalIPPool{
				Id: poolID,
				Metadata: &privatev1.Metadata{
					Name: poolName,
				},
				Spec: &privatev1.ExternalIPPoolSpec{},
				Status: &privatev1.ExternalIPPoolStatus{
					State: privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_PENDING,
				},
			}
			mockServer.addExternalIPPool(pool)

			cr := &v1alpha1.ExternalIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: poolNamespace,
					Labels: map[string]string{
						osacExternalIPPoolIDLabel: poolID,
					},
				},
				Spec: v1alpha1.ExternalIPPoolSpec{
					CIDRs:    []string{"10.0.0.0/24"},
					IPFamily: "IPv4",
				},
				Status: v1alpha1.ExternalIPPoolStatus{
					Phase: v1alpha1.ExternalIPPoolPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      poolName,
					Namespace: poolNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert gRPC Update called with state=READY
			Expect(mockServer.updates).To(HaveLen(1))
			updated := mockServer.updates[0]
			Expect(updated.GetStatus().GetState()).To(Equal(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY))

			// Assert finalizer was added
			fetched := &v1alpha1.ExternalIPPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: poolName, Namespace: poolNamespace}, fetched)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(fetched, osacExternalIPPoolFeedbackFinalizer)).To(BeTrue())
		})

		It("should preserve existing capacity when syncing phase", func() {
			pool := &privatev1.ExternalIPPool{
				Id: poolID,
				Metadata: &privatev1.Metadata{
					Name: poolName,
				},
				Spec: &privatev1.ExternalIPPoolSpec{},
				Status: &privatev1.ExternalIPPoolStatus{
					State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_PENDING,
					Total:     254,
					Allocated: 10,
					Available: 244,
				},
			}
			mockServer.addExternalIPPool(pool)

			cr := &v1alpha1.ExternalIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: poolNamespace,
					Labels: map[string]string{
						osacExternalIPPoolIDLabel: poolID,
					},
				},
				Spec: v1alpha1.ExternalIPPoolSpec{
					CIDRs:    []string{"10.0.0.0/24"},
					IPFamily: "IPv4",
				},
				Status: v1alpha1.ExternalIPPoolStatus{
					Phase: v1alpha1.ExternalIPPoolPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      poolName,
					Namespace: poolNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			updated := mockServer.updates[0]
			Expect(updated.GetStatus().GetState()).To(Equal(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY))
			Expect(updated.GetStatus().GetTotal()).To(Equal(int64(254)))
			Expect(updated.GetStatus().GetAllocated()).To(Equal(int64(10)))
			Expect(updated.GetStatus().GetAvailable()).To(Equal(int64(244)))
		})

		It("should sync Phase=Progressing to database state=PENDING", func() {
			pool := &privatev1.ExternalIPPool{
				Id: poolID,
				Metadata: &privatev1.Metadata{
					Name: poolName,
				},
				Spec: &privatev1.ExternalIPPoolSpec{},
				Status: &privatev1.ExternalIPPoolStatus{
					State: privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
				},
			}
			mockServer.addExternalIPPool(pool)

			cr := &v1alpha1.ExternalIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: poolNamespace,
					Labels: map[string]string{
						osacExternalIPPoolIDLabel: poolID,
					},
				},
				Spec: v1alpha1.ExternalIPPoolSpec{
					CIDRs:    []string{"10.0.0.0/24"},
					IPFamily: "IPv4",
				},
				Status: v1alpha1.ExternalIPPoolStatus{
					Phase: v1alpha1.ExternalIPPoolPhaseProgressing,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      poolName,
					Namespace: poolNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_PENDING))
		})

		It("should sync Phase=Failed to database state=FAILED", func() {
			pool := &privatev1.ExternalIPPool{
				Id: poolID,
				Metadata: &privatev1.Metadata{
					Name: poolName,
				},
				Spec: &privatev1.ExternalIPPoolSpec{},
				Status: &privatev1.ExternalIPPoolStatus{
					State: privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_PENDING,
				},
			}
			mockServer.addExternalIPPool(pool)

			cr := &v1alpha1.ExternalIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: poolNamespace,
					Labels: map[string]string{
						osacExternalIPPoolIDLabel: poolID,
					},
				},
				Spec: v1alpha1.ExternalIPPoolSpec{
					CIDRs:    []string{"10.0.0.0/24"},
					IPFamily: "IPv4",
				},
				Status: v1alpha1.ExternalIPPoolStatus{
					Phase: v1alpha1.ExternalIPPoolPhaseFailed,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      poolName,
					Namespace: poolNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_FAILED))
		})

		It("should skip CRs without externalippool-uuid label", func() {
			cr := &v1alpha1.ExternalIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: poolNamespace,
					Labels:    map[string]string{},
				},
				Spec: v1alpha1.ExternalIPPoolSpec{
					CIDRs:    []string{"10.0.0.0/24"},
					IPFamily: "IPv4",
				},
				Status: v1alpha1.ExternalIPPoolStatus{
					Phase: v1alpha1.ExternalIPPoolPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      poolName,
					Namespace: poolNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert no Update RPC was called
			Expect(mockServer.updates).To(BeEmpty())
		})

		It("should sync Phase=Deleting to database state=DELETING during deletion", func() {
			pool := &privatev1.ExternalIPPool{
				Id: poolID,
				Metadata: &privatev1.Metadata{
					Name: poolName,
				},
				Spec: &privatev1.ExternalIPPoolSpec{},
				Status: &privatev1.ExternalIPPoolStatus{
					State: privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
				},
			}
			mockServer.addExternalIPPool(pool)

			// Create CR with finalizers (simulating a previously reconciled CR)
			cr := &v1alpha1.ExternalIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: poolNamespace,
					Labels: map[string]string{
						osacExternalIPPoolIDLabel: poolID,
					},
					Finalizers: []string{osacExternalIPPoolFeedbackFinalizer, osacExternalIPPoolFinalizer},
				},
				Spec: v1alpha1.ExternalIPPoolSpec{
					CIDRs:    []string{"10.0.0.0/24"},
					IPFamily: "IPv4",
				},
				Status: v1alpha1.ExternalIPPoolStatus{
					Phase: v1alpha1.ExternalIPPoolPhaseDeleting,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Mark for deletion
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      poolName,
					Namespace: poolNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert Update RPC was called with DELETING state
			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_DELETING))

			// Feedback finalizer should still be present (other finalizers remain)
			fetched := &v1alpha1.ExternalIPPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: poolName, Namespace: poolNamespace}, fetched)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(fetched, osacExternalIPPoolFeedbackFinalizer)).To(BeTrue())
		})

		It("should sync Phase=Failed during deletion to database state=DELETE_FAILED", func() {
			pool := &privatev1.ExternalIPPool{
				Id: poolID,
				Metadata: &privatev1.Metadata{
					Name: poolName,
				},
				Spec: &privatev1.ExternalIPPoolSpec{},
				Status: &privatev1.ExternalIPPoolStatus{
					State: privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
				},
			}
			mockServer.addExternalIPPool(pool)

			// Create CR with finalizers (simulating a previously reconciled CR)
			cr := &v1alpha1.ExternalIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: poolNamespace,
					Labels: map[string]string{
						osacExternalIPPoolIDLabel: poolID,
					},
					Finalizers: []string{osacExternalIPPoolFeedbackFinalizer, osacExternalIPPoolFinalizer},
				},
				Spec: v1alpha1.ExternalIPPoolSpec{
					CIDRs:    []string{"10.0.0.0/24"},
					IPFamily: "IPv4",
				},
				Status: v1alpha1.ExternalIPPoolStatus{
					Phase: v1alpha1.ExternalIPPoolPhaseFailed,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Mark for deletion
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      poolName,
					Namespace: poolNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert Update RPC was called with DELETE_FAILED state
			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_DELETE_FAILED))
		})

		It("should remove feedback finalizer and signal when it is the last finalizer", func() {
			pool := &privatev1.ExternalIPPool{
				Id: poolID,
				Metadata: &privatev1.Metadata{
					Name: poolName,
				},
				Spec: &privatev1.ExternalIPPoolSpec{},
				Status: &privatev1.ExternalIPPoolStatus{
					State: privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
				},
			}
			mockServer.addExternalIPPool(pool)

			// Create CR with only feedback finalizer (simulating other finalizers already removed)
			cr := &v1alpha1.ExternalIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: poolNamespace,
					Labels: map[string]string{
						osacExternalIPPoolIDLabel: poolID,
					},
					Finalizers: []string{osacExternalIPPoolFeedbackFinalizer},
				},
				Spec: v1alpha1.ExternalIPPoolSpec{
					CIDRs:    []string{"10.0.0.0/24"},
					IPFamily: "IPv4",
				},
				Status: v1alpha1.ExternalIPPoolStatus{
					Phase: v1alpha1.ExternalIPPoolPhaseDeleting,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Mark for deletion
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      poolName,
					Namespace: poolNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert Update RPC was called with DELETING state
			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_DELETING))

			// Assert Signal RPC was called
			Expect(mockServer.signals).To(HaveLen(1))
			Expect(mockServer.signals[0]).To(Equal(poolID))

			// Assert finalizer was removed (CR should be gone since it was the last finalizer)
			fetched := &v1alpha1.ExternalIPPool{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: poolName, Namespace: poolNamespace}, fetched)
			Expect(err).To(HaveOccurred()) // CR should be garbage collected
		})

		It("should remove feedback finalizer when pool record is NotFound during deletion", func() {
			// No pool added to mock server (simulates fulfillment service already deleted the record)
			cr := &v1alpha1.ExternalIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: poolNamespace,
					Labels: map[string]string{
						osacExternalIPPoolIDLabel: poolID,
					},
					Finalizers: []string{osacExternalIPPoolFeedbackFinalizer},
				},
				Spec: v1alpha1.ExternalIPPoolSpec{
					CIDRs:    []string{"10.0.0.0/24"},
					IPFamily: "IPv4",
				},
				Status: v1alpha1.ExternalIPPoolStatus{
					Phase: v1alpha1.ExternalIPPoolPhaseDeleting,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Mark for deletion
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      poolName,
					Namespace: poolNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// No gRPC Update or Signal should have been called
			Expect(mockServer.updates).To(BeEmpty())
			Expect(mockServer.signals).To(BeEmpty())

			// CR should be gone (finalizer removed, it was the last one)
			fetched := &v1alpha1.ExternalIPPool{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: poolName, Namespace: poolNamespace}, fetched)
			Expect(err).To(HaveOccurred())
		})

		It("should return error when pool record is NotFound but CR is not being deleted", func() {
			// No pool added to mock server (simulates missing record)
			cr := &v1alpha1.ExternalIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: poolNamespace,
					Labels: map[string]string{
						osacExternalIPPoolIDLabel: poolID,
					},
					Finalizers: []string{osacExternalIPPoolFeedbackFinalizer},
				},
				Spec: v1alpha1.ExternalIPPoolSpec{
					CIDRs:    []string{"10.0.0.0/24"},
					IPFamily: "IPv4",
				},
				Status: v1alpha1.ExternalIPPoolStatus{
					Phase: v1alpha1.ExternalIPPoolPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      poolName,
					Namespace: poolNamespace,
				},
			})
			Expect(err).To(HaveOccurred())
		})

		It("should not update if status unchanged", func() {
			pool := &privatev1.ExternalIPPool{
				Id: poolID,
				Metadata: &privatev1.Metadata{
					Name: poolName,
				},
				Spec: &privatev1.ExternalIPPoolSpec{},
				Status: &privatev1.ExternalIPPoolStatus{
					State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
					Total:     254,
					Allocated: 10,
					Available: 244,
				},
			}
			mockServer.addExternalIPPool(pool)

			cr := &v1alpha1.ExternalIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: poolNamespace,
					Labels: map[string]string{
						osacExternalIPPoolIDLabel: poolID,
					},
					Finalizers: []string{osacExternalIPPoolFeedbackFinalizer},
				},
				Spec: v1alpha1.ExternalIPPoolSpec{
					CIDRs:    []string{"10.0.0.0/24"},
					IPFamily: "IPv4",
				},
				Status: v1alpha1.ExternalIPPoolStatus{
					Phase: v1alpha1.ExternalIPPoolPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      poolName,
					Namespace: poolNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert no Update RPC was called (state already READY, finalizer pre-seeded)
			Expect(mockServer.updates).To(BeEmpty())
		})
	})
})

// mockExternalIPPoolsServer implements privatev1.ExternalIPPoolsServer for testing.
// It stores pool objects in memory, records all Update and Signal calls for assertions,
// and returns proper gRPC status errors (codes.NotFound) when a pool is not found.
type mockExternalIPPoolsServer struct {
	privatev1.UnimplementedExternalIPPoolsServer
	mu            sync.Mutex
	publicIPPools map[string]*privatev1.ExternalIPPool
	updates       []*privatev1.ExternalIPPool
	signals       []string
}

func (m *mockExternalIPPoolsServer) addExternalIPPool(pool *privatev1.ExternalIPPool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.publicIPPools[pool.GetId()] = pool
}

func (m *mockExternalIPPoolsServer) Get(ctx context.Context, req *privatev1.ExternalIPPoolsGetRequest) (*privatev1.ExternalIPPoolsGetResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pool, ok := m.publicIPPools[req.GetId()]
	if !ok {
		return nil, grpcstatus.Errorf(codes.NotFound, "object with identifier '%s' not found", req.GetId())
	}

	return &privatev1.ExternalIPPoolsGetResponse{
		Object: pool,
	}, nil
}

func (m *mockExternalIPPoolsServer) Update(ctx context.Context, req *privatev1.ExternalIPPoolsUpdateRequest) (*privatev1.ExternalIPPoolsUpdateResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pool := req.GetObject()
	m.publicIPPools[pool.GetId()] = pool
	m.updates = append(m.updates, pool)

	return &privatev1.ExternalIPPoolsUpdateResponse{
		Object: pool,
	}, nil
}

func (m *mockExternalIPPoolsServer) Signal(ctx context.Context, req *privatev1.ExternalIPPoolsSignalRequest) (*privatev1.ExternalIPPoolsSignalResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.signals = append(m.signals, req.GetId())

	return &privatev1.ExternalIPPoolsSignalResponse{}, nil
}
