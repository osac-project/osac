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
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/rendering"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
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
	result.Flags().StringVarP(&runner.format, "output", "o", rendering.Table, outputFlagHelp)
	return result
}

type runnerContext struct {
	format string
}

// run fetches the status over gRPC and renders it in the requested format.
func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if err := rendering.ValidateFormat(c.format); err != nil {
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
	response, err := publicv1.NewBillingProviderStatusesClient(conn).Get(
		ctx, publicv1.BillingProviderStatusesGetRequest_builder{}.Build())
	if err != nil {
		return fmt.Errorf("failed to get billing provider status: %w", err)
	}
	return render(ctx, terminal.ConsoleFromContext(ctx), c.format, response.GetObject())
}

// render writes the status as a table, JSON or YAML; the JSON and YAML encoding is shared in the rendering package.
func render(ctx context.Context, console *terminal.Console, format string,
	status *publicv1.BillingProviderStatus) error {
	if format == rendering.Table {
		return renderTable(console, status)
	}
	value, err := rendering.Encode(status)
	if err != nil {
		return fmt.Errorf("failed to encode billing provider status: %w", err)
	}
	if format == rendering.JSON {
		console.RenderJson(ctx, value)
	} else {
		console.RenderYaml(ctx, value)
	}
	return nil
}

// renderTable writes the status fields as aligned key/value rows.
func renderTable(console *terminal.Console, status *publicv1.BillingProviderStatus) error {
	capabilities := make([]string, 0, len(status.GetSupportedCapabilities()))
	for _, capability := range status.GetSupportedCapabilities() {
		capabilities = append(capabilities, strings.TrimPrefix(capability.String(), "BILLING_PROVIDER_CAPABILITY_"))
	}
	lastCheck := "-"
	if status.HasLastSuccessfulCheck() {
		lastCheck = status.GetLastSuccessfulCheck().AsTime().Format(time.RFC3339)
	}
	writer := tabwriter.NewWriter(console, 0, 0, 2, ' ', 0)
	var writeErr error
	printf := func(format string, args ...any) {
		if writeErr == nil {
			_, writeErr = fmt.Fprintf(writer, format, args...)
		}
	}
	printf("State:\t%s\n", strings.TrimPrefix(status.GetState().String(), "BILLING_PROVIDER_STATE_"))
	printf("Provider:\t%s\n", rendering.Dash(status.GetProviderKey()))
	printf("Message:\t%s\n", rendering.Dash(status.GetMessage()))
	printf("Capabilities:\t%s\n", rendering.Dash(strings.Join(capabilities, ", ")))
	printf("Last successful check:\t%s\n", lastCheck)
	printf("Usage delivery:\t%s\n",
		strings.TrimPrefix(status.GetUsageDeliveryState().String(), "BILLING_USAGE_DELIVERY_STATE_"))
	if writeErr != nil {
		return writeErr
	}
	return writer.Flush()
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
