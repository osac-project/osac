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
	"k8s.io/apimachinery/pkg/runtime"
)

// NetworkAttachmentTargetKind is the kind of the workload of a NetworkAttachment.
// +kubebuilder:validation:Enum=ComputeInstance;BareMetalInstance
type NetworkAttachmentTargetKind string

const (
	// NetworkAttachmentTargetComputeInstance is a VM.
	NetworkAttachmentTargetComputeInstance NetworkAttachmentTargetKind = "ComputeInstance"
	// NetworkAttachmentTargetBareMetalInstance is a bare-metal server.
	NetworkAttachmentTargetBareMetalInstance NetworkAttachmentTargetKind = "BareMetalInstance"
)

// NetworkInterfaceType is the interface type of a NetworkAttachment.
// +kubebuilder:validation:Enum=overlay;physical
type NetworkInterfaceType string

const (
	// NetworkInterfaceOverlay is a VM interface on the Subnet's hub network.
	NetworkInterfaceOverlay NetworkInterfaceType = "overlay"
	// NetworkInterfacePhysical is a bare-metal host port.
	NetworkInterfacePhysical NetworkInterfaceType = "physical"
)

// NetworkAttachmentTarget is the workload of a NetworkAttachment.
type NetworkAttachmentTarget struct {
	// Kind is the workload kind.
	// +kubebuilder:validation:Required
	Kind NetworkAttachmentTargetKind `json:"kind"`

	// ID is the workload API ID.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ID string `json:"id"`
}

// NetworkAttachmentInterface is the attached workload interface.
type NetworkAttachmentInterface struct {
	// Type is the interface type.
	// +kubebuilder:validation:Required
	Type NetworkInterfaceType `json:"type"`

	// Name is the host interface name (physical only).
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name,omitempty"`

	// MACAddress is the host interface MAC address, lowercase and colon-separated (physical only).
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self.matches('^[0-9a-f]{2}(:[0-9a-f]{2}){5}$')",message="macAddress must be a lowercase colon-separated MAC address"
	MACAddress string `json:"macAddress,omitempty"`
}

// NetworkAttachmentSpec defines the desired state of NetworkAttachment.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable after creation"
// +kubebuilder:validation:XValidation:rule="self.interface.type == 'physical' ? has(self.interface.name) && has(self.interface.macAddress) : !has(self.interface.name) && !has(self.interface.macAddress)",message="interface name and macAddress are set exactly for a physical interface"
// +kubebuilder:validation:XValidation:rule="(self.target.kind == 'ComputeInstance') == (self.interface.type == 'overlay')",message="a ComputeInstance has an overlay interface and a BareMetalInstance a physical one"
type NetworkAttachmentSpec struct {
	// Target is the workload.
	// +kubebuilder:validation:Required
	Target NetworkAttachmentTarget `json:"target"`

	// Subnet is the Subnet API ID.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Subnet string `json:"subnet"`

	// Interface is the attached workload interface.
	// +kubebuilder:validation:Required
	Interface NetworkAttachmentInterface `json:"interface"`

	// SecurityGroups lists the selected SecurityGroup API IDs.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +listType=set
	SecurityGroups []string `json:"securityGroups"`
}

// NetworkAttachmentVMAttachment is the accepted vm_attachment result.
type NetworkAttachmentVMAttachment struct {
	// Namespace is the hub namespace where VMaaS creates the VM.
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`
}

// NetworkAttachmentDHCPLease is the accepted dhcp_lease result.
type NetworkAttachmentDHCPLease struct {
	// IPAddress is the leased IPv4 address.
	// +kubebuilder:validation:Required
	IPAddress string `json:"ipAddress"`

	// MACAddress is the MAC address that holds the lease.
	// +kubebuilder:validation:Required
	MACAddress string `json:"macAddress"`
}

// NetworkAttachmentStatus defines the observed state of NetworkAttachment.
type NetworkAttachmentStatus struct {
	// Conditions holds the Ready condition of the NetworkAttachment.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// NetworkManagers records the managers copied from the Subnet at create.
	// +kubebuilder:validation:Optional
	NetworkManagers *NetworkManagers `json:"networkManagers,omitempty"`

	// NetworkType records the network type copied from the Subnet; empty without a Kubernetes Manager.
	// +kubebuilder:validation:Optional
	NetworkType NetworkType `json:"networkType,omitempty"`

	// VMAttachment is the accepted vm_attachment result (overlay only).
	// +kubebuilder:validation:Optional
	VMAttachment *NetworkAttachmentVMAttachment `json:"vmAttachment,omitempty"`

	// DHCPLease is the accepted dhcp_lease result (physical only).
	// +kubebuilder:validation:Optional
	DHCPLease *NetworkAttachmentDHCPLease `json:"dhcpLease,omitempty"`

	// TaskInput is the task input stored before the first job (contract §4.5).
	// +kubebuilder:validation:Optional
	// +kubebuilder:pruning:PreserveUnknownFields
	TaskInput *runtime.RawExtension `json:"taskInput,omitempty"`

	// Jobs tracks the latest job per task and manager (contract §4.7).
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=task
	// +listMapKey=manager
	Jobs []NetworkJobStatus `json:"jobs,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=na
// +kubebuilder:printcolumn:name="Kind",type=string,JSONPath=`.spec.target.kind`
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.target.id`
// +kubebuilder:printcolumn:name="Subnet",type=string,JSONPath=`.spec.subnet`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// NetworkAttachment is OSAC's hub CR for one workload interface's attachment
// to one Subnet and its SecurityGroups. Its name is
// <lowercase target kind>-<target API ID>, it lives in the hub networking
// namespace and carries the osac.openshift.io/tenant annotation. It is not a
// fulfillment API resource.
type NetworkAttachment struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of NetworkAttachment
	// +required
	Spec NetworkAttachmentSpec `json:"spec"`

	// status defines the observed state of NetworkAttachment
	// +optional
	Status NetworkAttachmentStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// NetworkAttachmentList contains a list of NetworkAttachment
type NetworkAttachmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkAttachment `json:"items"`
}

// NetworkAttachmentName returns the CR name of the NetworkAttachment of a workload.
func NetworkAttachmentName(kind NetworkAttachmentTargetKind, id string) string {
	switch kind {
	case NetworkAttachmentTargetComputeInstance:
		return "computeinstance-" + id
	default:
		return "baremetalinstance-" + id
	}
}
