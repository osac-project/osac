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

// reconcileNetworking creates the BareMetalInstance's NetworkAttachment and
// mirrors its readiness. The osac-operator networking controller runs the
// Fabric Manager task that moves the host's port to the tenant network.
func (r *BareMetalInstanceReconciler) reconcileNetworking(
	ctx context.Context,
	bareMetalInstance *v1alpha1.BareMetalInstance,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if len(bareMetalInstance.Spec.NetworkAttachments) == 0 {
		setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionTrue, "Skipped", "No network attachments configured")
		return ctrl.Result{}, nil
	}

	if !r.NetworkingEnabled {
		log.Info("Networking provisioning disabled, skipping network attachment provisioning")
		setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionTrue, "Skipped", "Networking provisioning disabled")
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
	na, resolvedKey, err := r.getExistingNetworkAttachment(ctx, bareMetalInstance, attachment)
	if err != nil {
		return ctrl.Result{}, err
	}

	if na == nil {
		// The Fabric task needs the tenant of the attachment.
		tenant := bareMetalInstance.Annotations[tenantAnnotation]
		if tenant == "" {
			setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonTemplateFailed,
				fmt.Sprintf("the BareMetalInstance has no %s annotation", tenantAnnotation))
			return ctrl.Result{}, nil
		}
		status := networkAttachmentStatusFor(bareMetalInstance, attachment.SubnetRef)
		if status == nil {
			return ctrl.Result{}, fmt.Errorf("network attachment status for subnet %s was not initialized", attachment.SubnetRef)
		}
		if status.MACAddress == "" {
			mac, err := r.selectedPortMAC(ctx, bareMetalInstance, attachment.Interface)
			if errors.Is(err, errSelectedPortMACUnavailable) {
				bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
				bareMetalInstance.SetStatusCondition(v1alpha1.HostConditionAllocated, metav1.ConditionFalse,
					v1alpha1.HostConditionReasonSelectedPortMACUnavailable, err.Error())
				r.recordBMIEvent(bareMetalInstance, corev1.EventTypeWarning,
					v1alpha1.HostConditionReasonSelectedPortMACUnavailable, err.Error())
				return r.releaseHostWithoutSelectedPortMAC(ctx, bareMetalInstance, err.Error())
			}
			if err != nil {
				return ctrl.Result{}, err
			}
			status.MACAddress = mac
			setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonProgressing,
				"Recording the selected interface MAC before creating the network attachment")
			return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
		}
		na = &opv1alpha1.NetworkAttachment{
			ObjectMeta: metav1.ObjectMeta{
				Name:        key.Name,
				Namespace:   key.Namespace,
				Finalizers:  []string{BareMetalInstanceNetworkingFinalizer},
				Annotations: map[string]string{tenantAnnotation: tenant},
			},
			Spec: opv1alpha1.NetworkAttachmentSpec{
				Target: opv1alpha1.NetworkAttachmentTarget{
					Kind: opv1alpha1.NetworkAttachmentTargetBareMetalInstance,
					ID:   bareMetalInstance.Labels[bareMetalInstanceIDLabel],
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
		// Create only if missing; AlreadyExists means an earlier reconcile created it.
		if err := r.Create(ctx, na); err != nil && !apierrors.IsAlreadyExists(err) {
			return ctrl.Result{}, fmt.Errorf("create NetworkAttachment %s: %w", key.Name, err)
		}
		log.Info("Created NetworkAttachment", "networkAttachment", key.Name)
		setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonProgressing,
			"Waiting for the network attachment")
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
	}

	// Existing BMI-only attachments are reused through upgrades. Mirror their
	// recorded port identity into status without changing the NetworkAttachment.
	if status := networkAttachmentStatusFor(bareMetalInstance, attachment.SubnetRef); status != nil && status.MACAddress == "" {
		mac, err := normalizeMAC(na.Spec.Interface.MACAddress)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("existing NetworkAttachment %s has invalid interface MAC: %w", resolvedKey.Name, err)
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

	if attachment := primaryNetworkAttachment(bareMetalInstance); attachment != nil && r.NetworkingEnabled {
		_, ok := networkAttachmentKey(bareMetalInstance, attachment)
		if !ok {
			return r.removeNetworkingFinalizer(ctx, bareMetalInstance)
		}
		na, key, err := r.getExistingNetworkAttachment(ctx, bareMetalInstance, attachment)
		if err != nil {
			return ctrl.Result{}, false, err
		}
		if na != nil {
			if na.DeletionTimestamp.IsZero() {
				log.Info("Deleting NetworkAttachment", "networkAttachment", key.Name)
				if err := r.Delete(ctx, na); err != nil && !apierrors.IsNotFound(err) {
					return ctrl.Result{}, false, fmt.Errorf("delete NetworkAttachment %s: %w", key.Name, err)
				}
			} else if len(na.Finalizers) == 1 && na.Finalizers[0] == BareMetalInstanceNetworkingFinalizer {
				// The networking controller has finished the Fabric cleanup.
				base := na.DeepCopy()
				controllerutil.RemoveFinalizer(na, BareMetalInstanceNetworkingFinalizer)
				if err := r.Patch(ctx, na, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil &&
					!apierrors.IsNotFound(err) {
					return ctrl.Result{}, false, fmt.Errorf("remove finalizer from NetworkAttachment %s: %w", key.Name, err)
				}
			}
			setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonProgressing,
				"Waiting for network attachment cleanup")
			return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, false, nil
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
