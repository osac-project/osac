package driver

import (
	"context"
	"math/rand/v2"
	"net"
	"os"
	"strings"
	"time"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"k8s.io/klog/v2"

	"github.com/osac-project/osac/osac-csi-driver/pkg/proxy"
)

const (
	nodeNameEnv           = "NODE_NAME"
	lvmsNodeSocketEnv     = "OSAC_LVMS_NODE_SOCKET"
	defaultLVMSNodeSocket = "/run/topolvm/csi-topolvm.sock"

	backendContextKey        = "osac.backend"
	protocolContextKey       = "osac.protocol"
	vendorVolumeIDContextKey = "osac.volume-id"
	nfsProtocol              = "nfs"

	// Volume context keys the VAST NFS controller sets for a mount through a
	// VIP pool FQDN.
	vipPoolFQDNContextKey      = "vip_pool_fqdn"
	vipPoolFQDNRandomPrefixKey = "vip_pool_fqdn_random_prefix"

	nfsPort                 = "2049"
	vipReachabilityTimeout  = 5 * time.Second
	vipRandomPrefixLength   = 4
	vipRandomPrefixAlphabet = "abcdefghijklmnopqrstuvwxyz"
)

// reachabilityChecker reports whether a host:port can be reached.
type reachabilityChecker func(ctx context.Context, hostPort string) error

// mounter inspects and removes mounts on the node.
type mounter interface {
	IsTmpfsMount(path string) (bool, error)
	Unmount(path string) error
}

// NodeServer implements the CSI Node service.
// It routes node operations to the vendor CSI driver chosen by the backend
// and protocol in the volume context, and records that route so calls that
// carry no volume context can follow it.
type NodeServer struct {
	csi.UnimplementedNodeServer
	nodeID         string
	proxyMgr       *proxy.Manager
	vendorSockets  map[string]string
	routes         nodeRouteStore
	checkReachable reachabilityChecker
	mounter        mounter
}

// NodeOption configures a NodeServer.
type NodeOption func(*NodeServer)

// WithRouteStore sets the store that keeps volume routes.
func WithRouteStore(s nodeRouteStore) NodeOption {
	return func(n *NodeServer) { n.routes = s }
}

// withReachabilityChecker replaces the VIP reachability check.
func withReachabilityChecker(c reachabilityChecker) NodeOption {
	return func(n *NodeServer) { n.checkReachable = c }
}

// withMounter replaces the mounter used to clean up after a failed publish.
func withMounter(m mounter) NodeOption {
	return func(n *NodeServer) { n.mounter = m }
}

// NewNodeServer creates a new CSI node server. Routes are kept in memory
// unless a store is set with WithRouteStore.
func NewNodeServer(nodeID string, proxyMgr *proxy.Manager, vendorSockets map[string]string, opts ...NodeOption) *NodeServer {
	n := &NodeServer{
		nodeID:         nodeID,
		proxyMgr:       proxyMgr,
		vendorSockets:  normalizeVendorSockets(vendorSockets),
		routes:         newMemoryRouteStore(),
		checkReachable: dialReachable,
		mounter:        systemMounter{},
	}
	for _, opt := range opts {
		opt(n)
	}
	return n
}

// NodeStageVolume stages a volume at a staging path by proxying to the vendor CSI driver.
func (n *NodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	klog.Infof("NodeStageVolume called: volumeId=%s stagingPath=%s",
		req.GetVolumeId(), req.GetStagingTargetPath())

	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if req.GetStagingTargetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "staging target path is required")
	}
	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "volume capability is required")
	}

	socketPath, route, err := n.routeFromContext(req.GetVolumeId(), req.GetVolumeContext())
	if err != nil {
		return nil, err
	}

	vendorClient, err := n.vendorClient(socketPath)
	if err != nil {
		return nil, err
	}
	vendorReq := proto.Clone(req).(*csi.NodeStageVolumeRequest)
	vendorReq.VolumeId = route.VendorVolumeID
	resp, err := vendorClient.NodeStageVolume(ctx, vendorReq)
	if err != nil {
		if !isUnimplemented(err) {
			klog.Errorf("Vendor NodeStageVolume failed: %v", err)
			return nil, err
		}
		klog.Infof("Vendor does not implement NodeStageVolume, treating as no-op: volumeId=%s",
			req.GetVolumeId())
		resp = &csi.NodeStageVolumeResponse{}
	}

	if err := n.saveRoute(req.GetVolumeId(), route); err != nil {
		return nil, err
	}
	klog.Infof("NodeStageVolume succeeded: volumeId=%s", req.GetVolumeId())
	return resp, nil
}

