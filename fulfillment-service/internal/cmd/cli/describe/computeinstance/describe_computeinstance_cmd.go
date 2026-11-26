/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package computeinstance

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// Cmd creates the command to describe a compute instance.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "computeinstance [FLAG...] ID|NAME",
		Aliases:               []string{"computeinstances"},
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

	client := publicv1.NewComputeInstancesClient(conn)

	matched, err := lookup.FindScoped(ctx, ref, "compute instance", func(filter string, limit int32) ([]*publicv1.ComputeInstance, error) {
		resp, err := client.List(ctx, publicv1.ComputeInstancesListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to describe compute instance: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	return renderComputeInstance(c.console, matched)
}

func renderComputeInstance(w io.Writer, ci *publicv1.ComputeInstance) error {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	catalogItem := "-"
	if ci.Spec != nil {
		if ref := ci.Spec.GetCatalogItem(); ref != nil {
			if ref.GetName() != "" {
				catalogItem = ref.GetName()
			} else if ref.GetId() != "" {
				catalogItem = ref.GetId()
			}
		}
	}
	state := "-"
	if ci.Status != nil {
		state = ci.Status.State.String()
		state = strings.TrimPrefix(state, "COMPUTE_INSTANCE_STATE_")
	}
	if _, err := fmt.Fprintf(writer, "ID:\t%s\n", ci.Id); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "Catalog Item:\t%s\n", catalogItem); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "State:\t%s\n", state); err != nil {
		return err
	}
	if ci.Status != nil && ci.Status.GetLastRestartedAt() != nil {
		if _, err := fmt.Fprintf(writer, "Last Restarted At:\t%s\n", ci.Status.GetLastRestartedAt().AsTime().Format(time.RFC3339)); err != nil {
			return err
		}
	}
	for _, condition := range ci.GetStatus().GetConditions() {
		if condition.GetType() == publicv1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_PROVISIONED &&
			condition.GetStatus() == publicv1.ConditionStatus_CONDITION_STATUS_FALSE &&
			(condition.GetReason() == "RetryScheduled" || condition.GetReason() == "RetryRunning") {
			if _, err := fmt.Fprintln(writer, "Provisioning:\tRetrying"); err != nil {
				return err
			}
			break
		}
	}
	if failure := ci.GetStatus().GetLastProvisioningFailure(); failure != nil {
		reason := fmt.Sprintf("UNKNOWN:%d", failure.GetReason())
		if name, ok := publicv1.ComputeInstanceProvisioningFailureReason_name[int32(failure.GetReason())]; ok {
			reason = strings.TrimPrefix(name, "COMPUTE_INSTANCE_PROVISIONING_FAILURE_REASON_")
		}
		if _, err := fmt.Fprintf(writer, "Last Provisioning Failure Reason:\t%s\n", reason); err != nil {
			return err
		}
		if message := failure.GetMessage(); message != "" {
			if _, err := fmt.Fprintf(writer, "Last Provisioning Failure Message:\t%s\n", message); err != nil {
				return err
			}
		}
		if attempt := failure.GetAttempt(); attempt > 0 {
			if _, err := fmt.Fprintf(writer, "Last Provisioning Failure Attempt:\t%d\n", attempt); err != nil {
				return err
			}
		}
		if observed := failure.GetObservedAt(); observed != nil && observed.CheckValid() == nil {
			if _, err := fmt.Fprintf(writer, "Last Provisioning Failure Observed At:\t%s\n", observed.AsTime().UTC().Format(time.RFC3339)); err != nil {
				return err
			}
		}
	}
	return writer.Flush()
}

const shortHelp = `Describe a compute instance`

const longHelp = `
Describe a compute instance, including provisioning retry and the latest failure when available.

{{ bt 3 }}shell
{{ binary }} describe computeinstance my-vm
{{ bt 3 }}

During retry backoff ({{ bt }}RetryScheduled{{ bt }}), the additional rows can look like:

{{ bt 3 }}text
Provisioning:                         Retrying
Last Provisioning Failure Reason:     PROVIDER_REPORTED
Last Provisioning Failure Message:    Provisioning failed.
Last Provisioning Failure Attempt:    2
Last Provisioning Failure Observed At: 2026-01-02T03:04:05Z
{{ bt 3 }}

During a running retry ({{ bt }}RetryRunning{{ bt }}), the same latest-failure rows remain
visible alongside:

{{ bt 3 }}text
Provisioning: Retrying
{{ bt 3 }}

A terminal failure ({{ bt }}ProvisioningFailed{{ bt }}) retains its failure details,
but has no provisioning retry row. For example:

{{ bt 3 }}text
Last Provisioning Failure Reason:  PROVIDER_REPORTED
Last Provisioning Failure Message: Provisioning failed.
{{ bt 3 }}

Missing or legacy failure fields add no failure rows. A legacy scheduled retry
can still show {{ bt }}Provisioning: Retrying{{ bt }} without failure details.
An instance with neither retry nor failure fields shows only its existing details, for example:

{{ bt 3 }}text
ID:           my-vm
Catalog Item: small
State:        STARTING
{{ bt 3 }}

After the provisioning job succeeds, retry and failure rows disappear even while
{{ bt }}WaitingForVM{{ bt }} or {{ bt }}ProvisioningStorage{{ bt }} still reports infrastructure progress.
The existing state remains independent, for example:

{{ bt 3 }}text
ID:           my-vm
Catalog Item: small
State:        STARTING
{{ bt 3 }}

Failure presence and message text alone never imply retry. Failure timestamps use RFC 3339.
`
