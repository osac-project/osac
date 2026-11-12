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
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/management"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/networkattachment"
)

const (
	// bareMetalInstanceIDLabel carries the API ID of a BareMetalInstance.
	bareMetalInstanceIDLabel = "osac.openshift.io/baremetalinstance-uuid"

	// tenantAnnotation carries the tenant API ID of a BareMetalInstance and of
	// its NetworkAttachment.
	tenantAnnotation = "osac.openshift.io/tenant"
)

// errSelectedPortMACUnavailable marks a selected port whose MAC address the
// inventory does not have, or has in an invalid form. Retrying cannot fix it.
var errSelectedPortMACUnavailable = errors.New("selected port MAC address unavailable")

// primaryNetworkAttachment returns the BareMetalInstance's primary network
// attachment. A single attachment is the primary one even when primary is
// omitted, as the API defines. It returns nil when there is none.
func primaryNetworkAttachment(bmi *v1alpha1.BareMetalInstance) *v1alpha1.BareMetalNetworkAttachment {
	attachments := bmi.Spec.NetworkAttachments
	for i := range attachments {
		if attachments[i].Primary {
			return &attachments[i]
		}
	}
	if len(attachments) == 1 {
		return &attachments[0]
	}
	return nil
}

// networkAttachmentKey returns the key of the NetworkAttachment of a
// BareMetalInstance's attachment: baremetalinstance-<BMI API ID>-<interface>,
// next to the BareMetalInstance. It returns false when the BareMetalInstance has
// no API ID label, so it cannot have a NetworkAttachment.
func networkAttachmentKey(
	bmi *v1alpha1.BareMetalInstance, attachment *v1alpha1.BareMetalNetworkAttachment,
) (client.ObjectKey, bool) {
	id := bmi.Labels[bareMetalInstanceIDLabel]
	if id == "" {
		return client.ObjectKey{}, false
	}
	return client.ObjectKey{
		Namespace: bmi.Namespace,
		Name: opv1alpha1.NetworkAttachmentName(
			opv1alpha1.NetworkAttachmentTargetBareMetalInstance, id, attachment.Interface),
	}, true
}

// legacyNetworkAttachmentKey returns the BMI-only key used before interface
// names were included in NetworkAttachment names.
func legacyNetworkAttachmentKey(
	bmi *v1alpha1.BareMetalInstance,
) (client.ObjectKey, bool) {
	id := bmi.Labels[bareMetalInstanceIDLabel]
	if id == "" {
		return client.ObjectKey{}, false
	}
	return client.ObjectKey{
		Namespace: bmi.Namespace,
		Name:      opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, id, ""),
	}, true
}

// getNetworkAttachment reads a NetworkAttachment directly from the API server.
// It returns nil when the NetworkAttachment does not exist.
func (r *BareMetalInstanceReconciler) getNetworkAttachment(
	ctx context.Context, key client.ObjectKey,
) (*opv1alpha1.NetworkAttachment, error) {
	na := &opv1alpha1.NetworkAttachment{}
	if err := r.apiReaderOrClient().Get(ctx, key, na); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get NetworkAttachment %s: %w", key.Name, err)
	}
	return na, nil
}

// normalizeMAC returns a MAC address in lowercase, colon-separated form. It
// accepts ':' or '-' separators in any case.
func normalizeMAC(mac string) (string, error) {
	hw, err := net.ParseMAC(strings.TrimSpace(mac))
	if err != nil || len(hw) != 6 {
		return "", fmt.Errorf("invalid MAC address %q", mac)
	}
	return hw.String(), nil
}

// selectedPortMAC returns the inventory MAC address of a host interface. An
// error wrapping errSelectedPortMACUnavailable is final; any other error is an
// inventory backend failure and is worth a retry.
func (r *BareMetalInstanceReconciler) selectedPortMAC(
	ctx context.Context, bmi *v1alpha1.BareMetalInstance, iface string,
) (string, error) {
	if r.InventoryClient == nil {
		return "", fmt.Errorf("%w: inventory client not configured", errSelectedPortMACUnavailable)
	}
	macs, err := r.InventoryClient.GetHostLogicalPortMACs(ctx, bmi.Spec.ExternalHostID)
	if err != nil {
		return "", fmt.Errorf("get the logical port MAC addresses of host %s: %w", bmi.Spec.ExternalHostID, err)
	}
	mac := macs[iface]
	if mac == "" {
		return "", fmt.Errorf("%w: the inventory has no MAC address for interface %q", errSelectedPortMACUnavailable, iface)
	}
	normalized, err := normalizeMAC(mac)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errSelectedPortMACUnavailable, err)
	}
	return normalized, nil
}

func setNetworkAttachmentsReady(bmi *v1alpha1.BareMetalInstance, status metav1.ConditionStatus, reason, message string) {
	bmi.SetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady, status, reason, message)
}

func markNetworkingProvisioningDisabled(bmi *v1alpha1.BareMetalInstance) {
	message := "Provider networking provisioning is disabled"
	setNetworkAttachmentsReady(bmi, metav1.ConditionTrue,
		v1alpha1.HostConditionReasonProvisioningDisabled, message)
	bmi.SetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete, metav1.ConditionTrue,
		v1alpha1.HostConditionReasonProvisioningDisabled, message)
	setIPDiscoveryComplete(bmi, metav1.ConditionTrue,
		v1alpha1.HostConditionReasonProvisioningDisabled, message)
}

