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

// SecurityGroupProtocol represents network protocol types
// +kubebuilder:validation:Enum=tcp;udp;icmp;all
type SecurityGroupProtocol string

const (
	// SecurityGroupProtocolTCP represents TCP protocol
	SecurityGroupProtocolTCP SecurityGroupProtocol = "tcp"

	// SecurityGroupProtocolUDP represents UDP protocol
	SecurityGroupProtocolUDP SecurityGroupProtocol = "udp"

	// SecurityGroupProtocolICMP represents ICMP protocol
	SecurityGroupProtocolICMP SecurityGroupProtocol = "icmp"

	// SecurityGroupProtocolAll represents all protocols
	SecurityGroupProtocolAll SecurityGroupProtocol = "all"
)

// SecurityRule defines a single security rule for ingress or egress traffic
type SecurityRule struct {
	// Protocol specifies the network protocol
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Enum=tcp;udp;icmp;all
	Protocol SecurityGroupProtocol `json:"protocol"`

	// PortFrom specifies the start of the port range
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	PortFrom *int32 `json:"portFrom,omitempty"`

	// PortTo specifies the end of the port range
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	PortTo *int32 `json:"portTo,omitempty"`

	// SourceCIDR specifies the source CIDR block for this rule
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	SourceCIDR string `json:"sourceCidr,omitempty"`

	// DestinationCIDR specifies the destination CIDR block for this rule
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	DestinationCIDR string `json:"destinationCidr,omitempty"`
}

// SecurityGroupSpec defines the desired state of SecurityGroup
type SecurityGroupSpec struct {
	// VirtualNetwork is the ID of the parent VirtualNetwork
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="virtualNetwork is immutable"
	VirtualNetwork string `json:"virtualNetwork"`

	// ImplementationStrategy determines the backend used to enforce security rules.
	// Set by the fulfillment-service; defaults to network_policy when empty.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="implementationStrategy is immutable"
	ImplementationStrategy string `json:"implementationStrategy,omitempty"`

	// IngressRules defines the ingress security rules
	// +kubebuilder:validation:Optional
	IngressRules []SecurityRule `json:"ingressRules,omitempty"`

	// EgressRules defines the egress security rules
	// +kubebuilder:validation:Optional
	EgressRules []SecurityRule `json:"egressRules,omitempty"`
}

// SecurityGroupPhaseType is a valid value for .status.phase
// +kubebuilder:validation:Enum=Progressing;Ready;Failed;Deleting
type SecurityGroupPhaseType string

const (
	// SecurityGroupPhaseProgressing means an update is in progress
	SecurityGroupPhaseProgressing SecurityGroupPhaseType = "Progressing"

	// SecurityGroupPhaseReady means the security group and all associated resources are ready
	SecurityGroupPhaseReady SecurityGroupPhaseType = "Ready"

	// SecurityGroupPhaseFailed means the security group provisioning has failed
	SecurityGroupPhaseFailed SecurityGroupPhaseType = "Failed"

	// SecurityGroupPhaseDeleting means there has been a request to delete the SecurityGroup
	SecurityGroupPhaseDeleting SecurityGroupPhaseType = "Deleting"
)

// SecurityGroupStatus defines the observed state of SecurityGroup
type SecurityGroupStatus struct {
	// Phase provides a single-value overview of the state of the SecurityGroup
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Enum=Progressing;Ready;Failed;Deleting
	Phase SecurityGroupPhaseType `json:"phase,omitempty"`

	// DesiredConfigVersion is a hash of the spec, used to detect spec changes and control retry behavior.
	// +kubebuilder:validation:Optional
	DesiredConfigVersion string `json:"desiredConfigVersion,omitempty"`

	// ProvisioningJobs holds an array of JobStatus tracking provisioning and deprovisioning operations
	// +kubebuilder:validation:Optional
	ProvisioningJobs []JobStatus `json:"provisioningJobs,omitempty"`

	// BackendSecurityGroupID stores provider-specific security group identifier
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	BackendSecurityGroupID string `json:"backendSecurityGroupId,omitempty"`

	// Conditions holds an array of metav1.Condition that describe the state of the SecurityGroup
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=sg
// +kubebuilder:printcolumn:name="VirtualNetwork",type=string,JSONPath=`.spec.virtualNetwork`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// SecurityGroup is the Schema for the securitygroups API
type SecurityGroup struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of SecurityGroup
	// +required
	Spec SecurityGroupSpec `json:"spec"`

	// status defines the observed state of SecurityGroup
	// +optional
	Status SecurityGroupStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// SecurityGroupList contains a list of SecurityGroup
type SecurityGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SecurityGroup `json:"items"`
}

// GetName returns the name of the SecurityGroup resource
func (s *SecurityGroup) GetName() string {
	return s.ObjectMeta.Name
}
