/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package tenant

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	outputText = "text"
	outputJSON = "json"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "tenant [FLAG...] NAME",
		Aliases:               []string{string(proto.MessageName((*privatev1.Tenant)(nil)))},
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.ExactArgs(1),
		RunE:                  runner.run,
	}
	flags := result.Flags()
	flags.StringArrayVar(
		&runner.args.domains,
		"domain",
		nil,
		domainFlagHelp,
	)
	flags.StringVarP(
		&runner.args.output,
		"output",
		"o",
		outputText,
		outputFlagHelp,
	)
	return result
}

type runnerContext struct {
	args struct {
		domains []string
		output  string
	}
	console  *terminal.Console
	settings *config.Settings
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	c.console = terminal.ConsoleFromContext(ctx)
	c.settings = config.SettingsFromContext(ctx)
	if !c.settings.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	if c.args.output != outputText && c.args.output != outputJSON {
		return fmt.Errorf("unknown output format '%s', should be '%s' or '%s'",
			c.args.output, outputText, outputJSON)
	}
	if args[0] == "" {
		return fmt.Errorf("name is required")
	}

	conn, err := c.settings.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			logging.LoggerFromContext(ctx).ErrorContext(
				ctx,
				"Failed to close gRPC connection",
				slog.Any("error", closeErr),
			)
		}
	}()

	client := privatev1.NewTenantsClient(conn)
	response, err := client.Create(ctx, privatev1.TenantsCreateRequest_builder{
		Object: buildTenant(args[0], c.args.domains),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to create tenant: %w", err)
	}

	tenant := response.GetObject()
	if c.args.output == outputJSON {
		return renderJSON(ctx, c.console, tenant)
	}

	c.console.Infof(ctx, "Created tenant '%s' (ID: %s).\n",
		tenant.GetMetadata().GetName(), tenant.GetId())
	return nil
}

func buildTenant(name string, domains []string) *privatev1.Tenant {
	return privatev1.Tenant_builder{
		Metadata: privatev1.Metadata_builder{
			Name: name,
		}.Build(),
		Spec: privatev1.TenantSpec_builder{
			Domains: domains,
		}.Build(),
	}.Build()
}

const shortHelp = `Create a tenant`

const longHelp = `
Create a tenant without preparing a manifest.

To create a tenant with one e-mail domain:

{{ bt 3 }}shell
{{ binary }} create tenant example --domain example.com
{{ bt 3 }}

To create a tenant with multiple domains and JSON output:

{{ bt 3 }}shell
{{ binary }} create tenant example \
  --domain example.com \
  --domain corp.example.com \
  --output json
{{ bt 3 }}

The JSON output uses protobuf JSON encoding, including RFC 3339 timestamps and enum names.
`

const domainFlagHelp = `
_DOMAIN_ - E-mail domain associated with the tenant. This flag can be repeated.
`

const outputFlagHelp = `
_FORMAT_ - Output format: {{ bt }}text{{ bt }} or {{ bt }}json{{ bt }}.
`

func renderJSON(ctx context.Context, console *terminal.Console, message proto.Message) error {
	data, err := protojson.MarshalOptions{
		UseProtoNames: true,
		Multiline:     true,
	}.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to encode tenant: %w", err)
	}
	data = append(data, '\n')
	if _, err := console.Write(data); err != nil {
		return fmt.Errorf("tenant created, but failed to write JSON output: %w", err)
	}
	return nil
}
