/*
Copyright 2026.

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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("NATGatewayFeedbackController", func() {
	const (
		natGatewayName      = "test-natgateway"
		natGatewayNamespace = "test-namespace"
		natGatewayID        = "natgateway-123"
	)

	var (
		ctx        context.Context
		k8sClient  client.Client
		mockServer *mockNATGatewaysServer
		reconciler *NATGatewayFeedbackReconciler
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
		mockServer = &mockNATGatewaysServer{
			natGateways: make(map[string]*privatev1.NATGateway),
			updates:     make([]*privatev1.NATGateway, 0),
			signals:     make([]string, 0),
		}
		listener = bufconn.Listen(1024 * 1024)
		grpcServer = grpc.NewServer()
		privatev1.RegisterNATGatewaysServer(grpcServer, mockServer)

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
		reconciler = NewNATGatewayFeedbackReconciler(k8sClient, conn, natGatewayNamespace)
	})

	AfterEach(func() {
		if grpcServer != nil {
			grpcServer.Stop()
		}
		if listener != nil {
			_ = listener.Close()
		}
	})

	Context("when reconciling a NATGateway CR", func() {
		It("should sync Phase=Ready to database state=READY", func() {
			natGateway := &privatev1.NATGateway{
				Id: natGatewayID,
				Metadata: &privatev1.Metadata{
					Name: natGatewayName,
				},
				Spec: &privatev1.NATGatewaySpec{
					VirtualNetwork: &privatev1.VirtualNetworkLocalReference{Name: "vnet-123"},
					ExternalIp:     &privatev1.ExternalIPLocalReference{Id: "eip-456"},
				},
				Status: &privatev1.NATGatewayStatus{
					State: privatev1.NATGatewayState_NAT_GATEWAY_STATE_PENDING,
				},
			}
			mockServer.addNATGateway(natGateway)

			cr := &v1alpha1.NATGateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      natGatewayName,
					Namespace: natGatewayNamespace,
					Labels: map[string]string{
						osacNATGatewayIDLabel: natGatewayID,
					},
				},
				Spec: v1alpha1.NATGatewaySpec{
					VirtualNetwork: "vnet-123",
					ExternalIP:     "eip-456",
				},
				Status: v1alpha1.NATGatewayStatus{
					Phase: v1alpha1.NATGatewayPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      natGatewayName,
					Namespace: natGatewayNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.NATGatewayState_NAT_GATEWAY_STATE_READY))

			updated := &v1alpha1.NATGateway{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: natGatewayName, Namespace: natGatewayNamespace}, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, osacNATGatewayFeedbackFinalizer)).To(BeTrue())
		})

		It("should sync Phase=Progressing to database state=PENDING", func() {
			natGateway := &privatev1.NATGateway{
				Id: natGatewayID,
				Metadata: &privatev1.Metadata{
					Name: natGatewayName,
				},
				Spec: &privatev1.NATGatewaySpec{
					VirtualNetwork: &privatev1.VirtualNetworkLocalReference{Name: "vnet-123"},
					ExternalIp:     &privatev1.ExternalIPLocalReference{Id: "eip-456"},
				},
				Status: &privatev1.NATGatewayStatus{
					State: privatev1.NATGatewayState_NAT_GATEWAY_STATE_READY,
				},
			}
			mockServer.addNATGateway(natGateway)

			cr := &v1alpha1.NATGateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      natGatewayName,
					Namespace: natGatewayNamespace,
					Labels: map[string]string{
						osacNATGatewayIDLabel: natGatewayID,
					},
				},
				Spec: v1alpha1.NATGatewaySpec{
					VirtualNetwork: "vnet-123",
					ExternalIP:     "eip-456",
				},
				Status: v1alpha1.NATGatewayStatus{
					Phase: v1alpha1.NATGatewayPhaseProgressing,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      natGatewayName,
					Namespace: natGatewayNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.NATGatewayState_NAT_GATEWAY_STATE_PENDING))
		})

		It("should sync Phase=Failed to database state=FAILED", func() {
			natGateway := &privatev1.NATGateway{
				Id: natGatewayID,
				Metadata: &privatev1.Metadata{
					Name: natGatewayName,
				},
				Spec: &privatev1.NATGatewaySpec{
					VirtualNetwork: &privatev1.VirtualNetworkLocalReference{Name: "vnet-123"},
					ExternalIp:     &privatev1.ExternalIPLocalReference{Id: "eip-456"},
				},
				Status: &privatev1.NATGatewayStatus{
					State: privatev1.NATGatewayState_NAT_GATEWAY_STATE_PENDING,
				},
			}
			mockServer.addNATGateway(natGateway)

			cr := &v1alpha1.NATGateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      natGatewayName,
					Namespace: natGatewayNamespace,
					Labels: map[string]string{
						osacNATGatewayIDLabel: natGatewayID,
					},
				},
				Spec: v1alpha1.NATGatewaySpec{
					VirtualNetwork: "vnet-123",
					ExternalIP:     "eip-456",
				},
				Status: v1alpha1.NATGatewayStatus{
					Phase: v1alpha1.NATGatewayPhaseFailed,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      natGatewayName,
					Namespace: natGatewayNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.NATGatewayState_NAT_GATEWAY_STATE_FAILED))
		})

		It("should skip CRs without natgateway-uuid label", func() {
			cr := &v1alpha1.NATGateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      natGatewayName,
					Namespace: natGatewayNamespace,
					Labels:    map[string]string{},
				},
				Spec: v1alpha1.NATGatewaySpec{
					VirtualNetwork: "vnet-123",
					ExternalIP:     "eip-456",
				},
				Status: v1alpha1.NATGatewayStatus{
					Phase: v1alpha1.NATGatewayPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      natGatewayName,
					Namespace: natGatewayNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(BeEmpty())
		})

		It("should sync Phase=Deleting to database state=DELETING during deletion", func() {
			natGateway := &privatev1.NATGateway{
				Id: natGatewayID,
				Metadata: &privatev1.Metadata{
					Name: natGatewayName,
				},
				Spec: &privatev1.NATGatewaySpec{
					VirtualNetwork: &privatev1.VirtualNetworkLocalReference{Name: "vnet-123"},
					ExternalIp:     &privatev1.ExternalIPLocalReference{Id: "eip-456"},
				},
				Status: &privatev1.NATGatewayStatus{
					State: privatev1.NATGatewayState_NAT_GATEWAY_STATE_READY,
				},
			}
			mockServer.addNATGateway(natGateway)

			cr := &v1alpha1.NATGateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      natGatewayName,
					Namespace: natGatewayNamespace,
					Labels: map[string]string{
						osacNATGatewayIDLabel: natGatewayID,
					},
					Finalizers: []string{osacNATGatewayFeedbackFinalizer, osacNATGatewayFinalizer},
				},
				Spec: v1alpha1.NATGatewaySpec{
					VirtualNetwork: "vnet-123",
					ExternalIP:     "eip-456",
				},
				Status: v1alpha1.NATGatewayStatus{
					Phase: v1alpha1.NATGatewayPhaseDeleting,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      natGatewayName,
					Namespace: natGatewayNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.NATGatewayState_NAT_GATEWAY_STATE_DELETING))

			updated := &v1alpha1.NATGateway{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: natGatewayName, Namespace: natGatewayNamespace}, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, osacNATGatewayFeedbackFinalizer)).To(BeTrue())
		})

		It("should sync Phase=Failed during deletion to database state=FAILED", func() {
			natGateway := &privatev1.NATGateway{
				Id: natGatewayID,
				Metadata: &privatev1.Metadata{
					Name: natGatewayName,
				},
				Spec: &privatev1.NATGatewaySpec{
					VirtualNetwork: &privatev1.VirtualNetworkLocalReference{Name: "vnet-123"},
					ExternalIp:     &privatev1.ExternalIPLocalReference{Id: "eip-456"},
				},
				Status: &privatev1.NATGatewayStatus{
					State: privatev1.NATGatewayState_NAT_GATEWAY_STATE_READY,
				},
			}
			mockServer.addNATGateway(natGateway)

			cr := &v1alpha1.NATGateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      natGatewayName,
					Namespace: natGatewayNamespace,
					Labels: map[string]string{
						osacNATGatewayIDLabel: natGatewayID,
					},
					Finalizers: []string{osacNATGatewayFeedbackFinalizer, osacNATGatewayFinalizer},
				},
				Spec: v1alpha1.NATGatewaySpec{
					VirtualNetwork: "vnet-123",
					ExternalIP:     "eip-456",
				},
				Status: v1alpha1.NATGatewayStatus{
					Phase: v1alpha1.NATGatewayPhaseFailed,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      natGatewayName,
					Namespace: natGatewayNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.NATGatewayState_NAT_GATEWAY_STATE_FAILED))
		})

		It("should remove feedback finalizer and signal when it is the last finalizer", func() {
			natGateway := &privatev1.NATGateway{
				Id: natGatewayID,
				Metadata: &privatev1.Metadata{
					Name: natGatewayName,
				},
				Spec: &privatev1.NATGatewaySpec{
					VirtualNetwork: &privatev1.VirtualNetworkLocalReference{Name: "vnet-123"},
					ExternalIp:     &privatev1.ExternalIPLocalReference{Id: "eip-456"},
				},
				Status: &privatev1.NATGatewayStatus{
					State: privatev1.NATGatewayState_NAT_GATEWAY_STATE_READY,
				},
			}
			mockServer.addNATGateway(natGateway)

			cr := &v1alpha1.NATGateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      natGatewayName,
					Namespace: natGatewayNamespace,
					Labels: map[string]string{
						osacNATGatewayIDLabel: natGatewayID,
					},
					Finalizers: []string{osacNATGatewayFeedbackFinalizer},
				},
				Spec: v1alpha1.NATGatewaySpec{
					VirtualNetwork: "vnet-123",
					ExternalIP:     "eip-456",
				},
				Status: v1alpha1.NATGatewayStatus{
					Phase: v1alpha1.NATGatewayPhaseDeleting,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      natGatewayName,
					Namespace: natGatewayNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.NATGatewayState_NAT_GATEWAY_STATE_DELETING))

			Expect(mockServer.signals).To(HaveLen(1))
			Expect(mockServer.signals[0]).To(Equal(natGatewayID))

			updated := &v1alpha1.NATGateway{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: natGatewayName, Namespace: natGatewayNamespace}, updated)
			Expect(err).To(HaveOccurred())
		})

		It("should remove feedback finalizer when NATGateway record is NotFound during deletion", func() {
			cr := &v1alpha1.NATGateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      natGatewayName,
					Namespace: natGatewayNamespace,
					Labels: map[string]string{
						osacNATGatewayIDLabel: natGatewayID,
					},
					Finalizers: []string{osacNATGatewayFeedbackFinalizer},
				},
				Spec: v1alpha1.NATGatewaySpec{
					VirtualNetwork: "vnet-123",
					ExternalIP:     "eip-456",
				},
				Status: v1alpha1.NATGatewayStatus{
					Phase: v1alpha1.NATGatewayPhaseDeleting,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      natGatewayName,
					Namespace: natGatewayNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(BeEmpty())
			Expect(mockServer.signals).To(BeEmpty())

			updated := &v1alpha1.NATGateway{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: natGatewayName, Namespace: natGatewayNamespace}, updated)
			Expect(err).To(HaveOccurred())
		})

		It("should return error when NATGateway record is NotFound but CR is not being deleted", func() {
			cr := &v1alpha1.NATGateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      natGatewayName,
					Namespace: natGatewayNamespace,
					Labels: map[string]string{
						osacNATGatewayIDLabel: natGatewayID,
					},
					Finalizers: []string{osacNATGatewayFeedbackFinalizer},
				},
				Spec: v1alpha1.NATGatewaySpec{
					VirtualNetwork: "vnet-123",
					ExternalIP:     "eip-456",
				},
				Status: v1alpha1.NATGatewayStatus{
					Phase: v1alpha1.NATGatewayPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      natGatewayName,
					Namespace: natGatewayNamespace,
				},
			})
			Expect(err).To(HaveOccurred())
		})

		It("should not update if status unchanged", func() {
			natGateway := &privatev1.NATGateway{
				Id: natGatewayID,
				Metadata: &privatev1.Metadata{
					Name: natGatewayName,
				},
				Spec: &privatev1.NATGatewaySpec{
					VirtualNetwork: &privatev1.VirtualNetworkLocalReference{Name: "vnet-123"},
					ExternalIp:     &privatev1.ExternalIPLocalReference{Id: "eip-456"},
				},
				Status: &privatev1.NATGatewayStatus{
					State: privatev1.NATGatewayState_NAT_GATEWAY_STATE_READY,
				},
			}
			mockServer.addNATGateway(natGateway)

			cr := &v1alpha1.NATGateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      natGatewayName,
					Namespace: natGatewayNamespace,
					Labels: map[string]string{
						osacNATGatewayIDLabel: natGatewayID,
					},
					Finalizers: []string{osacNATGatewayFeedbackFinalizer},
				},
				Spec: v1alpha1.NATGatewaySpec{
					VirtualNetwork: "vnet-123",
					ExternalIP:     "eip-456",
				},
				Status: v1alpha1.NATGatewayStatus{
					Phase: v1alpha1.NATGatewayPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      natGatewayName,
					Namespace: natGatewayNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(BeEmpty())
		})
	})
})

var _ = Describe("syncNATGatewayDelete", func() {
	It("dates a failed gateway's report at the CR's recorded transition time", func() {
		remote := &privatev1.NATGateway{Id: "natgateway-del-failed", Status: &privatev1.NATGatewayStatus{}}
		recorded := metav1.NewTime(time.Unix(1000, 0))
		deletedAt := metav1.NewTime(time.Unix(2000, 0))
		cr := &v1alpha1.NATGateway{
			ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &deletedAt},
			Status:     v1alpha1.NATGatewayStatus{Phase: v1alpha1.NATGatewayPhaseFailed, StateTransitionTime: &recorded},
		}

		Expect(syncNATGatewayDelete(context.Background(), cr, remote)).To(Succeed())

		Expect(remote.GetStatus().GetStateTransitionTime().AsTime().Equal(recorded.Time)).To(BeTrue())
	})

	It("dates the forced DELETING of a pre-deletion phase at the deletion timestamp", func() {
		// The recorded time dates the Ready phase, not the deletion: reporting
		// DELETING at it would end billing at the Ready time and erase the span
		// up to the deletion.
		remote := &privatev1.NATGateway{Id: "natgateway-del-ready", Status: &privatev1.NATGatewayStatus{}}
		recorded := metav1.NewTime(time.Unix(1000, 0))
		deletedAt := metav1.NewTime(time.Unix(2000, 0))
		cr := &v1alpha1.NATGateway{
			ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &deletedAt},
			Status:     v1alpha1.NATGatewayStatus{Phase: v1alpha1.NATGatewayPhaseReady, StateTransitionTime: &recorded},
		}

		Expect(syncNATGatewayDelete(context.Background(), cr, remote)).To(Succeed())

		Expect(remote.GetStatus().GetStateTransitionTime().AsTime().Equal(deletedAt.Time)).To(BeTrue())
	})

	It("dates the forced DELETING at the later of the status time and the deletion timestamp", func() {
		// A reconciler status write can land after the delete request, putting
		// the CR's transition time past the deletion timestamp. Fulfillment
		// rejects a change whose time is not later than the stored one, so the
		// report must carry the later time — the status time — not the
		// deletion timestamp.
		remote := &privatev1.NATGateway{Id: "natgateway-del-late", Status: &privatev1.NATGatewayStatus{}}
		deletedAt := metav1.NewTime(time.Unix(2000, 0))
		statusAt := metav1.NewTime(time.Unix(3000, 0))
		cr := &v1alpha1.NATGateway{
			ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &deletedAt},
			Status:     v1alpha1.NATGatewayStatus{Phase: v1alpha1.NATGatewayPhaseDeleting, StateTransitionTime: &statusAt},
		}

		Expect(syncNATGatewayDelete(context.Background(), cr, remote)).To(Succeed())

		Expect(remote.GetStatus().GetStateTransitionTime().AsTime().Equal(statusAt.Time)).To(BeTrue())
	})

	It("dates the change at the deletion timestamp when the CR status carries no time", func() {
		remote := &privatev1.NATGateway{Id: "natgateway-del-fallback", Status: &privatev1.NATGatewayStatus{}}
		deletedAt := metav1.NewTime(time.Unix(2000, 0))
		cr := &v1alpha1.NATGateway{
			ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &deletedAt},
			Status:     v1alpha1.NATGatewayStatus{Phase: v1alpha1.NATGatewayPhaseReady},
		}

		Expect(syncNATGatewayDelete(context.Background(), cr, remote)).To(Succeed())

		Expect(remote.GetStatus().GetStateTransitionTime().AsTime().Equal(deletedAt.Time)).To(BeTrue())
	})

	It("keeps the remote's stored time when neither the CR nor its deletion carries one", func() {
		remote := &privatev1.NATGateway{Id: "natgateway-del-remote", Status: &privatev1.NATGatewayStatus{}}
		original := timestamppb.Now()
		remote.GetStatus().SetStateTransitionTime(original)

		Expect(syncNATGatewayDelete(context.Background(), &v1alpha1.NATGateway{}, remote)).To(Succeed())

		Expect(proto.Equal(remote.GetStatus().GetStateTransitionTime(), original)).To(BeTrue())
	})

	It("rejects a gateway with no time anywhere", func() {
		remote := &privatev1.NATGateway{Id: "natgateway-del-none", Status: &privatev1.NATGatewayStatus{}}

		Expect(syncNATGatewayDelete(context.Background(), &v1alpha1.NATGateway{}, remote)).To(
			MatchError(ContainSubstring("no state transition time")),
		)
	})
})

// mockNATGatewaysServer implements privatev1.NATGatewaysServer for testing
type mockNATGatewaysServer struct {
	privatev1.UnimplementedNATGatewaysServer
	mu          sync.Mutex
	natGateways map[string]*privatev1.NATGateway
	updates     []*privatev1.NATGateway
	signals     []string
}

func (m *mockNATGatewaysServer) addNATGateway(natGateway *privatev1.NATGateway) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.natGateways[natGateway.GetId()] = natGateway
}

func (m *mockNATGatewaysServer) Get(ctx context.Context, req *privatev1.NATGatewaysGetRequest) (*privatev1.NATGatewaysGetResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	natGateway, ok := m.natGateways[req.GetId()]
	if !ok {
		return nil, grpcstatus.Errorf(codes.NotFound, "object with identifier '%s' not found", req.GetId())
	}

	return &privatev1.NATGatewaysGetResponse{
		Object: natGateway,
	}, nil
}

func (m *mockNATGatewaysServer) Update(ctx context.Context, req *privatev1.NATGatewaysUpdateRequest) (*privatev1.NATGatewaysUpdateResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	natGateway := req.GetObject()
	m.natGateways[natGateway.GetId()] = natGateway
	m.updates = append(m.updates, natGateway)

	return &privatev1.NATGatewaysUpdateResponse{
		Object: natGateway,
	}, nil
}

func (m *mockNATGatewaysServer) Signal(ctx context.Context, req *privatev1.NATGatewaysSignalRequest) (*privatev1.NATGatewaysSignalResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.signals = append(m.signals, req.GetId())

	return &privatev1.NATGatewaysSignalResponse{}, nil
}
