/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package probe

import (
	"github.com/spf13/cobra"

	"github.com/osac-project/fulfillment-service/internal/cmd/service/probe/grpcserver"
)

// Cmd creates and returns the `probe` command.
func Cmd() *cobra.Command {
	result := &cobra.Command{
		Use:                   "probe COMMAND [FLAG...]",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
	}
	result.AddCommand(grpcserver.Cmd())
	return result
}

const shortHelp = `Probes components`

const longHelp = `
Probes components.
`
