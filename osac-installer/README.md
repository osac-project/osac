# OSAC Installer

This repository contains Kubernetes/OpenShift deployment configurations for the OSAC
platform, providing a fulfillment service framework for clusters and virtual machines.

> **Note:** Throughout this guide, `<project-name>` refers to your unique OSAC installation
> name, which is used as the namespace. Replace it with your chosen project name
> (e.g., `user1`, `team-a`, etc.).

## Overview

OSAC (Open Sovereign AI Cloud) provides a streamlined, self-service
framework for provisioning and managing OpenShift clusters and virtual machines. This
installer repository contains the Kubernetes/OpenShift deployment configurations needed
to deploy OSAC components on your infrastructure.

For detailed architecture, workflows, and design documentation, please refer to the
[OSAC documentation repository](https://github.com/osac-project/docs).

The OSAC platform provides:
- **Self-service provisioning** for clusters and virtual machines through a governed API
- **Template-based automation** using Red Hat Ansible Automation Platform
- **Multi-hub support** allowing multiple infrastructure hubs to be managed by a single fulfillment service
- **API access** via both gRPC and REST interfaces for integration with custom tools

This installer uses Helm to manage deployments via an umbrella chart (`charts/osac/`)
with per-environment values files under `values/`.

## OSAC Components

The OSAC platform relies on five core components to deliver governed self-service:

1. **Fulfillment Service:**
   The API and frontend entry point used to manage user requests and map them to specific
   templates.

2. **OSAC Operator:**
   An OpenShift operator residing on the Hub cluster (ACM/OCP-Virt). It orchestrates the
   lifecycle of clusters and VMs by coordinating between the Fulfillment Service and the
   automation backend.

3. **Console Proxy:**
   A Kubernetes aggregated API server that provides serial and VNC console access to
   ComputeInstance VMs. Deployed alongside the operator on each hub.

4. **Automation Backend (AAP):**
   Leverages the **Red Hat Ansible Automation Platform** to store and execute the custom
   template logic required for provisioning.

5. **Bare Metal Fulfillment Operator:**
   Kubernetes operator for managing bare-metal host pools. It watches **BareMetalPool**
   custom resources and reconciles them to their desired state by provisioning pools of
   bare-metal hosts organized by host type (e.g., GPU nodes, worker nodes). Uses profile
   templates to configure workflows and apply configuration parameters to selected hosts.
   It also contains a Kubernetes controller that manages bare-metal hosts via OpenStack
   Ironic. It watches **BareMetalInstance** CRs (defined by the Bare Metal Fulfillment Operator)
   and reconciles power state with Ironic.

### Prerequisites & Setup

> **System Requirements** This solution requires the following platforms to be installed
> and operational:
> * Red Hat OpenShift Advanced Cluster Management (RHACM)
> * Red Hat OpenShift Virtualization (OCP-Virt) - **Optional**: Only required for VM as a Service (VMaaS) support
> * Red Hat Ansible Automation Platform (AAP)
> * A network backend for bare metal provisioning: either **ESI** (Elastic System Infrastructure) or **Netris** (see [Network Backend Configuration](#network-backend-configuration-caas))

**Configuration Manifests**

The `/prerequisites` directory contains additional manifests required to configure the
target Hub cluster.

> **Warning: Cluster-Wide Impact** If you are using a shared cluster or are not the
> primary administrator, **do not apply these manifests without consultation.** These
> files modify cluster-wide settings. Please coordinate with the appropriate cluster
> administrators before proceeding.


### Prerequisites Summary

| **Category** | **Requirement** | **Notes / Details** |
|---------------|-----------------|----------------------|
| **Platform** | Red Hat OpenShift Container Platform (OCP) 4.17 or later | Must have cluster admin access to the hub cluster. |
| **Operators** | Red Hat Advanced Cluster Management (RHACM) 2.18+<br>Red Hat OpenShift Virtualization (OCP-Virt) 4.17+<br>Red Hat Ansible Automation Platform (AAP) 2.5+ | These must be installed and running prior to OSAC installation. |
| **CLI Tools** | `oc` (OpenShift CLI) v4.17+<br>`helm` v3.x<br>`git` | Ensure all CLIs are available in your `PATH`. |
| **Container Registry Access** | `registry.redhat.io` and `quay.io` | Verify credentials and pull secrets are valid in the target cluster namespace. |
| **Network / DNS** | Ingress route configured for OSAC services | Required for external access to fulfillment API and AAP UI. |
| **Authentication / IDM** | Organization Identity Provider (e.g., Keycloak, LDAP, RH-SSO) | Used for tenant and user identity mapping. |
| **Storage** | Dynamic storage class available (e.g., `ocs-storagecluster-cephfs`, `lvms-storage`) | Required for persistence of operator and AAP components. |
| **Permissions** | Cluster-admin access to deploy operators and create CRDs | Limited access users can only deploy into namespaces configured by the admin. |
| **License Files** | `license.zip` (AAP subscription) | Must be placed in your values directory (e.g., `values/<env>/license.zip`). |
| **Internet Access** | Outbound access to GitHub and `ghcr.io` (for fetching chart dependencies, OCI charts, and releases) | Required during installation and updates. |


## Installation

### Obtaining an AAP License (Subscription Manifest)

The AAP license is a **Subscription Manifest** (a `.zip` file), not a key file. To
obtain it:

1. **Log in** to the [Red Hat Customer Portal](https://access.redhat.com/).
2. **Navigate** to **Subscriptions** > **Subscription Allocations**.
3. **Create or select an allocation:** If you haven't created one, click
   "New Subscription Allocation" and set the type (usually "Satellite 6.x").
4. **Add entitlements:** Click on your allocation, go to the **Subscriptions** tab,
   and add your Ansible Automation Platform subscriptions.
5. **Download:** Click **Export Manifest** to download the `.zip` file.

Place the downloaded `license.zip` file in your values directory (e.g., `values/dev/license.zip`).

### Pre-Installation Steps

#### 1. Build Chart Dependencies

In the `osac` mono-repo, component charts are sibling directories referenced via
`file://` in the umbrella `Chart.yaml`. Build dependencies before installing:

```bash
make helm-deps
```

#### 2. Populate Local Secrets

Ensure your values directory contains the necessary secret files:

- **AAP License:** Place `license.zip` in `values/<env>/`
- **Pull Secret:** Place `pull-secret.json` in `values/<env>/`

### Installation

OSAC installs in three phases via `make install`. Each phase's outputs are
the next phase's inputs — this is required because Helm validates all templates
before applying any, and later phases depend on CRDs and secrets that earlier
phases create. See [docs/helm-deployment-guide.md](docs/helm-deployment-guide.md)
for a detailed explanation.

```bash
# Full install (infra + osac)
make install PLATFORM=openshift PROFILE=<profile> NS=<namespace>

# Or run phases individually:
make install-infra PLATFORM=openshift PROFILE=<profile> NS=<namespace>   # Infrastructure (operators, certs, Keycloak)
make install-osac  PLATFORM=openshift PROFILE=<profile> NS=<namespace>   # OSAC application
```

| Variable | Description |
|----------|-------------|
| `PLATFORM` | `kind` or `openshift` (required) |
| `PROFILE` | `dev`, `dev-full`, `vmaas-ci`, `bmaas-ci`, `caas-ci`, or `full-ci` (required; `dev-full` is kind only) |
| `NS` | Target namespace (required) |
| `EXTRA_HELM_ARGS` | Extra `--set`/`--set-string` args appended to helm commands |

#### Full local dev environment (`PROFILE=dev-full`, kind only)

`PROFILE=dev` on kind stands up only the control plane (cert-manager,
envoy-gateway, PostgreSQL, Keycloak, OpenBao, fulfillment-service, operator) —
the same footprint the integration tests use. `PROFILE=dev-full` is a **superset**:
it runs that identical chart-based install, then layers on everything needed for
the end-to-end "create a VM from the UI" experience:

```bash
make install PLATFORM=kind PROFILE=dev-full NS=osac
```

On top of `dev`, `dev-full` adds (via `scripts/dev-full/`, orchestrated by the
`install-devstack` target):

- **Virtualization** — Multus CNI + bridge plugin, KubeVirt (operator + CR, `l2bridge`
  binding), CDI
- **AWX** — the open-source AAP backend the operator drives: awx-operator + instance,
  configured with an inventory, a project (`github.com/osac-project/osac.git`,
  playbooks under `osac-aap/`), job templates, a Kubernetes credential, and the
  `awx-token` secret the operator reads
- **OSAC UI** — deployed directly (the chart's `ui.enabled` uses an OpenShift Route,
  unusable on kind) and routed through the shared Envoy Gateway
- **Seeded catalog** — a `fedora` disk image, `u1-small/medium/large` instance types,
  the `osac.templates.ocp_virt_vm` template, and a `linux-vm` catalog item (shared/global
  objects; networking is per-tenant and auto-provisioned, see below)
- **Ready-to-use tenant** — `provision-tenant.sh` creates a DB tenant (`tenant1`) via the
  private gRPC Tenants API, a matching enabled Keycloak organization, and adds the dev
  users (`tenant1_user`, `tenant1_admin`) as organization members so their tokens carry
  the `organization` claim needed to create resources. Creating the tenant auto-provisions
  its default VirtualNetwork + Subnet + SecurityGroup via tenant onboarding, so those are
  ready without manual seeding.

The `dev-full` overlay also sets `operator.controllers.networkingProvisioning=false`
so networking resources reconcile to READY without a real fabric (kind has none).

**Prerequisites** (beyond the base tools) — enforced by `scripts/dev-full/kind-runtime.sh check`:

- A **rootful** container runtime, because KubeVirt chowns `/dev/kvm`:
  - **Linux host** — rootful podman (invoked via `sudo`) or Docker
  - **Linux + Distrobox** — the rootful podman host socket (`/run/podman/podman.sock`);
    install the drop-in at `scripts/dev-full/manifests/podman-socket-rootful.conf`
  - **macOS** — Docker Desktop (auto-detected)
- **`/dev/kvm`** present (Linux), **`fs.inotify.max_user_instances >= 256`**, and
  `kind`, `helm`, `kubectl`, `jq`, `curl`, `openssl`, `python3` on `PATH`
- Override runtime detection with `KIND_EXPERIMENTAL_PROVIDER=docker|podman`

**Endpoints** (via the kind port mappings; every `*.localhost` name resolves to
127.0.0.1 automatically, so no `/etc/hosts` editing is needed):

- OSAC UI — `http://ui.osac.localhost:8080`
- AWX UI — `http://awx.awx.localhost:8080` (admin password:
  `kubectl -n awx get secret awx-admin-password -o jsonpath='{.data.password}' | base64 -d`)
- Keycloak — `https://keycloak.osac.localhost:8443`
- OSAC API — `https://fulfillment-api.osac.localhost:8443` (TLS Passthrough, SNI via Envoy)

**Log in** to the UI as `tenant1_user` (or `tenant1_admin`). The password is the
Keycloak dev-fixtures `default-user-password`:
`kubectl -n keycloak get secret keycloak-admin-credentials -o jsonpath='{.data.default-user-password}' | base64 -d`.
Both users are members of the `tenant1` organization, so they can immediately create
compute instances on the auto-provisioned default network.

Tear down everything (including the kind cluster, via the same runtime wrapper):

```bash
make uninstall PLATFORM=kind PROFILE=dev-full NS=osac
```

#### Configure Values

Each profile has two values files under `values/<profile>/`:

```bash
mkdir -p values/<project-name>
cp values/dev/infra.yaml values/<project-name>/infra.yaml
cp values/dev/instance.yaml values/<project-name>/instance.yaml
# Edit to match your cluster (see values file comments for guidance)
```

Prerequisites (cert-manager, AAP, LVMS, MetalLB, CNV, MCE) are installed
automatically by Phase 1. Each is gated by a values toggle (e.g.,
`certManager.enabled: true`). See [prerequisites/README.md](prerequisites/README.md)
for details on what each prerequisite provides.

#### AAP Configuration

AAP instance groups carry backend credentials for provisioning jobs.
Configure via Helm values under `aap.instanceGroups.*` in your values file.

See [docs/aap-configuration.md](docs/aap-configuration.md) for details.

#### Network Backend Configuration (CaaS)

By default the network backend is **ESI**. To switch to **Netris**, set
the Netris-specific values in your values file under `aap.instanceGroups.clusterFulfillment`.

See [docs/network-backend.md](docs/network-backend.md) for Netris-specific
variables and the `NETRIS_RESOURCE_CLASS_MAP` format.

#### DNS Backend Configuration (CaaS)

DNS record management uses a pluggable backend. The default is **AWS Route 53**.

See [docs/dns-backend.md](docs/dns-backend.md) for backend details, the
interface contract, and how to add a new provider.

#### Verify

```bash
helm status osac -n <project-name>
oc get pods -n <project-name>
oc logs -f job/osac-aap-bootstrap -n <project-name>
```

#### Upgrading

```bash
helm upgrade osac charts/osac/ \
  --namespace <project-name> \
  --values values/<project-name>/values.yaml \
  --timeout 40m \
  --wait
```

The post-upgrade hook re-publishes cluster templates automatically. This is
idempotent - existing templates are updated via PATCH while new templates
are created via POST.

#### Uninstalling

```bash
make uninstall
```

> **Note:** CRDs are preserved after uninstall (they have the
> `helm.sh/resource-policy: keep` annotation). To remove them manually:
> `oc delete crd -l app.kubernetes.io/part-of=osac`

#### Makefile Targets

```bash
make install       PLATFORM=... PROFILE=... NS=...  # Full install (infra + osac)
make install-infra PLATFORM=... PROFILE=... NS=...  # Infrastructure only
make install-osac  PLATFORM=... PROFILE=... NS=...  # OSAC application only
make uninstall     PLATFORM=... PROFILE=... NS=...  # Full uninstall
make test          PLATFORM=... PROFILE=... NS=... SUITE=...  # Integration tests
make helm-lint                                       # Lint all charts
make helm-template       # Dry-run render all templates
make helm-validate                                   # Lint + template (full validation)
make sync-charts         # Rebuild chart dependencies (legacy alias; runs helm dependency build)
```

### Monitor Progress

```bash
# Monitor pod creation and startup
$ watch oc get -n <project-name> pods
```

Once `helm install` completes successfully (including all post-install hooks),
OSAC is ready for use. Cluster templates are published automatically by a
post-install hook that runs after the AAP bootstrap job.

## OSAC CLI: Setup & Usage

To install the CLI and register a hub, follow these steps:

### 1. Install the Binary

Download the latest release and make it executable.

```bash
# Adjust URL for the latest version as needed
$ curl -L -o osac \
    https://github.com/osac-project/fulfillment-service/releases/latest/download/osac_Linux_x86_64
$ chmod +x osac

# Optional: Move to your path
$ sudo mv osac /usr/local/bin/
```

### 2. Log in to the Service

Authenticate with the fulfillment API. You will need the route address and a valid
token generation script.

```bash
$ osac login \
    --address <your-fulfillment-route-url> \
    --token-script "oc create token fulfillment-controller -n <project-name> \
    --duration 1h --as system:admin" \
    --insecure
```

> **Tip:** Retrieve your route URL using: `oc get routes -n <project-name>`

### 3. Register the Hub

To allow the OSAC operator to communicate with the fulfillment service, you must
obtain the kubeconfig and register the hub. The script located at
`scripts/create-hub-access-kubeconfig.sh` demonstrates how to generate the kubeconfig
for a hub.

```bash
# Generate the kubeconfig
$ ./scripts/create-hub-access-kubeconfig.sh

# Register the Hub
$ osac create hub \
    --kubeconfig=kubeconfig.hub-access \
    --id <hub-name> \
    --namespace <project-name>
```

### 4. Use the CLI

Once configured, you can use the OSAC CLI to manage clusters and virtual machines.
For detailed usage instructions and command reference, see
[OSAC-CLI-HOWTO.md](OSAC-CLI-HOWTO.md).

## Accessing Ansible Automation Platform

After deployment, you can access the AAP web interface to monitor jobs and manage automation:

### Get the AAP URL

```bash
$ oc get route -n <project-name> | grep osac-aap
```

> **Note:** The main AAP URL will be something like: `https://osac-aap-<project-name>.apps.your-cluster.com`

### Get the AAP Admin Password

```bash
# Extract the admin password
$ oc extract secret/osac-aap-admin-password -n <project-name> --to -
```

### Login to AAP

- Open the AAP controller URL in your browser
- Username: `admin`
- Password: (from the previous step)

### AAP API Token

The OSAC operator requires an API token to communicate with AAP. The
`create-api-token` Helm hook creates this automatically during `make install-osac`.
The token is stored in the `osac-aap-api-token` Secret.

## Tearing Down OSAC

To completely remove an OSAC deployment and all its prerequisites, use the teardown script:

```bash
# Using defaults (namespace: osac)
$ ./scripts/teardown.sh

# Or specify your namespace
$ INSTALLER_NAMESPACE=<project-name> ./scripts/teardown.sh

# Include all optional services in teardown (must match what was used during setup)
$ EXTRA_SERVICES=true INSTALLER_NAMESPACE=<project-name> ./scripts/teardown.sh
```

The script removes resources in reverse order:
1. OSAC CRs (while operator is running for finalizer processing)
2. Helm release and project namespace
3. Keycloak
4. AAP operator
5. Multicluster Engine and AgentServiceConfig (if `MCE_SERVICE=true`)
6. OpenShift Virtualization (if `VIRT_SERVICE=true`)
7. LVMS storage service (if `STORAGE_SERVICE=true`)
8. MetalLB ingress service (if `INGRESS_SERVICE=true`)
9. CA issuer, trust-manager, and cert-manager
10. Stale API services and CRD cleanup

> **Warning:** This removes **all** prerequisite operators and their namespaces. If other
> workloads on the cluster depend on these operators (e.g., cert-manager, MetalLB), do not
> run this script. Instead, manually uninstall:
> ```bash
> $ helm uninstall osac -n <project-name>
> $ oc delete namespace <project-name>
> ```

## Troubleshooting

### Common Issues

1. **cert-manager not ready**: Ensure cert-manager operator is installed and running
2. **Certificate issues**: Check cert-manager logs and certificate status
3. **ImagePullBackOff errors**: Verify registry credentials and image string
4. **Cluster templates missing or incomplete**: Cluster templates are published
   automatically by a Helm post-install hook (`osac-publish-templates`). If
   `osac get clustertemplates` returns missing or unexpected results, check the
   hook pod logs:
   `oc logs job/osac-publish-templates -n <project-name>`.
   See [docs/helm-deployment-guide.md](docs/helm-deployment-guide.md#template-publish-hook-failing)
   for details.

### Debug Commands

```bash
# Check certificate status
$ oc describe certificate -n <project-name>

# Check pod events
$ oc describe pod -n <project-name> <pod-name>

# Check service endpoints
$ oc get endpoints -n <project-name>

# View component logs
$ oc logs -n <project-name> deployment/fulfillment-service -c server --tail=100

# Get all events in namespace
$ oc get events -n <project-name> --sort-by=.metadata.creationTimestamp
```

## Support

For issues and questions:
- Check the troubleshooting section above
- Review component logs for error messages
- Verify prerequisites are properly installed
- Open issues in the [osac-project/osac](https://github.com/osac-project/osac) repository

## License

This project is licensed under the [Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0).
