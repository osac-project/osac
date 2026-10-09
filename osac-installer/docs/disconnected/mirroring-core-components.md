# Category 1B: Mirroring Core Components

## Aim

Mirror all OSAC dependency images and core component images into a
disconnected mirror registry using the `additionalImages` section of the
`oc-mirror` v2 `ImageSetConfiguration`.  Then deploy OSAC via the bastion
laptop workflow using the three-phase Helm architecture with disconnected
values.

> **Relationship to the main guide**: this document expands on the
> `additionalImages` and Helm chart sections of
> [disconnected-install.md](../disconnected-install.md).

## Prerequisites (TDD: define checks first)

| Check | Command / evidence |
| ----- | ------------------ |
| Category 1A complete: all 7 operators installed, CRDs registered | `oc get csv -A \| grep Succeeded` shows 7 CSVs |
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

### Image Reference

The following images are required (version tags are specified in the
ImageSetConfiguration below):

| Image | Registry | Purpose |
|-------|----------|---------|
| `ghcr.io/openbao/openbao` | ghcr.io | Per-tenant secret storage (Vault-compatible) |
| `quay.io/jetstack/trust-manager` | quay.io | CA bundle distribution |
| `quay.io/jetstack/trust-pkg-debian-bookworm` | quay.io | Base trust-store package |
| `quay.io/keycloak/keycloak` | quay.io | Identity provider |
| `quay.io/openshift/origin-cli` | quay.io | Helm hook jobs and EE image builds |
| `quay.io/sclorg/postgresql-18-c10s` | quay.io | Primary relational database (digest-pinned) |
| `ghcr.io/osac-project/osac-operator` | ghcr.io | Core platform operator |
| `ghcr.io/osac-project/fulfillment-service` | ghcr.io | Resource lifecycle and Volume API |
| `ghcr.io/osac-project/envoy` | ghcr.io | API gateway / proxy |
| `ghcr.io/osac-project/osac-aap` | ghcr.io | AAP integration component |
| `ghcr.io/osac-project/osac-ui` | ghcr.io | Web console |
| `ghcr.io/osac-project/bare-metal-fulfillment-operator` | ghcr.io | BMaaS fulfillment |
| `ghcr.io/osac-project/metering-service` | ghcr.io | Usage metering (conditional) |
| `ghcr.io/osac-project/metering-echo-adapter` | ghcr.io | Metering echo adapter (conditional) |
| `ghcr.io/osac-project/metering-m360-adapter` | ghcr.io | Metering M360 adapter (conditional) |
| `ghcr.io/osac-project/osac-csi-driver` | ghcr.io | CSI volume driver (conditional) |
| `registry.k8s.io/sig-storage/csi-provisioner` | registry.k8s.io | CSI sidecar (conditional) |
| `registry.k8s.io/sig-storage/csi-attacher` | registry.k8s.io | CSI sidecar (conditional) |
| `registry.k8s.io/sig-storage/csi-node-driver-registrar` | registry.k8s.io | CSI sidecar (conditional) |

### Dependency Images (Phase 1a / Phase 1b)

These images are consumed by the `osac-deps` and `osac-infra` Helm charts
for shared services such as trust management, identity, and databases.

```yaml
  additionalImages:
    # --- Phase 1 (Prerequisites) ---
    - name: ghcr.io/openbao/openbao:2.6.2
    - name: quay.io/jetstack/trust-manager:v0.20.0
    - name: quay.io/jetstack/trust-pkg-debian-bookworm:20230311-deb12u1.1
    - name: quay.io/keycloak/keycloak:26.6.4
    - name: quay.io/openshift/origin-cli:4.20
    - name: quay.io/sclorg/postgresql-18-c10s@sha256:6be2c9d855f06fb665257a6b0911676a38d740be7022cc61acee1c99a832b1b2
```

