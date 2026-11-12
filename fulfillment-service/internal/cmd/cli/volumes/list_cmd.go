/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package volumes

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// listCmd creates the "volumes list" subcommand for listing volumes with optional project filtering.
func listCmd() *cobra.Command {
	runner := &listRunner{}
	result := &cobra.Command{
		Use:   "list",
		Short: listShortHelp,
		Long:  listLongHelp,
		Args:  cobra.NoArgs,
		RunE:  runner.run,
	}
	flags := result.Flags()
	flags.StringVar(
		&runner.project,
		"project",
		"",
		listProjectFlagHelp,
	)
	return result
}

type listRunner struct {
	console *terminal.Console
	project string
}

func (c *listRunner) run(cmd *cobra.Command, args []string) (err error) {
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
	defer func() {
		if closeErr := conn.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close gRPC connection: %w", closeErr)
		}
	}()

	client := publicv1.NewVolumesClient(conn)

	var filter *string
	if c.project != "" {
		f := fmt.Sprintf("this.metadata.project == %s", strconv.Quote(c.project))
		filter = &f
	}

	allItems, err := listAllVolumes(ctx, client, filter)
	if err != nil {
		return err
	}

	if len(allItems) == 0 {
		c.console.Infof(ctx, "No volumes found.\n")
		return nil
	}

	return renderVolumeTable(c.console, allItems)
}

// listAllVolumes paginates through all results using offset-based paging. The server uses a default
// limit (typically 100) when none is specified; this function collects all pages so the output is
// complete.
func listAllVolumes(ctx context.Context, client publicv1.VolumesClient, filter *string) ([]*publicv1.Volume, error) {
	var allItems []*publicv1.Volume
	var offset int32
	for {
		resp, err := client.List(ctx, publicv1.VolumesListRequest_builder{
			Filter: filter,
			Offset: proto.Int32(offset),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to list volumes: %w", err)
		}

		allItems = append(allItems, resp.GetItems()...)

		// Stop when we have collected all matching items or when the server returned no items.
		// resp.GetSize() and resp.GetTotal() are int32, matching the proto field types.
		offset += resp.GetSize()
		if offset >= resp.GetTotal() || resp.GetSize() == 0 {
			break
		}
	}
	return allItems, nil
}

// renderVolumeTable writes a compact table of volumes to the console and returns any write error.
func renderVolumeTable(w *terminal.Console, volumes []*publicv1.Volume) error {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "ID\tNAME\tSTORAGE TIER\tSIZE (GiB)\tACCESS MODE\tSTATE"); err != nil {
		return fmt.Errorf("failed to render volume table header: %w", err)
	}
	for _, v := range volumes {
		name := v.GetMetadata().GetName()
		if name == "" {
			name = "-"
		}
		storageTier := v.GetSpec().GetStorageTier()
		if storageTier == "" {
			storageTier = "-"
		}
		sizeGib := strconv.FormatInt(v.GetSpec().GetSizeGib(), 10)
		accessMode := strings.TrimPrefix(v.GetSpec().GetAccessMode().String(), "VOLUME_ACCESS_MODE_")
		state := strings.TrimPrefix(v.GetStatus().GetState().String(), "VOLUME_STATE_")
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n",
			v.GetId(), name, storageTier, sizeGib, accessMode, state); err != nil {
			return fmt.Errorf("failed to render volume table row: %w", err)
		}
	}
	return writer.Flush()
}

const listShortHelp = `List volumes`

const listLongHelp = `
List all volumes visible to the current tenant.

To list all volumes:

{{ bt 3 }}shell
{{ binary }} volumes list
{{ bt 3 }}

To list volumes in a specific project:

{{ bt 3 }}shell
{{ binary }} volumes list --project team-a.frontend
{{ bt 3 }}
`

const listProjectFlagHelp = `
_PROJECT_ - Filter volumes by project name.
`
