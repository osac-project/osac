/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package subnet

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

// Cmd creates the command to describe a subnet.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "subnet [FLAG...] ID|NAME",
		Aliases:               []string{"subnets"},
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

	client := publicv1.NewSubnetsClient(conn)

	matched, err := lookup.Find(ref, "subnet", func(filter string, limit int32) ([]*publicv1.Subnet, error) {
		resp, err := client.List(ctx, publicv1.SubnetsListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to describe subnet: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	RenderSubnet(c.console, matched)

	return nil
}

// RenderSubnet writes a formatted description of s to w.
func RenderSubnet(w io.Writer, s *publicv1.Subnet) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	name := "-"
	if v := s.GetMetadata().GetName(); v != "" {
		name = v
	}

	ipv4Cidr := "-"
	if v := s.GetSpec().GetIpv4Cidr(); v != "" {
		ipv4Cidr = v
	}

	ipv6Cidr := "-"
	if v := s.GetSpec().GetIpv6Cidr(); v != "" {
		ipv6Cidr = v
	}

	state := "-"
	message := "-"
	if s.GetStatus() != nil {
		state = strings.TrimPrefix(s.GetStatus().GetState().String(), "SUBNET_STATE_")
		if v := s.GetStatus().GetMessage(); v != "" {
			message = v
		}
	}

	fmt.Fprintf(writer, "ID:\t%s\n", s.GetId())
	fmt.Fprintf(writer, "Name:\t%s\n", name)
	fmt.Fprintf(writer, "Virtual Network:\t%s\n", s.GetSpec().GetVirtualNetwork())
	fmt.Fprintf(writer, "IPv4 CIDR:\t%s\n", ipv4Cidr)
	fmt.Fprintf(writer, "IPv6 CIDR:\t%s\n", ipv6Cidr)
	fmt.Fprintf(writer, "State:\t%s\n", state)
	fmt.Fprintf(writer, "Message:\t%s\n", message)
	writer.Flush()
}

const shortHelp = "Describe a subnet"

const longHelp = `
Display detailed information about a subnet, referenced by identifier or name.

Examples:

{{ bt 3 }}shell
# Describe a subnet by identifier:
{{ binary }} describe subnet 019e5ff0-6266-7310-acf3-94e99a3786c9
{{ bt 3 }}

{{ bt 3 }}shell
# Describe a subnet by name:
{{ binary }} describe subnet my-subnet
{{ bt 3 }}
`