| Image | Version | Purpose |
| ----- | ------- | ------- |
| `openbao` | 2.6.2 | Per-tenant secret storage (Vault-compatible); hard runtime dependency |
| `trust-manager` | v0.20.0 | Distributes CA bundles across keycloak, osac, and postgres namespaces |
| `trust-pkg-debian-bookworm` | 20230311-deb12u1.1 | Base trust-store package consumed by trust-manager |
| `keycloak` | 26.6.4 | Identity provider; issues JWTs for all OSAC services |
| `origin-cli` | 4.20 | Used by osac-infra Helm hook jobs and osac-aap EE image builds |
| `postgresql-18-c10s` | (digest-pinned) | Primary relational database for fulfillment-service |

### OSAC Core Component Images (Phase 2)

```yaml
    # --- OSAC core components ---
    - name: ghcr.io/osac-project/osac-operator:0.0.18
    - name: ghcr.io/osac-project/fulfillment-service:0.0.111
    - name: ghcr.io/osac-project/envoy:v1.33.0
    - name: ghcr.io/osac-project/osac-aap:0.0.19
    - name: ghcr.io/osac-project/osac-ui:0.0.10
    - name: ghcr.io/osac-project/bare-metal-fulfillment-operator:0.0.18
```

| Image | Version | Purpose |
| ----- | ------- | ------- |
| `osac-operator` | 0.0.18 | Core platform operator |
| `fulfillment-service` | 0.0.111 | Resource lifecycle, Volume API, storage management |
| `envoy` | v1.33.0 | API gateway / proxy |
| `osac-aap` | 0.0.19 | AAP integration component |
| `osac-ui` | 0.0.10 | Web console |
| `bare-metal-fulfillment-operator` | 0.0.18 | BMaaS fulfillment |

### Conditional Images — Metering

Include only when `metering.enabled=true`:

```yaml
    # --- OSAC metering (conditional: metering.enabled) ---
    - name: ghcr.io/osac-project/metering-service:0.0.7
    - name: ghcr.io/osac-project/metering-echo-adapter:0.0.7
    - name: ghcr.io/osac-project/metering-m360-adapter:0.0.7
```

### Conditional Images — CSI Driver

Include only when `csiDriver.enabled=true`:

```yaml
    # --- OSAC CSI driver (conditional: csiDriver.enabled) ---
    - name: ghcr.io/osac-project/osac-csi-driver:0.0.7
    # --- CSI sidecars ---
    - name: registry.k8s.io/sig-storage/csi-provisioner:v5.1.0
    - name: registry.k8s.io/sig-storage/csi-attacher:v4.7.0
    - name: registry.k8s.io/sig-storage/csi-node-driver-registrar:v2.12.0
```

## Execution Steps

### Step 1 — Mirror images (connected bastion)

Include the `additionalImages` section in the unified `ImageSetConfiguration`
alongside the operators section from [mirroring-operators.md](mirroring-operators.md),
then run:

```bash
oc-mirror --v2 --config=imageset-config.yaml --workspace \
  file://$(pwd)/oc-mirror-workspace docker://<mirror-registry>:8443/
```

### Step 2 — Apply IDMS/ITMS (disconnected cluster)

```bash
# Apply or update the ImageDigestMirrorSet and ImageTagMirrorSet
oc apply -f oc-mirror-workspace/working-dir/cluster-resources/idms-oc-mirror.yaml
oc apply -f oc-mirror-workspace/working-dir/cluster-resources/itms-oc-mirror.yaml
```

Image resolution is handled transparently by the kubelet/CRI-O layer
using the IDMS and ITMS resources.  Pods reference upstream image names;
the node daemon resolves them to the mirror registry.

### Step 3 — Deploy Helm charts via bastion laptop

Helm charts **cannot be mirrored by `oc-mirror`** due to two missing
features:

- No `oci:` protocol support ([RFE-8713][rfe-8713])
- No values passthrough for Helm charts ([RFE-8748][rfe-8748])

Instead, use an internet-connected workstation (bastion laptop) to pull
charts from `ghcr.io` and deploy via SSH tunnel.

[rfe-8713]: https://issues.redhat.com/browse/RFE-8713
[rfe-8748]: https://issues.redhat.com/browse/RFE-8748