// reconcileNetworking creates the BareMetalInstance's NetworkAttachment and
// mirrors its readiness. The osac-operator networking controller runs the
// Fabric Manager task that moves the host's port to the tenant network.
func (r *BareMetalInstanceReconciler) reconcileNetworking(
	ctx context.Context,
	bareMetalInstance *v1alpha1.BareMetalInstance,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !r.NetworkingEnabled {
		log.Info("Networking provisioning disabled, skipping network attachment provisioning")
		markNetworkingProvisioningDisabled(bareMetalInstance)
		return ctrl.Result{}, nil
	}

	if len(bareMetalInstance.Spec.NetworkAttachments) == 0 {
		setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionTrue, "Skipped", "No network attachments configured")
		return ctrl.Result{}, nil
	}
	deleting, err := r.bareMetalInstanceDeletionStarted(ctx, bareMetalInstance)
	if err != nil {
		return ctrl.Result{}, err
	}
	if deleting {
		// Keep any create/apply milestone intact for handleDeletion. Mark a known
		// pre-create state so deletion can safely release the host.
		if !networkAttachmentMayHaveBeenApplied(bareMetalInstance) &&
			!networkAttachmentCreateMayBeInFlight(bareMetalInstance) {
			setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionFalse,
				v1alpha1.HostConditionReasonNetworkAttachmentNotCreated,
				"NetworkAttachment creation skipped because deletion has started")
		}
		return ctrl.Result{RequeueAfter: NetworkOffboardPollInterval}, nil
	}

	if controllerutil.AddFinalizer(bareMetalInstance, BareMetalInstanceNetworkingFinalizer) {
		if err := r.Update(ctx, bareMetalInstance); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	attachment := primaryNetworkAttachment(bareMetalInstance)
	if attachment == nil {
		setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonTemplateFailed,
			"the BareMetalInstance has no primary network attachment")
		return ctrl.Result{}, nil
	}
	// The interface name is part of the NetworkAttachment name.
	if errs := validation.IsDNS1123Label(attachment.Interface); len(errs) > 0 {
		setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonTemplateFailed,
			fmt.Sprintf("interface %q is not a valid DNS label: %s", attachment.Interface, strings.Join(errs, "; ")))
		return ctrl.Result{}, nil
	}
	key, ok := networkAttachmentKey(bareMetalInstance, attachment)
	if !ok {
		setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonTemplateFailed,
			fmt.Sprintf("the BareMetalInstance has no %s label", bareMetalInstanceIDLabel))
		return ctrl.Result{}, nil
	}

	r.initNetworkAttachmentStatuses(bareMetalInstance)
	na, resolvedKey, err := r.getExistingNetworkAttachment(ctx, bareMetalInstance, attachment)
	if err != nil {
		return ctrl.Result{}, err
	}

	if na == nil {
		return r.createNetworkAttachment(ctx, bareMetalInstance, attachment, key)
	}

	// Existing BMI-only attachments are reused through upgrades. Mirror their
	// recorded port identity into status without changing the NetworkAttachment.
	if resolvedKey != key {
		key = resolvedKey
	}
	if status := networkAttachmentStatusFor(bareMetalInstance, attachment.SubnetRef); status != nil && status.MACAddress == "" {
		mac, err := normalizeMAC(na.Spec.Interface.MACAddress)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("existing NetworkAttachment %s has invalid interface MAC: %w", key.Name, err)
		}
		status.MACAddress = mac
	}

	ready := meta.FindStatusCondition(na.Status.Conditions, opv1alpha1.NetworkConditionReady)
	switch {
	case ready != nil && ready.Status == metav1.ConditionTrue:
		wasReady := bareMetalInstance.IsStatusConditionTrue(v1alpha1.HostConditionNetworkAttachmentsReady)
		setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionTrue, "Succeeded", "Network attachment is ready")
		if !wasReady {
			r.recordBMIEvent(bareMetalInstance, corev1.EventTypeNormal, "NetworkAttached", "Network attachment is ready")
		}
		return ctrl.Result{}, nil
	case ready != nil && ready.Reason == opv1alpha1.ReasonFailed:
		previous := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
		alreadyReported := previous != nil && previous.Reason == v1alpha1.HostConditionReasonNetworkAttachmentFailed
		setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonNetworkAttachmentFailed, ready.Message)
		if !alreadyReported {
			r.recordBMIEvent(bareMetalInstance, corev1.EventTypeWarning,
				v1alpha1.HostConditionReasonNetworkAttachmentFailed, ready.Message)
		}
		return ctrl.Result{}, nil
	default:
		message := "Waiting for the network attachment"
		if ready != nil && ready.Message != "" {
			message = ready.Message
		}
		setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionFalse,
			v1alpha1.HostConditionReasonProgressing, message)
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
	}
}

