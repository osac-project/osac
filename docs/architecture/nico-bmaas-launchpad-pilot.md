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
  `no-issue/nico-bmaas-integration-audit`. The initial AWX `osac-aap` project
  was manually switched to that branch at revision `301eba88a`. Later
  redeployments must verify the actual AWX revision and execution-environment
  image. The pilot values overlay configures the BMI credential prompts.

The hub and the VM are different Kubernetes clusters. Set `KUBECONFIG` to the
Kind kubeconfig before running any installer or `kubectl` command below. Do
not install the pilot chart into the hub cluster.

## Hardware and NICo preflight

**Current priority: DPU-backed FNN BMaaS.** The GB300 onboarding notes report
BlueField-3 DPUs and their hosts reaching NICo `Ready`, with an FNN tenant VPC
and VPC Prefix used to install hosted-cluster workers. The reported BMC, DPU
management, and host overlay IPs are useful for diagnostics, but they are not
NICo Machine IDs or evidence that either tray is now free. Do not copy those
site-specific addresses or boot URLs into this repository. Confirm current
Instance assignment through NICo before creating an OSAC BMI.

For this pilot, configure the scoped AWX credential with
`NICO_BMAAS_NETWORK_VIRTUALIZATION_TYPE=FNN`, an approved
`NICO_BMAAS_VPC_ID` and `NICO_BMAAS_VPC_PREFIX_ID`, and no NICo Subnet ID or
Flat CIDR. The bound OSAC Subnet must represent the same tenant IPv4 range as
the NICo VPC Prefix; the seeded dev-full Subnet is not a verified match. The
NICo role sends one physical interface referencing the VPC Prefix. Validate
the actual Ready Instance GET response and installed OS connectivity on this
site before treating BMI `Ready` as a completed BMaaS test.

List OSAC Subnets from the **VM's Kind context**. The `subnet-uuid` label is
the public Subnet ID used in the BMI attachment and tenant binding; the
Kubernetes object name is a separate generated name:

```bash
kubectl get subnets.osac.openshift.io -A -o json |
  jq -r '.items[] | [.metadata.labels["osac.openshift.io/subnet-uuid"],
    .metadata.annotations["osac.openshift.io/tenant"], .spec.ipv4Cidr,
    .status.phase] | @tsv'
```

Verify that the intended tenant has a `Ready` Subnet matching the approved NICo
VPC Prefix CIDR. If none exists, provision one through the OSAC tenant network
workflow before adding its public ID to the binding.

The first hardware test targets one approved tray. Set
`NICO_BMAAS_TARGET_MACHINE_ID` in its administrator-owned AWX credential and
leave `NICO_BMAAS_INSTANCE_TYPE_ID` empty. Verify that the tenant account is
`Ready` with effective `targetedInstanceCreation` for the VPC's Site, and
that the selected host Machine is unassigned `Ready` with its expected DPU.
The module checks the Machine and VPC Site before POST and verifies the
returned Machine ID. This check does not reserve the Machine. Obtain a
standalone ARM64 OS/boot artifact for BMaaS; the InfraEnv iPXE script in the
onboarding notes installs hosted-cluster workers. Keep the site/DPU onboarding
and switch configuration under the NICo site owner's control.

The Flat-network guidance below records an alternative zero-DPU path; it is
not the current Launchpad pilot target.

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

**Flat-network pilot mode:** `nico.steps.bare_metal_instance` now accepts
`NICO_BMAAS_NETWORK_VIRTUALIZATION_TYPE=FLAT` with no NICo Subnet or VPC
Prefix ID. It sends `autoNetwork: true` without request interfaces and
requires one assigned IPv4 address within `NICO_BMAAS_FLAT_IPV4_CIDR`.
Set that CIDR to the administrator-verified OSAC Subnet CIDR representing
the approved HostInband segment. These request and response guards have unit
coverage, but no zero-DPU hardware or packet-path validation yet. Capture a
sanitized GET response from this NICo site to confirm its resolved-interface
representation before an OSAC BMI create. See the
[networking plan](nico-bmaas-networking-plan.md) and NVIDIA's
[Flat VPC guide](https://docs.nvidia.com/infra-controller/documentation/operations-day-2/managing-vp-cs/flat-vp-cs-and-zero-dpu-hosts)
and [Instance create API](https://docs.nvidia.com/infra-controller/rest-api-reference/api-reference/instance/create-instance).

For later pool-based tests, set `NICO_BMAAS_INSTANCE_TYPE_ID` and leave the
target Machine ID empty. NICo then chooses an eligible Machine and the module
records its ID after creation. Check an approved Machine's NICo state and
assignment through NICo, not with ICMP. One tray failed ping from the VM
while another answered; these results do not establish either Machine's NICo
allocation state.

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

### Repeat redeploy on the existing VM

The first redeploy needs a one-time bootstrap because an older VM checkout does
not yet have the `redeploy-nico-pilot` Make target. Run these commands **inside
the VM** and confirm the branch is `no-issue/nico-bmaas-integration-audit` with
no changed or untracked files before the fast-forward:

```bash
cd "$HOME/src/osac"
git status --short --branch
git branch --show-current
git fetch --no-tags https://github.com/vladikr/osac.git \
  no-issue/nico-bmaas-integration-audit
git merge --ff-only FETCH_HEAD
git rev-parse --short HEAD
```

Stop if the branch differs, the tree is dirty, or the fast-forward fails;
resolve that checkout state without discarding its changes. Once the VM, Kind
cluster, bindings, and AWX credentials are in place, refresh the pilot from
the now-updated checkout:

```bash
export KUBECONFIG="$HOME/.kube/osac-dev-kind-root.kubeconfig"
make -C "$HOME/src/osac/osac-installer" redeploy-nico-pilot \
  PLATFORM=kind PROFILE=dev-full NS=osac
```

The target checks the Kind context and existing pilot resources, requires a
clean checkout on the configured branch, fetches and fast-forwards that branch,
builds and loads commit-tagged AAP and BMF images, then upgrades the devstack
and OSAC Helm releases. It passes the same commit to the AWX project catalog
URL and restarts BMF so it reads the rotated AWX token, including when the
same commit is redeployed. It does not provision NICo hardware. Override
`NICO_PILOT_GITHUB_REPO`, `NICO_PILOT_BRANCH`, or
`NICO_PILOT_CONTAINER_TOOL` when using another fork, branch, or container
runtime. Verify the AWX project revision, image, BMI credential prompts, and
BMF rollout before a hardware request.

The steps below are retained for initial setup and for changes in components
outside this target's AAP/BMF scope.

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
untested until the standalone BMaaS boot source and OSAC tenant/Subnet binding
are verified. A later Flat VPC pilot needs a zero-DPU pool and a verified
HostInband-to-OSAC Subnet mapping.
