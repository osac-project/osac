package driver

import (
	"context"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/osac-project/osac/osac-csi-driver/pkg/fulfillment"
	"github.com/osac-project/osac/osac-csi-driver/pkg/proxy"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
)

// ControllerServer implements the CSI Controller service.
// It acts as a meta-driver, resolving storage tiers via the OSAC fulfillment
// service and proxying CSI calls to the appropriate vendor CSI driver.
type ControllerServer struct {
	csi.UnimplementedControllerServer
	fulfillment   fulfillment.Client
	proxyMgr      *proxy.Manager
	vendorSockets map[string]string
}

// NewControllerServer creates a new CSI controller server.
func NewControllerServer(fc fulfillment.Client, proxyMgr *proxy.Manager, vendorSockets map[string]string) *ControllerServer {
	return &ControllerServer{
		fulfillment:   fc,
		proxyMgr:      proxyMgr,
		vendorSockets: vendorSockets,
	}
}

// CreateVolume resolves the storage tier via the fulfillment service, then
// proxies the CreateVolume call to the appropriate vendor CSI driver.
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

	resolved, err := c.fulfillment.Resolve(ctx, tenant, tier)
	if err != nil {
		klog.Errorf("Failed to resolve tier %q: %v", tier, err)
		return nil, status.Errorf(codes.Internal, "failed to resolve storage tier %q: %v", tier, err)
	}
	klog.Infof("Resolved tier %q to backend %q at endpoint %q (protocol: %s)", tier, resolved.Backend, resolved.Endpoint, resolved.Protocol)

	vendorConn, err := c.proxyMgr.GetConnection(resolved.Endpoint)
	if err != nil {
		klog.Errorf("Failed to connect to vendor at %s: %v", resolved.Endpoint, err)
		return nil, status.Errorf(codes.Unavailable, "failed to connect to vendor CSI driver: %v", err)
	}

	vendorClient := csi.NewControllerClient(vendorConn)
	resp, err := vendorClient.CreateVolume(ctx, req)
	if err != nil {
		klog.Errorf("Vendor CreateVolume failed: %v", err)
		return nil, err
	}

	if resp.GetVolume() != nil {
		if resp.Volume.VolumeContext == nil {
			resp.Volume.VolumeContext = make(map[string]string)
		}
		resp.Volume.VolumeContext["osac.backend"] = resolved.Backend
		resp.Volume.VolumeContext["osac.volume-id"] = resp.Volume.VolumeId
		resp.Volume.VolumeContext["osac.protocol"] = resolved.Protocol
		klog.Infof("CreateVolume succeeded: volumeId=%s backend=%s", resp.Volume.VolumeId, resolved.Backend)
	}

	return resp, nil
}

// DeleteVolume proxies the DeleteVolume call to the vendor CSI driver.
// The backend is resolved from the volume's secrets map (key "osac.backend"),
// which is populated by the external-provisioner from the StorageClass parameters.
func (c *ControllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	klog.Infof("DeleteVolume called: volumeId=%s", req.GetVolumeId())

	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}

	socketPath, err := c.resolveVendorSocketFromSecrets(req.GetSecrets())
	if err != nil {
		return nil, err
	}

	vendorConn, err := c.proxyMgr.GetConnection(socketPath)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable,
			"failed to connect to vendor CSI driver: %v", err)
	}

	vendorClient := csi.NewControllerClient(vendorConn)
	resp, err := vendorClient.DeleteVolume(ctx, req)
	if err != nil {
		klog.Errorf("Vendor DeleteVolume failed: %v", err)
		return nil, err
	}

	klog.Infof("DeleteVolume succeeded: volumeId=%s", req.GetVolumeId())
	return resp, nil
}

// ControllerPublishVolume routes to the vendor based on volume_context or secrets.
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

	socketPath := c.resolveVendorSocket(req.GetVolumeContext())
	if socketPath == "" {
		return nil, status.Error(codes.InvalidArgument,
			"volume context missing required key \"osac.backend\"")
	}

	vendorConn, err := c.proxyMgr.GetConnection(socketPath)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "failed to connect to vendor CSI driver: %v", err)
	}

	vendorClient := csi.NewControllerClient(vendorConn)
	return vendorClient.ControllerPublishVolume(ctx, req)
}

// ControllerUnpublishVolume routes to the vendor based on secrets.
func (c *ControllerServer) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	klog.Infof("ControllerUnpublishVolume called: volumeId=%s nodeId=%s",
		req.GetVolumeId(), req.GetNodeId())

	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}

	socketPath, err := c.resolveVendorSocketFromSecrets(req.GetSecrets())
	if err != nil {
		return nil, err
	}

	vendorConn, err := c.proxyMgr.GetConnection(socketPath)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable,
			"failed to connect to vendor CSI driver: %v", err)
	}

	vendorClient := csi.NewControllerClient(vendorConn)
	return vendorClient.ControllerUnpublishVolume(ctx, req)
}

// ValidateVolumeCapabilities proxies the call to the vendor CSI driver.
func (c *ControllerServer) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	klog.Infof("ValidateVolumeCapabilities called: volumeId=%s", req.GetVolumeId())

	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if len(req.GetVolumeCapabilities()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume capabilities are required")
	}

	socketPath := c.resolveVendorSocket(req.GetVolumeContext())
	if socketPath == "" {
		return nil, status.Error(codes.InvalidArgument,
			"volume context missing required key \"osac.backend\"")
	}

	vendorConn, err := c.proxyMgr.GetConnection(socketPath)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable,
			"failed to connect to vendor CSI driver: %v", err)
	}

	vendorClient := csi.NewControllerClient(vendorConn)
	return vendorClient.ValidateVolumeCapabilities(ctx, req)
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

func (c *ControllerServer) resolveVendorSocket(volumeContext map[string]string) string {
	if volumeContext != nil {
		if backend, ok := volumeContext["osac.backend"]; ok && backend != "" {
			if socketPath, ok := c.vendorSockets[backend]; ok {
				return socketPath
			}
		}
	}
	return ""
}

func (c *ControllerServer) resolveVendorSocketFromSecrets(secrets map[string]string) (string, error) {
	backend := ""
	if secrets != nil {
		backend = secrets["osac.backend"]
	}
	if backend == "" {
		return "", status.Error(codes.InvalidArgument,
			"secrets missing required key \"osac.backend\"")
	}
	socketPath, ok := c.vendorSockets[backend]
	if !ok {
		return "", status.Errorf(codes.NotFound,
			"no vendor socket found for backend %q", backend)
	}
	return socketPath, nil
}
