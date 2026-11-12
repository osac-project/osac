/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package project

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/exit"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/reflection"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

//go:embed templates
var templatesFS embed.FS

func Cmd() *cobra.Command {
	runner := &runnerContext{
		marshalOptions: protojson.MarshalOptions{
			UseProtoNames: true,
		},
	}
	result := &cobra.Command{
		Use:                   "project [FLAG...] [NAME]",
		DisableFlagsInUseLine: true,
		Short:                 shortHelp,
		Long:                  longHelp,
		RunE:                  runner.run,
	}
	flags := result.Flags()
	flags.BoolVar(
		&runner.args.clear,
		"clear",
		false,
		clearFlagHelp,
	)
	flags.StringVarP(
		&runner.args.format,
		"output",
		"o",
		"",
		outputFlagHelp,
	)
	return result
}

type runnerContext struct {
	args struct {
		clear  bool
		format string
	}
	logger         *slog.Logger
	console        *terminal.Console
	marshalOptions protojson.MarshalOptions
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	c.logger = logging.LoggerFromContext(ctx)
	c.console = terminal.ConsoleFromContext(ctx)

	err := c.console.AddTemplates(templatesFS, "templates")
	if err != nil {
		return fmt.Errorf("failed to load templates: %w", err)
	}

	settings := config.SettingsFromContext(ctx)

	if c.args.clear && len(args) > 0 {
		return fmt.Errorf("cannot use --clear with a project name")
	}
	if c.args.clear && c.args.format != "" {
		return fmt.Errorf("cannot use --clear with --output")
	}
	if c.args.format != "" && len(args) > 0 {
		return fmt.Errorf("cannot use --output with a project name")
	}

	if c.args.clear {
		settings.SetProject("")
		err = settings.Save(ctx)
		if err != nil {
			return fmt.Errorf("failed to save settings: %w", err)
		}
		c.console.Render(ctx, "project_cleared.txt", nil)
		return nil
	}

	if len(args) == 0 {
		return c.showCurrentProject(cmd, settings)
	}

	return c.setProject(cmd, settings, args[0])
}

func (c *runnerContext) showCurrentProject(cmd *cobra.Command, settings *config.Settings) error {
	ctx := cmd.Context()
	project := config.ProjectFromContext(ctx)
	if project == "" {
		c.console.Render(ctx, "no_project.txt", nil)
		return nil
	}

	if c.args.format == "" {
		c.console.Render(ctx, "current_project.txt", map[string]any{
			"Project": project,
		})
		return nil
	}

	object, _, err := c.findProject(cmd, settings, project)
	if err != nil {
		return err
	}

	return c.renderObject(cmd, object)
}

func (c *runnerContext) setProject(cmd *cobra.Command, settings *config.Settings, name string) error {
	ctx := cmd.Context()

	// Resolve hierarchical navigation: ".." goes up, and a relative name is appended to the current project.
	resolvedName := c.resolveProjectName(ctx, settings, name)

	// Navigating up to the default (empty) project clears the saved project.
	if resolvedName == "" {
		settings.SetProject("")
		err := settings.Save(ctx)
		if err != nil {
			return fmt.Errorf("failed to save settings: %w", err)
		}
		c.console.Render(ctx, "project_cleared.txt", nil)
		return nil
	}

	// When the resolved name differs from the input (relative resolution was applied),
	// try the resolved (child) name first so that a bare name like "dev" under project
	// "team" finds "team.dev" before a root project named "dev". The raw input is used
	// as a fallback for explicit IDs or absolute names.
	var object proto.Message
	var objectHelper reflection.ObjectHelper
	var err error
	if name != resolvedName {
		object, objectHelper, err = c.findProjectQuiet(cmd, settings, resolvedName)
		if err != nil {
			// Fall through to the raw input only when the relative project was
			// not found (exit.Error from FindObject). Propagate connection and
			// service errors instead of discarding them.
			var exitErr exit.Error
			if errors.As(err, &exitErr) {
				object = nil
			} else {
				return err
			}
		}
	}
	if object == nil {
		object, objectHelper, err = c.findProject(cmd, settings, name)
		if err != nil {
			return err
		}
	}

	canonicalName := objectHelper.GetName(object)
	settings.SetProject(canonicalName)
	err = settings.Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to save settings: %w", err)
	}

	c.console.Render(ctx, "project_set.txt", map[string]any{
		"Project": canonicalName,
	})
	return nil
}

// noOpRenderer is a Renderer that discards all output. Used for silent lookups
// where error templates should not be displayed.
type noOpRenderer struct{}

func (noOpRenderer) Render(context.Context, string, any) {}

