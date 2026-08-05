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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ImageSourceType defines valid image source types
// +kubebuilder:validation:Enum=registry
type ImageSourceType string

const (
	// ImageSourceTypeRegistry indicates the image is from an OCI registry
	ImageSourceTypeRegistry ImageSourceType = "registry"
)

// ImageSpec defines the VM image configuration
type ImageSpec struct {
	// SourceType specifies the type of image source (currently only "registry" supported)
	// +kubebuilder:validation:Required
	SourceType ImageSourceType `json:"sourceType"`

	// SourceRef is the OCI image reference for the VM
	// Example: "quay.io/fedora/fedora-coreos:stable"
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	SourceRef string `json:"sourceRef"`
}

// DiskSpec defines disk configuration
type DiskSpec struct {
	// SizeGiB is the size of the disk in gibibytes
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	SizeGiB int32 `json:"sizeGiB"`
}

// RunStrategyType defines valid VM run strategies
// +kubebuilder:validation:Enum=Always;Halted
type RunStrategyType string

const (
	// RunStrategyAlways means the VM should always be running
	RunStrategyAlways RunStrategyType = "Always"

	// RunStrategyHalted means the VM should be stopped
	RunStrategyHalted RunStrategyType = "Halted"
)

// NetworkAttachment defines one NIC: a Subnet CR on the hub plus optional SecurityGroup CR names.
type NetworkAttachment struct {
	// SubnetRef is the name of the Subnet CR in the same namespace as the ComputeInstance.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="subnetRef is immutable"
	SubnetRef string `json:"subnetRef"`

	// SecurityGroupRefs lists SecurityGroup CR names in the same namespace as the ComputeInstance.
	// Mutable - can be changed to add/remove security groups without recreating the VM.
	// +kubebuilder:validation:Optional
	SecurityGroupRefs []string `json:"securityGroupRefs,omitempty"`
}

// ComputeInstanceSpec defines the desired state of ComputeInstance
type ComputeInstanceSpec struct {
	// TemplateID is the unique identifier of the compute instance template to use when creating this compute instance
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=^[a-zA-Z_][a-zA-Z0-9._]*$
	TemplateID string `json:"templateID"`

	// TemplateParameters allows passing additional template-specific parameters as JSON-encoded key-value pairs.
	// This complements the explicit fields (cores, memoryGiB, etc.) and is used for:
	// - Template-specific parameters not covered by explicit fields (e.g., exposed_ports)
	// - Custom parameters defined by specific templates
	// +kubebuilder:validation:Optional
	TemplateParameters string `json:"templateParameters,omitempty"`

	// Image defines the VM image configuration
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="image is immutable"
	Image ImageSpec `json:"image"`

	// GuestOSFamily specifies the guest operating system family for the VM.
	// To support future OS families, this field is a freeform string.
	// Currently, any value other than "windows" will be treated as "linux".
	// This field is immutable after creation.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="guestOSFamily is immutable"
	GuestOSFamily string `json:"guestOSFamily,omitempty"`

	// Cores is the number of CPU cores
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=128
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="cores is immutable"
	Cores int32 `json:"cores"`

	// MemoryGiB is the memory in gibibytes
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="memoryGiB is immutable"
	MemoryGiB int32 `json:"memoryGiB"`

	// BootDisk is the primary boot disk
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="bootDisk is immutable"
	BootDisk DiskSpec `json:"bootDisk"`

	// AdditionalDisks are supplementary disks
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="additionalDisks is immutable"
	AdditionalDisks []DiskSpec `json:"additionalDisks,omitempty"`

	// RunStrategy controls VM running state (MUTABLE)
	// +kubebuilder:validation:Required
	RunStrategy RunStrategyType `json:"runStrategy"`

	// UserDataSecretRef references cloud-init user data
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="userDataSecretRef is immutable"
	UserDataSecretRef *corev1.LocalObjectReference `json:"userDataSecretRef,omitempty"`

	// SSHKey is the SSH public key
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="sshKey is immutable"
	SSHKey string `json:"sshKey,omitempty"`

	// NetworkAttachments defines multiple NICs when more than one subnet (and optional security groups per NIC) is required.
	// The first entry is the primary subnet for VM placement (subnet-namespace annotation).
	// Subnet references (per NetworkAttachment) are immutable but security groups can be changed.
	//
	// Why immutability is required:
	// Currently, ComputeInstances are created with a single NIC. The VM instance must be created in the namespace
	// of the subnet it uses (the subnet-namespace annotation determines VM placement). This architectural constraint
	// means that changing the subnet would require recreating the VM in a different namespace, which is not supported.
	// Similarly, the array size cannot change because adding/removing NICs would require changing the primary subnet
	// or the VM's namespace, both of which are immutable after VM creation.
	//
	// Immutability enforcement:
	// - The +listType=map with +listMapKey=subnetRef markers correlate entries by subnetRef for updates
	// - The size check below prevents adding or removing entries
	// - The per-item subnetRef validation (self == oldSelf) prevents changing keys of existing entries
	// Combined, these ensure only securityGroupRefs can be modified.
	//
	// MaxItems is required for CEL cost budget calculation: NetworkAttachment.SubnetRef has a CEL validation
	// rule (self == oldSelf for immutability). Without maxItems, Kubernetes cannot bound the cost of iterating
	// over array items to validate this rule, causing CRD installation to fail with "estimated rule cost exceeds budget".
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:XValidation:rule="size(oldSelf) == 0 || (size(self) == size(oldSelf) && self.all(na, oldSelf.exists(old, old.subnetRef == na.subnetRef)))",message="cannot change or add/remove network attachments after initial assignment"
	// +listType=map
	// +listMapKey=subnetRef
	NetworkAttachments []NetworkAttachment `json:"networkAttachments,omitempty"`

	// RestartRequestedAt is a timestamp signal to request a VM restart (MUTABLE).
	//
	// Set this field to the current time (usually NOW) to request a restart.
	// The controller will execute the restart if this timestamp is greater than
	// status.lastRestartedAt.
	//
	// This is a declarative signal mechanism - the timestamp is a monotonically
	// increasing value to detect new restart requests, not a scheduled time.
	// Typically set to the current time for immediate restarts.
	//
	// External schedulers can set this field on a schedule to implement
	// scheduled maintenance windows if needed.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Format=date-time
	RestartRequestedAt *metav1.Time `json:"restartRequestedAt,omitempty"`
}

