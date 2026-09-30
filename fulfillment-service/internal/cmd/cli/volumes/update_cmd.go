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
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func updateCmd() *cobra.Command {
	runner := &updateRunner{}
	result := &cobra.Command{
		Use:                   "update [FLAG...] ID|NAME",
		Short:                 updateShortHelp,
		Long:                  updateLongHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.ExactArgs(1),
		RunE:                  runner.run,
	}
	flags := result.Flags()
	flags.StringVar(
		&runner.displayName,
		"display-name",
		"",
		updateDisplayNameFlagHelp,
	)
	flags.StringVar(
		&runner.description,
		"description",
		"",
		updateDescriptionFlagHelp,
	)
	return result
}

type updateRunner struct {
	console     *terminal.Console
	displayName string
	description string
}

func (c *updateRunner) run(cmd *cobra.Command, args []string) error {
	ref := args[0]

	ctx := cmd.Context()
	c.console = terminal.ConsoleFromContext(ctx)

	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	// Check that at least one flag is set:
	var paths []string
	if cmd.Flags().Changed("display-name") {
		paths = append(paths, "metadata.display_name")
	}
	if cmd.Flags().Changed("description") {
		paths = append(paths, "metadata.description")
	}
	if len(paths) == 0 {
		return fmt.Errorf("at least one of --display-name or --description must be specified")
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

	// Clone and apply changes:
	updated := proto.Clone(volume).(*publicv1.Volume)
	if cmd.Flags().Changed("display-name") {
		updated.GetMetadata().SetDisplayName(c.displayName)
	}
	if cmd.Flags().Changed("description") {
		updated.GetMetadata().SetDescription(c.description)
	}

	_, err = client.Update(ctx, publicv1.VolumesUpdateRequest_builder{
		Object: updated,
		UpdateMask: &fieldmaskpb.FieldMask{
			Paths: paths,
		},
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to update volume: %w", err)
	}

	c.console.Infof(ctx, "Updated volume '%s'.\n", volume.GetId())

	return nil
}

const updateShortHelp = `Update volume properties`

const updateLongHelp = `
Update mutable properties of a volume.

Volume spec fields (storage tier, size, access mode) are immutable after creation.
This command updates metadata fields such as display name and description.

To update the display name:

{{ bt 3 }}shell
{{ binary }} volumes update my-volume --display-name "Production Database Volume"
{{ bt 3 }}

To update the description:

{{ bt 3 }}shell
{{ binary }} volumes update vol-abc123 --description "Persistent storage for PostgreSQL"
{{ bt 3 }}
`

const updateDisplayNameFlagHelp = `
_NAME_ - Human-friendly display name for the volume.
`

const updateDescriptionFlagHelp = `
_TEXT_ - Human-readable description of the volume.
`
