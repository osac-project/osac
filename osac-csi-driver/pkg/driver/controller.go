package driver

import (
	"context"
	"time"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/osac-project/osac/osac-csi-driver/pkg/fulfillment"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
)

const (
	defaultPollInitialInterval = 1 * time.Second
	defaultPollMaxInterval     = 30 * time.Second
)

// ControllerServer implements the CSI Controller service.
// It delegates volume lifecycle to the OSAC fulfillment service and
// publish/unpublish operations to the OSAC control plane.
type ControllerServer struct {
	csi.UnimplementedControllerServer
	volumes      fulfillment.VolumeClient
	controlPlane fulfillment.ControlPlaneClient
	clusterID    string

	pollInitialInterval time.Duration
	pollMaxInterval     time.Duration
}

// NewControllerServer creates a new CSI controller server.
func NewControllerServer(vc fulfillment.VolumeClient, cpc fulfillment.ControlPlaneClient, clusterID string) *ControllerServer {
	return &ControllerServer{
		volumes:             vc,
		controlPlane:        cpc,
		clusterID:           clusterID,
		pollInitialInterval: defaultPollInitialInterval,
		pollMaxInterval:     defaultPollMaxInterval,
	}
}

// CreateVolume creates a volume through the fulfillment service and polls
// until it becomes available.
func (c *ControllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	klog.Infof("CreateVolume called: name=%s", req.GetName())

	if req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume name is required")
	}
	if len(req.GetVolumeCapabilities()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume capabilities are required")
	}

	tier := req.GetParameters()["tier"]
	if tier == "" {
		return nil, status.Error(codes.InvalidArgument, "parameter 'tier' is required")
	}

	tenant := req.GetParameters()["tenant"]
	if tenant == "" {
		tenant = "default"
	}

	var sizeBytes int64
	if cr := req.GetCapacityRange(); cr != nil {
		sizeBytes = cr.GetRequiredBytes()
		if sizeBytes == 0 {
			sizeBytes = cr.GetLimitBytes()
		}
	}

	accessMode := ""
	if len(req.GetVolumeCapabilities()) > 0 {
		if am := req.GetVolumeCapabilities()[0].GetAccessMode(); am != nil {
			accessMode = am.GetMode().String()
		}
	}

	params := fulfillment.CreateVolumeParams{
		Tenant:     tenant,
		Tier:       tier,
		SizeBytes:  sizeBytes,
		AccessMode: accessMode,
		ClusterID:  c.clusterID,
		PVCRef:     req.GetName(),
	}

	vol, err := c.volumes.CreateVolume(ctx, params)
	if err != nil {
		st, ok := status.FromError(err)
		if ok && st.Code() == codes.AlreadyExists {
			klog.Infof("Volume %q already exists, resolving via ListVolumes", req.GetName())
			vol, err = c.resolveExistingVolume(ctx, req.GetName())
			if err != nil {
				return nil, err
			}
			if !capacityCompatible(vol.CapacityBytes, req.GetCapacityRange()) {
				return nil, status.Errorf(codes.AlreadyExists,
					"volume %q already exists with incompatible capacity", req.GetName())
			}
		} else {
			klog.Errorf("Failed to create volume: %v", err)
			return nil, err
		}
	}
	if vol == nil {
		return nil, status.Errorf(codes.Internal,
			"fulfillment service returned no volume for %q", req.GetName())
	}

	if vol.State != fulfillment.VolumeStateAvailable {
		klog.Infof("Volume %q in state %s, polling until AVAILABLE", vol.ID, vol.State)
		vol, err = c.pollVolumeUntilAvailable(ctx, vol.ID)
		if err != nil {
			return nil, err
		}
	}

	klog.Infof("CreateVolume succeeded: volumeId=%s backend=%s", vol.ID, vol.Backend)

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      vol.ID,
			CapacityBytes: vol.CapacityBytes,
			VolumeContext: map[string]string{
				"osac.backend":   vol.Backend,
				"osac.volume-id": vol.VendorVolumeID,
				"osac.protocol":  vol.Protocol,
			},
		},
	}, nil
}

// DeleteVolume deletes a volume through the fulfillment service.
// The operator handles actual vendor-side deletion asynchronously.
func (c *ControllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	klog.Infof("DeleteVolume called: volumeId=%s", req.GetVolumeId())

	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}

	if err := c.volumes.DeleteVolume(ctx, req.GetVolumeId()); err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			klog.Infof("Volume %s already deleted", req.GetVolumeId())
			return &csi.DeleteVolumeResponse{}, nil
		}
		klog.Errorf("Failed to delete volume %s: %v", req.GetVolumeId(), err)
		return nil, err
	}

	klog.Infof("DeleteVolume succeeded: volumeId=%s", req.GetVolumeId())
	return &csi.DeleteVolumeResponse{}, nil
}

