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
	"github.com/spf13/cobra"
)

// Cmd creates the top-level volumes command with list, get, create, update, and delete subcommands.
func Cmd() *cobra.Command {
	result := &cobra.Command{
		Use:   "volumes",
		Short: shortHelp,
		Long:  longHelp,
	}
	result.AddCommand(listCmd())
	result.AddCommand(getCmd())
	result.AddCommand(createCmd())
	result.AddCommand(updateCmd())
	result.AddCommand(deleteCmd())
	return result
}

const shortHelp = `Manage volumes`

const longHelp = `
Manage standalone block storage volumes.

Volumes are tenant-scoped resources that represent storage provisioned on a backend storage array
through the OSAC storage control plane. This command provides CRUD operations on volumes.

To list all volumes:

{{ bt 3 }}shell
{{ binary }} volumes list
{{ bt 3 }}

To get details of a specific volume:

{{ bt 3 }}shell
{{ binary }} volumes get my-volume
{{ bt 3 }}

To create a new volume:

{{ bt 3 }}shell
{{ binary }} volumes create --name my-volume --storage-tier gold --size-gib 100
{{ bt 3 }}

To update a volume:

{{ bt 3 }}shell
{{ binary }} volumes update my-volume --display-name "Production DB"
{{ bt 3 }}

To delete a volume:

{{ bt 3 }}shell
{{ binary }} volumes delete my-volume
{{ bt 3 }}
`
