/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package externalippool

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

// Cmd creates the command to list or get external IP pools.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:     "externalippool [ID_OR_NAME]",
		Aliases: []string{"externalippools"},
		Short:   "List or get external IP pools",
		Long:    "List all available external IP pools, or display details for a specific pool by ID or name.",
		Example: `  # List all available pools
  osac get externalippool

  # Get a specific pool by name
  osac get externalippool pool-ipv4-prod

  # Get a specific pool by ID
  osac get externalippool pool-abc123`,
		Args: cobra.MaximumNArgs(1),
		RunE: runner.run,
	}
	return result
}

type runnerContext struct {
	console *terminal.Console
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	c.console = terminal.ConsoleFromContext(ctx)

	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	client := publicv1.NewExternalIPPoolsClient(conn)

	if len(args) == 0 {
		resp, err := client.List(ctx, publicv1.ExternalIPPoolsListRequest_builder{}.Build())
		if err != nil {
			return fmt.Errorf("failed to list external IP pools: %w", err)
		}
		if len(resp.GetItems()) == 0 {
			c.console.Infof(ctx, "No external IP pools found.\n")
			return nil
		}
		renderPoolTable(c.console, resp.GetItems())
		return nil
	}

	ref := args[0]
	pool, err := lookup.Find(ref, "external IP pool", func(filter string, limit int32) ([]*publicv1.ExternalIPPool, error) {
		resp, err := client.List(ctx, publicv1.ExternalIPPoolsListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to list external IP pools: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	renderPoolDetail(c.console, pool)
	return nil
}

// renderPoolTable writes a compact table of pools — used when listing all pools.
func renderPoolTable(w *terminal.Console, pools []*publicv1.ExternalIPPool) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tNAME\tIP-FAMILY\tAVAILABLE")
	for _, p := range pools {
		name := p.GetMetadata().GetName()
		if name == "" {
			name = "-"
		}
		ipFamily := strings.TrimPrefix(p.GetSpec().GetIpFamily().String(), "IP_FAMILY_")
		available := fmt.Sprintf("%d", p.GetStatus().GetAvailable())
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", p.GetId(), name, ipFamily, available)
	}
	writer.Flush()
}

// renderPoolDetail writes a detailed key-value view of a single pool — used when getting by name/id.
func renderPoolDetail(w *terminal.Console, p *publicv1.ExternalIPPool) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	name := "-"
	if v := p.GetMetadata().GetName(); v != "" {
		name = v
	}

	ipFamily := strings.TrimPrefix(p.GetSpec().GetIpFamily().String(), "IP_FAMILY_")

	fmt.Fprintf(writer, "ID:\t%s\n", p.GetId())
	fmt.Fprintf(writer, "Name:\t%s\n", name)
	fmt.Fprintf(writer, "IP Family:\t%s\n", ipFamily)
	fmt.Fprintf(writer, "Available:\t%d\n", p.GetStatus().GetAvailable())
	writer.Flush()
}