// ComputeInstancePhaseType is a valid value for .status.phase
type ComputeInstancePhaseType string

const (
	// ComputeInstancePhaseStarting means the compute instance is starting
	ComputeInstancePhaseStarting ComputeInstancePhaseType = "Starting"

	// ComputeInstancePhaseRunning means the compute instance is running
	ComputeInstancePhaseRunning ComputeInstancePhaseType = "Running"

	// ComputeInstancePhaseFailed means the compute instance deployment or update has failed
	ComputeInstancePhaseFailed ComputeInstancePhaseType = "Failed"

	// ComputeInstancePhaseDeleting means there has been a request to delete the ComputeInstance
	ComputeInstancePhaseDeleting ComputeInstancePhaseType = "Deleting"

	// ComputeInstancePhaseStopping means the compute instance is in the process of being stopped
	ComputeInstancePhaseStopping ComputeInstancePhaseType = "Stopping"

	// ComputeInstancePhaseStopped means the compute instance is stopped
	ComputeInstancePhaseStopped ComputeInstancePhaseType = "Stopped"

	// ComputeInstancePhasePaused means the compute instance is paused
	ComputeInstancePhasePaused ComputeInstancePhaseType = "Paused"
)

// ComputeInstanceConditionType is a valid value for .status.conditions.type
type ComputeInstanceConditionType string

const (
	// ComputeInstanceConditionConfigurationApplied means the current spec configuration has been applied.
	// True when desiredConfigVersion == reconciledConfigVersion, False while configuration is being applied.
	ComputeInstanceConditionConfigurationApplied ComputeInstanceConditionType = "ConfigurationApplied"

	// ComputeInstanceConditionReady means the compute instance is ready (KubeVirt VM readiness reflected on the CR).
	ComputeInstanceConditionReady ComputeInstanceConditionType = "Ready"

	// ComputeInstanceConditionRestartInProgress indicates a restart is in progress
	ComputeInstanceConditionRestartInProgress ComputeInstanceConditionType = "RestartInProgress"

	// ComputeInstanceConditionRestartFailed indicates a restart request has failed
	ComputeInstanceConditionRestartFailed ComputeInstanceConditionType = "RestartFailed"

	// ComputeInstanceConditionProvisioned means the infrastructure resources (compute, storage) have been allocated.
	// True when the KubeVirt VirtualMachine exists and storage provisioning is complete.
	ComputeInstanceConditionProvisioned ComputeInstanceConditionType = "Provisioned"

	// ComputeInstanceConditionRestartRequired means the compute instance requires a restart for
	// configuration changes to take effect. Synced from KubeVirt VM.Status.Conditions[RestartRequired].
	ComputeInstanceConditionRestartRequired ComputeInstanceConditionType = "RestartRequired"
)

