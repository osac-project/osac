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
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/management"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const (
	networkAttachmentApplyTask  = "apply_network_attachment"
	networkAttachmentDeleteTask = "delete_network_attachment"
	networkAttachmentQueryTask  = "query_dhcp_lease"

	eventReasonLeaseNotFound = "LeaseNotFound"
)

// providerNetworkingEnabled reports whether provider networking is configured.
func (r *BareMetalInstanceReconciler) providerNetworkingEnabled() bool {
	return r.NetworkingProvisioningEnabled
}

func (r *BareMetalInstanceReconciler) reconcileNetworkProvisionAndDiscovery(
	ctx context.Context, bmi *v1alpha1.BareMetalInstance,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if bmi.Spec.TemplateID != "" && bmi.Spec.TemplateID != shared.OsacNoopTemplate {
		result, err := r.reconcileProvisioning(ctx, bmi)
		if err != nil {
			bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
			return result, err
		}
		if !result.IsZero() {
			bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
			return result, nil
		}
		provisionCond := bmi.GetStatusCondition(v1alpha1.HostConditionProvisionTemplateComplete)
		if provisionCond != nil && provisionCond.Status != metav1.ConditionTrue {
			if v1alpha1.IsProvisionRetrying(provisionCond) {
				bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
				return ctrl.Result{}, nil
			}
			bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
			return ctrl.Result{}, nil
		}
	}

	if len(bmi.Spec.NetworkAttachments) == 0 {
		return ctrl.Result{}, nil
	}

	result, err := r.reconcileNetworking(ctx, bmi)
	if err != nil {
		bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		return result, err
	}
	if !result.IsZero() {
		bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
		return result, nil
	}
	networkCond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
	if networkCond == nil || networkCond.Status != metav1.ConditionTrue {
		if networkCond != nil && networkCond.Reason == v1alpha1.HostConditionReasonTemplateFailed {
			bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
			return ctrl.Result{}, nil
		}
		bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
	}

	result, err = r.reconcileNetworkHandoffPowerCycle(ctx, bmi)
	if err != nil {
		bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		return result, err
	}
	if bmi.Status.Phase == v1alpha1.BareMetalInstancePhaseFailed {
		return ctrl.Result{}, nil
	}
	if !result.IsZero() {
		bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
		return result, nil
	}

	result, err = r.reconcileIPDiscovery(ctx, bmi)
	if err != nil {
		bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		return result, err
	}
	if !result.IsZero() {
		bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
		return result, nil
	}
	ipCond := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
	if ipCond == nil || ipCond.Status != metav1.ConditionTrue {
		if ipCond != nil && ipCond.Reason == v1alpha1.HostConditionReasonTemplateFailed {
			bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
			return ctrl.Result{}, nil
		}
		bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
	}
	log.Info("Network handoff and IP discovery complete", "bmi", bmi.Name)
	return ctrl.Result{}, nil
}

