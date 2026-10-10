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
// +kubebuilder:validation:XValidation:rule="has(self.addOnOperators) == has(oldSelf.addOnOperators) && (!has(self.addOnOperators) || self.addOnOperators == oldSelf.addOnOperators)",message="addOnOperators is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.networkAttachment) == has(oldSelf.networkAttachment) && (!has(self.networkAttachment) || self.networkAttachment == oldSelf.networkAttachment)",message="networkAttachment is immutable after creation"
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
	// to build the cluster. Each request selects a BareMetalInstanceType.
	// +kubebuilder:validation:Optional
	NodeRequests []NodeRequest `json:"nodeRequests,omitempty"`
	// AddOnOperators lists the stable names of operators requested for the cluster.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MinLength=1
	AddOnOperators []string `json:"addOnOperators,omitempty"`

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

	// NetworkAttachment connects this cluster to a tenant subnet.
	// All node sets share the same subnet; the fabric interface for each
	// node set is resolved from the selected BareMetalInstanceType.
	// When omitted, the system populates the field from the tenant's
	// default subnet and security groups during creation.
	// +kubebuilder:validation:Optional
	NetworkAttachment *ClusterNetworkAttachment `json:"networkAttachment,omitempty"`
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

// ClusterNetworkAttachment defines the network attachment for a cluster,
// connecting it to a tenant subnet with optional security groups.
type ClusterNetworkAttachment struct {
	// SubnetRef is the name of the Subnet CR that the cluster connects to.
	// The subnet must be in Ready state at creation time.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	SubnetRef string `json:"subnetRef"`

	// SecurityGroupRefs lists SecurityGroup CR names to apply to the cluster's
	// network attachment. All security groups must belong to the same virtual
	// network as the subnet.
	// +kubebuilder:validation:Optional
	SecurityGroupRefs []string `json:"securityGroupRefs,omitempty"`
}

type NodeRequest struct {
	// NodeSet is the logical group key from the Fulfillment Cluster's spec.node_sets.
	// It is independent of the hardware profile selected by BareMetal.InstanceType.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	NodeSet string `json:"nodeSet"`
	// NumberOfNodes describes the desired number of nodes of this instance type.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	NumberOfNodes int `json:"numberOfNodes"`
	// BareMetal holds bare-metal-specific configuration for this node set.
	// This node request targets bare-metal workers.
	// +kubebuilder:validation:Required
	BareMetal *BareMetalNodeSpec `json:"bareMetal,omitempty"`
	// FabricInterface is the host NIC name used for tenant network traffic.
	// When set, IP discovery filters Agent inventory interfaces by this name,
	// preventing the provisioning NIC address from being returned.
	// Resolved from the instance type's fabric network port; may also be set explicitly.
	// +kubebuilder:validation:Optional
	FabricInterface string `json:"fabricInterface,omitempty"`
}

// NodeRequestStatus records the observed node count for an instance type.
// Unlike the desired count in NodeRequest, the observed count can be zero.
type NodeRequestStatus struct {
	// NodeSet identifies the logical group whose observed count is reported.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	NodeSet string `json:"nodeSet"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	NumberOfNodes int `json:"numberOfNodes"`
	// +kubebuilder:validation:Required
	BareMetal *BareMetalNodeSpec `json:"bareMetal,omitempty"`
	// +kubebuilder:validation:Optional
	FabricInterface string `json:"fabricInterface,omitempty"`
}

// BareMetalNodeSpec holds configuration specific to bare-metal node requests.
type BareMetalNodeSpec struct {
	// InstanceType names the BareMetalInstanceType (hardware profile) for the
	// nodes in this request.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	InstanceType string `json:"instanceType"`
}

// IsBareMetal reports whether this node request targets bare-metal workers.
func (nr NodeRequest) IsBareMetal() bool {
	return nr.BareMetal != nil
}

