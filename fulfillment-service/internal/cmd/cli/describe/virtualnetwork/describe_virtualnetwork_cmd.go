/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package virtualnetwork

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

// Cmd creates the command to describe a virtual network.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "virtualnetwork [FLAG...] ID|NAME",
		Aliases:               []string{"virtualnetworks"},
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.ExactArgs(1),
		RunE:                  runner.run,
	}
	return result
}

type runnerContext struct {
	logger  *slog.Logger
	console *terminal.Console
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ref := args[0]

	ctx := cmd.Context()

	c.logger = logging.LoggerFromContext(ctx)
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

	client := publicv1.NewVirtualNetworksClient(conn)

	matched, err := lookup.Find(ref, "virtual network", func(filter string, limit int32) ([]*publicv1.VirtualNetwork, error) {
		resp, err := client.List(ctx, publicv1.VirtualNetworksListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to describe virtual network: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	RenderVirtualNetwork(c.console, matched)

	return nil
}

// RenderVirtualNetwork writes a formatted description of vn to w.
func RenderVirtualNetwork(w io.Writer, vn *publicv1.VirtualNetwork) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	name := "-"
	if v := vn.GetMetadata().GetName(); v != "" {
		name = v
	}

	ipv4Cidr := "-"
	if v := vn.GetSpec().GetIpv4Cidr(); v != "" {
		ipv4Cidr = v
	}

	ipv6Cidr := "-"
	if v := vn.GetSpec().GetIpv6Cidr(); v != "" {
		ipv6Cidr = v
	}

	state := "-"
	message := "-"
	if vn.GetStatus() != nil {
		state = strings.TrimPrefix(vn.GetStatus().GetState().String(), "VIRTUAL_NETWORK_STATE_")
		if v := vn.GetStatus().GetMessage(); v != "" {
			message = v
		}
	}

	fmt.Fprintf(writer, "ID:\t%s\n", vn.GetId())
	fmt.Fprintf(writer, "Name:\t%s\n", name)
	fmt.Fprintf(writer, "Network Class:\t%s\n", vn.GetSpec().GetNetworkClass())
	fmt.Fprintf(writer, "IPv4 CIDR:\t%s\n", ipv4Cidr)
	fmt.Fprintf(writer, "IPv6 CIDR:\t%s\n", ipv6Cidr)
	fmt.Fprintf(writer, "State:\t%s\n", state)
	fmt.Fprintf(writer, "Message:\t%s\n", message)
	writer.Flush()
}

const shortHelp = `Describe a virtual network.`

const longHelp = `
Display detailed information about a virtual network, referenced by identifier or name.

Examples:

{{ bt 3 }}shell
# Describe a virtual network by identifier:
{{ binary }} describe virtualnetwork 019e5ff1-4c40-7185-8402-b2f0372b65e7
{{ bt 3 }}

{{ bt 3 }}shell
# Describe a virtual network by name:
{{ binary }} describe virtualnetwork my-network
{{ bt 3 }}
`
