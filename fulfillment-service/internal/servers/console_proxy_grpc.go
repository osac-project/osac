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
	"io"
	"log/slog"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/console"
)

// consoleProxyServer is the gRPC service implementation for ConsoleProxy.Connect.
type consoleProxyServer struct {
	publicv1.UnimplementedConsoleProxyServer
	core *ConsoleProxyCore
}

// NewConsoleProxyServer creates a new gRPC console proxy server.
func NewConsoleProxyServer(core *ConsoleProxyCore) publicv1.ConsoleProxyServer {
	return &consoleProxyServer{core: core}
}

// Connect handles a bidirectional console proxy stream. The ticket is extracted
// from the Authorization header in gRPC metadata (Bearer scheme), verified before
// the first Recv(). The CLI uses a separate gRPC connection without channel-level
// credentials for this service, passing the ticket as a per-call PerRPCCredentials
// so the Authorization header carries only the console ticket.
func (s *consoleProxyServer) Connect(stream publicv1.ConsoleProxy_ConnectServer) error {
	ctx := stream.Context()

	// Extract ticket from Authorization metadata.
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Errorf(codes.Unauthenticated, "missing metadata")
	}
	authValues := md.Get("authorization")
	if len(authValues) == 0 {
		return status.Errorf(codes.Unauthenticated, "missing authorization metadata")
	}
	rawTicket, err := ExtractBearerToken(authValues[0])
	if err != nil {
		return status.Errorf(codes.Unauthenticated, "%s", err.Error())
	}

	// Open ticket (atomically consumes JTI).
	ticket, err := s.core.OpenTicket(ctx, rawTicket)
	if err != nil {
		return status.Errorf(codes.Unauthenticated, "invalid ticket: %v", err)
	}

	// Send CONNECTING status.
	if err := sendProxyStatus(stream, publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_CONNECTING,
		"Connecting..."); err != nil {
		return err
	}

	// Connect backend.
	backend, sessionCtx, err := s.core.ConnectBackend(ctx, ticket)
	if err != nil {
		if sendErr := sendProxyStatus(stream, publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_ERROR,
			"failed to connect to console backend"); sendErr != nil {
			s.core.logger.ErrorContext(ctx, "Failed to send error status", slog.Any("error", sendErr))
		}
		s.core.logger.ErrorContext(ctx, "Failed to connect backend", slog.Any("error", err))
		var sessionErr *console.ErrSessionExists
		if errors.As(err, &sessionErr) {
			return status.Errorf(codes.FailedPrecondition, "console session already active")
		}
		return status.Errorf(codes.Unavailable, "failed to connect to console backend")
	}

	// Send CONNECTED status. If this fails, close the backend to avoid
	// leaking the session — Relay won't run to call its own defer.
	if err := sendProxyStatus(stream, publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_CONNECTED,
		"Connected"); err != nil {
		backend.Close()
		return err
	}

	// Relay with sessionCtx: cancelled on eviction, timeout, or client disconnect.
	return s.core.Relay(sessionCtx, newGrpcStreamRW(stream), backend)
}

// sendProxyStatus sends a ConsoleStatus message on the gRPC stream.
func sendProxyStatus(stream publicv1.ConsoleProxy_ConnectServer, state publicv1.ConsoleConnectionState, message string) error {
	return stream.Send(publicv1.ConsoleProxyConnectResponse_builder{
		Status: publicv1.ConsoleStatus_builder{
			State:   state,
			Message: message,
		}.Build(),
	}.Build())
}

// grpcStreamRW wraps a ConsoleProxy_ConnectServer as io.ReadWriteCloser.
// A single long-lived goroutine calls Recv() and feeds results into recvCh.
// Read drains from that channel. Close signals done to unblock both Read and
// the recv goroutine. The recv goroutine cannot exit immediately when done is
// closed if it is blocked inside Recv() — it exits once the gRPC handler
// returns and the framework cancels the stream context.
type grpcStreamRW struct {
	stream publicv1.ConsoleProxy_ConnectServer
	buf    []byte          // leftover from partial Read
	recvCh chan recvResult // fed by recvLoop
	done   chan struct{}
	once   sync.Once
}

type recvResult struct {
	data []byte
	err  error
}

func newGrpcStreamRW(stream publicv1.ConsoleProxy_ConnectServer) *grpcStreamRW {
	rw := &grpcStreamRW{
		stream: stream,
		recvCh: make(chan recvResult, 1),
		done:   make(chan struct{}),
	}
	go rw.recvLoop()
	return rw
}

// recvLoop reads from the gRPC stream until an error or Close().
func (rw *grpcStreamRW) recvLoop() {
	for {
		req, err := rw.stream.Recv()
		if err != nil {
			select {
			case rw.recvCh <- recvResult{nil, err}:
			case <-rw.done:
			}
			return
		}
		data := req.GetInput().GetData()
		if len(data) == 0 {
			continue
		}
		select {
		case rw.recvCh <- recvResult{data, nil}:
		case <-rw.done:
			return
		}
	}
}

func (rw *grpcStreamRW) Read(p []byte) (int, error) {
	if len(rw.buf) > 0 {
		n := copy(p, rw.buf)
		rw.buf = rw.buf[n:]
		return n, nil
	}

	select {
	case <-rw.done:
		return 0, io.ErrClosedPipe
	case r := <-rw.recvCh:
		if r.err != nil {
			return 0, r.err
		}
		n := copy(p, r.data)
		if n < len(r.data) {
			rw.buf = r.data[n:]
		}
		return n, nil
	}
}

func (rw *grpcStreamRW) Write(p []byte) (int, error) {
	err := rw.stream.Send(publicv1.ConsoleProxyConnectResponse_builder{
		Output: publicv1.ConsoleOutput_builder{
			Data: p,
		}.Build(),
	}.Build())
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (rw *grpcStreamRW) Close() error {
	rw.once.Do(func() { close(rw.done) })
	return nil
}

// Verify interface compliance.
var _ io.ReadWriteCloser = (*grpcStreamRW)(nil)
