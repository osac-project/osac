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
	return result
}

type runnerContext struct {
	format string
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
	response, err := publicv1.NewCatalogPricingInputsClient(conn).List(
		ctx, publicv1.CatalogPricingInputsListRequest_builder{}.Build())
	if err != nil {
		return fmt.Errorf("failed to list catalog pricing inputs: %w", err)
	}
	return render(ctx, terminal.ConsoleFromContext(ctx), c.format, response.GetItems())
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
  osac get catalogpricinginput -o json`

const outputFlagHelp = `_FORMAT_ - Output format. Must be one of {{ bt }}table{{ bt }}, {{ bt }}json{{ bt }} or {{ bt }}yaml{{ bt }}.`
