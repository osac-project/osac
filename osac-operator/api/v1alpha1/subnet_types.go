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
	"k8s.io/apimachinery/pkg/runtime"
)

// SubnetSpec defines the desired state of Subnet
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable after creation"
type SubnetSpec struct {
	// VirtualNetwork is the API ID of the parent VirtualNetwork
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="virtualNetwork is immutable"
	VirtualNetwork string `json:"virtualNetwork"`

	// IPv4CIDR is the required canonical IPv4 CIDR block for this subnet.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=18
	// +kubebuilder:validation:XValidation:rule="string(cidr(self).masked()) == self && !self.contains(':')",message="ipv4Cidr must be a canonical IPv4 CIDR"
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="ipv4Cidr is immutable"
	IPv4CIDR string `json:"ipv4Cidr,omitempty"`

	// AddressPlan is the NetworkClass address plan at the Subnet's create, in
	// sixteenths. OSAC computes status.ipam from it once.
	// +kubebuilder:validation:Required
	AddressPlan SubnetAddressPlan `json:"addressPlan"`
}

// SubnetStatus defines the observed state of Subnet
type SubnetStatus struct {
	// Conditions holds the Ready condition of the Subnet.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// NetworkManagers records the managers copied from the parent at create.
	// +kubebuilder:validation:Optional
	NetworkManagers *NetworkManagers `json:"networkManagers,omitempty"`

	// NetworkType records the network type copied from the parent at create; empty without a Kubernetes Manager.
	// +kubebuilder:validation:Optional
	NetworkType NetworkType `json:"networkType,omitempty"`

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

	// IPAM is the address layout computed once at create (contract §4.8).
	// +kubebuilder:validation:Optional
	IPAM *SubnetIPAM `json:"ipam,omitempty"`

	// NetworkSegment is the accepted segment of the Fabric create_subnet (contract §4.9).
	// +kubebuilder:validation:Optional
	NetworkSegment *NetworkSegment `json:"networkSegment,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=subnet
// +kubebuilder:printcolumn:name="VirtualNetwork",type=string,JSONPath=`.spec.virtualNetwork`
// +kubebuilder:printcolumn:name="IPv4CIDR",type=string,JSONPath=`.spec.ipv4Cidr`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
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