func (r *BareMetalInstanceReconciler) createNetworkAttachment(
	ctx context.Context,
	bmi *v1alpha1.BareMetalInstance,
	attachment *v1alpha1.BareMetalNetworkAttachment,
	key client.ObjectKey,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	tenant := bmi.Annotations[tenantAnnotation]
	if tenant == "" {
		setNetworkAttachmentsReady(bmi, metav1.ConditionFalse, v1alpha1.HostConditionReasonTemplateFailed,
			fmt.Sprintf("the BareMetalInstance has no %s annotation", tenantAnnotation))
		return ctrl.Result{}, nil
	}
	status := networkAttachmentStatusFor(bmi, attachment.SubnetRef)
	if status == nil {
		return ctrl.Result{}, fmt.Errorf("network attachment status for subnet %s was not initialized", attachment.SubnetRef)
	}
	if status.MACAddress == "" {
		mac, err := r.selectedPortMAC(ctx, bmi, attachment.Interface)
		if errors.Is(err, errSelectedPortMACUnavailable) {
			return r.failSelectedPortMAC(ctx, bmi, err.Error(), false)
		}
		if err != nil {
			return ctrl.Result{}, err
		}
		status.MACAddress = mac
		setNetworkAttachmentsReady(bmi, metav1.ConditionFalse, v1alpha1.HostConditionReasonNetworkAttachmentNotCreated,
			"Recording the selected interface MAC before creating the network attachment")
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
	}

	creation := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
	createIntentAlreadyPersisted := creation != nil &&
		creation.Reason == v1alpha1.HostConditionReasonNetworkAttachmentCreating
	attachmentMayHaveBeenApplied := networkAttachmentMayHaveBeenApplied(bmi)
	if !createIntentAlreadyPersisted {
		setNetworkAttachmentsReady(bmi, metav1.ConditionFalse,
			v1alpha1.HostConditionReasonNetworkAttachmentCreating,
			"Persisting the NetworkAttachment create intent")
		if bmi.ResourceVersion != "" {
			if err := r.Status().Update(ctx, bmi); err != nil {
				return ctrl.Result{}, fmt.Errorf("persist NetworkAttachment create intent: %w", err)
			}
		}
	}

	na := &opv1alpha1.NetworkAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        key.Name,
			Namespace:   key.Namespace,
			Finalizers:  []string{BareMetalInstanceNetworkingFinalizer},
			Annotations: map[string]string{tenantAnnotation: tenant},
		},
		Spec: opv1alpha1.NetworkAttachmentSpec{
			Target: opv1alpha1.NetworkAttachmentTarget{
				Kind: opv1alpha1.NetworkAttachmentTargetBareMetalInstance,
				ID:   bmi.Labels[bareMetalInstanceIDLabel],
			},
			Subnet: attachment.SubnetRef,
			Interface: opv1alpha1.NetworkAttachmentInterface{
				Type:       opv1alpha1.NetworkInterfacePhysical,
				Name:       attachment.Interface,
				MACAddress: status.MACAddress,
			},
			SecurityGroups: attachment.SecurityGroupRefs,
		},
	}
	if err := r.Create(ctx, na); err != nil && !apierrors.IsAlreadyExists(err) {
		if !createIntentAlreadyPersisted && networkAttachmentCreateWasRejected(err) {
			if attachmentMayHaveBeenApplied {
				setNetworkAttachmentsReady(bmi, metav1.ConditionFalse,
					v1alpha1.HostConditionReasonNetworkAttachmentMissing,
					"NetworkAttachment is missing after it may have been applied; retaining host ownership until cleanup is confirmed")
			} else {
				setNetworkAttachmentsReady(bmi, metav1.ConditionFalse,
					v1alpha1.HostConditionReasonNetworkAttachmentNotCreated,
					fmt.Sprintf("NetworkAttachment creation was rejected: %v", err))
			}
		}
		return ctrl.Result{}, fmt.Errorf("create NetworkAttachment %s: %w", key.Name, err)
	}
	log.Info("Ensured NetworkAttachment exists", "networkAttachment", key.Name)
	setNetworkAttachmentsReady(bmi, metav1.ConditionFalse, v1alpha1.HostConditionReasonProgressing,
		"Waiting for the network attachment")
	return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
}

func networkAttachmentCreateWasRejected(err error) bool {
	return apierrors.IsBadRequest(err) ||
		apierrors.IsForbidden(err) ||
		apierrors.IsInvalid(err) ||
		apierrors.IsMethodNotSupported(err) ||
		apierrors.IsNotFound(err) ||
		apierrors.IsUnauthorized(err)
}

func (r *BareMetalInstanceReconciler) getExistingNetworkAttachment(
	ctx context.Context,
	bmi *v1alpha1.BareMetalInstance,
	attachment *v1alpha1.BareMetalNetworkAttachment,
) (*opv1alpha1.NetworkAttachment, client.ObjectKey, error) {
	suffixedKey, ok := networkAttachmentKey(bmi, attachment)
	if !ok {
		return nil, client.ObjectKey{}, fmt.Errorf("the BareMetalInstance has no %s label", bareMetalInstanceIDLabel)
	}
	na, err := r.getNetworkAttachment(ctx, suffixedKey)
	if err != nil || na != nil {
		return na, suffixedKey, err
	}
	legacyKey, ok := legacyNetworkAttachmentKey(bmi)
	if !ok || legacyKey == suffixedKey {
		return nil, suffixedKey, nil
	}
	na, err = r.getNetworkAttachment(ctx, legacyKey)
	if err != nil {
		return nil, legacyKey, err
	}
	if na != nil {
		return na, legacyKey, nil
	}
	return nil, suffixedKey, nil
}

