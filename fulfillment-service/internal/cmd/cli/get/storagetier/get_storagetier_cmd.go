/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package storagetier

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

// Cmd creates the command to list or get storage tiers.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:     "storagetier [ID_OR_NAME]",
		Aliases: []string{"storagetiers"},
		Short:   shortHelp,
		Long:    longHelp,
		Example: example,
		Args:    cobra.MaximumNArgs(1),
		RunE:    runner.run,
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

	client := publicv1.NewStorageTiersClient(conn)

	if len(args) == 0 {
		resp, err := client.List(ctx, publicv1.StorageTiersListRequest_builder{}.Build())
		if err != nil {
			return fmt.Errorf("failed to list storage tiers: %w", err)
		}
		if len(resp.GetItems()) == 0 {
			c.console.Infof(ctx, "No storage tiers found.\n")
			return nil
		}
		renderTierTable(c.console, resp.GetItems())
		return nil
	}

	ref := args[0]
	tier, err := lookup.Find(ref, "storage tier", func(filter string, limit int32) ([]*publicv1.StorageTier, error) {
		resp, err := client.List(ctx, publicv1.StorageTiersListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to list storage tiers: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	renderTierTable(c.console, []*publicv1.StorageTier{tier})
	return nil
}

const shortHelp = `List or get storage tiers`

const longHelp = `
List all available storage tiers, or display details for a specific tier by ID or name.
`

const example = `  # List all available storage tiers
  osac get storagetier

  # Get a specific storage tier by name
  osac get storagetier gold

  # Get a specific storage tier by ID
  osac get storagetier tier-abc123`

// renderTierTable writes a compact table of storage tiers — used when listing all tiers.
func renderTierTable(w *terminal.Console, tiers []*publicv1.StorageTier) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tNAME\tDESCRIPTION\tPROTOCOL\tSTATE")
	for _, t := range tiers {
		name := t.GetMetadata().GetName()
		if name == "" {
			name = "-"
		}
		description := t.GetSpec().GetDescription()
		if description == "" {
			description = "-"
		}
		protocol := strings.TrimPrefix(t.GetSpec().GetProtocol().String(), "STORAGE_PROTOCOL_")
		state := strings.TrimPrefix(t.GetStatus().GetState().String(), "STORAGE_TIER_STATE_")
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", t.GetId(), name, description, protocol, state)
	}
	writer.Flush()
}
