# OSAC Helm Deployment Guide

This guide provides an administrator procedure for installing the prerequisite
Operators and infrastructure layer (phase 1) from a monorepo checkout, then
installing the matching published `osac` chart (phase 2) on a clean, connected
OpenShift cluster. If your cluster already has the prerequisites, see the
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
- [EVPN host networking (VMaaS)](#evpn-host-networking-vmaas)
- [Separate VMaaS workload cluster](#separate-vmaas-workload-cluster)
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
| External Keycloak with tenant identity providers | Keycloak 26.6.0 or later. Earlier versions do not apply OSAC's hide-on-unresolved-organization setting. The bundled chart uses 26.6.4. See the [Keycloak version requirement](../keycloak-configuration.md#keycloak-version-requirement). |
| Network | Outbound access to github.com, ghcr.io, quay.io, and registry.redhat.io. Building the `osac-infra` dependency also requires Helm workstation access to topolvm.github.io and netbox-community.github.io. |
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
without a tag prefix. For an umbrella release, `osac/v<VERSION>` is the GitHub
Release tag and checkout tag. The separate `osac-release/v<VERSION>` tag is a
release-only marker. Nightly source tags also use `osac/v`. If you already set
`OSAC_RELEASE_VERSION` while following that guide, keep the same value. Use the
same shell session for the remaining commands.

Before setup for an existing `osac-infra` release, check its installed values:

```console
helm get values osac-infra -n osac-infra
```

Set `NS` below to the recorded `osacNamespace`; if no override is listed, the
chart default is `osac`. The setup creates the AAP license Secret in `NS`, so
set the correct value first. Changing `osacNamespace` during an upgrade makes
Helm remove the old namespace and Kubernetes deletes its contents.

The license Secret is required only for chart-managed AAP with
`aap.bootstrap.enabled: true` (the default). For external AAP with its
bootstrap job disabled, skip `AAP_LICENSE_FILE` and the two Secret commands
below; external AAP administrators manage their AAP license, and the local
Secret is not used.

```bash
export OSAC_RELEASE_VERSION="<chart_version>"
export OSAC_CHART="oci://ghcr.io/osac-project/charts/osac"
git clone https://github.com/osac-project/osac.git
cd osac
git checkout "osac/v${OSAC_RELEASE_VERSION}"
cd osac-installer

# For an upgrade, use the installed osacNamespace (default: osac).
export NS=osac
export DOMAIN=$(oc get ingresses.config/cluster -o jsonpath='{.spec.domain}')
export OCP_VERSION=$(oc get clusterversion version -o jsonpath='{.status.desired.version}' | cut -d. -f1,2)
# Only for chart-managed AAP with a bootstrap job:
export AAP_LICENSE_FILE=/path/to/license.zip

oc create namespace "$NS" --dry-run=client -o yaml | oc apply -f -
# Only for chart-managed AAP with a bootstrap job:
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
The full external-PostgreSQL/-OpenBao wiring (connection Secrets, mTLS
certificates, values) is in the
[external services support guide](external-services-support-guide.md).
The customer install guide also explains how to configure `my-values.yaml` for
the services you plan to deploy. Check the LVMS device configuration and
MetalLB address range against your cluster. For VMaaS, set `cnv.enabled: true`
when this release should install and manage OpenShift Virtualization. Keep it
`false` when administrators manage an existing CNV installation, and confirm
that installation is ready on the workload cluster.

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

Before upgrading an existing release, inspect its effective values:

```bash
helm get values osac-deps -n osac-deps --all
```

Set `LVMS_ENABLED` to match `lvms.enabled` in this cluster's values file. Set
`LVMS_SUBSCRIPTION_EXPECTED` to `true` when an LVMS Subscription should exist,
including one managed outside this release; otherwise set it to `false`. For
an existing release, preserve the installed `lvms.enabled` value:

```bash
LVMS_ENABLED=true # Change to false when lvms.enabled is false.
LVMS_SUBSCRIPTION_EXPECTED="$LVMS_ENABLED"
# Override to true if an external Subscription should be checked.
```

When `LVMS_SUBSCRIPTION_EXPECTED` is `true`, check the LVMS Subscription
channel. Skip this check when LVMS is disabled and no Subscription should
exist:

```bash
if [ "$LVMS_SUBSCRIPTION_EXPECTED" = true ]; then
    oc get sub lvms-operator -n openshift-storage --ignore-not-found \
        -o jsonpath='channel={.spec.channel}{"\n"}'
fi
```

Set `lvms.enabled: false` only when LVMS is installed and managed outside the
existing `osac-deps` release. This prevents Helm from rendering resources in
`openshift-storage`. When upgrading an existing release, preserve its current
`lvms.enabled` value; disabling LVMS when the release manages it removes its
namespace and Subscription and can disrupt storage. The same values file is
used for `osac-infra`, so disabling LVMS also skips its configuration hook and
leaves the existing `LVMCluster` in place. Use `--take-ownership` only for
existing resources this release is intended to manage.

For an existing release that manages LVMS, use the recorded
`lvms.channel` from `helm get values osac-deps -n osac-deps --all` as
`LVMS_CHANNEL_OVERRIDE` if the Subscription is missing or omits
`.spec.channel`. Use `stable-$OCP_VERSION` for a new release only when neither
the Subscription nor the `osac-deps` Helm release exists.

```bash
LVMS_CHANNEL_OVERRIDE="${LVMS_CHANNEL_OVERRIDE:-}"
LVMS_CHANNEL=""
if [ "$LVMS_SUBSCRIPTION_EXPECTED" = true ]; then
    LVMS_SUBSCRIPTION=$(oc get sub lvms-operator -n openshift-storage \
        --ignore-not-found -o name) || exit 1
    if [ -n "$LVMS_SUBSCRIPTION" ]; then
        SUBSCRIPTION_CHANNEL=$(oc get sub lvms-operator -n openshift-storage \
            -o jsonpath='{.spec.channel}') || exit 1
        if [ -n "$SUBSCRIPTION_CHANNEL" ]; then
            LVMS_CHANNEL="$SUBSCRIPTION_CHANNEL"
        else
            LVMS_CHANNEL="$LVMS_CHANNEL_OVERRIDE"
        fi
        if [ -z "$LVMS_CHANNEL" ]; then
            echo "Set LVMS_CHANNEL_OVERRIDE to the installed Subscription channel." >&2
            exit 1
        fi
    else
        OSAC_DEPS_NAMESPACE=$(oc get namespace osac-deps \
            --ignore-not-found -o name) || exit 1
        if [ -z "$OSAC_DEPS_NAMESPACE" ]; then
            LVMS_CHANNEL="stable-$OCP_VERSION"
        elif HELM_STATUS_OUTPUT=$(helm status osac-deps -n osac-deps 2>&1); then
            LVMS_CHANNEL="$LVMS_CHANNEL_OVERRIDE"
            if [ -z "$LVMS_CHANNEL" ]; then
                echo "Set LVMS_CHANNEL_OVERRIDE to the installed lvms.channel value." >&2
                exit 1
            fi
        else
            HELM_STATUS_EXIT=$?
            if [[ "$HELM_STATUS_OUTPUT" == *"release: not found"* ]]; then
                LVMS_CHANNEL="stable-$OCP_VERSION"
            else
                printf 'Could not check Helm release osac-deps:\n%s\n' \
                    "$HELM_STATUS_OUTPUT" >&2
                exit "$HELM_STATUS_EXIT"
            fi
        fi
    fi
else
    LVMS_CHANNEL="stable-$OCP_VERSION"
fi
```

For a new release, leave `LVMS_CHANNEL_OVERRIDE` empty. When LVMS is disabled,
the block skips the Subscription lookup.

```bash
helm upgrade --install osac-deps ./charts/osac-deps \
    -n osac-deps --create-namespace \
    -f my-infra-values.yaml \
    --set lvms.channel="$LVMS_CHANNEL" \
    --take-ownership \
    --wait --timeout 30m
```

When `LVMS_SUBSCRIPTION_EXPECTED` is `true`, confirm the LVMS Subscription
channel and state:

```bash
if [ "$LVMS_SUBSCRIPTION_EXPECTED" = true ]; then
    oc get sub lvms-operator -n openshift-storage \
        -o jsonpath='channel={.spec.channel} state={.status.state}{"\n"}'
fi
```

### Phase 1b: install shared infrastructure

Build the `osac-infra` chart dependencies before installing the chart. Run this
from `osac-installer/`; Helm downloads them from the TopoLVM and NetBox Helm
repositories even when their charts are disabled. The machine running Helm
must be able to reach both repositories.

```bash
helm repo add topolvm https://topolvm.github.io/topolvm --force-update
helm repo add netbox https://netbox-community.github.io/netbox-chart/ --force-update
helm dependency build ./charts/osac-infra
```

When `keycloak.enabled: true` (the default), the chart renders the
`osacNamespace` namespace and Keycloak credential resources. This guide creates
that namespace earlier to store the AAP license secret, so pass
`--take-ownership` to let `osac-infra` adopt it. If Keycloak is disabled, the
chart skips this namespace resource, so this particular ownership reason does
not apply.

Use the installed values from the earlier check to preserve `lvms.channel`,
`keycloak.hostname`, `keycloak.route.publicIngress`, `keycloak.route.hostname`,
and `bundledVault.keycloakIssuerUrl` in `my-infra-values.yaml`. The `--set`
options below override that file, so remove or change them to match the
installed values. When bundled OpenBao is enabled, its issuer URL must match
Keycloak's external issuer, including any custom hostname.

When bundled Keycloak is enabled, it mounts the ConfigMap named by
`certs.caBundle.configMap` from the `keycloak` namespace and reads its
`bundle.pem` key. When `caIssuer.enabled: true`, the Phase 1b hook applies a
trust-manager `Bundle` that reconciles same-named ConfigMaps in the
`osac-infra`, `keycloak`, and OSAC release namespaces. This can replace roots
in directly managed ConfigMaps. Before changing `caIssuer.enabled` on an
existing `osac-infra` release, check whether it owns the `default-ca`
`ClusterIssuer` and `default-ca-cert` `Certificate`. Disabling the toggle
removes those resources; if Keycloak certificates use `default-ca`, plan an
issuer migration first to avoid interrupting certificate renewal. For an
existing `Bundle`, first copy the full `bundle.pem` data from each target
ConfigMap to a replacement ConfigMap with a different name in each affected
namespace. Update each consumer to use its replacement and verify that every
replacement is available. Removing or changing the `Bundle` can delete its
target ConfigMaps. Only after the replacements are ready, change or remove the
`Bundle` so it no longer targets those ConfigMaps, then set
`caIssuer.enabled: false` before Phase 1b. On a clean cluster, create the
`keycloak` and `osac-infra` namespaces before applying their
administrator-managed ConfigMaps.
Although Phase 1b can create `osac-infra` with `--create-namespace`, create it
beforehand so its ConfigMap is available before installation. The Phase 1b
command uses `--take-ownership` to adopt the chart-rendered `keycloak`
namespace. Before adoption, check whether an existing `keycloak` namespace is
owned or used by another integration. An existing release with Keycloak
enabled manages both the `keycloak` namespace and the namespace named by
`osacNamespace`. Disabling `keycloak.enabled` or uninstalling the release
removes these namespaces, and Kubernetes deletes their contents, including
administrator-managed ConfigMaps. Preserve those resources and check for other
consumers before changing ownership or disabling Keycloak. Provide a
`bundle.pem` ConfigMap in both
namespaces before Phase 1b when the enabled services consume it. The OSAC chart
uses a separate ConfigMap in the OSAC release namespace; before Phase 2, provide
it under the name configured by
`service.certs.caBundle.configMap`. Keep that name aligned with
`certs.caBundle.configMap`. For a custom name, also configure the related
consumer values listed in
[Section 5 of the customer install guide](customer-install-guide.md#5-helm-chart-configuration-parameters).

An administrator-managed `bundle.pem` must include every CA its consumers need.
Include the CA for the OSAC service certificates (`service.certs.issuerRef`)
and the Kafka broker whenever either is not already trusted by another CA in
the bundle. Also include private OIDC provider CAs and any other private
endpoints their consumers use. With the chart-managed bundle, set
`keycloak.oidcIdpTrust.caPem` to add a private OIDC provider CA; that setting
does not replace the other roots required by an administrator-managed bundle.
Keep the bundle current as CA certificates change. After updating an
administrator-managed bundle, wait until the OSAC release namespace's ConfigMap
contains the new `bundle.pem`, then restart the OSAC operator before switching
certificates. The operator loads this CA bundle at startup for Fulfillment API
TLS regardless of `global.fulfillmentTrust.enabled`, which only controls the
separate trust reconciler. If Keycloak uses the changed CA for an outbound OIDC
provider, ensure it reloads its truststore. A Phase 1b upgrade rolls out
Keycloak when the chart manages the bundle; after updating an
administrator-managed bundle, restart Keycloak.

Before Phase 1b on OpenShift, set `keycloak.uiUrl` in the values file passed to
this command to the bundled OSAC UI's HTTPS Route URL on this cluster. For the
default `osac` namespace, the URL is
`https://osac-ui-osac.<cluster-ingress-domain>`. In a separate-workload layout,
use the management cluster's ingress domain. Keep this value aligned with
`ui.externalHostname` if you customize the UI Route. See
[Keycloak osac-ui redirect URIs](#keycloak-osac-ui-redirect-uris).

Set `KEYCLOAK_ISSUER_URL` below to the exact issuer URL in Keycloak's token
`iss` claim. The example uses the default bundled Keycloak hostname. For
external Keycloak or a custom hostname, replace it with the matching issuer
URL. The command-line `--set` value overrides
`bundledVault.keycloakIssuerUrl` in `my-infra-values.yaml`; remove that argument
if you want the values file to supply the issuer.

```bash
KEYCLOAK_ISSUER_URL="https://keycloak-keycloak.$DOMAIN/realms/osac"
helm upgrade --install osac-infra ./charts/osac-infra \
    -n osac-infra --create-namespace \
    -f my-infra-values.yaml \
    --set osacNamespace="$NS" \
    --set lvms.channel="$LVMS_CHANNEL" \
    --set keycloak.hostname="https://keycloak-keycloak.$DOMAIN" \
    --set keycloak.route.publicIngress=true \
    --set keycloak.route.hostname="keycloak-keycloak.$DOMAIN" \
    --set "bundledVault.keycloakIssuerUrl=$KEYCLOAK_ISSUER_URL" \
    --take-ownership \
    --wait-for-jobs --timeout 30m
```

### Phase 2: install the OSAC platform

Use Helm 4 when it supports your cluster's Kubernetes version. Helm 4 is
incompatible with OpenShift 4.17's Kubernetes 1.30; use Helm 3.17.x or 3.18.x
for OpenShift 4.17. For other cluster versions, choose a Helm 3 minor that
supports the cluster's Kubernetes version; see the
[Helm 3 version-skew documentation](https://helm.sh/docs/v3/topics/version_skew/).
Helm 3.17.0 is the minimum version required for `--take-ownership` and for
installing a chart pinned by content digest (`oci://...@sha256:...`).

Before running the Phase 2 command, when the web console is enabled (the
default), set `ui.auth.oidcIssuerURL` in `my-values.yaml` to the same fully
resolved URL as `service.auth.issuerUrl`. The UI chart requires this explicit
URL; it does not inherit the service value or expand templates. For bundled
Keycloak, use `https://keycloak-keycloak.<cluster-domain>/realms/osac`,
replacing `<cluster-domain>` with the actual cluster domain. For external
Keycloak, use the realm issuer URL configured in `service.auth.issuerUrl`. See
the [customer install guide](customer-install-guide.md#42-configuring-the-helm-values).

In-place upgrades are supported only between patch releases within the same
OSAC minor version. Do not use this command for a minor-version change; follow
the customer install guide's
[minor-version migration requirements](customer-install-guide.md#444-settings-fixed-at-installation).

Before upgrading an existing release, compare its recorded overrides from
`helm get values osac -n "$NS"` with `my-values.yaml`. Carry every intentional
override into the file, including any `service.vault.endpoint` override and
the installed `global.clusterDomain`, `service.externalHostname`, and
`service.internalHostname`. The default `service.auth.issuerUrl` and
`service.idp.url` derive from `global.clusterDomain`, so preserve that value.
The `--set` options below override values in the file. Make `$DOMAIN` match the
installed domain or remove the domain option and keep its value in
`my-values.yaml`; remove or adjust the hostname options to preserve existing
API endpoints. Keep `ui.auth.oidcIssuerURL` aligned with the resolved
`service.auth.issuerUrl`.

```bash
helm upgrade --install osac \
    "$OSAC_CHART" \
    --version "$OSAC_RELEASE_VERSION" \
    -n "$NS" --create-namespace \
    -f my-values.yaml \
    --set global.clusterDomain="$DOMAIN" \
    --set service.externalHostname="fulfillment-api-$NS.$DOMAIN" \
    --set service.internalHostname="fulfillment-internal-api-$NS.$DOMAIN" \
    --wait --timeout 40m
```

On Helm 4, every later upgrade of the `osac` release, including
`helm upgrade --install`, must include `--server-side=true --force-conflicts`.
This also applies when upgrading a release installed with Helm 3: Helm 4's
default `--server-side=auto` follows the previous release's client-side apply
method, while `--force-conflicts` requires server-side apply. After
installation, the AAP operator owns `app.kubernetes.io/managed-by` on the `osac-aap` custom
resource. Helm 3 does not support `--force-conflicts`.

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
| `catalogSource` | OLM `CatalogSource` used by the phase-1 Operator `Subscription` resources. Override it when the cluster uses a mirrored catalog instead of the default Red Hat catalog. | `redhat-operators` |
| `catalogSourceNamespace` | Namespace of `catalogSource`. | `openshift-marketplace` |
| `certManager.enabled` | Creates the cert-manager Operator `Subscription` and the CA and trust resources. | `true` |
| `certManager.channel` | Update channel for `openshift-cert-manager-operator`. | `stable-v1` |
| `trustManager.enabled` | Deploys the vendored trust-manager. | `true` |
| `trustManager.upstream.enabled` | Uses an already-installed trust-manager instead of the vendored one. | `false` |
| `caIssuer.enabled` | Creates the `default-ca` `ClusterIssuer` and the `ca-bundle` `ConfigMap`. | `true` |
| `aapOperator.enabled` | Creates the AAP Operator `Subscription`. | `true` |
| `aapOperator.channel` | Update channel for the AAP Operator. | `stable-2.6-cluster-scoped` |
| `cnv.enabled` | Creates the OpenShift Virtualization `Subscription` and the `HyperConverged` custom resource when this release manages CNV. For VMaaS, leave it `false` when administrators manage CNV separately. | `false` |
| `cnv.channel` | Update channel for OpenShift Virtualization. | `stable` |
| `evpn.enabled` | Configures OpenShift host networking for VMaaS CUDN EVPN; requires OpenShift 4.22 or later. Leave disabled on Kind. | `false` |
| `lvms.enabled` | Creates the LVM Storage `Subscription` and worker-scoped `LVMCluster`, using the configured `optionalPaths`. It does not create those devices. | `false` |
| `lvms.channel` | Update channel for LVM Storage. Set it to `stable-<cluster_minor>` at installation. | `stable-4.22` |
| `lvms.deviceSelector.optionalPaths` | Explicit worker-device allowlist. The chart default is a development placeholder; `vmaas-ci` and `bmaas-ci` allowlist `/dev/sdb` and `/dev/vdb` for their standard and Netris SNO environments. `caas-ci` uses `/dev/sdb`; Netris `full-ci` uses `/dev/vdb`. Every listed path that exists may be used. | `[/dev/sample-osac-disk]` |
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
| `bundledVault.enabled` | Deploys an in-cluster OpenBao secret store for development and evaluation. Disabled by default; enable only with `bundledVault.keycloakIssuerUrl` set. Not intended for production. | `false` |
| `bundledVault.keycloakIssuerUrl` | Keycloak OIDC issuer URL used to configure OpenBao JWT authentication. Required when `bundledVault.enabled` is `true`; on OpenShift, use the external Route URL that matches the issuer in Keycloak tokens. | `""` |
| `bundledVault.podSecurityContext` | Pod-level `securityContext` for the OpenBao deployment. Defaults to `{runAsNonRoot: true}` with no hardcoded UID so OpenShift's restricted-v2 SCC can allocate from the namespace range. Override with a fixed UID (e.g. `runAsUser: 100`) on platforms that require one. | `{runAsNonRoot: true}` |
| `cliImage` | The `oc` image that the chart hook jobs use. | `origin-cli:4.20.0` |

## EVPN host networking (VMaaS)

Platform administrators can use the following Helm procedure to prepare
EVPN host networking. Coordinate it with the fabric administrators
before installing OSAC with provider networking enabled. The customer guide
requires the resulting host configuration and BGP readiness; administrators
can follow their environment's procedure to prepare those prerequisites.

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

When the management cluster runs VMs and this procedure prepares its EVPN host
networking, set `evpn.enabled: true` in the phase-1 values used on that cluster.
For the separate-cluster layout documented below, provider networking is
disabled on the management cluster; set `evpn.enabled: false` for a new release
and preserve the current settings on upgrades until the release owner plans a
change. EVPN host preparation on the workload cluster is optional in this
layout. If administrators use this procedure there, pass its `evpn` values to
the workload cluster's Phase 1b install. See
[Separate VMaaS workload cluster](#separate-vmaas-workload-cluster).

OSAC provider networking with VMs on a separate cluster is not supported in
this release. Use this layout with provider networking disabled.

Add a node by adding it to `evpn.nodes` and rerunning the phase 1b
`helm upgrade --install osac-infra` command. Remove a node by deleting its
entry and rerunning that command when at least one configured node remains.
The chart deletes that node's NNCP and `FRRConfiguration` resources. To remove
the final configured node, set `evpn.enabled: false` and `evpn.nodes: []`,
then rerun the phase 1b command against the cluster that owns this
`osac-infra` release and its EVPN resources. That upgrade does not run the EVPN
cleanup hook, so run the labeled EVPN deletion commands below after the upgrade
succeeds, with `KUBECONFIG` pointing to the same cluster. Deleting the NNCP does
not remove its interface addresses from the node, so remove stale addresses
separately when needed.

Uninstalling `osac-infra` does not remove the EVPN resources or revert the
cluster network operator patch. Helm removes the configure-EVPN ServiceAccount
and RBAC resources on uninstall. To remove the labeled EVPN resources manually:

```bash
oc delete routeadvertisements,vteps -l osac.openshift.io/evpn=true
oc delete frrconfigurations -n openshift-frr-k8s -l osac.openshift.io/evpn=true
oc delete nncp -l osac.openshift.io/evpn=true
```

The `nmstate/nmstate` custom resource is a shared NMState Operator singleton.
Do not delete it as part of routine EVPN cleanup. Only remove it after cluster
administrators confirm that no other integration depends on it:

```bash
oc delete nmstate nmstate
```

Restore the cluster network operator settings separately if the cluster no
longer needs them. The VTEP range, the fabric loopback ranges and the uplink
ranges must not overlap tenant address space. Add them to
`global.networking.excludedIpv4Cidrs` in the `osac` chart.

For phase 2 with chart `0.4.0`, use the complete
[CUDN EVPN with Netris example](customer-install-guide.md#cudn-evpn-with-netris).
Set `global.networking.fabricManager: netris` and
`global.networking.k8sManager: cudn_evpn`. The umbrella chart renders both
manager registrations automatically. Set
`operator.networkManagers.enabled: false` to suppress legacy subchart
registrations; do not add duplicate Netris or CUDN EVPN entries there.
Configure Netris coordinates and credentials in
`aap.instanceGroups.networkFulfillment.config` and `.secret`.
The old expert NetworkClass block and `k8s_only` selection do not apply to
`0.4.0`. Networking selection and the provider-provisioning switch are fixed
at the first installation; a successful Helm render does not verify the fabric.

### Adding CA roots to the shared bundle at runtime

The `ca-bundle` trust-manager Bundle includes the `ca.crt` key from every
Secret in the `cert-manager` namespace labeled
`osac.openshift.io/ca-bundle=additional`. Create one Secret per CA root:

```bash
oc create secret generic partner-ca \
    --namespace cert-manager \
    --from-file=ca.crt=/path/to/partner-root.pem
oc label secret partner-ca osac.openshift.io/ca-bundle=additional \
    --namespace cert-manager
```

Trust-manager adds matching Secrets to the `ca-bundle` ConfigMaps in the
`osac-infra` and OSAC namespaces, and in `keycloak` when Keycloak is enabled.
The next `osac-infra` install or upgrade applies the selector to the Bundle.
After that, creating, updating, labeling, or deleting a matching Secret
updates the bundle without another Helm upgrade. The source must contain only
public CA certificates in `ca.crt`; the Bundle selects that key specifically.
During CA rotation, keep both roots in the source until certificates and
clients have moved to the new root.

For Keycloak's outbound OIDC provider trust, set
`keycloak.oidcIdpTrust.caPem` with `--set-file` when installing or upgrading
`osac-infra`. The chart creates a labeled Secret in `cert-manager` from that
value. On install or upgrade, the CA bundle hook waits for trust-manager to
publish the current bundle before updating Keycloak's truststore checksum and
waiting for its rollout. Trust-manager updates from labeled Secret changes
outside Helm do not restart Keycloak automatically; apply them with an
`osac-infra` upgrade or restart Keycloak after the bundle updates:

```bash
oc rollout restart deployment/keycloak-service --namespace keycloak
```
## Separate VMaaS workload cluster

Run the OSAC management services on one OpenShift cluster and the virtual
machines on another. The clusters must have different API server addresses.
The workload API endpoint in the remote kubeconfig must resolve and be reachable
from the OSAC operator, AAP workers, and Console Proxy workloads on the
management cluster. Workstation-only access is insufficient; use direct
connectivity or a supported proxy or tunnel configured for those workloads.
The administrator installing phase 1 on each cluster and phase 2 on the
management cluster needs `cluster-admin` access. Disable CaaS and BMaaS. The
operator refuses to start with a remote kubeconfig when either the cluster
controller or the bare-metal-instance controller is enabled. Keep
`global.networking.provisioningEnabled: false` in the phase-2 values; provider
networking with VMs on a separate cluster is not supported in this release.

The remote workload kubeconfig is a separate credential from the account used
to install the charts. Use a dedicated workload-cluster service account and
grant the narrowest permissions validated for the enabled OSAC controllers
and AAP VM workflows. The OSAC operator guide documents the remote Storage
controller's cluster-scoped `get`, `list`, and `watch` permission on
`storage.k8s.io/storageclasses`; it does not define a complete least-privilege
role for every remote VM workflow. The kubeconfig is stored in a Secret on the
management cluster and mounted into the operator and AAP; the console proxy
also reads the labeled Secret. Restrict Secret access to the OSAC workloads
and cluster administrators.

Create the workload kubeconfig Secret on the management cluster before phase
2. The Secret contents and the console-proxy label are described in
[VMaaS dedicated cluster](../../../osac-operator/docs/vmaas-dedicated-cluster/README.md).
The phase-2 values that mount that Secret are in the
[customer install guide](customer-install-guide.md#64-installing-vmaas-on-a-separate-workload-cluster).

**Workload cluster.** Point `KUBECONFIG` at the workload cluster and refresh
`OCP_VERSION` for this cluster. Set `LVMS_ENABLED` to the `lvms.enabled` value
in `my-workload-infra-values.yaml`. For existing releases, inspect both
releases' values and repeat the Phase 1a value and Subscription checks with
this kubeconfig. Use this cluster's recorded `lvms.channel` for
`LVMS_CHANNEL_OVERRIDE` when needed, then rerun the Phase 1a channel-selection
block before running Phase 1a and Phase 1b. Do not reuse the other cluster's
LVMS channel; set or clear `LVMS_CHANNEL_OVERRIDE` for this cluster before
rerunning the block. Before Phase 1b, set `NS` from this cluster's
`osac-infra` `osacNamespace`; for a new release, use the value in the values
file or the default `osac`.

```bash
export OCP_VERSION=$(oc get clusterversion version -o jsonpath='{.status.desired.version}' | cut -d. -f1,2)
LVMS_ENABLED=true # Set to the value in my-workload-infra-values.yaml.
LVMS_SUBSCRIPTION_EXPECTED="$LVMS_ENABLED"
# Override to true if an external Subscription should be checked.
```

For an existing release, inspect overrides and computed values on this cluster
before setting `LVMS_CHANNEL_OVERRIDE` and running the channel-selection block:

```bash
helm get values osac-deps -n osac-deps
helm get values osac-infra -n osac-infra
helm get values osac-deps -n osac-deps --all
helm get values osac-infra -n osac-infra --all
if [ "$LVMS_SUBSCRIPTION_EXPECTED" = true ]; then
    oc get sub lvms-operator -n openshift-storage --ignore-not-found \
        -o jsonpath='channel={.spec.channel}{"\n"}'
fi
```

After checking an existing release, set `LVMS_CHANNEL_OVERRIDE` to this
cluster's recorded `lvms.channel` if the Subscription is missing or omits
`.spec.channel`. If no override is needed, clear a value carried over from
another cluster. The assignment below preserves a value you set and defaults
to empty when it is unset:

```bash
LVMS_CHANNEL_OVERRIDE="${LVMS_CHANNEL_OVERRIDE:-}"
```

Set `NS` from this cluster's `osacNamespace` before Phase 1b. For an upgrade,
use the recorded value; for a new release, use the value in
`my-workload-infra-values.yaml` or the default `osac`:

```bash
export NS=osac
```

In `my-workload-infra-values.yaml`, set `cnv.enabled: true` when this release
should install and manage OpenShift Virtualization on the workload cluster. If
administrators manage an existing CNV installation, keep `cnv.enabled: false`
and confirm that installation is ready before proceeding. The Phase 1a command
uses `--take-ownership`; with CNV enabled, Helm can adopt the rendered
`openshift-cnv` namespace and other CNV resources. Uninstalling `osac-deps`
can then delete those resources. Keep CNV disabled to preserve an
administrator-managed installation. Enable `lvms` if LVM Storage needs to be
installed on that cluster. Keep `evpn.enabled: false` when the workload cluster
uses the default pod network. If it uses CUDN EVPN, enable
`evpn` and configure phase 1b as described in
[EVPN host networking (VMaaS)](#evpn-host-networking-vmaas). This configures
host networking only; it does not enable OSAC provider networking across
clusters.
For a new workload-cluster release, set `certManager.enabled`,
`trustManager.enabled`, and `caIssuer.enabled` to `false`; the latter two remain
enabled by default even when `certManager` is disabled. Also disable
`aapOperator`, `keycloak`, `mce`, `kafka`, and `bundledVault`. For an existing
release, inspect and preserve its installed values for all these settings,
especially `aapOperator.enabled` and `mce.enabled`. Changing a release-managed
setting can remove or alter existing operators, namespaces, and services;
change it only as part of a planned migration. In particular, changing
`certManager.enabled` can remove its managed namespaces and disrupt certificate
issuance. Run
[Phase 1a](#phase-1a-install-prerequisite-operators) and
[Phase 1b](#phase-1b-install-shared-infrastructure) against that kubeconfig,
replacing `-f my-infra-values.yaml` with `-f my-workload-infra-values.yaml`
in both commands.
Do not install the `osac` chart on this cluster.

**Management cluster.** Point `KUBECONFIG` at the management cluster and
refresh `OCP_VERSION` for this cluster. Set `LVMS_ENABLED` to the
`lvms.enabled` value in `my-management-infra-values.yaml`. After switching back
from the workload cluster, recheck the management cluster's release values and
Subscription; do not reuse the workload cluster's `NS` or LVMS channel. Use
this cluster's recorded `lvms.channel` for `LVMS_CHANNEL_OVERRIDE` when needed,
then rerun the Phase 1a channel-selection block before running the Phase 1a
and Phase 1b commands. Set or clear the override for this cluster; for a new
release, use the `osacNamespace` in `my-management-infra-values.yaml` or the
default `osac`.

```bash
export OCP_VERSION=$(oc get clusterversion version -o jsonpath='{.status.desired.version}' | cut -d. -f1,2)
export DOMAIN=$(oc get ingresses.config/cluster -o jsonpath='{.spec.domain}')
LVMS_ENABLED=true # Set to the value in my-management-infra-values.yaml.
LVMS_SUBSCRIPTION_EXPECTED="$LVMS_ENABLED"
# Override to true if an external Subscription should be checked.
```

For existing releases, recheck overrides and computed values on this cluster
before setting `LVMS_CHANNEL_OVERRIDE` and running the channel-selection block:

```bash
helm get values osac-deps -n osac-deps
helm get values osac-infra -n osac-infra
helm get values osac-deps -n osac-deps --all
helm get values osac-infra -n osac-infra --all
if [ "$LVMS_SUBSCRIPTION_EXPECTED" = true ]; then
    oc get sub lvms-operator -n openshift-storage --ignore-not-found \
        -o jsonpath='channel={.spec.channel}{"\n"}'
fi
```

After checking an existing release, set `LVMS_CHANNEL_OVERRIDE` to this
cluster's recorded `lvms.channel` if the Subscription is missing or omits
`.spec.channel`. If no override is needed, clear a value carried over from
another cluster. The assignment below preserves a value you set and defaults
to empty when it is unset:

```bash
LVMS_CHANNEL_OVERRIDE="${LVMS_CHANNEL_OVERRIDE:-}"
```

Set `NS` to the management cluster's `osac-infra.osacNamespace` from its
recorded values for an upgrade; for a new release, use the value in
`my-management-infra-values.yaml` or the chart default `osac`:

```bash
export NS=osac
```

Create the AAP license Secret on the management cluster after setting `NS`,
only for chart-managed AAP with `aap.bootstrap.enabled: true`. Set
`AAP_LICENSE_FILE` to the local path of the license file if needed, then run:

```bash
oc create namespace "$NS" --dry-run=client -o yaml | oc apply -f -
oc create secret generic config-as-code-manifest-ig \
    --from-file=license.zip="$AAP_LICENSE_FILE" -n "$NS" \
    --dry-run=client -o yaml | oc apply --server-side -f -
oc label secret config-as-code-manifest-ig \
    osac.openshift.io/project=osac-aap -n "$NS" --overwrite
```

For external AAP with its bootstrap job disabled, skip the Secret commands
above; the management cluster does not read the local license Secret in that
configuration.

Create a separate `my-management-infra-values.yaml` for its prerequisites.
The values below are an example for a new management-cluster release, with the
certificate stack enabled and CNV and EVPN disabled. Before upgrading existing
phase-1 releases, inspect their current values and carry each release's
intentional settings into the values used for its upgrade:

```bash
# Recorded user-supplied overrides
helm get values osac-deps -n osac-deps
helm get values osac-infra -n osac-infra
if [ "$LVMS_SUBSCRIPTION_EXPECTED" = true ]; then
    oc get sub lvms-operator -n openshift-storage --ignore-not-found \
        -o jsonpath='channel={.spec.channel}{"\n"}'
fi

# Computed values, including chart defaults
helm get values osac-deps -n osac-deps --all
helm get values osac-infra -n osac-infra --all
```

Without `--all`, Helm shows the values recorded as overrides. With `--all`, it
also shows the installed chart's defaults. Compare computed values with the
target versions' defaults in
`osac-installer/charts/osac-deps/values.yaml` and
`osac-installer/charts/osac-infra/values.yaml` before choosing upgrade values.
Preserve intentional settings, but do not copy old chart defaults as explicit
overrides when you want the target chart's defaults to take effect.

If the releases' current values differ, use a separate values file for each
release. Do not replace existing release values with the defaults in this
example. In particular, retain `lvms.enabled: true` in phase-1 values if it is
enabled. Plan intentional toggle changes with the release owner. For an
existing management-cluster release, retain its current `cnv.enabled` and
`evpn.enabled` values unless its owner plans a migration. Changing
`cnv.enabled` from `true` to `false` can remove release-managed resources,
including the `openshift-cnv` namespace, and disrupt workloads. Before
disabling EVPN on an existing release, check whether its `osac-deps` release
manages NMState (`evpn.enabled: true` and `evpn.nmstate.install: true`).
Disabling EVPN in this case can remove the `openshift-nmstate` namespace and
Subscription. Inspect other NMState consumers, including management-cluster
workloads, and coordinate any change with the release owner. Set
`keycloak.uiUrl` in this file to the management cluster's actual UI Route URL
before Phase 1b. With `NS=osac`, its default host is `osac-ui-osac.<DOMAIN>`;
replace `<DOMAIN>` with the value from the command above:

The Phase 1 commands also have `--set` arguments, which override values from
the files passed with `-f`. When preparing upgrades, check `osacNamespace`,
`lvms.channel`, and the Keycloak hostname and Route settings
(`keycloak.hostname`, `keycloak.route.publicIngress`, and
`keycloak.route.hostname`). Remove any
conflicting `--set` arguments or change them to match the settings you intend
to preserve. Confirm these effective values before running Phase 2.

```yaml
certManager:
  enabled: true
trustManager:
  enabled: true
caIssuer:
  enabled: true
cnv:
  enabled: false # New release only; preserve the existing value on upgrades.
evpn:
  enabled: false # New release only; preserve the existing value on upgrades.
keycloak:
  uiUrl: https://osac-ui-osac.apps.example.com
```

If cert-manager is already installed and will remain administrator-managed,
follow [When Prerequisites Already Exist](#when-prerequisites-already-exist)
before the first Phase 1a install and set `certManager.enabled: false` in this
values file. The Phase 1a command uses `--take-ownership`; with
`certManager.enabled: true`, Helm can adopt the existing `cert-manager` and
`cert-manager-operator` namespaces and the rendered operator resources.
With `certManager.enabled: false`, create the `cert-manager` namespace before
Phase 1b, even if administrator-managed cert-manager runs elsewhere. When
`caIssuer.enabled: true`, the chart places its `Issuer` and `Certificate` in
`cert-manager`; configure the controller's cluster resource namespace as
`cert-manager` so it can resolve the `default-ca` Secret referenced by the
`ClusterIssuer`. The Keycloak Route patch hook also reads that Secret from
`cert-manager`. This chart has no setting to change the namespace. Changing an
existing cert-manager controller's cluster resource namespace does not move
Secrets used by `ClusterIssuer` resources. Copy those Secrets to the new
namespace before changing the setting, or certificate issuance and renewal may
fail. If the controller's cluster resource namespace must remain
different from `cert-manager`, use an administrator-managed `default-ca` issuer
and set `caIssuer.enabled: false`; see
[When Prerequisites Already Exist](#when-prerequisites-already-exist).

For a new release, run phase 1a and phase 1b, replacing
`-f my-infra-values.yaml` with `-f my-management-infra-values.yaml` in both
commands. When upgrading existing releases, pass the values files that preserve
each release's settings. The Phase 2 `helm upgrade --install osac` command
upgrades the `osac` Helm release. In-place upgrades are supported for patch
releases within the same OSAC minor version; see the
customer install guide's [supported upgrade
scope](customer-install-guide.md#444-settings-fixed-at-installation). For these
upgrades, preserve the existing `global.networking.provisioningEnabled`,
`global.networking.fabricManager`, `global.networking.k8sManager`,
`global.services.*`, and NetworkClass settings in `my-values.yaml`.
`provisioningEnabled` cannot change after installation; changing it prevents
the Fulfillment Service from starting. The `create-network-class` hook rejects
changed manager selections or NetworkClass defaults, capabilities, or address
plan. A service-toggle change can also fail the check if it changes the derived
address plan or violates a service constraint. Before upgrading, compare the
stored NetworkClass with the networking values in
`my-values.yaml`; see the customer install guide's
[NetworkClass check](customer-install-guide.md#446-checking-the-networkclass).
Use the remote workload kubeconfig values from the customer install guide as
well; they are separate from the NetworkClass settings. The Secret must exist
in the OSAC namespace before `helm upgrade --install osac`. For production,
keep `lvms.enabled: false` in the phase-2 `my-values.yaml`, including when the
workload cluster uses LVMS.
Storage administrators must prepare the StorageBackend, StorageTier, and tenant
storage configuration; installing the LVM Storage Operator and its operand does
not register an OSAC storage offering. See the
[storage documentation](../../storage/README.md), especially the
[tenant storage onboarding guide](../../storage/guides/tenant-onboarding.md).
The automatic local-storage registration hook is for development and CI only; see
[Evaluation only](customer-install-guide.md#92-evaluation-only).

## When Prerequisites Already Exist

If prerequisite Operators or infrastructure already exist outside the
phase-1 Helm releases and will remain administrator-managed, set their
toggles to `false` in `my-infra-values.yaml` before the first install. For an
existing `osac-deps` or `osac-infra` release, preserve its current values
instead of applying the defaults below. Disabling an enabled toggle removes
resources managed by that release. In particular, retain
`lvms.enabled: true` in phase-1 values when it is enabled, and plan intentional
changes with the release owner. If every component listed below already exists,
skip phase 1 entirely and install only the `osac` chart — see the
[customer install guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/customer-install-guide.md).

`keycloak.enabled` defaults to `true` and is omitted from the values example
below. Leave it enabled for a fresh bundled Keycloak installation. If an
external Keycloak will remain administrator-managed, set
`keycloak.enabled: false` in the phase-1 values before Phase 1b; otherwise,
Phase 1b adopts the `keycloak` namespace with `--take-ownership`.

For an initial install where these prerequisites remain administrator-managed,
use:

```yaml
certManager:  { enabled: false }
trustManager: { enabled: false }
caIssuer:     { enabled: false }
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
  The chart-managed `vg1` uses worker-node selection and the configured
  `optionalPaths`; no device setup is included. The default is a placeholder.
  `vmaas-ci` and `bmaas-ci` list both `/dev/sdb` and `/dev/vdb` to support their
  standard and Netris SNO environments; `caas-ci` sets `/dev/sdb`, and Netris
  `full-ci` sets `/dev/vdb` for its OCP VM. LVMS may use every listed path that
  exists on a selected worker, so these alternatives must remain mutually
  exclusive in those environments. If no listed path exists, LVMS may remain
  degraded and storage provisioning unavailable rather than discover other
  disks.
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
- When `default-ca` is administrator-managed, set `caIssuer.enabled: false` so
  `osac-infra` does not render or adopt the `default-ca` `ClusterIssuer` and
  `default-ca-cert` `Certificate`. This also disables the chart-managed
  self-signed `Issuer`, CA-bundle hook, and Keycloak Route patch hook when
  `keycloak.route.publicIngress` is enabled. Provide any required issuer
  separately. If an existing trust-manager `Bundle` still targets
  administrator-managed ConfigMaps, remove or change it so it no longer
  reconciles those ConfigMaps. Before Phase 2, provide the ConfigMap named by
  `service.certs.caBundle.configMap` in the OSAC release namespace, and set
  `service.certs.issuerRef` in `my-values.yaml` to an existing `ClusterIssuer`
  or `Issuer` for the OSAC service certificates.
- `certs.issuerRef` selects the issuer for certificates used by Kafka, bundled
  PostgreSQL, and OpenBao. If any of these are enabled and no `default-ca`
  ClusterIssuer will exist, set
  `certs.issuerRef.kind` and `certs.issuerRef.name` to a `ClusterIssuer`
  available cluster-wide or an `Issuer` present in each enabled certificate's
  namespace. Bundled Keycloak certificates always use a `ClusterIssuer` named
  `default-ca`, regardless of `certs.issuerRef`. When bundled Keycloak is
  enabled, provide that issuer or leave `caIssuer.enabled: true` so Phase 1b
  creates it. An administrator-managed `default-ca` must issue the chart's
  Keycloak certificates for their requested names: the database server
  certificate for `localhost` and `keycloak-database`, the TLS certificate for
  `keycloak`, `keycloak.keycloak.svc.cluster.local`, and the configured Route
  hostname, and a database client certificate with client-auth usage. The
  `keycloak-database-server-cert` Secret must include `ca.crt`, which PostgreSQL
  uses to trust the client certificate; issue that client certificate from a
  CA trusted by this `ca.crt`.
- `keycloak.enabled: false` requires a pre-configured Keycloak with the `osac`
  realm, clients, and roles already set up.

On a shared cluster, do not change cluster-scoped prerequisites without the
agreement of the cluster owner.

The chart-managed LVMS allowlist is intended for development and CI. Production
customers who manage LVMS should leave `lvms.enabled: false` and configure
their own `LVMCluster` device selector. This change does not alter the separate
manual prerequisite manifest or remote-cluster bootstrap path.

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
| `bundledVault.enabled` | `false` | Deploys an in-cluster OpenBao secret store for development and evaluation. Development profiles enable it. For a manual evaluation install, also set `bundledVault.keycloakIssuerUrl`. |
| `bundledVault.keycloakIssuerUrl` | `""` | External Keycloak issuer URL used by OpenBao JWT authentication. Required when bundled OpenBao is enabled; it must match the `iss` claim in Keycloak tokens. |
| `topolvm.enabled` | `false` | Installs the pinned TopoLVM CSI chart for Kind `dev-full`. Keep it disabled when using an existing cluster `StorageClass`. It uses an external `lvmd` service and disposable `vg1`; the profile exposes the non-default `topolvm-provisioner` StorageClass. |
| `netbox.enabled` | `false` | Deploys the NetBox community chart as a bare-metal inventory backend for the `bmaas_netbox` development and CI flavor. Not for production. On OpenShift it also creates an edge-terminated `Route` named `netbox` in the `osac-infra` namespace. OpenShift assigns the host, and HTTP is redirected to HTTPS. A post-install hook seeds the API token Secret in the OSAC namespace. |

The upgrade does not migrate data from the previous bundled PostgreSQL instance.
Back up any data you need before upgrading.

### Instance chart (`osac`) values

| Value | Default | What it does |
|-------|---------|-------------|
| `hubAccess.enabled` | `false` | Creates hub-access SA/RBAC and registers local cluster as a hub. Only for environments where fulfillment-service and hub are the same cluster. |
| `lvms.enabled` | `false` | Development and CI only. Registers the `local` LVMS StorageBackend and StorageTier in Fulfillment. Keep `false` in production; administrators prepare storage registration and tenant configuration. This instance-chart value is separate from the infrastructure toggle that installs LVM Storage. |

For an evaluation install with bundled OpenBao, pair the Phase 1b `osac-infra`
values with the Phase 2 `osac` values. Replace `<cluster-domain>` with the
cluster ingress domain and keep both issuer URLs aligned with Keycloak's
external issuer:

```yaml
# my-infra-values.yaml (osac-infra)
bundledVault:
  enabled: true
  keycloakIssuerUrl: https://keycloak-keycloak.<cluster-domain>/realms/osac
```

```yaml
# my-values.yaml (osac)
service:
  vault:
    keycloakIssuerUrl: https://keycloak-keycloak.<cluster-domain>/realms/osac
    keycloakClientId: osac-controller
    credentials:
      - secret:
          name: fulfillment-controller-credentials
          items:
            - key: client-secret
              param: client-secret
```

The `osac` chart defaults `service.vault.keycloakClientId` to empty. The
evaluation installation provides the `osac-controller` Keycloak client and its
`fulfillment-controller-credentials` Secret; set the client ID and Secret
reference in the `osac` values file. The reference maps the Secret's
`client-secret` key to the Vault `client-secret` parameter without putting the
credential value in the values file.

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

When `caIssuer.enabled: false`, the chart does not render this Route patch
hook. For a `reencrypt` Route, set `destinationCACertificate` to the CA
certificate that signs Keycloak's serving certificate. Keep it current and
update it whenever that certificate or its issuer changes; the chart will not
set or refresh it in this mode.

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

For Phase 1b's TopoLVM and NetBox dependencies, run
`helm dependency build ./charts/osac-infra` from `osac-installer/`. Helm
downloads the declared dependencies from `topolvm.github.io` and
`netbox-community.github.io` during this step, even when `topolvm.enabled` and
`netbox.enabled` are `false`. The disabled dependencies are not installed.
This repository access is needed from the machine running Helm, not from the
cluster at runtime.

Phase 2 installs the published `osac` chart from GHCR; it does not use the
local chart directory or require a monorepo checkout. OCI pulls require
outbound access to `ghcr.io`. A `not found` error on
`oci://ghcr.io/osac-project/charts/osac` usually means an incorrect or
unavailable version. Confirm that `OSAC_RELEASE_VERSION` matches the selected
umbrella release and check whether Helm can retrieve that chart:

```bash
helm show chart "$OSAC_CHART" --version "$OSAC_RELEASE_VERSION"
```

---

Looking for the `make` wrapper or the contributor value profiles? Those are
contributor tooling, not part of a real install — see the
[OSAC CI and Development Reference](https://github.com/osac-project/osac/blob/main/docs/guides/installation/developer-ci-reference.md).
