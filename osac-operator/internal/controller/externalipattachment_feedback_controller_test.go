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
	"errors"
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

var _ = Describe("ExternalIPAttachmentFeedbackController", func() {
	const (
		attachmentName      = "test-attachment"
		attachmentNamespace = "test-namespace"
		attachmentID        = "attachment-123"
		parentExternalIPID  = "externalip-456"
	)

	var (
		ctx                    context.Context
		k8sClient              client.Client
		mockAttachmentsServer  *mockExternalIPAttachmentsServer
		mockExternalIPsServer2 *mockExternalIPsServerForAttachment
		reconciler             *ExternalIPAttachmentFeedbackReconciler
		grpcServer             *grpc.Server
		listener               *bufconn.Listener
	)

	BeforeEach(func() {
		ctx = context.Background()

		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		k8sClient = fake.NewClientBuilder().WithScheme(scheme).Build()

		mockAttachmentsServer = &mockExternalIPAttachmentsServer{
			attachments: make(map[string]*privatev1.ExternalIPAttachment),
			updates:     make([]*privatev1.ExternalIPAttachment, 0),
			signals:     make([]string, 0),
		}
		mockExternalIPsServer2 = &mockExternalIPsServerForAttachment{
			publicIPs: make(map[string]*privatev1.ExternalIP),
			updates:   make([]*privatev1.ExternalIP, 0),
		}
		listener = bufconn.Listen(1024 * 1024)
		grpcServer = grpc.NewServer()
		privatev1.RegisterExternalIPAttachmentsServer(grpcServer, mockAttachmentsServer)
		privatev1.RegisterExternalIPsServer(grpcServer, mockExternalIPsServer2)

		go func() {
			_ = grpcServer.Serve(listener)
		}()

		conn, err := grpc.NewClient("passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
				return listener.Dial()
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		Expect(err).NotTo(HaveOccurred())

		reconciler = NewExternalIPAttachmentFeedbackReconciler(k8sClient, conn, attachmentNamespace)
	})

	AfterEach(func() {
		if grpcServer != nil {
			grpcServer.Stop()
		}
		if listener != nil {
			_ = listener.Close()
		}
	})

	Context("when reconciling a ExternalIPAttachment CR", func() {
		It("should sync Phase=Progressing to state=PENDING", func() {
			attachment := &privatev1.ExternalIPAttachment{
				Id: attachmentID,
				Metadata: &privatev1.Metadata{
					Name: attachmentName,
				},
				Spec: &privatev1.ExternalIPAttachmentSpec{},
				Status: &privatev1.ExternalIPAttachmentStatus{
					State: privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_UNSPECIFIED,
				},
			}
			attachment.GetSpec().SetExternalIp(parentExternalIPID)
			mockAttachmentsServer.addAttachment(attachment)

			cr := &v1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      attachmentName,
					Namespace: attachmentNamespace,
					Labels: map[string]string{
						osacExternalIPAttachmentIDLabel: attachmentID,
					},
				},
				Spec: v1alpha1.ExternalIPAttachmentSpec{
					ExternalIP: "some-externalip",
				},
				Status: v1alpha1.ExternalIPAttachmentStatus{
					Phase: v1alpha1.ExternalIPAttachmentPhaseProgressing,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      attachmentName,
					Namespace: attachmentNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockAttachmentsServer.updates).To(HaveLen(1))
			Expect(mockAttachmentsServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING))

			updated := &v1alpha1.ExternalIPAttachment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: attachmentName, Namespace: attachmentNamespace}, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, osacExternalIPAttachmentFeedbackFinalizer)).To(BeTrue())
		})

		It("should sync Phase=Ready to state=READY and set parent ExternalIP attached=true", func() {
			attachment := &privatev1.ExternalIPAttachment{
				Id: attachmentID,
				Metadata: &privatev1.Metadata{
					Name: attachmentName,
				},
				Spec: &privatev1.ExternalIPAttachmentSpec{},
				Status: &privatev1.ExternalIPAttachmentStatus{
					State: privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING,
				},
			}
			attachment.GetSpec().SetExternalIp(parentExternalIPID)
			mockAttachmentsServer.addAttachment(attachment)

			parentExternalIP := &privatev1.ExternalIP{
				Id: parentExternalIPID,
				Metadata: &privatev1.Metadata{
					Name: "parent-externalip",
				},
				Spec:   &privatev1.ExternalIPSpec{},
				Status: &privatev1.ExternalIPStatus{},
			}
			mockExternalIPsServer2.addExternalIP(parentExternalIP)

			cr := &v1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      attachmentName,
					Namespace: attachmentNamespace,
					Labels: map[string]string{
						osacExternalIPAttachmentIDLabel: attachmentID,
					},
				},
				Spec: v1alpha1.ExternalIPAttachmentSpec{
					ExternalIP: "some-externalip",
				},
				Status: v1alpha1.ExternalIPAttachmentStatus{
					Phase: v1alpha1.ExternalIPAttachmentPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      attachmentName,
					Namespace: attachmentNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockAttachmentsServer.updates).To(HaveLen(1))
			Expect(mockAttachmentsServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_READY))

			Expect(mockExternalIPsServer2.updates).To(HaveLen(1))
			Expect(mockExternalIPsServer2.updates[0].GetStatus().GetAttached()).To(BeTrue())
		})

		It("should sync Phase=Failed to state=FAILED", func() {
			attachment := &privatev1.ExternalIPAttachment{
				Id: attachmentID,
				Metadata: &privatev1.Metadata{
					Name: attachmentName,
				},
				Spec: &privatev1.ExternalIPAttachmentSpec{},
				Status: &privatev1.ExternalIPAttachmentStatus{
					State: privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING,
				},
			}
			attachment.GetSpec().SetExternalIp(parentExternalIPID)
			mockAttachmentsServer.addAttachment(attachment)

			cr := &v1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      attachmentName,
					Namespace: attachmentNamespace,
					Labels: map[string]string{
						osacExternalIPAttachmentIDLabel: attachmentID,
					},
				},
				Spec: v1alpha1.ExternalIPAttachmentSpec{
					ExternalIP: "some-externalip",
				},
				Status: v1alpha1.ExternalIPAttachmentStatus{
					Phase: v1alpha1.ExternalIPAttachmentPhaseFailed,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      attachmentName,
					Namespace: attachmentNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockAttachmentsServer.updates).To(HaveLen(1))
			Expect(mockAttachmentsServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_FAILED))
		})

		It("should sync state=DELETING during deletion and clear parent ExternalIP attached", func() {
			attachment := &privatev1.ExternalIPAttachment{
				Id: attachmentID,
				Metadata: &privatev1.Metadata{
					Name: attachmentName,
				},
				Spec: &privatev1.ExternalIPAttachmentSpec{},
				Status: &privatev1.ExternalIPAttachmentStatus{
					State: privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_READY,
				},
			}
			attachment.GetSpec().SetExternalIp(parentExternalIPID)
			mockAttachmentsServer.addAttachment(attachment)

			parentExternalIP := &privatev1.ExternalIP{
				Id: parentExternalIPID,
				Metadata: &privatev1.Metadata{
					Name: "parent-externalip",
				},
				Spec:   &privatev1.ExternalIPSpec{},
				Status: &privatev1.ExternalIPStatus{},
			}
			parentExternalIP.GetStatus().SetAttached(true)
			mockExternalIPsServer2.addExternalIP(parentExternalIP)

			cr := &v1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      attachmentName,
					Namespace: attachmentNamespace,
					Labels: map[string]string{
						osacExternalIPAttachmentIDLabel: attachmentID,
					},
					Finalizers: []string{osacExternalIPAttachmentFeedbackFinalizer, osacExternalIPAttachmentFinalizer},
				},
				Spec: v1alpha1.ExternalIPAttachmentSpec{
					ExternalIP: "some-externalip",
				},
				Status: v1alpha1.ExternalIPAttachmentStatus{
					Phase: v1alpha1.ExternalIPAttachmentPhaseDeleting,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      attachmentName,
					Namespace: attachmentNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockAttachmentsServer.updates).To(HaveLen(1))
			Expect(mockAttachmentsServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_DELETING))

			Expect(mockExternalIPsServer2.updates).To(HaveLen(1))
			Expect(mockExternalIPsServer2.updates[0].GetStatus().GetAttached()).To(BeFalse())
		})

		It("should sync state=FAILED during deletion when phase is Failed", func() {
			attachment := &privatev1.ExternalIPAttachment{
				Id: attachmentID,
				Metadata: &privatev1.Metadata{
					Name: attachmentName,
				},
				Spec: &privatev1.ExternalIPAttachmentSpec{},
				Status: &privatev1.ExternalIPAttachmentStatus{
					State: privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_READY,
				},
			}
			attachment.GetSpec().SetExternalIp(parentExternalIPID)
			mockAttachmentsServer.addAttachment(attachment)

			parentExternalIP := &privatev1.ExternalIP{
				Id: parentExternalIPID,
				Metadata: &privatev1.Metadata{
					Name: "parent-externalip",
				},
				Spec:   &privatev1.ExternalIPSpec{},
				Status: &privatev1.ExternalIPStatus{},
			}
			mockExternalIPsServer2.addExternalIP(parentExternalIP)

			cr := &v1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      attachmentName,
					Namespace: attachmentNamespace,
					Labels: map[string]string{
						osacExternalIPAttachmentIDLabel: attachmentID,
					},
					Finalizers: []string{osacExternalIPAttachmentFeedbackFinalizer, osacExternalIPAttachmentFinalizer},
				},
				Spec: v1alpha1.ExternalIPAttachmentSpec{
					ExternalIP: "some-externalip",
				},
				Status: v1alpha1.ExternalIPAttachmentStatus{
					Phase: v1alpha1.ExternalIPAttachmentPhaseFailed,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      attachmentName,
					Namespace: attachmentNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockAttachmentsServer.updates).To(HaveLen(1))
			Expect(mockAttachmentsServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_FAILED))
		})

		It("should skip CRs without externalipattachment-uuid label", func() {
			cr := &v1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      attachmentName,
					Namespace: attachmentNamespace,
					Labels:    map[string]string{},
				},
				Spec: v1alpha1.ExternalIPAttachmentSpec{
					ExternalIP: "some-externalip",
				},
				Status: v1alpha1.ExternalIPAttachmentStatus{
					Phase: v1alpha1.ExternalIPAttachmentPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      attachmentName,
					Namespace: attachmentNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockAttachmentsServer.updates).To(BeEmpty())
		})

		It("should remove feedback finalizer when CR without ID label is being deleted", func() {
			cr := &v1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:       attachmentName,
					Namespace:  attachmentNamespace,
					Labels:     map[string]string{},
					Finalizers: []string{osacExternalIPAttachmentFeedbackFinalizer},
				},
				Spec: v1alpha1.ExternalIPAttachmentSpec{
					ExternalIP: "some-externalip",
				},
				Status: v1alpha1.ExternalIPAttachmentStatus{
					Phase: v1alpha1.ExternalIPAttachmentPhaseDeleting,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      attachmentName,
					Namespace: attachmentNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			updated := &v1alpha1.ExternalIPAttachment{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: attachmentName, Namespace: attachmentNamespace}, updated)
			Expect(err).To(HaveOccurred())
		})

		It("should remove feedback finalizer when attachment record is NotFound during deletion", func() {
			cr := &v1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      attachmentName,
					Namespace: attachmentNamespace,
					Labels: map[string]string{
						osacExternalIPAttachmentIDLabel: attachmentID,
					},
					Finalizers: []string{osacExternalIPAttachmentFeedbackFinalizer},
				},
				Spec: v1alpha1.ExternalIPAttachmentSpec{
					ExternalIP: "some-externalip",
				},
				Status: v1alpha1.ExternalIPAttachmentStatus{
					Phase: v1alpha1.ExternalIPAttachmentPhaseDeleting,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      attachmentName,
					Namespace: attachmentNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockAttachmentsServer.updates).To(BeEmpty())
			Expect(mockAttachmentsServer.signals).To(BeEmpty())

			updated := &v1alpha1.ExternalIPAttachment{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: attachmentName, Namespace: attachmentNamespace}, updated)
			Expect(err).To(HaveOccurred())
		})

		It("should return error when attachment record is NotFound but CR is not being deleted", func() {
			cr := &v1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      attachmentName,
					Namespace: attachmentNamespace,
					Labels: map[string]string{
						osacExternalIPAttachmentIDLabel: attachmentID,
					},
					Finalizers: []string{osacExternalIPAttachmentFeedbackFinalizer},
				},
				Spec: v1alpha1.ExternalIPAttachmentSpec{
					ExternalIP: "some-externalip",
				},
				Status: v1alpha1.ExternalIPAttachmentStatus{
					Phase: v1alpha1.ExternalIPAttachmentPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      attachmentName,
					Namespace: attachmentNamespace,
				},
			})
			Expect(errors.Is(err, ErrExternalIPAttachmentNotFound)).To(BeTrue())
		})

		It("should not update if status unchanged", func() {
			attachment := &privatev1.ExternalIPAttachment{
				Id: attachmentID,
				Metadata: &privatev1.Metadata{
					Name: attachmentName,
				},
				Spec: &privatev1.ExternalIPAttachmentSpec{},
				Status: &privatev1.ExternalIPAttachmentStatus{
					State: privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_READY,
				},
			}
			attachment.GetSpec().SetExternalIp(parentExternalIPID)
			mockAttachmentsServer.addAttachment(attachment)

			parentExternalIP := &privatev1.ExternalIP{
				Id: parentExternalIPID,
				Metadata: &privatev1.Metadata{
					Name: "parent-externalip",
				},
				Spec:   &privatev1.ExternalIPSpec{},
				Status: &privatev1.ExternalIPStatus{},
			}
			parentExternalIP.GetStatus().SetAttached(true)
			mockExternalIPsServer2.addExternalIP(parentExternalIP)

			cr := &v1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      attachmentName,
					Namespace: attachmentNamespace,
					Labels: map[string]string{
						osacExternalIPAttachmentIDLabel: attachmentID,
					},
					Finalizers: []string{osacExternalIPAttachmentFeedbackFinalizer},
				},
				Spec: v1alpha1.ExternalIPAttachmentSpec{
					ExternalIP: "some-externalip",
				},
				Status: v1alpha1.ExternalIPAttachmentStatus{
					Phase: v1alpha1.ExternalIPAttachmentPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      attachmentName,
					Namespace: attachmentNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockAttachmentsServer.updates).To(BeEmpty())
			Expect(mockExternalIPsServer2.updates).To(BeEmpty())
		})

		It("should remove feedback finalizer and signal when it is the last finalizer", func() {
			attachment := &privatev1.ExternalIPAttachment{
				Id: attachmentID,
				Metadata: &privatev1.Metadata{
					Name: attachmentName,
				},
				Spec: &privatev1.ExternalIPAttachmentSpec{},
				Status: &privatev1.ExternalIPAttachmentStatus{
					State: privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_READY,
				},
			}
			attachment.GetSpec().SetExternalIp(parentExternalIPID)
			mockAttachmentsServer.addAttachment(attachment)

			parentExternalIP := &privatev1.ExternalIP{
				Id: parentExternalIPID,
				Metadata: &privatev1.Metadata{
					Name: "parent-externalip",
				},
				Spec:   &privatev1.ExternalIPSpec{},
				Status: &privatev1.ExternalIPStatus{},
			}
			mockExternalIPsServer2.addExternalIP(parentExternalIP)

			cr := &v1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      attachmentName,
					Namespace: attachmentNamespace,
					Labels: map[string]string{
						osacExternalIPAttachmentIDLabel: attachmentID,
					},
					Finalizers: []string{osacExternalIPAttachmentFeedbackFinalizer},
				},
				Spec: v1alpha1.ExternalIPAttachmentSpec{
					ExternalIP: "some-externalip",
				},
				Status: v1alpha1.ExternalIPAttachmentStatus{
					Phase: v1alpha1.ExternalIPAttachmentPhaseDeleting,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      attachmentName,
					Namespace: attachmentNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockAttachmentsServer.updates).To(HaveLen(1))
			Expect(mockAttachmentsServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_DELETING))

			Expect(mockAttachmentsServer.signals).To(HaveLen(1))
			Expect(mockAttachmentsServer.signals[0]).To(Equal(attachmentID))

			updated := &v1alpha1.ExternalIPAttachment{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: attachmentName, Namespace: attachmentNamespace}, updated)
			Expect(err).To(HaveOccurred())
		})
	})
})

