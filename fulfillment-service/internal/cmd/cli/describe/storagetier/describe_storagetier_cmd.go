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

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/terminal"
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

	client := privatev1.NewStorageTiersClient(conn)

	matched, err := lookup.Find(ref, "storage tier", func(filter string, limit int32) ([]*privatev1.StorageTier, error) {
		resp, err := client.List(ctx, privatev1.StorageTiersListRequest_builder{
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

func renderStorageTier(w io.Writer, st *privatev1.StorageTier) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	fmt.Fprintf(writer, "ID:\t%s\n", st.GetId())
	fmt.Fprintf(writer, "Name:\t%s\n", st.GetMetadata().GetName())

	state := strings.TrimPrefix(st.GetStatus().GetState().String(), "STORAGE_TIER_STATE_")
	fmt.Fprintf(writer, "State:\t%s\n", state)

	if desc := st.GetSpec().GetDescription(); desc != "" {
		fmt.Fprintf(writer, "Description:\t%s\n", desc)
	}

	backends := st.GetSpec().GetBackends()
	if len(backends) > 0 {
		for i, ba := range backends {
			if i > 0 {
				fmt.Fprintln(writer)
			}
			protocol := strings.TrimPrefix(ba.GetProtocol().String(), "STORAGE_PROTOCOL_")
			fmt.Fprintf(writer, "Backend ID:\t%s\n", ba.GetBackendId())
			fmt.Fprintf(writer, "Protocol:\t%s\n", protocol)
			fmt.Fprintf(writer, "Max Read BW (MB/s):\t%d\n", ba.GetMaxReadBandwidthMbs())
			fmt.Fprintf(writer, "Max Write BW (MB/s):\t%d\n", ba.GetMaxWriteBandwidthMbs())
			fmt.Fprintf(writer, "Quota (GiB):\t%d\n", ba.GetQuotaGib())
			fmt.Fprintf(writer, "Encryption:\t%t\n", ba.GetEncryptionEnabled())
		}
	}

	writer.Flush()
}

const shortHelp = `Describe a storage tier.`

const longHelp = `
Describe a storage tier.

Displays detailed information about a storage tier, including its backend association, protocol,
QoS settings (bandwidth limits, quota), and encryption configuration.

To describe a storage tier by name:

{{ bt 3 }}shell
{{ binary }} describe storagetier gold
{{ bt 3 }}
`
