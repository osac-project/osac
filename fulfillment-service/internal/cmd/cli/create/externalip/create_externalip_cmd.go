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
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/create/metautil"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/wait"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
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
	flags.StringVar(
		&runner.args.computeInstance,
		"compute-instance",
		"",
		computeInstanceFlagHelp,
	)
	flags.DurationVar(
		&runner.args.timeout,
		"timeout",
		5*time.Minute,
		timeoutFlagHelp,
	)
	result.MarkFlagRequired("pool") //nolint:errcheck
	return result
}

type runnerContext struct {
	args struct {
		name            string
		pool            string
		computeInstance string
		timeout         time.Duration
	}
	logger       *slog.Logger
	console      *terminal.Console
	settings     *config.Settings
	poolsClient  publicv1.ExternalIPPoolsClient
	eipsClient   publicv1.ExternalIPsClient
	ciClient     publicv1.ComputeInstancesClient
	attachClient publicv1.ExternalIPAttachmentsClient
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

	c.poolsClient = publicv1.NewExternalIPPoolsClient(conn)
	c.eipsClient = publicv1.NewExternalIPsClient(conn)
	c.ciClient = publicv1.NewComputeInstancesClient(conn)
	c.attachClient = publicv1.NewExternalIPAttachmentsClient(conn)

	return c.createExternalIP(ctx)
}

// createExternalIP performs the full create (and optional attach) workflow using the injected clients.
// It is a separate method so that unit tests can inject mock clients without a real gRPC connection.
func (c *runnerContext) createExternalIP(ctx context.Context) error {
	// Step 1: resolve pool and create the ExternalIP.
	pool, err := lookup.Find(c.args.pool, "external IP pool", func(filter string, limit int32) ([]*publicv1.ExternalIPPool, error) {
		resp, err := c.poolsClient.List(ctx, publicv1.ExternalIPPoolsListRequest_builder{
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

	createResp, err := c.eipsClient.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
		Object: publicv1.ExternalIP_builder{
			Metadata: metautil.ForCreate(ctx, c.args.name).Build(),
			Spec: publicv1.ExternalIPSpec_builder{
				Pool: &publicv1.ExternalIPPoolReference{Id: pool.GetId()},
			}.Build(),
		}.Build(),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to create external IP: %w", err)
	}
	eip := createResp.GetObject()

	// Without --compute-instance: done.
	if c.args.computeInstance == "" {
		c.console.Infof(ctx, "Created external IP '%s' (ID: %s).\n",
			eip.GetMetadata().GetName(), eip.GetId())
		return nil
	}

	// One-shot path: wait for ALLOCATED, then attach.

	// Step 2: poll until ALLOCATED.
	c.console.Infof(ctx, "Created external IP '%s' (ID: %s). Waiting for ALLOCATED...\n",
		eip.GetMetadata().GetName(), eip.GetId())

	eip, err = wait.Poll(ctx, wait.Options[*publicv1.ExternalIP]{
		Fetch: func(ctx context.Context) (*publicv1.ExternalIP, error) {
			resp, err := c.eipsClient.Get(ctx, publicv1.ExternalIPsGetRequest_builder{
				Id: eip.GetId(),
			}.Build())
			if err != nil {
				return nil, fmt.Errorf("failed to poll external IP: %w", err)
			}
			return resp.GetObject(), nil
		},
		StateOf: func(r *publicv1.ExternalIP) string {
			return r.GetStatus().GetState().String()
		},
		TargetState: publicv1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED.String(),
		FailureStates: []string{
			publicv1.ExternalIPState_EXTERNAL_IP_STATE_FAILED.String(),
			publicv1.ExternalIPState_EXTERNAL_IP_STATE_DELETING.String(),
		},
		Timeout: c.args.timeout,
		OnProgress: func(r *publicv1.ExternalIP) {
			c.console.Infof(ctx, "  external IP '%s': %s\n",
				r.GetMetadata().GetName(), r.GetStatus().GetState().String())
		},
	})
	if err != nil {
		return fmt.Errorf("external IP '%s' did not reach ALLOCATED: %w", eip.GetMetadata().GetName(), err)
	}

	// Step 3: resolve compute instance.
	ci, err := lookup.Find(c.args.computeInstance, "compute instance", func(filter string, limit int32) ([]*publicv1.ComputeInstance, error) {
		resp, err := c.ciClient.List(ctx, publicv1.ComputeInstancesListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to resolve compute instance %q: %w", c.args.computeInstance, err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	// Step 4: create the attachment.
	attachResp, err := c.attachClient.Create(ctx, publicv1.ExternalIPAttachmentsCreateRequest_builder{
		Object: publicv1.ExternalIPAttachment_builder{
			Metadata: metautil.ForCreate(ctx, "").Build(),
			Spec: publicv1.ExternalIPAttachmentSpec_builder{
				ExternalIp:      &publicv1.ExternalIPLocalReference{Id: eip.GetId()},
				ComputeInstance: &publicv1.ComputeInstanceLocalReference{Id: ci.GetId()},
			}.Build(),
		}.Build(),
	}.Build())
	if err != nil {
		return fmt.Errorf(
			"external IP '%s' is ALLOCATED but attachment to compute instance '%s' failed: %w",
			eip.GetMetadata().GetName(), ci.GetMetadata().GetName(), err,
		)
	}

	c.console.Infof(ctx,
		"Created external IP '%s' (ID: %s) and attached to compute instance '%s' (attachment ID: %s).\n",
		eip.GetMetadata().GetName(), eip.GetId(),
		ci.GetMetadata().GetName(), attachResp.GetObject().GetId(),
	)
	return nil
}

const shortHelp = `Create an external IP`

const longHelp = `
Allocate an external IP address from an ExternalIPPool.

The {{ bt }}--pool{{ bt }} flag is required and specifies the ExternalIPPool to allocate from.

When {{ bt }}--compute-instance{{ bt }} is provided, the command performs a one-shot attach:
  1. Creates the ExternalIP.
  2. Waits for it to reach ALLOCATED state.
  3. Resolves the compute instance by name or ID.
  4. Creates an ExternalIPAttachment linking the two resources.

Examples:

{{ bt 3 }}shell
# Create an external IP from a specific pool
{{ binary }} create externalip --name my-ip --pool pool-abc123

# Create and immediately attach to a compute instance
{{ binary }} create externalip --name my-ip --pool pool-abc123 --compute-instance my-vm

# One-shot attach with a custom wait timeout
{{ binary }} create externalip --name my-ip --pool pool-abc123 --compute-instance my-vm --timeout 10m
{{ bt 3 }}
`

const nameFlagHelp = `
_NAME_ - Name of the external IP.
`

const poolFlagHelp = `
_ID|NAME_ - ID or name of the parent ExternalIPPool to allocate the address from. Required.
`

const computeInstanceFlagHelp = `
_ID|NAME_ - ID or name of the ComputeInstance to attach the new ExternalIP to once it reaches
ALLOCATED state. When set, the command blocks until the attachment is created or the timeout
expires.
`

const timeoutFlagHelp = `
_DURATION_ - Maximum time to wait for the ExternalIP to reach ALLOCATED state when
{{ bt }}--compute-instance{{ bt }} is provided (e.g. "5m", "10m30s"). Defaults to 5 minutes.
`