```text
┌──────────────────┐     SSH tunnel     ┌───────────────┐     internal     ┌─────────────────────┐
│ Workstation      │ ──────────────────> │ Bastion Host  │ ──────────────> │ Disconnected OCP    │
│ (Internet)       │                    │ (DMZ)         │                 │ + Mirror Registry   │
│ • helm CLI       │                    │               │                 │                     │
│ • kubeconfig     │                    │               │                 │                     │
└──────────────────┘                    └───────────────┘                 └─────────────────────┘
```

### Step 4 — Three-phase Helm deployment

OSAC installation follows a strict three-phase ordering:

#### Phase 1a: `osac-deps` — Install operators and register CRDs

```bash
helm upgrade --install osac-deps \
  oci://ghcr.io/osac-project/charts/osac-deps --version 0.0.21 \
  --namespace osac-deps --create-namespace \
  --set catalogSource=cs-redhat-operator-index-v4-22 \
  --set catalogSourceNamespace=openshift-marketplace \
  --set cliImage=<mirror-registry>:8443/openshift/origin-cli:4.20
```

Wait for all operator CSVs to reach `Succeeded`:

```bash
oc get csv -A --no-headers | awk '{print $1, $2, $NF}'
```

#### Phase 1b: `osac-infra` — Create CRD instances and shared services

```bash
helm upgrade --install osac-infra \
  oci://ghcr.io/osac-project/charts/osac-infra --version 0.0.21 \
  --namespace osac-infra --create-namespace \
  -f osac-installer/values/disconnected-dev/infra.yaml \
  --set cliImage=<mirror-registry>:8443/openshift/origin-cli:4.20
```

This phase creates:

- TLS: `ClusterIssuer`, CA certificates, trust-manager `Bundle`, `ca-bundle` ConfigMaps
- Shared services: Keycloak (+ osac realm), PostgreSQL, OpenBao
- Operand CRs: `LVMCluster`, `HyperConverged`, MetalLB `IPAddressPool`/`L2Advertisement`, `Kafka`
- Shared secrets: `fulfillment-controller-credentials`, `keycloak-client-secrets`

#### Phase 2: `osac` — Deploy the OSAC platform

```bash
helm upgrade --install osac \
  oci://ghcr.io/osac-project/charts/osac --version 0.0.21 \
  --namespace osac --create-namespace \
  -f osac-installer/values/disconnected-dev/osac.yaml
```

### Disconnected Helm Values Reference

The following Helm values control disconnected behavior.  These are the
**only** disconnected-specific overrides; there is no `global.imageRegistry`
value.

| Value | Default | Disconnected override | Scope |
| ----- | ------- | --------------------- | ----- |
| `catalogSource` | `redhat-operators` | Name of the mirrored `CatalogSource` CR (e.g. `cs-redhat-operator-index-v4-22`) | `osac-deps` |
| `catalogSourceNamespace` | `openshift-marketplace` | Namespace of the `CatalogSource` | `osac-deps` |
| `cliImage` | `quay.io/openshift/origin-cli:4.20.0` | Mirrored origin-cli image reference | `osac-deps`, `osac-infra` |

> **Important**: Image resolution for all other images uses IDMS/ITMS at the
> kubelet/CRI-O level.  Pods reference their upstream image names and the
> node daemon transparently rewrites them to the mirror registry.

### Helm Weight Ordering (osac-deps)

Manual installation order within `osac-deps`:

1. Namespaces (8 resources)
2. ConfigMaps (hook scripts)
3. OperatorGroups (7 resources)
4. Subscriptions (7 resources)
5. Post-install hooks in weight order:
   - Weight 5 → `wait-cert-manager` (gated on `certManager.enabled`)
   - Weight 14 → subsequent dependency waits
   - Weight 15 → final dependency readiness

Within each hook weight: `ServiceAccount` → `ClusterRole`/`Role` →
`ClusterRoleBinding`/`RoleBinding` → `Job`

## Verification (BDD Scenarios)

