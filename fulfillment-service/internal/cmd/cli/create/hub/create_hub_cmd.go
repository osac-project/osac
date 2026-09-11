/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package hub

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
		Use:     "hub",
		Aliases: []string{string(proto.MessageName((*privatev1.Hub)(nil)))},
		Short:   shortHelp,
		Long:    longHelp,
		Args:    cobra.NoArgs,
		RunE:    runner.run,
	}
	flags := result.Flags()
	flags.StringVar(
		&runner.id,
		"id",
		"",
		idFlagHelp,
	)
	flags.StringVar(
		&runner.kubeconfig,
		"kubeconfig",
		"",
		kubeconfigFlagHelp,
	)
	flags.StringVar(
		&runner.namespace,
		"namespace",
		"",
		namespaceFlagHelp,
	)
	return result
}

type runnerContext struct {
	console    *terminal.Console
	id         string
	kubeconfig string
	namespace  string
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	// Get the context:
	ctx := cmd.Context()

	// Get the console:
	c.console = terminal.ConsoleFromContext(ctx)

	// Get the configuration:
	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	// Check the parameters:
	if c.id == "" {
		return fmt.Errorf("identifier is required")
	}
	if c.namespace == "" {
		return fmt.Errorf("namespace name is required")
	}
	if c.kubeconfig == "" {
		return fmt.Errorf("kubeconfig secret name is required")
	}

	// Create the gRPC connection from the configuration:
	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	// Create the client:
	client := privatev1.NewHubsClient(conn)

	// Prepare the hub:
	hub := c.hub()

	// Create the hub:
	response, err := client.Create(ctx, privatev1.HubsCreateRequest_builder{
		Object: hub,
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to create hub: %w", err)
	}

	// Display the result:
	hub = response.Object
	c.console.Infof(ctx, "Created hub `%s`.\n", hub.GetId())

	return nil
}

func (c *runnerContext) hub() *privatev1.Hub {
	return privatev1.Hub_builder{
		Id: c.id,
		Spec: privatev1.HubSpec_builder{
			KubeconfigSecret: privatev1.SecretLocalReference_builder{
				Name: c.kubeconfig,
			}.Build(),
			Namespace: c.namespace,
		}.Build(),
	}.Build()
}

const shortHelp = `Create a hub`

const longHelp = `
Create a hub.

Create a Secret containing the kubeconfig, then reference it when creating the hub:

{{ bt 3 }}shell
{{ binary }} --tenant shared create secret --name hub-kubeconfig --from-file=kubeconfig=/path/to/kubeconfig
{{ binary }} create hub --id my-hub --kubeconfig hub-kubeconfig --namespace osac
{{ bt 3 }}
`

const idFlagHelp = `
_ID_ - Unique identifier of the hub.
`

const kubeconfigFlagHelp = `
_NAME_ - Name of a Secret resource containing the kubeconfig in its
{{ bt }}kubeconfig{{ bt }} data key. The Secret must belong to the
{{ bt }}shared{{ bt }} tenant and can only be managed by a platform administrator.
See also {{ bt }}osac create secret{{ bt }}.
`

const namespaceFlagHelp = `
_NAMESPACE_ - Namespace where cluster orders will be created.
`
