/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package externalip

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
		Use:                   "externalip [FLAG...] ID|NAME",
		Aliases:               []string{"externalips"},
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

	client := publicv1.NewExternalIPsClient(conn)

	matched, err := lookup.Find(ref, "external IP", func(filter string, limit int32) ([]*publicv1.ExternalIP, error) {
		resp, err := client.List(ctx, publicv1.ExternalIPsListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to describe external IP: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	RenderExternalIP(c.console, matched)

	return nil
}

func RenderExternalIP(w io.Writer, eip *publicv1.ExternalIP) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	name := "-"
	if v := eip.GetMetadata().GetName(); v != "" {
		name = v
	}

	pool := "-"
	if v := eip.GetSpec().GetPool(); v != "" {
		pool = v
	}

	attached := "false"
	if eip.GetStatus() != nil && eip.GetStatus().GetAttached() {
		attached = "true"
	}

	address := "-"
	state := "-"
	message := "-"
	if eip.GetStatus() != nil {
		state = strings.TrimPrefix(eip.GetStatus().GetState().String(), "EXTERNAL_IP_STATE_")
		if v := eip.GetStatus().GetAddress(); v != "" {
			address = v
		}
		if v := eip.GetStatus().GetMessage(); v != "" {
			message = v
		}
	}

	fmt.Fprintf(writer, "ID:\t%s\n", eip.GetId())
	fmt.Fprintf(writer, "Name:\t%s\n", name)
	fmt.Fprintf(writer, "Pool:\t%s\n", pool)
	fmt.Fprintf(writer, "Attached:\t%s\n", attached)
	fmt.Fprintf(writer, "Address:\t%s\n", address)
	fmt.Fprintf(writer, "State:\t%s\n", state)
	fmt.Fprintf(writer, "Message:\t%s\n", message)
	writer.Flush()
}

const shortHelp = `Describe an external IP`

const longHelp = `
Display detailed information about an external IP, referenced by identifier or name.

Examples:

{{ bt 3 }}shell
# Describe an external IP by identifier:
{{ binary }} describe externalip 019e5fee-0742-78b7-8c4a-e2501f44783a
{{ bt 3 }}

{{ bt 3 }}shell
# Describe an external IP by name:
{{ binary }} describe externalip my-ip
{{ bt 3 }}
`
