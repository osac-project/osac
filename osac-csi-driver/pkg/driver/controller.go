package driver

import (
	"context"
	"time"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/osac-project/osac/osac-csi-driver/pkg/fulfillment"
	"github.com/osac-project/osac/osac-csi-driver/pkg/proxy"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
)

const (
	defaultPollInitialInterval = 1 * time.Second
	defaultPollMaxInterval     = 30 * time.Second

	// noAttachEndpoint is the sentinel vendor-controller endpoint for backends
	// that need no controller-side attach/detach — node-local storage such as
	// lvms/topolvm, whose CSIDriver sets attachRequired=false and exposes no
	// network-reachable CSI controller. A backend mapped to this value makes
	// ControllerPublish/UnpublishVolume a no-op instead of dialing a vendor.
	noAttachEndpoint = "none"
)

// ControllerServer implements the CSI Controller service.
// It delegates volume lifecycle to the OSAC fulfillment service and proxies
// publish/unpublish operations to vendor CSI controllers.
type ControllerServer struct {
	csi.UnimplementedControllerServer
	volumes  fulfillment.VolumeClient
	proxyMgr *proxy.Manager
	// vendorControllers maps an "osac.backend" name to the gRPC endpoint of that
	// vendor's CSI controller, used to proxy publish/unpublish operations.
	vendorControllers map[string]string
	clusterID         string

	pollInitialInterval time.Duration
	pollMaxInterval     time.Duration
}

// NewControllerServer creates a new CSI controller server. vendorControllers maps
// an "osac.backend" name to the gRPC endpoint of that vendor's CSI controller.
func NewControllerServer(vc fulfillment.VolumeClient, proxyMgr *proxy.Manager, vendorControllers map[string]string, clusterID string) *ControllerServer {
	return &ControllerServer{
		volumes:             vc,
		proxyMgr:            proxyMgr,
		vendorControllers:   vendorControllers,
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

// ControllerPublishVolume attaches a volume to a node by proxying to the vendor
// CSI controller for the volume's backend.
//
// OSAC-4187 (0.2, temporary): the controller talks to the vendor CSI controller
// directly, routed by "osac.backend", instead of going through a fulfillment
// control-plane attach API. This is a stop-gap for milestone 0.2 and is expected
// to be reworked in 0.3.
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

	backend, vendorVolumeID, err := c.resolvePublishTarget(ctx, req)
	if err != nil {
		return nil, err
	}

	vendorClient, err := c.vendorControllerClient(backend)
	if err != nil {
		return nil, err
	}
	if vendorClient == nil {
		klog.Infof("Backend %q needs no controller attach (node-local), ControllerPublishVolume is a no-op: volumeId=%s", backend, req.GetVolumeId())
		return &csi.ControllerPublishVolumeResponse{}, nil
	}

	// Translate the fulfillment volume id to the vendor-side volume id and
	// forward the remaining fields (including secrets) unchanged.
	vendorReq := &csi.ControllerPublishVolumeRequest{
		VolumeId:         vendorVolumeID,
		NodeId:           req.GetNodeId(),
		VolumeCapability: req.GetVolumeCapability(),
		Readonly:         req.GetReadonly(),
		Secrets:          req.GetSecrets(),
		VolumeContext:    req.GetVolumeContext(),
	}

	resp, err := vendorClient.ControllerPublishVolume(ctx, vendorReq)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
			klog.Infof("Volume %s already published to node %s", req.GetVolumeId(), req.GetNodeId())
			return &csi.ControllerPublishVolumeResponse{}, nil
		}
		if isUnimplemented(err) {
			klog.Infof("Vendor does not implement ControllerPublishVolume, treating as no-op: volumeId=%s", req.GetVolumeId())
			return &csi.ControllerPublishVolumeResponse{}, nil
		}
		klog.Errorf("Vendor ControllerPublishVolume failed for volume %s (vendor id %s): %v", req.GetVolumeId(), vendorVolumeID, err)
		return nil, err
	}

	klog.Infof("ControllerPublishVolume succeeded: volumeId=%s nodeId=%s", req.GetVolumeId(), req.GetNodeId())
	return resp, nil
}

