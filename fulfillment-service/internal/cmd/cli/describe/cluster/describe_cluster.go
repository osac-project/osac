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
	"cmp"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
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
				// Override public-API defaults because a cluster may reference any version.
				// Enum values are compared as text, so use != rather than a numeric range.
				filter = fmt.Sprintf("(%s) && this.spec.state != 0 && (this.spec.enabled == true || this.spec.enabled == false)", filter)
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

	return renderCluster(c.console, matched, version)
}

func renderCluster(w io.Writer, cluster *publicv1.Cluster, version *publicv1.ClusterVersion) error {
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
	observedVersion := cmp.Or(cluster.GetStatus().GetObservedVersion(), "-")
	if _, err := fmt.Fprintf(writer, "ID:\t%s\n", cluster.Id); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "Catalog Item:\t%s\n", catalogItem); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "State:\t%s\n", state); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "Control Plane Current Version:\t%s\n", observedVersion); err != nil {
		return err
	}

	if err := writer.Flush(); err != nil {
		return err
	}

	if err := renderNodeSets(w, cluster.GetStatus().GetNodeSets()); err != nil {
		return err
	}
	if err := renderUpgradeHistory(w, cluster.GetStatus().GetUpgradeHistory()); err != nil {
		return err
	}

	if cluster.GetSpec().HasVersion() {
		fmt.Fprintln(w, "Desired Version:")
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

	return nil
}

func renderNodeSets(w io.Writer, nodeSets map[string]*publicv1.ClusterNodeSetStatus) error {
	if len(nodeSets) == 0 {
		return nil
	}

	if _, err := fmt.Fprintln(w, "Node Sets:"); err != nil {
		return err
	}
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintf(writer, "\tName\tSize\tCurrent Version\n"); err != nil {
		return err
	}

	names := slices.Sorted(maps.Keys(nodeSets))

	for _, name := range names {
		status := nodeSets[name]
		size := "-"
		if status.HasSize() {
			size = strconv.Itoa(int(status.GetSize()))
		}
		observedVersion := cmp.Or(status.GetObservedVersion(), "-")
		if _, err := fmt.Fprintf(writer, "\t%s\t%s\t%s\n", name, size, observedVersion); err != nil {
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	return nil
}

func renderUpgradeHistory(w io.Writer, history []*publicv1.ClusterUpgradeHistoryEntry) error {
	if len(history) == 0 {
		return nil
	}

	if _, err := fmt.Fprintln(w, "Upgrade History:"); err != nil {
		return err
	}
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintf(writer, "\tSource\tTarget\tState\tAccepted At\tLast Transition\tMessage\n"); err != nil {
		return err
	}

	for _, entry := range history {
		sourceVersion := cmp.Or(entry.GetSourceVersion(), "-")
		targetVersion := cmp.Or(entry.GetTargetVersion(), "-")
		state := strings.TrimPrefix(entry.GetState().String(), "CLUSTER_UPGRADE_STATE_")
		message := cmp.Or(entry.GetMessage(), "-")
		if _, err := fmt.Fprintf(writer, "\t%s\t%s\t%s\t%s\t%s\t%s\n",
			sourceVersion,
			targetVersion,
			state,
			formatTimestamp(entry.GetAcceptedTime()),
			formatTimestamp(entry.GetStateTransitionTime()),
			message,
		); err != nil {
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	return nil
}

func formatTimestamp(timestamp *timestamppb.Timestamp) string {
	if timestamp == nil {
		return "-"
	}
	return timestamp.AsTime().Format(time.RFC3339)
}

const shortHelp = `Describe a cluster`

const longHelp = `
Describe a cluster.
`
