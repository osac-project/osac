/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package help

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/color"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/markdown"
	"github.com/osac-project/osac/fulfillment-service/internal/templating"
)

//go:embed templates
var templatesFS embed.FS

// Setup configures the given command and all its subcommands to render their help output as styled Markdown.
func Setup(cmd *cobra.Command) {
	// Create a silent logger for the templating engine, as help rendering happens before the persistent pre-run
	// hook sets up a proper logger, so we discard log output here.
	logger := slog.New(slog.DiscardHandler)

	// Build the templating engine from the embedded templates directory:
	engine, err := templating.NewEngine().
		SetLogger(logger).
		AddFS(templatesFS).
		SetDir("templates").
		AddFunction("flags", flagsFunc).
		Build()
	if err != nil {
		return
	}

	// Set the help function for the command and all its subcommands. The renderer is created each time the
	// help is displayed, so that it can adapt to the current terminal width and color capabilities.
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		out := c.OutOrStdout()
		useColor := color.Enabled(c, out)

		// Hide private-API subcommands when the user is not in private mode:
		hidePrivateSubcommands(c)

		// Render the help output:
		var buffer bytes.Buffer
		if err := engine.Execute(&buffer, "command_help.md", c); err != nil {
			c.PrintErrln("Error executing help template:", err)
			return
		}
		renderer, _, err := markdown.NewRenderer(out, useColor)
		if err != nil {
			c.PrintErrln("Error creating renderer:", err)
			return
		}
		text, err := renderer.Render(buffer.String())
		if err != nil {
			c.Print(buffer.String())
			return
		}
		if useColor {
			_, err = fmt.Fprint(out, text)
		} else {
			_, err = lipgloss.Fprint(out, text)
		}
		if err != nil {
			c.PrintErrln("Error writing help output:", err)
			return
		}
	})
}

// flagsFunc converts a pflag.FlagSet into a slice of visible flags, excluding hidden flags and the
// built-in help flag.
func flagsFunc(fs *pflag.FlagSet) []*pflag.Flag {
	var result []*pflag.Flag
	fs.VisitAll(func(f *pflag.Flag) {
		if !f.Hidden && f.Name != "help" {
			result = append(result, f)
		}
	})
	return result
}

const (
	privateAPIAnnotationKey   = "api"
	privateAPIAnnotationValue = "private"
)

// MarkPrivateAPI annotates a command as belonging to the private API. The help system uses this
// to hide such commands from non-admin users who have not logged in with --private.
func MarkPrivateAPI(cmd *cobra.Command) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[privateAPIAnnotationKey] = privateAPIAnnotationValue
	return cmd
}

func isPrivateAPICommand(cmd *cobra.Command) bool {
	return cmd.Annotations != nil && cmd.Annotations[privateAPIAnnotationKey] == privateAPIAnnotationValue
}

// hidePrivateSubcommands checks the user's config file to determine whether private-API mode is
// enabled, and hides subcommands annotated with api=private when it is not. This runs at
// help-render time because PersistentPreRunE does not execute for --help.
func hidePrivateSubcommands(c *cobra.Command) {
	hasPrivate := false
	for _, sub := range c.Commands() {
		if isPrivateAPICommand(sub) {
			hasPrivate = true
			break
		}
	}
	if !hasPrivate {
		return
	}

	isPrivate := false
	configDir := ""
	if f := c.Root().PersistentFlags().Lookup("config"); f != nil {
		configDir = f.Value.String()
		if !f.Changed {
			if envVal := os.Getenv("OSAC_CONFIG"); envVal != "" {
				configDir = envVal
			}
		}
	}
	if configDir != "" {
		if absDir, err := filepath.Abs(configDir); err == nil {
			data, err := os.ReadFile(filepath.Clean(filepath.Join(absDir, "config.json")))
			if err == nil {
				var cfg struct {
					Private bool `json:"private,omitempty"`
				}
				if json.Unmarshal(data, &cfg) == nil {
					isPrivate = cfg.Private
				}
			}
		}
	}

	for _, sub := range c.Commands() {
		if isPrivateAPICommand(sub) {
			sub.Hidden = !isPrivate
		}
	}
}
