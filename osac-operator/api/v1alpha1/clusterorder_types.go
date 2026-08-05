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

// Important: Run "make" to regenerate code after modifying this file

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ClusterOrderSpec defines the desired state of ClusterOrder
type ClusterOrderSpec struct {
	// TemplateID is the unique identigier of the cluster template to use when creating this cluster
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=^[a-zA-Z_][a-zA-Z0-9._]*$
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="templateID is immutable"
	TemplateID string `json:"templateID,omitempty"`
	// TemplateParameters is a JSON-encoded map of the parameter values for the
	// selected cluster template.
	// +kubebuilder:validation:Optional
	TemplateParameters string `json:"templateParameters,omitempty"`
	// NodeRequests defines the types of nodes and number of each type of node that will be used
	// to build the cluster. This value is optional and if not provided will be filled in with template-provided
	// defaults. The selected template may limit what node types you can request.
	// +kubebuilder:validation:Optional
	NodeRequests []NodeRequest `json:"nodeRequests,omitempty"`

	// PullSecret contains credentials for authenticating to container image repositories.
	// If not provided, the provider's default pull secret is used.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=262144
	PullSecret string `json:"pullSecret,omitempty"`
	// SSHPublicKey is an SSH public key installed on cluster worker nodes.
	// If not provided, the provider's default SSH key is used.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=16384
	SSHPublicKey string `json:"sshPublicKey,omitempty"`
	// ReleaseImage is the OCP release image URL that controls the OpenShift version.
	// If not provided, the template's default release image is used.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:Pattern=`^.+/.+:.+$`
	ReleaseImage string `json:"releaseImage,omitempty"`
	// Network contains cluster networking configuration.
	// +kubebuilder:validation:Optional
	Network *ClusterNetworkSpec `json:"network,omitempty"`
}

// ClusterNetworkSpec defines networking configuration for a cluster.
type ClusterNetworkSpec struct {
	// PodCIDR is the CIDR for the cluster's pod network.
	// Defaults to 10.128.0.0/14 if not specified.
	// +kubebuilder:validation:Optional
	// Coarse format check only — full CIDR validation (e.g. net.ParseCIDR) is done server-side.
	// +kubebuilder:validation:Pattern=`^([0-9]{1,3}\.){3}[0-9]{1,3}/[0-9]{1,2}$`
	PodCIDR string `json:"podCIDR,omitempty"`
	// ServiceCIDR is the CIDR for the cluster's service network.
	// Defaults to 172.30.0.0/16 if not specified.
	// +kubebuilder:validation:Optional
	// Coarse format check only — full CIDR validation (e.g. net.ParseCIDR) is done server-side.
	// +kubebuilder:validation:Pattern=`^([0-9]{1,3}\.){3}[0-9]{1,3}/[0-9]{1,2}$`
	ServiceCIDR string `json:"serviceCIDR,omitempty"`
}

type NodeRequest struct {
	// ResourceClass describes the type of node you are requesting
	// +kubebuilder:validation:Required
	ResourceClass string `json:"resourceClass"`
	// NumberOfNodes describes the number of nodes you want of the given resource class
	// +kubebuilder:validation:Required
	NumberOfNodes int `json:"numberOfNodes"`
}

// ClusterOrderPhaseType is a valid value for .status.phase
type ClusterOrderPhaseType string

const (
	// ClusterOrderPhaseProgressing means an update is in progress
	ClusterOrderPhaseProgressing ClusterOrderPhaseType = "Progressing"

	// ClusterOrderPhaseFailed means the cluster deployment or update has failed
	ClusterOrderPhaseFailed ClusterOrderPhaseType = "Failed"

	// ClusterOrderPhaseReady means the cluster and all associated resources are ready
	ClusterOrderPhaseReady ClusterOrderPhaseType = "Ready"

	// ClusterOrderPhaseDeleting means there has been a request to delete the ClusterOrder
	ClusterOrderPhaseDeleting ClusterOrderPhaseType = "Deleting"
)

// ClusterOrderConditionType is a valid value for .status.conditions.type
type ClusterOrderConditionType string

