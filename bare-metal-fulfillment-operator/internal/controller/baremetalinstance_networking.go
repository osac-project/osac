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
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const (
	bareMetalInstanceTenantAnnotation    = "osac.openshift.io/tenant"
	bareMetalInstanceOwnerAnnotation     = "osac.openshift.io/owner-reference"
	operatorNetworkAttachmentFinalizer   = "osac.openshift.io/networkattachment"
	bareMetalQueryDHCPLeaseTask          = "query_dhcp_lease"
	bareMetalApplyNetworkAttachmentTask  = "apply_network_attachment"
	bareMetalDeleteNetworkAttachmentTask = "delete_network_attachment"
)

func (r *BareMetalInstanceReconciler) providerNetworkingEnabled() bool {
	return r.NetworkingEnabled && r.NetworkingNamespace != ""
}

func (r *BareMetalInstanceReconciler) reconcileNetworking(
	ctx context.Context,
	bmi *v1alpha1.BareMetalInstance,
) (ctrl.Result, error) {
	if len(bmi.Spec.NetworkAttachments) == 0 {
		bmi.SetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady,
			metav1.ConditionTrue, "Skipped", "No network attachments configured")
		return ctrl.Result{}, nil
	}
	if !r.providerNetworkingEnabled() {
		bmi.SetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady,
			metav1.ConditionTrue, opv1alpha1.ReasonProvisioningDisabled,
			"Provider networking disabled; skipped NetworkAttachment create")
		return ctrl.Result{}, nil
	}
	if len(bmi.Spec.NetworkAttachments) != 1 {
		return r.failNetworkAttachment(bmi, "a BareMetalInstance supports exactly one network attachment")
	}
	if bmi.Spec.ExternalHostName == "" {
		return r.failNetworkAttachment(bmi, "assigned inventory host has no externalHostName")
	}
	apiID := bmi.Labels[BareMetalInstanceUUIDLabel]
	if apiID == "" {
		return r.failNetworkAttachment(bmi, fmt.Sprintf("BareMetalInstance has no %s label", BareMetalInstanceUUIDLabel))
	}
	if bmi.Annotations[bareMetalInstanceTenantAnnotation] == "" {
		return r.failNetworkAttachment(bmi, fmt.Sprintf("BareMetalInstance has no %s annotation", bareMetalInstanceTenantAnnotation))
	}

	if !controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer) {
		controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
		if err := r.Update(ctx, bmi); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
	}

	key := bareMetalNetworkAttachmentKey(r.NetworkingNamespace, apiID)
	na := &opv1alpha1.NetworkAttachment{}
	err := r.apiReaderOrClient().Get(ctx, key, na)
	if err == nil {
		if err := verifyBareMetalNetworkAttachment(bmi, na, true); err != nil {
			return r.failNetworkAttachment(bmi, err.Error())
		}
		return r.projectNetworkAttachmentReady(bmi, na), nil
	}
	if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("get NetworkAttachment %s: %w", key, err)
	}

	attachment := bmi.Spec.NetworkAttachments[0]
	if attachment.Interface == "" {
		return r.failNetworkAttachment(bmi, "network attachment has no selected interface")
	}
	if len(attachment.SecurityGroupRefs) == 0 {
		return r.failNetworkAttachment(bmi, "network attachment has no security groups")
	}
	if bmi.Spec.ExternalHostID == "" || r.InventoryClient == nil {
		return r.failNetworkAttachment(bmi, "inventory host is not available for interface MAC lookup")
	}

	macs, err := r.InventoryClient.GetHostLogicalPortMACs(ctx, bmi.Spec.ExternalHostID)
	if err != nil {
		message := fmt.Sprintf("read interface MACs for host %s: %v", bmi.Spec.ExternalHostID, err)
		logf.FromContext(ctx).Error(err, "Failed to read interface MACs from inventory",
			"inventoryHostID", bmi.Spec.ExternalHostID, "interface", attachment.Interface)
		if isRetryableInventoryError(err) {
			bmi.SetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady,
				metav1.ConditionFalse, v1alpha1.HostConditionReasonProgressing, message)
			return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
		}
		return r.failNetworkAttachment(bmi, message)
	}
	mac, ok := macs[attachment.Interface]
	if !ok || strings.TrimSpace(mac) == "" {
		return r.failNetworkAttachment(bmi,
			fmt.Sprintf("inventory host %s has no MAC recorded for interface %q", bmi.Spec.ExternalHostID, attachment.Interface))
	}
	mac, err = normalizeBareMetalMAC(mac)
	if err != nil {
		return r.failNetworkAttachment(bmi,
			fmt.Sprintf("inventory host %s has an invalid MAC for interface %q: %v", bmi.Spec.ExternalHostID, attachment.Interface, err))
	}

	r.initNetworkAttachmentStatuses(bmi)
	na = buildBareMetalNetworkAttachment(bmi, r.NetworkingNamespace, apiID, key.Name, mac)
	if err := r.Create(ctx, na); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return ctrl.Result{}, fmt.Errorf("create NetworkAttachment %s: %w", key, err)
		}
		// An API timeout can race another reconcile or hide a successful create.
		// Read the immutable object from the API server and adopt it only if every
		// identity and requested-spec field matches this BMI.
		existing := &opv1alpha1.NetworkAttachment{}
		if getErr := r.apiReaderOrClient().Get(ctx, key, existing); getErr != nil {
			if apierrors.IsNotFound(getErr) {
				return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
			}
			return ctrl.Result{}, fmt.Errorf("read NetworkAttachment %s after AlreadyExists: %w", key, getErr)
		}
		if verifyErr := verifyBareMetalNetworkAttachment(bmi, existing, true); verifyErr != nil ||
			existing.Spec.Interface.MACAddress != mac {
			if verifyErr == nil {
				verifyErr = fmt.Errorf("NetworkAttachment %s has a different interface MAC", key)
			}
			return r.failNetworkAttachment(bmi, verifyErr.Error())
		}
	}

	bmi.SetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady,
		metav1.ConditionFalse, v1alpha1.HostConditionReasonProgressing,
		"waiting for the network attachment")
	return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
}