```gherkin
Feature: Core component mirroring and deployment for disconnected OSAC

  Background:
    Given a disconnected OpenShift 4.22 cluster
    And all 7 OLM operators are installed (Category 1A complete)
    And a mirror registry populated with OSAC additional images
    And IDMS and ITMS resources are applied to the cluster

  Scenario: All dependency images are accessible from cluster nodes
    When a cluster node pulls "trust-manager:v0.20.0"
    Then the image resolves via ITMS to the mirror registry
    And the pull succeeds without error

  Scenario: Phase 1a deploys operators with mirrored catalog
    Given the osac-deps chart is installed with catalogSource override
    When I check Subscription sources
    Then no Subscription references "redhat-operators"
    And all Subscriptions reference the mirrored CatalogSource

  Scenario: Phase 1b creates shared services
    Given the osac-infra chart is installed
    When I check the osac-infra namespace
    Then the Keycloak deployment is available
    And the PostgreSQL StatefulSet is ready
    And the OpenBao StatefulSet is ready
    And the trust-manager Bundle is synced

  Scenario: Phase 2 deploys OSAC platform components
    Given the osac chart is installed
    When I list deployments in the osac namespace
    Then "osac-operator" deployment has available replicas > 0
    And "fulfillment-service" deployment has available replicas > 0
    And "envoy" deployment has available replicas > 0
    And "osac-ui" deployment has available replicas > 0

  Scenario: No image pull errors occur on disconnected nodes
    When I check events across all OSAC namespaces
    Then there are no events with reason "ImagePullBackOff"
    And there are no events with reason "ErrImagePull"

  Scenario: Helm charts are not mirrored via oc-mirror
    Given the bastion laptop has internet access
    When the operator installs Helm charts
    Then charts are pulled directly from ghcr.io via the workstation
    And rendered manifests are submitted to the cluster API
```

## Validation Commands

```bash
# 1. Verify IDMS and ITMS are applied
oc get imagedigestmirrorset
oc get imagetagmirrorset

# 2. Verify dependency images resolve via mirror
oc debug node/<node-name> -- chroot /host \
  crictl pull <mirror-registry>:8443/jetstack/trust-manager:v0.20.0

# 3. Verify Phase 1b services
oc get pods -n keycloak -l app=keycloak
oc get pods -n osac-postgres
oc get pods -n osac-vault

# 4. Verify Phase 2 deployments
oc get deployments -n osac

# 5. Check for image pull errors across all namespaces
oc get events -A --field-selector reason=Failed \
  -o custom-columns='NAMESPACE:.metadata.namespace,MESSAGE:.message' \
  | grep -i "pull"

# 6. Verify Helm releases
helm list -A | grep osac
```

## Troubleshooting

| Symptom | Likely cause | Resolution |
| ------- | ------------ | ---------- |
| `ImagePullBackOff` on dependency pods | IDMS/ITMS not applied or image tag mismatch | Verify `oc get itms -o yaml` includes the image; re-run `oc-mirror` |
| Helm install fails with `connection refused` | SSH tunnel not established to cluster API | Verify sshuttle/SSH tunnel; check `oc cluster-info` from workstation |
| `origin-cli` image not found | `cliImage` Helm value not overridden | Pass `--set cliImage=<mirror-registry>:8443/openshift/origin-cli:4.20` |
| Phase 1b hooks fail with `CRD not found` | Phase 1a operators not fully ready | Wait for all 7 CSVs to reach `Succeeded` before installing osac-infra |
| Keycloak pod in CrashLoopBackOff | PostgreSQL not ready or CA bundle not synced | Check trust-manager Bundle status; verify PostgreSQL StatefulSet |

## Known Gaps / Limitations

1. **Helm chart mirroring not supported** — `oc-mirror` cannot mirror Helm
   charts due to missing OCI protocol support (RFE-8713) and missing values
   passthrough (RFE-8748).  The bastion laptop approach is the recommended
   workaround.

2. **No `global.imageRegistry` value** — OSAC does not expose a global
   image registry override in its Helm values.  Disconnected image
   resolution relies entirely on IDMS/ITMS at the kubelet/CRI-O level.
   The only disconnected-specific Helm values are `catalogSource`,
   `catalogSourceNamespace`, and `cliImage`.

3. **PostgreSQL pinned by digest** — The PostgreSQL image is referenced
   by digest (`@sha256:...`) rather than tag.  This ensures exact version
   matching but requires the IDMS (not ITMS) to handle resolution.
