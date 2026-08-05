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
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	experiementalcredentials "google.golang.org/grpc/experimental/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/testing"
)

const testTicket = "test-ticket-value"

// testTLSConfig returns a tls.Config that trusts the self-signed localhost
// certificate used by testing.NewTLSServer. This avoids InsecureSkipVerify.
func testTLSConfig() *tls.Config {
	cert := testing.LocalhostCertificate()
	pool := x509.NewCertPool()
	pool.AddCert(must(x509.ParseCertificate(cert.Certificate[0])))
	return &tls.Config{
		RootCAs: pool,
	}
}

func must[T any](v T, err error) T {
	Expect(err).NotTo(HaveOccurred())
	return v
}

// sendProxyStatus sends a ConsoleStatus message on the proxy stream.
func sendProxyStatus(stream publicv1.ConsoleProxy_ConnectServer, state publicv1.ConsoleConnectionState, msg string) error {
	return stream.Send(publicv1.ConsoleProxyConnectResponse_builder{
		Status: publicv1.ConsoleStatus_builder{
			State:   state,
			Message: msg,
		}.Build(),
	}.Build())
}

// mockCreateSessionServer implements ConsoleSessions.Create, always returning a fixed ticket.
type mockCreateSessionServer struct {
	publicv1.UnimplementedConsoleSessionsServer
}

func (s *mockCreateSessionServer) Create(ctx context.Context, req *publicv1.ConsoleSessionsCreateRequest) (*publicv1.ConsoleSessionsCreateResponse, error) {
	return publicv1.ConsoleSessionsCreateResponse_builder{
		Object: publicv1.ConsoleSession_builder{
			Ticket:    testTicket,
			ExpiresAt: timestamppb.New(time.Now().Add(30 * time.Second)),
		}.Build(),
	}.Build(), nil
}

// startTestGRPCServer starts an in-process TLS gRPC server with both ConsoleSessions
// (for Create) and ConsoleProxy (for Connect) services.
func startTestGRPCServer(proxySrv publicv1.ConsoleProxyServer) (addr string, cleanup func()) {
	srv := testing.NewTLSServer()
	publicv1.RegisterConsoleSessionsServer(srv.Registrar(), &mockCreateSessionServer{})
	publicv1.RegisterConsoleProxyServer(srv.Registrar(), proxySrv)
	srv.Start()
	return srv.Address(), srv.Stop
}

// openTestStream starts a TLS gRPC server, connects a client, creates a session,
// opens a proxy stream with the ticket as a per-call credential, and waits for
// CONNECTED status. Returns the ready-to-use bidirectional stream.
func openTestStream(proxySrv publicv1.ConsoleProxyServer) (
	stream grpc.BidiStreamingClient[publicv1.ConsoleProxyConnectRequest, publicv1.ConsoleProxyConnectResponse],
	ctx context.Context,
	cancel context.CancelFunc,
	cleanup func(),
) {
	addr, grpcCleanup := startTestGRPCServer(proxySrv)

	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(experiementalcredentials.NewTLSWithALPNDisabled(testTLSConfig())),
	)
	Expect(err).NotTo(HaveOccurred())

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)

	// Create session to get ticket.
	consoleClient := publicv1.NewConsoleSessionsClient(conn)
	sessionResp, err := consoleClient.Create(ctx,
		publicv1.ConsoleSessionsCreateRequest_builder{
			Object: publicv1.ConsoleSession_builder{
				ResourceType: publicv1.ConsoleResourceType_CONSOLE_RESOURCE_TYPE_COMPUTE_INSTANCE,
				ResourceId:   "019dd9f6-0000-7000-8000-000000000001",
				Type:         publicv1.ConsoleType_CONSOLE_TYPE_VNC,
				ClientId:     "test-client",
			}.Build(),
		}.Build())
	Expect(err).NotTo(HaveOccurred())

	// Open proxy stream with ticket as per-call credential.
	proxyClient := publicv1.NewConsoleProxyClient(conn)
	stream, err = proxyClient.Connect(ctx, grpc.PerRPCCredentials(auth.NewTicketCredentials(sessionResp.GetObject().GetTicket())))
	Expect(err).NotTo(HaveOccurred())

	// Wait for CONNECTED status.
	resp, err := stream.Recv()
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.GetStatus().GetState()).To(Equal(
		publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_CONNECTED))

	cleanup = func() {
		cancel()
		conn.Close()
		grpcCleanup()
	}
	return
}