func (r *BareMetalInstanceReconciler) failNetworkAttachment(
	bmi *v1alpha1.BareMetalInstance,
	message string,
) (ctrl.Result, error) {
	bmi.SetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady,
		metav1.ConditionFalse, v1alpha1.HostConditionReasonTemplateFailed, message)
	bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
	return ctrl.Result{}, nil
}

// isRetryableInventoryError identifies backend transport and API timeout errors.
// Mapping validation errors remain terminal so bad inventory data does not poll forever.
func isRetryableInventoryError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) ||
		apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) ||
		apierrors.IsTooManyRequests(err) || apierrors.IsServiceUnavailable(err) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}

func buildBareMetalNetworkAttachment(
	bmi *v1alpha1.BareMetalInstance,
	namespace string,
	apiID string,
	name string,
	mac string,
) *opv1alpha1.NetworkAttachment {
	attachment := bmi.Spec.NetworkAttachments[0]
	annotations := map[string]string{
		bareMetalInstanceTenantAnnotation: bmi.Annotations[bareMetalInstanceTenantAnnotation],
	}
	if ownerReference := bmi.Annotations[bareMetalInstanceOwnerAnnotation]; ownerReference != "" {
		annotations[bareMetalInstanceOwnerAnnotation] = ownerReference
	}
	return &opv1alpha1.NetworkAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Finalizers:  []string{BareMetalInstanceNetworkingFinalizer},
			Annotations: annotations,
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
			SecurityGroups: slices.Clone(attachment.SecurityGroupRefs),
		},
	}
}

func normalizeBareMetalMAC(value string) (string, error) {
	parsed, err := net.ParseMAC(strings.TrimSpace(value))
	if err != nil || len(parsed) != 6 {
		return "", fmt.Errorf("expected a six-byte MAC address")
	}
	return strings.ToLower(parsed.String()), nil
}

