/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// FabricDomainType identifies the physical fabric used for east-west connectivity.
// Phase 1 supports EthernetEW; InfiniBandEW and NVLink are reserved for later phases.
// +kubebuilder:validation:Enum=EthernetEW;InfiniBandEW;NVLink
type FabricDomainType string

const (
	// FabricDomainTypeEthernetEW identifies an Ethernet east-west fabric.
	FabricDomainTypeEthernetEW FabricDomainType = "EthernetEW"

	// FabricDomainTypeInfiniBandEW identifies an InfiniBand east-west fabric, reserved for Phase 2.
	FabricDomainTypeInfiniBandEW FabricDomainType = "InfiniBandEW"

	// FabricDomainTypeNVLink identifies an NVIDIA NVLink fabric, reserved for Phase 3.
	FabricDomainTypeNVLink FabricDomainType = "NVLink"
)

// FabricDomainSpec defines the desired state of FabricDomain.
type FabricDomainSpec struct {
	// Type is the physical fabric type. It cannot be changed after creation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="type is immutable"
	Type FabricDomainType `json:"type"`

	// Servers lists the hostnames of the servers that belong to the fabric domain.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:MinLength=1
	Servers []string `json:"servers"`

	// VirtualNetwork is the ID of the OSAC VirtualNetwork associated with the domain.
	// For Netris EthernetEW, this VirtualNetwork is the source of truth for the VPC where the Server
	// Cluster is created. Hardware instance types supply the Server Cluster template binding.
	// InfiniBandEW and NVLink are reserved for later phases and may define different connectivity.
	// This reference is immutable after creation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="virtualNetwork is immutable"
	VirtualNetwork string `json:"virtualNetwork"`
}

// FabricDomainMemberState describes the provisioning state of an individual server.
// +kubebuilder:validation:Enum=Pending;Active;Failed
type FabricDomainMemberState string

const (
	// FabricDomainMemberStatePending means the server is awaiting provisioning.
	FabricDomainMemberStatePending FabricDomainMemberState = "Pending"

	// FabricDomainMemberStateActive means the server is active in the fabric domain.
	FabricDomainMemberStateActive FabricDomainMemberState = "Active"

	// FabricDomainMemberStateFailed means provisioning failed for the server.
	FabricDomainMemberStateFailed FabricDomainMemberState = "Failed"
)

// FabricDomainMemberStatus describes the observed state of one server in a fabric domain.
type FabricDomainMemberStatus struct {
	// Server is the hostname of the server.
	// +kubebuilder:validation:Required
	Server string `json:"server"`

	// State is the provisioning state of the server.
	// +kubebuilder:validation:Optional
	State FabricDomainMemberState `json:"state,omitempty"`

	// Message contains additional information about the server state.
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`
}

// FabricDomainPhase is a high-level summary of FabricDomain provisioning.
// +kubebuilder:validation:Enum=Progressing;Ready;Failed;Deleting
type FabricDomainPhase string

const (
	// FabricDomainPhaseProgressing means provisioning or an update is in progress.
	FabricDomainPhaseProgressing FabricDomainPhase = "Progressing"
	// FabricDomainPhaseReady means the backend server cluster matches the desired configuration.
	FabricDomainPhaseReady FabricDomainPhase = "Ready"
	// FabricDomainPhaseFailed means the most recent provisioning attempt failed.
	FabricDomainPhaseFailed FabricDomainPhase = "Failed"
	// FabricDomainPhaseDeleting means cleanup is in progress.
	FabricDomainPhaseDeleting FabricDomainPhase = "Deleting"
)

// FabricDomainStatus defines the observed state of FabricDomain.
type FabricDomainStatus struct {
	// ProvisionedAt records the first persisted Ready transition. It survives
	// membership updates and recovery so provisioning duration is observed once.
	// +kubebuilder:validation:Optional
	ProvisionedAt *metav1.Time `json:"provisionedAt,omitempty"`

	// Phase provides a single-value overview of FabricDomain provisioning.
	// +kubebuilder:validation:Optional
	Phase FabricDomainPhase `json:"phase,omitempty"`

	// Conditions describes the current state of the FabricDomain.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// BackendID is the provider-specific identifier of the server cluster.
	// +kubebuilder:validation:Optional
	BackendID string `json:"backendId,omitempty"`

	// ProvisioningConfig records the resolved backend binding before the first provisioning job.
	// A live Server Cluster cannot change its template, VPC, or site. Catalog and inventory
	// updates must remain compatible with this binding; deletion does not depend on those lookups.
	// +kubebuilder:validation:Optional
	ProvisioningConfig *FabricDomainProvisioningConfig `json:"provisioningConfig,omitempty"`

	// DesiredConfigVersion hashes the applied FabricDomain and resolved backend configuration.
	// +kubebuilder:validation:Optional
	DesiredConfigVersion string `json:"desiredConfigVersion,omitempty"`

	// ProvisioningIntent records that an AAP provisioning job may have been launched.
	// It is persisted before launch so deletion still attempts cleanup if job status cannot be saved.
	// +kubebuilder:validation:Optional
	ProvisioningIntent bool `json:"provisioningIntent,omitempty"`

	// ProvisioningJobs tracks AAP provisioning and cleanup jobs across reconciles.
	// +kubebuilder:validation:Optional
	ProvisioningJobs []JobStatus `json:"provisioningJobs,omitempty"`

	// VPCID is the provider-specific identifier of the VPC containing the fabric domain.
	// +kubebuilder:validation:Optional
	VPCID string `json:"vpcId,omitempty"`

	// Members contains the observed state of each server in the domain.
	// +kubebuilder:validation:Optional
	Members []FabricDomainMemberStatus `json:"members,omitempty"`
}

// FabricDomainProvisioningConfig identifies the backend configuration selected for a domain.
type FabricDomainProvisioningConfig struct {
	// NetworkClass is the ID of the class that scopes the hardware template binding.
	// +kubebuilder:validation:MinLength=1
	NetworkClass string `json:"networkClass"`
	// TemplateID is the Netris Server Cluster template selected from the members' instance types.
	// +kubebuilder:validation:Pattern=`^[1-9][0-9]*$`
	TemplateID string `json:"templateId"`
	// VPCID is the VirtualNetwork's Netris VPC at initial provisioning.
	// +kubebuilder:validation:Pattern=`^[1-9][0-9]*$`
	VPCID string `json:"vpcId"`
	// Region selects the configured Netris site for this domain.
	// +kubebuilder:validation:Optional
	Region string `json:"region,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=fd
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Servers",type=string,JSONPath=`.spec.servers`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// FabricDomain is the Schema for the fabricdomains API.
type FabricDomain struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of FabricDomain.
	// +required
	Spec FabricDomainSpec `json:"spec"`

	// status defines the observed state of FabricDomain.
	// +optional
	Status FabricDomainStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// FabricDomainList contains a list of FabricDomain.
type FabricDomainList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FabricDomain `json:"items"`
}

// GetName returns the name of the FabricDomain resource.
func (f *FabricDomain) GetName() string {
	return f.ObjectMeta.Name
}
