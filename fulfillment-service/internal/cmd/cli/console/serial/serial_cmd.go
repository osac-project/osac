/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package serial

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/muesli/cancelreader"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/console/connect"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/exit"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/terminal"
	"github.com/osac-project/fulfillment-service/internal/uuid"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"google.golang.org/grpc"
)

// Cmd returns the `console serial` command.
func Cmd() *cobra.Command {
	result := &cobra.Command{
		Use:   "serial",
		Short: "Serial console access",
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
		timeoutFlagHelp,
	)

	return result
}

type runnerContext struct {
	logger  *slog.Logger
	console *terminal.Console
	conn    *grpc.ClientConn
	args    struct {
		timeout time.Duration
	}
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	key := args[0]

	c.logger = logging.LoggerFromContext(ctx)
	c.console = terminal.ConsoleFromContext(ctx)

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

	if c.args.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.args.timeout)
		defer cancel()
	}

	opts := connect.Options{
		Logger:      c.logger,
		Console:     c.console,
		Conn:        c.conn,
		ProxyConn:   proxyConn,
		ClientID:    uuid.New(),
		InstanceID:  instanceID,
		ConsoleType: publicv1.ConsoleType_CONSOLE_TYPE_SERIAL,
		OnConnected: func(ctx context.Context) {
			c.console.Infof(ctx, "Connected to %s. Disconnect: Ctrl+] or Enter ~.\n", instanceID)
		},
	}

	err = connect.WithRetry(ctx, opts, c.proxyIO)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		c.console.Errorf(ctx, "\nSession timed out after %s.\n", c.args.timeout)
		return nil
	}
	return err
}

// proxyIO handles bidirectional I/O between the terminal and the gRPC stream.
func (c *runnerContext) proxyIO(ctx context.Context, cancel context.CancelFunc, stream grpc.BidiStreamingClient[publicv1.ConsoleProxyConnectRequest, publicv1.ConsoleProxyConnectResponse]) error {
	// Set terminal to raw mode.
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		oldState, err := term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("failed to set raw mode: %w", err)
		}
		defer func() {
			if err := term.Restore(fd, oldState); err != nil {
				slog.Debug("Failed to restore terminal", "error", err)
			}
		}()
	}

	stdinReader, err := cancelreader.NewReader(os.Stdin)
	if err != nil {
		return fmt.Errorf("failed to create cancelable stdin reader: %w", err)
	}
	defer func() {
		if err := stdinReader.Close(); err != nil {
			c.logger.DebugContext(ctx, "Failed to close stdin reader", "error", err)
		}
	}()

	escape := newEscapeDetector()
	return connect.Proxy(ctx, cancel, stream, connect.ProxyOptions{
		Reader: stdinReader,
		Writer: os.Stdout,
		InterruptRead: func() func() {
			if !stdinReader.Cancel() {
				c.logger.WarnContext(ctx, "Failed to cancel stdin reader, proxy cleanup may block")
			}
			return func() {}
		},
		InputFilter: func(data []byte) bool {
			if escape.feed(data) {
				c.console.Infof(ctx, "\nConnection closed.\n")
				return true
			}
			return false
		},
		OnDisconnect: func(msg string) {
			c.console.Infof(ctx, "\n%s\n", msg)
		},
	})
}

const shortHelp = "Access compute instance serial console"

const longHelp = `
Open an interactive serial console session to a compute instance.

The console provides direct access to the compute instance's serial port,
allowing you to interact with it as if connected via a physical serial cable.

The instance can be specified by name or identifier.

To disconnect: press {{ bt }}Ctrl+]{{ bt }} at any time, or type {{ bt }}~.{{ bt }}
after Enter. The session continues running after you disconnect.

Cloud images (e.g., Fedora) require a password to be set via _cloud-init_
at instance creation time. Without this, serial console login will be rejected.
Example _cloud-init_ configuration:

{{ bt 3 }}yaml
#cloud-config
password: ...
chpasswd:
  expire: false
{{ bt 3 }}

Pass it as a template parameter when creating the instance:

{{ bt 3 }}shell
{{ binary }} create computeinstance --template <template> \
-p cloud_init_config=<base64-encoded-config>
{{ bt 3}}
`

const timeoutFlagHelp = `
_TIMEOUT_ - Session timeout.
`
