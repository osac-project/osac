/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package instancetype

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

// Cmd creates the command to describe an instance type.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "instancetype [FLAG...] ID|NAME",
		Aliases:               []string{"instancetypes"},
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.ExactArgs(1),
		RunE:                  runner.run,
	}
	return result
}

type runnerContext struct {
	console *terminal.Console
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ref := args[0]

	// Get the context:
	ctx := cmd.Context()

	// Get the console:
	c.console = terminal.ConsoleFromContext(ctx)

	// Get the configuration:
	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	// Create the gRPC connection from the configuration:
	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	// Create the client:
	client := publicv1.NewInstanceTypesClient(conn)

	// Find the instance type:
	matched, err := lookup.Find(ref, "instance type", func(filter string, limit int32) ([]*publicv1.InstanceType, error) {
		resp, err := client.List(ctx, publicv1.InstanceTypesListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to describe instance type: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	// Display the result:
	renderInstanceType(c.console, matched)

	return nil
}

func renderInstanceType(w io.Writer, it *publicv1.InstanceType) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	// Base fields (always shown):
	fmt.Fprintf(writer, "Name:\t%s\n", it.GetMetadata().GetName())

	spec := it.GetSpec()
	if spec != nil {
		fmt.Fprintf(writer, "Cores:\t%d\n", spec.GetCores())
		fmt.Fprintf(writer, "Memory (GiB):\t%d\n", spec.GetMemoryGib())

		// State with prefix stripping (D-02):
		state := strings.TrimPrefix(spec.GetState().String(), "INSTANCE_TYPE_STATE_")
		fmt.Fprintf(writer, "State:\t%s\n", state)

		// Description (only when non-empty):
		if desc := spec.GetDescription(); desc != "" {
			fmt.Fprintf(writer, "Description:\t%s\n", desc)
		}

		// Conditional deprecation section (D-01):
		if dep := spec.GetDeprecation(); dep != nil {
			hasContent := dep.GetReplacement() != "" ||
				dep.GetDeprecationTimestamp() != nil ||
				dep.GetObsolescenceTimestamp() != nil
			if hasContent {
				if dep.GetReplacement() != "" {
					fmt.Fprintf(writer, "Replacement:\t%s\n", dep.GetReplacement())
				}
				if ts := dep.GetDeprecationTimestamp(); ts != nil {
					fmt.Fprintf(writer, "Deprecated At:\t%s\n", ts.AsTime().Format(time.RFC3339))
				}
				if ts := dep.GetObsolescenceTimestamp(); ts != nil {
					fmt.Fprintf(writer, "Obsolete At:\t%s\n", ts.AsTime().Format(time.RFC3339))
				}
			}
		}
	}

	writer.Flush()
}

const shortHelp = `Describe an instance type.`

const longHelp = `
Describe an instance type.

Displays detailed information about an instance type, including its compute configuration (cores,
memory), lifecycle state, and deprecation details if applicable.

To describe an instance type by name:

{{ bt 3 }}shell
{{ binary }} describe instancetype standard-4-16
{{ bt 3 }}
`
