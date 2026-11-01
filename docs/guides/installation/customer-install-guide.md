# Installing OSAC on OpenShift Container Platform (WIP)

**Last Updated**: 2026-10-29
**Audience**: Cloud administrators
**Status**: WIP — see the TBD note in [Section 10](#10-uninstalling-osac) for what's still open

---

Deploy OSAC (Open Sovereign AI Cloud) onto an existing Red Hat OpenShift
Container Platform cluster by using the OpenShift CLI (`oc`) and Helm. This
guide assumes the prerequisite Operators and infrastructure layer already
exist on the cluster. If you need to set those up yourself, see the
[Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md)
first. The guide covers the VMaaS, CaaS, and BMaaS services.

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

OSAC installs as three ordered Helm releases. Each release is a plain
`helm upgrade --install` command.

**Phase 1a: `osac-deps`.** Installs Operator Lifecycle Manager (OLM)
`Subscription` resources for the platform Operators in their own namespaces:
cert-manager and Ansible Automation Platform (AAP) by default, and standalone
multicluster engine, LVM Storage, MetalLB, OpenShift Virtualization, and Streams
for Apache Kafka when enabled, and the NMState Operator when EVPN host
networking is enabled (`evpn.enabled` and `evpn.nmstate.install`). Every
Operator is gated by a toggle — see
[Table 2.1](#table-21-platform-operators-and-components). Post-installation hooks wait for the cert-manager and AAP
`ClusterServiceVersion` (CSV) resources to reach `Succeeded`.

**Phase 1b: `osac-infra`.** Installs the shared, cluster-scoped resources: the
`default-ca` `ClusterIssuer`, trust-manager, the CA bundle `ConfigMap`,
Keycloak and the `osac` realm in the `keycloak` namespace, and the operand
custom resources (CRs) `HyperConverged`, `LVMCluster`, the MetalLB
`IPAddressPool`, and the Kafka CR. The `configure-*` hooks wait for each
remaining Operator CSV to reach `Succeeded` before applying its operand. For
testing, this phase can also deploy bundled PostgreSQL database and OpenBao.
With `evpn.enabled`, its `osac-infra-configure-evpn` hook also configures the
OpenShift side of EVPN for VMaaS: FRR and route advertisements in the cluster
network, per-node uplink and VTEP addresses, the `tenant-vtep` VTEP, and BGP
sessions to the provider's fabric (see
[Section 2.6](#26-evpn-host-networking-for-vmaas)).

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

An example nightly chart tag is
[`osac/v0.0.58-nightly.20261027.8402b1c.141.1`](https://github.com/osac-project/osac/tree/osac/v0.0.58-nightly.20261027.8402b1c.141.1).
It was built on 27 October 2026, before the install preflight, so it runs the
old shell validation hook; see the note below.
Check the [OSAC Nightly Build workflow](https://github.com/osac-project/osac/actions/workflows/nightly-build.yaml)
for newer builds and use the newest successful run. The chart includes a
`values.schema.json` file and a `values-example.yaml` file.

Select an umbrella chart version from the release links above and export it
once. The following example uses the `osac-release/v0.0.62` tag from the
[OSAC tags page](https://github.com/osac-project/osac/tags):

```console
export OSAC_RELEASE_VERSION="0.0.62"
```

> **Note**
>
> The install preflight described in
> [Section 11.1](#111-the-install-preflight-stops-the-installation) is in
> charts built after 30 October 2026: releases after
> `osac-release/v0.0.63` and nightly builds from after that date. Earlier
> charts, including the 0.0.62 release and the 27 October nightly in the
> examples here, run a shell validation hook instead and do not behave as
> described there. To evaluate the preflight, choose a nightly from the
> workflow above that was built after 30 October 2026; its chart contains
> `templates/preflight/_engine.tpl`.
>
> Provider networking also needs a newer chart. The `osac-release/v0.0.62` and
> `osac-release/v0.0.63` charts have no `cudn_evpn` role, and their schema
> rejects `k8sManager: cudn_evpn`. For
> [Section 2.6](#26-evpn-host-networking-for-vmaas) and
> [Section 4.4](#44-installing-with-provider-networking), use a chart built after
> the network manager contract merged on 1 November 2026.

Use only the chart version, without either tag prefix. For evaluation, set
`OSAC_RELEASE_VERSION` to an exact nightly version from the workflow above;
the linked example uses `0.0.58-nightly.20261027.8402b1c.141.1`. Select the
version before retrieving chart values and keep it unchanged throughout the
installation. Run the remaining commands in the same shell session; they
use this variable for the chart version and matching source downloads.

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

In this release, provider networking is supported for VMaaS only. CaaS and
BMaaS run with provider networking disabled; provider networking for them
comes in a later release.

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
| [Streams for Apache Kafka](https://access.redhat.com/articles/6644711) | `stable` | `osac-kafka` | `kafka.enabled` | Fulfillment events and metering; see [Kafka configuration](kafka-configuration.md) for external broker requirements. |
| [OpenShift Virtualization](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/virtualization/installing) | `stable` | `openshift-cnv` | `cnv.enabled` | VMaaS |
| [LVM Storage](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html-single/storage/index#persistent-storage-using-lvms) | `stable-<cluster_minor>` | `openshift-storage` | `lvms.enabled` | VMaaS |
| [MetalLB Operator](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/networking_operators/metallb-operator) | `stable` | `metallb-system` | `metallb.enabled` | CaaS |
| [multicluster engine for Kubernetes Operator](https://docs.redhat.com/en/documentation/red_hat_advanced_cluster_management_for_kubernetes/2.13/html/clusters/cluster_mce_overview) | `stable-2.17` | `multicluster-engine` | `mce.enabled` | CaaS |
| Kubernetes NMState Operator | `stable` (`evpn.nmstate.channel`) | `openshift-nmstate` | `evpn.nmstate.install` | VMaaS with provider networking |
| EVPN host networking (FRR, `tenant-vtep`, BGP to the fabric) | Not applicable | `openshift-frr-k8s`, cluster-scoped | `evpn.enabled` | VMaaS with provider networking |

Notes on individual components:

- **cert-manager Operator for Red Hat OpenShift** installs into the
  `cert-manager-operator` namespace and creates its operands in the
  `cert-manager` namespace. The `certificates.cert-manager.io` CRD is a
  mandatory install preflight check.
- **trust-manager** distributes the CA bundle. To use an existing
  trust-manager installation, set `trustManager.upstream.enabled=true`.
- **`default-ca` `ClusterIssuer`** is the default for `service.certs.issuerRef`.
  You can select another issuer independently of `caIssuer.enabled`.
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
- **MetalLB Operator** provides a `LoadBalancer`-class implementation. CaaS
  uses it on the hub to give each hosted cluster's API its management VIP.
  Only CaaS needs it. Any solution that provides one works if you disable
  `metallb.enabled`.
- **multicluster engine for Kubernetes Operator** is required for
  agent-based cluster provisioning but is disabled by default. Set
  `mce.enabled=true` when this installation owns standalone MCE; the `caas-ci`
  infrastructure profile does so explicitly. Leave it `false` when Red Hat
  Advanced Cluster Management for Kubernetes (RHACM) or another MCE
  installation already owns it. The disabled state also prevents the phase-1
  chart from applying its temporary Assisted image override `ConfigMap` and
  compatibility RBAC.
- **Kubernetes NMState Operator** sets the uplink and VTEP addresses of the
  VMaaS nodes. Set `evpn.nmstate.install: false` when it is already installed.
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
  For end-to-end external-database wiring — identity and certificate
  requirements, and values — see the
  [external services support guide](external-services-support-guide.md).
- **An external secret store.** The bundled OpenBao instance is for testing and
  CI, not production. Before installing the `osac` chart, prepare Vault or
  OpenBao, its Keycloak client, and the Kubernetes Secret and CA bundle used by
  OSAC. See the
  [secrets management configuration guide](secrets-management-configuration.md).

Required for CaaS:

- **DNS credentials.** With the default DNS backend, Route 53
  (`DNS_CLASS: dns.route53.dns`), you need an `AWS_ACCESS_KEY_ID` value and an
  `AWS_SECRET_ACCESS_KEY` value that can manage the target hosted zone. To run
  without OSAC managing DNS (`dns.noop.dns`), you need no AWS credentials; see
  [Section 8.4](#84-installing-without-dns-management). For more information,
  see
  [`dns-backend.md`](https://github.com/osac-project/osac/blob/main/osac-installer/docs/dns-backend.md).
- **The CaaS prerequisites** that OSAC does not create: the management
  publication on the hub (MetalLB and routes), the BMaaS provisioning network,
  and, with `dns.route53.dns`, the public DNS zone `DNS_ZONE`.
- **Provider networking disabled.** In this release, CaaS runs with provider
  networking disabled, so you also need provider DNS and routes, for every
  Cluster, for `api` and `api-int` (to the management API VIP) and `*.apps`
  (to the guest routers on the workers); and a provisioning network that
  carries each Cluster's own traffic (between its workers, from the workers to
  the management endpoints, and inbound to its routers and API) despite the
  BMaaS host-isolation filters. OSAC does not check these.
- **Coming with CaaS networking in a later release:** provider routes and DNS
  from the NATGateway ExternalIPs to the management endpoints, and the hosted
  cluster's OAuth `Route` host resolvable and reachable from wherever
  ExternalIPs are reachable. Each hosted cluster will also reach its operator
  catalog through its NATGateway, to install MetalLB.

Required for networking, for every service:

- **A Fabric Manager.** Every deployment names one in
  `global.networking.fabricManager`, also with provider networking disabled,
  because the `NetworkClass` needs it. With provider networking enabled, you
  get from the provider the execution environment image, the settings and
  secrets, the registration manifest, and the values agreed for the
  installation. With `fabricManager: netris`, the installer registers the
  Netris Fabric Manager itself, so you apply no registration; you still need
  the Netris settings and secrets. With
  provider networking disabled, OSAC reads no registration, and you need none
  of these. See [Section 4.4](#44-installing-with-provider-networking) and the
  [Fabric Manager Guide](../networking/customer-fabric-manager-guide.md).
- **A connected deployment.** OSAC networking supports connected deployments
  only, not air-gapped or disconnected ones: the hub, the AAP job pods, and the
  provider's networking services and APIs reach each other.

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
  host name and port name to switch port, in its settings or inventory.

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
  worker is a bare-metal instance (the installer does not check this);
  multicluster engine (standalone, or provided by RHACM if it's installed);
  MetalLB or another `LoadBalancer`-class implementation; a DNS backend; the
  CaaS prerequisites in
  [Section 2.4](#24-credentials-and-external-services); and
  `aap.instanceGroups.clusterFulfillment` configuration. In this release, CaaS
  runs with provider networking disabled.
  Hosted control planes provision etcd persistent volume claims from the
  hub's default storage class (see [Section 2.1](#21-cluster-and-access)).
  For more information,
  see
  [`aap-configuration.md`](https://github.com/osac-project/osac/blob/main/osac-installer/docs/aap-configuration.md).
- **BMaaS** (`global.services.bmaas.enabled`) requires BareMetalOperator and a
  `Provisioning` custom resource with `spec.watchAllNamespaces: true` when
  either `bmf.metal3.enabled` or `bmf.netbox.enabled` is `true`, and the
  BMaaS prerequisites in
  [Section 2.4](#24-credentials-and-external-services). In this release,
  BMaaS runs with provider networking disabled.

### 2.6 EVPN host networking for VMaaS

With VMaaS and provider networking enabled, the Kubernetes Manager
`cudn_evpn` connects VMs to the provider's fabric over EVPN. The cluster that
runs the VMs needs EVPN host networking: FRR and route advertisements in the
cluster network, an uplink address and a VTEP address on each VMaaS node, the
`tenant-vtep` VTEP, and BGP sessions from each node to the fabric. The phase-1
installation configures this OpenShift side from the `evpn` values. The
provider configures the fabric side. With provider networking disabled, skip
this section.

EVPN host networking needs OVN-Kubernetes and OpenShift Container Platform
4.22 or later. For the phase-1 procedure, see "EVPN host networking (VMaaS)"
in the
[Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md#evpn-host-networking-vmaas).

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

With the Netris Fabric Manager, also note:

- SecurityGroups are documented no-ops on Netris. `cudn_evpn` enforces them
  with NetworkPolicy on the hub.
- Netris allocates the VNIs itself (the VPC's VNI is the `l3_vni`, the V-Net's
  VXLAN ID the `l2_vni`), so no OSAC VNI range applies.
- Netris reserves its private IP allocation (by default `10.0.0.0/8`). Add it
  to `global.networking.excludedIpv4Cidrs`, so no VirtualNetwork, Subnet or
  ExternalIPPool takes an address from it; use another private range such as
  `172.16.0.0/12` for tenants. The 10.x addresses in the examples of this
  guide are for other Fabric Managers.
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
  (known limitation: two different templates fail as final). Reserving a
  cluster ingress address is not supported on Netris: the task fails as final.
- ICMP SecurityGroup rules need the registration capability
  `security_group_icmp`. Netris declares it, although its SecurityGroup tasks
  are no-ops. `cudn_evpn` does not, because a NetworkPolicy cannot match ICMP,
  so a NetworkClass with Netris and `cudn_evpn` rejects ICMP: the fulfillment
  API answers `InvalidArgument` with "use protocol all, which includes ICMP".
  A NetworkClass with Netris alone (`k8sManager` empty) accepts ICMP.
- DNS: VMs use the cluster DNS that OVN-Kubernetes DHCP gives them on the
  primary UDN. OSAC has no DNS setting.

#### Who does what

- **Whoever prepares the cluster** sets the `evpn` values in the
  infrastructure values file passed to both `osac-deps` and `osac-infra`.
  `osac-deps` installs the NMState Operator. The `osac-infra`
  post-installation and post-upgrade job `osac-infra-configure-evpn` (in the
  `osac-infra` namespace) first checks that the cluster uses OVN-Kubernetes
  and that every listed node exists, and then does the following, in order:
  1. Unless `configureClusterNetwork` is `false`, it patches the cluster
     network operator for FRR, route advertisements, routing via host, and
     global IP forwarding. This restarts OVN-Kubernetes once.
  2. It creates the `nmstate` instance.
  3. It creates one `NodeNetworkConfigurationPolicy`, `osac-evpn-<node>`, per
     node. The policy sets each supplied uplink address and creates the dummy
     interface `evpn-vtep0` with the node's VTEP address.
  4. It creates the VTEP `tenant-vtep` for `vtepCIDR`.
  5. For each node, it creates the `FRRConfiguration`s `osac-evpn-<node>` and
     `osac-evpn-raw-<node>` in `openshift-frr-k8s`. Each node peers with every
     uplink's `peerAddress` and `peerASN`, advertises its VTEP address as a
     /32, accepts only /32 routes inside `bgp.acceptPrefixes`, and activates
     the EVPN address family.
  6. It creates the `RouteAdvertisements` `osac-evpn`, which selects the CUDNs
     labelled `evpn: "true"` that `cudn_evpn` creates later.
  7. With `bgp.waitForEstablished`, it waits until every node-to-fabric BGP
     session is `Established`.
- **The provider (fabric admin)** configures the fabric side for every VMaaS
  node:
  - the switch ports and the leaf end of each uplink;
  - a BGP neighbor for each node uplink, with the node's ASN;
  - acceptance of the node VTEP addresses in `vtepCIDR`, and advertisement of
    the fabric loopbacks inside `bgp.acceptPrefixes`;
  - the EVPN address family;
  - for every VNI that OSAC uses, import and export of the route target
    `0:<VNI>`: `0:<l2_vni>` for the MAC-VRF and `0:<l3_vni>` for the IP-VRF;
  - frames of at least 1550 bytes in the underlay and on the node NICs.

  This needs nothing from OpenShift, so the provider can configure it before
  OpenShift is installed. The BGP sessions stay down, and the fabric retries
  them, until the node side exists. The `osac-infra` installation then waits
  for them to become `Established`.

#### The `evpn` values

| Value | Meaning | Provided by |
|---|---|---|
| `evpn.enabled` | Configures EVPN host networking. Default `false`. | You |
| `evpn.nmstate.install` | Installs the NMState Operator. Set `false` when it is already installed. Default `true`. | Cluster admin |
| `evpn.nmstate.channel` | NMState Operator channel. Default `stable`. | Cluster admin |
| `evpn.configureClusterNetwork` | Applies the cluster network operator settings (one OVN-Kubernetes restart). Default `true`. | Cluster admin |
| `evpn.vtepCIDR` | Required. The range that holds every node's VTEP address. Fixed after installation. | Agreed with the fabric admin |
| `evpn.bgp.asn` | Required. The node ASN; a node can override it. It must match the ASN that the fabric admin configured for the node's BGP neighbors. | Agreed with the fabric admin |
| `evpn.bgp.acceptPrefixes` | Required. The fabric loopback (remote VTEP) ranges. Only /32 routes inside them are accepted. | Fabric admin |
| `evpn.bgp.waitForEstablished` | Fails the installation unless every BGP session becomes `Established`. Default `true`. | You |
| `evpn.bgp.timeoutSeconds` | Maximum wait for each BGP session, at least 60. Default `900`. | You |
| `evpn.nodes[].name` | Kubernetes node name. Add one entry for every node that runs VMs. | Cluster admin |
| `evpn.nodes[].vtepIP` | The node's VTEP address, unique and inside `vtepCIDR`. | Agreed with the fabric admin |
| `evpn.nodes[].asn` | Optional node ASN override. Defaults to `evpn.bgp.asn`. | Fabric admin |
| `evpn.nodes[].uplinks[].interface` | The node interface cabled to a fabric leaf. One entry per cable. | Fabric admin |
| `evpn.nodes[].uplinks[].address` | Optional IPv4 CIDR for the uplink. Omit it when the address is already configured. | Fabric admin |
| `evpn.nodes[].uplinks[].peerAddress` | The leaf's BGP address. | Fabric admin |
| `evpn.nodes[].uplinks[].peerASN` | The leaf's ASN. | Fabric admin |

When `evpn.enabled` is `true`, the chart requires `vtepCIDR`, `bgp.asn`, at
least one `acceptPrefixes` entry, and at least one node. It rejects duplicate
node names, duplicate VTEP addresses, and a repeated `peerAddress` on a node.
List `vtepCIDR`, the fabric loopback ranges, and the uplink ranges in
`global.networking.excludedIpv4Cidrs`
([Section 4.4.1](#441-choosing-the-networking-settings)).

```yaml
evpn:
  enabled: true
  nmstate: {install: true, channel: stable}
  configureClusterNetwork: true
  vtepCIDR: 192.168.51.0/24
  bgp:
    asn: 4209999999
    acceptPrefixes: [192.168.52.0/24]
    waitForEstablished: true
    timeoutSeconds: 900
  nodes:
    - name: worker-0
      vtepIP: 192.168.51.1
      uplinks:
        - interface: ens6
          address: 192.168.53.0/31
          peerAddress: 192.168.53.1
          peerASN: 4200300002
```

#### Timing

The `osac-infra` installation waits up to `timeoutSeconds` for each session,
so have the provider configure the fabric side first. The job's deadline is
3600 seconds plus `timeoutSeconds` times the number of uplinks. Give
`helm upgrade --install osac-infra ... --wait-for-jobs` a `--timeout` above
that deadline; `make install` adds 300 seconds. With
`waitForEstablished: false`, the installation does not wait, and the sessions
come up when the fabric is ready.

#### Adding or removing a node

Add a node by adding its entry to `evpn.nodes` and rerunning the `osac-infra`
installation, after the provider configures its fabric side. Remove a node by
deleting its entry and rerunning the installation. The job deletes that node's
`NodeNetworkConfigurationPolicy` and `FRRConfiguration`s. It does not remove
the addresses from the node; remove stale addresses separately if needed.

#### Separate workload cluster

EVPN host networking can also be configured on a separate workload cluster,
with `make prepare-remote-cluster ... EVPN_VALUES=...` (see the Helm
Deployment Guide). OSAC provider networking with VMs on a separate cluster is
not supported in this release (see [Section 9.4](#94-not-supported)).

#### Removing EVPN host networking

Uninstalling `osac` or `osac-infra` leaves the EVPN resources and the cluster
network operator settings in place. To remove the resources:

```console
oc delete routeadvertisements,vteps -l osac.openshift.io/evpn=true
oc delete frrconfigurations -n openshift-frr-k8s -l osac.openshift.io/evpn=true
oc delete nncp -l osac.openshift.io/evpn=true
```

Restore the cluster network operator settings separately if the cluster no
longer needs them.

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
| `osac` umbrella chart | Selected in [Section 1.1](#11-what-is-released) | Release-specific | `oci://ghcr.io/osac-project/charts/osac` |
| cert-manager Operator for Red Hat OpenShift | `stable-v1` | `v1.20.0` | `charts/osac-deps/values.yaml` |
| Red Hat Ansible Automation Platform | `stable-2.6-cluster-scoped` | `v2.6.0` | `charts/osac-deps/values.yaml` |
| LVM Storage | `stable-<cluster_minor>` | `v4.22.0` | `charts/osac-deps/values.yaml` |
| MetalLB | `stable` | `v4.22.0` | `charts/osac-deps/values.yaml` |
| Kubernetes NMState Operator | `stable` (`evpn.nmstate.channel`) | Not recorded | `charts/osac-deps/values.yaml` and `charts/osac-infra/values.yaml` |
| OpenShift Virtualization | `stable` | `v4.22.6` | `charts/osac-deps/values.yaml` |
| multicluster engine | `stable-2.17` | `v2.17.2` | `charts/osac-deps/values.yaml` |
| Streams for Apache Kafka | `stable` | `v3.2.1-10` | `charts/osac-deps/values.yaml` |
| trust-manager | `v0.20.0` (fixed) | `v0.20.0` | `charts/osac-infra/templates/trust-manager.yaml` |
| Envoy (Fulfillment Service sidecar) | `v1.33.0` (fixed) | `v1.33.0` | `values/*/instance.yaml` |
| OpenBao (bundled secret store) | `2.6.2` (fixed) | `2.6.2` | `charts/osac-infra/values.yaml` |
| Keycloak (bundled) | Not applicable | `26.6.4` | `charts/osac-infra/` |
| OSAC web console chart | Pinned by the selected umbrella chart | Release-specific | `charts/osac/Chart.yaml` |
| PostgreSQL | 18 or later | `18` (Keycloak database) | `fulfillment-service/docs/INSTALL.md` |

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

2. Create `my-values.yaml` for the `osac` chart. Copy the keys you need from
   the chart `values-example.yaml` file, which describes a production setup
   with an external PostgreSQL database, an external Keycloak, and pinned image
   tags. Leave out `dbMigrate.dbUrl`, which the chart schema rejects. Set
   `global.osacDeploymentId` to a stable identity for this deployment; the
   install fails without it.
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

3. Add the following hardening to `my-values.yaml`:
   ```yaml
   bundledPostgres:
     enabled: false
   ```

   Add the following to the `osac-infra` values. The `osac` chart schema rejects
   these keys:
   ```yaml
   keycloak:
     devFixtures:
       enabled: false
     adminUsername: <admin_user>
     adminPassword: <strong_password>
   bundledVault:
     enabled: false
   ```

4. For a production deployment, add `service.vault` to `my-values.yaml` using
   the [secrets management configuration guide](secrets-management-configuration.md#configure-the-osac-instance).

5. Add the service-specific value blocks (`global.services.*`,
   `global.networking`, `aap`, `metering`, and `bmf`)
   from [Section 6](#6-installation-workflows-by-service) for the service you
   are installing.

6. Add the networking values from
   [Section 4.4.3](#443-setting-the-networking-helm-values).

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
  even when the API certificates use public trust.
- For a production deployment, the external PostgreSQL server is ready for
  the chart's client-certificate authentication, and `dbInit.host` is set
  in `my-values.yaml`.
- For a production deployment, the external Vault-compatible is configured
  according to the [secrets management configuration guide](secrets-management-configuration.md).
- You completed [Section 4.1](#41-preparing-the-cluster) and
  [Section 4.2](#42-configuring-the-helm-values).
- With provider networking enabled, you completed the networking prerequisites
  in [Section 4.4.2](#442-prerequisites-for-provider-networking).
- With VMaaS and provider networking enabled, EVPN host networking is
  configured ([Section 2.6](#26-evpn-host-networking-for-vmaas)).

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

In this release, provider networking is supported for VMaaS only. When you
enable CaaS or BMaaS, set `provisioningEnabled: false`; provider networking for
them comes in a later release.

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

   The VTEP range and the VNI range are fixed after the first install;
   changing them means reinstalling. The node ASN (`evpn.bgp.asn`, and any
   `evpn.nodes[].asn`) only has to match what the fabric admin configured for
   the nodes' BGP neighbors; the `osac-infra` installation fails when a BGP
   session does not become `Established`. OSAC does not check the fabric's
   route targets: `cudn_evpn` sets `0:<VNI>` on every CUDN, and if the fabric
   does not import and export exactly these, routed traffic fails while every
   task succeeds (same-Subnet traffic can still work, as on the Netris lab,
   where the leaves use `<ASN>:<VNI>`).

2. **Configure EVPN host networking** (VMaaS only). Whoever prepares the
   cluster sets the `evpn` values of the `osac-deps` and `osac-infra` releases
   ([Section 2.6](#26-evpn-host-networking-for-vmaas)). While it is missing,
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
  for TCP and UDP. With `cudn_evpn`, do not use `PROTOCOL_ICMP`: `cudn_evpn`
  does not declare `security_group_icmp`, so the chart rejects it with
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
  `<prefix>-networking-operations-ig` (prefix `osac` by default). Their pods
  read every key of the `ConfigMap` and the `Secret` `network-fulfillment-ig`
  in `$NS` as environment variables. The installer writes both from
  `aap.instanceGroups.networkFulfillment.config` and `.secret` at every
  install and rollout, so never edit them with `oc`. The key names come from
  the Fabric Manager's design and start with its registration name in upper
  case. Job pods read them when they start, so a change applies to the next
  job.

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
| `global.networking.provisioningEnabled` | Never changes. To change it, reinstall. | The fulfillment service refuses to start: "global.networking.provisioningEnabled cannot change after installation; reinstall to change it". |
| Enabled services (`global.services.*`) | Enabling BMaaS or CaaS while any VirtualNetwork exists is not supported. Disabling CaaS or BMaaS changes a derived address plan; see the `addressPlan` row. | The fulfillment service refuses to start: "Enabling BMaaS or CaaS after networking resources exist is not supported in this release; delete every VirtualNetwork or reinstall". |
| `fabricManager`, `k8sManager`, `networkClass.defaults`, `networkClass.eastWestCapabilities` | Any difference from the `NetworkClass` fails the rollout. Set `k8sManager` at the first install if VMaaS will run. To change them, see [Section 4.4.8](#448-replacing-the-networkclass). | The installer: "NetworkClass <field>[, <field>] cannot change after installation; see Replacing the NetworkClass". |
| `networkClass.addressPlan` | Never changes. The installer compares the plan in force with the stored one at every rollout. With `addressPlan` set, that is your plan. Unset, the installer derives it again from the enabled services and `k8sManager` ([Table 4.1](#table-41-default-address-plan)), so enabling or disabling CaaS or BMaaS changes it. To change it, see [Section 4.4.8](#448-replacing-the-networkclass). To disable a service without replacing the `NetworkClass`, first set `addressPlan` to the stored plan. | The installer: "NetworkClass spec.address_plan cannot change after installation; see Replacing the NetworkClass". A plan that breaks a rule fails earlier with the rule's message, for example "global.networking.networkClass.addressPlan.lbVip must be greater than 0 when CaaS is enabled". |
| `excludedIpv4Cidrs` | You can add ranges that overlap none of these: the default `virtualNetworkIPv4CIDR`, a VirtualNetwork, or an ExternalIPPool. | The fulfillment service refuses to start: "excludedIpv4Cidrs range <CIDR> overlaps <kind> <ID or CIDR>". |
| VTEP range and VNI range (`evpn.vtepCIDR`, the Fabric Manager's VNI setting; none for Netris) | Fixed. To change them, reinstall. You can add or remove VMaaS nodes ([Section 2.6](#26-evpn-host-networking-for-vmaas)). | Nothing: agree on both sides before the first install ([Section 4.4.2](#442-prerequisites-for-provider-networking)). |
| CaaS `EXTERNAL_ACCESS_BASE_DOMAIN`, `DNS_ZONE`, and `DNS_CLASS` | Do not change them while any Cluster exists. | Nothing in this release. Coming with CaaS networking in a later release: the installer checks it. |
| OSAC release | Patch releases (x.y.z) of the same minor release roll out in place. Another minor release, up or down, does not: delete all workloads and networking resources, or reinstall. | While any VirtualNetwork, ExternalIPPool, or workload exists, the fulfillment service refuses to start: "in-place upgrade or downgrade between minor releases is not supported; delete all workloads and networking resources or reinstall". |

The installer's checks run in the post-installation and post-upgrade Job
`create-network-class`, which is kept when it fails. Read a failure with
`oc logs job/create-network-class -n "$NS"`. The fulfillment service's
refusals appear in `oc logs deploy/fulfillment-grpc-server -n "$NS"`. A value
that breaks a rule of
[Section 4.4.1](#441-choosing-the-networking-settings) can also fail
`helm install` or `helm upgrade` before anything is applied, with a message
that starts with `global.networking.`.

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
   - `ansible_role` is `<namespace>.<collection>.<role>`. A provider's role is
     never in an `osac.*` collection; Fabric Managers that OSAC ships, such as
     Netris, use one and are registered by the installer.
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
   - A key with an empty value counts as absent. Any other `data` key, such as
     the typo `networkTypes`, fails the `NetworkClass`.

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

1. Set the API address, an admin token of the `admin` service account that
   the OSAC chart creates, and the CA file:

   ```console
   FULFILLMENT_API="https://$(oc get route fulfillment-internal-api -n "$NS" -o jsonpath='{.spec.host}')"
   OSAC_ADMIN_TOKEN="$(oc create token admin -n "$NS" --duration 1h)"
   oc get configmap ca-bundle -n "$NS" -o jsonpath='{.data.bundle\.pem}' > ca-bundle.crt
   CA_FILE=ca-bundle.crt
   ```

   `admin` is the emergency administrator account of the private API;
   restrict who can mint its tokens.

2. Read the status:

   ```console
   curl --fail-with-body --cacert "$CA_FILE" --header "Authorization: Bearer ${OSAC_ADMIN_TOKEN}" \
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
tenant onboarding waits for a `READY` pool.

**Procedure**

- For each pool, run the following command with the variables of
  [Section 4.4.6](#446-checking-the-networkclass). Each pool has exactly one
  IPv4 CIDR:

  ```console
  curl --fail-with-body --cacert "$CA_FILE" --request POST \
      "${FULFILLMENT_API}/api/private/v1/external_ip_pools" \
      --header "Authorization: Bearer ${OSAC_ADMIN_TOKEN}" --header "Content-Type: application/json" \
      --data '{"metadata": {"name": "pool-public-1"}, "spec": {"cidrs": ["198.51.100.0/24"], "ipFamily": "IP_FAMILY_IPV4"}}'
  ```

  The CIDR holds no address your infrastructure uses. It overlaps no other
  pool, no `excludedIpv4Cidrs` range, no default `virtualNetworkIPv4CIDR`, no
  VirtualNetwork, and no Cluster pod or service CIDR. Size the pools for one
  `default-nat` address per Tenant, plus one address per ExternalIP; a Tenant
  whose onboarding finds every pool full gets `DefaultNetworkingReady`
  `False`. Coming with CaaS networking in a later release: Cluster
  ExternalIPs, for a Cluster's API and ingress.

**Verification**

- The pool becomes `READY` when the Fabric Manager's pool task succeeds:

  ```console
  curl --fail-with-body --cacert "$CA_FILE" --header "Authorization: Bearer ${OSAC_ADMIN_TOKEN}" \
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
2. Delete every ExternalIPPool, then the `NetworkClass`, with the variables of
   [Section 4.4.6](#446-checking-the-networkclass):

   ```console
   curl --fail-with-body --cacert "$CA_FILE" --header "Authorization: Bearer ${OSAC_ADMIN_TOKEN}" \
       --request DELETE "${FULFILLMENT_API}/api/private/v1/external_ip_pools/<pool_id>"
   curl --fail-with-body --cacert "$CA_FILE" --header "Authorization: Bearer ${OSAC_ADMIN_TOKEN}" \
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
| `operator.fulfillment.credentials.secretName`, `clientIdKey`, `clientSecretKey` | schema | Name and keys of the Kubernetes Secret holding the `osac-controller` Keycloak service-account credentials. `osac-infra` provisions it for bundled Keycloak; external Keycloak deployments must provide an equivalent Secret. The OSAC chart only references it. |
| `operator.controllers.tenant`, `operator.controllers.networking`, `operator.controllers.storage` | schema | Enable or disable individual reconcilers. Which services run is driven by `global.services.*`. |
| `operator.controllers.volume` | subchart | Enables the Volume reconciler. |
| `operator.configSecret.name`, `operator.configSecret.optional` | schema | Additional Operator configuration Secret. |
| `operator.hubAccess.enabled` | schema | Creates the hub-access `ClusterRole` that the Fulfillment Service binds to. |
| `operator.stall.preparingInfrastructureThreshold`, `operator.stall.controlPlaneStartingThreshold`, `operator.stall.workersJoiningThreshold` | schema | How long a cluster provisioning phase can wait before the Operator marks it stalled. |
| `operator.tenants[]` | subchart | Tenants pre-created at installation. `shared` is the built-in tenant. |

<a id="table-53-networking-globalnetworking"></a>
**Table 5.3. Networking (`global.networking.*`)**

The installer creates the one `NetworkClass` from these values. Fabric Manager
registrations of provider Fabric Managers are `ConfigMap` resources that you
apply, not Helm values; the installer registers `cudn_evpn` itself, and the
Netris Fabric Manager when `fabricManager` is `netris`. EVPN host networking is
configured by the `evpn` values of the phase-1 releases
([Section 2.6](#26-evpn-host-networking-for-vmaas)), not by these. See
[Section 4.4](#44-installing-with-provider-networking).

| Parameter | Source | Description | Default |
|---|---|---|---|
| `global.networking.provisioningEnabled` | schema | Provider networking. When `false`, OSAC runs no networking job, and networking resources become `READY` without connectivity. Fixed at the first install. | `true` |
| `global.networking.excludedIpv4Cidrs` | schema | IPv4 CIDRs that your infrastructure uses: management, provisioning, and node networks and, with EVPN host networking, the EVPN ranges. No VirtualNetwork or ExternalIPPool can overlap them. You can add ranges later (see [Table 4.2](#table-42-settings-fixed-at-installation)). | `[]` |
| `global.networking.fabricManager` | schema | The registration `name` of the Fabric Manager. Required for every deployment. | Not set |
| `global.networking.k8sManager` | schema | The OSAC Kubernetes Manager: `cudn_evpn`, or empty. Required for VMaaS. | Not set |
| `global.networking.networkClass.defaults.virtualNetworkIPv4CIDR`, `global.networking.networkClass.defaults.subnetIPv4CIDR` | schema | The CIDRs of every tenant's default VirtualNetwork and Subnet. The Subnet CIDR is inside the VirtualNetwork CIDR and meets the prefix limit of the address plan. The chart defaults suit evaluation; set them for your deployment, because they cannot change after installation. | `10.200.0.0/16` and `10.200.0.0/20` |
| `global.networking.networkClass.defaults.ingressRules`, `global.networking.networkClass.defaults.egressRules` | schema | Rules of every tenant's default SecurityGroup, as camelCase SecurityGroup rules, for example `{protocol: PROTOCOL_TCP, ipv4Cidr: 0.0.0.0/0, portFrom: 22, portTo: 22}`. Set them for your deployment; they cannot change after installation. | `[]` and one allow-all egress rule |
| `global.networking.networkClass.eastWestCapabilities.supportsEastWestEthernet` | schema | Enables Ethernet east-west FabricDomains. Needs BMaaS and a Fabric Manager that declares `eastWestEthernet`. | `false` |
| `global.networking.networkClass.addressPlan.overlay`, `global.networking.networkClass.addressPlan.fabric`, `global.networking.networkClass.addressPlan.lbVip` | schema | Optional shares of every Subnet. Set all three or none. Fixed at installation (Table 4.2). See [Section 4.4.1](#441-choosing-the-networking-settings). | Derived from the enabled services ([Table 4.1](#table-41-default-address-plan)) |

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
| `aap.bootstrap.enabled`, `aap.bootstrap.image`, `aap.bootstrap.backoffLimit` | The postinstallation bootstrap job that loads config-as-code. `aap.bootstrap.image` is also the base image that a provider builds its execution environment image on. |
| `aap.configAsCode.eeImage` | The execution environment and job pod image of every OSAC instance group. With a provider Fabric Manager, set the provider's image, built on the release's `aap.bootstrap.image`. See [Section 4.4.3](#443-setting-the-networking-helm-values). |
| `aap.configAsCode.manifestSecret` | Secret that holds `license.zip`. |
| `aap.configAsCode.secret` | Secret with config-as-code runtime flags. |
| `aap.configAsCode.projectGitUri`, `aap.configAsCode.projectGitBranch` | Git source for the Ansible content that the bootstrap job imports. |
| `aap.instanceGroups.clusterFulfillment.enabled`, `aap.instanceGroups.clusterFulfillment.config`, `aap.instanceGroups.clusterFulfillment.secret` | The `cluster-fulfillment` instance group for CaaS provisioning. See [Section 6.2](#62-installing-osac-for-caas). |
| `aap.instanceGroups.networkFulfillment.enabled`, `aap.instanceGroups.networkFulfillment.config`, `aap.instanceGroups.networkFulfillment.secret` | The settings (`config`, the `ConfigMap` `network-fulfillment-ig`) and secrets (`secret`, the `Secret` `network-fulfillment-ig`) of the Fabric Manager, which networking jobs read as environment variables. Leave `enabled: true` (the default); `false` writes both empty and is rejected with provider networking enabled. The installer writes both at every rollout. See [Section 4.4.3](#443-setting-the-networking-helm-values). |
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
| `bmf.env.aapUrl` | subchart | AAP controller URL that the Bare Metal Fulfillment Operator drives. In this release, BMaaS host networking is not driven by `global.networking.provisioningEnabled`. | Not applicable |
| `bmf.env.enableNetworkingProvisioning` | schema | Enables the Bare Metal Fulfillment Operator's legacy network-attachment and DHCP-lease playbooks. Keep `"false"`. Coming with BMaaS networking in a later release: provider networking for hosts through the Fabric Manager. | `"false"` |
| `bmf.bcm.enabled`, `bmf.bcm.url`, `bmf.bcm.cert`, `bmf.bcm.key`, `bmf.bcm.caCert`, `bmf.bcm.insecureSkipVerify`, `bmf.bcm.hostClass`, `bmf.bcm.bmhNamespace` | subchart | Base Command Manager (BCM) backend. | `false` |
| `bmf.secrets.inventoryConfig`, `bmf.secrets.managementConfig`, `bmf.secrets.osClouds`, `bmf.configMaps.profiles` | schema | Names of the inventory, management, and `clouds.yaml` Secrets and the profiles `ConfigMap`. | Default names |
| `operatorCrds.install` | schema | Install the OSAC CRDs. Set it to `false` if a cluster administrator manages them. | `true` |
| `kafka.enabled` | schema | Creates the Fulfillment Service's Strimzi user and copies its credentials. Set to `false` for an external Kafka connection; this does not disable Kafka use or metering's Strimzi resources. See [Kafka configuration](kafka-configuration.md). | `true` |
| `service.kafka.connection` | schema | ConfigMap/Secret mappings for the required `brokers`, `user`, and `password` parameters. Supply resources in the OSAC namespace. | `fulfillment-service-kafka` Secret |
| `metering.database.connection` | schema | Metering service database connection. The metering service is always deployed: billing is not optional, and the event publisher writes usage events unconditionally. Requires Strimzi-managed Kafka, a database connection, and `global.osacDeploymentId` (see [Table 5.1](#table-51-service-enablement-my-valuesyaml) and [Kafka configuration](kafka-configuration.md#metering)). | Secret-based default |
| `metering.heartbeat.interval`, `metering.m360Adapter.enabled`, `metering.m360Adapter.m360.apiUrl`, `metering.m360Adapter.m360.apiKeySecret` | subchart | Heartbeat round interval and the Monetize360 billing adapter. | Not applicable |
| `validation.enabled` | schema | Runs the install preflight. When `false`, every check is skipped and recorded as skipped in the report. | `true` |
| `preflight.mode` | schema | `enforce` stops the installation on a blocking problem. `report` records problems without stopping. `strict` also stops on warnings. See [Section 11.1](#111-the-install-preflight-stops-the-installation). | `enforce` |
| `preflight.skip.<check_id>` | schema | Skips one preflight check. The value is the reason, which the report records. | `{}` |
| `bundledVault.enabled`, `bundledVault.image` | `osac-infra` | In-cluster OpenBao for testing and CI. Not intended for production. | `false` and `ghcr.io/openbao/openbao:2.6.2` |
| `hubAccess.enabled` | schema | Creates hub-access RBAC and registers the local cluster as its own hub. Single-cluster development only. | `false` |
| `bundledPostgres.enabled` | schema | Bundled PostgreSQL for testing only; not intended for production. | `false` |
| `dbInit.host` | schema | Host that the `db-init` pre-installation hook connects to, to create the databases. Set it to your external PostgreSQL host for a production deployment. | `postgres.osac-infra.svc.cluster.local` |
| `clusterVersions.enabled`, `clusterVersions.versions[]` | schema | OpenShift Container Platform release images offered to hosted clusters. Each entry has `version`, `image`, and an optional `default`. An entry with `default: true` also needs `diskImage`. | `false` and `[]` |

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
the cluster. Every workflow also needs the networking values and steps of
[Section 4.4](#44-installing-with-provider-networking). Then follow
[Section 4](#4-installing-osac).

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

1. In `my-values.yaml`, enable the VMaaS tier and select the managers. VMaaS
   always needs the Kubernetes Manager `cudn_evpn`:

   ```yaml
   global:
     services: { vmaas: { enabled: true }, caas: { enabled: false }, bmaas: { enabled: false }, maas: { enabled: false } }
     networking:
       provisioningEnabled: true
       fabricManager: acme_fabric
       k8sManager: cudn_evpn
   lvms:
     enabled: true
   aap:
     instanceGroups:
       publishTemplates:
         enabled: true
       networkFulfillment:
         enabled: true
         config: {}  # add the selected managers' settings per their owner docs
   ```

   The OSAC chart does not deploy the OSAC CSI routing meta-driver. Provider CSI
   setup and tenant storage provisioning are handled separately by the storage
   workflow; see the [storage documentation](../../storage/README.md).

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

   The `publishTemplates` hook publishes the compute instance
   templates that VMaaS needs. Disable it only if the required
   templates are provisioned separately.

   `lvms: { enabled: true }` runs the `osac` chart's `register-local-storage`
   hook, which creates the `local` `StorageBackend` and `StorageTier` that the
   first `ComputeInstance` needs. Without it, provisioning fails with an empty
   `storage_tier_definitions` even though every pod looks healthy.

2. Add the `service.*`, `aap.configAsCode.*`, and database Secret settings to
   `my-values.yaml`, and the Keycloak hardening to the `osac-infra` values, from [Section 4.2](#42-configuring-the-helm-values).

3. Install OSAC. See [Section 4.3](#43-installing-osac).

4. With provider networking enabled, apply the Fabric Manager registration,
   check the `NetworkClass`, and create the ExternalIPPools. See
   [Section 4.4.5](#445-applying-the-fabric-manager-registration) to
   [Section 4.4.7](#447-creating-the-externalippools). With provider networking
   disabled, check the `NetworkClass`
   ([Section 4.4.6](#446-checking-the-networkclass)).

**Verification**

- Complete [Section 7](#7-verifying-the-installation), including step 5.
- Create a ComputeInstance through the OSAC API or `osac create computeinstance`
  and confirm that it reaches `RUNNING`. With provider networking enabled, the tenant's
  `DefaultNetworkingReady` condition must be `True` first:

  ```console
  oc get computeinstance -A
  ```

### 6.2 Installing OSAC for CaaS

The AWS Route 53 DNS backend is the default; it's independent of the Fabric
Manager. CaaS workers are bare-metal instances, so CaaS also needs BMaaS. In
this release, CaaS runs with provider networking disabled. Coming with CaaS
networking in a later release: CaaS with provider networking and a Fabric
Manager such as Netris.

**Prerequisites**

- multicluster engine is installed (standalone, or provided by RHACM).
- MetalLB (or another `LoadBalancer`-class implementation) and LVM Storage
  (or another dynamic storage class) are installed.
- The BMaaS prerequisites of [Section 6.3](#63-installing-osac-for-bmaas)
  are met.
- The hub cluster has a default storage class (see
  [Section 2.1](#21-cluster-and-access)). Hosted control planes provision
  etcd persistent volume claims from it.
- You have AWS Route 53 credentials for the target hosted zone, or you install
  without DNS management
  ([Section 8.4](#84-installing-without-dns-management)).
- The CaaS prerequisites in
  [Section 2.4](#24-credentials-and-external-services), including those for
  provider networking disabled, are in place.

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
       provisioningEnabled: false   # fixed at the first install
       fabricManager: acme_fabric   # required also with provider networking disabled
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
       publishTemplates:
         enabled: true
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

   - OSAC publishes each cluster's `api`, `api-int`, and `*.apps` records in
     the public zone `DNS_ZONE`, which defaults to
     `EXTERNAL_ACCESS_BASE_DOMAIN`. Unset, `HOSTED_CLUSTER_BASE_DOMAIN`
     defaults to `hosted.` followed by the hub's apps domain without its
     `apps.` prefix.
   - Do not change `EXTERNAL_ACCESS_BASE_DOMAIN`, `DNS_ZONE`, or `DNS_CLASS`
     while any Cluster exists. OSAC does not check this in this release.
   - Add `networkClass.defaults` from
     [Section 4.4.3](#443-setting-the-networking-helm-values), and the `bmf`
     values from [Section 6.3](#63-installing-osac-for-bmaas). With provider
     networking disabled, you need no image, Fabric Manager settings, or
     registration.

2. Put the AWS credentials in a separate values file that is excluded
   from version control:

   ```yaml
   aap:
     instanceGroups:
       clusterFulfillment:
         secret:
           AWS_ACCESS_KEY_ID: "<route53_access_key_id>"
           AWS_SECRET_ACCESS_KEY: "<route53_secret_access_key>"
   ```

3. Install OSAC (see [Section 4.3](#43-installing-osac)), passing every
   values file, for example `helm ... -f my-values.yaml -f my-secrets.local.yaml
   ...`. For more information, see
   [`aap-configuration.md`](https://github.com/osac-project/osac/blob/main/osac-installer/docs/aap-configuration.md)
   and
   [`dns-backend.md`](https://github.com/osac-project/osac/blob/main/osac-installer/docs/dns-backend.md).

4. Check the `NetworkClass`
   ([Section 4.4.6](#446-checking-the-networkclass)). With provider networking
   disabled, OSAC creates no ExternalIPs, so you create no ExternalIPPools.

**Verification**

- Complete [Section 7](#7-verifying-the-installation), including step 12.
- Create a Cluster and watch the AAP `cluster-fulfillment` job.

### 6.3 Installing OSAC for BMaaS

**Prerequisites**

- BareMetalOperator is installed, with a `Provisioning` custom resource that has
  `spec.watchAllNamespaces: true`. OSAC does not install these; the install
  preflight stops the installation if they are missing.
- The BMaaS prerequisites in
  [Section 2.4](#24-credentials-and-external-services) are in place.

**Procedure**

1. In `my-values.yaml`, enable the BMaaS tier and the Metal3 backend. In this
   release, BMaaS runs with provider networking disabled, so hosts stay on the
   provisioning network and OSAC runs no networking job:

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
       aapUrl: "http://osac-aap/api/controller"
       aapInsecureSkipVerify: "true"
   aap:
     instanceGroups:
       publishTemplates:
         enabled: false
   ```

   `fabricManager` is required even with provider networking disabled; you
   apply no registration.
   Setting `publishTemplates.enabled` to `false` is safe here
   because a BMaaS-only deployment does not consume templates
   published by this hook. Set `bmf.metal3.namespace` to the
   namespace where your `BareMetalHost` resources live.

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

3. Check the phase-1 Operators enabled for this deployment. Each expected CSV
   must be `Succeeded`:

   ```console
   oc get csv -A \
     -o custom-columns='NAMESPACE:.metadata.namespace,NAME:.metadata.name,PHASE:.status.phase' \
     | awk 'NR == 1 || $2 ~ /^(aap-operator|cert-manager-operator|kubernetes-nmstate-operator|kubevirt-hyperconverged-operator|lvms-operator|metallb-operator|multicluster-engine)\./'
   ```

   The AAP CSV is named `aap-operator` and can appear in multiple namespaces.
   `kubevirt-hyperconverged-operator`,
   `lvms-operator`, and `metallb-operator` are expected only when the matching
   `cnv.enabled`, `lvms.enabled`, and `metallb.enabled` values are `true`.
   These values default to `false`. MCE may be provided by RHACM or an existing
   installation; expect the `multicluster-engine` CSV when `mce.enabled` is
   `true`, and use the provider's health checks when it is externally managed.
   With `evpn.enabled` and `evpn.nmstate.install` true, also expect the
   `kubernetes-nmstate-operator` CSV in `openshift-nmstate`.

   When `kafka.enabled` is `true` in `osac-deps`, also check the CSV installed
   by its `amq-streams` Subscription. It must be `Succeeded`:

   ```console
   KAFKA_CSV=$(oc get subscription amq-streams -n osac-kafka -o jsonpath='{.status.installedCSV}')
   oc get csv "$KAFKA_CSV" -n osac-kafka
   ```

   The `osac-deps` toggle defaults to `false`. If another team manages the
   Kafka Operator, use that installation's namespace and Subscription.

4. Check the phase-1 operands used by this deployment. OSAC creates the
   `HyperConverged`, `LVMCluster`, and `IPAddressPool` resources only when their
   matching `cnv.enabled`, `lvms.enabled`, and `metallb.enabled` values are
   `true`. Run these checks when enabled; when disabled, an absent OSAC-managed
   resource is expected. A platform-managed resource may still exist.

   ```console
   # When caIssuer.enabled is true in osac-infra
   oc get clusterissuer default-ca
   # When cnv.enabled is true
   oc get hyperconverged -n openshift-cnv
   # When lvms.enabled is true
   oc get lvmcluster -n openshift-storage
   # When metallb.enabled is true
   oc get ipaddresspool -n metallb-system
   ```

   Verify the trust bundle separately from the certificate issuer. For every
   deployment, including customer-managed bundles and upstream trust-manager
   installations, check the `ConfigMap` named by
   `service.certs.caBundle.configMap` in the OSAC release namespace. Replace
   `<ca_bundle_configmap>` with that configured name:

   ```console
   oc get configmap <ca_bundle_configmap> -n <namespace> -o yaml
   ```

   Only for the OSAC-managed default trust-manager and bundle configuration,
   also check the `ca-bundle` `Bundle`:

   ```console
   oc get bundle ca-bundle
   ```

   When `kafka.enabled` is `true` in `osac-infra`, also check that the
   bundled Kafka resource is `Ready`:

   ```console
   oc get kafka osac-kafka -n osac-kafka
   oc wait kafka/osac-kafka -n osac-kafka --for=condition=Ready --timeout=10m
   ```

   When that toggle is `false`, an absent OSAC-managed Kafka resource is
   expected. For an externally managed Kafka cluster, use its provider's
   health checks and the [Kafka configuration guide](kafka-configuration.md).

   Always verify the issuer selected by `service.certs.issuerRef`,
   independently of `caIssuer.enabled`. For `kind: ClusterIssuer`, run
   `oc get clusterissuer <issuer_name>`; for `kind: Issuer`, run
   `oc get issuer <issuer_name> -n <namespace>`. Use the configured
   `service.certs.issuerRef.name` and the OSAC release namespace. The configured
   issuer must report `Ready: True`. The separate `default-ca` check above
   applies only when `caIssuer.enabled` is `true`.

5. VMaaS with provider networking: check EVPN host networking
   ([Section 2.6](#26-evpn-host-networking-for-vmaas)). Each node's BGP
   sessions must be `Established`, the `tenant-vtep` VTEP must be `Accepted`
   with reason `Allocated`, and each `NodeNetworkConfigurationPolicy` must be
   `Available`:

   ```console
   oc get bgpsessionstates.frrk8s.metallb.io -n openshift-frr-k8s
   oc get vtep.k8s.ovn.org tenant-vtep -o jsonpath='{.status.conditions}'
   oc get nncp -l osac.openshift.io/evpn=true
   ```

   The `RouteAdvertisements` `osac-evpn` can show `ConfigurationPending` until
   OSAC creates the first CUDN; that is expected.

6. Check the infrastructure layer in `osac-infra`. With
   `bundledVault.enabled: true`, expect a `Running` pod with the
   `app=openbao` label in this namespace;
   no bundled OpenBao pod is expected when the value is `false`.

   ```console
   oc get pods -n osac-infra -l app=openbao
   ```

   If `keycloak.enabled` is `true`, also check that the `keycloak-service` and
   `keycloak-database` pods are `Running`. Skip this check for an external
   Keycloak.

   ```console
   oc get pods -n keycloak
   ```

7. Confirm that the configured OIDC issuer responds. Set
   `OSAC_OIDC_ISSUER_URL` to the fully resolved `service.auth.issuerUrl`,
   including the realm path. With the default bundled Keycloak route this
   is `https://keycloak-keycloak.$DOMAIN/realms/osac`. For an external
   Keycloak or a custom `keycloak.route.hostname`, use its configured issuer
   URL.

   For the bundled Keycloak, extract the shared trust bundle and select it
   for this check. Adjust the ConfigMap name and PEM key if you configured
   different values:

   ```console
   oc get configmap ca-bundle -n <namespace> -o jsonpath='{.data.bundle\.pem}' > osac-ca-bundle.pem
   export OSAC_OIDC_CA_FILE="osac-ca-bundle.pem"
   ```

   For an external Keycloak or a route with a different ingress certificate,
   set `OSAC_OIDC_CA_FILE` to the CA bundle that signs the issuer's
   certificate. For the bundled Keycloak or an issuer with a custom CA,
   run:

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

8. Check the phase-2 pods. All pods must be `Running` or `Completed`, with no
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

   The bundled OpenBao pod is in `osac-infra` and is checked in step 6.

9. Check that every `Certificate` is `Ready`:

   ```console
   oc get certificate -n <namespace>
   ```

   The `osac-db-init` hook runs before installation completes. Helm deletes
   its Job after success, so it is expected to be absent from `oc get jobs -A`
   after a deployed install. If the install fails during database
   initialization, see [Section 11.5](#115-the-osac-db-init-hook-fails) for
   the retained Job's logs.

10. Check the AAP bootstrap job. The `osac-aap-bootstrap` job runs as a
    postinstallation hook and takes 10 to 40 minutes:

    ```console
    oc logs -f job/osac-aap-bootstrap -n <namespace>
    ```

11. Check API reachability and log in with the `osac` CLI. For the bundled
    CA or a custom CA, extract the API trust bundle and pass it to both
    commands. Before running the extraction command below, replace
    `ca-bundle` with the ConfigMap named by `service.certs.caBundle.configMap`
    if you changed the default. The example reads the `bundle.pem` key;
    if your bundle uses a different PEM key, adjust the JSONPath as well.

    ```console
    ROUTE=$(oc get route fulfillment-api -n <namespace> -o jsonpath='{.spec.host}')
    oc get configmap ca-bundle -n <namespace> -o jsonpath='{.data.bundle\.pem}' > osac-ca-bundle.pem
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

    The extracted bundle must include the CA that signs the API route's
    certificate. Use `--insecure` (skips certificate verification) only for
    evaluation, never in production.

12. CaaS only: check that the cluster templates were published:

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

13. Check the web console. Open the console URL in a browser. The URL must
    redirect to Keycloak and, after you log in, show the OSAC console. Skip
    this step if `ui.enabled` is `false`.

    ```console
    UI=$(oc get route osac-ui -n <namespace> -o jsonpath='{.spec.host}')
    curl -skI "https://$UI"
    ```

    The response must be `200` or a `302` redirect to Keycloak.

14. Check networking. The `NetworkClass` must be `READY`
    ([Section 4.4.6](#446-checking-the-networkclass)), and, with provider
    networking enabled, at least one ExternalIPPool must be `READY`
    ([Section 4.4.7](#447-creating-the-externalippools)). After you create a
    Tenant, its `DefaultNetworkingReady` condition must become `True`:

    ```console
    curl -s --cacert "$CA_FILE" -H "Authorization: Bearer $OSAC_ADMIN_TOKEN" \
      "${FULFILLMENT_API}/api/private/v1/tenants" \
      | jq '.items[] | select(.metadata.name=="<tenant_name>") | .status.conditions'
    ```

15. Run a service smoke test, as described in the Verification section of the
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
oc get route -n <namespace>
oc get route -n keycloak
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
  oc extract secret/osac-aap-admin-password -n <namespace> --to -
  ```

- **Keycloak admin console.** Log in with the `keycloak.adminUsername` and
  `keycloak.adminPassword` values.

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
   or a custom CA, extract the API trust bundle and pass it to the CLI. Adjust
   the ConfigMap name and PEM key if you configured different
   `service.certs.caBundle` values, as described in step 11 of
   [Section 7](#7-verifying-the-installation):

   ```console
   oc get configmap ca-bundle -n <namespace> -o jsonpath='{.data.bundle\.pem}' > osac-ca-bundle.pem
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
         config:
           DNS_CLASS: dns.noop.dns
           EXTERNAL_ACCESS_BASE_DOMAIN: "<guest_domain>"
           HOSTED_CLUSTER_BASE_DOMAIN: "<guest_domain>"
           EXTERNAL_ACCESS_SUPPORTED_BASE_DOMAINS: "<supported_domain>"
   ```

   Leave `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` unset.

2. Install or upgrade OSAC. See [Section 4.3](#43-installing-osac).

3. Confirm the values took:

   ```console
   oc get cm cluster-fulfillment-ig -n "$NS" -o yaml \
       | grep -E 'DNS_CLASS|EXTERNAL_ACCESS|HOSTED_CLUSTER'
   ```

4. Create these DNS records yourself; `dns.noop.dns` stops OSAC from managing
   DNS, it does not create records for you:

   ```text
   api.<cluster>.<guest_domain>       -> API endpoint
   *.apps.<cluster>.<guest_domain>    -> ingress endpoint
   ```

   The records must resolve from the hub, the AAP execution environment, the
   managed host, and the hosted cluster.

Do not change `DNS_CLASS`, `DNS_ZONE`, or `EXTERNAL_ACCESS_BASE_DOMAIN` while
any Cluster exists (see
[Table 4.2](#table-42-settings-fixed-at-installation)).

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
- Prerequisite Operators and infrastructure already present on the cluster —
  installed by whoever prepares the cluster using the
  [Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md).
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
  Provider networking is supported for VMaaS only, and only with a Fabric
  Manager that the provider has verified in a lab against this OSAC release
  (see part 6 of the
  [Fabric Manager Guide](../networking/customer-fabric-manager-guide.md#6-verify-in-a-lab-and-go-live)).
  OSAC does not enforce this.

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

### 9.4 Not supported

- OSAC networking in air-gapped or disconnected deployments, and IPv6 or
  dual-stack networking.
- VMs in a cluster other than the hub. EVPN host networking can be configured
  on a separate workload cluster
  (`make prepare-remote-cluster ... EVPN_VALUES=...`), but OSAC provider
  networking with VMs on a separate cluster is not supported in this release.
- Provider networking for CaaS and BMaaS. Coming with CaaS networking and
  with BMaaS networking in a later release.
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

   EVPN host networking belongs to the phase-1 `osac-infra` release, and
   uninstalling that release also leaves it in place. To remove it, see
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

If your platform team also installed the phase-1 prerequisites for this
deployment and they need to come down too, see "Uninstall" in the
[Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md#uninstall).
Do not remove shared prerequisites on a cluster used by other deployments.

---

## 11. Troubleshooting

Most successful hook Jobs are deleted by Helm. Failed Jobs are normally
retained for inspection, but some infrastructure Jobs have a cleanup timer.
If installation or upgrade fails during a phase-2 hook, list the retained
Jobs in the `osac` release namespace:

```console
oc get jobs -n <namespace> | grep -E 'db-init|publish-templates|bootstrap|network-class|create-hub'
```

When `keycloak.enabled`, `keycloak.route.publicIngress`, and
`caIssuer.enabled` are all `true` in `osac-infra`, also check its
route-patching hook. Use the infrastructure release's namespace if it
differs from `osac-infra`:

```console
oc get jobs -n osac-infra | grep -E 'patch-keycloak-route'
```

Check the Job's status for a failure. This hook's completed and failed Jobs
are eligible for cleanup one hour after they finish, so collect failure
logs promptly.

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
pods. The Streams for Apache Kafka `Subscription` uses a manual install plan;
approve its `InstallPlan` in the `osac-kafka` namespace.

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
# When caIssuer.enabled is true in osac-infra
oc get clusterissuer default-ca
oc logs -n cert-manager deploy/cert-manager
```

Always check the `Issuer` or `ClusterIssuer` selected by
`service.certs.issuerRef` as described in step 4 of
[Section 7](#7-verifying-the-installation), independently of
`caIssuer.enabled`. It must exist and report `Ready: True`. Check `default-ca`
separately only when `caIssuer.enabled` is `true`.

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
  values derive from `global.clusterDomain`; confirm that it was set at
  installation and matches `keycloak.route.hostname`. To check the value the
  running container actually received:

  ```console
  oc get deploy fulfillment-grpc-server -n <namespace> -o jsonpath='{.spec.template.spec.containers[0].args}' | tr ',' '\n' | grep -E 'issuer|idp'
  ```
- Missing `osac-db-config` or `osac-db-metering-config`: check the `osac`
  Helm release and its rendered Secrets. The chart creates these resources
  during installation. For missing database client-certificate Secrets,
  check their Certificate resources and the configured issuer; see
  [Section 11.6](#116-certificates-never-become-ready).
- Missing controller-credential Secrets: check the phase-1 Keycloak
  configuration or the credentials supplied for an external Keycloak.
- `lookup openbao.<namespace>.svc ... no such host` or
  `Failed to provision vault namespace`: `bundledVault.enabled` is `false` but
  `service.vault.endpoint` still points at the in-cluster OpenBao, or the
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
- Networking resources stuck in `PENDING`: see
  [Section 11.13](#1113-networking-problems).
- **CaaS:** watch the AAP `cluster-fulfillment` job for the failing task.
  Route 53 credential errors appear there.

### 11.13 Networking problems

- **`helm install` or `helm upgrade` fails before anything is applied** with
  a message that starts with `global.networking.`: a networking value breaks a
  rule of [Section 4.4.1](#441-choosing-the-networking-settings). Fix the
  value and run the command again.
- **A rollout fails after the first install** with one of the messages in
  [Table 4.2](#table-42-settings-fixed-at-installation): a fixed setting
  changed. Restore the earlier value. `provisioningEnabled` can change only
  with a reinstall. Read the installer's message with
  `oc logs job/create-network-class -n <namespace>`.
- **The `osac-infra` installation fails in the `osac-infra-configure-evpn`
  job.** Read `oc logs job/osac-infra-configure-evpn -n osac-infra`.
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
  `aap.instanceGroups.networkFulfillment`, EVPN host networking that is not
  configured or whose BGP sessions are down
  ([Section 2.6](#26-evpn-host-networking-for-vmaas), step 5 of
  [Section 7](#7-verifying-the-installation)), route targets that the fabric
  does not import and export, or a role missing from the image, and roll out; the next job
  reads the new values. On the hub, read the custom resource's `Ready`
  condition and `status.jobs`.
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
  `network-fulfillment`, `storage-operations`, or `publish-templates`.
  Networking jobs run in `<prefix>-networking-operations-ig` and read the
  `network-fulfillment-ig` `ConfigMap` and `Secret`.
- **Fabric Manager** — The network manager that configures the provider's
  fabric: an Ansible role, written by the provider or shipped by OSAC (for
  example Netris), registered with a `ConfigMap` (the installer creates it for
  an OSAC-shipped one) and selected by `global.networking.fabricManager`.
  Every deployment has one. See the
  [Fabric Manager Guide](../networking/customer-fabric-manager-guide.md).
- **Kubernetes Manager** — The OSAC network manager that configures hub
  networking for VMs, selected by `global.networking.k8sManager`. This release
  has `cudn_evpn`, for the `evpn` network type. Its host side (FRR,
  `tenant-vtep`, BGP to the fabric) is configured by the `evpn` values of
  `osac-infra` ([Section 2.6](#26-evpn-host-networking-for-vmaas)).
- **EVPN host networking** — The OpenShift side of the EVPN connection between
  the VMaaS nodes and the provider's fabric, configured by the `osac-infra`
  `evpn` values.
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
