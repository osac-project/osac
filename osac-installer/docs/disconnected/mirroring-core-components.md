# Category 1B: Mirroring Core Components

## Aim

Mirror all OSAC dependency images and core component images into a
disconnected mirror registry using the `additionalImages` section of the
`oc-mirror` v2 `ImageSetConfiguration`.  Then deploy OSAC using the
[customer install guide](../../../docs/guides/installation/customer-install-guide.md)
with disconnected values.

> **Relationship to the main guide**: this document expands on the
> `additionalImages` and Helm chart sections of
> [disconnected-install.md](../disconnected-install.md).

## Prerequisites

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
| ------- | ---------- | --------- |
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

These images are consumed by the Phase 1a and Phase 1b shared services
such as trust management, identity, and databases.

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
| `origin-cli` | 4.20 | osac-aap EE image builds, osac-metering, kafka component of fulfillment-service,osac-csi-driver |
| `postgresql-18-c10s` | (digest-pinned) | Primary relational database for fulfillment-service |

### OSAC Core Component Images (Phase 2)

The following command( value passed to version argument varies based upon the release ) gives the tag of each sub-component of the osac.
`helm show values oci://ghcr.io/osac-project/charts/osac --version 0.0.21 > values-upstream.yaml`

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

`oc-mirror` v2 supports mirroring Helm charts via the `mirror.helm`
configuration for charts stored locally or in a repository index.
However, the OSAC charts on `ghcr.io` use the OCI registry protocol,
which `oc-mirror` does not yet support for Helm:

- No OCI registry pull for Helm charts ([RFE-8713][rfe-8713])
- No values passthrough for Helm charts ([RFE-8748][rfe-8748])

Until RFE-8713 is resolved, use an internet-connected workstation
(bastion laptop) to pull OCI charts from `ghcr.io` and deploy via
SSH tunnel.

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

### Step 4 — Three-phase deployment

OSAC installation follows a strict three-phase ordering:

#### Phase 1a: Install operators and register CRDs

Install the seven operators from the mirrored `redhat-operators`
catalog following the official Red Hat documentation.  Namespaces,
channels, the AAP version pin, and the verification steps are in
[mirroring-operators.md § Step 4](mirroring-operators.md#step-4--install-the-operators).
The mirrored `CatalogSource` must be named `redhat-operators` in
`openshift-marketplace` (see
[mirroring-operators.md § Step 3](mirroring-operators.md#step-3--rename-the-catalogsource-to-redhat-operators)).

Wait for all operator CSVs to reach `Succeeded`:

```bash
oc get csv -A --no-headers | awk '{print $1, $2, $NF}'
```

#### Phase 1b: Create CRD instances and shared services

Create the prerequisite CRD instances and shared services that the
`osac` chart expects, following the official documentation for each
component.  The authoritative prerequisite list, with documentation
links per component, is
[Table 2.1 of the customer install guide](../../../docs/guides/installation/customer-install-guide.md#table-21-platform-operators-and-components).
In summary, this phase provisions:

- TLS: `default-ca` `ClusterIssuer`, CA certificates, the trust-manager
  `Bundle`, and the `ca-bundle` ConfigMap
- Shared services: Keycloak (with the `osac` realm) in `keycloak`,
  Kafka in `osac-kafka`, PostgreSQL, and OpenBao
- Operand CRs: `LVMCluster`, `HyperConverged`, MetalLB
  `IPAddressPool`/`L2Advertisement` — create each per its Red Hat
  product documentation
  ([LVM Storage](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html-single/storage/index#persistent-storage-using-lvms),
  [OpenShift Virtualization](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/virtualization/installing),
  [MetalLB](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/networking_operators/metallb-operator))
- Shared secrets: `fulfillment-controller-credentials`,
  `keycloak-client-secrets`, and the database secrets the `osac` chart
  expects (see the customer install guide prerequisites)

Readiness gates before Phase 2: the `default-ca` `ClusterIssuer`
reports ready, the `ca-bundle` ConfigMap is populated, Keycloak
answers on its route, and the operand CRs report healthy status.

#### Phase 2: `osac` — Deploy the OSAC platform

Install the supported `osac` chart with your disconnected values file
(see [Section 4.2 of the customer install
guide](../../../docs/guides/installation/customer-install-guide.md#42-configuring-the-helm-values)
for how to prepare it):

```bash
helm upgrade --install osac \
  oci://ghcr.io/osac-project/charts/osac --version 0.0.21 \
  --namespace osac --create-namespace \
  -f my-values.yaml \
  --set cliImage=<mirror-registry>:8443/openshift/origin-cli:4.20 \
  --set global.clusterDomain=$(oc get ingresses.config/cluster -o jsonpath='{.spec.domain}')
```

### Disconnected Helm Values Reference

The `osac` chart defines one disconnected-specific Helm value.  There
is no `global.imageRegistry` value; all other image resolution uses
IDMS/ITMS at the kubelet/CRI-O level.

| Value | Default | Disconnected override | Scope |
| ----- | ------- | --------------------- | ----- |
| `cliImage` | `quay.io/openshift/origin-cli:4.20.0` | Mirrored origin-cli image reference | `osac` |

`cliImage` backs the chart's hook Jobs (pre-install validation,
database initialization, hub registration, and other cluster-side
setup tasks).

> **CatalogSource naming**: operator Subscriptions reference
> `source: redhat-operators`, so the mirrored CatalogSource must use
> that name.  See
> [mirroring-operators.md § CatalogSource naming requirement](mirroring-operators.md#catalogsource-naming-requirement).

### Recommended installation order

Within each phase, apply resources in this order and verify readiness
before moving on:

1. Namespaces for the operators
2. `OperatorGroup` in each namespace
3. `Subscription` for each operator (`redhat-operators` source)
4. InstallPlan approval (only where `installPlanApproval: Manual`
   is used, e.g. the AAP version pin)
5. Readiness gates:
   - All seven operator CSVs report `Succeeded` — cert-manager first,
     since every Phase 1b certificate and CA resource depends on it
   - Phase 1b CRD instances and shared services are healthy before
     installing the `osac` chart

## Validation Commands

```bash
# 1. Verify IDMS and ITMS are applied
oc get imagedigestmirrorset
oc get imagetagmirrorset

# 2. Verify dependency images resolve via ITMS
oc debug node/<node-name> -- chroot /host \
  crictl pull quay.io/jetstack/trust-manager:v0.20.0

# 3. Verify Phase 1b services
oc get clusterissuer default-ca
oc get pods -n keycloak -l app=keycloak
oc get pods -n osac-kafka
# PostgreSQL and OpenBao: verify the pods and services in the
# namespaces your Phase 1b deployment uses

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
| Phase 1b hooks fail with `CRD not found` | Phase 1a operators not fully ready | Wait for all 7 CSVs to reach `Succeeded` before creating the Phase 1b CRD instances |
| Keycloak pod in CrashLoopBackOff | PostgreSQL not ready or CA bundle not synced | Check trust-manager Bundle status; verify PostgreSQL StatefulSet |

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
