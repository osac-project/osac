# NICo-backed BMaaS: integration audit and first vertical slice

Status: audit begun 2026-10-22; pilot implementation and simulator evidence
updated 2026-10-27 in [nico-bmaas-pilot.md](nico-bmaas-pilot.md). This page
records the baseline gap analysis and NICo v2 REST contract. Its proposed
implementation order below predates the running pilot.

## Goal and boundary

The first deliverable is one BareMetalInstance (BMI) created through OSAC,
observed through its lifecycle, and deleted only after the NICo Machine that
hosted it is confirmed `Ready` and unassigned. Use a simulation-only NICo site
with Machine-a-Tron. Keep CaaS out of the first end-to-end test; later CaaS
workers should order BMIs through the same backend.

The pilot can use one configured OSAC tenant mapped to one NICo Tenant, one
site, one pre-existing NICo VPC/prefix, one instance type, and one approved
NICo Operating System. This limits initial provisioning work without weakening
the BMI ownership or cleanup checks. A NICo Instance is a tenant allocation;
a NICo Machine is the physical host. Their IDs must be stored separately.

## Existing OSAC paths

| Path | Current behavior | Assumption or gap for standalone BMaaS |
| --- | --- | --- |
| `osac-aap/.../nico/steps/roles/cluster_infra/tasks/create.yaml` | Gets a JWT, snapshots Agent CRs, creates cluster infrastructure, then handles NodePool scale changes and Agent labels. | Requires ClusterOrder, HyperShift/Assisted Installer resources, and a cluster working namespace. It is not a BMI entry point. |
| `create_nico_infra.yaml` | Creates or adopts VPC/prefix, optionally peers a management VPC, and saves IDs/created flags in a cluster ConfigMap. | BMI can start with a pre-existing tenant VPC/prefix. Cluster peering and ConfigMap ownership do not fit a single BMI. Its ConfigMap currently lacks OSAC tenant and owner-reference annotations. |
| `create_instances.yaml` / `create_instances_for_resource_class.yaml` | Gets InfraEnv iPXE, resolves a NICo instance type by name, reconciles instance count by `cluster`/`resource_class` labels, calls `nvidia.bare_metal.instance`, waits for Agent MACs, and stores instance IDs. | BMI needs one stable request keyed by BMI UID, a BMI OS/image mapping, and both NICo instance and Machine IDs. Random names plus count reconciliation cannot safely recover an uncertain create response. |
| `delete.yaml` / `delete_nico_infra.yaml` | Deletes Agents, then instances, peering, prefix, VPC, and the cluster ConfigMap. | `failed_when: false` on the instance/resource deletes permits removing the ConfigMap after a failure. No Machine `Ready` check is made. Do not call this a BMaaS cleanup path. |
| `scale_down_cleanup.yaml` | Deletes selected instances and removes successful IDs from the ConfigMap. | `ignore_errors: true` avoids aborting scale-down but still does not verify Machine `Ready`; its summary uses requested rather than verified deletion count. |
| `osac-aap/.../bm_host_provisioning` | Dispatches `create_{{ host_class }}.yaml` and `delete_{{ host_class }}.yaml`; supported classes are Metal3 and OpenStack. | A `nico` task file is straightforward, but merely adding it cannot work end to end because host allocation occurs before this job. |
| `bare-metal-fulfillment-operator` | `FindFreeHost` → set `spec.externalHostID` → `AssignHost` → set `hostClass` → management → AAP provision; deletion runs network/power/AAP steps before `UnassignHost`. | NICo atomically claims a Machine when the tenant creates an Instance. The inventory `AssignHost` API lacks tenant, VPC, OS/image and BMI context; it cannot safely create a NICo Instance. AAP job success currently does not prove NICo cleanup-to-Ready. |

The vendored `nvidia.bare_metal.instance` module's `wait: true` on deletion
polls the Instance GET until it returns 404. It does **not** poll the Machine.
The collection is pinned at 1.3.0, and OSAC defaults `api_path_prefix` to
`forge`; the current public REST documentation uses `/nico/`. Confirm the
deployed NICo version and path prefix before calling this collection against
the simulator. Do not edit `vendor/` to change this behavior.

The accepted [BMaaS networking design](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1437-bmaas-networking/design.md)
allows one fabric attachment and puts the port move in the generic network
dispatcher. A NICo VPC-backed Instance also configures the host network.
The first implementation must choose one owner for that port/network state and
ensure that OSAC's automatic network handoff/deletion does not duplicate it.
For the pilot, resolve a single OSAC Subnet to the pre-existing NICo VPC prefix
and treat NICo as the fabric owner; retain OSAC's tenant and owner annotations
on the BMI and any new child resources. The exact adapter/dispatcher hook
requires a design review against the current NetworkClass configuration.

