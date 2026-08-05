/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package describe

import (
	"github.com/spf13/cobra"

	"github.com/osac-project/fulfillment-service/internal/cmd/cli/describe/baremetalinstance"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/describe/cluster"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/describe/clusterversion"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/describe/computeinstance"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/describe/externalip"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/describe/externalipattachment"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/describe/instancetype"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/describe/natgateway"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/describe/networkclass"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/describe/securitygroup"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/describe/storagebackend"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/describe/storagetier"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/describe/subnet"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/describe/virtualnetwork"
)

func Cmd() *cobra.Command {
	result := &cobra.Command{
		Use:   "describe",
		Short: shortHelp,
		Long:  longHelp,
	}
	result.AddCommand(baremetalinstance.Cmd())
	result.AddCommand(cluster.Cmd())
	result.AddCommand(clusterversion.Cmd())
	result.AddCommand(computeinstance.Cmd())
	result.AddCommand(externalip.Cmd())
	result.AddCommand(externalipattachment.Cmd())
	result.AddCommand(instancetype.Cmd())
	result.AddCommand(natgateway.Cmd())
	result.AddCommand(networkclass.Cmd())
	result.AddCommand(virtualnetwork.Cmd())
	result.AddCommand(subnet.Cmd())
	result.AddCommand(securitygroup.Cmd())
	result.AddCommand(storagebackend.Cmd())
	result.AddCommand(storagetier.Cmd())
	return result
}

const shortHelp = `Describe a resource`

const longHelp = `
Describe a resource.
`