// NodeUnstageVolume unstages a volume by proxying to the vendor CSI driver.
func (n *NodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	klog.Infof("NodeUnstageVolume called: volumeId=%s stagingPath=%s",
		req.GetVolumeId(), req.GetStagingTargetPath())

	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if req.GetStagingTargetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "staging target path is required")
	}

	socketPath, route, found, err := n.recordedRoute(req.GetVolumeId())
	if err != nil {
		return nil, err
	}
	if !found {
		klog.Infof("NodeUnstageVolume: no route recorded for volume %q, treating as no-op", req.GetVolumeId())
		return &csi.NodeUnstageVolumeResponse{}, nil
	}

	vendorClient, err := n.vendorClient(socketPath)
	if err != nil {
		return nil, err
	}
	vendorReq := proto.Clone(req).(*csi.NodeUnstageVolumeRequest)
	vendorReq.VolumeId = route.VendorVolumeID
	resp, err := vendorClient.NodeUnstageVolume(ctx, vendorReq)
	if err != nil {
		if !isUnimplemented(err) {
			// Keep the route so that kubelet's retry can still reach the vendor.
			klog.Errorf("Vendor NodeUnstageVolume failed: %v", err)
			return nil, err
		}
		klog.Infof("Vendor does not implement NodeUnstageVolume, treating as no-op: volumeId=%s",
			req.GetVolumeId())
		resp = &csi.NodeUnstageVolumeResponse{}
	}

	if err := n.routes.Delete(req.GetVolumeId()); err != nil {
		return nil, status.Errorf(codes.Internal, "removing route for volume %q: %v", req.GetVolumeId(), err)
	}
	klog.Infof("NodeUnstageVolume succeeded: volumeId=%s", req.GetVolumeId())
	return resp, nil
}

// NodePublishVolume publishes a volume at a target path by proxying to the vendor CSI driver.
func (n *NodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	klog.Infof("NodePublishVolume called: volumeId=%s targetPath=%s",
		req.GetVolumeId(), req.GetTargetPath())

	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if req.GetTargetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "target path is required")
	}
	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "volume capability is required")
	}

	socketPath, route, err := n.routeFromContext(req.GetVolumeId(), req.GetVolumeContext())
	if err != nil {
		return nil, err
	}
	if err := n.checkVIPReachable(ctx, req.GetVolumeId(), route.Protocol, req.GetVolumeContext()); err != nil {
		return nil, err
	}

	vendorClient, err := n.vendorClient(socketPath)
	if err != nil {
		return nil, err
	}
	vendorReq := proto.Clone(req).(*csi.NodePublishVolumeRequest)
	vendorReq.VolumeId = route.VendorVolumeID
	resp, err := vendorClient.NodePublishVolume(ctx, vendorReq)
	if err != nil {
		klog.Errorf("Vendor NodePublishVolume failed: %v", err)
		n.removePlaceholderMount(req.GetTargetPath())
		return nil, err
	}

	if err := n.saveRoute(req.GetVolumeId(), route); err != nil {
		return nil, err
	}
	klog.Infof("NodePublishVolume succeeded: volumeId=%s", req.GetVolumeId())
	return resp, nil
}

// NodeUnpublishVolume unpublishes a volume by proxying to the vendor CSI driver.
// The route is kept: NodeUnstageVolume follows and removes it.
func (n *NodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	klog.Infof("NodeUnpublishVolume called: volumeId=%s targetPath=%s",
		req.GetVolumeId(), req.GetTargetPath())

	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if req.GetTargetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "target path is required")
	}

	socketPath, route, found, err := n.recordedRoute(req.GetVolumeId())
	if err != nil {
		return nil, err
	}
	if !found {
		klog.Infof("NodeUnpublishVolume: no route recorded for volume %q, treating as no-op", req.GetVolumeId())
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}

	vendorClient, err := n.vendorClient(socketPath)
	if err != nil {
		return nil, err
	}
	vendorReq := proto.Clone(req).(*csi.NodeUnpublishVolumeRequest)
	vendorReq.VolumeId = route.VendorVolumeID
	resp, err := vendorClient.NodeUnpublishVolume(ctx, vendorReq)
	if err != nil {
		klog.Errorf("Vendor NodeUnpublishVolume failed: %v", err)
		return nil, err
	}

	klog.Infof("NodeUnpublishVolume succeeded: volumeId=%s", req.GetVolumeId())
	return resp, nil
}