// HasBareMetalNodeSet reports whether the ClusterOrder requests any bare-metal node set.
func (co *ClusterOrder) HasBareMetalNodeSet() bool {
	for i := range co.Spec.NodeRequests {
		if co.Spec.NodeRequests[i].IsBareMetal() {
			return true
		}
	}
	return false
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

	// ClusterOrderConditionAddOnOperatorsReady indicates whether all add-on
	// operators have been successfully installed on the provisioned cluster.
	// Owned by the AddOnOperatorReconciler. Does not gate Phase=Ready.
	ClusterOrderConditionAddOnOperatorsReady ClusterOrderConditionType = "AddOnOperatorsReady"
	// ClusterOrderConditionFulfillmentTrustReady indicates whether fulfillment trust is synchronized.
	ClusterOrderConditionFulfillmentTrustReady ClusterOrderConditionType = "FulfillmentTrustReady"
)

// ClusterOrderClusterReferenceType contains a reference to the namespace created by this ClusterOrder
type ClusterOrderClusterReferenceType struct {
	// Namespace that contains the HostedCluster resource
	Namespace          string `json:"namespace"`
	HostedClusterName  string `json:"hostedClusterName"`
	ServiceAccountName string `json:"serviceAccountName"`
	RoleBindingName    string `json:"roleBindingName"`
}

// AddOnOperatorJobStatus tracks one add-on operator installation attempt.
// Name is the stable Ansible role name for the operator.
type AddOnOperatorJobStatus struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	JobStatus `json:",inline"`
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
	NodeRequests []NodeRequestStatus `json:"nodeRequests,omitempty"`

	// ProvisioningJobs tracks the history of provision and deprovision operations
	// Ordered chronologically, with latest operations at the end
	// Limited to the last N jobs (configurable via OSAC_MAX_JOB_HISTORY, default 10)
	// +kubebuilder:validation:Optional
	ProvisioningJobs []JobStatus `json:"provisioningJobs,omitempty"`

	// ClusterStorageJobs holds the history of cluster storage provisioning/deprovisioning jobs
	// +kubebuilder:validation:Optional
	ClusterStorageJobs []JobStatus `json:"clusterStorageJobs,omitempty"`

	// AddOnOperatorJobs holds the per-operator installation job history.
	// One entry is recorded for each operator attempt.
	// +kubebuilder:validation:Optional
	AddOnOperatorJobs []AddOnOperatorJobStatus `json:"addOnOperatorJobs,omitempty"`
	// FulfillmentTrustBundleHash is the hash of the last synchronized fulfillment trust bundle.
	// +kubebuilder:validation:Optional
	FulfillmentTrustBundleHash string `json:"fulfillmentTrustBundleHash,omitempty"`

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

	// DesiredWorkers is the sum of the positive numberOfNodes values of the
	// requested bare-metal node sets. It reports the requested capacity even before
	// any reservation or backing instance exists, and is independent of the length
	// of status.workers.
	// Populated by the BareMetalWorkerReconciler.
	// +kubebuilder:validation:Optional
	DesiredWorkers *int32 `json:"desiredWorkers,omitempty"`

	// CurrentWorkers is the number of retained requested slots that hold a
	// verified BareMetalInstance identity in an active phase (Provisioning,
	// WaitingForAgent, Binding, or Ready). Identity-less reservations, Failed,
	// retiring, surplus, and non-bare-metal entries are excluded.
	// Populated by the BareMetalWorkerReconciler.
	// +kubebuilder:validation:Optional
	CurrentWorkers *int32 `json:"currentWorkers,omitempty"`

	// ReadyWorkers is the subset of CurrentWorkers in the Ready phase, limited to
	// the requested node-set membership.
	// Populated by the BareMetalWorkerReconciler.
	// +kubebuilder:validation:Optional
	ReadyWorkers *int32 `json:"readyWorkers,omitempty"`

	// Workers holds per-worker lifecycle state for CaaS-managed worker resources.
	// Populated by and owned by the BareMetalWorkerReconciler, which also owns the
	// aggregate counts above.
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=name
	Workers []WorkerStatus `json:"workers,omitempty"`
}

