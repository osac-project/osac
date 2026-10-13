# OSAC Networking Manager Provider Guide

This guide explains how a cloud provider implements and registers Fabric and
Kubernetes Managers for OSAC networking. It follows the provider workflow from
choosing a manager role through packaging its Ansible role, registering data
models and managers, and checking the resulting behavior.

Start with [OSAC networking contract in brief](network-manager-contract-overview.md)
for the workflow and ownership summary; use this guide for the exact adapter contract.

> **Implementation status:** This guide describes the target contract in the
> [Network Manager Integration Contract Design in proposal PR #357](https://github.com/osac-project/enhancement-proposals/pull/357/files).
> The current OSAC code still uses the legacy ConfigMap-based manager registry
> and the `implementation-strategy` path to select provider playbooks that
> include roles from the historically named `osac.templates` collection.
> Those network implementations are executable Ansible roles; their
> `template_type: network` metadata currently filters them out of generic
> template discovery but does not register a manager or validate manager
> compatibility. The target contract uses a fixed generic networking job
> template and derives `osac.networking.<managerName>` from NetworkManager.
> Generic OSAC platform support, including installer acceptance of arbitrary
> manager pairs and provider secret keys, must ship before a provider can
> use this workflow end to end. The `network-fulfillment-ig` ConfigMap and
> Secret described later provide job environment values; they do not register
> manager implementations.

## Contents

- [How manager integration works](#how-manager-integration-works)
- [Before you start](#before-you-start)
- [Provider settings and secrets](#provider-settings-and-secrets)
- [Step 1: Choose the manager role](#step-1-choose-the-manager-role)
- [Step 2: Define any data exchanged between managers](#step-2-define-any-data-exchanged-between-managers)
- [Step 3: Implement the AAP manager role](#step-3-implement-the-aap-manager-role)
- [Step 4: Register models, managers, and NetworkClass](#step-4-register-models-managers-and-networkclass)
- [Step 5: Verify the integration](#step-5-verify-the-integration)
- [Provider readiness checklist](#provider-readiness-checklist)
- [Changing or removing a manager](#changing-or-removing-a-manager)
- [Implementation status and conformance ownership](#implementation-status-and-conformance-ownership)
- [Related documents](#related-documents)

## How manager integration works

In this guide, **networking profile** means the deployment's active
NetworkClass and selected managers. It is not an additional resource or tenant
network. OSAC creates and validates NetworkClass through its API, without a
provider create/delete task or runtime outputs. Provider-wide settings and
prerequisites belong to the existing installation/environment configuration.

OSAC gives tenants one networking API across virtual machines, managed
Kubernetes clusters, and bare-metal servers. The same `VirtualNetwork`,
`Subnet`, and other networking resources keep the same meaning across those
workloads. A provider supplies backend implementations behind that API:

- The **Fabric Manager** configures the provider's physical network and handles
  shared networking resources and physical workload interfaces. Every
  `NetworkClass` must select one.
- The **Kubernetes Manager** realizes overlay Subnet networking for supported
  workloads and applies attachment policy. Its `create_subnet` task must make
  the stable network reference required by the selected workload extension
  usable. The manager owns the Kubernetes resources and controller chain used
  to realize that reference; OSAC does not prescribe a resource kind. It is
  optional when VM workloads are not supported and does not replace the Fabric
  Manager.

The managers are independent. They do not call one another. Their jobs run in
the same networking fulfillment group and can read all settings and secrets
configured for that group; this is not per-manager isolation. If a Kubernetes
Manager needs a value produced by the Fabric Manager, OSAC carries that value
between the jobs as `NetworkData`.
`NetworkDataModel` defines the value's name, owner, and JSON Schema;
`NetworkData` stores one validated value for that model and owner. A
`NetworkManager` registers a role implementation and declares its input or
output model names. A `NetworkClass` selects the managers by their logical
names.

The registration check compares exact model names: every model declared in a
Kubernetes Manager's `networkInputs` must also be declared in the selected
Fabric Manager's `networkOutputs`. This catches missing data dependencies
before the `NetworkClass` is stored. It does not prove that the managers
implement the same network behavior; providers must verify that separately.
`networkOutputs` is a static declaration used for compatibility and result
validation. Keep it aligned with the Fabric role: because the provider
authors both, the role's tasks have manager-specific logic for the model
names the manager implements and produce their corresponding values.
For each Fabric create or reconcile task, OSAC derives the expected
model/owner keys from this declaration, the owner scopes in the operation
context, and NetworkData already stored. OSAC does not pass that computed set
to AAP. Each task uses `network_owner_context` to label values for every
declared owner present in the context and existing `network_data` to skip
values already stored. Thus a Subnet task may first return a
VirtualNetwork-scoped value if that parent value is missing; the value is
stored under the VirtualNetwork owner and reused by later tasks. A
NetworkClass-scoped value may first be returned by any eligible resource task
whose context includes the profile. The task returns every expected value
exactly once and no other value. OSAC checks that every applicable Kubernetes
input is stored before starting the Kubernetes job.

## Before you start

OSAC trusts each provider task's reported success after validating its result
envelope and required values. A provider must supply every task entry point
assigned to the role. A task may return a valid success without backend changes
when the provider deliberately handles that action as a no-op. The task still
returns every required result field and every expected real, schema-valid
NetworkData value. An omitted task or missing required output fails the request.
A successful no-op does not establish the corresponding network or policy
behavior; providers document their actual behavior and retain separate evidence
for the shared semantics they claim. Registration is a declaration check. [User]

1. Confirm that the OSAC release includes the generic manager support described
   in the implementation status note. That support includes the registration
   APIs and CRDs, fulfillment-service validation, NetworkData schema
   validation, manager dispatch, and owner-scoped data lifecycle.
2. Decide which workload services the deployment will offer. Fabric Manager
   support is required. Select a Kubernetes Manager if the deployment offers
   VMaaS; without one, OSAC rejects ComputeInstance requests. CaaS and BMaaS
   still use the Fabric Manager.
3. Prepare the provider's Ansible Automation Platform (AAP) execution
   environment (EE) with the `osac.networking` collection and the role for
   each manager. The role's name is the manager's `NetworkManager.spec.managerName`;
   OSAC derives its fully qualified role name as
   `osac.networking.<managerName>`. Compose and pin one execution environment
   containing all selected wrappers and their dependencies. Each wrapper may
   delegate to a provider-owned collection such as `acme.fabric`; OSAC dispatch
   continues to invoke the fixed wrapper. Independently supplied roles must be
   assembled into the same `osac.networking` collection, with duplicate role
   names rejected during packaging. Installing a second complete package with
   that same collection identity is not an additive manager installation.
   Set the resulting image through `aap.configAsCode.eeImage` and verify the
   exact collection/task version resolved in the job. [User]
4. Identify the backend settings and secret values each role needs, and the
   environment-variable names the role will read. Put non-secret values in
   the networking fulfillment group's ConfigMap and sensitive values in its
   Secret, as described below. These values are shared with every manager job
   in that group.

The fulfillment-service database is authoritative for provider registrations.
The Kubernetes custom resources in the networking hub are projections watched
by the operator. Submit registrations through the provider-facing
fulfillment-service API; do not create or edit the projected CRDs with
`kubectl`.

## Provider settings and secrets

Use the existing AAP networking fulfillment instance group for provider
settings. In Helm values, add non-secret entries under
`aap.instanceGroups.networkFulfillment.config` and sensitive entries under
`aap.instanceGroups.networkFulfillment.secret`. These settings produce the
group's Kubernetes ConfigMap and Secret. The networking instance-group pod
spec imports both with `envFrom`, so every manager job in the group receives
all configured keys as environment variables. There is no per-manager
environment or secret isolation: each role should read only the values it
needs, and the provider must trust every installed role in that group.
Use provider- and manager-specific prefixes for environment-variable names to
avoid collisions between independently authored roles. The current pod spec
provides values as environment variables rather than mounting arbitrary
provider files; a role can write a value to a job-local file when its backend
client requires one.

For example, a provider can add its own endpoint and CA bundle to the
ConfigMap and an API token to the Secret:

```yaml
aap:
  instanceGroups:
    networkFulfillment:
      config:
        ACME_CONTROLLER_URL: https://fabric-api.example.test
        ACME_CA_BUNDLE: "<PEM CA bundle>"
      secret:
        ACME_API_TOKEN: "<value supplied through the provider's secret source>"
```

Put API endpoints, resource selectors, and public CA bundles in the ConfigMap.
Put passwords, tokens, and private client keys in the Secret. The role owns
its environment-variable names and validates required values before making
backend changes. Do not copy these values into `NetworkManager`,
`NetworkClass`, `osac_job_vars`, NetworkData, or `osac_result`; tasks must
also keep secrets out of logs and result artifacts.

For a backend that uses a private CA, store its PEM bundle in a ConfigMap
value. The role can write that value to a job-local temporary file and pass
the file path to its backend client while keeping TLS certificate verification
enabled. This configures that client; it does not add the CA to the pod-wide
trust store. A manager that needs system-wide trust must use an execution
environment or AAP pod-spec configuration that installs the CA. Store a
private client key in the Secret, write it only to a temporary file with
restrictive permissions, and remove the file after use.

For the existing AAP instance-group Helm configuration, see
[AAP Configuration](../../../osac-installer/docs/aap-configuration.md).

## Step 1: Choose the manager role

### Fabric Manager

The Fabric Manager handles shared networking resources and physical workload
interfaces. Its shared task set includes creating and deleting
VirtualNetworks and Subnets, applying and removing physical workload network
attachments, managing ExternalIP pools and allocations, managing ExternalIP
attachments and NAT gateways, and creating or deleting
`FabricDomain` resources when the east-west capability is enabled. A selected workload-service extension may
also require its own task entry points; those tasks are listed separately
below and are not part of every Fabric Manager's base contract.

### Kubernetes Manager

The Kubernetes Manager handles Kubernetes-side Subnet realization for overlay
workload interfaces. `create_subnet` prepares the selected Subnet for workloads;
the manager chooses and owns the backing Kubernetes resources and controller
chain. OSAC does not prescribe a particular resource kind or network plugin.

For a ComputeInstance VM, the workload template first creates the VM halted,
without tenant network/interface fields. The networking controller then
creates a target-only `SubnetAttachment` and invokes
`apply_subnet_attachment`. The manager uses the request's immutable placement
and the supplied target-cluster access to add only the network/interface fields
for the requested Subnet and install policy on the exact stopped VM. It must
not start the VM or change its run control. OSAC reports
`NetworkAttachmentsReady=True` only after the job succeeds and the target
configuration and policy are confirmed. VMaaS starts the VM only after that
condition; the first VMI therefore boots with its network configured. On
deletion, VMaaS stops the VM and waits for it to stop before deleting the
request. The manager removes its network/interface fields and policy, and the
VM is deleted only after request cleanup completes.

For Kubernetes Manager tasks that operate on a workload cluster, OSAC supplies
the stable cluster ID as `osac_job_vars.target_cluster.id`. For an overlay
attachment task, it comes from the SubnetAttachment's immutable placement;
for a placement-scoped `Subnet` task, it comes from that stage's captured
extension context.
The networking AAP group reads its protected kubeconfig from the existing
remote-cluster Secret mounted at `OSAC_REMOTE_CLUSTER_KUBECONFIG`. This is a
separate Secret from the backend credentials in `network-fulfillment-ig`.
Create the kubeconfig Secret in the AAP namespace, make it readable by the
`osac-sa` service account, and set `REMOTE_CLUSTER_KUBECONFIG_SECRET_NAME` and
`REMOTE_CLUSTER_KUBECONFIG_SECRET_KEY` in the existing `config-as-code-ig`
Secret. These settings cause the networking instance-group pod spec to mount
the file read-only. See the [AAP config-as-code settings](../../../osac-aap/collections/ansible_collections/osac/config_as_code/README.md)
and [remote-cluster kubeconfig setup guide](../../../osac-operator/docs/vmaas-dedicated-cluster/README.md)
for the existing mount mechanism. The kubeconfig must contain a context whose
name exactly matches each supported cluster ID. For one cluster, configure
its matching context; for multiple clusters, configure one context per
supported ID in the same kubeconfig.

For tasks that receive `target_cluster`, treat this kubeconfig as required:
do not omit it or fall back to the AAP pod's in-cluster service account.

After loading `remote_cluster_kubeconfig` through OSAC's shared
`get_remote_cluster_kubeconfig` task, each Kubernetes API call must select the
explicit context. For `kubernetes.core.k8s` and `kubernetes.core.k8s_info`
tasks, pass:

```yaml
kubeconfig: "{{ remote_cluster_kubeconfig }}"
context: "{{ target_cluster.id }}"
```

Manager tasks must not rely on the kubeconfig's current context or choose
another cluster; another client library must be configured to use the same
explicit context. Credentials remain in the mounted Secret and never appear
in the request, `osac_job_vars`, NetworkData, results, or logs. If the context
is missing or cannot access the exact target, the task fails before changing
it and readiness does not advance. Every installed networking role can read
the shared Secret, so providers must trust those roles with its cluster
credentials. The Kubernetes Manager does not handle shared VirtualNetwork
operations, physical workload interfaces, or Fabric Manager work. It is
optional for deployments without VM overlay workloads.

The normative resource meanings and tenant-visible behavior are in the
[Unified Networking Design](https://github.com/danmanor/enhancement-proposals/blob/722efda/enhancements/OSAC-1433-unified-networking/design.md).
In particular, implementations must preserve these shared semantics:

- A `VirtualNetwork` is a Layer 3 (L3) routing domain. Subnets in it can route
  to each other, subject to SecurityGroup policy.
- Each `Subnet` is a separate Layer 2 (L2) broadcast domain. Workloads attached to the
  same Subnet share that L2 domain.
- SecurityGroup rules are stateful, apply to the workload's network attachment,
  combine as a union per workload and direction, allow established reply
  traffic, and deny unmatched new traffic. For a new flow between two
  workloads, both the sender's egress rules and receiver's ingress rules must
  permit it. For traffic to or from an endpoint outside OSAC workload policy,
  the attached workload's rule in the traffic's direction controls it. Each
  manager enforces the rules for its own attachment; it does not look up a
  peer workload's SecurityGroups.
- The current shared networking contract is IPv4-only and permits one tenant
  network attachment per workload.
- ExternalIP attachment provides inbound translation; NATGateway provides
  outbound source translation. They have separate lifecycles and behavior.

### Optional capabilities

Capabilities describe optional behavior beyond the base manager contract.
They come from a predefined OSAC list; providers cannot invent capability
strings. A Fabric Manager advertises support in
`NetworkManager.spec.capabilities`, and NetworkClass separately enables it
for the deployment. OSAC rejects enabling an unsupported capability;
advertising support alone does not enable tenant requests. Omitted or empty
capabilities leave the base networking contract in effect.

The current capability list in this proposal is:

| Capability (API enum value) | What it provides |
|---|---|
| `NETWORK_MANAGER_CAPABILITY_EAST_WEST_ETHERNET` | Ethernet east-west FabricDomain creation and deletion for participating servers, while preserving VirtualNetwork routing and Subnet broadcast domains. |

Implement the full task and behavior contract for every advertised capability.
Capability-specific enablement and task requirements are defined in the
[Network Manager Integration Contract](https://github.com/osac-project/enhancement-proposals/pull/357/files)
and [Multi-Fabric East-West Networking Design](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1382-multi-fabric-east-west-networking/design.md).
The examples below use only the base contract, without optional capabilities.

All networking resources use the selected generic manager contract; no
resource has a separate vendor-specific dispatch path.

## Step 2: Define any data exchanged between managers

Create a `NetworkDataModel` only for a value that needs a durable owner-scoped
lifecycle or must pass from one manager role to another. Do not put values,
credentials, or secrets in the model itself. A model's name, meaning, owner
scope, and schema are immutable. If any of them must change, register a new
model name.

For each model, decide:

1. **Name and meaning:** Use a provider-owned DNS label such as
   `acme-networking-subnet-vlan-id`. Names beginning with `osac-` are reserved
   for OSAC models. Equal JSON types do not make two values interchangeable;
   the name is part of the contract.
2. **Owner scope:** Choose one supported owner scope: `NetworkClass`,
   `VirtualNetwork`, or `Subnet`. NetworkClass scope shares a dynamic value
   across resources bound to that provider profile; VirtualNetwork and Subnet
   scopes give each tenant resource its own value. NetworkClass has no provider
   task, so a profile-scoped output is first returned by an eligible resource
   operation whose owner context includes that profile. OSAC removes it after
   all profile-bound resources and operations drain. A new owner kind requires
   OSAC platform support.
3. **Value schema:** Supply an inline JSON Schema Draft 2020-12 schema. The
   fulfillment service validates the schema when the model is created and
   validates every value against it. OSAC does not fetch schemas from URLs.
4. **Consumer:** Document which manager reads the value and what the manager
   must do with it. Put the model name in the Fabric Manager's outputs and in
   the Kubernetes Manager's inputs when it crosses between those roles.

For example, a VLAN-based Fabric Manager can publish one VLAN ID per Subnet,
which a Kubernetes `LocalNet`-style Manager can consume:

```yaml
apiVersion: osac.openshift.io/v1alpha1
kind: NetworkDataModel
metadata:
  name: acme-networking-subnet-vlan-id
spec:
  description: VLAN ID used to connect this Subnet to the provider fabric.
  ownerScope: Subnet
  schema:
    $schema: https://json-schema.org/draft/2020-12/schema
    type: integer
    minimum: 1
    maximum: 4094
```

For REST Create, send the resource fields without `apiVersion` and `kind`. For
example, this is a direct JSON request body for the same model:

```json
{
  "metadata": {"name": "acme-networking-subnet-vlan-id"},
  "spec": {
    "description": "VLAN ID used to connect this Subnet to the provider fabric.",
    "ownerScope": "Subnet",
    "schema": {
      "$schema": "https://json-schema.org/draft/2020-12/schema",
      "type": "integer",
      "minimum": 1,
      "maximum": 4094
    }
  }
}
```

Save that object as `network-data-model.json` and create it before either
manager registration:

```sh
curl --fail-with-body --request POST \
  "${FULFILLMENT_API}/api/private/v1/network_data_models" \
  --header "Authorization: Bearer ${OSAC_PROVIDER_TOKEN}" \
  --header "Content-Type: application/json" \
  --data-binary @network-data-model.json
```

The schema is the model's value contract. For example, this schema permits an
integer from 1 through 4094; it does not itself configure a switch or create a
Kubernetes network. The Fabric Manager allocates and configures the VLAN, then
returns its value. The Kubernetes Manager consumes the validated value and
maps it to its own backend configuration.

Provider models may use JSON scalars, arrays, or objects. Use one composite
object when related fields share a meaning, owner, and lifetime. Use separate
models when values have different meanings, owners, or lifetimes. JSON Schema
checks structure and constraints; each manager remains responsible for the
resource-specific meaning described by the model.

## Step 3: Implement the AAP manager role

`NetworkManager.spec.managerName` selects the role directory and is also the
logical name NetworkClass references. It must begin with a lowercase letter
and contain only lowercase letters, digits, or underscores. Names are unique
across Fabric and Kubernetes registrations. For example, `acme_fabric` is a
valid name; both the registration and role directory use that exact value.

Implement the role in the `osac.networking` collection at:

```text
collections/ansible_collections/osac/networking/roles/<managerName>/tasks/<task>.yml
```

For example, the Fabric manager role named `acme_fabric` has the FQCN
`osac.networking.acme_fabric` and stores its Subnet tasks at
`roles/acme_fabric/tasks/create_subnet.yml` and
`roles/acme_fabric/tasks/delete_subnet.yml`. Package the `osac.networking`
collection containing the provider's manager roles in the AAP execution
environment used by the generic OSAC networking job template.

OSAC already knows the resource kind and lifecycle action it is reconciling.
It chooses the manager role, order, and exact task file from this fixed
contract. The workload service creates a target-only SubnetAttachment request;
the networking controller dispatches the fixed attachment task and records its
job and result against that request. Providers implement the named task files;
they do not define or send an operation string or map one operation name to
another.

| Resource lifecycle action | Manager role and order | Workload target | Required task file stem |
|---|---|---|---|
| Create/delete VirtualNetwork | Fabric | None | `create_virtual_network` / `delete_virtual_network` |
| Create/delete Subnet | Fabric, then Kubernetes when configured; reverse order for deletion | None | `create_subnet` / `delete_subnet` |
| Create/delete FabricDomain | Fabric when the selected NetworkClass enables the declared east-west capability | None | `create_fabric_domain` / `delete_fabric_domain` |
| Apply/delete SubnetAttachment | Kubernetes for `overlay`; Fabric for `physical` | `SubnetAttachment` request plus separate workload `target` | `apply_subnet_attachment` / `delete_subnet_attachment` |
| Create/delete ExternalIPPool | Fabric | None | `create_external_ip_pool` / `delete_external_ip_pool` |
| Allocate/release ExternalIP | Fabric | None | `create_external_ip` / `delete_external_ip` |
| Create/delete ExternalIPAttachment | Fabric | `ComputeInstance`, `Cluster`, `BaremetalInstance` | `attach_external_ip` / `detach_external_ip` |
| Create/delete NATGateway | Fabric | None | `create_nat_gateway` / `delete_nat_gateway` |

This table lists the shared tasks and the optional capability task. Every
selected manager must provide all shared task files assigned to its role and
supported targets, plus the tasks for each capability it advertises. The task
filename stem in the table is the Ansible
`tasks_from` value; the filename in the collection has the `.yml` extension.
Each manager implements the task names assigned to its role with its own
backend behavior, including an explicitly documented no-op where chosen.
`apply_subnet_attachment` is one OSAC task name with role-specific behavior.
For a physical interface, the Fabric Manager maps the selected host port to
the provider segment for the requested Subnet and installs policy before the
service's handoff gate. For an overlay interface, the Kubernetes Manager
updates only the network/interface fields on the exact stopped target and
installs policy before the service can start it. `NetworkAttachmentsReady=True`
is written only after the manager result and target state are confirmed. The
manager never changes run control. The workload service owns when it creates,
starts, stops, or deletes the workload; OSAC's shared request and readiness
gate serialize the operation with those lifecycle steps. Resource tasks are
idempotent by resource API ID; network attachment tasks are idempotent by
request/target identity, logical interface, and placement. Each NetworkData
output is additionally idempotent by its model name and owner kind/API ID,
including when concurrent resource tasks can produce the same parent- or
profile-owned value. Kubernetes UIDs fence projections and job results; they
do not own backend allocations. [User]

### Workload-service task additions

Service extensions can add task entry points when their lifecycle needs
provider work beyond the shared resource contract. The extension defines the
task trigger, target and owner identities, extra inputs, readiness gate, and
task-specific result fields. The task still uses the common AAP invocation
and result envelope.

| Service extension | Additional manager task | Purpose |
|---|---|---|
| BMaaS; also used for CaaS worker instances | Fabric `query_dhcp_lease` for a `SubnetAttachment` request with a separate `BaremetalInstance` target | Discover the selected physical interface's tenant Dynamic Host Configuration Protocol (DHCP) lease after network handoff. See the [BMaaS networking design](https://github.com/danmanor/enhancement-proposals/blob/722efda/enhancements/OSAC-1437-bmaas-networking/design.md). |
| CaaS | Fabric `reserve_cluster_ingress_ip` / `release_cluster_ingress_ip` for a `ClusterOrder` | Reserve and release one tenant-Subnet ingress address for a guest cluster. See the [CaaS networking design](https://github.com/danmanor/enhancement-proposals/blob/722efda/enhancements/OSAC-1436-caas-networking/design.md). |

Providers implement these additional tasks only when they offer the
corresponding service extension. The CaaS reservation task uses the configured
`NetworkClass.spec.vip_prefix_length`; the shared manager contract passes the
captured value to `create_subnet`, while the CaaS extension defines address
reservation and release.

OSAC launches one generic networking AAP job template. The template runs the
OSAC-owned `playbook_osac_network_manager.yml`; providers do not select a
playbook or AAP job template. OSAC passes `osac_network_task_from` with the
exact task file stem and a separate `osac_job_vars` object. The generic
playbook derives the FQCN from the selected manager name:

```yaml
- name: Invoke the selected network manager
  ansible.builtin.include_role:
    name: "osac.networking.{{ osac_job_vars.manager.name }}"
    tasks_from: "{{ osac_network_task_from }}"
```

For example, a Subnet create request runs `create_subnet.yml`; OSAC supplies
`osac_network_task_from: create_subnet` to the generic playbook. Providers do
not supply or choose that value. The legacy
`implementation-strategy` selector is replaced by this fixed generic
networking job template. See the [Ansible `include_role`
documentation](https://docs.ansible.com/projects/ansible-core/2.17/collections/ansible/builtin/include_role_module.html)
and [collection role FQCN
documentation](https://docs.ansible.com/projects/ansible/latest/collections_guide/collections_using_playbooks.html).

The manager role reads its required settings and secrets from the environment
variables supplied to the networking fulfillment group. They are shared with
every role in that group; see [Provider settings and
secrets](#provider-settings-and-secrets) for the configuration path and
security boundary.

Create ordering for a Subnet is Fabric first, then Kubernetes when selected.
Delete ordering is Kubernetes first, then Fabric. OSAC routes network
attachment tasks by normalized interface type: Kubernetes Manager for
`overlay`, Fabric Manager for `physical`. The existing workload API resource
owns the attachment job. A separate `target` is supplied only when a service
extension operates on a different endpoint, such as a guest-cluster worker.

### AAP inputs for each task

OSAC passes `osac_network_task_from` separately from the
`osac_job_vars` object. OSAC chooses the task filename stem; a provider role
must not override it. The common `osac_job_vars` fields are summarized below. Exact types and
task-specific presence rules are in the manager contract’s
[canonical serialized task inputs](https://github.com/danmanor/enhancement-proposals/blob/722efda/enhancements/OSAC-1433-network-manager-integration-contract-networking/design.md#canonical-serialized-task-inputs).

| Field | Meaning |
|---|---|
| `manager` | Selected `managerName`, normalized role (`fabric` or `kubernetes`), and immutable registration `registration_id`. OSAC derives the role FQCN as `osac.networking.<managerName>`. |
| `network_class` | Bound profile's stable API `resource_id` and current hub `projection_uid`; includes the captured `vip_prefix_length` when configured and omits it otherwise. Manager registrations are captured by OSAC; the role does not resolve them by name. |
| `resource` | Complete current hub resource projection, including its immutable spec, `metadata.uid`, generation, and stable fulfillment-service `id`. For attachment apply/delete and DHCP lookup, this is the target-only `SubnetAttachment` request. |
| `network_owner_context` | Applicable NetworkClass, VirtualNetwork, and Subnet owners, each with `kind`, stable `resourceId`, and current `projectionUID`. OSAC derives eligible output keys from manager declarations, owner scopes, and values already stored; a child task may produce a missing value for a parent or profile owner in this context. |
| `network_data` | Applicable model name, owner kind and API resource ID, and schema-validated JSON value. Fabric receives existing values to reuse and returns only expected values not already stored; Kubernetes receives only its declared applicable inputs. An empty list is supplied when none apply. |
| `target` | Complete current workload projection, required when a task operates on a workload endpoint. For attachment work, it is separate from the `SubnetAttachment` in `resource`. |
| `target_cluster` | Stable hosting-cluster ID captured from a workload's immutable placement or a placement-scoped Subnet stage context. Required when a Kubernetes Manager task operates against that cluster; `target_cluster.id` names its context in the protected remote-cluster kubeconfig and contains no credential. |
| `network_attachment` | Normalized Subnet, interface descriptor, and complete SecurityGroup rules; required for workload attachment tasks. |

The AAP extra-vars object also carries `osac_dispatch_attempt_id` for launch
recovery. OSAC passes `osac_network_task_from` separately from `osac_job_vars`;
the operation table and canonical input contract define task-specific resolved
context and exact field presence.

OSAC resolves API references before launch. A provider task does not fetch
Fulfillment registrations or resources by name. The shared `create_subnet`
input contains the full parent VirtualNetwork snapshot and routing-domain
context; when configured, the bound NetworkClass's `vip_prefix_length` is
available as `network_class.vip_prefix_length`. The Fabric Manager uses it to
reserve the provider's configured prefix within the Subnet, and workload
allocators honor the resulting reserved ranges. Each service extension defines
any additional input required by its own workload-specific operation. All
allocators that serve the same Subnet coordinate address use through one
atomic allocator or disjoint ranges, and exclude the registered reserved
CIDRs. [User]

`network_attachment` is a sibling of `resource` and optional `target`. For
attachment and DHCP tasks, `resource` is the target-only `SubnetAttachment`
whose UID and generation identify the tracked job and returned result. The
separate `target` contains the current workload projection, while the request
stores the immutable target placement and selector. The service extension
resolves the selected-port MAC before physical apply, delete, or DHCP dispatch.
For Kubernetes workload-attachment work, OSAC copies the placement's
hosting-cluster ID to `target_cluster.id`; for a placement-scoped Subnet task,
it uses the captured ID from that stage's extension context. Both select the
explicit kubeconfig context. Credential material is not part of any
serialized input. [User]

OSAC uses two manager task entry points for workload attachments:
`apply_subnet_attachment` applies the requested Subnet connection and
SecurityGroup policy; `delete_subnet_attachment` removes policy and
manager-owned attachment state during teardown. The `SubnetAttachment` is the
durable operation owner. The workload service creates and deletes the request
at its lifecycle gates; the networking controller dispatches the task and
retains the request until cleanup succeeds. The [canonical serialized task
inputs and attachment schema](https://github.com/danmanor/enhancement-proposals/blob/722efda/enhancements/OSAC-1433-network-manager-integration-contract-networking/design.md#canonical-serialized-task-inputs)
are normative; this guide does not define a second payload shape.

For a physical bare-metal interface:

1. BMaaS provisions the host over its provisioning connection and resolves the
   selected port for the desired Subnet.
2. After OS provisioning, BMF creates a target-only SubnetAttachment. The
   networking controller invokes Fabric `apply_subnet_attachment` with the
   request as `resource`, the BMI as `target`, and the normalized Subnet,
   selected interface and SecurityGroups. The Fabric Manager binds that port
   to the provider segment behind the Subnet and installs stateful policy.
3. BMF waits for `NetworkAttachmentsReady=True`, which is mirrored only after
   the manager job succeeds and port membership and policy are confirmed. It
   then performs the single tenant handoff reboot and reports
   `NetworkHandoffComplete=True`.
4. The networking controller dispatches the separate `query_dhcp_lease` task
   only after handoff and selected-port MAC resolution. It writes
   `IPDiscoveryComplete=True` after one valid tenant lease is confirmed; BMF
   mirrors the address before reporting the BMI Ready.
5. Before teardown, BMF confirms host shutdown on the tenant network and
   reports `NetworkOffboardShutdownComplete=True`. It then deletes the
   SubnetAttachment and waits for `NetworkOffboardComplete=True` and request
   finalization. The Fabric Manager removes policy and restores the
   extension-defined provisioning baseline; BMF does not deprovision or release
   the host before that cleanup succeeds.

For a VM overlay interface:

1. The Kubernetes Manager prepares the requested Subnet using its selected
   implementation during `create_subnet`. The provider chooses the backing
   Kubernetes objects and controller chain; OSAC defines the shared resource
   and behavior contract.
2. The VM workload job creates the VM halted with no tenant network/interface
   fields and records its hosting cluster, namespace, VM name and UID in the
   immutable request placement. The ComputeInstance lifecycle creates the
   target-only SubnetAttachment. The networking controller invokes Kubernetes
   `apply_subnet_attachment` with the request as `resource`, the ComputeInstance
   as `target`, and the normalized attachment.
3. The manager uses OSAC's protected access to the selected workload cluster
   and patches only network/interface fields on the exact halted VM. It applies
   policy and verifies both the VM update and policy before returning success.
   It does not change the run strategy or start the VM. The request becomes
   `NetworkAttachmentsReady=True` only after the exact manager result and target
   state are accepted.
4. VMaaS waits for `NetworkAttachmentsReady=True`, then applies the requested
   final run strategy. When the VM should run, its first VMI boot therefore has
   the manager-applied network/interface fields and policy. Address discovery
   follows the VM start and reports the real tenant address. The guest OS must
   request DHCP on this interface; the Kubernetes Manager configures the
   network path and does not write a static IP inside the guest.
5. On deletion, VMaaS stops the VM and waits until no VMI remains before
   deleting the SubnetAttachment. The manager removes attachment-owned fields
   and policy from the exact stopped VM. VMaaS deletes the VM only after request
   cleanup completes.

The manager uses the protected kubeconfig at
`OSAC_REMOTE_CLUSTER_KUBECONFIG` with the explicit context named by
`target_cluster.id`. The request carries target identity and placement, never
credentials. These lifecycle examples describe OSAC integration points;
provider backend resources remain manager-owned.


A provider's interface baseline allows only the selected interface/Subnet's
trusted gateway ARP and DHCP setup traffic alongside tenant default deny.
This does not authorize arbitrary tenant traffic or rogue DHCP. CaaS API,
HTTPS, DNS and other bootstrap IP traffic still require normal SecurityGroup
egress rules. Qualify address discovery under deny-all rules and rejection of
untrusted infrastructure traffic separately from normal application policy. [User]

The [manager contract's workload attachment input in proposal PR #357](https://github.com/osac-project/enhancement-proposals/pull/357/files)
defines the physical-interface variants and all rule constraints.

For each Fabric create or reconcile task, OSAC expects one value for every
declared output model whose owner is present in `network_owner_context` and
does not already have a stored value. Existing values are passed to the task
for reuse and are not returned again. A Subnet operation may therefore return
Subnet-owned values and also create a missing VirtualNetwork- or
NetworkClass-owned value when that owner is in context. Later operations reuse
the stored value without returning it again. The provider role knows its own
declarations and labels each value with the resolved owner identity; OSAC
derives and validates the expected keys without asking the task for a missing-
output list. Existing backend allocations must be reused. Because separate
resource tasks may race to produce one missing parent/profile value, each
eligible task must use the NetworkData model and owner identity as an atomic
backend idempotency key and return the same value. Use backend idempotency or
provider-side coordination to prevent duplicate state. Delete and
non-producing tasks return an empty `network_data` list.

OSAC validates the complete result before writing any NetworkData record. If a
value already exists before a new manager dispatch, OSAC passes it to the task
and expects only the still-missing keys. For example, if the Subnet VNI is
already stored but its reserved ranges are not, the Fabric task reuses the VNI
and returns the missing ranges. If OSAC has already accepted a complete result
but a fulfillment-service write persisted only some of its values, the durable
operation journal replays that exact accepted result to finish persistence;
it does not launch the manager task again or recalculate the result. Identical
create retries succeed, while a different value for an existing key is
rejected.

OSAC validates the full result and every value before writing any NetworkData
record. Before a consuming Kubernetes task starts, OSAC verifies that each
applicable declared input has a persisted value. Manager tasks must not write
NetworkData directly or call the fulfillment-service API. For example, a
Fabric task that allocates VLAN 120 for a Subnet returns the value inside the
common result envelope:

```yaml
osac_result:
  resourceID: "<Subnet API ID>"
  resourceUID: "<subnet UID>"
  observedGeneration: 3
  data:
    network_data:
      - model_name: acme-networking-subnet-vlan-id
        owner:
          kind: Subnet
          resourceId: "<Subnet API ID>"
          projectionUID: "<Subnet hub UID>"
        value: 120
```

The operator validates the result and creates a separate `NetworkData` API
record through the fulfillment service. The fulfillment-service database is
authoritative, and its existing controller path projects the record as a CRD
in the networking hub. That record is OSAC's durable value; the provider
manager returns only the model name, owner, and value.
OSAC then passes the validated record to a Kubernetes Manager that declares
the model as an input:

```yaml
osac_job_vars:
  network_data:
    - model_name: acme-networking-subnet-vlan-id
      resource_kind: Subnet
      resource_id: "<owner API ID>"
      value: 120
```

The consumer reads the provider-defined fields and maps their documented
meaning to its backend API. The model name identifies the contract; matching
JSON types alone do not make two values interchangeable.

### Return values, errors, and retries

Every successful task, including `query_dhcp_lease`, returns one AAP
artifact named `osac_result`. The Ansible task publishes it with
`ansible.builtin.set_stats`; see the [Ansible `set_stats` documentation](https://docs.ansible.com/projects/ansible/latest/collections/ansible/builtin/set_stats_module.html).
The artifact echoes the stable fulfillment-service resource ID, the original
request CR UID, and observed generation; it does not repeat an operation or
task identifier because OSAC has that context from the exact tracked AAP job.
Every result contains `data.network_data`; use
an empty list when the task publishes no NetworkData values. Add only the
task-specific field required by that task. For the base tasks, these are
`data.external_ip` for
`create_external_ip`, `data.dhcp_leases` for `query_dhcp_lease`, or
`data.cluster_ingress.address` for `reserve_cluster_ingress_ip`. Optional
capability tasks use the additional result shape defined by their shared
[task/result contract](https://github.com/danmanor/enhancement-proposals/blob/722efda/enhancements/OSAC-1433-network-manager-integration-contract-networking/design.md#manager-result-envelope).
If backend work fails, fail the Ansible task with a diagnostic; do not publish
a success result for incomplete work.

For a task with no NetworkData output:

```yaml
- name: Return manager result to OSAC
  ansible.builtin.set_stats:
    data:
      osac_result:
        resourceID: "<authoritative resource API ID>"
        resourceUID: "<resource UID>"
        observedGeneration: 3
        data:
          network_data: []
```

For ExternalIP allocation, return the reserved address. The manager does not
patch the ExternalIP or any Kubernetes object:

```yaml
osac_result:
  resourceID: "<ExternalIP API ID>"
  resourceUID: "<external IP UID>"
  observedGeneration: 1
  data:
    network_data: []
    external_ip:
      address: 198.51.100.19
```

OSAC reads the result from the exact tracked AAP job and validates its stable
resource ID, projection UID, and generation against that job's request context.
It then validates that the
address is canonical IPv4 and inside the selected pool. OSAC writes the confirmed address to
`ExternalIP.status.address` through its existing status feedback path. A
missing or invalid result leaves the ExternalIP non-ready. Retrying the same
ExternalIP API ID must return the same provider reservation.

DHCP lease lookup uses the same result envelope:

```yaml
osac_result:
  resourceID: "<SubnetAttachment API ID>"
  resourceUID: "<SubnetAttachment request UID>"
  observedGeneration: 1
  data:
    network_data: []
    dhcp_leases:
      - subnet_ref: "<Subnet API ID>"
        interface: primary
        ip_address: 192.0.2.10
        mac_address: "02:00:00:00:00:10"
```

Attachment apply and lease lookup are separate jobs on the same request.
OSAC runs lookup only after BMF reports `NetworkHandoffComplete`, and supplies
the selected interface MAC for BMaaS and CaaS alike. Return one unambiguous
MAC-matching lease for the request's one attachment. A missing or
ambiguous match fails the task. A malformed, missing, stale, or mismatched
result fails the task and does not make the resource Ready.

**Every AAP job must be fully idempotent**, including create, apply, lookup,
allocation, release and delete, and every constituent task. This requirement
also covers the surrounding OSAC workload prepare/start jobs in these flows.
Repeating the same desired input after success or interruption at any step
must converge to the same intended state without duplicate allocations,
objects, rules, or destructive replacement. Backend allocations are keyed by immutable OSAC API resource IDs,
plus interface/placement where required. Kubernetes UIDs identify concrete
objects and fence results/writes; they are not backend allocation keys.
For a Cluster ingress reservation use the Cluster API ID, not ClusterOrder UID.
Recreating a hub projection must recover the same allocation. OSAC accounts
for previous jobs before dispatching new work and retains cleanup state during
deletion; a provider must recover its same API-ID-owned state even
when execution stopped after a backend write but before returning a result.
A compatible role/execution-environment upgrade must preserve identities and
allow unfinished jobs and subsequent reruns to converge. For a supported
future desired-state update, apply must reconcile the requested change;
idempotency is necessary but does not itself define update semantics. The
current immutable resource/NetworkData APIs remain unchanged. [User]

Fulfillment
commits a durable operation intent with the original complete non-secret
input, profile/registration identities, placement and frozen expected outputs
before launching AAP. `LaunchUnknown` is persisted before sending the request.
A lost acknowledgment is recovered by exact `attempt.correlation_id` and
original input, not by a recent job, workload name or empty bounded search.
While any prior execution is unknown or nonterminal, no blind retry or
opposite action may launch. Ambiguity requires authoritative repair. [User]

After AAP execution is terminal, OSAC validates the entire exact-job artifact
and current authoritative API owner/spec/provider binding, then persists it as `ResultAccepted`. It replays
that same accepted artifact after a crash until outputs/address feedback are
`OutputsCommitted`; only then does the stage complete and a consumer start.
A definitive failed attempt may use a new correlation UUID/job ID after
backoff. Providers recover partial creates by API ID and reuse real reservations,
including when no result was accepted. Delete waits for possibly started
operations and confirmed backend effects; an asynchronous queued delete is
not cleanup success. Keep NetworkData and captured placement through all
consumer and Fabric cleanup. [User]

### External address routing and NAT scope

For ExternalIPAttachment, use the supplied confirmed destination address and
`routing_domain.type` (`provider_management` or `tenant_virtual_network`).
Resolve vendor domain identifiers through the provider environment. Do not
infer the destination domain from the workload's tenant attachment; OSAC
supplies the correct context for the selected endpoint. [User]

NATGateway receives the whole immutable VirtualNetwork `ipv4_cidr`. Its source
translation covers that prefix, including later Subnets. Never derive a source
snapshot from the currently existing Subnet ranges or require NAT rule resizing
when a Subnet is created or deleted. [User]

### Ingress address reservation contract

Implement `reserve_cluster_ingress_ip` and `release_cluster_ingress_ip` as
mandatory Fabric entry points. Reserve receives the ClusterOrder and owning
Cluster identities plus resolved tenant Subnet context. Return one real,
exclusive IPv4 reservation in `data.cluster_ingress.address`; retries for the
same owner return the same reservation. Allocate only from the VIP slice
established at Subnet creation using `vip_prefix_length` and excluded from
DHCP/overlay allocation. Reservations do not mutate immutable NetworkData.

Fabric owns IPAM: reserve atomically and prevent duplicate active addresses
within one shared Subnet. The adapter validates its VIP slice and actual
backend reservation. OSAC validates usable Subnet containment and the exact
owner/job result, then records the address in the system-managed ClusterOrder
annotation `osac.openshift.io/ingress-allocated-address`. The provider returns
the standard result below; it does not patch that annotation. OSAC has no
separate ingress allocation registry. An annotation write failure cannot
trigger a new allocation; retries for the same owner return the same address.

Release removes only that owner's reservation and succeeds if it is already
absent. OSAC orders release after consumers are removed and retains the
allocation annotation until confirmed release in enabled mode. Release must
also recover by owner identity when a lost result left no annotation. The
address is separate from an ExternalIP allocation. [User]

```yaml
osac_result:
  resourceID: "<ClusterOrder API ID>"
  resourceUID: "<bound ClusterOrder UID>"
  observedGeneration: 1
  data:
    network_data: []
    cluster_ingress:
      address: 192.0.2.80
```

Kubernetes Managers may use backend-scoped Kubernetes credentials to create
namespaces, CUDNs and attachment policy. Manager jobs cannot mutate the OSAC
registrations, database NetworkData or authoritative resource projections; backend
credentials enter through the existing group Secret/ConfigMap `envFrom` path.
A role may create a job-local credential file from those environment values;
no additional mount or AAP Credential path is introduced. They are never
embedded in job variables or results. [User]

## Step 4: Register models, managers, and NetworkClass

Create objects in dependency order through the provider-facing
fulfillment-service APIs:

1. Create every provider-defined `NetworkDataModel`.
2. Create the Fabric `NetworkManager` and, if needed, the Kubernetes
   `NetworkManager`.
3. Create the `NetworkClass` that selects those logical manager names.

For exact common and per-task payload keys, types and required/absent rules,
use the [canonical serialized task inputs](https://github.com/danmanor/enhancement-proposals/blob/722efda/enhancements/OSAC-1433-network-manager-integration-contract-networking/design.md#canonical-serialized-task-inputs).
The release qualification bundle supplies complete producer/consumer fixtures;
small snippets in this guide illustrate one field and are not full job inputs.

The examples below show request objects sent to the provider-facing private
Fulfillment API; they are not `kubectl apply` manifests. For API requests,
send only resource fields such as `metadata` and `spec`; omit the CRD-only
`apiVersion` and `kind` fields shown in projected-object examples. The gRPC
package is `osac.private.v1`. REST Create sends the resource object as the request body;
the response returns the created object. The API generates each resource `id`.
Model references use NetworkDataModel `metadata.name`; manager selections use
NetworkManager `spec.managerName`. Get and Delete use the generated `id`.

| Resource | gRPC service | Create | List | Get | Delete |
|---|---|---|---|---|---|
| NetworkDataModel | `NetworkDataModels` | `POST /api/private/v1/network_data_models` | `GET /api/private/v1/network_data_models` | `GET /api/private/v1/network_data_models/{id}` | `DELETE /api/private/v1/network_data_models/{id}` |
| NetworkManager | `NetworkManagers` | `POST /api/private/v1/network_managers` | `GET /api/private/v1/network_managers` | `GET /api/private/v1/network_managers/{id}` | `DELETE /api/private/v1/network_managers/{id}` |
| NetworkData | `NetworkData` | `POST /api/private/v1/network_data` (OSAC identity only) | `GET /api/private/v1/network_data` | `GET /api/private/v1/network_data/{id}` | `DELETE /api/private/v1/network_data/{id}` (OSAC identity only) |

Create the deployment's profile after those registrations with the existing
`NetworkClasses.Create` gRPC method or `POST /api/private/v1/network_classes`.
NetworkClass Create resolves the selected manager names and validates their
roles, capabilities, and declared data
compatibility before it stores the profile.

List accepts the private API CEL `filter` parameter. To find one registration
by its object name, use `this.metadata.name == "acme-fabric-manager"`.
Update and Patch are not supported. Cloud Infrastructure Admins use the
existing private API authentication for registrations; only OSAC service
identities create or delete runtime NetworkData. No new CLI is defined.
In REST JSON and generated gRPC clients, manager roles use the protobuf enum
values `NETWORK_MANAGER_ROLE_FABRIC` and `NETWORK_MANAGER_ROLE_KUBERNETES`
shown in the examples.

To list registrations by name over REST, URL-encode the private API CEL
expression in the `filter` query parameter:

```sh
curl --fail-with-body --get \
  "${FULFILLMENT_API}/api/private/v1/network_managers" \
  --header "Authorization: Bearer ${OSAC_PROVIDER_TOKEN}" \
  --data-urlencode 'filter=this.metadata.name == "acme-fabric-manager"'
```

The List response contains the generated `id`; save it for later Get or
Delete. A NetworkClass selects `spec.managerName`; declarations in a manager
select model `metadata.name`. These are separate lookup keys and need not
equal the registration object name.

OSAC installs the common networking models. Use those model names for existing
OSAC-defined values; add a provider-defined model only for a distinct value
that is not already covered by the common models.

Example Fabric registration:

```yaml
apiVersion: osac.openshift.io/v1alpha1
kind: NetworkManager
metadata:
  name: acme-fabric-manager
spec:
  managerName: acme_fabric
  role: NETWORK_MANAGER_ROLE_FABRIC
  description: Fabric networking for the Acme provider
  networkOutputs:
    - acme-networking-subnet-vlan-id
```

The Fabric role reads its endpoint, template name, and credentials from the
networking fulfillment group's environment variables. They are not part of
this registration.

Example Kubernetes registration that consumes the same model:

```yaml
apiVersion: osac.openshift.io/v1alpha1
kind: NetworkManager
metadata:
  name: acme-kubernetes-manager
spec:
  managerName: acme_kubernetes
  role: NETWORK_MANAGER_ROLE_KUBERNETES
  description: Kubernetes VM networking for the Acme provider
  networkInputs:
    - acme-networking-subnet-vlan-id
```

The REST body for the Fabric registration is a JSON resource object with the
same fields. For example, save this object as `network-manager.json` for the
Create call above:

```json
{
  "metadata": {"name": "acme-fabric-manager"},
  "spec": {
    "managerName": "acme_fabric",
    "role": "NETWORK_MANAGER_ROLE_FABRIC",
    "description": "Fabric networking for the Acme provider",
    "networkOutputs": ["acme-networking-subnet-vlan-id"]
  }
}
```

Create the manager with the JSON body, using the provider's existing private
API bearer token:

```sh
curl --fail-with-body --request POST \
  "${FULFILLMENT_API}/api/private/v1/network_managers" \
  --header "Authorization: Bearer ${OSAC_PROVIDER_TOKEN}" \
  --header "Content-Type: application/json" \
  --data-binary @network-manager.json
```

The API's configured provider identity determines authorization; the request
body cannot grant itself permission.

`metadata.name` identifies the registration object. `spec.managerName` is the
logical manager name referenced by `NetworkClass` and the Ansible role
directory name. The role FQCN is derived as `osac.networking.<managerName>`;
there is no separate role reference field. Manager names use the safe lowercase
role format and are unique across Fabric and Kubernetes roles. Fabric Managers declare `networkOutputs` and
leave `networkInputs` empty; Kubernetes Managers declare `networkInputs` and
leave `networkOutputs` empty. Empty lists are valid. Every listed model name
must already exist.

Example `NetworkClass` body, shown as an illustrative fulfillment-service
object rather than a `kubectl` manifest:

```json
{
  "metadata": {"name": "acme-default"},
  "title": "Acme provider networking",
  "fabricManager": "acme_fabric",
  "k8sManager": "acme_kubernetes"
}
```

The `NetworkClass` selects the deployment's Fabric and optional Kubernetes
Manager. OSAC accepts the selection only if the names resolve to registrations
of the correct role and every Kubernetes input model is among the selected
Fabric Manager's output models. Provider template names and other backend settings are
supplied through the networking fulfillment group's ConfigMap and Secret;
they are not fields on NetworkManager or NetworkClass. This is data-contract
validation; it is not a
manager-name allowlist and it is not a behavioral conformance test. The
registration, model, and `NetworkClass` specs are immutable; changes require
replacing the object after removing its dependents. The fulfillment-service
API is authoritative, and the operator may wait for their hub CRD projections
before dispatching provider jobs.

The provider registration APIs support Create, Get, List, and Delete for
`NetworkDataModel` and `NetworkManager`. Update and Patch are rejected. OSAC
validates dependencies at the point where they are created:

| Create request | OSAC validation | Result on failure |
|---|---|---|
| `NetworkDataModel` | Supported owner scope, unique name, and valid inline JSON Schema | Reject the object and identify the invalid field or schema. |
| `NetworkManager` | Valid role-specific fields, valid globally unique `managerName`, and existing model references | Reject the object and identify the invalid declaration or model name. |
| `NetworkClass` | Each logical manager resolves to the expected role; all Kubernetes input model names are present in Fabric outputs | Reject before persisting the profile; no dependent manager job starts. |
| Fabric manager result | Resource UID and generation match the exact tracked job; every internally expected output appears exactly once, with no extra key, valid owner, and schema-matching value | Reject the complete result; do not persist invalid or incomplete NetworkData. |
| Kubernetes manager dispatch | Every declared input applicable to the resource action has one valid owner-scoped NetworkData value after Fabric results have been persisted | Fail before dispatch if an input is absent or invalid. |

If a registration has committed but its hub projection is not ready yet, OSAC
waits and retries reconciliation without dispatching an AAP job. This prevents
an incomplete projected registry from being treated as a valid manager
selection.

## Step 5: Verify the integration

The provider must show that the implementation preserves the shared API
behavior and implements all assigned operations. Registration success alone
proves only that the declared objects and model references passed validation.

OSAC must deliver a provider-runnable qualification selection from its own
`tests/e2e` pytest suite. Providers run the same assertions as OSAC against
their deployed managers and real backend; they do not rewrite the scenarios.
The detailed [contract test plan](https://github.com/danmanor/enhancement-proposals/blob/722efda/enhancements/OSAC-1433-network-manager-integration-contract-networking/testplan.md)
defines the required coverage. This is a delivery requirement: the existing
suite is a starting point, not yet a complete packaged qualification runner.

### Run the shared qualification suite

The target provider workflow is:

1. Select the test bundle matching the OSAC release and record the OSAC,
   manager registration, adapter collection and EE image revisions. OSAC
   supplies a pinned checkout/lockfile and a runner image built from those
   same tests, with the required CLI, Kubernetes and API clients.
2. Supply a documented environment profile: OSAC endpoint and authentication
   references, hub/workload access, exact selected NetworkClass/managers,
   enabled VMaaS/BMaaS/CaaS services, available images/templates and host
   capacity, test CIDRs and external pool, and a reachable external traffic
   probe. Secrets stay in environment variables or referenced secret files.
3. Run preflight. It verifies versions, access, capacity, test ranges and the
   managers selected by the actual NetworkClass before creating resources.
   It must not substitute a bundled manager or enable networking-disabled
   mode. The suite uses uniquely named test-owned resources and never assumes
   ownership of pre-existing tenant resources.
4. Run the published networking qualification selection. Reuse existing
   service journeys and shared fixtures; add missing network assertions and
   tag applicable tests across their owning service directories. The runner
   executes serially by default and uses bounded condition waits. Its release
   manifest lists every required test ID for the selected service profile.
5. Retain JUnit results, a readable coverage summary, sanitized diagnostics,
   version/profile metadata and cleanup results. A failed test exits nonzero;
   missing prerequisites or skipped required tests mark the run incomplete.
   Optional capabilities may be not applicable only when absent from the
   declared profile. A provider no-op never silently skips an applicable
   behavior check or turns its failure into a pass.

Checks include same-Subnet and cross-Subnet traffic, tenant isolation,
stateful policy allow/deny/reply behavior, DHCP/address uniqueness, inbound
DNAT and outbound SNAT, VM/BM/CaaS lifecycle and natural cleanup. They assert
observable behavior through OSAC APIs and workload/external probes, without
hard-coded vendor names or backend APIs.

A separately selected recovery group runs in a dedicated test environment.
It tests reruns after success, interrupted jobs after partial effects,
restarts, lost launch acknowledgments, interrupted cleanup, and compatible
adapter upgrades. OSAC supplies the AAP/controller fault controls needed to
make those scenarios repeatable; starting this group is explicit, and those
controls are absent from ordinary production execution. Contract-level
fault injection complements real E2E packet/lifecycle checks. A smoke run
alone cannot claim recovery coverage.

OSAC owns the test code, fixtures, runner and release coverage manifest; the
provider owns deployment prerequisites, configuration and execution against
its backend. Backend-specific internal correctness checks may supplement the
suite, but cannot replace or weaken its shared assertions. OSAC CI exercises
the same selection with its maintained manager implementations. Providers can
run it locally or in their own CI; no hosted certification service is required.

### Registration and data exchange

- Create a schema-valid model, then confirm an invalid schema is rejected.
- Confirm a manager with a missing model reference is rejected.
- Confirm a `NetworkClass` with the wrong manager role or unresolved logical
  manager name is rejected.
- Confirm a `NetworkClass` is rejected when a Kubernetes input model is not
  declared by the selected Fabric Manager.
- Create the NetworkClass and confirm no provider job starts. Create a
  NetworkClass-scoped model and verify that its first eligible Fabric resource
  operation returns one profile-owned value, which later operations reuse.
  Create VirtualNetwork- and Subnet-scoped models; verify each task returns
  missing values for declared owners present in its context. A child operation
  may first produce a parent-owned value, such as a VirtualNetwork VNI, when
  none is stored. Confirm OSAC stores the complete validated result before the
  Kubernetes task receives only its declared inputs. After all dependents drain,
  inject a NetworkData owner-cleanup failure and verify profile deletion stays
  pending without launching a provider job. Restore cleanup and verify the next
  reconciliation removes profile-owned NetworkData and completes deletion.
- Omit an expected output or return an unexpected/duplicate key; confirm OSAC
  rejects the complete result before storing any new NetworkData or starting
  the Kubernetes task. Also check wrong owners and schema-invalid values.
- Delete the Subnet; confirm the Kubernetes Manager and then Fabric Manager
  receive the existing values until cleanup finishes.

### Shared networking behavior

- Accept overlapping or identical private CIDRs and addresses across isolated
  VirtualNetworks, including different tenants. Keep network identity in
  allocation, routing, policy, DNAT destination lookup and cleanup.
- Distinguish that supported overlap from duplicate independently owned
  addresses in one shared Subnet. Ingress reservation uniqueness is scoped to
  that Subnet; it is never deployment-wide private-IP uniqueness.
- Verify deleting an address or range in one isolated network does not affect
  an equal address or range in another.


- Confirm Subnets within one VirtualNetwork have distinct L2 broadcast domains
  and L3 routing between them, subject to the attached SecurityGroups.
- Confirm workloads attached to one Subnet share its L2 broadcast domain.
- Confirm separate VirtualNetworks remain isolated.
- Confirm SecurityGroup rules apply on each network attachment, are stateful,
  combine as a union, allow established replies, and deny unmatched new
  traffic. For a new flow between two OSAC workloads, test that both source
  egress and destination ingress must allow it: allowing only source egress or
  only destination ingress must fail, while allowing both succeeds. Also test
  that groups combine independently at each endpoint. For traffic to or from
  an endpoint outside OSAC workload policy, verify that only the attached
  workload's rule in that direction controls the flow.
- Observe the provider's policy behavior before readiness and during teardown.
  A provider claiming SecurityGroup enforcement must show policy is in effect
  before traffic is enabled, unmatched traffic is denied, and policy failure
  leaves traffic unavailable. A successful no-op is recorded as a no-op; OSAC
  trusts its valid reported success without probing the data plane. [User]
- Exercise these behaviors for each supported target: ComputeInstance VM
  overlay, BaremetalInstance physical interfaces, including CaaS workers.

### Resource operations and retry behavior

- Exercise every operation and workload-target row in [Step 3](#step-3-implement-the-aap-manager-role),
  including create and delete order for Subnets.
- Confirm every job is safe to repeat after success and after interruption at
  each side-effect boundary, using a new AAP job ID but the same owner/input.
  Verify no duplicates or reallocation, correct recovery of partial work,
  repeated deletion, and convergence after a compatible adapter upgrade.
- Confirm ExternalIP addresses are unique within a pool, a retry for one
  ExternalIP API ID returns its same reservation, and release removes that
  reservation. Confirm the manager returns the address in
  `osac_result.data.external_ip.address`, OSAC validates it, and OSAC writes
  `ExternalIP.status.address` without manager access to the Kubernetes API.
- Confirm ExternalIP attachments are removed before an address is released,
  and NATGateway performs outbound SNAT without providing inbound access.
- Confirm the post-handoff DHCP job is tracked against the SubnetAttachment
  API ID and request UID, and receives the BaremetalInstance API ID and current
  projection UID as `target`, together with its selected-port MAC. Its result
  echoes the SubnetAttachment resource identity, request UID and generation,
  and returns one unambiguous matching lease per request in
  `osac_result.data.dhcp_leases`, with `data.network_data` present. The result
  does not repeat a task or operation identifier; OSAC gets that from the
  tracked AAP job.

Record the task/result checks separately from behavioral conformance evidence.
Every required task file and required real output must be present. Providers
document backend effects and any deliberate no-ops; task success alone proves
the provider reported success, not that a shared networking behavior exists.
Claims of complete shared semantics require evidence for every applicable
operation, target, model and resource behavior. [User]

## Provider readiness checklist

- [ ] Every assigned entry point exists and returns its required valid result.
- [ ] Repeated and interrupted jobs converge without duplicate side effects;
      compatible adapter upgrades preserve owned resources and unfinished work.
- [ ] Models, matching manager declarations and the selected profile are registered.
- [ ] The composed EE, configuration, credentials and infrastructure prerequisites
      are installed; their tested revisions are recorded.
- [ ] The shared suite completed for every supported service and enabled capability;
      required failures, skips and untested areas remain visible.
- [ ] Packet behavior, allocation, lifecycle, cleanup and recovery evidence is retained.
- [ ] Intentional no-ops and their actual effects/limitations are documented.

This checklist indexes implementation and evidence. Checking a box does not
replace a test result, and a no-op's successful return does not prove its
networking effect.

## Changing or removing a manager

`NetworkManager`, `NetworkDataModel`, and `NetworkClass` specs cannot be updated
or patched. To change role content, build and deploy a new AAP execution
environment containing the updated `osac.networking` collection. The role
name remains the registration's `managerName`, so updating task implementation
does not require changing the NetworkManager spec.
To change the manager name, role, input/output list, model meaning, owner
scope, schema, or manager selection, register replacement objects. The
fulfillment service blocks deletion while dependencies remain:

- A `NetworkManager` cannot be deleted while a `NetworkClass` selects its role
  and logical manager name.
- A `NetworkDataModel` cannot be deleted while a manager or NetworkData value
  references it.
- NetworkData remains available until all manager cleanup operations that can
  consume it have succeeded.

Remove dependent tenant resources and the `NetworkClass` as required by the
Unified Networking lifecycle. Then delete registrations in reverse dependency
order and create their replacements. Do not assume that removing a manager
registration also removes provider-side network state.

## Implementation status and conformance ownership

This guide specifies the target provider contract: registration objects and
REST/gRPC APIs, AAP role invocation and task entry points, shared fulfillment-
group settings and secrets, NetworkData exchange, manager
results, ExternalIP allocation, and the minimum
behavioral acceptance checks. The guide describes a proposed OSAC contract;
the current OSAC code still uses the legacy ConfigMap-based manager registry
and does not yet enforce generic NetworkDataModel, NetworkManager, or
NetworkData support. That legacy registry is separate from the
`network-fulfillment-ig` ConfigMap and Secret used to provide AAP job
environment values. Generic OSAC platform implementation must ship before
this onboarding flow can be used end to end. Its delivery includes removing
installer manager-name and dual-manager restrictions, accepting arbitrary
provider Secret keys (the current umbrella schema allows only NETRIS_PASSWORD),
bootstrapping owner-create model production and ingress reservation tasks,
and configuring the pinned provider-composed EE and backend-scoped Kubernetes
authorization. Non-secret networking group ConfigMap keys are already open.
These installer and dispatch changes are generic platform backlog. [User]
This target lifecycle also rejects Update and Patch on `NetworkClass`; the
current private API still permits some NetworkClass field updates and must
change to meet the target contract.

The provider is responsible for verifying each implementation against the
acceptance checks in Step 5 and retaining its own test output and backend
observations as conformance evidence. Registration validation checks declared
models, roles, capabilities, and input/output compatibility; it cannot prove
backend behavior. OSAC does not operate a hosted provider certification
service. A provider may select an implementation with documented no-op behavior when
every required task and result is supplied. OSAC trusts valid provider-reported
success. The provider must qualify each behavioral claim separately, including
any enabled optional capabilities. Current partial
implementations are not evidence of complete behavioral conformance. [User]

## Related documents

- [Unified Networking PRD](https://github.com/danmanor/enhancement-proposals/blob/722efda/enhancements/OSAC-1433-unified-networking/prd.md)
- [Unified Networking Design](https://github.com/danmanor/enhancement-proposals/blob/722efda/enhancements/OSAC-1433-unified-networking/design.md)
- [Network Manager Integration Contract PRD, Design, and Test Plan (proposal PR #357)](https://github.com/osac-project/enhancement-proposals/pull/357/files)
- [Multi-Fabric East-West Networking Design](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1382-multi-fabric-east-west-networking/design.md)
