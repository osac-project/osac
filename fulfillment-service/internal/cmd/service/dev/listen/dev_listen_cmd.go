/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package listen

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/fulfillment-service/internal/database"
	"github.com/osac-project/fulfillment-service/internal/logging"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	command := &cobra.Command{
		Use:                   "listen [FLAG...]",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}
	flags := command.Flags()
	database.AddFlags(flags)
	flags.StringVar(
		&runner.channel,
		"channel",
		"",
		channelFlagHelp,
	)
	return command
}

type runnerContext struct {
	logger  *slog.Logger
	flags   *pflag.FlagSet
	channel string
}

func (c *runnerContext) run(cmd *cobra.Command, argv []string) error {
	// Get the context:
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	// Get the dependencies from the context:
	c.logger = logging.LoggerFromContext(ctx)

	// Save the flags:
	c.flags = cmd.Flags()

	// Check flags:
	if c.channel == "" {
		return errors.New("channel name is mandatory")
	}

	// Get the database connection URL:
	c.logger.InfoContext(ctx, "Getting database connection details")
	dbTool, err := database.NewTool().
		SetLogger(c.logger).
		SetFlags(c.flags).
		Build()
	if err != nil {
		return fmt.Errorf("failed to get database connection details: %w", err)
	}
	dbUrl := dbTool.URL()

	// Create the listener:
	c.logger.InfoContext(ctx, "Creating listener")
	listener, err := database.NewListener().
		SetLogger(c.logger).
		SetUrl(dbUrl).
		SetChannel(c.channel).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}
	err = listener.Listen(ctx, c.processPayload)
	if err == nil || errors.Is(err, context.Canceled) {
		c.logger.InfoContext(ctx, "Listener finished")
	} else {
		c.logger.InfoContext(
			ctx,
			"Listener failed",
			slog.Any("error", err),
		)
	}

	// Wait for a signal:
	c.logger.InfoContext(ctx, "Waiting for signal")
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop
	c.logger.InfoContext(ctx, "Signal received, shutting down")
	return nil
}

func (c *runnerContext) processPayload(ctx context.Context, payload proto.Message) error {
	c.logger.InfoContext(
		ctx,
		"Received payload",
		slog.String("channel", c.channel),
		slog.Any("payload", payload),
	)

	return nil
}

const shortHelp = `Listens for notifications`

const longHelp = `
Listens for notifications.
`

const channelFlagHelp = `
_CHANNEL_ - Name of the channel.
`
