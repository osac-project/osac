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

	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"k8s.io/apimachinery/pkg/util/validation"
)

const computeInstanceDiskRegistrationUpdateMask = "status.volume_registration"
const diskRegistrationPlaceholderNode = "osac-volume-registration-pending"

type preparedDiskRegistration struct {
	diskKey string
	request *privatev1.VolumesCreateRequest
}

// prepareDiskRegistrations derives every request from the persisted disk fields.
// It validates the complete set before returning any request, so a caller cannot
// dispatch an earlier disk when a later disk is invalid.
func prepareDiskRegistrations(instance *privatev1.ComputeInstance) ([]preparedDiskRegistration, error) {
	if instance == nil {
		return nil, fmt.Errorf("ComputeInstance is required")
	}
	if instance.GetId() == "" {
		return nil, fmt.Errorf("ComputeInstance ID is required for disk registration")
	}
	metadata := instance.GetMetadata()
	if metadata == nil || metadata.GetTenant() == "" {
		return nil, fmt.Errorf("ComputeInstance '%s' must have a tenant for disk registration", instance.GetId())
	}

	type diskInput struct {
		key  string
		disk *privatev1.ComputeInstanceDisk
	}

	inputs := []diskInput{{key: "boot", disk: instance.GetSpec().GetBootDisk()}}
	for index, disk := range instance.GetSpec().GetAdditionalDisks().GetItems() {
		inputs = append(inputs, diskInput{
			key:  "disk-" + strconv.Itoa(index+1),
			disk: disk,
		})
	}

	prepared := make([]preparedDiskRegistration, 0, len(inputs))
	for _, input := range inputs {
		if input.disk == nil {
			return nil, fmt.Errorf("ComputeInstance '%s' disk '%s' is missing", instance.GetId(), input.key)
		}
		tierName := input.disk.GetStorageTier().GetName()
		if tierName == "" {
			return nil, fmt.Errorf("ComputeInstance '%s' disk '%s' has no resolved StorageTier name", instance.GetId(), input.key)
		}
		if input.disk.GetSizeGib() <= 0 {
			return nil, fmt.Errorf("ComputeInstance '%s' disk '%s' must have a positive size_gib", instance.GetId(), input.key)
		}

		name := "ci-" + instance.GetId() + "-" + input.key
		if problems := validation.IsDNS1123Label(name); len(problems) > 0 {
			return nil, fmt.Errorf("ComputeInstance '%s' disk '%s' Volume name %q is invalid: %s",
				instance.GetId(), input.key, name, problems[0])
		}

		volume := privatev1.Volume_builder{
			Metadata: privatev1.Metadata_builder{
				Name:    name,
				Tenant:  metadata.GetTenant(),
				Project: metadata.GetProject(),
			}.Build(),
			Spec: privatev1.VolumeSpec_builder{
				StorageTier: input.disk.GetStorageTier().GetName(),
				SizeGib:     int64(input.disk.GetSizeGib()),
				AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
				// LVMS requires a node in the Volume API before AAP selects placement.
				// This is not the VM/DataVolume node; keep it stable for exact-spec recovery.
				Topology: privatev1.VolumeTopology_builder{
					Segments: map[string]string{"osac.io/node": diskRegistrationPlaceholderNode},
				}.Build(),
			}.Build(),
		}.Build()
		prepared = append(prepared, preparedDiskRegistration{
			diskKey: input.key,
			request: privatev1.VolumesCreateRequest_builder{Object: volume}.Build(),
		})
	}
	return prepared, nil
}

