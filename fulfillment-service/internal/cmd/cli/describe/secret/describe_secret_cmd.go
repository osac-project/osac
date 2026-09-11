/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package secret

import (
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "secret [FLAG...] ID|NAME",
		Aliases:               []string{"secrets"},
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

	client := publicv1.NewSecretsClient(conn)

	matched, err := lookup.Find(ref, "secret", func(filter string, limit int32) ([]*publicv1.Secret, error) {
		resp, err := client.List(ctx, publicv1.SecretsListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to describe secret: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	// Fetch the full secret via Get to include data keys.
	getResp, err := client.Get(ctx, publicv1.SecretsGetRequest_builder{
		Id: matched.GetId(),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to get secret details: %w", err)
	}

	RenderSecret(c.console, getResp.GetObject())

	return nil
}

// RenderSecret writes a formatted description of a secret to w.
func RenderSecret(w io.Writer, secret *publicv1.Secret) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	name := "-"
	if v := secret.GetMetadata().GetName(); v != "" {
		name = v
	}

	project := "-"
	if v := secret.GetMetadata().GetProject(); v != "" {
		project = v
	}

	created := "-"
	if v := secret.GetMetadata().GetCreationTimestamp(); v != nil {
		created = v.AsTime().Format("2006-01-02T15:04:05Z")
	}

	fmt.Fprintf(writer, "ID:\t%s\n", secret.GetId())
	fmt.Fprintf(writer, "Name:\t%s\n", name)
	fmt.Fprintf(writer, "Project:\t%s\n", project)
	fmt.Fprintf(writer, "Created:\t%s\n", created)
	writer.Flush()

	labels := secret.GetMetadata().GetLabels()
	if len(labels) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Labels:")
		lw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		keys := make([]string, 0, len(labels))
		for k := range labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(lw, "  %s:\t%s\n", k, labels[k])
		}
		lw.Flush()
	} else {
		fmt.Fprintln(w, "Labels:  (none)")
	}

	data := secret.GetData()
	if len(data) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Data:")
		keys := make([]string, 0, len(data))
		for k := range data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "  %s  (%d bytes)\n", k, len(data[k]))
		}
	} else {
		fmt.Fprintln(w, "Data:  (none)")
	}
}

const shortHelp = `Describe a secret`

const longHelp = `
Display detailed information about a secret, referenced by identifier or name.

The output shows metadata and data key names with their sizes. Secret values are not displayed;
use {{ bt }}get secret <name> -o yaml{{ bt }} to retrieve actual values.

Examples:

{{ bt 3 }}shell
# Describe a secret by name:
{{ binary }} describe secret my-tls-cert
{{ bt 3 }}

{{ bt 3 }}shell
# Describe a secret by identifier:
{{ binary }} describe secret 019e5fee-0742-78b7-8c4a-e2501f44783a
{{ bt 3 }}
`