// NodeGetCapabilities returns the capabilities supported by this node plugin.
func (n *NodeServer) NodeGetCapabilities(_ context.Context, _ *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	klog.Infof("NodeGetCapabilities called")
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

// NodeGetInfo returns information about this node.
func (n *NodeServer) NodeGetInfo(_ context.Context, _ *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	nodeName := strings.TrimSpace(os.Getenv(nodeNameEnv))
	if nodeName == "" {
		return nil, status.Errorf(codes.FailedPrecondition,
			"%s environment variable is required", nodeNameEnv)
	}

	klog.Infof("NodeGetInfo called: nodeId=%s", nodeName)
	return &csi.NodeGetInfoResponse{
		NodeId: nodeName,
		AccessibleTopology: &csi.Topology{
			Segments: map[string]string{volumeNodeTopologyKey: nodeName},
		},
	}, nil
}

func normalizeVendorSockets(vendorSockets map[string]string) map[string]string {
	normalized := make(map[string]string, len(vendorSockets)+1)
	for backend, socketPath := range vendorSockets {
		normalized[backend] = socketPath
	}

	lvmsSocket := strings.TrimSpace(os.Getenv(lvmsNodeSocketEnv))
	if lvmsSocket == "" {
		lvmsSocket = normalized[lvmsProvider]
	}
	if lvmsSocket == "" {
		lvmsSocket = defaultLVMSNodeSocket
	}
	normalized[lvmsProvider] = lvmsSocket
	return normalized
}

// NodeGetVolumeStats proxies the call to the vendor CSI driver for the given volume.
func (n *NodeServer) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	klog.Infof("NodeGetVolumeStats called: volumeId=%s volumePath=%s",
		req.GetVolumeId(), req.GetVolumePath())

	socketPath, route, found, err := n.recordedRoute(req.GetVolumeId())
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, status.Errorf(codes.FailedPrecondition,
			"no route recorded for volume %q", req.GetVolumeId())
	}

	vendorClient, err := n.vendorClient(socketPath)
	if err != nil {
		return nil, err
	}
	vendorReq := proto.Clone(req).(*csi.NodeGetVolumeStatsRequest)
	vendorReq.VolumeId = route.VendorVolumeID
	return vendorClient.NodeGetVolumeStats(ctx, vendorReq)
}

func (n *NodeServer) vendorClient(socketPath string) (csi.NodeClient, error) {
	vendorConn, err := n.proxyMgr.GetConnection(socketPath)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable,
			"failed to connect to vendor CSI driver: %v", err)
	}
	return csi.NewNodeClient(vendorConn), nil
}

// resolveVendorSocket returns the vendor socket for the backend and protocol
// in a volume context.
func (n *NodeServer) resolveVendorSocket(volumeContext map[string]string) (string, error) {
	return vendorRoute(n.vendorSockets, volumeContext[backendContextKey], volumeContext[protocolContextKey])
}

// routeFromContext resolves the vendor socket and the route to record for a
// volume from its volume context.
func (n *NodeServer) routeFromContext(volumeID string, volumeContext map[string]string) (string, nodeRoute, error) {
	socketPath, err := n.resolveVendorSocket(volumeContext)
	if err != nil {
		return "", nodeRoute{}, err
	}
	backend := volumeContext[backendContextKey]
	vendorVolumeID, err := resolveVendorVolumeID(volumeID, backend, volumeContext)
	if err != nil {
		return "", nodeRoute{}, err
	}
	return socketPath, nodeRoute{
		Version:        nodeRouteVersion,
		Backend:        backend,
		Protocol:       strings.ToLower(volumeContext[protocolContextKey]),
		VendorVolumeID: vendorVolumeID,
	}, nil
}

// recordedRoute loads the route recorded for a volume and resolves its
// vendor socket. found is false when no route is recorded.
func (n *NodeServer) recordedRoute(volumeID string) (socketPath string, route nodeRoute, found bool, err error) {
	route, found, err = n.routes.Load(volumeID)
	if err != nil {
		return "", nodeRoute{}, false, status.Errorf(codes.Internal,
			"loading route for volume %q: %v", volumeID, err)
	}
	if !found {
		return "", nodeRoute{}, false, nil
	}
	socketPath, err = vendorRoute(n.vendorSockets, route.Backend, route.Protocol)
	if err != nil {
		return "", nodeRoute{}, false, err
	}
	return socketPath, route, true, nil
}

