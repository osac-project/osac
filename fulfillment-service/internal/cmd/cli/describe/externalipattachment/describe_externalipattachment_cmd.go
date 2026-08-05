/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package externalipattachment

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
		Use:                   "externalipattachment [FLAG...] ID|NAME",
		Aliases:               []string{"externalipattachments"},
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

	client := publicv1.NewExternalIPAttachmentsClient(conn)

	matched, err := lookup.Find(ref, "external IP attachment", func(filter string, limit int32) ([]*publicv1.ExternalIPAttachment, error) {
		resp, err := client.List(ctx, publicv1.ExternalIPAttachmentsListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to describe external IP attachment: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	RenderExternalIPAttachment(c.console, matched)

	return nil
}

func RenderExternalIPAttachment(w io.Writer, a *publicv1.ExternalIPAttachment) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	name := "-"
	if v := a.GetMetadata().GetName(); v != "" {
		name = v
	}

	externalIP := "-"
	if v := a.GetSpec().GetExternalIp(); v != "" {
		externalIP = v
	}

	targetType := "-"
	targetID := "-"
	if v := a.GetSpec().GetComputeInstance(); v != "" {
		targetType = "ComputeInstance"
		targetID = v
	} else if v := a.GetSpec().GetCluster(); v != "" {
		targetType = "Cluster"
		targetID = v
	} else if v := a.GetSpec().GetBaremetalInstance(); v != "" {
		targetType = "BaremetalInstance"
		targetID = v
	}

	state := "-"
	externalIPAddress := "-"
	message := "-"
	if a.GetStatus() != nil {
		state = strings.TrimPrefix(a.GetStatus().GetState().String(), "EXTERNAL_IP_ATTACHMENT_STATE_")
		if v := a.GetStatus().GetExternalIpAddress(); v != "" {
			externalIPAddress = v
		}
		if v := a.GetStatus().GetMessage(); v != "" {
			message = v
		}
	}

	fmt.Fprintf(writer, "ID:\t%s\n", a.GetId())
	fmt.Fprintf(writer, "Name:\t%s\n", name)
	fmt.Fprintf(writer, "External IP:\t%s\n", externalIP)
	fmt.Fprintf(writer, "Target Type:\t%s\n", targetType)
	fmt.Fprintf(writer, "Target:\t%s\n", targetID)
	if a.GetSpec().GetCluster() != "" {
		endpoint := strings.TrimPrefix(a.GetSpec().GetTargetEndpoint().String(), "EXTERNAL_IP_ATTACHMENT_ENDPOINT_")
		fmt.Fprintf(writer, "Target Endpoint:\t%s\n", endpoint)
	}
	fmt.Fprintf(writer, "External IP Address:\t%s\n", externalIPAddress)
	fmt.Fprintf(writer, "State:\t%s\n", state)
	fmt.Fprintf(writer, "Message:\t%s\n", message)
	writer.Flush()
}

const shortHelp = `Describe an external IP attachment`

const longHelp = `
Display detailed information about an external IP attachment, referenced by identifier or name.

Examples:

{{ bt 3 }}shell
# Describe an external IP attachment by identifier:
{{ binary }} describe externalipattachment 019e5fee-0742-78b7-8c4a-e2501f44783a
{{ bt 3 }}

{{ bt 3 }}shell
# Describe an external IP attachment by name:
{{ binary }} describe externalipattachment my-attachment
{{ bt 3 }}
`
