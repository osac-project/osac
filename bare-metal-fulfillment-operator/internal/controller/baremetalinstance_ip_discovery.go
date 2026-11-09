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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

func (r *BareMetalInstanceReconciler) reconcileIPDiscovery(
	ctx context.Context,
	bmi *v1alpha1.BareMetalInstance,
) (ctrl.Result, error) {
	if len(bmi.Spec.NetworkAttachments) == 0 {
		bmi.SetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete,
			metav1.ConditionTrue, "Skipped", "No network attachments configured")
		return ctrl.Result{}, nil
	}
	if !r.providerNetworkingEnabled() {
		bmi.SetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete,
			metav1.ConditionTrue, opv1alpha1.ReasonProvisioningDisabled,
			"Provider networking disabled; skipped DHCP lease discovery")
		return ctrl.Result{}, nil
	}
	if !bmi.IsStatusConditionTrue(v1alpha1.HostConditionNetworkHandoffComplete) {
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
	}
	apiID := bmi.Labels[BareMetalInstanceUUIDLabel]
	if apiID == "" {
		return r.failIPDiscovery(bmi, fmt.Sprintf("BareMetalInstance has no %s label", BareMetalInstanceUUIDLabel))
	}

	na := &opv1alpha1.NetworkAttachment{}
	key := bareMetalNetworkAttachmentKey(r.NetworkingNamespace, apiID)
	if err := r.apiReaderOrClient().Get(ctx, key, na); err != nil {
		if apierrors.IsNotFound(err) {
			bmi.SetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete,
				metav1.ConditionFalse, v1alpha1.HostConditionReasonProgressing,
				"waiting for NetworkAttachment DHCP lease")
			return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get NetworkAttachment %s for DHCP lease: %w", key, err)
	}
	if err := verifyBareMetalNetworkAttachment(bmi, na, true); err != nil {
		return r.failIPDiscovery(bmi, err.Error())
	}
	if !na.DeletionTimestamp.IsZero() {
		bmi.SetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete,
			metav1.ConditionFalse, v1alpha1.HostConditionReasonProgressing,
			"waiting for NetworkAttachment DHCP lease")
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
	}

	r.initNetworkAttachmentStatuses(bmi)
	if na.Status.DHCPLease != nil {
		ip := net.ParseIP(na.Status.DHCPLease.IPAddress)
		if ip == nil || ip.To4() == nil {
			return r.failIPDiscovery(bmi, "NetworkAttachment published an invalid DHCP lease IP address")
		}
		status := &bmi.Status.NetworkAttachmentStatuses[0]
		status.IPAddress = ip.String()
		bmi.SetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete,
			metav1.ConditionTrue, "Succeeded", "DHCP lease mirrored from NetworkAttachment")
		return ctrl.Result{}, nil
	}

	queryJob := latestBareMetalNetworkJob(na, bareMetalQueryDHCPLeaseTask)
	if queryJob != nil && queryJob.State == opv1alpha1.NetworkJobStateFailed {
		message := queryJob.Message
		if message == "" {
			message = "DHCP lease query failed"
		}
		if queryJob.JobID != "" {
			message = fmt.Sprintf("%s (AAP job ID: %s)", message, queryJob.JobID)
		}
		return r.failIPDiscovery(bmi, message)
	}

	message := "waiting for NetworkAttachment DHCP lease"
	if queryJob != nil && queryJob.Message != "" {
		message = queryJob.Message
		if queryJob.JobID != "" {
			message = fmt.Sprintf("%s (AAP job ID: %s)", message, queryJob.JobID)
		}
	}
	log.FromContext(ctx).V(1).Info("Waiting for NetworkAttachment DHCP lease", "networkattachment", key.Name)
	bmi.SetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete,
		metav1.ConditionFalse, v1alpha1.HostConditionReasonProgressing, message)
	return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
}

func (r *BareMetalInstanceReconciler) failIPDiscovery(
	bmi *v1alpha1.BareMetalInstance,
	message string,
) (ctrl.Result, error) {
	bmi.SetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete,
		metav1.ConditionFalse, v1alpha1.HostConditionReasonTemplateFailed, message)
	bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
	return ctrl.Result{}, nil
}

// initNetworkAttachmentStatuses ensures the existing BMI status projection is
// present before the NetworkAttachment is created and preserves any IP already
// accepted from its DHCP lease.
func (r *BareMetalInstanceReconciler) initNetworkAttachmentStatuses(bmi *v1alpha1.BareMetalInstance) {
	for i, attachment := range bmi.Spec.NetworkAttachments {
		if i >= len(bmi.Status.NetworkAttachmentStatuses) {
			bmi.Status.NetworkAttachmentStatuses = append(bmi.Status.NetworkAttachmentStatuses,
				v1alpha1.BareMetalNetworkAttachmentStatus{})
		}
		status := &bmi.Status.NetworkAttachmentStatuses[i]
		status.SubnetRef = attachment.SubnetRef
		status.Interface = attachment.Interface
		status.Primary = attachment.Primary || len(bmi.Spec.NetworkAttachments) == 1
	}
}