## NICo REST contract

`{base}` below is the NICo REST API origin; `{org}` is the NICo organization
name, **not** the OSAC tenant ID. Tenant operations require a JWT with the
tenant org's `TENANT_ADMIN` role. Machine inspection after an Instance has been
deleted requires a separate provider org credential with `PROVIDER_ADMIN`:
the tenant credential can inspect only Machines associated with its active
Instances. Store both credentials in secrets and never pass them in BMI fields,
logs, or Ansible artifacts.

| Purpose | Method and path | Required behavior |
| --- | --- | --- |
| Create | `POST {base}/v2/org/{tenantOrg}/nico/instance` | Body includes `name`, `tenantId`, `vpcId`, `instanceTypeId` or an authorized `machineId`, the catalog `operatingSystemId` (never an inline `ipxeScript`), `phoneHomeEnabled: true`, `userData` when the tenant supplied an SSH key or cloud-config, and one physical `interfaces` entry using `vpcPrefixId`/`subnetId`. Before the POST, `GET operating-system/{id}` must show a `Ready` `TemplatedIpxe` definition synced to the VPC's Site that references `${tenant-cloudinit-url}`. Persist returned `id`; persist `machineId` as soon as NICo assigns it and before BMI reaches Ready. Labels should include an OSAC ownership key derived from BMI UID and tenant ID. |
| Observe | `GET {base}/v2/org/{tenantOrg}/nico/instance/{instanceId}` | Read `status`, `machineId`, `tenantId`, and labels. The documented normal sequence is `Pending` → `Provisioning` → `Ready`; `Error` needs an explicit failure condition. With `phoneHomeEnabled`, NICo keeps the Instance `Provisioning` until the installed OS posts to the Site phone-home URL (`site.phoneHomeUrl` in the NICo REST configuration), so `Ready` is the boot-readiness evidence. `Configuring`, `Repairing`, `Updating`, and `Rebooting` are also possible after creation. |
| Diagnose | `GET {base}/v2/org/{tenantOrg}/nico/instance/{instanceId}/status-history` | Capture messages when creation, update, or deletion stalls; this is diagnostic evidence, not a readiness gate. |
| Change/reboot | `PATCH {base}/v2/org/{tenantOrg}/nico/instance/{instanceId}` | `triggerReboot`, `rebootWithCustomIpxe`, `applyUpdatesOnReboot`, OS, SSH keys, interfaces, and other updates exist. Map OSAC start/stop/restart semantics explicitly before enabling those controls; a reboot endpoint is not a power-off endpoint. |
| Delete | `DELETE {base}/v2/org/{tenantOrg}/nico/instance/{instanceId}` | HTTP 202 means accepted, not cleaned. Save `instanceId` and `machineId`; poll Instance status/GET through `Terminating` until absence, then continue Machine checks. A missing Instance is safe to treat as already deleted only when its ownership and Machine ID were previously recorded. |
| Verify release | `GET {base}/v2/org/{providerOrg}/nico/machine/{machineId}` | Require `status == Ready` and `instanceId == null`. Check no blocking health issue; use Core `managed-host show` and `machine health-report show` for site-level evidence or a stuck cleanup. Do not remove the BMI finalizer on timeout, `Error`, `Maintenance`, stale assignment, or unknown Machine. |
| Diagnose Machine | `GET {base}/v2/org/{providerOrg}/nico/machine/{machineId}/status-history` and `GET {base}/v2/org/{providerOrg}/nico/machine/{machineId}/health-report` | Record cleanup progress and inspect reported alerts when the Machine does not become `Ready`. Do not infer readiness from `isUsableByTenant`; NICo says that field does not mean a Machine is available for a new Instance. |

NICo's create API supports targeted `machineId` and `machineLabelSelector`
only with the tenant's `targetedInstanceCreation` capability. A Machine that
looks `Ready` is not reserved by that read: create may return 409 because
another request won. The first slice should let NICo select by instance type
unless precise placement is required. On a lost/uncertain create response,
search by the stable BMI ownership label and reconcile the result; NICo warns
that an empty REST result alone does not rule out a Core allocation. Do not
blindly issue a second POST. Escalate ambiguous Core allocations to a site
operator.

NICo's normal release state sequence is
`Assigned/BootingWithDiscoveryImage` → `Assigned/SwitchToAdminNetwork` →
`Assigned/WaitingForNetworkReconfig` → optional attestation →
`WaitingForCleanup/Init` → host sanitization →
`BomValidating/UpdatingInventory` → `Ready`. NICo removes tenant network
state before host cleanup. The OSAC deletion gate is the conjunction of
Instance absence, Machine unassignment/`Ready`, and no blocking cleanup
health report. It must retain the BMI finalizer and durable provider IDs until
that gate passes. Do not delete the Machine object; that is decommissioning,
not tenant cleanup.

