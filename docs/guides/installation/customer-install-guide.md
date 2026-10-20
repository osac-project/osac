# Installing OSAC on OpenShift Container Platform

**Last Updated**: 2026-10-09
**Audience**: Cloud administrators
**Status**: Draft for review

---

Deploy OSAC (Open Sovereign AI Cloud) onto an existing Red Hat OpenShift
Container Platform cluster by using the OpenShift CLI (`oc`) and Helm. This
guide assumes the prerequisite Operators and infrastructure layer already
exist on the cluster. If you need to set those up yourself, see the
[Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md)
first. The guide covers the VMaaS, CaaS, and BMaaS services.

## Contents

1. [OSAC installation architecture](#1-osac-installation-architecture)
2. [Prerequisites](#2-prerequisites)
3. [OSAC component versions](#3-osac-component-versions)
4. [Installing OSAC](#4-installing-osac)
5. [Helm chart configuration parameters](#5-helm-chart-configuration-parameters)
6. [Installation workflows by service](#6-installation-workflows-by-service)
7. [Verifying the installation](#7-verifying-the-installation)
8. [Postinstallation tasks](#8-postinstallation-tasks)
9. [Supported configurations](#9-supported-configurations)
10. [Uninstalling OSAC](#10-uninstalling-osac)
11. [Troubleshooting](#11-troubleshooting)
12. [Glossary](#12-glossary)

---

## 1. OSAC installation architecture

OSAC installs as three ordered Helm releases. Each release is a plain
`helm upgrade --install` command.

**Phase 1a: `osac-deps`.** Installs Operator Lifecycle Manager (OLM)
`Subscription` resources for the platform Operators in their own namespaces:
cert-manager and Ansible Automation Platform (AAP) by default, and standalone
multicluster engine, LVM Storage, MetalLB, OpenShift Virtualization, and Streams
for Apache Kafka when enabled. Every Operator is gated by a toggle — see
[Table 2.1](#table-21-platform-operators-and-components). Post-installation hooks wait for the cert-manager and AAP
`ClusterServiceVersion` (CSV) resources to reach `Succeeded`.

**Phase 1b: `osac-infra`.** Installs the shared, cluster-scoped resources: the
`default-ca` `ClusterIssuer`, trust-manager, the CA bundle `ConfigMap`,
Keycloak and the `osac` realm in the `keycloak` namespace, and the operand
custom resources (CRs) `HyperConverged`, `LVMCluster`, the MetalLB
`IPAddressPool`, and the Kafka CR. The `configure-*` hooks wait for each
remaining Operator CSV to reach `Succeeded` before applying its operand. For
testing, this phase can also deploy bundled PostgreSQL database and OpenBao.

**Phase 2: `osac`.** Installs the platform into your namespace: the OSAC
Operator and its CRDs, the Fulfillment Service with an Envoy sidecar, an AAP
instance and its bootstrap job, the OSAC web console, metering, the CSI driver,
and the Bare Metal Fulfillment Operator (BMaaS only). It connects to the
bundled OpenBao or the external secret store configured for the deployment.

This guide installs only phase 2. Phase 1a and phase 1b are set up ahead of
time by whoever prepares the cluster — see the
[Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md)
if that's you.

### 1.1 What is released

The OSAC platform chart (phase 2) is published as an OCI artifact at
`oci://ghcr.io/osac-project/charts/osac`. The latest tagged stable release is
`0.0.25`; rolling nightly builds are also published. The chart
includes a `values.schema.json` file and a `values-example.yaml` file.

For a current CaaS installation using the BMaaS worker flow, use `0.0.25` or
a later stable chart release. To match component versions to another chart
tag, inspect its dependencies with `helm show chart`.

To install for a specific service, follow the workflow in
[Section 6](#6-installation-workflows-by-service). Each workflow lists the
toggles and values to set.

---

## 2. Prerequisites

### 2.1 Cluster and access

- You have a Red Hat OpenShift Container Platform 4.22 cluster (4.22.4 to
  4.22.6, channel `stable-4.22`) and the `cluster-admin` role on it.
- A default storage class exists. The pre-installation validation hook issues
  a warning if none exists. To set one, run the following command:

  ```console
  $ oc patch storageclass <storage_class_name> -p '{"metadata":{"annotations":{"storageclass.kubernetes.io/is-default-class":"true"}}}'
  ```

  For more information, see
  [Changing the default storage class](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/storage/dynamic-provisioning#change-default-storage-class_dynamic-provisioning).
- The cluster pull secret (`openshift-config/pull-secret`) authenticates to
  `registry.redhat.io` and `quay.io`. To download a current pull secret, see
  the pull secret page in the
  [Red Hat Hybrid Cloud Console](https://console.redhat.com/openshift/install/pull-secret).
  To apply it, see
  [Updating the global cluster pull secret](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/images/managing-images).
- `github.com`, `ghcr.io`, `quay.io`, and `registry.redhat.io` are reachable
  from the cluster: `github.com` and `ghcr.io` for the OSAC images and the
  `osac-ui` OCI chart, `quay.io` for Keycloak, its PostgreSQL, trust-manager,
  and the `origin-cli` hook image, and `registry.redhat.io` for the Red Hat
  Operator catalogs and operands.
- The command
  `oc get ingresses.config/cluster -o jsonpath='{.spec.domain}'` returns your
  apps domain. The installation derives all route host names from it.

### 2.2 Client tools

- The OpenShift CLI (`oc`), matching the cluster version. The installation and
  its hooks call `oc` and `kubectl`.
- Helm 3.8 or later, for OCI registry support.
- ORAS CLI v1.3.0 or later, for publishing the CaaS QCOW2 image to an OCI
  registry. `--artifact-platform` was added in v1.3.0 and is still
  experimental; see the [ORAS installation guide](https://oras.land/docs/installation/)
  and [v1.3.0 release notes](https://github.com/oras-project/oras/releases/tag/v1.3.0).
- The `osac` CLI, latest release, for postinstallation hub registration and
  day-2 operations (see [Section 8.2](#82-installing-the-osac-cli)). Not
  required for the Helm installation.

Installing the published phase-2 chart requires only `oc` and `helm`.

### 2.3 Platform Operators and components

OSAC relies on the Operators and components in
[Table 2.1](#table-21-platform-operators-and-components). These are typically
installed by whoever prepares the cluster, ahead of your install — see the
[Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md)
if that's you. To install an Operator yourself, use OperatorHub in the web
console, or apply a `Subscription` and `OperatorGroup` from the
`redhat-operators` catalog. For more information, see
[Adding Operators to a cluster](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/operators/user-tasks).

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
| [Streams for Apache Kafka](https://access.redhat.com/articles/6644711) | `stable` | `osac-kafka` | `kafka.enabled` | Fulfillment events and optional metering; an external Kafka cluster can serve fulfillment. See [Kafka configuration](kafka-configuration.md). |
| [OpenShift Virtualization](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/virtualization/installing) | `stable` | `openshift-cnv` | `cnv.enabled` | VMaaS |
| [LVM Storage](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html-single/storage/index#persistent-storage-using-lvms) | `stable-<cluster_minor>` | `openshift-storage` | `lvms.enabled` | VMaaS |
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
- **MetalLB Operator** provides a `LoadBalancer`-class implementation. Any
  solution that provides one works if you disable `metallb.enabled`.
- **multicluster engine for Kubernetes Operator** is required for
  agent-based cluster provisioning but is disabled by default. Set
  `mce.enabled=true` when this installation owns standalone MCE; the `caas-ci`
  infrastructure profile does so explicitly. Leave it `false` when Red Hat
  Advanced Cluster Management for Kubernetes (RHACM) or another MCE
  installation already owns it. ISO-less provisioning requires the Assisted
  components introduced with MCE 5.0; this change was not backported to MCE
  2.x. Until a stable MCE 5.0 catalog is available, the chart remains on
  `stable-2.17` and temporarily overrides the `assisted-service`,
  `assisted-installer-agent`, `assisted-installer`, and
  `assisted-installer-controller` images with MCE 5.0 images, which are
  compatible with MCE 2.x. The override bridge is rendered only when
  `mce.enabled=true`; when RHACM or another external MCE owns the lifecycle,
  ensure it provides compatible Assisted 5.0 images. Once MCE 5.0 is available,
  update the chart's MCE channel/version and retire the temporary overrides.
  See the [MCE dependency values](../../../osac-installer/charts/osac-deps/values.yaml)
  and [Assisted image override values](../../../osac-installer/charts/osac-infra/values.yaml).

### 2.4 Credentials and external services

Always required:

- **Kafka access for the Fulfillment Service**, even when metering is
  disabled. Supply broker addresses, a SCRAM-SHA-512 username and password,
  certificate trust, and topic/group permissions. For an external broker,
  complete the [Kafka configuration guide](kafka-configuration.md) before
  installing OSAC. It includes a Strimzi example and the additional
  requirements for metering.
- **AAP subscription manifest (`license.zip`).** A Subscription Allocation
  export from the [Red Hat Customer Portal](https://access.redhat.com/). The AAP
  bootstrap job cannot start without it. You load it into the
  `config-as-code-manifest-ig` Secret in
  [Section 4.1](#41-preparing-the-cluster). For more information, see
  "Obtaining an AAP License" in
  [`osac-installer/README.md`](https://github.com/osac-project/osac/blob/main/osac-installer/README.md).

Required for a production deployment:

- **An external PostgreSQL 18 or later database.** The bundled PostgreSQL is
  for testing only and is not intended for production. For production, run
  your own database and create the `osac-db-config` and `osac-db-client-cert` Secrets in
  the install namespace, and the `osac-db-metering-config` and
  `osac-db-metering-client-cert` Secrets when metering is enabled. For more
  information, see
  [`fulfillment-service/docs/INSTALL.md`](https://github.com/osac-project/osac/blob/main/fulfillment-service/docs/INSTALL.md).
- **An external secret store.** The bundled OpenBao instance is for testing and
  CI, not production. Before installing the `osac` chart, prepare Vault or
  OpenBao, its Keycloak client, and the Kubernetes Secret and CA bundle used by
  OSAC. See the
  [secrets management configuration guide](secrets-management-configuration.md).

Required for CaaS:

- **DNS backend.** Route 53 is the default and only provider role accepted by
  the current chart schema. AWS credentials are needed when AAP must create or
  delete Route 53 records; they are not required merely to install the chart.
  The runtime `dns.noop.dns` role is not exposed by the chart schema yet; see
  [Section 8.4](#84-installing-without-dns-management) for the documented
  ConfigMap workaround. For details, see
  [`dns-backend.md`](../../../osac-installer/docs/dns-backend.md).
- **Networking backend.** Configure the normal installer path through
  `global.networking`, not by independently wiring a `NetworkClass` and
  manager ConfigMaps. The CaaS path in this guide uses the Netris fabric
  manager on connected IPv4 networking. Helm derives the manager registration,
  `NetworkClass`, and AAP backend settings from that facade. See
  [`network-backend.md`](../../../osac-installer/docs/network-backend.md).
- **BMaaS worker provisioning.** The current CaaS flow provisions bare-metal
  workers on demand through BMaaS. Enable and configure BMaaS, its inventory,
  worker `BareMetalInstanceType`, and the release `DiskImage`/`ClusterVersion`
  association as described in [Section 6.2](#62-installing-osac-for-caas-with-the-netris-network-backend).

Required for BMaaS with the Metal3 backend:

- **BareMetalOperator and a `Provisioning` custom resource.** When
  `bmf.metal3.enabled=true`, the pre-installation validation hook requires the
  `baremetalhosts.metal3.io` CRD and a `Provisioning` resource with
  `spec.watchAllNamespaces: true`. OSAC does not install BareMetalOperator.
- **BMC reachability.** Redfish or IPMI access from the cluster to each host
  BMC, plus DHCP and PXE for Ironic inspection.

### 2.5 Requirements by service

- **VMaaS** (`global.services.vmaas.enabled`) requires OpenShift Virtualization,
  LVM Storage or another dynamic storage class, and MetalLB or another
  `LoadBalancer`-class implementation.
- **CaaS** (`global.services.caas.enabled`) requires multicluster engine
  (standalone, or provided by RHACM if it's installed), MetalLB or another
  `LoadBalancer`-class implementation, LVM Storage or another dynamic storage
  class, a DNS backend, the selected network backend, and both
  `clusterFulfillment` and `networkFulfillment` AAP instance groups. The
  current bare-metal worker flow also requires enabled, configured BMaaS with
  available inventory, a worker `BareMetalInstanceType`, and a `DiskImage`
  associated with the selected `ClusterVersion`. See
  [`aap-configuration.md`](../../../osac-installer/docs/aap-configuration.md)
  and [Section 6.2](#62-installing-osac-for-caas-with-the-netris-network-backend).
- **BMaaS** (`global.services.bmaas.enabled`) requires a configured inventory
  backend. When using Metal3, see the BareMetalOperator and `Provisioning`
  prerequisites in [Section 2.4](#24-credentials-and-external-services).

---

## 3. OSAC component versions

OSAC subscribes to a specific update channel for each platform Operator. The
channel-tracked versions float as the channels publish updates.
[Table 3.1](#table-31-component-versions) lists each channel and the CSV version
that channel resolved to on OpenShift Container Platform 4.22.6 in September
2026. The chart and subchart versions in that table are a dated `0.0.17`
release snapshot, not the current stable tag; treat the observed versions as
indicative.

<a id="table-31-component-versions"></a>
**Table 3.1. Component versions**

| Component | Channel or fixed version | Observed on OCP 4.22.6 | Defined in |
|---|---|---|---|
| OpenShift Container Platform | `stable-4.22` | 4.22.4 to 4.22.6 | Validated range |
| `osac` umbrella chart | `0.0.17` released; `0.0.9-nightly.*` rolling | `0.0.17` | `oci://ghcr.io/osac-project/charts/osac` |
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
| OSAC web console chart | `0.0.6`, pinned at `HEAD` and in release `0.0.17` | Not applicable | `charts/osac/Chart.yaml` |
| PostgreSQL | 18 or later | `18` (Keycloak database) | `fulfillment-service/docs/INSTALL.md` |

The `osac` chart release `0.0.17` pins its subcharts to `osac-operator-crds`
`0.0.14`, `osac-operator` `0.0.14`, `fulfillment-service` `0.0.107`, `osac-aap`
`0.0.15`, `bare-metal-fulfillment-operator` `0.0.14`, and `osac-ui` `0.0.6`
through its `Chart.lock` file.

A tagged release pins every subchart and image to a specific version. Always
install from a tagged release for a real deployment.

---

## 4. Installing OSAC

Install OSAC with `oc` and Helm.

### 4.1 Preparing the cluster

**Prerequisites**

- You are logged in to the cluster as a user with `cluster-admin` privileges.
- The cluster pull secret is in place. See
  [Section 2.1](#21-cluster-and-access).
- You have the AAP subscription manifest file (`license.zip`).
- For a production deployment, your external PostgreSQL database is running and
  reachable from the cluster.
- For a production deployment, your external Vault-compatible store is running
  and reachable from the cluster. See the
  [secrets management configuration guide](secrets-management-configuration.md).

**Procedure**

Run all installation commands in the same shell session.

1. Set the shell variables that the rest of the installation uses:

   ```console
   $ export NS=<namespace>
   $ export DOMAIN=$(oc get ingresses.config/cluster -o jsonpath='{.spec.domain}')
   $ export OCP_VERSION=$(oc get clusterversion version -o jsonpath='{.status.desired.version}' | cut -d. -f1,2)
   $ export AAP_LICENSE_FILE=/path/to/license.zip
   ```

2. Create the target namespace:

   ```console
   $ oc create namespace "$NS" --dry-run=client -o yaml | oc apply -f -
   ```

3. Create the Secret that holds the AAP subscription manifest:

   ```console
   $ oc create secret generic config-as-code-manifest-ig --from-file=license.zip="$AAP_LICENSE_FILE" -n "$NS" --dry-run=client -o yaml | oc apply --server-side -f -
   ```

4. Label the Secret so that the AAP bootstrap job reads it:

   ```console
   $ oc label secret config-as-code-manifest-ig osac.openshift.io/project=osac-aap -n "$NS" --overwrite
   ```

5. For a production deployment, create the database Secrets in `$NS`:
   `osac-db-config` and `osac-db-client-cert`, and, when metering is enabled,
   `osac-db-metering-config` and `osac-db-metering-client-cert`. For more
   information, see
   [`fulfillment-service/docs/INSTALL.md`](https://github.com/osac-project/osac/blob/main/fulfillment-service/docs/INSTALL.md).

   > **Note**
   >
   > The chart pre-installation hook fails if the connection URL that these
   > Secrets carry does not resolve to a ready PostgreSQL Service.

6. For a production deployment, follow the
   [secrets management configuration guide](secrets-management-configuration.md)
   to prepare the Vault configuration.

### 4.2 Configuring the Helm values

You create one values file for the `osac` chart, `my-values.yaml`.

**Procedure**

1. Retrieve the full set of value keys from the chart:

   ```console
   $ helm show values oci://ghcr.io/osac-project/charts/osac --version 0.0.25 > values-upstream.yaml
   ```

2. Create `my-values.yaml` for the `osac` chart. Base it on the Production
   block of the chart `values-example.yaml` file, which uses an external
   PostgreSQL database, an external Keycloak, and pinned image tags.

3. Add the following hardening to `my-values.yaml`:
   ```yaml
   keycloak:
     devFixtures:
       enabled: false
     adminUsername: <admin_user>
     adminPassword: <strong_password>
   bundledPostgres:
     enabled: false
   bundledVault:
     enabled: false
   ```

4. For a production deployment, add `service.vault` to `my-values.yaml` using
   the [secrets management configuration guide](secrets-management-configuration.md#configure-the-osac-instance).

5. Add the service-specific value blocks (`global.services.*`,
   `operator.networkManagers`, `networkClass`, `aap`, `metering`, and `bmf`)
   from [Section 6](#6-installation-workflows-by-service) for the service you
   are installing.

For a full parameter reference, see
[Section 5](#5-helm-chart-configuration-parameters).

### 4.3 Installing OSAC

Use this procedure when the cluster already meets the prerequisites.

**Prerequisites**

- The prerequisite Operators from
  [Table 2.1](#table-21-platform-operators-and-components) are installed.
- The `default-ca` `ClusterIssuer`, trust-manager, the `ca-bundle` `ConfigMap`,
  Keycloak with the `osac` realm, and the credential Secrets exist.
- For a production deployment, the external PostgreSQL database and its
  `osac-db-*` Secrets exist.
- For a production deployment, the external Vault-compatible is configured
  according to the [secrets management configuration guide](secrets-management-configuration.md).
- You completed [Section 4.1](#41-preparing-the-cluster) and
  [Section 4.2](#42-configuring-the-helm-values).

**Procedure**

- Install the `osac` chart by running the following command:

  ```console
  $ helm install osac oci://ghcr.io/osac-project/charts/osac --version 0.0.25 \
      -n "$NS" --create-namespace \
      -f my-values.yaml \
      --set global.clusterDomain="$DOMAIN" \
      --set service.externalHostname="fulfillment-api-$NS.$DOMAIN" \
      --set service.internalHostname="fulfillment-internal-api-$NS.$DOMAIN" \
      --wait --timeout 40m
  ```

  Use a currently published nightly tag only to test unreleased fixes. The AAP
  bootstrap job takes 10 to 40 minutes. Helm does not return until it and, for
  CaaS, the `osac-publish-templates` hook have finished.

  On Helm 4, later `helm upgrade` (or re-running `helm upgrade --install`)
  must include `--force-conflicts`. The AAP operator takes field ownership of
  `app.kubernetes.io/managed-by` on the `osac-aap` custom resource, and Helm
  4 server-side apply fails without that flag. Helm 3 does not accept it.

**Verification**

- Complete [Section 7](#7-verifying-the-installation).

---

## 5. Helm chart configuration parameters

This section lists the parameters you are most likely to set. To retrieve the
complete set, run the following command, and review the `osac` chart
`values.schema.json` file:

```console
$ helm show values oci://ghcr.io/osac-project/charts/osac --version 0.0.25
```

Keys defined in `charts/osac/values.schema.json` are marked `schema`. Keys
marked `subchart` aren't in the umbrella schema; confirm those in the
subchart's own `values.yaml` file.

<a id="table-51-service-enablement-my-valuesyaml"></a>
**Table 5.1. Service enablement (`my-values.yaml`)**

| Parameter | Source | Description | Default |
|---|---|---|---|
| `global.clusterDomain` | schema | Apps domain. All route host names and the default issuer, IdP, and Vault URLs derive from it. Set at installation. | `""` |
| `global.osacDeploymentId` | schema | Required when `metering.enabled` is `true`. A stable identity for this deployment, stored in a retained `ConfigMap`. The chart fails to render without it when metering is on, and fails on upgrade if the value changes — never change it after the first successful install. | Not set |
| `global.services.vmaas.enabled` | schema | Enables the VMaaS tier. | `true` |
| `global.services.caas.enabled` | schema | Enables the CaaS tier. | `true` |
| `global.services.bmaas.enabled` | schema | Enables the BMaaS tier and gates the `bmf` subchart. | `true` |
| `global.services.maas.enabled` | schema | Enables the MaaS tier. | `true` |

**Table 5.2. OSAC Operator (`operator.*`)**

| Parameter | Source | Description |
|---|---|---|
| `operator.image.repository`, `operator.image.tag`, `operator.image.pullPolicy` | schema | Operator image. Use a release tag for production. |
| `operator.replicaCount`, `operator.resources.*` | schema | Operator sizing. |
| `operator.aap.url`, `operator.aap.token`, `operator.aap.insecureSkipVerify`, `operator.aap.statusPollInterval`, `operator.aap.templatePrefix` | schema | How the Operator reaches AAP. Set `insecureSkipVerify: "true"` for self-signed AAP routes. |
| `operator.fulfillment.serverAddress` | schema | Fulfillment gRPC endpoint used by the Operator. |
| `operator.fulfillment.credentials.secretName`, `clientIdKey`, `clientSecretKey` | schema | Name and keys of the Kubernetes Secret holding the `osac-controller` Keycloak service-account credentials. `osac-infra` provisions it for bundled Keycloak; external Keycloak deployments must provide an equivalent Secret. The OSAC chart only references it. |
| `operator.controllers.tenant`, `operator.controllers.networking`, `operator.controllers.storage` | schema | Enable or disable individual reconcilers. Which services run is driven by `global.services.*`. |
| `operator.controllers.networkingProvisioning` | schema | When `false`, networking custom resources reconcile to `READY` without a real fabric. |
| `operator.controllers.volume` | subchart | Enables the Volume reconciler. |
| `operator.configSecret.name`, `operator.configSecret.optional` | schema | Additional Operator configuration Secret. |
| `operator.hubAccess.enabled` | schema | Creates the hub-access `ClusterRole` that the Fulfillment Service binds to. |
| `operator.stall.preparingInfrastructureThreshold`, `operator.stall.controlPlaneStartingThreshold`, `operator.stall.workersJoiningThreshold` | schema | How long a cluster provisioning phase can wait before the Operator marks it stalled. |
| `operator.tenants[]` | subchart | Tenants pre-created at installation. `shared` is the built-in tenant. |
| `operator.networkManagers.fabricManagers.<name>.enabled`, `operator.networkManagers.k8sManagers.<name>.enabled` | subchart | Enables a fabric manager (`netris`, `cudn_net`) or a Kubernetes manager (`k8s_only`). |

**Table 5.3. Networking (umbrella level)**

| Parameter | Source | Description | Default |
|---|---|---|---|
| `global.networking.fabricManager`, `global.networking.k8sManager`, `global.networking.netris.*` | schema | Preferred backend facade. Helm derives the manager registration, default `NetworkClass`, and AAP backend settings. See [`network-backend.md`](../../../osac-installer/docs/network-backend.md). | No fabric; `k8s_only` |
| `operator.networkManagers.fabricManagers.*`, `operator.networkManagers.k8sManagers.*` | schema | Low-level manager registrations. Normally derived from `global.networking`; use directly only for an explicitly managed/custom registration. | Operator defaults |
| `networkClass.enabled`, `networkClass.title`, `networkClass.description`, `networkClass.fabricManager`, `networkClass.k8sManager` | schema | Low-level `NetworkClass` values. Prefer `global.networking`; use the facade's `networkClass` override when a targeted override is needed. | Manager-specific defaults |
| `networkClass.isDefault` | schema | Marks the class the deployment default. Only one `NetworkClass` can exist. | `true` |
| `networkClass.defaults.virtualNetworkIPv4CIDR`, `networkClass.defaults.subnetIPv4CIDR`, `networkClass.defaults.enableNatGateway`, `networkClass.defaults.egressRules` | schema | Tenant-onboarding defaults that auto-create the VirtualNetwork, Subnet, and SecurityGroup. | `10.200.0.0/16` and others |

**Table 5.4. Fulfillment Service (`service.*`, all `schema`)**

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
| `service.vault.endpoint` | Vault-compatible API URL. Defaults to the bundled OpenBao service in `osac-infra`. Set it to your external Vault or OpenBao URL when `bundledVault.enabled` is `false`; the Fulfillment chart requires a nonempty endpoint. See the [secrets management configuration guide](secrets-management-configuration.md). |
| `service.vault.namespace`, `service.vault.kvMountPath`, `service.vault.lifecycleRole`, `service.vault.lifecycleMountPath`, `service.vault.keycloakClientId`, `service.vault.keycloakIssuerUrl`, `service.vault.keycloakAudience`, `service.vault.caBundle`, `service.vault.credentials` | Vault namespace, mount paths, JWT role, Keycloak client and audience, CA bundle, and client credentials. Configure these for an external store as described in the [secrets management configuration guide](secrets-management-configuration.md). |

**Table 5.5. AAP (`aap.*`, all `schema`)**

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
| `aap.instanceGroups.clusterFulfillment.enabled`, `aap.instanceGroups.clusterFulfillment.config`, `aap.instanceGroups.clusterFulfillment.secret` | The `cluster-fulfillment` instance group for CaaS provisioning. See [Section 6.2](#62-installing-osac-for-caas-with-the-netris-network-backend). |
| `aap.instanceGroups.networkFulfillment.enabled`, `aap.instanceGroups.networkFulfillment.config`, `aap.instanceGroups.networkFulfillment.secret` | The `network-fulfillment` instance group for Netris. See [Section 6.2](#62-installing-osac-for-caas-with-the-netris-network-backend). |
| `aap.instanceGroups.storageFulfillment.config.STORAGE_SNAPSHOTS_ENABLED` | Snapshot toggle for the `storage-operations` instance group. |
| `aap.instanceGroups.publishTemplates.enabled` | Runs the postinstallation `osac-publish-templates` hook, which publishes cluster templates (CaaS, MaaS), compute instance templates (VMaaS), bare-metal instance templates, and add-on operators (CaaS) to the fulfillment catalog. Default `true`. Set to `false` only when no enabled service depends on hook-published templates — currently a BMaaS-only deployment — or when all required templates are provisioned separately. |
| `aap.instanceGroups.publishTemplates.config.OSAC_TEMPLATE_COLLECTIONS`, `aap.instanceGroups.publishTemplates.config.OSAC_FULFILLMENT_SERVICE_URI` | The Ansible collections to publish and the internal Fulfillment Service URI. |

**Table 5.6. Web console, BMaaS, and other parameters**

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
| `kafka.enabled` | schema | Creates the Fulfillment Service's Strimzi user and copies its credentials. Set to `false` for an external Kafka connection; this does not disable Kafka use or metering's Strimzi resources. See [Kafka configuration](kafka-configuration.md). | `true` |
| `service.kafka.connection` | schema | ConfigMap/Secret mappings for the required `brokers`, `user`, and `password` parameters. Supply resources in the OSAC namespace. | `fulfillment-service-kafka` Secret |
| `metering.enabled` | schema | Deploys the metering service. Requires Strimzi-managed Kafka, a database connection, and `global.osacDeploymentId` (see [Table 5.1](#table-51-service-enablement-my-valuesyaml) and [Kafka configuration](kafka-configuration.md#optional-metering)). | `false` |
| `metering.reconciliation.interval`, `metering.m360Adapter.enabled`, `metering.m360Adapter.m360.apiUrl`, `metering.m360Adapter.apiKeySecret` | subchart | Reconcile period and the Monetize360 billing adapter. | Not applicable |
| `validation.enabled` | schema | Runs the pre-installation validation hook. | `true` |
| `metallb.enabled`, `metallb.addressCIDR` | schema | In the `osac` chart, creates an `IPAddressPool` and an `L2Advertisement`. Edit the pool after installation to match your network. | `false` and `192.168.40.0/24` |
| `bundledVault.enabled`, `bundledVault.image` | schema | In-cluster OpenBao for testing and CI. Not intended for production. | `true` and `openbao:2.6.2` |
| `hubAccess.enabled` | schema | Creates hub-access RBAC and registers the local cluster as its own hub. Single-cluster development only. | `false` |
| `bundledPostgres.enabled` | schema | Bundled PostgreSQL for testing only; not intended for production. | `false` |
| `dbInit.host` | schema | Host that the `db-init` pre-installation hook connects to, to create the databases. Set it to your external PostgreSQL host for a production deployment. | `postgres.osac-infra.svc.cluster.local` |
| `clusterVersions.enabled`, `clusterVersions.versions[]` | schema | OpenShift Container Platform release images offered to hosted clusters. Each entry has `version`, `image`, and an optional `default`. | `false` and `[]` |

### 5.1 Values set on the command line

The installation command computes and passes the values in
[Table 5.7](#table-57-command-line-values). Add your own with `-f` files or
extra `--set` flags.

<a id="table-57-command-line-values"></a>
**Table 5.7. Command-line values**

| Flag | Value | Purpose |
|---|---|---|
| `--set global.clusterDomain=$DOMAIN` | `oc get ingresses.config/cluster -o jsonpath='{.spec.domain}'` | Route host names and the issuer and IdP URLs. |
| `--set service.externalHostname=fulfillment-api-$NS.$DOMAIN` | Derived | Public API route. |
| `--set service.internalHostname=fulfillment-internal-api-$NS.$DOMAIN` | Derived | Internal API route. |

---

## 6. Installation workflows by service

Each workflow gives the phase-2 (`my-values.yaml`) values to add for that
service. It assumes the phase-1 prerequisites for that service (see
[Table 2.1](#table-21-platform-operators-and-components)) already exist on
the cluster. Then follow [Section 4](#4-installing-osac).

### 6.1 Installing OSAC for VMaaS

**Prerequisites**

- OpenShift Virtualization is installed.
- LVM Storage is installed, or another dynamic storage class exists.
- MetalLB is installed, or another `LoadBalancer`-class implementation exists.

**Procedure**

1. In `my-values.yaml`, enable the VMaaS tier and configure fabric-less
   networking. Fabric-less networking creates the VirtualNetwork and Subnet by
   using ClusterUserDefinedNetwork (CUDN), the SecurityGroup by using a
   `NetworkPolicy`, and the ExternalIP by using MetalLB L2:

   The OSAC chart does not deploy the OSAC CSI routing meta-driver. Provider CSI
   setup and tenant storage provisioning are handled separately by the storage
   workflow.

   ```yaml
   global:
     services: { vmaas: { enabled: true }, caas: { enabled: false }, bmaas: { enabled: false }, maas: { enabled: false } }
   lvms:
     enabled: true
   operator:
     networkManagers:
       k8sManagers:
         k8s_only:
           enabled: true
   networkClass:
     fabricManager: ""
     k8sManager: "k8s_only"
   aap:
     instanceGroups:
       publishTemplates:
         enabled: true
   ```

   The `publishTemplates` hook publishes the compute instance
   templates that VMaaS needs. Disable it only if the required
   templates are provisioned separately.

   `lvms: { enabled: true }` runs the `osac` chart's `register-local-storage`
   hook, which creates the `local` `StorageBackend` and `StorageTier` that the
   first `ComputeInstance` needs. Without it, provisioning fails with an empty
   `storage_tier_definitions` even though every pod looks healthy.

2. Add the `service.*`, `aap.configAsCode.*`, Keycloak hardening, and database
   Secret settings from [Section 4.2](#42-configuring-the-helm-values).

3. Install OSAC. See [Section 4.3](#43-installing-osac).

**Verification**

- Complete [Section 7](#7-verifying-the-installation).
- Create a `ComputeInstance` custom resource and confirm that it reaches
  `RUNNING`:

  ```console
  $ oc get computeinstance -A
  ```

### 6.2 Installing OSAC for CaaS with the Netris network backend

The current CaaS flow provisions bare-metal worker nodes on demand through
BMaaS; it does not use a pre-booted static worker-agent pool. This flow is
available in stable chart `0.0.25` and later. Configure Netris through the
chart's `global.networking` facade, which derives the network-manager
registration, `NetworkClass`, and AAP backend variables. The AWS Route 53 DNS
backend is the default and is independent of the network backend. For the full
variable reference, see
[`network-backend.md`](../../../osac-installer/docs/network-backend.md).

**Prerequisites**

- MCE is installed (standalone, or provided by RHACM). ISO-less CaaS requires
  the Assisted 5.0 components; see the compatibility and transition guidance
  in [Section 2.3](#23-platform-operators-and-components).
- MetalLB (or another `LoadBalancer`-class implementation) and LVM Storage
  (or another dynamic storage class) are installed.
- BMaaS is enabled and configured with a usable provider backend, worker
  inventory, and BMC/network reachability. Define the worker
  `BareMetalInstanceType` used by the cluster.
- Publish the worker RHCOS QCOW2 image as an OCI artifact, register it as a
  `DiskImage`, and associate it with the selected `ClusterVersion` before
  provisioning the first cluster.
- The Netris controller, site, tenant, management VPC, and resource-class
  mapping are available. This guide's Netris CaaS path assumes connected IPv4
  networking and one provider-owned networking hub.
- For Route 53-managed DNS, provide credentials with permissions to manage the
  target hosted zone. If OSAC will not manage DNS, follow
  [Section 8.4](#84-installing-without-dns-management) before creating the
  first `ClusterOrder`.
- SSH private keys for the servers and bastion host are still required by the
  current CaaS AAP workflow; this is separate from the removed static worker
  pool.

#### Publish the worker RHCOS image

Publish the bootable, whole-disk QCOW2 image to an OCI-compliant registry
reachable from the BMaaS provisioning environment. For a private registry,
authenticate for the push and configure read credentials for the provisioning
environment. Registering a `DiskImage` stores its reference only; it does not
fetch the artifact or verify registry access. Replace the example registry,
repository, tag, and file path with your values. For a private registry, set
`REGISTRY_USER` and `REGISTRY_PASSWORD` through a secure mechanism rather than
putting passwords in shell history; omit the login command for a public
registry.

```console
$ export REGISTRY="registry.example.com"
$ export REPOSITORY="osac/rhcos"
$ export TAG="4.22.0"
$ export QCOW2="/path/to/rhcos-4.22.0-x86_64.qcow2"
$ printf '%s' "$REGISTRY_PASSWORD" | oras login \
    --username "$REGISTRY_USER" --password-stdin "$REGISTRY"
$ oras push -a disktype=qcow2 --artifact-platform linux/amd64 \
    "$REGISTRY/$REPOSITORY:$TAG" "$QCOW2"
```

The annotation marks the artifact as QCOW2 and the platform metadata identifies
this example as Linux/AMD64; use the platform matching your image and worker
architecture. `--artifact-platform` is experimental in ORAS 1.3; check the
[ORAS `push` documentation](https://oras.land/docs/commands/oras_push/) for
version-specific behavior. Use the published reference as the `DiskImage`
`source_ref`, for example
`oci://$REGISTRY/$REPOSITORY:$TAG`. Prefer the manifest digest printed by
`oras push` for an immutable reference:
`oci://$REGISTRY/$REPOSITORY@sha256:<digest>`. For the OSAC `DiskImage`
registration steps, see the
[Bare Metal Instance DiskImage guide](../developer/baremetalinstance-guide.md).

**Procedure**

1. In `my-values.yaml`, enable CaaS and BMaaS, select Netris through the
   `global.networking` facade, and enable both AAP instance groups. Helm
   derives `NETWORK_CLASS`, `NETWORK_STEPS_COLLECTION`, and the shared Netris
   values; do not set those derived values directly.

   ```yaml
   global:
     services:
       caas: { enabled: true }
       vmaas: { enabled: false }
       bmaas: { enabled: true }
       maas: { enabled: false }
     networking:
       fabricManager: netris
       k8sManager: ""
       netris:
         controllerUrl: "https://netris.example.com"
         credentials:
           username: "netris"
           externalSecret: true
         siteId: "5"
         tenantId: "1"
         tenantName: "Admin"
         mgmtVpcId: "4"
         mgmtVpcName: "RH-Infra"
         resourceClassMap: '{"fc430":{"server_cluster_template_id":89,"mgmt_interface":"ens4","vpc_interfaces":["ens13"]}}'
   clusterVersions:
     enabled: true
     versions:
       - version: "4.22.0"
         image: "quay.io/openshift-release-dev/ocp-release:4.22.0-multi"
         default: true
   aap:
     instanceGroups:
       publishTemplates:
         enabled: true
       clusterFulfillment:
         enabled: true
         config:
           DNS_CLASS: "dns.route53.dns"
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
   ```

   `global.networking.netris.resourceClassMap` is a JSON string. Each key is a
   resource-class name;
   `server_cluster_template_id` is the Netris server-cluster template,
   `mgmt_interface` is the management NIC, and `vpc_interfaces` are the
   data-plane NICs. Set the Netris password in the `netris-credentials` Secret
   in the AAP namespace because this example uses `externalSecret: true`.

2. Put Route 53 and SSH credentials in a separate values file excluded from
   version control. Omit the AWS keys if using the no-op DNS procedure in
   Section 8.4.

   ```yaml
   aap:
     instanceGroups:
       clusterFulfillment:
         secret:
           AWS_ACCESS_KEY_ID: "<route53_access_key_id>"
           AWS_SECRET_ACCESS_KEY: "<route53_secret_access_key>"
           SERVER_SSH_KEY: |
             <contents_of_your_openssh_private_key_file>
           SERVER_SSH_BASTION_KEY: |
             <contents_of_your_openssh_bastion_private_key_file>
   ```

3. Before creating a `ClusterOrder`, confirm that BMaaS has eligible worker
   hosts and that the selected `BareMetalInstanceType` and `ClusterVersion`
   resolve to the intended physical profile and RHCOS `DiskImage`. BMaaS
   provisions each worker BMI on demand, performs the physical network handoff,
   and discovers the tenant-network IP; do not pre-create or import a static
   pool of worker Agents.

4. Install OSAC as described in [Section 4.3](#43-installing-osac), passing
   every values file, for example `helm ... --version 0.0.25 -f
   my-values.yaml -f my-secrets.local.yaml ...`. Use `0.0.25` or a later
   stable chart release for this flow. For AAP group details, see
   [`aap-configuration.md`](../../../osac-installer/docs/aap-configuration.md);
   for DNS setup, see [`dns-backend.md`](../../../osac-installer/docs/dns-backend.md).

**Verification**

- Complete [Section 7](#7-verifying-the-installation), including step 10.
- Create a `ClusterOrder` and watch its status, the on-demand BMaaS BMI
  lifecycle, and the AAP `cluster-fulfillment` and `network-fulfillment` jobs.

### 6.3 Installing OSAC for BMaaS

**Prerequisites**

- BareMetalOperator is installed, with a `Provisioning` custom resource that has
  `spec.watchAllNamespaces: true`. OSAC does not install these; the
  pre-installation validation hook fails if they are missing.

**Procedure**

1. In `my-values.yaml`, enable the BMaaS tier and the Metal3 backend:

   ```yaml
   global:
     services: { bmaas: { enabled: true }, vmaas: { enabled: false }, caas: { enabled: false }, maas: { enabled: false } }
   operator:
     controllers:
       networkingProvisioning: false
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
       publishTemplates:
         enabled: false
   ```

   Setting `publishTemplates.enabled` to `false` is safe here
   because a BMaaS-only deployment does not consume templates
   published by this hook. Set `bmf.metal3.namespace` to the
   namespace where your `BareMetalHost` resources live.

2. Install OSAC. See [Section 4.3](#43-installing-osac).

**Verification**

- Complete [Section 7](#7-verifying-the-installation).
- Confirm that Metal3 is ready:

  ```console
  $ oc get provisioning
  $ oc get baremetalhosts -A
  ```

- Create a `BareMetalPool` or `BareMetalInstance` custom resource and confirm
  that the Bare Metal Fulfillment Operator reconciles it.

---

## 7. Verifying the installation

Perform the following steps in order. Each step assumes that the previous steps
passed. If a step fails, see [Section 11](#11-troubleshooting).

**Procedure**

1. Check the Helm releases. The `osac` release must show `STATUS: deployed`. A
   status of `pending-install` or `pending-upgrade` means that a hook is still
   running or has failed.

   ```console
   $ helm list -A | grep osac
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

5. Check the infrastructure layer:

   ```console
   $ oc get pods -n osac-infra
   ```

   If `keycloak.enabled` is `true`, also check that the `keycloak-service` and
   `keycloak-database` pods are `Running`. Skip this check for an external
   Keycloak.

   ```console
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
    $ oc extract secret/default-ca -n cert-manager --keys=tls.crt --to=- > default-ca.crt
    $ osac login --address "$ROUTE" --token-script "oc create token fulfillment-controller -n <namespace> --duration 1h" --ca-file default-ca.crt
    $ osac get tenants
    ```

    Use `--ca-file` to trust the route's certificate, including the
    self-signed `default-ca`. Use `--insecure` (skips certificate
    verification) only for evaluation, never in production.

12. Check the web console. Open the console URL in a browser. The URL must
    redirect to Keycloak and, after you log in, show the OSAC console. Skip
    this step if `ui.enabled` is `false`.

    ```console
    $ UI=$(oc get route osac-ui -n <namespace> -o jsonpath='{.spec.host}')
    $ curl -skI "https://$UI"
    ```

    The response must be `200` or a `302` redirect to Keycloak.

13. Run a service smoke test, as described in the Verification section of the
    workflow in [Section 6](#6-installation-workflows-by-service) for your
    service.

---

## 8. Postinstallation tasks

### 8.1 Accessing the OSAC consoles

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
  described in step 11 of [Section 7](#7-verifying-the-installation).
- **AAP.** Log in as `admin`. To retrieve the password, run the following
  command:

  ```console
  $ oc extract secret/osac-aap-admin-password -n <namespace> --to -
  ```

- **Keycloak admin console.** Log in with the `keycloak.adminUsername` and
  `keycloak.adminPassword` values.

### 8.2 Installing the `osac` CLI

Download the CLI version matching the `fulfillment-service` subchart pinned
by your `osac` chart release (`0.0.115` for chart `0.0.25`; verify the
subchart version with `helm show chart` for other releases). Releases are tagged
and published on the monorepo, not on the `fulfillment-service` repository
itself:

```console
$ curl -L -o osac https://github.com/osac-project/osac/releases/download/fulfillment-service/v0.0.115/osac_Linux_x86_64
$ chmod +x osac
$ sudo mv osac /usr/local/bin/
```

### 8.3 Registering the hub

The hub is the OpenShift Container Platform cluster that the OSAC Operator
and AAP run on and that provisions resources. This procedure applies when the
Fulfillment Service and the hub run on the same cluster.

**Procedure**

1. Log in to the Fulfillment Service:

   ```console
   $ osac login \
       --address "$(oc get route fulfillment-api -n <namespace> -o jsonpath='{.spec.host}')" \
       --token-script "oc create token fulfillment-controller -n <namespace> --duration 1h"
   ```

2. Generate the hub-access kubeconfig file:

   ```console
   $ curl -sO https://raw.githubusercontent.com/osac-project/osac/refs/tags/osac/v0.0.25/osac-installer/scripts/create-hub-access-kubeconfig.sh
   $ chmod +x create-hub-access-kubeconfig.sh
   $ ./create-hub-access-kubeconfig.sh
   ```

3. Register the hub:

   ```console
   $ osac create hub --kubeconfig=kubeconfig.hub-access --id <hub_name> --namespace <namespace>
   ```

If the API route presents a certificate your client doesn't trust, such as
the self-signed `default-ca`, add `--ca-file default-ca.crt` to the
`osac login` command (see step 11 of
[Section 7](#7-verifying-the-installation) for how to extract it). Use
`--insecure` instead only for evaluation, never in production. Add
`--as system:admin` only when your `oc` context cannot mint the token.

For networking, each deployment supports one provider-owned networking hub.
This boundary does not limit the number of hosted or workload clusters that
use that hub; see the
[networking decisions](../../agent-context/networking-decisions.md).

### 8.4 Installing without DNS management

`dns.route53.dns` is the default and only provider role accepted by the current
chart schema. The runtime `dns.noop.dns` role exists, but the chart schema does
not currently allow it in `DNS_CLASS`; the post-install patch below remains
necessary. Helm installation itself does not require AWS credentials. AAP
needs AWS credentials only when Route 53 is used to create or delete records.
To run CaaS without OSAC managing DNS, install normally and then patch the
`cluster-fulfillment-ig` `ConfigMap` before the first `ClusterOrder`.

**Procedure**

1. After installation, and before creating the first `ClusterOrder`, patch
   the `ConfigMap`:

   ```console
   $ export NS=<namespace>
   $ oc -n "$NS" patch cm cluster-fulfillment-ig --type merge -p '{
       "data": {
         "DNS_CLASS": "dns.noop.dns",
         "EXTERNAL_ACCESS_BASE_DOMAIN": "<guest_domain>",
         "HOSTED_CLUSTER_BASE_DOMAIN": "<guest_domain>",
         "EXTERNAL_ACCESS_SUPPORTED_BASE_DOMAINS": "<supported_domain>"
       }
     }'
   ```

2. Confirm the values took:

   ```console
   $ oc get cm cluster-fulfillment-ig -n "$NS" -o yaml \
       | grep -E 'DNS_CLASS|EXTERNAL_ACCESS|HOSTED_CLUSTER'
   ```

3. Create these DNS records yourself before provisioning; `dns.noop.dns` skips
   DNS changes, so it does not create or delete records for you:

   ```text
   api.<cluster>.<guest_domain>       -> API endpoint
   api-int.<cluster>.<guest_domain>   -> API endpoint
   *.apps.<cluster>.<guest_domain>    -> ingress endpoint
   ```

   The Netris workflow still waits for `api`, `api-int`, and
   `default-router.apps.<cluster>.<guest_domain>` to resolve after the no-op
   task. The wildcard apps record covers the default-router name. Ensure the
   records resolve from the hub, the AAP execution environment, the managed
   host, and the hosted cluster.

Reapply the patch after any Helm upgrade or infrastructure reinstall that
recreates this `ConfigMap`; the post-install override is not retained in chart
values.

### 8.5 Additional resources

- CaaS network backend configuration:
  [`network-backend.md`](https://github.com/osac-project/osac/blob/main/osac-installer/docs/network-backend.md)
- CaaS DNS backend configuration:
  [`dns-backend.md`](https://github.com/osac-project/osac/blob/main/osac-installer/docs/dns-backend.md)
- AAP instance group configuration:
  [`aap-configuration.md`](https://github.com/osac-project/osac/blob/main/osac-installer/docs/aap-configuration.md)

---

## 9. Supported configurations

### 9.1 Supported for production

- Deployment onto an existing OpenShift Container Platform cluster with
  `cluster-admin` privileges.
- The published `osac` chart at a tagged stable release, such as `0.0.25`.
  The chart `values-example.yaml` file documents the Production block: an external
  PostgreSQL database, an external Keycloak, and pinned image tags.
- Prerequisite Operators and infrastructure already present on the cluster —
  installed by whoever prepares the cluster using the
  [Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md).
- An external PostgreSQL 18 or later database with the `osac-db-*` Secrets
  created in advance. See
  [Section 2.4](#24-credentials-and-external-services).
- An external Vault-compatible store configured with a parent namespace,
  Keycloak JWT role, and `service.vault` values. See the
  [secrets management configuration guide](secrets-management-configuration.md).
- An external Keycloak configured with the `osac` realm, clients, and roles
  through `service.auth` and `service.idp`.
- A single OSAC installation hub cluster. Networking has a separate boundary:
  one provider-owned networking hub per deployment, with multiple hosted or
  workload clusters able to use it (see [Section 8.3](#83-registering-the-hub)).

### 9.2 Evaluation only

- The bundled PostgreSQL database (`bundledPostgres.enabled: true`) is for
  testing only and is not intended for production.
- The bundled OpenBao secret store (`bundledVault.enabled: true`). It is for
  testing and CI, not production.
- `keycloak.devFixtures.enabled: true` and the default `admin` Keycloak
  credentials.
- Development or CI reference values files and `main`/`latest` image tags.

### 9.3 Not covered by this guide

- Setting up the phase-1 prerequisites yourself. See the
  [Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md).
- Detailed configuration of an external Keycloak. The chart accepts an external
  Keycloak through `service.auth` and `service.idp`, but realm and client
  provisioning is out of scope. See
  [`fulfillment-service/docs/INSTALL.md`](https://github.com/osac-project/osac/blob/main/fulfillment-service/docs/INSTALL.md).
- Disconnected, IPv6, or dual-stack networking, and deployments requiring
  more than one provider-owned networking hub.
- CaaS VM worker nodes or multi-NIC cluster attachments; this CaaS flow uses
  BMaaS-backed bare-metal worker nodes and a single network attachment.

### 9.4 CaaS support boundary

| CaaS profile | Status | Boundary |
|---|---|---|
| On-demand bare-metal workers through BMaaS | Available in stable chart `0.0.25` and later | Workers are provisioned on demand; this replaces the static pre-booted agent pool. Availability in the chart does not certify a particular provider deployment. |
| Netris-backed CaaS networking | Supported chart configuration | Requires connected IPv4 networking and one provider-owned networking hub per deployment. Multiple hosted/workload clusters may use that hub. |
| `cudn_net` with `ci.steps` / virtual BM workers | CI-only | Not a production physical-Netris profile. |
| `agentless_net` | Limited baseline; not a complete CaaS backend | Do not use it to claim full production CaaS provisioning. |
| Disconnected, IPv6/dual-stack, or multi-network-hub CaaS; VM workers or multi-NIC attachments | Not supported or covered by this flow | The documented CaaS path is connected IPv4 with bare-metal workers and one cluster network attachment. |

The table describes shipped chart/configuration scope, not a guarantee that a
site's hardware, BMC access, fabric, or DNS setup has been validated. Complete
the environment-specific integration testing before production rollout.

---

## 10. Uninstalling OSAC

**Procedure**

1. Uninstall the `osac` release:

   ```console
   $ helm uninstall osac -n "$NS"
   ```

2. The CRDs are retained because they carry the
   `helm.sh/resource-policy: keep` annotation. To remove them, run the
   following command:

   ```console
   $ oc delete crd -l app.kubernetes.io/part-of=osac
   ```

If your platform team also installed the phase-1 prerequisites for this
deployment and they need to come down too, see "Uninstall" in the
[Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md#uninstall).
Do not remove shared prerequisites on a cluster used by other deployments.

---

## 11. Troubleshooting

Failed hook jobs are retained for inspection. To find them, run the following
command and then view the logs for each job:

```console
$ oc get pods -n <namespace> | grep -E 'validate|db-init|publish-templates|bootstrap'
```

### 11.1 The pre-installation validation hook fails

View the hook log:

```console
$ oc logs job/osac-pre-install-validate -n <namespace>
```

- `cert-manager CRDs not found`: cert-manager is not installed, or its CRDs are
  in a different API group. Install cert-manager. Set `certManager.enabled` to
  `false` only when a cert-manager distribution is present.
- `BareMetalHost CRD ... not found`, `No Provisioning CR found`, or
  `Provisioning CR has watchAllNamespaces: false`: these are BMaaS Metal3
  prerequisites. Install BareMetalOperator, then patch the `Provisioning`
  resource:

  ```console
  $ oc patch provisioning provisioning-configuration --type merge -p '{"spec":{"watchAllNamespaces": true}}'
  ```

  Or set `bmf.metal3.enabled` to `false`.
- `No default StorageClass found`: this is a warning, not a failure. Keycloak
  and PostgreSQL persistent volume claims remain `Pending`. Set a default
  storage class. See [Section 2.1](#21-cluster-and-access).

### 11.2 A Helm release is stuck in `pending-install` or `pending-upgrade`

A hook is still running or has failed. To find it, run the following command:

```console
$ oc get jobs,pods -n <namespace> | grep -Ev 'Complete|Running'
```

If a previous attempt was interrupted, clear the stuck release before you
retry:

```console
$ helm uninstall osac -n <namespace> --no-hooks
```

### 11.3 An Operator CSV never reaches `Succeeded`

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

### 11.4 An OCI pull fails

A `not found` error on `oci://ghcr.io/osac-project/charts/osac` usually
indicates an incorrect `--version` value. To list the tags, run the following
command:

```console
$ helm show chart oci://ghcr.io/osac-project/charts/osac --version 0.0.25
```

### 11.5 The `osac-db-init` hook fails

- `Secret osac-db-config not found`, `has an empty url key`, or
  `invalid PostgreSQL url`: create the `osac-db-config` and
  `osac-db-client-cert` Secrets before you install the `osac` chart (see
  [Section 4.1](#41-preparing-the-cluster)), or enable `bundledPostgres` for
  testing.
- `PostgreSQL Service ... has no ready endpoints`: the host in the database URL
  does not resolve to a running PostgreSQL database. Point `dbInit.host` and the
  URL at a reachable server.

### 11.6 Certificates never become `Ready`

```console
$ oc get certificate,certificaterequest -n <namespace>
$ oc get clusterissuer default-ca
$ oc logs -n cert-manager deploy/cert-manager
```

A missing or not-ready `default-ca` `ClusterIssuer` blocks every downstream
certificate. If you disabled `caIssuer.enabled`, `service.certs.issuerRef`
must name an issuer that exists.

### 11.7 The AAP instance does not start

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

- a field-ownership conflict on `app.kubernetes.io/managed-by` owned by the
  AAP operator (Helm 4): add `--force-conflicts` to the `helm upgrade`
  command. The operator takes ownership of that field on the `osac-aap`
  custom resource after install.

### 11.8 The `osac-aap-bootstrap` job fails

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

### 11.9 The `osac-publish-templates` hook fails

```console
$ oc logs job/osac-publish-templates -n <namespace> -c wait-for-fulfillment
$ oc logs job/osac-publish-templates -n <namespace> -c publish-templates
```

The hook publishes cluster templates (CaaS, MaaS), compute instance
templates (VMaaS), bare-metal instance templates, and add-on operators
(CaaS) to the fulfillment catalog. The init container polls the
Fulfillment Service REST gateway for up to 600 seconds. The job then
launches the `osac-publish-templates` AAP job template and requires a
valid `osac-aap-api-token` Secret. Set
`aap.instanceGroups.publishTemplates.enabled` to `false` only when no
enabled service depends on hook-published templates — currently a
BMaaS-only deployment — or when all required templates are provisioned
separately.

### 11.10 The `fulfillment-*` pods are in `CrashLoopBackOff`

```console
$ oc logs deploy/fulfillment-grpc-server -n <namespace>
$ oc logs deploy/fulfillment-rest-gateway -n <namespace>
$ oc logs deploy/fulfillment-controller -n <namespace>
```

- `issuer URL '...' is not trusted`: the `--auth-issuer-url` and `--idp-url`
  values of the service do not match the external host name of Keycloak. These
  values derive from `global.clusterDomain`; confirm that it was set at
  installation and matches `keycloak.route.hostname`. To check the value the
  running container actually received:

  ```console
  $ oc get deploy fulfillment-grpc-server -n <namespace> -o jsonpath='{.spec.template.spec.containers[0].args}' | tr ',' '\n' | grep -E 'issuer|idp'
  ```
- Missing `osac-db-config` or controller-credential Secrets: the prerequisite
  infrastructure did not create them. Check with whoever prepared the cluster.
- `lookup openbao.<namespace>.svc ... no such host` or
  `Failed to provision vault namespace`: `bundledVault.enabled` is `false` but
  `service.vault.endpoint` still points at the in-cluster OpenBao, or the
  external store's namespace, policy, JWT role, or credentials are incorrect.
  Check the endpoint and follow the
  [secrets management configuration guide](secrets-management-configuration.md#troubleshooting).

### 11.11 Web console login loops with an `issuer not trusted` error

This has the same root cause as the Fulfillment Service issuer error: a
`global.clusterDomain` mismatch between the web console OIDC configuration, the
Fulfillment Service, and the Keycloak `KC_HOSTNAME`. Confirm that all three
resolve to `keycloak-keycloak.<cluster_domain>` and run `helm upgrade` again
with the correct `global.clusterDomain`.

### 11.12 The first provisioning request fails while the pods are healthy

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

### 11.13 Additional resources

- "Troubleshooting" in the
  [Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md#troubleshooting)
- "Troubleshooting" and "Debug Commands" in
  [`osac-installer/README.md`](https://github.com/osac-project/osac/blob/main/osac-installer/README.md)

---

## 12. Glossary

- **Phases** — Phase 1 is the prerequisite Operators (`osac-deps`) and the
  infrastructure layer (`osac-infra`). Phase 2 is the OSAC platform (`osac`
  chart). This guide installs phase 2 only.
- **`osac-deps`, `osac-infra`, `osac`** — The three Helm charts. The `osac`
  chart is published as an OCI artifact; the other two are only in the monorepo.
- **Published-chart install** — Installing only the published `osac` chart
  (phase 2) onto a cluster where the prerequisites already exist. See the
  [Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md)
  if you need to set up the prerequisites yourself.
- **Operand** — The custom resource that an Operator reconciles, for example
  `HyperConverged` for OpenShift Virtualization, `LVMCluster` for LVM Storage,
  or `IPAddressPool` for MetalLB. The `osac-infra` chart creates these.
- **ClusterServiceVersion (CSV)** — The OLM record of an installed Operator
  version. A status of `Succeeded` means that the Operator is running.
- **Hub** — The OpenShift Container Platform cluster that the OSAC Operator and
  AAP run on and that provisions resources. This guide assumes that the
  Fulfillment Service runs on the hub, that is, the hub and the cluster
  running the Fulfillment Service are the same cluster.
- **Tenant** — An isolation boundary in OSAC, identified by the
  `osac.openshift.io/tenant` annotation. `shared` is the built-in tenant used
  for resources shared across all tenants.
- **VMaaS, CaaS, BMaaS, MaaS** — Virtual machine, cluster, bare-metal host,
  and raw-metal access as a service (per the chart schema; distinct from
  BMaaS). The service tiers, toggled by `global.services.*`.
- **ComputeInstance, Cluster, BareMetalInstance** — The user-facing resources
  for a virtual machine, a hosted cluster, and a bare-metal machine.
- **Instance group** — An AAP execution group with its own `ConfigMap` and
  Secret of environment variables, for example `cluster-fulfillment`,
  `network-fulfillment`, `storage-fulfillment`, or `publish-templates`.
- **Network backend (`NETWORK_CLASS`)** — How CaaS clusters get networking,
  driven by the `NetworkClass` custom resource's registered fabric and
  Kubernetes managers. Netris is the documented fabric manager.
- **DNS backend (`DNS_CLASS`)** — How CaaS clusters get DNS records:
  `dns.route53.dns` (default, AWS Route 53).
- **config-as-code** — The Ansible content that the `osac-aap-bootstrap` job
  loads into AAP. Its subscription manifest is stored in the
  `config-as-code-manifest-ig` Secret.
- **Bundled compared with external** — Bundled PostgreSQL is for testing, and
  OpenBao is for testing and CI. Production deployments use external services.
