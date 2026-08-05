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

var _ = Describe("SecurityGroupFeedbackController", func() {
	const (
		sgName      = "test-sg"
		sgNamespace = "test-namespace"
		sgID        = "sg-123"
		testVnetRef = "vnet-abc"
	)

	var (
		ctx        context.Context
		k8sClient  client.Client
		mockServer *mockSecurityGroupsServer
		reconciler *SecurityGroupFeedbackReconciler
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
		mockServer = &mockSecurityGroupsServer{
			securityGroups: make(map[string]*privatev1.SecurityGroup),
			updates:        make([]*privatev1.SecurityGroup, 0),
			signals:        make([]string, 0),
		}
		listener = bufconn.Listen(1024 * 1024)
		grpcServer = grpc.NewServer()
		privatev1.RegisterSecurityGroupsServer(grpcServer, mockServer)

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
		reconciler = NewSecurityGroupFeedbackReconciler(k8sClient, conn, sgNamespace)
	})

	AfterEach(func() {
		if grpcServer != nil {
			grpcServer.Stop()
		}
		if listener != nil {
			_ = listener.Close()
		}
	})

	Context("when reconciling a SecurityGroup CR", func() {
		It("should sync Phase=Ready to database state=READY", func() {
			// Create security group in mock database
			sg := &privatev1.SecurityGroup{
				Id: sgID,
				Metadata: &privatev1.Metadata{
					Name: sgName,
				},
				Spec: &privatev1.SecurityGroupSpec{
					VirtualNetwork: testVnetRef,
				},
				Status: &privatev1.SecurityGroupStatus{
					State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING,
				},
			}
			mockServer.addSecurityGroup(sg)

			// Create SecurityGroup CR with Phase=Ready
			cr := &v1alpha1.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sgName,
					Namespace: sgNamespace,
					Labels: map[string]string{
						osacSecurityGroupIDLabel: sgID,
					},
				},
				Spec: v1alpha1.SecurityGroupSpec{
					VirtualNetwork: testVnetRef,
				},
				Status: v1alpha1.SecurityGroupStatus{
					Phase: v1alpha1.SecurityGroupPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Reconcile
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      sgName,
					Namespace: sgNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert gRPC Update called with state=READY
			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY))

			// Assert finalizer was added
			updated := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: sgName, Namespace: sgNamespace}, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, osacSecurityGroupFeedbackFinalizer)).To(BeTrue())
		})

		It("should sync Phase=Progressing to database state=PENDING", func() {
			sg := &privatev1.SecurityGroup{
				Id: sgID,
				Metadata: &privatev1.Metadata{
					Name: sgName,
				},
				Spec: &privatev1.SecurityGroupSpec{
					VirtualNetwork: testVnetRef,
				},
				Status: &privatev1.SecurityGroupStatus{
					State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
				},
			}
			mockServer.addSecurityGroup(sg)

			cr := &v1alpha1.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sgName,
					Namespace: sgNamespace,
					Labels: map[string]string{
						osacSecurityGroupIDLabel: sgID,
					},
				},
				Spec: v1alpha1.SecurityGroupSpec{
					VirtualNetwork: testVnetRef,
				},
				Status: v1alpha1.SecurityGroupStatus{
					Phase: v1alpha1.SecurityGroupPhaseProgressing,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      sgName,
					Namespace: sgNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING))
		})

		It("should sync Phase=Failed to database state=FAILED", func() {
			sg := &privatev1.SecurityGroup{
				Id: sgID,
				Metadata: &privatev1.Metadata{
					Name: sgName,
				},
				Spec: &privatev1.SecurityGroupSpec{
					VirtualNetwork: testVnetRef,
				},
				Status: &privatev1.SecurityGroupStatus{
					State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING,
				},
			}
			mockServer.addSecurityGroup(sg)

			cr := &v1alpha1.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sgName,
					Namespace: sgNamespace,
					Labels: map[string]string{
						osacSecurityGroupIDLabel: sgID,
					},
				},
				Spec: v1alpha1.SecurityGroupSpec{
					VirtualNetwork: testVnetRef,
				},
				Status: v1alpha1.SecurityGroupStatus{
					Phase: v1alpha1.SecurityGroupPhaseFailed,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      sgName,
					Namespace: sgNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_FAILED))
		})

		It("should skip CRs without securitygroup-uuid label", func() {
			cr := &v1alpha1.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sgName,
					Namespace: sgNamespace,
					Labels:    map[string]string{},
				},
				Spec: v1alpha1.SecurityGroupSpec{
					VirtualNetwork: testVnetRef,
				},
				Status: v1alpha1.SecurityGroupStatus{
					Phase: v1alpha1.SecurityGroupPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      sgName,
					Namespace: sgNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert no Update RPC was called
			Expect(mockServer.updates).To(BeEmpty())
		})

		It("should sync Phase=Deleting to database state=DELETING during deletion", func() {
			sg := &privatev1.SecurityGroup{
				Id: sgID,
				Metadata: &privatev1.Metadata{
					Name: sgName,
				},
				Spec: &privatev1.SecurityGroupSpec{
					VirtualNetwork: testVnetRef,
				},
				Status: &privatev1.SecurityGroupStatus{
					State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
				},
			}
			mockServer.addSecurityGroup(sg)

			// Create CR with finalizer first (simulating a previously reconciled CR)
			cr := &v1alpha1.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sgName,
					Namespace: sgNamespace,
					Labels: map[string]string{
						osacSecurityGroupIDLabel: sgID,
					},
					Finalizers: []string{osacSecurityGroupFeedbackFinalizer, osacSecurityGroupFinalizer},
				},
				Spec: v1alpha1.SecurityGroupSpec{
					VirtualNetwork: testVnetRef,
				},
				Status: v1alpha1.SecurityGroupStatus{
					Phase: v1alpha1.SecurityGroupPhaseDeleting,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Mark for deletion
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      sgName,
					Namespace: sgNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert Update RPC was called with DELETING state
			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_DELETING))

			// Feedback finalizer should still be present (other finalizers remain)
			updated := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: sgName, Namespace: sgNamespace}, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, osacSecurityGroupFeedbackFinalizer)).To(BeTrue())
		})

		It("should sync Phase=Failed during deletion to database state=DELETE_FAILED", func() {
			sg := &privatev1.SecurityGroup{
				Id: sgID,
				Metadata: &privatev1.Metadata{
					Name: sgName,
				},
				Spec: &privatev1.SecurityGroupSpec{
					VirtualNetwork: testVnetRef,
				},
				Status: &privatev1.SecurityGroupStatus{
					State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
				},
			}
			mockServer.addSecurityGroup(sg)

			// Create CR with finalizers (simulating a previously reconciled CR)
			cr := &v1alpha1.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sgName,
					Namespace: sgNamespace,
					Labels: map[string]string{
						osacSecurityGroupIDLabel: sgID,
					},
					Finalizers: []string{osacSecurityGroupFeedbackFinalizer, osacSecurityGroupFinalizer},
				},
				Spec: v1alpha1.SecurityGroupSpec{
					VirtualNetwork: testVnetRef,
				},
				Status: v1alpha1.SecurityGroupStatus{
					Phase: v1alpha1.SecurityGroupPhaseFailed,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Mark for deletion
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      sgName,
					Namespace: sgNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert Update RPC was called with DELETE_FAILED state
			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_DELETE_FAILED))
		})

		It("should remove feedback finalizer and signal when it is the last finalizer", func() {
			sg := &privatev1.SecurityGroup{
				Id: sgID,
				Metadata: &privatev1.Metadata{
					Name: sgName,
				},
				Spec: &privatev1.SecurityGroupSpec{
					VirtualNetwork: testVnetRef,
				},
				Status: &privatev1.SecurityGroupStatus{
					State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
				},
			}
			mockServer.addSecurityGroup(sg)

			// Create CR with only feedback finalizer (simulating other finalizers already removed)
			cr := &v1alpha1.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sgName,
					Namespace: sgNamespace,
					Labels: map[string]string{
						osacSecurityGroupIDLabel: sgID,
					},
					Finalizers: []string{osacSecurityGroupFeedbackFinalizer},
				},
				Spec: v1alpha1.SecurityGroupSpec{
					VirtualNetwork: testVnetRef,
				},
				Status: v1alpha1.SecurityGroupStatus{
					Phase: v1alpha1.SecurityGroupPhaseDeleting,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Mark for deletion
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      sgName,
					Namespace: sgNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert Update RPC was called with DELETING state
			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_DELETING))

			// Assert Signal RPC was called
			Expect(mockServer.signals).To(HaveLen(1))
			Expect(mockServer.signals[0]).To(Equal(sgID))

			// Assert finalizer was removed (CR should be gone since it was the last finalizer)
			updated := &v1alpha1.SecurityGroup{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: sgName, Namespace: sgNamespace}, updated)
			Expect(err).To(HaveOccurred()) // CR should be garbage collected
		})

		It("should remove feedback finalizer when security group record is NotFound during deletion", func() {
			cr := &v1alpha1.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sgName,
					Namespace: sgNamespace,
					Labels: map[string]string{
						osacSecurityGroupIDLabel: sgID,
					},
					Finalizers: []string{osacSecurityGroupFeedbackFinalizer},
				},
				Spec: v1alpha1.SecurityGroupSpec{
					VirtualNetwork: testVnetRef,
				},
				Status: v1alpha1.SecurityGroupStatus{
					Phase: v1alpha1.SecurityGroupPhaseDeleting,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      sgName,
					Namespace: sgNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(BeEmpty())
			Expect(mockServer.signals).To(BeEmpty())

			updated := &v1alpha1.SecurityGroup{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: sgName, Namespace: sgNamespace}, updated)
			Expect(err).To(HaveOccurred())
		})

		It("should return error when security group record is NotFound but CR is not being deleted", func() {
			cr := &v1alpha1.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sgName,
					Namespace: sgNamespace,
					Labels: map[string]string{
						osacSecurityGroupIDLabel: sgID,
					},
					Finalizers: []string{osacSecurityGroupFeedbackFinalizer},
				},
				Spec: v1alpha1.SecurityGroupSpec{
					VirtualNetwork: testVnetRef,
				},
				Status: v1alpha1.SecurityGroupStatus{
					Phase: v1alpha1.SecurityGroupPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      sgName,
					Namespace: sgNamespace,
				},
			})
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(codes.NotFound))
		})

		It("should not update if status unchanged", func() {
			sg := &privatev1.SecurityGroup{
				Id: sgID,
				Metadata: &privatev1.Metadata{
					Name: sgName,
				},
				Spec: &privatev1.SecurityGroupSpec{
					VirtualNetwork: testVnetRef,
				},
				Status: &privatev1.SecurityGroupStatus{
					State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
				},
			}
			mockServer.addSecurityGroup(sg)

			cr := &v1alpha1.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sgName,
					Namespace: sgNamespace,
					Labels: map[string]string{
						osacSecurityGroupIDLabel: sgID,
					},
					Finalizers: []string{osacSecurityGroupFeedbackFinalizer},
				},
				Spec: v1alpha1.SecurityGroupSpec{
					VirtualNetwork: testVnetRef,
				},
				Status: v1alpha1.SecurityGroupStatus{
					Phase: v1alpha1.SecurityGroupPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      sgName,
					Namespace: sgNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Assert no Update RPC was called (state already READY, finalizer pre-seeded)
			Expect(mockServer.updates).To(BeEmpty())
		})
	})
})

