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
	"strings"

	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BareMetalInstanceRunStrategy controls the desired power state of a BareMetalInstance.
// +kubebuilder:validation:Enum=Always;Halted;""
type BareMetalInstanceRunStrategy string

const (
	// RunStrategyUnspecified means power state is not managed.
	RunStrategyUnspecified BareMetalInstanceRunStrategy = ""

	// RunStrategyAlways keeps the instance powered on.
	RunStrategyAlways BareMetalInstanceRunStrategy = "Always"

	// RunStrategyHalted keeps the instance powered off.
	RunStrategyHalted BareMetalInstanceRunStrategy = "Halted"
)

// BareMetalNetworkAttachment defines one NIC: a Subnet reference, optional SecurityGroup
// references, a physical interface binding, and primary gateway designation.
type BareMetalNetworkAttachment struct {
	// SubnetRef is the fulfillment Subnet ID. Must reference a Subnet in READY state.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="subnetRef is immutable"
	SubnetRef string `json:"subnetRef"`

	// SecurityGroupRefs lists fulfillment SecurityGroup IDs applied on this NIC.
	// Mutable — can be changed to add/remove security groups.
	// +kubebuilder:validation:Optional
	SecurityGroupRefs []string `json:"securityGroupRefs,omitempty"`

	// Interface is the physical interface name from the HostType's NetworkInterface list.
	// When omitted on a single-attachment instance, the system selects the first fabric-role interface.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="interface is immutable"
	Interface string `json:"interface,omitempty"`

	// Primary designates this attachment as the default gateway for multi-NIC instances.
	// When omitted on a single-attachment instance, that attachment is implicitly primary.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="primary is immutable"
	Primary bool `json:"primary,omitempty"`
}

// BareMetalInstanceSpec defines the desired state of BareMetalInstance.
type BareMetalInstanceSpec struct {
	// HostType is the resource class/type of the host.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="field is immutable"
	HostType string `json:"hostType"`
	// ExternalHostID is the host ID from external inventory (used by Host Management Operator as node identifier).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Type=string
	ExternalHostID string `json:"externalHostID"`
	// ExternalHostName is the host name from external inventory.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	ExternalHostName string `json:"externalHostName,omitempty"`
	// HostClass is host management backend class (e.g. openstack).
	HostClass string `json:"hostClass,omitempty"`
	// Selector defines additional host selection filters.
	// hostSelector accepts arbitrary key/value selectors such as managedBy or topology.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="field is immutable"
	Selector HostSelectorSpec `json:"selector,omitempty"`
	// InventoryLabels are labels to be applied to the host in the inventory system.
	// These labels are non-persistent and will be removed when the BareMetalInstance is deleted.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="field is immutable"
	InventoryLabels map[string]string `json:"inventorylabels,omitempty"`
	// InventoryPersistentLabels are labels to be applied to the host in the inventory system.
	// These labels are persistent and will remain on the host after the BareMetalInstance is deleted.
	// Persistent labels override InventoryLabels with the same key.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="field is immutable"
	InventoryPersistentLabels map[string]string `json:"inventorypersistentlabels,omitempty"`
	// TemplateID is the unique identifier of the host template to use.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=^[a-zA-Z_][a-zA-Z0-9._]*$
	TemplateID string `json:"templateID"`
	// TemplateParameters is a JSON-encoded map of the parameter values for the
	// selected host template.
	// +kubebuilder:validation:Optional
	TemplateParameters string `json:"templateParameters,omitempty"`
	// RunStrategy controls the desired power state of the instance.
	// "Always" keeps the instance powered on; "Halted" powers it off.
	// When empty, the operator will not manage the host's power state.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=Always;Halted
	RunStrategy BareMetalInstanceRunStrategy `json:"runStrategy,omitempty"`
	// RestartTrigger is used to trigger a restart of the physical machine.
	// Change it to any value to trigger the restart.
	// The value itself is not important; only the change matters.
	// +kubebuilder:validation:Optional
	RestartTrigger int64 `json:"restartTrigger"`
	// NetworkAttachments for the bare metal instance. One entry per physical NIC.
	// The list structure is immutable after creation (entries cannot be added or removed),
	// but securityGroupRefs within each entry can be updated.
	//
	// MaxItems is required for CEL cost budget calculation.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:XValidation:rule="size(oldSelf) == 0 || (size(self) == size(oldSelf) && self.all(na, oldSelf.exists(old, old.subnetRef == na.subnetRef)))",message="cannot change or add/remove network attachments after initial assignment"
	// +kubebuilder:validation:XValidation:rule="self.size() <= 1 || self.filter(x, x.primary == true).size() == 1",message="when multiple network attachments exist, exactly one must have primary set to true"
	// +listType=map
	// +listMapKey=subnetRef
	NetworkAttachments []BareMetalNetworkAttachment `json:"networkAttachments,omitempty"`
}

