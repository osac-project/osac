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
	"log/slog"
	"time"

	"github.com/coder/websocket"
)

// PingConfig holds the parameters for WebSocket keep-alive pings.
type PingConfig struct {
	PingInterval time.Duration
	PongTimeout  time.Duration
}

// DefaultPingConfig returns a PingConfig with the default values.
func DefaultPingConfig() PingConfig {
	return PingConfig{
		PingInterval: 15 * time.Second,
		PongTimeout:  10 * time.Second,
	}
}

// Ping sends periodic WebSocket pings to detect dead connections. It blocks
// until the context is cancelled or a ping fails, returning the cause.
// If PingInterval is zero, pinging is disabled and Ping blocks until the
// context is cancelled.
func Ping(ctx context.Context, conn *websocket.Conn, config PingConfig) error {
	if config.PingInterval == 0 {
		<-ctx.Done()
		return ctx.Err()
	}
	ticker := time.NewTicker(config.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, config.PongTimeout)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return err
			}
		}
	}
}

// StartPing launches a background goroutine that sends periodic pings and logs
// failures at info level. The goroutine exits when ctx is cancelled or a ping
// fails. If PingInterval is zero, no goroutine is started.
func StartPing(ctx context.Context, conn *websocket.Conn, logger *slog.Logger, config PingConfig) {
	if config.PingInterval == 0 {
		return
	}
	go func() {
		if err := Ping(ctx, conn, config); err != nil && ctx.Err() == nil {
			logger.InfoContext(ctx, "Ping timeout, tearing down connection",
				slog.Any("error", err),
			)
			_ = conn.CloseNow()
		}
	}()
}

// PingReceivedHandler returns an OnPingReceived callback that logs at debug
// level. The returned function has the signature expected by both
// websocket.AcceptOptions and websocket.DialOptions.
func PingReceivedHandler(logger *slog.Logger, ctx context.Context) func(context.Context, []byte) bool {
	return func(_ context.Context, payload []byte) bool {
		logger.DebugContext(ctx, "Ping received",
			slog.Int("payload_len", len(payload)),
		)
		return true
	}
}

// PongReceivedHandler returns an OnPongReceived callback that logs at debug
// level.
func PongReceivedHandler(logger *slog.Logger, ctx context.Context) func(context.Context, []byte) {
	return func(_ context.Context, payload []byte) {
		logger.DebugContext(ctx, "Pong received",
			slog.Int("payload_len", len(payload)),
		)
	}
}
