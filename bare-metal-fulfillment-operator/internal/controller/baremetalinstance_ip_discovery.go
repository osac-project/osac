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
			v1alpha1.HostConditionReasonProvisioningDisabled,
			"Provider networking disabled; skipped DHCP lease discovery",
		)
		return ctrl.Result{}, nil
	}

	// Lease mirroring runs only after the fabric port moved and the handoff
	// reboot completed; otherwise we would expose a provisioning-network lease.
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
		Namespace: r.NetworkingNamespace,
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

	queryJob := latestNetworkAttachmentQueryJob(na)
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

	msg := latestNetworkAttachmentQueryMessage(na)
	if msg == "" {
		msg = "waiting for NetworkAttachment DHCP lease"
	}
	if warningPrefix := r.maybeEmitLeaseNotFound(ctx, bareMetalInstance); warningPrefix != "" {
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

// maybeEmitLeaseNotFound emits Warning LeaseNotFound once after the design 10m
// window. The condition message remains the newest DHCP-query job message.
func (r *BareMetalInstanceReconciler) maybeEmitLeaseNotFound(
	ctx context.Context, bmi *v1alpha1.BareMetalInstance,
) string {
	handoff := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete)
	if handoff == nil || handoff.Status != metav1.ConditionTrue || handoff.LastTransitionTime.IsZero() {
		return ""
	}
	if time.Since(handoff.LastTransitionTime.Time) < LeaseNotFoundWarningAfter {
		return ""
	}
	warningPrefix := fmt.Sprintf("No tenant DHCP lease after %s since network handoff", LeaseNotFoundWarningAfter)
	pending := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
	alreadyWarned := pending != nil && strings.HasPrefix(pending.Message, warningPrefix)
	if !alreadyWarned {
		logf.FromContext(ctx).Info(warningPrefix)
		if r.Recorder != nil {
			r.Recorder.Eventf(bmi, nil, corev1.EventTypeWarning,
				eventReasonLeaseNotFound, "Reconcile", "%s", warningPrefix)
		}
	}
	return warningPrefix
}

func latestNetworkAttachmentQueryMessage(na *opv1alpha1.NetworkAttachment) string {
	for i := len(na.Status.Jobs) - 1; i >= 0; i-- {
		job := na.Status.Jobs[i]
		if job.Task != networkAttachmentQueryTask {
			continue
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
	return ""
}

func latestNetworkAttachmentQueryJob(na *opv1alpha1.NetworkAttachment) *opv1alpha1.NetworkJobStatus {
	for i := len(na.Status.Jobs) - 1; i >= 0; i-- {
		if na.Status.Jobs[i].Task == networkAttachmentQueryTask {
			return &na.Status.Jobs[i]
		}
	}
	return nil
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
