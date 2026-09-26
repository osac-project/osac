/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package fabricdomain

import (
	"errors"
	"fmt"
	"io"
	"strings"
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
	return &cobra.Command{
		Use:                   "fabricdomain [FLAG...] ID|NAME",
		Aliases:               []string{"fabricdomains"},
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.ExactArgs(1),
		RunE:                  runner.run,
	}
}

type runnerContext struct{ console *terminal.Console }

func (c *runnerContext) run(cmd *cobra.Command, args []string) (runErr error) {
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
	defer func() {
		if err := conn.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("failed to close gRPC connection: %w", err))
		}
	}()

	client := publicv1.NewFabricDomainsClient(conn)
	matched, err := lookup.Find(args[0], "fabric domain", func(filter string, limit int32) ([]*publicv1.FabricDomain, error) {
		response, err := client.List(ctx, publicv1.FabricDomainsListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to describe fabric domain: %w", err)
		}
		return response.GetItems(), nil
	})
	if err != nil {
		return err
	}

	return RenderFabricDomain(c.console, matched)
}

func RenderFabricDomain(w io.Writer, domain *publicv1.FabricDomain) error {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	name := domain.GetMetadata().GetName()
	if name == "" {
		name = "-"
	}
	domainType := strings.TrimPrefix(domain.GetSpec().GetType().String(), "FABRIC_DOMAIN_TYPE_")
	if domainType == "" || domainType == "UNSPECIFIED" {
		domainType = "-"
	}
	state := "-"
	conditionStatus := "-"
	message := "-"
	if conditions := domain.GetStatus().GetConditions(); len(conditions) > 0 {
		state = strings.TrimPrefix(conditions[0].GetType().String(), "FABRIC_DOMAIN_CONDITION_TYPE_")
		conditionStatus = strings.TrimPrefix(conditions[0].GetStatus().String(), "CONDITION_STATUS_")
		if conditions[0].GetMessage() != "" {
			message = conditions[0].GetMessage()
		}
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"ID", domain.GetId()},
		{"Name", name},
		{"Type", domainType},
		{"Servers", strings.Join(domain.GetSpec().GetServers(), ", ")},
		{"Virtual Network", domain.GetSpec().GetVirtualNetwork()},
		{"State", state},
		{"Status", conditionStatus},
		{"Message", message},
	} {
		if _, err := fmt.Fprintf(writer, "%s:\t%s\n", field.name, field.value); err != nil {
			return fmt.Errorf("failed to render fabric domain %s: %w", strings.ToLower(field.name), err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush fabric domain output: %w", err)
	}
	return nil
}

const shortHelp = `Describe a fabric domain`

const longHelp = `
Display detailed information about a fabric domain, referenced by identifier or name.

{{ bt 3 }}shell
{{ binary }} describe fabricdomain tenant-a-gpu-ew
{{ bt 3 }}
`
