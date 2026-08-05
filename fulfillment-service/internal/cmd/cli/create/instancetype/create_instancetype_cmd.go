/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package instancetype

import (
	"fmt"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

// Cmd creates the command to create an instance type.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "instancetype",
		Aliases:               []string{string(proto.MessageName((*privatev1.InstanceType)(nil)))},
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
	flags.Int32Var(
		&runner.cores,
		"cores",
		0,
		coresFlagHelp,
	)
	flags.Int32Var(
		&runner.memoryGiB,
		"memory-gib",
		0,
		memoryGibFlagHelp,
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
	cores       int32
	memoryGiB   int32
	description string
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
	if c.name == "" {
		return fmt.Errorf("name is required")
	}
	if c.cores <= 0 {
		return fmt.Errorf("cores must be greater than zero")
	}
	if c.memoryGiB <= 0 {
		return fmt.Errorf("memory-gib must be greater than zero")
	}

	// Create the gRPC connection from the configuration:
	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	// Create the client:
	client := privatev1.NewInstanceTypesClient(conn)

	// Prepare the instance type:
	instanceType := privatev1.InstanceType_builder{
		Id: c.name,
		Metadata: privatev1.Metadata_builder{
			Name: c.name,
		}.Build(),
		Spec: privatev1.InstanceTypeSpec_builder{
			Cores:       c.cores,
			MemoryGib:   c.memoryGiB,
			Description: c.description,
		}.Build(),
	}.Build()

	// Create the instance type:
	response, err := client.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
		Object: instanceType,
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to create instance type: %w", err)
	}

	// Display the result:
	c.console.Infof(ctx, "Created instance type '%s'.\n", response.GetObject().GetId())

	return nil
}

const shortHelp = `Create an instance type.`

const longHelp = `
Create an instance type.

An instance type defines a pre-configured compute bundle (CPU cores, memory) that can be referenced
by name when creating compute instances. Instance types are managed by Cloud Provider Admins.

To create an instance type:

{{ bt 3 }}shell
{{ binary }} create instancetype --name standard-4-16 --cores 4 --memory-gib 16 --description 'Balanced compute'
{{ bt 3 }}
`

const nameFlagHelp = `
_NAME_ - Name of the instance type. Must be a unique, human-readable identifier
(e.g., {{ bt }}standard-4-16{{ bt }}).
`

const coresFlagHelp = `
_CORES_ - Number of CPU cores for this instance type. Must be greater than zero.
`

const memoryGibFlagHelp = `
_MEMORY_ - Amount of memory in GiB for this instance type. Must be greater than zero.
`

const descriptionFlagHelp = `
_DESCRIPTION_ - Human friendly description of the instance type.
`
