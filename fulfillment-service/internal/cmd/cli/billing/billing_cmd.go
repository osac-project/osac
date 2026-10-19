//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package billing

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// Cmd displays provider status and reported costs.
func Cmd() *cobra.Command {
	result := &cobra.Command{
		Use:   "billing",
		Short: "View billing information",
		Long: `View billing provider status and costs.

Use {{ bt }}{{ binary }} get billingaccounts{{ bt }} to list provider accounts, and
{{ bt }}{{ binary }} get tenantbillingassignments{{ bt }} to list tenant links.`,
	}
	result.AddCommand(statusCmd(), costsCmd())
	return result
}

func withClient(cmd *cobra.Command, run func(context.Context, *grpc.ClientConn, io.Writer) error) error {
	ctx := cmd.Context()
	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}
	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()
	return run(ctx, conn, cmd.OutOrStdout())
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show billing provider status",
		Long:  "Show provider API reachability, usage delivery state, and supported operations.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withClient(cmd, func(ctx context.Context, conn *grpc.ClientConn, w io.Writer) error {
				response, err := publicv1.NewBillingProviderStatusesClient(conn).Get(ctx, &publicv1.BillingProviderStatusesGetRequest{})
				if err != nil {
					return fmt.Errorf("failed to get billing provider status: %w", err)
				}
				status := response.GetObject()
				if _, err := fmt.Fprintf(w, "State: %s\nProvider: %s\n", enumSuffix(status.GetState().String(), "BILLING_PROVIDER_STATE_"), clean(status.GetProviderKey())); err != nil {
					return err
				}
				if status.GetMessage() != "" {
					if _, err := fmt.Fprintf(w, "Message: %s\n", clean(status.GetMessage())); err != nil {
						return err
					}
				}
				if _, err := fmt.Fprintf(w, "Usage delivery: %s\n", enumSuffix(status.GetUsageDeliveryState().String(), "BILLING_USAGE_DELIVERY_STATE_")); err != nil {
					return err
				}
				if status.GetLastSuccessfulCheck() != nil {
					if _, err := fmt.Fprintf(w, "Last successful check: %s\n", status.GetLastSuccessfulCheck().AsTime().Format(time.RFC3339)); err != nil {
						return err
					}
				}
				for _, capability := range status.GetSupportedCapabilities() {
					if _, err := fmt.Fprintf(w, "Capability: %s\n", enumSuffix(capability.String(), "BILLING_PROVIDER_CAPABILITY_")); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
}

type costsOptions struct {
	from  string
	to    string
	limit int32
}

func costsCmd() *cobra.Command {
	options := &costsOptions{}
	result := &cobra.Command{
		Use:   "costs [FLAG...]",
		Short: "List billed costs",
		Long: `List provider-calculated costs for a billing period.

Use the global {{ bt }}--tenant{{ bt }} flag to select a tenant when authorized.
The provider may report costs without project or resource attribution.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := options.validate(); err != nil {
				return err
			}
			return withClient(cmd, func(ctx context.Context, conn *grpc.ClientConn, w io.Writer) error {
				request := publicv1.BillingCostsListRequest_builder{FromDate: options.from, ToDate: options.to, Limit: proto.Int32(options.limit)}
				if tenant := config.TenantFromContext(ctx); tenant != "" {
					request.TenantId = &tenant
				}
				response, err := publicv1.NewBillingCostsClient(conn).List(ctx, request.Build())
				if err != nil {
					return fmt.Errorf("failed to list billing costs: %w", err)
				}
				table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
				if _, err := fmt.Fprintln(table, "SERVICE\tPROJECT\tRESOURCE\tAMOUNT\tCURRENCY\tFROM\tTO"); err != nil {
					return err
				}
				for _, cost := range response.GetItems() {
					if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", clean(cost.GetService()), optional(cost.HasProjectId(), cost.GetProjectId()), optional(cost.HasResourceId(), cost.GetResourceId()), clean(cost.GetAmount()), clean(cost.GetCurrencyCode()), clean(cost.GetPeriodStart()), clean(cost.GetPeriodEnd())); err != nil {
						return err
					}
				}
				return table.Flush()
			})
		},
	}
	flags := result.Flags()
	flags.StringVar(&options.from, "from", "", "_[DATE]_ - First day in the billing period (YYYY-MM-DD)")
	flags.StringVar(&options.to, "to", "", "_[DATE]_ - Last day in the billing period (YYYY-MM-DD)")
	flags.Int32Var(&options.limit, "limit", 100, "_[COUNT]_ - Maximum number of cost records (1–100)")
	return result
}

func (o *costsOptions) validate() error {
	from, err := time.Parse("2006-01-02", o.from)
	if err != nil || from.Format("2006-01-02") != o.from {
		return fmt.Errorf("--from must be a valid date in YYYY-MM-DD format")
	}
	to, err := time.Parse("2006-01-02", o.to)
	if err != nil || to.Format("2006-01-02") != o.to {
		return fmt.Errorf("--to must be a valid date in YYYY-MM-DD format")
	}
	if from.After(to) {
		return fmt.Errorf("--from must be on or before --to")
	}
	if o.limit < 1 || o.limit > 100 {
		return fmt.Errorf("--limit must be between 1 and 100")
	}
	return nil
}

func enumSuffix(value, prefix string) string { return strings.TrimPrefix(value, prefix) }

func optional(present bool, value string) string {
	if !present {
		return "-"
	}
	if value == "" {
		return "(default)"
	}
	return clean(value)
}

// clean prevents provider text from writing terminal control sequences or table delimiters.
func clean(value string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || (r >= 0x80 && r <= 0x9f) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, value)
}
