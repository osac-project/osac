# OSAC Kind Development Environment

A lightweight kind-based dev environment for OSAC, replacing the full OpenShift
dependency for local development and testing.

## What This Gives You

A single-node kind cluster running the full OSAC stack with real KubeVirt and AWX:

| Component | Version | Status |
|-----------|---------|--------|
| cert-manager | v1.20.0 | Fully working |
| trust-manager | v0.22.0 | Fully working |
| Envoy Gateway (Gateway API) | v1.6.5 | Fully working |
| Authorino (AuthZ) | v0.23.1 | Fully working |
| PostgreSQL (mTLS) | — | Fully working |
| Keycloak (OIDC) | — | Fully working |
| Fulfillment Service (gRPC + REST) | latest | Fully working |
| OSAC Operator | latest | Fully working (AWX backend) |
| OSAC UI | latest | Fully working |
| KubeVirt | latest stable | Fully working (KVM accelerated) |
| CDI (Containerized Data Importer) | latest | Fully working |
| Multus CNI | latest | Fully working |
| AWX (AAP substitute) | latest | Fully working |
| Fake CRDs (HyperShift, OVN-K) | — | Stubs only |

**Setup time**: ~25 minutes from scratch (see [Installation Timeline](#installation-timeline)).

## Prerequisites

- **Docker** (macOS — Docker Desktop, auto-detected) or **podman** (Linux — rootful, see below)
- **kind** >= v0.20 (`go install sigs.k8s.io/kind@latest`)
- **helm** >= v3.10
- **kubectl**
- **openssl** (for CA generation)
- **python3** with `requests` module (`pip install requests`) — for AWX configuration
- **grpcurl** (optional — for automatic hub registration)

### System Tuning (one-time, Linux)

```bash
# kind nodes need many inotify watchers — need >= 256
sudo sysctl fs.inotify.max_user_instances=512
# Persist across reboots:
echo 'fs.inotify.max_user_instances=512' | sudo tee /etc/sysctl.d/99-kind-inotify.conf
sudo sysctl --system
```

### Rootful Podman (Linux)

KubeVirt requires rootful podman for KVM device access. The setup script
uses `sudo` automatically on Linux (or the rootful socket in Distrobox).

For **Distrobox** users, install the systemd socket override on the host:

```bash
sudo install -d /etc/systemd/system/podman.socket.d
sudo install -m 0644 kind-dev/podman-socket-rootful.conf \
  /etc/systemd/system/podman.socket.d/rootful-group.conf
sudo chgrp wheel /run/podman && sudo chmod 710 /run/podman
sudo systemctl daemon-reload && sudo systemctl restart podman.socket
```

### KVM Requirement

Host must have `/dev/kvm` available for KubeVirt (Intel VT-x / AMD-V):

```bash
ls /dev/kvm && grep -c -E 'vmx|svm' /proc/cpuinfo
```

No `/etc/hosts` entries needed — services are accessed via `*.localhost`
hostnames which resolve automatically via systemd-resolved (Linux) or
natively on macOS Sonoma+. The setup script adds a CoreDNS rewrite rule
so the same hostnames resolve inside pods too.

## Quick Start

```bash
cd kind-dev/

# Full setup (cluster + infra + OSAC)
./setup.sh

# Infrastructure only (no OSAC deployment)
./setup.sh --skip-osac

# Cluster only
./setup.sh --cluster-only

# Tear down
./teardown.sh
```

## Architecture

```
Laptop → api.osac.localhost:8443 (resolves to 127.0.0.1 via systemd-resolved)
       → Kind NodePort 30443
       → Envoy Gateway (TLS Passthrough, SNI match)
       → TLSRoute → fulfillment-api Service

Pods   → api.osac.localhost (CoreDNS rewrites to fulfillment-api.osac.svc.cluster.local)
       → ClusterIP directly
```

The setup mirrors the fulfillment-service integration test infrastructure
(see `fulfillment-service/internal/testing/kind.go`) but extracts it into
standalone scripts that any developer can run.

### Port Mappings

| Host Port | Container Port | Service |
|-----------|---------------|---------|
| 8443 | 30443 | Envoy Gateway (HTTPS ingress) |
| 8080 | 30080 | Envoy Gateway (HTTP ingress) |

### Hostnames

| Hostname | Purpose |
|----------|---------|
| `api.osac.localhost:8443` | Fulfillment API (external) |
| `internal-api.osac.localhost:8443` | Fulfillment API (internal/admin) |
| `keycloak.keycloak.localhost:8443` | Keycloak admin UI and OIDC |
| `ui.osac.localhost:8080` | OSAC UI web console |
| `awx.awx.localhost:8080` | AWX web UI (admin/password) |

## Research Findings

### OLM: Not Required

The production osac-installer uses OLM (Operator Lifecycle Manager) to deploy
prerequisites as OLM Subscriptions from the `redhat-operators` catalog. However,
**OLM is not needed for a kind dev environment**:

- cert-manager, trust-manager, Envoy Gateway: installed directly via Helm
- Authorino: installed from raw manifests
- OSAC operator + fulfillment-service: deployed via their own Helm charts

OLM adds several pods (olm-operator, catalog-operator, packageserver) and
complexity. Skip it unless you specifically need to test OLM-based installation.

### AAP: AWX (Open-Source Upstream)

The setup script installs AWX (open-source upstream of Red Hat AAP) via the
awx-operator Helm chart. AWX provides the full provisioning backend:

- The osac-operator calls AWX job templates for compute and networking operations
- Real `osac-aap` playbooks run on AWX with a pod-network override for kind
- Compute instance creation triggers KubeVirt VM provisioning end-to-end
- Networking templates run as no-ops (kind has no real networking backend)

AWX needs a Kubernetes credential to reach the cluster API. The setup script
creates this automatically along with all required job templates.

### KubeVirt: Full KVM-Accelerated VMs

The setup script installs real KubeVirt with KVM hardware acceleration,
plus CDI (Containerized Data Importer) and Multus CNI. The osac-operator's
ComputeInstance controller creates real VirtualMachine CRs that boot actual
guest VMs.

Requires `/dev/kvm` on the host (Intel VT-x / AMD-V) and rootful podman
on Linux. See [KVM Requirement](#kvm-requirement) above.

### Multiple Workers: Not Needed

A single control-plane node is sufficient. It runs all workloads (control plane +
OSAC services) on one node, which is simpler and uses fewer resources. Workers
would only be useful if you need to test node scheduling, taints/tolerations, or
run heavyweight workloads alongside the control plane.

## Customization

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CLUSTER_NAME` | `osac-dev` | Kind cluster name |
| `OSAC_NAMESPACE` | `osac` | Namespace for OSAC services |
| `KEYCLOAK_NAMESPACE` | `keycloak` | Namespace for Keycloak |

### Loading Custom Images

```bash
# Build and load a local fulfillment-service image
cd fulfillment-service
podman build -t fulfillment-service:dev .
podman save fulfillment-service:dev -o /tmp/fs.tar
kind load image-archive /tmp/fs.tar --name osac-dev

# Update the deployment
helm upgrade fulfillment-service charts/service \
  --namespace osac \
  --reuse-values \
  --set images.service=fulfillment-service:dev
```

**Important**: Use `podman save` + `kind load image-archive`, not
`kind load docker-image` (broken with podman — see
[kind#3945](https://github.com/kubernetes-sigs/kind/issues/3945)).

## Comparison with Existing Tools

| | kind-dev | cluster-tool | osac-installer |
|---|---------|-------------|---------------|
| **Target** | Local dev laptop | Baremetal server | OpenShift cluster |
| **Cluster type** | kind (containers) | SNO VM (libvirt) | OpenShift |
| **Setup time** | ~25 min | ~5 min (from snapshot) | ~30-45 min |
| **Resources** | ~8 GB RAM | 64+ GB RAM | Full OCP cluster |
| **AAP** | AWX (open-source) | Full | Full |
| **VMs** | Full KubeVirt (KVM) | Full KubeVirt | Full KubeVirt |
| **Use case** | Full-stack dev | Full integration | Production-like |

## Installation Timeline

Measured on a developer laptop (times are approximate):

| Phase | Duration | Cumulative |
|-------|----------|------------|
| Kind cluster + CoreDNS | ~0.5 min | 0.5 min |
| cert-manager + trust-manager | ~1 min | 1.5 min |
| Envoy Gateway + Authorino | ~1 min | 2.5 min |
| PostgreSQL + DB resources | ~1 min | 3.5 min |
| Keycloak | ~3 min | 6.5 min |
| OSAC umbrella chart + UI | ~2 min | 8.5 min |
| Multus CNI | ~0.5 min | 9 min |
| KubeVirt (operator + virt-*) | ~5 min | 14 min |
| CDI | ~1 min | 15 min |
| AWX operator | ~0.5 min | 15.5 min |
| AWX pods + migrations | ~10 min | 25.5 min |
| AWX configuration | ~1 min | ~25 min |
| Catalog seeding (template + instance types) | ~0.5 min | ~25 min |

## Seeded Catalog

The setup script seeds the fulfillment-service with networking resources,
a compute instance template, instance types, and a catalog item so you
can immediately create VMs:

| Resource | ID | Details |
|----------|----|---------|
| Network Class | `pod-network` | Default, uses cudn_net role (no real L2 on kind) |
| Virtual Network | `default` | 10.100.0.0/16, region: kind |
| Subnet | `default` | 10.100.0.0/24, in the default VN |
| Security Group | `default` | SSH inbound, all outbound |
| Template | `osac.templates.ocp_virt_vm` | Linux/Windows VM (defaults: 2c/2G, Fedora, 10G disk) |
| Catalog Item | `linux-vm` | Published, references the VM template |
| Instance Type | `u1-small` | 2 cores, 4 GiB RAM |
| Instance Type | `u1-medium` | 4 cores, 8 GiB RAM |
| Instance Type | `u1-large` | 8 cores, 16 GiB RAM |

## End-to-End Flow

With the full setup, the complete provisioning flow works:

```
osac create computeinstance --name kind-vm --template osac.templates.ocp_virt_vm ...
  → fulfillment-service stores it in PostgreSQL
  → fulfillment-controller creates ComputeInstance CR in osac namespace
  → osac-operator picks up CR, calls AWX: POST /api/v2/job_templates/osac-create-compute-instance/launch/
  → AWX runs the REAL osac-aap playbook (osac-aap/playbook_osac_create_compute_instance.yml)
  → Playbook creates DataVolume + VirtualMachine CR (with pod network override)
  → KubeVirt boots the VM with KVM hardware acceleration
  → VM Running, Ready=True, IP assigned
```

### Pod Network Override

The osac-aap playbooks hardcode Multus/CUDN networking. For kind, a small
override task replaces the network spec with pod networking. This is
configured automatically via AWX job template extra_vars by the setup script.

## Implementation Notes

- The operator runs in **local mode** by default (no `multicluster-runtime`
  remote provider needed). It watches and creates resources on its own cluster.
- Hub registration uses the internal K8s API (`https://kubernetes.default.svc.cluster.local:443`),
  not the host-reachable address.
- AWX `aap.url` must include `/api` path prefix.
- The ComputeInstance template ID must match the Ansible role name
  (`osac.templates.ocp_virt_vm`), not an arbitrary name.
- AWX project sync disables collection install (Red Hat proprietary
  collections like `ansible.platform` are not available in open-source AWX).
- **Rootless podman does NOT work** for KubeVirt — virt-handler needs to
  `chown` `/dev/kvm`, which rootless user namespaces deny.

## Known Limitations

- Networking provisioning templates are no-ops (kind has no real networking backend)
- No OpenShift Routes (uses Gateway API TLSRoute instead)
- Requires rootful podman on Linux (rootless does not support KubeVirt)
- Images must be loaded via `kind load image-archive` (not `docker-image`)
