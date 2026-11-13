package computeinstance

import (
	"context"
	"fmt"
	"strings"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const legacyDiskRegistrationNode = "osac-volume-registration-pending"

func IsLegacyComputeInstanceVolumeInventory(
	ctx context.Context,
	getParent func(context.Context, string) (*privatev1.ComputeInstance, error),
	volume *privatev1.Volume,
) (bool, error) {
	parentID, diskKey := legacyDiskRegistrationLocation(volume)
	if parentID == "" {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if getParent == nil {
		return false, fmt.Errorf("ComputeInstances lookup is required for legacy disk inventory proof")
	}
	parent, err := getParent(ctx, parentID)
	if status.Code(err) == codes.NotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if parent == nil {
		return false, fmt.Errorf("ComputeInstances lookup returned no parent for legacy disk inventory")
	}
	if parent.GetId() != parentID {
		return false, fmt.Errorf("ComputeInstances Get returned a different parent for legacy disk inventory")
	}
	if volume.GetMetadata().GetTenant() != parent.GetMetadata().GetTenant() ||
		volume.GetMetadata().GetProject() != parent.GetMetadata().GetProject() {
		return false, nil
	}
	prepared, err := prepareDiskRegistrations(parent)
	if err != nil {
		return false, err
	}
	candidate, found := preparedDiskByKey(prepared, diskKey)
	if !found || !diskRegistrationRequestMatches(volume, candidate.request) {
		return false, nil
	}
	registrations, err := diskRegistrationByKey(parent.GetStatus().GetVolumeRegistration())
	if err != nil {
		return false, err
	}
	volumes, err := diskVolumesByKey(parent.GetStatus().GetVolumes())
	if err != nil {
		return false, err
	}
	if err := validateDiskRegistrationKeys(parent, prepared, registrations, volumes); err != nil {
		return false, err
	}
	mapping := volumes[diskKey]
	if mapping != nil && mapping.GetVolumeId() == "" {
		return false, fmt.Errorf("legacy disk inventory has an empty persisted Volume ID")
	}
	if mapping != nil && volume.GetId() == "" {
		return false, nil
	}
	if mapping != nil && mapping.GetVolumeId() != volume.GetId() {
		return false, fmt.Errorf("legacy disk inventory conflicts with persisted Volume ID")
	}
	registration := registrations[diskKey]
	if registration == nil {
		if mapping != nil {
			return false, fmt.Errorf("mapped legacy disk inventory has no registration intent")
		}
		return false, nil
	}
	if mapping != nil && registration.GetState() == privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED {
		return true, nil
	}
	if mapping == nil && registration.GetState() == privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN &&
		hasDiskRegistrationFinalizer(parent) {
		return true, nil
	}
	return false, fmt.Errorf("legacy disk inventory has inconsistent persisted registration state")
}

func legacyDiskRegistrationLocation(volume *privatev1.Volume) (string, string) {
	segments := volume.GetSpec().GetTopology().GetSegments()
	if len(segments) != 1 || segments["osac.io/node"] != legacyDiskRegistrationNode ||
		volume.GetMetadata().GetCreator() != "system" {
		return "", ""
	}
	name, prefixed := strings.CutPrefix(volume.GetMetadata().GetName(), "ci-")
	if !prefixed {
		return "", ""
	}
	if parentID, boot := strings.CutSuffix(name, "-boot"); boot {
		return parentID, "boot"
	}
	separator := strings.LastIndex(name, "-disk-")
	if separator < 0 {
		return "", ""
	}
	return name[:separator], name[separator+1:]
}
