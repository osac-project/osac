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

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
)

var _ = Describe("VolumeFeedbackController", func() {
	const (
		volName      = "test-vol"
		volNamespace = "test-namespace"
		volID        = "vol-123"
	)

	var (
		ctx        context.Context
		fakeK8s    client.Client
		mockServer *mockVolumesServer
		reconciler *VolumeFeedbackReconciler
		grpcServer *grpc.Server
		listener   *bufconn.Listener
		grpcConn   *grpc.ClientConn
	)

	BeforeEach(func() {
		ctx = context.Background()

		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		fakeK8s = fake.NewClientBuilder().WithScheme(scheme).Build()

		mockServer = &mockVolumesServer{
			volumes: make(map[string]*privatev1.Volume),
			updates: make([]*privatev1.Volume, 0),
			signals: make([]string, 0),
		}
		listener = bufconn.Listen(1024 * 1024)
		grpcServer = grpc.NewServer()
		privatev1.RegisterVolumesServer(grpcServer, mockServer)

		go func() {
			_ = grpcServer.Serve(listener)
		}()

		var err error
		grpcConn, err = grpc.NewClient("passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
				return listener.Dial()
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		Expect(err).NotTo(HaveOccurred())

		reconciler = NewVolumeFeedbackReconciler(fakeK8s, grpcConn, volNamespace)
	})

	AfterEach(func() {
		if grpcConn != nil {
			_ = grpcConn.Close()
		}
		if grpcServer != nil {
			grpcServer.Stop()
		}
		if listener != nil {
			_ = listener.Close()
		}
	})

	Context("phase-to-state mapping", func() {
		It("should sync Phase=Ready to state=AVAILABLE", func() {
			mockServer.addVolume(newRemoteVolume(volID, privatev1.VolumeState_VOLUME_STATE_CREATING))

			cr := newVolumeFeedbackCR(volName, volNamespace, volID, v1alpha1.VolumePhaseReady, nil)
			cr.Status.VendorVolumeID = "vast-001"
			cr.Status.Backend = "vast-backend"
			cr.Status.Protocol = v1alpha1.VolumeProtocolBlock
			Expect(fakeK8s.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: volName, Namespace: volNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			updated := mockServer.updates[0]
			Expect(updated.GetStatus().GetState()).To(Equal(privatev1.VolumeState_VOLUME_STATE_AVAILABLE))
			Expect(updated.GetStatus().GetVendorVolumeId()).To(Equal("vast-001"))
			Expect(updated.GetStatus().GetBackend()).To(Equal("vast-backend"))

			// Signal should not be called on non-delete reconciles
			Expect(mockServer.signals).To(BeEmpty())

			updatedCR := &v1alpha1.Volume{}
			Expect(fakeK8s.Get(ctx, types.NamespacedName{Name: volName, Namespace: volNamespace}, updatedCR)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updatedCR, osacVolumeFeedbackFinalizer)).To(BeTrue())
		})

		It("should sync Phase=Progressing to state=CREATING", func() {
			mockServer.addVolume(newRemoteVolume(volID, privatev1.VolumeState_VOLUME_STATE_AVAILABLE))

			cr := newVolumeFeedbackCR(volName, volNamespace, volID, v1alpha1.VolumePhaseProgressing, nil)
			Expect(fakeK8s.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: volName, Namespace: volNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.VolumeState_VOLUME_STATE_CREATING))
			Expect(mockServer.signals).To(BeEmpty())
		})

		It("should sync Phase=Failed to state=FAILED", func() {
			mockServer.addVolume(newRemoteVolume(volID, privatev1.VolumeState_VOLUME_STATE_CREATING))

			cr := newVolumeFeedbackCR(volName, volNamespace, volID, v1alpha1.VolumePhaseFailed, nil)
			Expect(fakeK8s.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: volName, Namespace: volNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.VolumeState_VOLUME_STATE_FAILED))
			Expect(mockServer.signals).To(BeEmpty())
		})
	})

	Context("vendor field syncing", func() {
		It("should sync vendorVolumeID, backend, and protocol to remote", func() {
			mockServer.addVolume(newRemoteVolume(volID, privatev1.VolumeState_VOLUME_STATE_CREATING))

			cr := newVolumeFeedbackCR(volName, volNamespace, volID, v1alpha1.VolumePhaseReady, nil)
			cr.Status.VendorVolumeID = "netapp-vol-42"
			cr.Status.Backend = "netapp-cluster-1"
			cr.Status.Protocol = v1alpha1.VolumeProtocolNFS
			Expect(fakeK8s.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: volName, Namespace: volNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			updated := mockServer.updates[0]
			Expect(updated.GetStatus().GetVendorVolumeId()).To(Equal("netapp-vol-42"))
			Expect(updated.GetStatus().GetBackend()).To(Equal("netapp-cluster-1"))
			Expect(updated.GetStatus().GetProtocol()).To(Equal(privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS))
		})

		It("should map Block protocol correctly", func() {
			mockServer.addVolume(newRemoteVolume(volID, privatev1.VolumeState_VOLUME_STATE_CREATING))

			cr := newVolumeFeedbackCR(volName, volNamespace, volID, v1alpha1.VolumePhaseReady, nil)
			cr.Status.VendorVolumeID = "vast-001"
			cr.Status.Backend = "vast-backend"
			cr.Status.Protocol = v1alpha1.VolumeProtocolBlock
			Expect(fakeK8s.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: volName, Namespace: volNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetProtocol()).To(Equal(privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK))
		})

		It("should not overwrite remote fields when CR fields are empty", func() {
			remote := newRemoteVolume(volID, privatev1.VolumeState_VOLUME_STATE_CREATING)
			remote.GetStatus().SetVendorVolumeId("existing-id")
			remote.GetStatus().SetBackend("existing-backend")
			mockServer.addVolume(remote)

			cr := newVolumeFeedbackCR(volName, volNamespace, volID, v1alpha1.VolumePhaseProgressing, nil)
			Expect(fakeK8s.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: volName, Namespace: volNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			// Progressing maps to CREATING, which the remote already reports, and the CR
			// carries no vendor fields. Nothing changes, so no Update RPC is sent.
			Expect(mockServer.updates).To(BeEmpty())
		})
	})

	Context("label and identity handling", func() {
		It("should skip CRs without volume-uuid label", func() {
			cr := &v1alpha1.Volume{
				ObjectMeta: metav1.ObjectMeta{
					Name:      volName,
					Namespace: volNamespace,
					Labels:    map[string]string{},
				},
				Spec: v1alpha1.VolumeSpec{
					StorageTier: "gold",
					SizeGiB:     100,
					AccessMode:  v1alpha1.VolumeAccessModeReadWriteOnce,
				},
				Status: v1alpha1.VolumeStatus{
					Phase: v1alpha1.VolumePhaseReady,
				},
			}
			Expect(fakeK8s.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: volName, Namespace: volNamespace},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockServer.updates).To(BeEmpty())
		})
	})

	Context("deletion handling", func() {
		It("should sync Phase=Deleting to state=DELETING during deletion", func() {
			mockServer.addVolume(newRemoteVolume(volID, privatev1.VolumeState_VOLUME_STATE_AVAILABLE))

			cr := newVolumeFeedbackCR(volName, volNamespace, volID, v1alpha1.VolumePhaseDeleting,
				[]string{osacVolumeFeedbackFinalizer, osacVolumeFinalizer})
			Expect(fakeK8s.Create(ctx, cr)).To(Succeed())
			Expect(fakeK8s.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: volName, Namespace: volNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.VolumeState_VOLUME_STATE_DELETING))

			// Signal should NOT be called when other finalizers remain
			Expect(mockServer.signals).To(BeEmpty())

			// Feedback finalizer should remain (other finalizers still present)
			updatedCR := &v1alpha1.Volume{}
			Expect(fakeK8s.Get(ctx, types.NamespacedName{Name: volName, Namespace: volNamespace}, updatedCR)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updatedCR, osacVolumeFeedbackFinalizer)).To(BeTrue())
		})

		It("should sync Phase=Failed to state=FAILED during deletion", func() {
			mockServer.addVolume(newRemoteVolume(volID, privatev1.VolumeState_VOLUME_STATE_AVAILABLE))

			cr := newVolumeFeedbackCR(volName, volNamespace, volID, v1alpha1.VolumePhaseFailed,
				[]string{osacVolumeFeedbackFinalizer, osacVolumeFinalizer})
			Expect(fakeK8s.Create(ctx, cr)).To(Succeed())
			Expect(fakeK8s.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: volName, Namespace: volNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.VolumeState_VOLUME_STATE_FAILED))
		})

		It("should remove finalizer and signal when feedback finalizer is the last one", func() {
			mockServer.addVolume(newRemoteVolume(volID, privatev1.VolumeState_VOLUME_STATE_AVAILABLE))

			cr := newVolumeFeedbackCR(volName, volNamespace, volID, v1alpha1.VolumePhaseDeleting,
				[]string{osacVolumeFeedbackFinalizer})
			Expect(fakeK8s.Create(ctx, cr)).To(Succeed())
			Expect(fakeK8s.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: volName, Namespace: volNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(HaveLen(1))
			Expect(mockServer.updates[0].GetStatus().GetState()).To(Equal(privatev1.VolumeState_VOLUME_STATE_DELETING))
			Expect(mockServer.signals).To(HaveLen(1))
			Expect(mockServer.signals[0]).To(Equal(volID))

			// CR should be gone (last finalizer removed)
			updatedCR := &v1alpha1.Volume{}
			err = fakeK8s.Get(ctx, types.NamespacedName{Name: volName, Namespace: volNamespace}, updatedCR)
			Expect(err).To(HaveOccurred())
		})

		It("should still remove finalizer when signal fails", func() {
			mockServer.addVolume(newRemoteVolume(volID, privatev1.VolumeState_VOLUME_STATE_AVAILABLE))
			mockServer.signalErr = fmt.Errorf("signal unavailable")

			cr := newVolumeFeedbackCR(volName, volNamespace, volID, v1alpha1.VolumePhaseDeleting,
				[]string{osacVolumeFeedbackFinalizer})
			Expect(fakeK8s.Create(ctx, cr)).To(Succeed())
			Expect(fakeK8s.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: volName, Namespace: volNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			// CR should still be gone (finalizer removed despite signal failure)
			updatedCR := &v1alpha1.Volume{}
			err = fakeK8s.Get(ctx, types.NamespacedName{Name: volName, Namespace: volNamespace}, updatedCR)
			Expect(err).To(HaveOccurred())
		})

		It("should remove feedback finalizer when remote record is NotFound during deletion", func() {
			// Don't add volume to mock server (simulates archived record)
			cr := newVolumeFeedbackCR(volName, volNamespace, volID, v1alpha1.VolumePhaseDeleting,
				[]string{osacVolumeFeedbackFinalizer})
			Expect(fakeK8s.Create(ctx, cr)).To(Succeed())
			Expect(fakeK8s.Delete(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: volName, Namespace: volNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockServer.updates).To(BeEmpty())
			Expect(mockServer.signals).To(BeEmpty())

			// CR should be gone
			updatedCR := &v1alpha1.Volume{}
			err = fakeK8s.Get(ctx, types.NamespacedName{Name: volName, Namespace: volNamespace}, updatedCR)
			Expect(err).To(HaveOccurred())
		})
	})

	Context("idempotency", func() {
		It("should not call Update when remote state already matches", func() {
			remote := newRemoteVolume(volID, privatev1.VolumeState_VOLUME_STATE_AVAILABLE)
			remote.GetStatus().SetVendorVolumeId("vast-001")
			remote.GetStatus().SetBackend("vast-backend")
			mockServer.addVolume(remote)

			cr := newVolumeFeedbackCR(volName, volNamespace, volID, v1alpha1.VolumePhaseReady, nil)
			cr.Status.VendorVolumeID = "vast-001"
			cr.Status.Backend = "vast-backend"
			// Pre-seed the feedback finalizer so the reconciler doesn't add it (which triggers an update)
			cr.Finalizers = []string{osacVolumeFeedbackFinalizer}
			Expect(fakeK8s.Create(ctx, cr)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: volName, Namespace: volNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			// No Update RPC called since remote already matches
			Expect(mockServer.updates).To(BeEmpty())
		})
	})
})

// Helper to create a Volume CR for feedback controller tests.
func newVolumeFeedbackCR(name, namespace, id string, phase v1alpha1.VolumePhaseType, finalizers []string) *v1alpha1.Volume {
	cr := &v1alpha1.Volume{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				osacVolumeIDLabel: id,
			},
		},
		Spec: v1alpha1.VolumeSpec{
			StorageTier: "gold",
			SizeGiB:     100,
			AccessMode:  v1alpha1.VolumeAccessModeReadWriteOnce,
		},
		Status: v1alpha1.VolumeStatus{
			Phase: phase,
		},
	}
	if len(finalizers) > 0 {
		cr.Finalizers = finalizers
	}
	return cr
}