// reconcileNetworkingDeletion deletes the BareMetalInstance's NetworkAttachment
// and waits until it is gone. The networking controller moves the host's port
// back to the provisioning network before it removes its own finalizer; BMF
// then removes its finalizer from the NetworkAttachment and from the
// BareMetalInstance. It returns true once network cleanup is complete.
func (r *BareMetalInstanceReconciler) reconcileNetworkingDeletion(
	ctx context.Context,
	bareMetalInstance *v1alpha1.BareMetalInstance,
) (ctrl.Result, bool, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(bareMetalInstance, BareMetalInstanceNetworkingFinalizer) {
		return ctrl.Result{}, true, nil
	}

	if r.NetworkingEnabled {
		keys, err := r.existingNetworkAttachmentKeys(ctx, bareMetalInstance)
		if err != nil {
			return ctrl.Result{}, false, err
		}
		creating := networkAttachmentCreateMayBeInFlight(bareMetalInstance)
		if len(keys) == 0 && creating {
			setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionFalse,
				v1alpha1.HostConditionReasonNetworkAttachmentCreating,
				"Waiting for an uncertain NetworkAttachment create request to resolve")
			return ctrl.Result{RequeueAfter: NetworkOffboardPollInterval}, false, nil
		}
		if len(keys) == 0 && networkAttachmentMayHaveBeenApplied(bareMetalInstance) &&
			!networkAttachmentCleanupStarted(bareMetalInstance) {
			setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionFalse,
				v1alpha1.HostConditionReasonNetworkAttachmentMissing,
				"NetworkAttachment is missing after it may have been applied; retaining host ownership until cleanup is confirmed")
			return ctrl.Result{RequeueAfter: NetworkOffboardPollInterval}, false, nil
		}
		if len(keys) > 0 && !networkAttachmentCleanupStarted(bareMetalInstance) {
			setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionFalse,
				v1alpha1.HostConditionReasonNetworkDetaching,
				"Persisting NetworkAttachment cleanup intent before releasing host ownership")
			if err := r.Status().Update(ctx, bareMetalInstance); err != nil {
				return ctrl.Result{}, false, fmt.Errorf("persist NetworkAttachment cleanup intent: %w", err)
			}
			return ctrl.Result{RequeueAfter: NetworkOffboardPollInterval}, false, nil
		}
		allGone := true
		for _, key := range keys {
			log.Info("Reconciling NetworkAttachment deletion", "networkAttachment", key.Name)
			gone, err := networkattachment.DeleteOwned(ctx, r.Client, r.apiReaderOrClient(), key,
				&opv1alpha1.NetworkAttachment{}, BareMetalInstanceNetworkingFinalizer)
			if err != nil {
				return ctrl.Result{}, false, err
			}
			allGone = allGone && gone
		}
		if !allGone {
			return ctrl.Result{RequeueAfter: NetworkOffboardPollInterval}, false, nil
		}
	}

	return r.removeNetworkingFinalizer(ctx, bareMetalInstance)
}

func networkAttachmentMayHaveBeenApplied(bmi *v1alpha1.BareMetalInstance) bool {
	attachmentsReady := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
	if attachmentsReady != nil {
		if attachmentsReady.Status == metav1.ConditionTrue &&
			attachmentsReady.Reason != v1alpha1.HostConditionReasonProvisioningDisabled &&
			attachmentsReady.Reason != "Skipped" {
			return true
		}
		switch attachmentsReady.Reason {
		case v1alpha1.HostConditionReasonProvisioningDisabled,
			"Skipped",
			v1alpha1.HostConditionReasonTemplateFailed,
			v1alpha1.HostConditionReasonSelectedPortMACUnavailable,
			v1alpha1.HostConditionReasonNetworkAttachmentNotCreated,
			// Keep pre-create reasons from older BMF versions safe during upgrades.
			"NetworkAttachmentRecordingMAC",
			"NetworkAttachmentCreateRejected",
			"NetworkAttachmentCreationSkipped",
			v1alpha1.HostConditionReasonNetworkAttachmentCreating:
			// These reasons identify a known pre-create state or an uncertain
			// create request. The latter is handled separately by the deletion
			// path, which waits for the request to resolve.
		default:
			// Older Progressing conditions are ambiguous: they may have been
			// written before or after the create request. Fail closed.
			return true
		}
	}
	handoff := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete)
	if handoff != nil && handoff.Status == metav1.ConditionTrue &&
		handoff.Reason != v1alpha1.HostConditionReasonProvisioningDisabled {
		return true
	}
	return networkAttachmentCleanupStarted(bmi) ||
		bmi.IsStatusConditionTrue(v1alpha1.HostConditionNetworkOffboardComplete)
}

func networkAttachmentCleanupStarted(bmi *v1alpha1.BareMetalInstance) bool {
	condition := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
	return condition != nil && (condition.Reason == v1alpha1.HostConditionReasonNetworkDetaching ||
		condition.Reason == "NetworkAttachmentCleanupStarted")
}

func networkAttachmentCreateMayBeInFlight(bmi *v1alpha1.BareMetalInstance) bool {
	condition := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
	return condition != nil && condition.Reason == v1alpha1.HostConditionReasonNetworkAttachmentCreating
}