// reconcileNetworkHandoffPowerCycle moves a host through an observed
// power-off and power-on so its OS acquires a lease on the tenant network.
func (r *BareMetalInstanceReconciler) reconcileNetworkHandoffPowerCycle(
	ctx context.Context, bmi *v1alpha1.BareMetalInstance,
) (ctrl.Result, error) {
	if !r.providerNetworkingEnabled() {
		bmi.SetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete,
			metav1.ConditionTrue, "Skipped", "Networking provider not configured")
		return ctrl.Result{}, nil
	}

	condition := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete)
	if condition != nil {
		if condition.Status == metav1.ConditionTrue {
			return ctrl.Result{}, nil
		}
		if condition.Reason == v1alpha1.HostConditionReasonNetworkHandoffFailed {
			if networkHandoffDeadlineExceeded(bmi, time.Now()) {
				return ctrl.Result{}, nil
			}
			condition = nil
		}
	}

	state, err := r.ManagementClient.GetPowerState(ctx, bmi.Spec.ExternalHostID)
	if err != nil {
		if networkHandoffDeadlineExceeded(bmi, time.Now()) {
			return r.failNetworkHandoff(bmi, fmt.Sprintf("network handoff was not confirmed within %s", NetworkHandoffDeadline)), nil
		}
		return r.retryNetworkHandoff(bmi, handoffReason(condition), fmt.Sprintf("reading host power state: %v", err)), nil
	}
	if state == nil {
		return r.retryNetworkHandoff(bmi, handoffReason(condition), "management backend returned no host power state"), nil
	}

	if condition != nil && condition.Reason == v1alpha1.HostConditionReasonNetworkHandoffPoweringOn &&
		state.State == management.PowerOn && !state.IsTransitioning {
		bmi.SetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete,
			metav1.ConditionTrue, "Succeeded", "Host powered on onto the tenant network")
		return ctrl.Result{}, nil
	}

	if networkHandoffDeadlineExceeded(bmi, time.Now()) {
		return r.failNetworkHandoff(bmi, fmt.Sprintf("network handoff was not confirmed within %s", NetworkHandoffDeadline)), nil
	}
	if state.IsTransitioning {
		return r.retryNetworkHandoff(bmi, handoffReason(condition), "waiting for host power transition"), nil
	}

	var target management.PowerState
	var nextReason, message string
	switch state.State {
	case management.PowerOff:
		target = management.PowerOn
		nextReason = v1alpha1.HostConditionReasonNetworkHandoffPoweringOn
		message = "Powering on host onto the tenant network"
	case management.PowerOn:
		target = management.PowerOff
		nextReason = v1alpha1.HostConditionReasonNetworkHandoffPoweringOff
		message = "Powering off host before tenant-network boot"
	default:
		return r.retryNetworkHandoff(bmi, handoffReason(condition), fmt.Sprintf("unrecognized host power state %q", state.State)), nil
	}

	if err := r.ManagementClient.SetPowerState(ctx, bmi.Spec.ExternalHostID, target); err != nil {
		return r.retryNetworkHandoff(bmi, nextReason, fmt.Sprintf("requesting host power state %q: %v", target, err)), nil
	}
	return r.retryNetworkHandoff(bmi, nextReason, message), nil
}

func handoffReason(condition *metav1.Condition) string {
	if condition == nil {
		return v1alpha1.HostConditionReasonProgressing
	}
	return condition.Reason
}

func (r *BareMetalInstanceReconciler) retryNetworkHandoff(
	bmi *v1alpha1.BareMetalInstance, reason, message string,
) ctrl.Result {
	if reason != v1alpha1.HostConditionReasonNetworkHandoffPoweringOff &&
		reason != v1alpha1.HostConditionReasonNetworkHandoffPoweringOn {
		reason = v1alpha1.HostConditionReasonProgressing
	}
	bmi.SetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete,
		metav1.ConditionFalse, reason, message)
	bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
	interval := r.ManagementRecheckIntervalDuration
	if interval <= 0 {
		interval = DefaultManagementRecheckIntervalDuration
	}
	return ctrl.Result{RequeueAfter: interval}
}

func networkHandoffDeadlineExceeded(bmi *v1alpha1.BareMetalInstance, now time.Time) bool {
	ready := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
	return ready != nil && ready.Status == metav1.ConditionTrue &&
		!ready.LastTransitionTime.IsZero() && now.Sub(ready.LastTransitionTime.Time) >= NetworkHandoffDeadline
}

func (r *BareMetalInstanceReconciler) failNetworkHandoff(
	bmi *v1alpha1.BareMetalInstance, message string,
) ctrl.Result {
	r.setNetworkCondition(
		bmi,
		v1alpha1.HostConditionNetworkHandoffComplete,
		metav1.ConditionFalse,
		v1alpha1.HostConditionReasonNetworkHandoffFailed,
		message,
		corev1.EventTypeWarning,
		v1alpha1.HostConditionReasonNetworkHandoffFailed,
	)
	bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
	return ctrl.Result{}
}

