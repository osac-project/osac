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
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const (
	// bareMetalInstanceIDLabel carries the API ID of a BareMetalInstance.
	bareMetalInstanceIDLabel = "osac.openshift.io/baremetalinstance-uuid"

	// tenantAnnotation carries the tenant API ID of a BareMetalInstance and of
	// its NetworkAttachment.
	tenantAnnotation = "osac.openshift.io/tenant"

	networkAttachmentCreateRejectedMessage = "Network attachment create was rejected"
	networkAttachmentCleanupMessage        = "Waiting for network attachment cleanup"
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

func (r *BareMetalInstanceReconciler) networkingRequested(bmi *v1alpha1.BareMetalInstance) bool {
	return len(bmi.Spec.NetworkAttachments) > 0
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

// networkAttachmentKeys returns the current interface key and legacy BMI-only key.
func networkAttachmentKeys(bmi *v1alpha1.BareMetalInstance) []client.ObjectKey {
	attachment := primaryNetworkAttachment(bmi)
	if attachment == nil {
		return nil
	}
	current, ok := networkAttachmentKey(bmi, attachment)
	if !ok {
		return nil
	}
	keys := []client.ObjectKey{current}
	legacy := client.ObjectKey{
		Namespace: current.Namespace,
		Name: opv1alpha1.NetworkAttachmentName(
			opv1alpha1.NetworkAttachmentTargetBareMetalInstance, bmi.Labels[bareMetalInstanceIDLabel], ""),
	}
	if legacy != current {
		keys = append(keys, legacy)
	}
	return keys
}

// getNetworkAttachmentForDeletion reads the attachment once per deletion pass.
// It skips the read when BMF has no network finalizer, networking is disabled,
// or no attachment is requested.
func (r *BareMetalInstanceReconciler) getNetworkAttachmentForDeletion(
	ctx context.Context, bmi *v1alpha1.BareMetalInstance,
) (*opv1alpha1.NetworkAttachment, error) {
	if !controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer) ||
		!r.NetworkingEnabled || !r.networkingRequested(bmi) {
		return nil, nil
	}
	for _, key := range networkAttachmentKeys(bmi) {
		na, err := r.getNetworkAttachment(ctx, key)
		if err != nil || na != nil {
			return na, err
		}
	}
	return nil, nil
}

// networkAttachmentMayExist reports whether deletion must wait for an attachment.
func networkAttachmentMayExist(bmi *v1alpha1.BareMetalInstance) bool {
	ready := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
	if ready != nil {
		if ready.Status == metav1.ConditionTrue && ready.Reason == "Succeeded" {
			return true
		}
		if ready.Reason == v1alpha1.HostConditionReasonProgressing ||
			networkAttachmentCreateMayHaveSucceeded(ready) {
			return true
		}
		return false
	}
	handoff := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete)
	ipDiscovery := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
	return (handoff != nil && handoff.Status == metav1.ConditionTrue && handoff.Reason == "Succeeded") ||
		(ipDiscovery != nil && ipDiscovery.Status == metav1.ConditionTrue && ipDiscovery.Reason == "Succeeded")
}

func networkAttachmentCreateMayHaveSucceeded(ready *metav1.Condition) bool {
	// An unknown result may still commit after a retry was rejected.
	return ready != nil && ready.Status == metav1.ConditionUnknown &&
		(ready.Reason == v1alpha1.HostConditionReasonProgressing ||
			ready.Reason == v1alpha1.HostConditionReasonNetworkAttachmentFailed)
}

func setNetworkingProvisioningDisabled(bmi *v1alpha1.BareMetalInstance) {
	bmi.Status.NetworkAttachmentStatuses = nil
	setNetworkAttachmentsReady(bmi, metav1.ConditionTrue, v1alpha1.HostConditionReasonProvisioningDisabled,
		"Network attachment provisioning is disabled; no attachment was created")
	bmi.SetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete, metav1.ConditionTrue,
		v1alpha1.HostConditionReasonProvisioningDisabled,
		"Network handoff is disabled; no host power cycle was requested")
	bmi.SetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete, metav1.ConditionTrue,
		v1alpha1.HostConditionReasonProvisioningDisabled,
		"IP lease discovery is disabled; no lease was queried")
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
		setNetworkingProvisioningDisabled(bareMetalInstance)
		return ctrl.Result{}, nil
	}

	if !r.networkingRequested(bareMetalInstance) {
		setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionTrue, "Skipped", "No network attachments configured")
		return ctrl.Result{}, nil
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
	na, err := r.getNetworkAttachment(ctx, key)
	if err != nil {
		return ctrl.Result{}, err
	}

	if na == nil {
		return r.reconcileNetworkAttachmentCreation(ctx, bareMetalInstance, attachment, key)
	}

	// Mirror the selected port identity from the existing NetworkAttachment into
	// status without changing the NetworkAttachment.
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
		setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonProgressing, message)
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
	}
}

