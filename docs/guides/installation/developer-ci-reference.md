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

All targets require `PLATFORM=kind|openshift PROFILE=dev|vmaas-ci|...|cudn-evpn-netris-test NS=<namespace>`.

```bash
make uninstall PLATFORM=openshift PROFILE=vmaas-ci NS=osac
```

## CI reference profiles

Each profile under `values/` has an `infra.yaml` and an `instance.yaml`, used
by CI and local dev, never for a real deployment:

| Profile | Use case |
|---------|----------|
| `values/vmaas-ci/` | VMaaS CI (compute instances) |
| `values/caas-ci/` | CaaS CI (cluster provisioning) |
| `values/bmaas-ci/` | BMaaS CI (bare metal) |
| `values/full-ci/` | All services enabled |
| `values/dev/` | Local dev (Kind) |
| `values/cudn-evpn-netris-test/` | Explicit CUDN EVPN + Netris VMaaS/BMaaS E2E profile (OpenShift only) |

## AgentlessNet resource-operation stub

To deploy with the unified Networking API selecting the AgentlessNet stub, apply
the overlay after the profile values:

```bash
make install-osac \
  PLATFORM=openshift \
  PROFILE=bmaas-ci \
  NS=<disposable-osac-namespace> \
  EXTRA_HELM_ARGS="-f values/agentless-net-stub.yaml"
```

The overlay selects `agentless_net` through `global.networking`, which registers
the fabric manager and creates a default NetworkClass that selects it. It also
allows the facade to derive the AAP backend for profiles that preserve their
existing AAP settings by default. NetworkClass registration can succeed
while networking resources fail. The twelve resource-operation entrypoints for
VirtualNetwork, Subnet, SecurityGroup, ExternalIPPool, ExternalIP, and
NATGateway deliberately return `NotImplemented` before provider-side work.
Existing operator retries, provisioning job history, finalizers, and failed
resource status continue to apply.

Physical attachment, DHCP lease discovery, and ExternalIPAttachment are outside
this stub and await their planned API/CRD changes. Existing inline CaaS
workflows using `agentless_net.steps` are unchanged. AAP must run the project
content and execution environment containing the AgentlessNet role; a stale
project revision will not contain the fail-fast entrypoints.

## The `make` wrapper fails with `[[: not found`

`/bin/sh` is `dash`, for example on Ubuntu or WSL. Run the target with
`make SHELL=/bin/bash`, or use the `helm` commands in the
[Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md#installing)
instead.