func (r *BareMetalInstanceReconciler) reconcileNetworking(
	ctx context.Context,
	bareMetalInstance *v1alpha1.BareMetalInstance,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if len(bareMetalInstance.Spec.NetworkAttachments) == 0 {
		bareMetalInstance.SetStatusCondition(
			v1alpha1.HostConditionNetworkAttachmentsReady,
			metav1.ConditionTrue,
			"Skipped",
			"No network attachments configured",
		)
		return ctrl.Result{}, nil
	}
	if len(bareMetalInstance.Spec.NetworkAttachments) != 1 {
		message := "BareMetalInstance must have exactly one network attachment"
		r.setNetworkCondition(
			bareMetalInstance,
			v1alpha1.HostConditionNetworkAttachmentsReady,
			metav1.ConditionFalse,
			v1alpha1.HostConditionReasonNetworkAttachmentFailed,
			message,
			corev1.EventTypeWarning,
			v1alpha1.HostConditionReasonNetworkAttachmentFailed,
		)
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		return ctrl.Result{}, nil
	}

	if !r.providerNetworkingEnabled() {
		log.Info("Provider networking disabled, skipping NetworkAttachment create")
		bareMetalInstance.SetStatusCondition(
			v1alpha1.HostConditionNetworkAttachmentsReady,
			metav1.ConditionTrue,
			v1alpha1.HostConditionReasonNetworkProvisioningDisabled,
			"Provider networking disabled; skipped NetworkAttachment create",
		)
		return ctrl.Result{}, nil
	}

	if controllerutil.AddFinalizer(bareMetalInstance, BareMetalInstanceNetworkingFinalizer) {
		if err := r.Update(ctx, bareMetalInstance); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	apiID := bareMetalInstance.Labels[BareMetalInstanceUUIDLabel]
	if apiID == "" {
		return ctrl.Result{}, fmt.Errorf(
			"baremetalinstance %s/%s has no %s label",
			bareMetalInstance.Namespace, bareMetalInstance.Name, BareMetalInstanceUUIDLabel,
		)
	}

	if err := r.ensureNetworkAttachmentStatusMAC(ctx, bareMetalInstance); err != nil {
		bareMetalInstance.SetStatusCondition(
			v1alpha1.HostConditionNetworkAttachmentsReady,
			metav1.ConditionFalse,
			v1alpha1.HostConditionReasonProgressing,
			err.Error(),
		)
		return ctrl.Result{}, err
	}

	name := opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, apiID)
	na := &opv1alpha1.NetworkAttachment{}
	err := r.apiReaderOrClient().Get(ctx, client.ObjectKey{Namespace: bareMetalInstance.Namespace, Name: name}, na)
	if apierrors.IsNotFound(err) {
		na, err = r.buildBareMetalNetworkAttachment(bareMetalInstance, apiID, name)
		if err != nil {
			r.setNetworkCondition(
				bareMetalInstance,
				v1alpha1.HostConditionNetworkAttachmentsReady,
				metav1.ConditionFalse,
				v1alpha1.HostConditionReasonNetworkAttachmentFailed,
				err.Error(),
				corev1.EventTypeWarning,
				v1alpha1.HostConditionReasonNetworkAttachmentFailed,
			)
			bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
			return ctrl.Result{}, nil
		}
		if err := r.Create(ctx, na); err != nil && !apierrors.IsAlreadyExists(err) {
			return ctrl.Result{}, fmt.Errorf("create NetworkAttachment %s: %w", name, err)
		}
		bareMetalInstance.SetStatusCondition(
			v1alpha1.HostConditionNetworkAttachmentsReady,
			metav1.ConditionFalse,
			v1alpha1.HostConditionReasonProgressing,
			"waiting for the network attachment",
		)
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("get NetworkAttachment %s: %w", name, err)
	}
	if err := validateBareMetalNetworkAttachment(bareMetalInstance, na, apiID); err != nil {
		r.setNetworkCondition(
			bareMetalInstance,
			v1alpha1.HostConditionNetworkAttachmentsReady,
			metav1.ConditionFalse,
			v1alpha1.HostConditionReasonNetworkAttachmentFailed,
			err.Error(),
			corev1.EventTypeWarning,
			v1alpha1.HostConditionReasonNetworkAttachmentFailed,
		)
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		return ctrl.Result{}, nil
	}

	return r.mirrorNetworkAttachmentReady(bareMetalInstance, na), nil
}

