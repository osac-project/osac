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

// Types shared by the networking CRs and the workload CRs that run network
// manager tasks (network manager integration contract §4.1, §4.5, §4.7-§4.9).

// NetworkJobManager is the manager type a job runs on: a manager task on the
// Fabric or Kubernetes Manager, or none for a job that is not a manager task
// (for example the VMaaS provision job).
// +kubebuilder:validation:Enum=fabric;kubernetes;none
type NetworkJobManager string

const (
	// NetworkJobManagerFabric is a task of the Fabric Manager.
	NetworkJobManagerFabric NetworkJobManager = "fabric"
	// NetworkJobManagerKubernetes is a task of the OSAC Kubernetes Manager.
	NetworkJobManagerKubernetes NetworkJobManager = "kubernetes"
	// NetworkJobManagerNone is a job that is not a manager task.
	NetworkJobManagerNone NetworkJobManager = "none"
)

// NetworkJobState is the state of the latest job of one task and manager.
// +kubebuilder:validation:Enum=Running;Retrying;Succeeded;Failed;Cancelled
type NetworkJobState string

const (
	// NetworkJobStateRunning means the job was launched (or is about to be) and has not ended.
	NetworkJobStateRunning NetworkJobState = "Running"
	// NetworkJobStateRetrying means the job failed and a retry is scheduled.
	NetworkJobStateRetrying NetworkJobState = "Retrying"
	// NetworkJobStateSucceeded means the job succeeded and OSAC accepted its result.
	NetworkJobStateSucceeded NetworkJobState = "Succeeded"
	// NetworkJobStateFailed means the job failed finally.
	NetworkJobStateFailed NetworkJobState = "Failed"
	// NetworkJobStateCancelled means OSAC cancelled the job for a delete, or a retry never launched.
	NetworkJobStateCancelled NetworkJobState = "Cancelled"
)

