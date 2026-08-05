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
	"github.com/spf13/cobra"

	"github.com/osac-project/fulfillment-service/internal/terminal"
	"github.com/osac-project/fulfillment-service/internal/version"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "version [FLAG...]",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}
	return result
}

type runnerContext struct {
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	// Get the context:
	ctx := cmd.Context()

	// Get the console:
	console := terminal.ConsoleFromContext(ctx)

	// Print the version:
	console.Infof(ctx, "%s\n", version.Get())

	return nil
}

const shortHelp = "Display version details"

const longHelp = `
Display version details.
`
