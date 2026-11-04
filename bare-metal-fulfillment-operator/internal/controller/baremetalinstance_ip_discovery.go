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
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

func (r *BareMetalInstanceReconciler) reconcileIPDiscovery(
	ctx context.Context,
	bareMetalInstance *v1alpha1.BareMetalInstance,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if len(bareMetalInstance.Spec.NetworkAttachments) == 0 {
		bareMetalInstance.SetStatusCondition(
			v1alpha1.HostConditionIPDiscoveryComplete,
			metav1.ConditionTrue,
			"Skipped",
			"No network attachments configured",
		)
		return ctrl.Result{}, nil
	}

	if !r.providerNetworkingEnabled() {
		log.Info("Provider networking disabled, skipping DHCP lease mirror")
		bareMetalInstance.SetStatusCondition(
			v1alpha1.HostConditionIPDiscoveryComplete,
			metav1.ConditionTrue,
			v1alpha1.HostConditionReasonNetworkProvisioningDisabled,
			"Provider networking disabled; skipped DHCP lease discovery",
		)
		return ctrl.Result{}, nil
	}

	// Lease mirroring runs only after the fabric port moved and the handoff
	// power cycle completed; otherwise we could expose a provisioning-network lease.
	if !bareMetalInstance.IsStatusConditionTrue(v1alpha1.HostConditionNetworkHandoffComplete) {
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
	}

	apiID := bareMetalInstance.Labels[BareMetalInstanceUUIDLabel]
	if apiID == "" {
		return ctrl.Result{}, fmt.Errorf("BareMetalInstance %s has no %s label",
			bareMetalInstance.Name, BareMetalInstanceUUIDLabel)
	}

	na := &opv1alpha1.NetworkAttachment{}
	key := types.NamespacedName{
		Namespace: bareMetalInstance.Namespace,
		Name:      opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, apiID),
	}
	if err := r.apiReaderOrClient().Get(ctx, key, na); err != nil {
		if apierrors.IsNotFound(err) {
			r.setLeasePending(bareMetalInstance, "waiting for NetworkAttachment DHCP lease")
			return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
		}
		return ctrl.Result{}, err
	}

	r.initNetworkAttachmentStatuses(bareMetalInstance)
	expectedMAC := ""
	if len(bareMetalInstance.Status.NetworkAttachmentStatuses) > 0 {
		expectedMAC = bareMetalInstance.Status.NetworkAttachmentStatuses[0].MACAddress
	}

	if lease := na.Status.DHCPLease; lease != nil {
		if expectedMAC != "" && !strings.EqualFold(lease.MACAddress, expectedMAC) {
			log.Info("Ignoring NetworkAttachment DHCP lease with unexpected MAC")
		} else if net.ParseIP(lease.IPAddress) == nil {
			log.Info("Ignoring NetworkAttachment DHCP lease with invalid IP")
		} else {
			status := &bareMetalInstance.Status.NetworkAttachmentStatuses[0]
			status.IPAddress = lease.IPAddress
			if status.MACAddress == "" && lease.MACAddress != "" {
				status.MACAddress = strings.ToLower(lease.MACAddress)
			}
			r.setNetworkCondition(
				bareMetalInstance,
				v1alpha1.HostConditionIPDiscoveryComplete,
				metav1.ConditionTrue,
				"Succeeded",
				"DHCP lease mirrored from NetworkAttachment",
				corev1.EventTypeNormal,
				"IPDiscovered",
			)
			log.Info("Mirrored NetworkAttachment DHCP lease")
			return ctrl.Result{}, nil
		}
	}

	queryJob := latestNetworkAttachmentJob(na, networkAttachmentQueryTask)
	if queryJob != nil && queryJob.State == opv1alpha1.NetworkJobStateFailed {
		message := queryJob.Message
		if message == "" {
			message = "DHCP lease query failed"
		}
		r.setNetworkCondition(
			bareMetalInstance,
			v1alpha1.HostConditionIPDiscoveryComplete,
			metav1.ConditionFalse,
			v1alpha1.HostConditionReasonIPDiscoveryFailed,
			message,
			corev1.EventTypeWarning,
			v1alpha1.HostConditionReasonIPDiscoveryFailed,
		)
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		return ctrl.Result{}, nil
	}

	msg := latestNetworkAttachmentQueryMessage(queryJob)
	if msg == "" {
		msg = "waiting for NetworkAttachment DHCP lease"
	}
	if leaseNotFoundWarningDue(bareMetalInstance, time.Now()) {
		warningPrefix := leaseNotFoundWarningPrefix()
		if !leaseNotFoundWarningEmitted(bareMetalInstance, warningPrefix) {
			r.emitLeaseNotFound(ctx, bareMetalInstance, warningPrefix)
		}
		msg = warningPrefix + "; latest query: " + msg
	}
	r.setLeasePending(bareMetalInstance, msg)

	return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
}

