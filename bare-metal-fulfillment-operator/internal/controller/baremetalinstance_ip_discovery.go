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
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const (
	leaseNotFoundAfter  = 10 * time.Minute
	leaseNotFoundMarker = "No DHCP lease was found 10 minutes after handoff; discovery continues."
	maxIPDiscoveryJobs  = 10
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
	// and the host has power-cycled there; otherwise we would read/expose a provisioning
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
		setIPDiscoveryComplete(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonIPDiscoveryFailed,
			"the BareMetalInstance has no primary network attachment")
		return ctrl.Result{}, nil
	}
	_, ok := networkAttachmentKey(bareMetalInstance, attachment)
	if !ok {
		setIPDiscoveryComplete(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonIPDiscoveryFailed,
			"the BareMetalInstance has no NetworkAttachment")
		return ctrl.Result{}, nil
	}
	na, _, err := r.getExistingNetworkAttachment(ctx, bareMetalInstance, attachment)
	if err != nil {
		return ctrl.Result{}, err
	}
	if na == nil {
		setIPDiscoveryComplete(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonIPDiscoveryFailed,
			"the NetworkAttachment of the BareMetalInstance does not exist")
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		return ctrl.Result{}, nil
	}

	job := queryDHCPLeaseJob(na)
	recordIPDiscoveryJob(bareMetalInstance, job)

	if lease := na.Status.DHCPLease; lease != nil && net.ParseIP(lease.IPAddress) != nil {
		previous := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
		wasComplete := previous != nil && previous.Status == metav1.ConditionTrue
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
		if !wasComplete {
			r.recordBMIEvent(bareMetalInstance, corev1.EventTypeNormal, "IPDiscovered", "Tenant DHCP lease was discovered")
		}
		return ctrl.Result{}, nil
	}

	if job != nil && job.State == opv1alpha1.NetworkJobStateFailed {
		message := networkJobDiagnostic(job)
		previous := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
		if previous == nil || previous.Reason != v1alpha1.HostConditionReasonIPDiscoveryFailed {
			r.recordBMIEvent(bareMetalInstance, corev1.EventTypeWarning,
				v1alpha1.HostConditionReasonIPDiscoveryFailed, message)
		}
		setIPDiscoveryComplete(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonIPDiscoveryFailed, message)
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		return ctrl.Result{}, nil
	}

	message := networkJobDiagnostic(job)
	if message == "" {
		message = "Waiting for the tenant DHCP lease"
	}
	handoff := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete)
	if handoff != nil && handoff.Status == metav1.ConditionTrue && !handoff.LastTransitionTime.IsZero() &&
		time.Since(handoff.LastTransitionTime.Time) >= leaseNotFoundAfter {
		previous := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
		if previous == nil || !strings.Contains(previous.Message, leaseNotFoundMarker) {
			r.recordBMIEvent(bareMetalInstance, corev1.EventTypeWarning, "LeaseNotFound",
				"No tenant DHCP lease was found 10 minutes after handoff; lease queries continue")
		}
		message = strings.TrimSpace(message + "; " + leaseNotFoundMarker)
	}
	setIPDiscoveryComplete(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonLeasePending, message)
	return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
}

func networkJobDiagnostic(job *opv1alpha1.NetworkJobStatus) string {
	if job == nil {
		return ""
	}
	message := job.Message
	if job.JobID != "" {
		if message != "" {
			message += "; "
		}
		message += "AAP job ID: " + job.JobID
	}
	return message
}

func recordIPDiscoveryJob(bmi *v1alpha1.BareMetalInstance, job *opv1alpha1.NetworkJobStatus) {
	if job == nil {
		return
	}
	target := fmt.Sprintf("%s:%s:attempt-%d", job.Task, job.Manager, job.Attempt)
	status := opv1alpha1.JobStatus{
		JobID:   job.JobID,
		Type:    opv1alpha1.JobTypeProvision,
		State:   ipDiscoveryJobState(job.State),
		Message: job.Message,
		Target:  target,
	}
	jobs := bmi.Status.IPDiscoveryJobs
	for i := range jobs {
		if jobs[i].Target == target {
			status.Timestamp = jobs[i].Timestamp
			jobs[i] = status
			bmi.Status.IPDiscoveryJobs = jobs
			return
		}
	}
	status.Timestamp = metav1.Now()
	jobs = append(jobs, status)
	if len(jobs) > maxIPDiscoveryJobs {
		jobs = slices.Clone(jobs[len(jobs)-maxIPDiscoveryJobs:])
	}
	bmi.Status.IPDiscoveryJobs = jobs
}

func ipDiscoveryJobState(state opv1alpha1.NetworkJobState) opv1alpha1.JobState {
	switch state {
	case opv1alpha1.NetworkJobStateRunning:
		return opv1alpha1.JobStateRunning
	case opv1alpha1.NetworkJobStateRetrying:
		return opv1alpha1.JobStateWaiting
	case opv1alpha1.NetworkJobStateSucceeded:
		return opv1alpha1.JobStateSucceeded
	case opv1alpha1.NetworkJobStateFailed:
		return opv1alpha1.JobStateFailed
	case opv1alpha1.NetworkJobStateCancelled:
		return opv1alpha1.JobStateCanceled
	default:
		return opv1alpha1.JobStateUnknown
	}
}

// queryDHCPLeaseJob returns the latest query_dhcp_lease job of a NetworkAttachment.
func queryDHCPLeaseJob(na *opv1alpha1.NetworkAttachment) *opv1alpha1.NetworkJobStatus {
	for i := len(na.Status.Jobs) - 1; i >= 0; i-- {
		job := &na.Status.Jobs[i]
		if job.Task == "query_dhcp_lease" && job.Manager == opv1alpha1.NetworkJobManagerFabric {
			return job
		}
	}
	return nil
}

// initNetworkAttachmentStatuses ensures a status entry exists for every
// attachment, matched by the immutable subnetRef, and syncs Interface and
// Primary from spec without overwriting its recorded MAC or discovered IP.
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
