/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package volumes

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func createCmd() *cobra.Command {
	runner := &createRunner{}
	result := &cobra.Command{
		Use:                   "create",
		Short:                 createShortHelp,
		Long:                  createLongHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}
	flags := result.Flags()
	flags.StringVarP(
		&runner.name,
		"name",
		"n",
		"",
		createNameFlagHelp,
	)
	flags.StringVar(
		&runner.storageTier,
		"storage-tier",
		"",
		createStorageTierFlagHelp,
	)
	flags.Int64Var(
		&runner.sizeGib,
		"size-gib",
		0,
		createSizeGibFlagHelp,
	)
	flags.StringVar(
		&runner.accessMode,
		"access-mode",
		"ReadWriteOnce",
		createAccessModeFlagHelp,
	)
	flags.StringVar(
		&runner.project,
		"project",
		"",
		createProjectFlagHelp,
	)
	return result
}

type createRunner struct {
	console     *terminal.Console
	name        string
	storageTier string
	sizeGib     int64
	accessMode  string
	project     string
}

func (c *createRunner) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	c.console = terminal.ConsoleFromContext(ctx)

	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	// Validate required fields:
	if c.name == "" {
		return fmt.Errorf("name is required")
	}
	if c.storageTier == "" {
		return fmt.Errorf("storage-tier is required")
	}
	if c.sizeGib <= 0 {
		return fmt.Errorf("size-gib must be greater than zero")
	}

	// Parse access mode:
	accessMode, err := parseAccessMode(c.accessMode)
	if err != nil {
		return err
	}

	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	client := publicv1.NewVolumesClient(conn)

	// Build metadata:
	metadataBuilder := publicv1.Metadata_builder{
		Name: c.name,
	}
	if c.project != "" {
		metadataBuilder.Project = c.project
	}

	// Build volume:
	volume := publicv1.Volume_builder{
		Metadata: metadataBuilder.Build(),
		Spec: publicv1.VolumeSpec_builder{
			StorageTier: c.storageTier,
			SizeGib:     c.sizeGib,
			AccessMode:  accessMode,
		}.Build(),
	}.Build()

	response, err := client.Create(ctx, publicv1.VolumesCreateRequest_builder{
		Object: volume,
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to create volume: %w", err)
	}

	c.console.Infof(ctx, "Created volume '%s'.\n", response.GetObject().GetId())

	return nil
}

// parseAccessMode converts a user-friendly access mode name to its proto enum value.
// It accepts both Kubernetes-style names (ReadWriteOnce) and abbreviations (RWO).
func parseAccessMode(value string) (publicv1.VolumeAccessMode, error) {
	switch strings.ToLower(value) {
	case "readwriteonce", "rwo":
		return publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE, nil
	case "readonlymany", "rox":
		return publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_ONLY_MANY, nil
	case "readwritemany", "rwx":
		return publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_MANY, nil
	case "readwriteoncepod", "rwop":
		return publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE_POD, nil
	case "":
		return 0, fmt.Errorf(
			"access-mode is required, must be one of: ReadWriteOnce, ReadOnlyMany, ReadWriteMany, ReadWriteOncePod",
		)
	default:
		return 0, fmt.Errorf(
			"invalid access-mode '%s', must be one of: ReadWriteOnce (RWO), ReadOnlyMany (ROX), "+
				"ReadWriteMany (RWX), ReadWriteOncePod (RWOP)",
			value,
		)
	}
}

const createShortHelp = `Create a volume`

const createLongHelp = `
Create a standalone volume.

A volume represents block storage provisioned on a backend storage array through the OSAC storage
control plane. Volumes are scoped to the current tenant.

To create a volume:

{{ bt 3 }}shell
{{ binary }} volumes create \
  --name my-volume \
  --storage-tier gold \
  --size-gib 100 \
  --access-mode ReadWriteOnce
{{ bt 3 }}

To create a volume in a specific project:

{{ bt 3 }}shell
{{ binary }} volumes create \
  --name my-volume \
  --storage-tier gold \
  --size-gib 100 \
  --access-mode ReadWriteOnce \
  --project team-a.frontend
{{ bt 3 }}

Required fields: name, storage-tier, size-gib

Access modes follow Kubernetes conventions:
  - {{ bt }}ReadWriteOnce{{ bt }} ({{ bt }}RWO{{ bt }}): read-write by a single node (default)
  - {{ bt }}ReadOnlyMany{{ bt }} ({{ bt }}ROX{{ bt }}): read-only by many nodes
  - {{ bt }}ReadWriteMany{{ bt }} ({{ bt }}RWX{{ bt }}): read-write by many nodes
  - {{ bt }}ReadWriteOncePod{{ bt }} ({{ bt }}RWOP{{ bt }}): read-write by a single pod
`

const createNameFlagHelp = `
_NAME_ - Name of the volume. Must be a unique, human-readable identifier.
`

const createStorageTierFlagHelp = `
_TIER_ - Name of the storage tier that determines which backend and protocol serve this volume.
`

const createSizeGibFlagHelp = `
_SIZE_ - Requested storage capacity in gibibytes (GiB). Must be greater than zero.
`

const createAccessModeFlagHelp = `
_MODE_ - Kubernetes access mode for the volume. One of {{ bt }}ReadWriteOnce{{ bt }} ({{ bt }}RWO{{ bt }}),
{{ bt }}ReadOnlyMany{{ bt }} ({{ bt }}ROX{{ bt }}), {{ bt }}ReadWriteMany{{ bt }} ({{ bt }}RWX{{ bt }}),
{{ bt }}ReadWriteOncePod{{ bt }} ({{ bt }}RWOP{{ bt }}). Defaults to {{ bt }}ReadWriteOnce{{ bt }}.
`

const createProjectFlagHelp = `
_PROJECT_ - Project to assign the volume to.
`
