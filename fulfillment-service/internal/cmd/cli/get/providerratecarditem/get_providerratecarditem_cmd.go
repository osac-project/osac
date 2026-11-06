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
	result.Flags().StringVar(&runner.filter, "filter", "", filterFlagHelp)
	result.Flags().StringVar(&runner.order, "order", "", orderFlagHelp)
	result.Flags().Int32Var(&runner.offset, "offset", 0, offsetFlagHelp)
	result.Flags().Int32Var(&runner.limit, "limit", 0, limitFlagHelp)
	result.MarkFlagRequired("currency-code") //nolint:errcheck
	return result
}

type runnerContext struct {
	format       string
	tenantID     string
	service      string
	currencyCode string
	filter       string
	order        string
	offset       int32
	limit        int32
}

// run fetches the rows over gRPC and renders them in the requested format.
func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if err := output.Validate(c.format); err != nil {
		return err
	}
	if len(c.currencyCode) != 3 {
		return fmt.Errorf("invalid --currency-code %q: must be a 3-letter ISO-4217 currency code, such as USD",
			c.currencyCode)
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
	items, err := c.list(ctx, publicv1.NewProviderRateCardItemsClient(conn), cmd.Flags().Changed("limit"))
	if err != nil {
		return err
	}
	return render(ctx, terminal.ConsoleFromContext(ctx), c.format, items)
}

// list collects the rate-card rows over gRPC. Without an explicit --limit the server returns pages of its default
// size, so every page is fetched until the rate card is exhausted and the output is never silently truncated. An
// explicit --limit is sent once and verbatim, preserving the server's count-only response for --limit 0. The
// starting offset is clamped the same way the server clamps it, so the next-page arithmetic stays correct when
// paging from a negative offset.
func (c *runnerContext) list(ctx context.Context, client publicv1.ProviderRateCardItemsClient,
	explicitLimit bool) ([]*publicv1.ProviderRateCardItem, error) {
	offset := c.offset
	if offset < 0 {
		offset = 0
	}
	var items []*publicv1.ProviderRateCardItem
	for {
		builder := publicv1.ProviderRateCardItemsListRequest_builder{
			Offset:       proto.Int32(offset),
			CurrencyCode: proto.String(c.currencyCode),
		}
		if c.tenantID != "" {
			builder.TenantId = proto.String(c.tenantID)
		}
		if c.service != "" {
			builder.Service = proto.String(c.service)
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
			return nil, fmt.Errorf("failed to list provider rate card items: %w", err)
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

// render writes the rows as a table, JSON or YAML; the JSON and YAML encoding is shared in the output package.
func render(ctx context.Context, console *terminal.Console, format string, items []*publicv1.ProviderRateCardItem) error {
	if format == output.Table {
		return renderTable(console, items)
	}
	values, err := output.Messages(items)
	if err != nil {
		return fmt.Errorf("failed to encode provider rate card items: %w", err)
	}
	output.Render(ctx, console, format, values)
	return nil
}

// renderTable writes one row per rate-card item.
func renderTable(console *terminal.Console, items []*publicv1.ProviderRateCardItem) error {
	writer := tabwriter.NewWriter(console, 0, 0, 2, ' ', 0)
	var writeErr error
	printf := func(format string, args ...any) {
		if writeErr == nil {
			_, writeErr = fmt.Fprintf(writer, format, args...)
		}
	}
	printf("SERVICE\tCURRENCY\tCONDITIONS\tAMOUNT\tUNIT\tCATALOG ITEMS\n")
	for _, item := range items {
		printf("%s\t%s\t%s\t%s\t%s\t%s\n",
			item.GetService(), item.GetCurrencyCode(), renderConditions(item), item.GetRate().GetAmount(),
			item.GetRate().GetUnit(), output.OrDash(strings.Join(item.GetCatalogItemMatches(), ", ")))
	}
	if writeErr != nil {
		return writeErr
	}
	return writer.Flush()
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
conditions, rate amount and unit, and the OSAC catalog items each row can price. The {{ bt }}--currency-code{{ bt }}
flag is required: OSAC never discovers the currency of a billing account, so the 3-letter ISO-4217 code must always
be given. Only Cloud Provider Admins may run it. Fails when billing is disabled.
`

const example = `  # List the rows of one service and currency
  osac get providerratecarditem --service compute_instance --currency-code USD

  # List the rows of every service in one currency
  osac get providerratecarditem --currency-code USD

  # List rows as JSON
  osac get providerratecarditem --currency-code USD -o json

  # List only the first 20 rows
  osac get providerratecarditem --currency-code USD --limit 20`

const outputFlagHelp = `_FORMAT_ - Output format. Must be one of {{ bt }}table{{ bt }}, {{ bt }}json{{ bt }} or {{ bt }}yaml{{ bt }}.`

const tenantIDFlagHelp = `_TENANT_ID_ - Limit rows to the billing account linked to this tenant. Not supported yet: the server rejects this
filter until billing account assignment is implemented.`

const serviceFlagHelp = `_SERVICE_ - Limit rows to one OSAC service, such as {{ bt }}compute_instance{{ bt }}.`

const currencyCodeFlagHelp = `_CURRENCY_CODE_ - Required. Limit rows to one ISO-4217 currency, given as its 3-letter code, such as {{ bt }}USD{{ bt }}. OSAC
does not discover the currency, so the code must always be provided.`

const filterFlagHelp = `_EXPRESSION_ - CEL expression used for filtering results. The expression is evaluated against each row and only those for
which it returns true are included in the output.`

const orderFlagHelp = `_ORDER_ - Sort order of the results. Not implemented by the server yet; accepted for parity with other list commands.`

const offsetFlagHelp = `_INDEX_ - Index of the first result to return. Defaults to zero. Negative values are treated as zero by the server.`

const limitFlagHelp = `_COUNT_ - Maximum number of results to return. Zero returns only the total count and the server caps the value at 1000.
When not set every result is fetched, so the output is never truncated.`
