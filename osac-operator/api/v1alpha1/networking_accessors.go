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

// Accessors that let the network task engine (internal/networktask) work on
// every CR with job entries.

// GetNetworkJobs returns status.jobs.
func (o *VirtualNetwork) GetNetworkJobs() []NetworkJobStatus { return o.Status.Jobs }

// SetNetworkJobs replaces status.jobs.
func (o *VirtualNetwork) SetNetworkJobs(jobs []NetworkJobStatus) { o.Status.Jobs = jobs }

// GetNetworkJobs returns status.jobs.
func (o *Subnet) GetNetworkJobs() []NetworkJobStatus { return o.Status.Jobs }

// SetNetworkJobs replaces status.jobs.
func (o *Subnet) SetNetworkJobs(jobs []NetworkJobStatus) { o.Status.Jobs = jobs }

// GetNetworkJobs returns status.jobs.
func (o *SecurityGroup) GetNetworkJobs() []NetworkJobStatus { return o.Status.Jobs }

// SetNetworkJobs replaces status.jobs.
func (o *SecurityGroup) SetNetworkJobs(jobs []NetworkJobStatus) { o.Status.Jobs = jobs }

// GetNetworkJobs returns status.jobs.
func (o *ExternalIPPool) GetNetworkJobs() []NetworkJobStatus { return o.Status.Jobs }

// SetNetworkJobs replaces status.jobs.
func (o *ExternalIPPool) SetNetworkJobs(jobs []NetworkJobStatus) { o.Status.Jobs = jobs }

// GetNetworkJobs returns status.jobs.
func (o *ExternalIP) GetNetworkJobs() []NetworkJobStatus { return o.Status.Jobs }

// SetNetworkJobs replaces status.jobs.
func (o *ExternalIP) SetNetworkJobs(jobs []NetworkJobStatus) { o.Status.Jobs = jobs }

// GetNetworkJobs returns status.jobs.
func (o *ExternalIPAttachment) GetNetworkJobs() []NetworkJobStatus { return o.Status.Jobs }

// SetNetworkJobs replaces status.jobs.
func (o *ExternalIPAttachment) SetNetworkJobs(jobs []NetworkJobStatus) { o.Status.Jobs = jobs }

// GetNetworkJobs returns status.jobs.
func (o *NATGateway) GetNetworkJobs() []NetworkJobStatus { return o.Status.Jobs }

// SetNetworkJobs replaces status.jobs.
func (o *NATGateway) SetNetworkJobs(jobs []NetworkJobStatus) { o.Status.Jobs = jobs }

// GetNetworkJobs returns status.jobs.
func (o *FabricDomain) GetNetworkJobs() []NetworkJobStatus { return o.Status.Jobs }

// SetNetworkJobs replaces status.jobs.
func (o *FabricDomain) SetNetworkJobs(jobs []NetworkJobStatus) { o.Status.Jobs = jobs }

// GetNetworkJobs returns status.jobs.
func (o *NetworkAttachment) GetNetworkJobs() []NetworkJobStatus { return o.Status.Jobs }

// SetNetworkJobs replaces status.jobs.
func (o *NetworkAttachment) SetNetworkJobs(jobs []NetworkJobStatus) { o.Status.Jobs = jobs }

// GetNetworkJobs returns status.jobs.
func (o *ComputeInstance) GetNetworkJobs() []NetworkJobStatus { return o.Status.Jobs }

// SetNetworkJobs replaces status.jobs.
func (o *ComputeInstance) SetNetworkJobs(jobs []NetworkJobStatus) { o.Status.Jobs = jobs }

// GetTaskInput returns status.taskInput.
func (o *VirtualNetwork) GetTaskInput() *runtime.RawExtension { return o.Status.TaskInput }

// SetTaskInput replaces status.taskInput.
func (o *VirtualNetwork) SetTaskInput(input *runtime.RawExtension) { o.Status.TaskInput = input }

