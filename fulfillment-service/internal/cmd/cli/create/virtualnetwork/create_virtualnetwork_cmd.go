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
	"log/slog"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/create/netutil"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "virtualnetwork [FLAG...]",
		Aliases:               []string{string(proto.MessageName((*publicv1.VirtualNetwork)(nil)))},
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
		&runner.args.networkClass,
		"network-class",
		"",
		networkClassFlagHelp,
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
		name         string
		networkClass string
		ipv4Cidr     string
		ipv6Cidr     string
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

	if err := validateNetworkClass(c.args.networkClass); err != nil {
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

	client := publicv1.NewVirtualNetworksClient(conn)

	enableIpv4 := c.args.ipv4Cidr != ""
	enableIpv6 := c.args.ipv6Cidr != ""
	enableDualStack := enableIpv4 && enableIpv6

	spec := publicv1.VirtualNetworkSpec_builder{
		NetworkClass: c.args.networkClass,
		Capabilities: publicv1.VirtualNetworkCapabilities_builder{
			EnableIpv4:      enableIpv4,
			EnableIpv6:      enableIpv6,
			EnableDualStack: enableDualStack,
		}.Build(),
	}
	if c.args.ipv4Cidr != "" {
		spec.Ipv4Cidr = &c.args.ipv4Cidr
	}
	if c.args.ipv6Cidr != "" {
		spec.Ipv6Cidr = &c.args.ipv6Cidr
	}
	vn := publicv1.VirtualNetwork_builder{
		Metadata: publicv1.Metadata_builder{
			Name:   c.args.name,
			Tenant: c.settings.Tenant(),
		}.Build(),
		Spec: spec.Build(),
	}.Build()

	response, err := client.Create(ctx, publicv1.VirtualNetworksCreateRequest_builder{Object: vn}.Build())
	if err != nil {
		return fmt.Errorf("failed to create virtual network: %w", err)
	}

	c.console.Infof(ctx, "Created virtual network '%s' (ID: %s).\n", response.Object.GetMetadata().GetName(), response.Object.GetId())

	return nil
}

func validateNetworkClass(networkClass string) error {
	if networkClass == "" {
		return fmt.Errorf("network class is required")
	}
	return nil
}

const shortHelp = `Create a virtual network.`

const longHelp = `
Create a virtual network with the specified network class and IP addressing
configuration. At least one of {{ bt }}--ipv4-cidr{{ bt }} or
{{ bt }}--ipv6-cidr{{ bt }} must be provided.

To create an IPv4-only virtual network:

{{ bt 3 }}shell
{{ binary }} create virtualnetwork --name my-network --network-class udn-net --ipv4-cidr 10.0.0.0/16
{{ bt 3 }}

To create a dual-stack virtual network:

{{ bt 3 }}shell
{{ binary }} create virtualnetwork --name my-network --network-class udn-net --ipv4-cidr 10.0.0.0/16 --ipv6-cidr fd00::/64
{{ bt 3 }}
`

const nameFlagHelp = `
_NAME_ - Name of the virtual network.
`

const networkClassFlagHelp = `
_CLASS_ - Network class to use for this virtual network.
`

const ipv4CidrFlagHelp = `
_CIDR_ - IPv4 CIDR block for this network, for example
{{ bt }}10.0.0.0/16{{ bt }}.
`

const ipv6CidrFlagHelp = `
_CIDR_ - IPv6 CIDR block for this network, for example
{{ bt }}fd00::/64{{ bt }}.
`
