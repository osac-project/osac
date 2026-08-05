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
	"os"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/terminal"
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
		return fmt.Errorf("kubeconfig file is required")
	}
	if c.namespace == "" {
		return fmt.Errorf("namespace name is required")
	}

	// Create the gRPC connection from the configuration:
	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	// Create the client:
	client := privatev1.NewHubsClient(conn)

	// Read the kubeconfig file:
	kubeconfig, err := os.ReadFile(c.kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to read kubeconfig file '%s': %w", c.kubeconfig, err)
	}

	// Prepare the hub:
	hub := privatev1.Hub_builder{
		Id: c.id,
		Spec: privatev1.HubSpec_builder{
			Kubeconfig: kubeconfig,
			Namespace:  c.namespace,
		}.Build(),
	}.Build()

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

const shortHelp = `Create a hub.`

const longHelp = `
Create a hub.
`

const idFlagHelp = `
_ID_ - Unique identifier of the hub.
`

const kubeconfigFlagHelp = `
_FILE_ - Kubeconfig file containing the details to connect to the Kubernetes
API.
`

const namespaceFlagHelp = `
_NAMESPACE_ - Namespace where cluster orders will be created.
`