func (r *BareMetalInstanceReconciler) setLeasePending(bmi *v1alpha1.BareMetalInstance, message string) {
	bmi.SetStatusCondition(
		v1alpha1.HostConditionIPDiscoveryComplete,
		metav1.ConditionFalse,
		v1alpha1.HostConditionReasonLeasePending,
		message,
	)
}

func leaseNotFoundWarningDue(bmi *v1alpha1.BareMetalInstance, now time.Time) bool {
	handoff := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete)
	if handoff == nil || handoff.Status != metav1.ConditionTrue || handoff.LastTransitionTime.IsZero() {
		return false
	}
	return now.Sub(handoff.LastTransitionTime.Time) >= LeaseNotFoundWarningAfter
}

func leaseNotFoundWarningPrefix() string {
	return fmt.Sprintf("No tenant DHCP lease after %s since network handoff", LeaseNotFoundWarningAfter)
}

func leaseNotFoundWarningEmitted(bmi *v1alpha1.BareMetalInstance, warningPrefix string) bool {
	pending := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
	return pending != nil && strings.HasPrefix(pending.Message, warningPrefix)
}

func (r *BareMetalInstanceReconciler) emitLeaseNotFound(
	ctx context.Context, bmi *v1alpha1.BareMetalInstance, warningPrefix string,
) {
	logf.FromContext(ctx).Info(warningPrefix)
	if r.Recorder != nil {
		r.Recorder.Eventf(bmi, nil, corev1.EventTypeWarning,
			eventReasonLeaseNotFound, "Reconcile", "%s", warningPrefix)
	}
}

func latestNetworkAttachmentQueryMessage(job *opv1alpha1.NetworkJobStatus) string {
	if job == nil {
		return ""
	}
	message := job.Message
	if message == "" {
		message = "DHCP lease query in progress"
	}
	if job.JobID != "" {
		return fmt.Sprintf("%s (AAP job ID: %s)", message, job.JobID)
	}
	return message
}

// initNetworkAttachmentStatuses ensures statuses exist for all attachments and syncs
// SubnetRef, Interface, and Primary from spec to status without overwriting
// IPAddress or MACAddress already recorded.
func (r *BareMetalInstanceReconciler) initNetworkAttachmentStatuses(
	bareMetalInstance *v1alpha1.BareMetalInstance,
) {
	for i, na := range bareMetalInstance.Spec.NetworkAttachments {
		if i >= len(bareMetalInstance.Status.NetworkAttachmentStatuses) {
			bareMetalInstance.Status.NetworkAttachmentStatuses = append(
				bareMetalInstance.Status.NetworkAttachmentStatuses,
				v1alpha1.BareMetalNetworkAttachmentStatus{},
			)
		}
		bareMetalInstance.Status.NetworkAttachmentStatuses[i].SubnetRef = na.SubnetRef
		bareMetalInstance.Status.NetworkAttachmentStatuses[i].Interface = na.Interface
		bareMetalInstance.Status.NetworkAttachmentStatuses[i].Primary =
			na.Primary || len(bareMetalInstance.Spec.NetworkAttachments) == 1
	}
}
