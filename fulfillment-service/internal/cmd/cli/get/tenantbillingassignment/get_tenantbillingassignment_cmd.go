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
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

const (
	formatTable = "table"
	formatJson  = "json"
	formatYaml  = "yaml"
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
	result.Flags().StringVarP(&runner.format, "output", "o", formatTable, outputFlagHelp)
	return result
}

type runnerContext struct {
	format string
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if c.format != formatTable && c.format != formatJson && c.format != formatYaml {
		return fmt.Errorf("unknown output format '%s', should be '%s', '%s' or '%s'",
			c.format, formatTable, formatJson, formatYaml)
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
	response, err := publicv1.NewTenantBillingAssignmentsClient(conn).List(
		ctx, publicv1.TenantBillingAssignmentsListRequest_builder{}.Build())
	if err != nil {
		return fmt.Errorf("failed to list tenant billing assignments: %w", err)
	}
	return render(ctx, terminal.ConsoleFromContext(ctx), c.format, response)
}

func render(ctx context.Context, console *terminal.Console, format string,
	response *publicv1.TenantBillingAssignmentsListResponse) error {
	if format == formatTable {
		renderTable(ctx, console, response.GetItems())
		return nil
	}
	data, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(response)
	if err != nil {
		return fmt.Errorf("failed to encode tenant billing assignments: %w", err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("failed to encode tenant billing assignments: %w", err)
	}
	if format == formatJson {
		console.RenderJson(ctx, value)
	} else {
		console.RenderYaml(ctx, value)
	}
	return nil
}

// renderTable writes a compact table of assignments. An unset connection state is rendered as UNKNOWN rather
// than left blank, and the PENDING remediation message (or any other status message) is always shown, never
// truncated to a placeholder, so an operator can act on it directly from this output.
func renderTable(ctx context.Context, console *terminal.Console, items []*publicv1.TenantBillingAssignment) {
	if len(items) == 0 {
		console.Infof(ctx, "No tenant billing assignments found.\n")
		return
	}
	writer := tabwriter.NewWriter(console, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tTENANT\tACCOUNT\tSTATE\tEFFECTIVE-FROM\tEFFECTIVE-TO\tMESSAGE")
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
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			item.GetId(), orDash(item.GetSpec().GetTenantId()), orDash(item.GetSpec().GetBillingAccountId()),
			state, from, to, orDash(item.GetStatus().GetMessage()))
	}
	writer.Flush()
}

func orDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

const shortHelp = `List tenant billing assignments`

const longHelp = `
List tenant/billing-account assignments. Only Cloud Provider Admins may run this command.
{{ bt }}STATE{{ bt }} is {{ bt }}PENDING{{ bt }}, {{ bt }}CONNECTED{{ bt }} or {{ bt }}FAILED{{ bt }}; a value
OSAC could not classify is shown as {{ bt }}UNKNOWN{{ bt }}. {{ bt }}MESSAGE{{ bt }} carries the PENDING
remediation detail, such as {{ bt }}BILLING_PROVIDER_CORRELATION_WRITE_UNSUPPORTED{{ bt }}, when there is one.
`

const example = `  # List tenant billing assignments
  osac get tenantbillingassignment

  # List as JSON
  osac get tenantbillingassignment -o json`

const outputFlagHelp = `_FORMAT_ - Output format. Must be one of {{ bt }}table{{ bt }}, {{ bt }}json{{ bt }} or {{ bt }}yaml{{ bt }}.`
