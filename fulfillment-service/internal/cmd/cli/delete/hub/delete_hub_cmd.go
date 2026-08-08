/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package hub

import (
	"fmt"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

func Cmd() *cobra.Command {
	result := &cobra.Command{
		Use:     "hub ID|NAME",
		Aliases: []string{"hubs"},
		Short:   "Delete a hub",
		Long:    "Delete a hub by its ID or name.",
		Example: `  # Delete a hub by ID
  osac delete hub hub0`,
		Args: cobra.ExactArgs(1),
		RunE: run,
	}
	return result
}

func run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	console := terminal.ConsoleFromContext(ctx)

	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	client := privatev1.NewHubsClient(conn)

	ref := args[0]
	hub, err := lookup.Find(ref, "hub", func(filter string, limit int32) ([]*privatev1.Hub, error) {
		resp, err := client.List(ctx, privatev1.HubsListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to list hubs: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	_, err = client.Delete(ctx, privatev1.HubsDeleteRequest_builder{
		Id: hub.GetId(),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to delete hub '%s': %w", hub.GetId(), err)
	}

	console.Infof(ctx, "Deleted hub '%s'.\n", hub.GetId())
	return nil
}
