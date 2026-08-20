package sanity_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type fakeVendor struct {
	csi.UnimplementedIdentityServer
	csi.UnimplementedControllerServer
	csi.UnimplementedNodeServer

	nodeID string

	mu            sync.Mutex
	volumes       map[string]*csi.Volume
	volumesByName map[string]string
	nextID        atomic.Int64
}

func newFakeVendor(nodeID string) *fakeVendor {
	return &fakeVendor{
		nodeID:        nodeID,
		volumes:       make(map[string]*csi.Volume),
		volumesByName: make(map[string]string),
	}
}

func startFakeVendor(
	socketPath string, nodeID string,
) (*grpc.Server, *fakeVendor, error) {
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("removing old socket: %w", err)
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, nil, fmt.Errorf("listening on %s: %w", socketPath, err)
	}

	vendor := newFakeVendor(nodeID)
	srv := grpc.NewServer()
	csi.RegisterIdentityServer(srv, vendor)
	csi.RegisterControllerServer(srv, vendor)
	csi.RegisterNodeServer(srv, vendor)

	go func() {
		_ = srv.Serve(listener)
	}()

	return srv, vendor, nil
}

// --- Identity ---

func (f *fakeVendor) GetPluginInfo(
	_ context.Context, _ *csi.GetPluginInfoRequest,
) (*csi.GetPluginInfoResponse, error) {
	return &csi.GetPluginInfoResponse{
		Name:          "fake.csi.vendor",
		VendorVersion: "0.0.1",
	}, nil
}

func (f *fakeVendor) GetPluginCapabilities(
	_ context.Context, _ *csi.GetPluginCapabilitiesRequest,
) (*csi.GetPluginCapabilitiesResponse, error) {
	return &csi.GetPluginCapabilitiesResponse{
		Capabilities: []*csi.PluginCapability{
			{
				Type: &csi.PluginCapability_Service_{
					Service: &csi.PluginCapability_Service{
						Type: csi.PluginCapability_Service_CONTROLLER_SERVICE,
					},
				},
			},
		},
	}, nil
}

func (f *fakeVendor) Probe(
	_ context.Context, _ *csi.ProbeRequest,
) (*csi.ProbeResponse, error) {
	return &csi.ProbeResponse{Ready: wrapperspb.Bool(true)}, nil
}

// --- Controller ---

func (f *fakeVendor) CreateVolume(
	_ context.Context, req *csi.CreateVolumeRequest,
) (*csi.CreateVolumeResponse, error) {
	if req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume name is required")
	}
	if len(req.GetVolumeCapabilities()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume capabilities are required")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	var capacity int64 = 1 * 1024 * 1024 * 1024
	if cr := req.GetCapacityRange(); cr != nil && cr.GetRequiredBytes() > 0 {
		capacity = cr.GetRequiredBytes()
	}

	if existingID, ok := f.volumesByName[req.GetName()]; ok {
		existing := f.volumes[existingID]
		if existing.CapacityBytes != capacity {
			return nil, status.Errorf(codes.AlreadyExists,
				"volume %q already exists with different capacity",
				req.GetName())
		}
		return &csi.CreateVolumeResponse{Volume: existing}, nil
	}

	id := fmt.Sprintf("fake-vol-%d", f.nextID.Add(1))

	vol := &csi.Volume{
		VolumeId:      id,
		CapacityBytes: capacity,
		VolumeContext: req.GetParameters(),
	}

	f.volumes[id] = vol
	f.volumesByName[req.GetName()] = id
	return &csi.CreateVolumeResponse{Volume: vol}, nil
}

func (f *fakeVendor) DeleteVolume(
	_ context.Context, req *csi.DeleteVolumeRequest,
) (*csi.DeleteVolumeResponse, error) {
	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if vol, ok := f.volumes[req.GetVolumeId()]; ok {
		for name, vid := range f.volumesByName {
			if vid == vol.VolumeId {
				delete(f.volumesByName, name)
				break
			}
		}
		delete(f.volumes, req.GetVolumeId())
	}

	return &csi.DeleteVolumeResponse{}, nil
}

