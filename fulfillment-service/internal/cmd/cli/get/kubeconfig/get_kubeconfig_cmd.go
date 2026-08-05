/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package kubeconfig

import (
	"embed"
	"fmt"
	"log/slog"
	"slices"
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/exit"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

//go:embed templates
var templatesFS embed.FS

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "kubeconfig [FLAG...] ID|NAME",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		RunE:                  runner.run,
	}
	flags := result.Flags()
	flags.StringVar(
		&runner.args.key,
		"cluster",
		"",
		clusterFlagHelp,
	)
	flags.MarkDeprecated("cluster", "use positional argument instead.\n")
	return result
}

type runnerContext struct {
	logger  *slog.Logger
	flags   *pflag.FlagSet
	console *terminal.Console
	conn    *grpc.ClientConn
	args    struct {
		key string
	}
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	var err error

	// Get the context:
	ctx := cmd.Context()

	// Get the logger and flags:
	c.logger = logging.LoggerFromContext(ctx)
	c.console = terminal.ConsoleFromContext(ctx)

	// Load the templates for the console messages:
	err = c.console.AddTemplates(templatesFS, "templates")
	if err != nil {
		return fmt.Errorf("failed to load templates: %w", err)
	}

	// Get the flags:
	c.flags = cmd.Flags()

	// Get the configuration:
	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	// Create the gRPC connection from the configuration:
	c.conn, err = cfg.Connect(ctx, c.flags)
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer c.conn.Close()

	// Get the cluster name or identifier: from the flag if provided, otherwise from the first positional argument.
	key := c.args.key
	if key == "" && len(args) > 0 {
		key = args[0]
	}

	// Check the flags:
	if key == "" {
		c.console.Render(ctx, "no_key.txt", nil)
		return exit.Error(1)
	}

	// Try to find a cluster that has an identifier or name matching the given identifier:
	client := publicv1.NewClustersClient(c.conn)
	listFilter := fmt.Sprintf(
		"this.id == %[1]q || this.metadata.name == %[1]q",
		key,
	)
	listResponse, err := client.List(ctx, publicv1.ClustersListRequest_builder{
		Filter: proto.String(listFilter),
		Limit:  proto.Int32(10),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to list clusters: %w", err)
	}
	total := listResponse.GetTotal()
	clusters := listResponse.GetItems()
	var cluster *publicv1.Cluster
	switch total {
	case 0:
		c.console.Render(ctx, "no_match.txt", map[string]any{
			"Key": key,
		})
		return exit.Error(1)
	case 1:
		cluster = clusters[0]
	default:
		ids := make([]string, len(clusters))
		for i, cluster := range clusters {
			ids[i] = cluster.GetId()
		}
		sort.Strings(ids)
		ids = slices.Compact(ids)
		c.console.Render(ctx, "multiple_matches.txt", map[string]any{
			"Ids":   ids,
			"Key":   key,
			"Total": total,
		})
		return exit.Error(1)
	}

	// Get the kubeconfig:
	getKubeconfigResponse, err := client.GetKubeconfig(ctx, publicv1.ClustersGetKubeconfigRequest_builder{
		Id: cluster.GetId(),
	}.Build())
	if err != nil {
		return err
	}
	kcText := getKubeconfigResponse.GetKubeconfig()
	var kcYaml any
	err = yaml.Unmarshal([]byte(kcText), &kcYaml)
	if err != nil {
		c.logger.ErrorContext(
			ctx,
			"Failed to unmarshal kubeconfig",
			slog.Any("error", err),
		)
		c.console.Infof(ctx, "%s\n", kcText)
	} else {
		c.console.RenderYaml(ctx, kcYaml)
	}

	return nil
}

const shortHelp = `Get kubeconfig`

const longHelp = `
Get kubeconfig for a cluster.
`

const clusterFlagHelp = `
_NAME|ID_ - Name or identifier of the cluster.

This flag is deprecated. Use a positional argument instead.
`
