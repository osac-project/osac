/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package billingproviderstatus

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

// Cmd creates the command that shows the billing provider status.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:     "billingproviderstatus [FLAG...]",
		Aliases: []string{"billing-provider-status"},
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

// run fetches the status over gRPC and renders it in the requested format.
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
	response, err := publicv1.NewBillingProviderStatusesClient(conn).Get(
		ctx, publicv1.BillingProviderStatusesGetRequest_builder{}.Build())
	if err != nil {
		return fmt.Errorf("failed to get billing provider status: %w", err)
	}
	return render(ctx, terminal.ConsoleFromContext(ctx), c.format, response.GetObject())
}

// render writes the status as a table, JSON or YAML.
func render(ctx context.Context, console *terminal.Console, format string,
	status *publicv1.BillingProviderStatus) error {
	if format == formatTable {
		renderTable(console, status)
		return nil
	}
	data, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(status)
	if err != nil {
		return fmt.Errorf("failed to encode billing provider status: %w", err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("failed to encode billing provider status: %w", err)
	}
	if format == formatJson {
		console.RenderJson(ctx, value)
	} else {
		console.RenderYaml(ctx, value)
	}
	return nil
}

// renderTable writes the status fields as aligned key/value rows.
func renderTable(console *terminal.Console, status *publicv1.BillingProviderStatus) {
	capabilities := make([]string, 0, len(status.GetSupportedCapabilities()))
	for _, capability := range status.GetSupportedCapabilities() {
		capabilities = append(capabilities, strings.TrimPrefix(capability.String(), "BILLING_PROVIDER_CAPABILITY_"))
	}
	lastCheck := "-"
	if status.HasLastSuccessfulCheck() {
		lastCheck = status.GetLastSuccessfulCheck().AsTime().Format(time.RFC3339)
	}
	writer := tabwriter.NewWriter(console, 0, 0, 2, ' ', 0)
	fmt.Fprintf(writer, "State:\t%s\n", strings.TrimPrefix(status.GetState().String(), "BILLING_PROVIDER_STATE_"))
	fmt.Fprintf(writer, "Provider:\t%s\n", orDash(status.GetProviderKey()))
	fmt.Fprintf(writer, "Message:\t%s\n", orDash(status.GetMessage()))
	fmt.Fprintf(writer, "Capabilities:\t%s\n", orDash(strings.Join(capabilities, ", ")))
	fmt.Fprintf(writer, "Last successful check:\t%s\n", lastCheck)
	fmt.Fprintf(writer, "Usage delivery:\t%s\n",
		strings.TrimPrefix(status.GetUsageDeliveryState().String(), "BILLING_USAGE_DELIVERY_STATE_"))
	writer.Flush()
}

// orDash replaces an empty value with a dash for table output.
func orDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

const shortHelp = `Show the billing provider status`

const longHelp = `
Show whether billing is configured and whether the billing provider account API is reachable. The state is
{{ bt }}NOT_CONFIGURED{{ bt }} when billing is off, {{ bt }}AVAILABLE{{ bt }} after a successful probe and
{{ bt }}UNAVAILABLE{{ bt }} otherwise. Only cloud provider and infrastructure administrators may run it.
`

const example = `  # Show the status
  osac get billingproviderstatus

  # Show the status as JSON
  osac get billingproviderstatus -o json`

const outputFlagHelp = `_FORMAT_ - Output format. Must be one of {{ bt }}table{{ bt }}, {{ bt }}json{{ bt }} or {{ bt }}yaml{{ bt }}.`
