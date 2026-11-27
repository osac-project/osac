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

// VolumeTopology carries CSI-style placement segments for a volume request.
type VolumeTopology struct {
	// Segments is a CSI-style topology map (e.g. {"osac.io/node": "worker-1"}).
	// Keys mirror CSI topology keys; values are scheduler-resolved.
	Segments map[string]string `json:"segments,omitempty"`
}

// VolumeOwnerType identifies the kind of workload that owns a volume.
// +kubebuilder:validation:Enum=ComputeInstance
type VolumeOwnerType string

const (
	// VolumeOwnerTypeComputeInstance marks a volume owned by a ComputeInstance.
	VolumeOwnerTypeComputeInstance VolumeOwnerType = "ComputeInstance"
)

// VolumeOwner identifies the fulfillment workload that owns this volume.
type VolumeOwner struct {
	// Type is the kind of workload that owns the volume.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=ComputeInstance
	Type VolumeOwnerType `json:"type"`

	// Reference is the fulfillment record identifier of the owning workload.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Reference string `json:"reference"`
}

// DiskImageReference identifies a DiskImage catalog object that a volume is
// provisioned from. It follows the OSAC fullResourceReference standard and
// is copied verbatim from the ComputeInstance spec.
type DiskImageReference struct {
	// ID is the fulfillment record identifier of the DiskImage.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ID string `json:"id"`

	// Name is the DiskImage name, carried for observability.
	// +kubebuilder:validation:Optional
	Name string `json:"name,omitempty"`

	// Project is the DiskImage project, carried for observability.
	// +kubebuilder:validation:Optional
	Project string `json:"project,omitempty"`

	// Shared marks a shared-tenant DiskImage reference, carried verbatim
	// from the ComputeInstance spec; it is not interpreted here — the
	// reference resolves by ID.
	// +kubebuilder:validation:Optional
	Shared bool `json:"shared,omitempty"`
}

// VolumeTarget records where an owned volume's storage is provisioned.
type VolumeTarget struct {
	// DiskKey is the disk key on the owning workload ("boot", "disk-1", ...).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	DiskKey string `json:"diskKey"`

	// Namespace is the namespace where the provisioned storage is created.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`

	// DataVolumeName is the deterministic name of the provisioned storage
	// object that the owning workload references.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	DataVolumeName string `json:"dataVolumeName"`
}

// VolumeSpec defines the desired state of Volume.
type VolumeSpec struct {
	// StorageTier is the name of the StorageTier that determines which provider
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

	// AccessMode is the Kubernetes access mode for the volume.
	// https://kubernetes.io/docs/concepts/storage/persistent-volumes/#access-modes
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=ReadWriteOnce;ReadOnlyMany;ReadWriteMany;ReadWriteOncePod
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="accessMode is immutable"
	AccessMode VolumeAccessMode `json:"accessMode"`

	// Topology carries CSI-style placement constraints for this volume.
	// For node-local backends (e.g. LVMS), the "osac.io/node" segment
	// pins the volume to the scheduler-selected node; the ComputeInstance
	// using this volume is then node-pinned. For network backends,
	// topology may carry zone/region segments or be empty.
	// The target CSI driver must support WaitForFirstConsumer and
	// advertise VOLUME_ACCESSIBILITY_CONSTRAINTS for topology to take
	// effect. Immutable after creation.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="topology is immutable"
	Topology *VolumeTopology `json:"topology,omitempty"`

	// Owner identifies the fulfillment workload that owns this volume.
	// Set only by the VMaaS provisioning path; CaaS volumes are owned by
	// their CaaS cluster, for which a dedicated owner type is future
	// work — today this field distinguishes VMaaS volumes only. Immutable
	// after creation.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="owner is immutable"
	Owner *VolumeOwner `json:"owner,omitempty"`

	// DiskImage references the DiskImage catalog object the volume is
	// provisioned from. Absent for blank disks. Immutable after creation.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="diskImage is immutable"
	DiskImage *DiskImageReference `json:"diskImage,omitempty"`

	// Target records where an owned volume's storage is provisioned.
	// Immutable after creation.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="target is immutable"
	Target *VolumeTarget `json:"target,omitempty"`
}

