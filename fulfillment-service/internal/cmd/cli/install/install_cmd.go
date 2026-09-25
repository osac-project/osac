/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package install implements `osac install`, installation and lifecycle
// operations for the Hub cluster OSAC is deployed onto. See AGENTS.md in
// this directory: unlike the rest of this CLI, these commands are
// Kubernetes administration tools, not tenant-facing ones.
package install

import (
	"github.com/spf13/cobra"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/install/discover"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/install/status"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/install/validate"
)

func Cmd() *cobra.Command {
	result := &cobra.Command{
		Use:                   "install COMMAND [FLAG...]",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
	}
	result.AddCommand(discover.Cmd())
	result.AddCommand(status.Cmd())
	result.AddCommand(validate.Cmd())
	return result
}

const shortHelp = `Manage Hub cluster installation and lifecycle operations`

const longHelp = `
Installation and lifecycle operations for the Hub cluster OSAC is deployed
onto: checking a cluster's readiness before installing today, with more
lifecycle operations (log collection, upgrade assistance) planned.

Unlike the rest of this CLI, these commands are Kubernetes administration
tools: they require kubectl/oc-level cluster access, not an
{{ bt }}osac login{{ bt }} session, and assume familiarity with Kubernetes. They
exist for whoever sets up or operates a Hub cluster — a cluster admin or
SRE — and some of them, like {{ bt }}discover{{ bt }}, {{ bt }}status{{ bt }}, and
{{ bt }}validate{{ bt }}, run before OSAC, and therefore before
fulfillment-service, exists on that cluster.
`
