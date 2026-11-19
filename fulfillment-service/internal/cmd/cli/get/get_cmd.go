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
	"math"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/get/billingproviderstatus"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/get/externalippool"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/get/storagetier"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/get/token"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/reflection"
	"github.com/osac-project/osac/fulfillment-service/internal/rendering"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

//go:embed templates
var templatesFS embed.FS

// Possible output formats:
const (
	outputFormatTable = "table"
	outputFormatJson  = "json"
	outputFormatYaml  = "yaml"
)

// Cmd creates the `get` command with its object-type subcommands.
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
	result.AddCommand(billingproviderstatus.Cmd())
	result.AddCommand(externalippool.Cmd())
	result.AddCommand(storagetier.Cmd())
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
	ctx              context.Context
	logger           *slog.Logger
	console          *terminal.Console
	conn             *grpc.ClientConn
	marshalOptions   protojson.MarshalOptions
	globalHelper     reflection.Helper
	objectHelper     reflection.ObjectHelper
	listRPCBudget    int
	listObjectBudget int
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	var err error
	// Reject malformed output before configuration or connection preparation.
	if c.args.format != outputFormatTable && c.args.format != outputFormatJson && c.args.format != outputFormatYaml {
		return fmt.Errorf(
			"unknown output format '%s', should be '%s', '%s' or '%s'",
			c.args.format, outputFormatTable, outputFormatJson, outputFormatYaml,
		)
	}

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
		SetProjectFunc(config.ProjectFromContext).
		UseGetForStructuredOutput(&publicv1.Secret{}).
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

	// If watch mode is enabled, watch for events instead of listing
	if c.args.watch {
		return c.watch(ctx, args[1:])
	}

	// Get the objects:
	keys := args[1:]
	objects, err := c.fetchObjects(ctx, keys)
	if err != nil {
		return err
	}

	switch c.args.format {
	case outputFormatJson:
		return c.renderJson(ctx, objects)
	case outputFormatYaml:
		return c.renderYaml(ctx, objects)
	default:
		return c.renderTable(ctx, objects)
	}
}

// fetchObjects returns objects for the given keys.
// For cases when:
//  1. Object names or identifiers are provided
//  2. The output format is specified as yaml or json
//  3. The resource type is configured to use Get for structured output
//
// We fetch the objects individually via Get to obtain the full representation.
//
// Otherwise, we fetch the objects via List.
// This can result in partial data for resources like secrets where the data field is redacted
// when list is called and no names or identifiers are provided.
func (c *runnerContext) fetchObjects(ctx context.Context, keys []string) ([]proto.Message, error) {
	if len(keys) > 0 && c.args.format != outputFormatTable && c.objectHelper.UseGetForStructuredOutput() {
		return c.getByKeys(ctx, keys)
	}
	return c.list(ctx, keys)
}

func (c *runnerContext) getByKeys(ctx context.Context, keys []string) ([]proto.Message, error) {
	listResults, err := c.list(ctx, keys)
	if err != nil {
		return nil, err
	}
	results := make([]proto.Message, len(listResults))
	for i, obj := range listResults {
		id := c.objectHelper.GetId(obj)
		result, err := c.objectHelper.Get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("failed to get object: %w", err)
		}
		results[i] = result
	}
	return results, nil
}

func (c *runnerContext) listOptions(keys []string) reflection.ListOptions {
	options := reflection.ListOptions{Limit: 100}

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
	return options
}

func (c *runnerContext) list(ctx context.Context, keys []string) ([]proto.Message, error) {
	options := c.listOptions(keys)

	// These are finite safety limits, not user-facing paging controls. A failed
	// traversal discards its buffer; the caller renders only after completion.
	rpcBudget, objectBudget := c.listRPCBudget, c.listObjectBudget
	if rpcBudget == 0 {
		rpcBudget = 10000
	}
	if objectBudget == 0 {
		objectBudget = 1000000
	}
	if rpcBudget < 0 || objectBudget < 0 {
		return nil, fmt.Errorf("list budgets must not be negative")
	}
	var results []proto.Message
	seen := make(map[string]struct{})
	var total int32
	for calls := 0; ; calls++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if calls >= rpcBudget {
			return nil, fmt.Errorf("list RPC budget exhausted (%d)", rpcBudget)
		}
		page, err := c.objectHelper.List(ctx, options)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if page.OffsetSupported && len(page.Items) > int(options.Limit) {
			return nil, fmt.Errorf("list returned an oversized page")
		}
		if len(page.Items) > objectBudget-len(results) {
			return nil, fmt.Errorf("list object budget exhausted (%d)", objectBudget)
		}
		if page.TotalKnown {
			if page.Total < 0 {
				return nil, fmt.Errorf("list returned a negative total")
			}
			if calls == 0 {
				total = page.Total
			} else if page.Total != total {
				return nil, fmt.Errorf("list total changed during traversal")
			}
			if int64(len(results))+int64(len(page.Items)) > int64(total) {
				return nil, fmt.Errorf("list returned more objects than its total")
			}
		}
		for _, item := range page.Items {
			id, ok := reflection.ResolveFieldPath[string](item, "id")
			if !ok || id == "" {
				return nil, fmt.Errorf("list returned an object without an identifier")
			}
			if _, exists := seen[id]; exists {
				return nil, fmt.Errorf("list repeated object identifier %q", id)
			}
			seen[id] = struct{}{}
		}
		results = append(results, page.Items...)
		if !page.TotalKnown {
			if c.console != nil {
				c.console.Errorf(ctx, "List completeness unknown: the service does not report a total; showing only the pages read.\n")
			}
			return results, nil
		}
		if int64(len(results)) == int64(total) {
			if calls > 0 && c.console != nil {
				c.console.Errorf(ctx, "List completeness unknown: multiple pages do not form a point-in-time snapshot; "+
					"concurrent changes may cause objects to be missed.\n")
			}
			return results, nil
		}
		if !page.OffsetSupported {
			return nil, fmt.Errorf("list is incomplete and the service does not support offsets")
		}
		if len(page.Items) == 0 {
			return nil, fmt.Errorf("list made no progress before reaching its total")
		}
		options.Offset, err = nextListOffset(options.Offset, len(page.Items))
		if err != nil {
			return nil, err
		}
	}
}

