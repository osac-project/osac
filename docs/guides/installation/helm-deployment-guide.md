# OSAC Helm Deployment Guide

Deploy the prerequisite Operators and infrastructure layer (phase 1) for
OSAC on a clean connected OpenShift cluster, using `oc` and Helm from a
monorepo checkout. If your cluster already has the prerequisites, see the
[customer install guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/customer-install-guide.md)
to install just the published `osac` chart.

The Fulfillment Service and metering require Kafka. Metering is always deployed.
To use an existing broker, complete the [external Kafka configuration
guide](kafka-configuration.md), including its credentials, TLS trust, and
permissions, before installing the platform chart.

## Table of contents

- [Requirements](#requirements)
- [How It Works](#how-it-works)
  - [Phase 1a: `osac-deps`](#phase-1a-osac-deps)
  - [Phase 1b: `osac-infra`](#phase-1b-osac-infra)
  - [Phase 2: `osac`](#phase-2-osac)
  - [Post-Install Hooks](#post-install-hooks)
  - [Seeding ClusterVersions](#seeding-clusterversions)
- [Installing](#installing)
  - [Prepare values files](#prepare-values-files)
  - [Prepare external PostgreSQL](#prepare-external-postgresql)
  - [Storage class](#storage-class)
  - [Phase 1a: install prerequisite Operators](#phase-1a-install-prerequisite-operators)
  - [Phase 1b: install shared infrastructure](#phase-1b-install-shared-infrastructure)
  - [Phase 2: install the OSAC platform](#phase-2-install-the-osac-platform)
- [Phase-1 Parameters](#phase-1-parameters)
- [When Prerequisites Already Exist](#when-prerequisites-already-exist)
  - [How Toggles Affect Operands](#how-toggles-affect-operands)
  - [Skipping Phase 1 Entirely](#skipping-phase-1-entirely)
- [Development-Only Values](#development-only-values)
  - [Infra chart (`osac-infra`) values](#infra-chart-osac-infra-values)
  - [Instance chart (`osac`) values](#instance-chart-osac-values)
- [Infrastructure Configuration](#infrastructure-configuration)
  - [Keycloak Route with Public Ingress Certificates](#keycloak-route-with-public-ingress-certificates)
- [Uninstall](#uninstall)
- [Troubleshooting](#troubleshooting)
  - [AAP Bootstrap Failing](#aap-bootstrap-failing)
  - [Fulfillment Pods CrashLooping](#fulfillment-pods-crashlooping)
  - [Helm Install Timeout](#helm-install-timeout)
  - [Template Publish Hook Failing](#template-publish-hook-failing)
  - [Hook Job Failed](#hook-job-failed)
  - [An Operator CSV Never Reaches `Succeeded`](#an-operator-csv-never-reaches-succeeded)
  - [`helm dependency build` or an OCI Pull Fails](#helm-dependency-build-or-an-oci-pull-fails)

## Requirements

| Requirement | Details |
|-------------|---------|
| OpenShift | 4.17+ with cluster-admin access |
| CLI tools | `oc`, Helm 4 for Kubernetes versions it supports; Helm 3.17.x or 3.18.x for OpenShift 4.17 (Kubernetes 1.30), `git` |
| Network | Outbound access to github.com, ghcr.io, quay.io, and registry.redhat.io. Building the `osac-infra` dependency also requires Helm workstation access to topolvm.github.io. |
| AAP license | Subscription manifest (`license.zip`) from [Red Hat Customer Portal](https://access.redhat.com/) |

## How It Works

OSAC installs through three ordered Helm releases: Phase 1a, Phase 1b, and
Phase 2. Each release prepares resources required by the next. This order is
required because Helm validates all templates before applying any — a
template that references a CRD must have that CRD already registered on the
cluster.

### Phase 1a: `osac-deps`

Installs OLM Operator Subscriptions for cert-manager, AAP, LVMS, MetalLB, CNV,
and MCE. Post-install hooks wait for each operator's CSV to succeed and its
CRDs to register. Each operator is gated by a values toggle.

### Phase 1b: `osac-infra`

Creates infrastructure resources and operator operands after the required CRDs
are available: CertManager CR, ClusterIssuer, CA certificates, trust-manager
Bundle, Keycloak, LVMCluster, HyperConverged, MetalLB IPAddressPool, controller
credentials, bundled PostgreSQL (development only), and bundled OpenBao
(development only).

### Phase 2: `osac`

Installs OSAC: operator, fulfillment-service, AAP bootstrap, UI. All
prerequisite issuers, credential Secrets, and CRDs must already exist.
The `osac` chart creates its database configuration Secrets and Certificate
resources in this phase; cert-manager populates their client-certificate
Secrets.

### Post-Install Hooks

After phase 2, Helm runs post-install (and post-upgrade) hooks that
finalize the deployment:

| Hook | Weight | What it does |
|------|--------|-------------|
| `osac-publish-templates` | 20 | Publishes cluster templates (CaaS, MaaS), compute instance templates (VMaaS), bare-metal instance templates, and add-on operators (CaaS) to the fulfillment catalog |
| `seed-cluster-versions` | 30 | Optionally seeds shared DiskImages and ClusterVersions |

**Template publishing** (`osac-publish-templates`): An init container
waits for the fulfillment REST gateway to be healthy (up to 600s),
then launches the `osac-publish-templates` AAP job template and polls
until it completes. Helm blocks until this hook succeeds, so
`helm install` and `helm upgrade` will not report success until
templates are available. The underlying Ansible role uses a PATCH/POST
pattern, making re-publish on upgrade safe and idempotent.

This hook is enabled by default
(`aap.instanceGroups.publishTemplates.enabled: true`). CaaS and MaaS
depend on it for cluster templates and add-on operators; VMaaS depends
on it for compute instance templates. Set `enabled: false` only when
no enabled service depends on hook-published templates — currently a
BMaaS-only deployment — or when all required templates are provisioned
separately:

```yaml
aap:
  instanceGroups:
    publishTemplates:
      enabled: false
```

### Seeding ClusterVersions

Enable `clusterVersions.enabled` to seed versions after installation. A version
marked `default: true` must include a `diskImage`; Helm rejects missing or empty
image configuration before creating resources. The release `image` and the
DiskImage `sourceRef` serve different purposes: the former is the OpenShift
release pullspec, while the latter is the bootable registry disk artifact.

```yaml
clusterVersions:
  enabled: true
  versions:
  - version: "4.22.0"
    image: quay.io/openshift-release-dev/ocp-release:4.22.0-multi
    default: true
    diskImage:
      name: rhcos-4-22
      sourceRef: oci://quay.io/osac-project/rhcos:4.22.0
      architecture: [ARCHITECTURE_AMD64]
```

This personal RHCOS artifact is a development and evaluation example, not a
production default. Production operators should supply their own compatible
artifact.
`architecture` defaults to `[ARCHITECTURE_AMD64]` when omitted; ARM64 and S390X
are also supported metadata values. The hook creates a shared Linux registry
DiskImage first, then references it by name with `shared: true` in the version.
Non-default versions may omit `diskImage`.

On retry, HTTP 409 retains existing named records without updating their image
or default selection. Choose a new DiskImage name when changing immutable image
parameters; use the API to update existing ClusterVersion references or default
selection. All other API errors fail the hook and print the response. The new
fields and validation are exposed through the chart JSON Schema for the
Enclave Wizard; plugin schema adoption and live Wizard rendering require
separate verification.

## Installing

Select an exact umbrella chart version using
[What is released](customer-install-guide.md#11-what-is-released) in the
customer install guide. Replace `<chart_version>` below with that version,
without a tag prefix. Stable releases use `osac-release/vX.Y.Z`; their
matching `osac/vX.Y.Z` source tags point to the same commit and are used
for the checkout below. Nightly source tags also use `osac/v`. If you already
set `OSAC_RELEASE_VERSION` while following that guide, keep the same value.
Use the same shell session for the remaining commands.

```bash
export OSAC_RELEASE_VERSION="<chart_version>"
git clone https://github.com/osac-project/osac.git
cd osac
git checkout "osac/v${OSAC_RELEASE_VERSION}"
cd osac-installer

export NS=osac
export DOMAIN=$(oc get ingresses.config/cluster -o jsonpath='{.spec.domain}')
export OCP_VERSION=$(oc get clusterversion version -o jsonpath='{.status.desired.version}' | cut -d. -f1,2)
export AAP_LICENSE_FILE=/path/to/license.zip

oc create namespace "$NS" --dry-run=client -o yaml | oc apply -f -
oc create secret generic config-as-code-manifest-ig --from-file=license.zip="$AAP_LICENSE_FILE" -n "$NS" --dry-run=client -o yaml | oc apply --server-side -f -
oc label secret config-as-code-manifest-ig osac.openshift.io/project=osac-aap -n "$NS" --overwrite
```

### Prepare values files

From `osac-installer/`, create two deployment-specific values files before
running Helm:

| Values file | Used by | Configures |
|---|---|---|
| `my-infra-values.yaml` | Phase 1a (`osac-deps`) and phase 1b (`osac-infra`) | Operator subscriptions and shared infrastructure resources |
| `my-values.yaml` | Phase 2 (`osac`) | OSAC services and per-instance settings |

Example profiles under `osac-installer/values/` illustrate chart values and
feature combinations. Use them as references and adapt settings to your
deployment and cluster; infrastructure values inform `my-infra-values.yaml`,
and instance values inform `my-values.yaml`. Many profiles are tailored to
development or evaluation. Some profiles enable test Keycloak fixtures, bundled
PostgreSQL, and bundled OpenBao. Set
`keycloak.devFixtures.enabled: false` outside evaluation environments, change
the Keycloak bootstrap passwords, and disable `bundledPostgres` and
`bundledVault` for production deployments. Production deployments must provide
an external database and Vault-compatible secret store; see the [customer
install guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/customer-install-guide.md)
and [secrets management configuration guide](secrets-management-configuration.md).
The customer install guide also explains how to configure `my-values.yaml` for
the services you plan to deploy. Check the LVMS device configuration and
MetalLB address range against your cluster, and set `cnv.enabled: true` if
your deployment uses VMaaS.

### Prepare external PostgreSQL

For production, prepare PostgreSQL 18 or later before phase 2. The following
steps apply to the umbrella `osac` chart:

1. Provision a server with a hostname that resolves from the cluster and
   accepts connections from OSAC pods on port 5432. Configure a TLS server
   certificate that covers this hostname. The chart uses `sslmode=verify-full`;
   its database clients must trust the server certificate through the CA
   chain in their client-certificate Secrets' `ca.crt` key.

2. Configure PostgreSQL to trust client certificates issued by
   `service.certs.issuerRef`. The pre-installation hook authenticates as
   `postgres`, using a certificate with that common name, and creates the
   `osac_service` and `osac_metering` roles and databases. Allow that identity
   to connect to the `postgres` database and create roles and databases.
   Allow the application identities `osac_service` and `osac_metering` to
   authenticate with their matching client certificates and access their
   respective databases.

3. In the `my-values.yaml` created above, set the external server's hostname:

   ```yaml
   dbInit:
     host: postgres.example.com
   ```

   Replace `postgres.example.com` with your server's hostname. Keep
   `bundledPostgres.enabled: false` in both values files for production.
   The chart uses `dbInit.host` for the initialization hook and the
   generated application connection URLs.

4. Let the umbrella chart create `osac-db-config` and
   `osac-db-metering-config`. Its Certificate resources cause cert-manager
   to create `osac-db-client-cert` and `osac-db-metering-client-cert`.
   Do not pre-create these four Secrets for a fresh umbrella installation.

**Manual component installation**

The [Fulfillment Service installation guide](../../../fulfillment-service/docs/INSTALL.md)
retains manual PostgreSQL operator setup, database Secret creation, and
standalone `fulfillment-service` Helm installation procedures. Follow that
procedure for a standalone service installation. When using its PostgreSQL
setup examples for the umbrella chart, adapt authentication and permissions
to the certificate-based initialization and application identities above.

**Adopting existing resources**

For an intentional migration, `--take-ownership` lets Helm adopt existing
resources that the chart renders. It skips ownership metadata checks;
Helm then manages the database configuration Secrets using chart values,
and cert-manager reconciles the client-certificate Secrets. Back up the
existing configuration and coordinate the transfer with its current owner.
Adoption does not preserve custom connection data independently of the chart.
The Helm 4 `--force-conflicts` flag handles field conflicts and does not
replace this ownership transfer. The phase-2 command below does not enable
`--take-ownership`.

### Storage class

OSAC components create PVCs in both install phases, so make the intended
`StorageClass` the default before Phase 1. `osac-infra` creates a PVC for
Keycloak by default and for bundled PostgreSQL or OpenBao when enabled; Phase 2
workloads may also need storage. The charts do not select a class per PVC.
Check the available classes and which one is default with:

```bash
oc get storageclass
```

The default class is marked `(default)` in the output. The Phase 2 validation
hook warns when no default class exists, but PVCs created in Phase 1 may
already be `Pending`.

`lvms.enabled` and `topolvm.enabled` are both `false` by default, so you do not
need to set them explicitly when using an existing provisioner. Set
`lvms.enabled: true` when using LVMS to provide VMaaS storage; LVMS is not a
CaaS prerequisite because hosted control planes use the hub's default
`StorageClass` (see the [customer install guide](customer-install-guide.md)).
Set `topolvm.enabled: true` only for the Kind `dev-full` local CSI setup; its
TopoLVM storage class is not the cluster default.

### Phase 1a: install prerequisite Operators

If LVMS is already installed and the cluster already has the
`openshift-storage` namespace, set `lvms.enabled: false` in
`my-infra-values.yaml` before installing `osac-deps`. This prevents Helm from
rendering resources in that namespace. The same values file is used for
`osac-infra`, so this also skips its LVMS configuration hook and leaves the
existing `LVMCluster` in place. Use `--take-ownership` only for existing
resources that this release is intended to manage. With LVMS disabled, the
chart does not render resources in `openshift-storage`.

```bash
helm upgrade --install osac-deps ./charts/osac-deps \
    -n osac-deps --create-namespace \
    -f my-infra-values.yaml \
    --set lvms.channel="stable-$OCP_VERSION" \
    --take-ownership \
    --wait --timeout 30m
```

To check a specific Operator, for example LVMS:

```bash
oc get csv -n openshift-storage lvms-operator.v4.22.0
oc get sub lvms-operator -n openshift-storage -o jsonpath='channel={.spec.channel} state={.status.state}{"\n"}'
```

### Phase 1b: install shared infrastructure

Build the `osac-infra` chart dependency before installing the chart. Run this
from `osac-installer/`; the dependency is downloaded from the TopoLVM Helm
repository.

```bash
helm dependency build ./charts/osac-infra
```

When `keycloak.enabled: true` (the default), the chart renders the
`osacNamespace` namespace and Keycloak credential resources. This guide creates
that namespace earlier to store the AAP license secret, so pass
`--take-ownership` to let `osac-infra` adopt it. If Keycloak is disabled, the
chart skips this namespace resource, so this particular ownership reason does
not apply.

```bash
helm upgrade --install osac-infra ./charts/osac-infra \
    -n osac-infra --create-namespace \
    -f my-infra-values.yaml \
    --set osacNamespace="$NS" \
    --set lvms.channel="stable-$OCP_VERSION" \
    --set keycloak.hostname="https://keycloak-keycloak.$DOMAIN" \
    --set keycloak.route.publicIngress=true \
    --set keycloak.route.hostname="keycloak-keycloak.$DOMAIN" \
    --take-ownership \
    --wait-for-jobs --timeout 30m
```

### Phase 2: install the OSAC platform

Use Helm 4 when it supports your cluster's Kubernetes version. Helm 4 is
incompatible with OpenShift 4.17's Kubernetes 1.30; use Helm 3.17.x or 3.18.x
for OpenShift 4.17. For other cluster versions, choose a Helm 3 minor that
supports the cluster's Kubernetes version; see the
[Helm 3 version-skew documentation](https://helm.sh/docs/v3/topics/version_skew/).
Helm 3.17.0 is the minimum version required for `--take-ownership`. It is
also the minimum version that can install a chart pinned by content digest
(`OSAC_CHART_OCI_REF` containing `@sha256:...`, the `install-osac` Makefile
target's escape hatch for validating a staged OCI chart build by exact
artifact rather than by semver tag) — on an older Helm, `make install-osac`
rejects a digest-pinned `OSAC_CHART_OCI_REF` outright with an explicit
version error instead of failing deep inside Helm's own tag-constraint
parsing.

```bash
helm dependency update ./charts/osac
# --force-conflicts resolves an AAP field ownership conflict (Helm 4 only).
# Helm 3 users: omit this option; Helm 3 does not support it.
helm upgrade --install osac ./charts/osac \
    -n "$NS" --create-namespace \
    -f my-values.yaml \
    --set global.clusterDomain="$DOMAIN" \
    --set service.externalHostname="fulfillment-api-$NS.$DOMAIN" \
    --set service.internalHostname="fulfillment-internal-api-$NS.$DOMAIN" \
    --set-string ui.auth.oidcIssuerURL="https://keycloak-keycloak.$DOMAIN/realms/osac" \
    --force-conflicts \
    --wait --timeout 40m
```

`my-infra-values.yaml` configures `osac-deps` and `osac-infra`; see
[Phase-1 Parameters](#phase-1-parameters) below. For Phase 2,
`my-values.yaml` contains per-service configuration, its full parameter
reference, and verification steps; see the
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
| `evpn.enabled` | Configures OpenShift host networking for VMaaS CUDN EVPN; requires OpenShift 4.22 or later. Leave disabled on Kind. | `false` |
| `lvms.enabled` | Creates the LVM Storage `Subscription` and the `LVMCluster` custom resource. | `false` |
| `lvms.channel` | Update channel for LVM Storage. Set it to `stable-<cluster_minor>` at installation. | `stable-4.22` |
| `metallb.enabled` | Creates the MetalLB `Subscription`, the `caas-address-pool` `IPAddressPool`, and the `L2Advertisement`. | `false` |
| `metallb.channel` | Update channel for MetalLB. | `stable` |
| `mce.enabled` | Creates the standalone multicluster engine `Subscription`, agent configuration, and temporary Assisted image bridge. Enable it for CaaS unless RHACM or an existing MCE installation provides it. | `false` |
| `mce.channel` | Update channel for multicluster engine. | `stable-2.17` |
| `mce.imageService.enabled` | Creates and runs the Assisted Image Service. Keep enabled until the Assisted discovery-artifact fix is released and verified. | `true` |
| `mce.imageOverrides` | Temporary four-image MCE 5.0 Assisted operand bridge used until a stable MCE 5.0 catalog is available. Rendered only when `mce.enabled` is `true`. | Four pinned Assisted image entries |
| `mce.osImages` | RHCOS live-ISO entries for agent discovery. | `[]` |
| `kafka.enabled` | Creates the Streams for Apache Kafka `Subscription` and the Kafka custom resource. Enable when the installer should provide Kafka for fulfillment and metering. Keep `false` for an existing broker and follow [Kafka configuration](kafka-configuration.md). | `false` |
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
| `bundledPostgres.enabled` | Deploys bundled PostgreSQL for development and evaluation. It is not intended for production. | `false` |
| `bundledPostgres.database.name` | Bundled database name. | `service` |
| `bundledPostgres.database.user` | Bundled database owner. | `service` |
| `bundledVault.enabled` | Deploys an in-cluster OpenBao secret store for development and evaluation. Set it to `false` for production and configure external Vault in the `osac` values file; see the [secrets management configuration guide](secrets-management-configuration.md). | `true` |
| `bundledVault.podSecurityContext` | Pod-level `securityContext` for the OpenBao deployment. Defaults to `{runAsNonRoot: true}` with no hardcoded UID so OpenShift's restricted-v2 SCC can allocate from the namespace range. Override with a fixed UID (e.g. `runAsUser: 100`) on platforms that require one. | `{runAsNonRoot: true}` |
| `cliImage` | The `oc` image that the chart hook jobs use. | `origin-cli:4.20.0` |

## EVPN host networking (VMaaS)

Set the `evpn` values in the same infra values file passed to `osac-deps` and
`osac-infra`. The installer configures the OpenShift side of the VMaaS CUDN
EVPN connection in this order:

1. It enables FRR, route advertisements, routing via host, and global IP
   forwarding in the cluster network operator. This restarts OVN-Kubernetes
   once while it applies the network settings.
2. It installs the NMState operator when requested and applies the `nmstate`
   singleton.
3. It creates one `NodeNetworkConfigurationPolicy` per configured node. The
   policy adds each supplied uplink address and creates the `evpn-vtep0` dummy
   interface with that node's VTEP address.
4. It creates the `tenant-vtep` VTEP used by the CUDN.
5. It creates each node's underlay BGP `FRRConfiguration` and a raw FRR
   configuration that activates the EVPN address family.
6. It creates a `RouteAdvertisements` resource selecting CUDNs labelled
   `evpn: "true"`.
7. It waits for every configured node-to-fabric BGP session to become
   Established when `waitForEstablished` is enabled.

The cluster admin supplies the OpenShift node names and the NMState operator
channel. The fabric admin supplies the per-node cabling details: uplink
interface, optional uplink address, fabric peer address, and peer ASN. The
fabric admin also assigns the VTEP range, each node VTEP address, and the node
ASN, and configures the matching VTEP range and node ASN on the fabric. If a
node has a different ASN, set its `nodes[].asn`; otherwise the installer uses
`bgp.asn`. `bgp.acceptPrefixes` comes from the fabric admin and lists remote
fabric loopback ranges; the installer accepts only /32 routes within them.

| Value | Meaning and source |
|---|---|
| `evpn.enabled` | Enables EVPN host setup on OpenShift; requires OpenShift 4.22 or later. Keep `false` on Kind. |
| `evpn.nmstate.install` | Installs the NMState operator subscription; set `false` when it is already installed. |
| `evpn.nmstate.channel` | NMState operator channel supported by the cluster. |
| `evpn.configureClusterNetwork` | Applies the cluster network operator settings. It triggers one OVN-Kubernetes restart. |
| `evpn.vtepCIDR` | Required VTEP address range, assigned with the fabric admin. |
| `evpn.bgp.asn` | Required default node ASN, assigned with the fabric admin. |
| `evpn.bgp.acceptPrefixes` | Required fabric loopback IPv4 ranges supplied by the fabric admin. Only /32 routes are accepted. |
| `evpn.bgp.waitForEstablished` | Fails installation if a configured BGP session does not become Established. |
| `evpn.bgp.timeoutSeconds` | Maximum wait per BGP session. |
| `evpn.nodes[].name` | Kubernetes node name from the cluster admin. Add one entry for every VMaaS node. |
| `evpn.nodes[].vtepIP` | Unique address inside `vtepCIDR`, assigned with the fabric admin and configured on the fabric. |
| `evpn.nodes[].asn` | Optional node ASN override, assigned by the fabric admin. Defaults to `evpn.bgp.asn`. |
| `evpn.nodes[].uplinks[].interface` | Node interface name for a cable to a fabric leaf, supplied by the fabric admin. |
| `evpn.nodes[].uplinks[].address` | Optional IPv4 CIDR for the uplink. Omit it when the address is already configured. The fabric admin supplies it. |
| `evpn.nodes[].uplinks[].peerAddress` | IPv4 address of the connected fabric BGP peer, supplied by the fabric admin. |
| `evpn.nodes[].uplinks[].peerASN` | ASN of the connected fabric peer, supplied by the fabric admin. |

For a multicluster VMaaS deployment, `make install-multicluster` uses the
infra values to configure EVPN on the workload cluster. The management-cluster
overlay keeps `evpn.enabled` disabled.

Add a node by adding it to `evpn.nodes` and rerunning `make install`. Remove a
node by deleting its entry and rerunning `make install`; the installer deletes
that node's NNCP and FRRConfigurations. Deleting the NNCP does not remove its
interface addresses from the node, so remove stale addresses separately when
needed.

Uninstalling `osac-infra` does not remove the EVPN resources or revert the
cluster network operator patch. Helm removes the configure-EVPN ServiceAccount
and RBAC resources on uninstall. To remove the labeled EVPN resources manually:

```bash
oc delete routeadvertisements,vteps -l osac.openshift.io/evpn=true
oc delete frrconfigurations -n openshift-frr-k8s -l osac.openshift.io/evpn=true
oc delete nncp -l osac.openshift.io/evpn=true
```

Restore the cluster network operator settings separately if the cluster no
longer needs them. The VTEP range and uplink ranges must not overlap tenant
address space. Add these infrastructure ranges to
`global.networking.excludedIpv4Cidrs` in the `osac` chart when that value is
available in the installed chart.

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

- Even with `certManager.enabled: false`, the `osac` chart's install preflight
  (check `cert-manager-crds`) requires the `certificates.cert-manager.io` CRD. Any cert-manager
  distribution satisfies it.
- Disabling `cnv.enabled` skips the `HyperConverged` custom resource.
  Disabling `lvms.enabled` skips an `LVMCluster` custom resource that uses
  device class `vg1`, thin pool size 90 percent, and overprovision ratio 10.
  Disabling `metallb.enabled` skips an `IPAddressPool` custom resource named
  `caas-address-pool` with the fixed range `192.168.100.240` to
  `192.168.100.250`. If you already run the Operator, keep the toggle `false`
  and create your own operand. Edit the `IPAddressPool` after installation to
  use an address range that is valid for your network.
- MCE is disabled by default. Set `mce.enabled: true` when this installation
  owns standalone MCE for CaaS agent-based cluster provisioning. Leave it
  `false` when RHACM or an existing MCE installation owns that lifecycle. The
  disabled state also suppresses the default Assisted image override
  `ConfigMap` and its MCE 5.0 compatibility RBAC; the chart does not modify the
  existing installation.
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
- For a production deployment, a prepared external PostgreSQL server and
  `dbInit.host` set in the phase-2 values file. The `osac` chart and
  cert-manager create the database Secrets during phase 2. See
  [Prepare external PostgreSQL](#prepare-external-postgresql).
- For a production deployment, an external Vault-compatible store. Complete
  the [secrets management configuration guide](secrets-management-configuration.md)
  before installing the `osac` chart.

If any of these is missing, run the phase-1 installations with the matching
toggles set to `false`.

## Development-Only Values

Disable these for any real deployment.

### Infra chart (`osac-infra`) values

| Value | Default | What it does |
|-------|---------|-------------|
| `bundledPostgres.enabled` | `false` | Deploys bundled PostgreSQL for development and evaluation. It is not intended for production. |
| `bundledVault.enabled` | `true` | Deploys an in-cluster OpenBao secret store for development and evaluation. Set to `false` for production and follow the [secrets management configuration guide](secrets-management-configuration.md). |
| `topolvm.enabled` | `false` | Installs the pinned TopoLVM CSI chart for Kind `dev-full`. Keep it disabled when using an existing cluster `StorageClass`. It uses an external `lvmd` service and disposable `vg1`; the profile exposes the non-default `topolvm-provisioner` StorageClass. |

The upgrade does not migrate data from the previous bundled PostgreSQL instance.
Back up any data you need before upgrading.

### Instance chart (`osac`) values

| Value | Default | What it does |
|-------|---------|-------------|
| `hubAccess.enabled` | `false` | Creates hub-access SA/RBAC and registers local cluster as a hub. Only for environments where fulfillment-service and hub are the same cluster. |

## Infrastructure Configuration

### Keycloak Route with Public Ingress Certificates

For clusters with publicly-trusted wildcard ingress certificates (e.g.,
production OpenShift clusters with Let's Encrypt), you can configure Keycloak's
Route to use `reencrypt` termination instead of `passthrough`. Set the same
public hostname in both values:

```yaml
# my-infra-values.yaml
keycloak:
  hostname: "https://keycloak-keycloak.apps.<cluster-domain>"
  route:
    publicIngress: true
    hostname: "keycloak-keycloak.apps.<cluster-domain>"
```

Replace `<cluster-domain>` with the cluster's ingress domain. The install command
above sets the same public hostname and enables `publicIngress`.

**How it works:**

- `passthrough` (default): Browser connects directly to Keycloak's internal
  TLS cert (self-signed CA).
- `reencrypt`: Router presents the cluster's public ingress cert to browsers,
  then re-encrypts traffic to Keycloak's internal TLS endpoint using the CA
  cert.

**Requirements:**

- `caIssuer.enabled: true` (default) — The hook depends on cert-manager's CA
  bundle.
- The `osac-infra-patch-keycloak-route` hook Job automatically sets
  `destinationCACertificate` at install/upgrade time.

**When to use:**

- Production clusters where browser cert warnings are unacceptable.
- Environments with corporate CA or Let's Encrypt ingress certs.

### Keycloak osac-ui redirect URIs

Keycloak validates the browser's `redirect_uri` against the `osac-ui` client's
`redirectUris` and does **not** resolve relative entries against the Route
hostname, so the UI's real URL has to be known at realm-import time:

```yaml
# my-infra-values.yaml
keycloak:
  uiUrl: https://osac-ui-osac.apps.example.com  # scheme://host[:port], no path
```

The scheme must be `https`. Plain `http` is accepted only for the
local-development hosts `localhost`, `*.localhost` and `127.0.0.1` (the chart
default is the kind `dev-full` UI, `http://ui.osac.localhost:8080`); for any
other host the `resolve-realm-secrets` initContainer fails the install rather
than register a redirect URI that would return the authorization code
unencrypted.

`keycloak.uiUrl` is substituted into the `osac-ui` client's `rootUrl`,
`redirectUris` (`/callback` and `/*`) and `webOrigins` by the
`resolve-realm-secrets` initContainer. `make install-infra` sets it
automatically on OpenShift from the cluster ingress domain
(`https://osac-ui-<NS>.<DOMAIN>`), and `make install-osac` pins
`ui.externalHostname` to the same host so the Route matches. The chart default
targets the kind `dev-full` UI HTTPRoute.

If the value does not match the URL the browser actually uses, login fails at
Keycloak with `Invalid parameter: redirect_uri`. `make helm-validate` runs
`scripts/validate-keycloak-ui-redirect-uris.py`, which fails if the client ever
regains relative redirect URIs.

## Uninstall

When `keycloak.enabled: true` (the default), the `osac-infra` release owns the
`osacNamespace` after adopting it. Uninstall the `osac` release first, as shown
below. Uninstalling `osac-infra` then deletes that namespace and any resources
still inside it. When Keycloak is disabled, `osac-infra` does not render or own
that namespace, so uninstalling the release leaves it in place.

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

Run dependency commands from `osac-installer/` and pass the chart directory.
For the Phase 1b TopoLVM dependency, use
`helm dependency build ./charts/osac-infra`. Helm downloads the declared
dependency from `topolvm.github.io` during this step, even when
`topolvm.enabled: false`; the disabled dependency is not installed. This
repository access is needed from the machine running Helm, not from the
cluster at runtime. The Phase 2 `osac` chart uses `file://` subcharts and
requires a full monorepo checkout; the install procedure updates those with
`helm dependency update ./charts/osac`. OCI chart pulls require outbound
access to `ghcr.io`. A `not found` error on
`oci://ghcr.io/osac-project/charts/*` usually means an incorrect `--version`.
Confirm that `OSAC_RELEASE_VERSION` matches the selected umbrella release
and that its chart is available:

```bash
helm show chart oci://ghcr.io/osac-project/charts/osac --version "$OSAC_RELEASE_VERSION"
```

---

Looking for the `make` wrapper or the contributor value profiles? Those are
contributor tooling, not part of a real install — see the
[OSAC CI and Development Reference](https://github.com/osac-project/osac/blob/main/docs/guides/installation/developer-ci-reference.md).
