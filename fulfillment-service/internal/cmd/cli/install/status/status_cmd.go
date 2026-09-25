/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package status

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/install/render"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/install/secretref"
	"github.com/osac-project/osac/fulfillment-service/internal/exit"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	"github.com/osac-project/osac/osac-installer/pkg/install"
)

// defaultServices is every OSAC service: the default for --services, so a
// bare `osac install status` reports on everything OSAC might need.
var defaultServices = []string{"vmaas", "caas", "bmaas", "maas", "metering"}

const defaultInterval = 5 * time.Second

func Cmd() *cobra.Command {
	return newCmd(&runnerContext{loadClients: install.LoadClients})
}

// newCmd builds the command around the given runner: flags are bound to
// this exact runner instance and RunE calls its run method, so the two can
// never drift apart. Tests call this directly with a runner whose
// loadClients (and, for --watch, runProgram) is stubbed, instead of
// overwriting cmd.RunE on a command built by Cmd().
func newCmd(runner *runnerContext) *cobra.Command {
	result := &cobra.Command{
		Use:                   "status [FLAG...]",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}

	flags := result.Flags()
	flags.StringVar(
		&runner.args.kubeconfig,
		"kubeconfig",
		"",
		kubeconfigFlagHelp,
	)
	flags.StringSliceVar(
		&runner.args.services,
		"services",
		defaultServices,
		servicesFlagHelp,
	)
	flags.BoolVar(
		&runner.args.metal3,
		"metal3",
		false,
		metal3FlagHelp,
	)
	flags.StringArrayVar(
		&runner.args.requireSecrets,
		"require-secret",
		nil,
		requireSecretFlagHelp,
	)
	flags.BoolVarP(
		&runner.args.watch,
		"watch",
		"w",
		false,
		watchFlagHelp,
	)
	flags.DurationVar(
		&runner.args.interval,
		"interval",
		defaultInterval,
		intervalFlagHelp,
	)

	return result
}

type runnerContext struct {
	// loadClients builds the clients checks run against. Defaults to
	// install.LoadClients; overridden in tests to avoid depending on a real
	// kubeconfig or cluster.
	loadClients func(kubeconfigPath string) (*install.Clients, error)
	// runProgram runs the interactive --watch program. Defaults to a real
	// tea.Program.Run; overridden in tests, since that needs a real
	// terminal to drive.
	runProgram func(tea.Model) error
	args       struct {
		kubeconfig     string
		services       []string
		metal3         bool
		requireSecrets []string
		watch          bool
		interval       time.Duration
	}
}

func (r *runnerContext) run(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	console := terminal.ConsoleFromContext(ctx)

	clients, err := r.loadClients(r.args.kubeconfig)
	if err != nil {
		console.Errorf(ctx, "Failed to connect to the Hub cluster: %v\n", err)
		return exit.Error(1)
	}

	secretRefs, err := secretref.ParseAll(r.args.requireSecrets)
	if err != nil {
		console.Errorf(ctx, "Invalid --require-secret value: %v\n", err)
		return exit.Error(1)
	}

	checks, err := install.DefaultChecks(install.CheckOptions{Services: r.args.services, Metal3: r.args.metal3})
	if err != nil {
		console.Errorf(ctx, "Failed to build the prerequisite checks: %v\n", err)
		return exit.Error(1)
	}
	checks = append(checks, install.SecretChecks(secretRefs)...)

	if !r.args.watch {
		results := install.RunAll(ctx, clients, checks)
		console.Infof(ctx, "%s", renderStatus(results, render.Width(console.Stdout())))
		return nil
	}

	runProgram := r.runProgram
	if runProgram == nil {
		runProgram = func(m tea.Model) error {
			_, err := tea.NewProgram(m).Run()
			return err
		}
	}
	if err := runProgram(newWatchModel(ctx, clients, checks, r.args.interval)); err != nil {
		console.Errorf(ctx, "Failed to display status: %v\n", err)
		return exit.Error(1)
	}
	return nil
}

const shortHelp = `Show a live dashboard of the Hub cluster's readiness to install OSAC`

const longHelp = `
Runs OSAC's prerequisite checks against the target Hub cluster and shows a
progress bar and per-check status, fitted to the terminal.

With {{ bt }}--watch{{ bt }}/{{ bt }}-w{{ bt }}, checks re-run on an interval and the
view redraws in place instead of printing once and exiting -- press
{{ bt }}q{{ bt }} to quit. Without it, this runs once and exits, the same as a
single frame of the watch view.

Unlike {{ bt }}osac install validate{{ bt }}, this command never fails on a failed
check and doesn't produce scriptable output; use {{ bt }}osac install discover{{ bt }}
or {{ bt }}validate{{ bt }} (with {{ bt }}--json{{ bt }}) for that.

Requires kubectl/oc-level access to the target Hub cluster: a working
kubeconfig with permission to read CustomResourceDefinitions,
ClusterServiceVersions, ClusterVersion, StorageClasses, Secrets, and, with
{{ bt }}--metal3{{ bt }}, Metal3's Provisioning resource. This is different from
{{ bt }}osac login{{ bt }}, which authenticates against the fulfillment-service API,
not the Hub cluster's Kubernetes API.
`

const kubeconfigFlagHelp = `
_PATH_ - Path to the kubeconfig file to use. Defaults to the
{{ bt }}KUBECONFIG{{ bt }} environment variable, then {{ bt }}~/.kube/config{{ bt }},
using the current context — the same resolution {{ bt }}oc{{ bt }}/{{ bt }}kubectl{{ bt }} use.
`

const servicesFlagHelp = `
_[SERVICE...]{{ bt }},{{ bt }}...{{ bt }}]_ - Which OSAC services to check
prerequisites for: {{ bt }}vmaas{{ bt }}, {{ bt }}caas{{ bt }}, {{ bt }}bmaas{{ bt }},
{{ bt }}maas{{ bt }}, {{ bt }}metering{{ bt }}. Defaults to every service.
`

const metal3FlagHelp = `
_[BOOLEAN]_ - Also check Metal3 bare-metal prerequisites: the BareMetalHost
CRD and the Provisioning CR's {{ bt }}watchAllNamespaces{{ bt }} setting. Enable
this if you plan to install OSAC with the Metal3 backend.
`

const requireSecretFlagHelp = `
_NAMESPACE/NAME[:KEY,...]_ - Also check that the named Secret exists (and,
if given, that it has every listed key). Repeatable. Use this to check
Secrets your own {{ bt }}my-values.yaml{{ bt }} references, such as the AAP
license manifest or a database connection Secret — their names aren't fixed
by OSAC, so they aren't included by default.
`

const watchFlagHelp = `
_[BOOLEAN]_ - Re-run checks on an interval and redraw the view in place
instead of printing once and exiting. Press {{ bt }}q{{ bt }} to quit.
`

const intervalFlagHelp = `
_DURATION_ - How often to re-run checks in {{ bt }}--watch{{ bt }} mode, e.g.
{{ bt }}10s{{ bt }}, {{ bt }}1m{{ bt }}. Ignored without {{ bt }}--watch{{ bt }}.
`
