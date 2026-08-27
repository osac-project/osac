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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

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

	if r.NetworkingProvider == nil {
		log.Info("Networking provider not configured, skipping network attachment provisioning")
		bareMetalInstance.SetStatusCondition(
			v1alpha1.HostConditionNetworkAttachmentsReady,
			metav1.ConditionTrue,
			"Skipped",
			"Networking provider not configured",
		)
		return ctrl.Result{}, nil
	}

	if controllerutil.AddFinalizer(bareMetalInstance, BareMetalInstanceNetworkingFinalizer) {
		if err := r.Update(ctx, bareMetalInstance); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	log.Info("Reconciling network attachments", "count", len(bareMetalInstance.Spec.NetworkAttachments))

	desiredVersion, err := provisioning.ComputeDesiredConfigVersion(struct {
		NetworkAttachments []v1alpha1.BareMetalNetworkAttachment
		ExternalHostID     string
		HostClass          string
	}{
		bareMetalInstance.Spec.NetworkAttachments,
		bareMetalInstance.Spec.ExternalHostID,
		bareMetalInstance.Spec.HostClass,
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	if bareMetalInstance.Status.NetworkingJobs == nil {
		bareMetalInstance.Status.NetworkingJobs = []opv1alpha1.JobStatus{}
	}

	result, err := provisioning.RunProvisioningLifecycle(
		ctx, r.NetworkingProvider, bareMetalInstance,
		&provisioning.State{
			Jobs:                 &bareMetalInstance.Status.NetworkingJobs,
			DesiredConfigVersion: desiredVersion,
		},
		provisioning.DefaultMaxJobHistory, r.ProvisionPollIntervalDuration,
		&provisioning.PollCallbacks{
			OnFailed: func(message string) {
				bareMetalInstance.SetStatusCondition(
					v1alpha1.HostConditionNetworkAttachmentsReady,
					metav1.ConditionFalse,
					v1alpha1.HostConditionReasonTemplateFailed,
					message,
				)
			},
			OnSuccess: func(_ provisioning.ProvisionStatus) {
				bareMetalInstance.SetStatusCondition(
					v1alpha1.HostConditionNetworkAttachmentsReady,
					metav1.ConditionTrue,
					"Succeeded",
					"All network attachments provisioned",
				)
			},
		},
		func() bool {
			return provisioning.CheckAPIServerForNonTerminalProvisionJob(
				ctx, r.apiReaderOrClient(), client.ObjectKeyFromObject(bareMetalInstance), &v1alpha1.BareMetalInstance{},
				func(obj client.Object) []opv1alpha1.JobStatus {
					return obj.(*v1alpha1.BareMetalInstance).Status.NetworkingJobs
				},
			)
		},
		func() error {
			return r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(bareMetalInstance), bareMetalInstance.Status)
		},
	)
	if err != nil {
		return result, err
	}

	if result.RequeueAfter > 0 {
		netCond := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
		if netCond == nil || netCond.Reason != v1alpha1.HostConditionReasonTemplateFailed {
			bareMetalInstance.SetStatusCondition(
				v1alpha1.HostConditionNetworkAttachmentsReady,
				metav1.ConditionFalse,
				v1alpha1.HostConditionReasonProgressing,
				"Network attachment provisioning in progress",
			)
		}
	}

	return result, nil
}

func (r *BareMetalInstanceReconciler) reconcileNetworkingDeletion(
	ctx context.Context,
	bareMetalInstance *v1alpha1.BareMetalInstance,
) (ctrl.Result, bool, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(bareMetalInstance, BareMetalInstanceNetworkingFinalizer) {
		return ctrl.Result{}, true, nil
	}

	if r.NetworkingProvider == nil || len(bareMetalInstance.Spec.NetworkAttachments) == 0 {
		controllerutil.RemoveFinalizer(bareMetalInstance, BareMetalInstanceNetworkingFinalizer)
		if err := r.Update(ctx, bareMetalInstance); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{}, true, nil
	}

	log.Info("Deprovisioning network attachments")

	if bareMetalInstance.Status.NetworkingJobs == nil {
		bareMetalInstance.Status.NetworkingJobs = []opv1alpha1.JobStatus{}
	}

	result, done, err := provisioning.RunDeprovisioningLifecycle(
		ctx, r.NetworkingProvider, bareMetalInstance,
		&bareMetalInstance.Status.NetworkingJobs,
		provisioning.DefaultMaxJobHistory, r.ProvisionPollIntervalDuration,
	)
	// Persist NetworkingJobs changes made by RunDeprovisioningLifecycle.
	// The CRD has a status subresource, so r.Update does not write status fields.
	if statusErr := r.Status().Update(ctx, bareMetalInstance); statusErr != nil {
		return ctrl.Result{}, false, statusErr
	}
	// DeprovisionSkipped: no deprovision template configured — treat as done.
	if !done && result.IsZero() && err == nil {
		done = true
	}
	if err != nil {
		return result, false, err
	}
	if !done {
		return result, false, nil
	}

	controllerutil.RemoveFinalizer(bareMetalInstance, BareMetalInstanceNetworkingFinalizer)
	if err := r.Update(ctx, bareMetalInstance); err != nil {
		return ctrl.Result{}, false, err
	}

	log.Info("Network attachment deprovisioning completed")
	return ctrl.Result{}, true, nil
}
