package baremetalworker

import (
	"context"
	"net/netip"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type workerNetworkProof struct {
	mac            string
	leaseAddress   string
	networkReadyAt time.Time
	provisionReady time.Time
	bmiReady       bool
	bindingReady   bool
}

func (r *Reconciler) workerNetworkProof(
	ctx context.Context,
	co *v1alpha1.ClusterOrder,
	w v1alpha1.WorkerStatus,
	o *workerObservation,
) (workerNetworkProof, bool, error) {
	var proof workerNetworkProof
	if o == nil {
		return proof, false, nil
	}
	bmi, err := o.getBMI(ctx, r.fulfillment, w.BareMetalInstance.ID)
	if err != nil {
		return proof, false, err
	}
	if bmi == nil {
		return proof, false, nil
	}
	hubBMI := o.hubBMIs[w.BareMetalInstance.ID]
	if hubBMI == nil {
		return proof, false, nil
	}
	proof.bmiReady = hubBMI.Status.Phase == bmfov1alpha1.BareMetalInstancePhaseReady
	if !r.NetworkingProvisioningEnabled {
		return workerProvisioningProof(hubBMI, proof)
	}
	return workerTenantNetworkProof(co, bmi, hubBMI, o, proof)
}

func workerProvisioningProof(hubBMI *bmfov1alpha1.BareMetalInstance, proof workerNetworkProof) (workerNetworkProof, bool, error) {
	provisioned := hubBMI.GetStatusCondition(bmfov1alpha1.HostConditionProvisionTemplateComplete)
	if provisioned == nil || provisioned.Status != conditionStatusTrue || provisioned.LastTransitionTime.IsZero() {
		return proof, false, nil
	}
	proof.provisionReady = provisioned.LastTransitionTime.Time
	return proof, true, nil
}

func workerTenantNetworkProof(
	co *v1alpha1.ClusterOrder, bmi *privatev1.BareMetalInstance,
	hubBMI *bmfov1alpha1.BareMetalInstance, observed *workerObservation,
	proof workerNetworkProof,
) (workerNetworkProof, bool, error) {
	handoff := hubBMI.GetStatusCondition(bmfov1alpha1.HostConditionNetworkHandoffComplete)
	if handoff == nil || handoff.Status != conditionStatusTrue || handoff.LastTransitionTime.IsZero() {
		return proof, false, nil
	}
	proof.networkReadyAt = handoff.LastTransitionTime.Time
	leaseAccepted := hubBMI.GetStatusCondition(bmfov1alpha1.HostConditionIPDiscoveryComplete)
	if leaseAccepted == nil || leaseAccepted.Status != conditionStatusTrue || co.Spec.NetworkAttachment == nil ||
		co.Spec.NetworkAttachment.SubnetRef == "" {
		return proof, true, nil
	}
	specAttachment := privateWorkerNetworkAttachment(bmi, co.Spec.NetworkAttachment.SubnetRef)
	if specAttachment == nil || specAttachment.GetInterface() == "" {
		return proof, true, nil
	}
	interfaceName := specAttachment.GetInterface()
	privateLease := privateWorkerLease(bmi, co.Spec.NetworkAttachment.SubnetRef)
	if privateLease == nil || privateLease.GetInterface() != interfaceName || privateLease.GetIpAddress() == "" {
		return proof, true, nil
	}
	attachment := observed.networkAttachments[bmi.GetId()]
	if !selectedWorkerNetworkAttachmentReady(attachment, interfaceName, co.Spec.NetworkAttachment.SubnetRef) {
		return proof, true, nil
	}
	hubLease := hubWorkerLease(hubBMI, co.Spec.NetworkAttachment.SubnetRef)
	if !hubWorkerLeaseMatches(hubLease, interfaceName, attachment.Spec.Interface.MACAddress, privateLease.GetIpAddress()) {
		return proof, true, nil
	}
	proof.mac = attachment.Spec.Interface.MACAddress
	proof.leaseAddress = privateLease.GetIpAddress()
	proof.bindingReady = true
	return proof, true, nil
}

func privateWorkerNetworkAttachment(bmi *privatev1.BareMetalInstance, subnetRef string) *privatev1.BareMetalNetworkAttachment {
	for _, attachment := range bmi.GetSpec().GetNetworkAttachments() {
		subnet := attachment.GetSubnet()
		if subnet != nil && (subnet.GetId() == subnetRef || (subnet.GetId() == "" && subnet.GetName() == subnetRef)) {
			return attachment
		}
	}
	return nil
}

func privateWorkerLease(bmi *privatev1.BareMetalInstance, subnetRef string) *privatev1.BareMetalNetworkAttachmentStatus {
	for _, attachment := range bmi.GetStatus().GetNetworkAttachmentStatuses() {
		if attachment.GetSubnetRef() == subnetRef {
			return attachment
		}
	}
	return nil
}

func selectedWorkerNetworkAttachmentReady(
	attachment *v1alpha1.NetworkAttachment, interfaceName, subnetRef string,
) bool {
	return attachment != nil && attachment.Spec.Interface.Type == v1alpha1.NetworkInterfacePhysical &&
		attachment.Spec.Interface.Name == interfaceName && attachment.Spec.Subnet == subnetRef &&
		attachment.Spec.Interface.MACAddress != "" &&
		meta.IsStatusConditionTrue(attachment.Status.Conditions, v1alpha1.NetworkConditionReady)
}

func hubWorkerLease(
	hubBMI *bmfov1alpha1.BareMetalInstance, subnetRef string,
) *bmfov1alpha1.BareMetalNetworkAttachmentStatus {
	for i := range hubBMI.Status.NetworkAttachmentStatuses {
		if hubBMI.Status.NetworkAttachmentStatuses[i].SubnetRef == subnetRef {
			return &hubBMI.Status.NetworkAttachmentStatuses[i]
		}
	}
	return nil
}

func hubWorkerLeaseMatches(
	lease *bmfov1alpha1.BareMetalNetworkAttachmentStatus, interfaceName, mac, address string,
) bool {
	return lease != nil && lease.Interface == interfaceName && strings.EqualFold(lease.MACAddress, mac) &&
		lease.IPAddress != "" && lease.IPAddress == address
}

func workerManagementPublicationReady(co *v1alpha1.ClusterOrder) (time.Time, bool) {
	condition := meta.FindStatusCondition(co.Status.Conditions,
		string(v1alpha1.ClusterOrderConditionManagementPublicationReady))
	if condition == nil || condition.Status != conditionStatusTrue || condition.LastTransitionTime.IsZero() {
		return time.Time{}, false
	}
	return condition.LastTransitionTime.Time, true
}

func workerAPIRecordsPublished(co *v1alpha1.ClusterOrder) bool {
	if co.Status.APIDNSRecordsPublishedTime == nil || co.Status.APIDNSRecordsPublishedTime.IsZero() {
		return false
	}
	for _, job := range co.Status.Jobs {
		if job.Task == "publish_public_dns_api" && job.Manager == v1alpha1.NetworkJobManagerNone &&
			job.State == v1alpha1.NetworkJobStateSucceeded {
			return true
		}
	}
	return false
}

func agentMatchesWorkerLease(agent *unstructured.Unstructured, mac, lease string) bool {
	if mac == "" || lease == "" {
		return false
	}
	leaseAddress, err := netip.ParseAddr(lease)
	if err != nil || !leaseAddress.Is4() {
		return false
	}
	interfaces, found, err := unstructured.NestedSlice(agent.Object, "status", "inventory", "interfaces")
	if err != nil || !found {
		return false
	}
	for _, raw := range interfaces {
		iface, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		interfaceMAC, _ := iface["macAddress"].(string)
		if !strings.EqualFold(interfaceMAC, mac) {
			continue
		}
		addresses, ok := iface["ipV4Addresses"].([]interface{})
		if !ok {
			continue
		}
		for _, rawAddress := range addresses {
			value, ok := rawAddress.(string)
			if !ok {
				continue
			}
			address, err := netip.ParsePrefix(value)
			if err == nil && address.Addr().Is4() && address.Addr() == leaseAddress {
				return true
			}
			addressValue, err := netip.ParseAddr(value)
			if err == nil && addressValue.Is4() && addressValue == leaseAddress {
				return true
			}
		}
	}
	return false
}

func registrationDeadline(
	worker v1alpha1.WorkerStatus,
	evidence workerNetworkProof,
	managementReady time.Time,
	apiRecordsPublished *time.Time,
	providerNetworking bool,
) *metav1.Time {
	latest := managementReady
	networkReady := evidence.networkReadyAt
	if !providerNetworking {
		networkReady = evidence.provisionReady
	}
	if networkReady.IsZero() || managementReady.IsZero() {
		return worker.RegistrationDeadline
	}
	if networkReady.After(latest) {
		latest = networkReady
	}
	if providerNetworking {
		if apiRecordsPublished == nil || apiRecordsPublished.IsZero() {
			return worker.RegistrationDeadline
		}
		if apiRecordsPublished.After(latest) {
			latest = *apiRecordsPublished
		}
	}
	deadline := metav1.NewTime(latest.Add(agentRegistrationTimeout))
	if worker.RegistrationDeadline != nil && !deadline.After(worker.RegistrationDeadline.Time) {
		return worker.RegistrationDeadline
	}
	return &deadline
}