func validateBareMetalNetworkAttachment(
	bmi *v1alpha1.BareMetalInstance,
	na *opv1alpha1.NetworkAttachment,
	apiID string,
) error {
	if na.Spec.Target.Kind != opv1alpha1.NetworkAttachmentTargetBareMetalInstance || na.Spec.Target.ID != apiID {
		return fmt.Errorf("NetworkAttachment %s has a different target", na.Name)
	}
	if na.Annotations[BareMetalInstanceTenantAnnotation] != bmi.Annotations[BareMetalInstanceTenantAnnotation] {
		return fmt.Errorf("NetworkAttachment %s has a different tenant", na.Name)
	}
	if len(bmi.Spec.NetworkAttachments) != 1 {
		return fmt.Errorf("BareMetalInstance must have exactly one network attachment")
	}
	attachment := bmi.Spec.NetworkAttachments[0]
	if na.Spec.Subnet != attachment.SubnetRef || na.Spec.Interface.Name != attachment.Interface {
		return fmt.Errorf("NetworkAttachment %s does not match the BareMetalInstance network attachment", na.Name)
	}
	if !slices.Equal(na.Spec.SecurityGroups, attachment.SecurityGroupRefs) {
		return fmt.Errorf("NetworkAttachment %s has different security groups", na.Name)
	}
	if len(bmi.Status.NetworkAttachmentStatuses) > 0 && bmi.Status.NetworkAttachmentStatuses[0].MACAddress != "" &&
		na.Spec.Interface.MACAddress != bmi.Status.NetworkAttachmentStatuses[0].MACAddress {
		return fmt.Errorf("NetworkAttachment %s has a different interface MAC", na.Name)
	}
	return nil
}

func (r *BareMetalInstanceReconciler) ensureNetworkAttachmentStatusMAC(
	ctx context.Context,
	bmi *v1alpha1.BareMetalInstance,
) error {
	r.initNetworkAttachmentStatuses(bmi)
	attachment := bmi.Spec.NetworkAttachments[0]
	status := &bmi.Status.NetworkAttachmentStatuses[0]
	if status.MACAddress != "" {
		return nil
	}
	if attachment.Interface == "" {
		return fmt.Errorf("network attachment has no interface name")
	}
	if bmi.Spec.ExternalHostID == "" {
		return fmt.Errorf("baremetalinstance has no externalHostID for MAC lookup")
	}
	if bmi.Spec.ExternalHostName == "" {
		return fmt.Errorf("baremetalinstance has no externalHostName for the networking manager")
	}
	if r.InventoryClient == nil {
		return fmt.Errorf("inventory client not configured for MAC lookup")
	}
	macs, err := r.InventoryClient.GetHostLogicalPortMACs(ctx, bmi.Spec.ExternalHostID)
	if err != nil {
		return fmt.Errorf("lookup logical port MACs: %w", err)
	}
	mac, ok := macs[attachment.Interface]
	if !ok || mac == "" {
		return fmt.Errorf("no MAC recorded for interface %q on host %s", attachment.Interface, bmi.Spec.ExternalHostID)
	}
	mac, err = normalizeInventoryMACAddress(mac)
	if err != nil {
		return fmt.Errorf("invalid MAC for interface %q on host %s: %w", attachment.Interface, bmi.Spec.ExternalHostID, err)
	}
	status.MACAddress = mac
	return nil
}

func normalizeInventoryMACAddress(value string) (string, error) {
	mac, err := net.ParseMAC(value)
	if err != nil || len(mac) != 6 {
		return "", fmt.Errorf("invalid MAC address")
	}
	return mac.String(), nil
}

