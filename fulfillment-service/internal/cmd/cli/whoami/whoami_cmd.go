/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package whoami

import (
	"context"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/tokenutil"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/exit"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "whoami [FLAG...]",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}

	flags := result.Flags()
	flags.BoolVarP(
		&runner.args.showToken,
		"show-token",
		"t",
		false,
		showTokenFlagHelp,
	)
	flags.BoolVarP(
		&runner.args.showTokenClaims,
		"show-token-claims",
		"c",
		false,
		showTokenClaimsFlagHelp,
	)
	flags.BoolVarP(
		&runner.args.rfc3339,
		"rfc-3339",
		"R",
		false,
		rfc3339FlagHelp,
	)
	flags.BoolVarP(
		&runner.args.utc,
		"utc",
		"U",
		false,
		utcFlagHelp,
	)

	return result
}

type runnerContext struct {
	console *terminal.Console
	args    struct {
		showToken       bool
		showTokenClaims bool
		rfc3339         bool
		utc             bool
	}
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	c.console = terminal.ConsoleFromContext(ctx)
	cfg := config.SettingsFromContext(ctx)

	// Check if user is logged in
	if !cfg.Armed() {
		c.console.Errorf(ctx, "Not logged in. Use 'osac login' to authenticate.\n")
		return exit.Error(1)
	}

	// Get the token source to extract user information
	tokenSource, err := cfg.TokenSource(ctx)
	if err != nil || tokenSource == nil {
		c.console.Errorf(ctx, "Not logged in. Use 'osac login' to authenticate.\n")
		return exit.Error(1)
	}

	// Get the current token
	token, err := tokenSource.Token(ctx)
	if err != nil {
		c.console.Errorf(ctx, "Failed to retrieve authentication token: %v\n", err)
		return exit.Error(1)
	}
	if token == nil {
		c.console.Errorf(ctx, "Failed to retrieve authentication token. Please try logging in again with 'osac login'.\n")
		return exit.Error(1)
	}

	// Display user information from the token
	err = c.displayUserInfo(ctx, token)
	if err != nil {
		return err
	}

	return nil
}

func (c *runnerContext) displayUserInfo(ctx context.Context, token *auth.Token) error {
	// Parse the JWT token to extract claims
	if token.Access == "" {
		c.console.Errorf(ctx, "Not logged in. Use 'osac login' to authenticate.\n")
		return exit.Error(1)
	}

	// Parse the JWT without verification since we just want to read claims
	claims, err := tokenutil.ParseTokenClaims(token.Access)
	if err != nil {
		// If we can't parse the token, just show generic authentication message
		c.console.Infof(ctx, "Logged in (unable to read token details)\n")
		if c.args.showToken {
			c.console.Infof(ctx, "\nAccess token:\n%s\n", token.Access)
		}
		return nil
	}

	if len(claims) == 0 {
		c.console.Infof(ctx, "Logged in (unable to read token claims)\n")
		if c.args.showToken {
			c.console.Infof(ctx, "\nAccess token:\n%s\n", token.Access)
		}
		return nil
	}

	// Extract and display user information from claims
	if username, ok := claims["username"].(string); ok && username != "" {
		c.console.Infof(ctx, "Logged in as: %s\n", username)
	}

	// Show tenant
	tenant := config.TenantFromContext(ctx)
	if tenant != "" {
		c.console.Infof(ctx, "Tenant: %s\n", tenant)
	} else if orgMap, ok := claims["organization"].(map[string]interface{}); ok && len(orgMap) > 0 {
		// Organization in JWT is a map where keys are org names
		// Collect and sort to ensure deterministic selection
		var orgNames []string
		for orgName := range orgMap {
			orgNames = append(orgNames, orgName)
		}
		if len(orgNames) > 0 {
			sort.Strings(orgNames)
			c.console.Infof(ctx, "Tenant: %s\n", orgNames[0])
		}
	}

	// Show roles
	if realmAccess, ok := claims["realm_access"].(map[string]interface{}); ok {
		if rolesArray, ok := realmAccess["roles"].([]interface{}); ok && len(rolesArray) > 0 {
			var roles []string
			for _, role := range rolesArray {
				if roleStr, ok := role.(string); ok {
					roles = append(roles, roleStr)
				}
			}
			if len(roles) > 0 {
				c.console.Infof(ctx, "Roles: %s\n", strings.Join(roles, ", "))
			}
		}
	}

	// Show the access token if requested
	if c.args.showToken {
		c.console.Infof(ctx, "\nAccess token:\n%s\n", token.Access)
	}

	// Show token claims if requested
	if c.args.showTokenClaims {
		c.console.Infof(ctx, "\nToken claims:\n")
		tokenutil.DisplayTokenClaims(ctx, c.console, claims, c.args.rfc3339, c.args.utc)
	}

	return nil
}

const shortHelp = `Display current user and connection information`

const longHelp = `
Display information about the current authenticated user session.

Shows the authenticated username, configured tenant, and assigned roles.
`

const showTokenFlagHelp = `
_[BOOLEAN]_ - Display the raw access token. Use with caution as this exposes sensitive
authentication credentials.
`

const showTokenClaimsFlagHelp = `
_[BOOLEAN]_ - Display the decoded token claims in JSON format.
`

const rfc3339FlagHelp = `
_[BOOLEAN]_ - Display the time claims as RFC 3339 timestamps. By default the
time claims are displayed as seconds since the Unix epoch, as that is the
format used by JSON web tokens. Only applies when {{ bt }}--show-token-claims{{ bt }} is used.
`

const utcFlagHelp = `
_[BOOLEAN]_ - Display the time claims using the UTC time zone. Only applies when
{{ bt }}--rfc-3339{{ bt }} is used.
`