// nextListOffset checks in a widened domain before converting to the wire type.
func nextListOffset(current int32, count int) (int32, error) {
	if current < 0 || count < 0 || int64(count) > math.MaxInt32-int64(current) {
		return 0, fmt.Errorf("list offset overflow or negative input")
	}
	return current + int32(count), nil // #nosec G115 -- checked above
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
	var ctx context.Context
	if cmd != nil {
		ctx = cmd.Context()
	}
	return reflection.ObjectTypeNames(config.PackageNamesFromContext(ctx)...), cobra.ShellCompDirectiveNoFileComp
}

const shortHelp = `Get objects`

const longHelp = `
List or retrieve objects from the server.

When called with just an object type it lists objects of that type. When called with one or more identifiers or names
it retrieves only the matching objects. The CLI buffers list results and reads pages up to the service's reported total
within finite safety limits. A failed traversal prints no partial results. Even with an unchanged total and distinct
identifiers, results from multiple pages are not a point-in-time snapshot: concurrent changes can cause omissions or
make the read fail. The CLI warns on standard error when results span multiple pages or when the service does not
report a total. Without a total, it prints only the pages read so far.

To list clusters:

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
https://github.com/osac-project/osac/fulfillment-service/blob/main/docs/FILTER.md.

The output format can be changed with the {{ bt }}--output{{ bt }} flag. Supported formats are {{ bt }}table{{ bt }}
(default), {{ bt }}json{{ bt }} and {{ bt }}yaml{{ bt }}:

{{ bt 3 }}shell
{{ binary }} get clusters -o yaml
{{ bt 3 }}

Structured output uses protobuf field names ({{ bt }}snake_case{{ bt }}) and includes an {{ bt }}@type{{ bt }} on
each item: zero items are an empty list, one item is an object, and multiple items are a list. There is no CLI
{{ bt }}size{{ bt }}/{{ bt }}total{{ bt }}/{{ bt }}items{{ bt }} envelope; REST and gRPC response envelopes are unchanged.
Fields follow the selected public or private API descriptor, not a global output privacy filter.

## Billing objects

Use {{ bt }}billingaccount{{ bt }}/{{ bt }}billingaccounts{{ bt }},
{{ bt }}tenantbillingassignment{{ bt }}/{{ bt }}tenantbillingassignments{{ bt }}, or
{{ bt }}billingproviderstatus{{ bt }}/{{ bt }}billingproviderstatuses{{ bt }}. Fully qualified public/private names
also work. The configured API packages select short names, with private types preferred when private APIs are enabled.
The former hyphenated billing names and {{ bt }}tba{{ bt }} are not aliases. There are no billing
{{ bt }}--limit{{ bt }} or {{ bt }}--offset{{ bt }} flags or per-page notices.

Billing accounts are system-global: the selected tenant and project do not add filters. Assignments are scoped by
the selected tenant, never the selected project. Explicit CEL predicates are retained in both cases. These client
filters only narrow results; server authorization still controls visibility.

Keys match an immutable OSAC {{ bt }}id{{ bt }} or stable {{ bt }}metadata.name{{ bt }}, not the provider display
label, provider-native account reference, or locally retained attribution token. For accounts these are distinct
{{ bt }}status.display_name{{ bt }}, private {{ bt }}status.provider_account_reference{{ bt }}, and
{{ bt }}status.external_billing_account_id{{ bt }} fields. A locally retained token alone does not prove provider
matching or metering integration. Native identity is not a default table column.

Billing filters use nested fields, numeric enum values and timestamps. For example, list active accounts and current
assignments (use {{ bt }}has{{ bt }} instead of {{ bt }}!has{{ bt }} for closed assignment history):

{{ bt 3 }}shell
{{ binary }} get billingaccounts --filter 'this.status.state == 1 && this.status.state_observation_timestamp >= timestamp("2026-01-01T00:00:00Z")'
{{ binary }} get tenantbillingassignments --filter 'this.status.connection_state == 1 && !has(this.status.effective_to_timestamp)'
{{ bt 3 }}

An assignment in {{ bt }}PENDING{{ bt }} still needs provider-side matching and may require manual remediation;
gated publish and launch remain blocked with {{ bt }}BILLING_ACCOUNT_UNAVAILABLE{{ bt }}.

Billing provider status is a get-only singleton: exactly one empty Get request, independent of tenant/project.
It accepts no keys, {{ bt }}--filter{{ bt }} or {{ bt }}--watch{{ bt }}, even explicitly empty/false flags.
Its table has six horizontal status columns, not a vertical key/value display. Account and assignment tables include
{{ bt }}NAME{{ bt }} and descriptor-supported {{ bt }}DELETING{{ bt }} columns; assignments also show {{ bt }}TENANT{{ bt }}.
Empty collection tables use the generic no-matching-objects message.

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
