/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package update

import (
	"github.com/spf13/cobra"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/update/diskimage"
)

func Cmd() *cobra.Command {
	result := &cobra.Command{
		Use:                   "update",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
	}
	result.AddCommand(diskimage.Cmd())
	return result
}

const shortHelp = `Update resources`

const longHelp = `
Update existing resources with non-interactive flags.

Prefer {{ bt }}osac edit{{ bt }} for free-form YAML edits. Use {{ bt }}update{{ bt }}
subcommands when a specific field has a dedicated flag-based workflow.
`
