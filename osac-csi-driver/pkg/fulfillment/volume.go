package fulfillment

import "context"

// VolumeState represents the lifecycle state of a volume managed by the
// fulfillment service.
type VolumeState string

const (
	VolumeStateCreating  VolumeState = "CREATING"
	VolumeStateAvailable VolumeState = "AVAILABLE"
	VolumeStateDeleting  VolumeState = "DELETING"
	VolumeStateError     VolumeState = "ERROR"
)

// VolumeInfo describes a volume managed by the fulfillment service.
type VolumeInfo struct {
	ID             string
	Name           string
	State          VolumeState
	Backend        string
	VendorVolumeID string
	Protocol       string
	CapacityBytes  int64
}

// CreateVolumeParams are the parameters for creating a volume through the
// fulfillment service.
type CreateVolumeParams struct {
	Tenant     string
	Tier       string
	SizeBytes  int64
	AccessMode string
	ClusterID  string
	PVCRef     string
}

// ListVolumesParams are the filter parameters for listing volumes.
type ListVolumesParams struct {
	NameFilter string
}

// VolumeClient is the interface for managing volumes through the OSAC
// fulfillment service. CreateVolume returns codes.AlreadyExists when the
// volume already exists.
type VolumeClient interface {
	CreateVolume(ctx context.Context, params CreateVolumeParams) (*VolumeInfo, error)
	GetVolume(ctx context.Context, volumeID string) (*VolumeInfo, error)
	ListVolumes(ctx context.Context, params ListVolumesParams) ([]*VolumeInfo, error)
	DeleteVolume(ctx context.Context, volumeID string) error
}
