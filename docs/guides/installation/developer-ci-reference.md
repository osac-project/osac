# OSAC CI and Development Reference

**Audience**: OSAC contributors running the repository's own test suites or
a local dev cluster.

This is not an installation guide — for that, see the
[customer install guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/customer-install-guide.md)
(published chart) or the
[Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md)
(phase-1 prerequisites from a checkout). Nothing here is required to follow
either of those.

## The `make` wrapper

The repository `Makefile` wraps the phase-1 and phase-2 Helm commands with a
CI reference profile:

```bash
make helm-deps
make install PLATFORM=openshift PROFILE=vmaas-ci NS=osac
```

| Target | Description |
|--------|--------------|
| `make install` | Full install (infra + osac) |
| `make install-infra` | Infrastructure only (osac-deps + osac-infra) |
| `make install-osac` | OSAC instance only |
| `make uninstall` | Full uninstall (reverse order) |
| `make test` | Run integration tests (SUITE= required) |
| `make helm-lint` | Lint all charts |

All targets require `PLATFORM=kind|openshift PROFILE=dev|vmaas-ci|...|full-ci NS=<namespace>`.

```bash
make uninstall PLATFORM=openshift PROFILE=vmaas-ci NS=osac
```

## CI reference profiles

Each base profile under `values/` has an `infra.yaml` and an `instance.yaml`,
used by CI and local dev, never for a real deployment. Netris CI layers the
service-specific `netris.yaml` after the base instance values:

| Profile | Use case |
|---------|----------|
| `values/vmaas-ci/` | VMaaS CI (compute instances) |
| `values/caas-ci/` | CaaS CI (cluster provisioning) |
| `values/bmaas-ci/` | BMaaS CI (bare metal) |
| `values/full-ci/` | All services enabled |
| `values/dev/` | Local dev (Kind) |
| `values/vmaas-ci/netris.yaml` | Netris + CUDN EVPN overlay for VMaaS CI |
| `values/bmaas-ci/netris.yaml` | Netris overlay for BMaaS CI (no Kubernetes Manager) |
| `values/full-ci/netris.yaml` | Netris + CUDN EVPN overlay for full and stretched CI |

## Nightly evaluation builds

For evaluation builds, check the
[OSAC Nightly Build workflow](https://github.com/osac-project/osac/actions/workflows/nightly-build.yaml)
and select the newest successful run. Use its exact chart version; a newer
tag alone does not mean that the full pipeline succeeded. Nightly versions
are evaluation artifacts, not a substitute for the released installation
covered by the [customer install guide](customer-install-guide.md).

Set the chart location and version before retrieving chart values, and keep
the version unchanged throughout the evaluation:

```console
export OSAC_RELEASE_VERSION="<exact-nightly-chart-version>"
export OSAC_CHART="oci://ghcr.io/osac-project/charts/osac"
```

Use the chart version without the `osac/v` or `osac-release/v` tag prefix.
The matching nightly source tag uses `osac/v${OSAC_RELEASE_VERSION}`.
Retain the selected chart's pinned image and AAP content defaults.

### Building the nightly CLI

Nightly builds publish charts and images, but no CLI binary release assets.
Find the `fulfillment-service` version in the selected umbrella chart's
dependency inventory or chart metadata and set it below. Component versions
are independent of the umbrella version:

```console
export FULFILLMENT_SERVICE_VERSION="<pinned-fulfillment-service-version>"
```

If `FULFILLMENT_SERVICE_VERSION` contains `-nightly.`, build the CLI from
the exact umbrella source tag selected by `OSAC_RELEASE_VERSION`. This
uses the Fulfillment Service's documented
[CLI build procedure](../../../fulfillment-service/README.md#building-the-binaries).
Install Git and a Go toolchain satisfying the selected checkout's
`go.work` and `fulfillment-service/go.mod` requirements. The build needs
network access to download Go module dependencies. Use a new
`osac-cli-source` directory:

```console
git clone --depth 1 --branch "osac/v${OSAC_RELEASE_VERSION}" \
  https://github.com/osac-project/osac.git osac-cli-source &&
(
  cd osac-cli-source/fulfillment-service &&
  CGO_ENABLED=0 go build \
    -ldflags "-X github.com/osac-project/osac/fulfillment-service/internal/version.id=${FULFILLMENT_SERVICE_VERSION}" \
    -o osac ./cmd/osac &&
  sudo install -m 0755 osac /usr/local/bin/osac
)
```

## LVMS registration for evaluation

For a development or CI environment with LVMS already installed on the
workload cluster, merge this override into the OSAC instance's `my-values.yaml`:

```yaml
lvms:
  enabled: true
```

This enables the local-storage registration hook, which creates the `local`
StorageBackend and StorageTier with provider `lvms` in Fulfillment. It does
not install LVM Storage. For a separate workload cluster, the override applies
to the OSAC release on the management cluster, not an infrastructure release.
This registration setting is separate from the infrastructure chart's
`lvms.enabled` toggle that installs and configures LVM Storage. See the
[Helm Deployment Guide](helm-deployment-guide.md#how-toggles-affect-operands)
for the development/CI device allowlist and prerequisite lifecycle details.
This recipe does not extend the release's supported cluster layouts; retain
the [installation guide's limitations](customer-install-guide.md#94-not-supported).

Keep the OSAC instance's `lvms.enabled: false` in production. Storage
administrators prepare backend/tier registration and tenant storage
configuration through the [storage documentation](../../storage/README.md)
and [tenant onboarding guide](../../storage/guides/tenant-onboarding.md).

## VMaaS smoke test without provider networking

For an installation smoke test without a prepared fabric, set
`global.networking.provisioningEnabled: false` before the first install.
This skips provider jobs and does not verify VM networking or connectivity.
You cannot switch it to `true` later on the same deployment. Retain the
other VMaaS chart requirements described in the
[VMaaS installation workflow](customer-install-guide.md#61-installing-osac-for-vmaas).

## AgentlessNet VirtualNetwork namespace profile

AgentlessNet is not deployable on this branch: its chart entry renders no
registration that meets the network manager contract, so a NetworkClass cannot
select it. Adoption is a follow-up (OSAC-5533 tracks deployed coverage).
Existing inline CaaS workflows using `agentless_net.steps` are unchanged.

## The `make` wrapper fails with `[[: not found`

`/bin/sh` is `dash`, for example on Ubuntu or WSL. Run the target with
`make SHELL=/bin/bash`, or use the `helm` commands in the
[Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md#installing)
instead.