// GetTaskInput returns status.taskInput.
func (o *Subnet) GetTaskInput() *runtime.RawExtension { return o.Status.TaskInput }

// SetTaskInput replaces status.taskInput.
func (o *Subnet) SetTaskInput(input *runtime.RawExtension) { o.Status.TaskInput = input }

// GetTaskInput returns status.taskInput.
func (o *SecurityGroup) GetTaskInput() *runtime.RawExtension { return o.Status.TaskInput }

// SetTaskInput replaces status.taskInput.
func (o *SecurityGroup) SetTaskInput(input *runtime.RawExtension) { o.Status.TaskInput = input }

// GetTaskInput returns status.taskInput.
func (o *ExternalIPPool) GetTaskInput() *runtime.RawExtension { return o.Status.TaskInput }

// SetTaskInput replaces status.taskInput.
func (o *ExternalIPPool) SetTaskInput(input *runtime.RawExtension) { o.Status.TaskInput = input }

// GetTaskInput returns status.taskInput.
func (o *ExternalIP) GetTaskInput() *runtime.RawExtension { return o.Status.TaskInput }

// SetTaskInput replaces status.taskInput.
func (o *ExternalIP) SetTaskInput(input *runtime.RawExtension) { o.Status.TaskInput = input }

// GetTaskInput returns status.taskInput.
func (o *ExternalIPAttachment) GetTaskInput() *runtime.RawExtension { return o.Status.TaskInput }

// SetTaskInput replaces status.taskInput.
func (o *ExternalIPAttachment) SetTaskInput(input *runtime.RawExtension) { o.Status.TaskInput = input }

// GetTaskInput returns status.taskInput.
func (o *NATGateway) GetTaskInput() *runtime.RawExtension { return o.Status.TaskInput }

// SetTaskInput replaces status.taskInput.
func (o *NATGateway) SetTaskInput(input *runtime.RawExtension) { o.Status.TaskInput = input }

// GetTaskInput returns status.taskInput.
func (o *FabricDomain) GetTaskInput() *runtime.RawExtension { return o.Status.TaskInput }

// SetTaskInput replaces status.taskInput.
func (o *FabricDomain) SetTaskInput(input *runtime.RawExtension) { o.Status.TaskInput = input }

// GetTaskInput returns status.taskInput.
func (o *NetworkAttachment) GetTaskInput() *runtime.RawExtension { return o.Status.TaskInput }

// SetTaskInput replaces status.taskInput.
func (o *NetworkAttachment) SetTaskInput(input *runtime.RawExtension) { o.Status.TaskInput = input }

// GetNetworkManagers returns status.networkManagers.
func (o *VirtualNetwork) GetNetworkManagers() *NetworkManagers { return o.Status.NetworkManagers }

// SetNetworkManagers replaces status.networkManagers.
func (o *VirtualNetwork) SetNetworkManagers(managers *NetworkManagers) {
	o.Status.NetworkManagers = managers
}

// GetConditionsRef returns a pointer to status.conditions.
func (o *VirtualNetwork) GetConditionsRef() *[]metav1.Condition { return &o.Status.Conditions }

// GetNetworkManagers returns status.networkManagers.
func (o *Subnet) GetNetworkManagers() *NetworkManagers { return o.Status.NetworkManagers }

// SetNetworkManagers replaces status.networkManagers.
func (o *Subnet) SetNetworkManagers(managers *NetworkManagers) { o.Status.NetworkManagers = managers }

// GetConditionsRef returns a pointer to status.conditions.
func (o *Subnet) GetConditionsRef() *[]metav1.Condition { return &o.Status.Conditions }

// GetNetworkManagers returns status.networkManagers.
func (o *SecurityGroup) GetNetworkManagers() *NetworkManagers { return o.Status.NetworkManagers }

// SetNetworkManagers replaces status.networkManagers.
func (o *SecurityGroup) SetNetworkManagers(managers *NetworkManagers) {
	o.Status.NetworkManagers = managers
}

