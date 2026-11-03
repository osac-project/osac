/*
Copyright (c) 2026 Red Hat, Inc.

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
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// ReconcileDiskRegistrations creates or recovers the Volume records described
// by a persisted ComputeInstance. It never dispatches workload changes. A nil
// error means this pass encountered no error, not that registration is complete;
// callers must read the resulting ComputeInstance to decide what happens next.
func ReconcileDiskRegistrations(
	ctx context.Context,
	computeInstancesClient privatev1.ComputeInstancesClient,
	volumesClient privatev1.VolumesClient,
	computeInstanceID string,
) error {
	if computeInstancesClient == nil {
		return fmt.Errorf("ComputeInstances client is not configured")
	}
	if volumesClient == nil {
		return fmt.Errorf("volumes client is not configured")
	}
	if computeInstanceID == "" {
		return fmt.Errorf("ComputeInstance ID is required for disk registration")
	}

	current, err := getComputeInstanceForDiskRegistration(ctx, computeInstancesClient, computeInstanceID)
	if err != nil {
		return err
	}
	if !hasDiskRegistrationFinalizer(current) {
		return nil
	}
	prepared, err := prepareDiskRegistrations(current)
	if err != nil {
		return err
	}

	t := &task{r: &function{computeInstancesClient: computeInstancesClient}, computeInstance: current}
	if !current.GetMetadata().HasDeletionTimestamp() {
		_, conflict, persistErr := t.persistDiskRegistrationIntents(ctx, current, prepared)
		if persistErr != nil {
			return persistErr
		}
		if conflict {
			return nil
		}
	}

	for _, initialCandidate := range prepared {
		if err := reconcileDiskRegistrationCandidate(ctx, computeInstancesClient, volumesClient,
			computeInstanceID, initialCandidate.diskKey); err != nil {
			return err
		}
	}

	return nil
}

func reconcileDiskRegistrationCandidate(
	ctx context.Context,
	computeInstancesClient privatev1.ComputeInstancesClient,
	volumesClient privatev1.VolumesClient,
	computeInstanceID string,
	diskKey string,
) error {
	current, err := getComputeInstanceForDiskRegistration(ctx, computeInstancesClient, computeInstanceID)
	if err != nil {
		return err
	}
	if !hasDiskRegistrationFinalizer(current) {
		return nil
	}
	prepared, err := prepareDiskRegistrations(current)
	if err != nil {
		return err
	}
	candidate, found := preparedDiskByKey(prepared, diskKey)
	if !found {
		return fmt.Errorf("ComputeInstance '%s' disk '%s' is no longer present", computeInstanceID, diskKey)
	}
	registrations, err := diskRegistrationByKey(current.GetStatus().GetVolumeRegistration())
	if err != nil {
		return err
	}
	volumes, err := diskVolumesByKey(current.GetStatus().GetVolumes())
	if err != nil {
		return err
	}
	if err := validateDiskRegistrationKeys(current, prepared, registrations, volumes); err != nil {
		return err
	}
	registration := registrations[diskKey]
	if volume, exists := volumes[diskKey]; exists {
		if volume.GetVolumeId() == "" {
			return fmt.Errorf("ComputeInstance '%s' disk '%s' has an empty persisted Volume ID", computeInstanceID, diskKey)
		}
		if !current.GetMetadata().HasDeletionTimestamp() &&
			(registration == nil || registration.GetState() != privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED) {
			return persistDiskRegistrationResolution(ctx, computeInstancesClient,
				current, diskKey, volume.GetVolumeId())
		}
		return nil
	}
	if registration == nil {
		return nil
	}
	switch registration.GetState() {
	case privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN:
		return recoverUncertainDiskRegistration(ctx, computeInstancesClient, volumesClient,
			current, candidate)
	case privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED,
		privatev1.ComputeInstanceVolumeRegistration_STATE_NON_COMMITTED_FAILURE:
		if current.GetMetadata().HasDeletionTimestamp() {
			return nil
		}
		return createDiskRegistration(ctx, computeInstancesClient, volumesClient,
			computeInstanceID, current, candidate)
	default:
		return fmt.Errorf("ComputeInstance '%s' disk '%s' has unsupported registration state %d",
			computeInstanceID, diskKey, registration.GetState())
	}
}

func recoverUncertainDiskRegistration(
	ctx context.Context,
	computeInstancesClient privatev1.ComputeInstancesClient,
	volumesClient privatev1.VolumesClient,
	current *privatev1.ComputeInstance,
	candidate preparedDiskRegistration,
) error {
	volume, found, err := findDiskRegistrationVolume(ctx, volumesClient, current, candidate)
	if err != nil {
		return fmt.Errorf("failed to recover Volume for ComputeInstance '%s' disk '%s': %w",
			current.GetId(), candidate.diskKey, err)
	}
	if !found {
		return nil
	}
	return persistDiskRegistrationResolution(ctx, computeInstancesClient,
		current, candidate.diskKey, volume.GetId())
}

func createDiskRegistration(
	ctx context.Context,
	computeInstancesClient privatev1.ComputeInstancesClient,
	volumesClient privatev1.VolumesClient,
	computeInstanceID string,
	current *privatev1.ComputeInstance,
	candidate preparedDiskRegistration,
) error {
	claimed, err := claimDiskRegistration(ctx, computeInstancesClient, current, candidate)
	if err != nil {
		return fmt.Errorf("failed to commit attempted marker for ComputeInstance '%s' disk '%s': %w",
			computeInstanceID, candidate.diskKey, err)
	}
	if !claimed {
		return nil
	}
	current, candidate, claimed, err = reloadClaimedDiskRegistration(ctx, computeInstancesClient,
		computeInstanceID, candidate)
	if err != nil || !claimed {
		return err
	}
	response, createErr := volumesClient.Create(ctx, candidate.request)
	if createErr != nil {
		return handleDiskRegistrationCreateError(ctx, computeInstancesClient, volumesClient,
			computeInstanceID, current, candidate, createErr)
	}
	volume := (*privatev1.Volume)(nil)
	if response != nil {
		volume = response.GetObject()
	}
	if diskRegistrationVolumeMatches(volume, candidate.request) {
		return persistDiskRegistrationResolution(ctx, computeInstancesClient,
			current, candidate.diskKey, volume.GetId())
	}
	return recoverCreatedDiskRegistration(ctx, computeInstancesClient, volumesClient,
		current, candidate)
}

func reloadClaimedDiskRegistration(
	ctx context.Context,
	client privatev1.ComputeInstancesClient,
	computeInstanceID string,
	candidate preparedDiskRegistration,
) (*privatev1.ComputeInstance, preparedDiskRegistration, bool, error) {
	current, err := getComputeInstanceForDiskRegistration(ctx, client, computeInstanceID)
	if err != nil {
		return nil, preparedDiskRegistration{}, false, err
	}
	if !hasDiskRegistrationFinalizer(current) {
		return nil, preparedDiskRegistration{}, false, nil
	}
	if current.GetMetadata().HasDeletionTimestamp() {
		// Only this claim's owner knows Volume Create has not been called.
		return nil, preparedDiskRegistration{}, false,
			settleUndispatchedDiskRegistration(ctx, client, current, candidate.diskKey)
	}
	prepared, err := prepareDiskRegistrations(current)
	if err != nil {
		return nil, preparedDiskRegistration{}, false, err
	}
	latest, found := preparedDiskByKey(prepared, candidate.diskKey)
	if !found || !proto.Equal(latest.request.GetObject(), candidate.request.GetObject()) {
		return nil, preparedDiskRegistration{}, false,
			fmt.Errorf("ComputeInstance '%s' disk '%s' changed after its attempted marker was committed",
				computeInstanceID, candidate.diskKey)
	}
	registrations, err := diskRegistrationByKey(current.GetStatus().GetVolumeRegistration())
	if err != nil {
		return nil, preparedDiskRegistration{}, false, err
	}
	volumes, err := diskVolumesByKey(current.GetStatus().GetVolumes())
	if err != nil {
		return nil, preparedDiskRegistration{}, false, err
	}
	if err := validateDiskRegistrationKeys(current, prepared, registrations, volumes); err != nil {
		return nil, preparedDiskRegistration{}, false, err
	}
	if _, exists := volumes[candidate.diskKey]; exists ||
		registrations[candidate.diskKey] == nil ||
		registrations[candidate.diskKey].GetState() != privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN {
		return nil, preparedDiskRegistration{}, false, nil
	}
	return current, latest, true, nil
}

func settleUndispatchedDiskRegistration(
	ctx context.Context,
	client privatev1.ComputeInstancesClient,
	current *privatev1.ComputeInstance,
	diskKey string,
) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !hasDiskRegistrationFinalizer(current) {
			return nil
		}
		if !current.GetMetadata().HasDeletionTimestamp() {
			return fmt.Errorf("ComputeInstance '%s' deletion was cleared before settling disk '%s'",
				current.GetId(), diskKey)
		}
		// The locked update protects a newer registration result or Volume ID.
		conflict, err := persistDiskRegistrationState(ctx, client, current, diskKey,
			privatev1.ComputeInstanceVolumeRegistration_STATE_NON_COMMITTED_FAILURE)
		if err != nil || !conflict {
			return err
		}
		current, err = getComputeInstanceForDiskRegistration(ctx, client, current.GetId())
		if err != nil {
			return err
		}
	}
}

func handleDiskRegistrationCreateError(
	ctx context.Context,
	computeInstancesClient privatev1.ComputeInstancesClient,
	volumesClient privatev1.VolumesClient,
	computeInstanceID string,
	current *privatev1.ComputeInstance,
	candidate preparedDiskRegistration,
	createErr error,
) error {
	if isKnownNonCommittedVolumeCreateError(createErr) {
		latest, getErr := getComputeInstanceForDiskRegistration(ctx, computeInstancesClient, computeInstanceID)
		if getErr != nil {
			return fmt.Errorf("volume Create failed (%w) and ComputeInstance reload failed: %w", createErr, getErr)
		}
		conflict, stateErr := persistDiskRegistrationState(ctx, computeInstancesClient, latest,
			candidate.diskKey, privatev1.ComputeInstanceVolumeRegistration_STATE_NON_COMMITTED_FAILURE)
		if stateErr != nil {
			return fmt.Errorf("volume Create failed (%w) and retryable state could not be saved: %w", createErr, stateErr)
		}
		if conflict {
			return nil
		}
		return createErr
	}
	if err := recoverCreatedDiskRegistration(ctx, computeInstancesClient, volumesClient,
		current, candidate); err != nil {
		return fmt.Errorf("volume Create failed (%w) and recovery failed: %w", createErr, err)
	}
	return nil
}

func recoverCreatedDiskRegistration(
	ctx context.Context,
	computeInstancesClient privatev1.ComputeInstancesClient,
	volumesClient privatev1.VolumesClient,
	current *privatev1.ComputeInstance,
	candidate preparedDiskRegistration,
) error {
	volume, found, err := findDiskRegistrationVolume(ctx, volumesClient, current, candidate)
	if err != nil {
		return fmt.Errorf("volume Create returned no adoptable record and recovery lookup failed: %w", err)
	}
	if !found {
		return fmt.Errorf("volume Create for ComputeInstance '%s' disk '%s' returned no adoptable record",
			current.GetId(), candidate.diskKey)
	}
	return persistDiskRegistrationResolution(ctx, computeInstancesClient,
		current, candidate.diskKey, volume.GetId())
}

func getComputeInstanceForDiskRegistration(
	ctx context.Context,
	client privatev1.ComputeInstancesClient,
	id string,
) (*privatev1.ComputeInstance, error) {
	response, err := client.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: id}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to read ComputeInstance '%s' for disk registration: %w", id, err)
	}
	if response == nil || response.GetObject() == nil {
		return nil, fmt.Errorf("ComputeInstances Get returned no object for '%s'", id)
	}
	return response.GetObject(), nil
}

func hasDiskRegistrationFinalizer(instance *privatev1.ComputeInstance) bool {
	return instance != nil && instance.GetMetadata() != nil &&
		slices.Contains(instance.GetMetadata().GetFinalizers(), finalizers.Controller)
}

func preparedDiskByKey(prepared []preparedDiskRegistration, diskKey string) (preparedDiskRegistration, bool) {
	for _, candidate := range prepared {
		if candidate.diskKey == diskKey {
			return candidate, true
		}
	}
	return preparedDiskRegistration{}, false
}

func persistDiskRegistrationState(
	ctx context.Context,
	client privatev1.ComputeInstancesClient,
	current *privatev1.ComputeInstance,
	diskKey string,
	state privatev1.ComputeInstanceVolumeRegistration_State,
) (bool, error) {
	registrations := cloneDiskRegistrations(current.GetStatus().GetVolumeRegistration())
	registrationByKey, err := diskRegistrationByKey(registrations)
	if err != nil {
		return false, err
	}
	if _, exists := registrationByKey[diskKey]; !exists {
		return false, fmt.Errorf("ComputeInstance '%s' disk '%s' has no persisted registration intent", current.GetId(), diskKey)
	}
	volumesByKey, err := diskVolumesByKey(current.GetStatus().GetVolumes())
	if err != nil {
		return false, err
	}
	if _, hasVolume := volumesByKey[diskKey]; hasVolume {
		return false, nil
	}
	registration := registrationByKey[diskKey]
	if registration.GetState() != privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN {
		return false, nil
	}
	if registration.GetState() == state {
		return false, nil
	}
	registration.SetState(state)
	updated := proto.Clone(current).(*privatev1.ComputeInstance)
	updated.GetStatus().SetVolumeRegistration(registrations)
	response, err := client.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
		Object: updated,
		UpdateMask: &fieldmaskpb.FieldMask{
			Paths: []string{computeInstanceDiskRegistrationUpdateMask},
		},
		Lock: true,
	}.Build())
	if status.Code(err) == codes.Aborted {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if response == nil || response.GetObject() == nil {
		return false, fmt.Errorf("ComputeInstances Update returned no object while updating disk '%s'", diskKey)
	}
	return false, nil
}

func persistDiskRegistrationResolution(
	ctx context.Context,
	client privatev1.ComputeInstancesClient,
	current *privatev1.ComputeInstance,
	diskKey string,
	volumeID string,
) error {
	if volumeID == "" {
		return fmt.Errorf("ComputeInstance '%s' disk '%s' cannot resolve an empty Volume ID", current.GetId(), diskKey)
	}
	registrations := cloneDiskRegistrations(current.GetStatus().GetVolumeRegistration())
	registrationByKey, err := diskRegistrationByKey(registrations)
	if err != nil {
		return err
	}
	registration := registrationByKey[diskKey]
	if registration == nil {
		return fmt.Errorf("ComputeInstance '%s' disk '%s' has no persisted registration intent", current.GetId(), diskKey)
	}
	volumes := cloneComputeInstanceVolumes(current.GetStatus().GetVolumes())
	volumeByKey, err := diskVolumesByKey(volumes)
	if err != nil {
		return err
	}
	if existing, exists := volumeByKey[diskKey]; exists {
		if existing.GetVolumeId() != volumeID {
			return fmt.Errorf("ComputeInstance '%s' disk '%s' already has a different Volume ID", current.GetId(), diskKey)
		}
		if registration.GetState() == privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED {
			return nil
		}
	} else {
		volumes = append(volumes, privatev1.ComputeInstanceVolume_builder{
			DiskKey:  diskKey,
			VolumeId: volumeID,
		}.Build())
	}
	registration.SetState(privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED)
	updated := proto.Clone(current).(*privatev1.ComputeInstance)
	if !updated.HasStatus() {
		updated.SetStatus(&privatev1.ComputeInstanceStatus{})
	}
	updated.GetStatus().SetVolumes(volumes)
	updated.GetStatus().SetVolumeRegistration(registrations)
	response, err := client.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
		Object: updated,
		UpdateMask: &fieldmaskpb.FieldMask{
			Paths: []string{"status.volumes", "status.volume_registration"},
		},
		Lock: true,
	}.Build())
	if status.Code(err) == codes.Aborted {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to persist Volume ID for ComputeInstance '%s' disk '%s': %w", current.GetId(), diskKey, err)
	}
	if response == nil || response.GetObject() == nil {
		return fmt.Errorf("ComputeInstances Update returned no object while resolving disk '%s'", diskKey)
	}
	return nil
}

func isKnownNonCommittedVolumeCreateError(err error) bool {
	switch status.Code(err) {
	case codes.InvalidArgument, codes.FailedPrecondition, codes.NotFound, codes.PermissionDenied, codes.Unauthenticated:
		return true
	default:
		return false
	}
}

func findDiskRegistrationVolume(
	ctx context.Context,
	client privatev1.VolumesClient,
	instance *privatev1.ComputeInstance,
	candidate preparedDiskRegistration,
) (*privatev1.Volume, bool, error) {
	metadata := instance.GetMetadata()
	filter := fmt.Sprintf("this.metadata.name == %s && this.metadata.tenant == %s && this.metadata.project == %s",
		strconv.Quote(candidate.request.GetObject().GetMetadata().GetName()),
		strconv.Quote(metadata.GetTenant()),
		strconv.Quote(metadata.GetProject()))
	response, err := client.List(ctx, privatev1.VolumesListRequest_builder{
		Limit:  proto.Int32(1),
		Filter: proto.String(filter),
	}.Build())
	if err != nil {
		return nil, false, err
	}
	if response == nil {
		return nil, false, fmt.Errorf("volumes List returned no response")
	}
	items := response.GetItems()
	size, total := response.GetSize(), response.GetTotal()
	if size < 0 || total < 0 || size > 1 || int64(size) != int64(len(items)) || size > total ||
		(total > 0 && size == 0) {
		return nil, false, fmt.Errorf("volumes List returned inconsistent pagination metadata")
	}
	// List includes deletion-pending Volumes, which can share this name with an active Volume.
	if total != 1 {
		return nil, false, nil
	}
	volume := items[0]
	if volume == nil || volume.GetId() == "" {
		return nil, false, fmt.Errorf("volumes List returned an item without an ID")
	}
	if !diskRegistrationVolumeMatches(volume, candidate.request) {
		return nil, false, nil
	}
	return volume, true, nil
}

func diskRegistrationVolumeMatches(volume *privatev1.Volume, request *privatev1.VolumesCreateRequest) bool {
	if volume == nil || volume.GetId() == "" || volume.GetMetadata() == nil || volume.GetSpec() == nil {
		return false
	}
	expected := request.GetObject()
	metadata := volume.GetMetadata()
	expectedMetadata := expected.GetMetadata()
	if metadata.GetName() != expectedMetadata.GetName() ||
		metadata.GetTenant() != expectedMetadata.GetTenant() ||
		metadata.GetProject() != expectedMetadata.GetProject() ||
		metadata.GetCreator() != "system" ||
		metadata.HasDeletionTimestamp() {
		return false
	}
	if volume.GetStatus().GetState() == privatev1.VolumeState_VOLUME_STATE_DELETING ||
		volume.GetStatus().GetState() == privatev1.VolumeState_VOLUME_STATE_DELETED {
		return false
	}
	return proto.Equal(volume.GetSpec(), expected.GetSpec())
}

func diskRegistrationsComplete(instance *privatev1.ComputeInstance, prepared []preparedDiskRegistration) (bool, error) {
	registrations, err := diskRegistrationByKey(instance.GetStatus().GetVolumeRegistration())
	if err != nil {
		return false, err
	}
	volumes, err := diskVolumesByKey(instance.GetStatus().GetVolumes())
	if err != nil {
		return false, err
	}
	if err := validateDiskRegistrationKeys(instance, prepared, registrations, volumes); err != nil {
		return false, err
	}
	for _, candidate := range prepared {
		registration := registrations[candidate.diskKey]
		volume := volumes[candidate.diskKey]
		if registration == nil || registration.GetState() != privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED ||
			volume == nil || volume.GetVolumeId() == "" {
			return false, nil
		}
	}
	return true, nil
}

// diskRegistrationProgress describes only durable registration state. In
// particular, a transport error is never included in the public condition.
func diskRegistrationProgress(instance *privatev1.ComputeInstance) (bool, string, string, error) {
	prepared, err := prepareDiskRegistrations(instance)
	if err != nil {
		return false, "", "", err
	}
	registrations, err := diskRegistrationByKey(instance.GetStatus().GetVolumeRegistration())
	if err != nil {
		return false, "", "", err
	}
	volumes, err := diskVolumesByKey(instance.GetStatus().GetVolumes())
	if err != nil {
		return false, "", "", err
	}
	if err := validateDiskRegistrationKeys(instance, prepared, registrations, volumes); err != nil {
		return false, "", "", err
	}
	blockers := make([]string, 0, len(prepared))
	resolved := 0
	failed := false
	for _, candidate := range prepared {
		registration := registrations[candidate.diskKey]
		volume := volumes[candidate.diskKey]
		if volume != nil && volume.GetVolumeId() != "" {
			resolved++
		}
		if registration != nil && registration.GetState() == privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED &&
			volume != nil && volume.GetVolumeId() != "" {
			continue
		}
		blockers = append(blockers, candidate.diskKey)
		if registration != nil && registration.GetState() == privatev1.ComputeInstanceVolumeRegistration_STATE_NON_COMMITTED_FAILURE {
			failed = true
		}
	}
	if len(blockers) == 0 {
		return true, "WaitingForVM", "All disk Volume IDs are registered; waiting for the VM.", nil
	}
	message := fmt.Sprintf("Disk Volume IDs persisted: %d/%d; blocked disks: %s; ",
		resolved, len(prepared), strings.Join(blockers, ", "))
	if failed {
		return false, "VolumeRegistrationFailed", message +
			"Volume creation was rejected without a commit; retrying registration.", nil
	}
	return false, "VolumeRegistrationPending", message +
		"registration or recovery is pending.", nil
}
