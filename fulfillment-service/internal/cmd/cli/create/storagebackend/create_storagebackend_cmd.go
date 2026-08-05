/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package storagebackend

import (
	"fmt"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

// Cmd creates the command to create a storage backend.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "storagebackend",
		Aliases:               []string{string(proto.MessageName((*privatev1.StorageBackend)(nil)))},
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
		&runner.provider,
		"provider",
		"",
		providerFlagHelp,
	)
	flags.StringVar(
		&runner.endpoint,
		"endpoint",
		"",
		endpointFlagHelp,
	)
	flags.StringVar(
		&runner.username,
		"username",
		"",
		usernameFlagHelp,
	)
	flags.StringVar(
		&runner.password,
		"password",
		"",
		passwordFlagHelp,
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
	provider    string
	endpoint    string
	username    string
	password    string
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
	if c.provider == "" {
		return fmt.Errorf("provider is required")
	}
	if c.endpoint == "" {
		return fmt.Errorf("endpoint is required")
	}
	if c.username == "" {
		return fmt.Errorf("username is required")
	}
	if c.password == "" {
		return fmt.Errorf("password is required")
	}

	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	client := privatev1.NewStorageBackendsClient(conn)

	storageBackend := privatev1.StorageBackend_builder{
		Metadata: privatev1.Metadata_builder{
			Name: c.name,
		}.Build(),
		Spec: privatev1.StorageBackendSpec_builder{
			Provider:    c.provider,
			Description: c.description,
			Endpoint:    c.endpoint,
			Credentials: privatev1.StorageBackendCredentials_builder{
				Username: c.username,
				Password: c.password,
			}.Build(),
		}.Build(),
	}.Build()

	response, err := client.Create(ctx, privatev1.StorageBackendsCreateRequest_builder{
		Object: storageBackend,
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to create storage backend: %w", err)
	}

	c.console.Infof(ctx, "Created storage backend '%s'.\n", response.GetObject().GetId())

	return nil
}

const shortHelp = `Create a storage backend.`

const longHelp = `
Create a storage backend.

A storage backend represents a registered storage array (e.g., VAST, Ceph, Pure) with its management
endpoint and credentials. Storage backends are managed by Cloud Provider Admins via the private API.
Tenants interact with storage indirectly through storage tiers.

To create a storage backend:

{{ bt 3 }}shell
{{ binary }} create storagebackend --name vast-prod \
  --provider vast \
  --endpoint https://storage.example.com:8443 \
  --username admin \
  --password secret
{{ bt 3 }}
`

const nameFlagHelp = `
_NAME_ - Name of the storage backend. Must be a unique, human-readable identifier
(e.g., {{ bt }}vast-prod{{ bt }}).
`

const providerFlagHelp = `
_PROVIDER_ - Storage provider identifier (e.g., {{ bt }}vast{{ bt }}, {{ bt }}ceph{{ bt }},
{{ bt }}pure{{ bt }}). Immutable after creation.
`

const endpointFlagHelp = `
_URL_ - Management endpoint for the storage array (e.g., {{ bt }}https://storage.example.com:8443{{ bt }}).
`

const usernameFlagHelp = `
_USERNAME_ - Username for storage management API authentication.
`

const passwordFlagHelp = `
_PASSWORD_ - Password for storage management API authentication.
`

const descriptionFlagHelp = `
_DESCRIPTION_ - Human-readable description of the storage backend.
`
