/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// FabricDomainType identifies the physical fabric used for east-west connectivity.
// +kubebuilder:validation:Enum=EthernetEW;InfiniBandEW;NVLink
type FabricDomainType string

const (
	// FabricDomainTypeEthernetEW identifies an Ethernet east-west fabric.
	FabricDomainTypeEthernetEW FabricDomainType = "EthernetEW"

	// FabricDomainTypeInfiniBandEW identifies an InfiniBand east-west fabric.
	FabricDomainTypeInfiniBandEW FabricDomainType = "InfiniBandEW"

	// FabricDomainTypeNVLink identifies an NVIDIA NVLink fabric.
	FabricDomainTypeNVLink FabricDomainType = "NVLink"
)

// FabricDomainSpec defines the desired state of FabricDomain.
type FabricDomainSpec struct {
	// Type is the physical fabric type. It cannot be changed after creation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="type is immutable"
	Type FabricDomainType `json:"type"`

	// Servers lists the hostnames of the servers that belong to the fabric domain.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:Items:MinLength=1
	Servers []string `json:"servers"`

	// VirtualNetworks lists the VirtualNetwork resources associated with the domain.
	// Phase 1 supports exactly one virtual network. These references cannot be changed after creation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=1
	// +kubebuilder:validation:Items:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="virtualNetworks is immutable"
	VirtualNetworks []string `json:"virtualNetworks"`
}

// FabricDomainMemberState describes the provisioning state of an individual server.
// +kubebuilder:validation:Enum=Pending;Active;Failed
type FabricDomainMemberState string

const (
	// FabricDomainMemberStatePending means the server is awaiting provisioning.
	FabricDomainMemberStatePending FabricDomainMemberState = "Pending"

	// FabricDomainMemberStateActive means the server is active in the fabric domain.
	FabricDomainMemberStateActive FabricDomainMemberState = "Active"

	// FabricDomainMemberStateFailed means provisioning failed for the server.
	FabricDomainMemberStateFailed FabricDomainMemberState = "Failed"
)

// FabricDomainMemberStatus describes the observed state of one server in a fabric domain.
type FabricDomainMemberStatus struct {
	// Server is the hostname of the server.
	// +kubebuilder:validation:Required
	Server string `json:"server"`

	// State is the provisioning state of the server.
	// +kubebuilder:validation:Optional
	State FabricDomainMemberState `json:"state,omitempty"`

	// Message contains additional information about the server state.
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`
}

// FabricDomainStatus defines the observed state of FabricDomain.
type FabricDomainStatus struct {
	// Conditions describes the current state of the FabricDomain.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// BackendID is the provider-specific identifier of the server cluster.
	// +kubebuilder:validation:Optional
	BackendID string `json:"backendId,omitempty"`

	// VPCID is the provider-specific identifier of the VPC containing the fabric domain.
	// +kubebuilder:validation:Optional
	VPCID string `json:"vpcId,omitempty"`

	// Members contains the observed state of each server in the domain.
	// +kubebuilder:validation:Optional
	Members []FabricDomainMemberStatus `json:"members,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=fd
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Servers",type=string,JSONPath=`.spec.servers`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// FabricDomain is the Schema for the fabricdomains API.
type FabricDomain struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of FabricDomain.
	// +required
	Spec FabricDomainSpec `json:"spec"`

	// status defines the observed state of FabricDomain.
	// +optional
	Status FabricDomainStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// FabricDomainList contains a list of FabricDomain.
type FabricDomainList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FabricDomain `json:"items"`
}

// GetName returns the name of the FabricDomain resource.
func (f *FabricDomain) GetName() string {
	return f.ObjectMeta.Name
}
