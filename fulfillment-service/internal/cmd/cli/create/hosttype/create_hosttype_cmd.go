/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package hosttype

import (
	"fmt"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "hosttype",
		Aliases:               []string{string(proto.MessageName((*privatev1.HostType)(nil)))},
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}
	flags := result.Flags()
	flags.StringVar(
		&runner.name,
		"name",
		"",
		nameFlagHelp,
	)
	flags.StringVar(
		&runner.title,
		"title",
		"",
		titleFlagHelp,
	)
	flags.StringVar(
		&runner.description,
		"description",
		"",
		descriptionFlagHelp,
	)
	return result
}

type runnerContext struct {
	console     *terminal.Console
	name        string
	title       string
	description string
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	c.console = terminal.ConsoleFromContext(ctx)

	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	if c.name == "" {
		return fmt.Errorf("name is required")
	}

	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	client := privatev1.NewHostTypesClient(conn)

	hostType := privatev1.HostType_builder{
		Id: c.name,
		Metadata: privatev1.Metadata_builder{
			Name: c.name,
		}.Build(),
		Title:       c.title,
		Description: c.description,
	}.Build()

	response, err := client.Create(ctx, privatev1.HostTypesCreateRequest_builder{
		Object: hostType,
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to create host type: %w", err)
	}

	c.console.Infof(ctx, "Created host type '%s'.\n", response.GetObject().GetId())

	return nil
}

const shortHelp = `Create a host type`

const longHelp = `
Create a host type.

A host type describes a set of hosts that share characteristics such as CPU, memory, and GPU
configuration. For example, {{ bt }}acme_1tb{{ bt }} for hosts with 1 TiB of RAM, or
{{ bt }}ibm_mi300x{{ bt }} for hosts with MI300X GPUs.

Host types must be registered before creating cluster orders that reference them.

To create a host type:

{{ bt 3 }}shell
{{ binary }} create hosttype --name dgx --title 'NVIDIA DGX' --description 'DGX H100 host'
{{ bt 3 }}

To add network interfaces to a host type, use {{ bt }}osac create -f{{ bt }} with a YAML or JSON
file instead.
`

const nameFlagHelp = `
_NAME_ - Name of the host type. Must be a unique identifier (e.g., {{ bt }}dgx{{ bt }},
{{ bt }}fc430{{ bt }}).
`

const titleFlagHelp = `
_TITLE_ - Human friendly short description of the host type, suitable for displaying in a single
line on a UI or CLI.
`

const descriptionFlagHelp = `
_DESCRIPTION_ - Human friendly long description of the host type, using Markdown format.
`
