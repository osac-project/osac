/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package tenant

import (
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/reflection"
	"github.com/osac-project/fulfillment-service/internal/terminal"
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
		Use:                   "tenant [FLAG...] [NAME]",
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
		return fmt.Errorf("cannot use --clear with a tenant name")
	}
	if c.args.clear && c.args.format != "" {
		return fmt.Errorf("cannot use --clear with --output")
	}
	if c.args.format != "" && len(args) > 0 {
		return fmt.Errorf("cannot use --output with a tenant name")
	}

	if c.args.clear {
		settings.SetTenant("")
		err = settings.Save(ctx)
		if err != nil {
			return fmt.Errorf("failed to save settings: %w", err)
		}
		c.console.Render(ctx, "tenant_cleared.txt", nil)
		return nil
	}

	if len(args) == 0 {
		return c.showCurrentTenant(cmd, settings)
	}

	return c.setTenant(cmd, settings, args[0])
}

func (c *runnerContext) showCurrentTenant(cmd *cobra.Command, settings *config.Settings) error {
	ctx := cmd.Context()
	tenant := config.TenantFromContext(ctx)
	if tenant == "" {
		c.console.Render(ctx, "no_tenant.txt", nil)
		return nil
	}

	if c.args.format == "" {
		c.console.Render(ctx, "current_tenant.txt", map[string]any{
			"Tenant": tenant,
		})
		return nil
	}

	object, _, err := c.findTenant(cmd, settings, tenant)
	if err != nil {
		return err
	}

	return c.renderObject(cmd, object)
}

func (c *runnerContext) setTenant(cmd *cobra.Command, settings *config.Settings, name string) error {
	ctx := cmd.Context()

	object, objectHelper, err := c.findTenant(cmd, settings, name)
	if err != nil {
		return err
	}

	canonicalName := objectHelper.GetName(object)
	settings.SetTenant(canonicalName)
	err = settings.Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to save settings: %w", err)
	}

	c.console.Render(ctx, "tenant_set.txt", map[string]any{
		"Tenant": canonicalName,
	})
	return nil
}

// findTenant connects to the server and resolves a tenant name or ID to a single object.
func (c *runnerContext) findTenant(cmd *cobra.Command, settings *config.Settings, ref string) (proto.Message, reflection.ObjectHelper, error) {
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

	objectHelper := helper.Lookup("tenant")
	if objectHelper == nil {
		return nil, nil, fmt.Errorf("tenant resource type not found on the server")
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

const shortHelp = `Manage the current tenant`

const longHelp = `
Manage the current tenant for CLI operations.

To see the current tenant:

{{ bt 3 }}shell
{{ binary }} tenant
{{ bt 3 }}

To set the current tenant:

{{ bt 3 }}shell
{{ binary }} tenant my-tenant
{{ bt 3 }}

To clear the current tenant:

{{ bt 3 }}shell
{{ binary }} tenant --clear
{{ bt 3 }}

To view the full tenant object:

{{ bt 3 }}shell
{{ binary }} tenant -o yaml
{{ bt 3 }}

When a tenant is set, it is used as the default for all operations. The
{{ bt }}--tenant{{ bt }} global flag overrides the saved tenant.
`

const clearFlagHelp = `
_[BOOLEAN]_ - Clear the saved tenant.
`

const outputFlagHelp = `
_FORMAT_ - Output format for the tenant object. Must be {{ bt }}json{{ bt }} or {{ bt }}yaml{{ bt }}.
`
