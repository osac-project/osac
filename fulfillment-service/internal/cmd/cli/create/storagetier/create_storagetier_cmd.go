/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package storagetier

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

// Cmd creates the command to create a storage tier.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "storagetier",
		Aliases:               []string{string(proto.MessageName((*privatev1.StorageTier)(nil)))},
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
		&runner.description,
		"description",
		"",
		descriptionFlagHelp,
	)
	flags.StringVar(
		&runner.backendID,
		"backend-id",
		"",
		backendIDFlagHelp,
	)
	flags.StringVar(
		&runner.protocol,
		"protocol",
		"",
		protocolFlagHelp,
	)
	flags.Int32Var(
		&runner.maxReadBandwidthMbs,
		"max-read-bandwidth-mbs",
		0,
		maxReadBandwidthFlagHelp,
	)
	flags.Int32Var(
		&runner.maxWriteBandwidthMbs,
		"max-write-bandwidth-mbs",
		0,
		maxWriteBandwidthFlagHelp,
	)
	flags.Int64Var(
		&runner.quotaGiB,
		"quota-gib",
		0,
		quotaGibFlagHelp,
	)
	flags.BoolVar(
		&runner.encryptionEnabled,
		"encryption-enabled",
		false,
		encryptionEnabledFlagHelp,
	)
	return result
}

type runnerContext struct {
	console              *terminal.Console
	name                 string
	description          string
	backendID            string
	protocol             string
	maxReadBandwidthMbs  int32
	maxWriteBandwidthMbs int32
	quotaGiB             int64
	encryptionEnabled    bool
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
	if c.backendID == "" {
		return fmt.Errorf("backend-id is required")
	}

	protocolValue, err := parseProtocol(c.protocol)
	if err != nil {
		return err
	}

	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	client := privatev1.NewStorageTiersClient(conn)

	storageTier := privatev1.StorageTier_builder{
		Metadata: privatev1.Metadata_builder{
			Name: c.name,
		}.Build(),
		Spec: privatev1.StorageTierSpec_builder{
			Description: c.description,
			Backends: []*privatev1.BackendAssociation{
				privatev1.BackendAssociation_builder{
					BackendId:            c.backendID,
					Protocol:             protocolValue,
					MaxReadBandwidthMbs:  c.maxReadBandwidthMbs,
					MaxWriteBandwidthMbs: c.maxWriteBandwidthMbs,
					QuotaGib:             c.quotaGiB,
					EncryptionEnabled:    c.encryptionEnabled,
				}.Build(),
			},
		}.Build(),
	}.Build()

	response, err := client.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
		Object: storageTier,
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to create storage tier: %w", err)
	}

	c.console.Infof(ctx, "Created storage tier '%s'.\n", response.GetObject().GetId())

	return nil
}

func parseProtocol(value string) (privatev1.StorageProtocol, error) {
	switch strings.ToUpper(value) {
	case "NFS":
		return privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS, nil
	case "BLOCK":
		return privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK, nil
	case "":
		return privatev1.StorageProtocol_STORAGE_PROTOCOL_UNSPECIFIED, nil
	default:
		return 0, fmt.Errorf("invalid protocol '%s', must be NFS or BLOCK", value)
	}
}

const shortHelp = `Create a storage tier.`

const longHelp = `
Create a storage tier.

A storage tier groups a storage backend with protocol, bandwidth, quota, and encryption settings.
Storage tiers are managed by Cloud Provider Admins via the private API and are consumed indirectly
by tenants when provisioning storage-enabled resources.

To create a storage tier:

{{ bt 3 }}shell
{{ binary }} create storagetier --name gold \
  --backend-id 019f46a9-b580-7990-9384-648ba80eec1c \
  --protocol NFS \
  --max-read-bandwidth-mbs 2000 \
  --max-write-bandwidth-mbs 1000 \
  --quota-gib 4096 \
  --encryption-enabled
{{ bt 3 }}
`

const nameFlagHelp = `
_NAME_ - Name of the storage tier. Must be a unique, human-readable identifier
(e.g., {{ bt }}gold{{ bt }}, {{ bt }}standard{{ bt }}).
`

const descriptionFlagHelp = `
_DESCRIPTION_ - Human-readable description of the storage tier.
`

const backendIDFlagHelp = `
_ID_ - Identifier of the storage backend to associate with this tier.
`

const protocolFlagHelp = `
_PROTOCOL_ - Storage protocol: {{ bt }}NFS{{ bt }} or {{ bt }}BLOCK{{ bt }}.
`

const maxReadBandwidthFlagHelp = `
_MBS_ - Maximum read bandwidth in megabytes per second.
`

const maxWriteBandwidthFlagHelp = `
_MBS_ - Maximum write bandwidth in megabytes per second.
`

const quotaGibFlagHelp = `
_GIB_ - Storage quota in gibibytes (GiB).
`

const encryptionEnabledFlagHelp = `
_[BOOLEAN]_ - Whether data at rest is encrypted on this backend.
`
