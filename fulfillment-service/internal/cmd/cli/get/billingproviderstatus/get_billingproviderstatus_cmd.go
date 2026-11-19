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
	"unicode"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/packages"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
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
		Aliases: []string{"billingproviderstatuses", packages.PublicV1 + ".BillingProviderStatus", packages.PrivateV1 + ".BillingProviderStatus"},
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
	_, usePrivate := cfg.Packages()[packages.PrivateV1]
	switch cmd.CalledAs() {
	case packages.PublicV1 + ".BillingProviderStatus":
		usePrivate = false
	case packages.PrivateV1 + ".BillingProviderStatus":
		if !usePrivate {
			return fmt.Errorf("private billing provider status requires private API access")
		}
	}
	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()
	console := terminal.ConsoleFromContext(ctx)
	if usePrivate {
		response, err := privatev1.NewBillingProviderStatusesClient(conn).Get(ctx, &privatev1.BillingProviderStatusesGetRequest{})
		if err != nil {
			return fmt.Errorf("failed to get billing provider status: %w", err)
		}
		if !response.HasObject() {
			return fmt.Errorf("no object returned by billing provider status Get")
		}
		return render(ctx, console, c.format, response.GetObject())
	}
	response, err := publicv1.NewBillingProviderStatusesClient(conn).Get(
		ctx, publicv1.BillingProviderStatusesGetRequest_builder{}.Build())
	if err != nil {
		return fmt.Errorf("failed to get billing provider status: %w", err)
	}
	if !response.HasObject() {
		return fmt.Errorf("no object returned by billing provider status Get")
	}
	return render(ctx, console, c.format, response.GetObject())
}

// render writes the status as a table, JSON or YAML.
func render(ctx context.Context, console *terminal.Console, format string, status proto.Message) error {
	if format == formatTable {
		switch status := status.(type) {
		case *publicv1.BillingProviderStatus:
			return renderTable(console, status)
		case *privatev1.BillingProviderStatus:
			return renderTable(console, status)
		}
		return fmt.Errorf("unexpected billing provider status type %T", status)
	}
	object, err := anypb.New(status)
	if err != nil {
		return fmt.Errorf("failed to encode billing provider status: %w", err)
	}
	data, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(object)
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

type statusEnum interface {
	~int32
	String() string
}

// renderTable uses the same columns for both API versions without registering a collection layout.
func renderTable[S, C, U statusEnum](console *terminal.Console, status interface {
	GetState() S
	GetProviderKey() string
	GetMessage() string
	GetSupportedCapabilities() []C
	GetLastSuccessfulCheck() *timestamppb.Timestamp
	GetUsageDeliveryState() U
}) error {
	capabilities := make([]string, 0, len(status.GetSupportedCapabilities()))
	for _, capability := range status.GetSupportedCapabilities() {
		capabilities = append(capabilities, enumLabel(capability, "BILLING_PROVIDER_CAPABILITY_"))
	}
	lastCheck := "-"
	if timestamp := status.GetLastSuccessfulCheck(); timestamp != nil {
		lastCheck = timestamp.AsTime().Format(time.RFC3339Nano)
	}
	writer := tabwriter.NewWriter(console, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "STATE\tPROVIDER\tMESSAGE\tCAPABILITIES\tLAST-SUCCESSFUL-CHECK\tUSAGE-DELIVERY")
	fmt.Fprintln(writer, strings.Join([]string{
		enumLabel(status.GetState(), "BILLING_PROVIDER_STATE_"),
		orDash(status.GetProviderKey()), orDash(status.GetMessage()),
		orDash(strings.Join(capabilities, ",")), lastCheck,
		enumLabel(status.GetUsageDeliveryState(), "BILLING_USAGE_DELIVERY_STATE_"),
	}, "\t"))
	return writer.Flush()
}

func enumLabel[E statusEnum](value E, prefix string) string {
	if label, ok := strings.CutPrefix(value.String(), prefix); ok {
		return label
	}
	return fmt.Sprintf("UNKNOWN:%d", value)
}

// orDash replaces an empty value with a dash for table output.
func orDash(value string) string {
	if value == "" {
		return "-"
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
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