// VolumeAccessMode defines valid Kubernetes PersistentVolume access modes.
// https://kubernetes.io/docs/concepts/storage/persistent-volumes/#access-modes
// +kubebuilder:validation:Enum=ReadWriteOnce;ReadOnlyMany;ReadWriteMany;ReadWriteOncePod
type VolumeAccessMode string

const (
	VolumeAccessModeReadWriteOnce    VolumeAccessMode = "ReadWriteOnce"
	VolumeAccessModeReadOnlyMany     VolumeAccessMode = "ReadOnlyMany"
	VolumeAccessModeReadWriteMany    VolumeAccessMode = "ReadWriteMany"
	VolumeAccessModeReadWriteOncePod VolumeAccessMode = "ReadWriteOncePod"
)

// VolumeProtocol defines valid storage protocols for volumes.
// When adding a value here, also add it to the kubebuilder Enum below, to the
// crdProtocolToProto switch in internal/controller/volume_feedback_controller.go,
// and to allVolumeProtocols in that controller's test (which fails if the switch
// is left incomplete).
// +kubebuilder:validation:Enum=Block;NFS
type VolumeProtocol string

const (
	VolumeProtocolBlock VolumeProtocol = "Block"
	VolumeProtocolNFS   VolumeProtocol = "NFS"
)

// VolumePhaseType is a valid value for .status.phase
type VolumePhaseType string

const (
	VolumePhaseProgressing VolumePhaseType = "Progressing"
	VolumePhaseReady       VolumePhaseType = "Ready"
	VolumePhaseFailed      VolumePhaseType = "Failed"
	VolumePhaseDeleting    VolumePhaseType = "Deleting"
	VolumePhaseDeleted     VolumePhaseType = "Deleted"
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
	// +kubebuilder:validation:Enum=Progressing;Ready;Failed;Deleting;Deleted
	Phase VolumePhaseType `json:"phase,omitempty"`

	// Conditions holds an array of metav1.Condition that describe the state of the Volume.
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// VendorVolumeID is the opaque identifier assigned by the vendor storage array.
	// Set by the Volume controller after vendor CSI CreateVolume succeeds.
	// +kubebuilder:validation:Optional
	VendorVolumeID string `json:"vendorVolumeID,omitempty"`

	// Provider identifies the registered VendorProvisioner implementation selected for this volume.
	// Resolved during tier resolution at creation time.
	// +kubebuilder:validation:Optional
	Provider string `json:"provider,omitempty"`

	// Protocol is the storage protocol used for this volume.
	// Resolved during tier resolution at creation time.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=Block;NFS
	Protocol VolumeProtocol `json:"protocol,omitempty"`

	// VendorContext holds backend-specific attach parameters needed by the vendor CSI controller
	// (for example VAST's "subsystem" and "vip_pool_name"). Opaque to the operator's own
	// reconciliation logic: set from the same parameters used for the vendor CreateVolume call,
	// synced to fulfillment-service by the feedback controller, and forwarded unchanged by
	// osac-csi-driver to the vendor CSI controller's ControllerPublishVolume.
	// +kubebuilder:validation:Optional
	VendorContext map[string]string `json:"vendorContext,omitempty"`

	// StateTransitionTime is the authoritative time at which the lifecycle
	// phase last changed.
	// +kubebuilder:validation:Optional
	StateTransitionTime *metav1.Time `json:"stateTransitionTime,omitempty"`

	// ProvisionedSizeGiB is the capacity last committed on the vendor array.
	// +kubebuilder:validation:Optional
	ProvisionedSizeGiB int64 `json:"provisionedSizeGiB,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vol
// +kubebuilder:printcolumn:name="Tier",type=string,JSONPath=`.spec.storageTier`
// +kubebuilder:printcolumn:name="Size",type=integer,JSONPath=`.spec.sizeGiB`
// +kubebuilder:printcolumn:name="Access",type=string,JSONPath=`.spec.accessMode`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Provider",type=string,JSONPath=`.status.provider`,priority=1
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