func (f *fakeVendor) ValidateVolumeCapabilities(
	_ context.Context, req *csi.ValidateVolumeCapabilitiesRequest,
) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if len(req.GetVolumeCapabilities()) == 0 {
		return nil, status.Error(codes.InvalidArgument,
			"volume capabilities are required")
	}

	f.mu.Lock()
	_, exists := f.volumes[req.GetVolumeId()]
	f.mu.Unlock()

	if !exists {
		return nil, status.Errorf(codes.NotFound,
			"volume %q not found", req.GetVolumeId())
	}

	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeCapabilities: req.GetVolumeCapabilities(),
		},
	}, nil
}

func (f *fakeVendor) ControllerPublishVolume(
	_ context.Context, req *csi.ControllerPublishVolumeRequest,
) (*csi.ControllerPublishVolumeResponse, error) {
	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if req.GetNodeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "node ID is required")
	}

	// The OSAC meta-driver creates volumes through the fulfillment service, not
	// through this vendor, so the vendor-side volume id it forwards on publish is
	// one this double never saw via CreateVolume. Accept any volume/node id — the
	// existence semantics are exercised against the real vendor, not here.
	return &csi.ControllerPublishVolumeResponse{
		PublishContext: map[string]string{
			"fake.device": "/dev/fake0",
		},
	}, nil
}

func (f *fakeVendor) ControllerUnpublishVolume(
	_ context.Context, req *csi.ControllerUnpublishVolumeRequest,
) (*csi.ControllerUnpublishVolumeResponse, error) {
	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

func (f *fakeVendor) ControllerGetCapabilities(
	_ context.Context, _ *csi.ControllerGetCapabilitiesRequest,
) (*csi.ControllerGetCapabilitiesResponse, error) {
	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: []*csi.ControllerServiceCapability{
			{
				Type: &csi.ControllerServiceCapability_Rpc{
					Rpc: &csi.ControllerServiceCapability_RPC{
						Type: csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
					},
				},
			},
			{
				Type: &csi.ControllerServiceCapability_Rpc{
					Rpc: &csi.ControllerServiceCapability_RPC{
						Type: csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
					},
				},
			},
		},
	}, nil
}

// --- Node ---

func (f *fakeVendor) NodeStageVolume(
	_ context.Context, req *csi.NodeStageVolumeRequest,
) (*csi.NodeStageVolumeResponse, error) {
	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if req.GetStagingTargetPath() == "" {
		return nil, status.Error(codes.InvalidArgument,
			"staging target path is required")
	}
	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument,
			"volume capability is required")
	}
	return &csi.NodeStageVolumeResponse{}, nil
}

func (f *fakeVendor) NodeUnstageVolume(
	_ context.Context, req *csi.NodeUnstageVolumeRequest,
) (*csi.NodeUnstageVolumeResponse, error) {
	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if req.GetStagingTargetPath() == "" {
		return nil, status.Error(codes.InvalidArgument,
			"staging target path is required")
	}
	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (f *fakeVendor) NodePublishVolume(
	_ context.Context, req *csi.NodePublishVolumeRequest,
) (*csi.NodePublishVolumeResponse, error) {
	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if req.GetTargetPath() == "" {
		return nil, status.Error(codes.InvalidArgument,
			"target path is required")
	}
	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument,
			"volume capability is required")
	}
	if err := os.MkdirAll(req.GetTargetPath(), 0750); err != nil {
		return nil, status.Errorf(codes.Internal,
			"creating target path: %v", err)
	}
	return &csi.NodePublishVolumeResponse{}, nil
}

func (f *fakeVendor) NodeUnpublishVolume(
	_ context.Context, req *csi.NodeUnpublishVolumeRequest,
) (*csi.NodeUnpublishVolumeResponse, error) {
	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if req.GetTargetPath() == "" {
		return nil, status.Error(codes.InvalidArgument,
			"target path is required")
	}
	if err := os.RemoveAll(req.GetTargetPath()); err != nil && !os.IsNotExist(err) {
		return nil, status.Errorf(codes.Internal,
			"removing target path: %v", err)
	}
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (f *fakeVendor) NodeGetInfo(
	_ context.Context, _ *csi.NodeGetInfoRequest,
) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{NodeId: f.nodeID}, nil
}

func (f *fakeVendor) NodeGetCapabilities(
	_ context.Context, _ *csi.NodeGetCapabilitiesRequest,
) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: []*csi.NodeServiceCapability{
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
					},
				},
			},
		},
	}, nil
}