// ControllerUnpublishVolume detaches a volume from a node by proxying to the
// vendor CSI controller for the volume's backend.
//
// OSAC-4187 (0.2, temporary): see ControllerPublishVolume. The unpublish request
// carries no volume context, so the backend and vendor-side volume id are
// resolved from the fulfillment service.
func (c *ControllerServer) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	klog.Infof("ControllerUnpublishVolume called: volumeId=%s nodeId=%s", req.GetVolumeId(), req.GetNodeId())

	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}

	vol, err := c.volumes.GetVolume(ctx, req.GetVolumeId())
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			klog.Infof("Volume %s not found, treating unpublish as no-op", req.GetVolumeId())
			return &csi.ControllerUnpublishVolumeResponse{}, nil
		}
		return nil, err
	}

	vendorClient, err := c.vendorControllerClient(vol.Backend)
	if err != nil {
		return nil, err
	}
	if vendorClient == nil {
		klog.Infof("Backend %q needs no controller attach (node-local), ControllerUnpublishVolume is a no-op: volumeId=%s", vol.Backend, req.GetVolumeId())
		return &csi.ControllerUnpublishVolumeResponse{}, nil
	}

	vendorReq := &csi.ControllerUnpublishVolumeRequest{
		VolumeId: vol.VendorVolumeID,
		NodeId:   req.GetNodeId(),
		Secrets:  req.GetSecrets(),
	}

	if _, err := vendorClient.ControllerUnpublishVolume(ctx, vendorReq); err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			klog.Infof("Volume %s already unpublished from node %s", req.GetVolumeId(), req.GetNodeId())
			return &csi.ControllerUnpublishVolumeResponse{}, nil
		}
		if isUnimplemented(err) {
			klog.Infof("Vendor does not implement ControllerUnpublishVolume, treating as no-op: volumeId=%s", req.GetVolumeId())
			return &csi.ControllerUnpublishVolumeResponse{}, nil
		}
		klog.Errorf("Vendor ControllerUnpublishVolume failed for volume %s (vendor id %s): %v", req.GetVolumeId(), vol.VendorVolumeID, err)
		return nil, err
	}

	klog.Infof("ControllerUnpublishVolume succeeded: volumeId=%s nodeId=%s", req.GetVolumeId(), req.GetNodeId())
	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

// resolvePublishTarget determines the backend and vendor-side volume id for a
// publish request. It prefers the volume context (present on publish requests)
// and falls back to the fulfillment service for any missing value.
func (c *ControllerServer) resolvePublishTarget(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (backend, vendorVolumeID string, err error) {
	vctx := req.GetVolumeContext()
	backend = vctx["osac.backend"]
	vendorVolumeID = vctx["osac.volume-id"]

	if backend != "" && vendorVolumeID != "" {
		return backend, vendorVolumeID, nil
	}

	vol, err := c.volumes.GetVolume(ctx, req.GetVolumeId())
	if err != nil {
		return "", "", err
	}
	if backend == "" {
		backend = vol.Backend
	}
	if vendorVolumeID == "" {
		vendorVolumeID = vol.VendorVolumeID
	}
	return backend, vendorVolumeID, nil
}

// vendorControllerClient resolves the vendor CSI controller endpoint for a
// backend and returns a client connected to it. It returns a nil client (and nil
// error) when the backend is configured with the noAttachEndpoint sentinel,
// signalling that the caller should treat attach/detach as a no-op.
func (c *ControllerServer) vendorControllerClient(backend string) (csi.ControllerClient, error) {
	endpoint, err := c.resolveVendorController(backend)
	if err != nil {
		return nil, err
	}
	if endpoint == noAttachEndpoint {
		return nil, nil
	}
	conn, err := c.proxyMgr.GetConnection(endpoint)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable,
			"failed to connect to vendor CSI controller for backend %q: %v", backend, err)
	}
	return csi.NewControllerClient(conn), nil
}

func (c *ControllerServer) resolveVendorController(backend string) (string, error) {
	if backend == "" {
		return "", status.Error(codes.InvalidArgument,
			"cannot resolve vendor controller: backend is empty")
	}
	endpoint, ok := c.vendorControllers[backend]
	if !ok {
		return "", status.Errorf(codes.NotFound,
			"no vendor controller configured for backend %q", backend)
	}
	return endpoint, nil
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
