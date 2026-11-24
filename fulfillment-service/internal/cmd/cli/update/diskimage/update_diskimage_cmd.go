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
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/create/fieldutil"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// Cmd creates the command to update a disk image's pull secret.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "diskimage ID|NAME",
		Aliases:               []string{"diskimages"},
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.ExactArgs(1),
		RunE:                  runner.run,
	}
	flags := result.Flags()
	flags.StringVar(
		&runner.pullSecret,
		"pull-secret",
		"",
		pullSecretFlagHelp,
	)
	flags.BoolVar(
		&runner.clearPullSecret,
		"clear-pull-secret",
		false,
		clearPullSecretFlagHelp,
	)
	result.MarkFlagsMutuallyExclusive("pull-secret", "clear-pull-secret")
	result.MarkFlagsOneRequired("pull-secret", "clear-pull-secret")
	return result
}

type runnerContext struct {
	console         *terminal.Console
	pullSecret      string
	clearPullSecret bool
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	c.console = terminal.ConsoleFromContext(ctx)

	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	var pullSecretRef *publicv1.SecretReference
	if !c.clearPullSecret {
		var err error
		// Always parse when --pull-secret is selected. An empty value must not
		// clear the reference; only --clear-pull-secret may do that.
		pullSecretRef, err = fieldutil.ParsePullSecretFlag(c.pullSecret)
		if err != nil {
			return err
		}
	}

	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	client := publicv1.NewDiskImagesClient(conn)
	image, err := lookup.Find(args[0], "disk image", func(filter string, limit int32) ([]*publicv1.DiskImage, error) {
		resp, err := client.List(ctx, publicv1.DiskImagesListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to list disk images: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	updated := publicv1.DiskImage_builder{
		Id: image.GetId(),
		Spec: publicv1.DiskImageSpec_builder{
			PullSecret: pullSecretRef,
		}.Build(),
	}.Build()

	_, err = client.Update(ctx, publicv1.DiskImagesUpdateRequest_builder{
		Object: updated,
		UpdateMask: &fieldmaskpb.FieldMask{
			Paths: []string{"spec.pull_secret"},
		},
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to update disk image: %w", err)
	}

	if c.clearPullSecret {
		c.console.Infof(ctx, "Cleared pull secret on disk image '%s'.\n", image.GetId())
	} else {
		c.console.Infof(ctx, "Updated pull secret on disk image '%s'.\n", image.GetId())
	}
	return nil
}

const shortHelp = `Update a disk image pull secret`

const longHelp = `
Update the optional pull Secret on a disk image.

Set or change the pull Secret:

{{ bt 3 }}shell
{{ binary }} update diskimage fedora-41 --pull-secret registry-creds
{{ bt 3 }}

Clear the pull Secret (public image):

{{ bt 3 }}shell
{{ binary }} update diskimage fedora-41 --clear-pull-secret
{{ bt 3 }}
`

const pullSecretFlagHelp = `
_NAME_ or {{ bt }}name=<name>[,project=<project>][,shared=true]{{ bt }} - Pull
Secret of type {{ bt }}pull-secret{{ bt }} used when downloading the image from a
private registry.
`

const clearPullSecretFlagHelp = `
Clear the disk image pull Secret so the image needs no registry credentials.
`
