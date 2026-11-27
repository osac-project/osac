// Package datavolume maps VM disk requests to CDI DataVolumes.
package datavolume

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	cdiv1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const (
	volumeIDLabel    = "osac.openshift.io/volume-uuid"
	storageVolumeID  = "osac.openshift.io/storage-volume-id"
	tenantAnnotation = "osac.openshift.io/tenant"
)

// ResolvedDiskImage carries the full registry URL for a Volume's image reference.
type ResolvedDiskImage struct {
	URL string
}

// BuildDataVolume describes a VM disk at the Volume's required provisioning target.
// A Volume with a disk image requires its resolved image. For a blank disk, pass nil.
func BuildDataVolume(volume *osacv1alpha1.Volume, resolvedImage *ResolvedDiskImage) *cdiv1beta1.DataVolume {
	var source cdiv1beta1.DataVolumeSource
	if volume.Spec.DiskImage == nil {
		source.Blank = &cdiv1beta1.DataVolumeBlankImage{}
	} else {
		source.Registry = &cdiv1beta1.DataVolumeSourceRegistry{
			URL:        ptr.To(resolvedImage.URL),
			PullMethod: ptr.To(cdiv1beta1.RegistryPullNode),
		}
	}

	return &cdiv1beta1.DataVolume{
		TypeMeta: metav1.TypeMeta{
			APIVersion: cdiv1beta1.SchemeGroupVersion.String(),
			Kind:       "DataVolume",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      volume.Spec.Target.DataVolumeName,
			Namespace: volume.Spec.Target.Namespace,
			Annotations: map[string]string{
				storageVolumeID:  volume.Labels[volumeIDLabel],
				tenantAnnotation: volume.Annotations[tenantAnnotation],
			},
		},
		Spec: cdiv1beta1.DataVolumeSpec{
			Source: &source,
			Storage: &cdiv1beta1.StorageSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse(fmt.Sprintf("%dGi", volume.Spec.SizeGiB)),
					},
				},
			},
		},
	}
}
