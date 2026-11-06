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

	// Resolve the target compute instance (if any) before creating
	// anything, so a mistyped name or an unusable instance fails
	// before an address is allocated from the pool.
	var ci *publicv1.ComputeInstance
	if c.args.computeInstance != "" {
		ci, err = lookup.Find(c.args.computeInstance, "compute instance", func(filter string, limit int32) ([]*publicv1.ComputeInstance, error) {
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

	// wait.Poll returns the zero value on error, so keep the
	// created object for error reporting instead of overwriting it.
	allocated, err := wait.Poll(ctx, wait.Options[*publicv1.ExternalIP]{
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
		return fmt.Errorf("external IP '%s' (ID: %s) did not reach ALLOCATED: %w",
			eip.GetMetadata().GetName(), eip.GetId(), err)
	}
	eip = allocated

	// Step 3: the attachment API requires the target instance to be
	// RUNNING, READY, and to have a network attachment address. The
	// instance may reach RUNNING before its network is up, so wait
	// for full readiness before creating the attachment.
	ci, err = c.waitForComputeInstanceReady(ctx, ci)
	if err != nil {
		return err
	}

	// Step 4: create the attachment. The attachment name is required
	// by the API; derive a stable, descriptive name from the
	// external IP.
	attachName := eip.GetMetadata().GetName()
	if attachName == "" {
		attachName = fmt.Sprintf("eip-%s", eip.GetId())
	}
	attachResp, err := c.attachClient.Create(ctx, publicv1.ExternalIPAttachmentsCreateRequest_builder{
		Object: publicv1.ExternalIPAttachment_builder{
			Metadata: metautil.ForCreate(ctx, attachName+"-attachment").Build(),
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

// waitForComputeInstanceReady polls until the compute instance satisfies
// the attachment API's preconditions: RUNNING state, READY condition, and
// a reported network attachment address. RUNNING can precede network
// readiness, so waiting here avoids allocating an external IP that cannot
// be attached yet.
func (c *runnerContext) waitForComputeInstanceReady(ctx context.Context, ci *publicv1.ComputeInstance) (*publicv1.ComputeInstance, error) {
	ready, err := wait.Poll(ctx, wait.Options[*publicv1.ComputeInstance]{
		Fetch: func(ctx context.Context) (*publicv1.ComputeInstance, error) {
			resp, err := c.ciClient.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: ci.GetId(),
			}.Build())
			if err != nil {
				return nil, fmt.Errorf("failed to poll compute instance: %w", err)
			}
			return resp.GetObject(), nil
		},
		StateOf: func(r *publicv1.ComputeInstance) string {
			if computeInstanceAttachable(r) {
				return "ATTACHABLE"
			}
			return r.GetStatus().GetState().String()
		},
		TargetState: "ATTACHABLE",
		FailureStates: []string{
			publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED.String(),
			publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING.String(),
			publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED.String(),
		},
		Timeout: c.args.timeout,
		OnProgress: func(r *publicv1.ComputeInstance) {
			c.console.Infof(ctx, "  compute instance '%s': %s\n",
				r.GetMetadata().GetName(), r.GetStatus().GetState().String())
		},
	})
	if err != nil {
		return nil, fmt.Errorf("compute instance '%s' (ID: %s) did not become ready for attachment: %w",
			ci.GetMetadata().GetName(), ci.GetId(), err)
	}
	return ready, nil
}

// computeInstanceAttachable reports whether the instance satisfies the
// attachment API's preconditions: RUNNING state, READY condition set to
// TRUE, and at least one reported network attachment address.
func computeInstanceAttachable(ci *publicv1.ComputeInstance) bool {
	if ci.GetStatus().GetState() != publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING {
		return false
	}
	ready := false
	for _, condition := range ci.GetStatus().GetConditions() {
		if condition.GetType() == publicv1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_READY &&
			condition.GetStatus() == publicv1.ConditionStatus_CONDITION_STATUS_TRUE {
			ready = true
			break
		}
	}
	for _, status := range ci.GetStatus().GetComputeNetworkAttachmentStatuses() {
		if status.GetIpAddress() != "" {
			return ready
		}
	}
	return false
}

const shortHelp = `Create an external IP`

const longHelp = `
Allocate an external IP address from an ExternalIPPool.

The {{ bt }}--pool{{ bt }} flag is required and specifies the ExternalIPPool to allocate from.

When {{ bt }}--compute-instance{{ bt }} is provided, the command performs a one-shot attach:
  1. Resolves the compute instance by name or ID.
  2. Creates the ExternalIP.
  3. Waits for it to reach ALLOCATED state.
  4. Waits for the compute instance to be RUNNING, READY, and to have
     a network attachment address.
  5. Creates an ExternalIPAttachment linking the two resources.

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