type mockExternalIPAttachmentsServer struct {
	privatev1.UnimplementedExternalIPAttachmentsServer
	mu          sync.Mutex
	attachments map[string]*privatev1.ExternalIPAttachment
	updates     []*privatev1.ExternalIPAttachment
	signals     []string
}

func (m *mockExternalIPAttachmentsServer) addAttachment(attachment *privatev1.ExternalIPAttachment) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attachments[attachment.GetId()] = attachment
}

func (m *mockExternalIPAttachmentsServer) Get(ctx context.Context, req *privatev1.ExternalIPAttachmentsGetRequest) (*privatev1.ExternalIPAttachmentsGetResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	attachment, ok := m.attachments[req.GetId()]
	if !ok {
		return nil, grpcstatus.Errorf(codes.NotFound, "object with identifier '%s' not found", req.GetId())
	}

	return &privatev1.ExternalIPAttachmentsGetResponse{
		Object: attachment,
	}, nil
}

func (m *mockExternalIPAttachmentsServer) Update(ctx context.Context, req *privatev1.ExternalIPAttachmentsUpdateRequest) (*privatev1.ExternalIPAttachmentsUpdateResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	attachment := req.GetObject()
	m.attachments[attachment.GetId()] = attachment
	m.updates = append(m.updates, attachment)

	return &privatev1.ExternalIPAttachmentsUpdateResponse{
		Object: attachment,
	}, nil
}

