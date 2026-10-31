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

// ExternalIPSpec defines the desired state of ExternalIP
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable after creation"
type ExternalIPSpec struct {
	// Pool is the API ID of the ExternalIPPool this IP is allocated from.
	// This field is immutable after creation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="pool is immutable"
	Pool string `json:"pool"`
}

// ExternalIPStatus defines the observed state of ExternalIP
type ExternalIPStatus struct {
	// Conditions holds the Ready condition of the ExternalIP.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// NetworkManagers records the Fabric Manager copied from the pool at create.
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

	// Address is the allocated external IP address, set only from an accepted
	// create_external_ip result. The API state is ALLOCATED exactly when it is
	// set and the ExternalIP is not being deleted.
	// +kubebuilder:validation:Optional
	Address string `json:"address,omitempty"`

	// Attached indicates whether a ExternalIPAttachment has successfully attached this IP to a target.
	// +kubebuilder:validation:Optional
	Attached bool `json:"attached,omitempty"`

	// StateTransitionTime records when the ExternalIP entered its current state.
	// It is authoritative for lifecycle consumers and is populated by the operator.
	// +kubebuilder:validation:Optional
	StateTransitionTime *metav1.Time `json:"stateTransitionTime,omitempty"`

	// AttachmentTransitionTime records when the settled ExternalIP attachment changed.
	// It is authoritative for lifecycle consumers and is populated by attachment feedback.
	// +kubebuilder:validation:Optional
	AttachmentTransitionTime *metav1.Time `json:"attachmentTransitionTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=externalip
// +kubebuilder:printcolumn:name="Pool",type=string,JSONPath=`.spec.pool`
// +kubebuilder:printcolumn:name="Address",type=string,JSONPath=`.status.address`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ExternalIP is the Schema for the externalips API
type ExternalIP struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of ExternalIP
	// +required
	Spec ExternalIPSpec `json:"spec"`

	// status defines the observed state of ExternalIP
	// +optional
	Status ExternalIPStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ExternalIPList contains a list of ExternalIP
type ExternalIPList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ExternalIP `json:"items"`
}

// GetName returns the name of the ExternalIP resource
func (p *ExternalIP) GetName() string {
	return p.ObjectMeta.Name
}
