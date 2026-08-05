/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package get

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/osac-project/fulfillment-service/internal/cmd/cli/get/externalippool"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/get/kubeconfig"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/get/password"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/get/token"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/packages"
	"github.com/osac-project/fulfillment-service/internal/reflection"
	"github.com/osac-project/fulfillment-service/internal/rendering"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

//go:embed templates
var templatesFS embed.FS

// Possible output formats:
const (
	outputFormatTable = "table"
	outputFormatJson  = "json"
	outputFormatYaml  = "yaml"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{
		marshalOptions: protojson.MarshalOptions{
			UseProtoNames: true,
		},
	}
	result := &cobra.Command{
		Use:                   "get [FLAG...] OBJECT [ID|NAME]...",
		DisableFlagsInUseLine: true,
		Short:                 shortHelp,
		Long:                  longHelp,
		RunE:                  runner.run,
		ValidArgsFunction:     completeObjectTypes,
	}
	result.AddCommand(externalippool.Cmd())
	result.AddCommand(kubeconfig.Cmd())
	result.AddCommand(password.Cmd())
	result.AddCommand(token.Cmd())
	flags := result.Flags()
	flags.StringVarP(
		&runner.args.format,
		"output",
		"o",
		outputFormatTable,
		outputFlagHelp,
	)
	flags.StringVar(
		&runner.args.filter,
		"filter",
		"",
		filterFlagHelp,
	)
	flags.BoolVarP(
		&runner.args.watch,
		"watch",
		"w",
		false,
		watchFlagHelp,
	)
	return result
}

type runnerContext struct {
	args struct {
		format string
		filter string
		watch  bool
	}
	ctx            context.Context
	logger         *slog.Logger
	console        *terminal.Console
	conn           *grpc.ClientConn
	marshalOptions protojson.MarshalOptions
	globalHelper   reflection.Helper
	objectHelper   reflection.ObjectHelper
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	var err error

	// Get the context:
	ctx := cmd.Context()

	// Save the context. This is needed because some of the CEL functions that we create need the context, but
	// there is no way to pass it directly. Refrain from using tis for other purposes.
	c.ctx = ctx

	// Get the logger and console:
	c.logger = logging.LoggerFromContext(ctx)
	c.console = terminal.ConsoleFromContext(ctx)

	// Load the templates for the console messages:
	err = c.console.AddTemplates(templatesFS, "templates")
	if err != nil {
		return fmt.Errorf("failed to load templates: %w", err)
	}

	// Get the configuration:
	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	// Create the gRPC connection from the configuration:
	c.conn, err = cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer c.conn.Close()

	// Create the reflection helper:
	c.globalHelper, err = reflection.NewHelper().
		SetLogger(c.logger).
		SetConnection(c.conn).
		AddPackages(cfg.Packages()).
		SetTenantFunc(config.TenantFromContext).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create reflection tool: %w", err)
	}

	// Check that the object type has been specified:
	if len(args) == 0 {
		c.console.Render(ctx, "no_object.txt", map[string]any{
			"Helper": c.globalHelper,
		})
		return nil
	}

	// Get the object helper:
	c.objectHelper = c.globalHelper.Lookup(args[0])
	if c.objectHelper == nil {
		c.console.Render(ctx, "wrong_object.txt", map[string]any{
			"Helper": c.globalHelper,
			"Object": args[0],
		})
		return nil
	}

	// Check the flags:
	if c.args.format != outputFormatTable && c.args.format != outputFormatJson && c.args.format != outputFormatYaml {
		return fmt.Errorf(
			"unknown output format '%s', should be '%s', '%s' or '%s'",
			c.args.format, outputFormatTable, outputFormatJson, outputFormatYaml,
		)
	}

	// If watch mode is enabled, watch for events instead of listing
	if c.args.watch {
		return c.watch(ctx, args[1:])
	}

	// Get the objects using the list method, which will handle filtering by identifiers or names if provided.
	objects, err := c.list(ctx, args[1:])
	if err != nil {
		return err
	}

	// Render the items:
	var render func(context.Context, []proto.Message) error
	switch c.args.format {
	case outputFormatJson:
		render = c.renderJson
	case outputFormatYaml:
		render = c.renderYaml
	default:
		render = c.renderTable
	}
	return render(ctx, objects)
}

