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

// ExternalIPSpec defines the desired state of ExternalIP
type ExternalIPSpec struct {
	// Pool is the name of the ExternalIPPool this IP is allocated from.
	// This field is immutable after creation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="pool is immutable"
	Pool string `json:"pool"`
}

// ExternalIPPhaseType is a valid value for .status.phase
type ExternalIPPhaseType string

const (
	// ExternalIPPhaseProgressing means an update is in progress
	ExternalIPPhaseProgressing ExternalIPPhaseType = "Progressing"

	// ExternalIPPhaseFailed means the IP provisioning has failed
	ExternalIPPhaseFailed ExternalIPPhaseType = "Failed"

	// ExternalIPPhaseReady means the IP and all associated resources are ready
	ExternalIPPhaseReady ExternalIPPhaseType = "Ready"

	// ExternalIPPhaseDeleting means there has been a request to delete the ExternalIP
	ExternalIPPhaseDeleting ExternalIPPhaseType = "Deleting"
)

// ExternalIPStateType is a valid value for .status.state
type ExternalIPStateType string

const (
	// ExternalIPStatePending means the IP allocation is pending
	ExternalIPStatePending ExternalIPStateType = "Pending"

	// ExternalIPStateAllocated means the IP has been allocated from the pool
	ExternalIPStateAllocated ExternalIPStateType = "Allocated"

	// ExternalIPStateFailed means provisioning failed
	ExternalIPStateFailed ExternalIPStateType = "Failed"
)

// ExternalIPStatus defines the observed state of ExternalIP
type ExternalIPStatus struct {
	// Phase provides a single-value overview of the state of the ExternalIP
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Enum=Progressing;Failed;Ready;Deleting
	Phase ExternalIPPhaseType `json:"phase,omitempty"`

	// DesiredConfigVersion is a hash of the spec, used to detect spec changes and control retry behavior.
	// +kubebuilder:validation:Optional
	DesiredConfigVersion string `json:"desiredConfigVersion,omitempty"`

	// ProvisioningJobs holds an array of JobStatus tracking provisioning and deprovisioning operations
	// +kubebuilder:validation:Optional
	ProvisioningJobs []JobStatus `json:"provisioningJobs,omitempty"`

	// Conditions holds an array of metav1.Condition that describe the state of the ExternalIP
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// Address is the allocated external IP address
	// +kubebuilder:validation:Optional
	Address string `json:"address,omitempty"`

	// State tracks the attachment lifecycle of the ExternalIP
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Enum=Pending;Allocated;Failed
	State ExternalIPStateType `json:"state,omitempty"`

	// Attached indicates whether a ExternalIPAttachment has successfully attached this IP to a target.
	// +kubebuilder:validation:Optional
	Attached bool `json:"attached,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=externalip
// +kubebuilder:printcolumn:name="Pool",type=string,JSONPath=`.spec.pool`
// +kubebuilder:printcolumn:name="Address",type=string,JSONPath=`.status.address`
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
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