const (
	// ClusterOrderConditionAccepted means the order has been accepted but work has not yet started
	ClusterOrderConditionAccepted ClusterOrderConditionType = "Accepted"

	// ClusterOrderConditionProgressing means that an update is in progress
	ClusterOrderConditionProgressing ClusterOrderConditionType = "Progressing"

	// ClusterOrderConditionControlPlaneAvailable means the cluster control plane is ready
	ClusterOrderConditionControlPlaneAvailable ClusterOrderConditionType = "ControlPlaneAvailable"

	// ClusterOrderConditionAvailable means the cluster is available
	ClusterOrderConditionAvailable ClusterOrderConditionType = "Available"

	// ClusterOrderConditionClusterStorageReady indicates whether StorageClasses
	// and CSI drivers are installed on the CaaS cluster for this tenant.
	// Owned by the OSAC Storage Controller. Does not gate Phase=Ready.
	ClusterOrderConditionClusterStorageReady ClusterOrderConditionType = "ClusterStorageReady"
)

// ClusterOrderClusterReferenceType contains a reference to the namespace created by this ClusterOrder
type ClusterOrderClusterReferenceType struct {
	// Namespace that contains the HostedCluster resource
	Namespace          string `json:"namespace"`
	HostedClusterName  string `json:"hostedClusterName"`
	ServiceAccountName string `json:"serviceAccountName"`
	RoleBindingName    string `json:"roleBindingName"`
}

// ClusterOrderStatus defines the observed state of ClusterOrder
type ClusterOrderStatus struct {
	// Phase provides a single-value overview of the state of the ClusterOrder
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Enum=Progressing;Failed;Ready;Deleting
	Phase ClusterOrderPhaseType `json:"phase,omitempty"`

	// Conditions holds an array of metav1.Condition that describe the state of the ClusterOrder
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// Reference to the namespace that contains the HostedCluster resource
	// +kubebuilder:validation:Optional
	ClusterReference *ClusterOrderClusterReferenceType `json:"clusterReference,omitempty"`

	// NodeRequests reflects how many nodes are currently associated with the ClusterOrder
	NodeRequests []NodeRequest `json:"nodeRequests,omitempty"`

	// ProvisioningJobs tracks the history of provision and deprovision operations
	// Ordered chronologically, with latest operations at the end
	// Limited to the last N jobs (configurable via OSAC_MAX_JOB_HISTORY, default 10)
	// +kubebuilder:validation:Optional
	ProvisioningJobs []JobStatus `json:"provisioningJobs,omitempty"`

	// ClusterStorageJobs holds the history of cluster storage provisioning/deprovisioning jobs
	// +kubebuilder:validation:Optional
	ClusterStorageJobs []JobStatus `json:"clusterStorageJobs,omitempty"`

	// DesiredConfigVersion is a hash of the current spec, used to detect spec changes
	// that require re-provisioning.
	// +kubebuilder:validation:Optional
	DesiredConfigVersion string `json:"desiredConfigVersion,omitempty"`

	// ApiEndpoint is the internal API server VIP allocated by MetalLB.
	// Written by the CaaS template after VIP discovery.
	// +kubebuilder:validation:Optional
	ApiEndpoint string `json:"apiEndpoint,omitempty"`

	// IngressEndpoint is the internal ingress VIP allocated by MetalLB.
	// Written by the CaaS template after VIP discovery.
	// +kubebuilder:validation:Optional
	IngressEndpoint string `json:"ingressEndpoint,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=cord
// +kubebuilder:printcolumn:name="Template",type=string,JSONPath=`.spec.templateID`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Cluster Storage",type=string,JSONPath=`.status.conditions[?(@.type=="ClusterStorageReady")].status`,priority=1

// ClusterOrder is the Schema for the clusterorders API
type ClusterOrder struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterOrderSpec   `json:"spec,omitempty"`
	Status ClusterOrderStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterOrderList contains a list of ClusterOrder
type ClusterOrderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterOrder `json:"items"`
}

// GetName returns the name of the ClusterOrder resource
func (co *ClusterOrder) GetName() string {
	return co.ObjectMeta.Name
}