// drainHandler is a StreamHandler that reads from the stream until
// it receives a DISCONNECTED status or EOF.
func drainHandler(_ context.Context, _ context.CancelFunc, stream grpc.BidiStreamingClient[publicv1.ConsoleProxyConnectRequest, publicv1.ConsoleProxyConnectResponse]) error {
	for {
		resp, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if st := resp.GetStatus(); st != nil {
			if st.GetState() == publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_DISCONNECTED {
				return nil
			}
		}
	}
}

// testConsoleProxyServer implements ConsoleProxyServer for reconnect testing.
// It simulates transient failures for the first N connections.
type testConsoleProxyServer struct {
	publicv1.UnimplementedConsoleProxyServer
	connectCount atomic.Int32
	failFirst    int32
}

func (s *testConsoleProxyServer) Connect(stream publicv1.ConsoleProxy_ConnectServer) error {
	count := s.connectCount.Add(1)

	// Simulate transient failures for the first N connections.
	if count <= s.failFirst {
		return status.Error(codes.Unavailable, fmt.Sprintf("simulated failure %d", count))
	}

	if err := sendProxyStatus(stream, publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_CONNECTING, "Connecting..."); err != nil {
		return err
	}
	if err := sendProxyStatus(stream, publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_CONNECTED, "Connected"); err != nil {
		return err
	}

	// Send disconnect immediately so the client exits cleanly.
	if err := sendProxyStatus(stream, publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_DISCONNECTED, "Session ended by server"); err != nil {
		return err
	}

	return nil
}

// eofAfterConnectProxyServer sends CONNECTING + CONNECTED then returns (server-side stream close).
type eofAfterConnectProxyServer struct {
	publicv1.UnimplementedConsoleProxyServer
}

func (s *eofAfterConnectProxyServer) Connect(stream publicv1.ConsoleProxy_ConnectServer) error {
	if err := sendProxyStatus(stream, publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_CONNECTING, "Connecting..."); err != nil {
		return err
	}
	if err := sendProxyStatus(stream, publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_CONNECTED, "Connected"); err != nil {
		return err
	}

	// Return immediately -- this causes EOF on client's Recv().
	return nil
}

// echoConsoleProxyServer echoes data back through the console proxy stream.
type echoConsoleProxyServer struct {
	publicv1.UnimplementedConsoleProxyServer
	connectCount atomic.Int32
}

func (s *echoConsoleProxyServer) Connect(stream publicv1.ConsoleProxy_ConnectServer) error {
	s.connectCount.Add(1)

	if err := sendProxyStatus(stream, publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_CONNECTED, "Connected"); err != nil {
		return err
	}

	// Echo loop.
	for {
		req, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if input := req.GetInput(); input != nil {
			data := input.GetData()
			if len(data) > 0 {
				err := stream.Send(publicv1.ConsoleProxyConnectResponse_builder{
					Output: publicv1.ConsoleOutput_builder{
						Data: data,
					}.Build(),
				}.Build())
				if err != nil {
					return err
				}
			}
		}
	}
}

// disconnectAfterConnectProxyServer sends CONNECTED then DISCONNECTED immediately.
type disconnectAfterConnectProxyServer struct {
	publicv1.UnimplementedConsoleProxyServer
}

func (s *disconnectAfterConnectProxyServer) Connect(stream publicv1.ConsoleProxy_ConnectServer) error {
	if err := sendProxyStatus(stream, publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_CONNECTED, "Connected"); err != nil {
		return err
	}
	if err := sendProxyStatus(stream, publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_DISCONNECTED, "Session ended"); err != nil {
		return err
	}

	return nil
}