func verifyBareMetalNetworkAttachment(
	bmi *v1alpha1.BareMetalInstance,
	na *opv1alpha1.NetworkAttachment,
	requireBMFfinalizer bool,
) error {
	if len(bmi.Spec.NetworkAttachments) != 1 {
		return fmt.Errorf("BareMetalInstance %s has an unsupported number of network attachments", bmi.Name)
	}
	apiID := bmi.Labels[BareMetalInstanceUUIDLabel]
	attachment := bmi.Spec.NetworkAttachments[0]
	if na.Spec.Target.Kind != opv1alpha1.NetworkAttachmentTargetBareMetalInstance || na.Spec.Target.ID != apiID {
		return fmt.Errorf("NetworkAttachment %s targets a different BareMetalInstance", na.Name)
	}
	if na.Annotations[bareMetalInstanceTenantAnnotation] != bmi.Annotations[bareMetalInstanceTenantAnnotation] ||
		na.Annotations[bareMetalInstanceOwnerAnnotation] != bmi.Annotations[bareMetalInstanceOwnerAnnotation] {
		return fmt.Errorf("NetworkAttachment %s has conflicting tenant or owner metadata", na.Name)
	}
	if na.Spec.Subnet != attachment.SubnetRef || na.Spec.Interface.Type != opv1alpha1.NetworkInterfacePhysical ||
		na.Spec.Interface.Name != attachment.Interface || !sameStringSet(na.Spec.SecurityGroups, attachment.SecurityGroupRefs) {
		return fmt.Errorf("NetworkAttachment %s has a conflicting immutable spec", na.Name)
	}
	if _, err := normalizeBareMetalMAC(na.Spec.Interface.MACAddress); err != nil {
		return fmt.Errorf("NetworkAttachment %s has an invalid physical interface MAC", na.Name)
	}
	if requireBMFfinalizer && !controllerutil.ContainsFinalizer(na, BareMetalInstanceNetworkingFinalizer) {
		return fmt.Errorf("NetworkAttachment %s is missing the BMF networking finalizer", na.Name)
	}
	return nil
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	l := slices.Clone(left)
	r := slices.Clone(right)
	sort.Strings(l)
	sort.Strings(r)
	return slices.Equal(l, r)
}

func bareMetalNetworkAttachmentKey(namespace, apiID string) client.ObjectKey {
	return client.ObjectKey{
		Namespace: namespace,
		Name:      opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, apiID),
	}
}

func (r *BareMetalInstanceReconciler) projectNetworkAttachmentReady(
	bmi *v1alpha1.BareMetalInstance,
	na *opv1alpha1.NetworkAttachment,
) ctrl.Result {
	if !na.DeletionTimestamp.IsZero() {
		bmi.SetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady,
			metav1.ConditionFalse, v1alpha1.HostConditionReasonDetaching,
			latestBareMetalNetworkMessage(na, bareMetalDeleteNetworkAttachmentTask,
				"NetworkAttachment is deleting"))
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}
	}
	ready := meta.FindStatusCondition(na.Status.Conditions, opv1alpha1.NetworkConditionReady)
	if ready != nil && ready.Status == metav1.ConditionTrue {
		bmi.SetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady,
			metav1.ConditionTrue, "Succeeded", ready.Message)
		return ctrl.Result{}
	}
	if ready != nil && ready.Status == metav1.ConditionFalse && ready.Reason == opv1alpha1.ReasonFailed {
		returnSetNetworkAttachmentFailure(bmi, ready.Message)
		return ctrl.Result{}
	}
	if job := latestBareMetalNetworkJob(na, bareMetalApplyNetworkAttachmentTask); job != nil &&
		job.State == opv1alpha1.NetworkJobStateFailed {
		returnSetNetworkAttachmentFailure(bmi, job.Message)
		return ctrl.Result{}
	}
	bmi.SetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady,
		metav1.ConditionFalse, v1alpha1.HostConditionReasonProgressing,
		latestBareMetalNetworkMessage(na, bareMetalApplyNetworkAttachmentTask, "waiting for the network attachment"))
	return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}
}

func returnSetNetworkAttachmentFailure(bmi *v1alpha1.BareMetalInstance, message string) {
	if message == "" {
		message = "NetworkAttachment apply failed"
	}
	bmi.SetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady,
		metav1.ConditionFalse, v1alpha1.HostConditionReasonTemplateFailed, message)
	bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
}

func latestBareMetalNetworkJob(na *opv1alpha1.NetworkAttachment, task string) *opv1alpha1.NetworkJobStatus {
	for i := len(na.Status.Jobs) - 1; i >= 0; i-- {
		if na.Status.Jobs[i].Task == task && na.Status.Jobs[i].Manager == opv1alpha1.NetworkJobManagerFabric {
			return &na.Status.Jobs[i]
		}
	}
	return nil
}

