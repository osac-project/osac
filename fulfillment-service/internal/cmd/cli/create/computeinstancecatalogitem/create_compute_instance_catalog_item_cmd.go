/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package computeinstancecatalogitem

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "computeinstancecatalogitem [FLAG...]",
		Aliases:               []string{string(proto.MessageName((*publicv1.ComputeInstanceCatalogItem)(nil)))},
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
		&runner.args.title,
		"title",
		"",
		titleFlagHelp,
	)
	flags.StringVar(
		&runner.args.description,
		"description",
		"",
		descriptionFlagHelp,
	)
	flags.StringVarP(
		&runner.args.template,
		"template",
		"t",
		"",
		templateFlagHelp,
	)
	flags.BoolVar(
		&runner.args.published,
		"published",
		false,
		publishedFlagHelp,
	)
	result.MarkFlagRequired("template") //nolint:errcheck
	return result
}

type runnerContext struct {
	args struct {
		name        string
		title       string
		description string
		template    string
		published   bool
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

	templatesClient := publicv1.NewComputeInstanceTemplatesClient(conn)
	template, err := lookup.Find(c.args.template, "compute instance template",
		func(filter string, limit int32) ([]*publicv1.ComputeInstanceTemplate, error) {
			response, err := templatesClient.List(ctx, publicv1.ComputeInstanceTemplatesListRequest_builder{
				Filter: proto.String(filter),
				Limit:  proto.Int32(limit),
			}.Build())
			if err != nil {
				return nil, fmt.Errorf("failed to list templates: %w", err)
			}
			return response.GetItems(), nil
		})
	if err != nil {
		return err
	}

	client := publicv1.NewComputeInstanceCatalogItemsClient(conn)

	catalogItem := publicv1.ComputeInstanceCatalogItem_builder{
		Metadata: publicv1.Metadata_builder{
			Name: c.args.name,
		}.Build(),
		Title:       c.args.title,
		Description: c.args.description,
		Template:    &publicv1.ComputeInstanceTemplateReference{Id: template.GetId()},
		Published:   c.args.published,
	}.Build()

	response, err := client.Create(ctx, publicv1.ComputeInstanceCatalogItemsCreateRequest_builder{
		Object: catalogItem,
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to create compute instance catalog item: %w", err)
	}

	catalogItem = response.Object
	c.console.Infof(ctx, "Created compute instance catalog item '%s'.\n", catalogItem.GetId())

	return nil
}

const shortHelp = `Create a compute instance catalog item`

const longHelp = `
Create a compute instance catalog item. A catalog item defines a curated
compute instance offering that references an underlying compute instance
template.

To configure typed field policies, use {{ bt }}osac create -f{{ bt }} with a
YAML file instead.
`

const nameFlagHelp = `
_NAME_ - Name of the compute instance catalog item.
`

const titleFlagHelp = `
_TITLE_ - Human-friendly short description, suitable for displaying in a
single line.
`

const descriptionFlagHelp = `
_TEXT_ - Human-friendly long description in Markdown format.
`

const templateFlagHelp = `
_ID_OR_NAME_ - Identifier or name of the underlying compute instance template.
If a name matches more than one visible template, use its identifier.
`

const publishedFlagHelp = `
_[BOOLEAN]_ - Whether this catalog item is published.
`
