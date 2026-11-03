/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package billingaccount

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/get/output"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// Cmd creates the command that lists discovered billing accounts.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:     "billingaccount [FLAG...]",
		Aliases: []string{"billingaccounts", "billing-accounts"},
		Short:   shortHelp,
		Long:    longHelp,
		Example: example,
		Args:    cobra.NoArgs,
		RunE:    runner.run,
	}
	result.Flags().StringVarP(&runner.format, "output", "o", output.Table, output.FlagHelp)
	return result
}

type runnerContext struct {
	format string
}

// run validates the output format, connects using the configured login, and renders the listed billing accounts.
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
	response, err := publicv1.NewBillingAccountsClient(conn).List(
		ctx, publicv1.BillingAccountsListRequest_builder{}.Build())
	if err != nil {
		return fmt.Errorf("failed to list billing accounts: %w", err)
	}
	return render(ctx, terminal.ConsoleFromContext(ctx), c.format, response)
}

// render writes response as a table, JSON, or YAML depending on format.
func render(ctx context.Context, console *terminal.Console, format string,
	response *publicv1.BillingAccountsListResponse) error {
	if format == output.Table {
		renderTable(ctx, console, response.GetItems())
		return nil
	}
	return output.Render(ctx, console, format, "billing accounts", response)
}

// renderTable writes a compact table of discovered billing accounts. An empty currency and an unknown state are
// rendered explicitly - "-" and "UNKNOWN" - rather than left blank, so a viewer can tell "not supplied" apart
// from a rendering bug. The display-name and correlation-id cells are provider-supplied text and are sanitized
// on the way out - ingest already sanitizes stored display names, but correlation values are stored raw for
// exact matching, and rows written before that are best not trusted either.
func renderTable(ctx context.Context, console *terminal.Console, items []*publicv1.BillingAccount) {
	if len(items) == 0 {
		console.Infof(ctx, "No billing accounts found.\n")
		return
	}
	writer := tabwriter.NewWriter(console, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tPROVIDER\tDISPLAY-NAME\tCURRENCY\tSTATE\tCORRELATION-ID\tSTATUS-OBSERVED")
	for _, item := range items {
		observed := "-"
		if item.HasStatusObservedAt() {
			observed = item.GetStatusObservedAt().AsTime().Format(time.RFC3339)
		}
		state := strings.TrimPrefix(item.GetState().String(), "BILLING_ACCOUNT_STATE_")
		if state == "" || state == "UNSPECIFIED" {
			state = "UNKNOWN"
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			item.GetId(), output.OrDash(item.GetProviderKey()),
			output.OrDash(billing.SanitizeProviderText(item.GetDisplayName())),
			output.OrDash(item.GetBaseCurrency()), state,
			output.OrDash(billing.SanitizeProviderText(item.GetCorrelationId())), observed)
	}
	writer.Flush()
}

const shortHelp = `List discovered billing accounts`

const longHelp = `
List the accounts discovered from the configured billing provider. An empty list means no accounts have been
discovered yet; it does not mean billing is disabled. {{ bt }}CURRENCY{{ bt }} shows {{ bt }}-{{ bt }} when the
provider does not supply one. {{ bt }}STATE{{ bt }} is {{ bt }}ACTIVE{{ bt }}, {{ bt }}INACTIVE{{ bt }} or
{{ bt }}UNKNOWN{{ bt }}; a value OSAC could not classify is always shown as {{ bt }}UNKNOWN{{ bt }}, never guessed
as {{ bt }}ACTIVE{{ bt }}. Only Cloud Provider Admins may run this command.
`

const example = `  # List discovered billing accounts
  osac get billingaccount

  # List as JSON
  osac get billingaccount -o json`
