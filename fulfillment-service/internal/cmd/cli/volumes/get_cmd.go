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

// getCmd creates the "volumes get" subcommand for displaying detailed volume information.
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

func (c *getRunner) run(cmd *cobra.Command, args []string) (err error) {
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
	defer func() {
		if closeErr := conn.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close gRPC connection: %w", closeErr)
		}
	}()

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

	return renderVolume(c.console, matched)
}

// renderVolume writes a detailed key-value description of a volume to w and returns any write error.
func renderVolume(w io.Writer, v *publicv1.Volume) error {
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

	fields := []struct {
		label string
		value any
	}{
		{"ID", v.GetId()},
		{"Name", name},
		{"Project", project},
		{"Storage Tier", storageTier},
		{"Size (GiB)", v.GetSpec().GetSizeGib()},
		{"Access Mode", accessMode},
		{"State", state},
		{"Message", message},
	}

	for _, f := range fields {
		if _, err := fmt.Fprintf(writer, "%s:\t%v\n", f.label, f.value); err != nil {
			return fmt.Errorf("failed to render volume detail: %w", err)
		}
	}

	return writer.Flush()
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