// BareMetalInstanceReference identifies the fulfillment resource backing a worker.
// Name is reserved before creation; ID is populated after creation succeeds.
type BareMetalInstanceReference struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name,omitempty"`
	// +kubebuilder:validation:Optional
	ID string `json:"id,omitempty"`
}

const (
	WorkerBMICreateStateReserved  = "Reserved"
	WorkerBMICreateStateAttempted = "Attempted"
)

// WorkerStatus holds the lifecycle state of a single CaaS-managed worker resource.
type WorkerStatus struct {
	// NodeSet is the Fulfillment spec.node_sets map key identifying this worker's
	// logical group (e.g. "compute", "gpu"), not its hardware-profile name.
	// +kubebuilder:validation:Required
	NodeSet string `json:"nodeSet"`

	// InstanceType is the BareMetalInstanceType (hardware profile) this worker was
	// provisioned from. Exposed as the instance_type metric label; sourced from
	// spec.nodeRequests[].bareMetal.instanceType.
	// +kubebuilder:validation:Optional
	InstanceType string `json:"instanceType,omitempty"`

	// Name is the stable worker slot identity, unique within the cluster.
	// New slots use opaque generated names, independent of the ClusterOrder name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Kind of the backing resource (e.g. BareMetalInstance).
	// +kubebuilder:validation:Required
	Kind string `json:"kind"`

	// BareMetalInstance records the reserved name and fulfillment ID of the backing BMI.
	// +kubebuilder:validation:Optional
	BareMetalInstance BareMetalInstanceReference `json:"bareMetalInstance,omitempty"`

	// BMICreateState distinguishes a never-attempted reservation from a BMI Create
	// that may have reached fulfillment. Reserved is initialized only for a new
	// reservation or after confirmed incarnation cleanup. Attempted is persisted
	// with optimistic locking before calling Create and retained on unknown outcomes.
	// An omitted value is legacy/unknown and must not authorize cancellation.
	// A recorded BMI ID takes precedence over this field.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=Reserved;Attempted
	BMICreateState string `json:"bmiCreateState,omitempty"`

	// Phase of the worker lifecycle.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Provisioning;WaitingForAgent;Binding;Ready;Failed;Unbinding;Deleting
	Phase string `json:"phase"`

	// CreationTimestamp is when this worker entry was first created.
	// +kubebuilder:validation:Required
	CreationTimestamp metav1.Time `json:"creationTimestamp"`

	// AttemptCount tracks how many times this worker slot has been provisioned.
	AttemptCount int32 `json:"attemptCount"`

	// AttemptStartedAt is the durable start of the current provisioning attempt.
	// It is recorded before the first BMI Create and survives lost acknowledgements,
	// so the agent registration timeout is measured from the attempt, not from the
	// parent ClusterOrder or an unrelated failure timestamp. It is not refreshed on
	// errors or re-observation, and is cleared only after the old attempt's cleanup
	// completes.
	// +kubebuilder:validation:Optional
	AttemptStartedAt *metav1.Time `json:"attemptStartedAt,omitempty"`

	// LastFailureReason is a machine-readable reason for the last failure
	// (e.g. AgentRegistrationTimeout).
	// +kubebuilder:validation:Optional
	LastFailureReason string `json:"lastFailureReason,omitempty"`

	// LastFailureMessage is a human-readable description of the last failure.
	// +kubebuilder:validation:Optional
	LastFailureMessage string `json:"lastFailureMessage,omitempty"`

	// LastFailureTime is when the last failure occurred.
	// +kubebuilder:validation:Optional
	LastFailureTime *metav1.Time `json:"lastFailureTime,omitempty"`

	// NextRetryTime is when the controller will attempt the next retry.
	// +kubebuilder:validation:Optional
	NextRetryTime *metav1.Time `json:"nextRetryTime,omitempty"`

	// ReadySince is when the worker first transitioned to Ready after the most recent retry.
	// Used to determine when attemptCount can be reset after MinHealthyDuration.
	// +kubebuilder:validation:Optional
	ReadySince *metav1.Time `json:"readySince,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:validation:XValidation:rule="has(self.spec) == has(oldSelf.spec)",message="spec presence is immutable after creation"
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