// ControllerPublishVolume publishes a volume to a node via the control plane.
func (c *ControllerServer) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	klog.Infof("ControllerPublishVolume called: volumeId=%s nodeId=%s", req.GetVolumeId(), req.GetNodeId())

	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if req.GetNodeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "node ID is required")
	}
	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "volume capability is required")
	}

	if err := c.controlPlane.PublishVolume(ctx, req.GetVolumeId(), req.GetNodeId()); err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
			klog.Infof("Volume %s already published to node %s", req.GetVolumeId(), req.GetNodeId())
			return &csi.ControllerPublishVolumeResponse{}, nil
		}
		klog.Errorf("Failed to publish volume %s to node %s: %v", req.GetVolumeId(), req.GetNodeId(), err)
		return nil, err
	}

	klog.Infof("ControllerPublishVolume succeeded: volumeId=%s nodeId=%s", req.GetVolumeId(), req.GetNodeId())
	return &csi.ControllerPublishVolumeResponse{}, nil
}

// ControllerUnpublishVolume unpublishes a volume from a node via the control plane.
func (c *ControllerServer) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	klog.Infof("ControllerUnpublishVolume called: volumeId=%s nodeId=%s", req.GetVolumeId(), req.GetNodeId())

	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}

	if err := c.controlPlane.UnpublishVolume(ctx, req.GetVolumeId(), req.GetNodeId()); err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			klog.Infof("Volume %s already unpublished from node %s", req.GetVolumeId(), req.GetNodeId())
			return &csi.ControllerUnpublishVolumeResponse{}, nil
		}
		klog.Errorf("Failed to unpublish volume %s from node %s: %v", req.GetVolumeId(), req.GetNodeId(), err)
		return nil, err
	}

	klog.Infof("ControllerUnpublishVolume succeeded: volumeId=%s nodeId=%s", req.GetVolumeId(), req.GetNodeId())
	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

// ValidateVolumeCapabilities confirms volume existence and capabilities.
func (c *ControllerServer) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	klog.Infof("ValidateVolumeCapabilities called: volumeId=%s", req.GetVolumeId())

	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if len(req.GetVolumeCapabilities()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume capabilities are required")
	}

	if _, err := c.volumes.GetVolume(ctx, req.GetVolumeId()); err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return nil, status.Errorf(codes.NotFound, "volume %q not found", req.GetVolumeId())
		}
		return nil, err
	}

	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeCapabilities: req.GetVolumeCapabilities(),
		},
	}, nil
}

// ControllerGetCapabilities returns the capabilities supported by this controller.
func (c *ControllerServer) ControllerGetCapabilities(_ context.Context, _ *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	klog.Infof("ControllerGetCapabilities called")
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

func (c *ControllerServer) resolveExistingVolume(ctx context.Context, name string) (*fulfillment.VolumeInfo, error) {
	volumes, err := c.volumes.ListVolumes(ctx, fulfillment.ListVolumesParams{NameFilter: name})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list volumes: %v", err)
	}
	for _, v := range volumes {
		if v != nil && v.Name == name {
			return v, nil
		}
	}
	return nil, status.Errorf(codes.Internal,
		"volume %q reported as existing but not found via list", name)
}

func capacityCompatible(volBytes int64, cr *csi.CapacityRange) bool {
	if cr == nil {
		return true
	}
	if cr.GetRequiredBytes() > 0 && volBytes < cr.GetRequiredBytes() {
		return false
	}
	if cr.GetLimitBytes() > 0 && volBytes > cr.GetLimitBytes() {
		return false
	}
	return true
}

func (c *ControllerServer) pollVolumeUntilAvailable(ctx context.Context, volumeID string) (*fulfillment.VolumeInfo, error) {
	interval := c.pollInitialInterval
	for {
		vol, err := c.volumes.GetVolume(ctx, volumeID)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to get volume %s: %v", volumeID, err)
		}

		switch vol.State {
		case fulfillment.VolumeStateAvailable:
			return vol, nil
		case fulfillment.VolumeStateError:
			return nil, status.Errorf(codes.Internal, "volume %s entered error state", volumeID)
		case fulfillment.VolumeStateCreating:
			// continue polling
		default:
			return nil, status.Errorf(codes.Internal, "volume %s in unexpected state %s", volumeID, vol.State)
		}

		select {
		case <-ctx.Done():
			return nil, status.Errorf(codes.DeadlineExceeded,
				"timed out waiting for volume %s to become available", volumeID)
		case <-time.After(interval):
		}

		interval = min(interval*2, c.pollMaxInterval)
	}
}