func (r *BareMetalInstanceReconciler) existingNetworkAttachmentKeys(
	ctx context.Context, bmi *v1alpha1.BareMetalInstance,
) ([]client.ObjectKey, error) {
	attachment := primaryNetworkAttachment(bmi)
	if attachment == nil {
		return nil, nil
	}
	suffixedKey, ok := networkAttachmentKey(bmi, attachment)
	if !ok {
		return nil, nil
	}
	keys := []client.ObjectKey{suffixedKey}
	if legacyKey, ok := legacyNetworkAttachmentKey(bmi); ok && legacyKey != suffixedKey {
		keys = append(keys, legacyKey)
	}
	existing := make([]client.ObjectKey, 0, len(keys))
	for _, key := range keys {
		na, err := r.getNetworkAttachment(ctx, key)
		if err != nil {
			return nil, err
		}
		if na != nil {
			existing = append(existing, key)
		}
	}
	return existing, nil
}

// removeNetworkingFinalizer completes network cleanup of a BareMetalInstance.
func (r *BareMetalInstanceReconciler) removeNetworkingFinalizer(
	ctx context.Context, bareMetalInstance *v1alpha1.BareMetalInstance,
) (ctrl.Result, bool, error) {
	controllerutil.RemoveFinalizer(bareMetalInstance, BareMetalInstanceNetworkingFinalizer)
	if err := r.Update(ctx, bareMetalInstance); err != nil {
		return ctrl.Result{}, false, err
	}
	logf.FromContext(ctx).Info("Network attachment cleanup completed")
	return ctrl.Result{}, true, nil
}

// networkingRequested reports whether this instance has tenant networking enabled and configured.
func (r *BareMetalInstanceReconciler) networkingRequested(bmi *v1alpha1.BareMetalInstance) bool {
	return r.NetworkingEnabled && len(bmi.Spec.NetworkAttachments) > 0
}

// failSelectedPortMAC records the selected-interface failure and releases the
// host only when provisioning has not started, preserving inventory-lock ownership.
func (r *BareMetalInstanceReconciler) failSelectedPortMAC(
	ctx context.Context, bmi *v1alpha1.BareMetalInstance, diagnostic string, inventoryLocked bool,
) (ctrl.Result, error) {
	bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
	bmi.SetStatusCondition(v1alpha1.HostConditionAllocated, metav1.ConditionFalse,
		v1alpha1.HostConditionReasonSelectedPortMACUnavailable, diagnostic)
	r.recordBMIEvent(bmi, corev1.EventTypeWarning,
		v1alpha1.HostConditionReasonSelectedPortMACUnavailable, diagnostic)
	if inventoryLocked {
		return r.releaseHostWithoutSelectedPortMACLocked(ctx, bmi, diagnostic)
	}
	return r.releaseHostWithoutSelectedPortMAC(ctx, bmi, diagnostic)
}

// releaseHostWithoutSelectedPortMAC returns an assigned host to inventory after
// the selected physical port cannot be used for tenant networking, provided
// provisioning has not started.
func (r *BareMetalInstanceReconciler) releaseHostWithoutSelectedPortMAC(
	ctx context.Context, bmi *v1alpha1.BareMetalInstance, diagnostic string,
) (ctrl.Result, error) {
	hostID := bmi.Spec.ExternalHostID
	if hostID == "" {
		bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		bmi.SetStatusCondition(v1alpha1.HostConditionAllocated, metav1.ConditionFalse,
			v1alpha1.HostConditionReasonSelectedPortMACUnavailable, diagnostic)
		return ctrl.Result{}, nil
	}
	if !inventory.TryLock(hostID) {
		return ctrl.Result{RequeueAfter: r.TryLockFailPollIntervalDuration}, nil
	}
	defer inventory.Unlock(hostID)
	return r.releaseHostWithoutSelectedPortMACLocked(ctx, bmi, diagnostic)
}

// releaseHostWithoutSelectedPortMACLocked releases the host when the caller
// already holds its inventory lock.
func (r *BareMetalInstanceReconciler) releaseHostWithoutSelectedPortMACLocked(
	ctx context.Context, bmi *v1alpha1.BareMetalInstance, diagnostic string,
) (ctrl.Result, error) {
	hostID := bmi.Spec.ExternalHostID
	if hostID == "" {
		bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		bmi.SetStatusCondition(v1alpha1.HostConditionAllocated, metav1.ConditionFalse,
			v1alpha1.HostConditionReasonSelectedPortMACUnavailable, diagnostic)
		return ctrl.Result{}, nil
	}
	if hasProvisioningActivity(bmi) {
		// Keep ownership of an imaged or potentially-imaged host. The normal
		// deletion path deprovisions it before releasing inventory ownership.
		return ctrl.Result{}, nil
	}

	if r.InventoryClient == nil {
		return ctrl.Result{}, errors.New("inventory client not configured while releasing host")
	}
	if err := r.InventoryClient.UnassignHost(ctx, hostID, string(bmi.UID), inventoryLabelsToRemove(bmi)); err != nil {
		return ctrl.Result{}, fmt.Errorf("release host %s after selected-port MAC failure: %w", hostID, err)
	}

	updated := bmi.DeepCopy()
	updated.Spec.ExternalHostID = ""
	updated.Spec.ExternalHostName = ""
	updated.Spec.HostClass = ""
	if err := r.Update(ctx, updated); err != nil {
		return ctrl.Result{}, fmt.Errorf("clear host assignment after selected-port MAC failure: %w", err)
	}
	bmi.Spec = updated.Spec
	bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
	bmi.SetStatusCondition(v1alpha1.HostConditionAllocated, metav1.ConditionFalse,
		v1alpha1.HostConditionReasonSelectedPortMACUnavailable, diagnostic)
	return ctrl.Result{}, nil
}

