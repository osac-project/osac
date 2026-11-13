package utils

import privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"

// For 0.4 only: ticket - OSAC-7670
// IsComputeInstanceVolumeInventory classifies registration-only disks, not authorization.
func IsComputeInstanceVolumeInventory(volume *privatev1.Volume) bool {
	segments := volume.GetSpec().GetTopology().GetSegments()
	return len(segments) == 1 && segments["osac.io/node"] == "osac-volume-registration-pending"
}
