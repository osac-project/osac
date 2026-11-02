# NICo BMaaS pilot on NVIDIA Launchpad

This runbook records the current deployment and the inputs needed to replay a
single OSAC BareMetalInstance (BMI) create/delete against real NICo hardware.
The [simulator result](nico-bmaas-tenant-flow.md) established the control-plane
path; it did not install an operating system or test tenant networking.

## Current topology

- The NVIDIA hub OpenShift cluster runs NICo Core, NICo REST, Keycloak, ACM,
  and Assisted Installer. OSAC and AWX are not installed there.
- A designated RHEL host runs a libvirt VM on the default NAT network. The VM
  runs CentOS Stream 9, rootful Docker, and a
  single-node `osac-dev` Kind cluster. OSAC, AWX, UI, and KubeVirt run in Kind.
- The VM reaches the NICo REST and Keycloak routes through the hub ingress.
  Both routes were verified with the hub ingress CA. A `404` from the NICo
  REST root path confirms connectivity, not API authorization.
- The VM has an `osac` checkout on the fork branch
  `no-issue/nico-bmaas-integration-audit`. The AWX `osac-aap` project was
  manually switched to that branch at revision
  `301eba88a44ba006f28b317e1f8fddbbd11557fd`. The BMI create/delete
  templates were manually set to prompt for credentials. The pilot values
  overlay now expresses those settings for a fresh devstack install.

The hub and the VM are different Kubernetes clusters. Set `KUBECONFIG` to the
Kind kubeconfig before running any installer or `kubectl` command below. Do
not install the pilot chart into the hub cluster.

## Hardware and NICo preflight

An earlier provisioning recording appears to show zero-DPU GB300 trays on a
Flat VPC and a site `HostInband` network. Treat this as a hypothesis for the
current hub: the recording does not establish the present Machine state,
Tenant, allocations, VPC binding, or permitted test pool. BMC addresses shown
there are not NICo Machine IDs. The host IP prefix and VLAN do not establish
that an OSAC tenant Subnet exists or that tenant traffic is isolated.

Ask the site owner for an approved test Machine or isolated pool, the NICo Site,
tenant account and IdP organization, and a `Registered` compute allocation for
the chosen Instance Type. Confirm the VPC's network virtualization type.
For an Ethernet virtualizer or FNN VPC, obtain its `Registered` IP Block
allocation and tenant Subnet or VPC Prefix ID; choose a matching OSAC Subnet.
For a Flat VPC, confirm that each intended Machine is zero-DPU and its
`HostInband` segment is bound to that VPC. NICo does not use a tenant Subnet or
VPC Prefix in the Flat Instance request. OSAC still requires a Subnet
attachment, so the pilot must define an explicit OSAC-to-Flat network mapping
and label its networking limitation before enabling create. The seeded
`tenant1` OSAC Subnet is `10.200.0.0/20`; do not assume it represents the
recorded site network.

Confirm the boot method: a NICo Operating System ID or a real iPXE script with
reachable artifacts. An iPXE script that exits proves only NICo allocation.

