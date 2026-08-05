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

// NATGatewaySpec defines the desired state of NATGateway.
// The entire spec is immutable after creation: to change the configuration,
// delete the NATGateway and create a new one.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable after creation"
type NATGatewaySpec struct {
	// VirtualNetwork is the name of the parent VirtualNetwork whose egress
	// traffic is source-NATted through this gateway.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	VirtualNetwork string `json:"virtualNetwork"`

	// ExternalIP is the name of the ExternalIP resource to use as the SNAT
	// source address for outbound traffic.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ExternalIP string `json:"externalIP"`
}

// NATGatewayPhaseType is a valid value for .status.phase
type NATGatewayPhaseType string

const (
	// NATGatewayPhaseProgressing means the SNAT rule is being configured
	NATGatewayPhaseProgressing NATGatewayPhaseType = "Progressing"

	// NATGatewayPhaseFailed means the SNAT rule configuration has failed
	NATGatewayPhaseFailed NATGatewayPhaseType = "Failed"

	// NATGatewayPhaseReady means the SNAT rule is active and egress traffic is being NATted
	NATGatewayPhaseReady NATGatewayPhaseType = "Ready"

	// NATGatewayPhaseDeleting means the SNAT rule is being removed
	NATGatewayPhaseDeleting NATGatewayPhaseType = "Deleting"
)

// NATGatewayStatus defines the observed state of NATGateway
type NATGatewayStatus struct {
	// Phase provides a single-value overview of the state of the NATGateway
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Enum=Progressing;Ready;Failed;Deleting
	Phase NATGatewayPhaseType `json:"phase,omitempty"`

	// DesiredConfigVersion is a hash of the spec, used to detect spec changes and control retry behavior.
	// +kubebuilder:validation:Optional
	DesiredConfigVersion string `json:"desiredConfigVersion,omitempty"`

	// ProvisioningJobs holds an array of JobStatus tracking provisioning and deprovisioning operations
	// +kubebuilder:validation:Optional
	ProvisioningJobs []JobStatus `json:"provisioningJobs,omitempty"`

	// Conditions holds an array of metav1.Condition that describe the state of the NATGateway
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=natgw
// +kubebuilder:printcolumn:name="VirtualNetwork",type=string,JSONPath=`.spec.virtualNetwork`
// +kubebuilder:printcolumn:name="ExternalIP",type=string,JSONPath=`.spec.externalIP`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// NATGateway is the Schema for the natgateways API
type NATGateway struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of NATGateway
	// +required
	Spec NATGatewaySpec `json:"spec"`

	// status defines the observed state of NATGateway
	// +optional
	Status NATGatewayStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// NATGatewayList contains a list of NATGateway
type NATGatewayList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NATGateway `json:"items"`
}

// GetName returns the name of the NATGateway resource
func (n *NATGateway) GetName() string {
	return n.ObjectMeta.Name
}
