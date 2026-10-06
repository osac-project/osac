/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package catalogitem

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/color"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
)

type fetchFunc func(context.Context, *grpc.ClientConn, string) (view, error)

func newCommand(name, kind string, fetch fetchFunc) *cobra.Command {
	return &cobra.Command{
		Use:     name + " [FLAG...] ID|NAME",
		Aliases: []string{name + "s"},
		Short:   "Describe a " + kind,
		Long: fmt.Sprintf(`
Describe a %s and its governed resource fields and Template parameters.

An editable field without a value has no catalog default. Other fields follow normal creation behavior.
Use {{ bt }}{{ binary }} get %s ID|NAME -o yaml{{ bt }} for the full Catalog Item definition.
`, kind, name),
		DisableFlagsInUseLine: true,
		Args:                  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg := config.SettingsFromContext(ctx)
			if !cfg.Armed() {
				return fmt.Errorf("there is no configuration, run the 'login' command")
			}
			conn, err := cfg.Connect(ctx, cmd.Flags())
			if err != nil {
				return fmt.Errorf("failed to create gRPC connection: %w", err)
			}
			defer conn.Close()
			v, err := fetch(ctx, conn, args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			return render(out, v, color.Enabled(cmd, out))
		},
	}
}
