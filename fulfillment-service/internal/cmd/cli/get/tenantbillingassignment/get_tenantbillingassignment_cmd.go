/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package tenantbillingassignment

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/get/output"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// Cmd creates the command that lists tenant billing assignments.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:     "tenantbillingassignment [FLAG...]",
		Aliases: []string{"tenantbillingassignments", "tenant-billing-assignments", "tba"},
		Short:   shortHelp,
		Long:    longHelp,
		Example: example,
		Args:    cobra.NoArgs,
		RunE:    runner.run,
	}
	result.Flags().StringVarP(&runner.format, "output", "o", output.Table, output.FlagHelp)
	result.Flags().Int32Var(&runner.limit, "limit", 0, limitFlagHelp)
	result.Flags().Int32Var(&runner.offset, "offset", 0, offsetFlagHelp)
	return result
}

type runnerContext struct {
	format string
	limit  int32
	offset int32
}

// run validates the output format and the page bounds, connects using the configured login, and renders the listed
// tenant billing assignments.
func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if err := output.Validate(c.format); err != nil {
		return err
	}
	if c.limit < 0 {
		return fmt.Errorf("--limit must be >= 0, got %d", c.limit)
	}
	if c.offset < 0 {
		return fmt.Errorf("--offset must be >= 0, got %d", c.offset)
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
	response, err := listTenantBillingAssignments(ctx, publicv1.NewTenantBillingAssignmentsClient(conn), c.limit, c.offset)
	if err != nil {
		return err
	}
	return render(ctx, terminal.ConsoleFromContext(ctx), c.format, c.offset, response)
}

// listTenantBillingAssignments requests one page of assignments, sending limit and offset as explicit fields so
// the server sees the bounds even when they are zero.
func listTenantBillingAssignments(ctx context.Context, client publicv1.TenantBillingAssignmentsClient,
	limit, offset int32) (*publicv1.TenantBillingAssignmentsListResponse, error) {
	response, err := client.List(ctx, publicv1.TenantBillingAssignmentsListRequest_builder{
		Limit:  proto.Int32(limit),
		Offset: proto.Int32(offset),
	}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to list tenant billing assignments: %w", err)
	}
	return response, nil
}

// render writes response as a table, JSON, or YAML depending on format. In table mode the paging continuation
// hint goes to the console's error stream; JSON and YAML keep the response envelope, which carries size and total.
func render(ctx context.Context, console *terminal.Console, format string, offset int32,
	response *publicv1.TenantBillingAssignmentsListResponse) error {
	if format == output.Table {
		return renderTable(ctx, console, offset, response)
	}
	return output.Render(ctx, console, format, "tenant billing assignments", response)
}

// renderTable writes a compact table of assignments. An unset connection state is rendered as UNKNOWN rather
// than left blank, and the PENDING remediation message (or any other status message) is always shown, never
// truncated to a placeholder, so an operator can act on it directly from this output. When the page does not
// reach the total, a continuation hint is written to the console's error stream after the table.
func renderTable(ctx context.Context, console *terminal.Console, offset int32,
	response *publicv1.TenantBillingAssignmentsListResponse) error {
	items := response.GetItems()
	total := response.GetTotal()
	if len(items) == 0 {
		if total == 0 {
			return console.Infof(ctx, "No tenant billing assignments found.\n")
		}
		return output.WritePageNotice(console.Stderr(), "tenant billing assignments", offset, 0, total)
	}
	writer := tabwriter.NewWriter(console, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "ID\tTENANT\tACCOUNT\tSTATE\tEFFECTIVE-FROM\tEFFECTIVE-TO\tMESSAGE"); err != nil {
		return fmt.Errorf("failed to render tenant billing assignments table header: %w", err)
	}
	for _, item := range items {
		from := "-"
		if item.GetStatus().HasEffectiveFrom() {
			from = item.GetStatus().GetEffectiveFrom().AsTime().Format(time.RFC3339)
		}
		to := "-"
		if item.GetStatus().HasEffectiveTo() {
			to = item.GetStatus().GetEffectiveTo().AsTime().Format(time.RFC3339)
		}
		state := strings.TrimPrefix(item.GetStatus().GetConnectionState().String(), "TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_")
		if state == "" || state == "UNSPECIFIED" {
			state = "UNKNOWN"
		}
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			item.GetId(), output.OrDash(item.GetSpec().GetTenantId()), output.OrDash(item.GetSpec().GetBillingAccountId()),
			state, from, to, output.OrDash(item.GetStatus().GetMessage())); err != nil {
			return fmt.Errorf("failed to render tenant billing assignments table row: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush tenant billing assignments table: %w", err)
	}
	return output.WritePageNotice(console.Stderr(), "tenant billing assignments", offset, len(items), total)
}

const shortHelp = `List tenant billing assignments`

const longHelp = `
List tenant/billing-account assignments. Only Cloud Provider Admins may run this command.
{{ bt }}STATE{{ bt }} is {{ bt }}PENDING{{ bt }}, {{ bt }}CONNECTED{{ bt }} or {{ bt }}FAILED{{ bt }}; a value
OSAC could not classify is shown as {{ bt }}UNKNOWN{{ bt }}. {{ bt }}MESSAGE{{ bt }} carries the PENDING
remediation detail, such as {{ bt }}BILLING_PROVIDER_CORRELATION_WRITE_UNSUPPORTED{{ bt }}, when there is one.

Results are paged. {{ bt }}--limit 0{{ bt }} asks for the server default page of 100 assignments, values above
1000 are clamped to 1000 by the server rather than rejected, and {{ bt }}--offset{{ bt }} skips ahead. In table
mode, when more assignments remain, a continuation hint such as {{ bt }}Showing 1-100 of 240 tenant billing
assignments. Next page: --offset 100{{ bt }} is written to standard error so it never mixes with the table on
standard output; JSON and YAML output include the {{ bt }}size{{ bt }} and {{ bt }}total{{ bt }} fields of the
response instead. Paging is not a snapshot: rows created or deleted between requests can shift an offset, so do
not treat separately fetched pages as a consistent export.
`

const example = `  # List tenant billing assignments
  osac get tenantbillingassignment

  # List as JSON
  osac get tenantbillingassignment -o json

  # List one page of up to 50 assignments
  osac get tenantbillingassignment --limit 50

  # List the next page
  osac get tenantbillingassignment --limit 50 --offset 50`

const limitFlagHelp = `
_N_ - Maximum number of tenant billing assignments to return. {{ bt }}0{{ bt }} (the default) asks for the server
default page of 100, and values above 1000 are clamped to 1000 by the server, not rejected. In table mode a
continuation hint for the next page is written to standard error; JSON and YAML output includes the
{{ bt }}size{{ bt }} and {{ bt }}total{{ bt }} fields instead. Must be >= 0.
`

const offsetFlagHelp = `
_N_ - Index of the first tenant billing assignment to return, {{ bt }}0{{ bt }} (the default) first. Must be >= 0.
Use the offset from the table-mode standard error continuation hint to fetch the next page.
`
