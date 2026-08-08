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
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/osac-project/osac/fulfillment-service/internal/console"
)

// consoleContextKey is the type used to pass console metadata through context.
type consoleContextKey int

const consoleStateKey consoleContextKey = 0

// consoleSessionState passes session metadata from the inner WS handler back
// to ConsoleMetrics via request context. Because the handler upgrades to 101
// before validating the ticket, a 101 alone does not mean success -- the
// handler must explicitly set established.
type consoleSessionState struct {
	consoleType string
	established bool
}

// initConsoleState returns a context with a fresh consoleSessionState.
func initConsoleState(ctx context.Context) context.Context {
	return context.WithValue(ctx, consoleStateKey, &consoleSessionState{})
}

// setConsoleType stores the console type in the state already present in ctx.
func setConsoleType(ctx context.Context, ct string) {
	if s, ok := ctx.Value(consoleStateKey).(*consoleSessionState); ok {
		s.consoleType = ct
	}
}

// consoleTypeFromContext extracts the console type from context, or "" if absent.
func consoleTypeFromContext(ctx context.Context) string {
	if s, ok := ctx.Value(consoleStateKey).(*consoleSessionState); ok {
		return s.consoleType
	}
	return ""
}

// setSessionEstablished marks the state already present in ctx as established.
func setSessionEstablished(ctx context.Context) {
	if s, ok := ctx.Value(consoleStateKey).(*consoleSessionState); ok {
		s.established = true
	}
}

// sessionEstablishedFromContext reports whether the session was established,
// or false if absent.
func sessionEstablishedFromContext(ctx context.Context) bool {
	if s, ok := ctx.Value(consoleStateKey).(*consoleSessionState); ok {
		return s.established
	}
	return false
}

// validConsoleTypes is the set of known console type values for metrics labeling.
var validConsoleTypes = map[string]bool{
	console.ConsoleTypeVNC:    true,
	console.ConsoleTypeSerial: true,
}

// ConsolePanicRecovery wraps an http.Handler with panic recovery, logging the panic
// and returning HTTP 500. Same role as panicInterceptor in the gRPC chain.
func ConsolePanicRecovery(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.ErrorContext(r.Context(), "Console handler panicked",
					slog.Any("panic", rec),
					slog.String("stack", string(debug.Stack())),
				)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ConsoleLogging wraps an http.Handler with structured request logging.
// It logs the sanitized path (without query string) to avoid token exposure.
func ConsoleLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Sanitize the path to strip query parameters (may contain token).
		cleanPath := r.URL.Path

		logger.InfoContext(r.Context(), "Console request started",
			slog.String("method", r.Method),
			slog.String("path", cleanPath),
		)

		next.ServeHTTP(w, r)

		logger.InfoContext(r.Context(), "Console request completed",
			slog.String("method", r.Method),
			slog.String("path", cleanPath),
			slog.Duration("duration", time.Since(start)),
		)
	})
}

// consoleMetricsMiddleware wraps an http.Handler with Prometheus connection metrics.
type consoleMetricsMiddleware struct {
	next         http.Handler
	connectTotal *prometheus.CounterVec
	activeConns  prometheus.Gauge
	connDuration *prometheus.HistogramVec
}

// ConsoleMetrics creates a middleware that records console WebSocket connection metrics.
// The console_type label on connectTotal and connDuration is set by the inner handler
// via setConsoleType after ticket verification and read back here via consoleTypeFromContext.
// activeConns is unlabeled because the type is unknown until after ticket verification.
func ConsoleMetrics(registerer prometheus.Registerer, next http.Handler) http.Handler {
	connectTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: "console_websocket",
			Name:      "connections_total",
			Help:      "Total number of console WebSocket connections.",
		},
		[]string{"console_type", "status"},
	)
	activeConns := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Subsystem: "console_websocket",
			Name:      "active_connections",
			Help:      "Number of active console WebSocket connections.",
		},
	)
	connDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem: "console_websocket",
			Name:      "connection_duration_seconds",
			Help:      "Duration of console WebSocket connections.",
			Buckets:   prometheus.ExponentialBuckets(1, 2, 15),
		},
		[]string{"console_type"},
	)

	registerer.MustRegister(connectTotal, activeConns, connDuration)

	return &consoleMetricsMiddleware{
		next:         next,
		connectTotal: connectTotal,
		activeConns:  activeConns,
		connDuration: connDuration,
	}
}

func (m *consoleMetricsMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	m.activeConns.Inc()
	defer m.activeConns.Dec()

	r = r.WithContext(initConsoleState(r.Context()))
	m.next.ServeHTTP(w, r)

	// The handler sets console_type via context after ticket verification.
	consoleType := consoleTypeFromContext(r.Context())
	if !validConsoleTypes[consoleType] {
		consoleType = "unknown"
	}

	// established is set only after a successful backend connection (see
	// setSessionEstablished), so it already implies success without a
	// separate HTTP status check.
	if sessionEstablishedFromContext(r.Context()) {
		m.connDuration.WithLabelValues(consoleType).Observe(time.Since(start).Seconds())
		m.connectTotal.WithLabelValues(consoleType, "success").Inc()
	} else {
		m.connectTotal.WithLabelValues(consoleType, "error").Inc()
	}
}
