# OSAC Networking Manager Provider Guide

This guide explains how a cloud provider implements and registers Fabric and
Kubernetes Managers for OSAC networking. It follows the provider workflow from
choosing a manager role through packaging its Ansible role, registering data
models and managers, and checking the resulting behavior.

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
> Generic OSAC platform support described below must ship before a provider can
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
- [Changing or removing a manager](#changing-or-removing-a-manager)
- [Implementation status and conformance ownership](#implementation-status-and-conformance-ownership)
- [Related documents](#related-documents)

## How manager integration works

OSAC gives tenants one networking API across virtual machines, managed
Kubernetes clusters, and bare-metal servers. The same `VirtualNetwork`,
`Subnet`, and other networking resources keep the same meaning across those
workloads. A provider supplies backend implementations behind that API:

- The **Fabric Manager** configures the provider's physical network and handles
  shared networking resources and physical workload interfaces. Every
  `NetworkClass` must select one.
- The **Kubernetes Manager** configures Kubernetes-side networking for VM
  overlays. It is optional when VM workloads are not supported. It does not
  replace the Fabric Manager.

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
to AAP. The task uses `network_owner_context` to label each value and existing
`network_data` to skip values already stored. It must return every expected
value exactly once and no other value. OSAC checks that every applicable
Kubernetes input is stored before starting the Kubernetes job.

## Before you start

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
   `osac.networking.<managerName>`.
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
        ACME_SERVER_CLUSTER_TEMPLATE: spectrum-x-gpu-template
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
interfaces. Its required task set includes creating and deleting
VirtualNetworks and Subnets, applying and removing policy on Cluster and
BaremetalInstance interfaces, managing ExternalIP pools and allocations,
managing ExternalIP attachments and NAT gateways, and querying Dynamic Host
Configuration Protocol (DHCP) leases for Clusters and BaremetalInstances. If
Ethernet east-west support is enabled, it also creates and deletes
FabricDomains.

### Kubernetes Manager

The Kubernetes Manager handles Kubernetes-side Subnet networking and applies
or removes network attachment policy for ComputeInstance VM overlays. It does
not handle shared VirtualNetwork operations, physical workload interfaces, or
Fabric Manager work. A Kubernetes Manager is optional for deployments without
VM workloads.

The normative resource meanings and tenant-visible behavior are in the
[Unified Networking Design](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1433-unified-networking/design.md).
In particular, implementations must preserve these shared semantics:

- A `VirtualNetwork` is a Layer 3 (L3) routing domain. Subnets in it can route
  to each other, subject to SecurityGroup policy.
- Each `Subnet` is a separate Layer 2 (L2) broadcast domain. Workloads attached to the
  same Subnet share that L2 domain.
- SecurityGroup rules are stateful, apply to the workload's network attachment,
  combine as a union, allow established reply traffic, and deny unmatched new
  traffic.
- The current shared networking contract is IPv4-only and permits one tenant
  network attachment per workload.
- ExternalIP attachment provides inbound translation; NATGateway provides
  outbound source translation. They have separate lifecycles and behavior.

Capabilities are a predefined OSAC list, not provider-defined strings. The
only capability in this contract is `EAST_WEST_ETHERNET`, serialized in the
manager API as `NETWORK_MANAGER_CAPABILITY_EAST_WEST_ETHERNET`. The Fabric
`NetworkManager.spec.capabilities` field declares that an implementation can
run Ethernet east-west tasks. The deployment enables that behavior by
setting `NetworkClass.spec.east_west_capabilities.supports_east_west_ethernet`
to true. Both declarations are required: fulfillment-service rejects a
NetworkClass that enables the behavior when its selected Fabric Manager lacks
the capability; when the NetworkClass field is false or omitted, OSAC rejects
a FabricDomain request before AAP dispatch. A capability on a manager alone
does not enable tenant requests.

When enabled, `create_fabric_domain` and `delete_fabric_domain` use the same
generic Fabric Manager registration, collection role, and AAP template as the
other tasks. Backend settings such as a provider template name come from the
networking fulfillment group's environment variables, not NetworkClass or
NetworkManager. The Fabric Manager validates its required values when it
executes the task and preserves the FabricDomain, VirtualNetwork, and Subnet
semantics defined in
the [Unified Networking Design](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1433-unified-networking/design.md)
and [Multi-Fabric East-West Networking Design](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1382-multi-fabric-east-west-networking/design.md).

All networking resources, including `FabricDomain`, use the selected generic
manager contract; no resource has a separate vendor-specific dispatch path.

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
   `VirtualNetwork`, or `Subnet`. The owner determines how long the value lives
   and which operations can receive it. A new owner kind requires OSAC platform
   support.
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
contract. Providers implement the named task files; they do not define or
send an operation string or map one operation name to another.

| Resource lifecycle action | Manager role and order | Workload target | Required task file stem |
|---|---|---|---|
| Create/delete VirtualNetwork | Fabric | None | `create_virtual_network` / `delete_virtual_network` |
| Create/delete Subnet | Fabric, then Kubernetes when configured; reverse order for deletion | None | `create_subnet` / `delete_subnet` |
| Create/delete FabricDomain | Fabric, only when it declares `EAST_WEST_ETHERNET` and NetworkClass enables it | None | `create_fabric_domain` / `delete_fabric_domain` |
| Apply/delete workload attachment | Kubernetes for `ComputeInstance`; Fabric for `Cluster` and `BaremetalInstance` | `ComputeInstance`, `Cluster`, `BaremetalInstance` | `apply_workload_attachment` / `delete_workload_attachment` |
| Create/delete ExternalIPPool | Fabric | None | `create_external_ip_pool` / `delete_external_ip_pool` |
| Allocate/release ExternalIP | Fabric | None | `create_external_ip` / `delete_external_ip` |
| Create/delete ExternalIPAttachment | Fabric | `ComputeInstance`, `Cluster`, `BaremetalInstance` | `attach_external_ip` / `detach_external_ip` |
| Create/delete NATGateway | Fabric | None | `create_nat_gateway` / `delete_nat_gateway` |
| Query DHCP lease | Fabric | `Cluster`, `BaremetalInstance` | `query_dhcp_lease` |

Every selected manager must provide all task files assigned to its role and
supported targets. The task filename stem in the table is the Ansible
`tasks_from` value; the filename in the collection has the `.yml` extension.
Each manager implements the task names assigned to its role with its own
backend behavior. All create/apply and delete tasks must be idempotent by
resource UID.

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

A Fabric Manager that declares `EAST_WEST_ETHERNET` must implement both
FabricDomain task files. Each task receives the FabricDomain, its associated
VirtualNetwork context, required NetworkData, and access to the same
networking-group environment variables as the other manager tasks.
NetworkManager cannot register a partial task set.

Create ordering for a Subnet is Fabric first, then Kubernetes when selected.
Delete ordering is Kubernetes first, then Fabric. OSAC routes workload
attachment tasks to the manager that owns that interface: the Kubernetes
Manager for a VM overlay, and the Fabric Manager for Cluster worker and
bare-metal interfaces.

### AAP inputs for each task

OSAC passes `osac_network_task_from` separately from the
`osac_job_vars` object. OSAC chooses the task filename stem; a provider role
must not override it. The common `osac_job_vars` fields are:

| Field | Meaning |
|---|---|
| `manager` | The selected manager's `name` (equal to its `managerName`) and normalized `role`. OSAC derives the role FQCN from this name. Provider settings are supplied through the job environment, not this field. |
| `resource` | The OSAC resource being reconciled, including its API version, kind, metadata, and spec. The [Unified Networking Design](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1433-unified-networking/design.md) defines each resource's fields and meaning. |
| `network_owner_context` | `owners` lists each live NetworkClass, VirtualNetwork, or Subnet owner relevant to the operation as `{kind, uid}`. OSAC resolves each UID from the hub object's `metadata.uid`. The Fabric role uses this context to label output values. |
| `network_data` | Existing values that apply to this resource action. Kubernetes receives only declared, applicable inputs. Fabric receives existing values for reuse and cleanup. |
| `workload_attachment` | For workload attachment tasks, the normalized Subnet, interface, and complete SecurityGroup policy. |

`workload_attachment` is a sibling of `resource`. OSAC keeps `resource` as the
owning workload so its UID and generation identify the job. The normalized
attachment includes the resolved Subnet and parent VirtualNetwork, the
interface or interfaces, and the complete ingress and egress rules of each
attached SecurityGroup. On apply, establish the Subnet connection and install
policy before traffic is enabled. On delete, remove workload-owned policy and
detach the interface before workload teardown.

For a VM, the Kubernetes Manager receives an overlay interface; for Cluster
and BaremetalInstance targets, the Fabric Manager receives resolved physical
interfaces. The input shape and policy requirements are:

```yaml
osac_network_task_from: apply_workload_attachment
osac_job_vars:
  manager: {name: acme_kubernetes, role: kubernetes}
  resource:
    kind: ComputeInstance
    metadata: {uid: "<workload UID>", generation: 3}
  network_owner_context:
    owners:
      - {kind: NetworkClass, uid: "<networkclass UID>"}
      - {kind: VirtualNetwork, uid: "<network UID>"}
      - {kind: Subnet, uid: "<subnet UID>"}
  workload_attachment:
    subnet:
      uid: "<subnet UID>"
      name: app-subnet
      ipv4_cidr: 192.0.2.0/24
      virtual_network: {uid: "<network UID>", name: app-network}
    interfaces:
      - {type: overlay, name: primary}
    security_groups:
      - uid: "<group UID>"
        name: web
        ingress:
          - {protocol: TCP, port_from: 443, port_to: 443, ipv4_cidr: 0.0.0.0/0}
        egress: []
```

The [manager contract's workload attachment input in proposal PR #357](https://github.com/osac-project/enhancement-proposals/pull/357/files)
defines the physical-interface variants and all rule constraints.

A Fabric task returns values according to the provider's implementation of
its manager's declared `networkOutputs`. The provider-authored role already
knows those model names; OSAC does not need to send the registration list as
an AAP input. OSAC derives the required
model/owner keys from that declaration, the owner scopes in
`network_owner_context`, and NetworkData already stored. It does not send the
computed set to AAP. The task uses `network_owner_context.owners` to set each
output's owner kind and UID, and existing `network_data` to skip values
already stored. It returns each required key exactly once and no other key.
A model whose owner scope is absent from the context, or whose value is
already stored, is not expected again. On delete, the task receives existing
values and owner context for cleanup and returns no new NetworkData.
OSAC validates the full result and every value before writing any NetworkData
record. Before a consuming Kubernetes task starts, OSAC verifies that each
applicable declared input has a persisted value. Manager tasks must not write
NetworkData directly or call the fulfillment-service API. For example, a
Fabric task that allocates VLAN 120 for a Subnet returns the value inside the
common result envelope:

```yaml
osac_result:
  resourceUID: "<subnet UID>"
  observedGeneration: 3
  data:
    network_data:
      - model_name: acme-networking-subnet-vlan-id
        owner:
          kind: Subnet
          uid: "<subnet UID>"
        value: 120
```

The fulfillment service validates the result and creates a separate
`NetworkData` API object with a generated name. That object is OSAC's durable
record; the provider manager returns only the model name, owner, and value.
OSAC then passes the validated record to a Kubernetes Manager that declares
the model as an input:

```yaml
osac_job_vars:
  network_data:
    - model_name: acme-networking-subnet-vlan-id
      resource_kind: Subnet
      resource_uid: "<subnet UID>"
      value: 120
```

The consumer reads the provider-defined fields and maps their documented
meaning to its backend API. The model name identifies the contract; matching
JSON types alone do not make two values interchangeable.

### Return values, errors, and retries

Every successful task, including `query_dhcp_lease`, returns one AAP
artifact named `osac_result`. The Ansible task publishes it with
`ansible.builtin.set_stats`; see the [Ansible `set_stats` documentation](https://docs.ansible.com/projects/ansible/latest/collections/ansible/builtin/set_stats_module.html).
The artifact includes the owning resource UID and the observed generation; it
does not repeat an operation or task identifier because OSAC has that context
from the exact tracked AAP job. Every result contains `data.network_data`; use
an empty list when the task publishes no NetworkData values. Add only the
task-specific field required by that task: `data.external_ip` for
`create_external_ip` or `data.dhcp_leases` for `query_dhcp_lease`.
If backend work fails, fail the Ansible task with a diagnostic; do not publish
a success result for incomplete work.

For a task with no NetworkData output:

```yaml
- name: Return manager result to OSAC
  ansible.builtin.set_stats:
    data:
      osac_result:
        resourceUID: "<resource UID>"
        observedGeneration: 3
        data:
          network_data: []
```

For ExternalIP allocation, return the reserved address. The manager does not
patch the ExternalIP or any Kubernetes object:

```yaml
osac_result:
  resourceUID: "<external IP UID>"
  observedGeneration: 1
  data:
    network_data: []
    external_ip:
      address: 198.51.100.19
```

OSAC reads the result from the exact tracked AAP job and validates its UID and
generation against that job's request context. It then validates that the
address is canonical IPv4 and inside the selected pool. OSAC writes the confirmed address to
`ExternalIP.status.address` through its existing status feedback path. A
missing or invalid result leaves the ExternalIP non-ready. Retrying the same
ExternalIP UID must return the same provider reservation.

DHCP lease lookup uses the same result envelope:

```yaml
osac_result:
  resourceUID: "<workload UID>"
  observedGeneration: 1
  data:
    network_data: []
    dhcp_leases:
      - subnet_ref: "<subnet UID>"
        interface: primary
        ip_address: 192.0.2.10
        mac_address: "02:00:00:00:00:10"
```

Return one unambiguous lease for each requested attachment. A missing or
ambiguous match fails the task. A malformed, missing, stale, or mismatched
result fails the task and does not make the resource Ready.

Create/apply and delete tasks must be idempotent by resource UID;
workload-attachment operations are also scoped by interface. A retry may be a
new AAP job with a new job ID. OSAC polls the exact job ID it launched and does
not treat another job's result as success. For Subnet create retries, reuse
NetworkData values already persisted by OSAC rather than allocating
replacements. During delete, keep values until consumer and Fabric cleanup have
succeeded.

## Step 4: Register models, managers, and NetworkClass

Create objects in dependency order through the provider-facing
fulfillment-service APIs:

1. Create every provider-defined `NetworkDataModel`.
2. Create the Fabric `NetworkManager` and, if needed, the Kubernetes
   `NetworkManager`.
3. Create the `NetworkClass` that selects those logical manager names.

The examples below show request objects sent to the provider-facing private
Fulfillment API; they are not `kubectl apply` manifests. For API requests,
send only resource fields such as `metadata` and `spec`; omit the CRD-only
`apiVersion` and `kind` fields shown in projected-object examples. The gRPC
package is `osac.private.v1`. REST Create sends the resource object as the request body;
the response returns the created object. The API generates each resource `id`.
References use `metadata.name`; Get and Delete use the generated `id`.

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
In REST JSON and generated gRPC clients, `NetworkManager.role` and
`NetworkManager.capabilities` use the protobuf enum values
`NETWORK_MANAGER_ROLE_FABRIC`, `NETWORK_MANAGER_ROLE_KUBERNETES`, and
`NETWORK_MANAGER_CAPABILITY_EAST_WEST_ETHERNET` shown in the examples.

To list registrations by name over REST, URL-encode the private API CEL
expression in the `filter` query parameter:

```sh
curl --fail-with-body --get \
  "${FULFILLMENT_API}/api/private/v1/network_managers" \
  --header "Authorization: Bearer ${OSAC_PROVIDER_TOKEN}" \
  --data-urlencode 'filter=this.metadata.name == "acme-fabric-manager"'
```

The List response contains the generated `id`; save it for later Get or
Delete. References in a NetworkClass or another registration continue to use
the stable resource name.

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
  capabilities:
    - NETWORK_MANAGER_CAPABILITY_EAST_WEST_ETHERNET
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
    "capabilities": ["NETWORK_MANAGER_CAPABILITY_EAST_WEST_ETHERNET"],
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
  "fabricManager": "acme_fabric",
  "k8sManager": "acme_kubernetes",
  "spec": {
    "eastWestCapabilities": {"supportsEastWestEthernet": true}
  }
}
```

The `NetworkClass` selects the deployment's Fabric and optional Kubernetes
Manager. OSAC accepts the selection only if the names resolve to registrations
of the correct role and every Kubernetes input model is among the selected
Fabric Manager's output models. To enable Ethernet east-west support, its
specification also sets `east_west_capabilities.supports_east_west_ethernet`
to true; OSAC accepts that only if the selected Fabric Manager declares
`EAST_WEST_ETHERNET`. Provider template names and other backend settings are
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

Use the acceptance checks below and the cases in the manager contract's
[Test Plan in proposal PR #357](https://github.com/osac-project/enhancement-proposals/pull/357/files)
as the provider conformance suite. Run them against the AAP execution
environment and actual backend that will be selected. OSAC may use these cases
for its own supported implementations; it does not run a hosted provider
certification service.

### Registration and data exchange

- Create a schema-valid model, then confirm an invalid schema is rejected.
- Confirm a manager with a missing model reference is rejected.
- Confirm a `NetworkClass` with the wrong manager role or unresolved logical
  manager name is rejected.
- Confirm a `NetworkClass` is rejected when a Kubernetes input model is not
  declared by the selected Fabric Manager.
- Confirm a NetworkClass that enables Ethernet east-west is rejected unless
  the selected Fabric Manager declares `EAST_WEST_ETHERNET`; confirm a
  declaration on the manager alone does not enable FabricDomain requests.
- Create a VirtualNetwork and then a Subnet; confirm OSAC expects only models
  whose owner scopes are present and whose values are not already stored.
  Confirm the Fabric task returns every expected model/owner key once, OSAC
  stores the values, and the Kubernetes task receives only its declared
  inputs.
- Omit an expected output or return an unexpected/duplicate key; confirm OSAC
  rejects the complete result before storing any new NetworkData or starting
  the Kubernetes task. Also check wrong owners and schema-invalid values.
- Delete the Subnet; confirm the Kubernetes Manager and then Fabric Manager
  receive the existing values until cleanup finishes.

### Shared networking behavior

- Confirm Subnets within one VirtualNetwork have distinct L2 broadcast domains
  and L3 routing between them, subject to the attached SecurityGroups.
- Confirm workloads attached to one Subnet share its L2 broadcast domain.
- Confirm separate VirtualNetworks remain isolated.
- When Ethernet east-west is enabled, create and delete a `FabricDomain`
  through the selected Fabric Manager and confirm it does not change
  VirtualNetwork routing or Subnet broadcast domains.
- Confirm SecurityGroup rules apply on each network attachment, are stateful,
  combine as a union, allow established replies, and deny unmatched new
  traffic.
- Confirm policy is in effect before a workload is made Ready and is removed
  before workload teardown. A policy failure must leave traffic unavailable
  and the workload non-ready.
- Exercise these behaviors for each supported target: ComputeInstance VM
  overlay, Cluster worker interface, and BaremetalInstance physical interface.

### Resource operations and retry behavior

- Exercise every operation and workload-target row in [Step 3](#step-3-implement-the-aap-manager-role),
  including create and delete order for Subnets.
- Confirm create/apply and delete are safe to repeat for the same resource UID
  and that a failed attempt can be retried with a new AAP job ID.
- Confirm ExternalIP addresses are unique within a pool, a retry for one
  ExternalIP UID returns its same reservation, and release removes that
  reservation. Confirm the manager returns the address in
  `osac_result.data.external_ip.address`, OSAC validates it, and OSAC writes
  `ExternalIP.status.address` without manager access to the Kubernetes API.
- Confirm ExternalIP attachments are removed before an address is released,
  and NATGateway performs outbound SNAT without providing inbound access.
- Confirm DHCP lease lookup returns one unambiguous lease per requested
  attachment in `osac_result.data.dhcp_leases`, with the resource UID,
  observed generation, and `data.network_data` list present. The result does
  not repeat a task or operation identifier; OSAC gets that from the tracked
  AAP job.

Do not declare a new implementation ready for tenant use until provider
conformance evidence covers every applicable operation, target, data model,
and shared resource behavior.

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
results, FabricDomain handling, ExternalIP allocation, and the minimum
behavioral acceptance checks. The guide describes a proposed OSAC contract;
the current OSAC code still uses the legacy ConfigMap-based manager registry
and does not yet enforce generic NetworkDataModel, NetworkManager, or
NetworkData support. That legacy registry is separate from the
`network-fulfillment-ig` ConfigMap and Secret used to provide AAP job
environment values. Generic OSAC platform implementation must ship before
this onboarding flow can be used end to end.
This target lifecycle also rejects Update and Patch on `NetworkClass`; the
current private API still permits some NetworkClass field updates and must
change to meet the target contract.

The provider is responsible for verifying each implementation against the
acceptance checks in Step 5 and retaining its own test output and backend
observations as conformance evidence. Registration validation checks declared
models, roles, capabilities, and input/output compatibility; it cannot prove
backend behavior. OSAC does not operate a hosted provider certification
service. A deployment must not select a manager
for tenant use until its provider has verified every operation and applicable
target, including FabricDomain when Ethernet east-west is enabled.

## Related documents

- [Unified Networking PRD](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1433-unified-networking/prd.md)
- [Unified Networking Design](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1433-unified-networking/design.md)
- [Network Manager Integration Contract PRD, Design, and Test Plan (proposal PR #357)](https://github.com/osac-project/enhancement-proposals/pull/357/files)
- [Multi-Fabric East-West Networking Design](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1382-multi-fabric-east-west-networking/design.md)
