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