// findProjectQuiet is like findProject but uses a no-op renderer so that "not found" and
// "multiple matches" templates are not displayed. It is used for speculative lookups
// when the caller will fall back to a different ref on failure.
func (c *runnerContext) findProjectQuiet(cmd *cobra.Command, settings *config.Settings, ref string) (proto.Message, reflection.ObjectHelper, error) {
	ctx := cmd.Context()

	if !settings.Armed() {
		return nil, nil, fmt.Errorf("there is no configuration, run the 'login' command")
	}

	conn, err := settings.Connect(ctx, cmd.Flags())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	helper, err := reflection.NewHelper().
		SetLogger(c.logger).
		SetConnection(conn).
		AddPackages(settings.Packages()).
		SetTenantFunc(config.TenantFromContext).
		Build()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create reflection tool: %w", err)
	}

	objectHelper := helper.Lookup("project")
	if objectHelper == nil {
		return nil, nil, fmt.Errorf("project resource type not found on the server")
	}

	object, err := objectHelper.FindObject(ctx, ref, noOpRenderer{})
	if err != nil {
		return nil, nil, err
	}
	return object, objectHelper, nil
}

// resolveProjectName resolves a project name relative to the current project context, supporting
// hierarchical navigation. A ".." navigates to the parent project, and a relative name is appended
// to the current project with a dot separator, following a filesystem-like navigation model.
func (c *runnerContext) resolveProjectName(ctx context.Context, _ *config.Settings, name string) string {
	current := config.ProjectFromContext(ctx)

	if name == ".." {
		if current == "" {
			return ""
		}
		lastDot := strings.LastIndex(current, ".")
		if lastDot < 0 {
			// At top-level project, going up returns to the default (empty) project.
			return ""
		}
		return current[:lastDot]
	}

	// If there is no current project, the name is absolute.
	if current == "" {
		return name
	}

	// Otherwise the name is relative to the current project.
	return current + "." + name
}

// findProject connects to the server and resolves a project name or ID to a single object. When
// the lookup fails it renders a "not found" message with up to 10 available project names as hints.
func (c *runnerContext) findProject(cmd *cobra.Command, settings *config.Settings, ref string) (proto.Message, reflection.ObjectHelper, error) {
	ctx := cmd.Context()

	if !settings.Armed() {
		return nil, nil, fmt.Errorf("there is no configuration, run the 'login' command")
	}

	conn, err := settings.Connect(ctx, cmd.Flags())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	helper, err := reflection.NewHelper().
		SetLogger(c.logger).
		SetConnection(conn).
		AddPackages(settings.Packages()).
		SetTenantFunc(config.TenantFromContext).
		Build()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create reflection tool: %w", err)
	}

	objectHelper := helper.Lookup("project")
	if objectHelper == nil {
		return nil, nil, fmt.Errorf("project resource type not found on the server")
	}

	object, err := objectHelper.FindObject(ctx, ref, c.console)
	if err != nil {
		return nil, nil, err
	}
	return object, objectHelper, nil
}

func (c *runnerContext) renderObject(cmd *cobra.Command, object proto.Message) error {
	ctx := cmd.Context()

	data, err := c.marshalOptions.Marshal(object)
	if err != nil {
		return err
	}
	var value any
	err = json.Unmarshal(data, &value)
	if err != nil {
		return err
	}

	switch c.args.format {
	case "json":
		c.console.RenderJson(ctx, value)
	case "yaml":
		c.console.RenderYaml(ctx, value)
	default:
		return fmt.Errorf("unknown output format '%s', should be 'json' or 'yaml'", c.args.format)
	}

	return nil
}

const shortHelp = `Manage the current project`

const longHelp = `
Manage the current project for CLI operations.

To see the current project:

{{ bt 3 }}shell
{{ binary }} project
{{ bt 3 }}

To set the current project:

{{ bt 3 }}shell
{{ binary }} project my-project
{{ bt 3 }}

To clear the current project:

{{ bt 3 }}shell
{{ binary }} project --clear
{{ bt 3 }}

To view the full project object:

{{ bt 3 }}shell
{{ binary }} project -o yaml
{{ bt 3 }}

When a project is set, it is used as the default for all operations. The
{{ bt }}--project{{ bt }} global flag overrides the saved project.

Project names are treated as relative paths when a current project is already
set. For example, if the current project is {{ bt }}a{{ bt }}, running
{{ bt }}{{ binary }} project b{{ bt }} sets the project to {{ bt }}a.b{{ bt }}.
Use {{ bt }}{{ binary }} project ..{{ bt }} to navigate to the parent project.
`

const clearFlagHelp = `
_[BOOLEAN]_ - Clear the saved project.
`

const outputFlagHelp = `
_FORMAT_ - Output format for the project object. Must be {{ bt }}json{{ bt }} or {{ bt }}yaml{{ bt }}.
`