func networkAttachmentStatusFor(
	bmi *v1alpha1.BareMetalInstance, subnetRef string,
) *v1alpha1.BareMetalNetworkAttachmentStatus {
	for i := range bmi.Status.NetworkAttachmentStatuses {
		if bmi.Status.NetworkAttachmentStatuses[i].SubnetRef == subnetRef {
			return &bmi.Status.NetworkAttachmentStatuses[i]
		}
	}
	return nil
}

func (r *BareMetalInstanceReconciler) reconcileNetworkProvisionAndDiscovery(ctx context.Context, bareMetalInstance *v1alpha1.BareMetalInstance) (ctrl.Result, error) {
	result, deleting, err := r.requeueIfBareMetalInstanceDeleting(ctx, bareMetalInstance)
	if err != nil || deleting {
		return result, err
	}

	// 1) Provisioning runs FIRST, while the server is still on the provisioning
	//    network segment (the initial provisioning-network attach done at bootstrap). Networking must
	//    NOT run before provisioning — the tenant must not reach a half-built host.
	//
	//    NetworkHandoffComplete is not reset after Ready, so an in-place
	//    re-provision would run over the tenant network. In-place re-provisioning
	//    is unsupported; recreate the BMI to change its OS.
	if bareMetalInstance.Spec.TemplateID != "" && bareMetalInstance.Spec.TemplateID != shared.OsacNoopTemplate {
		result, provErr := r.reconcileProvisioning(ctx, bareMetalInstance)
		if provErr != nil {
			bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
			return result, provErr
		}
		if !result.IsZero() {
			bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
			return result, nil
		}
		provisionCond := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionProvisionTemplateComplete)
		if provisionCond != nil && provisionCond.Status != metav1.ConditionTrue {
			if v1alpha1.IsProvisionRetrying(provisionCond) {
				bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
				return ctrl.Result{}, nil
			}
			bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
			return ctrl.Result{}, nil
		}
	}
	result, deleting, err = r.requeueIfBareMetalInstanceDeleting(ctx, bareMetalInstance)
	if err != nil || deleting {
		return result, err
	}

	if !r.NetworkingEnabled {
		markNetworkingProvisioningDisabled(bareMetalInstance)
		return ctrl.Result{}, nil
	}

	if len(bareMetalInstance.Spec.NetworkAttachments) == 0 {
		return ctrl.Result{}, nil
	}

	// 2) Move the fabric port provisioning network -> tenant network (post-provision).
	result, netErr := r.reconcileNetworking(ctx, bareMetalInstance)
	if netErr != nil {
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		return result, netErr
	}
	if !result.IsZero() {
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
		return result, nil
	}
	netCond := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
	if bareMetalInstance.Status.Phase == v1alpha1.BareMetalInstancePhaseFailed &&
		isSelectedPortMACFailure(bareMetalInstance) {
		return ctrl.Result{}, nil
	}
	if bareMetalInstance.Status.Phase == v1alpha1.BareMetalInstancePhaseFailed &&
		netCond != nil && netCond.Status == metav1.ConditionTrue {
		// A prior network retry may have failed while the provisioning and
		// handoff conditions remain recoverable. Let the terminal condition
		// checks below decide whether to stay failed or continue the lifecycle.
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
	}
	if netCond == nil || netCond.Status != metav1.ConditionTrue {
		if netCond != nil && isFinalNetworkAttachmentFailure(netCond.Reason) {
			bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
			return ctrl.Result{}, nil
		}
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
	}
	return r.reconcileNetworkHandoffAndIPDiscovery(ctx, bareMetalInstance)
}

func (r *BareMetalInstanceReconciler) reconcileNetworkHandoffAndIPDiscovery(
	ctx context.Context, bareMetalInstance *v1alpha1.BareMetalInstance,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	// 3) Power-cycle so the OS re-DHCPs on the tenant network (single default route).
	result, rbErr := r.reconcileNetworkHandoffPowerCycle(ctx, bareMetalInstance)
	if rbErr != nil {
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		return result, rbErr
	}
	if !result.IsZero() {
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
		return result, nil
	}
	handoffCond := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete)
	if handoffCond != nil && handoffCond.Reason == v1alpha1.HostConditionReasonNetworkHandoffFailed {
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		return ctrl.Result{}, nil
	}

	// 4) Discover the tenant-network DHCP lease (only after handoff reboot).
	result, ipErr := r.reconcileIPDiscovery(ctx, bareMetalInstance)
	if ipErr != nil {
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		return result, ipErr
	}
	if !result.IsZero() {
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
		return result, nil
	}
	ipCond := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
	if ipCond == nil || ipCond.Status != metav1.ConditionTrue {
		if ipCond != nil && (ipCond.Reason == v1alpha1.HostConditionReasonTemplateFailed ||
			ipCond.Reason == v1alpha1.HostConditionReasonIPDiscoveryFailed) {
			bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
			return ctrl.Result{}, nil
		}
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
	}
	log.Info("Network handoff and IP discovery complete", "bmi", bareMetalInstance.Name)
	return ctrl.Result{}, nil
}

func isFinalNetworkAttachmentFailure(reason string) bool {
	return reason == v1alpha1.HostConditionReasonTemplateFailed ||
		reason == v1alpha1.HostConditionReasonNetworkAttachmentFailed
}