func (r *BareMetalInstanceReconciler) buildBareMetalNetworkAttachment(
	bmi *v1alpha1.BareMetalInstance,
	apiID, name string,
) (*opv1alpha1.NetworkAttachment, error) {
	attachment := bmi.Spec.NetworkAttachments[0]
	if len(attachment.SecurityGroupRefs) == 0 {
		return nil, fmt.Errorf("network attachment requires at least one security group")
	}
	if len(bmi.Status.NetworkAttachmentStatuses) == 0 || bmi.Status.NetworkAttachmentStatuses[0].MACAddress == "" {
		return nil, fmt.Errorf("network attachment MAC is not recorded")
	}
	mac := bmi.Status.NetworkAttachmentStatuses[0].MACAddress
	if attachment.Interface == "" {
		return nil, fmt.Errorf("network attachment has no interface name")
	}

	tenant := bmi.Annotations[BareMetalInstanceTenantAnnotation]
	if tenant == "" {
		return nil, fmt.Errorf("BareMetalInstance %s missing required %s annotation",
			bmi.Name, BareMetalInstanceTenantAnnotation)
	}

	return &opv1alpha1.NetworkAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  bmi.Namespace,
			Finalizers: []string{BareMetalInstanceNetworkingFinalizer},
			Annotations: map[string]string{
				BareMetalInstanceTenantAnnotation: tenant,
			},
		},
		Spec: opv1alpha1.NetworkAttachmentSpec{
			Target: opv1alpha1.NetworkAttachmentTarget{
				Kind: opv1alpha1.NetworkAttachmentTargetBareMetalInstance,
				ID:   apiID,
			},
			Subnet: attachment.SubnetRef,
			Interface: opv1alpha1.NetworkAttachmentInterface{
				Type:       opv1alpha1.NetworkInterfacePhysical,
				Name:       attachment.Interface,
				MACAddress: mac,
			},
			SecurityGroups: attachment.SecurityGroupRefs,
		},
	}, nil
}

func (r *BareMetalInstanceReconciler) mirrorNetworkAttachmentReady(
	bmi *v1alpha1.BareMetalInstance,
	na *opv1alpha1.NetworkAttachment,
) ctrl.Result {
	if !na.DeletionTimestamp.IsZero() {
		bmi.SetStatusCondition(
			v1alpha1.HostConditionNetworkAttachmentsReady,
			metav1.ConditionFalse,
			v1alpha1.HostConditionReasonNetworkDetaching,
			networkAttachmentConditionMessage(na, "NetworkAttachment is deleting"),
		)
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}
	}

	ready := meta.FindStatusCondition(na.Status.Conditions, opv1alpha1.NetworkConditionReady)
	if ready != nil && ready.Status == metav1.ConditionFalse && ready.Reason == opv1alpha1.ReasonFailed {
		message := ready.Message
		if message == "" {
			message = "NetworkAttachment apply failed"
		}
		r.setNetworkCondition(
			bmi,
			v1alpha1.HostConditionNetworkAttachmentsReady,
			metav1.ConditionFalse,
			v1alpha1.HostConditionReasonNetworkAttachmentFailed,
			message,
			corev1.EventTypeWarning,
			v1alpha1.HostConditionReasonNetworkAttachmentFailed,
		)
		bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		return ctrl.Result{}
	}
	if ready != nil && ready.Status == metav1.ConditionTrue {
		message := ready.Message
		if message == "" {
			message = "NetworkAttachment is Ready"
		}
		r.setNetworkCondition(
			bmi,
			v1alpha1.HostConditionNetworkAttachmentsReady,
			metav1.ConditionTrue,
			"Succeeded",
			message,
			corev1.EventTypeNormal,
			"NetworkAttached",
		)
		return ctrl.Result{}
	}

	bmi.SetStatusCondition(
		v1alpha1.HostConditionNetworkAttachmentsReady,
		metav1.ConditionFalse,
		v1alpha1.HostConditionReasonProgressing,
		networkAttachmentConditionMessage(na, "waiting for the network attachment"),
	)
	return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}
}

func networkAttachmentConditionMessage(na *opv1alpha1.NetworkAttachment, fallback string) string {
	if ready := meta.FindStatusCondition(na.Status.Conditions, opv1alpha1.NetworkConditionReady); ready != nil && ready.Message != "" {
		return ready.Message
	}
	if msg := latestNetworkAttachmentJobMessage(na); msg != "" {
		return msg
	}
	return fallback
}

func latestNetworkAttachmentJobMessage(na *opv1alpha1.NetworkAttachment) string {
	prefer := []string{networkAttachmentApplyTask, networkAttachmentDeleteTask}
	for _, task := range prefer {
		if job := latestNetworkAttachmentJob(na, task); job != nil && job.Message != "" {
			return job.Message
		}
	}
	for i := len(na.Status.Jobs) - 1; i >= 0; i-- {
		if na.Status.Jobs[i].Message != "" {
			return na.Status.Jobs[i].Message
		}
	}
	return ""
}

