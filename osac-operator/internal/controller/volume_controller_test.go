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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = Describe("VolumeReconciler", func() {
	var (
		reconciler *VolumeReconciler
		mockProv   *MockVendorProvisioner
		testCtx    context.Context
		vol        *osacv1alpha1.Volume
	)

	BeforeEach(func() {
		testCtx = context.TODO()
		mockProv = NewMockVendorProvisioner()
		reconciler = &VolumeReconciler{
			Client:          k8sClient,
			Scheme:          k8sClient.Scheme(),
			mgr:             testMcManager,
			VolumeNamespace: "default",
			VendorProvisioners: VendorProvisionerRegistry{
				"vast-primary": mockProv,
			},
		}

		vol = &osacv1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-vol",
				Namespace: "default",
			},
			Spec: osacv1alpha1.VolumeSpec{
				StorageTier: "gold",
				SizeGiB:     100,
				AccessMode:  osacv1alpha1.VolumeAccessModeReadWriteOnce,
			},
		}
	})

	// stampBackendProtocol simulates the fulfillment-service stamping
	// status.backend and status.protocol on the Volume CR after creation.
	stampBackendProtocol := func(v *osacv1alpha1.Volume) {
		fresh := &osacv1alpha1.Volume{}
		ExpectWithOffset(1, k8sClient.Get(testCtx, types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, fresh)).To(Succeed())
		fresh.Status.Backend = "vast-primary"
		fresh.Status.Provider = "vast-primary"
		fresh.Status.Protocol = osacv1alpha1.VolumeProtocolBlock
		ExpectWithOffset(1, k8sClient.Status().Update(testCtx, fresh)).To(Succeed())
	}

	AfterEach(func() {
		volKey := types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}
		existingVol := &osacv1alpha1.Volume{}
		if err := k8sClient.Get(testCtx, volKey, existingVol); err == nil {
			existingVol.Finalizers = nil
			_ = k8sClient.Update(testCtx, existingVol)
			_ = k8sClient.Delete(testCtx, existingVol)
		}
	})

	It("should add finalizer on first reconcile", func() {
		Expect(k8sClient.Create(testCtx, vol)).To(Succeed())

		_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		Expect(err).ToNot(HaveOccurred())

		updated := &osacv1alpha1.Volume{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}, updated)).To(Succeed())
		Expect(updated.Finalizers).To(ContainElement(osacVolumeFinalizer))
	})

	It("should reach Ready on first reconcile when the mock provisioner succeeds", func() {
		Expect(k8sClient.Create(testCtx, vol)).To(Succeed())
		stampBackendProtocol(vol)

		_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		Expect(err).ToNot(HaveOccurred())

		updated := &osacv1alpha1.Volume{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}, updated)).To(Succeed())
		// Phase should be Ready because the mock provisioner succeeds immediately
		Expect(updated.Status.Phase).To(Equal(osacv1alpha1.VolumePhaseReady))
	})

	It("should provision volume and set status fields on success", func() {
		Expect(k8sClient.Create(testCtx, vol)).To(Succeed())
		stampBackendProtocol(vol)

		// A single reconcile adds the finalizer and provisions to Ready:
		// handleUpdate adds the finalizer, then falls through to
		// handleProvisioning in the same pass.
		_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		Expect(err).ToNot(HaveOccurred())

		// A second reconcile is idempotent (phase is already Ready).
		_, err = reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		Expect(err).ToNot(HaveOccurred())

		updated := &osacv1alpha1.Volume{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}, updated)).To(Succeed())

		Expect(updated.Status.Phase).To(Equal(osacv1alpha1.VolumePhaseReady))
		Expect(updated.Status.VendorVolumeID).To(HavePrefix("mock-"))
		Expect(updated.Status.Backend).To(Equal("mock-backend"))
		Expect(updated.Status.Protocol).To(Equal(osacv1alpha1.VolumeProtocolBlock))
		Expect(mockProv.CreateCallCount()).To(BeNumerically(">=", 1))

		cond := apimeta.FindStatusCondition(updated.Status.Conditions, string(osacv1alpha1.VolumeConditionVendorProvisioned))
		Expect(cond).ToNot(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("Provisioned"))
	})

	It("should set phase to Failed when vendor provisioning fails", func() {
		mockProv.CreateErr = fmt.Errorf("vendor array unreachable")

		Expect(k8sClient.Create(testCtx, vol)).To(Succeed())
		stampBackendProtocol(vol)

		// A single reconcile adds the finalizer and attempts provisioning,
		// which fails and transitions the phase to Failed (no error returned;
		// the failure is recorded in the condition).
		_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		Expect(err).ToNot(HaveOccurred())

		updated := &osacv1alpha1.Volume{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}, updated)).To(Succeed())

		Expect(updated.Status.Phase).To(Equal(osacv1alpha1.VolumePhaseFailed))

		cond := apimeta.FindStatusCondition(updated.Status.Conditions, string(osacv1alpha1.VolumeConditionVendorProvisioned))
		Expect(cond).ToNot(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("ProvisioningFailed"))
		Expect(cond.Message).To(ContainSubstring("vendor array unreachable"))
	})

	It("does not auto-retry provisioning once Failed (terminal phase)", func() {
		mockProv.CreateErr = fmt.Errorf("vendor array unreachable")

		Expect(k8sClient.Create(testCtx, vol)).To(Succeed())
		stampBackendProtocol(vol)

		// First reconcile provisions, fails, and lands in Failed.
		_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		Expect(err).ToNot(HaveOccurred())

		updated := &osacv1alpha1.Volume{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}, updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(osacv1alpha1.VolumePhaseFailed))
		countAfterFailure := mockProv.CreateCallCount()

		// Clear the vendor error: a later reconcile must NOT retry provisioning,
		// because Failed is terminal (recovery requires recreating the Volume).
		mockProv.CreateErr = nil
		_, err = reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		Expect(err).ToNot(HaveOccurred())

		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}, updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(osacv1alpha1.VolumePhaseFailed))
		Expect(mockProv.CreateCallCount()).To(Equal(countAfterFailure))
	})

	It("should not re-provision when already Ready", func() {
		Expect(k8sClient.Create(testCtx, vol)).To(Succeed())
		stampBackendProtocol(vol)

		// Reconcile until Ready
		for range 3 {
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
				Request: reconcile.Request{
					NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
				},
			})
			Expect(err).ToNot(HaveOccurred())
		}

		countBefore := mockProv.CreateCallCount()

		// One more reconcile should be a no-op
		_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(mockProv.CreateCallCount()).To(Equal(countBefore))
	})

	It("should handle deletion with vendor deprovisioning", func() {
		Expect(k8sClient.Create(testCtx, vol)).To(Succeed())
		stampBackendProtocol(vol)

		// Reconcile to Ready
		for range 3 {
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
				Request: reconcile.Request{
					NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
				},
			})
			Expect(err).ToNot(HaveOccurred())
		}

		// Delete
		Expect(k8sClient.Delete(testCtx, vol)).To(Succeed())

		_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(mockProv.DeleteCallCount()).To(BeNumerically(">=", 1))

		// Volume should be gone after finalizer removal
		deleted := &osacv1alpha1.Volume{}
		err = k8sClient.Get(testCtx, types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}, deleted)
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("should return error and keep finalizer when vendor deprovisioning fails", func() {
		Expect(k8sClient.Create(testCtx, vol)).To(Succeed())
		stampBackendProtocol(vol)

		// Reconcile to Ready
		for range 3 {
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
				Request: reconcile.Request{
					NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
				},
			})
			Expect(err).ToNot(HaveOccurred())
		}

		// Inject vendor delete failure
		mockProv.DeleteErr = fmt.Errorf("storage array unavailable")

		Expect(k8sClient.Delete(testCtx, vol)).To(Succeed())

		_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		Expect(err).To(HaveOccurred())

		// Finalizer must still be present — the volume was not deprovisioned
		still := &osacv1alpha1.Volume{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}, still)).To(Succeed())
		Expect(still.Finalizers).To(ContainElement(osacVolumeFinalizer))
		// Phase must be Deleting so the feedback controller syncs VOLUME_STATE_DELETING
		Expect(still.Status.Phase).To(Equal(osacv1alpha1.VolumePhaseDeleting))
	})

	It("should keep finalizer when provisioned but no VendorProvisioner is configured", func() {
		Expect(k8sClient.Create(testCtx, vol)).To(Succeed())
		stampBackendProtocol(vol)

		// Reconcile to Ready so the volume has a VendorVolumeID.
		for range 3 {
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
				Request: reconcile.Request{
					NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
				},
			})
			Expect(err).ToNot(HaveOccurred())
		}

		// Simulate a misconfigured restart: provisioner is gone but the volume
		// was already provisioned on the array.
		reconciler.VendorProvisioners = nil

		Expect(k8sClient.Delete(testCtx, vol)).To(Succeed())

		_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		// Must error and retain the finalizer so the backend volume is not leaked.
		Expect(err).To(HaveOccurred())

		still := &osacv1alpha1.Volume{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}, still)).To(Succeed())
		Expect(still.Finalizers).To(ContainElement(osacVolumeFinalizer))
	})

	It("should pass tenant, tier, protocol, and backend through to the vendor create request", func() {
		// The controller derives the vendor create request from the CR: tenant
		// from the annotation, tier from the spec, backend/protocol from the
		// status stamped by fulfillment-service. A regression that drops or
		// mis-maps any of these would silently provision on the wrong
		// backend/tenant, so assert every field the controller populates.
		vol.Annotations = map[string]string{osacTenantKey: "acme"}
		vol.Spec.Topology = &osacv1alpha1.VolumeTopology{
			Segments: map[string]string{"osac.io/node": "worker-1"},
		}
		Expect(k8sClient.Create(testCtx, vol)).To(Succeed())

		// Stamp the backend/protocol that fulfillment-service resolves before
		// the operator provisions.
		vol.Status.Backend = "vast-primary"
		vol.Status.Provider = "vast-primary"
		vol.Status.Protocol = osacv1alpha1.VolumeProtocolBlock
		Expect(k8sClient.Status().Update(testCtx, vol)).To(Succeed())

		_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		Expect(err).ToNot(HaveOccurred())

		Expect(mockProv.CreateCallCount()).To(BeNumerically(">=", 1))
		req := mockProv.LastCreateReq
		Expect(req.Name).To(Equal("test-vol"))
		Expect(req.Provider).To(Equal("vast-primary"))
		Expect(req.Backend).To(Equal("vast-primary"))
		Expect(req.Tenant).To(Equal("acme"))
		Expect(req.Tier).To(Equal("gold"))
		Expect(req.SizeGiB).To(Equal(int64(100)))
		Expect(req.AccessMode).To(Equal(osacv1alpha1.VolumeAccessModeReadWriteOnce))
		Expect(req.Protocol).To(Equal(osacv1alpha1.VolumeProtocolBlock))
		Expect(req.Topology.Segments).To(Equal(map[string]string{"osac.io/node": "worker-1"}))
	})

	It("should pass tenant, backend, and vendor volume ID through to the vendor delete request", func() {
		vol.Annotations = map[string]string{osacTenantKey: "acme"}
		Expect(k8sClient.Create(testCtx, vol)).To(Succeed())
		stampBackendProtocol(vol)

		// Reconcile to Ready so the volume has a VendorVolumeID and backend.
		for range 3 {
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
				Request: reconcile.Request{
					NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
				},
			})
			Expect(err).ToNot(HaveOccurred())
		}

		provisioned := &osacv1alpha1.Volume{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}, provisioned)).To(Succeed())
		Expect(provisioned.Status.VendorVolumeID).To(HavePrefix("mock-"))

		Expect(k8sClient.Delete(testCtx, vol)).To(Succeed())

		_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		Expect(err).ToNot(HaveOccurred())

		Expect(mockProv.DeleteCallCount()).To(BeNumerically(">=", 1))
		req := mockProv.LastDeleteReq
		Expect(req.Tenant).To(Equal("acme"))
		Expect(req.Provider).To(Equal(provisioned.Status.Provider))
		Expect(req.Backend).To(Equal(provisioned.Status.Backend))
		Expect(req.VendorVolumeID).To(Equal(provisioned.Status.VendorVolumeID))
	})

	It("dispatches by provider and does not call another registered provider", func() {
		otherProv := NewMockVendorProvisioner()
		reconciler.VendorProvisioners = VendorProvisionerRegistry{
			"vast-primary": mockProv,
			"other":        otherProv,
		}
		vol.Spec.Topology = &osacv1alpha1.VolumeTopology{
			Segments: map[string]string{"osac.io/node": "worker-2"},
		}
		Expect(k8sClient.Create(testCtx, vol)).To(Succeed())

		stamped := &osacv1alpha1.Volume{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}, stamped)).To(Succeed())
		stamped.Status.Provider = "other"
		stamped.Status.Backend = "other-backend"
		stamped.Status.Protocol = osacv1alpha1.VolumeProtocolBlock
		Expect(k8sClient.Status().Update(testCtx, stamped)).To(Succeed())

		_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		Expect(err).ToNot(HaveOccurred())

		Expect(otherProv.CreateCallCount()).To(Equal(int64(1)))
		Expect(mockProv.CreateCallCount()).To(Equal(int64(0)))
		Expect(otherProv.LastCreateReq.Provider).To(Equal("other"))
		Expect(otherProv.LastCreateReq.Backend).To(Equal("other-backend"))
		Expect(otherProv.LastCreateReq.Topology.Segments).To(Equal(map[string]string{"osac.io/node": "worker-2"}))
	})

	It("fails clearly when the resolved provider is not registered", func() {
		Expect(k8sClient.Create(testCtx, vol)).To(Succeed())
		stampBackendProtocol(vol)

		stamped := &osacv1alpha1.Volume{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}, stamped)).To(Succeed())
		stamped.Status.Provider = "netapp"
		Expect(k8sClient.Status().Update(testCtx, stamped)).To(Succeed())

		_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		Expect(err).ToNot(HaveOccurred())

		updated := &osacv1alpha1.Volume{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}, updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(osacv1alpha1.VolumePhaseFailed))
		condition := apimeta.FindStatusCondition(updated.Status.Conditions, string(osacv1alpha1.VolumeConditionVendorProvisioned))
		Expect(condition).ToNot(BeNil())
		Expect(condition.Reason).To(Equal("ProviderNotImplemented"))
		Expect(condition.Message).To(ContainSubstring(`provider "netapp" is not implemented`))
		Expect(mockProv.CreateCallCount()).To(Equal(int64(0)))
	})

	It("should stay in Progressing (not crash) when no VendorProvisioner is configured", func() {
		// Most setups (LVMS/dev) run with no vendor backend configured. The
		// controller must not dereference a nil provisioner; it leaves the volume
		// in Progressing and does not error, so the operator stays healthy.
		reconciler.VendorProvisioners = nil

		Expect(k8sClient.Create(testCtx, vol)).To(Succeed())
		stampBackendProtocol(vol)

		for range 2 {
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
				Request: reconcile.Request{
					NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
				},
			})
			Expect(err).ToNot(HaveOccurred())
		}

		updated := &osacv1alpha1.Volume{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}, updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(osacv1alpha1.VolumePhaseProgressing))
		Expect(updated.Status.VendorVolumeID).To(BeEmpty())
	})

	It("should delete cleanly when never provisioned and no VendorProvisioner is configured", func() {
		// A volume that never provisioned has no VendorVolumeID, so deletion must
		// remove the finalizer without needing a provisioner — nothing leaks on
		// the array because nothing was created.
		reconciler.VendorProvisioners = nil

		Expect(k8sClient.Create(testCtx, vol)).To(Succeed())
		stampBackendProtocol(vol)
		_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		Expect(err).ToNot(HaveOccurred())

		Expect(k8sClient.Delete(testCtx, vol)).To(Succeed())
		_, err = reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		Expect(err).ToNot(HaveOccurred())

		deleted := &osacv1alpha1.Volume{}
		err = k8sClient.Get(testCtx, types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}, deleted)
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("should return not-found gracefully when volume is already deleted", func() {
		_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "nonexistent", Namespace: "default"},
			},
		})
		Expect(err).ToNot(HaveOccurred())
	})

	It("should requeue without writing status when backend is empty", func() {
		Expect(k8sClient.Create(testCtx, vol)).To(Succeed())

		// First reconcile adds finalizer but backend/protocol are empty so it
		// should requeue without setting Phase — no status mutations at all.
		res, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))

		updated := &osacv1alpha1.Volume{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}, updated)).To(Succeed())
		Expect(updated.Status.Phase).To(BeEmpty())
		Expect(mockProv.CreateCallCount()).To(Equal(int64(0)))
	})

	It("should requeue without writing status when protocol is empty", func() {
		Expect(k8sClient.Create(testCtx, vol)).To(Succeed())

		// Stamp only backend, leave protocol empty.
		vol.Status.Backend = "vast-primary"
		Expect(k8sClient.Status().Update(testCtx, vol)).To(Succeed())

		res, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))

		updated := &osacv1alpha1.Volume{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}, updated)).To(Succeed())
		Expect(updated.Status.Phase).To(BeEmpty())
		Expect(mockProv.CreateCallCount()).To(Equal(int64(0)))
	})

	It("should provision once backend and protocol are populated", func() {
		Expect(k8sClient.Create(testCtx, vol)).To(Succeed())

		// First reconcile: backend/protocol empty → requeue.
		res, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))
		Expect(mockProv.CreateCallCount()).To(Equal(int64(0)))

		// Simulate FS stamping backend/protocol.
		updated := &osacv1alpha1.Volume{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}, updated)).To(Succeed())
		updated.Status.Backend = "vast-primary"
		updated.Status.Protocol = osacv1alpha1.VolumeProtocolBlock
		Expect(k8sClient.Status().Update(testCtx, updated)).To(Succeed())

		// Second reconcile: backend/protocol present → provisions.
		_, err = reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(mockProv.CreateCallCount()).To(BeNumerically(">=", 1))

		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}, updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(osacv1alpha1.VolumePhaseReady))
	})

})
