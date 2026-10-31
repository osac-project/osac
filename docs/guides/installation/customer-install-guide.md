# Installing OSAC on OpenShift Container Platform (WIP)

**Last Updated**: 2026-10-30
**Audience**: Cloud administrators
**Status**: WIP — see the TBD notes in [Section 6.2](#62-installing-osac-for-caas-with-the-netris-network-backend)
and [Section 8.3](#83-registering-the-hub) for what's still open

---

Deploy OSAC (Open Sovereign AI Cloud) onto an existing Red Hat OpenShift
Container Platform cluster by using the OpenShift CLI (`oc`) and Helm. This
guide covers the VMaaS, CaaS, and BMaaS services and assumes that your
platform administrators have prepared the prerequisite Operators and external
services listed in [Section 2](#2-prerequisites).

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

Install the published `osac` Helm chart into your namespace with
`helm upgrade --install`. The chart deploys the OSAC Operator and its CRDs,
the Fulfillment Service with an Envoy sidecar, an Ansible Automation
Platform (AAP) instance and its bootstrap job, the OSAC web console, metering,
and the Bare Metal Fulfillment Operator (BMaaS only).

The chart connects to the certificate issuer, CA bundle, Keycloak realm,
PostgreSQL database, and Vault-compatible secret store supplied by your
platform administrators. These services and the required platform Operators
must be ready before you install OSAC. See [Section 2](#2-prerequisites).

### 1.1 What is released

The OSAC platform chart is published as an OCI artifact at
`oci://ghcr.io/osac-project/charts/osac`. Find the latest umbrella version on
the [OSAC releases page](https://github.com/osac-project/osac/releases) by
selecting a release with an `osac-release/vX.Y.Z` tag. The repository also lists
individual component releases, such as `osac-operator`; use the `osac-release/v`
tag prefix to identify the umbrella chart. If its release page is not yet
available, select a stable `osac-release/vX.Y.Z` tag from the
[OSAC tags page](https://github.com/osac-project/osac/tags), then confirm
that the chart is available in OCI using `helm show chart` as described in
[Section 3](#3-osac-component-versions).

A stable release's `osac-release/vX.Y.Z` tag and matching `osac/vX.Y.Z`
source tag point to the same commit. The source checkout and download
commands below use `osac/v`; nightly source tags also use this prefix.

For evaluation builds, check the
[OSAC Nightly Build workflow](https://github.com/osac-project/osac/actions/workflows/nightly-build.yaml)
and select the newest successful run. Use its exact chart version; a newer
tag alone does not mean that the full pipeline succeeded. The chart
includes a `values.schema.json` file and a `values-example.yaml` file.

Select an umbrella chart version from the release links above and export it
once. The following example uses the published stable
[`osac-release/v0.0.63` release](https://github.com/osac-project/osac/releases/tag/osac-release%2Fv0.0.63):

```console
export OSAC_RELEASE_VERSION="0.0.63"
```

> **Note**
>
> The install preflight described in
> [Section 11.1](#111-the-install-preflight-stops-the-installation) is in
> charts built after 30 October 2026: releases after
> `osac-release/v0.0.63` and nightly builds from after that date. Earlier
> charts, including the `0.0.63` example above, run a shell validation hook
> instead and do not behave as described there. To evaluate the preflight,
> choose a nightly from the workflow above that was built after 30 October
> 2026; its chart contains
> `templates/preflight/_engine.tpl`.

Use only the chart version, without either tag prefix. For evaluation, set
`OSAC_RELEASE_VERSION` to the exact nightly version from that successful run.
Select the version before retrieving chart values and keep it unchanged
throughout the installation. Run the remaining commands in the same shell
session; they use this variable for the chart version and matching source
downloads.

To install for a specific service, follow the workflow in
[Section 6](#6-installation-workflows-by-service). Each workflow lists the
toggles and values to set.

---

## 2. Prerequisites

### 2.1 Cluster and access

- You have a Red Hat OpenShift Container Platform cluster, version 4.22 or
  later (validated on 4.22.4 to 4.22.6, channel `stable-4.22`), and the
  `cluster-admin` role on it.
- A default storage class exists. The install preflight stops a new
  installation that includes the AAP instance or CaaS if none exists, because
  the volume claims would stay `Pending` (CaaS hosted control planes provision
  their etcd volumes from it); it only warns on an upgrade. To set one, run
  the following command:

  ```console
  oc patch storageclass <storage_class_name> -p '{"metadata":{"annotations":{"storageclass.kubernetes.io/is-default-class":"true"}}}'
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
- Helm 3.8 or later, for OCI registry support. Use Helm 3.18 or later, or
  Helm 4, so that a failed hook job prints its log in the Helm output; older
  versions only report that the job failed. The install preflight runs on the
  machine where you run Helm, with your `kubeconfig`, so that machine needs
  `cluster-admin` access to the cluster (see
  [Section 11.1](#111-the-install-preflight-stops-the-installation)).
- The `osac` CLI matching the selected chart's `fulfillment-service` version,
  for postinstallation verification, hub registration, and day-2 operations
  (see [Section 8.2](#82-installing-the-osac-cli)). Nightly component versions
  require Git and Go to build the CLI from source. The CLI is not required
  for the Helm installation.

Installing the published `osac` chart requires only `oc` and `helm`.

### 2.3 Platform Operators and components

OSAC relies on the Operators and components in
[Table 2.1](#table-21-platform-operators-and-components). Ask your platform
administrators to prepare the required Operators and components before you
install OSAC.

The Channel column lists the reference update channels for prerequisite
Operators. Their resolved `ClusterServiceVersion` (CSV) versions change as
the channels publish updates. The versions observed on OpenShift Container
Platform 4.22.6 in September 2026 are listed in
[Section 3](#3-osac-component-versions).

<a id="table-21-platform-operators-and-components"></a>
**Table 2.1. Platform Operators and components**

| Component | Channel | Namespace | Required for |
|---|---|---|---|
| [cert-manager Operator for Red Hat OpenShift](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/security_and_compliance/cert-manager-operator-for-red-hat-openshift) | `stable-v1` | `cert-manager-operator` | All services |
| trust-manager | `v0.20.0` (fixed) | `cert-manager` | All services |
| Configured certificate issuer (default: `default-ca` `ClusterIssuer`) | Not applicable | Cluster-scoped for `ClusterIssuer`; OSAC release namespace for `Issuer` | All services |
| Keycloak and the `osac` realm | Not applicable | Provider-defined | All services |
| [Red Hat Ansible Automation Platform Operator](https://docs.redhat.com/en/documentation/red_hat_ansible_automation_platform/2.6/html/installing_on_openshift_container_platform/index) | `stable-2.6-cluster-scoped` | `ansible-aap` | All services |
| [Streams for Apache Kafka](https://access.redhat.com/articles/6644711) | `stable` | Provider-defined | Fulfillment events and metering; see [Kafka configuration](kafka-configuration.md) for external broker requirements. |
| [OpenShift Virtualization](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/virtualization/installing) | `stable` | `openshift-cnv` | VMaaS |
| [LVM Storage](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html-single/storage/index#persistent-storage-using-lvms) | `stable-<cluster_minor>` | `openshift-storage` | VMaaS |
| [MetalLB Operator](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/networking_operators/metallb-operator) | `stable` | `metallb-system` | VMaaS and CaaS |
| [multicluster engine for Kubernetes Operator](https://docs.redhat.com/en/documentation/red_hat_advanced_cluster_management_for_kubernetes/2.13/html/clusters/cluster_mce_overview) | `stable-2.17` | `multicluster-engine` | CaaS |

Notes on individual components:

- **cert-manager Operator for Red Hat OpenShift** installs into the
  `cert-manager-operator` namespace and creates its operands in the
  `cert-manager` namespace. The `certificates.cert-manager.io` CRD is a
  mandatory install preflight check.
- **trust-manager** distributes the CA bundle. Ensure the CA-bundle
  `ConfigMap` named by `service.certs.caBundle.configMap` exists in the OSAC
  release namespace.
- **Certificate issuer:** `service.certs.issuerRef` defaults to the
  `default-ca` `ClusterIssuer`. Select an existing, ready `Issuer` or
  `ClusterIssuer` for your deployment.
- **Keycloak** provides OIDC for the API and web console. Prepare the `osac`
  realm, clients, roles, and credential Secrets before installing OSAC.
- **Red Hat Ansible Automation Platform Operator** installs the Operator only.
  The `osac` chart creates the AAP instance. You supply the subscription
  manifest. See [Section 2.4](#24-credentials-and-external-services).
- **Streams for Apache Kafka** (formerly AMQ Streams) provides the
  Strimzi resources used by OSAC. Confirm the Operator and Kafka cluster
  are ready with your Kafka administrators.
- **OpenShift Virtualization** was formerly named Container-native
  Virtualization (CNV).
- **LVM Storage** uses a channel that tracks the OpenShift Container Platform
  minor version. Another dynamic storage class can satisfy the VMaaS storage
  prerequisite.
- **MetalLB Operator** provides a `LoadBalancer`-class implementation. Another
  implementation can satisfy this prerequisite.
- **multicluster engine for Kubernetes Operator** is required for agent-based
  cluster provisioning. An existing standalone installation or Red Hat
  Advanced Cluster Management for Kubernetes (RHACM) can provide it.

### 2.4 Credentials and external services

Always required:

- **Kafka access for the Fulfillment Service and metering.** Metering is
  always deployed. With `kafka.enabled: true` in the umbrella `osac` chart,
  the chart creates the Fulfillment Service's `KafkaUser` and copies its
  Strimzi-generated SCRAM credentials into the release namespace. The
  metering chart creates its own Kafka resources. For external Kafka, set
  `kafka.enabled: false` in the umbrella chart and supply broker addresses,
  a SCRAM-SHA-512 username and password, certificate trust, and topic/group
  permissions. Complete the [Kafka configuration guide](kafka-configuration.md)
  before installing OSAC with external Kafka. It includes a Strimzi example
  and the additional requirements for metering.
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
  your own server and set `dbInit.host` in
  [Section 4.2](#42-configuring-the-helm-values). The chart creates the
  `osac-db-config` and `osac-db-metering-config` Secrets, and cert-manager
  creates `osac-db-client-cert` and `osac-db-metering-client-cert` from the
  chart's Certificate resources. Do not pre-create these four Secrets for
  this installation. For manual database preparation, see
  [Prepare external PostgreSQL](helm-deployment-guide.md#prepare-external-postgresql).
- **An external secret store.** The bundled OpenBao instance is for testing and
  CI, not production. Before installing the `osac` chart, prepare Vault or
  OpenBao, its Keycloak client, and the Kubernetes Secret and CA bundle used by
  OSAC. See the
  [secrets management configuration guide](secrets-management-configuration.md).

Required for CaaS:

- **DNS credentials.** The chart schema accepts `dns.route53.dns` (the
  default) and `dns.noop.dns`. Route 53 requires an `AWS_ACCESS_KEY_ID` value
  and an `AWS_SECRET_ACCESS_KEY` value that can manage the target hosted zone.
  `dns.noop.dns` does not. To run without OSAC managing DNS, set
  `DNS_CLASS` as described in
  [Section 8.4](#84-installing-without-dns-management). For more information,
  see
  [`dns-backend.md`](https://github.com/osac-project/osac/blob/main/osac-installer/docs/dns-backend.md).
- **A `NetworkClass` with a registered manager.** OSAC provisions cluster
  networking through a `NetworkClass` custom resource, which requires a
  fabric manager, a Kubernetes manager, or both, to be registered and
  referenced (`networkClass.fabricManager` / `networkClass.k8sManager`). At
  this stage, CaaS assumes the cluster runs on servers that are part of a
  physical fabric managed by a fabric manager, such as Netris. Netris
  requires the controller URL, credentials, site and tenant IDs, and SSH keys
  to the servers and the bastion host. Other fabric and Kubernetes managers
  may be registered on your cluster; check with whoever manages your OSAC
  networking configuration for what's available. For more information, see
  [`network-backend.md`](https://github.com/osac-project/osac/blob/main/osac-installer/docs/network-backend.md).

Required for BMaaS with the Metal3 or NetBox backend:

- **BareMetalOperator and a `Provisioning` custom resource.** When
  either `bmf.metal3.enabled` or `bmf.netbox.enabled` is `true`, the install
  preflight requires the `baremetalhosts.metal3.io` CRD and a `Provisioning`
  resource with `spec.watchAllNamespaces: true`. OSAC does not install
  BareMetalOperator.
- **BMC reachability (Metal3 and NetBox).** Redfish or IPMI access from the
  cluster to each host BMC. When BMO/Ironic performs inspection, provide DHCP
  and PXE or the equivalent provisioning-network prerequisites for the
  configured inspection mode.

### 2.5 Requirements by service

- **VMaaS** (`global.services.vmaas.enabled`) requires OpenShift Virtualization,
  LVM Storage or another dynamic storage class, and MetalLB or another
  `LoadBalancer`-class implementation.
- **CaaS** (`global.services.caas.enabled`) requires multicluster engine
  (standalone, or provided by RHACM if it's installed), a DNS backend, a
  network backend, and `aap.instanceGroups.clusterFulfillment` configuration.
  Hosted control planes provision etcd persistent volume claims from the
  hub's default storage class (see [Section 2.1](#21-cluster-and-access)).
  For more information,
  see
  [`aap-configuration.md`](https://github.com/osac-project/osac/blob/main/osac-installer/docs/aap-configuration.md).
- **BMaaS** (`global.services.bmaas.enabled`) requires BareMetalOperator and a
  `Provisioning` custom resource with `spec.watchAllNamespaces: true` when
  either `bmf.metal3.enabled` or `bmf.netbox.enabled` is `true`.

---

## 3. OSAC component versions

Your platform administrators manage the prerequisite Operator subscriptions.
Channel-tracked versions change as the channels publish updates.
[Table 3.1](#table-31-component-versions) lists each channel and the CSV version
that channel resolved to on OpenShift Container Platform 4.22.6 in September
2026. Treat the observed versions as indicative.

<a id="table-31-component-versions"></a>
**Table 3.1. Component versions**

| Component | Channel or fixed version | Observed on OCP 4.22.6 |
|---|---|---|
| OpenShift Container Platform | `stable-4.22` | 4.22.4 to 4.22.6 |
| `osac` umbrella chart | Selected in [Section 1.1](#11-what-is-released) | Release-specific |
| cert-manager Operator for Red Hat OpenShift | `stable-v1` | `v1.20.0` |
| Red Hat Ansible Automation Platform | `stable-2.6-cluster-scoped` | `v2.6.0` |
| LVM Storage | `stable-<cluster_minor>` | `v4.22.0` |
| MetalLB | `stable` | `v4.22.0` |
| OpenShift Virtualization | `stable` | `v4.22.6` |
| multicluster engine | `stable-2.17` | `v2.17.2` |
| Streams for Apache Kafka | `stable` | `v3.2.1-10` |
| trust-manager | `v0.20.0` (fixed) | `v0.20.0` |
| Envoy (Fulfillment Service sidecar) | `v1.33.0` (fixed) | `v1.33.0` |
| OpenBao (secret store) | `2.6.2` | `2.6.2` |
| Keycloak | Not applicable | `26.6.4` |
| OSAC web console chart | Pinned by the selected umbrella chart | Release-specific |
| PostgreSQL | 18 or later | `18` (Keycloak database) |

The selected `osac` chart pins its subcharts through its `Chart.lock` file.
Consult the release's dependency inventory for their exact versions. You can
also inspect the published chart metadata:

```console
helm show chart oci://ghcr.io/osac-project/charts/osac --version "$OSAC_RELEASE_VERSION"
```

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
   export NS=<namespace>
   export DOMAIN=$(oc get ingresses.config/cluster -o jsonpath='{.spec.domain}')
   export OCP_VERSION=$(oc get clusterversion version -o jsonpath='{.status.desired.version}' | cut -d. -f1,2)
   export AAP_LICENSE_FILE=/path/to/license.zip
   ```

2. Create the target namespace:

   ```console
   oc create namespace "$NS" --dry-run=client -o yaml | oc apply -f -
   ```

3. Create the Secret that holds the AAP subscription manifest:

   ```console
   oc create secret generic config-as-code-manifest-ig --from-file=license.zip="$AAP_LICENSE_FILE" -n "$NS" --dry-run=client -o yaml | oc apply --server-side -f -
   ```

4. Label the Secret so that the AAP bootstrap job reads it:

   ```console
   oc label secret config-as-code-manifest-ig osac.openshift.io/project=osac-aap -n "$NS" --overwrite
   ```

5. For a production deployment, prepare an external PostgreSQL server that
   is reachable from the cluster on port 5432. Configure TLS and
   client-certificate authentication for the issuer selected by
   `service.certs.issuerRef`. The database initialization hook connects as
   `postgres` and needs permission to create the application roles and
   databases. Follow
   [Prepare external PostgreSQL](helm-deployment-guide.md#prepare-external-postgresql).
   The chart and cert-manager create the four `osac-db-*` Secrets during
   installation; do not create them manually.

   > **Note**
   >
   > The install preflight stops the installation if a Secret that
   > `service.database.connection` names (and that the chart does not create
   > itself) is missing or lacks a key, or if `dbInit.host` names an in-cluster
   > Service that does not exist. A Service that exists but has no ready
   > endpoints yet is only a warning, because the `db-init` hook retries.

6. For a production deployment, follow the
   [secrets management configuration guide](secrets-management-configuration.md)
   to prepare the Vault configuration.

### 4.2 Configuring the Helm values

You create one values file for the `osac` chart, `my-values.yaml`.

**Procedure**

1. Retrieve the full set of value keys from the chart:

   ```console
   helm show values oci://ghcr.io/osac-project/charts/osac --version "$OSAC_RELEASE_VERSION" > values-upstream.yaml
   ```

2. Create `my-values.yaml` for the `osac` chart. Base it on the Production
   block of the chart `values-example.yaml` file, which uses an external
   PostgreSQL database, an external Keycloak, and pinned image tags.
   For production, set `dbInit.host` in this file to the reachable hostname
   of the external PostgreSQL server prepared in
   [Section 4.1](#41-preparing-the-cluster):

   ```yaml
   dbInit:
     host: postgres.example.com
   ```

   Replace `postgres.example.com` with your server's hostname. This value
   configures both the initialization hook and the chart-generated database
   connection URLs for the Fulfillment Service and metering.

3. Confirm with your platform administrators that Keycloak uses production
   credentials and that the database and secret store meet the production
   prerequisites in [Section 2.4](#24-credentials-and-external-services).

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
- The issuer configured by `service.certs.issuerRef`, trust-manager, the
  `ConfigMap` named by `service.certs.caBundle.configMap`, Keycloak with the
  `osac` realm, and the credential Secrets exist. The `ConfigMap` is required
  even when the API certificates use public trust. Set
  `service.certs.caBundle.configMap` to the existing ConfigMap name in the
  OSAC release namespace; see step 4 of
  [Section 7](#7-verifying-the-installation).
- For a production deployment, the external PostgreSQL server is ready for
  the chart's client-certificate authentication, and `dbInit.host` is set
  in `my-values.yaml`.
- For a production deployment, the external Vault-compatible is configured
  according to the [secrets management configuration guide](secrets-management-configuration.md).
- You completed [Section 4.1](#41-preparing-the-cluster) and
  [Section 4.2](#42-configuring-the-helm-values).

**Procedure**

- Install the `osac` chart by running the following command:

  ```console
  helm install osac oci://ghcr.io/osac-project/charts/osac --version "$OSAC_RELEASE_VERSION" \
      -n "$NS" --create-namespace \
      -f my-values.yaml \
      --set global.clusterDomain="$DOMAIN" \
      --set service.externalHostname="fulfillment-api-$NS.$DOMAIN" \
      --set service.internalHostname="fulfillment-internal-api-$NS.$DOMAIN" \
      --wait --timeout 40m
  ```

  This command uses the same stable or nightly chart version selected in
  [Section 1.1](#11-what-is-released) and used to retrieve the chart values in
  [Section 4.2](#42-configuring-the-helm-values). Nightly versions are for
  testing unreleased fixes. The AAP bootstrap job takes 10 to 40 minutes.
  Helm does not return until it and, for CaaS, the `osac-publish-templates`
  hook have finished.

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
helm show values oci://ghcr.io/osac-project/charts/osac --version "$OSAC_RELEASE_VERSION"
```

Keys defined in `charts/osac/values.schema.json` are marked `schema`. Keys
marked `subchart` aren't in the umbrella schema; confirm those in the
subchart's own `values.yaml` file.

<a id="table-51-service-enablement-my-valuesyaml"></a>
**Table 5.1. Service enablement (`my-values.yaml`)**

| Parameter | Source | Description | Default |
|---|---|---|---|
| `global.clusterDomain` | schema | Apps domain. All route host names and the default issuer, IdP, and Vault URLs derive from it. Set at installation. | `""` |
| `global.osacDeploymentId` | schema | Required. A stable identity for this deployment, stored in a retained `ConfigMap`. The chart fails to render without it, and fails on upgrade if the value changes — never change it after the first successful install. | Not set |
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
| `operator.fulfillment.credentials.secretName`, `clientIdKey`, `clientSecretKey` | schema | Name and keys of the Kubernetes Secret holding the `osac-controller` Keycloak service-account credentials. Provide this Secret before installation; the OSAC chart only references it. |
| `operator.controllers.tenant`, `operator.controllers.networking`, `operator.controllers.storage` | schema | Enable or disable individual reconcilers. Which services run is driven by `global.services.*`. |
| `operator.controllers.networkingProvisioning` | schema | When `false`, networking custom resources reconcile to `READY` without a real fabric. |
| `operator.controllers.volume` | subchart | Enables the Volume reconciler. |
| `operator.configSecret.name`, `operator.configSecret.optional` | schema | Additional Operator configuration Secret. |
| `operator.hubAccess.enabled` | schema | Creates the hub-access `ClusterRole` that the Fulfillment Service binds to. |
| `operator.stall.preparingInfrastructureThreshold`, `operator.stall.controlPlaneStartingThreshold`, `operator.stall.workersJoiningThreshold` | schema | How long a cluster provisioning phase can wait before the Operator marks it stalled. |
| `operator.tenants[]` | subchart | Tenants pre-created at installation. `shared` is the built-in tenant. |
| `operator.networkManagers.fabricManagers.<name>.enabled`, `operator.networkManagers.k8sManagers.<name>.enabled` | schema | Creates manager registration `ConfigMap` resources. Built-in registrations include `netris`, `cudn_net`, `agentless_net`, and `k8s_only`. Custom registrations such as `cudn_evpn` also need `capabilities: [ipv4]`; `description` is optional. See [CUDN EVPN with Netris](#cudn-evpn-with-netris). |
| `operator.remoteClusterKubeconfig`, `operator.remoteClusterKubeconfigSecretName`, `operator.remoteClusterKubeconfigSecretKey` | schema | Kubeconfig mount for VMaaS on a separate workload cluster. Set the path and Secret name together. The key defaults to `kubeconfig`. See [Section 6.4](#64-installing-vmaas-on-a-separate-workload-cluster). |

**Table 5.3. Networking (umbrella level)**

Normal deployments configure `global.networking`; Helm derives the AAP
backend, manager registrations, and NetworkClass from it. The top-level
`networkClass` block is a legacy expert override and is ignored unless
`global.expertOverrides.networkClass: true`. CUDN EVPN uses that expert path
as shown in [Section 6.1](#cudn-evpn-with-netris).
The Fulfillment API permits one NetworkClass per deployment.

| Parameter | Source | Description | Default |
|---|---|---|---|
| `global.networking.fabricManager`, `global.networking.k8sManager` | schema | Selects the normal networking profile. Fabric-less VMaaS uses `""` and `k8s_only`; Netris uses `netris` and `""`. `global.networking.k8sManager` accepts only `""` or `k8s_only`; use the expert example for CUDN EVPN. | `""` and `k8s_only` |
| `global.networking.networkClass` | schema | Optional overrides merged into the generated NetworkClass, including its title, description, and tenant-onboarding defaults. | `{}`; derived from the selected profile |
| `global.expertOverrides.networkClass` | schema | Makes the top-level `networkClass` block authoritative. | `false` |
| `networkClass.enabled` | schema | Runs NetworkClass creation through the expert path. The normal profile creates its NetworkClass independently of this value. | `false` |
| `networkClass.title`, `networkClass.description` | schema | Expert NetworkClass title and optional description. A nonempty title is required when the expert block is enabled. | Both `""` |
| `networkClass.fabricManager`, `networkClass.k8sManager` | schema | Expert manager names, matching registrations under `operator.networkManagers`. The CUDN EVPN/Netris example selects `netris` and `cudn_evpn`. | Both `""` |
| `networkClass.isDefault` | schema | Legacy validation input for the `cudn_net` profile, which requires `true`. Not sent to the Fulfillment API; it does not mark a default class. | Not set |
| `networkClass.defaults.virtualNetworkIPv4CIDR`, `networkClass.defaults.subnetIPv4CIDR`, `networkClass.defaults.enableNatGateway`, `networkClass.defaults.egressRules` | schema | Expert tenant-onboarding defaults that auto-create the VirtualNetwork, Subnet, and SecurityGroup. | `10.200.0.0/16`, `10.200.0.0/20`, `false`, and allow-all IPv4 egress |

**Table 5.4. Fulfillment Service (`service.*`, all `schema`)**

| Parameter | Description |
|---|---|
| `service.externalHostname`, `service.internalHostname` | Public and internal API route host names. Set at installation. |
| `service.variant` | `openshift` or `kind`. Use `openshift`. |
| `service.images.service`, `service.images.envoy` | Fulfillment Service and Envoy sidecar images. |
| `service.certs.issuerRef.kind`, `service.certs.issuerRef.name` | cert-manager issuer for the service certificates. |
| `service.certs.caBundle.configMap` | Existing CA-bundle `ConfigMap` mounted by the OSAC hooks. It must contain the PEM bundle under the fixed `bundle.pem` key. This value does not create or rename the ConfigMap. Set it to the existing ConfigMap name in the OSAC release namespace. Defaults to `ca-bundle`; see step 4 of [Section 7](#7-verifying-the-installation). |
| `service.auth.issuerUrl` | OIDC issuer. Templated from `global.clusterDomain` by default. |
| `service.auth.controllerCredentials[]` | Volume sources for the controller OIDC client credentials. |
| `service.idp.provider`, `service.idp.url`, `service.idp.credentials[]` | Identity provider for user and role management. Only `keycloak` is supported. |
| `service.database.connection[]` | Volume sources for the database URL and client certificate, for example `osac-db-config` and `osac-db-client-cert`. |
| `service.log.level`, `service.log.headers`, `service.log.bodies` | Log verbosity. Keep `headers` and `bodies` set to `false` in production. |
| `service.vault.endpoint` | Vault-compatible API URL. Set it to your existing Vault or OpenBao URL; the Fulfillment chart requires a nonempty endpoint. See the [secrets management configuration guide](secrets-management-configuration.md). |
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
| `aap.configAsCode.remoteClusterKubeconfigSecretName`, `aap.configAsCode.remoteClusterKubeconfigSecretKey` | Secret name and key that AAP config-as-code uses for VM operations on a separate workload cluster. The key defaults to `kubeconfig`. See [Section 6.4](#64-installing-vmaas-on-a-separate-workload-cluster). |
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
| `metering.database.connection` | schema | Metering service database connection. The metering service is always deployed: billing is not optional, and the event publisher writes usage events unconditionally. Requires Strimzi-managed Kafka, a database connection, and `global.osacDeploymentId` (see [Table 5.1](#table-51-service-enablement-my-valuesyaml) and [Kafka configuration](kafka-configuration.md#metering)). | Secret-based default |
| `metering.heartbeat.interval`, `metering.m360Adapter.enabled`, `metering.m360Adapter.m360.apiUrl`, `metering.m360Adapter.apiKeySecret` | subchart | Heartbeat round interval and the Monetize360 billing adapter. | Not applicable |
| `validation.enabled` | schema | Runs the install preflight. When `false`, every check is skipped and recorded as skipped in the report. | `true` |
| `preflight.mode` | schema | `enforce` stops the installation on a blocking problem. `report` records problems without stopping. `strict` also stops on warnings. See [Section 11.1](#111-the-install-preflight-stops-the-installation). | `enforce` |
| `preflight.skip.<check_id>` | schema | Skips one preflight check. The value is the reason, which the report records. | `{}` |
| `hubAccess.enabled` | schema | Creates hub-access RBAC and registers the local cluster as its own hub. Single-cluster development only. | `false` |
| `dbInit.host` | schema | Host that the `db-init` pre-installation hook connects to, to create the databases. Set it to your external PostgreSQL host for a production deployment. | Development default; override for production |
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

Each workflow gives the `my-values.yaml` values to add for that service.
It assumes the prerequisites for that service (see
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
   workflow; see the [storage documentation](../../storage/README.md).

   ```yaml
   global:
     services: { vmaas: { enabled: true }, caas: { enabled: false }, bmaas: { enabled: false }, maas: { enabled: false } }
     networking:
       fabricManager: ""
       k8sManager: k8s_only
   lvms:
     enabled: true
   operator:
     networkManagers:
       k8sManagers:
         k8s_only:
           enabled: true
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

   For VMaaS that bridges virtual machines onto a Netris EVPN fabric, use
   [CUDN EVPN with Netris](#cudn-evpn-with-netris) below instead of this
   fabric-less networking configuration. That setup requires OpenShift 4.22
   or later.

   For virtual machines on a different cluster from the OSAC management
   services, see
   [Section 6.4](#64-installing-vmaas-on-a-separate-workload-cluster).

2. Add the shared service, database, and secret store settings from
   [Section 4.2](#42-configuring-the-helm-values).

3. Install OSAC. See [Section 4.3](#43-installing-osac).

**Verification**

- Complete [Section 7](#7-verifying-the-installation).
- Create a `ComputeInstance` custom resource and confirm that it reaches
  `RUNNING`:

  ```console
  oc get computeinstance -A
  ```

#### CUDN EVPN with Netris

Your platform administrators must prepare the EVPN host networking, including
the EVPN `FRRConfiguration` resources, and configure the external
EVPN/BGP/VTEP fabric before installation. They must also make the
`cudn_evpn` implementation available in the AAP execution environment. See
[EVPN host networking (VMaaS)](helm-deployment-guide.md#evpn-host-networking-vmaas).

Replace the service-specific block in step 1 with the following
`my-values.yaml` values. Keep the shared settings from
[Section 4.2](#42-configuring-the-helm-values).
Set the Netris URL, user, site, and tenant to your deployment's values:

```yaml
global:
  services: { vmaas: { enabled: true }, caas: { enabled: false }, bmaas: { enabled: false }, maas: { enabled: false } }
  networking:
    fabricManager: netris
    k8sManager: ""
    netris:
      controllerUrl: "https://netris.example.com"
      validateCerts: true
      credentials:
        username: netris
        externalSecret: true
      siteId: "5"
      tenantId: "1"
      tenantName: Admin
  expertOverrides:
    networkClass: true
operator:
  networkManagers:
    enabled: true
    fabricManagers:
      cudn_net:
        enabled: false
      netris:
        enabled: true
    k8sManagers:
      k8s_only:
        enabled: false
      cudn_evpn:
        enabled: true
        description: "OVN EVPN VM-to-fabric bridging"
        capabilities: [ipv4]
networkClass:
  enabled: true
  title: "CUDN EVPN with Netris"
  description: "IPv4 networking through Netris and CUDN EVPN"
  fabricManager: netris
  k8sManager: cudn_evpn
lvms:
  enabled: true
aap:
  instanceGroups:
    publishTemplates:
      enabled: true
    clusterFulfillment:
      enabled: true
    networkFulfillment:
      enabled: true
```

With `externalSecret: true`, create the `netris-credentials` Secret with a
`NETRIS_PASSWORD` key in the AAP namespace before installation. Alternatively,
supply `global.networking.netris.credentials.password` through a separate
gitignored values file as described in [Section 6.2](#62-installing-osac-for-caas-with-the-netris-network-backend).

`global.networking` selects the Netris AAP backend. Its `k8sManager` must stay
empty because the normal profile accepts only `""` or `k8s_only` and cannot
select two managers. `global.expertOverrides.networkClass: true` enables the
separate top-level block, where `enabled: true`, a nonempty `title`, and both
manager names select the EVPN/Netris combination. Setting only
`networkClass.k8sManager` leaves the normal profile in control.

The operator subchart supplies the Netris registration defaults. It has no
built-in `cudn_evpn` registration, so the example supplies `capabilities`
explicitly. An empty capabilities list is rejected; `description` is optional.

### 6.2 Installing OSAC for CaaS with the Netris network backend

The AWS Route 53 DNS backend is the default; it's independent of the network
backend. The schema also accepts `dns.noop.dns`. See
[Section 8.4](#84-installing-without-dns-management). For the full variable
reference, see
[`network-backend.md`](https://github.com/osac-project/osac/blob/main/osac-installer/docs/network-backend.md).

**Prerequisites**

- multicluster engine is installed (standalone, or provided by RHACM).
- MetalLB (or another `LoadBalancer`-class implementation) is installed.
- The hub cluster has a default storage class (see
  [Section 2.1](#21-cluster-and-access)). Hosted control planes provision
  etcd persistent volume claims from it.
- If `DNS_CLASS` is `dns.route53.dns`, you have AWS Route 53 credentials
  for the target hosted zone. They are not required for `dns.noop.dns`.
- Access to the Netris controller: URL, user name, password, site and tenant
  IDs, and management VPC details.
- SSH private keys for the servers and the bastion host.

> **Note**: OSAC-managed storage tiers are not yet available for CaaS tenant
> clusters. The tenant-cluster CSI storage provider is under development.

**Procedure**

1. In `my-values.yaml`, enable the CaaS tier, the OpenShift Container Platform
   release images offered to hosted clusters, and the `cluster-fulfillment`
   and `network-fulfillment` instance groups with the Netris coordinates:

   ```yaml
   global:
     services: { caas: { enabled: true }, vmaas: { enabled: false }, bmaas: { enabled: false }, maas: { enabled: false } }
     networking:
       fabricManager: netris
       k8sManager: ""
       netris:
         controllerUrl: "https://netris.example.com"
         credentials:
           username: "netris"
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

   In `global.networking.netris.resourceClassMap`, each key is a resource-class name;
   `server_cluster_template_id` is the Netris server-cluster template,
   `mgmt_interface` is the management NIC, and `vpc_interfaces` are the
   data-plane NICs.

2. Put the AWS and Netris credentials in a separate values file that is
   excluded from version control:

   ```yaml
   global:
     networking:
       netris:
         credentials:
           password: "<netris_password>"
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

3. Install OSAC (see [Section 4.3](#43-installing-osac)), passing every
   values file, for example `helm ... -f my-values.yaml -f my-secrets.local.yaml
   ...`. For more information, see
   [`aap-configuration.md`](https://github.com/osac-project/osac/blob/main/osac-installer/docs/aap-configuration.md)
   and
   [`dns-backend.md`](https://github.com/osac-project/osac/blob/main/osac-installer/docs/dns-backend.md).

**Verification**

- Complete [Section 7](#7-verifying-the-installation), including step 11.
- Create a `ClusterOrder` custom resource and watch the AAP
  `cluster-fulfillment` and `network-fulfillment` jobs.

### 6.3 Installing OSAC for BMaaS

**Prerequisites**

- BareMetalOperator is installed, with a `Provisioning` custom resource that has
  `spec.watchAllNamespaces: true`. OSAC does not install these; the install
  preflight stops the installation if they are missing.

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
  oc get provisioning
  oc get baremetalhosts -A
  ```

- Create a `BareMetalPool` or `BareMetalInstance` custom resource and confirm
  that the Bare Metal Fulfillment Operator reconciles it.

### 6.4 Installing VMaaS on a separate workload cluster

Use this layout when OSAC management services run on one OpenShift cluster
and virtual machines run on another. Disable CaaS and BMaaS: the operator
refuses to start when either the cluster controller or the bare-metal-instance
controller is enabled with a remote kubeconfig.

Your platform administrators must prepare the management cluster with the
OSAC prerequisites and the workload cluster with OpenShift Virtualization,
dynamic storage, and a `LoadBalancer`-class implementation as described in
[Section 2](#2-prerequisites).
Create the workload kubeconfig Secret on the management cluster, including
the console-proxy label, as described in
[VMaaS dedicated cluster](https://github.com/osac-project/osac/blob/main/osac-operator/docs/vmaas-dedicated-cluster/README.md).

Start with the VMaaS values from [Section 6.1](#61-installing-osac-for-vmaas),
including the storage registration and networking settings. For the LVMS
workload cluster, keep `lvms.enabled: true` in `my-values.yaml` on the
management cluster. This registers the `local` StorageBackend and StorageTier
in fulfillment; installing LVM Storage alone does not register them.

Merge these values into `my-values.yaml` to enable VMaaS only and point the
operator and AAP at the Secret:

```yaml
global:
  services: { vmaas: { enabled: true }, caas: { enabled: false }, bmaas: { enabled: false }, maas: { enabled: false } }
lvms:
  enabled: true
operator:
  remoteClusterKubeconfig: /var/run/secrets/remote/kubeconfig
  remoteClusterKubeconfigSecretName: osac-remote-kubeconfig
  remoteClusterKubeconfigSecretKey: kubeconfig
aap:
  configAsCode:
    remoteClusterKubeconfigSecretName: osac-remote-kubeconfig
    remoteClusterKubeconfigSecretKey: kubeconfig
```

`remoteClusterKubeconfig` is the path inside the operator container. The
Secret name and key must match the Secret on the management cluster. The
chart default key is `kubeconfig`.

Install OSAC on the management cluster. See
[Section 4.3](#43-installing-osac). Complete
[Section 7](#7-verifying-the-installation) there, and confirm that the
virtual machine is created on the workload cluster.

---

## 7. Verifying the installation

Perform the following steps in order. Each step assumes that the previous steps
passed. If a step fails, see [Section 11](#11-troubleshooting).

**Procedure**

1. Check the Helm releases. The `osac` release must show `STATUS: deployed`. A
   status of `pending-install` or `pending-upgrade` means that a hook is still
   running or has failed.

   ```console
   helm list -A | grep osac
   ```

2. Check the install preflight report. The installation writes it before it
   creates anything. No result may have the status `fail`, and
   `summary.blocking` must be `0`:

   ```console
   $ oc get configmap osac-preflight-report -n <namespace> \
       -o jsonpath='{.data.render\.json}' | jq '.summary'
   ```

   A result with the status `warn` did not stop the installation; its message
   says what to review. See
   [Section 11.1](#111-the-install-preflight-stops-the-installation).

3. Check the prerequisite Operators required by your enabled services in
   [Table 2.1](#table-21-platform-operators-and-components). Each expected CSV
   must be `Succeeded`:

   ```console
   oc get csv -A \
     -o custom-columns='NAMESPACE:.metadata.namespace,NAME:.metadata.name,PHASE:.status.phase' \
     | awk 'NR == 1 || $2 ~ /^(aap-operator|cert-manager-operator|kubevirt-hyperconverged-operator|lvms-operator|metallb-operator|multicluster-engine)\./'
   ```

   The AAP CSV is named `aap-operator` and can appear in multiple namespaces.
   Check the OpenShift Virtualization CSV for VMaaS and the LVM Storage and
   MetalLB CSVs if those providers satisfy your storage and load-balancer
   prerequisites. For CaaS, check standalone MCE or the health of the MCE
   installation provided by RHACM.

   Also check the CSV installed by your Kafka Operator's Subscription.
   Replace the placeholders with the namespace and Subscription name supplied
   by your Kafka administrators. The CSV must be `Succeeded`:

   ```console
   KAFKA_CSV=$(oc get subscription <kafka_subscription> -n <kafka_namespace> -o jsonpath='{.status.installedCSV}')
   oc get csv "$KAFKA_CSV" -n <kafka_namespace>
   ```

4. Verify the configured certificate issuer and CA bundle.

   Check the issuer selected by `service.certs.issuerRef`. For
   `kind: ClusterIssuer`, run `oc get clusterissuer <issuer_name>`; for
   `kind: Issuer`, run `oc get issuer <issuer_name> -n <namespace>`. Use the
   configured `service.certs.issuerRef.name` and the OSAC release namespace.
   The issuer must report `Ready: True`.

   Verify the trust bundle separately from the certificate issuer. For every
   deployment, including deployments with publicly trusted API certificates,
   the CA-bundle `ConfigMap` must already exist in the OSAC release namespace.
   Set `service.certs.caBundle.configMap` in `my-values.yaml` to its name;
   the default is `ca-bundle`. This value only references the ConfigMap and
   does not create or rename it.

   Replace `<ca_bundle_configmap>` below with the configured name and
   `<namespace>` with the OSAC release namespace:

   ```console
   oc get configmap <ca_bundle_configmap> -n <namespace> -o yaml
   ```

   The ConfigMap must contain the CA bundle under the fixed `bundle.pem` key
   required by the OSAC hooks. Extract it once for the client checks below:

   ```console
   oc get configmap <ca_bundle_configmap> -n <namespace> \
     -o jsonpath='{.data.bundle\.pem}' > osac-ca-bundle.pem
   test -s osac-ca-bundle.pem
   ```

   Keep this file for the OIDC, API, and hub-registration checks. It must
   contain the CA that signs the endpoint's certificate. If an endpoint
   uses a different CA, obtain the matching PEM bundle from its
   administrator and use that file for that endpoint.

5. Check the prerequisite services with your platform administrators:

   - PostgreSQL must be reachable at `dbInit.host` and accept the configured
     client-certificate authentication.
   - Vault or OpenBao must be reachable at `service.vault.endpoint` and ready
     for tenant namespace creation. See the
     [secrets management configuration guide](secrets-management-configuration.md).
   - Kafka must be ready at the configured broker addresses with the required
     credentials, TLS trust, and permissions. Use your Kafka provider's health
     checks and the [Kafka configuration guide](kafka-configuration.md).
   - OpenShift Virtualization and the configured storage and load-balancer
     providers must be ready on the workload cluster for VMaaS.

6. Confirm that the configured OIDC issuer responds. Set
   `OSAC_OIDC_ISSUER_URL` to the fully resolved `service.auth.issuerUrl`,
   including the realm path. Use the issuer URL supplied by your Keycloak
   administrator.

   If the issuer certificate is signed by a CA in the trust bundle extracted
   in step 4 of [this section](#7-verifying-the-installation), use that file:

   ```console
   export OSAC_OIDC_CA_FILE="osac-ca-bundle.pem"
   ```

   If the issuer uses a different CA, set `OSAC_OIDC_CA_FILE` to the
   matching PEM bundle supplied by its administrator. For an issuer with
   a custom CA, run:

   ```console
   export OSAC_OIDC_ISSUER_URL="<oidc_issuer_url>"
   curl --fail-with-body --silent --show-error \
     --cacert "$OSAC_OIDC_CA_FILE" \
     "${OSAC_OIDC_ISSUER_URL%/}/.well-known/openid-configuration"
   ```

   For an issuer with a publicly trusted certificate, use the system
   trust store instead:

   ```console
   export OSAC_OIDC_ISSUER_URL="<oidc_issuer_url>"
   curl --fail-with-body --silent --show-error \
     "${OSAC_OIDC_ISSUER_URL%/}/.well-known/openid-configuration"
   ```

7. Check the OSAC pods. All pods must be `Running` or `Completed`, with no
   pods in `CrashLoopBackOff` or `ImagePullBackOff`:

   ```console
   oc get pods -n <namespace>
   ```

   Depending on the enabled services, expect the following pods:

   - Always: `fulfillment-grpc-server`, `fulfillment-rest-gateway`,
     `fulfillment-controller`, `fulfillment-ingress-proxy`, `osac-operator`,
     `osac-operator-console-proxy`, and the `osac-aap-*` pods.
   - With `ui.enabled`: `osac-ui`.
   - Always: the `osac-metering` pods.
   - VMaaS: `fulfillment-console-proxy`.
   - BMaaS: the Bare Metal Fulfillment Operator pod.

8. Check that every `Certificate` is `Ready`:

   ```console
   oc get certificate -n <namespace>
   ```

   The `osac-db-init` hook runs before installation completes. Helm deletes
   its Job after success, so it is expected to be absent from `oc get jobs -A`
   after a deployed install. If the install fails during database
   initialization, see [Section 11.5](#115-the-osac-db-init-hook-fails) for
   the retained Job's logs.

9. Check the AAP bootstrap job. The `osac-aap-bootstrap` job runs as a
   postinstallation hook and takes 10 to 40 minutes:

   ```console
   oc logs -f job/osac-aap-bootstrap -n <namespace>
   ```

10. Check API reachability and log in with the `osac` CLI. For a custom CA,
    use the API trust bundle prepared in step 4 of
    [this section](#7-verifying-the-installation) for both commands:

    ```console
    ROUTE=$(oc get route fulfillment-api -n <namespace> -o jsonpath='{.spec.host}')
    curl --fail-with-body --cacert osac-ca-bundle.pem "https://$ROUTE/healthz"
    oc get serviceaccount admin -n <namespace>
    osac login \
      --address "$ROUTE" \
      --token-script "oc create token admin -n <namespace> --duration 1h" \
      --ca-file osac-ca-bundle.pem
    osac get tenants
    ```

    For a publicly trusted API certificate, use the system trust store:

    ```console
    ROUTE=$(oc get route fulfillment-api -n <namespace> -o jsonpath='{.spec.host}')
    curl --fail-with-body "https://$ROUTE/healthz"
    oc get serviceaccount admin -n <namespace>
    osac login \
      --address "$ROUTE" \
      --token-script "oc create token admin -n <namespace> --duration 1h"
    osac get tenants
    ```

    An error that `serviceaccounts "fulfillment-controller" not found` means
    the token command used the deployment name. The chart creates the `admin`
    service account in the release namespace and includes it in the default
    `auth.emergencyServiceAccounts`. This account grants full platform
    administrator permissions; use it for initial installation verification
    or emergency recovery. For routine access, configure identity
    authentication as described in the
    [Tenant Identity and Access Guide](../tenant-identity-and-access-guide.md).
    If `admin` is missing, check that the Fulfillment Service chart installed
    successfully and that `auth.emergencyServiceAccounts` includes `admin`.

    Use `--insecure` (skips certificate verification) only for evaluation,
    never in production.

11. CaaS only: check that the cluster templates were published:

    ```console
    osac get clustertemplates
    ```

    The `osac get clustertemplates` output must be non-empty. Helm deletes
    the `osac-publish-templates` hook Job after success. Only if template
    publication fails, read the retained Job's init-container logs:

    ```console
    oc logs job/osac-publish-templates -n <namespace> -c wait-for-fulfillment
    ```

    If `wait-for-fulfillment` succeeded and `publish-templates` started,
    read the publishing container's logs:

    ```console
    oc logs job/osac-publish-templates -n <namespace> -c publish-templates
    ```

12. Check the web console. Open the console URL in a browser. The URL must
    redirect to Keycloak and, after you log in, show the OSAC console. Skip
    this step if `ui.enabled` is `false`.

    ```console
    UI=$(oc get route osac-ui -n <namespace> -o jsonpath='{.spec.host}')
    curl -skI "https://$UI"
    ```

    The response must be `200` or a `302` redirect to Keycloak.

13. Run a service smoke test, as described in the Verification section of the
    workflow in [Section 6](#6-installation-workflows-by-service) for your
    service.

---

## 8. Postinstallation tasks

### 8.1 Accessing the OSAC consoles

The OSAC web console and Fulfillment API routes are in the install
namespace. Host names are auto-assigned as
`<route>-<namespace>.<cluster_domain>` unless you set `ui.externalHostname`
or `service.externalHostname`. Obtain the Keycloak console URL from your
identity provider administrator.

```console
oc get route -n <namespace>
```

- **OSAC web console.** Deployed whenever `ui.enabled` is `true`, which is the
  default. The route uses edge TLS termination and redirects HTTP to HTTPS. Log
  in through Keycloak SSO against the `osac` realm, using a realm user or
  a federated identity provider configured by your Keycloak administrator.
- **Fulfillment API.** Authenticate with an OIDC token or with `osac login`, as
  described in step 10 of [Section 7](#7-verifying-the-installation).
- **AAP managed by the chart** (`aap.aap.instance.enabled: true`). Its route
  is in the install namespace. Log in as `admin`. For the default instance
  name, retrieve the password with the following command:

  ```console
  oc extract secret/osac-aap-admin-password -n <namespace> --to -
  ```

  If you change `aap.aap.instance.name`, replace `osac-aap` in the Secret name
  with your instance name.
- **Externally managed AAP** (`aap.aap.instance.enabled: false`). Obtain the
  AAP URL and credentials from your AAP administrator.
- **Keycloak admin console.** Use the URL and administrator credentials
  supplied by your identity provider administrator.

### 8.2 Installing the `osac` CLI

Use the CLI version matching the `fulfillment-service` subchart pinned by
your selected `osac` chart. Find its version in the dependency inventory
or chart metadata shown in [Section 3](#3-osac-component-versions), then set
`FULFILLMENT_SERVICE_VERSION` to that value below. Component versions are
independent of `OSAC_RELEASE_VERSION`:

```console
export FULFILLMENT_SERVICE_VERSION="<fulfillment_service_version>"
```

**Stable component version**

For a stable `fulfillment-service` version, download its published CLI
binary from the monorepo release. The following example is for Linux
x86_64:

```console
curl --fail --location --show-error -o osac "https://github.com/osac-project/osac/releases/download/fulfillment-service/v${FULFILLMENT_SERVICE_VERSION}/osac_Linux_x86_64" &&
  chmod +x osac &&
  sudo mv osac /usr/local/bin/
```

**Nightly component version**

Nightly builds publish charts and images, but no CLI binary release assets.
If `FULFILLMENT_SERVICE_VERSION` contains `-nightly.`, build the CLI from
the exact umbrella source tag selected by `OSAC_RELEASE_VERSION`. This
uses the Fulfillment Service's documented
[CLI build procedure](../../../fulfillment-service/README.md#building-the-binaries).
Install Git and a Go toolchain satisfying the selected checkout's
`go.work` and `fulfillment-service/go.mod` requirements. The build needs
network access to download Go module dependencies. Use a new
`osac-cli-source` directory:

```console
git clone --depth 1 --branch "osac/v${OSAC_RELEASE_VERSION}" \
  https://github.com/osac-project/osac.git osac-cli-source &&
(
  cd osac-cli-source/fulfillment-service &&
  CGO_ENABLED=0 go build \
    -ldflags "-X github.com/osac-project/osac/fulfillment-service/internal/version.id=${FULFILLMENT_SERVICE_VERSION}" \
    -o osac ./cmd/osac &&
  sudo install -m 0755 osac /usr/local/bin/osac
)
```

Use the installed CLI for the verification commands in
[Section 7](#7-verifying-the-installation) and hub registration below.

### 8.3 Registering the hub

The hub is the OpenShift Container Platform cluster that the OSAC Operator
and AAP run on and that provisions resources. This procedure applies when the
Fulfillment Service and the hub run on the same cluster.

**Prerequisites**

Set top-level `hubAccess.enabled: true` in `my-values.yaml` when installing
or upgrading the `osac` chart. It defaults to `false`. This creates the
`hub-access` ServiceAccount, RBAC, and token Secret required by
`create-hub-access-kubeconfig.sh`:

```yaml
hubAccess:
  enabled: true
```

This setting also runs the `fulfillment-create-hub` hook to register the
local hub automatically as `hub`. Use the manual steps below only if the
local hub is not already registered.

**Procedure**

1. Log in to the Fulfillment Service through `fulfillment-internal-api`.
   Hub registration uses the private API, so run this procedure from an
   administrator host that can reach the internal route. For a custom CA,
   use the API trust bundle prepared in step 4 of
   [Section 7](#7-verifying-the-installation):

   ```console
   osac login \
       --address "$(oc get route fulfillment-internal-api -n <namespace> -o jsonpath='{.spec.host}')" \
       --token-script "oc create token admin -n <namespace> --duration 1h" \
       --private \
       --ca-file osac-ca-bundle.pem
   ```

   For a publicly trusted API certificate, use the system trust store:

   ```console
   osac login \
       --address "$(oc get route fulfillment-internal-api -n <namespace> -o jsonpath='{.spec.host}')" \
       --token-script "oc create token admin -n <namespace> --duration 1h" \
       --private
   ```

   Check for an existing registration:

   ```console
   osac get hubs
   ```

   If the local hub is already registered, skip the remaining manual steps.

2. Select the OSAC namespace and generate the hub-access kubeconfig file:

   ```console
   oc project <namespace>
   oc get secret hub-access -n <namespace>
   umask 077
   curl --fail --silent --show-error --location --remote-name "https://raw.githubusercontent.com/osac-project/osac/refs/tags/osac/v${OSAC_RELEASE_VERSION}/osac-installer/scripts/create-hub-access-kubeconfig.sh" &&
     chmod +x create-hub-access-kubeconfig.sh &&
     ./create-hub-access-kubeconfig.sh
   HUB_SERVER_URL=$(oc config view --minify -o jsonpath='{.clusters[*].cluster.server}')
   HUB_SERVER_NAME=${HUB_SERVER_URL#*.}
   HUB_SERVER_NAME=${HUB_SERVER_NAME%%.*}
   export HUB_KUBECONFIG="/tmp/kubeconfig.hub-access.${HUB_SERVER_NAME}"
   ```

   The script writes `/tmp/kubeconfig.hub-access.<server_name>`. The
   commands above derive the same server name from the current context and
   store the generated path in `HUB_KUBECONFIG`.

3. Register the hub using the generated file:

   ```console
   osac create hub --kubeconfig="$HUB_KUBECONFIG" --name <hub_name> --namespace <namespace>
   ```

Use `--insecure` only for evaluation, never in production. Add
`--as system:admin` only when your `oc` context cannot mint the token.

Multi-hub environments are currently unsupported, and support may be added in future releases.

### 8.4 Installing without DNS management

The chart schema accepts `dns.route53.dns` and `dns.noop.dns`. Set
`dns.noop.dns` in `my-values.yaml` when OSAC should not create or delete DNS
records. Route 53 credentials are not required for that class.

**Procedure**

1. In `my-values.yaml`, before the first `ClusterOrder`, set the DNS class
   and the guest domain values on the `cluster-fulfillment` instance group:

   ```yaml
   aap:
     instanceGroups:
       clusterFulfillment:
         enabled: true
         config:
           DNS_CLASS: dns.noop.dns
           EXTERNAL_ACCESS_BASE_DOMAIN: <guest_domain>
           HOSTED_CLUSTER_BASE_DOMAIN: <guest_domain>
           EXTERNAL_ACCESS_SUPPORTED_BASE_DOMAINS: <supported_domain>
   ```

2. Install or upgrade OSAC with that values file. See
   [Section 4.3](#43-installing-osac). Helm renders these keys into the
   `cluster-fulfillment-ig` `ConfigMap`.

3. Confirm the values:

   ```console
   oc get cm cluster-fulfillment-ig -n "$NS" -o yaml \
       | grep -E 'DNS_CLASS|EXTERNAL_ACCESS|HOSTED_CLUSTER'
   ```

4. Create these DNS records yourself. `dns.noop.dns` stops OSAC from managing
   DNS. It does not create records for you:

   ```text
   api.<cluster>.<guest_domain>       -> API endpoint
   api-int.<cluster>.<guest_domain>   -> API endpoint
   *.apps.<cluster>.<guest_domain>    -> ingress endpoint
   ```

   The records must resolve from the hub, the AAP execution environment, the
   managed host, and the hosted cluster.

On a later upgrade, keep `DNS_CLASS: dns.noop.dns` in the values file. Helm
reconciles the `ConfigMap` from those values.

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
- The published `osac` chart at a stable tagged release. The chart
  `values-example.yaml` file documents the Production block: an external
  PostgreSQL database, an external Keycloak, and pinned image tags.
- Prerequisite Operators and external services prepared by your platform
  administrators before installing OSAC.
- An external PostgreSQL 18 or later server configured for the chart's
  client-certificate authentication and selected through `dbInit.host`. See
  [Section 2.4](#24-credentials-and-external-services).
- An external Vault-compatible store configured with a parent namespace,
  Keycloak JWT role, and `service.vault` values. See the
  [secrets management configuration guide](secrets-management-configuration.md).
- An external Keycloak configured with the `osac` realm, clients, and roles
  through `service.auth` and `service.idp`.
- A single hub cluster.

### 9.2 Evaluation only

- Bundled PostgreSQL and OpenBao instances supplied for testing or CI.
- Keycloak test fixtures and default administrator credentials.
- Development or CI reference values files and `main`/`latest` image tags.

### 9.3 Not covered by this guide

- Installing and operating the prerequisite Operators and external services.
  Coordinate their setup with your platform administrators.
- Detailed configuration of an external Keycloak. The chart accepts an external
  Keycloak through `service.auth` and `service.idp`, but realm and client
  provisioning is out of scope. See
  [`fulfillment-service/docs/INSTALL.md`](https://github.com/osac-project/osac/blob/main/fulfillment-service/docs/INSTALL.md).
- Multi-hub topologies (TBD — see the note in
  [Section 8.3](#83-registering-the-hub)).

---

## 10. Uninstalling OSAC

**Procedure**

1. Uninstall the `osac` release:

   ```console
   helm uninstall osac -n "$NS"
   ```

2. The CRDs are retained because they carry the
   `helm.sh/resource-policy: keep` annotation. To remove them, run the
   following command:

   ```console
   oc delete crd -l app.kubernetes.io/part-of=osac
   ```

3. The install preflight report is a Helm hook resource, so Helm does not
   remove it with the release. To remove it, run the following command:

   ```console
   $ oc delete configmap -n "$NS" -l osac.openshift.io/preflight-report=true
   ```

Prerequisite Operators and external services have their own lifecycle.
Coordinate their removal with your platform administrators. Do not remove
shared prerequisites on a cluster used by other deployments.

---

## 11. Troubleshooting

Most successful hook Jobs are deleted by Helm. Failed Jobs are normally
retained for inspection. If installation or upgrade fails during an OSAC
hook, list the retained Jobs in the `osac` release namespace:

```console
oc get jobs -n <namespace> | grep -E 'db-init|publish-templates|bootstrap|create-hub'
```

With Helm 3.18 or later, or Helm 4, Helm prints the log of a failed hook job in
its own output, so the cause is usually already on your screen.

### 11.1 The install preflight stops the installation

`helm install` and `helm upgrade` check the cluster before they create
anything. If a prerequisite is missing, the command fails within seconds,
creates nothing, and lists every problem once, with its fix:

```console
Error: INSTALLATION FAILED: execution error at (osac/templates/preflight/report.yaml:17:4): OSAC preflight: 2 blocking problem(s), 0 warning(s). Nothing was changed.

  FAIL    default-storageclass: no default StorageClass is set (1 StorageClass(es) exist)
          Fix: Mark a StorageClass as default: oc patch storageclass <name> ...
  FAIL    cert-manager-crds: CustomResourceDefinition certificates.cert-manager.io not found
          Fix: Install the cert-manager Operator for Red Hat OpenShift ...

To skip a check: --set preflight.skip.<id>="<reason>"
To only report, never block: --set preflight.mode=report
OSAC-PREFLIGHT-JSON v1: {"apiVersion":"preflight.osac.openshift.io/v1alpha1", ...}
```

Fix every listed problem and run the same command again. To confirm that
nothing was installed, run `helm status osac -n <namespace>`: it reports
`release: not found`. A check that depends on a failed check is reported as
`unknown` and is not counted again.

**Requirements.** The preflight runs while Helm renders the chart, with the
credentials of the user who runs Helm. A render without a cluster, such as
`helm template` or a GitOps tool that renders manifests, cannot look at the
cluster: every cluster check is reported as `unknown` and nothing is blocked.
Create the namespace before you install ([Section 4.1](#41-preparing-the-cluster)):
the preflight runs before Helm creates a namespace, so with only
`--create-namespace` it stops at `install-namespace-active`.

**After upgrading from a release without the preflight.** The earlier
validation Job, `osac-pre-install-validate`, no longer exists. Helm deletes it
after a successful run, but a failed one stays. If an earlier attempt left it
behind, delete it, and the `osac-validate` ServiceAccount and the
`osac-validate-<namespace>` ClusterRole and ClusterRoleBinding that it used:

```console
oc delete job osac-pre-install-validate -n <namespace> --ignore-not-found
oc delete serviceaccount osac-validate -n <namespace> --ignore-not-found
oc delete clusterrolebinding,clusterrole osac-validate-<namespace> --ignore-not-found
```

**Checks.** A check that does not apply to your configuration is recorded as
`skipped` with the reason.

| Check | What it verifies | Applies when |
|---|---|---|
| `install-namespace-active` | The release namespace exists and is not terminating. | Always |
| `ocp-version` | The OpenShift version is 4.22 or later (warning only). | OpenShift |
| `nodes-schedulable` | At least one node is Ready, not cordoned, and has no `NoSchedule` or `NoExecute` taint. The chart sets no tolerations, so a tainted node cannot run its pods. | Always; a warning on upgrade |
| `default-storageclass` | A default storage class exists. | Always; required on a new installation with the AAP instance or CaaS, otherwise a warning |
| `cert-manager-crds` | The `certificates.cert-manager.io` CRD exists. | Always |
| `cert-issuer-ready` | The issuer in `service.certs.issuerRef` exists and is `Ready`. | Always; a warning on upgrade |
| `ca-bundle-present` | The `service.certs.caBundle.configMap` ConfigMap exists in the namespace. | Always |
| `operator-ingress-ca` | The OpenShift-managed ConfigMap `default-ingress-cert` in `openshift-config-managed` exists and has the key `ca-bundle.crt`. The operator reads it at startup and exits without it. | OpenShift, with `operator.enabled` and `operator.ignition.trustIngressCA=true` |
| `db-secrets` | Secrets named in `service.database.connection` that the chart does not create exist, have their keys, and carry a valid PostgreSQL URL. | Always |
| `db-init-host-endpoints` | When `dbInit.host` is an in-cluster Service, it exists and has a ready endpoint. A Service that is only starting is a warning, because the `db-init` hook retries for up to 5 minutes. | In-cluster host |
| `kafka-access` | The `Kafka` resource is `Ready`, or the Secrets in `service.kafka.connection` exist when `kafka.enabled=false`. | Always; a warning on upgrade |
| `controller-credentials` | The Fulfillment controller credential Secrets exist with their keys. | `operator.enabled` |
| `vault-config` | The Secrets and CA bundle named in `service.vault` exist. | `service.vault.endpoint` is set |
| `aap-operator-ready` | The `AnsibleAutomationPlatform` CRD is established. | OpenShift with the AAP instance; a warning on upgrade |
| `aap-license-secret` | The `config-as-code-manifest-ig` Secret exists with `license.zip` and the project label. | OpenShift with the AAP bootstrap |
| `metal3-bmh-crd`, `metal3-watch-all-namespaces` | The `BareMetalHost` CRD exists and the `Provisioning` resource has `watchAllNamespaces: true`. To fix the second: `oc patch provisioning provisioning-configuration --type merge -p '{"spec":{"watchAllNamespaces": true}}'` | BMaaS with `bmf.metal3.enabled` or `bmf.netbox.enabled` |
| `kubevirt-crds` | OpenShift Virtualization is installed. | VMaaS |
| `mce-or-acm-crds` | The multicluster engine or RHACM is installed. | CaaS |

**Skipping a check.** Set `preflight.skip.<check_id>` to the reason, for
example `--set preflight.skip.cert-issuer-ready="created by GitOps after this
install"`. The report records the reason. A check id that does not exist is
reported as a warning. `validation.enabled=false` skips every check.

**Access needed.** The preflight reads the cluster with the credentials of
whoever runs `helm install`: the `kube-system` Namespace (to tell that the
cluster answers), Nodes, StorageClasses, CustomResourceDefinitions, the
Operator and custom resources named in the table, and the Secrets, ConfigMaps
and Services the checks name. If a read is forbidden, Helm stops the render
with an error such as `namespaces "kube-system" is forbidden: ... cannot get
resource "namespaces"`, and nothing is installed. Grant the read access, or set
`validation.enabled=false`, which makes no cluster read at all.

**Modes.** `preflight.mode=enforce` (the default) stops the installation on a
blocking problem. `strict` also stops on warnings. `report` records problems
without stopping the installation.

**Reading the result in a script or another tool.** The result is JSON
(schema `preflight.osac.openshift.io/v1alpha1`):

- After a failed run, the `OSAC-PREFLIGHT-JSON v1:` line in the error output.
  It always starts at the beginning of a line. The same command gives it on
  Helm 3 and Helm 4:

  ```console
  $ helm install osac ... 2>&1 >/dev/null | sed -n 's/^OSAC-PREFLIGHT-JSON v1: //p' | jq '.results[] | select(.blocking)'
  ```

- After a run that passed, the `osac-preflight-report` ConfigMap, key
  `render.json`, holds the last report that passed. Compare `release.revision`
  with the deployed revision to know whether it is current.
- Without installing, on Helm 3, run
  `helm template osac ... --dry-run=server --set preflight.mode=report -s templates/preflight/report.yaml`.
  It prints the report as a ConfigMap and exits `0` even when problems exist
  and the cluster lacks the chart's CRDs. Helm 4 maps every rendered kind to the cluster before it
  prints anything, so on Helm 4 this fails with `resource mapping not found`
  on a cluster that lacks a CRD the chart renders; use the error line of a
  normal run, or `helm install ... --dry-run=server -o json` when the CRDs
  exist (the report is the `osac-preflight-report` entry of `hooks`).

### 11.2 A Helm release is stuck in `pending-install` or `pending-upgrade`

A hook is still running or has failed. To find it, run the following command:

```console
oc get jobs,pods -n <namespace> | grep -Ev 'Complete|Running'
```

If a previous attempt was interrupted, clear the stuck release before you
retry:

```console
helm uninstall osac -n <namespace> --no-hooks
```

### 11.3 An Operator CSV never reaches `Succeeded`

```console
oc get subscription,installplan,csv -n <operator_namespace>
oc get pods -n openshift-marketplace
oc get packagemanifest | wc -l
```

`ImagePullBackOff` on the marketplace catalog pods, or an empty
`packagemanifest` list, indicates that the cluster pull secret cannot
authenticate to `registry.redhat.io`. Refresh `openshift-config/pull-secret`
(see [Section 2.1](#21-cluster-and-access)) and delete the failed marketplace
pods. If your Kafka Operator's `Subscription` uses a manual install plan,
approve its `InstallPlan` in the Operator's namespace.

### 11.4 An OCI pull fails

A `not found` error on `oci://ghcr.io/osac-project/charts/osac` usually
indicates an incorrect `--version` value. Confirm that `OSAC_RELEASE_VERSION`
matches a published chart version from [Section 1.1](#11-what-is-released),
then verify that Helm can retrieve its metadata:

```console
helm show chart oci://ghcr.io/osac-project/charts/osac --version "$OSAC_RELEASE_VERSION"
```

### 11.5 The `osac-db-init` hook fails

If the install or upgrade fails during this hook, inspect the retained Job's
init container that waits for the certificate:

```console
oc logs job/osac-db-init -n <namespace> -c wait-for-cert
```

If `wait-for-cert` succeeded and the `psql` container started, read its logs:

```console
oc logs job/osac-db-init -n <namespace> -c psql
```

- `db-init certificate not ready`: check the Certificate named
  `osac-db-init`, the `osac-db-init-cert` Secret, and the issuer selected by
  `service.certs.issuerRef`. See [Section 11.6](#116-certificates-never-become-ready).
- Hostname resolution, connection refusal, or timeout errors: verify
  `dbInit.host` in `my-values.yaml` and connectivity to PostgreSQL on port
  5432. Set the host as described in
  [Section 4.2](#42-configuring-the-helm-values).
- TLS or certificate-authentication errors: check the server's certificate
  and trust of the configured issuer. PostgreSQL must accept the hook's
  `postgres` client identity and allow it to create roles and databases.
  See [Prepare external PostgreSQL](helm-deployment-guide.md#prepare-external-postgresql).
- `Secret <name> not found`, `is missing key(s)`, or `is not a valid PostgreSQL
  URL` from the `db-secrets` preflight check: create the Secret that
  `service.database.connection` names, with the keys it lists (see
  [Section 4.1](#41-preparing-the-cluster)).
- `Service <name> in namespace <namespace> not found` from the
  `db-init-host-endpoints` preflight check: `dbInit.host` names an in-cluster
  Service that does not exist. Create it or point `dbInit.host` at a reachable
  server.
- `Service <name> in namespace <namespace> has no ready endpoints yet` from the
  same check is a warning and does not stop the installation. PostgreSQL is
  still starting, and the `db-init` hook retries for up to 5 minutes. If the
  hook still fails, see the steps in this section.

### 11.6 Certificates never become `Ready`

```console
oc get certificate,certificaterequest -n <namespace>
oc logs -n cert-manager deploy/cert-manager
```

Always check the `Issuer` or `ClusterIssuer` selected by
`service.certs.issuerRef` as described in step 4 of
[Section 7](#7-verifying-the-installation). It must exist and report
`Ready: True`.

### 11.7 The AAP instance does not start

The `osac-aap-*` pods are stuck, or the `AnsibleAutomationPlatform` custom
resource is not progressing:

```console
oc get aap,pods -n <namespace>
oc get apiservice v1beta1.metrics.k8s.io
oc get csr | grep -c Pending
```

- `Unable to determine if virtual resource` in the AAP custom resource: a
  broken `APIService`, usually `v1beta1.metrics.k8s.io` when metrics-server is
  not ready, makes API discovery fail for the Ansible-based Operator. Fix
  metrics-server, or delete the unavailable `APIService`.
- `tls: internal error` on `oc logs`, `oc debug`, or in the AAP status: pending
  `kubernetes.io/kubelet-serving` CSRs. To approve them, run the following
  command:

  ```console
  oc get csr -o name | xargs oc adm certificate approve
  ```

- `label validation error: key "app.kubernetes.io/managed-by" must equal "Helm"`:
  the AAP Operator rewrote the `managed-by` label of the custom resource after
  an interrupted installation. Restore the label and the
  `meta.helm.sh/release-name` and `meta.helm.sh/release-namespace` annotations,
  then reinstall:

  ```console
  oc label aap osac-aap -n <namespace> app.kubernetes.io/managed-by=Helm --overwrite
  ```

- a field-ownership conflict on `app.kubernetes.io/managed-by` owned by the
  AAP operator (Helm 4): add `--force-conflicts` to the `helm upgrade`
  command. The operator takes ownership of that field on the `osac-aap`
  custom resource after install.

### 11.8 The `osac-aap-bootstrap` job fails

```console
oc logs -f job/osac-aap-bootstrap -n <namespace>
oc get secret config-as-code-manifest-ig -n <namespace>
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
oc logs job/osac-publish-templates -n <namespace> -c wait-for-fulfillment
oc logs job/osac-publish-templates -n <namespace> -c publish-templates
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
oc logs deploy/fulfillment-grpc-server -n <namespace>
oc logs deploy/fulfillment-rest-gateway -n <namespace>
oc logs deploy/fulfillment-controller -n <namespace>
```

- `issuer URL '...' is not trusted`: the `--auth-issuer-url` and `--idp-url`
  values of the service do not match the external host name of Keycloak. These
  values default to URLs derived from `global.clusterDomain`. For an
  existing Keycloak, set `service.auth.issuerUrl` to its realm issuer URL and
  `service.idp.url` to its server base URL. To check the values the running
  container actually received:

  ```console
  oc get deploy fulfillment-grpc-server -n <namespace> -o jsonpath='{.spec.template.spec.containers[0].args}' | tr ',' '\n' | grep -E 'issuer|idp'
  ```
- Missing `osac-db-config` or `osac-db-metering-config`: check the `osac`
  Helm release and its rendered Secrets. The chart creates these resources
  during installation. For missing database client-certificate Secrets,
  check their Certificate resources and the configured issuer; see
  [Section 11.6](#116-certificates-never-become-ready).
- Missing controller-credential Secrets: verify the credentials supplied
  by your Keycloak administrator and the Secret name and keys configured in
  `operator.fulfillment.credentials`.
- `lookup openbao.<namespace>.svc ... no such host` or
  `Failed to provision vault namespace`: `service.vault.endpoint` does not
  point at your reachable Vault or OpenBao instance, or the
  external store's namespace, policy, JWT role, or credentials are incorrect.
  Check the endpoint and follow the
  [secrets management configuration guide](secrets-management-configuration.md#troubleshooting).

### 11.11 Web console login loops with an `issuer not trusted` error

Confirm that the web console OIDC configuration and the Fulfillment
Service use the issuer URL supplied by your Keycloak administrator. The
issuer must match the `iss` claim in Keycloak tokens. Correct the OSAC
values and run `helm upgrade` again.

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

- **`osac` chart** — The OSAC platform Helm chart, published as an OCI artifact.
- **Published-chart install** — Installing the published `osac` chart onto a
  cluster where the prerequisite Operators and external services already exist.
- **Operand** — The custom resource that an Operator reconciles, for example
  `HyperConverged` for OpenShift Virtualization, `LVMCluster` for LVM Storage,
  or `IPAddressPool` for MetalLB.
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
  `dns.route53.dns` (default, AWS Route 53) or `dns.noop.dns` (OSAC does not
  create records).
- **config-as-code** — The Ansible content that the `osac-aap-bootstrap` job
  loads into AAP. Its subscription manifest is stored in the
  `config-as-code-manifest-ig` Secret.
- **Bundled compared with external** — Bundled PostgreSQL is for testing, and
  OpenBao is for testing and CI. Production deployments use external services.
