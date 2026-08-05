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
	"log/slog"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/create/netutil"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "subnet [FLAG...]",
		Aliases:               []string{string(proto.MessageName((*publicv1.Subnet)(nil)))},
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
		&runner.args.ipv4Cidr,
		"ipv4-cidr",
		"",
		ipv4CidrFlagHelp,
	)
	flags.StringVar(
		&runner.args.ipv6Cidr,
		"ipv6-cidr",
		"",
		ipv6CidrFlagHelp,
	)
	return result
}

type runnerContext struct {
	args struct {
		name           string
		virtualNetwork string
		ipv4Cidr       string
		ipv6Cidr       string
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

	if err := netutil.ValidateVirtualNetwork(c.args.virtualNetwork); err != nil {
		return err
	}
	if err := netutil.ValidateCIDRs(c.args.ipv4Cidr, c.args.ipv6Cidr); err != nil {
		return err
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

	client := publicv1.NewSubnetsClient(conn)

	spec := publicv1.SubnetSpec_builder{
		VirtualNetwork: vn.GetId(),
	}
	if c.args.ipv4Cidr != "" {
		spec.Ipv4Cidr = &c.args.ipv4Cidr
	}
	if c.args.ipv6Cidr != "" {
		spec.Ipv6Cidr = &c.args.ipv6Cidr
	}
	subnet := publicv1.Subnet_builder{
		Metadata: publicv1.Metadata_builder{
			Name:   c.args.name,
			Tenant: c.settings.Tenant(),
		}.Build(),
		Spec: spec.Build(),
	}.Build()

	response, err := client.Create(ctx, publicv1.SubnetsCreateRequest_builder{Object: subnet}.Build())
	if err != nil {
		return fmt.Errorf("failed to create subnet: %w", err)
	}

	c.console.Infof(ctx, "Created subnet '%s' (ID: %s).\n", response.Object.GetMetadata().GetName(), response.Object.GetId())

	return nil
}

const shortHelp = `Create a subnet.`

const longHelp = `
Create a subnet within an existing virtual network. At least one of
{{ bt }}--ipv4-cidr{{ bt }} or {{ bt }}--ipv6-cidr{{ bt }} must be provided.

To create an IPv4-only subnet:

{{ bt 3 }}shell
{{ binary }} create subnet --name my-subnet --virtual-network vnet-abc123 --ipv4-cidr 10.0.1.0/24
{{ bt 3 }}

To create a dual-stack subnet:

{{ bt 3 }}shell
{{ binary }} create subnet --name my-subnet --virtual-network vnet-abc123 --ipv4-cidr 10.0.1.0/24 --ipv6-cidr fd00:1234::/64
{{ bt 3 }}
`

const nameFlagHelp = `
_NAME_ - Name of the subnet.
`

const virtualNetworkFlagHelp = `
_ID|NAME_ - ID or name of the parent virtual network.
`

const ipv4CidrFlagHelp = `
_CIDR_ - IPv4 CIDR block for this subnet, for example
{{ bt }}10.0.1.0/24{{ bt }}.
`

const ipv6CidrFlagHelp = `
_CIDR_ - IPv6 CIDR block for this subnet, for example
{{ bt }}fd00:1234::/64{{ bt }}.
`
