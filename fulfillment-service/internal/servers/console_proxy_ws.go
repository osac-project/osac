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
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/osac-project/osac/fulfillment-service/internal/console"
)

// WebSocket close codes for setup errors, sent as a Close frame after the
// upgrade completes. Browsers collapse any non-101 handshake response into a
// generic error/1006 close, so setup errors can only reach browser JS as a
// CloseEvent after the WebSocket upgrade succeeds.
const (
	wsStatusUnauthorized websocket.StatusCode = 3000 // IANA-registered.
	wsStatusConsoleInUse websocket.StatusCode = 4409 // Private-use: HTTP 409 equivalent.
)

// WebSocket close reasons. Fixed and short -- never include validation
// details, backend URLs, tokens, or raw error messages.
const (
	wsReasonUnauthorized = "unauthorized"
	wsReasonConsoleInUse = "console session already active"
	wsReasonBadGateway   = "failed to connect to console backend"
)

// ConsoleProxyWSHandler serves WebSocket console connections. Ticket
// verification and backend connection happen after the WebSocket upgrade, so
// that setup errors can be reported as close frames (see wsStatus* codes)
// instead of an HTTP status.
type ConsoleProxyWSHandler struct {
	core           *ConsoleProxyCore
	allowedOrigins []string
	pingConfig     console.PingConfig
}

// NewConsoleProxyWSHandler creates a new WebSocket console proxy handler.
// The allowedOrigins list is passed to the library's OriginPatterns and controls
// which Origins are accepted during WebSocket upgrade. A wildcard "*" permits
// all origins. Cookie-based auth additionally requires a non-empty Origin.
func NewConsoleProxyWSHandler(core *ConsoleProxyCore, allowedOrigins []string, pingConfig console.PingConfig) *ConsoleProxyWSHandler {
	return &ConsoleProxyWSHandler{
		core:           core,
		allowedOrigins: allowedOrigins,
		pingConfig:     pingConfig,
	}
}

// extractTicket gets the ticket from Authorization header or console-ticket cookie.
// Authorization header takes precedence.
func extractTicket(r *http.Request) (string, error) {
	if authHeader := r.Header.Get("Authorization"); authHeader != "" {
		return ExtractBearerToken(authHeader)
	}

	cookie, err := r.Cookie("console-ticket")
	if err == nil && cookie.Value != "" {
		return cookie.Value, nil
	}

	return "", errors.New("missing ticket: set Authorization header or console-ticket cookie")
}

// closeWithStatus sends a WebSocket close frame with an app-level status
// code and reason. Close performs the close handshake -- up to 5s to write
// the frame plus up to another 5s waiting for the peer's close frame --
// unlike CloseNow which drops the connection immediately.
func closeWithStatus(ctx context.Context, logger *slog.Logger, ws *websocket.Conn, code websocket.StatusCode, reason string) {
	if err := ws.Close(code, reason); err != nil {
		logger.DebugContext(ctx, "Failed to complete WS close handshake",
			slog.Int("code", int(code)),
			slog.String("reason", reason),
			slog.Any("error", err),
		)
	}
}

// backendCloseStatus maps a ConnectBackend error to a WebSocket close code
// and reason.
func backendCloseStatus(err error) (websocket.StatusCode, string) {
	if _, ok := errors.AsType[*console.ErrSessionExists](err); ok {
		return wsStatusConsoleInUse, wsReasonConsoleInUse
	}
	return websocket.StatusBadGateway, wsReasonBadGateway
}

func (h *ConsoleProxyWSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Cookie-based auth (no Authorization header) requires a non-empty Origin
	// for CSWSH protection. The library's OriginPatterns auto-passes empty
	// Origin, so we must reject it ourselves for cookie auth. Handshake-level
	// rejections abort the upgrade with a plain HTTP 403.
	if r.Header.Get("Authorization") == "" && r.Header.Get("Origin") == "" {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	// Extract the ticket value now, but defer reporting failure until after
	// the WebSocket upgrade so the browser can see a close code instead of a
	// generic 1006.
	rawTicket, ticketErr := extractTicket(r)

	// Upgrade to WebSocket.
	// OriginPatterns delegates origin validation to the library. The library
	// auto-passes empty Origin (hence the CSWSH guard above) and same-origin
	// requests; a rejected origin aborts the handshake with HTTP 403.
	wsLogger := h.core.logger.With(slog.String("component", "client_ws"))
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: h.allowedOrigins,
		Subprotocols:   []string{"binary"},
		OnPingReceived: console.PingReceivedHandler(wsLogger),
		OnPongReceived: console.PongReceivedHandler(wsLogger),
	})
	if err != nil {
		h.core.logger.ErrorContext(r.Context(), "Failed to accept WebSocket connection",
			slog.Any("error", err),
		)
		return
	}
	defer func() { _ = ws.CloseNow() }()

	// r.Context() stays valid for the life of ServeHTTP even after Accept
	// hijacks the connection (see http.Hijacker), but coder/websocket still
	// advises against relying on it post-Accept. Own the connection's
	// context explicitly instead: WithoutCancel keeps the request-scoped
	// values, and WithCancel gives the handler control of when it ends.
	connCtx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()

	if ticketErr != nil {
		closeWithStatus(connCtx, wsLogger, ws, wsStatusUnauthorized, wsReasonUnauthorized)
		return
	}

	// OpenTicket may fetch JWKS over the network; bound it.
	openCtx, openCancel := context.WithTimeout(connCtx, 30*time.Second)
	defer openCancel()

	ticket, err := h.core.OpenTicket(openCtx, rawTicket)
	if err != nil {
		closeWithStatus(connCtx, wsLogger, ws, wsStatusUnauthorized, wsReasonUnauthorized)
		return
	}

	// Expose console_type to outer middleware (ConsoleMetrics reads it after handler returns).
	setConsoleType(r.Context(), ticket.ConsoleType)

	backend, sessionCtx, err := h.core.ConnectBackend(connCtx, ticket)
	if err != nil {
		h.core.logger.ErrorContext(connCtx, "Failed to connect backend", slog.Any("error", err))
		code, reason := backendCloseStatus(err)
		closeWithStatus(connCtx, wsLogger, ws, code, reason)
		return
	}

	// The session is established -- let the outer middleware count this
	// connection as a success rather than an upgraded-but-rejected attempt.
	setSessionEstablished(r.Context())

	// Start a ping goroutine to keep the client-facing WebSocket alive.
	console.StartPing(sessionCtx, ws, wsLogger, h.pingConfig)

	// Relay uses sessionCtx, which is cancelled on eviction or session
	// timeout. A client disconnect ends the relay via I/O error instead.
	// Relay logs errors internally.
	clientConn := websocket.NetConn(sessionCtx, ws, websocket.MessageBinary)
	_ = h.core.Relay(sessionCtx, clientConn, backend)
}
