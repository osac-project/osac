/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package networkclass

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "networkclass [FLAG...] ID|NAME",
		Aliases:               []string{"networkclasses"},
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

	client := publicv1.NewNetworkClassesClient(conn)

	matched, err := lookup.Find(ref, "network class", func(filter string, limit int32) ([]*publicv1.NetworkClass, error) {
		resp, err := client.List(ctx, publicv1.NetworkClassesListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to describe network class: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	renderNetworkClass(c.console, matched)

	return nil
}

func boolYesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// renderNetworkClass writes a formatted description of nc to w.
func renderNetworkClass(w io.Writer, nc *publicv1.NetworkClass) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	name := "-"
	if v := nc.GetMetadata().GetName(); v != "" {
		name = v
	}

	title := "-"
	if v := nc.GetTitle(); v != "" {
		title = v
	}

	description := "-"
	if v := nc.GetDescription(); v != "" {
		description = v
	}

	supportsIPv4 := "-"
	supportsIPv6 := "-"
	supportsDualStack := "-"
	if caps := nc.GetCapabilities(); caps != nil {
		supportsIPv4 = boolYesNo(caps.GetSupportsIpv4())
		supportsIPv6 = boolYesNo(caps.GetSupportsIpv6())
		supportsDualStack = boolYesNo(caps.GetSupportsDualStack())
	}

	isDefault := "-"
	if nc.HasIsDefault() {
		isDefault = boolYesNo(nc.GetIsDefault())
	}

	state := "-"
	message := "-"
	if nc.GetStatus() != nil {
		state = strings.TrimPrefix(nc.GetStatus().GetState().String(), "NETWORK_CLASS_STATE_")
		if v := nc.GetStatus().GetMessage(); v != "" {
			message = v
		}
	}

	fmt.Fprintf(writer, "ID:\t%s\n", nc.GetId())
	fmt.Fprintf(writer, "Name:\t%s\n", name)
	fmt.Fprintf(writer, "Title:\t%s\n", title)
	fmt.Fprintf(writer, "Description:\t%s\n", description)
	fmt.Fprintf(writer, "Supports IPv4:\t%s\n", supportsIPv4)
	fmt.Fprintf(writer, "Supports IPv6:\t%s\n", supportsIPv6)
	fmt.Fprintf(writer, "Supports Dual Stack:\t%s\n", supportsDualStack)
	fmt.Fprintf(writer, "Default:\t%s\n", isDefault)
	fmt.Fprintf(writer, "State:\t%s\n", state)
	fmt.Fprintf(writer, "Message:\t%s\n", message)
	writer.Flush()
}

const shortHelp = "Describe a network class"

const longHelp = `
Display detailed information about a network class, referenced by identifier or name.

Examples:

{{ bt 3 }}shell
# Describe a network class by identifier:
{{ binary }} describe networkclass 019e5ff0-6266-7310-acf3-94e99a3786c9
{{ bt 3 }}

{{ bt 3 }}shell
# Describe a network class by name:
{{ binary }} describe networkclass udn-net
{{ bt 3 }}
`
