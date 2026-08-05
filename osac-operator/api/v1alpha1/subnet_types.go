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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SubnetSpec defines the desired state of Subnet
type SubnetSpec struct {
	// VirtualNetwork is the ID of the parent VirtualNetwork
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="virtualNetwork is immutable"
	VirtualNetwork string `json:"virtualNetwork"`

	// IPv4CIDR is the IPv4 CIDR block for this subnet
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="ipv4Cidr is immutable"
	IPv4CIDR string `json:"ipv4Cidr,omitempty"`

	// IPv6CIDR is the IPv6 CIDR block for this subnet
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="ipv6Cidr is immutable"
	IPv6CIDR string `json:"ipv6Cidr,omitempty"`
}

// SubnetPhaseType is a valid value for .status.phase
type SubnetPhaseType string

const (
	// SubnetPhaseProgressing means an update is in progress
	SubnetPhaseProgressing SubnetPhaseType = "Progressing"

	// SubnetPhaseFailed means the subnet provisioning has failed
	SubnetPhaseFailed SubnetPhaseType = "Failed"

	// SubnetPhaseReady means the subnet and all associated resources are ready
	SubnetPhaseReady SubnetPhaseType = "Ready"

	// SubnetPhaseDeleting means there has been a request to delete the Subnet
	SubnetPhaseDeleting SubnetPhaseType = "Deleting"
)

// SubnetConditionType is a valid value for .status.conditions.type
type SubnetConditionType string

const (
	// SubnetConditionAccepted means the order has been accepted but work has not yet started
	SubnetConditionAccepted SubnetConditionType = "Accepted"

	// SubnetConditionProgressing means that an update is in progress
	SubnetConditionProgressing SubnetConditionType = "Progressing"

	// SubnetConditionReady means the subnet is ready
	SubnetConditionReady SubnetConditionType = "Ready"

	// SubnetConditionNetworkProvisioned means the network has been provisioned
	SubnetConditionNetworkProvisioned SubnetConditionType = "NetworkProvisioned"

	// SubnetConditionNetworkReady means the network is ready
	SubnetConditionNetworkReady SubnetConditionType = "NetworkReady"
)

// SubnetStatus defines the observed state of Subnet
type SubnetStatus struct {
	// Phase provides a single-value overview of the state of the Subnet
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Enum=Progressing;Failed;Ready;Deleting
	Phase SubnetPhaseType `json:"phase,omitempty"`

	// DesiredConfigVersion is a hash of the spec, used to detect spec changes and control retry behavior.
	// +kubebuilder:validation:Optional
	DesiredConfigVersion string `json:"desiredConfigVersion,omitempty"`

	// ProvisioningJobs holds an array of JobStatus tracking provisioning and deprovisioning operations
	// +kubebuilder:validation:Optional
	ProvisioningJobs []JobStatus `json:"provisioningJobs,omitempty"`

	// Conditions holds an array of metav1.Condition that describe the state of the Subnet
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// BackendNetworkID stores provider-specific network identifier
	// +kubebuilder:validation:Optional
	BackendNetworkID string `json:"backendNetworkId,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=subnet
// +kubebuilder:printcolumn:name="VirtualNetwork",type=string,JSONPath=`.spec.virtualNetwork`
// +kubebuilder:printcolumn:name="IPv4CIDR",type=string,JSONPath=`.spec.ipv4Cidr`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Subnet is the Schema for the subnets API
type Subnet struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Subnet
	// +required
	Spec SubnetSpec `json:"spec"`

	// status defines the observed state of Subnet
	// +optional
	Status SubnetStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// SubnetList contains a list of Subnet
type SubnetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Subnet `json:"items"`
}

// GetName returns the name of the Subnet resource
func (s *Subnet) GetName() string {
	return s.ObjectMeta.Name
}