// BareMetalInstancePhaseType is a valid value for .status.phase
type BareMetalInstancePhaseType string

const (
	// BareMetalInstancePhaseAllocating means searching for a free host to allocate
	BareMetalInstancePhaseAllocating BareMetalInstancePhaseType = "Allocating"

	// BareMetalInstancePhaseProgressing means the host is being worked on (allocating, provisioning, power changes, etc.)
	BareMetalInstancePhaseProgressing BareMetalInstancePhaseType = "Progressing"

	// BareMetalInstancePhaseReady means the host is ready and stable
	BareMetalInstancePhaseReady BareMetalInstancePhaseType = "Ready"

	// BareMetalInstancePhaseFailed means reconciliation has failed
	BareMetalInstancePhaseFailed BareMetalInstancePhaseType = "Failed"

	// BareMetalInstancePhaseDeleting means the resource is being deleted
	BareMetalInstancePhaseDeleting BareMetalInstancePhaseType = "Deleting"
)

// BareMetalInstanceConditionType is a valid value for .status.conditions.type
type BareMetalInstanceConditionType string

const (
	// HostConditionAllocated means the host has been allocated.
	HostConditionAllocated BareMetalInstanceConditionType = "Allocated"

	// HostConditionAvailable means the host is available for provisioning.
	HostConditionAvailable BareMetalInstanceConditionType = "Available"

	// HostConditionPowerSynced tracks the host power synchronization state.
	// Set condition status to True and reason to PowerOn when power on is successful.
	// Set condition status to True and reason to PowerOff when power off is successful.
	// Set condition status to False and reason to IronicAPIFailure when the operation fails.
	HostConditionPowerSynced BareMetalInstanceConditionType = "PowerSynced"

	// HostConditionProvisionTemplateComplete tracks provision template completion.
	// Set condition status True on success.
	// Set condition status False with reason Progressing or TemplateFailed while not complete.
	HostConditionProvisionTemplateComplete BareMetalInstanceConditionType = "ProvisionTemplateComplete"

	// HostConditionDeprovisionTemplateComplete tracks deprovision template completion.
	// Set condition status True on success.
	// Set condition status False with reason Progressing or TemplateFailed while not complete.
	HostConditionDeprovisionTemplateComplete BareMetalInstanceConditionType = "DeprovisionTemplateComplete"
)

// Host condition reason values
const (
	// HostConditionReasonProgressing indicates the template workflow is still running.
	HostConditionReasonProgressing = "Progressing"

	// HostConditionReasonTemplateFailed indicates the template workflow failed.
	HostConditionReasonTemplateFailed = "TemplateFailed"

	// HostConditionReasonPowerOn indicates the host is powered on successfully.
	HostConditionReasonPowerOn = "PowerOn"

	// HostConditionReasonPowerOff indicates the host is powered off successfully.
	HostConditionReasonPowerOff = "PowerOff"

	// HostConditionReasonIronicAPIFailure indicates a power operation failed due to Ironic API error.
	HostConditionReasonIronicAPIFailure = "IronicAPIFailure"

	// HostConditionReasonPowerSyncFailed indicates a restart has failed
	HostConditionReasonPowerSyncFailed = "PowerSyncFailed"

	// HostConditionReasonPowerSyncRequired indicates a restart is required
	// Reserved for future use — not set by any current code path
	HostConditionReasonPowerSyncRequired = "PowerSyncRequired"
)

// HostSelectorSpec defines additional host selection constraints.
type HostSelectorSpec struct {
	// HostSelector is a map of arbitrary selector key/value pairs
	// (for example managedBy, topology, rack, zone).
	// +kubebuilder:validation:Optional
	HostSelector map[string]string `json:"hostSelector,omitempty"`
}

