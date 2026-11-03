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

package networktask

import (
	"fmt"
	"strings"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// Task names of the network manager contract §4.10.
const (
	CreateVirtualNetwork    = "create_virtual_network"
	DeleteVirtualNetwork    = "delete_virtual_network"
	CreateSubnet            = "create_subnet"
	DeleteSubnet            = "delete_subnet"
	CreateSecurityGroup     = "create_security_group"
	DeleteSecurityGroup     = "delete_security_group"
	ApplyNetworkAttachment  = "apply_network_attachment"
	DeleteNetworkAttachment = "delete_network_attachment"
	QueryDHCPLease          = "query_dhcp_lease"
	CreateExternalIPPool    = "create_external_ip_pool"
	DeleteExternalIPPool    = "delete_external_ip_pool"
	CreateExternalIP        = "create_external_ip"
	DeleteExternalIP        = "delete_external_ip"
	AttachExternalIP        = "attach_external_ip"
	DetachExternalIP        = "detach_external_ip"
	CreateNATGateway        = "create_nat_gateway"
	DeleteNATGateway        = "delete_nat_gateway"
	CreateFabricDomain      = "create_fabric_domain"
	DeleteFabricDomain      = "delete_fabric_domain"
	ReserveClusterIngressIP = "reserve_cluster_ingress_ip"
	ReleaseClusterIngressIP = "release_cluster_ingress_ip"
)

// FabricTasks is the fixed Fabric Manager task set (21 tasks).
var FabricTasks = map[string]bool{
	CreateVirtualNetwork: true, DeleteVirtualNetwork: true,
	CreateSubnet: true, DeleteSubnet: true,
	CreateSecurityGroup: true, DeleteSecurityGroup: true,
	ApplyNetworkAttachment: true, DeleteNetworkAttachment: true,
	QueryDHCPLease:       true,
	CreateExternalIPPool: true, DeleteExternalIPPool: true,
	CreateExternalIP: true, DeleteExternalIP: true,
	AttachExternalIP: true, DetachExternalIP: true,
	CreateNATGateway: true, DeleteNATGateway: true,
	CreateFabricDomain: true, DeleteFabricDomain: true,
	ReserveClusterIngressIP: true, ReleaseClusterIngressIP: true,
}

// KubernetesTasks is the fixed Kubernetes Manager task set (8 tasks).
var KubernetesTasks = map[string]bool{
	CreateVirtualNetwork: true, DeleteVirtualNetwork: true,
	CreateSubnet: true, DeleteSubnet: true,
	CreateSecurityGroup: true, DeleteSecurityGroup: true,
	ApplyNetworkAttachment: true, DeleteNetworkAttachment: true,
}

// ValidateTask rejects a task outside the manager type's fixed set (contract
// §4.3). Jobs with manager none are not manager tasks and are not checked.
func ValidateTask(task string, manager v1alpha1.NetworkJobManager) error {
	switch manager {
	case v1alpha1.NetworkJobManagerFabric:
		if FabricTasks[task] {
			return nil
		}
	case v1alpha1.NetworkJobManagerKubernetes:
		if KubernetesTasks[task] {
			return nil
		}
	case v1alpha1.NetworkJobManagerNone:
		if task != "" {
			return nil
		}
	}
	return fmt.Errorf("task %q is not in the fixed task set of the %s manager", task, manager)
}

// IsCleanupTask reports whether a task is a delete, detach, release or
// removal. Cleanup tasks are retried after every failure, whatever osac_error
// says, and may run while the resource is being deleted.
func IsCleanupTask(task string) bool {
	for _, prefix := range []string{"delete_", "detach_", "release_", "remove_"} {
		if strings.HasPrefix(task, prefix) {
			return true
		}
	}
	return task == "deprovision"
}

// TemplateName is the AAP job template of a task: <prefix>-<task with dashes>.
func TemplateName(prefix, task string) string {
	return prefix + "-" + strings.ReplaceAll(task, "_", "-")
}

// JobLabel is the AAP label of one launch: osac-<task>-<manager>-<API ID>-<attempt>.
func JobLabel(task string, manager v1alpha1.NetworkJobManager, apiID string, attempt int32) string {
	return fmt.Sprintf("osac-%s-%s-%s-%d", task, manager, apiID, attempt)
}

// FindJob returns the entry of task and manager, or nil.
func FindJob(jobs []v1alpha1.NetworkJobStatus, task string, manager v1alpha1.NetworkJobManager) *v1alpha1.NetworkJobStatus {
	for i := range jobs {
		if jobs[i].Task == task && jobs[i].Manager == manager {
			return &jobs[i]
		}
	}
	return nil
}

// Started reports whether a job of task and manager was ever recorded. The
// finalizer rule (contract §4.7) runs a cleanup task only for managers whose
// create, apply, attach or reserve started.
func Started(obj v1alpha1.NetworkTaskObject, task string, manager v1alpha1.NetworkJobManager) bool {
	return FindJob(obj.GetNetworkJobs(), task, manager) != nil
}

// Succeeded reports whether the latest job of task and manager succeeded.
func Succeeded(obj v1alpha1.NetworkTaskObject, task string, manager v1alpha1.NetworkJobManager) bool {
	entry := FindJob(obj.GetNetworkJobs(), task, manager)
	return entry != nil && entry.State == v1alpha1.NetworkJobStateSucceeded
}

func setJob(obj v1alpha1.NetworkTaskObject, entry v1alpha1.NetworkJobStatus) {
	jobs := append([]v1alpha1.NetworkJobStatus(nil), obj.GetNetworkJobs()...)
	for i := range jobs {
		if jobs[i].Task == entry.Task && jobs[i].Manager == entry.Manager {
			jobs[i] = entry
			obj.SetNetworkJobs(jobs)
			return
		}
	}
	obj.SetNetworkJobs(append(jobs, entry))
}

// ManagerRef returns the recorded manager of a manager type, or nil.
func ManagerRef(managers *v1alpha1.NetworkManagers, manager v1alpha1.NetworkJobManager) *v1alpha1.NetworkManagerRef {
	if managers == nil {
		return nil
	}
	switch manager {
	case v1alpha1.NetworkJobManagerFabric:
		return managers.Fabric
	case v1alpha1.NetworkJobManagerKubernetes:
		return managers.Kubernetes
	}
	return nil
}