// VirtualMachineReferenceType contains a reference to the KubeVirt VirtualMachine CR created by this ComputeInstance
type VirtualMachineReferenceType struct {
	// Namespace that contains the VirtualMachine resources
	Namespace                  string `json:"namespace"`
	KubeVirtVirtualMachineName string `json:"kubeVirtVirtualMachineName"`
}

// TenantReferenceType contains a reference to the tenant that contains the ComputeInstance resources
type TenantReferenceType struct {
	// Name of the tenant
	Name string `json:"name"`
	// Namespace of the tenant
	Namespace string `json:"namespace"`
}

// ComputeInstanceStatus defines the observed state of ComputeInstance.
type ComputeInstanceStatus struct {
	// Phase provides a single-value overview of the state of the ComputeInstance
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Enum=Starting;Running;Failed;Deleting;Stopping;Stopped;Paused
	Phase ComputeInstancePhaseType `json:"phase,omitempty"`

	// Conditions holds an array of metav1.Condition that describe the state of the ComputeInstance
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// Reference to the KubeVirt VirtualMachine CR created by this ComputeInstance
	// +kubebuilder:validation:Optional
	VirtualMachineReference *VirtualMachineReferenceType `json:"virtualMachineReference,omitempty"`

	// Reference to the tenant that contains the ComputeInstance resources
	// +kubebuilder:validation:Optional
	TenantReference *TenantReferenceType `json:"tenantReference,omitempty"`

	// DesiredConfigVersion is the version (hash) of the desired configuration of the ComputeInstance
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	DesiredConfigVersion string `json:"desiredConfigVersion,omitempty"`

	// LastRestartedAt records when the last restart was initiated by the controller.
	//
	// This is set to spec.restartRequestedAt when the controller processes a restart request.
	// It will be empty if no restart has been performed yet.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=date-time
	LastRestartedAt *metav1.Time `json:"lastRestartedAt,omitempty"`

	// ProvisioningJobs tracks the history of provision and deprovision operations
	// Ordered chronologically, with latest operations at the end
	// Limited to the last N jobs (configurable via OSAC_MAX_JOB_HISTORY, default 10)
	// +kubebuilder:validation:Optional
	ProvisioningJobs []JobStatus `json:"provisioningJobs,omitempty"`

	// IPAddress is the primary IP address of the running instance, taken from the KubeVirt VirtualMachineInstance.
	// Populated when the instance is ready (phase Running).
	// +kubebuilder:validation:Optional
	IPAddress string `json:"ipAddress,omitempty"`

	// ExternalIPAddress is the external IP address attached to this instance via an ExternalIPAttachment.
	// Populated when an ExternalIPAttachment in state "Ready" references this ComputeInstance.
	// +kubebuilder:validation:Optional
	ExternalIPAddress string `json:"externalIPAddress,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ci
// +kubebuilder:printcolumn:name="Template",type=string,JSONPath=`.spec.templateID`
// +kubebuilder:printcolumn:name="Cores",type=integer,JSONPath=`.spec.cores`
// +kubebuilder:printcolumn:name="Memory",type=integer,JSONPath=`.spec.memoryGiB`
// +kubebuilder:printcolumn:name="OS",type=string,JSONPath=`.spec.guestOSFamily`
// +kubebuilder:printcolumn:name="RunStrategy",type=string,JSONPath=`.spec.runStrategy`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="IP",type=string,JSONPath=`.status.ipAddress`
// +kubebuilder:printcolumn:name="ExternalIP",type=string,JSONPath=`.status.externalIPAddress`,priority=1

// ComputeInstance is the Schema for the computeinstances API
type ComputeInstance struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of ComputeInstance
	// +required
	Spec ComputeInstanceSpec `json:"spec"`

	// status defines the observed state of ComputeInstance
	// +optional
	Status ComputeInstanceStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ComputeInstanceList contains a list of ComputeInstance
type ComputeInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ComputeInstance `json:"items"`
}

// GetName returns the name of the ComputeInstance resource
func (ci *ComputeInstance) GetName() string {
	return ci.ObjectMeta.Name
}

// PrimarySubnetRef returns the Subnet CR name used for VM placement and the subnet-namespace annotation:
// the first networkAttachments[].subnetRef when that list is non-empty, otherwise empty string.
func (s ComputeInstanceSpec) PrimarySubnetRef() string {
	if len(s.NetworkAttachments) > 0 {
		return s.NetworkAttachments[0].SubnetRef
	}
	return ""
}