// BareMetalNICStatus holds the MAC address of a single physical network interface.
type BareMetalNICStatus struct {
	// MAC is the hardware MAC address of this interface, lowercased (e.g. "aa:bb:cc:dd:ee:ff").
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`
	MAC string `json:"mac"`
}

// BareMetalHardware holds physical hardware metadata discovered from the inventory backend.
type BareMetalHardware struct {
	// NICs lists the physical network interfaces reported by the inventory backend.
	// +kubebuilder:validation:Optional
	NICs []BareMetalNICStatus `json:"nics,omitempty"`
}

// BareMetalNetworkAttachmentStatus captures the runtime networking state for a single NIC.
type BareMetalNetworkAttachmentStatus struct {
	// Interface is the physical interface name from the spec's network attachment.
	// +kubebuilder:validation:Optional
	Interface string `json:"interface,omitempty"`

	// SubnetRef is the fulfillment Subnet ID associated with this attachment.
	// +kubebuilder:validation:Optional
	SubnetRef string `json:"subnetRef,omitempty"`

	// IPAddress is the IP discovered after DHCP assignment.
	// +kubebuilder:validation:Optional
	IPAddress string `json:"ipAddress,omitempty"`

	// Primary indicates whether this attachment is the default gateway attachment.
	// +kubebuilder:validation:Optional
	Primary bool `json:"primary,omitempty"`
}

// BareMetalInstanceStatus defines the observed state of BareMetalInstance.
type BareMetalInstanceStatus struct {
	// Phase provides a single-value overview of the state of the BareMetalInstance
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Enum=Allocating;Progressing;Ready;Failed;Deleting
	Phase BareMetalInstancePhaseType `json:"phase,omitempty"`
	// ProvisioningJobs tracks the history of provision and deprovision operations
	// Ordered chronologically, with latest operations at the end
	// Limited to the last N jobs (configurable via OSAC_MAX_JOB_HISTORY, default 10)
	// +kubebuilder:validation:Optional
	ProvisioningJobs []opv1alpha1.JobStatus `json:"provisioningJobs,omitempty"`
	// Conditions holds an array of metav1.Condition describing host state.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
	// DesiredConfigVersion is a hash of the spec, used to detect spec changes and control retry behavior.
	// +kubebuilder:validation:Optional
	DesiredConfigVersion string `json:"desiredConfigVersion,omitempty"`
	// RunStrategy is the observed power state of the instance.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=Always;Halted
	RunStrategy BareMetalInstanceRunStrategy `json:"runStrategy,omitempty"`
	// RestartTrigger is the observed restart version of the instance
	// +kubebuilder:validation:Optional
	RestartTrigger int64 `json:"restartTrigger"`
	// NetworkAttachmentStatuses captures runtime networking state for each NIC.
	// Populated by the operator after DHCP lease discovery.
	// +kubebuilder:validation:Optional
	NetworkAttachmentStatuses []BareMetalNetworkAttachmentStatus `json:"networkAttachmentStatuses,omitempty"`
	// Hardware holds physical hardware metadata fetched from the inventory backend at allocation time.
	// Absent until the inventory backend provides data.
	// +kubebuilder:validation:Optional
	Hardware *BareMetalHardware `json:"hardware,omitempty"`
}

// GetPoolID returns the owning BareMetalPool UID if the BareMetalInstance is owned by a BareMetalPool.
func (h *BareMetalInstance) GetPoolID() (string, bool) {
	for _, ownerReference := range h.OwnerReferences {
		if ownerReference.Controller == nil || !*ownerReference.Controller {
			continue
		}
		if strings.Contains(ownerReference.APIVersion, "osac.openshift.io") && ownerReference.Kind == "BareMetalPool" {
			return string(ownerReference.UID), true
		}
	}
	return "", false
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=bmi
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="HostType",type=string,JSONPath=`.spec.hostType`
// +kubebuilder:printcolumn:name="Template",type=string,JSONPath=`.spec.templateID`
// +kubebuilder:printcolumn:name="HostClass",type=string,JSONPath=`.spec.hostClass`
// +kubebuilder:printcolumn:name="ExternalHostID",type=string,JSONPath=`.spec.externalHostID`

// BareMetalInstance is the Schema for the baremetalinstances API.
type BareMetalInstance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BareMetalInstanceSpec   `json:"spec,omitempty"`
	Status BareMetalInstanceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BareMetalInstanceList contains a list of BareMetalInstance.
type BareMetalInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BareMetalInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BareMetalInstance{}, &BareMetalInstanceList{})
}
