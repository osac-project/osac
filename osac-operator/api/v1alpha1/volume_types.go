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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VolumeSpec defines the desired state of Volume.
type VolumeSpec struct {
	// StorageTier is the name of the StorageTier that determines which backend
	// and protocol serve this volume.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="storageTier is immutable"
	StorageTier string `json:"storageTier"`

	// SizeGiB is the requested storage capacity in gibibytes.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="sizeGiB is immutable"
	SizeGiB int64 `json:"sizeGiB"`

	// AccessMode is the Kubernetes access mode for the volume
	// (e.g., "ReadWriteOnce", "ReadWriteMany").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="accessMode is immutable"
	AccessMode string `json:"accessMode"`

	// Cluster is the name of the cluster where the PVC that triggered this
	// volume exists. Empty for standalone volumes created via the API without
	// a cluster association.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="cluster is immutable"
	Cluster string `json:"cluster,omitempty"`

	// PVCRef is the reference to the PVC that triggered this volume creation.
	// Set by the CSI driver. Empty for API-driven volume creation.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="pvcRef is immutable"
	PVCRef *PVCReferenceType `json:"pvcRef,omitempty"`
}

// PVCReferenceType identifies the PVC that triggered volume creation.
type PVCReferenceType struct {
	// Name of the PersistentVolumeClaim.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Namespace of the PersistentVolumeClaim.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`

	// Cluster where the PersistentVolumeClaim exists.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Cluster string `json:"cluster"`
}

// PVReferenceType identifies the PV created for a volume.
type PVReferenceType struct {
	// Name of the PersistentVolume.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Cluster where the PersistentVolume exists.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Cluster string `json:"cluster"`
}

// VolumePhaseType is a valid value for .status.phase
type VolumePhaseType string

const (
	VolumePhaseProgressing VolumePhaseType = "Progressing"
	VolumePhaseReady       VolumePhaseType = "Ready"
	VolumePhaseFailed      VolumePhaseType = "Failed"
	VolumePhaseDeleting    VolumePhaseType = "Deleting"
)

// VolumeConditionType is a valid value for .status.conditions.type
type VolumeConditionType string

const (
	// VolumeConditionVendorProvisioned indicates whether the volume has been
	// provisioned on the vendor storage array.
	VolumeConditionVendorProvisioned VolumeConditionType = "VendorProvisioned"
)

// VolumeStatus defines the observed state of Volume.
type VolumeStatus struct {
	// Phase provides a single-value overview of the state of the Volume.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=Progressing;Ready;Failed;Deleting
	Phase VolumePhaseType `json:"phase,omitempty"`

	// Conditions holds an array of metav1.Condition that describe the state of the Volume.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// VendorVolumeID is the opaque identifier assigned by the vendor storage array.
	// Set by the Volume controller after vendor CSI CreateVolume succeeds.
	// +kubebuilder:validation:Optional
	VendorVolumeID string `json:"vendorVolumeID,omitempty"`

	// Backend is the name of the StorageBackend that serves this volume.
	// Resolved during tier resolution at creation time.
	// +kubebuilder:validation:Optional
	Backend string `json:"backend,omitempty"`

	// Protocol is the storage protocol used for this volume (e.g., NFS, BLOCK).
	// Resolved during tier resolution at creation time.
	// +kubebuilder:validation:Optional
	Protocol string `json:"protocol,omitempty"`

	// PVCRef is the operator-confirmed reference to the PVC on the tenant cluster.
	// +kubebuilder:validation:Optional
	PVCRef *PVCReferenceType `json:"pvcRef,omitempty"`

	// PVRef is the reference to the PV created for this volume on the tenant cluster.
	// +kubebuilder:validation:Optional
	PVRef *PVReferenceType `json:"pvRef,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vol
// +kubebuilder:printcolumn:name="Tier",type=string,JSONPath=`.spec.storageTier`
// +kubebuilder:printcolumn:name="Size",type=integer,JSONPath=`.spec.sizeGiB`
// +kubebuilder:printcolumn:name="Access",type=string,JSONPath=`.spec.accessMode`
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.cluster`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Backend",type=string,JSONPath=`.status.backend`,priority=1
// +kubebuilder:printcolumn:name="VendorID",type=string,JSONPath=`.status.vendorVolumeID`,priority=1

// Volume is the Schema for the volumes API.
type Volume struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// +required
	Spec VolumeSpec `json:"spec"`

	// +optional
	Status VolumeStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// VolumeList contains a list of Volume.
type VolumeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Volume `json:"items"`
}
