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

// NetworkClassDefaults are the tenant networking defaults of the deployment.
type NetworkClassDefaults struct {
	// VirtualNetworkIPv4CIDR is the default VirtualNetwork CIDR.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=18
	// +kubebuilder:validation:XValidation:rule="string(cidr(self).masked()) == self && !self.contains(':')",message="virtualNetworkIpv4Cidr must be a canonical IPv4 CIDR"
	VirtualNetworkIPv4CIDR string `json:"virtualNetworkIpv4Cidr"`

	// SubnetIPv4CIDR is the default Subnet CIDR, inside VirtualNetworkIPv4CIDR.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=18
	// +kubebuilder:validation:XValidation:rule="string(cidr(self).masked()) == self && !self.contains(':')",message="subnetIpv4Cidr must be a canonical IPv4 CIDR"
	SubnetIPv4CIDR string `json:"subnetIpv4Cidr"`

	// IngressRules are the rules of the default SecurityGroup for ingress traffic.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=100
	IngressRules []SecurityRule `json:"ingressRules,omitempty"`

	// EgressRules are the rules of the default SecurityGroup for egress traffic.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=100
	EgressRules []SecurityRule `json:"egressRules,omitempty"`
}

// NetworkClassEastWestCapabilities enables east-west fabrics.
type NetworkClassEastWestCapabilities struct {
	// SupportsEastWestEthernet enables Ethernet east-west FabricDomains.
	// +kubebuilder:validation:Optional
	SupportsEastWestEthernet bool `json:"supportsEastWestEthernet,omitempty"`
}

// NetworkClassSpec defines the desired state of NetworkClass. It is immutable
// after creation.
// +kubebuilder:validation:XValidation:rule="has(self.k8sManager) == has(oldSelf.k8sManager) && (!has(self.k8sManager) || self.k8sManager == oldSelf.k8sManager)",message="k8sManager is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.eastWestCapabilities) == has(oldSelf.eastWestCapabilities) && (!has(self.eastWestCapabilities) || self.eastWestCapabilities == oldSelf.eastWestCapabilities)",message="eastWestCapabilities is immutable"
type NetworkClassSpec struct {
	// FabricManager is the registration name of the Fabric Manager.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="fabricManager is immutable"
	FabricManager string `json:"fabricManager"`

	// K8sManager is the registration name of the OSAC Kubernetes Manager; empty for none.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=63
	K8sManager string `json:"k8sManager,omitempty"`

	// Defaults are the tenant networking defaults.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="defaults is immutable"
	Defaults NetworkClassDefaults `json:"defaults"`

	// EastWestCapabilities enables east-west fabrics.
	// +kubebuilder:validation:Optional
	EastWestCapabilities *NetworkClassEastWestCapabilities `json:"eastWestCapabilities,omitempty"`

	// AddressPlan is the Subnet address plan, in sixteenths.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="addressPlan is immutable"
	AddressPlan SubnetAddressPlan `json:"addressPlan"`
}

// NetworkClassStatus defines the observed state of NetworkClass.
type NetworkClassStatus struct {
	// Conditions holds the Ready condition: True with reason Succeeded once the
	// managers resolved, False with reason Failed and the reason in its message.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// NetworkManagers are the resolved managers.
	// +kubebuilder:validation:Optional
	NetworkManagers *NetworkManagers `json:"networkManagers,omitempty"`

	// NetworkType is the resolved network type; empty without a Kubernetes Manager.
	// +kubebuilder:validation:Optional
	NetworkType NetworkType `json:"networkType,omitempty"`

	// SecurityGroupICMP is true when every resolved manager declares
	// security_group_icmp. Unset while the managers are not resolved.
	// +kubebuilder:validation:Optional
	SecurityGroupICMP *bool `json:"securityGroupIcmp,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=nc
// +kubebuilder:printcolumn:name="FabricManager",type=string,JSONPath=`.spec.fabricManager`
// +kubebuilder:printcolumn:name="K8sManager",type=string,JSONPath=`.spec.k8sManager`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// NetworkClass is the deployment's network manager selection, tenant defaults
// and Subnet address plan. The fulfillment controller creates it in the hub
// networking namespace with the label osac.openshift.io/networkclass-uuid.
type NetworkClass struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of NetworkClass
	// +required
	Spec NetworkClassSpec `json:"spec"`

	// status defines the observed state of NetworkClass
	// +optional
	Status NetworkClassStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// NetworkClassList contains a list of NetworkClass
type NetworkClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkClass `json:"items"`
}
