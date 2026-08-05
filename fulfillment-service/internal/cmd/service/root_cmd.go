/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package service

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/osac-project/fulfillment-service/internal/cmd/cli/help"
	"github.com/osac-project/fulfillment-service/internal/cmd/service/dev"
	"github.com/osac-project/fulfillment-service/internal/cmd/service/migrate"
	"github.com/osac-project/fulfillment-service/internal/cmd/service/probe"
	"github.com/osac-project/fulfillment-service/internal/cmd/service/start"
	"github.com/osac-project/fulfillment-service/internal/cmd/service/version"
	"github.com/osac-project/fulfillment-service/internal/logging"
)

func Root() *cobra.Command {
	// create the runner and the command:
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "fulfillment-service COMMAND [FLAG...]",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		SilenceUsage:          true,
		SilenceErrors:         true,
		PersistentPreRunE:     runner.persistentPreRun,
	}

	// Add flags:
	logging.AddFlags(result.PersistentFlags())

	// Add commands:
	result.AddCommand(dev.Cmd())
	result.AddCommand(migrate.Cmd())
	result.AddCommand(probe.Cmd())
	result.AddCommand(start.Cmd())
	result.AddCommand(version.Cmd())

	// Configure the root command, and therefore all its subcommands, to use Markdown for their help output:
	help.Setup(result)

	return result
}

type runnerContext struct {
}

func (c *runnerContext) persistentPreRun(cmd *cobra.Command, args []string) error {
	// Create the logger configured with the command line flags:
	logger, err := logging.NewLogger().
		SetFlags(cmd.Flags()).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}

	// Replace the default context with one that contains the logger:
	ctx := cmd.Context()
	ctx = logging.LoggerIntoContext(ctx, logger)
	cmd.SetContext(ctx)

	return nil
}

const shortHelp = `Fulfillment service.`

const longHelp = `
Fulfillment service.
`