func isSelectedPortMACFailure(bmi *v1alpha1.BareMetalInstance) bool {
	allocated := bmi.GetStatusCondition(v1alpha1.HostConditionAllocated)
	return allocated != nil && allocated.Status == metav1.ConditionFalse &&
		allocated.Reason == v1alpha1.HostConditionReasonSelectedPortMACUnavailable
}

func (r *BareMetalInstanceReconciler) recordBMIEvent(
	bmi *v1alpha1.BareMetalInstance, eventType, reason, message string,
) {
	if r.EventRecorder != nil {
		r.EventRecorder.Eventf(bmi, nil, eventType, reason, "Reconcile", "%s", message)
	}
}

const networkHandoffTimeout = 15 * time.Minute

// reconcileNetworkHandoffPowerCycle powers the host off and back on after its
// port moves to the tenant network. Both steps are persisted in the handoff
// condition and complete only after a matching, non-transitioning observation.
func (r *BareMetalInstanceReconciler) reconcileNetworkHandoffPowerCycle(
	ctx context.Context, bmi *v1alpha1.BareMetalInstance,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !r.NetworkingEnabled {
		log.Info("Networking provisioning disabled, skipping network-handoff power cycle")
		markNetworkingProvisioningDisabled(bmi)
		return ctrl.Result{}, nil
	}
	deleting, err := r.bareMetalInstanceDeletionStarted(ctx, bmi)
	if err != nil {
		return ctrl.Result{}, err
	}
	if deleting {
		return ctrl.Result{RequeueAfter: NetworkOffboardPollInterval}, nil
	}

	handoffCond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete)
	if handoffCond != nil && handoffCond.Status == metav1.ConditionTrue &&
		handoffCond.Reason != v1alpha1.HostConditionReasonProvisioningDisabled {
		return ctrl.Result{}, nil
	}
	if handoffCond != nil && handoffCond.Reason == v1alpha1.HostConditionReasonNetworkHandoffFailed {
		return ctrl.Result{}, nil
	}

	if handoffCond == nil || handoffCond.Status != metav1.ConditionFalse ||
		(handoffCond.Reason != v1alpha1.HostConditionReasonPoweringOff &&
			handoffCond.Reason != v1alpha1.HostConditionReasonPoweringOn) {
		bmi.SetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete,
			metav1.ConditionFalse, v1alpha1.HostConditionReasonPoweringOff,
			"Starting the network handoff power cycle")
		return ctrl.Result{RequeueAfter: r.ManagementRecheckIntervalDuration}, nil
	}

	target := management.PowerOff
	stepReason := v1alpha1.HostConditionReasonPoweringOff
	if handoffCond.Reason == v1alpha1.HostConditionReasonPoweringOn {
		target = management.PowerOn
		stepReason = v1alpha1.HostConditionReasonPoweringOn
	}

	if r.ManagementClient == nil {
		r.failNetworkHandoff(bmi, "management client is not configured")
		return ctrl.Result{}, nil
	}

	powerStatus, getErr := r.ManagementClient.GetPowerState(ctx, bmi.Spec.ExternalHostID)
	if getErr == nil && powerStatus != nil && !powerStatus.IsTransitioning && powerStatus.State == target {
		if target == management.PowerOff {
			bmi.SetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete,
				metav1.ConditionFalse, v1alpha1.HostConditionReasonPoweringOn,
				"Host is off; waiting to confirm power on")
			return ctrl.Result{RequeueAfter: r.ManagementRecheckIntervalDuration}, nil
		}
		bmi.SetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete,
			metav1.ConditionTrue, "Succeeded", "Host power cycle onto the tenant network completed")
		return ctrl.Result{}, nil
	}

	deadline := networkHandoffDeadline(bmi, handoffCond)
	if !deadline.IsZero() && !time.Now().Before(deadline) {
		diagnostic := fmt.Sprintf("handoff did not observe %s within 15 minutes", target)
		if getErr != nil {
			diagnostic += fmt.Sprintf(": GetPowerState failed: %v", getErr)
		} else if powerStatus == nil {
			diagnostic += ": management backend returned no power status"
		} else {
			diagnostic += fmt.Sprintf(": observed %s transitioning=%t", powerStatus.State, powerStatus.IsTransitioning)
		}
		r.failNetworkHandoff(bmi, diagnostic)
		return ctrl.Result{}, nil
	}

	var message string
	switch {
	case getErr != nil:
		message = fmt.Sprintf("GetPowerState failed while waiting for %s: %v", target, getErr)
	case powerStatus == nil:
		message = "Management backend returned no power status"
	case powerStatus.IsTransitioning:
		message = fmt.Sprintf("Host is transitioning; waiting for %s", target)
	default:
		setErr := r.ManagementClient.SetPowerState(ctx, bmi.Spec.ExternalHostID, target)
		if setErr != nil && !errors.Is(setErr, management.ErrTransitioning) {
			r.failNetworkHandoff(bmi, fmt.Sprintf("SetPowerState(%s) failed: %v", target, setErr))
			return ctrl.Result{}, nil
		}
		if errors.Is(setErr, management.ErrTransitioning) {
			message = "Management backend reports a power transition in progress"
		} else {
			message = fmt.Sprintf("Requested power state %s; waiting for confirmation", target)
		}
	}
	bmi.SetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete, metav1.ConditionFalse, stepReason, message)
	log.V(1).Info("Network handoff power step is pending", "host", bmi.Spec.ExternalHostID, "target", target)
	return ctrl.Result{RequeueAfter: r.ManagementRecheckIntervalDuration}, nil
}

