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
	// bareMetalInstanceIDLabel carries the API ID of a BareMetalInstance.
	bareMetalInstanceIDLabel = "osac.openshift.io/baremetalinstance-uuid"

	// tenantAnnotation carries the tenant API ID of a BareMetalInstance and of
	// its NetworkAttachment.
	tenantAnnotation = "osac.openshift.io/tenant"
)

// networkingEnabled reports whether BMF attaches hosts to tenant networks.
func (r *BareMetalInstanceReconciler) networkingEnabled() bool {
	return r.NetworkingNamespace != ""
}

// networkAttachmentKey returns the key of the BareMetalInstance's
// NetworkAttachment. It returns false when the BareMetalInstance has no API ID
// label, so it cannot have a NetworkAttachment.
func (r *BareMetalInstanceReconciler) networkAttachmentKey(bmi *v1alpha1.BareMetalInstance) (client.ObjectKey, bool) {
	id := bmi.Labels[bareMetalInstanceIDLabel]
	if id == "" {
		return client.ObjectKey{}, false
	}
	return client.ObjectKey{
		Namespace: r.NetworkingNamespace,
		Name:      opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, id),
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

// selectedPortMAC returns the inventory MAC address of a host interface.
func (r *BareMetalInstanceReconciler) selectedPortMAC(
	ctx context.Context, bmi *v1alpha1.BareMetalInstance, iface string,
) (string, error) {
	if r.InventoryClient == nil {
		return "", fmt.Errorf("inventory client not configured")
	}
	macs, err := r.InventoryClient.GetHostLogicalPortMACs(ctx, bmi.Spec.ExternalHostID)
	if err != nil {
		return "", err
	}
	mac := macs[iface]
	if mac == "" {
		return "", fmt.Errorf("the inventory has no MAC address for interface %q", iface)
	}
	return normalizeMAC(mac)
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

	if !r.networkingEnabled() {
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

	key, ok := r.networkAttachmentKey(bareMetalInstance)
	if !ok {
		setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonTemplateFailed,
			fmt.Sprintf("the BareMetalInstance has no %s label", bareMetalInstanceIDLabel))
		return ctrl.Result{}, nil
	}

	na, err := r.getNetworkAttachment(ctx, key)
	if err != nil {
		return ctrl.Result{}, err
	}

	if na == nil {
		attachment := bareMetalInstance.Spec.NetworkAttachments[0]
		mac, err := r.selectedPortMAC(ctx, bareMetalInstance, attachment.Interface)
		if err != nil {
			setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonTemplateFailed,
				fmt.Sprintf("cannot resolve the MAC address of interface %q: %v", attachment.Interface, err))
			return ctrl.Result{}, nil
		}
		na = &opv1alpha1.NetworkAttachment{
			ObjectMeta: metav1.ObjectMeta{
				Name:        key.Name,
				Namespace:   key.Namespace,
				Finalizers:  []string{BareMetalInstanceNetworkingFinalizer},
				Annotations: map[string]string{tenantAnnotation: bareMetalInstance.Annotations[tenantAnnotation]},
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
					MACAddress: mac,
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

	ready := meta.FindStatusCondition(na.Status.Conditions, opv1alpha1.NetworkConditionReady)
	switch {
	case ready != nil && ready.Status == metav1.ConditionTrue:
		setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionTrue, "Succeeded", "Network attachment is ready")
		return ctrl.Result{}, nil
	case ready != nil && ready.Reason == opv1alpha1.ReasonFailed:
		setNetworkAttachmentsReady(bareMetalInstance, metav1.ConditionFalse, v1alpha1.HostConditionReasonTemplateFailed, ready.Message)
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

	if key, ok := r.networkAttachmentKey(bareMetalInstance); ok && r.networkingEnabled() {
		na, err := r.getNetworkAttachment(ctx, key)
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

	controllerutil.RemoveFinalizer(bareMetalInstance, BareMetalInstanceNetworkingFinalizer)
	if err := r.Update(ctx, bareMetalInstance); err != nil {
		return ctrl.Result{}, false, err
	}

	log.Info("Network attachment cleanup completed")
	return ctrl.Result{}, true, nil
}
