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

// ExternalIPPoolSpec defines the desired state of ExternalIPPool
type ExternalIPPoolSpec struct {
	// CIDRs is the list of CIDR blocks for this pool. All CIDRs must match the declared IPFamily.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="cidrs is immutable"
	CIDRs []string `json:"cidrs"`

	// IPFamily indicates the IP address family for this pool (IPv4 or IPv6)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Enum=IPv4;IPv6
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="ipFamily is immutable"
	IPFamily string `json:"ipFamily"`

	// ImplementationStrategy determines the backend used to advertise IPs (e.g., metallb-l2).
	// Defaults to metallb-l2 for v7.0.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Enum=metallb-l2;netris
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="implementationStrategy is immutable"
	ImplementationStrategy string `json:"implementationStrategy,omitempty"`
}

// ExternalIPPoolPhaseType is a valid value for .status.phase
type ExternalIPPoolPhaseType string

const (
	// ExternalIPPoolPhaseProgressing means an update is in progress
	ExternalIPPoolPhaseProgressing ExternalIPPoolPhaseType = "Progressing"

	// ExternalIPPoolPhaseFailed means the pool provisioning has failed
	ExternalIPPoolPhaseFailed ExternalIPPoolPhaseType = "Failed"

	// ExternalIPPoolPhaseReady means the pool and all associated resources are ready
	ExternalIPPoolPhaseReady ExternalIPPoolPhaseType = "Ready"

	// ExternalIPPoolPhaseDeleting means there has been a request to delete the ExternalIPPool
	ExternalIPPoolPhaseDeleting ExternalIPPoolPhaseType = "Deleting"
)

// ExternalIPPoolStatus defines the observed state of ExternalIPPool
type ExternalIPPoolStatus struct {
	// Phase provides a single-value overview of the state of the ExternalIPPool
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Enum=Progressing;Failed;Ready;Deleting
	Phase ExternalIPPoolPhaseType `json:"phase,omitempty"`

	// DesiredConfigVersion is a hash of the spec, used to detect spec changes and control retry behavior.
	// +kubebuilder:validation:Optional
	DesiredConfigVersion string `json:"desiredConfigVersion,omitempty"`

	// ProvisioningJobs holds an array of JobStatus tracking provisioning and deprovisioning operations
	// +kubebuilder:validation:Optional
	ProvisioningJobs []JobStatus `json:"provisioningJobs,omitempty"`

	// Conditions holds an array of metav1.Condition that describe the state of the ExternalIPPool
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// Total is the total number of usable IP addresses across all CIDRs in this pool.
	// Uses int64 to accommodate large IPv6 CIDR ranges.
	// +kubebuilder:validation:Optional
	Total int64 `json:"total,omitempty"`

	// Allocated is the number of IPs currently allocated from the pool.
	// +kubebuilder:validation:Optional
	Allocated int64 `json:"allocated,omitempty"`

	// Available is the number of IPs available for allocation.
	// +kubebuilder:validation:Optional
	Available int64 `json:"available,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=externalippool
// +kubebuilder:printcolumn:name="IPFamily",type=string,JSONPath=`.spec.ipFamily`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Total",type=integer,JSONPath=`.status.total`
// +kubebuilder:printcolumn:name="Available",type=integer,JSONPath=`.status.available`
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
