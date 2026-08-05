/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package externalipattachment

import (
	"fmt"
	"log/slog"
	"strings"

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
		Use:                   "externalipattachment [FLAG...]",
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
		&runner.args.externalIP,
		"externalip",
		"",
		externalIPFlagHelp,
	)
	flags.StringVar(
		&runner.args.computeInstance,
		"compute-instance",
		"",
		computeInstanceFlagHelp,
	)
	flags.StringVar(
		&runner.args.cluster,
		"cluster",
		"",
		clusterFlagHelp,
	)
	flags.StringVar(
		&runner.args.baremetalInstance,
		"baremetal-instance",
		"",
		baremetalInstanceFlagHelp,
	)
	flags.StringVar(
		&runner.args.targetEndpoint,
		"target-endpoint",
		"",
		targetEndpointFlagHelp,
	)
	result.MarkFlagRequired("externalip") //nolint:errcheck
	result.MarkFlagsMutuallyExclusive("compute-instance", "cluster", "baremetal-instance")
	return result
}

type runnerContext struct {
	args struct {
		name              string
		externalIP        string
		computeInstance   string
		cluster           string
		baremetalInstance string
		targetEndpoint    string
	}
	logger  *slog.Logger
	console *terminal.Console
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
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

	if c.args.computeInstance == "" && c.args.cluster == "" && c.args.baremetalInstance == "" {
		return fmt.Errorf("exactly one target is required: --compute-instance, --cluster, or --baremetal-instance")
	}

	eipClient := publicv1.NewExternalIPsClient(conn)
	eip, err := lookup.Find(c.args.externalIP, "external IP", func(filter string, limit int32) ([]*publicv1.ExternalIP, error) {
		resp, err := eipClient.List(ctx, publicv1.ExternalIPsListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to resolve external IP %q: %w", c.args.externalIP, err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	spec := publicv1.ExternalIPAttachmentSpec_builder{
		ExternalIp: eip.GetId(),
	}

	var targetDesc string

	switch {
	case c.args.computeInstance != "":
		ciClient := publicv1.NewComputeInstancesClient(conn)
		ci, err := lookup.Find(c.args.computeInstance, "compute instance", func(filter string, limit int32) ([]*publicv1.ComputeInstance, error) {
			resp, err := ciClient.List(ctx, publicv1.ComputeInstancesListRequest_builder{
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
		spec.ComputeInstance = proto.String(ci.GetId())
		targetDesc = fmt.Sprintf("compute instance '%s'", ci.GetId())

	case c.args.cluster != "":
		clClient := publicv1.NewClustersClient(conn)
		cl, err := lookup.Find(c.args.cluster, "cluster", func(filter string, limit int32) ([]*publicv1.Cluster, error) {
			resp, err := clClient.List(ctx, publicv1.ClustersListRequest_builder{
				Filter: proto.String(filter),
				Limit:  proto.Int32(limit),
			}.Build())
			if err != nil {
				return nil, fmt.Errorf("failed to resolve cluster %q: %w", c.args.cluster, err)
			}
			return resp.GetItems(), nil
		})
		if err != nil {
			return err
		}
		spec.Cluster = proto.String(cl.GetId())
		targetDesc = fmt.Sprintf("cluster '%s'", cl.GetId())

		if c.args.targetEndpoint != "" {
			endpoint, err := parseTargetEndpoint(c.args.targetEndpoint)
			if err != nil {
				return err
			}
			spec.TargetEndpoint = endpoint
		}

	case c.args.baremetalInstance != "":
		bmClient := publicv1.NewBareMetalInstancesClient(conn)
		bm, err := lookup.Find(c.args.baremetalInstance, "bare metal instance", func(filter string, limit int32) ([]*publicv1.BareMetalInstance, error) {
			resp, err := bmClient.List(ctx, publicv1.BareMetalInstancesListRequest_builder{
				Filter: proto.String(filter),
				Limit:  proto.Int32(limit),
			}.Build())
			if err != nil {
				return nil, fmt.Errorf("failed to resolve bare metal instance %q: %w", c.args.baremetalInstance, err)
			}
			return resp.GetItems(), nil
		})
		if err != nil {
			return err
		}
		spec.BaremetalInstance = proto.String(bm.GetId())
		targetDesc = fmt.Sprintf("bare metal instance '%s'", bm.GetId())
	}

	attachClient := publicv1.NewExternalIPAttachmentsClient(conn)

	attachment := publicv1.ExternalIPAttachment_builder{
		Metadata: publicv1.Metadata_builder{
			Name:   c.args.name,
			Tenant: cfg.Tenant(),
		}.Build(),
		Spec: spec.Build(),
	}.Build()

	response, err := attachClient.Create(ctx, publicv1.ExternalIPAttachmentsCreateRequest_builder{
		Object: attachment,
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to create external IP attachment: %w", err)
	}

	c.console.Infof(ctx, "Created external IP attachment '%s' (external IP '%s' -> %s).\n",
		response.GetObject().GetId(), eip.GetId(), targetDesc)

	return nil
}

func parseTargetEndpoint(value string) (publicv1.ExternalIPAttachmentEndpoint, error) {
	switch strings.ToLower(value) {
	case "api":
		return publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API, nil
	case "ingress":
		return publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS, nil
	default:
		return publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_UNSPECIFIED,
			fmt.Errorf("invalid target endpoint %q: must be 'api' or 'ingress'", value)
	}
}

const shortHelp = `Attach an external IP to a resource.`

const longHelp = `
Create an external IP attachment to bind an external IP to a target resource.

The {{ bt }}--externalip{{ bt }} flag is required. Exactly one target flag must be provided:
{{ bt }}--compute-instance{{ bt }}, {{ bt }}--cluster{{ bt }}, or {{ bt }}--baremetal-instance{{ bt }}.

When targeting a cluster, use {{ bt }}--target-endpoint{{ bt }} to specify whether the external IP
should be routed to the cluster's API server ({{ bt }}api{{ bt }}) or ingress controller
({{ bt }}ingress{{ bt }}).

Examples:

{{ bt 3 }}shell
# Attach to a compute instance
{{ binary }} create externalipattachment --externalip my-ip --compute-instance my-vm --name vm-att

# Attach to a cluster API server
{{ binary }} create externalipattachment --externalip my-ip --cluster my-cluster --target-endpoint api --name api-att

# Attach to a cluster ingress
{{ binary }} create externalipattachment --externalip my-ip --cluster my-cluster --target-endpoint ingress --name ingress-att

# Attach to a bare metal instance
{{ binary }} create externalipattachment --externalip my-ip --baremetal-instance my-server --name bm-att
{{ bt 3 }}
`

const nameFlagHelp = `
_NAME_ - Name of the external IP attachment.
`

const externalIPFlagHelp = `
_ID|NAME_ - Identifier or name of the external IP to attach. Required.
`

const computeInstanceFlagHelp = `
_ID|NAME_ - Identifier or name of the compute instance to attach the external IP to.
Mutually exclusive with {{ bt }}--cluster{{ bt }} and {{ bt }}--baremetal-instance{{ bt }}.
`

const clusterFlagHelp = `
_ID|NAME_ - Identifier or name of the cluster to attach the external IP to.
Use {{ bt }}--target-endpoint{{ bt }} to specify the cluster endpoint (api or ingress).
Mutually exclusive with {{ bt }}--compute-instance{{ bt }} and {{ bt }}--baremetal-instance{{ bt }}.
`

const baremetalInstanceFlagHelp = `
_ID|NAME_ - Identifier or name of the bare metal instance to attach the external IP to.
Mutually exclusive with {{ bt }}--compute-instance{{ bt }} and {{ bt }}--cluster{{ bt }}.
`

const targetEndpointFlagHelp = `
_api|ingress_ - Cluster endpoint to route the external IP to. Only applicable when targeting a
cluster with {{ bt }}--cluster{{ bt }}.
`
