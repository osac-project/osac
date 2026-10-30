/*
Copyright (c) 2026 Red Hat Inc.

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

package computeinstance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const volumeRecordCleanupRetryInterval = time.Second

func (t *task) releaseFinalizerAfterVolumeCleanup(ctx context.Context, release func()) error {
	if release == nil {
		return t.retryVolumeCleanup(errors.New("finalizer release callback is not configured"))
	}
	if t.computeInstance == nil || t.computeInstance.GetId() == "" {
		return t.retryVolumeCleanup(errors.New("ComputeInstance is not configured"))
	}

	instance := t.computeInstance
	recoveryNeeded, err := diskRegistrationRecoveryNeeded(instance)
	if err != nil {
		return t.retryVolumeCleanup(err)
	}
	if recoveryNeeded {
		if t.r == nil || t.r.computeInstancesClient == nil || t.r.volumesClient == nil {
			return t.retryVolumeCleanup(errors.New("disk registration recovery clients are not configured"))
		}
		if err = ReconcileDiskRegistrations(ctx, t.r.computeInstancesClient, t.r.volumesClient, instance.GetId()); err != nil {
			return t.retryVolumeCleanup(err)
		}
		instance, err = getComputeInstanceForDiskRegistration(ctx, t.r.computeInstancesClient, instance.GetId())
		if err != nil {
			return t.retryVolumeCleanup(err)
		}
		prepared, err := prepareDiskRegistrations(instance)
		if err != nil {
			return t.retryVolumeCleanup(err)
		}
		if _, err := diskRegistrationsComplete(instance, prepared); err != nil {
			return t.retryVolumeCleanup(err)
		}
	}

	return t.reconcileVolumeRecordCleanup(ctx, instance, release)
}

func diskRegistrationRecoveryNeeded(instance *privatev1.ComputeInstance) (bool, error) {
	registrations, err := diskRegistrationByKey(instance.GetStatus().GetVolumeRegistration())
	if err != nil {
		return false, err
	}
	volumes, err := diskVolumesByKey(instance.GetStatus().GetVolumes())
	if err != nil {
		return false, err
	}
	for diskKey, registration := range registrations {
		if registration.GetState() == privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN && volumes[diskKey] == nil {
			return true, nil
		}
	}
	return false, nil
}

func (t *task) reconcileVolumeRecordCleanup(
	ctx context.Context,
	instance *privatev1.ComputeInstance,
	release func(),
) error {
	if _, err := diskRegistrationByKey(instance.GetStatus().GetVolumeRegistration()); err != nil {
		return t.retryVolumeCleanup(err)
	}
	volumesByKey, err := diskVolumesByKey(instance.GetStatus().GetVolumes())
	if err != nil {
		return t.retryVolumeCleanup(err)
	}
	registrationPending := false
	for _, registration := range instance.GetStatus().GetVolumeRegistration() {
		if registration == nil {
			return t.retryVolumeCleanup(errors.New("volume registration state is empty"))
		}
		diskKey := registration.GetDiskKey()
		if volumesByKey[diskKey] != nil {
			continue
		}
		switch registration.GetState() {
		case privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED,
			privatev1.ComputeInstanceVolumeRegistration_STATE_NON_COMMITTED_FAILURE:
			continue
		case privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN:
			registrationPending = true
			t.logVolumeCleanupPending(ctx, instance.GetId(), diskKey, "registration outcome is still uncertain", "")
		case privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED:
			return t.retryVolumeCleanup(fmt.Errorf("disk %q is resolved without a persisted Volume ID", diskKey))
		default:
			return t.retryVolumeCleanup(fmt.Errorf("disk %q has unsupported registration state %d",
				diskKey, registration.GetState()))
		}
	}
	if registrationPending {
		return t.retryVolumeCleanup(errors.New("volume registration recovery is still pending"))
	}
	if len(volumesByKey) == 0 {
		release()
		return nil
	}
	if t.r == nil || t.r.volumesClient == nil {
		return t.retryVolumeCleanup(errors.New("volumes client is not configured"))
	}
	if instance.GetMetadata() == nil || instance.GetMetadata().GetTenant() == "" {
		return t.retryVolumeCleanup(errors.New("ComputeInstance tenant is required for Volume cleanup"))
	}

	seenIDs := make(map[string]struct{}, len(volumesByKey))
	pending := false
	for _, storedVolume := range instance.GetStatus().GetVolumes() {
		volumeID := storedVolume.GetVolumeId()
		if volumeID == "" {
			return t.retryVolumeCleanup(fmt.Errorf("disk %q has an empty persisted Volume ID", storedVolume.GetDiskKey()))
		}
		if _, duplicate := seenIDs[volumeID]; duplicate {
			return t.retryVolumeCleanup(fmt.Errorf("volume ID %q is recorded for more than one disk", volumeID))
		}
		seenIDs[volumeID] = struct{}{}

		stillPresent, cleanupErr := t.deleteAndConfirmVolumeRecord(ctx, instance, storedVolume.GetDiskKey(), volumeID)
		if cleanupErr != nil {
			return t.retryVolumeCleanup(cleanupErr)
		}
		pending = pending || stillPresent
	}
	if pending {
		return t.retryVolumeCleanup(errors.New("volume records are still present"))
	}

	release()
	return nil
}

func (t *task) deleteAndConfirmVolumeRecord(
	ctx context.Context,
	instance *privatev1.ComputeInstance,
	diskKey string,
	volumeID string,
) (bool, error) {
	volume, found, err := t.getVolumeRecordForCleanup(ctx, instance, diskKey, volumeID)
	if err != nil || !found {
		return false, err
	}
	if volumeRecordDeletionStarted(volume) {
		t.logVolumeCleanupPending(ctx, instance.GetId(), diskKey, "waiting for Volume record removal", volumeID)
		return true, nil
	}

	_, err = t.r.volumesClient.Delete(ctx, privatev1.VolumesDeleteRequest_builder{Id: volumeID}.Build())
	if err != nil && status.Code(err) != codes.NotFound {
		return false, fmt.Errorf("failed to request deletion of recorded Volume %q: %w", volumeID, err)
	}

	_, found, err = t.getVolumeRecordForCleanup(ctx, instance, diskKey, volumeID)
	if err != nil || !found {
		return false, err
	}
	t.logVolumeCleanupPending(ctx, instance.GetId(), diskKey, "record remains visible after Delete", volumeID)
	return true, nil
}

func (t *task) getVolumeRecordForCleanup(
	ctx context.Context,
	instance *privatev1.ComputeInstance,
	diskKey string,
	volumeID string,
) (*privatev1.Volume, bool, error) {
	response, err := t.r.volumesClient.Get(ctx, privatev1.VolumesGetRequest_builder{Id: volumeID}.Build())
	if status.Code(err) == codes.NotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("failed to confirm removal of recorded Volume %q: %w", volumeID, err)
	}
	if response == nil || response.GetObject() == nil {
		return nil, false, fmt.Errorf("Get returned no object for recorded Volume %q", volumeID)
	}
	volume := response.GetObject()
	if err := validateVolumeRecordForParent(volume, instance, diskKey, volumeID); err != nil {
		return nil, false, fmt.Errorf("disk %q: %w", diskKey, err)
	}
	return volume, true, nil
}

func validateVolumeRecordForParent(
	volume *privatev1.Volume,
	instance *privatev1.ComputeInstance,
	diskKey string,
	volumeID string,
) error {
	if volume == nil || volume.GetId() != volumeID || volume.GetMetadata() == nil ||
		instance == nil || instance.GetMetadata() == nil {
		return fmt.Errorf("Get returned an unexpected object for recorded volume %q", volumeID)
	}
	metadata := volume.GetMetadata()
	parentMetadata := instance.GetMetadata()
	expectedName := fmt.Sprintf("ci-%s-%s", instance.GetId(), diskKey)
	if metadata.GetName() != expectedName ||
		metadata.GetTenant() != parentMetadata.GetTenant() ||
		metadata.GetProject() != parentMetadata.GetProject() ||
		metadata.GetCreator() != "system" {
		return fmt.Errorf("recorded volume %q does not match ComputeInstance disk %q", volumeID, diskKey)
	}
	return nil
}

// DELETED means backend cleanup completed; the Volume record may still be visible.
func volumeRecordDeletionStarted(volume *privatev1.Volume) bool {
	return volume.GetMetadata().HasDeletionTimestamp() ||
		volume.GetStatus().GetState() == privatev1.VolumeState_VOLUME_STATE_DELETING ||
		volume.GetStatus().GetState() == privatev1.VolumeState_VOLUME_STATE_DELETED
}

func (t *task) logVolumeCleanupPending(
	ctx context.Context,
	computeInstanceID string,
	diskKey string,
	reason string,
	volumeID string,
) {
	if t.r == nil || t.r.logger == nil {
		return
	}
	attrs := []any{
		slog.String("compute_instance_id", computeInstanceID),
		slog.String("disk_key", diskKey),
		slog.String("reason", reason),
	}
	if volumeID != "" {
		attrs = append(attrs, slog.String("volume_id", volumeID))
	}
	t.r.logger.InfoContext(ctx, "Volume record cleanup pending", attrs...)
}

func (t *task) retryVolumeCleanup(err error) error {
	if err == nil {
		err = errors.New("volume cleanup is pending")
	}
	computeInstanceID := ""
	if t != nil && t.computeInstance != nil {
		computeInstanceID = t.computeInstance.GetId()
	}
	return controllers.RequeueAtInterval(fmt.Errorf(
		"volume cleanup for ComputeInstance %q is pending: %w", computeInstanceID, err),
		volumeRecordCleanupRetryInterval,
	)
}
