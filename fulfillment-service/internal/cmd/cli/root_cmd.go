/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/annotate"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/console"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/create"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/delete"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/describe"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/edit"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/get"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/help"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/label"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/login"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/logout"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/scale"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/tenant"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/version"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/whoami"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

func Root() (result *cobra.Command, err error) {
	// create the runner and the command:
	runner := &runnerContext{}
	result = &cobra.Command{
		Use:                   "osac COMMAND [FLAG...]",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		SilenceUsage:          true,
		SilenceErrors:         true,
		PersistentPreRunE:     runner.persistentPreRun,
	}

	// Determine the name of the binary, as we will use it to determine the cache and config directories:
	runner.binaryName = filepath.Base(os.Args[0])

	// Determine the default configuration directory:
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		err = fmt.Errorf("failed to determine user configuration directory: %w", err)
		return
	}
	defaultConfigDir := filepath.Join(userConfigDir, runner.binaryName)

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
		&runner.args.configDir,
		configFlag,
		defaultConfigDir,
		configFlagHelp,
	)
	flags.StringVar(
		&runner.args.cacheDir,
		cacheFlag,
		defaultCacheDir,
		cacheFlagHelp,
	)
	flags.StringVar(
		&runner.args.tenant,
		tenantFlag,
		"",
		tenantFlagHelp,
	)
	flags.Bool(
		help.NoColorFlag,
		false,
		help.NoColorFlagHelp,
	)

	// Add commands:
	result.AddCommand(annotate.Cmd())
	result.AddCommand(console.Cmd())
	result.AddCommand(create.Cmd())
	result.AddCommand(delete.Cmd())
	result.AddCommand(describe.Cmd())
	result.AddCommand(edit.Cmd())
	result.AddCommand(get.Cmd())
	result.AddCommand(label.Cmd())
	result.AddCommand(login.Cmd())
	result.AddCommand(logout.Cmd())
	result.AddCommand(scale.Cmd())
	result.AddCommand(tenant.Cmd())
	result.AddCommand(version.Cmd())
	result.AddCommand(whoami.Cmd())

	// Configure the root command, and therefore all its subcommands, to use Markdown for their help output:
	help.Setup(result)

	return
}

type runnerContext struct {
	binaryName string
	args       struct {
		configDir string
		cacheDir  string
		tenant    string
	}
}

func (c *runnerContext) persistentPreRun(cmd *cobra.Command, args []string) error {
	var err error

	// Get the actual flags:
	flags := cmd.Flags()

	// Determine the configuration directory, using the environment variable only if the user hasn't explicitly set the flag:
	configDir := c.args.configDir
	if !flags.Changed(configFlag) {
		value := os.Getenv(configEnvVar)
		if value != "" {
			configDir = value
		}
	}
	configDir, err = filepath.Abs(configDir)
	if err != nil {
		return fmt.Errorf(
			"failed to calculate absolute path of config directory '%s': %w",
			configDir, err,
		)
	}
	err = os.MkdirAll(configDir, 0700)
	if errors.Is(err, os.ErrExist) {
		err = nil
	}
	if err != nil {
		return fmt.Errorf("failed to create config directory '%s': %w", configDir, err)
	}

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
	if errors.Is(err, os.ErrExist) {
		err = nil
	}
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

	// Create and load the settings:
	settings, err := config.NewSettings().
		SetLogger(logger).
		SetDir(configDir).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create settings: %w", err)
	}
	err = settings.Load(cmd.Context())
	if err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}

	// Create the console:
	console, err := terminal.NewConsole().
		SetLogger(logger).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create console: %w", err)
	}

	// Resolve the effective tenant: flag takes precedence over saved setting.
	tenant := settings.Tenant()
	if flags.Changed(tenantFlag) {
		tenant = c.args.tenant
	}

	// Replace the default context with one that contains the logger, the settings, the console,
	// and the resolved tenant:
	ctx := cmd.Context()
	ctx = logging.LoggerIntoContext(ctx, logger)
	ctx = config.SettingsIntoContext(ctx, settings)
	ctx = terminal.ConsoleIntoContext(ctx, console)
	if tenant != "" {
		ctx = config.TenantIntoContext(ctx, tenant)
	}
	cmd.SetContext(ctx)

	return nil
}

// Names of command line flags:
const (
	configFlag = "config"
	cacheFlag  = "cache"
	tenantFlag = "tenant"
)

// Names of the environment variables:
const (
	configEnvVar = "OSAC_CONFIG"
	cacheEnvVar  = "OSAC_CACHE"
)

const shortHelp = `CLI for the _Open Sovereign AI Cloud_ platform`

const longHelp = `
Command line interface for the _Open Sovereign AI Cloud_ platform.
`

const configFlagHelp = `
_DIRECTORY_ - Directory where configuration files are stored. Can also be set with the {{ bt }}OSAC_CONFIG{{ bt }}
environment variable. If both are provided, the flag takes precedence.

Configuration is stored in a file named {{ bt }}config.json{{ bt }} inside this directory.

Secrets, such as tokens and passwords, are stored in the operating system keyring. If the keyring is not available,
they are stored in a file named {{ bt }}secrets.json{{ bt }} inside this directory.
`

const tenantFlagHelp = `
_NAME_ - Scope operations to the specified tenant. When set, list operations automatically filter by tenant,
and create operations populate {{ bt }}metadata.tenant{{ bt }} on new objects. If a current tenant has been
saved with {{ bt }}{{ binary }} tenant <name>{{ bt }}, the flag takes precedence.
`

const cacheFlagHelp = `
_DIRECTORY_ - Directory where cache and log files are stored. Can also be set with the {{ bt }}OSAC_CACHE{{ bt }}
environment variable. If both are provided, the flag takes precedence.
`
