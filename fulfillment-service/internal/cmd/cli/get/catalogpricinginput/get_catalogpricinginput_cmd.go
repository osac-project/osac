/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package catalogpricinginput

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/output"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// Cmd creates the command that shows the metered catalog pricing input inventory.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:     "catalogpricinginput [FLAG...]",
		Aliases: []string{"catalogpricinginputs"},
		Short:   shortHelp,
		Long:    longHelp,
		Example: example,
		Args:    cobra.NoArgs,
		RunE:    runner.run,
	}
	result.Flags().StringVarP(&runner.format, "output", "o", output.Table, outputFlagHelp)
	result.Flags().StringVar(&runner.filter, "filter", "", filterFlagHelp)
	result.Flags().StringVar(&runner.order, "order", "", orderFlagHelp)
	result.Flags().Int32Var(&runner.offset, "offset", 0, offsetFlagHelp)
	result.Flags().Int32Var(&runner.limit, "limit", 0, limitFlagHelp)
	return result
}

type runnerContext struct {
	format string
	filter string
	order  string
	offset int32
	limit  int32
}

// run fetches the inventory over gRPC and renders it in the requested format.
func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if err := output.Validate(c.format); err != nil {
		return err
	}
	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}
	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()
	items, err := c.list(ctx, publicv1.NewCatalogPricingInputsClient(conn), cmd.Flags().Changed("limit"))
	if err != nil {
		return err
	}
	return render(ctx, terminal.ConsoleFromContext(ctx), c.format, items)
}

// list collects the inventory rows over gRPC. Without an explicit --limit the server returns pages of its default
// size, so every page is fetched until the inventory is exhausted and the output is never silently truncated. An
// explicit --limit is sent once and verbatim, preserving the server's count-only response for --limit 0. The
// starting offset is clamped the same way the server clamps it, so the next-page arithmetic stays correct when
// paging from a negative offset.
func (c *runnerContext) list(ctx context.Context, client publicv1.CatalogPricingInputsClient,
	explicitLimit bool) ([]*publicv1.CatalogPricingInput, error) {
	offset := c.offset
	if offset < 0 {
		offset = 0
	}
	var items []*publicv1.CatalogPricingInput
	for {
		builder := publicv1.CatalogPricingInputsListRequest_builder{
			Offset: proto.Int32(offset),
		}
		if c.filter != "" {
			builder.Filter = proto.String(c.filter)
		}
		if c.order != "" {
			builder.Order = proto.String(c.order)
		}
		if explicitLimit {
			builder.Limit = proto.Int32(c.limit)
		}
		response, err := client.List(ctx, builder.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to list catalog pricing inputs: %w", err)
		}
		items = append(items, response.GetItems()...)
		if explicitLimit {
			return items, nil
		}
		offset += response.GetSize()
		if response.GetSize() == 0 || offset >= response.GetTotal() {
			return items, nil
		}
	}
}

// render writes the inventory as a table, JSON or YAML; the JSON and YAML encoding is shared in the output package.
func render(ctx context.Context, console *terminal.Console, format string,
	items []*publicv1.CatalogPricingInput) error {
	if format == output.Table {
		renderTable(console, items)
		return nil
	}
	values, err := output.Messages(items)
	if err != nil {
		return fmt.Errorf("failed to encode catalog pricing inputs: %w", err)
	}
	output.Render(ctx, console, format, values)
	return nil
}

// renderTable writes one row per (service, dimension, value) tuple.
func renderTable(console *terminal.Console, items []*publicv1.CatalogPricingInput) {
	writer := tabwriter.NewWriter(console, 0, 0, 2, ' ', 0)
	fmt.Fprintf(writer, "SERVICE\tDIMENSION\tVALUE\tACCEPTS ANY VALUE\tCATALOG ITEMS\n")
	for _, item := range items {
		value := item.GetValue()
		if item.GetAcceptsAnyValue() && value == "" {
			value = "<any>"
		}
		names := make([]string, 0, len(item.GetCatalogItems()))
		for _, catalogItem := range item.GetCatalogItems() {
			names = append(names, catalogItem.GetName())
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%t\t%s\n",
			item.GetService(), item.GetDimension(), value, item.GetAcceptsAnyValue(), strings.Join(names, ", "))
	}
	writer.Flush()
}

const shortHelp = `Show the metered catalog pricing input inventory`

const longHelp = `
List every distinct metered {{ bt }}(service, dimension, value){{ bt }} tuple across draft and published VMaaS and
CaaS catalog items, with the catalog items that use or offer each value. Compare this inventory against a billing
provider's rate card to find gaps before publishing. Only cloud provider administrators may run it.
`

const example = `  # Show the inventory
  osac get catalogpricinginput

  # Show the inventory as JSON
  osac get catalogpricinginput -o json

  # Show only the rows of one service
  osac get catalogpricinginput --filter "this.service == 'cluster'"

  # Show only the first 20 rows
  osac get catalogpricinginput --limit 20`

const outputFlagHelp = `_FORMAT_ - Output format. Must be one of {{ bt }}table{{ bt }}, {{ bt }}json{{ bt }} or {{ bt }}yaml{{ bt }}.`

const filterFlagHelp = `_EXPRESSION_ - CEL expression used for filtering results. The expression is evaluated against each row and only those for
which it returns true are included in the output.`

const orderFlagHelp = `_ORDER_ - Sort order of the results. Not implemented by the server yet; accepted for parity with other list commands.`

const offsetFlagHelp = `_INDEX_ - Index of the first result to return. Defaults to zero. Negative values are treated as zero by the server.`

const limitFlagHelp = `_COUNT_ - Maximum number of results to return. Zero returns only the total count and the server caps the value at 1000.
When not set every result is fetched, so the output is never truncated.`