// NetworkJobStatus tracks the latest AAP job of one task on one manager
// (contract §4.7 Job tracking). OSAC never removes an entry.
type NetworkJobStatus struct {
	// Task is the task name, for example create_subnet.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Task string `json:"task"`

	// Manager is the manager type the task runs on.
	// +kubebuilder:validation:Required
	Manager NetworkJobManager `json:"manager"`

	// Attempt counts the task's launches on this manager.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	Attempt int32 `json:"attempt"`

	// State is the state of the latest job.
	// +kubebuilder:validation:Required
	State NetworkJobState `json:"state"`

	// JobID is the AAP job ID; empty until AAP returned it.
	// +kubebuilder:validation:Optional
	JobID string `json:"jobID,omitempty"`

	// Message is the job's osac_error message, the AAP failure text, or why
	// OSAC rejected the result; while retrying it also names the next retry time.
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`
}

// NetworkManagerRef records one manager selected for a resource.
type NetworkManagerRef struct {
	// Name is the registration name of the manager.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// AnsibleRole is the fully qualified Ansible role of the manager.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	AnsibleRole string `json:"ansibleRole"`
}

// NetworkManagers records the managers of a resource at create (contract §4.1).
type NetworkManagers struct {
	// Fabric is the Fabric Manager.
	// +kubebuilder:validation:Optional
	Fabric *NetworkManagerRef `json:"fabric,omitempty"`

	// Kubernetes is the OSAC Kubernetes Manager; absent when none is selected.
	// +kubebuilder:validation:Optional
	Kubernetes *NetworkManagerRef `json:"kubernetes,omitempty"`
}

// NetworkType names the multi-tenancy technology of a network (contract §4.9).
// +kubebuilder:validation:Enum=evpn
type NetworkType string

const (
	// NetworkTypeEVPN is an EVPN control plane with a VXLAN data plane.
	NetworkTypeEVPN NetworkType = "evpn"
)

// SubnetAddressPlan gives the shares of a Subnet's addresses, in sixteenths,
// placed in the order overlay, fabric, lbVip (contract §4.8). The shares sum
// to 16. The fulfillment API carries the same plan as fractions (n/16).
// +kubebuilder:validation:XValidation:rule="self.overlay + self.fabric + self.lbVip == 16",message="addressPlan shares must sum to 16 sixteenths"
type SubnetAddressPlan struct {
	// Overlay is the share of VM addresses from the Kubernetes Manager, in sixteenths.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=16
	Overlay int32 `json:"overlay"`

	// Fabric is the share of the Fabric Manager's range, in sixteenths.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=16
	Fabric int32 `json:"fabric"`

	// LBVIP is the share of LoadBalancer VIP reservations, in sixteenths.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=16
	LBVIP int32 `json:"lbVip"`
}

// SubnetIPAM is the address layout of a Subnet, computed once at create (contract §4.8).
type SubnetIPAM struct {
	// GatewayIPv4 is the Subnet's network address plus one.
	// +kubebuilder:validation:Required
	GatewayIPv4 string `json:"gatewayIpv4"`

	// OverlayIPv4CIDRs is the overlay range.
	// +kubebuilder:validation:Optional
	OverlayIPv4CIDRs []string `json:"overlayIpv4Cidrs,omitempty"`

	// PhysicalIPv4CIDRs is the Fabric Manager's range.
	// +kubebuilder:validation:Optional
	PhysicalIPv4CIDRs []string `json:"physicalIpv4Cidrs,omitempty"`

	// LBVIPIPv4CIDRs is the LoadBalancer VIP range.
	// +kubebuilder:validation:Optional
	LBVIPIPv4CIDRs []string `json:"lbVipIpv4Cidrs,omitempty"`
}

// NetworkSegment is the segment the Fabric Manager returned for a Subnet (contract §4.9).
// The L3 VNI belongs to the VirtualNetwork (VirtualNetworkStatus.L3VNI).
type NetworkSegment struct {
	// Type is the network type of the segment.
	// +kubebuilder:validation:Required
	Type NetworkType `json:"type"`

	// L2VNI is the Subnet's L2 VNI.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=16777215
	L2VNI int32 `json:"l2Vni"`
}

// Ready condition reasons of the networking CRs (Unified design, Status).
const (
	// ReasonSucceeded means every task the resource needs succeeded.
	ReasonSucceeded = "Succeeded"
	// ReasonUpdating means a SecurityGroup rule update runs on a READY group.
	ReasonUpdating = "Updating"
	// ReasonProvisioningDisabled means provider networking is disabled and no task ran.
	ReasonProvisioningDisabled = "ProvisioningDisabled"
	// ReasonProgressing (conditions.go) and ReasonFailed (conditions.go) are the
	// other Ready reasons.
)

// Condition type of every networking CR.
const (
	// NetworkConditionReady is the only condition type of a networking CR.
	NetworkConditionReady = "Ready"
)

// NetworkTaskObject is implemented by every CR whose status has job entries
// (networking CRs, ComputeInstance, ClusterOrder).
// +kubebuilder:object:generate=false
type NetworkTaskObject interface {
	metav1.Object
	runtime.Object
	// GetNetworkJobs returns status.jobs.
	GetNetworkJobs() []NetworkJobStatus
	// SetNetworkJobs replaces status.jobs.
	SetNetworkJobs([]NetworkJobStatus)
}

// NetworkInputObject is a NetworkTaskObject with a stored task input.
// +kubebuilder:object:generate=false
type NetworkInputObject interface {
	NetworkTaskObject
	// GetTaskInput returns status.taskInput.
	GetTaskInput() *runtime.RawExtension
	// SetTaskInput replaces status.taskInput.
	SetTaskInput(*runtime.RawExtension)
}

// NetworkResourceObject is a networking CR: it records its managers and has a Ready condition.
// +kubebuilder:object:generate=false
type NetworkResourceObject interface {
	NetworkInputObject
	// GetNetworkManagers returns status.networkManagers (nil when not recorded yet).
	GetNetworkManagers() *NetworkManagers
	// SetNetworkManagers replaces status.networkManagers.
	SetNetworkManagers(*NetworkManagers)
	// GetConditionsRef returns a pointer to status.conditions.
	GetConditionsRef() *[]metav1.Condition
}
