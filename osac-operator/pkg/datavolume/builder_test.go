package datavolume_test

import (
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	cdiv1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/datavolume"
)

func TestBuildDataVolume(t *testing.T) {
	const imageURL = "docker://registry.example.org/os/boot:v1"

	tests := []struct {
		name       string
		imageRef   *osacv1alpha1.DiskImageReference
		resolved   *datavolume.ResolvedDiskImage
		sizeGiB    int64
		wantSize   string
		wantSource cdiv1beta1.DataVolumeSource
	}{
		{
			name:       "blank disk",
			sizeGiB:    7,
			wantSize:   "7Gi",
			wantSource: cdiv1beta1.DataVolumeSource{Blank: &cdiv1beta1.DataVolumeBlankImage{}},
		},
		{
			name:     "registry disk",
			imageRef: &osacv1alpha1.DiskImageReference{ID: "catalog-image"},
			resolved: &datavolume.ResolvedDiskImage{URL: imageURL},
			sizeGiB:  24,
			wantSize: "24Gi",
			wantSource: cdiv1beta1.DataVolumeSource{Registry: &cdiv1beta1.DataVolumeSourceRegistry{
				URL:        ptr.To(imageURL),
				PullMethod: ptr.To(cdiv1beta1.RegistryPullNode),
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			volume := &osacv1alpha1.Volume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hub-volume", Namespace: "hub", UID: "hub-uid",
					Labels:      map[string]string{"osac.openshift.io/volume-uuid": "fulfillment-id"},
					Annotations: map[string]string{"osac.openshift.io/tenant": "tenant-a"},
				},
				Spec: osacv1alpha1.VolumeSpec{
					Owner: &osacv1alpha1.VolumeOwner{
						Type: osacv1alpha1.VolumeOwnerTypeComputeInstance, Reference: "instance-id",
					},
					Target:     &osacv1alpha1.VolumeTarget{Namespace: "vm-namespace", DataVolumeName: "vm-disk"},
					DiskImage:  tc.imageRef,
					SizeGiB:    tc.sizeGiB,
					AccessMode: osacv1alpha1.VolumeAccessModeReadWriteMany,
				},
			}
			original := volume.DeepCopy()
			dv := datavolume.BuildDataVolume(volume, tc.resolved)

			g.Expect(dv.Namespace).To(Equal("vm-namespace"))
			g.Expect(dv.Name).To(Equal("vm-disk"))
			g.Expect(dv.Annotations).To(HaveKeyWithValue("osac.openshift.io/storage-volume-id", "fulfillment-id"))
			g.Expect(dv.Annotations).To(HaveKeyWithValue("osac.openshift.io/tenant", "tenant-a"))
			g.Expect(dv.Spec.Source).To(Equal(&tc.wantSource))
			g.Expect(dv.Spec.Storage).NotTo(BeNil())
			g.Expect(dv.Spec.Storage.AccessModes).To(ConsistOf(corev1.ReadWriteOnce))
			g.Expect(dv.Spec.Storage.Resources.Requests.Storage().Cmp(resource.MustParse(tc.wantSize))).To(BeZero())
			g.Expect(dv.Spec.Storage.StorageClassName).To(BeNil())

			dv.Annotations["osac.openshift.io/tenant"] = "another-tenant"
			g.Expect(volume).To(Equal(original))
			if tc.resolved != nil {
				*dv.Spec.Source.Registry.URL = "another-url"
				g.Expect(tc.resolved.URL).To(Equal(imageURL))
			}
		})
	}
}
