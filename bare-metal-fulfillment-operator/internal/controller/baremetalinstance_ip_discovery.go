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

package controller

import (
	"context"
	"net"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

func setIPDiscoveryComplete(bmi *v1alpha1.BareMetalInstance, status metav1.ConditionStatus, reason, message string) {
	bmi.SetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete, status, reason, message)
}

// reconcileIPDiscovery mirrors the tenant DHCP lease into the BareMetalInstance
// status. The osac-operator networking controller runs query_dhcp_lease once
// NetworkHandoffComplete is True and records the accepted lease in the
// NetworkAttachment status.
func (r *BareMetalInstanceReconciler) reconcileIPDiscovery(
	ctx context.Context,
	bareMetalInstance *v1alpha1.BareMetalInstance,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if len(bareMetalInstance.Spec.NetworkAttachments) == 0 {
		setIPDiscoveryComplete(bareMetalInstance, metav1.ConditionTrue, "Skipped", "No network attachments configured")
		return ctrl.Result{}, nil
	}

	// IP discovery must run only after the fabric port has moved to the tenant network
	// and the host has rebooted there; otherwise we would read/expose a provisioning
	// -network lease to the tenant.
	if !bareMetalInstance.IsStatusConditionTrue(v1alpha1.HostConditionNetworkHandoffComplete) {
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
	}

	if !r.NetworkingEnabled {
		log.Info("Networking provisioning disabled, skipping IP discovery")
		setIPDiscoveryComplete(bareMetalInstance, metav1.ConditionTrue, "Skipped", "Networking provisioning disabled")
		return ctrl.Result{}, nil
	}

	attachment := primaryNetworkAttachment(bareMetalInstance)
	if attachment == nil {
		setIPDiscoveryComplete(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonTemplateFailed,
			"the BareMetalInstance has no primary network attachment")
		return ctrl.Result{}, nil
	}
	key, ok := networkAttachmentKey(bareMetalInstance, attachment)
	if !ok {
		setIPDiscoveryComplete(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonTemplateFailed,
			"the BareMetalInstance has no NetworkAttachment")
		return ctrl.Result{}, nil
	}
	na, err := r.getNetworkAttachment(ctx, key)
	if err != nil {
		return ctrl.Result{}, err
	}
	if na == nil {
		setIPDiscoveryComplete(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonTemplateFailed,
			"the NetworkAttachment of the BareMetalInstance does not exist")
		return ctrl.Result{}, nil
	}

	if lease := na.Status.DHCPLease; lease != nil && net.ParseIP(lease.IPAddress) != nil {
		r.initNetworkAttachmentStatuses(bareMetalInstance)
		// Status entries are keyed by the immutable subnetRef, like the spec list.
		for i := range bareMetalInstance.Status.NetworkAttachmentStatuses {
			if bareMetalInstance.Status.NetworkAttachmentStatuses[i].SubnetRef == attachment.SubnetRef {
				bareMetalInstance.Status.NetworkAttachmentStatuses[i].IPAddress = lease.IPAddress
			}
		}
		log.Info("Discovered IP for attachment", "interface", na.Spec.Interface.Name, "subnet", na.Spec.Subnet,
			"ip", lease.IPAddress)
		setIPDiscoveryComplete(bareMetalInstance, metav1.ConditionTrue, "Succeeded", "DHCP lease discovery completed")
		return ctrl.Result{}, nil
	}

	if job := queryDHCPLeaseJob(na); job != nil && job.State == opv1alpha1.NetworkJobStateFailed {
		setIPDiscoveryComplete(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonTemplateFailed, job.Message)
		return ctrl.Result{}, nil
	}

	setIPDiscoveryComplete(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonProgressing,
		"DHCP lease discovery in progress")
	return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
}

// queryDHCPLeaseJob returns the latest query_dhcp_lease job of a NetworkAttachment.
func queryDHCPLeaseJob(na *opv1alpha1.NetworkAttachment) *opv1alpha1.NetworkJobStatus {
	for i := range na.Status.Jobs {
		job := &na.Status.Jobs[i]
		if job.Task == "query_dhcp_lease" && job.Manager == opv1alpha1.NetworkJobManagerFabric {
			return job
		}
	}
	return nil
}

// initNetworkAttachmentStatuses ensures a status entry exists for every
// attachment, matched by the immutable subnetRef, and syncs Interface and
// Primary from spec to status without overwriting a discovered IP address.
func (r *BareMetalInstanceReconciler) initNetworkAttachmentStatuses(
	bareMetalInstance *v1alpha1.BareMetalInstance,
) {
	primary := primaryNetworkAttachment(bareMetalInstance)
	for i := range bareMetalInstance.Spec.NetworkAttachments {
		attachment := &bareMetalInstance.Spec.NetworkAttachments[i]
		statuses := &bareMetalInstance.Status.NetworkAttachmentStatuses
		j := slices.IndexFunc(*statuses, func(s v1alpha1.BareMetalNetworkAttachmentStatus) bool {
			return s.SubnetRef == attachment.SubnetRef
		})
		if j < 0 {
			*statuses = append(*statuses, v1alpha1.BareMetalNetworkAttachmentStatus{SubnetRef: attachment.SubnetRef})
			j = len(*statuses) - 1
		}
		(*statuses)[j].Interface = attachment.Interface
		(*statuses)[j].Primary = attachment == primary
	}
}
