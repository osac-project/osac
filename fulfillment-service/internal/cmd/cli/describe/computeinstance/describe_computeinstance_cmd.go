/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package computeinstance

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

// Cmd creates the command to describe a compute instance.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "computeinstance [FLAG...] ID|NAME",
		Aliases:               []string{"computeinstances"},
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

	client := publicv1.NewComputeInstancesClient(conn)

	matched, err := lookup.Find(ref, "compute instance", func(filter string, limit int32) ([]*publicv1.ComputeInstance, error) {
		resp, err := client.List(ctx, publicv1.ComputeInstancesListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to describe compute instance: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	renderComputeInstance(c.console, matched)

	return nil
}

func renderComputeInstance(w io.Writer, ci *publicv1.ComputeInstance) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	catalogItem := "-"
	if ci.Spec != nil {
		if catalogItemID := ci.Spec.GetCatalogItem(); catalogItemID != "" {
			catalogItem = catalogItemID
		}
	}
	state := "-"
	if ci.Status != nil {
		state = ci.Status.State.String()
		state = strings.TrimPrefix(state, "COMPUTE_INSTANCE_STATE_")
	}
	fmt.Fprintf(writer, "ID:\t%s\n", ci.Id)
	fmt.Fprintf(writer, "Catalog Item:\t%s\n", catalogItem)
	fmt.Fprintf(writer, "State:\t%s\n", state)
	if ci.Status != nil && ci.Status.GetLastRestartedAt() != nil {
		fmt.Fprintf(writer, "Last Restarted At:\t%s\n", ci.Status.GetLastRestartedAt().AsTime().Format(time.RFC3339))
	}
	writer.Flush()
}

const shortHelp = `Describe a compute instance.`

const longHelp = `
Describe a compute instance.
`
