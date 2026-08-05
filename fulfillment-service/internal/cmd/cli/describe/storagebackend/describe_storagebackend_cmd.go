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
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

// Cmd creates the command to describe a storage backend.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "storagebackend [FLAG...] ID|NAME",
		Aliases:               []string{"storagebackends"},
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

	client := privatev1.NewStorageBackendsClient(conn)

	matched, err := lookup.Find(ref, "storage backend", func(filter string, limit int32) ([]*privatev1.StorageBackend, error) {
		resp, err := client.List(ctx, privatev1.StorageBackendsListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to describe storage backend: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	renderStorageBackend(c.console, matched)

	return nil
}

func renderStorageBackend(w io.Writer, sb *privatev1.StorageBackend) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	fmt.Fprintf(writer, "ID:\t%s\n", sb.GetId())
	fmt.Fprintf(writer, "Name:\t%s\n", sb.GetMetadata().GetName())

	spec := sb.GetSpec()
	if spec != nil {
		fmt.Fprintf(writer, "Provider:\t%s\n", spec.GetProvider())
		fmt.Fprintf(writer, "Endpoint:\t%s\n", spec.GetEndpoint())

		if desc := spec.GetDescription(); desc != "" {
			fmt.Fprintf(writer, "Description:\t%s\n", desc)
		}

		if creds := spec.GetCredentials(); creds != nil {
			fmt.Fprintf(writer, "Username:\t%s\n", creds.GetUsername())
		}
	}

	status := sb.GetStatus()
	if status != nil {
		state := strings.TrimPrefix(status.GetState().String(), "STORAGE_BACKEND_STATE_")
		fmt.Fprintf(writer, "State:\t%s\n", state)

		if msg := status.GetMessage(); msg != "" {
			fmt.Fprintf(writer, "Message:\t%s\n", msg)
		}
	}

	writer.Flush()
}

const shortHelp = `Describe a storage backend.`

const longHelp = `
Describe a storage backend.

Displays detailed information about a storage backend, including its provider, management endpoint,
credentials (username only — password is redacted), and current state.

To describe a storage backend by name:

{{ bt 3 }}shell
{{ binary }} describe storagebackend vast-prod
{{ bt 3 }}
`
