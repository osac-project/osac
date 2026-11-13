# Installing OSAC on OpenShift Container Platform (WIP)

**Last Updated**: 2026-11-06
**Audience**: Cloud administrators
**Status**: WIP — see the TBD note in [Section 10](#10-uninstalling-osac)
for what's still open

---

Deploy OSAC (Open Sovereign AI Cloud) onto an existing Red Hat OpenShift
Container Platform cluster by using the OpenShift CLI (`oc`) and Helm. This
guide covers the VMaaS, CaaS, and BMaaS services and assumes that your
platform administrators have prepared the prerequisite Operators and external
services listed in [Section 2](#2-prerequisites).
For prerequisite setup instructions, see the
[Helm Deployment Guide](helm-deployment-guide.md) before continuing.

## Contents

1. [OSAC installation architecture](#1-osac-installation-architecture)
    - [1.1 What is released](#11-what-is-released)
    - [1.2 Networking](#12-networking)
2. [Prerequisites](#2-prerequisites)
    - [2.1 Cluster and access](#21-cluster-and-access)
    - [2.2 Client tools](#22-client-tools)
    - [2.3 Platform Operators and components](#23-platform-operators-and-components)
    - [2.4 Credentials and external services](#24-credentials-and-external-services)
    - [2.5 Requirements by service](#25-requirements-by-service)
    - [2.6 EVPN host networking for VMaaS](#26-evpn-host-networking-for-vmaas)
3. [OSAC component versions](#3-osac-component-versions)
4. [Installing OSAC](#4-installing-osac)
    - [4.1 Preparing the cluster](#41-preparing-the-cluster)
    - [4.2 Configuring the Helm values](#42-configuring-the-helm-values)
    - [4.3 Installing OSAC](#43-installing-osac)
    - [4.4 Installing with provider networking](#44-installing-with-provider-networking)
        - [4.4.1 Choosing the networking settings](#441-choosing-the-networking-settings)
        - [4.4.2 Prerequisites for provider networking](#442-prerequisites-for-provider-networking)
        - [4.4.3 Setting the networking Helm values](#443-setting-the-networking-helm-values)
        - [4.4.4 Settings fixed at installation](#444-settings-fixed-at-installation)
        - [4.4.5 Applying the Fabric Manager registration](#445-applying-the-fabric-manager-registration)
        - [4.4.6 Checking the `NetworkClass`](#446-checking-the-networkclass)
        - [4.4.7 Creating the ExternalIPPools](#447-creating-the-externalippools)
        - [4.4.8 Replacing the `NetworkClass`](#448-replacing-the-networkclass)
5. [Helm chart configuration parameters](#5-helm-chart-configuration-parameters)
    - [5.1 Values set on the command line](#51-values-set-on-the-command-line)
6. [Installation workflows by service](#6-installation-workflows-by-service)
    - [6.1 Installing OSAC for VMaaS](#61-installing-osac-for-vmaas)
    - [6.2 Installing OSAC for CaaS](#62-installing-osac-for-caas)
    - [6.3 Installing OSAC for BMaaS](#63-installing-osac-for-bmaas)
    - [6.4 Installing VMaaS on a separate workload cluster](#64-installing-vmaas-on-a-separate-workload-cluster)
7. [Verifying the installation](#7-verifying-the-installation)
8. [Postinstallation tasks](#8-postinstallation-tasks)
    - [8.1 Accessing the OSAC consoles](#81-accessing-the-osac-consoles)
    - [8.2 Installing the `osac` CLI](#82-installing-the-osac-cli)
    - [8.3 Registering the hub](#83-registering-the-hub)
    - [8.4 Installing without DNS management](#84-installing-without-dns-management)
    - [8.5 Additional resources](#85-additional-resources)
9. [Supported configurations](#9-supported-configurations)
    - [9.1 Supported for production](#91-supported-for-production)
    - [9.2 Evaluation only](#92-evaluation-only)
    - [9.3 Not covered by this guide](#93-not-covered-by-this-guide)
    - [9.4 Not supported](#94-not-supported)
10. [Uninstalling OSAC](#10-uninstalling-osac)
11. [Troubleshooting](#11-troubleshooting)
    - [11.1 The install preflight stops the installation](#111-the-install-preflight-stops-the-installation)
    - [11.2 A Helm release is stuck in `pending-install` or `pending-upgrade`](#112-a-helm-release-is-stuck-in-pending-install-or-pending-upgrade)
    - [11.3 An Operator CSV never reaches `Succeeded`](#113-an-operator-csv-never-reaches-succeeded)
    - [11.4 An OCI pull fails](#114-an-oci-pull-fails)
    - [11.5 The `osac-db-init` hook fails](#115-the-osac-db-init-hook-fails)
    - [11.6 Certificates never become `Ready`](#116-certificates-never-become-ready)
    - [11.7 The AAP instance does not start](#117-the-aap-instance-does-not-start)
    - [11.8 The `osac-aap-bootstrap` job fails](#118-the-osac-aap-bootstrap-job-fails)
    - [11.9 The `osac-publish-templates` hook fails](#119-the-osac-publish-templates-hook-fails)
    - [11.10 The `fulfillment-*` pods are in `CrashLoopBackOff`](#1110-the-fulfillment--pods-are-in-crashloopbackoff)
    - [11.11 Web console login loops with an `issuer not trusted` error](#1111-web-console-login-loops-with-an-issuer-not-trusted-error)
    - [11.12 The first provisioning request fails while the pods are healthy](#1112-the-first-provisioning-request-fails-while-the-pods-are-healthy)
    - [11.13 Networking problems](#1113-networking-problems)
    - [11.14 Additional resources](#1114-additional-resources)
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
selecting a release with an `osac/v<VERSION>` tag. The repository also lists
individual component releases, such as `osac-operator`; use the `osac/v` tag
prefix to identify the umbrella chart. If its release page is not yet
available, select the matching `osac/v<VERSION>` tag from the
[OSAC tags page](https://github.com/osac-project/osac/tags), then confirm
that the chart is available in OCI using `helm show chart` as described in
[Section 3](#3-osac-component-versions).

An umbrella release's `osac-release/v<VERSION>` tag and matching
`osac/v<VERSION>` source tag point to the same commit. The source checkout
and download commands below use `osac/v`; nightly source tags also use this
prefix. This guide targets the OSAC `0.4.0` release.

For evaluation builds, check the
[OSAC Nightly Build workflow](https://github.com/osac-project/osac/actions/workflows/nightly-build.yaml)
and select the newest successful run. Use its exact chart version; a newer
tag alone does not mean that the full pipeline succeeded. The chart
includes a `values.schema.json` file and a `values-example.yaml` file.

Select the final umbrella chart version from the release links above and
export it once. The following example uses version `0.4.0`:

```console
export OSAC_RELEASE_VERSION="0.4.0"
export OSAC_CHART="oci://ghcr.io/osac-project/charts/osac"
```

The preflight described in
[Section 11.1](#111-the-install-preflight-stops-the-installation) requires a
chart containing `templates/preflight/_engine.tpl`, as in version `0.4.0`.
Older charts can have a different schema and validation workflow. Keep the
selected chart's pinned image and AAP content defaults; remove stale image or
Ansible revision overrides from an older values file. The bundled example
values, combined with the documented external-service and separate-workload
settings, were rendered successfully with `helm template` against the `0.4.0`
chart contents.

Use only the chart version, without either tag prefix. For evaluation, set
`OSAC_RELEASE_VERSION` to the exact nightly version from that successful run.
Select the version before retrieving chart values and keep it unchanged
throughout the installation. Run the remaining commands in the same shell
session; they use `OSAC_CHART` for the chart location and
`OSAC_RELEASE_VERSION` for the chart version and matching source downloads.

To install for a specific service, follow the workflow in
[Section 6](#6-installation-workflows-by-service). Each workflow lists the
toggles and values to set.

### 1.2 Networking

OSAC gives tenants VirtualNetworks, Subnets, SecurityGroups, ExternalIPs, and
NATGateways through two network managers:

- **Fabric Manager.** Configures the provider's physical fabric. The provider
  writes it, or uses one that OSAC ships, such as Netris. Every deployment
  selects one.
- **Kubernetes Manager.** Configures hub networking for VMs. OSAC writes it.
  This release has `cudn_evpn`, for the `evpn` network type. Only VMaaS needs
  it. Its host side is EVPN host networking (see
  [Section 2.6](#26-evpn-host-networking-for-vmaas)).

The installer creates the deployment's one `NetworkClass` from the Helm values
`global.networking.*`. The `NetworkClass` names both managers and holds the
tenant defaults. With provider networking enabled, the provider's Fabric
Manager registers with a `ConfigMap` that you apply. Its Ansible role runs in
the AAP execution environment image `aap.configAsCode.eeImage`.

`global.networking.provisioningEnabled` turns provider networking on or off.
With it on (the default), OSAC runs the managers' tasks. With it off, OSAC runs
no networking job: networking resources become `READY` without connectivity,
and workloads use the fallback of their service. You set it at the first
install, and it never changes.

Provider networking is supported for VMaaS and for the CaaS endpoint lifecycle.
CaaS endpoint reservation and managed endpoint DNS require
`global.networking.provisioningEnabled: true`. With it disabled, ClusterOrder
creates no endpoint reservation, automatic ExternalIP resources or attachments,
or endpoint DNS; configure CaaS reachability and DNS separately. BMaaS-only
installations may still run with provider networking disabled; BMaaS-specific
provider networking remains unsupported.

The installer also creates an admission policy. It rejects any create, update
(also of `status`), or delete of an OSAC networking custom resource
(`NetworkClass`, `ExternalIPPool`, `VirtualNetwork`, `Subnet`,
`SecurityGroup`, `ExternalIP`, `ExternalIPAttachment`, `NATGateway`,
`FabricDomain`, `NetworkAttachment`) in the OSAC namespace that does not come
from the OSAC controllers, with the message "OSAC manages this resource; change
it through the fulfillment API". This includes removing a finalizer from one of
these resources.

To install with provider networking, see
[Section 4.4](#44-installing-with-provider-networking).

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
- With VMaaS, the nodes pull each VM's root disk image themselves (CDI
  `pullMethod: node`), so creating a VM needs no tenant network and no
  NATGateway. The nodes must reach the image registry, and a registry that
  needs credentials must be in the cluster pull secret; there is no per-image
  secret.
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
- `curl` and `jq`, for the private API calls that set up networking (see
  [Section 4.4](#44-installing-with-provider-networking)).

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
| [LVM Storage](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html-single/storage/index#persistent-storage-using-lvms) | `stable-<cluster_minor>` | `openshift-storage` | VMaaS | `lvms.enabled` | Development/CI VMaaS using OSAC-managed local storage |
| [MetalLB Operator](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/networking_operators/metallb-operator) | `stable` | `metallb-system` | CaaS |
| [multicluster engine for Kubernetes Operator](https://docs.redhat.com/en/documentation/red_hat_advanced_cluster_management_for_kubernetes/2.13/html/clusters/cluster_mce_overview) | `stable-2.17` | `multicluster-engine` | CaaS |
| Kubernetes NMState Operator | `stable` | `openshift-nmstate` | VMaaS with provider networking |
| EVPN host networking (FRR, `tenant-vtep`, BGP to the fabric) | Not applicable | `openshift-frr-k8s`, cluster-scoped | VMaaS with provider networking |

Notes on individual components:

- **cert-manager Operator for Red Hat OpenShift** installs into the
  `cert-manager-operator` namespace and creates its operands in the
  `cert-manager` namespace. The `certificates.cert-manager.io` CRD is a
  mandatory install preflight check.
- trust-manager is required for every installation and can distribute the CA
  bundle. `caIssuer.enabled` is `true` by default, so the chart's `Bundle`
  manages the ConfigMap named by `certs.caBundle.configMap` (normally
  `ca-bundle`) and can overwrite it. Before removing or changing an existing
  `Bundle`, copy the full `bundle.pem` data from each ConfigMap it manages to a
  replacement ConfigMap with a different name in each affected namespace.
  Update the affected services to use those replacements and verify they are
  available first; removing a `Bundle` can delete its target ConfigMaps. If
  `service.certs.caBundle.configMap` and `certs.caBundle.configMap` name the
  same ConfigMap and you manage it directly, complete this transfer before
  changing the `Bundle` so it no longer targets that ConfigMap. Then set
  `caIssuer.enabled: false` in the infrastructure values file, such as
  `my-infra-values.yaml`, not `my-values.yaml`, and apply the infrastructure
  release before installing or upgrading OSAC. If
  `service.certs.caBundle.configMap` uses a different name, you can provide
  that ConfigMap directly and leave `caIssuer.enabled: true`. On an existing
  installation, check who owns `default-ca` and which certificates use it
  before disabling `caIssuer`; it removes the issuer and CA certificate and
  may stop Keycloak certificate renewal. See [CA bundle and issuer guidance](helm-deployment-guide.md#when-prerequisites-already-exist).
  Disabling `caIssuer.enabled` also disables the chart's issuer, so provide any
  issuer you need. The ConfigMap must exist in the namespace where OSAC is
  installed.
  Disabling `caIssuer.enabled` also disables the automatic Keycloak Route
  update. If your public-ingress Keycloak Route uses `reencrypt`, set
  `destinationCACertificate` to the CA that signs Keycloak's serving
  certificate and keep it current. See
  [Keycloak Route with Public Ingress Certificates](helm-deployment-guide.md#keycloak-route-with-public-ingress-certificates).
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
- **MetalLB Operator** provides the CaaS API management VIPs on the hub. Ask
  your platform administrators for the reserved, free, routable address range
  and add a covering CIDR to `global.networking.excludedIpv4Cidrs` before
  creating Clusters. Another load-balancer implementation can provide them.
- **multicluster engine for Kubernetes Operator** is required for agent-based
  cluster provisioning. An existing standalone installation or Red Hat
  Advanced Cluster Management for Kubernetes (RHACM) can provide it.
  minor version; the installation sets it. The OSAC-managed LVMS path is for
  development and CI and uses an explicit `optionalPaths` allowlist; it does
  not create devices. In production, disable `lvms.enabled` and configure the
  customer-managed LVMS operator and its `LVMCluster` selector yourself; do not
  rely on dynamic device discovery.
- **MetalLB Operator** provides a `LoadBalancer`-class implementation. CaaS
  uses it on the hub to give each hosted cluster's API its management VIP.
  Only CaaS needs it. Any solution that provides one works if you disable
  `metallb.enabled`. With `metallb.enabled`, the installation creates the
  address pool `caas-address-pool` and sets it to addresses `.240` to `.250`
  of the first node's network. It keeps the first three octets of that node's
  `InternalIP`, so it assumes a /24. It resets the pool to that range on every
  install and upgrade of the infrastructure chart. Before the first Cluster,
  make sure that range is free and routable on your management network, and
  add a CIDR that covers it to `global.networking.excludedIpv4Cidrs`, for
  example `<prefix>.240/28`: a Cluster keeps its VIP for its whole life. To
  use a different range, patch the `IPAddressPool` after the installation and
  again after every infrastructure upgrade, add a CIDR that covers that range
  to `global.networking.excludedIpv4Cidrs`, and check the pool before you
  create Clusters.
- **multicluster engine for Kubernetes Operator** is required for
  agent-based cluster provisioning but is disabled by default. Set
  `mce.enabled=true` when this installation owns standalone MCE; the `caas-ci`
  infrastructure profile does so explicitly. Leave it `false` when Red Hat
  Advanced Cluster Management for Kubernetes (RHACM) or another MCE
  installation already owns it. The disabled state also prevents the phase-1
  chart from applying its temporary Assisted image override `ConfigMap` and
  compatibility RBAC.
- **Kubernetes NMState Operator** sets the uplink and VTEP addresses of the
  VMaaS nodes. Your platform administrators prepare it for EVPN networking.
- **EVPN host networking** needs OVN-Kubernetes and OpenShift Container
  Platform 4.22 or later. See
  [Section 2.6](#26-evpn-host-networking-for-vmaas).

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

Required when the OSAC chart bootstraps chart-managed AAP:

- **AAP subscription manifest (`license.zip`).** A Subscription Allocation
  export from the [Red Hat Customer Portal](https://access.redhat.com/). The AAP
  bootstrap job cannot start without it. You load it into the
  `config-as-code-manifest-ig` Secret in
  [Section 4.1](#41-preparing-the-cluster). External AAP administrators manage
  their AAP license; when `aap.bootstrap.enabled` is `false`, this local Secret
  is not used. For more information, see "Obtaining an AAP License" in
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
  For end-to-end external-database wiring — identity and certificate
  requirements, and values — see the
  [external services support guide](external-services-support-guide.md).
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
- **The CaaS prerequisites** that OSAC does not create: the management
  publication on the hub (MetalLB and routes), the BMaaS provisioning network,
  and, with `dns.route53.dns`, the public DNS zone `DNS_ZONE`.
- **CaaS endpoint networking.** Set
  `global.networking.provisioningEnabled: true` for the managed endpoint
  lifecycle. The NetworkClass address plan needs a non-empty `lbVip` share, and
  automatic API and ingress ExternalIP attachments need a `READY`
  ExternalIPPool. The ClusterOrder controller reserves the guest ingress VIP,
  waits for the automatic endpoint attachments, and then publishes DNS. With
  provider networking disabled, it creates no ingress reservation, automatic
  ExternalIP resources or attachments, or endpoint DNS. In that mode, configure
  the routes and DNS records yourself.
- **Netris CaaS worker attachments.** If Netris is the Fabric Manager and
  workers use physical network attachments, set `NETRIS_BM_PROVISIONING_VNET`
  in `aap.instanceGroups.networkFulfillment.config` to the existing V-Net
  carrying their provisioning ports. The physical apply and cleanup tasks
  require this setting; if it is missing, those tasks report a retryable
  configuration error. See [Section 4.4.3](#443-setting-the-networking-helm-values).

Required for networking, for every service:

- **A Fabric Manager.** Every deployment names one in
  `global.networking.fabricManager`, also with provider networking disabled,
  because the `NetworkClass` needs it. Netris is the only Fabric Manager that
  OSAC ships; any other is supplied by the provider
  ([Section 9.4](#94-not-supported)). With provider networking enabled, you
  get from the provider the execution environment image, the settings and
  secrets, the registration manifest, and the values agreed for the
  installation. With `fabricManager: netris`, the installer registers the
  Netris Fabric Manager itself, so you apply no registration and need no
  provider image; you still need the Netris settings and secrets (see "Netris
  settings" in [Section 4.4.3](#443-setting-the-networking-helm-values)). With
  provider networking disabled, OSAC reads no registration, and you need none
  of these. See [Section 4.4](#44-installing-with-provider-networking) and the
  [Fabric Manager Guide](../networking/customer-fabric-manager-guide.md).
- **A connected deployment for OSAC `0.4.0`.** The networking procedures in
  this guide require the hub, the AAP job pods, and the provider's networking
  services and APIs to reach each other. This guide does not cover air-gapped
  or disconnected deployments.

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

Required for BMaaS, with any backend:

- **The provisioning network.** The provisioning network, with host isolation,
  and each host's first attachment to it; and BMC reachability on a management
  network isolated from tenants. OSAC does not create these.
- **Coming with BMaaS networking in a later release:** the inventory's map of
  port names to MAC addresses for each host, and the Fabric Manager's map from
  host name and port name to switch port, in its settings or inventory. To
  prepare, record for each host a JSON object `{"<port name>": "<MAC>"}`,
  with the port names of its BareMetalInstanceType: on Metal3, the
  BareMetalHost annotation `osac.openshift.io/interface-macs`; on NetBox, the
  device custom field `osac_interface_macs`; on BCM, the device
  `extra_values["osac_interface_macs"]`.

### 2.5 Requirements by service

Choose the services at the first install. Enabling BMaaS or CaaS after
networking resources exist is not supported (see
[Table 4.2](#table-42-settings-fixed-at-installation)).

- **VMaaS** (`global.services.vmaas.enabled`) requires OpenShift Virtualization,
  LVM Storage or another dynamic storage class, and OSAC's Kubernetes Manager
  `cudn_evpn`. With provider networking enabled, it also requires a Fabric
  Manager whose registration lists the `evpn` network type, EVPN host
  networking on the cluster that runs the VMs
  ([Section 2.6](#26-evpn-host-networking-for-vmaas)), and the fabric side
  configured by the provider. See
  [Section 4.4](#44-installing-with-provider-networking).
- **CaaS** (`global.services.caas.enabled`) requires BMaaS, because every
  worker is a bare-metal instance (the installer rejects CaaS without VMaaS or
  BMaaS, but does not require BMaaS);
  multicluster engine (standalone, or provided by RHACM if it's installed);
  MetalLB or another `LoadBalancer`-class implementation; a DNS backend; the
  CaaS prerequisites in
  [Section 2.4](#24-credentials-and-external-services); and
  `aap.instanceGroups.clusterFulfillment` configuration. The managed endpoint
  lifecycle requires provider networking enabled, a non-empty `lbVip` address
  share, and a `READY` ExternalIPPool for automatic API and ingress attachments.
  Hosted control planes provision etcd persistent volume claims from the
  hub's default storage class (see [Section 2.1](#21-cluster-and-access)).
  For more information,
  see
  [`aap-configuration.md`](https://github.com/osac-project/osac/blob/main/osac-installer/docs/aap-configuration.md).
- **BMaaS** (`global.services.bmaas.enabled`) requires BareMetalOperator and a
  `Provisioning` custom resource with `spec.watchAllNamespaces: true` when
  either `bmf.metal3.enabled` or `bmf.netbox.enabled` is `true`, and the
  BMaaS prerequisites in
  [Section 2.4](#24-credentials-and-external-services). A BMaaS-only
  installation may run with provider networking disabled. When BMaaS is
  installed with CaaS, use the CaaS networking setting from
  [Section 6.2](#62-installing-osac-for-caas).

### 2.6 EVPN host networking for VMaaS

With VMaaS and provider networking enabled, the Kubernetes Manager
`cudn_evpn` connects VMs to the provider's fabric over EVPN. The cluster that
runs the VMs needs EVPN host networking: FRR and route advertisements in the
cluster network, an uplink address and a VTEP address on each VMaaS node, the
`tenant-vtep` VTEP, and BGP sessions from each node to the fabric. Your
platform administrators configure this OpenShift side; the provider configures
the fabric side. With provider networking disabled, skip
this section.

EVPN host networking needs OVN-Kubernetes and OpenShift Container Platform
4.22 or later. Coordinate preparation with the responsible platform and fabric
administrators, using the administrator procedure for your environment. The
[Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md#evpn-host-networking-vmaas)
provides one example procedure for preparing the required host configuration.

Before installation, confirm these inputs and the administrator procedure
with the teams preparing the VMaaS cluster and fabric. Ask them where to
provide the values and how they will verify the resulting host configuration
and BGP readiness.

| Value | Information to confirm | Owner or source |
|---|---|---|
| `evpn.enabled` | Enable host networking for CUDN EVPN on the VMaaS cluster. | Platform administrator |
| `evpn.nmstate.install`, `evpn.nmstate.channel` | Whether NMState must be installed and which supported channel to use. | Cluster administrator |
| `evpn.configureClusterNetwork` | Whether to apply the cluster network settings; this restarts OVN-Kubernetes once. | Cluster administrator |
| `evpn.vtepCIDR` | Address range reserved for node VTEP addresses and configured on the fabric. | Agree with the fabric administrator |
| `evpn.bgp.asn`, `evpn.nodes[].asn` | Node ASN and any per-node overrides; they must match the fabric BGP configuration. | Fabric administrator |
| `evpn.bgp.acceptPrefixes` | Fabric loopback ranges to accept; only `/32` routes are accepted. | Fabric administrator |
| `evpn.bgp.waitForEstablished`, `evpn.bgp.timeoutSeconds` | Whether installation waits for BGP sessions (default `true`) and the maximum wait per session (default `900` seconds). | Platform administrator |
| `evpn.nodes[].name` | Kubernetes node name for each node that will run VMs. | Cluster administrator |
| `evpn.nodes[].vtepIP` | Unique address inside `evpn.vtepCIDR` for each node, configured on the fabric as well. | Agree with the platform and fabric administrators |
| `evpn.nodes[].uplinks[].interface`, `evpn.nodes[].uplinks[].address` | Node interface connected to a fabric leaf; the address is optional when already configured. | Cluster and fabric administrators |
| `evpn.nodes[].uplinks[].peerAddress`, `evpn.nodes[].uplinks[].peerASN` | Address and ASN of the connected fabric BGP peer. | Fabric administrator |

For a VirtualNetwork's Subnet, `cudn_evpn` creates a
`ClusterUserDefinedNetwork` (CUDN) on `tenant-vtep`, with the VNIs that the
Fabric Manager allocated: a MAC-VRF (VNI `<l2_vni>`, route target `0:<l2_vni>`) and
an IP-VRF (VNI `<l3_vni>`, route target `0:<l3_vni>`). OVN-Kubernetes serves
the VMs' gateway (the CUDN's `defaultGatewayIPs`) on every node and routes
into the IP-VRF. The fabric carries the MAC-VRF and the IP-VRF to the nodes and
routes the IP-VRF. It need not serve the VM gateway. A Fabric Manager may
also serve the same gateway address on its own devices. If it does, it uses
the same MAC address as OVN-Kubernetes or accepts that routed traffic can see
two gateway MAC addresses, and its design says which. The Netris Fabric Manager
serves the gateway on its V-Net, and the shared-MAC setting is a follow-up, so
routed traffic can see two gateway MAC addresses today.

`cudn_evpn` builds one CUDN per VirtualNetwork, owned by its first Subnet.
In this release VMs run only on that Subnet. A VM on another Subnet of the
same VirtualNetwork fails as final with "cudn_evpn implementation limit: VMs
can attach only to the Subnet that owns its VirtualNetwork's hub network".

With the Netris Fabric Manager, also note:

- SecurityGroups are documented no-ops on Netris. `cudn_evpn` enforces them
  with NetworkPolicy on the hub.
- Netris allocates both VNIs: the VPC's VNI maps to `l3_vni`, and the
  V-Net's VXLAN ID maps to `l2_vni`; OSAC has no VNI range to configure.
- Netris reserves its private IP allocation (by default `10.0.0.0/8`). Add it
  to `global.networking.excludedIpv4Cidrs`, so no VirtualNetwork, Subnet or
  ExternalIPPool takes an address from it; use another private range such as
  `172.16.0.0/12` for tenants. The chart's default tenant CIDRs
  (`10.200.0.0/16` and `10.200.0.0/20`) lie inside it, so set
  `networkClass.defaults` outside it, for example `172.16.0.0/16` and
  `172.16.0.0/20`; otherwise `create-network-class` fails with
  "excludedIpv4Cidrs range 10.0.0.0/8 overlaps the default VirtualNetwork
  CIDR". The 10.x addresses in the examples of this guide are for other Fabric
  Managers.
- Observed on the lab: Netris builds a V-Net on a switch only where a member
  port exists. A probe V-Net with no member port appeared on no leaf or
  SoftGate. Same-Subnet VM traffic works. Routed traffic needs the VPC on the
  leaves and SoftGates.
- `cudn_evpn` uses the route target `0:<VNI>`. Netris has no route-target
  setting. In the lab probe its SoftGates used `0:<VNI>`, but its leaves used
  `<ASN>:<VNI>`. NAT egress and ExternalIP ingress over Netris pass in the OSAC
  lab, with the IPAM objects outside the VPC and the rules inside it. Routed
  traffic that depends on the leaves importing `0:<VNI>` is not yet verified.
  An optional route-target field in the Fabric result is a follow-up. A task is
  READY when Netris accepted the configuration, not when traffic flows.
- Netris objects of the ExternalIP and NATGateway tasks. An ExternalIPPool is a
  Netris IPAM allocation named `osac-pool-<pool id>`, plus a common subnet
  `osac-pool-<pool id>-common` over the same CIDR. Netris blackholes the return
  traffic of a /32 nat address that has no common subnet around it. An
  ExternalIP is a /32 reservation named `osac-eip-<ExternalIP id>` inside it. A NATGateway is one SNAT rule
  `osac-natgw-<NATGateway id>` per VirtualNetwork, from the VirtualNetwork
  CIDR to the ExternalIP. An ExternalIPAttachment is a DNAT rule
  `osac-eipa-<attachment id>` to the destination address (Netris runs NAT
  rules on the SoftGates). A
  delete that finds a rule whose addresses differ from the stored input fails
  and writes nothing. OSAC retries it, so the resource stays DELETING until the
  rule is repaired.
- A FabricDomain is a Netris Server Cluster `osac-fd-<id>`. Its template is
  the one distinct `server_cluster_template_id` in `NETRIS_RESOURCE_CLASS_MAP`
  (known limitation: two different templates fail as final). CaaS ingress
  addresses are reserved from the Subnet's recorded `lb_vip_ipv4_cidrs` range;
  configure a non-empty `lbVip` share in the Subnet address plan for CaaS.
- ICMP SecurityGroup rules need the registration capability
  `security_group_icmp`. Netris declares it, although its SecurityGroup tasks
  are no-ops. `cudn_evpn` does not, because a NetworkPolicy cannot match ICMP,
  so a NetworkClass with Netris and `cudn_evpn` rejects ICMP: the fulfillment
  API answers `InvalidArgument` with "use protocol all, which includes ICMP".
  A NetworkClass with Netris alone (`k8sManager` empty) accepts ICMP.
- DNS: VMs use the cluster DNS that OVN-Kubernetes DHCP gives them on the
  primary UDN. OSAC has no DNS setting.

#### Who does what

- **Platform administrators** prepare the NMState Operator and verify that
  the cluster uses OVN-Kubernetes and every selected VMaaS node exists. They
  configure the following host resources using their environment's
  administrator procedure. The
  [Helm Deployment Guide](helm-deployment-guide.md#evpn-host-networking-vmaas)
  provides an example:
  1. They configure the cluster network operator for FRR, route advertisements, routing via host, and
     global IP forwarding. This restarts OVN-Kubernetes once.
  2. They create the `nmstate` instance.
  3. They create one `NodeNetworkConfigurationPolicy`, `osac-evpn-<node>`, per
     node. The policy sets each supplied uplink address and creates the dummy
     interface `evpn-vtep0` with the node's VTEP address.
  4. They create the VTEP `tenant-vtep` for `vtepCIDR`.
  5. For each node, they create the `FRRConfiguration` resources `osac-evpn-<node>` and
     `osac-evpn-raw-<node>` in `openshift-frr-k8s`. Each node peers with every
     uplink's `peerAddress` and `peerASN`, advertises its VTEP address as a
     /32, accepts only /32 routes inside `bgp.acceptPrefixes`, and activates
     the EVPN address family.
  6. They create the `RouteAdvertisements` `osac-evpn`, which selects the CUDNs
     labelled `evpn: "true"` that `cudn_evpn` creates later.
  7. They confirm that every node-to-fabric BGP session is `Established`.
- **The provider (fabric admin)** configures the fabric side for every VMaaS
  node:
  - the switch ports and the leaf end of each uplink;
  - a BGP neighbor for each node uplink, with the node's ASN;
  - acceptance of the node VTEP addresses in `vtepCIDR`, and advertisement of
    the fabric loopbacks inside `bgp.acceptPrefixes`;
  - the EVPN address family;
  - for every VNI that OSAC uses, import and export of the route target
    `0:<VNI>`: `0:<l2_vni>` for the MAC-VRF and `0:<l3_vni>` for the IP-VRF;
  - VXLAN on UDP port 4789 between the fabric VTEPs and the node VTEPs, with
    broadcast, unknown-unicast and multicast traffic flooded by ingress
    replication (no multicast underlay);
  - frames of at least 1550 bytes in the underlay and on the node NICs.

  This needs nothing from OpenShift, so the provider can configure it before
  OpenShift is installed. The BGP sessions stay down, and the fabric retries
  them, until the node side exists. Your platform administrators verify that
  they become `Established` before you install OSAC.

#### Host-network settings and lifecycle

Agree with your platform and fabric administrators on the VTEP range, each
node's VTEP address, node and fabric ASNs, uplink interfaces and addresses,
fabric peers, and fabric loopback ranges. The node ASN, VTEP range, and any
provider VNI range are fixed after setup. Include the VTEP, fabric loopback,
and uplink ranges in `global.networking.excludedIpv4Cidrs` before installing
OSAC ([Section 4.4.1](#441-choosing-the-networking-settings)).

Platform administrators own the host-network values, BGP readiness checks,
node additions and removals, and cleanup. Follow the administrator procedure
for your environment. For the example setup using Helm, lifecycle procedures,
including removal of the final configured node, are in
[EVPN host networking (VMaaS)](helm-deployment-guide.md#evpn-host-networking-vmaas).
Deleting a `NodeNetworkConfigurationPolicy` does not remove interface
addresses from the node; administrators remove stale addresses separately.
Uninstalling OSAC leaves EVPN host resources and cluster-network settings in
place.

EVPN host networking can be prepared on a separate workload cluster, but
OSAC provider networking with VMs on a separate cluster is not supported in
this release (see [Section 9.4](#94-not-supported)). Use
[Section 6.4](#64-installing-vmaas-on-a-separate-workload-cluster) with provider
networking disabled.

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
| Kubernetes NMState Operator | `stable` (`evpn.nmstate.channel`) | Not recorded |

The selected `osac` chart pins its subcharts through its `Chart.lock` file.
Consult the release's dependency inventory for their exact versions. You can
also inspect the published chart metadata:

```console
helm show chart "$OSAC_CHART" --version "$OSAC_RELEASE_VERSION"
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
- For chart-managed AAP with `aap.bootstrap.enabled: true`, you have the AAP
  subscription manifest file (`license.zip`). External AAP administrators
  manage their AAP license; no local manifest Secret is used when its bootstrap
  job is disabled.
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
   # Only for chart-managed AAP with a bootstrap job:
   export AAP_LICENSE_FILE=/path/to/license.zip
   ```

2. Create the target namespace:

   ```console
   oc create namespace "$NS" --dry-run=client -o yaml | oc apply -f -
   ```

3. For chart-managed AAP with `aap.bootstrap.enabled: true`, create the Secret
   that holds the AAP subscription manifest. Skip this step for external AAP
   when its bootstrap job is disabled:

   ```console
   oc create secret generic config-as-code-manifest-ig --from-file=license.zip="$AAP_LICENSE_FILE" -n "$NS" --dry-run=client -o yaml | oc apply --server-side -f -
   ```

4. For chart-managed AAP with `aap.bootstrap.enabled: true`, label the Secret
   so that the AAP bootstrap job reads it. Skip this step when the bootstrap
   job is disabled:

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

1. Retrieve the chart defaults and unpack the selected chart to access its
   bundled example values:

   ```console
   helm show values "$OSAC_CHART" --version "$OSAC_RELEASE_VERSION" > values-upstream.yaml
   OSAC_CHART_DIR=$(mktemp -d)
   helm pull "$OSAC_CHART" \
     --version "$OSAC_RELEASE_VERSION" \
     --untar \
     --untardir "$OSAC_CHART_DIR"
   ```

2. Create `my-values.yaml` for the `osac` chart. Copy the keys you need from
   `$OSAC_CHART_DIR/osac/values-example.yaml`, which demonstrates external
   PostgreSQL and Keycloak settings but uses development `latest` image tags.
   For production, omit its image blocks and scalar image overrides, including
   `aap.bootstrap.image`, so the selected chart's pinned image defaults apply.
   If you need image overrides, use the pinned values from
   `values-upstream.yaml`. Leave out `dbMigrate.dbUrl`, which the chart schema
   rejects. Set `global.osacDeploymentId` to a stable identity for this
   deployment; the install fails without it.
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

   When the web console is enabled (`ui.enabled: true`, the default), set
   `ui.auth.oidcIssuerURL` in `my-values.yaml` to the fully resolved
   `service.auth.issuerUrl`. The UI chart requires an explicit URL; it does
   not inherit the service value or expand templates. For example, with an
   external Keycloak:

   ```yaml
   service:
     auth:
       issuerUrl: https://keycloak.example.com/realms/osac
     idp:
       url: https://keycloak.example.com
   ui:
     auth:
       oidcIssuerURL: https://keycloak.example.com/realms/osac
   ```

   Replace the two realm issuer URLs with the issuer supplied by your
   Keycloak administrators. Set `service.idp.url` to the external Keycloak
   base URL; the Fulfillment Service uses it for user and role management.
   For BMaaS, when `bmf.secretAPI.url` is set, also set its separate issuer
   value to the same realm issuer:

   ```yaml
   bmf:
     secretAPI:
       issuerUrl: https://keycloak.example.com/realms/osac
   ```

   If you keep the default `service.auth.issuerUrl`, set
   `ui.auth.oidcIssuerURL` to
   `https://keycloak-keycloak.<cluster-domain>/realms/osac`, replacing
   `<cluster-domain>` with the actual `global.clusterDomain` value.

4. For a production deployment, add `service.vault` to `my-values.yaml` using
   the [secrets management configuration guide](secrets-management-configuration.md#configure-the-osac-instance).

5. Add the service-specific value blocks (`global.services.*`,
   `global.networking`, `aap`, `metering`, and `bmf`)
   from [Section 6](#6-installation-workflows-by-service) for the service you
   are installing.

6. Add the networking values from
   [Section 4.4.3](#443-setting-the-networking-helm-values).

Any workflow that uses AAP for provisioning requires an Operator AAP token. This
includes CaaS `ClusterOrder` and VMaaS `ComputeInstance` provisioning, including
VMaaS on a separate workload cluster, as well as provider networking. The token
is required even when provider networking is disabled.

**Chart-managed AAP.** The `create-api-token` hook waits for the chart-managed
AAP admin Secret and gateway, creates `osac-aap-api-token`, and restarts
`deploy/osac-operator`. For production, set `operator.aap.url` to the AAP
Controller's trusted HTTPS Route and set `operator.aap.insecureSkipVerify` to
`"false"`. An empty URL defaults to `http://osac-aap/api/controller`, which
sends the token without TLS. Set the Operator to read the token Secret:

```yaml
operator:
  aap:
    url: https://<trusted-aap-route>/api/controller
    insecureSkipVerify: "false"
    tokenSecret:
      name: osac-aap-api-token
      key: token
```

The Deployment reads the Secret only when this reference is set. If
`operator.aap.tokenSecret.name` is set, the Deployment uses the Secret and
ignores `operator.aap.token`; clear the Secret name when switching to a direct
token.

**Externally managed AAP.** The chart's bootstrap and token hooks wait for the
chart-managed AAP admin Secret and gateway. Setting
`aap.aap.instance.enabled: false` alone does not disable those hooks. Disable
the chart-managed instance, bootstrap, token creation, and template-publishing
hook, and configure the external Controller URL and token Secret:

```yaml
aap:
  aap:
    instance:
      enabled: false
  bootstrap:
    enabled: false
  apiToken:
    create: false
  instanceGroups:
    publishTemplates:
      enabled: false
operator:
  aap:
    url: https://<external-aap>/api/controller
    insecureSkipVerify: "false"
    tokenSecret:
      name: osac-aap-api-token
      key: token
# Include this block when BMaaS is enabled. BMF has its own AAP URL.
bmf:
  env:
    aapUrl: https://<external-aap>/api/controller
    aapInsecureSkipVerify: "false"
```

Before installation, create `osac-aap-api-token` in the OSAC release namespace
with the external AAP API token under the `token` key. For example, store the
token in a protected local file and create the Secret with:

```console
oc create secret generic osac-aap-api-token \
  --namespace <namespace> \
  --from-file=token=<path-to-token-file>
```

Do not put the token in the values file. The external AAP must already be
reachable and bootstrapped with the release-compatible OSAC configuration,
including the organizations, projects, job templates, inventories, credentials,
execution environments, and instance groups required by enabled services. For
networking instance-group settings and secrets, also follow
[Section 4.4.3](#443-setting-the-networking-helm-values). For VMaaS, the
`aap.configAsCode.remoteClusterKubeconfigSecretName` setting configures only
chart-managed AAP. External jobs in another cluster or namespace cannot mount a
Secret from the OSAC release namespace. Follow
[Section 6.4](#64-installing-vmaas-on-a-separate-workload-cluster) to provide a
kubeconfig for the cluster that hosts the VMs to both external
`compute-instance-operations` and `storage-operations` instance groups. Use the
workload-cluster kubeconfig when VMs run on a separate workload cluster; if VMs
remain on the management (hub) cluster while external AAP jobs run elsewhere,
use the hub kubeconfig. The token must be authorized to launch and read those
jobs. Because the chart's
publication hook targets the chart-managed AAP instance, publish the required
templates to Fulfillment separately. See
[Section 5](#5-helm-chart-configuration-parameters) for the related values.

After rotating the external token in the Secret, restart the operators so they
reload it from their environment:

```console
oc rollout restart deployment/osac-operator -n <namespace>
oc rollout status deployment/osac-operator -n <namespace>
# When BMaaS is enabled:
oc rollout restart deployment/bmf-operator-controller-manager -n <namespace>
oc rollout status deployment/bmf-operator-controller-manager -n <namespace>
```

For production HTTPS connections, the AAP certificate must be trusted by each
operator's system trust store. The Fulfillment CA bundle does not configure AAP
trust. Keep `operator.aap.insecureSkipVerify` and, when BMaaS is enabled,
`bmf.env.aapInsecureSkipVerify` set to `"false"`; set either to `"true"` only
for temporary evaluation with an untrusted certificate.

When the Fulfillment Service and hub run on the same cluster, set the top-level
`hubAccess.enabled` value before installing:

```yaml
hubAccess:
  enabled: true
```

This enables the local hub access resources and registration hook. It is
separate from `operator.hubAccess.enabled`; see
[Section 8.3](#83-registering-the-hub) for the automatic registration behavior.

For a full parameter reference, see
[Section 5](#5-helm-chart-configuration-parameters).

### 4.3 Installing OSAC

Use this procedure when the cluster already meets the prerequisites.

**Prerequisites**

- The prerequisite Operators from
  [Table 2.1](#table-21-platform-operators-and-components) are installed.
- The issuer configured by `service.certs.issuerRef`, trust-manager, the
  CA-bundle `ConfigMap` named by `service.certs.caBundle.configMap`, Keycloak
  with the `osac` realm, and the credential Secrets exist. The ConfigMap must
  exist in the OSAC release namespace and is required even when the API
  certificates use public trust. trust-manager is required even when
  administrators provide the ConfigMap directly. Confirm its name and contents
  with your platform administrators; see step 4 of
  [Section 7](#7-verifying-the-installation).
- For a production deployment, the external PostgreSQL server is ready for
  the chart's client-certificate authentication, and `dbInit.host` is set
  in `my-values.yaml`.
- For a production deployment, the external Vault-compatible is configured
  according to the [secrets management configuration guide](secrets-management-configuration.md).
- You completed [Section 4.1](#41-preparing-the-cluster) and
  [Section 4.2](#42-configuring-the-helm-values).
- For AAP-backed provisioning, `my-values.yaml` sets
  `operator.aap.tokenSecret` to a Secret containing the AAP token, or sets a
  direct `operator.aap.token`. This is required for CaaS `ClusterOrder`, VMaaS
  `ComputeInstance` (including separate-workload deployments), and provider
  networking, even when provider networking is disabled. For chart-managed AAP,
  the `create-api-token` hook creates the Secret; for external AAP, create it
  before installation and disable the chart-managed bootstrap and token hooks
  as described in [Section 4.2](#42-configuring-the-helm-values). Without a
  usable token, AAP requests can fail with HTTP 401.
- When the Fulfillment Service and hub run on the same cluster,
  `my-values.yaml` sets top-level `hubAccess.enabled: true` before installation.
  This runs the local hub registration hook; see
  [Section 8.3](#83-registering-the-hub). `operator.hubAccess.enabled` is a
  separate setting and does not replace it.
- With provider networking enabled, you completed the networking prerequisites
  in [Section 4.4.2](#442-prerequisites-for-provider-networking).
- With VMaaS and provider networking enabled, EVPN host networking is
  configured ([Section 2.6](#26-evpn-host-networking-for-vmaas)).

**Procedure**

- Install the `osac` chart by running the following command:

  ```console
  helm install osac "$OSAC_CHART" --version "$OSAC_RELEASE_VERSION" \
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

  On Helm 4, every later upgrade of the `osac` release, including
  `helm upgrade --install`, must include `--server-side=true --force-conflicts`.
  This also applies when upgrading a release installed with Helm 3: Helm 4's
  default `--server-side=auto` follows the previous release's client-side apply
  method, while `--force-conflicts` requires server-side apply. The AAP operator
  takes field ownership of `app.kubernetes.io/managed-by` on the `osac-aap`
  custom resource. Helm 3 does not accept `--force-conflicts`.

  Add `-f` for every other values file, such as the networking secrets file of
  [Section 4.4.3](#443-setting-the-networking-helm-values), at install and at
  every later `helm upgrade`.

**Next steps**

- Complete the networking steps after installation: with provider networking
  enabled, from
  [Section 4.4.5](#445-applying-the-fabric-manager-registration) to
  [Section 4.4.7](#447-creating-the-externalippools); with it disabled, only
  [Section 4.4.6](#446-checking-the-networkclass).

**Verification**

- Complete [Section 7](#7-verifying-the-installation).

### 4.4 Installing with provider networking

Follow this section for every deployment. It covers the values that select the
managers, the provider's Fabric Manager, and the setup after installation.

With provider networking disabled, you still set `fabricManager` (any
registration name, for example the one you plan to use) and, with VMaaS,
`k8sManager: cudn_evpn`, and you check the `NetworkClass`. OSAC reads no
registration and runs no networking job. You need no registration, no EVPN
host networking, and no provider image, settings, or secrets. The
`NetworkClass` becomes `READY` without managers.

Provider networking supports VMaaS and the CaaS endpoint lifecycle. Set
`provisioningEnabled: true` for CaaS endpoint reservation, automatic ExternalIP
attachments, and managed endpoint DNS. A BMaaS-only installation may use
`provisioningEnabled: false`; BMaaS-specific provider networking is not
supported. This value never changes after the first install, and enabling
BMaaS or CaaS after networking resources exist is not supported (Table 4.2).
Adding CaaS to an installation with provider networking disabled requires a
reinstall with it enabled.

The provider's side (building, verifying, and handing over the Fabric Manager)
is in the [Fabric Manager Guide](../networking/customer-fabric-manager-guide.md). The
provider gives you the execution environment image, the settings and secrets,
the registration manifest, and the values you agree on below.

The examples use the namespace `$NS` from
[Section 4.1](#41-preparing-the-cluster), where the OSAC operator and AAP run,
and a provider Fabric Manager named `acme_fabric`.

#### 4.4.1 Choosing the networking settings

Decide these before the first install. Most of them never change afterward
(see [Section 4.4.4](#444-settings-fixed-at-installation)).

- **Provider networking.** `global.networking.provisioningEnabled`: `true` (the
  default) or `false`.
- **Managers.** `global.networking.fabricManager` is the `name` in the Fabric
  Manager's registration. `global.networking.k8sManager` is `cudn_evpn` when
  VMaaS is enabled, or will be enabled later, and empty otherwise. `cudn_evpn` uses the `evpn` network
  type, so with provider networking enabled the Fabric Manager must list
  `evpn` in its `network_types`.
- **Excluded ranges.** `global.networking.excludedIpv4Cidrs` lists the IPv4
  CIDRs your infrastructure uses: management, provisioning, and node networks,
  and, with EVPN host networking, the VTEP range
  (`evpn.vtepCIDR`), the fabric loopback ranges, and the node uplink ranges
  ([Section 2.6](#26-evpn-host-networking-for-vmaas)). No VirtualNetwork or ExternalIPPool can overlap them.
- **Tenant defaults.** `global.networking.networkClass.defaults` gives every
  tenant the same default VirtualNetwork CIDR, Subnet CIDR, and SecurityGroup
  rules. Allow only minimal access, for example SSH and HTTPS: the default
  rules apply to every tenant.
- **Address plan.** `global.networking.networkClass.addressPlan` splits every
  Subnet into three shares: `overlay` (VM addresses), `fabric` (bare-metal
  addresses and the provider's devices), and `lbVip` (CaaS ingress addresses).
  Leave it unset; at the first install the installer derives it from the
  enabled services, as [Table 4.1](#table-41-default-address-plan) shows. The
  plan never changes after installation, so enable every service you will run
  from the first install. A derived plan also changes when you disable CaaS or
  BMaaS (Table 4.2).

<a id="table-41-default-address-plan"></a>
**Table 4.1. Default address plan (`overlay` / `fabric` / `lbVip`)**

| Enabled services | With `k8sManager` | Without `k8sManager` |
|---|---|---|
| VMaaS, BMaaS, CaaS | 0.5 / 0.25 / 0.25 | Not allowed |
| VMaaS, BMaaS | 0.5 / 0.5 / 0 | Not allowed |
| VMaaS | 0.75 / 0.25 / 0 | Not allowed |
| BMaaS, CaaS | 0.5 / 0.25 / 0.25 | 0 / 0.75 / 0.25 |
| BMaaS | 0.5 / 0.5 / 0 | 0 / 1 / 0 |
| None of them (for example MaaS only) | 0.75 / 0.25 / 0 | 0 / 1 / 0 |

MaaS adds nothing to the plan. If you set `addressPlan`, the installer
validates it, and a rollout that breaks a rule fails with a message that names
the field:

- Set all three shares. Each is a multiple of 0.0625 from 0 to 1, and they sum
  to exactly 1.
- `overlay` is greater than 0 exactly when `k8sManager` is set. VMaaS needs
  `k8sManager`.
- BMaaS or CaaS needs `fabric` greater than 0. CaaS needs `lbVip` greater than
  0. CaaS workers are bare-metal instances, so also enable BMaaS. The
  installer does not check this in this release.

The plan sets the longest prefix a VirtualNetwork or Subnet can have, so that
every range holds at least 16 addresses: /26 for 0.5 / 0.25 / 0.25,
0 / 0.75 / 0.25, and 0.75 / 0.25 / 0; /27 for 0.5 / 0.5 / 0; /28 for one range,
such as 0 / 1 / 0; and /24 for plans in sixteenths. The default Subnet CIDR
must meet this limit.

#### 4.4.2 Prerequisites for provider networking

Complete these before the first install with provider networking enabled.

1. **Agree on the shared values with the provider.** For every deployment:
   the ExternalIPPool CIDRs. List your infrastructure ranges in
   `excludedIpv4Cidrs`. With `cudn_evpn`, also
   agree on the EVPN values of
   [Section 2.6](#26-evpn-host-networking-for-vmaas) (VTEP range, node VTEP
   addresses, node ASN, uplink addressing, fabric peers and their ASNs, fabric
   loopback ranges) and on the VNI range kept for OSAC (none for Netris: Netris
   allocates the VNIs).

   The node ASN (`evpn.bgp.asn`, and any `evpn.nodes[].asn`), the VTEP range
   and the VNI range are fixed after the first install; changing them means
   reinstalling. OSAC keeps no copy of them and cannot detect a change, so
   compare them with the provider's values before the first install. The node
   ASN must match what the fabric admin configured for the nodes' BGP
   neighbors; have your administrators verify that every BGP session becomes
   `Established` before continuing. OSAC does not check the fabric's
   route targets: `cudn_evpn` sets `0:<VNI>` on every CUDN, and if the fabric
   does not import and export exactly these, routed traffic fails while every
   task succeeds (same-Subnet traffic can still work, as on the Netris lab,
   where the leaves use `<ASN>:<VNI>`).

2. **Confirm EVPN host networking is ready** (VMaaS only). Your platform
   administrators prepare it as described in
   [Section 2.6](#26-evpn-host-networking-for-vmaas). While it is missing,
   `cudn_evpn` fails its Subnet task and retries it. You set no gateway MAC
   address: OVN-Kubernetes serves the VMs' gateway on every node and routes
   into the IP-VRF.

3. **Confirm that the provider prepared the fabric.** With `cudn_evpn`, the
   provider configures the fabric side of
   [Section 2.6](#26-evpn-host-networking-for-vmaas) for every VMaaS node,
   including the route targets `0:<VNI>`. The fabric carries the MAC-VRF and
   the IP-VRF to the nodes and routes the IP-VRF. The underlay and the node
   NICs carry frames of at least 1550 bytes. The upstream path brings the
   ExternalIPPool CIDRs to the fabric edge.

4. **Make the image pullable.** The hub pulls the provider's execution
   environment image for every job. If its registry needs credentials, add
   them to the cluster pull secret (see
   [Section 2.1](#21-cluster-and-access)).

#### 4.4.3 Setting the networking Helm values

Add the networking values to `my-values.yaml`. With provider networking
enabled, also add the image and the Fabric Manager's settings:

```yaml
global:
  networking:
    provisioningEnabled: true                 # fixed at the first install
    excludedIpv4Cidrs: [192.168.48.0/21]      # includes the EVPN ranges
    fabricManager: acme_fabric                # the registration name
    k8sManager: cudn_evpn                     # when VMaaS is or will be enabled; empty otherwise
    networkClass:
      defaults:                               # tenant defaults
        virtualNetworkIPv4CIDR: 10.0.0.0/16
        subnetIPv4CIDR: 10.0.1.0/24
        ingressRules:                         # default SecurityGroup
          - {protocol: PROTOCOL_TCP, ipv4Cidr: 0.0.0.0/0, portFrom: 22, portTo: 22}
        egressRules:
          - {protocol: PROTOCOL_ALL, ipv4Cidr: 0.0.0.0/0}
      # addressPlan: leave unset to derive it (Table 4.1)
aap:
  configAsCode:
    eeImage: registry.acme.example/osac/osac-aap-acme:<base_tag>-1   # from the provider
  instanceGroups:
    networkFulfillment:
      enabled: true                           # the default
      config:                                 # ConfigMap network-fulfillment-ig
        ACME_FABRIC_API_URL: https://fabric-api.acme.example
        ACME_FABRIC_VNI_RANGE: "10000-29999"  # a shared value
        ACME_FABRIC_CA_BUNDLE: |              # a multi-line value
          -----BEGIN CERTIFICATE-----
          MIIB...
          -----END CERTIFICATE-----
```

- **Rules.** Each default rule is a SecurityGroup rule in camelCase:
  `protocol` is `PROTOCOL_TCP`, `PROTOCOL_UDP`, `PROTOCOL_ICMP`, or
  `PROTOCOL_ALL`; `ipv4Cidr` is required; `portFrom` and `portTo` are set only
  for TCP and UDP. With `cudn_evpn` and provider networking enabled, do not
  use `PROTOCOL_ICMP`: `cudn_evpn` does not declare `security_group_icmp`, so
  the chart rejects it with
  "PROTOCOL_ICMP needs the security_group_icmp capability, which cudn_evpn
  does not declare; use PROTOCOL_ALL". `PROTOCOL_ALL` includes ICMP. There is
  no Helm value for this capability: OSAC derives it from the managers'
  registrations. The default CIDRs are canonical, the
  Subnet CIDR is inside the VirtualNetwork CIDR, and the VirtualNetwork CIDR
  does not overlap `excludedIpv4Cidrs`.
- **Image.** The provider builds the image on the `osac-aap` image of the OSAC
  release (the chart value `aap.bootstrap.image`) and rebuilds it for every
  release. AAP uses it for every OSAC instance group.
- **Settings and secrets.** Networking jobs run in the AAP instance group
  `<prefix>-networking-operations-ig` (prefix `osac` by default). With
  chart-managed AAP, job pods read every key of the `ConfigMap` and `Secret`
  `network-fulfillment-ig` in `$NS` as environment variables. The chart writes
  both from `aap.instanceGroups.networkFulfillment.config` and `.secret` at
  every install and rollout, so do not edit these chart-managed resources with
  `oc`. The key names come from the Fabric Manager's design and start with its
  registration name in upper case. Job pods read the values when they start,
  so a change applies to the next job.

  With external AAP, job pods can read these resources only when the ConfigMap
  and Secret are in the same cluster and namespace as the pods. Objects in the
  OSAC release namespace are not available to jobs running in another cluster
  or namespace. Create matching `network-fulfillment-ig` resources in the
  cluster and namespace where the external networking container group's job
  pods run, using the same keys and values from
  `aap.instanceGroups.networkFulfillment.config` and `.secret`. Configure that
  group's `pod_spec_override` to expose them as environment variables:

  ```yaml
  spec:
    containers:
      - name: worker
        envFrom:
          - configMapRef:
              name: network-fulfillment-ig
              optional: true
          - secretRef:
              name: network-fulfillment-ig
  ```

  For a Netris controller certificate from a private CA, also mount the CA
  bundle in this group: the Netris tasks run in `networking-operations` jobs,
  not in `cluster-fulfillment`. Create a ConfigMap with the CA under the
  `bundle.pem` key in the external job namespace and add these entries to the
  same `pod_spec_override`; the path in `NETRIS_CA_PATH` must match the
  mounted file:

  ```yaml
  spec:
    volumes:
      - name: netris-ca-bundle
        configMap:
          name: <NETRIS_CA_CONFIGMAP>
          items:
            - key: bundle.pem
              path: bundle.pem
    containers:
      - name: worker
        env:
          - name: NETRIS_CA_PATH
            value: /etc/netris/ca/bundle.pem
        volumeMounts:
          - name: netris-ca-bundle
            mountPath: /etc/netris/ca/bundle.pem
            subPath: bundle.pem
            readOnly: true
  ```

  With `k8sManager: cudn_evpn`, the networking tasks manage Kubernetes
  networking resources on the hub cluster, and their Kubernetes modules use
  the job pod's default authentication without an explicit API endpoint: the
  tasks target the cluster that `K8S_AUTH_KUBECONFIG` or the pod's
  service-account token points to. When the external group's job pods run
  outside the hub cluster, that default is the execution cluster, so the
  tasks would manage the wrong cluster. Provide an authorized hub kubeconfig
  the same way as the external `cluster-fulfillment` group
  ([Section 6.2](#62-installing-osac-for-caas)): create the
  `osac-hub-kubeconfig` Secret in the external job namespace and add the
  kubeconfig volume, mount, and `K8S_AUTH_KUBECONFIG` variable to this
  `pod_spec_override`, merging them with the CA entries above.

  For example, generate a non-secret env file from the `config` values and a
  protected env file from `.secret` using your secret manager, then apply them
  to the external job cluster:

  ```console
  AAP_CONTEXT="<external-aap-job-cluster-context>"
  AAP_NAMESPACE="<external-aap-job-namespace>"
  oc --context "$AAP_CONTEXT" create configmap network-fulfillment-ig \
    --namespace "$AAP_NAMESPACE" --from-env-file=network-fulfillment-ig.env \
    --dry-run=client -o yaml |
    oc --context "$AAP_CONTEXT" apply -f -
  oc --context "$AAP_CONTEXT" create secret generic network-fulfillment-ig \
    --namespace "$AAP_NAMESPACE" --from-env-file=network-fulfillment-ig.secret \
    --dry-run=client -o yaml |
    oc --context "$AAP_CONTEXT" apply -f -
  ```

  Reapply the ConfigMap and Secret from their current sources whenever these
  settings or secrets change; new job pods then read the updated values. Keep
  the secret file out of version control. For multi-line settings or secrets,
  use the corresponding resource's `--from-file=<KEY>=<path>` input instead of
  an env file.

  **Netris CaaS CA bundle (`cluster-fulfillment`).** For chart-managed AAP,
  `NETRIS_CA_CONFIGMAP` in
  `aap.instanceGroups.networkFulfillment.config` tells config-as-code which
  ConfigMap contains the Netris CA under the `bundle.pem` key. With external
  AAP, the bootstrap does not create a mount for the external
  `cluster-fulfillment` jobs. Ensure a ConfigMap with that same name and key is
  present in the cluster and namespace where those jobs run. Creating the
  ConfigMap or setting `NETRIS_CA_CONFIGMAP` alone does not expose its
  certificate to a job. Add the following volume, volume-mount, and environment
  entries to the external group's `pod_spec_override`; the path in
  `NETRIS_CA_PATH` must match the mounted file:

  ```yaml
  spec:
    volumes:
      - name: netris-ca-bundle
        configMap:
          name: <NETRIS_CA_CONFIGMAP>
          items:
            - key: bundle.pem
              path: bundle.pem
    containers:
      - name: worker
        env:
          - name: NETRIS_CA_PATH
            value: /etc/netris/ca/bundle.pem
        volumeMounts:
          - name: netris-ca-bundle
            mountPath: /etc/netris/ca/bundle.pem
            subPath: bundle.pem
            readOnly: true
  ```

  Keep the ConfigMap synchronized with the source CA bundle; newly launched
  jobs then mount the current certificate. The chart-managed mount is shown in
  the [CaaS cluster-fulfillment template](../../../osac-aap/collections/ansible_collections/osac/config_as_code/roles/aap/templates/cluster-fulfillment-ig.j2).

Put the secrets in a separate values file that is excluded from version
control, for example `my-secrets.local.yaml`, generated from your secret
store:

```yaml
aap:
  instanceGroups:
    networkFulfillment:
      secret:                                 # Secret network-fulfillment-ig
        ACME_FABRIC_API_TOKEN: "<token_from_your_secret_store>"
```

Pass this file at install and at every later `helm upgrade`. Every role in the
instance group can read these values, and networking jobs run as OSAC's job
service account, which has `cluster-admin`, so install only roles you trust.

**Netris settings.** With `fabricManager: netris`, the Netris role ships in the
`osac-aap` image of the release, so you need no provider image. Put these keys
in `config` and `secret`. Every value is a quoted string, for example
`NETRIS_SITE_ID: "5"`. A missing or malformed setting fails the task as
retryable with "Missing or malformed setting in network-fulfillment-ig".

| Key | Where | Needed for |
|---|---|---|
| `NETRIS_CONTROLLER_URL` | `config` | Every task. The Netris controller URL; the chart requires `https://`. |
| `NETRIS_USERNAME` | `config` | Every task. |
| `NETRIS_PASSWORD` | `secret` | Every task. |
| `NETRIS_SITE_ID`, `NETRIS_TENANT_ID` | `config` | Every task. Positive integers. |
| `NETRIS_MGMT_VPC_ID` | `config` | NATGateways, ExternalIP attach and detach, and `provider_management` destinations such as the CaaS API endpoint. A positive integer. |
| `NETRIS_VALIDATE_CERTS` | `config` | Optional. `"true"` (the default) or `"false"`; the chart rejects other values. |
| `NETRIS_CA_CONFIGMAP` | `config` | Optional. Name of an AAP-namespace ConfigMap with `bundle.pem`. The networking job mounts it read-only and sets `NETRIS_CA_PATH`. |
| `NETRIS_PORT_SETTLE_SECONDS` | `config` | Optional. Default `15`. |
| `NETRIS_BM_PROVISIONING_VNET` | `config` | Required for CaaS workers with physical attachments, BMaaS, and FabricDomains: the Netris provisioning V-Net name. |
| `NETRIS_RESOURCE_CLASS_MAP` | `config` | BMaaS and FabricDomains only, so not used in this release: a non-empty JSON object that maps each resource class to its Netris server cluster template. |

When `NETRIS_CA_CONFIGMAP` is set, the networking operations job mounts its
`bundle.pem` read-only at `/etc/netris/ca/bundle.pem`. With a Netris controller
certificate from a private CA, create that ConfigMap in the AAP namespace and
set `NETRIS_CA_CONFIGMAP`. When installer validation is enabled, the ConfigMap
must be immutable and contain a non-empty, parseable `bundle.pem`; follow the
[Netris CA ConfigMap creation procedure](../../../osac-installer/docs/network-backend.md#netris-ca-configmap-lifecycle).
Certificate verification is controlled separately by `NETRIS_VALIDATE_CERTS`,
which defaults to `"true"`; the CA ConfigMap does not override an explicit
`"false"` value. For private-CA configurations that require verification, keep
`NETRIS_VALIDATE_CERTS` set to `"true"`. For every key, see
[`network-backend.md`](https://github.com/osac-project/osac/blob/main/osac-installer/docs/network-backend.md).

Keep AAP job records at least as long as networking resources exist: OSAC
uses them to track networking jobs. Set AAP's Cleanup Job Details schedule so
it keeps them that long.

#### 4.4.4 Settings fixed at installation

Set these at the first install. A later rollout that breaks a rule in
[Table 4.2](#table-42-settings-fixed-at-installation) fails with the message
shown. The `NetworkClass` and the stored settings stay unchanged, but the
release is marked failed, and the other resources of the rollout may already
be updated. Restore the earlier value and roll out again.

<a id="table-42-settings-fixed-at-installation"></a>
**Table 4.2. Settings fixed at installation**

| Setting | Rule after the first install | What enforces it |
|---|---|---|
| `global.networking.provisioningEnabled` | Never changes. To change it, reinstall. | The fulfillment service, the OSAC operator and the bare-metal fulfillment operator refuse to start: "global.networking.provisioningEnabled cannot change after installation; reinstall to change it". |
| Enabled services (`global.services.*`) | Enabling BMaaS or CaaS while any VirtualNetwork exists is not supported. Disabling CaaS or BMaaS changes a derived address plan; see the `addressPlan` row. | The fulfillment service refuses to start: "Enabling BMaaS or CaaS after networking resources exist is not supported in this release; delete every VirtualNetwork or reinstall". |
| `fabricManager`, `k8sManager`, `networkClass.defaults`, `networkClass.eastWestCapabilities` | Any difference from the `NetworkClass` fails the rollout. Set `k8sManager` at the first install if VMaaS will run. To change them, see [Section 4.4.8](#448-replacing-the-networkclass). | The installer: "NetworkClass <field>[, <field>] cannot change after installation; see Replacing the NetworkClass". |
| `networkClass.addressPlan` | Never changes. The installer compares the plan in force with the stored one at every rollout. With `addressPlan` set, that is your plan. Unset, the installer derives it again from the enabled services and `k8sManager` ([Table 4.1](#table-41-default-address-plan)), so enabling or disabling CaaS or BMaaS changes it. To change it, see [Section 4.4.8](#448-replacing-the-networkclass). To disable a service without replacing the `NetworkClass`, first set `addressPlan` to the stored plan. | The installer: "NetworkClass spec.address_plan cannot change after installation; see Replacing the NetworkClass". A plan that breaks a rule fails earlier with the rule's message, for example "global.networking.networkClass.addressPlan.lbVip must be greater than 0 when CaaS is enabled". |
| `excludedIpv4Cidrs` | You can add ranges that overlap none of these: the default `virtualNetworkIPv4CIDR`, a VirtualNetwork, or an ExternalIPPool. | The fulfillment service refuses to start: "excludedIpv4Cidrs range <CIDR> overlaps <kind> <ID or CIDR>". |
| Node ASN, VTEP range and VNI range (`evpn.bgp.asn` and `evpn.nodes[].asn`, `evpn.vtepCIDR`, the Fabric Manager's VNI setting; none for Netris) | Fixed. To change them, reinstall. You can add or remove VMaaS nodes ([Section 2.6](#26-evpn-host-networking-for-vmaas)). | Nothing: agree on both sides before the first install ([Section 4.4.2](#442-prerequisites-for-provider-networking)). |
| CaaS `EXTERNAL_ACCESS_BASE_DOMAIN`, `HOSTED_CLUSTER_BASE_DOMAIN`, `DNS_ZONE`, and `DNS_CLASS` | Do not change them while any Cluster exists. Keep each in-use HostedCluster base domain in `EXTERNAL_ACCESS_SUPPORTED_BASE_DOMAINS` until all Clusters using it are deleted. | The endpoint jobs reject unsupported publication domains; removing a published domain blocks DNS cleanup and holds its automatic endpoint IPs until the domain is supported again. |
| OSAC release | Patch releases (x.y.z) of the same minor release roll out in place. Another minor release, up or down, does not: delete all workloads and networking resources, or reinstall. | While any VirtualNetwork, ExternalIPPool, or workload exists, the fulfillment service refuses to start: "in-place upgrade or downgrade between minor releases is not supported; delete all workloads and networking resources or reinstall". |

The installer's checks run in the post-installation and post-upgrade Job
`create-network-class`, which is kept when it fails. Read a failure with
`oc logs job/create-network-class -n "$NS"`. The fulfillment service's
refusals appear in `oc logs deploy/fulfillment-grpc-server -n "$NS"`. A value
that breaks a rule of
[Section 4.4.1](#441-choosing-the-networking-settings) can also fail
`helm install` or `helm upgrade` before anything is applied: a value of the
wrong form fails the values schema, naming its path, and a rule across values
fails with a message that starts with `global.networking.`.

#### 4.4.5 Applying the Fabric Manager registration

Use this procedure with provider networking enabled, after
[Section 4.1](#41-preparing-the-cluster), before or after the installation.
Until you apply it, the `NetworkClass` is `FAILED` with the message "fabric
manager "acme_fabric" matches no ConfigMap labelled
osac.openshift.io/network-fabric-manager". Applying the registration resolves
it again. The installer registers `cudn_evpn` itself, and, with
`global.networking.fabricManager: netris`, also the Netris Fabric Manager
(`ConfigMap` `osac-network-fabric-manager-netris`: name `netris`, `ansible_role`
`osac.templates.netris`, `network_types` `evpn`, `capabilities`
`eastWestEthernet,security_group_icmp`, `immutable`). For `netris`, skip this procedure.

**Procedure**

1. Get the registration manifest from the provider. It is one `ConfigMap`,
   applied in `$NS`, the namespace of the OSAC Helm release:

   ```yaml
   apiVersion: v1
   kind: ConfigMap
   metadata:
     name: network-manager-acme-fabric      # any name
     labels:
       osac.openshift.io/network-fabric-manager: "true"
   immutable: true
   data:
     name: acme_fabric                      # the value of global.networking.fabricManager
     ansible_role: acme.network.fabric
     network_types: evpn
     capabilities: security_group_icmp
     description: Acme Fabric Manager (VXLAN EVPN fabric)
   ```

   - `immutable: true` is required. To change a registration, delete it and
     create a new one.
   - `name` uses lowercase letters, digits, and underscores, and starts with a
     letter.
   - `ansible_role` is `<namespace>.<collection>.<role>`, each part of
     lowercase letters, digits, and underscores (no hyphens). A provider's role
     is never in an `osac.*` collection; Fabric Managers that OSAC ships, such
     as Netris, use one and are registered by the installer.
   - `network_types` is comma-separated, with no spaces; only `evpn` in this
     release. It must list `evpn` when `k8sManager` is `cudn_evpn`.
   - `capabilities` is optional, comma-separated, with no spaces. The values
     are `eastWestEthernet`, needed for
     `networkClass.eastWestCapabilities.supportsEastWestEthernet`, and
     `security_group_icmp`, which the manager declares only if its
     `create_security_group` handles `icmp` rules. The NetworkClass accepts
     ICMP rules only when every manager declares it, and `cudn_evpn` does not.
     Acme declares it, as in the FM guide's example. An unknown value fails
     the `NetworkClass`.
   - An optional key (`capabilities`, `network_types`, `description`) with an
     empty value counts as absent. Any other `data` key, such as the typo
     `networkTypes`, fails the `NetworkClass`, even with an empty value.
   - Two registrations with the same `name` fail the `NetworkClass` ("matches
     more than one ConfigMap"), and so does one `ConfigMap` with both manager
     labels.

2. Apply it:

   ```console
   oc apply -n "$NS" -f acme-registration.yaml
   ```

OSAC checks only the format of `ansible_role`. If the role or a task file is
missing from the image, every task fails and is retried until the image has
it.

#### 4.4.6 Checking the `NetworkClass`

Read the `NetworkClass` through the private API of the Fulfillment Service.

**Procedure**

1. Set the API address and a short-lived token for the `admin` service
   account that the OSAC chart creates:

   ```console
   FULFILLMENT_API="https://$(oc get route fulfillment-internal-api -n "$NS" -o jsonpath='{.spec.host}')"
   OSAC_ADMIN_TOKEN="$(oc create token admin -n "$NS" --duration 1h)"
   ```

   `admin` is the emergency administrator account of the private API;
   restrict who can mint its tokens.

   For a Route certificate signed by a private CA, set `CA_FILE` to a PEM
   bundle that trusts the internal API Route. If the shared bundle configured
   by `service.certs.caBundle.configMap` contains that CA, replace
   `<ca_bundle_configmap>` with its name and extract it:

   ```console
   CA_BUNDLE_CONFIGMAP="<ca_bundle_configmap>"
   oc get configmap "$CA_BUNDLE_CONFIGMAP" -n "$NS" \
     -o jsonpath='{.data.bundle\.pem}' > route-ca-bundle.crt
   CA_FILE=route-ca-bundle.crt
   ```

   If the shared bundle does not trust the Route, use the Route CA bundle
   provided by your platform administrators. For a publicly trusted Route,
   run `unset CA_FILE` so requests use the system trust store.

2. In the same Bash session, initialize optional CA arguments from `CA_FILE`
   after you set or unset it. Changing `CA_FILE` does not update an existing
   `CURL_CA_ARGS` array. Repeat this initialization before later requests
   whenever `CA_FILE` changes. When it is unset, curl uses the system trust
   store:

   ```console
   CURL_CA_ARGS=()
   if [ -n "${CA_FILE:-}" ]; then
     CURL_CA_ARGS=(--cacert "$CA_FILE")
   fi
   ```

3. Check the `NetworkClass`:

   ```console
   curl --fail-with-body "${CURL_CA_ARGS[@]}" --header "Authorization: Bearer ${OSAC_ADMIN_TOKEN}" \
       "${FULFILLMENT_API}/api/private/v1/network_classes" | jq '.items[0].status'
   ```

**Verification**

- `state` is `NETWORK_CLASS_STATE_READY`. With provider networking enabled,
  the status also records the managers (the Fabric Manager, and with
  `cudn_evpn` also the Kubernetes Manager) and, with `cudn_evpn`, the network
  type `evpn`. With provider networking disabled, both are empty.
- If the state is `FAILED`, `message` gives the reason: for example, no
  registration has the name of `global.networking.fabricManager`, the
  registration lacks `immutable: true`, or its `network_types` lacks `evpn`.
  OSAC resolves a `PENDING` or `FAILED` `NetworkClass` again whenever a
  registration is created or deleted, so create the missing registration, or
  delete the wrong one and create it again. A wrong Helm value needs
  [Section 4.4.8](#448-replacing-the-networkclass). OSAC never resolves a
  `READY` `NetworkClass` again.

#### 4.4.7 Creating the ExternalIPPools

ExternalIPs and NATGateways, including each tenant's default NATGateway
ExternalIP `default-nat`, take their addresses from an ExternalIPPool. Create
the pools after the `NetworkClass` is `READY` and before the first Tenant:
tenant onboarding waits for a `READY` pool. Every pool is shared by all
tenants: OSAC places it in the `shared` tenant and rejects a pool that names
another tenant with `PERMISSION_DENIED`.

**Procedure**

- For each pool, run the following command with the variables, including
  `CURL_CA_ARGS`, from [Section 4.4.6](#446-checking-the-networkclass). Each
  pool has exactly one IPv4 CIDR. If you changed `CA_FILE` since that section,
  repeat its step 2 before these requests to refresh `CURL_CA_ARGS`:

  ```console
  curl --fail-with-body "${CURL_CA_ARGS[@]}" --request POST \
      "${FULFILLMENT_API}/api/private/v1/external_ip_pools" \
      --header "Authorization: Bearer ${OSAC_ADMIN_TOKEN}" --header "Content-Type: application/json" \
      --data '{"metadata": {"name": "pool-public-1"}, "spec": {"cidrs": ["198.51.100.0/24"], "ipFamily": "IP_FAMILY_IPV4"}}'
  ```

  The CIDR holds no address your infrastructure uses. It overlaps no other
  pool, no `excludedIpv4Cidrs` range, no default `virtualNetworkIPv4CIDR`, no
  VirtualNetwork, and no Cluster pod or service CIDR. Size the pools for one
  `default-nat` address per Tenant, plus one address per ExternalIP; a Tenant
  whose onboarding finds every pool full gets `DefaultNetworkingReady`
  `False`. A CaaS Cluster requesting automatic external access needs two
  available addresses in the same `READY` pool at create time, one each for the
  API and ingress endpoints. Capacity split across pools does not meet this
  requirement.

**Verification**

- The pool becomes `READY` when the Fabric Manager's pool task succeeds:

  ```console
  curl --fail-with-body "${CURL_CA_ARGS[@]}" --header "Authorization: Bearer ${OSAC_ADMIN_TOKEN}" \
      "${FULFILLMENT_API}/api/private/v1/external_ip_pools" | jq '.items[] | {id, status}'
  ```

With provider networking disabled, OSAC rejects every ExternalIP, so tenants
get no default NATGateway and no pool is needed.

#### 4.4.8 Replacing the `NetworkClass`

You need a new `NetworkClass` for a new Fabric Manager role name or network
types, new `fabricManager`, `k8sManager`, or `networkClass` values, or a new
address plan, for example to enable CaaS on a plan without `lbVip`: once the
`NetworkClass` is `READY`, every resource uses the managers it recorded.

**Procedure**

1. Delete every workload, then every networking resource that is not a tenant
   default, then every Tenant, which deletes its defaults. The reserved tenants
   `system` and `shared` have no defaults; keep them. Each delete runs the
   recorded managers' tasks, so the image must still hold the recorded roles.
2. Delete every ExternalIPPool, then the `NetworkClass`, with the variables,
   including `CURL_CA_ARGS`, from
   [Section 4.4.6](#446-checking-the-networkclass). If `CA_FILE` changed since
   that section, repeat its step 2 before these requests to refresh
   `CURL_CA_ARGS`:

   ```console
   curl --fail-with-body "${CURL_CA_ARGS[@]}" --header "Authorization: Bearer ${OSAC_ADMIN_TOKEN}" \
       --request DELETE "${FULFILLMENT_API}/api/private/v1/external_ip_pools/<pool_id>"
   curl --fail-with-body "${CURL_CA_ARGS[@]}" --header "Authorization: Bearer ${OSAC_ADMIN_TOKEN}" \
       --request DELETE "${FULFILLMENT_API}/api/private/v1/network_classes/<network_class_id>"
   ```
3. Roll out the new Helm values. The installer creates the new
   `NetworkClass`.
4. Once the `NetworkClass` is `READY`, create the ExternalIPPools again
   ([Section 4.4.7](#447-creating-the-externalippools)), then the Tenants.

---

## 5. Helm chart configuration parameters

This section lists the parameters you are most likely to set. To retrieve the
complete set, run the following command, and review the `osac` chart
`values.schema.json` file:

```console
helm show values "$OSAC_CHART" --version "$OSAC_RELEASE_VERSION"
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
| `operator.aap.url`, `operator.aap.insecureSkipVerify`, `operator.aap.statusPollInterval`, `operator.aap.templatePrefix` | schema | How the Operator reaches AAP. An empty URL defaults to `http://osac-aap/api/controller`; set a trusted HTTPS Controller Route for production. The chart defaults `insecureSkipVerify` to `"true"`; explicitly set it to `"false"` for production and use an AAP certificate trusted by the Operator's system trust store. `"true"` disables certificate verification and is for temporary evaluation only. The Fulfillment CA bundle does not configure AAP trust. |
| `operator.aap.token` | schema | Direct AAP token value. If `operator.aap.tokenSecret.name` is set, the Deployment uses that Secret and ignores this value; clear the Secret name when switching to a direct token. |
| `operator.aap.tokenSecret.name` | schema | Secret containing the AAP API token. Set to `osac-aap-api-token` when using the `create-api-token` hook or when supplying a token for external AAP. |
| `operator.aap.tokenSecret.key` | schema | Key in the referenced Secret containing the AAP token. The hook-created Secret uses `token`. |
| `operator.fulfillment.serverAddress` | schema | Fulfillment gRPC endpoint. When configured, the Operator uses it to verify the saved installation networking setting before reconciliation; it can be empty for standalone runs. |
| `operator.fulfillment.tls.caBundle.configMap` | schema | ConfigMap with the PEM bundle used by the Operator's Fulfillment TLS client. Keep this name aligned with `service.certs.caBundle.configMap` when using a custom bundle name. The default is `ca-bundle`; the key defaults to `bundle.pem`. |
| `operator.fulfillment.credentials.secretName`, `clientIdKey`, `clientSecretKey` | schema | Name and keys of the Kubernetes Secret holding the `osac-controller` Keycloak service-account credentials. Provide this Secret before installation; the OSAC chart only references it. |
| `operator.controllers.tenant`, `operator.controllers.networking`, `operator.controllers.storage` | schema | Enable or disable individual reconcilers. Which services run is driven by `global.services.*`. |
| `operator.controllers.volume` | subchart | Enables the Volume reconciler. |
| `operator.configSecret.name`, `operator.configSecret.optional` | schema | Additional Operator configuration Secret. |
| `operator.hubAccess.enabled` | schema | Creates only the hub-access `ClusterRole` that the Fulfillment Service binds to. It is separate from the top-level `hubAccess.enabled`, which creates local hub access resources and registers the local hub. |
| `operator.stall.preparingInfrastructureThreshold`, `operator.stall.controlPlaneStartingThreshold`, `operator.stall.workersJoiningThreshold` | schema | How long a cluster provisioning phase can wait before the Operator marks it stalled. |
| `operator.tenants[]` | subchart | Tenants pre-created at installation. `shared` is the built-in tenant. |
| `operator.networkManagers.enabled` | subchart | Controls legacy operator-subchart registrations. Set `false` in the Netris/CUDN EVPN examples to suppress those registrations. The umbrella chart automatically creates the selected `netris` and `cudn_evpn` registrations from `global.networking`; do not register those managers again here. |
| `operator.remoteClusterKubeconfig`, `operator.remoteClusterKubeconfigSecretName`, `operator.remoteClusterKubeconfigSecretKey` | schema | Kubeconfig mount for VMaaS on a separate workload cluster. Set the path and Secret name together. The key defaults to `kubeconfig`. See [Section 6.4](#64-installing-vmaas-on-a-separate-workload-cluster). |

<a id="table-53-networking-globalnetworking"></a>
**Table 5.3. Networking (`global.networking.*`)**

The installer creates the one `NetworkClass` from these values. Fabric Manager
registrations of provider Fabric Managers are `ConfigMap` resources that you
apply, not Helm values; the installer registers `cudn_evpn` itself, and the
Netris Fabric Manager when `fabricManager` is `netris`. EVPN host networking is
prepared by your platform administrators
([Section 2.6](#26-evpn-host-networking-for-vmaas)). See
[Section 4.4](#44-installing-with-provider-networking).

For chart `0.4.0`, configure `global.networking` directly. The chart creates
the deployment NetworkClass through the Fulfillment API. Manager selection,
NetworkClass defaults, capabilities, and address plan are fixed at installation;
an upgrade rejects changes to the existing NetworkClass spec. The provider
provisioning switch is also fixed at installation.

Normal deployments configure `global.networking`; Helm derives the AAP
backend, manager registrations, and NetworkClass from it. Chart `0.4.0`
configures CUDN EVPN through `global.networking`, as shown in
[Section 6.1](#cudn-evpn-with-netris). The legacy top-level `networkClass` and
`global.expertOverrides` paths are not part of the `0.4.0` chart schema.

| Parameter | Source | Description | Default |
|---|---|---|---|
| `global.networking.provisioningEnabled` | schema | Provider networking. When `false`, OSAC runs no networking job, and networking resources become `READY` without connectivity. Fixed at the first install. | `true` |
| `global.networking.excludedIpv4Cidrs` | schema | IPv4 CIDRs that your infrastructure uses: management, provisioning, and node networks and, with EVPN host networking, the EVPN ranges. No VirtualNetwork or ExternalIPPool can overlap them. You can add ranges later (see [Table 4.2](#table-42-settings-fixed-at-installation)). | `[]` |
| `global.networking.fabricManager` | schema | The registration `name` of the Fabric Manager. Required for every deployment. | Not set |
| `global.networking.k8sManager` | schema | The OSAC Kubernetes Manager: `cudn_evpn`, or empty. Required for VMaaS. | Not set |
| `global.networking.caBundle.adminGroups` | schema | Groups allowed to create, update, or delete the customer-managed Netris CA ConfigMap used by CaaS `cluster-fulfillment` jobs. Its name comes from `aap.instanceGroups.networkFulfillment.config.NETRIS_CA_CONFIGMAP`; this protects the ConfigMap but does not mount it into networking jobs. Use `eeImage` and `NETRIS_CA_PATH` for private-CA networking jobs (see [Section 4.4.3](#443-setting-the-networking-helm-values)). Add identity-provider groups bound directly to `cluster-admin`; they are not included in the defaults. | `["system:masters", "system:cluster-admins"]` |
| `global.networking.networkClass.defaults.virtualNetworkIPv4CIDR`, `global.networking.networkClass.defaults.subnetIPv4CIDR` | schema | The CIDRs of every tenant's default VirtualNetwork and Subnet. The Subnet CIDR is inside the VirtualNetwork CIDR and meets the prefix limit of the address plan. The chart defaults suit evaluation, except with Netris, whose reserved `10.0.0.0/8` contains them ([Section 2.6](#26-evpn-host-networking-for-vmaas)); set them for your deployment, because they cannot change after installation. | `10.200.0.0/16` and `10.200.0.0/20` |
| `global.networking.networkClass.defaults.ingressRules`, `global.networking.networkClass.defaults.egressRules` | schema | Rules of every tenant's default SecurityGroup, as camelCase SecurityGroup rules, for example `{protocol: PROTOCOL_TCP, ipv4Cidr: 0.0.0.0/0, portFrom: 22, portTo: 22}`. Set them for your deployment; they cannot change after installation. | `[]` and one allow-all egress rule |
| `global.networking.networkClass.eastWestCapabilities.supportsEastWestEthernet` | schema | Enables Ethernet east-west FabricDomains. Needs BMaaS and a Fabric Manager that declares `eastWestEthernet`. In this release BMaaS runs with provider networking disabled, so a FabricDomain becomes `READY` (reason `ProvisioningDisabled`) without any fabric configuration. | `false` |
| `global.networking.networkClass.addressPlan.overlay`, `global.networking.networkClass.addressPlan.fabric`, `global.networking.networkClass.addressPlan.lbVip` | schema | Optional shares of every Subnet. Set all three or none. Fixed at installation (Table 4.2). See [Section 4.4.1](#441-choosing-the-networking-settings). | Derived from the enabled services ([Table 4.1](#table-41-default-address-plan)) |

When migrating an older values file, remove `k8s_only`, the top-level
`networkClass` and `global.expertOverrides` blocks, and
`global.networking.networkClass.title` / `description`. The `0.4.0` schema
does not accept these configuration paths. There is no default-class flag to
set. Move Netris connection settings from `global.networking.netris` to the
AAP instance groups shown in [Section 6](#6-installation-workflows-by-service).
Remove `metering.enabled` as well; metering is always deployed. Set nonempty
`service.externalHostname` and `service.internalHostname` when rendering with
Helm directly; the installation command in Section 4.3 supplies both.

**Table 5.4. Fulfillment Service (`service.*`, all `schema`)**

| Parameter | Description |
|---|---|
| `service.externalHostname`, `service.internalHostname` | Public and internal API route host names. Set at installation. |
| `service.variant` | `openshift` or `kind`. Use `openshift`. |
| `service.images.service`, `service.images.envoy` | Fulfillment Service and Envoy sidecar images. |
| `service.certs.issuerRef.kind`, `service.certs.issuerRef.name` | cert-manager issuer for the service certificates. |
| `service.certs.caBundle.configMap` | Name of the existing CA-bundle `ConfigMap` in the OSAC release namespace. OSAC components mount it, so it must contain the PEM bundle under the fixed `bundle.pem` key. This value does not create or rename the ConfigMap; the default name is `ca-bundle`. When using a custom name, set `operator.fulfillment.tls.caBundle.configMap`, `ui.api.fulfillment.certs.caBundle.configMap`, `ui.auth.certs.caBundle.configMap`, and `metering.certs.caBundle.configMap` to the same name. Include the Kafka broker CA and every other CA required by consumers of the bundle, including OSAC service connections and the OIDC/Keycloak endpoint. If BMaaS is enabled, also set `bmf.secretAPI.caBundle.configMap` to that name. Ask your platform administrators to make the bundle available under this name. See step 4 of [Section 7](#7-verifying-the-installation). |
| `service.auth.issuerUrl` | OIDC issuer. Templated from `global.clusterDomain` by default. |
| `service.auth.controllerCredentials[]` | Volume sources for the controller OIDC client credentials. |
| `service.idp.provider`, `service.idp.url`, `service.idp.credentials[]` | Identity provider for user and role management. Only `keycloak` is supported. |
| `service.database.connection[]` | Volume sources for the database URL and client certificate, for example `osac-db-config` and `osac-db-client-cert`. |
| `service.log.level`, `service.log.headers`, `service.log.bodies` | Log verbosity. Keep `headers` and `bodies` set to `false` in production. |
| `service.vault.endpoint` | Required Vault-compatible API URL. The default targets the bundled OpenBao service; in production, set it to a reachable Vault or OpenBao endpoint. See the [secrets management configuration guide](secrets-management-configuration.md). |
| `service.vault.namespace`, `service.vault.kvMountPath`, `service.vault.lifecycleRole`, `service.vault.lifecycleMountPath`, `service.vault.keycloakClientId`, `service.vault.keycloakIssuerUrl`, `service.vault.keycloakAudience`, `service.vault.caBundle`, `service.vault.credentials` | Vault namespace, mount paths, JWT role, Keycloak client and audience, CA bundle, and client credentials. Configure these for an external store as described in the [secrets management configuration guide](secrets-management-configuration.md). |

**Table 5.5. AAP (`aap.*`, all `schema`)**

| Parameter | Description |
|---|---|
| `aap.aap.instance.enabled`, `aap.aap.instance.name` | Create the AAP instance custom resource. Set `enabled: false` when AAP is managed externally. |
| `aap.aap.instance.controller.disabled`, `aap.aap.instance.hub.disabled`, `aap.aap.instance.lightspeed.disabled` | Turn off individual AAP components. |
| `aap.aap.instance.redisMode` | `standalone` or `cluster`. |
| `aap.aap.instance.routeTlsTerminationMechanism` | `Edge`, `Passthrough`, or `Reencrypt` for the AAP routes. |
| `aap.bootstrap.enabled`, `aap.bootstrap.image`, `aap.bootstrap.backoffLimit` | The postinstallation bootstrap job that loads config-as-code. Disable the job when AAP is externally managed and bootstrap it separately. `aap.bootstrap.image` is also the base image that a provider builds its execution environment image on. |
| `aap.apiToken.create` | Creates an API token for Operator provisioning after chart-managed AAP is ready. Set to `false` for externally managed AAP and supply the token Secret yourself. |
| `aap.configAsCode.eeImage` | The execution environment and job pod image of every OSAC instance group. With a provider Fabric Manager, set the provider's image, built on the release's `aap.bootstrap.image`. See [Section 4.4.3](#443-setting-the-networking-helm-values). |
| `aap.configAsCode.manifestSecret` | Secret that holds `license.zip`. |
| `aap.configAsCode.secret` | Secret with config-as-code runtime flags. |
| `aap.configAsCode.projectGitUri`, `aap.configAsCode.projectGitBranch` | Git source for the Ansible content that the bootstrap job imports. |
| `aap.configAsCode.remoteClusterKubeconfigSecretName`, `aap.configAsCode.remoteClusterKubeconfigSecretKey` | Secret name and key that AAP config-as-code uses for VM operations on a separate workload cluster. The key defaults to `kubeconfig`. See [Section 6.4](#64-installing-vmaas-on-a-separate-workload-cluster). |
| `aap.configAsCode.importAgentsEnabled`, `aap.configAsCode.importBcmAgentsEnabled` | Enable bare-metal agent import and the BCM inventory backend. |
| `aap.instanceGroups.clusterFulfillment.enabled`, `aap.instanceGroups.clusterFulfillment.config`, `aap.instanceGroups.clusterFulfillment.secret` | The `cluster-fulfillment` instance group for CaaS provisioning. See [Section 6.2](#62-installing-osac-for-caas). |
| `aap.instanceGroups.networkFulfillment.enabled`, `aap.instanceGroups.networkFulfillment.config`, `aap.instanceGroups.networkFulfillment.secret` | The Fabric Manager settings (`config`) and secrets (`secret`) exposed to networking jobs as environment variables. With chart-managed AAP, the chart writes the `network-fulfillment-ig` ConfigMap and Secret at every rollout. With external AAP, maintain copies in the external job cluster as described in [Section 4.4.3](#443-setting-the-networking-helm-values). Leave `enabled: true` (the default); `false` renders empty local resources and is rejected with provider networking enabled. |
| `aap.instanceGroups.storageFulfillment.config.STORAGE_SNAPSHOTS_ENABLED` | Snapshot toggle for the `storage-operations` instance group. |
| `aap.instanceGroups.publishTemplates.enabled` | Runs the postinstallation `osac-publish-templates` hook, which publishes cluster templates (CaaS, MaaS), compute instance templates (VMaaS), bare-metal instance templates, and add-on operators (CaaS) to the fulfillment catalog. Default `true`. Set to `false` when AAP is external; publish required templates to Fulfillment separately. For chart-managed AAP, set it to `false` only when no enabled service depends on hook-published templates or when all required templates are provisioned separately. |
| `aap.instanceGroups.publishTemplates.config.OSAC_TEMPLATE_COLLECTIONS`, `aap.instanceGroups.publishTemplates.config.OSAC_FULFILLMENT_SERVICE_URI` | The Ansible collections to publish and the internal Fulfillment Service URI. |

**Table 5.6. Web console, BMaaS, and other parameters**

| Parameter | Source | Description | Default |
|---|---|---|---|
| `ui.enabled` | schema | Deploys the OSAC web console. | `true` |
| `ui.externalHostname` | schema | Route host name. Auto-assigned from `global.clusterDomain` if empty. | `""` |
| `ui.api.fulfillment.url` | schema | Fulfillment API that the web console calls. | Internal URL |
| `ui.api.fulfillment.certs.caBundle.configMap`, `ui.auth.certs.caBundle.configMap` | schema | CA-bundle ConfigMaps for the web console's Fulfillment API and authentication TLS clients. Set both to match `service.certs.caBundle.configMap` when using a custom name. | `ca-bundle` |
| `ui.auth.oidcClientId` | schema | OIDC client ID for the web console. | `osac-ui` |
| `ui.auth.oidcIssuerURL` | schema | Required when the web console is enabled. Set explicitly to the fully resolved `service.auth.issuerUrl`; templates are not expanded. | `""` |
| `ui.images.ui` | schema | Web console image. | Release tag |
| `bmf.metal3.enabled`, `bmf.metal3.namespace`, `bmf.metal3.hostClass` | schema | Use the Metal3 backend. Set `namespace` to where your `BareMetalHost` resources live. Requires BareMetalOperator and a `Provisioning` custom resource with `spec.watchAllNamespaces: true`. | `false` |
| `bmf.env.aapInsecureSkipVerify` | schema | Skip TLS verification for BMF's AAP API calls. Set to `"false"` for production and use an AAP certificate trusted by BMF's system trust store; `"true"` is for temporary evaluation only. The Fulfillment CA bundle does not configure AAP trust. | `"true"` |
| `bmf.env.aapUrl` | subchart | AAP controller URL that the Bare Metal Fulfillment Operator drives. Set it to the external Controller URL when BMaaS uses external AAP; it does not inherit `operator.aap.url`. | Not applicable |
| `bmf.secretAPI.url`, `bmf.secretAPI.issuerUrl`, `bmf.secretAPI.caBundle.*` | schema | When `bmf.netbox.enabled` is false and `bmf.secretAPI.url` is empty, BMF runs in standalone mode without Fulfillment verification. NetBox requires `bmf.secretAPI.url`. When the URL is set, BMF uses the Fulfillment private API, Keycloak issuer, and optional CA bundle to verify the saved installation networking setting, including when NetBox is disabled. If Keycloak is external, create the `fulfillment-controller-credentials` Secret with `client-id` and `client-secret` keys in the OSAC namespace before starting BMF. | Internal Fulfillment API URL; Keycloak `osac` realm issuer URL; `ca-bundle` ConfigMap (`bundle.pem`) |
| `bmf.bcm.enabled`, `bmf.bcm.url`, `bmf.bcm.cert`, `bmf.bcm.key`, `bmf.bcm.caCert`, `bmf.bcm.insecureSkipVerify`, `bmf.bcm.hostClass`, `bmf.bcm.bmhNamespace` | subchart | Base Command Manager (BCM) backend. | `false` |
| `bmf.secrets.inventoryConfig`, `bmf.secrets.managementConfig`, `bmf.secrets.osClouds`, `bmf.configMaps.profiles` | schema | Names of the inventory, management, and `clouds.yaml` Secrets and the profiles `ConfigMap`. | Default names |
| `operatorCrds.install` | schema | Install the OSAC CRDs. Set it to `false` if a cluster administrator manages them. | `true` |
| `kafka.enabled` | schema | Creates the Fulfillment Service's Strimzi user and copies its credentials. Set to `false` for an external Kafka connection; this does not disable Kafka use or metering's Strimzi resources. See [Kafka configuration](kafka-configuration.md). | `true` |
| `service.kafka.connection` | schema | ConfigMap/Secret mappings for the required `brokers`, `user`, and `password` parameters. Supply resources in the OSAC namespace. | `fulfillment-service-kafka` Secret |
| `metering.database.connection` | schema | Metering service database connection. The metering service is always deployed: billing is not optional, and the event publisher writes usage events unconditionally. Requires Strimzi-managed Kafka, a database connection, and `global.osacDeploymentId` (see [Table 5.1](#table-51-service-enablement-my-valuesyaml) and [Kafka configuration](kafka-configuration.md#metering)). | Secret-based default |
| `metering.heartbeat.interval`, `metering.m360Adapter.enabled`, `metering.m360Adapter.m360.apiUrl`, `metering.m360Adapter.m360.apiKeySecret` | subchart | Heartbeat round interval and the Monetize360 billing adapter. | Not applicable |
| `metering.readers[]` | schema | Grants each named external reader read/describe access to every tenant's `osac.metering.heartbeat.` topics and read access to its same-named Kafka consumer group. Add or revoke readers through Helm values. | `[]` |
| `validation.enabled` | schema | Runs the install preflight. When `false`, every check is skipped and recorded as skipped in the report. | `true` |
| `preflight.mode` | schema | `enforce` stops the installation on a blocking problem. `report` records problems without stopping. `strict` also stops on warnings. See [Section 11.1](#111-the-install-preflight-stops-the-installation). | `enforce` |
| `preflight.skip.<check_id>` | schema | Skips one preflight check. The value is the reason, which the report records. | `{}` |
| `hubAccess.enabled` | schema | Creates hub-access resources and registers the local cluster as its own hub. Set to `true` before installation when the Fulfillment Service and hub run on the same cluster; see [Section 8.3](#83-registering-the-hub). | `false` |
| `dbInit.host` | schema | Host that the `db-init` pre-installation hook connects to, to create the databases. Set it to your external PostgreSQL host for a production deployment. | Development default; override for production |
| `clusterVersions.enabled`, `clusterVersions.versions[]` | schema | OpenShift Container Platform release images offered to hosted clusters. Each entry has `version`, `image`, and an optional `default`. A default entry also requires `diskImage.name` and `diskImage.sourceRef`, an OCI operating-system disk artifact. | `false` and `[]` |

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
the cluster. Every workflow also needs the networking values and steps of
[Section 4.4](#44-installing-with-provider-networking). Then follow
[Section 4](#4-installing-osac).

The examples use chart-managed AAP, where template publication is enabled by
default. For external AAP, keep
`aap.instanceGroups.publishTemplates.enabled: false` from
[Section 4.2](#42-configuring-the-helm-values) and do not add a service-level
override; publish the required templates to Fulfillment separately.

### 6.1 Installing OSAC for VMaaS

**Prerequisites**

- OpenShift Virtualization is installed.
- LVM Storage is installed, or another dynamic storage class exists.
- With provider networking enabled, you have the provider's Fabric Manager,
  with `evpn` in the `network_types` of its registration.
- With provider networking enabled, you completed
  [Section 4.4.2](#442-prerequisites-for-provider-networking), and EVPN host
  networking is configured
  ([Section 2.6](#26-evpn-host-networking-for-vmaas)).

**Procedure**

1. In `my-values.yaml`, enable the VMaaS tier and select its Fabric Manager
   and CUDN EVPN Kubernetes Manager. Chart `0.4.0` requires a nonempty
   Fabric Manager and `k8sManager: cudn_evpn`; the older fabric-less
   `k8s_only` example is not accepted.

   ```yaml
   global:
     services: { vmaas: { enabled: true }, caas: { enabled: false }, bmaas: { enabled: false }, maas: { enabled: false } }
     networking:
       provisioningEnabled: true
       fabricManager: acme_fabric
       k8sManager: cudn_evpn
   lvms:
     enabled: false  # production storage registration is administrator-managed
   aap:
     instanceGroups:
       networkFulfillment:
         enabled: true
         config: {}  # add the selected managers' settings per their owner docs
   ```

   Add the rest of the networking values from
   [Section 4.4.3](#443-setting-the-networking-helm-values). With only VMaaS
   enabled, the default address plan is 0.75 / 0.25 / 0, so the default Subnet
   prefix can be at most /26.

   With provider networking disabled (`provisioningEnabled: false`), each VM
   runs on the cluster's default pod network. OSAC creates no ExternalIP, and
   the VM's address is not a tenant address. You still set `fabricManager` and
   `k8sManager`, but you apply no registration. OSAC runs no networking job,
   so you need neither EVPN host networking nor a provider image, settings, or
   secrets.

   The `publishTemplates` hook publishes the compute instance templates that
   VMaaS needs. Disable it only if those templates are supplied separately.

   For production, keep `lvms.enabled: false`, including when the workload
   cluster uses LVMS. Have your storage administrators prepare the appropriate
   CSI integration, StorageBackend, StorageTier, and tenant storage
   configuration according to the [storage documentation](../../storage/README.md),
   especially its [tenant storage onboarding
   guide](../../storage/guides/tenant-onboarding.md).
   Installing LVM Storage or another dynamic storage class alone does not
   register an OSAC storage offering. The automatic LVMS registration hook is
   for development and CI only; see [Section 9.2](#92-evaluation-only).

   For Netris connection settings, see
   [CUDN EVPN with Netris](#cudn-evpn-with-netris).

   For an installation smoke test without a prepared fabric, set
   `global.networking.provisioningEnabled: false` before the first install.
   This skips provider jobs and does not verify VM networking or connectivity.
   You cannot switch it to `true` later on the same deployment.

   For virtual machines on a different cluster from the OSAC management
   services, see
   [Section 6.4](#64-installing-vmaas-on-a-separate-workload-cluster).

2. Add the shared service, database, and secret store settings from
   [Section 4.2](#42-configuring-the-helm-values).

3. Install OSAC. See [Section 4.3](#43-installing-osac).

4. With provider networking enabled, apply the Fabric Manager registration,
   check the `NetworkClass`, and create the ExternalIPPools. See
   [Section 4.4.5](#445-applying-the-fabric-manager-registration) to
   [Section 4.4.7](#447-creating-the-externalippools). With provider networking
   disabled, check the `NetworkClass`
   ([Section 4.4.6](#446-checking-the-networkclass)).

**Verification**

- Complete [Section 7](#7-verifying-the-installation), including step 13.
- Create a ComputeInstance through the OSAC API or `osac create computeinstance`
  and confirm that it reaches `RUNNING`. With provider networking enabled, the tenant's
  `DefaultNetworkingReady` condition must be `True` first:

  ```console
  oc get computeinstance -A
  ```

#### CUDN EVPN with Netris

Before installation, confirm with the responsible platform and fabric
administrators that EVPN host networking, including the
`FRRConfiguration` resources, and the external EVPN/BGP/VTEP fabric are ready.
Use the administrator procedure for your environment; the
[Helm Deployment Guide](helm-deployment-guide.md#evpn-host-networking-vmaas)
provides one example. The `cudn_evpn` implementation must also be available in
the AAP execution environment.

Keep the VMaaS block from step 1 and the shared settings from
[Section 4.2](#42-configuring-the-helm-values). Merge these Netris settings
into `my-values.yaml`, replacing the example coordinates and adding your
infrastructure ranges to `excludedIpv4Cidrs`. The tenant defaults below avoid
Netris's reserved `10.0.0.0/8` range
(see [Section 2.6](#26-evpn-host-networking-for-vmaas)):

```yaml
global:
  networking:
    fabricManager: netris
    k8sManager: cudn_evpn
    excludedIpv4Cidrs:
      - 10.0.0.0/8  # Netris reservation; also add your EVPN/infrastructure ranges
    networkClass:
      defaults:
        virtualNetworkIPv4CIDR: 172.16.0.0/16
        subnetIPv4CIDR: 172.16.0.0/20
operator:
  networkManagers:
    enabled: false
aap:
  instanceGroups:
    networkFulfillment:
      enabled: true
      config:
        NETRIS_CONTROLLER_URL: "https://netris.example.com"
        NETRIS_VALIDATE_CERTS: "true"
        NETRIS_USERNAME: "netris"
        NETRIS_SITE_ID: "5"
        NETRIS_TENANT_ID: "1"
        # Required only for provider-management ExternalIP destinations:
        NETRIS_MGMT_VPC_ID: "<netris_management_vpc_id>"
```

Put the password in a separate values file excluded from version control. Add
`NETRIS_PASSWORD` under `aap.instanceGroups.networkFulfillment.secret` in that
local file, using the value from your secret store. Do not commit the populated
file.

```yaml
aap:
  instanceGroups:
    networkFulfillment:
      secret: {}  # Add NETRIS_PASSWORD locally from your secret store.
```

Pass both files to Helm, for example `-f my-values.yaml -f my-secrets.local.yaml`.
With chart-managed AAP, the chart writes these settings to the
`network-fulfillment-ig` ConfigMap and Secret in the OSAC namespace. With
external AAP, also apply the copies in the external job cluster as described in
[Section 4.4.3](#443-setting-the-networking-helm-values). Replace
`<netris_management_vpc_id>` with the
provider-supplied positive integer ID when using ExternalIP destinations in
`provider_management`; otherwise omit `NETRIS_MGMT_VPC_ID`.
`NATGateway` resources and VM ExternalIP attachments in the tenant network
use that VirtualNetwork's VPC instead. Use a Netris endpoint whose certificate
is trusted by the execution environment; coordinate private CA trust with
your platform administrators.

In `0.4.0`, `global.networking.fabricManager: netris` and
`global.networking.k8sManager: cudn_evpn` directly select the two managers.
The chart supplies their registration roles, network types, and capabilities.
The old expert NetworkClass block and manual `capabilities: [ipv4]`
registrations are not needed. Keep the chart's pinned AAP execution image and
project revision together so the installed Ansible content matches the chart.

### 6.2 Installing OSAC for CaaS

The AWS Route 53 DNS backend is the default; it's independent of the Fabric
Manager. CaaS workers are bare-metal instances, so CaaS also needs BMaaS. The
managed endpoint lifecycle requires provider networking enabled and a Cluster
template that advertises the `supports_automatic_caas_endpoints` capability.
For such templates, you can omit `auto_external_ip_attachment`; Fulfillment
enables it automatically. OSAC reserves the guest ingress VIP, allocates API
and ingress ExternalIPs, creates their attachments, and publishes endpoint DNS
after they are ready. The Subnet address plan must have a non-empty `lbVip`
share. With provider networking disabled, these endpoint jobs do not run, so
configure endpoint routes and DNS outside OSAC.

**Prerequisites**

- multicluster engine is installed (standalone, or provided by RHACM).
- MetalLB (or another `LoadBalancer`-class implementation) and LVM Storage
  (or another dynamic storage class) are installed.
- The BMaaS prerequisites of [Section 6.3](#63-installing-osac-for-bmaas)
  are met.
- The hub cluster has a default storage class (see
  [Section 2.1](#21-cluster-and-access)). Hosted control planes provision
  etcd persistent volume claims from it.
- If `DNS_CLASS` is `dns.route53.dns`, you have AWS Route 53 credentials
  for the target hosted zone. They are not required for `dns.noop.dns`.
- The CaaS prerequisites in
  [Section 2.4](#24-credentials-and-external-services), including those for
  provider networking, are in place.
- Every BareMetalInstanceType used for workers has a network port with role
  `fabric` ([Section 6.3](#63-installing-osac-for-bmaas)).

> **Note**: OSAC-managed storage tiers are not yet available for CaaS tenant
> clusters. The tenant-cluster CSI storage provider is under development.

**Procedure**

1. In `my-values.yaml`, enable the CaaS and BMaaS tiers, the OpenShift
   Container Platform release images offered to hosted clusters, the managers,
   and the `cluster-fulfillment` instance group:

   ```yaml
   global:
     services: { caas: { enabled: true }, vmaas: { enabled: false }, bmaas: { enabled: true }, maas: { enabled: false } }
     networking:
       provisioningEnabled: true    # required for the CaaS endpoint lifecycle
       fabricManager: acme_fabric
       k8sManager: ""               # set cudn_evpn when VMaaS is or will be enabled too
   clusterVersions:
     enabled: true
     versions:
       - version: "4.22.0"
         image: "quay.io/openshift-release-dev/ocp-release:4.22.0-multi"
         default: true
         diskImage:
           name: rhcos-4-22
           sourceRef: oci://quay.io/osac-project/rhcos:4.22.0
           architecture: [ARCHITECTURE_AMD64]
   aap:
     instanceGroups:
       clusterFulfillment:
         enabled: true
         config:
           DNS_CLASS: "dns.route53.dns"
           EXTERNAL_ACCESS_BASE_DOMAIN: "clusters.example.com"
           EXTERNAL_ACCESS_SUPPORTED_BASE_DOMAINS: "clusters.example.com"
           EXTERNAL_ACCESS_API_INTERNAL_NETWORK: "hypershift"
           HOSTED_CLUSTER_BASE_DOMAIN: "clusters.example.com"
           HOSTED_CLUSTER_CONTROLLER_AVAILABILITY_POLICY: "HighlyAvailable"
           HOSTED_CLUSTER_INFRASTRUCTURE_AVAILABILITY_POLICY: "HighlyAvailable"
   ```

   - After the automatic API ExternalIP attachment is ready, OSAC publishes
     `api` and `api-int` to the API ExternalIP. After ingress is configured and
     its ExternalIP attachment is ready, OSAC publishes `*.apps` to the ingress
     ExternalIP. Both use `DNS_ZONE`, which defaults to
     `EXTERNAL_ACCESS_BASE_DOMAIN`. The endpoint jobs wait for the records to
     resolve.
   - `EXTERNAL_ACCESS_SUPPORTED_BASE_DOMAINS` must contain
     `HOSTED_CLUSTER_BASE_DOMAIN`. The endpoint jobs use this value for the
     HostedCluster API and ingress record names and reject an unsupported value
     before contacting the DNS provider.
   - `DNS_ZONE` defaults to `EXTERNAL_ACCESS_BASE_DOMAIN`; configure it to
     contain the HostedCluster record names. In the common case, set
     `HOSTED_CLUSTER_BASE_DOMAIN` and `EXTERNAL_ACCESS_BASE_DOMAIN` to the same
     value. Unset, `HOSTED_CLUSTER_BASE_DOMAIN` defaults to `hosted.` followed
     by the hub's apps domain without its `apps.` prefix, so set it.
   - Do not change `EXTERNAL_ACCESS_BASE_DOMAIN`, `HOSTED_CLUSTER_BASE_DOMAIN`,
     `DNS_ZONE`, or `DNS_CLASS` while any Cluster exists. Keep each in-use
     HostedCluster base domain in `EXTERNAL_ACCESS_SUPPORTED_BASE_DOMAINS` until
     all Clusters using it are deleted. Removing a domain earlier makes DNS
     cleanup retry and holds the automatic endpoint IPs until the domain is
     supported again.
   - Add `networkClass.defaults` and the `bmf` values from
     [Section 6.3](#63-installing-osac-for-bmaas). Confirm the derived address
     plan has a non-empty `lbVip` share ([Section 4.4.1](#441-choosing-the-networking-settings),
     [Table 4.1](#table-41-default-address-plan)).
     With provider networking enabled, apply the Fabric Manager registration,
     configure its settings, and set the execution-environment image.

2. If `DNS_CLASS` is `dns.route53.dns`, put the AWS credentials in a separate
   values file excluded from version control, such as `my-secrets.local.yaml`:

   ```yaml
   aap:
     instanceGroups:
       clusterFulfillment:
         secret:
           AWS_ACCESS_KEY_ID: "<route53_access_key_id>"
           AWS_SECRET_ACCESS_KEY: "<route53_secret_access_key>"
   ```

   If `DNS_CLASS` is `dns.noop.dns`, set it in `my-values.yaml` as described
   in [Section 8.4](#84-installing-without-dns-management), skip the Route 53
   credential instructions and leave the AWS credentials unset.

3. For external AAP, prepare the CaaS settings for its `cluster-fulfillment`
   container group. This step applies with either DNS backend; skip it only for
   chart-managed AAP. The chart-managed `cluster-fulfillment-ig` ConfigMap and
   Secret are created in the OSAC release namespace and are not automatically
   available to external job pods in another namespace or cluster. Create
   matching resources in the namespace where the external group's job pods run,
   using values from `aap.instanceGroups.clusterFulfillment.config` and
   `.secret`. Generate the ConfigMap input from the non-secret values and the
   Secret input from your protected secret store; keep the secret file out of
   version control. For example:

   ```console
   AAP_CONTEXT="<external-aap-job-cluster-context>"
   AAP_NAMESPACE="<external-aap-job-namespace>"
   oc --context "$AAP_CONTEXT" create configmap cluster-fulfillment-ig \
     --namespace "$AAP_NAMESPACE" --from-env-file=cluster-fulfillment-ig.env \
     --dry-run=client -o yaml |
     oc --context "$AAP_CONTEXT" apply -f -
   oc --context "$AAP_CONTEXT" create secret generic cluster-fulfillment-ig \
     --namespace "$AAP_NAMESPACE" --from-env-file=cluster-fulfillment-ig.secret \
     --dry-run=client -o yaml |
     oc --context "$AAP_CONTEXT" apply -f -
   ```

   If no secret keys are configured, create an empty `cluster-fulfillment-ig`
   Secret with `data: {}` so the job pod's required Secret reference resolves.

   The hosted-cluster workflow must authenticate to the OSAC management (hub)
   cluster with an identity authorized to provision hosted clusters. When
   external `cluster-fulfillment` jobs run on another cluster, the projected
   service-account token belongs to that execution cluster; without separate
   hub credentials, the job may use that identity and fail to provision on the
   hub. In that layout, create a Secret with a protected hub kubeconfig in the
   external job namespace and keep the kubeconfig out of values files and
   version control. Run this in the same shell session with the
   `AAP_CONTEXT` and `AAP_NAMESPACE` set above. The kubeconfig's API endpoint
   must be reachable from the job pods and its CA must be trusted there:

   ```console
   oc --context "$AAP_CONTEXT" create secret generic osac-hub-kubeconfig \
     --namespace "$AAP_NAMESPACE" \
     --from-file=kubeconfig=/path/to/hub-kubeconfig \
     --dry-run=client -o yaml |
     oc --context "$AAP_CONTEXT" apply -f -
   ```

   Add the CaaS `envFrom` entries to the external container group's
   `pod_spec_override`. When the `osac-hub-kubeconfig` Secret above exists
   (external jobs on another cluster), also add its volume mount and the
   `K8S_AUTH_KUBECONFIG` setting; the hosted-cluster workflow reads this
   variable for Kubernetes API access. When the jobs run on the hub cluster,
   omit the kubeconfig volume and variable: a required Secret volume for an
   absent Secret keeps the pod from starting, and the job uses its own
   service-account identity. Merge these entries with any Netris CA mount
   from [Section 4.4.3](#443-setting-the-networking-helm-values):

   ```yaml
   spec:
     containers:
       - name: worker
         envFrom:
           - configMapRef:
               name: cluster-fulfillment-ig
               optional: true
           - secretRef:
               name: cluster-fulfillment-ig
   ```

   With the hub kubeconfig Secret, add these entries to the same override:

   ```yaml
   spec:
     volumes:
       - name: hub-kubeconfig
         secret:
           secretName: osac-hub-kubeconfig
           items:
             - key: kubeconfig
               path: hub-kubeconfig
     containers:
       - name: worker
         volumeMounts:
           - name: hub-kubeconfig
             mountPath: /etc/osac
             readOnly: true
         env:
           - name: K8S_AUTH_KUBECONFIG
             value: /etc/osac/hub-kubeconfig
   ```

   Keep the external ConfigMap and Secret synchronized with the configured
   values; newly launched jobs then read the updated settings and credentials.

4. Install OSAC (see [Section 4.3](#43-installing-osac)), passing every
   values file that contains required settings. For `dns.noop.dns`, leave the
   Route 53 credentials unset; keep passing `my-secrets.local.yaml` if it
   contains other required values. For example,
   `helm ... -f my-values.yaml -f my-secrets.local.yaml ...`. For more
   information, see
   [`aap-configuration.md`](https://github.com/osac-project/osac/blob/main/osac-installer/docs/aap-configuration.md)
   and
   [`dns-backend.md`](https://github.com/osac-project/osac/blob/main/osac-installer/docs/dns-backend.md).

5. Check the `NetworkClass`
   ([Section 4.4.6](#446-checking-the-networkclass)). With provider networking
   enabled and a Cluster using automatic external access, create a `READY`
   ExternalIPPool with at least two available addresses in one pool before
   creating the Cluster
   ([Section 4.4.7](#447-creating-the-externalippools)). With provider
   networking disabled, OSAC creates no ExternalIPs, so you create no
   ExternalIPPools.

**Verification**

- Complete [Section 7](#7-verifying-the-installation), including step 11.
- Create a Cluster and watch the AAP `cluster-fulfillment` job.

### 6.3 Installing OSAC for BMaaS

**Prerequisites**

- BareMetalOperator is installed, with a `Provisioning` custom resource that has
  `spec.watchAllNamespaces: true`. OSAC does not install these; the install
  preflight stops the installation if they are missing.
- The BMaaS prerequisites in
  [Section 2.4](#24-credentials-and-external-services) are in place.
- Every BareMetalInstanceType has at least one network port with role
  `fabric`. Every BareMetalInstance
  and Cluster gets a default network attachment on the first `fabric` port,
  and without one, creating them fails with "has no fabric-role network port"
  or "has no network port with role 'fabric'". Use the port names of the
  inventory's MAC map ([Section 2.4](#24-credentials-and-external-services)).

**Procedure**

1. In `my-values.yaml`, enable the BMaaS tier and the Metal3 backend. This
   example is for a BMaaS-only installation, where provider networking stays
   disabled and hosts stay on the provisioning network:

   ```yaml
   global:
     services: { bmaas: { enabled: true }, vmaas: { enabled: false }, caas: { enabled: false }, maas: { enabled: false } }
     networking:
       provisioningEnabled: false   # fixed at the first install
       fabricManager: acme_fabric   # required also with provider networking disabled
       k8sManager: ""
   bmf:
     metal3:
       enabled: true
       namespace: host-inventory
       hostClass: metal3
     env:
       aapUrl: "https://<trusted-aap-route>/api/controller"
       aapInsecureSkipVerify: "false"
   aap:
     instanceGroups:
       publishTemplates:
         enabled: false
   ```

   `fabricManager` is required even with provider networking disabled; you
   apply no registration. This BMaaS-only sample is not the CaaS configuration:
   CaaS uses the ClusterOrder endpoint lifecycle in
   [Section 6.2](#62-installing-osac-for-caas), which requires the shared
   `provisioningEnabled` setting to be `true`. The standalone BareMetalInstance
   networking path remains unsupported with provider networking enabled.
   Setting `publishTemplates.enabled` to `false` is safe here
   because a BMaaS-only deployment does not consume templates
   published by this hook. Set `bmf.metal3.namespace` to the
   namespace where your `BareMetalHost` resources live.

   If CaaS or VMaaS is also enabled with chart-managed AAP, set
   `aap.instanceGroups.publishTemplates.enabled: true` or remove this
   BMaaS-only `false` override so the hook publishes their required templates.
   With external AAP, keep it `false` and publish the required templates
   separately as described in [Section 4.2](#42-configuring-the-helm-values).

   For production, use the HTTPS Route for chart-managed AAP with a certificate
   trusted by BMF's system trust store. The internal
   `http://osac-aap/api/controller` URL is evaluation-only unless another
   mechanism protects the cluster network; otherwise it sends the AAP token
   without transport encryption. When AAP is external, set `bmf.env.aapUrl` to
   its Controller URL from [Section 4.2](#42-configuring-the-helm-values).
   BMF has a separate URL setting from `operator.aap.url`. Keep
   `bmf.env.aapInsecureSkipVerify: "false"` for production HTTPS connections.

   Also add `networkClass.defaults` from
   [Section 4.4.3](#443-setting-the-networking-helm-values); they apply with
   provider networking disabled too, and cannot change later. Without
   `k8sManager`, the default address plan is 0 / 1 / 0. Coming with BMaaS
   networking in a later release: moving hosts to tenant Subnets through the
   Fabric Manager (BMaaS provider networking).

2. Install OSAC. See [Section 4.3](#43-installing-osac).

3. Check the `NetworkClass`
   ([Section 4.4.6](#446-checking-the-networkclass)).

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
refuses to start when either the ClusterOrder or BareMetalInstance controller
is enabled with a remote kubeconfig. The `values-example.yaml` sets
`operator.controllers.clusterOrder: true`, and explicit controller values
override service toggles, so set both `clusterOrder` and `bareMetalInstance`
to `false` in `my-values.yaml` as shown below. Provider networking across
these clusters is not supported in this release; set
`global.networking.provisioningEnabled: false` at the first installation.

Your platform administrators must prepare the management cluster with the
OSAC prerequisites and the workload cluster with OpenShift Virtualization,
dynamic storage as described in
[Section 2](#2-prerequisites).
Create the workload kubeconfig Secret on the management cluster, including
the console-proxy label, as described in
[VMaaS dedicated cluster](https://github.com/osac-project/osac/blob/main/osac-operator/docs/vmaas-dedicated-cluster/README.md).

The `kubevirt-crds` install preflight checks the cluster where Helm runs, which
is the management cluster. After OpenShift Virtualization is installed on the
workload cluster, point `KUBECONFIG` at that cluster and verify the CRD checked
by the preflight:

```console
oc get crd kubevirts.kubevirt.io
```

Ensure OpenShift Virtualization is installed on the workload cluster. A
separate workload cluster does not require removing an existing installation
from the management cluster.

If your platform administrators use Helm-based infrastructure charts, set
`cnv.enabled: false` in the management cluster's infrastructure values, not
in OSAC `my-values.yaml`, for a new release that should not manage OpenShift
Virtualization. For an existing management-cluster release, keep its current
setting unless its owner plans a migration. Changing `true` to `false` can remove release-managed resources,
including the `openshift-cnv` namespace, and disrupt workloads. Check release
ownership and plan how to preserve or migrate the resources before changing
the value.

On the workload cluster, set `cnv.enabled: true` in the infrastructure values
only if those charts install and manage OpenShift Virtualization; keep it
`false` when administrators manage an existing installation. In OSAC
`my-values.yaml`, skip only the `kubevirt-crds` preflight check and give the
verified reason shown below. Leave the remaining checks enabled.

Start with the VMaaS values from [Section 6.1](#61-installing-osac-for-vmaas),
including the storage registration and networking settings. Select storage
based on the workload cluster, and put its values in `my-values.yaml` on the
management cluster. For production, keep `lvms.enabled: false` for every
storage provider, including LVMS. Have your storage administrators prepare
that provider's StorageBackend, StorageTier, and tenant storage configuration
as described in Section 6.1 and the [tenant storage onboarding
guide](../../storage/guides/tenant-onboarding.md). For an overview of storage
providers and ownership models, see the
[storage documentation](../../storage/README.md).
For development or CI with LVMS, see
[Section 9.2](#92-evaluation-only).

The chart still requires a non-empty `fabricManager` value when provider
networking is disabled, and VMaaS requires `k8sManager: cudn_evpn`. No Fabric
Manager registration or `evpn` network type is required for this layout. The
`acme_fabric` value below is only a non-empty placeholder.

Merge these values into `my-values.yaml` to enable VMaaS only and point the
operator and AAP at the Secret:

```yaml
global:
  services: { vmaas: { enabled: true }, caas: { enabled: false }, bmaas: { enabled: false }, maas: { enabled: false } }
  networking:
    provisioningEnabled: false
    fabricManager: acme_fabric
    k8sManager: cudn_evpn
lvms:
  enabled: false  # production storage registration is administrator-managed
operator:
  controllers:
    clusterOrder: false
    bareMetalInstance: false
  remoteClusterKubeconfig: /var/run/secrets/remote/kubeconfig
  remoteClusterKubeconfigSecretName: osac-remote-kubeconfig
  remoteClusterKubeconfigSecretKey: kubeconfig
aap:
  configAsCode:
    remoteClusterKubeconfigSecretName: osac-remote-kubeconfig
    remoteClusterKubeconfigSecretKey: kubeconfig
preflight:
  skip:
    kubevirt-crds: "OpenShift Virtualization CRD verified on workload cluster"
```

`remoteClusterKubeconfig` is the path inside the operator container. The
Secret name and key must match the Secret on the management cluster. The
chart default key is `kubeconfig`.
The workload API endpoint in that kubeconfig must resolve and be reachable from
the management-cluster workloads that use it, including the OSAC operator, AAP
workers, and Console Proxy. Workstation-only access is insufficient; use direct
connectivity or a supported proxy or tunnel configured for those workloads.

The `aap.configAsCode.remoteClusterKubeconfigSecretName` values configure
chart-managed AAP. For external AAP, ensure a Secret with the kubeconfig for the
VM cluster is present in the cluster and namespace where the external compute
and storage job pods run. Use the workload-cluster kubeconfig when VMs run on a
separate workload cluster. If VMs remain on the management (hub) cluster while
external AAP jobs run elsewhere, use the hub kubeconfig instead; the OSAC
operator continues to use its in-cluster access. A Secret in
another cluster or namespace cannot be mounted. Use the same name and key as
the values above (`osac-remote-kubeconfig` and `kubeconfig` in this example),
and keep the Secret current whenever the VM-cluster kubeconfig changes. Add the
following entries to the
`pod_spec_override` for both the `compute-instance-operations` and
`storage-operations` instance groups so each job exposes the file at the path
expected by `OSAC_REMOTE_CLUSTER_KUBECONFIG`:

```yaml
spec:
  volumes:
    - name: remote-cluster-kubeconfig
      secret:
        secretName: osac-remote-kubeconfig
        items:
          - key: kubeconfig
            path: remote-cluster-kubeconfig
  containers:
    - name: worker
      volumeMounts:
        - name: remote-cluster-kubeconfig
          mountPath: /etc/osac
          readOnly: true
      env:
        - name: OSAC_REMOTE_CLUSTER_KUBECONFIG
          value: /etc/osac/remote-cluster-kubeconfig
```

For example, apply the VM-cluster kubeconfig file as a Secret in the external
job cluster. Keep the file protected and out of version control:

```console
AAP_CONTEXT="<external-aap-job-cluster-context>"
AAP_NAMESPACE="<external-aap-job-namespace>"
oc --context "$AAP_CONTEXT" create secret generic osac-remote-kubeconfig \
  --namespace "$AAP_NAMESPACE" \
  --from-file=kubeconfig=/path/to/workload-kubeconfig \
  --dry-run=client -o yaml |
  oc --context "$AAP_CONTEXT" apply -f -
```

Before installing OSAC, point `KUBECONFIG` back to the management cluster and
confirm that the active API server is its endpoint. The OSAC Helm command uses
the active cluster context:

```console
export KUBECONFIG=/path/to/management-kubeconfig
oc whoami --show-server
```

Confirm that the server shown is the management cluster.

Install OSAC on the management cluster. See
[Section 4.3](#43-installing-osac). After installation, run the OSAC and
preflight checks in [Section 7](#7-verifying-the-installation) on the
management cluster. For VMaaS, check the OpenShift Virtualization CSV from
Section 7 on the workload cluster. Check the LVM Storage and MetalLB CSVs on
whichever cluster hosts those providers. Confirm that the virtual machine is
created on the workload cluster.

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
     | awk 'NR == 1 || $2 ~ /^(aap-operator|cert-manager-operator|kubernetes-nmstate-operator|kubevirt-hyperconverged-operator|lvms-operator|metallb-operator|multicluster-engine)\./'
   ```

   The AAP CSV is named `aap-operator` and can appear in multiple namespaces.
   Check the OpenShift Virtualization CSV for VMaaS and the LVM Storage and
   MetalLB CSVs if those providers satisfy your storage and load-balancer
   prerequisites. For CaaS, check standalone MCE or the health of the MCE
   installation provided by RHACM.
   For EVPN host networking, also check the Kubernetes NMState Operator CSV.

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
   deployment, including customer-managed bundles and upstream trust-manager
   installations, check the CA-bundle `ConfigMap` in the OSAC release
   namespace.

   Confirm that trust-manager is installed in the `cert-manager` namespace.
   If administrators use it to distribute the CA bundle, confirm that it
   distributes the ConfigMap to the OSAC release namespace under the name
   configured by `service.certs.caBundle.configMap`. Administrators may
   provide the ConfigMap directly, but trust-manager is still required. Set
   this value in `my-values.yaml` to the name they provide; it defaults to
   `ca-bundle`. Changing the value does not create or rename a ConfigMap. For
   a custom name, set the related CA-bundle values listed in
   [Section 5](#5-helm-chart-configuration-parameters) to the same name before
   installation. For customer-managed trust, ensure that the ConfigMap already
   exists in the OSAC release namespace.

   Replace `<ca_bundle_configmap>` below with that shared name and
   `<namespace>` with the OSAC release namespace:

   ```console
   oc get configmap <ca_bundle_configmap> -n <namespace> -o yaml
   ```

   If your administrators use trust-manager to distribute the bundle, ask
   them for the corresponding `Bundle` name and verify it. The resource is
   cluster-scoped and needs no namespace flag:

   ```console
   oc get bundles.trust.cert-manager.io <bundle_name> -o yaml
   ```

   In `status.conditions`, find the condition with `type: Synced`. Confirm its
   `status` is `True` and its `observedGeneration` matches the Bundle's
   `metadata.generation` before using the ConfigMap. This verifies that
   trust-manager has processed the current Bundle configuration, but not that
   a changed source Secret has reached the target ConfigMap; Secret updates do
   not change the Bundle generation. After a CA source changes, repeat the
   ConfigMap command above and confirm that `bundle.pem` contains the expected
   CA by comparing its SHA-256 fingerprint with the fingerprint of the
   administrator-provided certificate. Compare the SHA-256 fingerprint of
   each CA that should no longer be trusted with the fingerprints of the
   certificates in the updated `bundle.pem`; none should match.

   The target ConfigMap must store the bundle under the fixed `bundle.pem` key
   required by OSAC. After it contains the expected CA, extract `bundle.pem`
   for the client checks below. Repeat the ConfigMap check and extraction after
   each CA source change. If a bundle change affects the Fulfillment
   certificate CA or the CA used to reach the OIDC issuer, restart the OSAC
   operator after the updated bundle appears in the target ConfigMap and before
   switching certificates. The operator's OAuth client loads its CA pool at
   startup. After removing any CA from this bundle, wait for the updated bundle
   to appear in the target ConfigMap and restart the operator again, including
   when removing the old CA after a rotation. Use the refreshed file for client
   checks.

   ```console
   oc get configmap <ca_bundle_configmap> -n <namespace> \
     -o jsonpath='{.data.bundle\.pem}' > osac-ca-bundle.pem
   test -s osac-ca-bundle.pem
   ```

   If Section 4.4.6 uses this shared bundle for the internal API Route, set
   `CA_FILE=osac-ca-bundle.pem` in that same Bash session after extraction.
   Before the tenant check in step 13, repeat step 2 of Section 4.4.6 to rebuild
   `CURL_CA_ARGS` from the refreshed bundle.

   Refreshing this local file or running an OSAC installation or upgrade does
   not reload Keycloak's truststore. When adding a CA, wait for the updated
   bundle to reach its ConfigMap and restart Keycloak before switching the
   outbound OIDC provider to use that CA. After removing any CA from a bundle
   Keycloak uses, wait for the updated bundle to reach its ConfigMap and restart
   Keycloak as well.
   For bundled Keycloak, run:

   ```console
   oc rollout restart deployment/keycloak-service -n keycloak
   oc rollout status deployment/keycloak-service -n keycloak
   oc exec deployment/keycloak-service -n keycloak -- \
     cat /etc/keycloak/truststore/ca-bundle.pem > keycloak-truststore.pem
   ```

   Compare the CA fingerprints in `keycloak-truststore.pem` with the ConfigMap
   Keycloak mounts. For bundled Keycloak, use the name set by
   `certs.caBundle.configMap` in `my-infra-values.yaml` (`ca-bundle` by
   default) in the `keycloak` namespace. Extract its `bundle.pem`:

   ```console
   oc get configmap <keycloak_ca_bundle_configmap> -n keycloak \
     -o jsonpath='{.data.bundle\.pem}' > keycloak-ca-bundle.pem
   test -s keycloak-ca-bundle.pem
   ```

   Compare its CA fingerprints with `keycloak-truststore.pem` to check that the
   mounted file matches the ConfigMap. This does not prove Keycloak has reloaded
   its running truststore. For externally managed Keycloak, compare with the CA
   bundle configured for that deployment. The mounted file does not show every
   CA Keycloak trusts. Check
   `KC_TRUSTSTORE_PATHS`, any JVM truststore setting, and Java's default
   truststore too. Use isolated TLS tests to verify effective trust: connect
   Keycloak to a test OIDC provider with a valid certificate signed only by the
   new CA and confirm it succeeds before switching providers. After removing a
   CA, test with a valid certificate signed only by that CA and confirm Keycloak
   rejects the TLS connection. See
   [Adding CA roots to the shared bundle at runtime](helm-deployment-guide.md#adding-ca-roots-to-the-shared-bundle-at-runtime).

   Keep this file for the OIDC, API, and hub-registration checks. It must
   contain the CA that signs the endpoint's certificate. If an endpoint
   uses a different CA, obtain the matching PEM bundle from its
   administrator and use that file for that endpoint.

   If this installation uses the bundled Kafka service, also confirm that its
   Kafka resource reports `Ready=True` before proceeding.

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

   For the bundled Keycloak, use the shared trust bundle extracted in
   step 4 of [this section](#7-verifying-the-installation):

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
     and `osac-operator-console-proxy`.
   - When `aap.aap.instance.enabled` is `true`: the `osac-aap-*` pods.
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

9. Verify AAP according to its configuration. When
   `aap.bootstrap.enabled` is `true`, the `osac-aap-bootstrap` job runs as a
   postinstallation hook and takes 10 to 40 minutes:

   ```console
   oc logs -f job/osac-aap-bootstrap -n <namespace>
   ```

   With external AAP (`aap.aap.instance.enabled: false` and
   `aap.bootstrap.enabled: false`), do not expect `osac-aap-*` pods or the
   bootstrap job. For a private AAP CA, use the PEM CA bundle that was added to
   the OSAC Operator's system trust store. This is separate from the Fulfillment
   API CA bundle; use the same administrator-provided source that configured
   Operator trust. Create a temporary ConfigMap from that PEM file:

   ```console
   AAP_CA_FILE=/path/to/operator-trusted-aap-ca.pem
   oc create configmap aap-connectivity-ca \
     --from-file=ca.crt="$AAP_CA_FILE" -n <namespace>
   oc run aap-connectivity-check -n <namespace> --rm -i --restart=Never \
     --image=quay.io/openshift/origin-cli:4.20.0 \
     --overrides='{
       "apiVersion":"v1",
       "spec":{
         "volumes":[
           {"name":"aap-ca",
            "configMap":{"name":"aap-connectivity-ca"}}
         ],
         "containers":[
           {
             "name":"aap-connectivity-check",
             "image":"quay.io/openshift/origin-cli:4.20.0",
             "command":[
               "curl","--fail-with-body","--silent","--show-error",
               "--cacert","/aap-ca/ca.crt",
               "https://<external-aap>/api/controller/v2/ping/"
             ],
             "volumeMounts":[
               {"name":"aap-ca","mountPath":"/aap-ca","readOnly":true}
             ]
           }
         ]
       }
     }'
   oc delete configmap aap-connectivity-ca -n <namespace>
   ```

   Replace `<external-aap>` with the Controller host. If its certificate is
   publicly trusted, omit the ConfigMap and mount and use the image's system
   trust store:

   ```console
   oc run aap-connectivity-check -n <namespace> --rm -i --restart=Never \
     --image=quay.io/openshift/origin-cli:4.20.0 --command -- \
     curl --fail-with-body --silent --show-error \
       "https://<external-aap>/api/controller/v2/ping/"
   ```

10. Check API reachability and log in with the `osac` CLI. For the bundled
    CA or a custom CA, use the API trust bundle prepared in step 4 of
    [this section](#7-verifying-the-installation) for both commands:

    ```console
    ROUTE=$(oc get route fulfillment-api -n <namespace> -o jsonpath='{.spec.host}')
    curl --fail-with-body --cacert osac-ca-bundle.pem \
      "https://$ROUTE/.well-known/jwks.json"
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
    curl --fail-with-body "https://$ROUTE/.well-known/jwks.json"
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

13. Check networking. The `NetworkClass` must be `READY`
    ([Section 4.4.6](#446-checking-the-networkclass)), and, with provider
    networking enabled, at least one ExternalIPPool must be `READY`
    ([Section 4.4.7](#447-creating-the-externalippools)). After you create a
    Tenant, its `DefaultNetworkingReady` condition must become `True`. In the
    API output it appears as type
    `TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY` with status
    `CONDITION_STATUS_TRUE`:

    Use `FULFILLMENT_API` and `CURL_CA_ARGS` from
    [Section 4.4.6](#446-checking-the-networkclass). For a publicly trusted
    Route, run `unset CA_FILE`; whenever `CA_FILE` changes, repeat step 2 of
    Section 4.4.6 before reusing `CURL_CA_ARGS`. Create a fresh token
    immediately before the request:

    ```console
    OSAC_ADMIN_TOKEN="$(oc create token admin -n "$NS" --duration 1h)"
    curl -s "${CURL_CA_ARGS[@]}" -H "Authorization: Bearer $OSAC_ADMIN_TOKEN" \
      "${FULFILLMENT_API}/api/private/v1/tenants" \
      | jq '.items[] | select(.metadata.name=="<tenant_name>") | .status.conditions'
    ```

    `READY` means the managers accepted the configuration, not that traffic
    flows, and the install preflight checks no networking. With provider
    networking enabled, prove the data path with test VMs: traffic between two
    VMs, a VM reaching its gateway, NAT egress through a NATGateway, and
    ingress through an attached ExternalIP.

14. Run a service smoke test, as described in the Verification section of the
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
   administrator host that can reach the internal route. For the bundled CA
   or a custom CA, use the API trust bundle prepared in step 4 of
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

To run CaaS without OSAC managing DNS, set the DNS backend `dns.noop.dns` in
the Helm values. You then need no Route 53 credentials, and you create the
cluster DNS records yourself. Set it at the first install, before the first
Cluster exists.

**Procedure**

1. In `my-values.yaml`, set the DNS backend and the domains:

   ```yaml
   aap:
     instanceGroups:
       clusterFulfillment:
         enabled: true
         config:
           DNS_CLASS: dns.noop.dns
           EXTERNAL_ACCESS_BASE_DOMAIN: "<guest_domain>"
           HOSTED_CLUSTER_BASE_DOMAIN: "<guest_domain>"
           EXTERNAL_ACCESS_SUPPORTED_BASE_DOMAINS: "<guest_domain>"
   ```

   Leave `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` unset.

2. Install or upgrade OSAC. See [Section 4.3](#43-installing-osac).

3. Confirm the values took:

   ```console
   oc get cm cluster-fulfillment-ig -n "$NS" -o yaml \
       | grep -E 'DNS_CLASS|EXTERNAL_ACCESS|HOSTED_CLUSTER'
   ```

4. This manual DNS procedure requires provider networking enabled and
   automatic API and ingress ExternalIPs. With provider networking disabled,
   OSAC allocates no ExternalIPs; provision and manage endpoint addresses and
   DNS outside this procedure.

   Find the ClusterOrder name and endpoint addresses on the hub. Set the Cluster
   ID from the Fulfillment Cluster record:

   ```bash
   CLUSTERS_JSON=$(osac get clusters --filter 'this.metadata.name == "my-cluster"' -o json) || exit 1
   CLUSTER_COUNT=$(jq 'length' <<<"$CLUSTERS_JSON") || exit 1
   if [ "$CLUSTER_COUNT" -ne 1 ]; then
     echo "Expected one Cluster named my-cluster; scope the query by tenant and project." >&2
     exit 1
   fi
   CLUSTER_ID=$(jq -r '.[0].id' <<<"$CLUSTERS_JSON") || exit 1
   oc get clusterorders -A \
     -l "osac.openshift.io/clusterorder-uuid=${CLUSTER_ID}" \
     -o custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name
   selector="osac.openshift.io/auto-created=true,osac.openshift.io/auto-created-kind=cluster"
   selector="${selector},osac.openshift.io/auto-created-for=${CLUSTER_ID}"
   for endpoint in api ingress; do
     oc get externalips -A \
       -l "${selector},osac.openshift.io/auto-created-endpoint=${endpoint}" \
       -o custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name,ADDRESS:.status.address
   done
   ```

   Use the ClusterOrder `metadata.name` as `<cluster>` in the records below,
   and each ExternalIP `status.address` for its matching endpoint. An empty
   address means allocation is still pending; query again until it appears.

   As soon as the API ExternalIP is allocated, create the API DNS records; do
   not wait for the ingress attachment or a Ready guest node. `dns.noop.dns`
   does not create or remove records, so point each record to its corresponding
   ExternalIP address. Create the ingress record when its ExternalIP is
   allocated:

   ```text
   api.<cluster>.<guest_domain>       -> API endpoint
   api-int.<cluster>.<guest_domain>   -> API endpoint
   *.apps.<cluster>.<guest_domain>    -> ingress endpoint
   ```

   The records must resolve from the hub, the AAP execution environment, the
   managed host, and the hosted cluster. Create them while the Cluster
   installs: the job fails when `api`, `api-int`, and
   `default-router.apps.<cluster>.<guest_domain>` do not resolve from the AAP
   execution environment within 30 minutes.

   Before deleting the Cluster or its automatic ExternalIP attachments, remove
   these manually managed records. The no-op DNS backend cannot confirm their
   removal, and OSAC may release the ExternalIP after its cleanup job succeeds.

Do not change `DNS_CLASS`, `DNS_ZONE`, `EXTERNAL_ACCESS_BASE_DOMAIN`, or
`HOSTED_CLUSTER_BASE_DOMAIN` while any Cluster exists (see
[Table 4.2](#table-42-settings-fixed-at-installation)).

On a later upgrade, keep `DNS_CLASS: dns.noop.dns` in the values file. Helm
reconciles the `ConfigMap` from those values.

### 8.5 Additional resources

- Fabric Manager build, handover, and lab verification:
  [Fabric Manager Guide](../networking/customer-fabric-manager-guide.md)
- EVPN host networking: "EVPN host networking (VMaaS)" in the
  [Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md#evpn-host-networking-vmaas)
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
- Networking in a connected deployment, with one `NetworkClass`, IPv4 only.
  Provider networking supports VMaaS and the standard CaaS endpoint lifecycle
  only with a Fabric Manager that passes the networking conformance suite
  profiles for each enabled service against this OSAC release. For CaaS, the
  verification must include `reserve_cluster_ingress_ip` and
  `release_cluster_ingress_ip`. Until the conformance suite covers those tasks,
  the provider's lab verification stands in for it (see part 6 of the
  [Fabric Manager Guide](../networking/customer-fabric-manager-guide.md#6-verify-in-a-lab-and-go-live)).
  OSAC does not enforce this.

### 9.2 Evaluation only

- Bundled PostgreSQL and OpenBao instances supplied for testing or CI.
- Keycloak test fixtures and default administrator credentials.
- Development or CI reference values files and `main`/`latest` image tags.

**LVMS registration for evaluation**

For a development or CI environment with LVMS already installed on the
workload cluster, merge this override into `my-values.yaml`:

```yaml
lvms:
  enabled: true
```

This enables the local-storage registration hook, which creates the `local`
StorageBackend and StorageTier with provider `lvms` in Fulfillment. It does
not install LVM Storage. For a separate workload cluster, pass the override
to the OSAC release on the management cluster. Keep `lvms.enabled: false` in
production and have your storage administrators prepare the StorageBackend,
StorageTier, and tenant storage configuration as described in
[Section 6.1](#61-installing-osac-for-vmaas).

### 9.3 Not covered by this guide

- Installing and operating the prerequisite Operators and external services.
  Coordinate their setup with your platform administrators.
- Detailed configuration of an external Keycloak. The chart accepts an external
  Keycloak through `service.auth` and `service.idp`, but realm and client
  provisioning is out of scope. See
  [`fulfillment-service/docs/INSTALL.md`](https://github.com/osac-project/osac/blob/main/fulfillment-service/docs/INSTALL.md).

### 9.4 Not supported

- OSAC networking in air-gapped or disconnected deployments, and IPv6 or
  dual-stack networking.
- Provider networking for VMs in a cluster other than the hub. Platform
  administrators can prepare EVPN host networking on a separate workload
  cluster using the administrator procedure in the [Helm Deployment
  Guide](helm-deployment-guide.md#evpn-host-networking-vmaas). This configures
  host networking only; OSAC provider networking with VMs on a separate
  cluster is not supported in this release. VMaaS with provider
  networking disabled is supported as described in
  [Section 6.4](#64-installing-vmaas-on-a-separate-workload-cluster).
- BMaaS-specific provider networking beyond the CaaS worker and endpoint flow
  described in [Section 6.2](#62-installing-osac-for-caas).
- East-west FabricDomains with fabric configuration, which need BMaaS
  networking, and the FabricDomain types InfiniBand and NVLink (the API answers
  `Unimplemented`).
- The agentless VLAN Fabric Manager (`agentless_net`). It appears in the chart
  schema, but its registration does not meet the network manager contract, so
  a `NetworkClass` cannot select it. Netris is the only Fabric Manager that
  OSAC ships in this release; a provider can supply its own.
- More than one hub. OSAC networking supports exactly one hub, the cluster
  that runs the OSAC operator, AAP, and the VMs.
- In-place upgrade or downgrade between minor releases. Only patch releases of
  the same minor release roll out in place (see
  [Table 4.2](#table-42-settings-fixed-at-installation)).
- Changing a setting fixed at installation, including enabling BMaaS or CaaS
  after networking resources exist (see
  [Table 4.2](#table-42-settings-fixed-at-installation)).
- Creating, changing, or deleting OSAC networking custom resources outside the
  fulfillment API. The admission policy rejects it (see
  [Section 1.2](#12-networking)). A lost custom resource, for
  example after an etcd restore, is not supported.

---

## 10. Uninstalling OSAC

**Procedure**

1. Uninstall the `osac` release:

   ```console
   helm uninstall osac -n "$NS"
   ```

   The uninstall removes the OSAC admission policy and its binding first, so
   the OSAC custom resources can then be deleted.

   > **Note**
   >
   > TBD: the designs do not say whether you must delete the workloads,
   > Tenants, and ExternalIPPools first, so that the Fabric Manager removes
   > their state from the provider's fabric, or how OSAC custom resources
   > that still hold `osac.openshift.io/` finalizers are removed once the
   > operator is gone.

2. The Fabric Manager registration `ConfigMap` that you applied is not part of
   the release, so Helm does not remove it. Delete it when you no longer need
   it:

   ```console
   oc delete configmap network-manager-acme-fabric -n "$NS"
   ```

   EVPN host networking has its own lifecycle and remains after OSAC is
   uninstalled. Coordinate its removal with your platform administrators; see
   [Section 2.6](#26-evpn-host-networking-for-vmaas).

3. The CRDs are retained because they carry the
   `helm.sh/resource-policy: keep` annotation. To remove them, run the
   following command:

   ```console
   oc get crd -o name | grep '\.osac\.openshift\.io$' | xargs oc delete
   ```

4. The install preflight report is a Helm hook resource, so Helm does not
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
oc get jobs -n <namespace> | grep -E 'db-init|publish-templates|bootstrap|network-class|create-hub'
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

A `not found` error on the selected `OSAC_CHART` usually indicates an
incorrect registry path or `--version` value. Confirm that `OSAC_RELEASE_VERSION`
matches a published chart version from [Section 1.1](#11-what-is-released),
then verify that Helm can retrieve its metadata:

```console
helm show chart "$OSAC_CHART" --version "$OSAC_RELEASE_VERSION"
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
  AAP operator (Helm 4): add `--server-side=true --force-conflicts` to the
  `helm upgrade` command. The server-side option is required if the release was
  installed with Helm 3, because Helm 4's `--server-side=auto` follows that
  release's client-side apply method. The operator takes ownership of the field
  on the `osac-aap` custom resource after install.

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
- `... cannot change after installation ...`,
  `Enabling BMaaS or CaaS after networking resources exist ...`,
  `in-place upgrade or downgrade ...`, or
  `excludedIpv4Cidrs range ... overlaps ...` in the `fulfillment-grpc-server`
  log: a setting fixed at installation changed. Restore it (see
  [Table 4.2](#table-42-settings-fixed-at-installation)).

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
- Networking resources stuck in `PENDING`: see
  [Section 11.13](#1113-networking-problems).
- **CaaS:** watch the AAP `cluster-fulfillment` job for the failing task.
  Route 53 credential errors appear there.

### 11.13 Networking problems

- **`helm install` or `helm upgrade` fails before anything is applied**: a
  networking value breaks a rule of
  [Section 4.4.1](#441-choosing-the-networking-settings). A value of the wrong
  form, such as a malformed CIDR or an unknown `k8sManager`, fails the values
  schema ("values don't meet the specifications of the schema(s)"), naming its
  path, for example `/global/networking/fabricManager`. A rule across values
  fails with a message that starts with `global.networking.`. Fix the value
  and run the command again. Values that still set the removed keys
  `bmf.env.enableNetworkingProvisioning` or
  `operator.controllers.networkingProvisioning` fail with "... was removed; use
  global.networking.provisioningEnabled"; remove them.
- **A rollout fails after the first install** with one of the messages in
  [Table 4.2](#table-42-settings-fixed-at-installation): a fixed setting
  changed. Restore the earlier value. `provisioningEnabled` can change only
  with a reinstall. Read the installer's message with
  `oc logs job/create-network-class -n <namespace>`.
- **EVPN host setup fails.** Ask your platform administrators for the setup
  logs and use the host-network procedure in the
  [Helm Deployment Guide](helm-deployment-guide.md#evpn-host-networking-vmaas).
  - "node <node>: BGP session to <peer> not Established; check the fabric side
    and the evpn values": the fabric side is not ready, or the `evpn` values
    differ from the fabric's ([Section 2.6](#26-evpn-host-networking-for-vmaas)).
  - "configured EVPN node '<node>' does not exist" or "EVPN requires
    defaultNetwork.type OVNKubernetes": fix the values or the cluster. Nothing
    on the cluster was changed.
- **The `NetworkClass` is `FAILED`.** Read its `message`
  ([Section 4.4.6](#446-checking-the-networkclass)), then fix or re-create the
  registration. For a wrong Helm value, see
  [Section 4.4.8](#448-replacing-the-networkclass).
- **A networking resource stays `PENDING`.** Its `message` names the running
  task and AAP job ID, or, after a retryable failure, the manager's message and
  the next retry time. OSAC retries with a backoff from 2 to 30 minutes, with
  no limit. Fix the cause, for example a setting in
  `aap.instanceGroups.networkFulfillment`, a missing or not accepted VTEP
  `tenant-vtep` ([Section 2.6](#26-evpn-host-networking-for-vmaas)), or a role
  missing from the image, and roll out; the next job reads the new values. On
  the hub, read the custom resource's `Ready` condition and `status.jobs`.
- **A Subnet stays `PENDING` with "waiting for the VirtualNetwork's
  l3_vni".** The VirtualNetwork was created by an earlier release, before the
  Fabric Manager reported its `l3_vni`. Delete the VirtualNetwork and create it
  again.
- **Resources are `READY`, but VM traffic fails.** A task is `READY` when the
  manager accepted the configuration, not when traffic flows, and `cudn_evpn`
  checks only the VTEP. Check the node-to-fabric BGP sessions (step 5 of
  [Section 7](#7-verifying-the-installation)) and that the fabric imports and
  exports the route targets `0:<VNI>`
  ([Section 4.4.2](#442-prerequisites-for-provider-networking)). With BGP down
  or the route targets missing, routed traffic fails; same-Subnet traffic can
  still work.
- **A networking resource is `FAILED`.** The failure is final; OSAC does not
  retry it. Its owner deletes it and creates it again. A `FAILED`
  SecurityGroup can also be fixed with a rule update.
- **A delete stays `DELETING`.** The delete task is failing and is retried
  until it succeeds. Repair the provider's backend. You cannot remove the
  finalizer by hand: the admission policy rejects it.
- **A Tenant's `DefaultNetworkingReady` is `False`.** Reason
  `ResourcesPending`: a default is still `PENDING`, or onboarding waits for the
  `NetworkClass` or a `READY` ExternalIPPool; the message says which. Reason
  `ResourceFailed`: a default failed, or every `READY` pool is full. A failed
  default SecurityGroup can be fixed with a rule update; otherwise add pool
  capacity or repair the cause, then delete and create the Tenant again.
- **"OSAC manages this resource; change it through the fulfillment API".** The
  admission policy rejected a change made outside the fulfillment API (see
  [Section 1.2](#12-networking)). Use the API or the `osac` CLI.

### 11.14 Additional resources

- "Troubleshooting" in the
  [Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md#troubleshooting)
- "EVPN host networking (VMaaS)" in the
  [Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md#evpn-host-networking-vmaas)
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
  `network-fulfillment`, `storage-operations`, or `publish-templates`.
  With chart-managed AAP, networking jobs run in
  `<prefix>-networking-operations-ig` and read the `network-fulfillment-ig`
  `ConfigMap` and `Secret`. External AAP requires equivalent resources in its
  job cluster; see [Section 4.4.3](#443-setting-the-networking-helm-values).
- **Fabric Manager** — The network manager that configures the provider's
  fabric: an Ansible role, written by the provider or shipped by OSAC (for
  example Netris), registered with a `ConfigMap` (the installer creates it for
  an OSAC-shipped one) and selected by `global.networking.fabricManager`.
  Every deployment has one. See the
  [Fabric Manager Guide](../networking/customer-fabric-manager-guide.md).
- **Kubernetes Manager** — The OSAC network manager that configures hub
  networking for VMs, selected by `global.networking.k8sManager`. This release
  has `cudn_evpn`, for the `evpn` network type. Its host side (FRR,
  `tenant-vtep`, BGP to the fabric) is prepared by your platform administrators
  ([Section 2.6](#26-evpn-host-networking-for-vmaas)).
- **EVPN host networking** — The OpenShift side of the EVPN connection between
  the VMaaS nodes and the provider's fabric, prepared by your platform
  administrators.
- **`NetworkClass`** — The deployment's one networking configuration: the
  selected managers, the tenant defaults, and the address plan. The installer
  creates it from `global.networking.*`.
- **Provider networking enabled or disabled** — The value of
  `global.networking.provisioningEnabled`, fixed at installation. When
  disabled, OSAC runs no networking job.
- **ExternalIPPool** — A range of external IPv4 addresses that a cloud
  administrator creates through the private API. ExternalIPs and NATGateways
  take their addresses from it.
- **Execution environment image** — The AAP job image
  (`aap.configAsCode.eeImage`). With a provider Fabric Manager, it holds the
  provider's Ansible collection on top of OSAC's image.
- **DNS backend (`DNS_CLASS`)** — How CaaS clusters get DNS records:
  `dns.route53.dns` (default, AWS Route 53), or `dns.noop.dns` (OSAC manages
  no DNS records; see [Section 8.4](#84-installing-without-dns-management)).
- **config-as-code** — The Ansible content that the `osac-aap-bootstrap` job
  loads into AAP. Its subscription manifest is stored in the
  `config-as-code-manifest-ig` Secret.
- **Bundled compared with external** — Bundled PostgreSQL is for testing, and
  OpenBao is for testing and CI. Production deployments use external services.
