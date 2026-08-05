/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package osacdev

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/osac-project/fulfillment-service/internal/cache"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/help"
	"github.com/osac-project/fulfillment-service/internal/cmd/osac-dev/generate"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

func Root() (result *cobra.Command, err error) {
	// create the runner and the command:
	runner := &runnerContext{}
	result = &cobra.Command{
		Use:                   "osac-dev COMMAND [FLAG...]",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		SilenceUsage:          true,
		SilenceErrors:         true,
		PersistentPreRunE:     runner.persistentPreRun,
	}

	// Determine the name of the binary, as we will use it to determine the cache directory:
	runner.binaryName = filepath.Base(os.Args[0])

	// Determine the default cache directory:
	userCacheDir, err := os.UserCacheDir()
	if err != nil {
		err = fmt.Errorf("failed to determine user cache directory: %w", err)
		return
	}
	defaultCacheDir := filepath.Join(userCacheDir, runner.binaryName)

	// Add flags:
	flags := result.PersistentFlags()
	logging.AddFlags(flags)
	flags.StringVar(
		&runner.args.cacheDir,
		cacheFlag,
		defaultCacheDir,
		cacheFlagHelp,
	)

	// Add commands:
	result.AddCommand(generate.Cmd())

	// Configure the root command, and therefore all its subcommands, to use Markdown for their help output:
	help.Setup(result)

	return
}

type runnerContext struct {
	binaryName string
	args       struct {
		cacheDir string
	}
}

func (c *runnerContext) persistentPreRun(cmd *cobra.Command, args []string) error {
	var err error

	// Get the actual flags:
	flags := cmd.Flags()

	// Determine the cache directory, using the environment variable only if the user hasn't explicitly set the flag:
	cacheDir := c.args.cacheDir
	if !flags.Changed(cacheFlag) {
		value := os.Getenv(cacheEnvVar)
		if value != "" {
			cacheDir = value
		}
	}
	cacheDir, err = filepath.Abs(cacheDir)
	if err != nil {
		return fmt.Errorf(
			"failed to calculate absolute path of cache directory '%s': %w",
			cacheDir, err,
		)
	}
	err = os.MkdirAll(cacheDir, 0700)
	if err != nil {
		return fmt.Errorf("failed to create cache directory '%s': %w", cacheDir, err)
	}

	// By the default the logger is configured to write to the log file, and only errors. This Will be overridden by
	// the command line flags.
	logFile := filepath.Join(cacheDir, c.binaryName+".log")
	logger, err := logging.NewLogger().
		SetFile(logFile).
		SetFlags(cmd.Flags()).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}

	// Create the console:
	console, err := terminal.NewConsole().
		SetLogger(logger).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create console: %w", err)
	}

	// Replace the default context with one that contains the cache directory, the logger, and the console:
	ctx := cmd.Context()
	ctx = cache.DirIntoContext(ctx, cacheDir)
	ctx = logging.LoggerIntoContext(ctx, logger)
	ctx = terminal.ConsoleIntoContext(ctx, console)
	cmd.SetContext(ctx)

	return nil
}

// Names of command line flags:
const (
	cacheFlag = "cache"
)

// Names of the environment variables:
const (
	cacheEnvVar = "OSAC_DEV_CACHE"
)

const shortHelp = `Development tools for the _Open Sovereign AI Cloud_ platform`

const longHelp = `
Development tools for the _Open Sovereign AI Cloud_ platform.
`

const cacheFlagHelp = `
_DIRECTORY_ - Directory where cache and log files are stored. Can also be set with the {{ bt }}OSAC_DEV_CACHE{{ bt }}
environment variable. If both are provided, the flag takes precedence.
`
