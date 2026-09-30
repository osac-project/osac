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
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func getCmd() *cobra.Command {
	runner := &getRunner{}
	result := &cobra.Command{
		Use:                   "get [FLAG...] ID|NAME",
		Short:                 getShortHelp,
		Long:                  getLongHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.ExactArgs(1),
		RunE:                  runner.run,
	}
	return result
}

type getRunner struct {
	console *terminal.Console
}

func (c *getRunner) run(cmd *cobra.Command, args []string) error {
	ref := args[0]

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

	matched, err := lookup.Find(ref, "volume", func(filter string, limit int32) ([]*publicv1.Volume, error) {
		resp, err := client.List(ctx, publicv1.VolumesListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to list volumes: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	renderVolume(c.console, matched)

	return nil
}

// renderVolume writes a detailed key-value description of a volume to w.
func renderVolume(w io.Writer, v *publicv1.Volume) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	name := "-"
	if val := v.GetMetadata().GetName(); val != "" {
		name = val
	}

	project := "-"
	if val := v.GetMetadata().GetProject(); val != "" {
		project = val
	}

	storageTier := "-"
	if val := v.GetSpec().GetStorageTier(); val != "" {
		storageTier = val
	}

	accessMode := strings.TrimPrefix(v.GetSpec().GetAccessMode().String(), "VOLUME_ACCESS_MODE_")
	state := strings.TrimPrefix(v.GetStatus().GetState().String(), "VOLUME_STATE_")

	message := "-"
	if v.GetStatus().HasMessage() {
		if val := v.GetStatus().GetMessage(); val != "" {
			message = val
		}
	}

	fmt.Fprintf(writer, "ID:\t%s\n", v.GetId())
	fmt.Fprintf(writer, "Name:\t%s\n", name)
	fmt.Fprintf(writer, "Project:\t%s\n", project)
	fmt.Fprintf(writer, "Storage Tier:\t%s\n", storageTier)
	fmt.Fprintf(writer, "Size (GiB):\t%d\n", v.GetSpec().GetSizeGib())
	fmt.Fprintf(writer, "Access Mode:\t%s\n", accessMode)
	fmt.Fprintf(writer, "State:\t%s\n", state)
	fmt.Fprintf(writer, "Message:\t%s\n", message)

	writer.Flush()
}

const getShortHelp = `Get volume details`

const getLongHelp = `
Get detailed information about a volume.

Displays a key-value view of a volume identified by its ID or name.

To get a volume by name:

{{ bt 3 }}shell
{{ binary }} volumes get my-volume
{{ bt 3 }}

To get a volume by ID:

{{ bt 3 }}shell
{{ binary }} volumes get vol-abc123
{{ bt 3 }}
`
