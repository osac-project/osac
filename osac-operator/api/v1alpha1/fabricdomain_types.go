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

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// FabricDomainType identifies the physical fabric used for east-west connectivity.
// This release supports only ethernet_ew.
// +kubebuilder:validation:Enum=ethernet_ew
type FabricDomainType string

const (
	// FabricDomainTypeEthernetEW identifies an Ethernet east-west fabric.
	FabricDomainTypeEthernetEW FabricDomainType = "ethernet_ew"
)

// FabricDomainSpec defines the desired state of FabricDomain.
// +kubebuilder:validation:XValidation:rule="self.servers == oldSelf.servers",message="FabricDomain servers cannot change in this release; delete and recreate the FabricDomain"
type FabricDomainSpec struct {
	// Type is the physical fabric type. It cannot be changed after creation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="type is immutable"
	Type FabricDomainType `json:"type"`

	// Servers lists the inventory host names of the servers that belong to the fabric domain.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:MinLength=1
	Servers []string `json:"servers"`

	// VirtualNetwork is the API ID of the VirtualNetwork the domain is bound to.
	// This reference is immutable after creation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="virtualNetwork is immutable"
	VirtualNetwork string `json:"virtualNetwork"`
}

// FabricDomainStatus defines the observed state of FabricDomain.
type FabricDomainStatus struct {
	// Conditions holds the Ready condition of the FabricDomain.
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

	// BackendID is the backend_id the Fabric Manager returned.
	// +kubebuilder:validation:Optional
	BackendID string `json:"backendId,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=fd
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Servers",type=string,JSONPath=`.spec.servers`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
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
