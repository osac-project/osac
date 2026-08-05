/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package console

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// KubeVirtBackendBuilder builds a KubeVirtBackend.
type KubeVirtBackendBuilder struct {
	logger     *slog.Logger
	caPool     *x509.CertPool
	pingConfig PingConfig
}

// kubeVirtBackend connects to compute instance consoles via pre-computed
// WebSocket URIs embedded in encrypted console tickets.
type kubeVirtBackend struct {
	logger     *slog.Logger
	caPool     *x509.CertPool
	pingConfig PingConfig
}

// NewKubeVirtBackend creates a new builder for the KubeVirt backend.
func NewKubeVirtBackend() *KubeVirtBackendBuilder {
	return &KubeVirtBackendBuilder{}
}

func (b *KubeVirtBackendBuilder) SetLogger(value *slog.Logger) *KubeVirtBackendBuilder {
	b.logger = value
	return b
}

// SetCAPool sets a CA pool for TLS when dialing backend WebSocket endpoints.
func (b *KubeVirtBackendBuilder) SetCAPool(value *x509.CertPool) *KubeVirtBackendBuilder {
	b.caPool = value
	return b
}

// SetPingConfig sets the WebSocket ping configuration for backend connections.
func (b *KubeVirtBackendBuilder) SetPingConfig(value PingConfig) *KubeVirtBackendBuilder {
	b.pingConfig = value
	return b
}

func (b *KubeVirtBackendBuilder) Build() (Backend, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.pingConfig.PingInterval < 0 {
		return nil, fmt.Errorf(
			"ping interval must be non-negative, got %s",
			b.pingConfig.PingInterval,
		)
	}
	if b.pingConfig.PingInterval > 0 && b.pingConfig.PongTimeout <= 0 {
		return nil, fmt.Errorf(
			"pong timeout must be positive, got %s",
			b.pingConfig.PongTimeout,
		)
	}
	return &kubeVirtBackend{
		logger:     b.logger,
		caPool:     b.caPool,
		pingConfig: b.pingConfig,
	}, nil
}

// Connect dials the pre-computed WebSocket URI from the target, using the
// backend's CA pool for TLS verification. The returned connection wraps the
// WebSocket in a net.Conn via websocket.NetConn for bidirectional io.Copy.
// A background goroutine sends periodic WebSocket pings to detect silent
// connection death from intermediate network devices (NAT, firewalls).
func (b *kubeVirtBackend) Connect(ctx context.Context, target Target) (io.ReadWriteCloser, error) {
	b.logger.InfoContext(ctx, "Connecting to console",
		slog.String("uri", target.BackendURI),
	)

	dialOpts := &websocket.DialOptions{}

	if target.BackendToken != "" {
		dialOpts.HTTPHeader = http.Header{
			"Authorization": []string{"Bearer " + target.BackendToken},
		}
	}

	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13}
	if b.caPool != nil {
		tlsConfig.RootCAs = b.caPool
	}
	dialOpts.HTTPClient = &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:     tlsConfig,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}

	wsLogger := b.logger.With(slog.String("component", "backend_ws"))
	dialOpts.OnPingReceived = PingReceivedHandler(wsLogger, ctx)
	dialOpts.OnPongReceived = PongReceivedHandler(wsLogger, ctx)

	conn, _, err := websocket.Dial(ctx, target.BackendURI, dialOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to console: %w", err)
	}

	b.logger.InfoContext(ctx, "Connected to console",
		slog.String("uri", target.BackendURI),
	)

	// Start a background ping goroutine that keeps the WebSocket alive across
	// NAT gateways and firewalls. Runs until the context is cancelled (session
	// end, eviction, or timeout).
	StartPing(ctx, conn, wsLogger, b.pingConfig)

	return websocket.NetConn(ctx, conn, websocket.MessageBinary), nil
}
