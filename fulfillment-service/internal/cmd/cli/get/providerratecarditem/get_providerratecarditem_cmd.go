/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package providerratecarditem

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

// Cmd creates the command that lists provider rate-card rows.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:     "providerratecarditem [FLAG...]",
		Aliases: []string{"providerratecarditems", "provider-rate-card-items"},
		Short:   shortHelp,
		Long:    longHelp,
		Example: example,
		Args:    cobra.NoArgs,
		RunE:    runner.run,
	}
	result.Flags().StringVarP(&runner.format, "output", "o", output.Table, outputFlagHelp)
	result.Flags().StringVar(&runner.tenantID, "tenant-id", "", tenantIDFlagHelp)
	result.Flags().StringVar(&runner.service, "service", "", serviceFlagHelp)
	result.Flags().StringVar(&runner.currencyCode, "currency-code", "", currencyCodeFlagHelp)
	return result
}

type runnerContext struct {
	format       string
	tenantID     string
	service      string
	currencyCode string
}

// run fetches the rows over gRPC and renders them in the requested format.
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
	response, err := publicv1.NewProviderRateCardItemsClient(conn).List(ctx, publicv1.ProviderRateCardItemsListRequest_builder{
		TenantId:     proto.String(c.tenantID),
		Service:      proto.String(c.service),
		CurrencyCode: proto.String(c.currencyCode),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to list provider rate card items: %w", err)
	}
	return render(ctx, terminal.ConsoleFromContext(ctx), c.format, response.GetItems())
}

// render writes the rows as a table, JSON or YAML; the JSON and YAML encoding is shared in the output package.
func render(ctx context.Context, console *terminal.Console, format string, items []*publicv1.ProviderRateCardItem) error {
	if format == output.Table {
		renderTable(console, items)
		return nil
	}
	values, err := output.Messages(items)
	if err != nil {
		return fmt.Errorf("failed to encode provider rate card items: %w", err)
	}
	output.Render(ctx, console, format, values)
	return nil
}

// renderTable writes one row per rate-card item.
func renderTable(console *terminal.Console, items []*publicv1.ProviderRateCardItem) {
	writer := tabwriter.NewWriter(console, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "SERVICE\tCURRENCY\tCONDITIONS\tAMOUNT\tUNIT\tCATALOG ITEMS")
	for _, item := range items {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n",
			item.GetService(), item.GetCurrencyCode(), renderConditions(item), item.GetRate().GetAmount(),
			item.GetRate().GetUnit(), output.OrDash(strings.Join(item.GetCatalogItemMatches(), ", ")))
	}
	writer.Flush()
}

// renderConditions renders every resource condition as "dimension=value" (ANY omits the value), joined by "&".
func renderConditions(item *publicv1.ProviderRateCardItem) string {
	conditions := item.GetResourceConditions()
	if len(conditions) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		if condition.GetOperator() == publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_EQUALS {
			parts = append(parts, fmt.Sprintf("%s=%s", condition.GetDimension(), condition.GetValue()))
		} else {
			parts = append(parts, fmt.Sprintf("%s=ANY", condition.GetDimension()))
		}
	}
	return strings.Join(parts, " & ")
}

const shortHelp = `List provider rate-card rows`

const longHelp = `
List the billing provider's rate-card rows as currency-specific, provider-neutral rows: service, currency, match
conditions, rate amount and unit, and the OSAC catalog items each row can price. Only Cloud Provider Admins may run
it. Fails when billing is disabled.
`

const example = `  # List every row for the default mapped service
  osac get providerratecarditem

  # List rows for one service and currency
  osac get providerratecarditem --service compute_instance --currency-code USD

  # List the rate a tenant's linked account resolves
  osac get providerratecarditem --tenant-id tenant-a

  # List rows as JSON
  osac get providerratecarditem -o json`

const outputFlagHelp = `_FORMAT_ - Output format. Must be one of {{ bt }}table{{ bt }}, {{ bt }}json{{ bt }} or {{ bt }}yaml{{ bt }}.`

const tenantIDFlagHelp = `_TENANT_ID_ - Read the rate resolved for this tenant's linked billing account.`

const serviceFlagHelp = `_SERVICE_ - Limit rows to one OSAC service, such as {{ bt }}compute_instance{{ bt }}.`

const currencyCodeFlagHelp = `_CURRENCY_CODE_ - Limit rows to one ISO-4217 currency, such as {{ bt }}USD{{ bt }}.`
