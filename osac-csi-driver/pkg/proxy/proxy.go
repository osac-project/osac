// Package proxy manages gRPC client connections to vendor CSI driver sockets.
package proxy

import (
	"fmt"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/klog/v2"
)

// Manager manages gRPC client connections to vendor CSI driver sockets.
type Manager struct {
	mu            sync.Mutex
	vendorSockets map[string]string
	connections   map[string]*grpc.ClientConn
}

// NewManager creates a new proxy manager with the given vendor socket mappings.
func NewManager(vendorSockets map[string]string) *Manager {
	return &Manager{
		vendorSockets: vendorSockets,
		connections:   make(map[string]*grpc.ClientConn),
	}
}

// GetConnection returns a gRPC client connection to the given endpoint.
// Supports both unix sockets (paths starting with /) and TCP endpoints (host:port).
// Connections are lazily created and cached for reuse.
func (m *Manager) GetConnection(endpoint string) (*grpc.ClientConn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if conn, ok := m.connections[endpoint]; ok {
		return conn, nil
	}

	target := endpoint
	if strings.HasPrefix(endpoint, "/") {
		target = "unix://" + endpoint
	} else if !strings.Contains(endpoint, "://") {
		target = "dns:///" + endpoint
	}

	klog.Infof("Creating new gRPC connection to vendor endpoint: %s (target: %s)", endpoint, target)
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("connecting to vendor CSI driver at %s: %w", endpoint, err)
	}

	m.connections[endpoint] = conn
	return conn, nil
}

// Close closes all cached gRPC client connections.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var firstErr error
	for path, conn := range m.connections {
		if err := conn.Close(); err != nil {
			klog.Errorf("Error closing connection to %s: %v", path, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	m.connections = make(map[string]*grpc.ClientConn)
	return firstErr
}
