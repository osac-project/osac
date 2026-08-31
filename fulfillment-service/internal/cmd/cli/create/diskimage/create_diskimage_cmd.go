/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package diskimage

import (
	"fmt"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/create/fieldutil"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

var sourceTypeMap = map[string]publicv1.SourceType{
	"registry": publicv1.SourceType_SOURCE_TYPE_REGISTRY,
}

var guestOsFamilyMap = map[string]publicv1.GuestOSFamily{
	"linux":   publicv1.GuestOSFamily_GUEST_OS_FAMILY_LINUX,
	"windows": publicv1.GuestOSFamily_GUEST_OS_FAMILY_WINDOWS,
}

var architectureMap = map[string]publicv1.Architecture{
	"amd64": publicv1.Architecture_ARCHITECTURE_AMD64,
	"arm64": publicv1.Architecture_ARCHITECTURE_ARM64,
	"s390x": publicv1.Architecture_ARCHITECTURE_S390X,
}

// Cmd creates the command to create a disk image.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "diskimage",
		Aliases:               []string{string(proto.MessageName((*publicv1.DiskImage)(nil)))},
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
		&runner.sourceType,
		"source-type",
		"registry",
		sourceTypeFlagHelp,
	)
	flags.StringVar(
		&runner.sourceRef,
		"source-ref",
		"",
		sourceRefFlagHelp,
	)
	flags.StringVar(
		&runner.guestOsFamily,
		"guest-os-family",
		"linux",
		guestOsFamilyFlagHelp,
	)
	flags.StringArrayVar(
		&runner.architecture,
		"architecture",
		nil,
		architectureFlagHelp,
	)
	return result
}

type runnerContext struct {
	console       *terminal.Console
	name          string
	sourceType    string
	sourceRef     string
	guestOsFamily string
	architecture  []string
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
	if c.sourceRef == "" {
		return fmt.Errorf("source-ref is required")
	}
	if len(c.architecture) == 0 {
		return fmt.Errorf("at least one --architecture is required")
	}

	sourceType, err := fieldutil.ParseEnum(c.sourceType, sourceTypeMap, "source-type")
	if err != nil {
		return err
	}
	guestOsFamily, err := fieldutil.ParseEnum(c.guestOsFamily, guestOsFamilyMap, "guest-os-family")
	if err != nil {
		return err
	}
	architectures := make([]publicv1.Architecture, len(c.architecture))
	for i, a := range c.architecture {
		arch, err := fieldutil.ParseEnum(a, architectureMap, "architecture")
		if err != nil {
			return err
		}
		architectures[i] = arch
	}

	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	client := publicv1.NewDiskImagesClient(conn)

	diskImage := publicv1.DiskImage_builder{
		Id: c.name,
		Metadata: publicv1.Metadata_builder{
			Name: c.name,
		}.Build(),
		Spec: publicv1.DiskImageSpec_builder{
			SourceType:    sourceType,
			SourceRef:     c.sourceRef,
			GuestOsFamily: guestOsFamily,
			Architecture:  architectures,
		}.Build(),
	}.Build()

	response, err := client.Create(ctx, publicv1.DiskImagesCreateRequest_builder{
		Object: diskImage,
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to create disk image: %w", err)
	}

	c.console.Infof(ctx, "Created disk image '%s'.\n", response.GetObject().GetId())

	return nil
}

const shortHelp = `Create a disk image`

const longHelp = `
Create a disk image.

A disk image defines a reference to an OCI container disk artifact with curated metadata — guest OS
family, architecture, and lifecycle state. Disk images provide a discoverable catalog for VM
provisioning.

To create a disk image:

{{ bt 3 }}shell
{{ binary }} create diskimage --name fedora-41 --source-ref quay.io/containerdisks/fedora:41 --architecture amd64
{{ bt 3 }}

To create a multi-architecture image:

{{ bt 3 }}shell
{{ binary }} create diskimage --name fedora-41 --source-ref quay.io/containerdisks/fedora:41 --architecture amd64 --architecture arm64
{{ bt 3 }}

To create a Windows image:

{{ bt 3 }}shell
{{ binary }} create diskimage --name windows-2022 --source-ref quay.io/containerdisks/windows:2022 --guest-os-family windows --architecture amd64
{{ bt 3 }}
`

const nameFlagHelp = `
_NAME_ - Name of the disk image. Must be a unique, human-readable identifier
(e.g., {{ bt }}fedora-41{{ bt }}).
`

const sourceTypeFlagHelp = `
_SOURCE_TYPE_ - Type of the disk image source. Valid values: {{ bt }}registry{{ bt }}. Defaults to
{{ bt }}registry{{ bt }}.
`

const sourceRefFlagHelp = `
_SOURCE_REF_ - Reference to the disk image source (e.g.,
{{ bt }}quay.io/containerdisks/fedora:41{{ bt }}).
`

const guestOsFamilyFlagHelp = `
_GUEST_OS_FAMILY_ - Guest operating system family. Valid values: {{ bt }}linux{{ bt }},
{{ bt }}windows{{ bt }}. Defaults to {{ bt }}linux{{ bt }}.
`

const architectureFlagHelp = `
_ARCHITECTURE_ - CPU architecture supported by this disk image. Repeatable. Valid values:
{{ bt }}amd64{{ bt }}, {{ bt }}arm64{{ bt }}, {{ bt }}s390x{{ bt }}.
`