func (r *BareMetalInstanceReconciler) reconcileNetworkAttachmentCreation(
	ctx context.Context,
	bmi *v1alpha1.BareMetalInstance,
	attachment *v1alpha1.BareMetalNetworkAttachment,
	key client.ObjectKey,
) (ctrl.Result, error) {
	tenant := bmi.Annotations[tenantAnnotation]
	if tenant == "" {
		if networkAttachmentMayExist(bmi) {
			// Preserve unresolved create state so deletion keeps host ownership.
			return ctrl.Result{RequeueAfter: networkRetryInterval}, nil
		}
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
			return r.failSelectedPortMAC(bmi, err.Error())
		}
		if err != nil {
			return ctrl.Result{}, err
		}
		status.MACAddress = mac
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
	}
	ready := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
	if ready != nil && ready.Reason == v1alpha1.HostConditionReasonNetworkAttachmentFailed &&
		ready.ObservedGeneration == bmi.Generation {
		return ctrl.Result{}, nil
	}
	createMayHaveSucceeded := networkAttachmentCreateMayHaveSucceeded(ready)
	if ready == nil || (ready.Reason != v1alpha1.HostConditionReasonProgressing && !createMayHaveSucceeded) {
		// Record the create before sending it so deletion keeps host ownership.
		setNetworkAttachmentsReady(bmi, metav1.ConditionFalse,
			v1alpha1.HostConditionReasonProgressing, "Network attachment provisioning is in progress")
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
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
		if apierrors.IsBadRequest(err) || apierrors.IsInvalid(err) ||
			apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
			logf.FromContext(ctx).Error(err, "NetworkAttachment create was rejected", "networkAttachment", key.Name)
			status := metav1.ConditionFalse
			if createMayHaveSucceeded {
				status = metav1.ConditionUnknown
			}
			setNetworkAttachmentCreateRejected(bmi, status)
			r.recordBMIEvent(bmi, corev1.EventTypeWarning,
				v1alpha1.HostConditionReasonNetworkAttachmentFailed, networkAttachmentCreateRejectedMessage)
			return ctrl.Result{}, nil
		}
		setNetworkAttachmentsReady(bmi, metav1.ConditionUnknown,
			v1alpha1.HostConditionReasonProgressing, "Waiting for the network attachment")
		return ctrl.Result{}, fmt.Errorf("create NetworkAttachment %s: %w", key.Name, err)
	}
	logf.FromContext(ctx).Info("Created NetworkAttachment", "networkAttachment", key.Name)
	setNetworkAttachmentsReady(bmi, metav1.ConditionFalse, v1alpha1.HostConditionReasonProgressing,
		"Waiting for the network attachment")
	return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
}

func setNetworkAttachmentCreateRejected(bmi *v1alpha1.BareMetalInstance, status metav1.ConditionStatus) {
	setNetworkAttachmentsReady(bmi, status, v1alpha1.HostConditionReasonNetworkAttachmentFailed,
		networkAttachmentCreateRejectedMessage)
	if condition := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady); condition != nil {
		condition.ObservedGeneration = bmi.Generation
	}
}

// failSelectedPortMAC leaves the assigned host in place and records a terminal
// failure so operators can identify the host that needs its inventory fixed.
func (r *BareMetalInstanceReconciler) failSelectedPortMAC(
	bmi *v1alpha1.BareMetalInstance, diagnostic string,
) (ctrl.Result, error) {
	bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
	bmi.SetStatusCondition(v1alpha1.HostConditionAllocated, metav1.ConditionFalse,
		v1alpha1.HostConditionReasonSelectedPortMACUnavailable, diagnostic)
	r.recordBMIEvent(bmi, corev1.EventTypeWarning,
		v1alpha1.HostConditionReasonSelectedPortMACUnavailable, diagnostic)
	return ctrl.Result{}, nil
}

// reconcileNetworkingDeletion deletes the attachment observed this pass and
// waits for cleanup. An absent read cannot rule out a Create that may still commit.
func (r *BareMetalInstanceReconciler) reconcileNetworkingDeletion(
	ctx context.Context,
	bareMetalInstance *v1alpha1.BareMetalInstance,
	observedAttachment *opv1alpha1.NetworkAttachment,
) (ctrl.Result, bool, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(bareMetalInstance, BareMetalInstanceNetworkingFinalizer) {
		return ctrl.Result{}, true, nil
	}

	if attachment := primaryNetworkAttachment(bareMetalInstance); attachment != nil && r.NetworkingEnabled {
		if observedAttachment != nil {
			key := client.ObjectKeyFromObject(observedAttachment)
			na := observedAttachment
			if na.DeletionTimestamp.IsZero() {
				log.Info("Deleting NetworkAttachment", "networkAttachment", key.Name)
				if err := r.Delete(ctx, na); err != nil && !apierrors.IsNotFound(err) {
					return ctrl.Result{}, false, fmt.Errorf("delete NetworkAttachment %s: %w", key.Name, err)
				}
			} else if len(na.Finalizers) == 1 && na.Finalizers[0] == BareMetalInstanceNetworkingFinalizer {
				ready := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
				if ready == nil || ready.Reason != v1alpha1.HostConditionReasonDetaching {
					setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionFalse,
						v1alpha1.HostConditionReasonDetaching, networkAttachmentCleanupMessage)
					return ctrl.Result{RequeueAfter: networkRetryInterval}, false, nil
				}
				// The networking controller has finished the Fabric cleanup.
				base := na.DeepCopy()
				controllerutil.RemoveFinalizer(na, BareMetalInstanceNetworkingFinalizer)
				if err := r.Patch(ctx, na, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil &&
					!apierrors.IsNotFound(err) {
					return ctrl.Result{}, false, fmt.Errorf("remove finalizer from NetworkAttachment %s: %w", key.Name, err)
				}
			}
			setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionFalse,
				v1alpha1.HostConditionReasonDetaching, networkAttachmentCleanupMessage)
			return ctrl.Result{RequeueAfter: networkRetryInterval}, false, nil
		}
		if networkAttachmentMayExist(bareMetalInstance) {
			setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionFalse,
				v1alpha1.HostConditionReasonProgressing, "Waiting for network attachment cleanup")
			return ctrl.Result{RequeueAfter: networkRetryInterval}, false, nil
		}
	}

	return r.removeNetworkingFinalizer(ctx, bareMetalInstance)
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
