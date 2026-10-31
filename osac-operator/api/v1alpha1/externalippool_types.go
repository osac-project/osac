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

// ExternalIPPoolSpec defines the desired state of ExternalIPPool
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable after creation"
type ExternalIPPoolSpec struct {
	// CIDRs is the list of canonical IPv4 CIDR blocks for this pool. Exactly one is supported.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=1
	// +kubebuilder:validation:items:MaxLength=18
	// +kubebuilder:validation:XValidation:rule="self.all(c, string(cidr(c).masked()) == c && !c.contains(':'))",message="cidrs must contain only canonical IPv4 CIDRs"
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="cidrs is immutable"
	CIDRs []string `json:"cidrs"`

	// IPFamily indicates the IP address family for this pool. Only IPv4 is supported.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Enum=IPv4
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="ipFamily is immutable"
	IPFamily string `json:"ipFamily"`
}

// ExternalIPPoolStatus defines the observed state of ExternalIPPool
type ExternalIPPoolStatus struct {
	// Conditions holds the Ready condition of the ExternalIPPool.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// NetworkManagers records the Fabric Manager taken from the NetworkClass at create.
	// +kubebuilder:validation:Optional
	NetworkManagers *NetworkManagers `json:"networkManagers,omitempty"`

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
// +kubebuilder:resource:shortName=externalippool
// +kubebuilder:printcolumn:name="IPFamily",type=string,JSONPath=`.spec.ipFamily`
// +kubebuilder:printcolumn:name="CIDRs",type=string,JSONPath=`.spec.cidrs`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ExternalIPPool is the Schema for the externalippools API
type ExternalIPPool struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of ExternalIPPool
	// +required
	Spec ExternalIPPoolSpec `json:"spec"`

	// status defines the observed state of ExternalIPPool
	// +optional
	Status ExternalIPPoolStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ExternalIPPoolList contains a list of ExternalIPPool
type ExternalIPPoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ExternalIPPool `json:"items"`
}

// GetName returns the name of the ExternalIPPool resource
func (p *ExternalIPPool) GetName() string {
	return p.ObjectMeta.Name
}
