/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package upgrade

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/clusterversions"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/reflection"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func clusterCmd() *cobra.Command {
	runner := &runnerContext{
		connect: func(ctx context.Context, flags *pflag.FlagSet, cfg *config.Settings) (*grpc.ClientConn, error) {
			return cfg.Connect(ctx, flags)
		},
	}
	result := &cobra.Command{
		Use:                   "cluster ID|NAME",
		Aliases:               []string{"clusters"},
		Short:                 clusterShortHelp,
		Long:                  clusterLongHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.ExactArgs(1),
		RunE:                  runner.run,
	}
	result.Flags().StringVar(
		&runner.args.version,
		"version",
		"",
		versionFlagHelp,
	)
	if err := result.MarkFlagRequired("version"); err != nil {
		panic(fmt.Sprintf("failed to mark version flag as required: %v", err))
	}
	return result
}

type runnerContext struct {
	args struct {
		version string
	}
	console *terminal.Console
	logger  *slog.Logger
	connect func(ctx context.Context, flags *pflag.FlagSet, cfg *config.Settings) (*grpc.ClientConn, error)
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) (err error) {
	ctx := cmd.Context()

	c.console = terminal.ConsoleFromContext(ctx)
	c.logger = logging.LoggerFromContext(ctx)

	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	conn, err := c.connect(ctx, cmd.Flags(), cfg)
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("failed to close gRPC connection: %w", closeErr))
		}
	}()

	helper, err := reflection.NewHelper().
		SetLogger(c.logger).
		SetConnection(conn).
		AddPackages(cfg.Packages()).
		SetTenantFunc(config.TenantFromContext).
		SetProjectFunc(config.ProjectFromContext).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create reflection tool: %w", err)
	}
	c.console.SetHelper(helper)

	return upgradeCluster(
		ctx,
		publicv1.NewClustersClient(conn),
		publicv1.NewClusterVersionsClient(conn),
		c.console,
		args[0],
		c.args.version,
	)
}

func upgradeCluster(
	ctx context.Context,
	clusters publicv1.ClustersClient,
	versions publicv1.ClusterVersionsClient,
	console *terminal.Console,
	clusterRef, versionRef string,
) error {
	cluster, err := lookup.Find(clusterRef, "cluster", func(filter string, limit int32) ([]*publicv1.Cluster, error) {
		response, err := clusters.List(ctx, publicv1.ClustersListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to list clusters: %w", err)
		}
		return response.GetItems(), nil
	})
	if err != nil {
		return err
	}

	version, err := clusterversions.Find(ctx, versions, console, versionRef)
	if err != nil {
		return err
	}

	updated := publicv1.Cluster_builder{
		Id: cluster.GetId(),
		Spec: publicv1.ClusterSpec_builder{
			Version: publicv1.ClusterVersionReference_builder{
				Name: version.GetMetadata().GetName(),
			}.Build(),
		}.Build(),
	}.Build()

	_, err = clusters.Update(ctx, publicv1.ClustersUpdateRequest_builder{
		Object: updated,
		UpdateMask: &fieldmaskpb.FieldMask{
			Paths: []string{"spec.version"},
		},
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to upgrade cluster: %w", err)
	}

	console.Infof(ctx, "Upgraded cluster '%s' to version '%s'.\n",
		cluster.GetId(), version.GetSpec().GetVersion())
	return nil
}

const clusterShortHelp = `Upgrade a cluster to a target version`

const clusterLongHelp = `
Upgrade the control plane and every participating node set with one request.

To upgrade a cluster to version 4.17.0:

{{ bt 3 }}shell
{{ binary }} upgrade cluster my-cluster --version 4.17.0
{{ bt 3 }}

The cluster ID or name can be used interchangeably. The version can be either a
catalog version name or an OpenShift semantic version; OSAC resolves it to the
catalog version reference before sending the update.
`

const versionFlagHelp = `
_VERSION_ - Target cluster version. This can be a catalog version name or an OpenShift semantic version.
`
