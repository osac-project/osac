/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package clusterversion

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

// Cmd creates the command to create a cluster version.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "clusterversion",
		Aliases:               []string{string(proto.MessageName((*privatev1.ClusterVersion)(nil)))},
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}
	flags := result.Flags()
	flags.StringVar(
		&runner.name,
		"name",
		"",
		nameFlagHelp,
	)
	flags.StringVar(
		&runner.version,
		"version",
		"",
		versionFlagHelp,
	)
	flags.StringVar(
		&runner.image,
		"image",
		"",
		imageFlagHelp,
	)
	flags.BoolVar(
		&runner.enabled,
		"enabled",
		true,
		enabledFlagHelp,
	)
	flags.BoolVar(
		&runner.isDefault,
		"default",
		false,
		defaultFlagHelp,
	)
	flags.StringVar(
		&runner.state,
		"state",
		"",
		stateFlagHelp,
	)
	result.MarkFlagRequired("version") //nolint:errcheck
	result.MarkFlagRequired("image")   //nolint:errcheck
	return result
}

type runnerContext struct {
	console   *terminal.Console
	name      string
	version   string
	image     string
	enabled   bool
	isDefault bool
	state     string
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	c.console = terminal.ConsoleFromContext(ctx)

	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	parsedState, err := parseState(c.state)
	if err != nil {
		return err
	}

	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	client := privatev1.NewClusterVersionsClient(conn)

	spec := privatev1.ClusterVersionSpec_builder{
		Version: c.version,
		Image:   c.image,
		State:   parsedState,
	}
	if cmd.Flags().Changed("enabled") {
		spec.Enabled = proto.Bool(c.enabled)
	}
	if cmd.Flags().Changed("default") {
		spec.IsDefault = proto.Bool(c.isDefault)
	}

	cv := privatev1.ClusterVersion_builder{
		Spec: spec.Build(),
	}
	if c.name != "" {
		cv.Metadata = privatev1.Metadata_builder{
			Name: c.name,
		}.Build()
	}

	response, err := client.Create(ctx, privatev1.ClusterVersionsCreateRequest_builder{
		Object: cv.Build(),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to create cluster version: %w", err)
	}

	c.console.Infof(ctx, "Created cluster version '%s'.\n", response.GetObject().GetId())

	return nil
}

func parseState(value string) (privatev1.ClusterVersionState, error) {
	switch strings.ToUpper(value) {
	case "ACTIVE":
		return privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_ACTIVE, nil
	case "DEPRECATED":
		return privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_DEPRECATED, nil
	case "OBSOLETE":
		return privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE, nil
	case "":
		return privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_UNSPECIFIED, nil
	default:
		return 0, fmt.Errorf("invalid state '%s', must be ACTIVE, DEPRECATED, or OBSOLETE", value)
	}
}

const shortHelp = `Create a cluster version`

const longHelp = `
Create a cluster version.

A cluster version defines an available OpenShift version that can be selected when creating
clusters. Cluster versions are managed by Cloud Provider Admins and control which release images
are available for provisioning.

If {{ bt }}--name{{ bt }} is omitted, the server auto-generates it from the version string by
lowercasing and replacing non-alphanumeric characters with dashes (e.g., {{ bt }}4.17.0{{ bt }}
becomes {{ bt }}4-17-0{{ bt }}).

To create a cluster version:

{{ bt 3 }}shell
{{ binary }} create clusterversion --version 4.17.0 \
  --image quay.io/openshift-release-dev/ocp-release:4.17.0-multi \
  --enabled --default
{{ bt 3 }}
`

const nameFlagHelp = `
_NAME_ - Name of the cluster version. If omitted, auto-generated from the version string.
`

const versionFlagHelp = `
_VERSION_ - SemVer 2.0.0 version string (e.g., {{ bt }}4.17.0{{ bt }}, {{ bt }}4.17.0-rc.1{{ bt }}).
Must be unique among active cluster versions.
`

const imageFlagHelp = `
_IMAGE_ - OCI release image pullspec
(e.g., {{ bt }}quay.io/openshift-release-dev/ocp-release:4.17.0-multi{{ bt }}).
`

const enabledFlagHelp = `
_[BOOLEAN]_ - Whether new clusters may select this version. Defaults to true if not specified.
`

const defaultFlagHelp = `
_[BOOLEAN]_ - Whether this is the default version for new clusters. At most one active cluster
version may be the default.
`

const stateFlagHelp = `
_STATE_ - Lifecycle state: {{ bt }}ACTIVE{{ bt }}, {{ bt }}DEPRECATED{{ bt }}, or
{{ bt }}OBSOLETE{{ bt }}. Defaults to ACTIVE if not specified.
`
