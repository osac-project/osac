# Local CaaS integration tests

This directory provides a local integration-test environment built on Kind. It
runs Fulfillment, the OSAC operator, Bare Metal Fulfillment (BMF), and AWX, with
simulated hardware and external provider controllers. No physical servers or
guest OpenShift cluster are required.

The environment uses the Kind dev profile with provider networking disabled.
It does not validate physical boot, guest OpenShift health, provider
connectivity, or billing correctness.

## Set up

You need Go 1.26.3, Helm 3, Kind, a container runtime, and `kubectl`, plus Git,
Make, Bash, and Python 3. The examples use rootless Podman. See the
[installer prerequisites](../../../osac-installer/README.md#prerequisites--setup)
for additional host and installation requirements.

Use a dedicated, disposable Kind cluster. The checked-in
[Kind configuration](../../../osac-installer/kind-config-dev-caas.yaml) binds
loopback ports 8443 and 8080. These ports can conflict with another local OSAC
installation, even if you choose a different cluster name.

Run all commands below from the repository root. Install the module-pinned
Ginkgo CLI and add its location to your `PATH`:

```bash
(cd tests/integration/caas && go install github.com/onsi/ginkgo/v2/ginkgo)
export PATH="$(go env GOPATH)/bin:$PATH"
```

To prepare the environment without running cluster scenarios:

```bash
export CONTAINER_TOOL=podman
make -C tests/integration/caas prepare
```

`prepare` creates the infrastructure, builds and loads local images, checks the
AWX bootstrap configuration, deploys the services, and prepares persistent test
users, tenants, service accounts, and hub. The installation uses namespace
`osac`, the local application chart, and fixed `:it` image tags. AWX fetches
playbooks from the Git source described under [Configuration](#configuration).

To run setup one step at a time:

```bash
make -C tests/integration/caas _infrastructure
make -C tests/integration/caas _images
make -C tests/integration/caas _bootstrap-check
make -C tests/integration/caas _deploy
make -C tests/integration/caas fixtures
```

## Run tests

Run against the prepared installation without rebuilding or redeploying. Complete
`prepare` (or the staged setup including `fixtures`) before the first run:

```bash
make -C tests/integration/caas run
```

`PROCS` defaults to four. Each Ginkgo process gets a separate inventory group
containing two simulated hosts. Override `PROCS` to change the process count.

**Run only one suite invocation at a time**, including `fixtures`. Do not load
images, redeploy services, update Helm releases, or manually change simulator
controls while tests are running.

To list the selected tests without running them, use a single-process dry run:

```bash
make -C tests/integration/caas run PROCS=1 \
  GINKGO_ARGS='--label-filter=caas-it --dry-run'
```

### Simulation profiles

Fixtures select a simulation profile for each case. Cases share a settings
ConfigMap with separate entries bound to their InfraEnv, HostedCluster and CAPI
Cluster UIDs. Profile changes affect only that case and do not restart the simulator.

| Profile | Simulated state | Recovery |
|---|---|---|
| `normal` | Agents finish installation and detach during deletion; the control plane can become ready. | — |
| `installation-stalled` | Agents remain in the installing state. | Switch to `normal` to continue installation. |
| `installation-failed` | Agents enter a terminal installation error. | Failed Agents remain failed after a profile change. |
| `detach-stalled` | Installed Agents remain bound during deletion. | Switch to `normal` to allow detachment. |
| `control-plane-stalled` | Workers install, but the control plane remains unavailable. | Switch to `normal` to allow readiness. |

### Logging and reports

Test results and logs appear in Ginkgo output. To enable debug logging:

```bash
make -C tests/integration/caas run LOG_LEVEL=debug
```

`run` changes test logging. To change simulator logging, stop the tests and run
`_deploy LOG_LEVEL=debug`. Valid levels are `debug`, `info`, `warn`, and `error`.

For JSON or JUnit reports, pass report options through `GINKGO_ARGS`. Set
`--output-dir` to an absolute path outside the source tree. For `--json-report`
and `--junit-report`, use filenames without directories.

## Configuration

Use the same cluster name, runtime, and kubeconfig throughout setup, testing,
and cleanup. Clients use the kubeconfig's current context.

| Input | Default | Purpose |
|---|---|---|
| `KIND_CLUSTER_NAME` | `osac-dev-caas` | Dedicated Kind cluster. |
| `KUBECONFIG` | `~/.kube/<cluster-name>-kind.kubeconfig` | Kubernetes client configuration. |
| `CONTAINER_TOOL` | `podman` | Image builds and the Kind provider. |
| `PROCS` | `4` | Number of parallel Ginkgo processes. |
| `GINKGO_ARGS` | `--label-filter=caas-it` | Test filters and report options. |
| `LOG_LEVEL` | `info` | Test logging; simulator logging when deploying. |
| `PROJECT_URL` | Checkout's `origin` URL, or `https://github.com/osac-project/osac.git` if unavailable | AWX playbook source. |
| `PROJECT_COMMIT` | Checkout's full HEAD revision | Git revision to fetch from `PROJECT_URL`. |

### AWX Git source

For playbooks on a contributor fork:

```bash
make -C tests/integration/caas _deploy \
  PROJECT_URL=https://github.com/my-account/osac.git \
  PROJECT_COMMIT="$(git rev-parse HEAD)"
```

AWX fetches committed playbooks and controller configuration from `PROJECT_URL`
at `PROJECT_COMMIT`. The revision must be available from that repository. AWX
does not use uncommitted files from your checkout.

The environment deploys the local OSAC Helm chart. Published-chart overrides
(`OSAC_CHART_OCI_REF` and `OSAC_CHART_OCI_VERSION`) are not supported.

## Local development

### Checks

These checks do not require a running cluster:

```bash
make -C tests/integration/caas check     # Go, pinned CRD, and Helm checks
make -C tests/integration/caas lint      # Go lint checks
make -C tests/integration/caas lint-fix  # Formatting and automatic lint fixes
make -C tests/integration/caas unit      # Internal unit tests
```

Bare `make` in this directory runs `check`. The Make targets select the module's
Go workspace. Use `unit` for unit tests. Running `go test ./...` from this module
also runs the preparation and cluster suites, which require the deployed environment.

To check the AWX bootstrap configuration locally:

```bash
make -C tests/integration/caas _bootstrap-check
```

This requires an existing AWX execution-environment image. It does not pull
images or contact application APIs.

### Updating images and CRDs

`_deploy` uses images that are already built and loaded. Run `_images` again
after changing image contents, then run `_deploy` to update the installation.

For simulator-only changes, run `_simulator-image-build` followed by
`_simulator-image-load` with tests stopped. Loading the image restarts an existing
simulator Deployment if it uses that image.

Chart checks and deployment prepare the CRDs pinned in `charts/crds.txt`,
reusing files with matching checksums. Run `prepare-crds` to prepare them manually.

## Cleanup and reuse

Each case deletes its inputs and waits for controllers and AWX to finish
cleanup. Its original hosts must be released before another case can reuse the
inventory group.

After all processes finish, the suite checks that application resources and
provider outputs are gone, automation has stopped, and the original hosts are
released. If these checks pass, it deletes its hardware and discovery-data
fixtures.

A cleanup failure stops further tests. Preserve the failed state for
investigation. Do not remove finalizers or repair inventory merely to force
reuse. Avoid Ginkgo's `--keep-going` option.

Before updating Helm releases, finish application cleanup and confirm that
inventory is released. Recreate the disposable cluster after CRD or provider
networking-mode changes.

To remove the installation:

```bash
make -C tests/integration/caas destroy
```

**This deletes the entire selected Kind cluster and all its contents.** If you
used a custom `KIND_CLUSTER_NAME` or `CONTAINER_TOOL`, pass those values again.

See [Integration testing](../../../docs/INTEGRATION-TESTING.md) for the wider
project testing guide.