// prepareAndClaimDiskRegistration persists the intent list and then atomically
// changes one eligible disk to UNKNOWN before returning its Create request.
func (t *task) prepareAndClaimDiskRegistration(ctx context.Context) (*privatev1.VolumesCreateRequest, bool, error) {
	if t.r == nil || t.r.computeInstancesClient == nil {
		return nil, false, fmt.Errorf("ComputeInstances client is not configured")
	}
	if t.computeInstance == nil || t.computeInstance.GetId() == "" {
		return nil, false, fmt.Errorf("ComputeInstance is required for disk registration")
	}

	getResponse, err := t.r.computeInstancesClient.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{
		Id: t.computeInstance.GetId(),
	}.Build())
	if err != nil {
		return nil, false, fmt.Errorf("failed to read ComputeInstance '%s' before disk registration: %w", t.computeInstance.GetId(), err)
	}
	if getResponse == nil {
		return nil, false, fmt.Errorf("ComputeInstances Get returned no response for '%s'", t.computeInstance.GetId())
	}
	current := getResponse.GetObject()
	if current == nil {
		return nil, false, fmt.Errorf("ComputeInstance '%s' was not returned before disk registration", t.computeInstance.GetId())
	}
	if !canPrepareDiskRegistration(current) {
		return nil, false, nil
	}

	prepared, err := prepareDiskRegistrations(current)
	if err != nil {
		return nil, false, err
	}

	current, conflict, err := t.persistDiskRegistrationIntents(ctx, current, prepared)
	if err != nil || conflict {
		return nil, false, err
	}

	registrationByKey, err := diskRegistrationByKey(current.GetStatus().GetVolumeRegistration())
	if err != nil {
		return nil, false, err
	}
	volumeByKey, err := diskVolumesByKey(current.GetStatus().GetVolumes())
	if err != nil {
		return nil, false, err
	}

	for _, candidate := range prepared {
		if volume, exists := volumeByKey[candidate.diskKey]; exists {
			if volume.GetVolumeId() == "" {
				return nil, false, fmt.Errorf("ComputeInstance '%s' disk '%s' has an empty persisted Volume ID",
					current.GetId(), candidate.diskKey)
			}
			continue
		}

		registration := registrationByKey[candidate.diskKey]
		if registration == nil {
			return nil, false, fmt.Errorf("ComputeInstance '%s' disk '%s' has no persisted registration intent",
				current.GetId(), candidate.diskKey)
		}
		if registration.GetState() != privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED &&
			registration.GetState() != privatev1.ComputeInstanceVolumeRegistration_STATE_NON_COMMITTED_FAILURE {
			continue
		}
		if !canPrepareDiskRegistration(current) {
			return nil, false, nil
		}

		claimed, err := claimDiskRegistration(ctx, t.r.computeInstancesClient, current, candidate)
		if err != nil {
			return nil, false, fmt.Errorf("failed to commit attempted marker for ComputeInstance '%s' disk '%s': %w",
				current.GetId(), candidate.diskKey, err)
		}
		if claimed {
			return candidate.request, true, nil
		}
		return nil, false, nil
	}

	return nil, false, nil
}

func canPrepareDiskRegistration(instance *privatev1.ComputeInstance) bool {
	metadata := instance.GetMetadata()
	return metadata != nil &&
		!metadata.HasDeletionTimestamp() &&
		slices.Contains(metadata.GetFinalizers(), finalizers.Controller)
}

