/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package securitygroup

import (
	"fmt"
	"log/slog"
	"net/netip"
	"strconv"
	"strings"

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
		Use:                   "securitygroup [FLAG...]",
		Aliases:               []string{string(proto.MessageName((*publicv1.SecurityGroup)(nil)))},
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
	flags.StringArrayVar(
		&runner.args.ingressRules,
		"ingress",
		nil,
		ingressFlagHelp,
	)
	flags.StringArrayVar(
		&runner.args.egressRules,
		"egress",
		nil,
		egressFlagHelp,
	)
	return result
}

type runnerContext struct {
	args struct {
		name           string
		virtualNetwork string
		ingressRules   []string
		egressRules    []string
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

	ingress, err := parseSecurityRules(c.args.ingressRules)
	if err != nil {
		return fmt.Errorf("invalid ingress rule: %w", err)
	}
	egress, err := parseSecurityRules(c.args.egressRules)
	if err != nil {
		return fmt.Errorf("invalid egress rule: %w", err)
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

	client := publicv1.NewSecurityGroupsClient(conn)

	sg := publicv1.SecurityGroup_builder{
		Metadata: publicv1.Metadata_builder{
			Name:   c.args.name,
			Tenant: c.settings.Tenant(),
		}.Build(),
		Spec: publicv1.SecurityGroupSpec_builder{
			VirtualNetwork: vn.GetId(),
			Ingress:        ingress,
			Egress:         egress,
		}.Build(),
	}.Build()

	response, err := client.Create(ctx, publicv1.SecurityGroupsCreateRequest_builder{Object: sg}.Build())
	if err != nil {
		return fmt.Errorf("failed to create security group: %w", err)
	}

	c.console.Infof(ctx, "Created security group '%s' (ID: %s).\n", response.Object.GetMetadata().GetName(), response.Object.GetId())

	return nil
}

// parseSecurityRules parses a slice of rule strings in key=value,... format into SecurityRule protos.
// Returns nil, nil for empty input (no rules is valid).
func parseSecurityRules(ruleArgs []string) ([]*publicv1.SecurityRule, error) {
	if len(ruleArgs) == 0 {
		return nil, nil
	}

	rules := make([]*publicv1.SecurityRule, 0, len(ruleArgs))
	for i, ruleArg := range ruleArgs {
		rule, err := parseSecurityRule(ruleArg)
		if err != nil {
			return nil, fmt.Errorf("rule %d (%q): %w", i+1, ruleArg, err)
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func parseSecurityRule(ruleArg string) (*publicv1.SecurityRule, error) {
	pairs := strings.Split(ruleArg, ",")

	seen := make(map[string]bool)
	var protocol publicv1.Protocol
	var protocolSet bool
	var portFrom, portTo *int32
	var ipv4Cidr, ipv6Cidr *string

	for _, pair := range pairs {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid key=value pair: %q", pair)
		}
		key := parts[0]
		value := parts[1]

		if seen[key] {
			return nil, fmt.Errorf("duplicate key %q in rule", key)
		}
		seen[key] = true

		switch key {
		case "protocol":
			p, err := parseProtocol(value)
			if err != nil {
				return nil, err
			}
			protocol = p
			protocolSet = true
		case "port-from":
			port, err := parsePort(value)
			if err != nil {
				return nil, fmt.Errorf("invalid port-from: %w", err)
			}
			portFrom = &port
		case "port-to":
			port, err := parsePort(value)
			if err != nil {
				return nil, fmt.Errorf("invalid port-to: %w", err)
			}
			portTo = &port
		case "ipv4-cidr":
			prefix, err := netip.ParsePrefix(value)
			if err != nil {
				return nil, fmt.Errorf("invalid ipv4-cidr %q: %w", value, err)
			}
			if !prefix.Addr().Is4() {
				return nil, fmt.Errorf("invalid ipv4-cidr %q: address is not IPv4", value)
			}
			ipv4Cidr = &value
		case "ipv6-cidr":
			prefix, err := netip.ParsePrefix(value)
			if err != nil {
				return nil, fmt.Errorf("invalid ipv6-cidr %q: %w", value, err)
			}
			if !prefix.Addr().Is6() {
				return nil, fmt.Errorf("invalid ipv6-cidr %q: address is not IPv6", value)
			}
			ipv6Cidr = &value
		default:
			return nil, fmt.Errorf("unknown key %q in rule", key)
		}
	}

	if !protocolSet {
		return nil, fmt.Errorf("protocol is required in rule %q", ruleArg)
	}

	// Validate port range consistency
	if (portFrom == nil) != (portTo == nil) {
		return nil, fmt.Errorf("port-from and port-to must both be specified or both be omitted")
	}
	if portFrom != nil && portTo != nil && *portFrom > *portTo {
		return nil, fmt.Errorf("port-from (%d) must be less than or equal to port-to (%d)", *portFrom, *portTo)
	}

	return publicv1.SecurityRule_builder{
		Protocol: protocol,
		PortFrom: portFrom,
		PortTo:   portTo,
		Ipv4Cidr: ipv4Cidr,
		Ipv6Cidr: ipv6Cidr,
	}.Build(), nil
}

func parseProtocol(value string) (publicv1.Protocol, error) {
	switch strings.ToLower(value) {
	case "tcp":
		return publicv1.Protocol_PROTOCOL_TCP, nil
	case "udp":
		return publicv1.Protocol_PROTOCOL_UDP, nil
	case "icmp":
		return publicv1.Protocol_PROTOCOL_ICMP, nil
	case "all":
		return publicv1.Protocol_PROTOCOL_ALL, nil
	default:
		return publicv1.Protocol_PROTOCOL_UNSPECIFIED, fmt.Errorf("invalid protocol %q: must be one of tcp, udp, icmp, all", value)
	}
}

func parsePort(value string) (int32, error) {
	n, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid port value %q: %w", value, err)
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("port %d is out of range (1-65535)", n)
	}
	return int32(n), nil
}

const shortHelp = `Create a security group.`

const longHelp = `
Create a security group with optional ingress and egress firewall rules.

Rules are specified using {{ bt }}key=value{{ bt }} pairs separated by commas. For example:
{{ bt }}protocol=tcp,port-from=80,port-to=80,ipv4-cidr=0.0.0.0/0{{ bt }}.

Supported keys:

* {{ bt }}protocol{{ bt }} - Required, one of {{ bt }}tcp{{ bt }}, {{ bt }}udp{{ bt }}, {{ bt }}icmp{{ bt }} or {{ bt }}all{{ bt }}.
* {{ bt }}port-from{{ bt }} - Optional, port number from which the traffic is allowed.
* {{ bt }}port-to{{ bt }} - Optional, port number to which the traffic is allowed.
* {{ bt }}ipv4-cidr{{ bt }} - Optional, IPv4 CIDR block for the traffic.

Examples:

{{ bt 3 }}shell
# Create a security group with an HTTP ingress rule
{{ binary }} create securitygroup --name web-sg --virtual-network <virtualnetwork-id> \
--ingress protocol=tcp,port-from=80,port-to=80,ipv4-cidr=0.0.0.0/0
{{ bt 3 }}

{{ bt 3 }}shell
# Create a security group with multiple rules
{{ binary }} create securitygroup --name app-sg --virtual-network <virtualnetwork-id> \
--ingress protocol=tcp,port-from=443,port-to=443,ipv4-cidr=0.0.0.0/0 \
--ingress protocol=icmp,ipv4-cidr=10.0.0.0/8 \
--egress protocol=all
{{ bt 3 }}
`

const nameFlagHelp = `
_NAME_ - Name of the security group.
`

const virtualNetworkFlagHelp = `
_ID|NAME_ - ID or name of the virtual network to associate with this security
group.
`

const ingressFlagHelp = `
_RULE_ - Ingress rule in {{ bt }}key=value,...{{ bt }} format, for example
{{ bt }}protocol=tcp,port-from=80,port-to=80,ipv4-cidr=0.0.0.0/0{{ bt }}.
Can be specified multiple times.
`

const egressFlagHelp = `
_RULE_ - Egress rule in {{ bt }}key=value,...{{ bt }} format, for example
{{ bt }}protocol=all{{ bt }}. Can be specified multiple times.
`
