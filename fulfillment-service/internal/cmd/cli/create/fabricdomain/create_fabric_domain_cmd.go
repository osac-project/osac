/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package fabricdomain

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "fabricdomain [FLAG...]",
		Aliases:               []string{string(proto.MessageName((*publicv1.FabricDomain)(nil)))},
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}
	flags := result.Flags()
	flags.StringVarP(&runner.args.name, "name", "n", "", nameFlagHelp)
	flags.StringVar(&runner.args.domainType, "type", "ethernet_ew", typeFlagHelp)
	flags.StringSliceVar(&runner.args.servers, "servers", nil, serversFlagHelp)
	flags.StringVar(&runner.args.virtualNetwork, "virtual-network", "", virtualNetworkFlagHelp)
	_ = result.MarkFlagRequired("name")
	_ = result.MarkFlagRequired("servers")
	_ = result.MarkFlagRequired("virtual-network")
	return result
}

type runnerContext struct {
	args struct {
		name           string
		domainType     string
		servers        []string
		virtualNetwork string
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

	domainType, err := parseType(c.args.domainType)
	if err != nil {
		return err
	}
	servers := make([]string, 0, len(c.args.servers))
	for _, server := range c.args.servers {
		server = strings.TrimSpace(server)
		if server != "" {
			servers = append(servers, server)
		}
	}
	if len(servers) == 0 {
		return fmt.Errorf("at least one server must be specified")
	}

	conn, err := c.settings.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	vnClient := publicv1.NewVirtualNetworksClient(conn)
	virtualNetwork, err := lookup.Find(c.args.virtualNetwork, "virtual network", func(filter string, limit int32) ([]*publicv1.VirtualNetwork, error) {
		response, err := vnClient.List(ctx, publicv1.VirtualNetworksListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to resolve virtual network %q: %w", c.args.virtualNetwork, err)
		}
		return response.GetItems(), nil
	})
	if err != nil {
		return err
	}

	object := publicv1.FabricDomain_builder{
		Metadata: publicv1.Metadata_builder{
			Name:   c.args.name,
			Tenant: c.settings.Tenant(),
		}.Build(),
		Spec: publicv1.FabricDomainSpec_builder{
			Type:            domainType,
			Servers:         servers,
			VirtualNetworks: []string{virtualNetwork.GetId()},
		}.Build(),
	}.Build()

	client := publicv1.NewFabricDomainsClient(conn)
	response, err := client.Create(ctx, publicv1.FabricDomainsCreateRequest_builder{Object: object}.Build())
	if err != nil {
		return fmt.Errorf("failed to create fabric domain: %w", err)
	}

	c.console.Infof(ctx, "Created fabric domain '%s' (ID: %s).\n", response.GetObject().GetMetadata().GetName(), response.GetObject().GetId())
	return nil
}

func parseType(value string) (publicv1.FabricDomainType, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ethernet_ew":
		return publicv1.FabricDomainType_FABRIC_DOMAIN_TYPE_ETHERNET_EW, nil
	default:
		return publicv1.FabricDomainType_FABRIC_DOMAIN_TYPE_UNSPECIFIED, fmt.Errorf("unsupported fabric domain type %q; only 'ethernet_ew' is supported", value)
	}
}

const shortHelp = `Create a fabric domain`

const longHelp = `
Create an east-west fabric domain for a virtual network.

Phase 1 supports the {{ bt }}ethernet_ew{{ bt }} type. Servers may be supplied
as a comma-separated list or by repeating the {{ bt }}--servers{{ bt }} flag.

{{ bt 3 }}shell
{{ binary }} create fabricdomain --name tenant-a-gpu-ew --type ethernet_ew \\
  --servers hgx-01,hgx-02,hgx-03,hgx-04 --virtual-network tenant-a-vn
{{ bt 3 }}
`

const nameFlagHelp = `
_NAME_ - Name of the fabric domain. Required.
`

const typeFlagHelp = `
_TYPE_ - Fabric domain type. Currently only {{ bt }}ethernet_ew{{ bt }} is supported.
`

const serversFlagHelp = `
_HOSTNAMES_ - Comma-separated server hostnames, or repeat this flag. Required.
`

const virtualNetworkFlagHelp = `
_ID|NAME_ - Virtual network associated with the fabric domain. Required.
`
