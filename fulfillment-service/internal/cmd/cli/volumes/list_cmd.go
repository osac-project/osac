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

func (c *listRunner) run(cmd *cobra.Command, args []string) error {
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

	client := publicv1.NewVolumesClient(conn)

	reqBuilder := publicv1.VolumesListRequest_builder{}
	if c.project != "" {
		filter := fmt.Sprintf("this.metadata.project == %s", strconv.Quote(c.project))
		reqBuilder.Filter = proto.String(filter)
	}

	resp, err := client.List(ctx, reqBuilder.Build())
	if err != nil {
		return fmt.Errorf("failed to list volumes: %w", err)
	}

	if len(resp.GetItems()) == 0 {
		c.console.Infof(ctx, "No volumes found.\n")
		return nil
	}

	renderVolumeTable(c.console, resp.GetItems())
	return nil
}

// renderVolumeTable writes a compact table of volumes.
func renderVolumeTable(w *terminal.Console, volumes []*publicv1.Volume) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tNAME\tSTORAGE TIER\tSIZE (GiB)\tACCESS MODE\tSTATE")
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
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n",
			v.GetId(), name, storageTier, sizeGib, accessMode, state)
	}
	writer.Flush()
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
