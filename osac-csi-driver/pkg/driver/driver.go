// Package driver implements the OSAC CSI meta-driver gRPC services.
package driver

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/osac-project/osac/osac-csi-driver/pkg/fulfillment"
	"github.com/osac-project/osac/osac-csi-driver/pkg/proxy"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"
)

// Driver is the OSAC CSI meta-driver that proxies CSI calls to vendor CSI drivers.
type Driver struct {
	name     string
	version  string
	nodeID   string
	endpoint string
	srv      *grpc.Server

	identity   csi.IdentityServer
	controller csi.ControllerServer
	node       csi.NodeServer
}

// NewDriver creates a new OSAC CSI driver instance.
func NewDriver(name, version, endpoint, nodeID, clusterID string, vc fulfillment.VolumeClient, cpc fulfillment.ControlPlaneClient, vendorSockets map[string]string) (*Driver, error) {
	if name == "" {
		return nil, fmt.Errorf("driver name is required")
	}
	if nodeID == "" {
		return nil, fmt.Errorf("node ID is required")
	}
	if vc == nil {
		return nil, fmt.Errorf("volume client is required")
	}

	// clusterID is optional — the fulfillment service may identify the
	// cluster from connection credentials or a mounted ConfigMap.

	proxyMgr := proxy.NewManager(vendorSockets)

	return &Driver{
		name:       name,
		version:    version,
		nodeID:     nodeID,
		endpoint:   endpoint,
		identity:   NewIdentityServer(name, version),
		controller: NewControllerServer(vc, cpc, clusterID),
		node:       NewNodeServer(nodeID, proxyMgr, vendorSockets),
	}, nil
}

// Run starts the gRPC server and listens for CSI requests.
// It blocks until a SIGTERM or SIGINT is received.
func (d *Driver) Run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	return d.RunWithContext(ctx)
}

// RunWithContext starts the gRPC server and blocks until the context is cancelled.
func (d *Driver) RunWithContext(ctx context.Context) error {
	u, err := url.Parse(d.endpoint)
	if err != nil {
		return fmt.Errorf("parsing endpoint URL %q: %w", d.endpoint, err)
	}

	if u.Scheme != "unix" {
		return fmt.Errorf("only unix:// endpoints are supported, got %q", u.Scheme)
	}

	socketPath := u.Path

	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing old socket file %q: %w", socketPath, err)
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listening on %q: %w", socketPath, err)
	}

	if err := os.Chmod(socketPath, 0666); err != nil {
		return fmt.Errorf("setting socket permissions on %q: %w", socketPath, err)
	}

	klog.Infof("OSAC CSI driver %q starting on %s", d.name, d.endpoint)

	d.srv = grpc.NewServer(
		grpc.UnaryInterceptor(loggingInterceptor),
	)

	csi.RegisterIdentityServer(d.srv, d.identity)
	csi.RegisterControllerServer(d.srv, d.controller)
	csi.RegisterNodeServer(d.srv, d.node)

	go func() {
		<-ctx.Done()
		klog.Infof("Context cancelled, stopping gracefully...")
		d.Stop()
	}()

	klog.Infof("OSAC CSI driver serving on %s", socketPath)
	if err := d.srv.Serve(listener); err != nil {
		return fmt.Errorf("serving gRPC: %w", err)
	}

	return nil
}

// Stop gracefully stops the gRPC server.
func (d *Driver) Stop() {
	if d.srv != nil {
		klog.Infof("Stopping OSAC CSI driver...")
		d.srv.GracefulStop()
	}
}

func loggingInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	klog.Infof("gRPC call: %s", info.FullMethod)
	resp, err := handler(ctx, req)
	if err != nil {
		klog.Errorf("gRPC call %s failed: %v", info.FullMethod, err)
	} else {
		klog.Infof("gRPC call %s succeeded", info.FullMethod)
	}
	return resp, err
}