## Proposed implementation order

1. **Simulator and contract fixture.** Deploy a simulation-only NICo Core
   site with Machine-a-Tron and the DPF simulator. Verify one ingested Machine
   is `Ready`. Exercise the REST create/read/delete/read-Machine sequence
   manually with two scoped credentials. Record the exact NICo image/version,
   REST path prefix, instance type, OS, VPC/prefix, and cleanup timing.
2. **Small reusable NICo lifecycle role.** Extract JWT handling and
   instance create/observe/delete into a standalone role under `nico/steps`
   or `bm_host_provisioning` with explicit BMI context. Use a stable BMI UID
   label/name, one Instance per BMI, a pre-existing VPC/prefix, and AAP
   `set_stats` artifacts for `{instanceId,machineId}`. Delete by saved IDs,
   verify the prior tenant/ownership label before acting, then poll with a
   provider-scoped read until the Machine is `Ready`. Fail closed on timeout;
   do not use `failed_when: false` or discard identifiers. Keep the existing
   `cluster_infra` flow isolated until this contract is proven.
3. **Operator sequencing and persistence.** Add a NICo-managed BMI path in
   the bare-metal operator that invokes create with the full BMI spec before
   treating the host as allocated. Read AAP artifacts and persist both NICo
   IDs durably (prefer status fields, with CRD generation). Do not implement
   NICo by making the existing `AssignHost` create an Instance with static
   credentials: its method signature lacks the data and ownership needed for
   a correct call. Make the NICo path's management and network steps explicit
   so Metal3 power and generic port moves are not sent to NICo hardware.
   On delete, keep the BMI finalizer until the standalone role confirms
   cleanup-to-Ready; retain IDs and a visible failing condition for retries.
4. **Tenant and catalog wiring.** Map the OSAC tenant to a NICo Tenant/org,
   instance type to a NICo Instance Type, approved image to NICo Operating
   System/iPXE, and the one OSAC network attachment to a NICo prefix. Reject
   missing or cross-tenant mappings before POST. Publish the BMI template and
   install secret references without embedding credentials in a catalog item.
5. **End-to-end proof.** Create one BMI through OSAC API/CLI, observe OSAC and
   NICo `Ready`, delete it through OSAC, and prove the original NICo Machine
   reaches unassigned `Ready`. Repeat with create timeout, delete timeout,
   lost POST response, 409 competing allocation, and blocked cleanup. The
   first test belongs to OSAC's deployed E2E tier; role/API boundary cases
   belong to AAP contract tests and operator unit/envtest respectively.

Machine-a-Tron deployment is not a small Ansible mock: NVIDIA's guide
requires a simulation-only Core site, DPF simulator, Helm chart, image
registry, `kubectl`, `helm`, and a build/push path. Do not run it alongside
real hardware. The local workspace currently has Podman but lacks `kind`,
`helm`, `kubectl`, and `devspace` on PATH, and no NICo/Machine-a-Tron container
was running when this audit was made. The subsequent two-guest simulator
installation and REST/AAP role results are documented in the pilot record.

## Source references

- [NVIDIA Instance create](https://docs.nvidia.com/infra-controller/rest-api-reference/api-reference/instance/create-instance), [get](https://docs.nvidia.com/infra-controller/rest-api-reference/api-reference/instance/get-instance), [status history](https://docs.nvidia.com/infra-controller/rest-api-reference/api-reference/instance/get-instance-status-history), [update](https://docs.nvidia.com/infra-controller/rest-api-reference/api-reference/instance/update-instance), and [delete](https://docs.nvidia.com/infra-controller/rest-api-reference/api-reference/instance/delete-instance)
- [NVIDIA Machine get and authorization](https://docs.nvidia.com/infra-controller/rest-api-reference/api-reference/machine/get-machine), [Machine status history](https://docs.nvidia.com/infra-controller/rest-api-reference/api-reference/machine/get-machine-status-history), [health reports](https://docs.nvidia.com/infra-controller/rest-api-reference/api-reference/machine/get-all-machine-health-report), [lifecycle status FAQ](https://docs.nvidia.com/infra-controller/documentation/reference/fa-qs), and [tenant lifecycle cleanup](https://docs.nvidia.com/infra-controller/documentation/operations-day-2/tenant-lifecycle-cleanup)
- [NVIDIA Machine-a-Tron deployment](https://docs.nvidia.com/infra-controller/documentation/reference/development/machine-a-tron-deployment)
