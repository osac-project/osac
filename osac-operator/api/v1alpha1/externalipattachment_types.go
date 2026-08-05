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

// ExternalIPAttachmentTargetEndpoint specifies which cluster endpoint receives the DNAT rule.
// +kubebuilder:validation:Enum=API;Ingress
type ExternalIPAttachmentTargetEndpoint string

const (
	ExternalIPAttachmentTargetEndpointAPI     ExternalIPAttachmentTargetEndpoint = "API"
	ExternalIPAttachmentTargetEndpointIngress ExternalIPAttachmentTargetEndpoint = "Ingress"
)

// ExternalIPAttachmentSpec defines the desired state of ExternalIPAttachment.
// The entire spec is immutable after creation: to change the target, delete
// the attachment and create a new one.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable after creation"
// +kubebuilder:validation:XValidation:rule="(has(self.computeInstance) ? 1 : 0) + (has(self.cluster) ? 1 : 0) <= 1",message="at most one target field may be set"
// +kubebuilder:validation:XValidation:rule="!has(self.cluster) || has(self.targetEndpoint)",message="targetEndpoint is required when cluster is set"
// +kubebuilder:validation:XValidation:rule="!has(self.computeInstance) || !has(self.targetEndpoint)",message="targetEndpoint must not be set when computeInstance is set"
type ExternalIPAttachmentSpec struct {
	// ExternalIP is the name of the ExternalIP resource to attach.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ExternalIP string `json:"externalIP"`

	// ComputeInstance is the UUID of the ComputeInstance to attach to.
	// Exactly one target field (computeInstance or cluster) must be set.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	ComputeInstance *string `json:"computeInstance,omitempty"`

	// Cluster is the UUID of the ClusterOrder to attach to.
	// Exactly one target field (computeInstance or cluster) must be set.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	Cluster *string `json:"cluster,omitempty"`

	// TargetEndpoint specifies which cluster endpoint receives the DNAT rule.
	// Required when cluster is set; must not be set when computeInstance is set.
	// +kubebuilder:validation:Optional
	TargetEndpoint *ExternalIPAttachmentTargetEndpoint `json:"targetEndpoint,omitempty"`
}

// ExternalIPAttachmentPhaseType is a valid value for .status.phase
type ExternalIPAttachmentPhaseType string

const (
	// ExternalIPAttachmentPhaseProgressing means the attach or detach operation is in progress
	ExternalIPAttachmentPhaseProgressing ExternalIPAttachmentPhaseType = "Progressing"

	// ExternalIPAttachmentPhaseFailed means the attach or detach operation has failed
	ExternalIPAttachmentPhaseFailed ExternalIPAttachmentPhaseType = "Failed"

	// ExternalIPAttachmentPhaseReady means the external IP is attached and routing traffic
	ExternalIPAttachmentPhaseReady ExternalIPAttachmentPhaseType = "Ready"

	// ExternalIPAttachmentPhaseDeleting means a detach operation is in progress
	ExternalIPAttachmentPhaseDeleting ExternalIPAttachmentPhaseType = "Deleting"
)

// ExternalIPAttachmentStatus defines the observed state of ExternalIPAttachment
type ExternalIPAttachmentStatus struct {
	// Phase provides a single-value overview of the state of the ExternalIPAttachment
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Enum=Progressing;Failed;Ready;Deleting
	Phase ExternalIPAttachmentPhaseType `json:"phase,omitempty"`

	// DesiredConfigVersion is a hash of the spec, used to detect spec changes and control retry behavior.
	// +kubebuilder:validation:Optional
	DesiredConfigVersion string `json:"desiredConfigVersion,omitempty"`

	// ProvisioningJobs holds an array of JobStatus tracking provisioning and deprovisioning operations
	// +kubebuilder:validation:Optional
	ProvisioningJobs []JobStatus `json:"provisioningJobs,omitempty"`

	// Conditions holds an array of metav1.Condition that describe the state of the ExternalIPAttachment
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=externalipattachment
// +kubebuilder:printcolumn:name="ExternalIP",type=string,JSONPath=`.spec.externalIP`
// +kubebuilder:printcolumn:name="ComputeInstance",type=string,JSONPath=`.spec.computeInstance`
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.cluster`
// +kubebuilder:printcolumn:name="TargetEndpoint",type=string,JSONPath=`.spec.targetEndpoint`,priority=1
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ExternalIPAttachment is the Schema for the externalipattachments API
type ExternalIPAttachment struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of ExternalIPAttachment
	// +required
	Spec ExternalIPAttachmentSpec `json:"spec"`

	// status defines the observed state of ExternalIPAttachment
	// +optional
	Status ExternalIPAttachmentStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ExternalIPAttachmentList contains a list of ExternalIPAttachment
type ExternalIPAttachmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ExternalIPAttachment `json:"items"`
}

// GetName returns the name of the ExternalIPAttachment resource
func (p *ExternalIPAttachment) GetName() string {
	return p.ObjectMeta.Name
}
