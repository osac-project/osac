/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package migrate

import (
	"fmt"
	"math"

	"github.com/spf13/cobra"

	"github.com/osac-project/fulfillment-service/internal/database"
	"github.com/osac-project/fulfillment-service/internal/logging"
)

// Cmd creates and returns the `migrate` command.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	command := &cobra.Command{
		Use:                   "migrate [FLAG...]",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}
	database.AddFlags(command.Flags())
	return command
}

// runnerContext contains the data and logic needed to run the `migrate` command.
type runnerContext struct {
}

// run executes the `migrate` command.
func (c *runnerContext) run(cmd *cobra.Command, argv []string) error {
	// Get the context:
	ctx := cmd.Context()

	// Get the logger:
	logger := logging.LoggerFromContext(ctx)

	// Create the database tool:
	dbTool, err := database.NewTool().
		SetLogger(logger).
		SetFlags(cmd.Flags()).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create database tool: %w", err)
	}

	// Wait for the database to be available:
	logger.InfoContext(ctx, "Waiting for database to be available")
	err = dbTool.Wait(ctx)
	if err != nil {
		return fmt.Errorf("failed waiting for database: %w", err)
	}

	// Run the migrations:
	logger.InfoContext(ctx, "Running database migrations")
	err = dbTool.Migrate(ctx, math.MaxUint)
	if err != nil {
		return fmt.Errorf("failed to run database migrations: %w", err)
	}

	logger.InfoContext(ctx, "Database migrations completed successfully")
	return nil
}

const shortHelp = `Run database migrations`

const longHelp = `
Run database migrations.
`
