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

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
)

func listUnstructured(
	ctx context.Context,
	c client.Client,
	gvk schema.GroupVersionKind,
	namespace string,
	labels map[string]string,
	opts ...client.ListOption,
) (*unstructured.UnstructuredList, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvk)
	allOpts := append([]client.ListOption{
		client.InNamespace(namespace),
		client.MatchingLabels(labels),
	}, opts...)
	if err := c.List(ctx, list, allOpts...); err != nil {
		return nil, err
	}
	return list, nil
}

func (r *BareMetalInstanceReconciler) reconcileAutoCleanup(
	ctx context.Context,
	bareMetalInstance *v1alpha1.BareMetalInstance,
) (ctrl.Result, bool, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(bareMetalInstance, BareMetalInstanceCleanupFinalizer) {
		return ctrl.Result{}, true, nil
	}

	log.Info("Running auto-cleanup for auto-provisioned resources")

	bmiID := bareMetalInstance.Labels[bareMetalInstanceIDLabel]
	if bmiID == "" {
		log.Info("BareMetalInstance has no UUID label, skipping auto-cleanup",
			"label", bareMetalInstanceIDLabel)
		controllerutil.RemoveFinalizer(bareMetalInstance, BareMetalInstanceCleanupFinalizer)
		if updateErr := r.Update(ctx, bareMetalInstance); updateErr != nil {
			return ctrl.Result{}, false, updateErr
		}
		return ctrl.Result{}, true, nil
	}

	labels := map[string]string{
		autoCreatedLabel:    "true",
		autoCreatedForLabel: bmiID,
	}

	attachments, err := listUnstructured(
		ctx, r.Client, externalIPAttachmentGVK,
		bareMetalInstance.Namespace, labels,
	)
	if err != nil {
		if apimeta.IsNoMatchError(err) {
			log.Info("ExternalIPAttachment CRD not installed, skipping cleanup",
				"error", err)
			attachments = &unstructured.UnstructuredList{}
		} else {
			log.Error(err, "Cannot list ExternalIPAttachments, retrying")
			return ctrl.Result{}, false, err
		}
	}

	for i := range attachments.Items {
		att := &attachments.Items[i]
		if att.GetDeletionTimestamp().IsZero() {
			log.Info("Deleting auto-provisioned ExternalIPAttachment",
				"name", att.GetName())
			if err := r.Delete(ctx, att); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, false,
					fmt.Errorf("failed to delete ExternalIPAttachment %s: %w",
						att.GetName(), err)
			}
		}
	}

	if len(attachments.Items) > 0 {
		log.Info("Waiting for ExternalIPAttachment deletion to complete",
			"remaining", len(attachments.Items))
		return ctrl.Result{
			RequeueAfter: DefaultManagementRecheckIntervalDuration,
		}, false, nil
	}

	eips, err := listUnstructured(
		ctx, r.Client, externalIPGVK,
		bareMetalInstance.Namespace, labels,
	)
	if err != nil {
		if apimeta.IsNoMatchError(err) {
			log.Info("ExternalIP CRD not installed, skipping cleanup",
				"error", err)
			eips = &unstructured.UnstructuredList{}
		} else {
			log.Error(err, "Cannot list ExternalIPs, retrying")
			return ctrl.Result{}, false, err
		}
	}

	for i := range eips.Items {
		eip := &eips.Items[i]
		if eip.GetDeletionTimestamp().IsZero() {
			log.Info("Deleting auto-provisioned ExternalIP",
				"name", eip.GetName())
			if err := r.Delete(ctx, eip); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, false,
					fmt.Errorf("failed to delete ExternalIP %s: %w",
						eip.GetName(), err)
			}
		}
	}

	if len(eips.Items) > 0 {
		log.Info("Waiting for ExternalIP deletion to complete",
			"remaining", len(eips.Items))
		return ctrl.Result{
			RequeueAfter: DefaultManagementRecheckIntervalDuration,
		}, false, nil
	}

	controllerutil.RemoveFinalizer(bareMetalInstance, BareMetalInstanceCleanupFinalizer)
	if err := r.Update(ctx, bareMetalInstance); err != nil {
		return ctrl.Result{}, false, err
	}

	log.Info("Auto-cleanup completed")
	return ctrl.Result{}, true, nil
}

func (r *BareMetalInstanceReconciler) addCleanupFinalizerIfNeeded(
	ctx context.Context,
	bareMetalInstance *v1alpha1.BareMetalInstance,
) error {
	if controllerutil.ContainsFinalizer(bareMetalInstance, BareMetalInstanceCleanupFinalizer) {
		return nil
	}

	bmiID := bareMetalInstance.Labels[bareMetalInstanceIDLabel]
	if bmiID == "" {
		return nil
	}

	labels := map[string]string{
		autoCreatedLabel:    "true",
		autoCreatedForLabel: bmiID,
	}

	eips, err := listUnstructured(
		ctx, r.Client, externalIPGVK,
		bareMetalInstance.Namespace, labels,
		client.Limit(1),
	)
	if err != nil {
		logf.FromContext(ctx).V(1).Info("Skipping cleanup finalizer check",
			"error", err)
		return nil
	}

	if len(eips.Items) > 0 {
		if controllerutil.AddFinalizer(bareMetalInstance, BareMetalInstanceCleanupFinalizer) {
			return r.Update(ctx, bareMetalInstance)
		}
	}
	return nil
}
