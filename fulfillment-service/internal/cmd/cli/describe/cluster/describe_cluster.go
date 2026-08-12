/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package cluster

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

// Cmd creates the command to describe a cluster.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "cluster [FLAG...] ID|NAME",
		Aliases:               []string{"clusters"},
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

	client := publicv1.NewClustersClient(conn)

	matched, err := lookup.Find(ref, "cluster", func(filter string, limit int32) ([]*publicv1.Cluster, error) {
		resp, err := client.List(ctx, publicv1.ClustersListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to describe cluster: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	// Resolve the cluster version, if the cluster references one. Failure to resolve it is not fatal -- version
	// details are supplementary to the cluster description.
	var version *publicv1.ClusterVersion
	if matched.GetSpec().HasVersion() {
		versionName := matched.GetSpec().GetVersion().GetName()
		versionsClient := publicv1.NewClusterVersionsClient(conn)
		version, err = lookup.Find(versionName, "cluster version",
			func(filter string, limit int32) ([]*publicv1.ClusterVersion, error) {
				// Override the public-API default filter that hides obsolete versions.
				// A cluster may reference any version regardless of lifecycle state.
				filter = fmt.Sprintf("(%s) && this.spec.state > 0", filter)
				resp, err := versionsClient.List(ctx, publicv1.ClusterVersionsListRequest_builder{
					Filter: proto.String(filter),
					Limit:  proto.Int32(limit),
				}.Build())
				if err != nil {
					return nil, fmt.Errorf("failed to fetch cluster version: %w", err)
				}
				return resp.GetItems(), nil
			},
		)
		if err != nil {
			c.console.Errorf(ctx, "Warning: failed to resolve cluster version %q: %v\n", versionName, err)
			version = nil
		}
	}

	renderCluster(c.console, matched, version)

	return nil
}

func renderCluster(w io.Writer, cluster *publicv1.Cluster, version *publicv1.ClusterVersion) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	catalogItem := "-"
	if cluster.Spec != nil {
		if ref := cluster.Spec.GetCatalogItem(); ref != nil {
			if ref.GetName() != "" {
				catalogItem = ref.GetName()
			} else if ref.GetId() != "" {
				catalogItem = ref.GetId()
			}
		}
	}
	state := "-"
	if cluster.Status != nil {
		state = cluster.Status.State.String()
		state = strings.TrimPrefix(state, "CLUSTER_STATE_")
	}
	fmt.Fprintf(writer, "ID:\t%s\n", cluster.Id)
	fmt.Fprintf(writer, "Catalog Item:\t%s\n", catalogItem)
	fmt.Fprintf(writer, "State:\t%s\n", state)

	writer.Flush()

	// Version section — rendered as indented fields under a "Version:" header.
	if cluster.GetSpec().HasVersion() {
		fmt.Fprintln(w, "Version:")
		versionWriter := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

		fmt.Fprintf(versionWriter, "\tName:\t%s\n", cluster.GetSpec().GetVersion().GetName())

		versionStr := "-"
		versionState := "-"
		if version.GetSpec() != nil {
			versionStr = version.GetSpec().GetVersion()
			versionState = strings.TrimPrefix(
				version.GetSpec().GetState().String(),
				"CLUSTER_VERSION_STATE_",
			)
		}
		fmt.Fprintf(versionWriter, "\tVersion:\t%s\n", versionStr)
		fmt.Fprintf(versionWriter, "\tState:\t%s\n", versionState)

		if dep := version.GetSpec().GetDeprecation(); dep != nil {
			if ts := dep.GetDeprecationTimestamp(); ts != nil {
				fmt.Fprintf(versionWriter, "\tDeprecated At:\t%s\n", ts.AsTime().Format(time.RFC3339))
			}
			if ts := dep.GetObsolescenceTimestamp(); ts != nil {
				fmt.Fprintf(versionWriter, "\tObsolete At:\t%s\n", ts.AsTime().Format(time.RFC3339))
			}
		}

		versionWriter.Flush()
	}
}

const shortHelp = `Describe a cluster`

const longHelp = `
Describe a cluster.
`
