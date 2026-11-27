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

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// These specs pin the API server's evaluation of the Volume CRD's optional
// immutable fields against the real envtest API server. Fake clients do not
// run CEL validation, so only this suite can answer how Kubernetes treats a
// field that is absent from the old or the new object: a field-level
// self == oldSelf rule alone is skipped in that case, which allowed a
// remove-then-re-add sequence to swap an "immutable" field. The type-level
// VolumeSpec rules close that bypass by preserving field presence.
var _ = Describe("Volume CRD optional-field immutability", func() {
	var (
		testCtx context.Context
		vol     *osacv1alpha1.Volume
	)

	BeforeEach(func() {
		testCtx = context.TODO()
		vol = &osacv1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "vol-immutability-",
				Namespace:    "default",
			},
			Spec: osacv1alpha1.VolumeSpec{
				StorageTier: "gold",
				SizeGiB:     100,
				AccessMode:  osacv1alpha1.VolumeAccessModeReadWriteOnce,
				Topology: &osacv1alpha1.VolumeTopology{
					Segments: map[string]string{"osac.io/node": "worker-1"},
				},
				Owner: &osacv1alpha1.VolumeOwner{
					Type:      osacv1alpha1.VolumeOwnerTypeComputeInstance,
					Reference: "ci-original",
				},
				DiskImage: &osacv1alpha1.DiskImageReference{ID: "img-original"},
				Target: &osacv1alpha1.VolumeTarget{
					DiskKey:        "boot",
					Namespace:      "vm-ns",
					DataVolumeName: "vm-root-disk",
				},
			},
		}
		Expect(k8sClient.Create(testCtx, vol)).To(Succeed())
	})

	// cleanupVolume deletes a test volume, surfacing teardown failures so a
	// fixture cannot silently linger in the API server. A NotFound result is
	// the expected end state and is ignored.
	cleanupVolume := func(name string) {
		key := types.NamespacedName{Name: name, Namespace: "default"}
		existing := &osacv1alpha1.Volume{}
		err := k8sClient.Get(testCtx, key, existing)
		if apierrors.IsNotFound(err) {
			return
		}
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		existing.Finalizers = nil
		ExpectWithOffset(1, k8sClient.Update(testCtx, existing)).To(Succeed())
		ExpectWithOffset(1, k8sClient.Delete(testCtx, existing)).To(Succeed())
	}

	AfterEach(func() {
		cleanupVolume(vol.Name)
	})

	// fetch returns a fresh copy of the stored volume.
	fetch := func() *osacv1alpha1.Volume {
		fresh := &osacv1alpha1.Volume{}
		ExpectWithOffset(1, k8sClient.Get(testCtx,
			types.NamespacedName{Name: vol.Name, Namespace: vol.Namespace}, fresh)).To(Succeed())
		return fresh
	}

	It("rejects changing the owner to a different value", func() {
		updated := fetch()
		updated.Spec.Owner = &osacv1alpha1.VolumeOwner{
			Type:      osacv1alpha1.VolumeOwnerTypeComputeInstance,
			Reference: "ci-other",
		}
		err := k8sClient.Update(testCtx, updated)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("owner is immutable"))
	})

	DescribeTable("rejects removing an immutable optional field",
		func(field string, clear func(spec *osacv1alpha1.VolumeSpec)) {
			updated := fetch()
			clear(&updated.Spec)
			err := k8sClient.Update(testCtx, updated)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(field + " is immutable"))
		},
		Entry("topology", "topology", func(spec *osacv1alpha1.VolumeSpec) { spec.Topology = nil }),
		Entry("owner", "owner", func(spec *osacv1alpha1.VolumeSpec) { spec.Owner = nil }),
		Entry("diskImage", "diskImage", func(spec *osacv1alpha1.VolumeSpec) { spec.DiskImage = nil }),
		Entry("target", "target", func(spec *osacv1alpha1.VolumeSpec) { spec.Target = nil }),
	)

	It("rejects adding an owner to an ownerless volume", func() {
		ownerless := &osacv1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "vol-ownerless-",
				Namespace:    "default",
			},
			Spec: osacv1alpha1.VolumeSpec{
				StorageTier: "gold",
				SizeGiB:     100,
				AccessMode:  osacv1alpha1.VolumeAccessModeReadWriteOnce,
			},
		}
		Expect(k8sClient.Create(testCtx, ownerless)).To(Succeed())
		DeferCleanup(func() {
			cleanupVolume(ownerless.Name)
		})

		updated := &osacv1alpha1.Volume{}
		Expect(k8sClient.Get(testCtx,
			types.NamespacedName{Name: ownerless.Name, Namespace: ownerless.Namespace}, updated)).To(Succeed())
		updated.Spec.Owner = &osacv1alpha1.VolumeOwner{
			Type:      osacv1alpha1.VolumeOwnerTypeComputeInstance,
			Reference: "ci-added",
		}
		err := k8sClient.Update(testCtx, updated)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("owner is immutable"))
	})

	It("accepts an update that leaves the immutable spec fields unchanged", func() {
		updated := fetch()
		if updated.Labels == nil {
			updated.Labels = map[string]string{}
		}
		updated.Labels["test-marker"] = "unchanged-spec"
		Expect(k8sClient.Update(testCtx, updated)).To(Succeed())
	})
})
