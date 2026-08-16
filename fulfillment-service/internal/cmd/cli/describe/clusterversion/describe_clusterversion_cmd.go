/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package clusterversion

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

// Cmd creates the command to describe a cluster version.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "clusterversion [FLAG...] ID|NAME",
		Aliases:               []string{"clusterversions"},
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

	client := publicv1.NewClusterVersionsClient(conn)

	matched, err := lookup.Find(ref, "cluster version", func(filter string, limit int32) ([]*publicv1.ClusterVersion, error) {
		resp, err := client.List(ctx, publicv1.ClusterVersionsListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to describe cluster version: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	renderClusterVersion(c.console, matched)

	return nil
}

func renderClusterVersion(w io.Writer, cv *publicv1.ClusterVersion) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	name := "-"
	if v := cv.GetMetadata().GetName(); v != "" {
		name = v
	}
	fmt.Fprintf(writer, "Name:\t%s\n", name)

	spec := cv.GetSpec()
	if spec != nil {
		fmt.Fprintf(writer, "Version:\t%s\n", spec.GetVersion())

		state := strings.TrimPrefix(spec.GetState().String(), "CLUSTER_VERSION_STATE_")
		fmt.Fprintf(writer, "State:\t%s\n", state)

		enabled := "-"
		if spec.HasEnabled() {
			enabled = fmt.Sprintf("%t", spec.GetEnabled())
		}
		fmt.Fprintf(writer, "Enabled:\t%s\n", enabled)

		isDefault := "-"
		if spec.HasIsDefault() {
			isDefault = fmt.Sprintf("%t", spec.GetIsDefault())
		}
		fmt.Fprintf(writer, "Default:\t%s\n", isDefault)

		if dep := spec.GetDeprecation(); dep != nil {
			if ts := dep.GetDeprecationTimestamp(); ts != nil {
				fmt.Fprintf(writer, "Deprecated At:\t%s\n", ts.AsTime().Format(time.RFC3339))
			}
			if ts := dep.GetObsolescenceTimestamp(); ts != nil {
				fmt.Fprintf(writer, "Obsolete At:\t%s\n", ts.AsTime().Format(time.RFC3339))
			}
		}
	}

	writer.Flush()
}

const shortHelp = `Describe a cluster version`

const longHelp = `
Describe a cluster version.

Displays detailed information about a cluster version, including its version string, lifecycle
state, availability, and deprecation details.

To describe a cluster version by name:

{{ bt 3 }}shell
{{ binary }} describe clusterversion 4-17-0
{{ bt 3 }}
`
