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
	"bytes"
	"context"
	"log/slog"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mockWSPath is the URL path that newMockWSServerWithHandler registers.
const mockWSPath = "/apis/console.osac.openshift.io/v1alpha1/namespaces/test/computeinstances/test/console"

// dialMockServer starts a mock WebSocket server with the given handler, dials it,
// and returns the client connection. Server and connection cleanup is automatic
// via DeferCleanup.
func dialMockServer(handler mockWSHandler) *websocket.Conn {
	GinkgoHelper()

	server, err := newMockWSServerWithHandler(handler)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	DeferCleanup(cancel)

	conn, _, err := websocket.Dial(ctx, "ws://"+server.Addr()+mockWSPath, nil)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = conn.CloseNow() })

	return conn
}

var _ = Describe("Ping", func() {
	It("should send pings and exit on context cancellation", func() {
		// Echo server has a Read loop, so pong replies are processed automatically.
		conn := dialMockServer(func(ctx context.Context, conn *websocket.Conn) {
			for {
				_, _, err := conn.Read(ctx)
				if err != nil {
					return
				}
			}
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// The concurrent reader needs its own context that outlives the
		// test context — coder/websocket closes the TCP connection when
		// the Read context is cancelled.
		readerCtx, readerCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer readerCancel()
		go func() {
			conn.Read(readerCtx)
		}()

		errCh := make(chan error, 1)
		go func() {
			errCh <- Ping(ctx, conn, PingConfig{
				PingInterval: 50 * time.Millisecond,
				PongTimeout:  500 * time.Millisecond,
			})
		}()

		// Ping should not return while pings are succeeding.
		Consistently(errCh, 150*time.Millisecond).ShouldNot(Receive())

		// Cancel and verify Ping exits promptly.
		cancel()
		Eventually(errCh, time.Second).Should(Receive())
	})

	It("should return error when server closes connection", func() {
		conn := dialMockServer(func(ctx context.Context, conn *websocket.Conn) {
			conn.Close(websocket.StatusNormalClosure, "")
		})

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// Concurrent reader required for Ping to process frames.
		readerCtx, readerCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer readerCancel()
		go func() {
			conn.Read(readerCtx)
		}()

		err := Ping(ctx, conn, PingConfig{
			PingInterval: 50 * time.Millisecond,
			PongTimeout:  500 * time.Millisecond,
		})
		Expect(err).To(HaveOccurred())
		Expect(err).NotTo(MatchError(context.Canceled))
		Expect(err).NotTo(MatchError(context.DeadlineExceeded))
	})

	It("should return error when pong times out", func() {
		// Server accepts but never reads — no pong will be sent back
		// because the library only sends automatic pong replies from
		// within a Read call.
		conn := dialMockServer(func(ctx context.Context, conn *websocket.Conn) {
			<-ctx.Done()
		})

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// Concurrent reader on client side so Ping can process incoming frames.
		readerCtx, readerCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer readerCancel()
		go func() {
			conn.Read(readerCtx)
		}()

		start := time.Now()
		err := Ping(ctx, conn, PingConfig{
			PingInterval: 50 * time.Millisecond,
			PongTimeout:  100 * time.Millisecond,
		})
		elapsed := time.Since(start)
		Expect(err).To(HaveOccurred())
		// Ping should have failed after roughly PingInterval + PongTimeout,
		// not after the 2s outer context timeout.
		Expect(elapsed).To(BeNumerically("<", time.Second))
		// The error should come from the pong timeout context, not the outer context.
		Expect(err).NotTo(MatchError(context.Canceled))
	})

	It("should block until context cancellation when interval is zero", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		conn := dialMockServer(func(ctx context.Context, conn *websocket.Conn) {
			for {
				_, _, err := conn.Read(ctx)
				if err != nil {
					return
				}
			}
		})

		err := Ping(ctx, conn, PingConfig{
			PingInterval: 0,
			PongTimeout:  500 * time.Millisecond,
		})
		Expect(err).To(MatchError(context.DeadlineExceeded))
	})

	It("should respect custom ping interval", func() {
		conn := dialMockServer(func(ctx context.Context, conn *websocket.Conn) {
			for {
				_, _, err := conn.Read(ctx)
				if err != nil {
					return
				}
			}
		})

		// Context expires before the first ping fires (interval=2s > timeout=200ms).
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		readerCtx, readerCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer readerCancel()
		go func() {
			conn.Read(readerCtx)
		}()

		err := Ping(ctx, conn, PingConfig{
			PingInterval: 2 * time.Second,
			PongTimeout:  500 * time.Millisecond,
		})
		Expect(err).To(MatchError(context.DeadlineExceeded))
	})
})

var _ = Describe("StartPing", func() {
	It("should close connection when ping fails", func() {
		// Server sends a message then closes — the ping goroutine will
		// detect the failure and call CloseNow on the client connection.
		conn := dialMockServer(func(ctx context.Context, conn *websocket.Conn) {
			conn.Write(ctx, websocket.MessageBinary, []byte("hello"))
			conn.Close(websocket.StatusNormalClosure, "")
		})

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// Drain the initial message so the server can proceed to close.
		_, _, err := conn.Read(ctx)
		Expect(err).NotTo(HaveOccurred())

		// Capture logs to verify StartPing hit the CloseNow code path,
		// not just that the connection happened to be broken already.
		var logBuf bytes.Buffer
		testLogger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

		StartPing(ctx, conn, testLogger, PingConfig{
			PingInterval: 50 * time.Millisecond,
			PongTimeout:  500 * time.Millisecond,
		})

		// StartPing should detect the dead connection and log the
		// teardown message, which only appears when CloseNow is called.
		Eventually(func() string {
			return logBuf.String()
		}, 2*time.Second, 50*time.Millisecond).Should(ContainSubstring("tearing down"))
	})

	It("should not start goroutine when interval is zero", func() {
		// Server closes immediately — if StartPing launched a goroutine,
		// the ping would fail and CloseNow would be called.
		conn := dialMockServer(func(ctx context.Context, conn *websocket.Conn) {
			conn.Close(websocket.StatusNormalClosure, "")
		})

		var logBuf bytes.Buffer
		testLogger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		StartPing(ctx, conn, testLogger, PingConfig{
			PingInterval: 0,
			PongTimeout:  100 * time.Millisecond,
		})

		// Give time for any hypothetical goroutine to detect the closed connection.
		time.Sleep(200 * time.Millisecond)

		Expect(logBuf.String()).NotTo(ContainSubstring("tearing down"))
	})

	It("should not close connection on context cancellation", func() {
		conn := dialMockServer(func(ctx context.Context, conn *websocket.Conn) {
			for {
				_, _, err := conn.Read(ctx)
				if err != nil {
					return
				}
			}
		})

		ctx, cancel := context.WithCancel(context.Background())

		// The concurrent reader needs a separate long-lived context so
		// it doesn't close the TCP connection when the test context is cancelled.
		readerCtx, readerCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer readerCancel()
		go func() {
			conn.Read(readerCtx)
		}()

		// Use a dedicated logger to capture StartPing output — it logs
		// "tearing down connection" only when it calls CloseNow.
		var logBuf bytes.Buffer
		testLogger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

		StartPing(ctx, conn, testLogger, PingConfig{
			PingInterval: 50 * time.Millisecond,
			PongTimeout:  500 * time.Millisecond,
		})

		// Let a few pings succeed.
		time.Sleep(200 * time.Millisecond)

		// Cancel context — StartPing should exit without calling CloseNow.
		cancel()

		// Wait for the goroutine to observe the cancellation and exit.
		time.Sleep(100 * time.Millisecond)

		// StartPing should NOT have logged the teardown message, which
		// only appears when CloseNow is called (ctx.Err() == nil path).
		Expect(logBuf.String()).NotTo(ContainSubstring("tearing down"))
	})
})
