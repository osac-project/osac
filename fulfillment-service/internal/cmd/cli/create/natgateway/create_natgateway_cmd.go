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
	"log/slog"

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
		Use:                   "natgateway [FLAG...]",
		Aliases:               []string{string(proto.MessageName((*publicv1.NATGateway)(nil)))},
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}
	flags := result.Flags()
	flags.StringVarP(
		&runner.args.name,
		"name",
		"n",
		"",
		nameFlagHelp,
	)
	flags.StringVar(
		&runner.args.virtualNetwork,
		"virtual-network",
		"",
		virtualNetworkFlagHelp,
	)
	flags.StringVar(
		&runner.args.externalIP,
		"externalip",
		"",
		externalIPFlagHelp,
	)
	result.MarkFlagRequired("virtual-network") //nolint:errcheck
	result.MarkFlagRequired("externalip")      //nolint:errcheck
	return result
}

type runnerContext struct {
	args struct {
		name           string
		virtualNetwork string
		externalIP     string
	}
	logger   *slog.Logger
	console  *terminal.Console
	settings *config.Settings
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	c.logger = logging.LoggerFromContext(ctx)
	c.console = terminal.ConsoleFromContext(ctx)

	c.settings = config.SettingsFromContext(ctx)
	if !c.settings.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	conn, err := c.settings.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	vnClient := publicv1.NewVirtualNetworksClient(conn)
	vn, err := lookup.Find(c.args.virtualNetwork, "virtual network", func(filter string, limit int32) ([]*publicv1.VirtualNetwork, error) {
		resp, err := vnClient.List(ctx, publicv1.VirtualNetworksListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to resolve virtual network %q: %w", c.args.virtualNetwork, err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	eipClient := publicv1.NewExternalIPsClient(conn)
	eip, err := lookup.Find(c.args.externalIP, "external IP", func(filter string, limit int32) ([]*publicv1.ExternalIP, error) {
		resp, err := eipClient.List(ctx, publicv1.ExternalIPsListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to resolve external IP %q: %w", c.args.externalIP, err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	client := publicv1.NewNATGatewaysClient(conn)

	natGateway := publicv1.NATGateway_builder{
		Metadata: publicv1.Metadata_builder{
			Name:   c.args.name,
			Tenant: c.settings.Tenant(),
		}.Build(),
		Spec: publicv1.NATGatewaySpec_builder{
			VirtualNetwork: vn.GetId(),
			ExternalIp:     eip.GetId(),
		}.Build(),
	}.Build()

	response, err := client.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{Object: natGateway}.Build())
	if err != nil {
		return fmt.Errorf("failed to create NAT gateway: %w", err)
	}

	c.console.Infof(ctx, "Created NAT gateway '%s' (ID: %s).\n", response.Object.GetMetadata().GetName(), response.Object.GetId())

	return nil
}

const shortHelp = `Create a NAT gateway.`

const longHelp = `
Create a NAT gateway for outbound traffic (SNAT) from a virtual network.

The NAT gateway provides a dedicated egress identity for all resources within a virtual network.
All outbound traffic from the virtual network's CIDR is source-NATted to the associated external
IP address.

Both {{ bt }}--virtual-network{{ bt }} and {{ bt }}--externalip{{ bt }} are required. Only one NAT
gateway is allowed per virtual network.

Examples:

{{ bt 3 }}shell
# Create a NAT gateway
{{ binary }} create natgateway --name my-natgw --virtual-network vnet-abc123 --externalip eip-xyz789

# Create using resource names
{{ binary }} create natgateway --name my-natgw --virtual-network my-vnet --externalip my-ip
{{ bt 3 }}
`

const nameFlagHelp = `
_NAME_ - Name of the NAT gateway.
`

const virtualNetworkFlagHelp = `
_ID|NAME_ - ID or name of the parent virtual network. Required.
`

const externalIPFlagHelp = `
_ID|NAME_ - ID or name of the external IP to use for SNAT. Required.
`
