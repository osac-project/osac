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
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	osactesting "github.com/osac-project/fulfillment-service/internal/testing"
)

var _ = Describe("ConsoleMetrics middleware", func() {
	var (
		metricsServer *osactesting.MetricsServer
	)

	BeforeEach(func() {
		metricsServer = osactesting.NewMetricsServer()
	})

	AfterEach(func() {
		metricsServer.Close()
	})

	Describe("error vs upgrade detection on a hijackable server", func() {
		It("should count http.Error as error even when Hijack is available", func() {
			handler := ConsoleMetrics(metricsServer.Registry(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
			}))

			srv := httptest.NewServer(handler)
			defer srv.Close()

			resp, err := http.Get(srv.URL)
			Expect(err).NotTo(HaveOccurred())
			resp.Body.Close()

			metrics := metricsServer.Metrics()
			Expect(metrics).To(osactesting.MatchLine(`console_websocket_connections_total\{.*status="error".*\} 1`))
			Expect(metrics).NotTo(osactesting.MatchLine(`console_websocket_connection_duration_seconds_count`))
		})

		It("should count 101 Switching Protocols as success", func() {
			handler := ConsoleMetrics(metricsServer.Registry(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusSwitchingProtocols)
				conn, _, err := w.(http.Hijacker).Hijack()
				Expect(err).NotTo(HaveOccurred())
				conn.Close()
			}))

			srv := httptest.NewServer(handler)
			defer srv.Close()

			resp, err := http.Get(srv.URL)
			if err == nil {
				resp.Body.Close()
			}

			metrics := metricsServer.Metrics()
			Expect(metrics).To(osactesting.MatchLine(`console_websocket_connections_total\{.*status="success".*\} 1`))
			Expect(metrics).To(osactesting.MatchLine(`console_websocket_connection_duration_seconds_count\{.*\} 1`))
		})

		It("should distinguish errors from upgrades in the same server", func() {
			var callCount atomic.Int32
			handler := ConsoleMetrics(metricsServer.Registry(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if callCount.Add(1) == 1 {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				w.WriteHeader(http.StatusSwitchingProtocols)
				conn, _, err := w.(http.Hijacker).Hijack()
				Expect(err).NotTo(HaveOccurred())
				conn.Close()
			}))

			srv := httptest.NewServer(handler)
			defer srv.Close()

			// First request: error path.
			resp, err := http.Get(srv.URL)
			Expect(err).NotTo(HaveOccurred())
			resp.Body.Close()

			// Second request: hijack path.
			resp, err = http.Get(srv.URL)
			if err == nil {
				resp.Body.Close()
			}

			metrics := metricsServer.Metrics()
			Expect(metrics).To(osactesting.MatchLine(`console_websocket_connections_total\{.*status="error".*\} 1`))
			Expect(metrics).To(osactesting.MatchLine(`console_websocket_connections_total\{.*status="success".*\} 1`))
			Expect(metrics).To(osactesting.MatchLine(`console_websocket_connection_duration_seconds_count\{.*\} 1`))
		})
	})

	Describe("activeConns gauge during connection", func() {
		It("should be >0 while handler is running and 0 after", func() {
			var gaugeObserved atomic.Bool
			handlerStarted := make(chan struct{})
			handlerRelease := make(chan struct{})

			handler := ConsoleMetrics(metricsServer.Registry(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(handlerStarted)
				<-handlerRelease
				http.Error(w, "done", http.StatusUnauthorized)
			}))

			srv := httptest.NewServer(handler)
			defer srv.Close()

			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp, err := http.Get(srv.URL)
				if err == nil {
					resp.Body.Close()
				}
			}()

			// Wait for the handler to be in-flight, then check gauge.
			<-handlerStarted
			metrics := metricsServer.Metrics()
			for _, line := range metrics {
				if line == "console_websocket_active_connections 1" {
					gaugeObserved.Store(true)
				}
			}
			Expect(gaugeObserved.Load()).To(BeTrue(), "activeConns should be 1 while handler is running")

			// Release the handler and wait for completion.
			close(handlerRelease)
			wg.Wait()

			metrics = metricsServer.Metrics()
			Expect(metrics).To(osactesting.MatchLine(`console_websocket_active_connections 0`))
		})
	})

	Describe("console_type label", func() {
		It("should use the type set by the inner handler", func() {
			handler := ConsoleMetrics(metricsServer.Registry(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				setConsoleType(r.Context(), "vnc")
				w.WriteHeader(http.StatusSwitchingProtocols)
				conn, _, err := w.(http.Hijacker).Hijack()
				Expect(err).NotTo(HaveOccurred())
				conn.Close()
			}))

			srv := httptest.NewServer(handler)
			defer srv.Close()

			resp, err := http.Get(srv.URL)
			if err == nil {
				resp.Body.Close()
			}

			metrics := metricsServer.Metrics()
			Expect(metrics).To(osactesting.MatchLine(`console_websocket_connections_total\{console_type="vnc",status="success"\} 1`))
			Expect(metrics).To(osactesting.MatchLine(`console_websocket_connection_duration_seconds_count\{console_type="vnc"\} 1`))
		})

		It("should default to 'unknown' when not set", func() {
			handler := ConsoleMetrics(metricsServer.Registry(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
			}))

			srv := httptest.NewServer(handler)
			defer srv.Close()

			resp, err := http.Get(srv.URL)
			Expect(err).NotTo(HaveOccurred())
			resp.Body.Close()

			metrics := metricsServer.Metrics()
			Expect(metrics).To(osactesting.MatchLine(`console_websocket_connections_total\{console_type="unknown",status="error"\} 1`))
		})

		It("should default to 'unknown' for invalid console type", func() {
			handler := ConsoleMetrics(metricsServer.Registry(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				setConsoleType(r.Context(), "invalid-type")
				http.Error(w, "bad gateway", http.StatusBadGateway)
			}))

			srv := httptest.NewServer(handler)
			defer srv.Close()

			resp, err := http.Get(srv.URL)
			Expect(err).NotTo(HaveOccurred())
			resp.Body.Close()

			metrics := metricsServer.Metrics()
			Expect(metrics).To(osactesting.MatchLine(`console_websocket_connections_total\{console_type="unknown",status="error"\} 1`))
		})
	})

	Describe("real WebSocket upgrade", func() {
		It("should detect coder/websocket upgrade as success", func() {
			wsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				setConsoleType(r.Context(), "serial")
				ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
					InsecureSkipVerify: true,
				})
				Expect(err).NotTo(HaveOccurred())
				ws.CloseNow()
			})
			handler := ConsoleMetrics(metricsServer.Registry(), wsHandler)

			srv := httptest.NewServer(handler)
			defer srv.Close()

			wsURL := "ws" + srv.URL[len("http"):]
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ws, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{})
			Expect(err).NotTo(HaveOccurred())
			ws.CloseNow()

			Eventually(func() []string {
				return metricsServer.Metrics()
			}, 2*time.Second, 50*time.Millisecond).Should(osactesting.MatchLine(
				`console_websocket_connections_total\{.*status="success".*\} 1`,
			))

			metrics := metricsServer.Metrics()
			Expect(metrics).To(osactesting.MatchLine(`console_websocket_connection_duration_seconds_count\{.*\} 1`))
			Expect(metrics).To(osactesting.MatchLine(`console_websocket_active_connections 0`))
		})
	})
})
