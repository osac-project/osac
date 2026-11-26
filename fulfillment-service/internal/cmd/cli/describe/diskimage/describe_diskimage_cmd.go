/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package diskimage

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// Cmd creates the command to describe a disk image.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "diskimage [FLAG...] ID|NAME",
		Aliases:               []string{"diskimages"},
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.ExactArgs(1),
		RunE:                  runner.run,
	}
	return result
}

type runnerContext struct {
	console *terminal.Console
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ref := args[0]

	ctx := cmd.Context()

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

	client := publicv1.NewDiskImagesClient(conn)

	matched, err := lookup.Find(ref, "disk image", func(filter string, limit int32) ([]*publicv1.DiskImage, error) {
		resp, err := client.List(ctx, publicv1.DiskImagesListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to describe disk image: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	return renderDiskImage(c.console, matched)
}

func renderDiskImage(w io.Writer, di *publicv1.DiskImage) error {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	if _, err := fmt.Fprintf(writer, "Name:\t%s\n", di.GetMetadata().GetName()); err != nil {
		return err
	}

	spec := di.GetSpec()
	if spec != nil {
		sourceType := strings.TrimPrefix(spec.GetSourceType().String(), "SOURCE_TYPE_")
		if _, err := fmt.Fprintf(writer, "Source Type:\t%s\n", sourceType); err != nil {
			return err
		}

		if _, err := fmt.Fprintf(writer, "Source Ref:\t%s\n", spec.GetSourceRef()); err != nil {
			return err
		}

		guestOsFamily := strings.TrimPrefix(spec.GetGuestOsFamily().String(), "GUEST_OS_FAMILY_")
		if _, err := fmt.Fprintf(writer, "Guest OS Family:\t%s\n", guestOsFamily); err != nil {
			return err
		}

		archStrings := make([]string, len(spec.GetArchitecture()))
		for i, a := range spec.GetArchitecture() {
			archStrings[i] = strings.ToLower(strings.TrimPrefix(a.String(), "ARCHITECTURE_"))
		}
		if _, err := fmt.Fprintf(writer, "Architecture:\t%s\n", strings.Join(archStrings, ", ")); err != nil {
			return err
		}

		lifecycle := strings.TrimPrefix(spec.GetLifecycle().String(), "DISK_IMAGE_LIFECYCLE_")
		if _, err := fmt.Fprintf(writer, "Lifecycle:\t%s\n", lifecycle); err != nil {
			return err
		}

		if pullSecret := spec.GetPullSecret(); pullSecret != nil &&
			(pullSecret.GetId() != "" || pullSecret.GetName() != "") {
			switch {
			case pullSecret.GetName() != "" && pullSecret.GetProject() != "":
				if _, err := fmt.Fprintf(writer, "Pull Secret:\t%s (project %s)\n",
					pullSecret.GetName(), pullSecret.GetProject()); err != nil {
					return err
				}
			case pullSecret.GetName() != "":
				if _, err := fmt.Fprintf(writer, "Pull Secret:\t%s\n", pullSecret.GetName()); err != nil {
					return err
				}
			default:
				if _, err := fmt.Fprintf(writer, "Pull Secret:\t%s\n", pullSecret.GetId()); err != nil {
					return err
				}
			}
		}

		if dep := spec.GetDeprecation(); dep != nil {
			hasContent := dep.GetDeprecationTimestamp() != nil ||
				dep.GetObsolescenceTimestamp() != nil
			if hasContent {
				if ts := dep.GetDeprecationTimestamp(); ts != nil {
					if _, err := fmt.Fprintf(writer, "Deprecated At:\t%s\n", ts.AsTime().Format(time.RFC3339)); err != nil {
						return err
					}
				}
				if ts := dep.GetObsolescenceTimestamp(); ts != nil {
					if _, err := fmt.Fprintf(writer, "Obsolete At:\t%s\n", ts.AsTime().Format(time.RFC3339)); err != nil {
						return err
					}
				}
			}
		}
	}

	return writer.Flush()
}

const shortHelp = `Describe a disk image`

const longHelp = `
Describe a disk image.

Displays detailed information about a disk image, including its source reference, guest OS family,
architecture, lifecycle state, and deprecation details if applicable.

To describe a disk image by name:

{{ bt 3 }}shell
{{ binary }} describe diskimage fedora-41
{{ bt 3 }}
`
