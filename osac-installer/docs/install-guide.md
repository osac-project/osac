# Installing OSAC on OpenShift Container Platform

Deploy OSAC (Open Sovereign AI Cloud) onto an existing Red Hat OpenShift
Container Platform cluster by using the OpenShift CLI (`oc`) and Helm. You can
deploy onto a cluster where the platform Operators are already installed, or let
OSAC install them. The guide covers the VMaaS, CaaS, and BMaaS services.

## Contents

1. [OSAC installation architecture](#1-osac-installation-architecture)
2. [Prerequisites](#2-prerequisites)
3. [OSAC component versions](#3-osac-component-versions)
4. [Installing OSAC](#4-installing-osac)
5. [Installing when prerequisites already exist](#5-installing-when-prerequisites-already-exist)
6. [Helm chart configuration parameters for OSAC](#6-helm-chart-configuration-parameters-for-osac)
7. [Installation workflows by service](#7-installation-workflows-by-service)
8. [Verifying the installation](#8-verifying-the-installation)
9. [Postinstallation tasks](#9-postinstallation-tasks)
10. [Supported configurations](#10-supported-configurations)
11. [Uninstalling OSAC](#11-uninstalling-osac)
12. [Troubleshooting](#12-troubleshooting)
13. [Glossary](#13-glossary)

---

## 1. OSAC installation architecture

OSAC installs as three ordered Helm releases. Each release is a plain
`helm upgrade --install` command.

**Phase 1a: `osac-deps`.** Installs Operator Lifecycle Manager (OLM)
`Subscription` resources for the platform Operators in their own namespaces:
cert-manager and Ansible Automation Platform (AAP) always, and LVM Storage,
MetalLB, OpenShift Virtualization, multicluster engine, and Streams for Apache
Kafka when enabled. Post-installation hooks wait for the cert-manager and AAP
`ClusterServiceVersion` (CSV) resources to reach `Succeeded`.

**Phase 1b: `osac-infra`.** Installs the shared, cluster-scoped resources: the
`default-ca` `ClusterIssuer`, trust-manager, the CA bundle `ConfigMap`,
Keycloak and the `osac` realm in the `keycloak` namespace, and the operand
custom resources (CRs) `HyperConverged`, `LVMCluster`, the MetalLB
`IPAddressPool`, and the Kafka CR. The `configure-*` hooks wait for each
remaining Operator CSV to reach `Succeeded` before applying its operand. For
evaluation, this phase also deploys a bundled PostgreSQL database.

**Phase 2: `osac`.** Installs the platform into your namespace: the OSAC
Operator and its CRDs, the Fulfillment Service with an Envoy sidecar, an AAP
instance and its bootstrap job, the OSAC web console, metering, the CSI driver,
the Bare Metal Fulfillment Operator (BMaaS only), and a bundled OpenBao secret
store.

### 1.1 What is released

The OSAC platform chart (phase 2) is published as an OCI artifact at
`oci://ghcr.io/osac-project/charts/osac`. The latest tagged release is `0.0.8`,
and a rolling `0.0.9-nightly.*` channel is also available. The chart includes a
`values.schema.json` file and a `values-example.yaml` file.

The `osac-deps` and `osac-infra` prerequisite charts (phase 1) are not
published. You install them with Helm from a local clone of the monorepo, or
you satisfy the prerequisites yourself. There is no single-command prerequisite
installer and no OLM-based Operator.

The monorepo directory `osac-installer/` contains the authoritative chart
sources. Chart and subchart versions cited from `charts/` reflect monorepo
`HEAD`, which can be ahead of the last tagged release.

### 1.2 Installation routes

Choose one of the following routes:

- **Route A: published chart.** Use this route when the prerequisite Operators
  and the infrastructure layer (CA issuer, trust-manager, Keycloak, and, for
  production, a database) already exist on the cluster. You run a single
  `helm install` of the published `osac` chart. See
  [Section 4.3](#43-installing-osac-by-using-the-published-chart-route-a).
- **Route B: source checkout.** Use this route to have OSAC install the
  prerequisite Operators and the infrastructure layer. You run
  `helm upgrade --install` for `osac-deps`, `osac-infra`, and then `osac` from a
  monorepo clone. See
  [Section 4.4](#44-installing-osac-from-a-source-checkout-route-b).

If some prerequisites exist and others do not, use Route B and disable the
toggles for the components that are present. See
[Section 5](#5-installing-when-prerequisites-already-exist).

To install for a specific service, follow the workflow in
[Section 7](#7-installation-workflows-by-service). Each workflow lists the
toggles and values to set.

> **Note**
>
> The repository `Makefile` (`make install`, `make install-infra`,
> `make install-osac`) wraps the same commands with the CI reference profiles.
> Use it for development only. See
> [Section 4.5](#45-wrapping-the-installation-commands-with-make-development-only).

---

## 2. Prerequisites

### 2.1 Cluster and access

- You have a Red Hat OpenShift Container Platform 4.22 cluster and the
  `cluster-admin` role on it. OSAC is validated on OpenShift Container Platform
  4.22.4 to 4.22.6, channel `stable-4.22`. Earlier minor versions are not
  exercised by CI, and some of the Operator channels in
  [Table 2.1](#23-platform-operators-and-components) do not resolve on them.
- A default storage class exists. PostgreSQL and Keycloak request persistent
  volume claims; without a default storage class, they remain `Pending`. The
  pre-installation validation hook issues a warning if no default storage class
  exists. To set one, run the following command:

  ```console
  $ oc patch storageclass <storage_class_name> -p '{"metadata":{"annotations":{"storageclass.kubernetes.io/is-default-class":"true"}}}'
  ```

  For more information, see
  [Changing the default storage class](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/storage/dynamic-provisioning#change-default-storage-class_dynamic-provisioning).
- The cluster pull secret (`openshift-config/pull-secret`) authenticates to
  `registry.redhat.io` and `quay.io`, so that OLM can pull the Red Hat Operator
  catalogs and operands. To download a current pull secret, see the pull secret
  page in the [Red Hat Hybrid Cloud Console](https://console.redhat.com/openshift/install/pull-secret).
  To apply it, see
  [Updating the global cluster pull secret](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/images/managing-images).
- The cluster has egress to `github.com` and `ghcr.io` for the OSAC images and
  the `osac-ui` OCI chart, to `quay.io` for Keycloak, its PostgreSQL,
  trust-manager, and the `origin-cli` hook image, and to the Red Hat registries.
- The command
  `oc get ingresses.config/cluster -o jsonpath='{.spec.domain}'` returns your
  apps domain. The installation derives all route host names from it.

### 2.2 Client tools

- The OpenShift CLI (`oc`), matching the cluster version. The installation and
  its hooks call `oc` and `kubectl`.
- Helm 3.8 or later, for OCI registry support.
- `git`, for Route B only, to clone the source for the phase-1 charts.
- The `osac` CLI, latest release, for postinstallation hub registration and
  day-2 operations. Not required for the Helm installation.

Installing only the published phase-2 chart onto a cluster that already meets
the prerequisites requires only `oc` and `helm`. The `make` command and `bash`
are required only for the development wrapper in
[Section 4.5](#45-wrapping-the-installation-commands-with-make-development-only).

### 2.3 Platform Operators and components

OSAC relies on the Operators and components in
[Table 2.1](#table-21-platform-operators-and-components). On Route B,
`osac-deps` creates the OLM `Subscription` and `OperatorGroup` resources for
every Operator whose `my-infra-values.yaml` toggle is `true`. To install an
Operator yourself, use OperatorHub in the web console, or apply a `Subscription`
and `OperatorGroup` from the `redhat-operators` catalog. For more information,
see [Adding Operators to a cluster](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/operators/user-tasks).

The Channel column lists the update channel that OSAC subscribes to. The
resolved CSV versions float as the channels publish updates; the versions
observed on OpenShift Container Platform 4.22.6 in September 2026 are listed in
[Section 3](#3-osac-component-versions).

<a id="table-21-platform-operators-and-components"></a>
**Table 2.1. Platform Operators and components**

| Component | Channel | Namespace | Toggle | Required for |
|---|---|---|---|---|
| [cert-manager Operator for Red Hat OpenShift](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/security_and_compliance/cert-manager-operator-for-red-hat-openshift) | `stable-v1` | `cert-manager-operator` | `certManager.enabled` | All services |
| trust-manager | `v0.20.0` (fixed) | `cert-manager` | `trustManager.enabled` | All services |
| `default-ca` `ClusterIssuer` | Not applicable | Cluster-scoped | `caIssuer.enabled` | All services |
| Keycloak and the `osac` realm | Not applicable | `keycloak` | `keycloak.enabled` | All services |
| [Red Hat Ansible Automation Platform Operator](https://docs.redhat.com/en/documentation/red_hat_ansible_automation_platform/2.6/html/installing_on_openshift_container_platform/index) | `stable-2.6-cluster-scoped` | `ansible-aap` | `aapOperator.enabled` | All services |
| [Streams for Apache Kafka](https://access.redhat.com/articles/6644711) | `stable` | `osac-kafka` | `kafka.enabled` | Metering |
| [OpenShift Virtualization](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/virtualization/installing) | `stable` | `openshift-cnv` | `cnv.enabled` | VMaaS |
| [LVM Storage](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html-single/storage/index#persistent-storage-using-lvms) | `stable-<cluster_minor>` | `openshift-storage` | `lvms.enabled` | VMaaS and BMaaS |
| [MetalLB Operator](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/networking_operators/metallb-operator) | `stable` | `metallb-system` | `metallb.enabled` | VMaaS and CaaS |
| [multicluster engine for Kubernetes Operator](https://docs.redhat.com/en/documentation/red_hat_advanced_cluster_management_for_kubernetes/2.13/html/clusters/cluster_mce_overview) | `stable-2.17` | `multicluster-engine` | `mce.enabled` | CaaS |

Notes on individual components:

- **cert-manager Operator for Red Hat OpenShift** installs into the
  `cert-manager-operator` namespace and creates its operands in the
  `cert-manager` namespace. The `certificates.cert-manager.io` CRD is a
  mandatory pre-installation check.
- **trust-manager** distributes the CA bundle. To use an existing
  trust-manager installation, set `trustManager.upstream.enabled=true`.
- **`default-ca` `ClusterIssuer`** is referenced by `service.certs.issuerRef`.
  If you disable `caIssuer.enabled`, provide your own issuer.
- **Keycloak** provides OIDC for the API and web console. This is a custom
  deployment, not the Red Hat build of Keycloak Operator.
- **Red Hat Ansible Automation Platform Operator** installs the Operator only.
  The `osac` chart creates the AAP instance. You supply the subscription
  manifest. See [Section 2.4](#24-credentials-and-external-services).
- **Streams for Apache Kafka** (formerly AMQ Streams) uses a manual install
  plan. Approve the install plan in the `osac-kafka` namespace if it does not
  progress.
- **OpenShift Virtualization** was formerly named Container-native
  Virtualization (CNV).
- **LVM Storage** uses a channel that tracks the OpenShift Container Platform
  minor version; the installation sets it. Any dynamic storage class works if
  you disable `lvms.enabled`.
- **multicluster engine for Kubernetes Operator** is required for
  agent-based cluster provisioning. If Red Hat Advanced Cluster Management for
  Kubernetes (RHACM) is installed, leave `mce.enabled=false`; RHACM manages its
  own multicluster engine.

> **Note**
>
> The `stable-2.17` multicluster engine channel pairs with OpenShift Container
> Platform 4.22, verified as `multicluster-engine.v2.17.2` on 4.22.6. On another
> OpenShift Container Platform version, take the channel from the multicluster
> engine support matrix for that release, indexed from the
> [MCE 2.8 support matrix](https://access.redhat.com/articles/7099674).

### 2.4 Credentials and external services

Always required:

- **AAP subscription manifest (`license.zip`).** A Subscription Allocation
  export from the [Red Hat Customer Portal](https://access.redhat.com/). The AAP
  bootstrap job cannot start without it. You load it into the
  `config-as-code-manifest-ig` Secret in
  [Section 4.1](#41-preparing-the-cluster). For more information, see
  "Obtaining an AAP License" in [`../README.md`](../README.md).

Required for a production deployment:

- **An external PostgreSQL 18 or later database.** The bundled PostgreSQL is a
  single ephemeral pod that loses data on restart. For production, run your own
  database and create the `osac-db-config` and `osac-db-client-cert` Secrets in
  the install namespace, and the `osac-db-metering-config` and
  `osac-db-metering-client-cert` Secrets when metering is enabled. For more
  information, see `fulfillment-service/docs/INSTALL.md`.
- **An external secret store.** The bundled OpenBao secret store is a single
  ephemeral pod that loses data on restart.

Required for CaaS:

- **DNS credentials.** For the default AWS Route 53 backend, an
  `AWS_ACCESS_KEY_ID` value and an `AWS_SECRET_ACCESS_KEY` value that can manage
  the target hosted zone. For more information, see
  [`dns-backend.md`](dns-backend.md).
- **Network-fabric access.** The network backend is `esi` (default) or
  `netris`, selected by `NETWORK_CLASS`. Netris requires the controller URL,
  credentials, site and tenant IDs, and SSH keys to the servers and the bastion
  host. For more information, see [`network-backend.md`](network-backend.md).

Required for BMaaS with the Metal3 backend:

- **BareMetalOperator and a `Provisioning` custom resource.** When
  `bmf.metal3.enabled=true`, the pre-installation validation hook requires the
  `baremetalhosts.metal3.io` CRD and a `Provisioning` resource with
  `spec.watchAllNamespaces: true`. OSAC does not install BareMetalOperator.
- **BMC reachability.** Redfish or IPMI access from the cluster to each host
  BMC, plus DHCP and PXE for Ironic inspection.

### 2.5 Requirements by service

- **VMaaS** (`global.services.vmaas.enabled`) requires OpenShift Virtualization,
  LVM Storage or another dynamic storage class, and MetalLB.
- **CaaS** (`global.services.caas.enabled`) requires multicluster engine or
  RHACM, a DNS backend, a network backend, and
  `aap.instanceGroups.clusterFulfillment` configuration. For more information,
  see [`aap-configuration.md`](aap-configuration.md).
- **BMaaS** (`global.services.bmaas.enabled`) requires BareMetalOperator and a
  `Provisioning` custom resource with `spec.watchAllNamespaces: true`, and LVM
  Storage or another storage class.

The `values/<service>-ci/` directories are CI reference profiles for each
service. Do not use them for a real deployment; see
[Section 4.2](#42-configuring-the-helm-values).

---

## 3. OSAC component versions

OSAC subscribes to a specific update channel for each platform Operator. The
channel-tracked versions float as the channels publish updates.
[Table 3.1](#table-31-component-versions) lists each channel and the CSV version
that channel resolved to on OpenShift Container Platform 4.22.6 in September
2026. Treat the observed versions as indicative.

<a id="table-31-component-versions"></a>
**Table 3.1. Component versions**

| Component | Channel or fixed version | Observed on OCP 4.22.6 | Defined in |
|---|---|---|---|
| OpenShift Container Platform | `stable-4.22` | 4.22.4 to 4.22.6 | Validated range |
| `osac` umbrella chart | `0.0.8` released; `0.0.9-nightly.*` rolling | `0.0.8` | `oci://ghcr.io/osac-project/charts/osac` |
| cert-manager Operator for Red Hat OpenShift | `stable-v1` | `v1.20.0` | `charts/osac-deps/values.yaml` |
| Red Hat Ansible Automation Platform | `stable-2.6-cluster-scoped` | `v2.6.0` | `charts/osac-deps/values.yaml` |
| LVM Storage | `stable-<cluster_minor>` | `v4.22.0` | `charts/osac-deps/values.yaml` |
| MetalLB | `stable` | `v4.22.0` | `charts/osac-deps/values.yaml` |
| OpenShift Virtualization | `stable` | `v4.22.6` | `charts/osac-deps/values.yaml` |
| multicluster engine | `stable-2.17` | `v2.17.2` | `charts/osac-deps/values.yaml` |
| Streams for Apache Kafka | `stable` | `v3.2.1-10` | `charts/osac-deps/values.yaml` |
| trust-manager | `v0.20.0` (fixed) | `v0.20.0` | `charts/osac-infra/templates/trust-manager.yaml` |
| Envoy (Fulfillment Service sidecar) | `v1.33.0` (fixed) | `v1.33.0` | `values/*/instance.yaml` |
| OpenBao (bundled secret store) | `2.6.2` (fixed) | `2.6.2` | `charts/osac/values.yaml` |
| Keycloak (bundled) | Not applicable | `26.6.4` | `charts/osac-infra/` |
| OSAC web console chart | `0.0.6` at `HEAD`; `0.0.5` in release `0.0.8` | Not applicable | `charts/osac/Chart.yaml` |
| PostgreSQL | 18 or later | `18` (Keycloak database) | `fulfillment-service/docs/INSTALL.md` |

The `osac` chart release `0.0.8` pins its subcharts to `osac-operator-crds`
`0.0.10`, `osac-operator` `0.0.10`, `fulfillment-service` `0.0.79`, `osac-aap`
`0.0.11`, `bare-metal-fulfillment-operator` `0.0.10`, and `osac-ui` `0.0.5`
through its `Chart.lock` file.

A tagged release pins every subchart and image to a specific version. The
nightly channel and monorepo `HEAD` track development tags. The
`values/<service>-ci/` profiles pin `main` and `latest` image tags and are for
testing only.

---

## 4. Installing OSAC

Install OSAC with `oc` and Helm. First prepare the cluster
([Section 4.1](#41-preparing-the-cluster)) and your values files
([Section 4.2](#42-configuring-the-helm-values)), then follow Route A
([Section 4.3](#43-installing-osac-by-using-the-published-chart-route-a)) or
Route B
([Section 4.4](#44-installing-osac-from-a-source-checkout-route-b)).

### 4.1 Preparing the cluster

Perform this procedure for both routes.

**Prerequisites**

- You are logged in to the cluster as a user with `cluster-admin` privileges.
- You have the AAP subscription manifest file (`license.zip`).
- For a production deployment, your external PostgreSQL database is running and
  reachable from the cluster.

**Procedure**

Run all installation commands in the same shell session.

1. Set the shell variables that the rest of the installation uses:

   ```console
   $ export NS=<namespace>
   $ export DOMAIN=$(oc get ingresses.config/cluster -o jsonpath='{.spec.domain}')
   $ export OCP_VERSION=$(oc get clusterversion version -o jsonpath='{.status.desired.version}' | cut -d. -f1,2)
   ```

2. Create the target namespace:

   ```console
   $ oc create namespace "$NS" --dry-run=client -o yaml | oc apply -f -
   ```

3. Create the Secret that holds the AAP subscription manifest:

   ```console
   $ oc create secret generic config-as-code-manifest-ig --from-file=license.zip=/path/to/license.zip -n "$NS" --dry-run=client -o yaml | oc apply --server-side -f -
   ```

4. Label the Secret so that the AAP bootstrap job reads it:

   ```console
   $ oc label secret config-as-code-manifest-ig osac.openshift.io/project=osac-aap -n "$NS" --overwrite
   ```

5. For a production deployment, create the database Secrets in `$NS`:
   `osac-db-config` and `osac-db-client-cert`, and, when metering is enabled,
   `osac-db-metering-config` and `osac-db-metering-client-cert`. For more
   information, see `fulfillment-service/docs/INSTALL.md`.

   > **Note**
   >
   > The chart pre-installation hook fails if the connection URL that these
   > Secrets carry does not resolve to a ready PostgreSQL Service.

### 4.2 Configuring the Helm values

You create one values file for the `osac` chart, `my-values.yaml`. For Route B
you also create a values file for `osac-deps` and `osac-infra`,
`my-infra-values.yaml`.

> **Warning**
>
> Do not use the `values/<service>-ci/` profiles for a real deployment. They
> set `keycloak.devFixtures.enabled: true`, which seeds fixed, known passwords;
> `keycloak.adminUsername: admin` and `keycloak.adminPassword: admin`; and
> `bundledPostgres.enabled: true`, which deploys an ephemeral database. Use
> them only as a reference for the structure of the service-specific value
> blocks.

**Procedure**

1. Retrieve the full set of value keys from the chart:

   ```console
   $ helm show values oci://ghcr.io/osac-project/charts/osac --version 0.0.8 > values-upstream.yaml
   ```

2. Create `my-values.yaml` for the `osac` chart. Base it on the Production
   block of the chart `values-example.yaml` file, which uses an external
   PostgreSQL database, an external Keycloak, and pinned image tags.

3. Add the following hardening to `my-values.yaml`:

   ```yaml
   keycloak:
     devFixtures: { enabled: false }
     adminUsername: <admin_user>
     adminPassword: <strong_password>
   bundledPostgres: { enabled: false }
   bundledVault:    { enabled: false }
   ```

4. Add the service-specific value blocks (`global.services.*`, `csiDriver`,
   `operator.networkManagers`, `networkClass`, `aap`, `metering`, and `bmf`)
   from [Section 7](#7-installation-workflows-by-service) for the service you
   are installing. Take the structure from
   `values/vmaas-ci/instance.yaml`, `values/caas-ci/instance.yaml`, or
   `values/bmaas-ci/instance.yaml`, and replace the `main` and `latest` image
   tags with release tags.

5. For Route B, create `my-infra-values.yaml` for `osac-deps` and `osac-infra`:

   ```yaml
   certManager:  { enabled: true }
   trustManager: { enabled: true }
   aapOperator:  { enabled: true }
   cnv:          { enabled: true }
   lvms:         { enabled: true }
   metallb:      { enabled: true }
   mce:          { enabled: false }
   kafka:        { enabled: true }
   caIssuer:     { enabled: true }
   keycloak:
     enabled: true
     adminUsername: <admin_user>
     adminPassword: <strong_password>
     devFixtures: { enabled: false }
   bundledPostgres: { enabled: false }
   ```

   Set a toggle to `false` for any component that is already installed on the
   cluster. See
   [Section 5](#5-installing-when-prerequisites-already-exist).

For a full parameter reference, see
[Section 6](#6-helm-chart-configuration-parameters-for-osac).

### 4.3 Installing OSAC by using the published chart (Route A)

Use this procedure when the cluster already meets the prerequisites.

**Prerequisites**

- The prerequisite Operators from
  [Table 2.1](#table-21-platform-operators-and-components) are installed.
- The `default-ca` `ClusterIssuer`, trust-manager, the `ca-bundle` `ConfigMap`,
  Keycloak with the `osac` realm, and the credential Secrets exist. For the
  complete list, see
  [Section 5.2](#52-skipping-phase-1).
- For a production deployment, the external PostgreSQL database and its
  `osac-db-*` Secrets exist.
- You completed [Section 4.1](#41-preparing-the-cluster) and
  [Section 4.2](#42-configuring-the-helm-values).

**Procedure**

- Install the `osac` chart by running the following command:

  ```console
  $ helm install osac oci://ghcr.io/osac-project/charts/osac --version 0.0.8 \
      -n "$NS" --create-namespace \
      -f my-values.yaml \
      --set global.clusterDomain="$DOMAIN" \
      --set service.externalHostname="fulfillment-api-$NS.$DOMAIN" \
      --set service.internalHostname="fulfillment-internal-api-$NS.$DOMAIN" \
      --wait --timeout 40m
  ```

  Use `--version 0.0.9-nightly.<build>` only to test unreleased fixes. The AAP
  bootstrap job takes 10 to 40 minutes. Helm does not return until it and, for
  CaaS, the `osac-publish-templates` hook have finished.

**Verification**

- Complete [Section 8](#8-verifying-the-installation).

### 4.4 Installing OSAC from a source checkout (Route B)

Use this procedure to have OSAC install the prerequisite Operators and the
infrastructure layer.

**Prerequisites**

- `git` is installed.
- You completed [Section 4.1](#41-preparing-the-cluster) and
  [Section 4.2](#42-configuring-the-helm-values), including
  `my-infra-values.yaml`.

**Procedure**

1. Clone the repository and resolve the chart dependencies:

   ```console
   $ git clone https://github.com/osac-project/osac.git
   $ cd osac/osac-installer
   $ helm dependency build ./charts/osac
   ```

2. Install phase 1a, the prerequisite Operator `Subscription` resources. Pass
   `--set lvms.channel=stable-$OCP_VERSION` because the LVM Storage channel
   tracks the OpenShift Container Platform minor version:

   ```console
   $ helm upgrade --install osac-deps ./charts/osac-deps \
       -n osac-deps --create-namespace \
       -f my-infra-values.yaml \
       --set lvms.channel="stable-$OCP_VERSION" \
       --wait --timeout 30m
   ```

3. Install phase 1b, the CA issuer, trust-manager, Keycloak, and the operand
   custom resources:

   ```console
   $ helm upgrade --install osac-infra ./charts/osac-infra \
       -n osac-infra --create-namespace \
       -f my-infra-values.yaml \
       --set osacNamespace="$NS" \
       --set lvms.channel="stable-$OCP_VERSION" \
       --set keycloak.hostname="https://keycloak-keycloak.$DOMAIN" \
       --set keycloak.route.hostname="keycloak-keycloak.$DOMAIN" \
       --wait-for-jobs --timeout 30m
   ```

4. Install phase 2, the OSAC platform. This is the same chart as Route A, from
   the local checkout:

   ```console
   $ helm upgrade --install osac ./charts/osac \
       -n "$NS" --create-namespace \
       -f my-values.yaml \
       --set global.clusterDomain="$DOMAIN" \
       --set service.externalHostname="fulfillment-api-$NS.$DOMAIN" \
       --set service.internalHostname="fulfillment-internal-api-$NS.$DOMAIN" \
       --wait --timeout 40m
   ```

**Verification**

- Complete [Section 8](#8-verifying-the-installation).

### 4.5 Wrapping the installation commands with make (development only)

The `make install PLATFORM=openshift PROFILE=<dir> NS=<namespace>` command runs
the Route B procedure with `values/<dir>/infra.yaml` and
`values/<dir>/instance.yaml`. It derives the `DOMAIN` and `OCP_VERSION` values
in the same way and creates the AAP Secret from `values/<dir>/license.zip` or
from the path in `AAP_LICENSE_FILE`. The `make install-infra` command runs
phase 1, `make install-osac` runs phase 2, and `EXTRA_HELM_ARGS` appends
`--set` flags. Use this command only for CI and local development against the
`values/<service>-ci/` profiles.

---

## 5. Installing when prerequisites already exist

If some or all of the prerequisite Operators and infrastructure components
already exist on the cluster, disable the corresponding toggles in
`my-infra-values.yaml` and run Route B. If every component listed in
[Section 5.2](#52-skipping-phase-1) already exists, skip phase 1 and use
Route A.

**Procedure**

1. In `my-infra-values.yaml`, set to `false` the toggle for each component that
   is already present:

   ```yaml
   certManager:  { enabled: false }
   trustManager: { enabled: false }
   aapOperator:  { enabled: false }
   cnv:          { enabled: false }
   lvms:         { enabled: false }
   metallb:      { enabled: false }
   mce:          { enabled: false }
   ```

2. Run the Route B procedure. See
   [Section 4.4](#44-installing-osac-from-a-source-checkout-route-b). The
   phase-1 installations are idempotent, so it is safe to run them when most
   toggles are `false`.

### 5.1 How toggles affect operands

- Even with `certManager.enabled: false`, the pre-installation validation hook
  requires the `certificates.cert-manager.io` CRD. Any cert-manager
  distribution satisfies it.
- An Operator toggle also gates its operand custom resource. Disabling
  `cnv.enabled` skips the `HyperConverged` custom resource. Disabling
  `lvms.enabled` skips an `LVMCluster` custom resource that uses device class
  `vg1`, thin pool size 90 percent, and overprovision ratio 10. Disabling
  `metallb.enabled` skips an `IPAddressPool` custom resource named
  `caas-address-pool` with the fixed range `192.0.2.240` to `192.0.2.250`. If
  you already run the Operator, keep the toggle `false` and create your own
  operand. Edit the `IPAddressPool` after installation to use an address range
  that is valid for your network.
- `caIssuer.enabled: false` requires you to provide a `ClusterIssuer` and set
  `service.certs.issuerRef` in `my-values.yaml`.
- `keycloak.enabled: false` requires a pre-configured Keycloak with the `osac`
  realm, clients, and roles. That configuration is out of scope for this guide.

> **Warning**
>
> On a shared cluster, do not change cluster-scoped prerequisites without the
> agreement of the cluster owner.

### 5.2 Skipping phase 1

Skipping phase 1 and using Route A is safe only when the cluster already
provides everything that the two phase-1 charts create and the `osac` release
depends on.

Phase 1a provides:

- The `certificates.cert-manager.io` CRD.
- The AAP Operator.
- The service Operators you need: OpenShift Virtualization, LVM Storage,
  MetalLB, multicluster engine, and Streams for Apache Kafka.

Phase 1b provides:

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

---

## 6. Helm chart configuration parameters for OSAC

This section lists the parameters you are most likely to set. To retrieve the
complete set, run the following command, and review
`charts/osac-deps/values.yaml`, `charts/osac-infra/values.yaml`, and the `osac`
chart `values.schema.json` file:

```console
$ helm show values oci://ghcr.io/osac-project/charts/osac --version 0.0.8
```

### 6.1 Phase-1 parameters

The same values file is passed to both `osac-deps` and `osac-infra`. Keys that a
chart does not recognize are ignored.

<a id="table-61-phase-1-parameters"></a>
**Table 6.1. Phase-1 parameters (`my-infra-values.yaml`)**

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
| `keycloak.route.publicIngress` | Changes the Keycloak route from `passthrough` to `reencrypt` for clusters with publicly trusted ingress certificates. | `false` |
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

### 6.2 Phase-2 parameters

Keys defined in `charts/osac/values.schema.json` are marked `schema`. Keys that
the `values/<service>-ci/` profiles use but the umbrella schema does not define
pass through to a subchart and are marked `subchart`; confirm those in the
subchart `values.yaml` file.

**Table 6.2. Service enablement (`my-values.yaml`)**

| Parameter | Source | Description | Default |
|---|---|---|---|
| `global.clusterDomain` | schema | Apps domain. All route host names and the default issuer, IdP, and Vault URLs derive from it. Set at installation. | `""` |
| `global.services.vmaas.enabled` | schema | Enables the VMaaS tier. | `true` |
| `global.services.caas.enabled` | schema | Enables the CaaS tier. | `true` |
| `global.services.bmaas.enabled` | schema | Enables the BMaaS tier and gates the `bmf` subchart. | `true` |
| `global.services.maas.enabled` | schema | Enables the MaaS tier. | `true` |

**Table 6.3. OSAC Operator (`operator.*`)**

| Parameter | Source | Description |
|---|---|---|
| `operator.image.repository`, `operator.image.tag`, `operator.image.pullPolicy` | schema | Operator image. Use a release tag for production. |
| `operator.replicaCount`, `operator.resources.*` | schema | Operator sizing. |
| `operator.aap.url`, `operator.aap.token`, `operator.aap.insecureSkipVerify`, `operator.aap.statusPollInterval`, `operator.aap.templatePrefix` | schema | How the Operator reaches AAP. Set `insecureSkipVerify: "true"` for self-signed AAP routes. |
| `operator.fulfillment.serverAddress`, `operator.fulfillment.tokenFile` | schema | Fulfillment gRPC endpoint and the service account token that the Operator presents. |
| `operator.controllers.tenant`, `operator.controllers.networking`, `operator.controllers.storage` | schema | Enable or disable individual reconcilers. Which services run is driven by `global.services.*`. |
| `operator.controllers.networkingProvisioning` | schema | When `false`, networking custom resources reconcile to `READY` without a real fabric. |
| `operator.controllers.volume` | subchart | Enables the Volume reconciler. |
| `operator.configSecret.name`, `operator.configSecret.optional` | schema | Additional Operator configuration Secret. |
| `operator.hubAccess.enabled` | schema | Creates the hub-access `ClusterRole` that the Fulfillment Service binds to. |
| `operator.stall.preparingInfrastructureThreshold`, `operator.stall.controlPlaneStartingThreshold`, `operator.stall.workersJoiningThreshold` | schema | How long a cluster provisioning phase can wait before the Operator marks it stalled. |
| `operator.tenants[]` | subchart | Tenants pre-created at installation. `shared` is the built-in tenant. |
| `operator.networkManagers.fabricManagers.<name>.enabled`, `operator.networkManagers.k8sManagers.<name>.enabled` | subchart | Enables a fabric manager (`netris`, `cudn_net`) or a Kubernetes manager (`k8s_only`). |

**Table 6.4. Networking (umbrella level)**

| Parameter | Source | Description | Default |
|---|---|---|---|
| `networkManagers[]` | schema | Registers network-manager `ConfigMap` resources that the Operator dispatcher discovers. Each entry has `name`, `role` (`fabric` or `k8s`), `description`, and `capabilities`. Equivalent to `operator.networkManagers.*`. | `[]` |
| `networkClass.enabled` | schema | Creates the default `NetworkClass` in the Fulfillment Service after installation. Required for VirtualNetwork and tenant onboarding. | `true` |
| `networkClass.title`, `networkClass.description` | schema | Required title and description for the `NetworkClass`. | CUDN text |
| `networkClass.fabricManager`, `networkClass.k8sManager` | schema | Which registered manager backs the class. For fabric-less VMaaS, set `fabricManager: ""` and `k8sManager: "k8s_only"`. For Netris, set `fabricManager: "netris"`. | `cudn_net` and `""` |
| `networkClass.isDefault` | schema | Marks the class the deployment default. Only one `NetworkClass` can exist. | `true` |
| `networkClass.defaults.virtualNetworkIPv4CIDR`, `networkClass.defaults.subnetIPv4CIDR`, `networkClass.defaults.enableNatGateway`, `networkClass.defaults.egressRules` | schema | Tenant-onboarding defaults that auto-create the VirtualNetwork, Subnet, and SecurityGroup. | `10.200.0.0/16` and others |

**Table 6.5. Fulfillment Service (`service.*`, all `schema`)**

| Parameter | Description |
|---|---|
| `service.externalHostname`, `service.internalHostname` | Public and internal API route host names. Set at installation. |
| `service.variant` | `openshift` or `kind`. Use `openshift`. |
| `service.images.service`, `service.images.envoy` | Fulfillment Service and Envoy sidecar images. |
| `service.certs.issuerRef.kind`, `service.certs.issuerRef.name` | cert-manager issuer for the service certificates. |
| `service.certs.caBundle.configMap` | `ConfigMap` that holds the CA bundle. |
| `service.auth.issuerUrl` | OIDC issuer. Templated from `global.clusterDomain` by default. |
| `service.auth.controllerCredentials[]` | Volume sources for the controller OIDC client credentials. |
| `service.idp.provider`, `service.idp.url`, `service.idp.credentials[]` | Identity provider for user and role management. Only `keycloak` is supported. |
| `service.database.connection[]` | Volume sources for the database URL and client certificate, for example `osac-db-config` and `osac-db-client-cert`. |
| `service.log.level`, `service.log.headers`, `service.log.bodies` | Log verbosity. Keep `headers` and `bodies` set to `false` in production. |
| `service.vault.endpoint` | OpenBao API URL. Templated to the in-cluster bundled OpenBao by default. Set it to `""` to disable Vault integration. |
| `service.vault.namespace`, `service.vault.kvMountPath`, `service.vault.lifecycleRole`, `service.vault.lifecycleMountPath`, `service.vault.keycloakClientId`, `service.vault.keycloakIssuerUrl`, `service.vault.keycloakAudience`, `service.vault.caBundle`, `service.vault.credentials` | Vault mount paths, the Keycloak client and audience that the controller authenticates with, and its CA and credentials. |

**Table 6.6. AAP (`aap.*`, all `schema`)**

| Parameter | Description |
|---|---|
| `aap.aap.instance.enabled`, `aap.aap.instance.name` | Create the AAP instance custom resource. Set `enabled: false` when AAP is managed externally. |
| `aap.aap.instance.controller.disabled`, `aap.aap.instance.hub.disabled`, `aap.aap.instance.lightspeed.disabled` | Turn off individual AAP components. |
| `aap.aap.instance.redisMode` | `standalone` or `cluster`. |
| `aap.aap.instance.routeTlsTerminationMechanism` | `Edge`, `Passthrough`, or `Reencrypt` for the AAP routes. |
| `aap.bootstrap.enabled`, `aap.bootstrap.image`, `aap.bootstrap.eeImage`, `aap.bootstrap.backoffLimit` | The postinstallation bootstrap job that loads config-as-code. |
| `aap.configAsCode.manifestSecret` | Secret that holds `license.zip`. |
| `aap.configAsCode.secret` | Secret with config-as-code runtime flags. |
| `aap.configAsCode.projectGitUri`, `aap.configAsCode.projectGitBranch` | Git source for the Ansible content that the bootstrap job imports. |
| `aap.configAsCode.importAgentsEnabled`, `aap.configAsCode.importBcmAgentsEnabled` | Enable bare-metal agent import and the BCM inventory backend. |
| `aap.instanceGroups.clusterFulfillment.enabled`, `aap.instanceGroups.clusterFulfillment.config`, `aap.instanceGroups.clusterFulfillment.secret` | The `cluster-fulfillment` instance group for CaaS provisioning. See [Section 7.2](#72-installing-osac-for-caas-with-the-esi-network-backend) and [Section 7.3](#73-installing-osac-for-caas-with-the-netris-network-backend). |
| `aap.instanceGroups.networkFulfillment.enabled`, `aap.instanceGroups.networkFulfillment.config`, `aap.instanceGroups.networkFulfillment.secret` | The `network-fulfillment` instance group for Netris. See [Section 7.3](#73-installing-osac-for-caas-with-the-netris-network-backend). |
| `aap.instanceGroups.storageFulfillment.config.STORAGE_SNAPSHOTS_ENABLED`, `aap.instanceGroups.storageFulfillment.secret.VAST_ENDPOINT`, `aap.instanceGroups.storageFulfillment.secret.VAST_USERNAME`, `aap.instanceGroups.storageFulfillment.secret.VAST_PASSWORD` | The `storage-operations` instance group: snapshot toggle and VAST management credentials. |
| `aap.instanceGroups.publishTemplates.enabled` | Runs the postinstallation `osac-publish-templates` hook. Default `true`. Set it to `false` for VMaaS-only or BMaaS-only installations. |
| `aap.instanceGroups.publishTemplates.config.OSAC_TEMPLATE_COLLECTIONS`, `aap.instanceGroups.publishTemplates.config.OSAC_FULFILLMENT_SERVICE_URI` | The Ansible collections to publish and the internal Fulfillment Service URI. |

**Table 6.7. Web console, BMaaS, and other parameters**

| Parameter | Source | Description | Default |
|---|---|---|---|
| `ui.enabled` | schema | Deploys the OSAC web console. | `true` |
| `ui.externalHostname` | schema | Route host name. Auto-assigned from `global.clusterDomain` if empty. | `""` |
| `ui.api.fulfillment.url` | schema | Fulfillment API that the web console calls. | Internal URL |
| `ui.auth.oidcClientId` | schema | OIDC client ID for the web console. | `osac-ui` |
| `ui.images.ui` | schema | Web console image. | Release tag |
| `bmf.metal3.enabled`, `bmf.metal3.namespace`, `bmf.metal3.hostClass` | schema | Use the Metal3 backend. Set `namespace` to where your `BareMetalHost` resources live. Requires BareMetalOperator and a `Provisioning` custom resource with `spec.watchAllNamespaces: true`. | `false` |
| `bmf.env.aapInsecureSkipVerify` | schema | Skip TLS verification for AAP API calls. | `"true"` |
| `bmf.env.aapUrl`, `bmf.env.enableNetworkingProvisioning` | subchart | AAP controller URL that the Bare Metal Fulfillment Operator drives, and its networking-provisioning toggle. | Not applicable |
| `bmf.bcm.enabled`, `bmf.bcm.url`, `bmf.bcm.cert`, `bmf.bcm.key`, `bmf.bcm.caCert`, `bmf.bcm.insecureSkipVerify`, `bmf.bcm.hostClass`, `bmf.bcm.bmhNamespace` | subchart | Base Command Manager (BCM) backend. | `false` |
| `bmf.secrets.inventoryConfig`, `bmf.secrets.managementConfig`, `bmf.secrets.osClouds`, `bmf.configMaps.profiles` | schema | Names of the inventory, management, and `clouds.yaml` Secrets and the profiles `ConfigMap`. | Default names |
| `operatorCrds.install` | schema | Install the OSAC CRDs. Set it to `false` if a cluster administrator manages them. | `true` |
| `csiDriver.enabled` | schema | Deploys the CSI routing driver. Enable it for VMaaS. | `false` |
| `metering.enabled` | schema | Deploys the metering service. Requires Kafka and a database connection. | `false` |
| `metering.reconciliation.interval`, `metering.m360Adapter.enabled`, `metering.m360Adapter.m360.apiUrl`, `metering.m360Adapter.apiKeySecret` | subchart | Reconcile period and the Monetize360 billing adapter. | Not applicable |
| `validation.enabled` | schema | Runs the pre-installation validation hook. | `true` |
| `metallb.enabled`, `metallb.addressCIDR` | schema | In the `osac` chart, creates an `IPAddressPool` and an `L2Advertisement`. Edit the pool after installation to match your network. | `false` and `192.168.40.0/24` |
| `bundledVault.enabled`, `bundledVault.image`, `bundledVault.devRootToken` | schema | Ephemeral in-cluster OpenBao. For evaluation only. | `true` and `openbao:2.6.2` |
| `hubAccess.enabled` | schema | Creates hub-access RBAC and registers the local cluster as its own hub. Single-cluster development only. | `false` |
| `bundledPostgres.enabled` | schema | Ephemeral in-cluster PostgreSQL. For evaluation only. | `false` |
| `dbInit.host` | schema | Host that the `db-init` pre-installation hook connects to, to create the databases. Set it to your external PostgreSQL host for a production deployment. | `postgres.osac-infra.svc.cluster.local` |
| `clusterVersions.enabled`, `clusterVersions.versions[]` | schema | OpenShift Container Platform release images offered to hosted clusters. Each entry has `version`, `image`, and an optional `default`. | `false` and `[]` |

### 6.3 Values set on the command line

The installation commands compute and pass the values in
[Table 6.8](#table-68-command-line-values). Add your own with `-f` files or
extra `--set` flags.

<a id="table-68-command-line-values"></a>
**Table 6.8. Command-line values**

| Flag | Value | Purpose |
|---|---|---|
| `--set global.clusterDomain=$DOMAIN` | `oc get ingresses.config/cluster -o jsonpath='{.spec.domain}'` | Route host names and the issuer and IdP URLs. |
| `--set service.externalHostname=fulfillment-api-$NS.$DOMAIN` | Derived | Public API route. |
| `--set service.internalHostname=fulfillment-internal-api-$NS.$DOMAIN` | Derived | Internal API route. |
| `--set lvms.channel=stable-$OCP_VERSION` | `oc get clusterversion` minor version | LVM Storage channel tracks the OpenShift Container Platform minor version. |
| `--set osacNamespace=$NS` | Your namespace | `osac-infra` cross-namespace stamping. |
| `--set keycloak.hostname=https://keycloak-keycloak.$DOMAIN` | Derived | Keycloak issuer host name. |
| `--set keycloak.route.hostname=keycloak-keycloak.$DOMAIN` | Derived | Keycloak route. |

---

## 7. Installation workflows by service

Each workflow gives the phase-1 (`my-infra-values.yaml`) and phase-2
(`my-values.yaml`) settings for one service. After you set the values, install
by using Route A
([Section 4.3](#43-installing-osac-by-using-the-published-chart-route-a)) or
Route B
([Section 4.4](#44-installing-osac-from-a-source-checkout-route-b)). If a
prerequisite is already on the cluster, set its phase-1 toggle to `false`. See
[Section 5](#5-installing-when-prerequisites-already-exist).

### 7.1 Installing OSAC for VMaaS

**Prerequisites**

- OpenShift Virtualization is installed, or you enable `cnv.enabled` for
  Route B.
- LVM Storage is installed, or another dynamic storage class exists.
- MetalLB is installed, or you enable `metallb.enabled` for Route B.

**Procedure**

1. In `my-infra-values.yaml`, enable the VMaaS Operators:

   ```yaml
   certManager: { enabled: true }
   trustManager: { enabled: true }
   caIssuer:    { enabled: true }
   aapOperator: { enabled: true }
   cnv:     { enabled: true }
   lvms:    { enabled: true }
   metallb: { enabled: true }
   mce:     { enabled: false }
   kafka:   { enabled: true }
   keycloak:
     enabled: true
     adminUsername: <admin_user>
     adminPassword: <strong_password>
     devFixtures: { enabled: false }
   bundledPostgres: { enabled: false }
   ```

2. In `my-values.yaml`, enable the VMaaS tier and configure fabric-less
   networking. Fabric-less networking creates the VirtualNetwork and Subnet by
   using ClusterUserDefinedNetwork (CUDN), the SecurityGroup by using a
   `NetworkPolicy`, and the ExternalIP by using MetalLB L2:

   ```yaml
   global:
     services: { vmaas: { enabled: true }, caas: { enabled: false }, bmaas: { enabled: false }, maas: { enabled: false } }
   csiDriver: { enabled: true }
   operator:
     networkManagers:
       k8sManagers:
         k8s_only: { enabled: true }
   networkClass:
     fabricManager: ""
     k8sManager: "k8s_only"
   aap:
     instanceGroups:
       publishTemplates: { enabled: false }
   ```

3. Add the `service.*`, `aap.configAsCode.*`, Keycloak hardening, and database
   Secret settings from [Section 4.2](#42-configuring-the-helm-values).

4. Install by using Route A or Route B.

**Verification**

- Complete [Section 8](#8-verifying-the-installation).
- Create a `ComputeInstance` custom resource and confirm that it reaches
  `RUNNING`:

  ```console
  $ oc get computeinstance -A
  ```

### 7.2 Installing OSAC for CaaS with the ESI network backend

The ESI network backend and the AWS Route 53 DNS backend are the defaults.

**Prerequisites**

- multicluster engine or RHACM is installed.
- MetalLB and LVM Storage are installed.
- You have AWS Route 53 credentials for the target hosted zone.

**Procedure**

1. In `my-infra-values.yaml`, use the VMaaS Operator set from
   [Section 7.1](#71-installing-osac-for-vmaas), but set `cnv.enabled: false`
   and enable multicluster engine with RHCOS agent images:

   ```yaml
   mce:
     enabled: true
     osImages:
       - openshiftVersion: "4.22"
         version: "<rhcos_build>"
         url: "https://mirror.openshift.com/.../rhcos-4.22.0-x86_64-live-iso.x86_64.iso"
         cpuArchitecture: "x86_64"
   ```

2. In `my-values.yaml`, enable the CaaS tier, the OpenShift Container Platform
   release images offered to hosted clusters, and the `cluster-fulfillment`
   instance group:

   ```yaml
   global:
     services: { caas: { enabled: true }, vmaas: { enabled: false }, bmaas: { enabled: false }, maas: { enabled: false } }
   clusterVersions:
     enabled: true
     versions:
       - version: "4.22.0"
         image: "quay.io/openshift-release-dev/ocp-release:4.22.0-multi"
         default: true
   aap:
     instanceGroups:
       publishTemplates: { enabled: true }
       clusterFulfillment:
         enabled: true
         config:
           NETWORK_CLASS: "esi"
           NETWORK_STEPS_COLLECTION: "osac.steps"
           DNS_CLASS: "dns.route53.dns"
           EXTERNAL_ACCESS_BASE_DOMAIN: "clusters.example.com"
           EXTERNAL_ACCESS_SUPPORTED_BASE_DOMAINS: "clusters.example.com"
           HOSTED_CLUSTER_CONTROLLER_AVAILABILITY_POLICY: "HighlyAvailable"
           HOSTED_CLUSTER_INFRASTRUCTURE_AVAILABILITY_POLICY: "HighlyAvailable"
   ```

3. Put the AWS credentials in a separate values file that is excluded from
   version control, for example `my-secrets.local.yaml`:

   ```yaml
   aap:
     instanceGroups:
       clusterFulfillment:
         secret:
           AWS_ACCESS_KEY_ID: "<route53_access_key_id>"
           AWS_SECRET_ACCESS_KEY: "<route53_secret_access_key>"
   ```

4. Install by using Route A or Route B, and pass every values file, for
   example `helm ... -f my-values.yaml -f my-secrets.local.yaml ...`. For more
   information, see [`aap-configuration.md`](aap-configuration.md) and
   [`dns-backend.md`](dns-backend.md).

**Verification**

- Complete [Section 8](#8-verifying-the-installation), including step 10.
- Create a `ClusterOrder` custom resource and watch the AAP
  `cluster-fulfillment` job.

### 7.3 Installing OSAC for CaaS with the Netris network backend

This workflow uses the same Operators as
[Section 7.2](#72-installing-osac-for-caas-with-the-esi-network-backend) but
switches the network backend from ESI to the Netris controller API. For the
full variable reference, see [`network-backend.md`](network-backend.md).

**Prerequisites**

- The prerequisites from
  [Section 7.2](#72-installing-osac-for-caas-with-the-esi-network-backend).
- Access to the Netris controller: URL, user name, password, site and tenant
  IDs, and management VPC details.
- SSH private keys for the servers and the bastion host.

**Procedure**

1. Use the `my-infra-values.yaml` file from
   [Section 7.2](#72-installing-osac-for-caas-with-the-esi-network-backend)
   without change.

2. In `my-values.yaml`, set `NETWORK_CLASS: netris` and the Netris coordinates
   on both the `clusterFulfillment` and `networkFulfillment` instance groups:

   ```yaml
   aap:
     instanceGroups:
       publishTemplates: { enabled: true }
       clusterFulfillment:
         enabled: true
         config:
           NETWORK_CLASS: "netris"
           NETWORK_STEPS_COLLECTION: "netris.steps"
           DNS_CLASS: "dns.route53.dns"
           NETRIS_CONTROLLER_URL: "https://netris.example.com"
           NETRIS_USERNAME: "netris"
           NETRIS_SITE_ID: "5"
           NETRIS_TENANT_ID: "1"
           NETRIS_TENANT_NAME: "Admin"
           NETRIS_MGMT_VPC_ID: "4"
           NETRIS_MGMT_VPC_NAME: "RH-Infra"
           NETRIS_RESOURCE_CLASS_MAP: '{"fc430":{"server_cluster_template_id":89,"mgmt_interface":"ens4","vpc_interfaces":["ens13"]}}'
           SERVER_SSH_BASTION_HOST: "bastion.example.com"
           SERVER_SSH_BASTION_USER: "ubuntu"
           SERVER_SSH_USER: "core"
           SERVER_MGMT_ROUTE_DESTINATION: "198.51.100.0/30"
           SERVER_MGMT_ROUTE_GATEWAY: "192.0.2.1"
           EXTERNAL_ACCESS_BASE_DOMAIN: "clusters.example.com"
           EXTERNAL_ACCESS_SUPPORTED_BASE_DOMAINS: "clusters.example.com"
           EXTERNAL_ACCESS_API_INTERNAL_NETWORK: "hypershift"
           HOSTED_CLUSTER_BASE_DOMAIN: "clusters.example.com"
           HOSTED_CLUSTER_CONTROLLER_AVAILABILITY_POLICY: "HighlyAvailable"
           HOSTED_CLUSTER_INFRASTRUCTURE_AVAILABILITY_POLICY: "HighlyAvailable"
       networkFulfillment:
         enabled: true
         config:
           NETRIS_CONTROLLER_URL: "https://netris.example.com"
           NETRIS_USERNAME: "netris"
           NETRIS_SITE_ID: "5"
           NETRIS_TENANT_ID: "1"
           NETRIS_TENANT_NAME: "Admin"
   ```

   In `NETRIS_RESOURCE_CLASS_MAP`, each key is a resource-class name;
   `server_cluster_template_id` is the Netris server-cluster template,
   `mgmt_interface` is the management NIC, and `vpc_interfaces` are the
   data-plane NICs.

3. Put the secrets in a separate values file that is excluded from version
   control:

   ```yaml
   aap:
     instanceGroups:
       clusterFulfillment:
         secret:
           NETRIS_PASSWORD: "<netris_password>"
           AWS_ACCESS_KEY_ID: "<route53_access_key_id>"
           AWS_SECRET_ACCESS_KEY: "<route53_secret_access_key>"
           SERVER_SSH_KEY: |
             -----BEGIN OPENSSH PRIVATE KEY-----
             ...
           SERVER_SSH_BASTION_KEY: |
             -----BEGIN OPENSSH PRIVATE KEY-----
             ...
       networkFulfillment:
         secret:
           NETRIS_PASSWORD: "<netris_password>"
   ```

4. Register the `netris` fabric manager and point the default `NetworkClass` at
   it:

   ```yaml
   networkManagers:
     - name: netris
       role: fabric
       description: "Netris SDN fabric manager"
       capabilities: "ipv4"
   networkClass:
     fabricManager: "netris"
     k8sManager: ""
   ```

5. Install by using Route A or Route B, and pass every values file.

**Verification**

- Complete [Section 8](#8-verifying-the-installation).
- Create a `ClusterOrder` custom resource and watch the AAP
  `cluster-fulfillment` and `network-fulfillment` jobs.

### 7.4 Installing OSAC for BMaaS

**Prerequisites**

- LVM Storage is installed, or another storage class exists.
- BareMetalOperator is installed, with a `Provisioning` custom resource that has
  `spec.watchAllNamespaces: true`. OSAC does not install these; the
  pre-installation validation hook fails if they are missing.

**Procedure**

1. In `my-infra-values.yaml`, enable only LVM Storage among the service
   Operators:

   ```yaml
   certManager: { enabled: true }
   trustManager: { enabled: true }
   caIssuer:    { enabled: true }
   aapOperator: { enabled: true }
   cnv:     { enabled: false }
   lvms:    { enabled: true }
   metallb: { enabled: false }
   mce:     { enabled: false }
   kafka:   { enabled: true }
   keycloak:
     enabled: true
     adminUsername: <admin_user>
     adminPassword: <strong_password>
     devFixtures: { enabled: false }
   bundledPostgres: { enabled: false }
   ```

2. In `my-values.yaml`, enable the BMaaS tier and the Metal3 backend:

   ```yaml
   global:
     services: { bmaas: { enabled: true }, vmaas: { enabled: false }, caas: { enabled: false }, maas: { enabled: false } }
   operator:
     controllers: { networkingProvisioning: false }
   bmf:
     metal3:
       enabled: true
       namespace: host-inventory
       hostClass: metal3
     env:
       aapUrl: "http://osac-aap/api/controller"
       aapInsecureSkipVerify: "true"
       enableNetworkingProvisioning: "false"
   aap:
     instanceGroups:
       publishTemplates: { enabled: false }
   ```

   Set `bmf.metal3.namespace` to the namespace where your `BareMetalHost`
   resources live.

3. Install by using Route A or Route B.

**Verification**

- Complete [Section 8](#8-verifying-the-installation).
- Confirm that Metal3 is ready:

  ```console
  $ oc get provisioning
  $ oc get baremetalhosts -A
  ```

- Create a `BareMetalPool` or `BareMetalInstance` custom resource and confirm
  that the Bare Metal Fulfillment Operator reconciles it.

---

## 8. Verifying the installation

Perform the following steps in order. Each step assumes that the previous steps
passed. If a step fails, see [Section 12](#12-troubleshooting).

**Procedure**

1. Check the Helm releases. Every release must show `STATUS: deployed`. A status
   of `pending-install` or `pending-upgrade` means that a hook is still running
   or has failed.

   ```console
   $ helm list -A | grep -E 'osac|osac-infra|osac-deps'
   ```

2. Check the pre-installation validation hook. The log ends with
   `=== Validation passed ===`.

   ```console
   $ oc logs job/osac-pre-install-validate -n <namespace>
   ```

   A missing `certificates.cert-manager.io` CRD aborts the installation. When
   `bmf.metal3.enabled` is set, a missing `BareMetalHost` CRD or a
   `Provisioning` resource without `watchAllNamespaces: true` also aborts the
   installation. A message about a missing default storage class is a warning
   only.

3. Check the phase-1 Operators. Each expected CSV must be `Succeeded`:

   ```console
   $ oc get csv -A | grep -E 'cert-manager|ansible-automation|lvms|metallb|kubevirt|multicluster'
   ```

4. Check the phase-1 operands. Each operand must be ready:

   ```console
   $ oc get hyperconverged -n openshift-cnv
   $ oc get lvmcluster -n openshift-storage
   $ oc get ipaddresspool -n metallb-system
   $ oc get clusterissuer default-ca
   $ oc get bundle -n cert-manager
   ```

5. Check the infrastructure layer and Keycloak. The `keycloak-service` and
   `keycloak-database` pods must be `Running`.

   ```console
   $ oc get pods -n osac-infra
   $ oc get pods -n keycloak
   ```

6. Confirm that the Keycloak realm responds:

   ```console
   $ curl -sk "https://keycloak-keycloak.$DOMAIN/realms/osac/.well-known/openid-configuration"
   ```

7. Check the phase-2 pods. All pods must be `Running` or `Completed`, with no
   pods in `CrashLoopBackOff` or `ImagePullBackOff`:

   ```console
   $ oc get pods -n <namespace>
   ```

   Depending on the enabled services, expect the following pods:

   - Always: `fulfillment-grpc-server`, `fulfillment-rest-gateway`,
     `fulfillment-controller`, `fulfillment-ingress-proxy`, `osac-operator`,
     `osac-operator-console-proxy`, and the `osac-aap-*` pods.
   - With `ui.enabled`: `osac-ui`.
   - With `metering.enabled`: the `osac-metering` pods.
   - With `bundledVault.enabled`: `openbao-0`.
   - VMaaS: `fulfillment-console-proxy`.
   - BMaaS: the Bare Metal Fulfillment Operator pod.

8. Check the certificates and the database initialization. Every `Certificate`
   must be `Ready`, and the `osac-db-init` job must complete:

   ```console
   $ oc get certificate -n <namespace>
   $ oc logs job/osac-db-init -n <namespace>
   ```

9. Check the AAP bootstrap job. The `osac-aap-bootstrap` job runs as a
   postinstallation hook and takes 10 to 40 minutes:

   ```console
   $ oc logs -f job/osac-aap-bootstrap -n <namespace>
   ```

10. CaaS only: check that the cluster templates were published:

    ```console
    $ oc logs job/osac-publish-templates -n <namespace>
    $ osac get clustertemplates
    ```

    The `osac get clustertemplates` output must be non-empty.

11. Check API reachability and log in with the `osac` CLI:

    ```console
    $ ROUTE=$(oc get route fulfillment-api -n <namespace> -o jsonpath='{.spec.host}')
    $ curl -sk "https://$ROUTE/healthz"
    $ osac login --address "$ROUTE" --token-script "oc create token fulfillment-controller -n <namespace> --duration 1h" --insecure
    $ osac get tenants
    ```

    Add `--insecure` only when the route uses the `default-ca` certificate.

12. Check the web console. Open the console URL in a browser. The URL must
    redirect to Keycloak and, after you log in, show the OSAC console. Skip
    this step if `ui.enabled` is `false`.

    ```console
    $ UI=$(oc get route osac-ui -n <namespace> -o jsonpath='{.spec.host}')
    $ curl -skI "https://$UI"
    ```

    The response must be `200` or a `302` redirect to Keycloak.

13. Run a service smoke test, as described in the Verification section of the
    workflow in [Section 7](#7-installation-workflows-by-service) for your
    service.

---

## 9. Postinstallation tasks

### 9.1 Accessing the OSAC consoles

OSAC exposes four consoles as OpenShift Container Platform `Route` resources.
The OSAC web console, Fulfillment API, and AAP routes are in the install
namespace; the Keycloak route is in the `keycloak` namespace. Host names are
auto-assigned as `<route>-<namespace>.<cluster_domain>` unless you set
`ui.externalHostname`, `service.externalHostname`, or `keycloak.route.hostname`.

```console
$ oc get route -n <namespace>
$ oc get route -n keycloak
```

- **OSAC web console.** Deployed whenever `ui.enabled` is `true`, which is the
  default. The route uses edge TLS termination and redirects HTTP to HTTPS. Log
  in through Keycloak SSO against the `osac` realm. With the bundled Keycloak,
  use the `keycloak.adminUsername` and `keycloak.adminPassword` values that you
  set. Production deployments use realm users or a federated identity provider.
- **Fulfillment API.** Authenticate with an OIDC token or with `osac login`, as
  described in step 11 of [Section 8](#8-verifying-the-installation).
- **AAP.** Log in as `admin`. To retrieve the password, run the following
  command:

  ```console
  $ oc extract secret/osac-aap-admin-password -n <namespace> --to -
  ```

- **Keycloak admin console.** Log in with the `keycloak.adminUsername` and
  `keycloak.adminPassword` values.

### 9.2 Installing the `osac` CLI

```console
$ curl -L -o osac https://github.com/osac-project/fulfillment-service/releases/latest/download/osac_Linux_x86_64
$ chmod +x osac
$ sudo mv osac /usr/local/bin/
```

### 9.3 Registering the hub

This procedure applies when the Fulfillment Service and the hub run on the same
cluster. For multi-cluster hub topologies, see [`../README.md`](../README.md)
and [`../OSAC-CLI-HOWTO.md`](../OSAC-CLI-HOWTO.md).

**Procedure**

1. Log in to the Fulfillment Service:

   ```console
   $ osac login \
       --address "$(oc get route fulfillment-api -n <namespace> -o jsonpath='{.spec.host}')" \
       --token-script "oc create token fulfillment-controller -n <namespace> --duration 1h"
   ```

2. Generate the hub-access kubeconfig file:

   ```console
   $ ./scripts/create-hub-access-kubeconfig.sh
   ```

3. Register the hub:

   ```console
   $ osac create hub --kubeconfig=kubeconfig.hub-access --id <hub_name> --namespace <namespace>
   ```

Add `--insecure` to the `osac login` command only when the API route presents a
certificate that your client does not trust, such as the self-signed
`default-ca`. Add `--as system:admin` only when your `oc` context cannot mint
the token.

### 9.4 Additional resources

- CaaS network backend configuration: [`network-backend.md`](network-backend.md)
- CaaS DNS backend configuration: [`dns-backend.md`](dns-backend.md)
- AAP instance group configuration: [`aap-configuration.md`](aap-configuration.md)

---

## 10. Supported configurations

### 10.1 Supported for production

- Deployment onto an existing OpenShift Container Platform cluster with
  `cluster-admin` privileges.
- The published `osac` chart at a tagged release, such as `0.0.8`. The chart
  `values-example.yaml` file documents the Production block: an external
  PostgreSQL database, an external Keycloak, and pinned image tags.
- Prerequisite Operators installed by `osac-deps` on Route B, or pre-existing
  and disabled through the `my-infra-values.yaml` toggles on Route A. See
  [Section 5](#5-installing-when-prerequisites-already-exist).
- An external PostgreSQL 18 or later database with the `osac-db-*` Secrets
  created in advance. See
  [Section 2.4](#24-credentials-and-external-services).
- An external Keycloak configured with the `osac` realm, clients, and roles
  through `service.auth` and `service.idp`.
- A single hub cluster.

### 10.2 Evaluation only

- The bundled PostgreSQL database (`bundledPostgres.enabled: true`). It is
  ephemeral and loses data on restart.
- The bundled OpenBao secret store (`bundledVault.enabled: true`). It runs in
  development mode and loses data on restart.
- `keycloak.devFixtures.enabled: true` and the default `admin` Keycloak
  credentials.
- The `values/<service>-ci/` profiles and their development image tags.

### 10.3 Not covered by this guide

- A supported external secret store values path for the `osac` chart. The
  bundled OpenBao is the only wired option. For a standalone secret store, see
  `fulfillment-service/docs/INSTALL.md`.
- A published chart for phase 1. Route B requires a monorepo clone.
- Detailed configuration of an external Keycloak. The chart accepts an external
  Keycloak through `service.auth` and `service.idp`, but realm and client
  provisioning is out of scope. See `fulfillment-service/docs/INSTALL.md`.
- Multi-hub topologies.

---

## 11. Uninstalling OSAC

**Procedure**

1. Uninstall phase 2:

   ```console
   $ helm uninstall osac -n "$NS"
   ```

2. For a Route B installation, uninstall phase 1 in reverse order:

   ```console
   $ helm uninstall osac-infra -n osac-infra
   $ helm uninstall osac-deps -n osac-deps
   ```

3. The CRDs are retained because they carry the
   `helm.sh/resource-policy: keep` annotation. To remove them, run the
   following command:

   ```console
   $ oc delete crd -l app.kubernetes.io/part-of=osac
   ```

> **Warning**
>
> The repository script `scripts/teardown.sh` also removes the prerequisite
> Operators and their namespaces. Do not run it on a shared cluster. For more
> information, see "Tearing Down OSAC" in [`../README.md`](../README.md).

---

## 12. Troubleshooting

Failed hook jobs are retained for inspection. To find them, run the following
command and then view the logs for each job:

```console
$ oc get pods -n <namespace> | grep -E 'validate|db-init|publish-templates|bootstrap'
```

### 12.1 The pre-installation validation hook fails

View the hook log:

```console
$ oc logs job/osac-pre-install-validate -n <namespace>
```

- `cert-manager CRDs not found`: cert-manager is not installed, or its CRDs are
  in a different API group. Install cert-manager. Set `certManager.enabled` to
  `false` only when a cert-manager distribution is present.
- `BareMetalHost CRD ... not found`, `No Provisioning CR found`, or
  `Provisioning CR has watchAllNamespaces: false`: these are BMaaS Metal3
  prerequisites. Install BareMetalOperator and create or patch a `Provisioning`
  resource with `spec.watchAllNamespaces: true`, or set `bmf.metal3.enabled` to
  `false`.
- `No default StorageClass found`: this is a warning, not a failure. Keycloak
  and PostgreSQL persistent volume claims remain `Pending`. Set a default
  storage class. See [Section 2.1](#21-cluster-and-access).

### 12.2 A Helm release is stuck in `pending-install` or `pending-upgrade`

A hook is still running or has failed. To find it, run the following command:

```console
$ for ns in osac-deps osac-infra <namespace>; do oc get jobs,pods -n "$ns" | grep -Ev 'Complete|Running'; done
```

If a previous attempt was interrupted, clear the stuck release before you
retry:

```console
$ helm uninstall <release> -n <namespace> --no-hooks
```

The `osac-infra` release uses `--wait-for-jobs`, so a hanging `configure-*` hook
blocks the whole phase.

### 12.3 An Operator CSV never reaches `Succeeded`

```console
$ oc get subscription,installplan,csv -n <operator_namespace>
$ oc get pods -n openshift-marketplace
$ oc get packagemanifest | wc -l
```

`ImagePullBackOff` on the marketplace catalog pods, or an empty
`packagemanifest` list, indicates that the cluster pull secret cannot
authenticate to `registry.redhat.io`. Refresh `openshift-config/pull-secret`
(see [Section 2.1](#21-cluster-and-access)) and delete the failed marketplace
pods. The Streams for Apache Kafka `Subscription` uses a manual install plan;
approve its `InstallPlan` in the `osac-kafka` namespace.

### 12.4 `helm dependency build` or an OCI pull fails

The `helm` command requires outbound access to `ghcr.io`, and the `file://`
subcharts require a full checkout. A `not found` error on
`oci://ghcr.io/osac-project/charts/*` usually indicates an incorrect
`--version` value. To list the tags, run the following command:

```console
$ helm show chart oci://ghcr.io/osac-project/charts/osac --version 0.0.8
```

### 12.5 The `osac-db-init` hook fails

- `Secret osac-db-config not found`, `has an empty url key`, or
  `invalid PostgreSQL url`: create the `osac-db-config` and
  `osac-db-client-cert` Secrets before you install the `osac` chart (see
  [Section 4.1](#41-preparing-the-cluster)), or enable `bundledPostgres` for
  evaluation.
- `PostgreSQL Service ... has no ready endpoints`: the host in the database URL
  does not resolve to a running PostgreSQL database. Point `dbInit.host` and the
  URL at a reachable server.

### 12.6 Certificates never become `Ready`

```console
$ oc get certificate,certificaterequest -n <namespace>
$ oc get clusterissuer default-ca
$ oc logs -n cert-manager deploy/cert-manager
```

A missing or not-ready `default-ca` `ClusterIssuer` blocks every downstream
certificate. The `osac-infra` chart creates it when `caIssuer.enabled` is
`true`. If you disabled it, `service.certs.issuerRef` must name an issuer that
exists.

### 12.7 The AAP instance does not start

The `osac-aap-*` pods are stuck, or the `AnsibleAutomationPlatform` custom
resource is not progressing:

```console
$ oc get aap,pods -n <namespace>
$ oc get apiservice v1beta1.metrics.k8s.io
$ oc get csr | grep -c Pending
```

- `Unable to determine if virtual resource` in the AAP custom resource: a
  broken `APIService`, usually `v1beta1.metrics.k8s.io` when metrics-server is
  not ready, makes API discovery fail for the Ansible-based Operator. Fix
  metrics-server, or delete the unavailable `APIService`.
- `tls: internal error` on `oc logs`, `oc debug`, or in the AAP status: pending
  `kubernetes.io/kubelet-serving` CSRs. To approve them, run the following
  command:

  ```console
  $ oc get csr -o name | xargs oc adm certificate approve
  ```

- `label validation error: key "app.kubernetes.io/managed-by" must equal "Helm"`:
  the AAP Operator rewrote the `managed-by` label of the custom resource after
  an interrupted installation. Restore the label and the
  `meta.helm.sh/release-name` and `meta.helm.sh/release-namespace` annotations,
  then reinstall:

  ```console
  $ oc label aap osac-aap -n <namespace> app.kubernetes.io/managed-by=Helm --overwrite
  ```

### 12.8 The `osac-aap-bootstrap` job fails

```console
$ oc logs -f job/osac-aap-bootstrap -n <namespace>
$ oc get secret config-as-code-manifest-ig -n <namespace>
```

Common causes:

- The AAP subscription manifest Secret is missing or invalid. Recreate it. See
  [Section 4.1](#41-preparing-the-cluster).
- AAP is not yet reachable. The job retries up to `aap.bootstrap.backoffLimit`
  times.
- The config-as-code Git source (`aap.configAsCode.projectGitUri` and
  `aap.configAsCode.projectGitBranch`) is unreachable.

### 12.9 The `osac-publish-templates` hook fails (CaaS)

```console
$ oc logs job/osac-publish-templates -n <namespace> -c wait-for-fulfillment
$ oc logs job/osac-publish-templates -n <namespace> -c publish-templates
```

The init container polls the Fulfillment Service REST gateway for up to 600
seconds. The job then launches the `osac-publish-templates` AAP job template
and requires a valid `osac-aap-api-token` Secret. To disable the hook for
VMaaS-only or BMaaS-only installations, set
`aap.instanceGroups.publishTemplates.enabled` to `false`.

### 12.10 The `fulfillment-*` pods are in `CrashLoopBackOff`

```console
$ oc logs deploy/fulfillment-grpc-server -n <namespace>
$ oc logs deploy/fulfillment-controller -n <namespace>
```

- `issuer URL '...' is not trusted`: the `--auth-issuer-url` and `--idp-url`
  values of the service do not match the external host name of Keycloak. These
  values derive from `global.clusterDomain`; confirm that it was set at
  installation and matches `keycloak.route.hostname`.
- Missing `osac-db-config` or controller-credential Secrets: phase 1b did not
  run, or you skipped it without creating the Secrets. See
  [Section 5](#5-installing-when-prerequisites-already-exist).
- `lookup openbao.<namespace>.svc ... no such host` or
  `Failed to provision vault namespace`: `bundledVault.enabled` is `false` but
  `service.vault.endpoint` still points at the in-cluster OpenBao. Enable
  `bundledVault`, point `service.vault.endpoint` at an external Vault, or set it
  to `""` to disable Vault integration.

### 12.11 Web console login loops with an `issuer not trusted` error

This has the same root cause as the Fulfillment Service issuer error: a
`global.clusterDomain` mismatch between the web console OIDC configuration, the
Fulfillment Service, and the Keycloak `KC_HOSTNAME`. Confirm that all three
resolve to `keycloak-keycloak.<cluster_domain>` and run `helm upgrade` again
with the correct `global.clusterDomain`.

### 12.12 The first provisioning request fails while the pods are healthy

- **VMaaS:** `Storage tier "..." is not available for tenant "shared"`, or an
  empty `status.storageClasses` field on the `shared` `Tenant`: the storage
  tier that the create-VM playbook requests is not registered. Check
  `oc get sc --show-labels` for the `osac.openshift.io/tenant` and
  `osac.openshift.io/storage-tier` labels and the `status.storageClasses` field
  of the `Tenant`, and align the requested tier with a labeled storage class.
- Networking custom resources stuck in `PROGRESSING` on a cluster with no real
  fabric: set `operator.controllers.networkingProvisioning` to `false`.
- **CaaS:** watch the AAP `cluster-fulfillment` job for the failing task. Netris
  or Route 53 credential errors, and `NETRIS_RESOURCE_CLASS_MAP` errors, appear
  there.

### 12.13 The make wrapper fails with `[[: not found`

`/bin/sh` is `dash`, for example on Ubuntu or WSL. Run the target with
`make SHELL=/bin/bash`, or use the `oc` and `helm` commands in
[Section 4](#4-installing-osac).

### 12.14 Additional resources

- "Troubleshooting" in [`helm-deployment-guide.md`](helm-deployment-guide.md)
- "Troubleshooting" and "Debug Commands" in [`../README.md`](../README.md)

---

## 13. Glossary

- **Phases** — Phase 1 is the prerequisite Operators (`osac-deps`) and the
  infrastructure layer (`osac-infra`). Phase 2 is the OSAC platform (`osac`
  chart).
- **`osac-deps`, `osac-infra`, `osac`** — The three Helm charts. The `osac`
  chart is published as an OCI artifact; the other two are only in the monorepo.
- **Route A, Route B** — Route A installs only the published `osac` chart onto a
  cluster that already has the prerequisites. Route B installs all three charts
  from a checkout.
- **Operand** — The custom resource that an Operator reconciles, for example
  `HyperConverged` for OpenShift Virtualization, `LVMCluster` for LVM Storage,
  or `IPAddressPool` for MetalLB. The `osac-infra` chart creates these.
- **ClusterServiceVersion (CSV)** — The OLM record of an installed Operator
  version. A status of `Succeeded` means that the Operator is running.
- **Hub** — The OpenShift Container Platform cluster that the OSAC Operator and
  AAP run on and that provisions resources. This guide assumes that the
  Fulfillment Service and the hub are the same cluster.
- **Tenant** — An isolation boundary in OSAC, identified by the
  `osac.openshift.io/tenant` annotation. `shared` is the built-in default
  tenant.
- **VMaaS, CaaS, BMaaS, MaaS** — Virtual machine, cluster, bare metal, and metal
  as a service. The service tiers, toggled by `global.services.*`.
- **ComputeInstance, ClusterOrder, BareMetalInstance** — The user-facing custom
  resources for a virtual machine, a hosted cluster, and a bare-metal machine.
- **Instance group** — An AAP execution group with its own `ConfigMap` and
  Secret of environment variables, for example `cluster-fulfillment`,
  `network-fulfillment`, `storage-fulfillment`, or `publish-templates`.
- **Network backend (`NETWORK_CLASS`)** — How CaaS clusters get networking:
  `esi` (default) or `netris`.
- **DNS backend (`DNS_CLASS`)** — How CaaS clusters get DNS records:
  `dns.route53.dns` (default, AWS Route 53).
- **config-as-code** — The Ansible content that the `osac-aap-bootstrap` job
  loads into AAP. Its subscription manifest is stored in the
  `config-as-code-manifest-ig` Secret.
- **Bundled compared with external** — Bundled PostgreSQL and OpenBao are
  ephemeral in-cluster pods for evaluation. Production deployments use external
  services.