func (t *task) persistDiskRegistrationIntents(
	ctx context.Context,
	current *privatev1.ComputeInstance,
	prepared []preparedDiskRegistration,
) (*privatev1.ComputeInstance, bool, error) {
	registrations := cloneDiskRegistrations(current.GetStatus().GetVolumeRegistration())
	registrationByKey, err := diskRegistrationByKey(registrations)
	if err != nil {
		return nil, false, err
	}
	volumeByKey, err := diskVolumesByKey(current.GetStatus().GetVolumes())
	if err != nil {
		return nil, false, err
	}
	if err := validateDiskRegistrationKeys(current, prepared, registrationByKey, volumeByKey); err != nil {
		return nil, false, err
	}
	changed := false
	for _, disk := range prepared {
		registration := registrationByKey[disk.diskKey]
		if volume, exists := volumeByKey[disk.diskKey]; exists && volume.GetVolumeId() == "" {
			return nil, false, fmt.Errorf("ComputeInstance '%s' disk '%s' has an empty persisted Volume ID",
				current.GetId(), disk.diskKey)
		}
		if registration != nil {
			if _, hasVolume := volumeByKey[disk.diskKey]; hasVolume && registration.GetState() != privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED {
				registration.SetState(privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED)
				changed = true
			}
			if _, hasVolume := volumeByKey[disk.diskKey]; !hasVolume && registration.GetState() == privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED {
				return nil, false, fmt.Errorf("ComputeInstance '%s' disk '%s' is marked resolved without a persisted Volume ID",
					current.GetId(), disk.diskKey)
			}
			continue
		}

		state := privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED
		if _, hasVolume := volumeByKey[disk.diskKey]; hasVolume {
			state = privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED
		}
		registration = privatev1.ComputeInstanceVolumeRegistration_builder{
			DiskKey: disk.diskKey,
			State:   state,
		}.Build()
		registrations = append(registrations, registration)
		registrationByKey[disk.diskKey] = registration
		changed = true
	}
	if !changed {
		return current, false, nil
	}

	updated, err := t.updateDiskRegistrationList(ctx, current, registrations)
	if status.Code(err) == codes.Aborted {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("failed to persist ComputeInstance '%s' disk registration intents: %w", current.GetId(), err)
	}
	if updated == nil {
		return nil, false, fmt.Errorf("ComputeInstances update returned no object while persisting disk registration intents")
	}
	return updated, false, nil
}

func (t *task) updateDiskRegistrationList(
	ctx context.Context,
	current *privatev1.ComputeInstance,
	registrations []*privatev1.ComputeInstanceVolumeRegistration,
) (*privatev1.ComputeInstance, error) {
	updated := proto.Clone(current).(*privatev1.ComputeInstance)
	if !updated.HasStatus() {
		updated.SetStatus(&privatev1.ComputeInstanceStatus{})
	}
	updated.GetStatus().SetVolumeRegistration(registrations)
	response, err := t.r.computeInstancesClient.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
		Object: updated,
		UpdateMask: &fieldmaskpb.FieldMask{
			Paths: []string{computeInstanceDiskRegistrationUpdateMask},
		},
		Lock: true,
	}.Build())
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, fmt.Errorf("ComputeInstances Update returned no response")
	}
	return response.GetObject(), nil
}

func validateDiskRegistrationKeys(
	instance *privatev1.ComputeInstance,
	prepared []preparedDiskRegistration,
	registrations map[string]*privatev1.ComputeInstanceVolumeRegistration,
	volumes map[string]*privatev1.ComputeInstanceVolume,
) error {
	required := make(map[string]bool, len(prepared))
	for _, candidate := range prepared {
		required[candidate.diskKey] = true
	}
	for diskKey := range registrations {
		if !required[diskKey] {
			return fmt.Errorf("ComputeInstance '%s' has registration state for unknown disk '%s'", instance.GetId(), diskKey)
		}
	}
	for diskKey := range volumes {
		if !required[diskKey] {
			return fmt.Errorf("ComputeInstance '%s' has a Volume ID for unknown disk '%s'", instance.GetId(), diskKey)
		}
	}
	return nil
}