**Flat-network code gap:** `nico.steps.bare_metal_instance` currently requires
exactly one NICo `subnet_id` or `vpc_prefix_id`, sends a physical `interfaces`
entry, and reads the assigned IP from that entry. NICo requires
`autoNetwork: true` and no request interfaces for a zero-DPU Flat Instance;
its resolved IP appears in `status.network.interfaces`. Extend and test this
mode, including an unambiguous mapping back to the single OSAC attachment,
before creating an OSAC BMI on these trays. See NVIDIA's
[Flat VPC guide](https://docs.nvidia.com/infra-controller/documentation/operations-day-2/managing-vp-cs/flat-vp-cs-and-zero-dpu-hosts)
and [Instance create API](https://docs.nvidia.com/infra-controller/rest-api-reference/api-reference/instance/create-instance).

The current NICo module requests an Instance Type; it does not request a
Machine ID. NICo chooses an eligible Machine and the module records its ID
after creation. Use an isolated eligible pool for a first hardware test, or
add and verify explicit Machine targeting before issuing a create. Check the
approved Machine's NICo state and assignment through NICo, not with ICMP.
One tray failed ping from the VM while another answered; these results do not
establish either Machine's NICo allocation state.

NICo's [Instance create API](https://docs.nvidia.com/infra-controller/rest-api-reference/api-reference/instance/create-instance)
supports `machineId` only when the tenant has Targeted Instance Creation
capability. Its [tenant management guide](https://docs.nvidia.com/infra-controller/documentation/configuration-day-1/tenant-management)
describes the required compute and IP Block allocations.

The site owner should also confirm that deleting the test Instance is allowed
to run NICo's full cleanup workflow and identify whom to contact if the
Machine does not return to unassigned `Ready`. Do not remove the BMI finalizer
to work around unfinished NICo cleanup. NVIDIA documents the asynchronous
[release-to-Ready cleanup](https://docs.nvidia.com/infra-controller/documentation/operations-day-2/tenant-lifecycle-cleanup).

The recording's mTLS/gRPC access to NICo Core does not supply OSAC with NICo
REST authorization. The BMI role calls REST with JWT bearer tokens; confirm
the REST issuer, tenant and provider roles, and a secure credential handoff.
The InfraEnv iPXE script shown for HostedCluster workers is a CaaS boot path.
For a standalone BMaaS pilot, agree on the OS or iPXE artifact and on what
installed-OS signal will count as success.

## Replay the OSAC control plane

Build from one reviewed `osac` commit and record that commit and all built
image digests with the run. On the VM, from `~/src/osac/osac-installer`:

```bash
export KUBECONFIG="$HOME/.kube/osac-dev-kind-root.kubeconfig"
kubectl get nodes
make install-infra PLATFORM=kind PROFILE=dev-full NS=osac

export CONTAINER_TOOL=docker
make -C ../fulfillment-service image-build \
  IMG=ghcr.io/osac-project/fulfillment-service:latest CONTAINER_TOOL=docker
make -C ../osac-operator image-build \
  IMG=ghcr.io/osac-project/osac-operator:latest CONTAINER_TOOL=docker
make -C ../bare-metal-fulfillment-operator image-build \
  IMG=ghcr.io/osac-project/bare-metal-fulfillment-operator:latest CONTAINER_TOOL=docker
make -C ../osac-aap execution-environment-build \
  IMG=ghcr.io/osac-project/osac-aap:latest CONTAINER_TOOL=docker
docker build -t ghcr.io/osac-project/osac-ui:latest \
  -f ../osac-ui/Containerfile ..
make kind-load-images PLATFORM=kind PROFILE=dev-full NS=osac CONTAINER_TOOL=docker
kind load docker-image ghcr.io/osac-project/bare-metal-fulfillment-operator:latest \
  --name osac-dev

# First install the base OSAC chart with the BMF controller disabled.
make install-osac PLATFORM=kind PROFILE=dev-full NS=osac

# The pilot overlay selects the fork project and its BMI credential prompts.
# It is safe here because the devstack chart ignores the BMF values.
make install-devstack PLATFORM=kind PROFILE=dev-full NS=osac \
  INSTANCE_VALUES_EXTRA='-f values/dev/kind-instance-devfull.yaml -f values/dev/kind-instance-nico-pilot.yaml'
```

The devstack hook must finish with the AWX project `successful` on the
intended revision. Both `osac-create-bare-metal-instance` and
`osac-delete-bare-metal-instance` must have
`ask_credential_on_launch: true`. The pilot overlay pins its AWX template
catalog URL to revision `301eba88a`; update that URL when the catalog changes
and verify that the AWX project checkout contains the same playbooks and roles.

Do not enable the BMF controller until its inputs exist in the Kind `osac`
namespace: `osac-inventory-config-nico-pilot` and
`osac-management-config-nico-pilot` Secrets, the administrator-owned
`osac-nico-tenant-bindings` ConfigMap, and an AWX token. The binding maps the
exact OSAC tenant annotation and public OSAC Subnet UUID to a complete AWX
credential list: the existing `kind-cluster` credential ID and one scoped
NICo credential ID. See the [binding contract](../../bare-metal-fulfillment-operator/README.md#nico-bmaas-tenant-bindings-pilot).
The two non-secret BMF client configuration files can be created as follows;
keep their source files outside Git:

```yaml
# inventory.yaml
name: nico-inventory
type: nico
hostClass: nico
options: {}
---
# management.yaml (a separate file)
name: nico-management
type: nico
options: {}
```

```bash
kubectl -n osac create secret generic osac-inventory-config-nico-pilot \
  --from-file=inventory.yaml=inventory.yaml
kubectl -n osac create secret generic osac-management-config-nico-pilot \
  --from-file=management.yaml=management.yaml
```

Create NICo credentials in AWX, never in Helm values or Git. The job needs
tenant authorization for Instance operations, provider read authorization
for Machine/health cleanup checks, and the trusted hub ingress CA inside the
execution environment. AWX custom credentials can inject a temporary CA file
and point `SSL_CERT_FILE` to it; see [AWX credential file injectors](https://docs.ansible.com/projects/awx/en/24.6.1/userguide/credential_types.html).

After these inputs are present, enable BMF with a second OSAC chart upgrade:

```bash
make install-osac PLATFORM=kind PROFILE=dev-full NS=osac \
  INSTANCE_VALUES_EXTRA='-f values/dev/kind-instance-devfull.yaml -f values/dev/kind-instance-nico-pilot.yaml'
kubectl -n osac get deployments
```

Before creating a BMI, verify the OSAC attachment's intended network mapping,
the AWX credential IDs in the binding, NICo allocations, Machine state, and
boot artifact. Create one BMI under the bound tenant. Save its OSAC UID, NICo
Instance ID, and original NICo Machine ID. During deletion, OSAC must retain
the BMI until NICo reports the Instance absent and that same Machine
unassigned `Ready`, with no blocking health report. Verify both sides
independently. Investigate a stuck finalizer and preserve the saved IDs for
recovery.

## What still needs automation

The libvirt VM, guest toolchain, image builds, private NICo identities, CA
trust, and tenant allocations were prepared manually. Turn the VM and tool
setup into a versioned bootstrap script or Ansible playbook; pin image
versions/digests in a private deployment inventory. Define a repeatable,
secret-safe AWX credential and ConfigMap provisioning procedure for each
OSAC-to-NICo tenant/Subnet binding. Numeric AWX credential IDs must be
rediscovered if AWX is recreated. The live NVIDIA hardware flow remains
untested until the site owner designates a safe pool and supplies tenant and
boot inputs. If the hub uses Flat VPCs, the NICo module and OSAC network
binding also need the Flat-mode work described above before a BMI create.
