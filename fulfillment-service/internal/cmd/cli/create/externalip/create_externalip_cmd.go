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
	"log/slog"

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
		Use:                   "externalip [FLAG...]",
		Aliases:               []string{string(proto.MessageName((*publicv1.ExternalIP)(nil)))},
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}
	flags := result.Flags()
	flags.StringVarP(
		&runner.args.name,
		"name",
		"n",
		"",
		nameFlagHelp,
	)
	flags.StringVar(
		&runner.args.pool,
		"pool",
		"",
		poolFlagHelp,
	)
	result.MarkFlagRequired("pool") //nolint:errcheck
	return result
}

type runnerContext struct {
	args struct {
		name string
		pool string
	}
	logger   *slog.Logger
	console  *terminal.Console
	settings *config.Settings
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	c.logger = logging.LoggerFromContext(ctx)
	c.console = terminal.ConsoleFromContext(ctx)

	c.settings = config.SettingsFromContext(ctx)
	if !c.settings.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	conn, err := c.settings.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	poolClient := publicv1.NewExternalIPPoolsClient(conn)
	pool, err := lookup.Find(c.args.pool, "external IP pool", func(filter string, limit int32) ([]*publicv1.ExternalIPPool, error) {
		resp, err := poolClient.List(ctx, publicv1.ExternalIPPoolsListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to resolve external IP pool %q: %w", c.args.pool, err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	client := publicv1.NewExternalIPsClient(conn)

	externalIP := publicv1.ExternalIP_builder{
		Metadata: publicv1.Metadata_builder{
			Name:   c.args.name,
			Tenant: c.settings.Tenant(),
		}.Build(),
		Spec: publicv1.ExternalIPSpec_builder{
			Pool: pool.GetId(),
		}.Build(),
	}.Build()

	response, err := client.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{Object: externalIP}.Build())
	if err != nil {
		return fmt.Errorf("failed to create external IP: %w", err)
	}

	c.console.Infof(ctx, "Created external IP '%s' (ID: %s).\n", response.Object.GetMetadata().GetName(), response.Object.GetId())

	return nil
}

const shortHelp = `Create an external IP.`

const longHelp = `
Allocate an external IP address from an ExternalIPPool.

The {{ bt }}--pool{{ bt }} flag is required and specifies the ExternalIPPool to allocate from.

Examples:

{{ bt 3 }}shell
# Create an external IP from a specific pool
{{ binary }} create externalip --name my-ip --pool pool-abc123

# Create an external IP using pool name
{{ binary }} create externalip --name my-ip --pool external-pool-1
{{ bt 3 }}
`

const nameFlagHelp = `
_NAME_ - Name of the external IP.
`

const poolFlagHelp = `
_ID|NAME_ - ID or name of the parent ExternalIPPool to allocate the address from. Required.
`
