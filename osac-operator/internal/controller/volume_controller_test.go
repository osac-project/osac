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
			Client:            k8sClient,
			Scheme:            k8sClient.Scheme(),
			mgr:               testMcManager,
			VolumeNamespace:   "default",
			VendorProvisioner: mockProv,
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

		// First reconcile adds finalizer
		_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})
		Expect(err).ToNot(HaveOccurred())

		// Second reconcile provisions
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

		// First reconcile adds finalizer
		_, _ = reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
			},
		})

		// Second reconcile attempts provisioning
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

	It("should not re-provision when already Ready", func() {
		Expect(k8sClient.Create(testCtx, vol)).To(Succeed())

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

	It("should return not-found gracefully when volume is already deleted", func() {
		_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "nonexistent", Namespace: "default"},
			},
		})
		Expect(err).ToNot(HaveOccurred())
	})

	It("should skip provisioning when no VendorProvisioner is configured", func() {
		reconciler.VendorProvisioner = nil

		Expect(k8sClient.Create(testCtx, vol)).To(Succeed())

		// Reconcile adds finalizer + sets Progressing
		for range 2 {
			_, _ = reconciler.Reconcile(testCtx, mcreconcile.Request{
				Request: reconcile.Request{
					NamespacedName: types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace},
				},
			})
		}

		updated := &osacv1alpha1.Volume{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}, updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(osacv1alpha1.VolumePhaseProgressing))
		Expect(updated.Status.VendorVolumeID).To(BeEmpty())
	})
})
