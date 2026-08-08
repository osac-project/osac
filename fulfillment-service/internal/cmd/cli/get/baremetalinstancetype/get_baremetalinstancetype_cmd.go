/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalinstancetype

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

// Cmd creates the command to list or get bare metal instance types.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:     "baremetalinstancetype [ID_OR_NAME]",
		Aliases: []string{"baremetalinstancetypes"},
		Short:   "List or get bare metal instance types",
		Long:    "List all available bare metal instance types, or display details for a specific type by ID or name.",
		Example: `  # List all available types
  osac get baremetalinstancetype

  # Get a specific type by name
  osac get baremetalinstancetype gpu-large

  # Get a specific type by ID
  osac get baremetalinstancetype type-abc123`,
		Args: cobra.MaximumNArgs(1),
		RunE: runner.run,
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

	client := publicv1.NewBareMetalInstanceTypesClient(conn)

	if len(args) == 0 {
		resp, err := client.List(ctx, publicv1.BareMetalInstanceTypesListRequest_builder{}.Build())
		if err != nil {
			return fmt.Errorf("failed to list bare metal instance types: %w", err)
		}
		if len(resp.GetItems()) == 0 {
			c.console.Infof(ctx, "No bare metal instance types found.\n")
			return nil
		}
		renderBareMetalInstanceTypeTable(c.console, resp.GetItems())
		return nil
	}

	ref := args[0]
	bmiType, err := lookup.Find(ref, "bare metal instance type", func(filter string, limit int32) ([]*publicv1.BareMetalInstanceType, error) {
		resp, err := client.List(ctx, publicv1.BareMetalInstanceTypesListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to list bare metal instance types: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	renderBareMetalInstanceTypeDetail(c.console, bmiType)
	return nil
}

// renderBareMetalInstanceTypeTable writes a compact table of types — used when listing all types.
func renderBareMetalInstanceTypeTable(w *terminal.Console, types []*publicv1.BareMetalInstanceType) {
	if len(types) == 0 {
		fmt.Fprintf(w, "No bare metal instance types found.\n")
		return
	}

	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tUUID\tDESCRIPTION")
	for _, t := range types {
		name := t.GetMetadata().GetName()
		if name == "" {
			name = "-"
		}
		description := t.GetSpec().GetDescription()
		if description == "" {
			description = "-"
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\n", name, t.GetId(), description)
	}
	writer.Flush()
}

// renderBareMetalInstanceTypeDetail writes a detailed key-value view of a single type — used when getting by name/id.
func renderBareMetalInstanceTypeDetail(w *terminal.Console, t *publicv1.BareMetalInstanceType) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	name := "-"
	if v := t.GetMetadata().GetName(); v != "" {
		name = v
	}

	description := "-"
	if v := t.GetSpec().GetDescription(); v != "" {
		description = v
	}

	fmt.Fprintf(writer, "ID:\t%s\n", t.GetId())
	fmt.Fprintf(writer, "Name:\t%s\n", name)
	fmt.Fprintf(writer, "Description:\t%s\n", description)

	// Show basic hardware info in get view
	if hardware := t.GetSpec().GetHardware(); hardware != nil {
		if cpu := hardware.GetCpu(); cpu != nil {
			fmt.Fprintf(writer, "CPU Cores:\t%d\n", cpu.GetCores())
			if arch := cpu.GetArchitecture(); arch != "" {
				fmt.Fprintf(writer, "Architecture:\t%s\n", arch)
			}
		}
		if memory := hardware.GetMemory(); memory != nil {
			fmt.Fprintf(writer, "Memory:\t%d GB\n", memory.GetTotalGb())
		}
	}

	writer.Flush()
}