// GetConditionsRef returns a pointer to status.conditions.
func (o *SecurityGroup) GetConditionsRef() *[]metav1.Condition { return &o.Status.Conditions }

// GetNetworkManagers returns status.networkManagers.
func (o *ExternalIPPool) GetNetworkManagers() *NetworkManagers { return o.Status.NetworkManagers }

// SetNetworkManagers replaces status.networkManagers.
func (o *ExternalIPPool) SetNetworkManagers(managers *NetworkManagers) {
	o.Status.NetworkManagers = managers
}

// GetConditionsRef returns a pointer to status.conditions.
func (o *ExternalIPPool) GetConditionsRef() *[]metav1.Condition { return &o.Status.Conditions }

// GetNetworkManagers returns status.networkManagers.
func (o *ExternalIP) GetNetworkManagers() *NetworkManagers { return o.Status.NetworkManagers }

// SetNetworkManagers replaces status.networkManagers.
func (o *ExternalIP) SetNetworkManagers(managers *NetworkManagers) {
	o.Status.NetworkManagers = managers
}

// GetConditionsRef returns a pointer to status.conditions.
func (o *ExternalIP) GetConditionsRef() *[]metav1.Condition { return &o.Status.Conditions }

// GetNetworkManagers returns status.networkManagers.
func (o *ExternalIPAttachment) GetNetworkManagers() *NetworkManagers { return o.Status.NetworkManagers }

// SetNetworkManagers replaces status.networkManagers.
func (o *ExternalIPAttachment) SetNetworkManagers(managers *NetworkManagers) {
	o.Status.NetworkManagers = managers
}

// GetConditionsRef returns a pointer to status.conditions.
func (o *ExternalIPAttachment) GetConditionsRef() *[]metav1.Condition { return &o.Status.Conditions }

// GetNetworkManagers returns status.networkManagers.
func (o *NATGateway) GetNetworkManagers() *NetworkManagers { return o.Status.NetworkManagers }

// SetNetworkManagers replaces status.networkManagers.
func (o *NATGateway) SetNetworkManagers(managers *NetworkManagers) {
	o.Status.NetworkManagers = managers
}

// GetConditionsRef returns a pointer to status.conditions.
func (o *NATGateway) GetConditionsRef() *[]metav1.Condition { return &o.Status.Conditions }

// GetNetworkManagers returns status.networkManagers.
func (o *FabricDomain) GetNetworkManagers() *NetworkManagers { return o.Status.NetworkManagers }

// SetNetworkManagers replaces status.networkManagers.
func (o *FabricDomain) SetNetworkManagers(managers *NetworkManagers) {
	o.Status.NetworkManagers = managers
}

// GetConditionsRef returns a pointer to status.conditions.
func (o *FabricDomain) GetConditionsRef() *[]metav1.Condition { return &o.Status.Conditions }

// GetNetworkManagers returns status.networkManagers.
func (o *NetworkAttachment) GetNetworkManagers() *NetworkManagers { return o.Status.NetworkManagers }

// SetNetworkManagers replaces status.networkManagers.
func (o *NetworkAttachment) SetNetworkManagers(managers *NetworkManagers) {
	o.Status.NetworkManagers = managers
}

// GetConditionsRef returns a pointer to status.conditions.
func (o *NetworkAttachment) GetConditionsRef() *[]metav1.Condition { return &o.Status.Conditions }

var (
	_ NetworkResourceObject = &VirtualNetwork{}

	_ NetworkResourceObject = &Subnet{}

	_ NetworkResourceObject = &SecurityGroup{}

	_ NetworkResourceObject = &ExternalIPPool{}

	_ NetworkResourceObject = &ExternalIP{}

	_ NetworkResourceObject = &ExternalIPAttachment{}

	_ NetworkResourceObject = &NATGateway{}

	_ NetworkResourceObject = &FabricDomain{}

	_ NetworkResourceObject = &NetworkAttachment{}

	_ NetworkTaskObject = &ComputeInstance{}
)
