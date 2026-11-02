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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/networkattachment"
)

const (
	networkAttachmentApplyTask  = "apply_network_attachment"
	networkAttachmentDeleteTask = "delete_network_attachment"
	networkAttachmentQueryTask  = "query_dhcp_lease"

	eventReasonLeaseNotFound = "LeaseNotFound"
)

// providerNetworkingEnabled reports whether hub NetworkAttachment create/delete
// is configured. An empty NetworkingNamespace means provider networking is
// disabled (Design §Provider networking disabled).
func (r *BareMetalInstanceReconciler) providerNetworkingEnabled() bool {
	return r.NetworkingNamespace != ""
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

	if !r.providerNetworkingEnabled() {
		log.Info("Provider networking disabled, skipping NetworkAttachment create")
		bareMetalInstance.SetStatusCondition(
			v1alpha1.HostConditionNetworkAttachmentsReady,
			metav1.ConditionTrue,
			v1alpha1.HostConditionReasonProvisioningDisabled,
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
	err := r.apiReaderOrClient().Get(ctx, client.ObjectKey{Namespace: r.NetworkingNamespace, Name: name}, na)
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

	return r.mirrorNetworkAttachmentReady(bareMetalInstance, na), nil
}

func (r *BareMetalInstanceReconciler) needsNetworkOffboard(
	ctx context.Context, bmi *v1alpha1.BareMetalInstance,
) (bool, error) {
	if !r.providerNetworkingEnabled() || len(bmi.Spec.NetworkAttachments) == 0 ||
		bmi.Spec.ExternalHostID == "" {
		return false, nil
	}
	apiID := bmi.Labels[BareMetalInstanceUUIDLabel]
	if apiID == "" {
		return false, nil
	}
	key := client.ObjectKey{
		Namespace: r.NetworkingNamespace,
		Name:      opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, apiID),
	}
	na := &opv1alpha1.NetworkAttachment{}
	if err := r.apiReaderOrClient().Get(ctx, key, na); apierrors.IsNotFound(err) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("get NetworkAttachment before offboarding: %w", err)
	}
	return true, nil
}

func (r *BareMetalInstanceReconciler) ensureNetworkAttachmentStatusMAC(
	ctx context.Context,
	bmi *v1alpha1.BareMetalInstance,
) error {
	attachment := bmi.Spec.NetworkAttachments[0]
	if len(bmi.Status.NetworkAttachmentStatuses) == 0 {
		bmi.Status.NetworkAttachmentStatuses = []v1alpha1.BareMetalNetworkAttachmentStatus{{
			Interface: attachment.Interface,
			SubnetRef: attachment.SubnetRef,
			Primary:   attachment.Primary || len(bmi.Spec.NetworkAttachments) == 1,
		}}
	}
	status := &bmi.Status.NetworkAttachmentStatuses[0]
	if status.Interface == "" {
		status.Interface = attachment.Interface
	}
	if status.SubnetRef == "" {
		status.SubnetRef = attachment.SubnetRef
	}
	if status.MACAddress != "" {
		return nil
	}
	if attachment.Interface == "" {
		return fmt.Errorf("network attachment has no interface name")
	}
	if bmi.Spec.ExternalHostID == "" {
		return fmt.Errorf("baremetalinstance has no externalHostID for MAC lookup")
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
	return inventory.NormalizeMACAddress(value)
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
	iface := attachment.Interface
	if iface == "" {
		iface = bmi.Status.NetworkAttachmentStatuses[0].Interface
	}
	if iface == "" {
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
			Namespace:  r.NetworkingNamespace,
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
				Name:       iface,
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
			v1alpha1.HostConditionReasonDetaching,
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
		for i := len(na.Status.Jobs) - 1; i >= 0; i-- {
			job := na.Status.Jobs[i]
			if job.Task == task && job.Message != "" {
				return job.Message
			}
		}
	}
	for i := len(na.Status.Jobs) - 1; i >= 0; i-- {
		if na.Status.Jobs[i].Message != "" {
			return na.Status.Jobs[i].Message
		}
	}
	return ""
}

func latestNetworkAttachmentTaskMessage(na *opv1alpha1.NetworkAttachment, task string) string {
	for i := len(na.Status.Jobs) - 1; i >= 0; i-- {
		job := na.Status.Jobs[i]
		if job.Task == task && job.Message != "" {
			return job.Message
		}
	}
	return ""
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
		// Without an API ID there is no deterministic NetworkAttachment name;
		// drop the finalizer so deletion can proceed.
		log.Info("Removing networking finalizer; BMI has no API ID label")
		controllerutil.RemoveFinalizer(bareMetalInstance, BareMetalInstanceNetworkingFinalizer)
		if err := r.Update(ctx, bareMetalInstance); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{}, true, nil
	}

	gone, err := r.deleteBareMetalNetworkAttachment(ctx, apiID)
	if err != nil {
		return ctrl.Result{}, false, err
	}
	if !gone {
		message := r.networkAttachmentDeleteMessage(ctx, apiID)
		bareMetalInstance.SetStatusCondition(
			v1alpha1.HostConditionNetworkAttachmentsReady,
			metav1.ConditionFalse,
			v1alpha1.HostConditionReasonDetaching,
			message,
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
// the BMF finalizer from it once only that finalizer remains (Design Delete
// steps 5–6). It reads the API server, not the cache. It returns true when the
// NetworkAttachment is gone.
func (r *BareMetalInstanceReconciler) deleteBareMetalNetworkAttachment(
	ctx context.Context,
	apiID string,
) (bool, error) {
	key := client.ObjectKey{
		Namespace: r.NetworkingNamespace,
		Name:      opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, apiID),
	}
	gone, err := networkattachment.DeleteOwned(
		ctx, r.apiReaderOrClient(), r.Client, key, BareMetalInstanceNetworkingFinalizer,
	)
	if err != nil {
		return false, err
	}
	if gone {
		return true, nil
	}
	return false, nil
}

func (r *BareMetalInstanceReconciler) networkAttachmentDeleteMessage(ctx context.Context, apiID string) string {
	key := client.ObjectKey{
		Namespace: r.NetworkingNamespace,
		Name:      opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, apiID),
	}
	na := &opv1alpha1.NetworkAttachment{}
	if err := r.apiReaderOrClient().Get(ctx, key, na); err == nil {
		if message := latestNetworkAttachmentTaskMessage(na, networkAttachmentDeleteTask); message != "" {
			return message
		}
	}
	return "waiting for NetworkAttachment deletion"
}
