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
	"errors"
	"log/slog"
	"net/http"

	"github.com/coder/websocket"

	"github.com/osac-project/fulfillment-service/internal/console"
)

// ConsoleProxyWSHandler serves WebSocket console connections.
// Auth is verified BEFORE upgrade -- invalid tickets get HTTP 401.
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

func (h *ConsoleProxyWSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Cookie-based auth (no Authorization header) requires a non-empty Origin
	// for CSWSH protection. The library's OriginPatterns auto-passes empty
	// Origin, so we must reject it ourselves for cookie auth.
	if r.Header.Get("Authorization") == "" && r.Header.Get("Origin") == "" {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	// Phase 1: Extract and verify ticket BEFORE upgrade.
	rawTicket, err := extractTicket(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	ticket, err := h.core.OpenTicket(ctx, rawTicket)
	if err != nil {
		http.Error(w, "invalid ticket", http.StatusUnauthorized)
		return
	}

	// Expose console_type to outer middleware (ConsoleMetrics reads it after handler returns).
	setConsoleType(r.Context(), ticket.ConsoleType)

	// Phase 2: Connect backend BEFORE upgrade (fail fast with HTTP error).
	backend, sessionCtx, err := h.core.ConnectBackend(ctx, ticket)
	if err != nil {
		h.core.logger.ErrorContext(ctx, "Failed to connect backend", slog.Any("error", err))
		var sessionErr *console.ErrSessionExists
		if errors.As(err, &sessionErr) {
			http.Error(w, "console session already active", http.StatusConflict)
			return
		}
		http.Error(w, "failed to connect to console backend", http.StatusBadGateway)
		return
	}

	// Phase 3: Upgrade to WebSocket.
	// OriginPatterns delegates origin validation to the library, which matches
	// each pattern via path.Match against scheme://host (if pattern contains "://")
	// or host alone. The library also auto-passes when r.Host == Origin host
	// (same-origin requests) and when Origin is empty.
	wsLogger := h.core.logger.With(slog.String("component", "client_ws"))
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: h.allowedOrigins,
		Subprotocols:   []string{"binary"},
		OnPingReceived: console.PingReceivedHandler(wsLogger, sessionCtx),
		OnPongReceived: console.PongReceivedHandler(wsLogger, sessionCtx),
	})
	if err != nil {
		h.core.logger.ErrorContext(ctx, "Failed to accept WebSocket connection",
			slog.Any("error", err),
		)
		backend.Close()
		return
	}
	defer func() { _ = ws.CloseNow() }()

	// Start a ping goroutine to keep the client-facing WebSocket alive.
	console.StartPing(sessionCtx, ws, wsLogger, h.pingConfig)

	// Phase 4: Relay with sessionCtx: cancelled on eviction, timeout,
	// or client disconnect. Relay logs errors internally.
	clientConn := websocket.NetConn(sessionCtx, ws, websocket.MessageBinary)
	_ = h.core.Relay(sessionCtx, clientConn, backend)
}
