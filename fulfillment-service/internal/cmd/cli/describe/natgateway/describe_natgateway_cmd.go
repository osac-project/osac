/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package natgateway

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

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "natgateway [FLAG...] ID|NAME",
		Aliases:               []string{"natgateways"},
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

	client := publicv1.NewNATGatewaysClient(conn)

	matched, err := lookup.Find(ref, "NAT gateway", func(filter string, limit int32) ([]*publicv1.NATGateway, error) {
		resp, err := client.List(ctx, publicv1.NATGatewaysListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to describe NAT gateway: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	RenderNATGateway(c.console, matched)

	return nil
}

// RenderNATGateway writes a formatted description of ng to w.
func RenderNATGateway(w io.Writer, ng *publicv1.NATGateway) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	name := "-"
	if v := ng.GetMetadata().GetName(); v != "" {
		name = v
	}

	state := "-"
	message := "-"
	if ng.GetStatus() != nil {
		state = strings.TrimPrefix(ng.GetStatus().GetState().String(), "NAT_GATEWAY_STATE_")
		if v := ng.GetStatus().GetMessage(); v != "" {
			message = v
		}
	}

	fmt.Fprintf(writer, "ID:\t%s\n", ng.GetId())
	fmt.Fprintf(writer, "Name:\t%s\n", name)
	fmt.Fprintf(writer, "Virtual Network:\t%s\n", ng.GetSpec().GetVirtualNetwork())
	fmt.Fprintf(writer, "External IP:\t%s\n", ng.GetSpec().GetExternalIp())
	fmt.Fprintf(writer, "State:\t%s\n", state)
	fmt.Fprintf(writer, "Message:\t%s\n", message)
	writer.Flush()
}

const shortHelp = "Describe a NAT gateway"

const longHelp = `
Display detailed information about a NAT gateway, referenced by identifier or name.

Examples:

{{ bt 3 }}shell
# Describe a NAT gateway by identifier:
{{ binary }} describe natgateway 019e5ff0-6266-7310-acf3-94e99a3786c9

# Describe a NAT gateway by name:
{{ binary }} describe natgateway my-natgw
{{ bt 3 }}
`