func latestNetworkAttachmentJob(
	na *opv1alpha1.NetworkAttachment,
	task string,
) *opv1alpha1.NetworkJobStatus {
	for i := len(na.Status.Jobs) - 1; i >= 0; i-- {
		if na.Status.Jobs[i].Task == task {
			return &na.Status.Jobs[i]
		}
	}
	return nil
}

func (r *BareMetalInstanceReconciler) reconcileNetworkingDeletion(
	ctx context.Context,
	bareMetalInstance *v1alpha1.BareMetalInstance,
) (ctrl.Result, bool, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(bareMetalInstance, BareMetalInstanceNetworkingFinalizer) {
		return ctrl.Result{}, true, nil
	}

	if !r.providerNetworkingEnabled() || len(bareMetalInstance.Spec.NetworkAttachments) == 0 {
		controllerutil.RemoveFinalizer(bareMetalInstance, BareMetalInstanceNetworkingFinalizer)
		if err := r.Update(ctx, bareMetalInstance); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{}, true, nil
	}

	apiID := bareMetalInstance.Labels[BareMetalInstanceUUIDLabel]
	if apiID == "" {
		return ctrl.Result{}, false, fmt.Errorf(
			"BareMetalInstance %s is missing required %s label; retaining networking finalizer until attachment cleanup is verified",
			bareMetalInstance.Name, BareMetalInstanceUUIDLabel,
		)
	}

	na, gone, err := r.deleteBareMetalNetworkAttachment(ctx, bareMetalInstance.Namespace, apiID)
	if err != nil {
		return ctrl.Result{}, false, err
	}
	if !gone {
		bareMetalInstance.SetStatusCondition(
			v1alpha1.HostConditionNetworkAttachmentsReady,
			metav1.ConditionFalse,
			v1alpha1.HostConditionReasonNetworkDetaching,
			networkAttachmentDeleteMessage(na),
		)
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, false, nil
	}

	controllerutil.RemoveFinalizer(bareMetalInstance, BareMetalInstanceNetworkingFinalizer)
	if err := r.Update(ctx, bareMetalInstance); err != nil {
		return ctrl.Result{}, false, err
	}
	log.Info("NetworkAttachment deleted; networking finalizer removed")
	return ctrl.Result{}, true, nil
}

// deleteBareMetalNetworkAttachment deletes the hub NetworkAttachment and removes
// the BMF finalizer from it once only that finalizer remains. It reads the API
// server, not the cache. It returns the current
// NetworkAttachment while deletion is pending, or gone=true when it is absent.
func (r *BareMetalInstanceReconciler) deleteBareMetalNetworkAttachment(
	ctx context.Context,
	namespace string,
	apiID string,
) (*opv1alpha1.NetworkAttachment, bool, error) {
	key := client.ObjectKey{
		Namespace: namespace,
		Name:      opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, apiID),
	}
	na := &opv1alpha1.NetworkAttachment{}
	if err := r.apiReaderOrClient().Get(ctx, key, na); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, true, nil
		}
		return nil, false, err
	}
	if na.DeletionTimestamp.IsZero() {
		if err := r.Delete(ctx, na); err != nil && !apierrors.IsNotFound(err) {
			return nil, false, fmt.Errorf("delete NetworkAttachment %s: %w", key.Name, err)
		}
		return na, false, nil
	}
	if len(na.Finalizers) == 1 && na.Finalizers[0] == BareMetalInstanceNetworkingFinalizer {
		base := na.DeepCopy()
		controllerutil.RemoveFinalizer(na, BareMetalInstanceNetworkingFinalizer)
		if err := r.Patch(ctx, na, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil && !apierrors.IsNotFound(err) {
			return nil, false, err
		}
	}
	return na, false, nil
}

func networkAttachmentDeleteMessage(na *opv1alpha1.NetworkAttachment) string {
	if na != nil {
		if job := latestNetworkAttachmentJob(na, networkAttachmentDeleteTask); job != nil && job.Message != "" {
			return job.Message
		}
	}
	return "waiting for NetworkAttachment deletion"
}
