/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package token

import (
	"github.com/golang-jwt/jwt/v5"
	"github.com/spf13/cobra"

	"github.com/osac-project/fulfillment-service/internal/cmd/cli/tokenutil"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/exit"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "token [FLAG...]",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		RunE:                  runner.run,
	}
	flags := result.Flags()
	flags.BoolVarP(
		&runner.refresh,
		"refresh",
		"r",
		false,
		refreshFlagHelp,
	)
	flags.BoolVarP(
		&runner.header,
		"header",
		"H",
		false,
		headerFlagHelp,
	)
	flags.BoolVarP(
		&runner.payload,
		"payload",
		"p",
		false,
		payloadFlagHelp,
	)
	flags.BoolVarP(
		&runner.rfc3339,
		"rfc-3339",
		"R",
		false,
		rfc3339FlagHelp,
	)
	flags.BoolVarP(
		&runner.utc,
		"utc",
		"U",
		false,
		utcFlagHelp,
	)

	return result
}

type runnerContext struct {
	console *terminal.Console
	refresh bool
	header  bool
	payload bool
	rfc3339 bool
	utc     bool
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	var err error

	// Get the context:
	ctx := cmd.Context()

	// Get the console:
	c.console = terminal.ConsoleFromContext(ctx)

	// Get the configuration:
	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		c.console.Errorf(ctx, "There is no configuration, run the 'login' command.\n")
		return exit.Error(1)
	}

	// Get the token:
	source, err := cfg.TokenSource(ctx)
	if err != nil {
		return err
	}
	token, err := source.Token(ctx)
	if err != nil {
		return err
	}

	// Select the token to print:
	var selected string
	if !c.refresh {
		if token.Access == "" {
			c.console.Errorf(ctx, "No access token available.\n")
			return exit.Error(1)
		}
		selected = token.Access
	} else {
		if token.Refresh == "" {
			c.console.Errorf(ctx, "No refresh token available.\n")
			return exit.Error(1)
		}
		selected = token.Refresh
	}

	// If the header or the payload have been requested, then try to parse the selected token as a JWT:
	var parsed *jwt.Token
	if c.header || c.payload {
		parser := jwt.NewParser(jwt.WithJSONNumber())
		parsed, _, err = parser.ParseUnverified(selected, &jwt.MapClaims{})
		if err != nil {
			c.console.Errorf(ctx, "Failed to parse token as a JSON web token: %s\n", err)
			return exit.Error(1)
		}
	}

	// Print the token:
	switch {
	case c.header:
		c.console.RenderJson(ctx, parsed.Header)
	case c.payload:
		claims := *parsed.Claims.(*jwt.MapClaims)
		tokenutil.DisplayTokenClaims(ctx, c.console, claims, c.rfc3339, c.utc)
	default:
		c.console.Infof(ctx, "%s\n", selected)
	}
	return nil
}

const shortHelp = `Shows the authentication token, requesting a new one if necessary`

const longHelp = `
Shows the authentication token, requesting a new one if necessary.
`

const refreshFlagHelp = `
_[BOOLEAN]_ - Show the refresh token instead of the access token.
`

const headerFlagHelp = `
_[BOOLEAN]_ - Print the token header. This only works if the token is a JSON
web token.
`

const payloadFlagHelp = `
_[BOOLEAN]_ - Show the token payload in JSON format. This only works if the
token is a JSON web token.
`

const rfc3339FlagHelp = `
_[BOOLEAN]_ - Display the time claims as RFC 3339 timestamps. By default the
time claims are displayed as seconds since the Unix epoch, as that is the
format used by JSON web tokens.
`

const utcFlagHelp = `
_[BOOLEAN]_ - Display the time claims using the UTC time zone.
`