func (c *runnerContext) list(ctx context.Context, keys []string) (results []proto.Message, err error) {
	var options reflection.ListOptions

	// If keys (identifiers or names) were provided, build a CEL filter to match them.
	if len(keys) > 0 {
		var values []string
		for _, key := range keys {
			values = append(values, strconv.Quote(key))
		}
		list := strings.Join(values, ", ")
		options.Filter = fmt.Sprintf(
			`this.id in [%[1]s] || this.metadata.name in [%[1]s]`,
			list,
		)
	}

	// Apply the user-provided filter if specified.
	if c.args.filter != "" {
		if options.Filter != "" {
			options.Filter = fmt.Sprintf("(%s) && (%s)", options.Filter, c.args.filter)
		} else {
			options.Filter = c.args.filter
		}
	}

	listResult, err := c.objectHelper.List(ctx, options)
	if err != nil {
		return
	}
	results = listResult.Items
	return
}

func (c *runnerContext) renderTable(ctx context.Context, objects []proto.Message) error {
	// Check if there are results:
	if len(objects) == 0 {
		c.console.Render(ctx, "no_matching_objects.txt", nil)
		return nil
	}

	// Create the table renderer:
	renderer, err := rendering.NewTableRenderer().
		SetLogger(c.logger).
		SetHelper(c.globalHelper).
		SetWriter(c.console).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create table renderer: %w", err)
	}

	// Use the table renderer to render the objects:
	return renderer.Render(ctx, objects)
}

func (c *runnerContext) renderJson(ctx context.Context, objects []proto.Message) error {
	values, err := c.encodeObjects(objects)
	if err != nil {
		return err
	}
	if len(values) == 1 {
		c.console.RenderJson(ctx, values[0])
	} else {
		c.console.RenderJson(ctx, values)
	}
	return nil
}

func (c *runnerContext) renderYaml(ctx context.Context, objects []proto.Message) error {
	values, err := c.encodeObjects(objects)
	if err != nil {
		return err
	}
	if len(values) == 1 {
		c.console.RenderYaml(ctx, values[0])
	} else {
		c.console.RenderYaml(ctx, values)
	}
	return nil
}

func (c *runnerContext) encodeObjects(objects []proto.Message) (result []any, err error) {
	values := make([]any, len(objects))
	for i, object := range objects {
		values[i], err = c.encodeObject(object)
		if err != nil {
			return
		}
	}
	result = values
	return
}

func (c *runnerContext) encodeObject(object proto.Message) (result any, err error) {
	wrapper, err := anypb.New(object)
	if err != nil {
		return
	}
	var data []byte
	data, err = c.marshalOptions.Marshal(wrapper)
	if err != nil {
		return
	}
	err = json.Unmarshal(data, &result)
	return
}

func completeObjectTypes(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return reflection.ObjectTypeNames(packages.Public...), cobra.ShellCompDirectiveNoFileComp
}

const shortHelp = `Get objects`

const longHelp = `
List or retrieve objects from the server.

When called with just an object type it lists all objects of that type. When called with one or more identifiers or
names it retrieves only the matching objects.

To list all clusters:

{{ bt 3 }}shell
{{ binary }} get clusters
{{ bt 3 }}

To retrieve a specific cluster by name:

{{ bt 3 }}shell
{{ binary }} get cluster my-cluster
{{ bt 3 }}

Multiple identifiers or names can be specified at once:

{{ bt 3 }}shell
{{ binary }} get cluster my-cluster my-other-cluster
{{ bt 3 }}

Results can be filtered using CEL expressions with the
{{ bt }}--filter{{ bt }} flag:

{{ bt 3 }}shell
{{ binary }} get clusters --filter "this.metadata.labels['env'] == 'production'"
{{ bt 3 }}

For more information about the CEL expressions supported by the server see
https://github.com/osac-project/fulfillment-service/blob/main/docs/FILTER.md.

The output format can be changed with the {{ bt }}--output{{ bt }} flag. Supported formats are {{ bt }}table{{ bt }}
(default), {{ bt }}json{{ bt }} and {{ bt }}yaml{{ bt }}:

{{ bt 3 }}shell
{{ binary }} get clusters -o yaml
{{ bt 3 }}

To watch for changes to objects in real time use the
{{ bt }}--watch{{ bt }} flag:

{{ bt 3 }}shell
{{ binary }} get clusters --watch
{{ bt 3 }}

There are also subcommands for retrieving specific kinds of data such as kubeconfigs, passwords and tokens. Use
{{ bt }}--help{{ bt }} on any subcommand for details.
`

const outputFlagHelp = `
_FORMAT_ - Output format. Must be one of {{ bt }}table{{ bt }}, {{ bt }}json{{ bt }} or {{ bt }}yaml{{ bt }}.
`

const filterFlagHelp = `
_EXPRESSION_ - CEL expression used for filtering results. The expression is evaluated against each object and only those
for which it returns true are included in the output.
`

const watchFlagHelp = `
_[BOOLEAN]_ - Watch for changes to objects and display events in real time instead of listing the current state.
`
