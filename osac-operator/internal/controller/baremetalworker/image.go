// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
// restart. Missing version or image catalog data blocks worker creation and is
// reported in status as well as returned for error backoff.
func (r *Reconciler) resolveDiskImage(
	ctx context.Context, co *v1alpha1.ClusterOrder,
) (*privatev1.DiskImageReference, error) {
	clusterID := co.Labels[clusterOrderIDLabel]
	if clusterID == "" {
		return nil, fmt.Errorf("ClusterOrder missing %s label", clusterOrderIDLabel)
	}

	cluster, err := r.fulfillment.GetCluster(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("getting cluster %s: %w", clusterID, err)
	}

	versionID := cluster.GetSpec().GetVersion().GetId()
	if versionID == "" {
		return nil, r.reportDiskImageConfigError(ctx, co, fmt.Errorf("cluster %s has no version reference", clusterID))
	}

	cv, err := r.fulfillment.GetClusterVersion(ctx, versionID)
	if err != nil {
		lookupErr := fmt.Errorf("getting ClusterVersion %s: %w", versionID, err)
		if status.Code(err) == codes.NotFound {
			return nil, r.reportDiskImageConfigError(ctx, co, lookupErr)
		}
		return nil, lookupErr
	}

	diskImageKey := refKeyStr(cv.GetSpec().GetDiskImage())
	if diskImageKey == "" {
		return nil, r.reportDiskImageConfigError(ctx, co, fmt.Errorf("ClusterVersion %s has no disk_image reference", versionID))
	}

	di, err := r.fulfillment.GetDiskImage(ctx, diskImageKey)
	if err != nil {
		lookupErr := fmt.Errorf("getting DiskImage %s: %w", diskImageKey, err)
		if status.Code(err) == codes.NotFound {
			return nil, r.reportDiskImageConfigError(ctx, co, lookupErr)
		}
		return nil, lookupErr
	}
	image := privatev1.DiskImageReference_builder{
		Id:   di.GetId(),
		Name: di.GetMetadata().GetName(),
	}.Build()

	if apimeta.IsStatusConditionTrue(co.Status.Conditions, v1alpha1.ConditionRHCOSImageNotFound) {
		if condErr := r.setRHCOSImageNotFound(ctx, co, metav1.ConditionFalse, reasonDiskImageResolved,
			"disk_image reference resolved"); condErr != nil {
			return nil, condErr
		}
	}

	return image, nil
}

func (r *Reconciler) reportDiskImageConfigError(ctx context.Context, co *v1alpha1.ClusterOrder, configErr error) error {
	if err := r.setRHCOSImageNotFound(ctx, co, metav1.ConditionTrue, reasonDiskImageNotFound, configErr.Error()); err != nil {
		return err
	}
	return configErr
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
	return r.patchStatus(ctx, co, func(latest *v1alpha1.ClusterOrder) {
		latest.SetStatusCondition(v1alpha1.ConditionRHCOSImageNotFound, status, message, reason)
	})
}
