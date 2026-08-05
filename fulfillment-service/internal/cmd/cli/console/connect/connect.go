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
	"fmt"
	"log/slog"
	"time"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/terminal"
	"google.golang.org/grpc"
)

// Options configures a console connection session.
type Options struct {
	Logger      *slog.Logger
	Console     *terminal.Console
	Conn        *grpc.ClientConn
	ProxyConn   *grpc.ClientConn
	ClientID    string
	InstanceID  string
	ConsoleType publicv1.ConsoleType
	// OnConnected is called after the server reports CONNECTED status,
	// before the handler is invoked. Use it to print connection banners.
	OnConnected func(ctx context.Context)
}

// StreamHandler is called after a successful connection is established.
// The handler owns the stream for bidirectional I/O proxying and receives
// a cancel function to tear down the stream context when it is done.
// Returning nil means clean disconnect; returning an error triggers retry logic.
type StreamHandler func(ctx context.Context, cancel context.CancelFunc, stream grpc.BidiStreamingClient[publicv1.ConsoleProxyConnectRequest, publicv1.ConsoleProxyConnectResponse]) error

// WithRetry runs the connection loop: create ticket, connect via proxy,
// wait for CONNECTED status, then delegate to the handler. On transient
// errors or connection loss, it retries with exponential backoff up to 5
// times. On permanent errors, it returns immediately.
func WithRetry(ctx context.Context, opts Options, handler StreamHandler) error {
	const maxConsecutiveRetries = 5
	consecutiveFailures := 0
	backoff := time.Second
	wasConnected := false

	spinner := terminal.NewSpinner(opts.Console.Stderr())
	spinner.Start(fmt.Sprintf("Connecting to %s...", opts.InstanceID))
	defer spinner.Stop()

	opts.OnConnected = withSpinnerStop(spinner, opts.OnConnected)

	for {
		err := connectOnce(ctx, opts, handler)
		if err == nil {
			// Clean disconnect (e.g., escape sequence). Done.
			return nil
		}

		spinner.Stop()

		// If the context expired (timeout or cancel), return immediately.
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Check if this is a permanent error (no retry).
		if IsPermanentError(err) {
			opts.Logger.DebugContext(ctx, "Permanent error, not retrying", "error", err)
			return fmt.Errorf("%s", UserFacingError(err))
		}

		// If we were connected and then lost the connection, reset the
		// retry counter -- the server was reachable, this is a new failure
		// sequence.
		if errors.Is(err, ErrConnectionLost) {
			opts.Logger.DebugContext(ctx, "Connection lost, resetting retry counter", "error", err)
			fmt.Fprintln(spinner.Writer())
			wasConnected = true
			consecutiveFailures = 0
			backoff = time.Second
		}

		consecutiveFailures++
		if consecutiveFailures > maxConsecutiveRetries {
			opts.Console.Errorf(ctx, "Gave up after %d consecutive failures.\n",
				maxConsecutiveRetries)
			return fmt.Errorf("%s", UserFacingError(err))
		}

		spinner.Start(retryMessage(wasConnected, opts.InstanceID, consecutiveFailures, maxConsecutiveRetries, backoff))

		opts.Logger.DebugContext(ctx, "Retrying after transient error",
			"error", err, "attempt", consecutiveFailures, "backoff", backoff)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		backoff = min(backoff*2, 30*time.Second)
	}
}

func retryMessage(wasConnected bool, instanceID string, attempt, max int, backoff time.Duration) string {
	secs := int(backoff.Seconds())
	switch {
	case wasConnected && attempt == 1: // first reconnect
		return "Connection lost, reconnecting..."
	case wasConnected: // subsequent reconnects
		return fmt.Sprintf("Connection lost, reconnecting in %ds (%d/%d)...", secs, attempt, max)
	case attempt == 1: // first connect
		return fmt.Sprintf("Connecting to %s...", instanceID)
	default: // subsequent connects
		return fmt.Sprintf("Connecting to %s, retrying in %ds (%d/%d)...", instanceID, secs, attempt, max)
	}
}

// getTicket requests a single-use console session ticket from the
// fulfillment-service. The ticket is a short-lived JWT that authorises
// a connection to the console proxy.
func getTicket(ctx context.Context, conn *grpc.ClientConn, opts Options) (string, error) {
	consoleClient := publicv1.NewConsoleSessionsClient(conn)
	sessionResp, err := consoleClient.Create(ctx,
		publicv1.ConsoleSessionsCreateRequest_builder{
			Object: publicv1.ConsoleSession_builder{
				ResourceType: publicv1.ConsoleResourceType_CONSOLE_RESOURCE_TYPE_COMPUTE_INSTANCE,
				ResourceId:   opts.InstanceID,
				Type:         opts.ConsoleType,
				ClientId:     opts.ClientID,
			}.Build(),
		}.Build())
	if err != nil {
		return "", fmt.Errorf("failed to create console session: %w", err)
	}
	ticket := sessionResp.GetObject().GetTicket()
	if ticket == "" {
		return "", fmt.Errorf("server returned empty console session ticket")
	}
	return ticket, nil
}

// connectOnce obtains a fresh ticket, then opens a single gRPC proxy stream
// with the ticket as a per-call credential, waits for CONNECTED status, then delegates to
// the handler. Each attempt gets its own context so that when it returns, the
// gRPC stream is explicitly cancelled.
func connectOnce(ctx context.Context, opts Options, handler StreamHandler) error {
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	ticket, err := getTicket(streamCtx, opts.Conn, opts)
	if err != nil {
		return err
	}

	// Connect to the console proxy with the ticket as a per-call credential.
	proxyConn := opts.ProxyConn
	if proxyConn == nil {
		proxyConn = opts.Conn
	}
	proxyClient := publicv1.NewConsoleProxyClient(proxyConn)

	stream, err := proxyClient.Connect(streamCtx, grpc.PerRPCCredentials(auth.NewTicketCredentials(ticket)))
	if err != nil {
		return fmt.Errorf("failed to open console proxy stream: %w", err)
	}

	// Wait for connected status.
	if err := waitForConnected(streamCtx, stream); err != nil {
		return err
	}

	opts.Logger.DebugContext(streamCtx, "Console stream established", "instance", opts.InstanceID)

	if opts.OnConnected != nil {
		opts.OnConnected(ctx)
	}

	err = handler(streamCtx, streamCancel, stream)
	if err != nil {
		// We were connected and then lost the connection.
		return errors.Join(ErrConnectionLost, err)
	}
	return nil
}

func withSpinnerStop(spinner *terminal.Spinner, fn func(context.Context)) func(context.Context) {
	return func(ctx context.Context) {
		spinner.Stop()
		if fn != nil {
			fn(ctx)
		}
	}
}

// waitForConnected receives status messages until the server reports CONNECTED.
func waitForConnected(ctx context.Context,
	stream grpc.BidiStreamingClient[publicv1.ConsoleProxyConnectRequest, publicv1.ConsoleProxyConnectResponse]) error {
	for {
		resp, err := stream.Recv()
		if err != nil {
			return err
		}

		if st := resp.GetStatus(); st != nil {
			switch st.GetState() {
			case publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_CONNECTED:
				return nil
			case publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_CONNECTING:
			case publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_DISCONNECTED:
				return fmt.Errorf("server disconnected: %s", st.GetMessage())
			case publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_ERROR:
				return fmt.Errorf("server error: %s", st.GetMessage())
			}
		}
	}
}
