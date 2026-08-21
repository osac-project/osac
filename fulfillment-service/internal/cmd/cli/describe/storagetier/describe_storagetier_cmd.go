/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package storagetier

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

// Cmd creates the command to describe a storage tier.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "storagetier [FLAG...] ID|NAME",
		Aliases:               []string{"storagetiers"},
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

	client := publicv1.NewStorageTiersClient(conn)

	matched, err := lookup.Find(ref, "storage tier", func(filter string, limit int32) ([]*publicv1.StorageTier, error) {
		resp, err := client.List(ctx, publicv1.StorageTiersListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to describe storage tier: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	renderStorageTier(c.console, matched)

	return nil
}

// renderStorageTier writes a detailed key-value description of a storage tier to w.
func renderStorageTier(w io.Writer, st *publicv1.StorageTier) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	name := "-"
	if v := st.GetMetadata().GetName(); v != "" {
		name = v
	}

	description := "-"
	if v := st.GetSpec().GetDescription(); v != "" {
		description = v
	}

	protocol := strings.TrimPrefix(st.GetSpec().GetProtocol().String(), "STORAGE_PROTOCOL_")
	state := strings.TrimPrefix(st.GetStatus().GetState().String(), "STORAGE_TIER_STATE_")

	message := "-"
	if v := st.GetStatus().GetMessage(); v != "" {
		message = v
	}

	fmt.Fprintf(writer, "ID:\t%s\n", st.GetId())
	fmt.Fprintf(writer, "Name:\t%s\n", name)
	fmt.Fprintf(writer, "Description:\t%s\n", description)
	fmt.Fprintf(writer, "Protocol:\t%s\n", protocol)
	fmt.Fprintf(writer, "Max Read BW (MB/s):\t%d\n", st.GetSpec().GetMaxReadBandwidthMbs())
	fmt.Fprintf(writer, "Max Write BW (MB/s):\t%d\n", st.GetSpec().GetMaxWriteBandwidthMbs())
	fmt.Fprintf(writer, "Encryption Enabled:\t%t\n", st.GetSpec().GetEncryptionEnabled())
	fmt.Fprintf(writer, "State:\t%s\n", state)
	fmt.Fprintf(writer, "Message:\t%s\n", message)

	writer.Flush()
}

const shortHelp = `Describe a storage tier`

const longHelp = `
Describe a storage tier.

Displays detailed information about a storage tier, including protocol, QoS settings (bandwidth
limits), and encryption configuration.

To describe a storage tier by name:

{{ bt 3 }}shell
{{ binary }} describe storagetier gold
{{ bt 3 }}
`