// Helper to create a remote Volume proto for the mock server.
func newRemoteVolume(id string, state privatev1.VolumeState) *privatev1.Volume {
	return privatev1.Volume_builder{
		Id: id,
		Metadata: privatev1.Metadata_builder{
			Name: "test-vol",
		}.Build(),
		Spec: privatev1.VolumeSpec_builder{
			StorageTier: "gold",
			SizeGib:     100,
			AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
		}.Build(),
		Status: privatev1.VolumeStatus_builder{
			State: state,
		}.Build(),
	}.Build()
}

// mockVolumesServer implements privatev1.VolumesServer for testing.
type mockVolumesServer struct {
	privatev1.UnimplementedVolumesServer
	mu        sync.Mutex
	volumes   map[string]*privatev1.Volume
	updates   []*privatev1.Volume
	signals   []string
	signalErr error
}

func (m *mockVolumesServer) addVolume(vol *privatev1.Volume) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.volumes[vol.GetId()] = vol
}

func (m *mockVolumesServer) Get(_ context.Context, req *privatev1.VolumesGetRequest) (*privatev1.VolumesGetResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	vol, ok := m.volumes[req.GetId()]
	if !ok {
		return nil, grpcstatus.Errorf(codes.NotFound, "object with identifier '%s' not found", req.GetId())
	}

	return privatev1.VolumesGetResponse_builder{
		Object: vol,
	}.Build(), nil
}

func (m *mockVolumesServer) Update(_ context.Context, req *privatev1.VolumesUpdateRequest) (*privatev1.VolumesUpdateResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	vol := req.GetObject()
	m.volumes[vol.GetId()] = vol
	m.updates = append(m.updates, vol)

	return privatev1.VolumesUpdateResponse_builder{
		Object: vol,
	}.Build(), nil
}

func (m *mockVolumesServer) Signal(_ context.Context, req *privatev1.VolumesSignalRequest) (*privatev1.VolumesSignalResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.signals = append(m.signals, req.GetId())

	if m.signalErr != nil {
		return nil, m.signalErr
	}
	return &privatev1.VolumesSignalResponse{}, nil
}
