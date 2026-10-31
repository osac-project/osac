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

// SecurityRule defines a single security rule for ingress or egress traffic.
// Ports are set exactly for tcp and udp, both of them, with portFrom <= portTo.
// +kubebuilder:validation:XValidation:rule="(self.protocol in ['tcp','udp']) ? (has(self.portFrom) && has(self.portTo)) : (!has(self.portFrom) && !has(self.portTo))",message="portFrom and portTo are set exactly for tcp and udp rules"
// +kubebuilder:validation:XValidation:rule="!has(self.portFrom) || !has(self.portTo) || self.portFrom <= self.portTo",message="portFrom must not exceed portTo"
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
	// +kubebuilder:validation:MaxLength=18
	// +kubebuilder:validation:XValidation:rule="self == '' || (string(cidr(self).masked()) == self && !self.contains(':'))",message="sourceCidr must be a canonical IPv4 CIDR"
	SourceCIDR string `json:"sourceCidr,omitempty"`

	// DestinationCIDR specifies the destination CIDR block for this rule
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:MaxLength=18
	// +kubebuilder:validation:XValidation:rule="self == '' || (string(cidr(self).masked()) == self && !self.contains(':'))",message="destinationCidr must be a canonical IPv4 CIDR"
	DestinationCIDR string `json:"destinationCidr,omitempty"`
}

// SecurityGroupSpec defines the desired state of SecurityGroup. The rules
// change on a rule update; the VirtualNetwork never changes.
type SecurityGroupSpec struct {
	// VirtualNetwork is the ID of the parent VirtualNetwork
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="virtualNetwork is immutable"
	VirtualNetwork string `json:"virtualNetwork"`

	// IngressRules defines the ingress security rules
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=100
	// +kubebuilder:validation:XValidation:rule="self.all(r, r.sourceCidr != '' && (!has(r.destinationCidr) || r.destinationCidr == ''))",message="ingress rules must contain only a source CIDR"
	IngressRules []SecurityRule `json:"ingressRules,omitempty"`

	// EgressRules defines the egress security rules
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=100
	// +kubebuilder:validation:XValidation:rule="self.all(r, r.destinationCidr != '' && (!has(r.sourceCidr) || r.sourceCidr == ''))",message="egress rules must contain only a destination CIDR"
	EgressRules []SecurityRule `json:"egressRules,omitempty"`
}

// SecurityGroupStatus defines the observed state of SecurityGroup
type SecurityGroupStatus struct {
	// Conditions holds the Ready condition of the SecurityGroup.
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
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=sg
// +kubebuilder:printcolumn:name="VirtualNetwork",type=string,JSONPath=`.spec.virtualNetwork`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
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
