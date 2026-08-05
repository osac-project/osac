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
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/osac-project/fulfillment-service/internal/console"
)

// consoleContextKey is the type used to pass console metadata through context.
type consoleContextKey int

const (
	consoleTypeKey consoleContextKey = iota
)

// consoleTypeHolder is a mutable container injected into the request context
// by ConsoleMetrics. The inner handler sets the value after ticket verification
// via setConsoleType, and ConsoleMetrics reads it after the handler returns.
type consoleTypeHolder struct {
	value string
}

// initConsoleType returns a context with an empty consoleTypeHolder.
func initConsoleType(ctx context.Context) context.Context {
	return context.WithValue(ctx, consoleTypeKey, &consoleTypeHolder{})
}

// setConsoleType stores the console type in the holder already present in ctx.
func setConsoleType(ctx context.Context, ct string) {
	if h, ok := ctx.Value(consoleTypeKey).(*consoleTypeHolder); ok {
		h.value = ct
	}
}

// consoleTypeFromContext extracts the console type from context, or "" if absent.
func consoleTypeFromContext(ctx context.Context) string {
	if h, ok := ctx.Value(consoleTypeKey).(*consoleTypeHolder); ok {
		return h.value
	}
	return ""
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
// Status code is not captured here — ConsoleMetrics handles that.
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

// statusWriter captures the HTTP status code written by the handler.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

// Hijack delegates to the underlying ResponseWriter so that WebSocket upgrades work
// when the writer is wrapped by logging or metrics middleware.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("underlying ResponseWriter does not support hijacking")
	}
	return h.Hijack()
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

	r = r.WithContext(initConsoleType(r.Context()))
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	m.next.ServeHTTP(sw, r)

	// The handler sets console_type via context after ticket verification.
	consoleType := consoleTypeFromContext(r.Context())
	if !validConsoleTypes[consoleType] {
		consoleType = "unknown"
	}

	// coder/websocket writes 101 Switching Protocols before hijacking the
	// connection, so a successful WebSocket upgrade is detected by the status
	// code. Pre-upgrade failures (401/403/502) call http.Error which sets a
	// different status code.
	upgraded := sw.status == http.StatusSwitchingProtocols
	if upgraded {
		m.connDuration.WithLabelValues(consoleType).Observe(time.Since(start).Seconds())
		m.connectTotal.WithLabelValues(consoleType, "success").Inc()
	} else {
		m.connectTotal.WithLabelValues(consoleType, "error").Inc()
	}
}
