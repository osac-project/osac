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

## AgentlessNet VirtualNetwork namespace profile

To deploy the AgentlessNet VirtualNetwork namespace and forwarding baseline,
apply the overlay after the profile values:

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
existing AAP settings by default. A VirtualNetwork job requires one
authoritative network node under `all.children.net_nodes.hosts` in serialized
`AGENTLESS_NET_VN_INVENTORY` YAML/JSON from the existing `network-fulfillment-ig`
ConfigMap. The networking worker imports it through `envFrom`. Supply SSH
access through an AAP credential or `AGENTLESS_NET_SSH_PRIVATE_KEY` from the
`network-fulfillment-ig` Secret. The role creates the namespace, `/31` transit
link, and forwarding baseline.

Subnet, SecurityGroup, ExternalIPPool, ExternalIP, ExternalIPAttachment, and
NATGateway operations remain unsupported. The namespace profile does not
provide physical attachment, DHCP lease discovery, BGP, NAT, or external
connectivity or tenant `DefaultNetworkingReady`. Manager changes require draining
and replacing resources. Inventory configuration and the retained `/31`/SQLite
design differences are documented in the
[network backend guide](../../../osac-installer/docs/network-backend.md#agentlessnet-virtualnetwork-baseline).
Existing inline CaaS workflows using `agentless_net.steps` are
unchanged. AAP must run the project content and execution environment
containing the AgentlessNet role.

## The `make` wrapper fails with `[[: not found`

`/bin/sh` is `dash`, for example on Ubuntu or WSL. Run the target with
`make SHELL=/bin/bash`, or use the `helm` commands in the
[Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md#installing)
instead.
