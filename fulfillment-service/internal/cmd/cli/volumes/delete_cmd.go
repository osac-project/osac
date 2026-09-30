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

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func deleteCmd() *cobra.Command {
	runner := &deleteRunner{}
	result := &cobra.Command{
		Use:                   "delete [FLAG...] ID|NAME",
		Short:                 deleteShortHelp,
		Long:                  deleteLongHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.ExactArgs(1),
		RunE:                  runner.run,
	}
	return result
}

type deleteRunner struct {
	console *terminal.Console
}

func (c *deleteRunner) run(cmd *cobra.Command, args []string) error {
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

	// Resolve volume by name or ID:
	volume, err := lookup.Find(ref, "volume", func(filter string, limit int32) ([]*publicv1.Volume, error) {
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

	_, err = client.Delete(ctx, publicv1.VolumesDeleteRequest_builder{
		Id: volume.GetId(),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to delete volume: %w", err)
	}

	c.console.Infof(ctx, "Deleted volume '%s'.\n", volume.GetId())

	return nil
}

const deleteShortHelp = `Delete a volume`

const deleteLongHelp = `
Delete a volume.

The volume can be referenced by its identifier or by its name.

To delete a volume:

{{ bt 3 }}shell
{{ binary }} volumes delete my-volume
{{ bt 3 }}
`
