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
// +kubebuilder:validation:XValidation:rule="(has(self.computeInstance) ? 1 : 0) + (has(self.cluster) ? 1 : 0) + (has(self.baremetalInstance) ? 1 : 0) == 1",message="exactly one target field must be set"
// +kubebuilder:validation:XValidation:rule="!has(self.cluster) || has(self.targetEndpoint)",message="targetEndpoint is required when cluster is set"
// +kubebuilder:validation:XValidation:rule="!has(self.computeInstance) || !has(self.targetEndpoint)",message="targetEndpoint must not be set when computeInstance is set"
// +kubebuilder:validation:XValidation:rule="!has(self.baremetalInstance) || !has(self.targetEndpoint)",message="targetEndpoint must not be set when baremetalInstance is set"
type ExternalIPAttachmentSpec struct {
	// ExternalIP is the API ID of the ExternalIP to attach.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ExternalIP string `json:"externalIP"`

	// ComputeInstance is the UUID of the ComputeInstance to attach to.
	// Exactly one target field (computeInstance, cluster, or baremetalInstance) must be set.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	ComputeInstance *string `json:"computeInstance,omitempty"`

	// Cluster is the UUID of the ClusterOrder to attach to.
	// Exactly one target field (computeInstance, cluster, or baremetalInstance) must be set.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	Cluster *string `json:"cluster,omitempty"`

	// BaremetalInstance is the UUID of the BareMetalInstance to attach to.
	// Exactly one target field (computeInstance, cluster, or baremetalInstance) must be set.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	BaremetalInstance *string `json:"baremetalInstance,omitempty"`

	// TargetEndpoint specifies which cluster endpoint receives the DNAT rule.
	// Required when cluster is set; must not be set when computeInstance or baremetalInstance is set.
	// +kubebuilder:validation:Optional
	TargetEndpoint *ExternalIPAttachmentTargetEndpoint `json:"targetEndpoint,omitempty"`
}

// RoutingDomainType is the routing domain of an ExternalIPAttachment destination.
// +kubebuilder:validation:Enum=tenant_virtual_network;provider_management
type RoutingDomainType string

const (
	// RoutingDomainTenantVirtualNetwork is a tenant VirtualNetwork and Subnet.
	RoutingDomainTenantVirtualNetwork RoutingDomainType = "tenant_virtual_network"
	// RoutingDomainProviderManagement is the provider's management network.
	RoutingDomainProviderManagement RoutingDomainType = "provider_management"
)

// ExternalIPAttachmentRoutingDomain says where the destination address lives.
// +kubebuilder:validation:XValidation:rule="(self.type == 'tenant_virtual_network') == (has(self.virtualNetworkId) && has(self.subnetId))",message="virtualNetworkId and subnetId are set exactly for tenant_virtual_network"
type ExternalIPAttachmentRoutingDomain struct {
	// Type is the routing domain type.
	// +kubebuilder:validation:Required
	Type RoutingDomainType `json:"type"`

	// VirtualNetworkID is the VirtualNetwork API ID (tenant_virtual_network only).
	// +kubebuilder:validation:Optional
	VirtualNetworkID string `json:"virtualNetworkId,omitempty"`

	// SubnetID is the Subnet API ID (tenant_virtual_network only).
	// +kubebuilder:validation:Optional
	SubnetID string `json:"subnetId,omitempty"`
}

// ExternalIPAttachmentDestination is the DNAT destination sent to attach_external_ip.
type ExternalIPAttachmentDestination struct {
	// Address is the destination IPv4 address.
	// +kubebuilder:validation:Required
	Address string `json:"address"`

	// RoutingDomain is where Address lives.
	// +kubebuilder:validation:Required
	RoutingDomain ExternalIPAttachmentRoutingDomain `json:"routingDomain"`
}

// ExternalIPAttachmentStatus defines the observed state of ExternalIPAttachment
type ExternalIPAttachmentStatus struct {
	// Conditions holds the Ready condition of the ExternalIPAttachment.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// NetworkManagers records the Fabric Manager copied from the ExternalIP at create.
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

	// Destination is the destination sent to attach_external_ip.
	// +kubebuilder:validation:Optional
	Destination *ExternalIPAttachmentDestination `json:"destination,omitempty"`

	// StateTransitionTime records when the ExternalIPAttachment entered its current state.
	// It is authoritative for lifecycle consumers and is populated by the operator.
	// +kubebuilder:validation:Optional
	StateTransitionTime *metav1.Time `json:"stateTransitionTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=externalipattachment
// +kubebuilder:printcolumn:name="ExternalIP",type=string,JSONPath=`.spec.externalIP`
// +kubebuilder:printcolumn:name="ComputeInstance",type=string,JSONPath=`.spec.computeInstance`
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.cluster`
// +kubebuilder:printcolumn:name="BaremetalInstance",type=string,JSONPath=`.spec.baremetalInstance`,priority=1
// +kubebuilder:printcolumn:name="TargetEndpoint",type=string,JSONPath=`.spec.targetEndpoint`,priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
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