func claimDiskRegistration(
	ctx context.Context,
	client privatev1.ComputeInstancesClient,
	current *privatev1.ComputeInstance,
	candidate preparedDiskRegistration,
) (bool, error) {
	if !canPrepareDiskRegistration(current) {
		return false, nil
	}
	registrations := cloneDiskRegistrations(current.GetStatus().GetVolumeRegistration())
	registrationByKey, err := diskRegistrationByKey(registrations)
	if err != nil {
		return false, err
	}
	volumeByKey, err := diskVolumesByKey(current.GetStatus().GetVolumes())
	if err != nil {
		return false, err
	}
	if _, exists := volumeByKey[candidate.diskKey]; exists {
		return false, nil
	}
	registration := registrationByKey[candidate.diskKey]
	if registration == nil || (registration.GetState() != privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED &&
		registration.GetState() != privatev1.ComputeInstanceVolumeRegistration_STATE_NON_COMMITTED_FAILURE) {
		return false, nil
	}
	registration.SetState(privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN)
	updated := proto.Clone(current).(*privatev1.ComputeInstance)
	updated.GetStatus().SetVolumeRegistration(registrations)
	_, reason, message, err := diskRegistrationProgress(updated)
	if err != nil {
		return false, err
	}
	(&task{computeInstance: updated}).updateCondition(
		privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_PROVISIONED,
		privatev1.ConditionStatus_CONDITION_STATUS_FALSE, reason, message)
	response, err := client.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
		Object: updated,
		UpdateMask: &fieldmaskpb.FieldMask{
			Paths: []string{computeInstanceDiskRegistrationUpdateMask, "status.conditions"},
		},
		Lock: true,
	}.Build())
	if status.Code(err) == codes.Aborted {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if response == nil || response.GetObject() == nil {
		return false, fmt.Errorf("ComputeInstances Update returned no object while claiming disk '%s'", candidate.diskKey)
	}
	return true, nil
}

func diskRegistrationByKey(
	registrations []*privatev1.ComputeInstanceVolumeRegistration,
) (map[string]*privatev1.ComputeInstanceVolumeRegistration, error) {
	byKey := make(map[string]*privatev1.ComputeInstanceVolumeRegistration, len(registrations))
	for _, registration := range registrations {
		if registration == nil || registration.GetDiskKey() == "" {
			return nil, fmt.Errorf("ComputeInstance has an invalid disk registration entry")
		}
		if _, exists := byKey[registration.GetDiskKey()]; exists {
			return nil, fmt.Errorf("ComputeInstance has duplicate disk registration for '%s'", registration.GetDiskKey())
		}
		switch registration.GetState() {
		case privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED,
			privatev1.ComputeInstanceVolumeRegistration_STATE_NON_COMMITTED_FAILURE,
			privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN,
			privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED:
		default:
			return nil, fmt.Errorf("ComputeInstance disk '%s' has unsupported registration state %d",
				registration.GetDiskKey(), registration.GetState())
		}
		byKey[registration.GetDiskKey()] = registration
	}
	return byKey, nil
}

func diskVolumesByKey(volumes []*privatev1.ComputeInstanceVolume) (map[string]*privatev1.ComputeInstanceVolume, error) {
	byKey := make(map[string]*privatev1.ComputeInstanceVolume, len(volumes))
	for _, volume := range volumes {
		if volume == nil || volume.GetDiskKey() == "" {
			return nil, fmt.Errorf("ComputeInstance has an invalid disk Volume entry")
		}
		if _, exists := byKey[volume.GetDiskKey()]; exists {
			return nil, fmt.Errorf("ComputeInstance has duplicate Volume entry for disk '%s'", volume.GetDiskKey())
		}
		byKey[volume.GetDiskKey()] = volume
	}
	return byKey, nil
}

func cloneDiskRegistrations(
	registrations []*privatev1.ComputeInstanceVolumeRegistration,
) []*privatev1.ComputeInstanceVolumeRegistration {
	if registrations == nil {
		return nil
	}
	cloned := make([]*privatev1.ComputeInstanceVolumeRegistration, len(registrations))
	for index, registration := range registrations {
		if registration != nil {
			cloned[index] = proto.Clone(registration).(*privatev1.ComputeInstanceVolumeRegistration)
		}
	}
	return cloned
}

func cloneComputeInstanceVolumes(volumes []*privatev1.ComputeInstanceVolume) []*privatev1.ComputeInstanceVolume {
	if volumes == nil {
		return nil
	}
	cloned := make([]*privatev1.ComputeInstanceVolume, len(volumes))
	for index, volume := range volumes {
		if volume != nil {
			cloned[index] = proto.Clone(volume).(*privatev1.ComputeInstanceVolume)
		}
	}
	return cloned
}
