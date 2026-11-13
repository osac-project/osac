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
	"context"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// Cmd creates commands for inspecting configured Hubs.
func Cmd() *cobra.Command {
	result := &cobra.Command{
		Use:   "hub",
		Short: "Inspect configured Hubs",
		Long:  "List and inspect configured Hubs without displaying their credentials.",
	}
	result.AddCommand(listCmd())
	result.AddCommand(describeCmd())
	return result
}

func listCmd() *cobra.Command {
	return &cobra.Command{
		Use:                   "list",
		Aliases:               []string{"ls"},
		Short:                 "List configured Hubs",
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) (err error) {
			ctx := cmd.Context()
			client, conn, err := connect(ctx, cmd)
			if err != nil {
				return err
			}
			defer func() {
				if closeErr := conn.Close(); err == nil {
					err = closeErr
				}
			}()
			hubs, err := listAll(ctx, client)
			if err != nil {
				return fmt.Errorf("failed to list hubs: %w", err)
			}
			return renderHubList(terminal.ConsoleFromContext(ctx), hubs)
		},
	}
}

func describeCmd() *cobra.Command {
	return &cobra.Command{
		Use:                   "describe ID|NAME",
		Short:                 "Describe a configured Hub",
		DisableFlagsInUseLine: true,
		Args:                  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			ctx := cmd.Context()
			client, conn, err := connect(ctx, cmd)
			if err != nil {
				return err
			}
			defer func() {
				if closeErr := conn.Close(); err == nil {
					err = closeErr
				}
			}()
			hub, err := lookup.Find(args[0], "hub", func(filter string, limit int32) ([]*privatev1.Hub, error) {
				response, err := client.List(ctx, privatev1.HubsListRequest_builder{
					Filter: proto.String(filter),
					Limit:  proto.Int32(limit),
				}.Build())
				if err != nil {
					return nil, fmt.Errorf("failed to find hub: %w", err)
				}
				return response.GetItems(), nil
			})
			if err != nil {
				return err
			}
			return renderHub(terminal.ConsoleFromContext(ctx), hub)
		},
	}
}

func connect(ctx context.Context, cmd *cobra.Command) (privatev1.HubsClient, interface{ Close() error }, error) {
	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return nil, nil, errors.New("there is no configuration, run the 'login' command")
	}
	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	return privatev1.NewHubsClient(conn), conn, nil
}

func listAll(ctx context.Context, client privatev1.HubsClient) ([]*privatev1.Hub, error) {
	const pageSize int32 = 100
	var result []*privatev1.Hub
	for offset := int32(0); ; offset += pageSize {
		response, err := client.List(ctx, privatev1.HubsListRequest_builder{Offset: proto.Int32(offset), Limit: proto.Int32(pageSize)}.Build())
		if err != nil {
			return nil, err
		}
		result = append(result, response.GetItems()...)
		if int64(len(result)) >= int64(response.GetTotal()) || len(response.GetItems()) == 0 {
			return result, nil
		}
	}
}

func renderHubList(w io.Writer, hubs []*privatev1.Hub) error {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "ID\tNAME\tNAMESPACE"); err != nil {
		return err
	}
	for _, hub := range hubs {
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\n", hub.GetId(), hub.GetMetadata().GetName(), hub.GetSpec().GetNamespace()); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func renderHub(w io.Writer, hub *privatev1.Hub) error {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintf(writer, "ID:\t%s\nName:\t%s\nNamespace:\t%s\n",
		hub.GetId(), hub.GetMetadata().GetName(), hub.GetSpec().GetNamespace()); err != nil {
		return err
	}
	// Never render Hub.Spec.Kubeconfig or its secret reference.
	return writer.Flush()
}