// mockSecurityGroupsServer implements privatev1.SecurityGroupsServer for testing
type mockSecurityGroupsServer struct {
	privatev1.UnimplementedSecurityGroupsServer
	mu             sync.Mutex
	securityGroups map[string]*privatev1.SecurityGroup
	updates        []*privatev1.SecurityGroup
	signals        []string
}

func (m *mockSecurityGroupsServer) addSecurityGroup(sg *privatev1.SecurityGroup) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.securityGroups[sg.GetId()] = sg
}

func (m *mockSecurityGroupsServer) Get(ctx context.Context, req *privatev1.SecurityGroupsGetRequest) (*privatev1.SecurityGroupsGetResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sg, ok := m.securityGroups[req.GetId()]
	if !ok {
		return nil, grpcstatus.Errorf(codes.NotFound, "object with identifier '%s' not found", req.GetId())
	}

	return &privatev1.SecurityGroupsGetResponse{
		Object: sg,
	}, nil
}

func (m *mockSecurityGroupsServer) Update(ctx context.Context, req *privatev1.SecurityGroupsUpdateRequest) (*privatev1.SecurityGroupsUpdateResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sg := req.GetObject()
	m.securityGroups[sg.GetId()] = sg
	m.updates = append(m.updates, sg)

	return &privatev1.SecurityGroupsUpdateResponse{
		Object: sg,
	}, nil
}

func (m *mockSecurityGroupsServer) Signal(ctx context.Context, req *privatev1.SecurityGroupsSignalRequest) (*privatev1.SecurityGroupsSignalResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.signals = append(m.signals, req.GetId())

	return &privatev1.SecurityGroupsSignalResponse{}, nil
}