// saveRoute records a route after a successful vendor call. A failure is
// returned as Internal so that kubelet retries; vendor stage and publish are
// idempotent.
func (n *NodeServer) saveRoute(volumeID string, route nodeRoute) error {
	if err := n.routes.Save(volumeID, route); err != nil {
		return status.Errorf(codes.Internal, "recording route for volume %q: %v", volumeID, err)
	}
	return nil
}

// resolveVendorVolumeID returns the vendor's volume ID from the volume
// context. lvms volumes without one fall back to the CSI volume ID; every
// other backend must carry "osac.volume-id".
func resolveVendorVolumeID(volumeID, backend string, volumeContext map[string]string) (string, error) {
	if vendorVolumeID := strings.TrimSpace(volumeContext[topolvmVolumeIDContextKey]); vendorVolumeID != "" {
		return vendorVolumeID, nil
	}
	if vendorVolumeID := strings.TrimSpace(volumeContext[vendorVolumeIDContextKey]); vendorVolumeID != "" {
		return vendorVolumeID, nil
	}
	if backend == lvmsProvider {
		return volumeID, nil
	}
	return "", status.Errorf(codes.InvalidArgument,
		"volume context missing required key %q for backend %q", vendorVolumeIDContextKey, backend)
}

// checkVIPReachable dials the VIP pool of an NFS volume on the NFS port
// before the vendor mounts it. An unreachable VIP otherwise surfaces only as
// a vendor mount timeout. The log line carries no host name or dial error;
// the error returned to the caller names the pool.
func (n *NodeServer) checkVIPReachable(ctx context.Context, volumeID, protocol string, volumeContext map[string]string) error {
	fqdn := strings.TrimSpace(volumeContext[vipPoolFQDNContextKey])
	if protocol != nfsProtocol || fqdn == "" {
		return nil
	}

	// The vendor mounts "<random label>.<fqdn>" when the random prefix is on,
	// and the pool's DNS need only cover "*.<fqdn>", so probe the same shape.
	host := fqdn
	if volumeContext[vipPoolFQDNRandomPrefixKey] == "true" {
		host = randomDNSLabel() + "." + fqdn
	}

	checkCtx, cancel := context.WithTimeout(ctx, vipReachabilityTimeout)
	defer cancel()
	if err := n.checkReachable(checkCtx, net.JoinHostPort(host, nfsPort)); err != nil {
		klog.Errorf("NFS VIP reachability check failed on TCP %s for volume %s", nfsPort, volumeID)
		return status.Errorf(codes.Unavailable,
			"VAST VIP pool %s is not reachable on TCP %s from node %s; check tenant network routing and DNS",
			fqdn, nfsPort, n.nodeID)
	}
	return nil
}

// removePlaceholderMount unmounts a tmpfs a vendor left at targetPath after
// a failed publish. Failures are logged; the vendor's error is what the
// caller returns.
func (n *NodeServer) removePlaceholderMount(targetPath string) {
	tmpfs, err := n.mounter.IsTmpfsMount(targetPath)
	if err != nil {
		klog.Warningf("Checking for a placeholder mount at %s: %v", targetPath, err)
		return
	}
	if !tmpfs {
		return
	}
	if err := n.mounter.Unmount(targetPath); err != nil {
		klog.Warningf("Removing placeholder tmpfs at %s: %v", targetPath, err)
		return
	}
	klog.Infof("Removed placeholder tmpfs at %s after a failed publish", targetPath)
}

func dialReachable(ctx context.Context, hostPort string) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", hostPort)
	if err != nil {
		return err
	}
	return conn.Close()
}

func randomDNSLabel() string {
	b := make([]byte, vipRandomPrefixLength)
	for i := range b {
		b[i] = vipRandomPrefixAlphabet[rand.IntN(len(vipRandomPrefixAlphabet))]
	}
	return string(b)
}

func isUnimplemented(err error) bool {
	st, ok := status.FromError(err)
	return ok && (st.Code() == codes.Unimplemented ||
		strings.Contains(st.Message(), "not implemented"))
}
