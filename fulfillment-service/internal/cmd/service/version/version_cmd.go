/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package version

import (
	"log/slog"

	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/version"
	"github.com/spf13/cobra"
)

// Cmd creates and returns the `version` command.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	command := &cobra.Command{
		Use:                   "version [FLAG...]",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}
	return command
}

// runnerContext contains the data and logic needed to run the `version` command.
type runnerContext struct {
}

// run executes the `version` command.
func (c *runnerContext) run(cmd *cobra.Command, argv []string) error {
	// Get the context:
	ctx := cmd.Context()

	// Get the logger:
	logger := logging.LoggerFromContext(ctx)

	// Print the values:
	logger.Info(
		"Version",
		slog.String("version", version.Get()),
	)

	return nil
}

// shortHelp is the short help text for the `version` command.
const shortHelp = `Display version details`

// longHelp is the long help text for the `version` command.
const longHelp = `
Display version details.
`
