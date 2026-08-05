/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"

	"github.com/osac-project/fulfillment-service/internal/console"
)

// ticketOpener abstracts the ticket-opening operation so that tests can inject
// stubs without requiring real JWE key material. *console.TicketOpener satisfies
// this interface.
type ticketOpener interface {
	Open(ctx context.Context, tokenString string) (*console.Ticket, error)
}

// ConsoleProxyCoreBuilder contains the data and logic needed to create a console proxy core. Don't create instances
// of this type directly, use the NewConsoleProxyCore function instead.
type ConsoleProxyCoreBuilder struct {
	logger  *slog.Logger
	opener  *console.TicketOpener
	manager *console.Manager
}

// ConsoleProxyCore contains the shared logic for both gRPC and WebSocket console
// proxy transports. Each transport extracts the ticket via its native auth mechanism
// and passes it here along with an io.ReadWriteCloser for the client side.
type ConsoleProxyCore struct {
	logger  *slog.Logger
	opener  ticketOpener
	manager *console.Manager
}

// NewConsoleProxyCore creates a builder that can then be used to configure and create a new console proxy core.
func NewConsoleProxyCore() *ConsoleProxyCoreBuilder {
	return &ConsoleProxyCoreBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *ConsoleProxyCoreBuilder) SetLogger(value *slog.Logger) *ConsoleProxyCoreBuilder {
	b.logger = value
	return b
}

// SetOpener sets the ticket opener used to decrypt and verify console tickets. This is mandatory.
func (b *ConsoleProxyCoreBuilder) SetOpener(value *console.TicketOpener) *ConsoleProxyCoreBuilder {
	b.opener = value
	return b
}

// SetManager sets the console session manager. This is mandatory.
func (b *ConsoleProxyCoreBuilder) SetManager(value *console.Manager) *ConsoleProxyCoreBuilder {
	b.manager = value
	return b
}

// Build uses the data stored in the builder to create and configure a new console proxy core.
func (b *ConsoleProxyCoreBuilder) Build() (*ConsoleProxyCore, error) {
	// Check parameters:
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.opener == nil {
		return nil, errors.New("opener is mandatory")
	}
	if b.manager == nil {
		return nil, errors.New("manager is mandatory")
	}

	// Create and populate the object:
	return &ConsoleProxyCore{
		logger:  b.logger,
		opener:  b.opener,
		manager: b.manager,
	}, nil
}

// ExtractBearerToken extracts the token from a Bearer authorization value.
// Used by both gRPC (from metadata) and WebSocket (from HTTP header) transports.
func ExtractBearerToken(authValue string) (string, error) {
	if len(authValue) < 7 || !strings.EqualFold(authValue[:7], "Bearer ") {
		return "", errors.New("authorization must use Bearer scheme")
	}
	token := strings.TrimSpace(authValue[7:])
	if token == "" {
		return "", errors.New("authorization must use Bearer scheme")
	}
	return token, nil
}

// OpenTicket decrypts and verifies a raw ticket JWT and logs the result. Both
// transports call this after extracting the raw token from their respective auth
// mechanisms.
func (c *ConsoleProxyCore) OpenTicket(ctx context.Context, rawTicket string) (*console.Ticket, error) {
	ticket, err := c.opener.Open(ctx, rawTicket)
	if err != nil {
		return nil, err
	}
	c.logger.InfoContext(ctx, "Console ticket opened",
		slog.String("jti", ticket.JTI),
		slog.String("user", ticket.User),
		slog.String("console_type", ticket.ConsoleType),
	)
	return ticket, nil
}

// ConnectBackend opens a backend connection using the target details embedded
// in the encrypted ticket. Returns the connection and the session context, which
// is cancelled on eviction, session timeout, or parent context cancellation.
// Callers must pass sessionCtx to Relay so that session lifecycle events
// terminate the proxy — the backend websocket ignores context after dial.
func (c *ConsoleProxyCore) ConnectBackend(ctx context.Context, ticket *console.Ticket) (io.ReadWriteCloser, context.Context, error) {
	target := console.Target{
		ResourceType: console.ResourceTypeComputeInstance,
		BackendURI:   ticket.TargetURI,
		BackendToken: ticket.TargetToken,
	}

	result, err := c.manager.Connect(ctx, target, ticket.User, ticket.ClientID)
	if err != nil {
		return nil, nil, err
	}

	return result.Conn, result.SessionCtx, nil
}

// Relay copies data bidirectionally between two io.ReadWriteCloser values.
// It closes the backend when done. The client side is closed by the transport.
func (c *ConsoleProxyCore) Relay(ctx context.Context, client, backend io.ReadWriteCloser) error {
	defer backend.Close()

	errCh := make(chan error, 2)

	// Backend -> client.
	go func() {
		_, err := io.CopyBuffer(client, backend, make([]byte, 64*1024))
		errCh <- err
	}()

	// Client -> backend.
	go func() {
		_, err := io.CopyBuffer(backend, client, make([]byte, 64*1024))
		errCh <- err
	}()

	// Context cancellation -> close both sides to unblock io.Copy.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			client.Close()
			backend.Close()
		case <-done:
		}
	}()

	// Wait for the first copy goroutine to finish. Its result — even nil —
	// is the session outcome. Close both sides to unblock the peer goroutine,
	// then drain it so no goroutines are leaked.
	firstErr := <-errCh
	client.Close()
	backend.Close()
	<-errCh

	// When the context was cancelled (eviction, timeout, client disconnect),
	// the error is a forced-close artifact, not a genuine relay failure.
	if ctx.Err() != nil {
		c.logger.DebugContext(ctx, "Console relay ended",
			slog.String("reason", ctx.Err().Error()),
		)
		return nil
	}
	if firstErr != nil {
		c.logger.WarnContext(ctx, "Console proxy error", slog.Any("error", firstErr))
	}
	return firstErr
}