func networkHandoffDeadline(bmi *v1alpha1.BareMetalInstance, handoffCond *metav1.Condition) time.Time {
	readyCond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
	if readyCond != nil && !readyCond.LastTransitionTime.IsZero() {
		return readyCond.LastTransitionTime.Add(networkHandoffTimeout)
	}
	if handoffCond != nil && !handoffCond.LastTransitionTime.IsZero() {
		return handoffCond.LastTransitionTime.Add(networkHandoffTimeout)
	}
	return time.Time{}
}

func (r *BareMetalInstanceReconciler) failNetworkHandoff(bmi *v1alpha1.BareMetalInstance, diagnostic string) {
	bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
	bmi.SetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete, metav1.ConditionFalse,
		v1alpha1.HostConditionReasonNetworkHandoffFailed, diagnostic)
	r.recordBMIEvent(bmi, corev1.EventTypeWarning, v1alpha1.HostConditionReasonNetworkHandoffFailed, diagnostic)
}

// reconcileNetworkOffboardShutdown powers off the host during deletion BEFORE
// the fabric port moves back to the provisioning network. This ensures tenant
// workloads never run on the provisioning network — the workload stops while
// the port is still on the tenant network, and by the time anything boots on
// provisioning network it is the bare-metal management backend's cleaning image, not the tenant OS.
func (r *BareMetalInstanceReconciler) reconcileNetworkOffboardShutdown(
	ctx context.Context, bmi *v1alpha1.BareMetalInstance,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	offboardCond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkOffboardComplete)
	if offboardCond != nil && offboardCond.Status == metav1.ConditionTrue {
		return ctrl.Result{}, nil
	}
	if r.ManagementClient == nil {
		return r.pendingNetworkOffboarding(bmi, "management client is not configured")
	}

	powerStatus, err := r.ManagementClient.GetPowerState(ctx, bmi.Spec.ExternalHostID)
	if err != nil {
		return r.pendingNetworkOffboarding(bmi, fmt.Sprintf("GetPowerState failed: %v", err))
	}
	if powerStatus == nil {
		return r.pendingNetworkOffboarding(bmi, "management backend returned no power status")
	}

	if powerStatus.State == management.PowerOff && !powerStatus.IsTransitioning {
		bmi.SetStatusCondition(v1alpha1.HostConditionNetworkOffboardComplete,
			metav1.ConditionTrue, "Succeeded",
			"Host powered off; safe to move port to provisioning network")
		log.Info("Offboard shutdown complete", "host", bmi.Spec.ExternalHostID)
		return ctrl.Result{}, nil
	}

	if powerStatus.IsTransitioning {
		log.Info("Host transitioning, waiting for power off", "host", bmi.Spec.ExternalHostID)
		return r.pendingNetworkOffboarding(bmi,
			"Waiting for the host power transition to finish before network cleanup")
	}

	// Host is powered on — power it off
	if err := r.ManagementClient.SetPowerState(ctx, bmi.Spec.ExternalHostID, management.PowerOff); err != nil {
		if errors.Is(err, management.ErrTransitioning) {
			return r.pendingNetworkOffboarding(bmi,
				"Management backend reports a transition; waiting to retry power off")
		}
		return r.retryNetworkOffboarding(bmi, fmt.Sprintf("SetPowerState(OFF) failed: %v", err))
	}
	bmi.SetStatusCondition(v1alpha1.HostConditionNetworkOffboardComplete,
		metav1.ConditionFalse, v1alpha1.HostConditionReasonOffboarding,
		"Powering off host before moving port to provisioning network")
	log.Info("Triggered offboard shutdown", "host", bmi.Spec.ExternalHostID)
	return ctrl.Result{RequeueAfter: NetworkOffboardPollInterval}, nil
}

func (r *BareMetalInstanceReconciler) pendingNetworkOffboarding(
	bmi *v1alpha1.BareMetalInstance, diagnostic string,
) (ctrl.Result, error) {
	bmi.SetStatusCondition(v1alpha1.HostConditionNetworkOffboardComplete,
		metav1.ConditionFalse, v1alpha1.HostConditionReasonOffboarding, diagnostic)
	return ctrl.Result{RequeueAfter: NetworkOffboardPollInterval}, nil
}

func (r *BareMetalInstanceReconciler) retryNetworkOffboarding(
	bmi *v1alpha1.BareMetalInstance, diagnostic string,
) (ctrl.Result, error) {
	r.recordNetworkOffboardingFailure(bmi, diagnostic)
	return ctrl.Result{RequeueAfter: NetworkOffboardPollInterval}, nil
}

func (r *BareMetalInstanceReconciler) recordNetworkOffboardingFailure(
	bmi *v1alpha1.BareMetalInstance, diagnostic string,
) {
	previous := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkOffboardComplete)
	if previous == nil || previous.Reason != v1alpha1.HostConditionReasonPowerOffFailed {
		r.recordBMIEvent(bmi, corev1.EventTypeWarning,
			v1alpha1.HostConditionReasonPowerOffFailed, diagnostic)
	}
	bmi.SetStatusCondition(v1alpha1.HostConditionNetworkOffboardComplete,
		metav1.ConditionFalse, v1alpha1.HostConditionReasonPowerOffFailed, diagnostic)
}
