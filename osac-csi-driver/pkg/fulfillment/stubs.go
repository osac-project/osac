package fulfillment

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
)

// VolumeStub is an in-memory VolumeClient used during development before the
// real gRPC client is available. Volumes are created in AVAILABLE state
// immediately.
type VolumeStub struct {
	mu      sync.Mutex
	volumes map[string]*VolumeInfo
	nextID  int

	DefaultBackend  string
	DefaultProtocol string
}

func NewVolumeStub(backend, protocol string) *VolumeStub {
	return &VolumeStub{
		volumes:         make(map[string]*VolumeInfo),
		DefaultBackend:  backend,
		DefaultProtocol: protocol,
	}
}

func (s *VolumeStub) CreateVolume(_ context.Context, params CreateVolumeParams) (*VolumeInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	klog.Infof("fulfillment stub: CreateVolume(tenant=%q, tier=%q, pvcRef=%q)", params.Tenant, params.Tier, params.PVCRef)

	for _, v := range s.volumes {
		if v.Name == params.PVCRef {
			return nil, status.Errorf(codes.AlreadyExists, "volume %q already exists with id %s", params.PVCRef, v.ID)
		}
	}

	size := params.SizeBytes
	if size == 0 {
		size = 1 * 1024 * 1024 * 1024
	}

	s.nextID++
	id := fmt.Sprintf("stub-vol-%d", s.nextID)
	vol := &VolumeInfo{
		ID:             id,
		Name:           params.PVCRef,
		State:          VolumeStateAvailable,
		Backend:        s.DefaultBackend,
		VendorVolumeID: id,
		Protocol:       s.DefaultProtocol,
		CapacityBytes:  size,
	}
	s.volumes[id] = vol
	return vol, nil
}

func (s *VolumeStub) GetVolume(_ context.Context, volumeID string) (*VolumeInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	vol, ok := s.volumes[volumeID]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "volume %q not found", volumeID)
	}
	return vol, nil
}

func (s *VolumeStub) ListVolumes(_ context.Context, params ListVolumesParams) ([]*VolumeInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var result []*VolumeInfo
	for _, v := range s.volumes {
		if params.NameFilter == "" || v.Name == params.NameFilter {
			result = append(result, v)
		}
	}
	return result, nil
}

func (s *VolumeStub) DeleteVolume(_ context.Context, volumeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	klog.Infof("fulfillment stub: DeleteVolume(%q)", volumeID)
	delete(s.volumes, volumeID)
	return nil
}

// ControlPlaneStub is a no-op ControlPlaneClient used during development.
type ControlPlaneStub struct{}

func (s *ControlPlaneStub) PublishVolume(_ context.Context, volumeID, nodeID string) error {
	klog.Infof("control plane stub: PublishVolume(volumeID=%q, nodeID=%q)", volumeID, nodeID)
	return nil
}

func (s *ControlPlaneStub) UnpublishVolume(_ context.Context, volumeID, nodeID string) error {
	klog.Infof("control plane stub: UnpublishVolume(volumeID=%q, nodeID=%q)", volumeID, nodeID)
	return nil
}
