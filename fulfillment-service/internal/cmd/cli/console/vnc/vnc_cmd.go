/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package vnc

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/console/connect"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/exit"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/terminal"
	"github.com/osac-project/fulfillment-service/internal/uuid"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
)

//go:embed templates
var templatesFS embed.FS

// Cmd returns the `console vnc` command.
func Cmd() *cobra.Command {
	result := &cobra.Command{
		Use:   "vnc",
		Short: "VNC console access",
	}
	result.AddCommand(computeInstanceCmd())
	return result
}

func computeInstanceCmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "computeinstance [FLAG...] ID|NAME",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.ExactArgs(1),
		RunE:                  runner.run,
	}

	flags := result.Flags()
	flags.DurationVar(
		&runner.args.timeout,
		"timeout",
		30*time.Minute,
		"Session timeout.",
	)
	flags.IntVar(
		&runner.args.port,
		"port",
		0,
		"TCP port for the local VNC proxy (0 = random).",
	)
	flags.BoolVar(
		&runner.args.proxyOnly,
		"proxy-only",
		false,
		"Start the proxy but do not launch a VNC viewer.",
	)
	flags.StringVar(
		&runner.args.viewer,
		"viewer",
		"",
		"VNC viewer binary (path or name on PATH).",
	)

	return result
}

type runnerContext struct {
	logger   *slog.Logger
	console  *terminal.Console
	conn     *grpc.ClientConn
	listener net.Listener
	v        *viewer
	session  *viewerSession
	args     struct {
		timeout   time.Duration
		port      int
		proxyOnly bool
		viewer    string
	}
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	key := args[0]

	c.logger = logging.LoggerFromContext(ctx)
	c.console = terminal.ConsoleFromContext(ctx)

	// Load the templates for the console messages:
	if err := c.console.AddTemplates(templatesFS, "templates"); err != nil {
		return fmt.Errorf("failed to load templates: %w", err)
	}

	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		c.console.Errorf(ctx, "Not logged in. Run 'osac login' first.\n")
		return exit.Error(1)
	}

	var err error
	c.conn, err = cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create connection: %w", err)
	}
	defer c.conn.Close()

	proxyConn, err := cfg.ConnectPlain(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create proxy connection: %w", err)
	}
	defer proxyConn.Close()

	instanceID, err := connect.ResolveInstance(ctx, c.conn, key)
	if err != nil {
		return err
	}

	if !c.args.proxyOnly {
		c.v, err = c.detectViewer(ctx, c.args.viewer)
		if err != nil {
			return err
		}
	}

	// The listener is long-lived and shared across retries.
	c.listener, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", c.args.port))
	if err != nil {
		return fmt.Errorf("failed to start VNC proxy listener: %w", err)
	}
	defer c.listener.Close()
	defer func() {
		if c.session != nil {
			c.session.close()
		}
	}()

	if c.args.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.args.timeout)
		defer cancel()
	}

	c.console.Render(ctx, "proxy_listening.txt", map[string]any{
		"Address": c.listener.Addr(),
	})

	opts := connect.Options{
		Logger:      c.logger,
		Console:     c.console,
		Conn:        c.conn,
		ProxyConn:   proxyConn,
		ClientID:    uuid.New(),
		InstanceID:  instanceID,
		ConsoleType: publicv1.ConsoleType_CONSOLE_TYPE_VNC,
		OnConnected: func(ctx context.Context) {
			c.console.Render(ctx, "connected.txt", map[string]any{
				"Instance": instanceID,
			})
		},
	}

	err = connect.WithRetry(ctx, opts, c.proxyVNC)
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		c.console.Render(ctx, "session_timeout.txt", map[string]any{
			"Timeout": c.args.timeout,
		})
		return nil
	case errors.Is(err, context.Canceled):
		err = nil
	}
	if err == nil && c.session != nil {
		if exitErr := c.session.ExitErr(); exitErr != nil {
			return fmt.Errorf("viewer exited with error: %w", exitErr)
		}
	}
	return err
}

// proxyVNC handles one VNC session per connection attempt. On each retry,
// it closes the previous viewer session and starts a fresh one, ensuring
// the RFB handshake is negotiated cleanly with the new backend.
func (c *runnerContext) proxyVNC(
	ctx context.Context,
	cancel context.CancelFunc,
	stream grpc.BidiStreamingClient[publicv1.ConsoleProxyConnectRequest, publicv1.ConsoleProxyConnectResponse],
) error {
	// Recycle: close previous viewer + TCP (no-op on first call).
	if c.session != nil {
		if c.session.close() {
			return nil // viewer was closed by the user during retry backoff
		}
	}

	c.session = newViewerSession(c.v, c.listener)
	if err := c.session.start(ctx); err != nil {
		return err
	}
	conn := c.session.Conn()
	if conn == nil {
		return nil // viewer exited before connecting
	}

	proxyErr := connect.Proxy(ctx, cancel, stream, connect.ProxyOptions{
		Reader:  conn,
		Writer:  conn,
		BufSize: 32 * 1024,
		InterruptRead: func() func() {
			if err := conn.(*net.TCPConn).SetReadDeadline(time.Now()); err != nil {
				c.logger.DebugContext(ctx, "SetReadDeadline failed", "error", err)
			}
			return func() {
				if err := conn.(*net.TCPConn).SetReadDeadline(time.Time{}); err != nil {
					c.logger.DebugContext(ctx, "SetReadDeadline reset failed", "error", err)
				}
			}
		},
		OnDisconnect: func(msg string) {
			c.logger.DebugContext(ctx, "VNC stream disconnected", "message", msg)
		},
	})
	// Viewer TCP is localhost — not a transient failure, exit cleanly.
	if errors.Is(proxyErr, connect.ErrLocalIOFailed) {
		return nil
	}
	return proxyErr
}

const shortHelp = "Access compute instance VNC console"

const longHelp = `
Open a VNC console session to a compute instance.

Starts a local TCP proxy and launches a VNC viewer. The proxy bridges
the local TCP socket to the server-side VNC stream via gRPC.

The instance can be specified by name or identifier.

If no viewer is found, use {{ bt }}--proxy-only{{ bt }} to start only the TCP proxy
and connect with your own VNC client.
`
