// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"fmt"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	reasonDiskImageNotFound = "DiskImageNotFound"
	reasonDiskImageResolved = "DiskImageResolved"
)

// resolveDiskImage reads the Cluster's ClusterVersion reference via the fulfillment-service
// private API and validates the referenced DiskImage. It returns the canonical DiskImage
// reference; fulfillment-service resolves its source URL when it materializes the provider CR.
// It re-resolves on every reconcile so a ClusterVersion upgrade takes effect without controller
// restart. Returns a nil reference (and no error) when the ClusterVersion carries no disk_image
// reference; reconciliation is requeued so workers are not created until a usable image is
// available.
func (r *Reconciler) resolveDiskImage(
	ctx context.Context, co *v1alpha1.ClusterOrder,
) (*privatev1.DiskImageReference, ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	clusterID := co.Labels[clusterOrderIDLabel]
	if clusterID == "" {
		log.Info("ClusterOrder missing clusterorder-uuid label, requeuing")
		return nil, ctrl.Result{RequeueAfter: infraEnvRequeueInterval}, nil
	}

	cluster, err := r.fulfillment.GetCluster(ctx, clusterID)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("getting cluster %s: %w", clusterID, err)
	}

	versionID := cluster.GetSpec().GetVersion().GetId()
	if versionID == "" {
		return nil, ctrl.Result{}, fmt.Errorf("cluster %s has no version reference", clusterID)
	}

	cv, err := r.fulfillment.GetClusterVersion(ctx, versionID)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("getting cluster version %s: %w", versionID, err)
	}

	diskImage := cv.GetSpec().GetDiskImage()
	diskImageKey := refKeyStr(diskImage)
	if diskImageKey == "" {
		log.Info("ClusterVersion has no disk_image, setting RHCOSImageNotFound", "clusterVersion", versionID)
		if condErr := r.setRHCOSImageNotFound(ctx, co, metav1.ConditionTrue, reasonDiskImageNotFound,
			fmt.Sprintf("ClusterVersion %s has no disk_image reference", versionID)); condErr != nil {
			return nil, ctrl.Result{}, condErr
		}
		return nil, ctrl.Result{RequeueAfter: infraEnvRequeueInterval}, nil
	}

	di, err := r.fulfillment.GetDiskImage(ctx, diskImageKey)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("getting disk image %s: %w", diskImageKey, err)
	}
	image := privatev1.DiskImageReference_builder{
		Id:   di.GetId(),
		Name: di.GetMetadata().GetName(),
	}.Build()

	if apimeta.IsStatusConditionTrue(co.Status.Conditions, v1alpha1.ConditionRHCOSImageNotFound) {
		if condErr := r.setRHCOSImageNotFound(ctx, co, metav1.ConditionFalse, reasonDiskImageResolved,
			"disk_image reference resolved"); condErr != nil {
			return nil, ctrl.Result{}, condErr
		}
	}

	return image, ctrl.Result{}, nil
}

// refKeyStr derives the lookup key for a DiskImage reference, mirroring the ComputeInstance
// reconciler's RefKeyStr (OSAC-3724): prefer the id, fall back to the name. The fulfillment-service
// ClusterVersion validation backfills the id on write, so the id branch is the usual path, but a
// name-only reference must still resolve identically to keep the two paths consistent. Kept as a
// local helper because the canonical RefKeyStr lives in the separate fulfillment-service Go module
// and cannot be imported here.
func refKeyStr(ref *privatev1.DiskImageReference) string {
	if ref.GetId() != "" {
		return ref.GetId()
	}
	return ref.GetName()
}

func (r *Reconciler) setRHCOSImageNotFound(
	ctx context.Context, co *v1alpha1.ClusterOrder, status metav1.ConditionStatus, reason, message string,
) error {
	return r.patchStatusWithRetry(ctx, co, func(latest *v1alpha1.ClusterOrder) {
		latest.SetStatusCondition(v1alpha1.ConditionRHCOSImageNotFound, status, message, reason)
	})
}