func (m *mockExternalIPAttachmentsServer) Signal(ctx context.Context, req *privatev1.ExternalIPAttachmentsSignalRequest) (*privatev1.ExternalIPAttachmentsSignalResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.signals = append(m.signals, req.GetId())
	return &privatev1.ExternalIPAttachmentsSignalResponse{}, nil
}

type mockExternalIPsServerForAttachment struct {
	privatev1.UnimplementedExternalIPsServer
	mu        sync.Mutex
	publicIPs map[string]*privatev1.ExternalIP
	updates   []*privatev1.ExternalIP
}

func (m *mockExternalIPsServerForAttachment) addExternalIP(publicIP *privatev1.ExternalIP) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.publicIPs[publicIP.GetId()] = publicIP
}

func (m *mockExternalIPsServerForAttachment) Get(ctx context.Context, req *privatev1.ExternalIPsGetRequest) (*privatev1.ExternalIPsGetResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	publicIP, ok := m.publicIPs[req.GetId()]
	if !ok {
		return nil, grpcstatus.Errorf(codes.NotFound, "object with identifier '%s' not found", req.GetId())
	}

	return &privatev1.ExternalIPsGetResponse{
		Object: publicIP,
	}, nil
}

func (m *mockExternalIPsServerForAttachment) Update(ctx context.Context, req *privatev1.ExternalIPsUpdateRequest) (*privatev1.ExternalIPsUpdateResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	publicIP := req.GetObject()
	m.publicIPs[publicIP.GetId()] = publicIP
	m.updates = append(m.updates, publicIP)

	return &privatev1.ExternalIPsUpdateResponse{
		Object: publicIP,
	}, nil
}
