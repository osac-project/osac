# Category B: Mirroring Core Components

- [Category B: Mirroring Core Components](#category-b-mirroring-core-components)
  - [Aim](#aim)
  - [Prerequisites](#prerequisites)
  - [Resources Required](#resources-required)
  - [ImageSetConfiguration Section](#imagesetconfiguration-section)
    - [Image Reference](#image-reference)
    - [Dependency Images (Phase 1a / Phase 1b)](#dependency-images-phase-1a--phase-1b)
    - [OSAC Core Component Images (Phase 2)](#osac-core-component-images-phase-2)
    - [Conditional Images - BMaaS](#conditional-images---bmaas)
    - [Metering Images](#metering-images)
  - [Execution Steps](#execution-steps)
    - [Step 1 — Mirror images (connected bastion)](#step-1--mirror-images-connected-bastion)
    - [Step 2 — Apply IDMS/ITMS (disconnected cluster)](#step-2--apply-idmsitms-disconnected-cluster)
    - [Step 3 - Deliver the OCI Helm chart to the disconnected site](#step-3---deliver-the-oci-helm-chart-to-the-disconnected-site)
    - [Step 4 - Continue to disconnected prerequisites and installation](#step-4---continue-to-disconnected-prerequisites-and-installation)
  - [Mirror Validation Commands](#mirror-validation-commands)
  - [Known Gaps / Limitations](#known-gaps--limitations)

## Aim

Mirror all OSAC dependency images and core component images into a
disconnected mirror registry using the `additionalImages` section of the
`oc-mirror` v2 `ImageSetConfiguration`. Installation procedures
are in [installation.md](installation.md).

> **Relationship to the main guide**: this page is Category B of the
> [OSAC Disconnected Mirroring Guide](README.md). It owns the single
> consolidated `additionalImages` inventory. It does not install Helm
> charts; installation belongs in [installation.md](installation.md).

## Prerequisites

| Check | Command / evidence |
| ----- | ------------------ |
| Category A handoff complete for the selected profile | Selected-tier package manifests are visible; installation.md later verifies selected-tier CSVs (Core=3, Core+VMaaS=6, Full-stack=7) |
| Mirror registry reachable from bastion and cluster nodes | `curl -sSf https://<registry>:8443/v2/ -k` |
| `oc-mirror` v2 available on bastion | `oc-mirror version` |
| `helm` CLI available on workstation (bastion laptop) | `helm version` |
| Workstation has internet access to `ghcr.io` | `curl -sSf https://ghcr.io/v2/` |
| SSH tunnel or sshuttle VPN from workstation to cluster API | `oc cluster-info` succeeds from workstation |
| `kubeconfig` configured for the disconnected cluster | `oc whoami --show-server` |

## Resources Required

| Resource | Minimum |
| -------- | ------- |
| Additional disk beyond operator mirroring | ~10 GiB for additional images |
| Network (connected side) | ~5 GiB download for core images |
| Bastion laptop | Internet + SSH access to cluster |

## ImageSetConfiguration Section

All `additionalImages` entries belong in the **single authoritative
ISC**.  Category pages contribute entries to this ISC but never
maintain a separate copy.  Every image reference below uses a **fully
qualified registry FQDN** — no short image names.

### Image Reference

The following images are included according to scope. The metering
*service* is required for every deployment; the M360 adapter is optional
and disabled by default. Version tags are specified in the
ImageSetConfiguration below:

| Image | Registry | Purpose |
| ------- | ---------- | --------- |
| `ghcr.io/openbao/openbao` | ghcr.io | Per-tenant secret storage (Vault-compatible) |
| `quay.io/jetstack/trust-manager` | quay.io | CA bundle distribution |
| `quay.io/jetstack/trust-pkg-debian-bookworm` | quay.io | Base trust-store package |
| `quay.io/keycloak/keycloak` | quay.io | Identity provider |
| `quay.io/openshift/origin-cli` | quay.io | Helm hook jobs and EE image builds |
| `quay.io/sclorg/postgresql-18-c10s` | quay.io | Primary relational database (digest-pinned) |
| `ghcr.io/osac-project/osac-operator` | ghcr.io | Core platform operator |
| `ghcr.io/osac-project/fulfillment-service` | ghcr.io | Resource lifecycle and Volume API |
| `ghcr.io/osac-project/envoy` | ghcr.io | OSAC Envoy build |
| `docker.io/envoyproxy/envoy` | docker.io | Upstream Envoy control-plane dependency |
| `ghcr.io/osac-project/osac-aap:0.0.45` | ghcr.io | AAP integration workload |
| `ghcr.io/osac-project/osac-ui` | ghcr.io | Web console |
| `ghcr.io/osac-project/bare-metal-fulfillment-operator` | ghcr.io | BMaaS fulfillment *(BMaaS only)* |
| `ghcr.io/osac-project/metering-service` | ghcr.io | Usage metering (mandatory) |
| `ghcr.io/osac-project/metering-m360-adapter` | ghcr.io | Metering M360 adapter (optional — mirror only if enabling M360 integration) |

### Dependency Images (Phase 1a / Phase 1b)

These images are consumed by the Phase 1a and Phase 1b shared services
such as trust management, identity, and databases.

```yaml
  additionalImages:
    # --- Phase 1 (Prerequisites) ---
    - name: ghcr.io/openbao/openbao:2.6.2
    - name: quay.io/jetstack/trust-manager:v0.20.0
    - name: quay.io/jetstack/trust-pkg-debian-bookworm:20230311-deb12u1.1
    - name: quay.io/keycloak/keycloak:26.6.4
    - name: quay.io/openshift/origin-cli:4.20.0
    - name: quay.io/sclorg/postgresql-18-c10s@sha256:6be2c9d855f06fb665257a6b0911676a38d740be7022cc61acee1c99a832b1b2
```

| Image | Version | Purpose |
| ----- | ------- | ------- |
| `openbao` | 2.6.2 | Per-tenant secret storage (Vault-compatible); hard runtime dependency |
| `trust-manager` | v0.20.0 | Distributes CA bundles across keycloak, osac, and postgres namespaces |
| `trust-pkg-debian-bookworm` | 20230311-deb12u1.1 | Base trust-store package consumed by trust-manager |
| `keycloak` | 26.6.4 | Identity provider; issues JWTs for all OSAC services |
| `origin-cli` | 4.20.0 | osac-aap EE image builds, osac-metering, kafka component of fulfillment-service |
| `postgresql-18-c10s` | (digest-pinned) | Primary relational database for fulfillment-service |

### OSAC Core Component Images (Phase 2)

Extract the tag of each sub-component from the released chart:

```bash
helm show values oci://ghcr.io/osac-project/charts/osac --version 0.0.62 > values-upstream.yaml
```

| Image | Tag | Condition |
| ----- | --- | --------- |
| `ghcr.io/osac-project/osac-operator` | `0.0.39` | Always |
| `ghcr.io/osac-project/fulfillment-service` | `0.0.131` | Always |
| `ghcr.io/osac-project/envoy` | `v1.33.0` | Always |
| `ghcr.io/osac-project/osac-aap` | `0.0.45` | Always |
| `ghcr.io/osac-project/osac-ui` | `0.0.30` | Always |
| `ghcr.io/osac-project/metering-service` | `0.0.27` | Always (mandatory) |
| `ghcr.io/osac-project/bare-metal-fulfillment-operator` | `0.0.38` | BMaaS only |

```yaml
    # --- OSAC core components (chart 0.0.62) ---
    - name: ghcr.io/osac-project/osac-operator:0.0.39
    - name: ghcr.io/osac-project/fulfillment-service:0.0.131
    - name: ghcr.io/osac-project/envoy:v1.33.0
    - name: docker.io/envoyproxy/envoy:v1.37.1
    - name: ghcr.io/osac-project/osac-aap:0.0.45
    - name: ghcr.io/osac-project/osac-ui:0.0.30
```

### Conditional Images - BMaaS

Include only when `global.services.bmaas.enabled: true`:

```yaml
    # --- BMaaS only (conditional: global.services.bmaas.enabled) ---
    - name: ghcr.io/osac-project/bare-metal-fulfillment-operator:0.0.38
```

### Metering Images

The metering **service** is mandatory for every deployment. The M360
**adapter** is optional and disabled by default
(`m360Adapter.enabled=false` in packaged values); mirror it only when
enabling M360 integration:

```yaml
    # --- OSAC metering service (mandatory) ---
    - name: ghcr.io/osac-project/metering-service:0.0.27
    # --- OSAC metering adapter (M360) — optional; uncomment when enabling M360 ---
    # - name: ghcr.io/osac-project/metering-m360-adapter:0.0.27
```

## Execution Steps

### Step 1 — Mirror images (connected bastion)

Include the `additionalImages` section in the unified `ImageSetConfiguration`
alongside the operators section from [mirroring-operators.md](mirroring-operators.md),
then run the canonical mirror command from
[mirroring-operators.md § Step 1](mirroring-operators.md#step-1--dry-run-connected-bastion)
(dry run first, then full mirror).

### Step 2 — Apply IDMS/ITMS (disconnected cluster)

```bash
# Apply or update the ImageDigestMirrorSet and ImageTagMirrorSet
oc apply -f oc-mirror-workspace/working-dir/cluster-resources/idms-oc-mirror.yaml
oc apply -f oc-mirror-workspace/working-dir/cluster-resources/itms-oc-mirror.yaml
```

Image resolution is handled transparently by the kubelet/CRI-O layer
using the IDMS and ITMS resources.  Pods reference upstream image names;
the node daemon resolves them to the mirror registry.

### Step 3 - Deliver the OCI Helm chart to the disconnected site

`oc-mirror` v2 supports mirroring Helm charts via the `mirror.helm`
configuration for charts stored locally or in a repository index.
However, the OSAC charts on `ghcr.io` use the OCI registry protocol,
which `oc-mirror` does not yet support for Helm:

- No OCI registry pull for Helm charts ([RFE-8713][rfe-8713])
- No values passthrough for Helm charts ([RFE-8748][rfe-8748])

Until RFE-8713 is resolved, use an internet-connected workstation
(bastion laptop) to pull OCI charts from `ghcr.io` for delivery to
the disconnected site.

<!-- Moved to installation.md §4.1 Preparing the cluster. See docs/guides/installation/disconnected/installation.md -->

[rfe-8713]: https://issues.redhat.com/browse/RFE-8713
[rfe-8748]: https://issues.redhat.com/browse/RFE-8748

```text
Connected workstation (OCI chart) --> approved artifact transfer --> disconnected chart staging
```

<!-- Moved to installation.md §4.1 Preparing the cluster. See docs/guides/installation/disconnected/installation.md -->

### Step 4 - Continue to disconnected prerequisites and installation

Doc 1 ends after mirror inventory and handoff verification. Continue to
[prerequisites and configuration](prereqs.md), then follow the
[OSAC Disconnected Installation Guide](installation.md). Do not install
operators, create operand resources, or install the OSAC chart from this
mirroring page.

## Mirror Validation Commands

```bash
# 1. Verify generated mirror policy resources are applied
oc get imagedigestmirrorset
oc get imagetagmirrorset
```

<!-- Moved to prereqs.md §3. Registry access and bounded configuration. See docs/guides/installation/disconnected/prereqs.md -->

<!-- Moved to installation.md §11.2 Other installation failures. See docs/guides/installation/disconnected/installation.md -->

Installed services, OSAC Deployments, and Helm releases are verified in
[installation.md](installation.md), not in Doc 1.

<!-- Moved to installation.md §11.2 Other installation failures. See docs/guides/installation/disconnected/installation.md -->
<!-- Moved to operators.md §6. Completion, Troubleshooting and Day 2. See docs/guides/installation/disconnected/operators.md -->
<!-- Moved to installation.md §11.2 Other installation failures. See docs/guides/installation/disconnected/installation.md -->

## Known Gaps / Limitations

1. **OCI Helm chart mirroring not supported** — `oc-mirror` v2 can
   mirror Helm charts from local or repository-index sources via
   `mirror.helm`, but cannot pull OCI-format charts from a registry
   (RFE-8713) or pass Helm values (RFE-8748).  The bastion laptop
   approach is the recommended workaround for OSAC's OCI charts.

2. **No `global.imageRegistry` value** — OSAC does not expose a global
   image registry override in its Helm values.  Disconnected image
   resolution relies entirely on IDMS/ITMS at the kubelet/CRI-O level.
   The only disconnected-specific Helm value is `cliImage` (scoped to
   the `osac` chart).  The mirrored CatalogSource must be named
   `redhat-operators` because operator Subscriptions reference that
   source name.

3. **PostgreSQL pinned by digest** — The PostgreSQL image is referenced
   by digest (`@sha256:...`) rather than tag.  This ensures exact version
   matching but requires the IDMS (not ITMS) to handle resolution.
