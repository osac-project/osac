/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package connect

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	experiementalcredentials "google.golang.org/grpc/experimental/credentials"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

func TestConnect(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Console connect")
}

// Logger used for tests:
var (
	logger  *slog.Logger
	console *terminal.Console
)

var _ = BeforeSuite(func() {
	// Create a logger that writes to the Ginkgo writer, so that the log messages will be attached to the output of
	// the right test:
	options := &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}
	handler := slog.NewTextHandler(GinkgoWriter, options)
	logger = slog.New(handler)

	// Same for the console:
	var err error
	console, err = terminal.NewConsole().
		SetLogger(logger).
		SetStdout(GinkgoWriter).
		SetStderr(GinkgoWriter).
		Build()
	Expect(err).NotTo(HaveOccurred())
})

func makeOpts(conn *grpc.ClientConn) Options {
	return Options{
		Logger:      logger,
		Console:     console,
		Conn:        conn,
		ClientID:    "test-client",
		InstanceID:  "test-vm",
		ConsoleType: publicv1.ConsoleType_CONSOLE_TYPE_SERIAL,
	}
}

var _ = Describe("Auto-reconnect", func() {
	It("should classify transient server errors for retry", func() {
		testSrv := &testConsoleProxyServer{failFirst: 2}
		addr, cleanup := startTestGRPCServer(testSrv)
		DeferCleanup(cleanup)

		conn, err := grpc.NewClient(addr,
			grpc.WithTransportCredentials(experiementalcredentials.NewTLSWithALPNDisabled(testTLSConfig())),
		)
		Expect(err).NotTo(HaveOccurred())
		defer conn.Close()

		opts := makeOpts(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// First call: proxy returns Unavailable (transient).
		err = connectOnce(ctx, opts, drainHandler)
		Expect(err).To(HaveOccurred())
		Expect(IsPermanentError(err)).To(BeFalse(), "Unavailable should be transient")
		Expect(testSrv.connectCount.Load()).To(Equal(int32(1)))

		// Second call: proxy returns Unavailable again.
		err = connectOnce(ctx, opts, drainHandler)
		Expect(err).To(HaveOccurred())
		Expect(IsPermanentError(err)).To(BeFalse())
		Expect(testSrv.connectCount.Load()).To(Equal(int32(2)))

		// Third call: proxy succeeds (sends CONNECTED then DISCONNECTED, exits cleanly).
		err = connectOnce(ctx, opts, drainHandler)
		// Server sends DISCONNECTED, drainHandler returns nil.
		Expect(err).To(SatisfyAny(BeNil(), MatchError(ErrConnectionLost)))
		Expect(testSrv.connectCount.Load()).To(Equal(int32(3)))
	})

	It("should not retry on permanent errors", func() {
		permanentSrv := &publicv1.UnimplementedConsoleProxyServer{}
		addr, cleanup := startTestGRPCServer(permanentSrv)
		DeferCleanup(cleanup)

		conn, err := grpc.NewClient(addr,
			grpc.WithTransportCredentials(experiementalcredentials.NewTLSWithALPNDisabled(testTLSConfig())),
		)
		Expect(err).NotTo(HaveOccurred())
		defer conn.Close()

		opts := makeOpts(conn)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err = connectOnce(ctx, opts, drainHandler)
		Expect(err).To(HaveOccurred())
		Expect(IsPermanentError(err)).To(BeTrue(), "Unimplemented should be permanent")
	})

	It("should exercise the full WithRetry path", func() {
		// Server fails first 2, succeeds on 3rd.
		testSrv := &testConsoleProxyServer{failFirst: 2}
		addr, cleanup := startTestGRPCServer(testSrv)
		DeferCleanup(cleanup)

		conn, err := grpc.NewClient(addr,
			grpc.WithTransportCredentials(experiementalcredentials.NewTLSWithALPNDisabled(testTLSConfig())),
		)
		Expect(err).NotTo(HaveOccurred())
		defer conn.Close()

		opts := makeOpts(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// WithRetry should retry the transient failures and eventually succeed.
		err = WithRetry(ctx, opts, drainHandler)
		// Should complete -- either nil or a clean exit from the DISCONNECTED status.
		Expect(err).To(SatisfyAny(BeNil(), MatchError(ErrConnectionLost)))
		Expect(testSrv.connectCount.Load()).To(Equal(int32(3)))
	})
})

// Verify connectOnce handles EOF from a stream that sends only init response then closes.
var _ = Describe("connectOnce edge cases", func() {
	It("should handle stream EOF after connected status", func() {
		// Server that connects then immediately returns (EOF on stream).
		eofSrv := &eofAfterConnectProxyServer{}
		addr, cleanup := startTestGRPCServer(eofSrv)
		DeferCleanup(cleanup)

		conn, err := grpc.NewClient(addr,
			grpc.WithTransportCredentials(experiementalcredentials.NewTLSWithALPNDisabled(testTLSConfig())),
		)
		Expect(err).NotTo(HaveOccurred())
		defer conn.Close()

		opts := makeOpts(conn)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Should handle EOF gracefully (return nil or io.EOF wrapped error).
		err = connectOnce(ctx, opts, drainHandler)
		// EOF from server = clean disconnect.
		Expect(err).To(SatisfyAny(BeNil(), MatchError(ErrConnectionLost)))
	})
})

var _ = Describe("Proxy", func() {
	It("should bridge data bidirectionally", func() {
		stream, ctx, cancel, cleanup := openTestStream(&echoConsoleProxyServer{})
		defer cleanup()

		// Simulate local connection with a pipe.
		pipeServer, pipeClient := net.Pipe()
		defer pipeServer.Close()
		defer pipeClient.Close()

		bridgeDone := make(chan error, 1)
		go func() {
			bridgeDone <- Proxy(ctx, cancel, stream, ProxyOptions{
				Reader: pipeServer,
				Writer: pipeServer,
				InterruptRead: func() func() {
					pipeServer.SetReadDeadline(time.Now())
					return func() { pipeServer.SetReadDeadline(time.Time{}) }
				},
			})
		}()

		testData := []byte("hello VNC")
		_, err := pipeClient.Write(testData)
		Expect(err).NotTo(HaveOccurred())

		buf := make([]byte, 256)
		pipeClient.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := pipeClient.Read(buf)
		Expect(err).NotTo(HaveOccurred())
		Expect(buf[:n]).To(Equal(testData))

		testData2 := []byte("more data")
		_, err = pipeClient.Write(testData2)
		Expect(err).NotTo(HaveOccurred())

		pipeClient.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err = pipeClient.Read(buf)
		Expect(err).NotTo(HaveOccurred())
		Expect(buf[:n]).To(Equal(testData2))

		// Close client side -- bridge should exit cleanly.
		pipeClient.Close()

		Eventually(bridgeDone).WithTimeout(2 * time.Second).Should(Receive(BeNil()))
	})

	It("should handle server disconnect status", func() {
		stream, ctx, cancel, cleanup := openTestStream(&disconnectAfterConnectProxyServer{})
		defer cleanup()

		pipeServer, pipeClient := net.Pipe()
		defer pipeServer.Close()
		defer pipeClient.Close()

		err := Proxy(ctx, cancel, stream, ProxyOptions{
			Reader: pipeServer,
			Writer: pipeServer,
			InterruptRead: func() func() {
				pipeServer.SetReadDeadline(time.Now())
				return func() { pipeServer.SetReadDeadline(time.Time{}) }
			},
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("should return ErrLocalIOFailed when the local side breaks", func() {
		stream, ctx, cancel, cleanup := openTestStream(&echoConsoleProxyServer{})
		defer cleanup()

		// Use io.Pipe so CloseWithError can inject a non-EOF error,
		// simulating a TCP connection reset from a crashed viewer.
		// (net.Pipe Close produces EOF, which Proxy treats as clean shutdown.)
		pr, pw := io.Pipe()
		defer pr.Close()

		bridgeDone := make(chan error, 1)
		go func() {
			bridgeDone <- Proxy(ctx, cancel, stream, ProxyOptions{
				Reader: pr,
				Writer: io.Discard,
				InterruptRead: func() func() {
					pr.Close()
					return func() {}
				},
			})
		}()

		// Simulate viewer crash: inject a non-EOF read error.
		pw.CloseWithError(errors.New("connection reset by peer"))

		var proxyErr error
		Eventually(bridgeDone).WithTimeout(2 * time.Second).Should(Receive(&proxyErr))
		Expect(errors.Is(proxyErr, ErrLocalIOFailed)).To(BeTrue(),
			"expected ErrLocalIOFailed, got: %v", proxyErr)
	})
})
