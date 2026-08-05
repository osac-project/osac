// Package fulfillment provides the client interface for the OSAC fulfillment service.
package fulfillment

import (
	"context"

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
// backend. Used during development before the real gRPC client is available.
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
