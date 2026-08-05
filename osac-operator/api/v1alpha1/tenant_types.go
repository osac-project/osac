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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// TenantSpec defines the desired state of Tenant.
type TenantSpec struct {
}

type TenantPhaseType string

const (
	TenantPhaseProgressing TenantPhaseType = "Progressing"
	TenantPhaseReady       TenantPhaseType = "Ready"
	TenantPhaseFailed      TenantPhaseType = "Failed"
	TenantPhaseDeleting    TenantPhaseType = "Deleting"
)

// TenantConditionType is a valid value for .status.conditions.type
type TenantConditionType string

const (
	// TenantConditionNamespaceReady indicates whether the tenant namespace
	// exists on the target cluster. Owned by the Tenant controller.
	TenantConditionNamespaceReady TenantConditionType = "NamespaceReady"

	// TenantConditionStorageBackendReady indicates whether the tenant's storage
	// backend is provisioned (hub Secret exists with credentials).
	// Owned by the OSAC Storage Controller.
	TenantConditionStorageBackendReady TenantConditionType = "StorageBackendReady"

	// TenantConditionClusterStorageReady indicates whether StorageClasses and CSI
	// drivers are installed on the target cluster for this tenant.
	// Owned by the OSAC Storage Controller.
	TenantConditionClusterStorageReady TenantConditionType = "ClusterStorageReady"
)

// Reason constants for Tenant conditions
const (
	TenantReasonFound                       = "Found"
	TenantReasonNotFound                    = "NotFound"
	TenantReasonMultipleFound               = "MultipleFound"
	TenantReasonNoProvider                  = "NoProvider"
	TenantReasonBackendConfiguredNoProvider = "BackendConfiguredNoProvider"
)

// ResolvedStorageClass captures a single resolved StorageClass for a specific
// storage tier. The OSAC Storage Controller populates one entry per tier.
type ResolvedStorageClass struct {
	// Name is the name of the resolved Kubernetes StorageClass.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Tier is the storage tier this StorageClass provides,
	// taken from the osac.openshift.io/storage-tier label.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$`
	Tier string `json:"tier"`
}

// StorageBackendStatus tracks provisioning status for a single storage backend.
type StorageBackendStatus struct {
	// Name is the storage backend identifier (e.g., "vast-1").
	Name string `json:"name"`
	// Provider is the storage provider type (e.g., "vast").
	Provider string `json:"provider"`
	// Ready indicates whether this backend is provisioned for the tenant.
	Ready bool `json:"ready"`
	// Message provides human-readable status or error information.
	Message string `json:"message,omitempty"`
}

// ClusterStorageStatus tracks StorageClass installation status for a single cluster.
type ClusterStorageStatus struct {
	// ClusterName identifies the target cluster.
	ClusterName string `json:"clusterName"`
	// Ready indicates whether StorageClasses are installed on this cluster.
	Ready bool `json:"ready"`
	// Reason provides a machine-readable reason for the current state.
	Reason string `json:"reason,omitempty"`
}

// TenantStatus defines the observed state of Tenant.
type TenantStatus struct {
	// Phase is the phase of the tenant
	Phase TenantPhaseType `json:"phase,omitempty"`

	// Namespace is the namespace allocated to the tenant on the target cluster
	Namespace string `json:"namespace,omitempty"`

	// StorageClasses lists all resolved StorageClass mappings for the tenant,
	// one per storage tier.
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=tier
	StorageClasses []ResolvedStorageClass `json:"storageClasses,omitempty"`

	// Conditions holds an array of metav1.Condition that describe the state of the Tenant
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// ProvisioningJobs holds the history of provisioning jobs triggered for this tenant
	ProvisioningJobs []JobStatus `json:"provisioningJobs,omitempty"`

	// StorageBackendJobs holds the history of storage backend provisioning/deprovisioning jobs
	StorageBackendJobs []JobStatus `json:"storageBackendJobs,omitempty"`

	// ClusterStorageJobs holds the history of cluster storage provisioning/deprovisioning jobs
	ClusterStorageJobs []JobStatus `json:"clusterStorageJobs,omitempty"`

	// StorageBackends tracks per-backend provisioning status.
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=name
	StorageBackends []StorageBackendStatus `json:"storageBackends,omitempty"`

	// ClusterStorage tracks per-cluster StorageClass installation status.
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=clusterName
	ClusterStorage []ClusterStorageStatus `json:"clusterStorage,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Tenant Namespace",type=string,JSONPath=`.status.namespace`
// +kubebuilder:printcolumn:name="Storage Classes",type=string,JSONPath=`.status.storageClasses[*].name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Backend Ready",type=string,JSONPath=`.status.conditions[?(@.type=="StorageBackendReady")].status`,priority=1
// +kubebuilder:printcolumn:name="Cluster Storage",type=string,JSONPath=`.status.conditions[?(@.type=="ClusterStorageReady")].status`,priority=1

// Tenant is the Schema for the tenants API.
type Tenant struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TenantSpec   `json:"spec,omitempty"`
	Status TenantStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TenantList contains a list of Tenant.
type TenantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Tenant `json:"items"`
}
