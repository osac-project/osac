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
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

const (
	outputFormatTable = "table"
	outputFormatJson  = "json"
	outputFormatYaml  = "yaml"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{
		marshalOptions: protojson.MarshalOptions{
			UseProtoNames: true,
		},
	}
	result := &cobra.Command{
		Use:     "hub [ID|NAME]",
		Aliases: []string{"hubs"},
		Short:   "List or get hubs",
		Long:    "List all registered hubs, or display details for a specific hub by ID or name.",
		Example: `  # List all hubs
  osac get hub

  # Get a specific hub by ID
  osac get hub hub0

  # Get hub details in JSON format
  osac get hub hub0 -o json`,
		Args: cobra.MaximumNArgs(1),
		RunE: runner.run,
	}
	flags := result.Flags()
	flags.StringVarP(
		&runner.args.format,
		"output",
		"o",
		outputFormatTable,
		"_FORMAT_ - Output format. Must be one of {{ bt }}table{{ bt }}, {{ bt }}json{{ bt }} or {{ bt }}yaml{{ bt }}.",
	)
	return result
}

type runnerContext struct {
	args struct {
		format string
	}
	console        *terminal.Console
	marshalOptions protojson.MarshalOptions
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	c.console = terminal.ConsoleFromContext(ctx)

	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	if c.args.format != outputFormatTable && c.args.format != outputFormatJson && c.args.format != outputFormatYaml {
		return fmt.Errorf(
			"unknown output format '%s', should be '%s', '%s' or '%s'",
			c.args.format, outputFormatTable, outputFormatJson, outputFormatYaml,
		)
	}

	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	client := privatev1.NewHubsClient(conn)

	if len(args) == 0 {
		resp, err := client.List(ctx, privatev1.HubsListRequest_builder{}.Build())
		if err != nil {
			return fmt.Errorf("failed to list hubs: %w", err)
		}
		if len(resp.GetItems()) == 0 {
			c.console.Infof(ctx, "No hubs found.\n")
			return nil
		}
		return c.renderList(ctx, resp.GetItems())
	}

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

	return c.renderDetail(ctx, hub)
}

func (c *runnerContext) renderList(ctx context.Context, hubs []*privatev1.Hub) error {
	switch c.args.format {
	case outputFormatJson:
		return c.renderJson(ctx, hubs)
	case outputFormatYaml:
		return c.renderYaml(ctx, hubs)
	default:
		return c.renderHubTable(hubs)
	}
}

func (c *runnerContext) renderDetail(ctx context.Context, hub *privatev1.Hub) error {
	switch c.args.format {
	case outputFormatJson:
		return c.renderJson(ctx, []*privatev1.Hub{hub})
	case outputFormatYaml:
		return c.renderYaml(ctx, []*privatev1.Hub{hub})
	default:
		return c.renderHubDetail(hub)
	}
}

func (c *runnerContext) renderHubTable(hubs []*privatev1.Hub) error {
	writer := tabwriter.NewWriter(c.console, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tNAME\tNAMESPACE\tREGISTERED")
	for _, h := range hubs {
		name := h.GetMetadata().GetName()
		if name == "" {
			name = "-"
		}
		created := "-"
		if ts := h.GetMetadata().GetCreationTimestamp(); ts.IsValid() {
			created = ts.AsTime().Format("2006-01-02T15:04:05Z")
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", h.GetId(), name, h.GetSpec().GetNamespace(), created)
	}
	return writer.Flush()
}

func (c *runnerContext) renderHubDetail(h *privatev1.Hub) error {
	writer := tabwriter.NewWriter(c.console, 0, 0, 2, ' ', 0)

	name := "-"
	if v := h.GetMetadata().GetName(); v != "" {
		name = v
	}

	created := "-"
	if ts := h.GetMetadata().GetCreationTimestamp(); ts.IsValid() {
		created = ts.AsTime().Format("2006-01-02T15:04:05Z")
	}
	creator := "-"
	if v := h.GetMetadata().GetCreator(); v != "" {
		creator = v
	}

	fmt.Fprintf(writer, "ID:\t%s\n", h.GetId())
	fmt.Fprintf(writer, "Name:\t%s\n", name)
	fmt.Fprintf(writer, "Namespace:\t%s\n", h.GetSpec().GetNamespace())
	fmt.Fprintf(writer, "Creator:\t%s\n", creator)
	fmt.Fprintf(writer, "Created:\t%s\n", created)
	return writer.Flush()
}

func (c *runnerContext) renderJson(ctx context.Context, hubs []*privatev1.Hub) error {
	values, err := c.encodeObjects(hubs)
	if err != nil {
		return err
	}
	if len(values) == 1 {
		c.console.RenderJson(ctx, values[0])
	} else {
		c.console.RenderJson(ctx, values)
	}
	return nil
}

func (c *runnerContext) renderYaml(ctx context.Context, hubs []*privatev1.Hub) error {
	values, err := c.encodeObjects(hubs)
	if err != nil {
		return err
	}
	if len(values) == 1 {
		c.console.RenderYaml(ctx, values[0])
	} else {
		c.console.RenderYaml(ctx, values)
	}
	return nil
}

func (c *runnerContext) encodeObjects(hubs []*privatev1.Hub) ([]any, error) {
	values := make([]any, len(hubs))
	for i, h := range hubs {
		v, err := c.encodeObject(h)
		if err != nil {
			return nil, err
		}
		values[i] = v
	}
	return values, nil
}

func (c *runnerContext) encodeObject(hub *privatev1.Hub) (any, error) {
	clone := proto.Clone(hub).(*privatev1.Hub)
	clone.GetSpec().SetKubeconfig(nil)
	wrapper, err := anypb.New(clone)
	if err != nil {
		return nil, err
	}
	data, err := c.marshalOptions.Marshal(wrapper)
	if err != nil {
		return nil, err
	}
	var result any
	err = json.Unmarshal(data, &result)
	return result, err
}
