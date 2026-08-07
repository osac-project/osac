// Package fulfillment provides the client interface for the OSAC fulfillment service.
package fulfillment

import (
	"context"

	"google.golang.org/grpc"
	"k8s.io/klog/v2"
)

// ResolveResult contains the resolved storage backend details for a tenant's
// storage tier. Returned by Client.Resolve after the fulfillment-service
// determines which vendor CSI driver should handle the request.
type ResolveResult struct {
	Backend  string
	Endpoint string
	Protocol string
	Params   map[string]string
}

// Client defines the interface for communicating with the OSAC fulfillment
// service's Volume API. The CSI driver calls Resolve during CreateVolume to
// determine which vendor backend to route to.
type Client interface {
	Resolve(ctx context.Context, tenant, tier string) (*ResolveResult, error)
	Close() error
}

// LoggingStub is a Client that logs calls and returns a configurable default
// backend. Used during development before the Volume API is available.
type LoggingStub struct {
	DefaultBackend  string
	DefaultEndpoint string
	DefaultProtocol string
}

func (s *LoggingStub) Resolve(_ context.Context, tenant, tier string) (*ResolveResult, error) {
	klog.Infof("fulfillment stub: Resolve(tenant=%q, tier=%q) -> backend=%q", tenant, tier, s.DefaultBackend)
	return &ResolveResult{
		Backend:  s.DefaultBackend,
		Endpoint: s.DefaultEndpoint,
		Protocol: s.DefaultProtocol,
	}, nil
}

func (s *LoggingStub) Close() error { return nil }

// GRPCClient is a Client that connects to the fulfillment-service.
// TODO(OSAC-2872): implement Resolve via the Volume API once it lands
// server-side. Until then, Resolve delegates to the LoggingStub.
type GRPCClient struct {
	conn *grpc.ClientConn
	stub LoggingStub
}

// NewGRPCClientFromConn creates a fulfillment client from an existing gRPC
// connection. The caller owns the connection lifecycle; Close will close it.
func NewGRPCClientFromConn(conn *grpc.ClientConn) *GRPCClient {
	return &GRPCClient{conn: conn}
}

// Resolve delegates to the logging stub until the Volume API is implemented.
func (c *GRPCClient) Resolve(ctx context.Context, tenant, tier string) (*ResolveResult, error) {
	return c.stub.Resolve(ctx, tenant, tier)
}

func (c *GRPCClient) Close() error {
	return c.conn.Close()
}
