/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package scale

import (
	"context"
	"embed"
	"fmt"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

//go:embed templates
var templatesFS embed.FS

//go:generate mockgen -source=../../../api/osac/public/v1/clusters_service_grpc.pb.go -destination=clusters_client_mock.go -package=scale ClustersClient

func Cmd() *cobra.Command {
	result := &cobra.Command{
		Use:                   "scale",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
	}
	result.AddCommand(clusterCmd())
	return result
}

type runnerContext struct {
	args struct {
		nodeSet string
		size    int32
	}
	console *terminal.Console
	client  publicv1.ClustersClient
}

func clusterCmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "cluster ID|NAME",
		Aliases:               []string{"clusters"},
		Short:                 clusterShortHelp,
		Long:                  clusterLongHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.ExactArgs(1),
		RunE:                  runner.run,
	}
	flags := result.Flags()
	flags.StringVar(
		&runner.args.nodeSet,
		"node-set",
		"",
		nodeSetFlagHelp,
	)
	_ = result.MarkFlagRequired("node-set")
	flags.Int32Var(
		&runner.args.size,
		"size",
		0,
		sizeFlagHelp,
	)
	_ = result.MarkFlagRequired("size")
	return result
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	if c.args.size < 0 {
		return fmt.Errorf("--size must be >= 0, got %d", c.args.size)
	}

	ctx := cmd.Context()

	c.console = terminal.ConsoleFromContext(ctx)

	err := c.console.AddTemplates(templatesFS, "templates")
	if err != nil {
		return fmt.Errorf("failed to load templates: %w", err)
	}

	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	c.client = publicv1.NewClustersClient(conn)

	return scaleCluster(ctx, c.client, c.console, args[0], c.args.nodeSet, c.args.size)
}

func scaleCluster(
	ctx context.Context,
	client publicv1.ClustersClient,
	console *terminal.Console,
	clusterRef string,
	nodeSetName string,
	newSize int32,
) error {
	cluster, err := lookup.Find(clusterRef, "cluster", func(filter string, limit int32) ([]*publicv1.Cluster, error) {
		resp, err := client.List(ctx, publicv1.ClustersListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to list clusters: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	nodeSets := cluster.GetSpec().GetNodeSets()
	if len(nodeSets) == 0 {
		console.Render(ctx, "no_node_sets.txt", map[string]any{
			"ClusterId": cluster.GetId(),
		})
		return fmt.Errorf("cluster %q has no node sets; the scale command only supports CaaS clusters", cluster.GetId())
	}

	nodeSet, ok := nodeSets[nodeSetName]
	if !ok {
		console.Render(ctx, "node_set_not_found.txt", map[string]any{
			"ClusterId": cluster.GetId(),
			"NodeSet":   nodeSetName,
			"NodeSets":  nodeSets,
		})
		return fmt.Errorf("node set %q not found in cluster %q", nodeSetName, cluster.GetId())
	}

	previousSize := nodeSet.GetSize()

	updated := proto.Clone(cluster).(*publicv1.Cluster)
	updated.GetSpec().GetNodeSets()[nodeSetName].SetSize(newSize)

	_, err = client.Update(ctx, publicv1.ClustersUpdateRequest_builder{
		Object: updated,
		UpdateMask: &fieldmaskpb.FieldMask{
			Paths: []string{"spec.node_sets"},
		},
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to scale cluster: %w", err)
	}

	console.Infof(ctx, "Scaled cluster '%s' node set '%s': %d → %d\n",
		cluster.GetId(), nodeSetName, previousSize, newSize)
	console.Infof(ctx, "Run 'osac describe cluster %s' to monitor reconciliation.\n", cluster.GetId())

	return nil
}

const shortHelp = `Scale CaaS cluster node sets`

const longHelp = `
Set the size of a node set in a CaaS cluster.

To set three worker nodes in a cluster:

{{ bt 3 }}shell
{{ binary }} scale cluster my-cluster --node-set workers --size 3
{{ bt 3 }}

The command is non-interactive and suitable for scripting. To scale relative to
the current size, read the current value first:

{{ bt 3 }}shell
current=$({{ binary }} get clusters my-cluster -o yaml | yq '.spec.node_sets.workers.size')
{{ binary }} scale cluster my-cluster --node-set workers --size $((current + 1))
{{ bt 3 }}

After scaling, the cluster transitions to {{ bt }}PROGRESSING{{ bt }} until the change is applied.
Use {{ bt }}{{ binary }} describe cluster my-cluster{{ bt }} to monitor progress.
`

const clusterShortHelp = `Scale a CaaS cluster node set to an absolute size`

const clusterLongHelp = `
Set the absolute size of a named node set in a CaaS cluster.

To scale the {{ bt }}workers{{ bt }} node set to 3 nodes:

{{ bt 3 }}shell
{{ binary }} scale cluster my-cluster --node-set workers --size 3
{{ bt 3 }}

The cluster ID or name can be used interchangeably. The {{ bt }}--node-set{{ bt }} value must match
the key of one of the node sets in {{ bt }}spec.node_sets{{ bt }}. Use {{ bt }}{{ binary }} get clusters my-cluster -o
yaml{{ bt }} to see the available node set names.
`

const nodeSetFlagHelp = `
_NAME_ - Name of the node set to scale. Must match a key in {{ bt }}spec.node_sets{{ bt }}.
`

const sizeFlagHelp = `
_N_ - Absolute target number of nodes. Must be >= 0.
`