func latestBareMetalNetworkMessage(na *opv1alpha1.NetworkAttachment, task, fallback string) string {
	if job := latestBareMetalNetworkJob(na, task); job != nil {
		if job.Message != "" {
			if job.JobID != "" {
				return fmt.Sprintf("%s (AAP job ID: %s)", job.Message, job.JobID)
			}
			return job.Message
		}
	}
	if ready := meta.FindStatusCondition(na.Status.Conditions, opv1alpha1.NetworkConditionReady); ready != nil && ready.Message != "" {
		return ready.Message
	}
	return fallback
}

func (r *BareMetalInstanceReconciler) networkAttachmentForDeletion(
	ctx context.Context,
	bmi *v1alpha1.BareMetalInstance,
) (*opv1alpha1.NetworkAttachment, error) {
	if !r.providerNetworkingEnabled() || len(bmi.Spec.NetworkAttachments) == 0 {
		return nil, nil
	}
	apiID := bmi.Labels[BareMetalInstanceUUIDLabel]
	if apiID == "" {
		if !controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer) {
			// The finalizer is added before any NetworkAttachment create, so none exists.
			return nil, nil
		}
		return nil, fmt.Errorf("BareMetalInstance %s has no %s label; cannot safely locate its NetworkAttachment",
			bmi.Name, BareMetalInstanceUUIDLabel)
	}
	key := bareMetalNetworkAttachmentKey(r.NetworkingNamespace, apiID)
	na := &opv1alpha1.NetworkAttachment{}
	if err := r.apiReaderOrClient().Get(ctx, key, na); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get NetworkAttachment %s before deletion: %w", key, err)
	}
	if err := verifyBareMetalNetworkAttachment(bmi, na,
		na.DeletionTimestamp.IsZero() || controllerutil.ContainsFinalizer(na, BareMetalInstanceNetworkingFinalizer)); err != nil {
		return nil, err
	}
	return na, nil
}

func (r *BareMetalInstanceReconciler) reconcileNetworkingDeletion(
	ctx context.Context,
	bmi *v1alpha1.BareMetalInstance,
) (ctrl.Result, bool, error) {
	if !r.providerNetworkingEnabled() || len(bmi.Spec.NetworkAttachments) == 0 {
		return r.removeBMIBareMetalNetworkingFinalizer(ctx, bmi)
	}

	na, err := r.networkAttachmentForDeletion(ctx, bmi)
	if err != nil {
		return ctrl.Result{}, false, err
	}
	if na == nil {
		return r.removeBMIBareMetalNetworkingFinalizer(ctx, bmi)
	}
	key := client.ObjectKeyFromObject(na)
	if na.DeletionTimestamp.IsZero() {
		if err := r.Delete(ctx, na); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, false, fmt.Errorf("delete NetworkAttachment %s: %w", key, err)
		}
		bmi.SetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady,
			metav1.ConditionFalse, v1alpha1.HostConditionReasonDetaching, "waiting for NetworkAttachment cleanup")
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, false, nil
	}
	if controllerutil.ContainsFinalizer(na, operatorNetworkAttachmentFinalizer) {
		message := latestBareMetalNetworkMessage(na, bareMetalDeleteNetworkAttachmentTask,
			"waiting for the networking controller to remove its finalizer")
		bmi.SetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady,
			metav1.ConditionFalse, v1alpha1.HostConditionReasonDetaching, message)
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, false, nil
	}
	if controllerutil.ContainsFinalizer(na, BareMetalInstanceNetworkingFinalizer) {
		base := na.DeepCopy()
		controllerutil.RemoveFinalizer(na, BareMetalInstanceNetworkingFinalizer)
		if err := r.Patch(ctx, na, client.MergeFrom(base)); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, false, fmt.Errorf("remove BMF finalizer from NetworkAttachment %s: %w", key, err)
		}
	}
	return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, false, nil
}

func (r *BareMetalInstanceReconciler) removeBMIBareMetalNetworkingFinalizer(
	ctx context.Context,
	bmi *v1alpha1.BareMetalInstance,
) (ctrl.Result, bool, error) {
	if controllerutil.RemoveFinalizer(bmi, BareMetalInstanceNetworkingFinalizer) {
		if err := r.Update(ctx, bmi); err != nil {
			return ctrl.Result{}, false, err
		}
	}
	return ctrl.Result{}, true, nil
}

func (r *BareMetalInstanceReconciler) needsNetworkOffboard(
	ctx context.Context,
	bmi *v1alpha1.BareMetalInstance,
) (bool, error) {
	na, err := r.networkAttachmentForDeletion(ctx, bmi)
	return na != nil, err
}
