# OSAC Helm Deployment Guide

Deploy OSAC on a clean connected OpenShift cluster using the three-phase
Helm install. This guide is for setting up the prerequisite Operators and
infrastructure layer (phase 1) from a monorepo checkout. If your cluster
already has the prerequisites, see the
[customer install guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/customer-install-guide.md)
to install just the published `osac` chart.

## Requirements

| Requirement | Details |
|-------------|---------|
| OpenShift | 4.17+ with cluster-admin access |
| CLI tools | `oc`, `helm`, `git`, `make` |
| Network | Outbound access to github.com, ghcr.io, quay.io, registry.redhat.io |
| AAP license | Subscription manifest (`license.zip`) from [Red Hat Customer Portal](https://access.redhat.com/) |

## Quick Start

```bash
git clone https://github.com/osac-project/osac.git
cd osac
git checkout osac/v0.0.17   # pin to a tagged release; HEAD skips phase-compatibility testing
cd osac-installer
make helm-deps

# Place your AAP license file
cp /path/to/license.zip values/vmaas-ci/

# Install (infra + osac)
make install PLATFORM=openshift PROFILE=vmaas-ci NS=osac
```

## How It Works

OSAC installs in three phases. Each phase's outputs are the next phase's
inputs. This is required because Helm validates all templates before applying
any — a template that references a CRD must have that CRD already registered
on the cluster.

### `make install-infra`

Installs infrastructure in two Helm releases:

1. **osac-deps** — OLM operator Subscriptions (cert-manager, AAP, LVMS, MetalLB,
   CNV, MCE). Post-install hooks wait for each operator's CSV to succeed and CRDs
   to register. Each operator is gated by a values toggle.

2. **osac-infra** — CRD instances: CertManager CR, ClusterIssuer, CA certificates,
   trust-manager Bundle, Keycloak, LVMCluster, HyperConverged, MetalLB IPAddressPool,
   controller credentials, and bundled PostgreSQL (dev/CI only).

### `make install-osac`

Installs OSAC: operator, fulfillment-service, AAP bootstrap, UI. All
prerequisites are ready - certificates issued, secrets created, CRDs
registered.

### Post-Install Hooks

After Phase 3, Helm runs post-install (and post-upgrade) hooks that
finalize the deployment:

| Hook | Weight | What it does |
|------|--------|-------------|
| `osac-publish-templates` | 20 | Publishes cluster templates to the fulfillment service catalog |

**Cluster template publishing** (`osac-publish-templates`): An init
container waits for the fulfillment REST gateway to be healthy (up to
600s), then launches the `osac-publish-templates` AAP job template and
polls until it completes. Helm blocks until this hook succeeds, so
`helm install` and `helm upgrade` will not report success until cluster
templates are available. The underlying Ansible role uses a PATCH/POST
pattern, making re-publish on upgrade safe and idempotent.

This hook is enabled by default (`aap.instanceGroups.publishTemplates.enabled: true`).
To disable it (e.g., in environments without CaaS):

```yaml
aap:
  instanceGroups:
    publishTemplates:
      enabled: false
```

## Manual Installation (without make)

`make install` wraps the commands below with the CI reference profiles
(`values/<profile>/infra.yaml` and `instance.yaml`). To install by hand —
against your own values files instead of a `values/<profile>/` directory —
run the three phases directly:

```bash
export NS=osac
export DOMAIN=$(oc get ingresses.config/cluster -o jsonpath='{.spec.domain}')
export OCP_VERSION=$(oc get clusterversion version -o jsonpath='{.status.desired.version}' | cut -d. -f1,2)
export AAP_LICENSE_FILE=/path/to/license.zip

oc create namespace "$NS" --dry-run=client -o yaml | oc apply -f -
oc create secret generic config-as-code-manifest-ig --from-file=license.zip="$AAP_LICENSE_FILE" -n "$NS" --dry-run=client -o yaml | oc apply --server-side -f -
oc label secret config-as-code-manifest-ig osac.openshift.io/project=osac-aap -n "$NS" --overwrite

# Phase 1a: prerequisite Operator Subscriptions
helm upgrade --install osac-deps ./charts/osac-deps \
    -n osac-deps --create-namespace \
    -f my-infra-values.yaml \
    --set lvms.channel="stable-$OCP_VERSION" \
    --wait --timeout 30m
```

To check a specific Operator, for example LVMS:

```bash
oc get csv -n openshift-storage lvms-operator.v4.22.0
oc get sub lvms-operator -n openshift-storage -o jsonpath='channel={.spec.channel} state={.status.state}{"\n"}'
```

```bash
# Phase 1b: CA issuer, trust-manager, Keycloak, and operand CRs
helm upgrade --install osac-infra ./charts/osac-infra \
    -n osac-infra --create-namespace \
    -f my-infra-values.yaml \
    --set osacNamespace="$NS" \
    --set lvms.channel="stable-$OCP_VERSION" \
    --set keycloak.hostname="https://keycloak-keycloak.$DOMAIN" \
    --set keycloak.route.hostname="keycloak-keycloak.$DOMAIN" \
    --wait-for-jobs --timeout 30m

# Phase 2: the OSAC platform itself
helm dependency update ./charts/osac
helm upgrade --install osac ./charts/osac \
    -n "$NS" --create-namespace \
    -f my-values.yaml \
    --set global.clusterDomain="$DOMAIN" \
    --set service.externalHostname="fulfillment-api-$NS.$DOMAIN" \
    --set service.internalHostname="fulfillment-internal-api-$NS.$DOMAIN" \
    --wait --timeout 40m
```

`my-infra-values.yaml` configures `osac-deps` and `osac-infra` — see
[Phase-1 Parameters](#phase-1-parameters) below. For the phase-2
`my-values.yaml` (per-service configuration, the full parameter reference,
and verification), see the
[customer install guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/customer-install-guide.md).

## Phase-1 Parameters

The same values file (`my-infra-values.yaml`) is passed to both `osac-deps`
and `osac-infra`. Keys a chart doesn't recognize are ignored.

| Parameter | Description | Default |
|---|---|---|
| `certManager.enabled` | Creates the cert-manager Operator `Subscription` and the CA and trust resources. | `true` |
| `certManager.channel` | Update channel for `openshift-cert-manager-operator`. | `stable-v1` |
| `trustManager.enabled` | Deploys the vendored trust-manager. | `true` |
| `trustManager.upstream.enabled` | Uses an already-installed trust-manager instead of the vendored one. | `false` |
| `caIssuer.enabled` | Creates the `default-ca` `ClusterIssuer` and the `ca-bundle` `ConfigMap`. | `true` |
| `aapOperator.enabled` | Creates the AAP Operator `Subscription`. | `true` |
| `aapOperator.channel` | Update channel for the AAP Operator. | `stable-2.6-cluster-scoped` |
| `cnv.enabled` | Creates the OpenShift Virtualization `Subscription` and the `HyperConverged` custom resource. Required for VMaaS. | `false` |
| `cnv.channel` | Update channel for OpenShift Virtualization. | `stable` |
| `lvms.enabled` | Creates the LVM Storage `Subscription` and the `LVMCluster` custom resource. | `false` |
| `lvms.channel` | Update channel for LVM Storage. Set it to `stable-<cluster_minor>` at installation. | `stable-4.22` |
| `metallb.enabled` | Creates the MetalLB `Subscription`, the `caas-address-pool` `IPAddressPool`, and the `L2Advertisement`. | `false` |
| `metallb.channel` | Update channel for MetalLB. | `stable` |
| `mce.enabled` | Creates the multicluster engine `Subscription` and the agent configuration. Required for CaaS. | `false` |
| `mce.channel` | Update channel for multicluster engine. | `stable-2.17` |
| `mce.osImages` | RHCOS live-ISO entries for agent discovery. | `[]` |
| `kafka.enabled` | Creates the Streams for Apache Kafka `Subscription` and the Kafka custom resource. Required for metering. | `false` |
| `kafka.replicas` | Kafka broker replica count. | `3` |
| `kafka.storage.size` | Kafka broker storage size. | `100Gi` |
| `kafka.version` | Kafka version. | `4.2.0` |
| `kafka.metadataVersion` | Kafka metadata version. | `4.2-IV0` |
| `keycloak.enabled` | Deploys the bundled Keycloak and the `osac` realm in the `keycloak` namespace. | `true` |
| `keycloak.adminUsername` | Keycloak bootstrap admin user name. Change this value. | `admin` |
| `keycloak.adminPassword` | Keycloak bootstrap admin password. Change this value. | `admin` |
| `keycloak.defaultUserPassword` | Password seeded for the built-in realm users. | `foobar` |
| `keycloak.devFixtures.enabled` | Seeds fixed, known passwords for the built-in test users. Must be `false` outside of evaluation. | `false` |
| `keycloak.route.hostname` | External route host name for Keycloak. The installation sets it. | `""` |
| `keycloak.route.publicIngress` | Changes the Keycloak route from `passthrough` to `reencrypt` for clusters with publicly trusted ingress certificates. See [Infrastructure Configuration](#infrastructure-configuration). | `false` |
| `keycloak.realmOverwrite` | Re-imports the realm definition on upgrade. | `true` |
| `keycloak.images.keycloak` | Keycloak image. | `keycloak:26.6.4` |
| `keycloak.images.postgres` | Keycloak database image. | `postgresql-18-c10s` |
| `osacNamespace` | Namespace that the `osac` platform release uses. `osac-infra` stamps cross-namespace resources with it. Set it to your namespace. | `osac` |
| `csiNamespace` | Namespace that the CSI driver subchart expects. | `osac-csi` |
| `csiReleaseName` | Release name that the CSI driver subchart expects. | `osac` |
| `bundledPostgres.enabled` | Deploys an ephemeral in-cluster PostgreSQL database. Set it to `false` for production. | `false` |
| `bundledPostgres.database.name` | Bundled database name. | `service` |
| `bundledPostgres.database.user` | Bundled database owner. | `service` |
| `cliImage` | The `oc` image that the chart hook jobs use. | `origin-cli:4.20.0` |

## When Prerequisites Already Exist

If some or all of the prerequisite Operators and infrastructure already exist
on the cluster, set the corresponding toggle to `false` in
`my-infra-values.yaml` and run the phase-1 installs anyway — they're
idempotent, so running them with most toggles `false` is safe. If every
component listed below already exists, skip phase 1 entirely and install only
the `osac` chart — see the
[customer install guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/customer-install-guide.md).

```yaml
certManager:  { enabled: false }
trustManager: { enabled: false }
aapOperator:  { enabled: false }
cnv:          { enabled: false }
lvms:         { enabled: false }
metallb:      { enabled: false }
mce:          { enabled: false }
```

### How Toggles Affect Operands

- Even with `certManager.enabled: false`, the pre-installation validation hook
  requires the `certificates.cert-manager.io` CRD. Any cert-manager
  distribution satisfies it.
- Disabling `cnv.enabled` skips the `HyperConverged` custom resource.
  Disabling `lvms.enabled` skips an `LVMCluster` custom resource that uses
  device class `vg1`, thin pool size 90 percent, and overprovision ratio 10.
  Disabling `metallb.enabled` skips an `IPAddressPool` custom resource named
  `caas-address-pool` with the fixed range `192.168.100.240` to
  `192.168.100.250`. If you already run the Operator, keep the toggle `false`
  and create your own operand. Edit the `IPAddressPool` after installation to
  use an address range that is valid for your network.
- `caIssuer.enabled: false` requires you to provide a `ClusterIssuer` and set
  `service.certs.issuerRef` in the phase-2 `my-values.yaml`.
- `keycloak.enabled: false` requires a pre-configured Keycloak with the `osac`
  realm, clients, and roles already set up.

On a shared cluster, do not change cluster-scoped prerequisites without the
agreement of the cluster owner.

### Skipping Phase 1 Entirely

Safe only when the cluster already provides everything that the two phase-1
charts create and the `osac` release depends on.

**Phase 1a provides:**

- The `certificates.cert-manager.io` CRD.
- The AAP Operator.
- The service Operators you need: OpenShift Virtualization, LVM Storage,
  MetalLB, multicluster engine, and Streams for Apache Kafka.

**Phase 1b provides:**

- The `default-ca` `ClusterIssuer`.
- trust-manager and the shared `ca-bundle` `ConfigMap`.
- Keycloak with the `osac` realm.
- The `fulfillment-controller-credentials` and `keycloak-client-secrets`
  Secrets, which an `osac-infra` postinstallation hook creates.
- An operand custom resource for each Operator you use.
- For a production deployment, an external PostgreSQL database with the
  `osac-db-*` Secrets.

If any of these is missing, run the phase-1 installations with the matching
toggles set to `false`.

## Values Files

Each profile has two files: `infra.yaml` (infrastructure config) and `instance.yaml` (OSAC instance config).

| Profile | Use case |
|---------|----------|
| `values/vmaas-ci/` | VMaaS CI (compute instances) |
| `values/caas-ci/` | CaaS CI (cluster provisioning) |
| `values/bmaas-ci/` | BMaaS CI (bare metal) |
| `values/dev/` | Local dev (Kind) |

Copy and customize for your environment:

```bash
mkdir -p values/my-env
cp values/dev/infra.yaml values/my-env/
cp values/dev/instance.yaml values/my-env/
# Edit to match your cluster
```

Key settings:

| Setting | Description |
|---------|-------------|
| `service.externalHostname` | Required. Set automatically by `make install-osac`. |
| `service.internalHostname` | Required. Set automatically by `make install-osac`. |
| `service.auth.issuerUrl` | Keycloak realm URL (default works for in-cluster Keycloak) |
| `operator.controllers.*` | Enable/disable individual controllers |

## CI/Dev-Only Features

These values control bundled dev/CI services. Disable in production.

### Infra chart (`osac-infra`) values

| Value | Default | What it does |
|-------|---------|-------------|
| `bundledPostgres.enabled` | `false` | Deploys a single-pod ephemeral PostgreSQL. Uses `fsync=off` and `emptyDir` — data lost on restart. Not for production. |
| `bundledVault.enabled` | `true` | Deploys a single-pod ephemeral OpenBao (Vault-compatible) secret store in the `osac-infra` namespace. Dev mode — data is lost on restart. Not for production. The OSAC instance chart connects via FQDN (`openbao.osac-infra.svc.cluster.local`). |

### Instance chart (`osac`) values

| Value | Default | What it does |
|-------|---------|-------------|
| `hubAccess.enabled` | `false` | Creates hub-access SA/RBAC and registers local cluster as a hub. Only for environments where fulfillment-service and hub are the same cluster. |

## Infrastructure Configuration

### Keycloak Route with Public Ingress Certificates

For clusters with publicly-trusted wildcard ingress certificates (e.g., production OpenShift clusters with Let's Encrypt), you can configure Keycloak's Route to use `reencrypt` termination instead of `passthrough`:

```yaml
# values/<profile>/infra.yaml
keycloak:
  route:
    publicIngress: true  # Switches Route from passthrough to reencrypt
    hostname: keycloak.apps.example.com  # Optional: custom hostname
```

**How it works:**
- `passthrough` (default): Browser connects directly to Keycloak's internal TLS cert (self-signed CA)
- `reencrypt`: Router presents the cluster's public ingress cert to browsers, then re-encrypts traffic to Keycloak's internal TLS endpoint using the CA cert

**Requirements:**
- `caIssuer.enabled: true` (default) — The hook depends on cert-manager's CA bundle
- The `osac-infra-patch-keycloak-route` hook Job automatically sets `destinationCACertificate` at install/upgrade time

**When to use:**
- Production clusters where browser cert warnings are unacceptable
- Environments with corporate CA or Let's Encrypt ingress certs

## Makefile Targets

All targets require `PLATFORM=kind|openshift PROFILE=dev|vmaas-ci|... NS=<namespace>`.

| Target | Description |
|--------|-------------|
| `make install` | Full install (infra + osac) |
| `make install-infra` | Infrastructure only (osac-deps + osac-infra) |
| `make install-osac` | OSAC instance only |
| `make uninstall` | Full uninstall (reverse order) |
| `make test` | Run integration tests (SUITE= required) |
| `make helm-lint` | Lint all charts |

## Uninstall

```bash
make uninstall PLATFORM=openshift PROFILE=vmaas-ci NS=osac
```

Without `make`, uninstall in reverse phase order:

```bash
helm uninstall osac -n "$NS"
helm uninstall osac-infra -n osac-infra
helm uninstall osac-deps -n osac-deps
```

The CRDs are retained (`helm.sh/resource-policy: keep`). To remove them:

```bash
oc delete crd -l app.kubernetes.io/part-of=osac
```

The repository script `osac-installer/scripts/teardown.sh` also removes the
prerequisite Operators and their namespaces. Do not run it on a shared
cluster. For more information, see "Tearing Down OSAC" in
[`osac-installer/README.md`](https://github.com/osac-project/osac/blob/main/osac-installer/README.md).

## Troubleshooting

### AAP Bootstrap Failing

```bash
oc logs -f job/osac-aap-bootstrap -n ${NAMESPACE}
oc get secret config-as-code-manifest-ig -n ${NAMESPACE}  # license exists?
```

### Fulfillment Pods CrashLooping

```bash
oc logs deployment/fulfillment-grpc-server -n ${NAMESPACE}
```

Common causes: missing `fulfillment-db` secret, cert-manager certificates
not issued (`oc get certificate -n ${NAMESPACE}`), missing controller
credentials.

### Helm Install Timeout

The AAP bootstrap hook can take 10-40 minutes. Monitor with:

```bash
oc logs -f job/osac-aap-bootstrap -n ${NAMESPACE}
```

### Template Publish Hook Failing

The `osac-publish-templates` post-install hook must complete for Helm to
report success. If it fails, cluster templates may be missing or incomplete
(on upgrade, previously published templates may still exist).

**Check hook pod status and logs:**

```bash
oc get pods -n ${NAMESPACE} | grep publish-templates
oc logs job/osac-publish-templates -n ${NAMESPACE} -c wait-for-fulfillment  # init container
oc logs job/osac-publish-templates -n ${NAMESPACE} -c publish-templates     # main container
```

**Common causes:**

- **Fulfillment service not ready** - The init container polls
  `https://fulfillment-rest-gateway:8000/healthz` for up to 600s. If it
  times out, check that the fulfillment service pods are running and the
  `fulfillment-rest-gateway` Service exists.
- **AAP token missing or empty** - The main container reads the `osac-aap-api-token`
  Secret and fails if the token is absent or empty. Verify the secret exists
  and contains a valid token:
  `oc get secret osac-aap-api-token -n ${NAMESPACE} -o jsonpath='{.data.token}' | base64 -d`.
- **AAP job template not found** - The `osac-publish-templates` job template
  must exist in AAP. Verify via AAP UI or API after the bootstrap job
  completes.
- **AAP job failure** - The hook logs include the AAP job stdout on failure.
  Check AAP for the job run details.

The hook has `backoffLimit: 6` and `activeDeadlineSeconds: 1300`. After
6 retries or 1300s, the Job fails and Helm reports the install as failed.

### Hook Job Failed

Failed hook pods are preserved for debugging (`hook-succeeded` delete
policy). Check logs:

```bash
oc get pods -n ${NAMESPACE} | grep -v Running | grep -v Completed
oc logs <failed-pod> -n ${NAMESPACE}
```

### An Operator CSV Never Reaches `Succeeded`

```bash
oc get subscription,installplan,csv -n <operator_namespace>
oc get pods -n openshift-marketplace
oc get packagemanifest | wc -l
```

`ImagePullBackOff` on the marketplace catalog pods, or an empty
`packagemanifest` list, means the cluster pull secret can't authenticate to
`registry.redhat.io`. Refresh `openshift-config/pull-secret` and delete the
failed marketplace pods. The Streams for Apache Kafka `Subscription` uses a
manual install plan; approve its `InstallPlan` in the `osac-kafka` namespace.

### `helm dependency build` or an OCI Pull Fails

The `helm` command requires outbound access to `ghcr.io`, and the `file://`
subcharts require a full checkout — run `helm dependency build` from
`osac-installer/`, not a subdirectory. A `not found` error on
`oci://ghcr.io/osac-project/charts/*` usually means an incorrect `--version`.
To list the tags:

```bash
helm show chart oci://ghcr.io/osac-project/charts/osac --version 0.0.17
```

### The `make` Wrapper Fails with `[[: not found`

`/bin/sh` is `dash`, for example on Ubuntu or WSL. Run the target with
`make SHELL=/bin/bash`, or use the `helm` commands under
[Manual Installation](#manual-installation-without-make) instead.
