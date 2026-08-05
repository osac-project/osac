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

// Cmd creates the command to describe a security group.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "securitygroup [FLAG...] ID|NAME",
		Aliases:               []string{"securitygroups"},
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

	client := publicv1.NewSecurityGroupsClient(conn)

	matched, err := lookup.Find(ref, "security group", func(filter string, limit int32) ([]*publicv1.SecurityGroup, error) {
		resp, err := client.List(ctx, publicv1.SecurityGroupsListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to describe security group: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	RenderSecurityGroup(c.console, matched)

	return nil
}

// RenderSecurityGroup writes a formatted description of sg to w.
func RenderSecurityGroup(w io.Writer, sg *publicv1.SecurityGroup) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	name := "-"
	if v := sg.GetMetadata().GetName(); v != "" {
		name = v
	}

	state := "-"
	message := "-"
	if sg.GetStatus() != nil {
		state = strings.TrimPrefix(sg.GetStatus().GetState().String(), "SECURITY_GROUP_STATE_")
		if v := sg.GetStatus().GetMessage(); v != "" {
			message = v
		}
	}

	fmt.Fprintf(writer, "ID:\t%s\n", sg.GetId())
	fmt.Fprintf(writer, "Name:\t%s\n", name)
	fmt.Fprintf(writer, "Virtual Network:\t%s\n", sg.GetSpec().GetVirtualNetwork())
	fmt.Fprintf(writer, "State:\t%s\n", state)
	fmt.Fprintf(writer, "Message:\t%s\n", message)
	writer.Flush()
	fmt.Fprintln(w)

	renderRules(w, "Ingress Rules", sg.GetSpec().GetIngress())
	renderRules(w, "Egress Rules", sg.GetSpec().GetEgress())
}

func renderRules(w io.Writer, label string, rules []*publicv1.SecurityRule) {
	if len(rules) == 0 {
		fmt.Fprintf(w, "%s:  (none)\n", label)
		return
	}

	fmt.Fprintf(w, "%s:\n", label)
	ruleWriter := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(ruleWriter, "  PROTOCOL\tPORT-FROM\tPORT-TO\tIPV4-CIDR\tIPV6-CIDR\n")
	for _, rule := range rules {
		protocol := strings.ToLower(strings.TrimPrefix(rule.GetProtocol().String(), "PROTOCOL_"))

		portFrom := "-"
		if rule.HasPortFrom() {
			portFrom = fmt.Sprintf("%d", rule.GetPortFrom())
		}

		portTo := "-"
		if rule.HasPortTo() {
			portTo = fmt.Sprintf("%d", rule.GetPortTo())
		}

		ipv4Cidr := "-"
		if v := rule.GetIpv4Cidr(); v != "" {
			ipv4Cidr = v
		}

		ipv6Cidr := "-"
		if v := rule.GetIpv6Cidr(); v != "" {
			ipv6Cidr = v
		}

		fmt.Fprintf(ruleWriter, "  %s\t%s\t%s\t%s\t%s\n", protocol, portFrom, portTo, ipv4Cidr, ipv6Cidr)
	}
	ruleWriter.Flush()
}

const shortHelp = "Describe a security group"

const longHelp = `
Display detailed information about a security group, referenced by identifier or name,
including its ingress and egress rules.

Examples:

{{ bt 3 }}shell
# Describe a security group by identifier:
{{ binary }} describe securitygroup 019e5fef-6d56-78d1-b282-7b5456b86888
{{ bt 3 }}

{{ bt 3 }}shell
# Describe a security group by name:
{{ binary }} describe securitygroup web-sg
{{ bt 3 }}
`
