# OSAC AAP

Ansible roles and playbooks used by OSAC to provision infrastructure through
Ansible Automation Platform.

This component is part of the OSAC monorepo, not an isolated project. Its APIs,
generated artifacts, deployment configuration, and runtime behavior may affect
other components. Apply the repository-wide rules in
[`../AGENTS.md`](../AGENTS.md), consider downstream consumers before changing
behavior, and follow the instructions for every affected component.

## Required context

Before changing this component, identify the documents relevant to the change
below, then read and follow them. These documents are authoritative for their
respective areas.

- Component setup and playbook entry points: [`README.md`](README.md)
- Role and workflow examples: `collections/ansible_collections/osac/`
- Integration fixtures and targets: `tests/integration/`
- Deployment contracts: [`../docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md)

## Invariants

- `pyproject.toml` and `uv.lock` own development dependencies; rerun
  `uv sync --all-groups` after changing them.
- Local collections live under `collections/ansible_collections/`; third-party
  collections live under `vendor/`. `ansible.cfg` searches `vendor/` before
  local collections, so stale vendored content can hide local changes.
- After changing `collections/requirements.yml`, reinstall the collections and review the complete `vendor/` diff.
- Use fully qualified Ansible module names and give every task a `name:`.
- Role directories use underscores. A network template role must define at least one `fabric_manager` or `k8s_manager`, and manager names must match the corresponding role directory names.
- A template role's `meta/osac.yaml` must match its actual `template_type`; verify the real template before copying fields from another type.
- Network roles are not published as ComputeClass-family resources by `playbook_osac_config_as_code.yml`. Cluster, compute-instance, and bare-metal template metadata is published by config-as-code; do not assume that behavior applies to network roles.
- NetworkClass resources are created by the installer from configured manager names; role capabilities are informational. Only one NetworkClass may exist per deployment, so adding a backend replaces the configured NetworkClass rather than creating another.
- Preserve tenant and owner-reference annotations on resources created by playbooks.
- Use the shared remote-kubeconfig service role for remote workflows and preserve the required empty primary-network label syntax.
- Never expose credentials in tasks, logs, fixtures, examples, or generated artifacts.

## Integration Testing

See [suite boundaries and coverage gaps](../docs/INTEGRATION-TESTING.md#osac-aap).

| Touched area | Required validation | Command / follow-up |
|---|---|---|
| Filters, variable transforms, and isolated plugin logic | Unit | `uv run pytest tests/unit` |
| Netris ExternalIP session transport and redirect isolation | Unit / local HTTP transport | `uv run pytest tests/unit/test_netris_external_ip_transport.py`; real Ansible HTTP and module subprocesses against a cookie-enforcing loopback fixture, including missing/incorrect-session rejection and redirects with and without response cookies. Both supplied and response-derived cookies must stay off redirect targets. No Kind, AAP, or real provider. The loopback provider is `tests/unit/netris_session_fixture.py`; it requires the exact session cookie on every request. |
| Keycloak token lookup, cache refresh, and error handling | Unit | `uv run pytest tests/unit/plugins/lookup/test_fulfillment_token.py` |
| AAP publish-templates credential and Kubernetes token wiring | Unit | `uv run pytest tests/unit/test_publish_templates_auth_config.py` |
| Template publishing certificate validation | Local protocol integration | Run the `test_cert_validation` play in `collections/ansible_collections/osac/service/roles/publish_templates/tests/test.yml` with `uv run ansible-playbook`; the mock HTTPS server tests the client transport only |
| Config-as-code networking templates and launch policy | Component contract | `tests/integration/targets/config_as_code_job_templates/tasks/baseline.yml`; renders controller templates without contacting AAP |
| Config-as-code container-group pod specs, including the CaaS Netris settings of cluster fulfillment | Unit plus component contract | `tests/unit/test_config_as_code_cluster_netris_settings.py`, `tests/unit/test_publish_templates_auth_config.py` and `tests/integration/targets/config_as_code_pod_specs/tasks/baseline.yml`; renders the pod specs and the AAP chart's config-as-code Secret without contacting AAP |
| Generic networking task playbook: task allowlists, launch input validation, task-file lookup, and typed result handling | Unit plus component integration | `tests/unit/plugins/lookup/test_launch_extra_vars.py`, `tests/unit/plugins/lookup/test_network_task_file.py`, and `tests/integration/targets/network_task_playbook/tasks/baseline.yml`; child Ansible processes without Kubernetes or AAP check the fixed task sets, input rejection that runs no manager task, missing role or task file, and one real task (the Netris SecurityGroup no-op); running every task of a set is the manager target (`netris_fabric`, `cudn_evpn`) |
| AgentlessNet VirtualNetwork/Subnet allocation, current SQLite schema-v1 validation, ExternalIPPool and ExternalIPAttachment identity/deletion checks, parent locks, VLAN interfaces, switch discovery parsing, and derived DHCP-manager helpers | Unit | `tests/unit/test_agentless_network_state.py`, `tests/unit/test_agentless_net_network.py`, and `tests/unit/test_agentless_net_subnet.py`; run `uv run pytest tests/unit` |
| AgentlessNet ExternalIPAttachment DNAT, scoped forwarding, local route, and conntrack cleanup | Unit | `tests/unit/test_agentless_net_external_ip.py`; run `uv run pytest tests/unit` |
| AgentlessNet VN and Cumulus Subnet inventory, host registration, trunk validation, and password rejection | Contract | `agentless_net_stub` baseline target runs `files/validate_vn_inventory.yml` in fresh Ansible processes; omitted switch platform defaults to Cumulus and explicit unsupported platforms fail; no SSH/provider commands |
| AgentlessNet Subnet Fulfillment UUID to Kubernetes UID/tenant mapping | Component integration | `agentless_net_subnet` target includes `_resolve_subnet_parent.yaml` against real Kind; no AAP, SSH, or switch operations |
| AgentlessNet ExternalIP UID-owned reservation, retry, and release | Unit | `tests/unit/test_agentless_network_state.py`; run `uv run pytest tests/unit`. Temporary SQLite is real. Kubernetes, AAP, SSH, switches, and Linux provider commands are omitted. |
| AgentlessNet NATGateway v1 VN-wide SNAT, transit exemption, ExternalIP consumer exclusivity, dependency deletion guards, scoped forwarding, bypass, route and conntrack cleanup | Unit | `tests/unit/test_agentless_network_state.py` and `tests/unit/test_agentless_net_nat_gateway.py`; run `uv run --group development pytest tests/unit`. SQLite is real; Linux commands are mocked. Subnet lifecycle is independent and no active source list or revision is saved. |
| AgentlessNet NATGateway tasks through the generic network task playbook | Component integration ([DEV] OSAC-6202) | `tests/integration/targets/agentless_net_nat_gateway/tasks/baseline.yml`; runs `playbook_osac_network_task.yml` in a child process with the stored operator input, no typed output, `osac_error` retryability, temporary schema-v1 SQLite, retry and failed-cleanup retention. Provider commands are replaced by test-only fixtures; AAP server, Kubernetes API, SSH, BGP peer convergence and external traffic are omitted. |
| AgentlessNet ExternalIP allocation and release playbooks | Component integration ([DEV] OSAC-6307) | `tests/integration/targets/agentless_net_external_ip/tasks/baseline.yml`; included by `make test`. Kind CR fixtures, Ansible playbooks, JSON job artifacts, and temporary SQLite are real; ExternalIP jobs receive unusable Kubernetes credentials to prove they do not access the API. AAP server, SSH, and provider networking are omitted. |
| AgentlessNet ExternalIPPool CR identity to saved provider state, idempotent registration, and identity-checked deletion | Component integration | `tests/integration/targets/agentless_net_external_ip_pool/tasks/baseline.yml`; real Kind CRs run through the top-level pool playbooks and state module against temporary SQLite using local delegation; no AAP, SSH, switches, or provider commands |
| AgentlessNet BMI access-port inventory and DHCP lease lookup | Unit plus component contract | `tests/unit/test_agentless_network_state.py`, `tests/unit/test_agentless_net_subnet.py`, and `tests/integration/targets/agentless_net_attachment/tasks/baseline.yml`; saved-state/lease parsing uses temporary SQLite/files, and a child Ansible process exercises real host/interface inventory validation including duplicate mappings and trunk overlap. AAP, SSH, switches, and provider commands are omitted; deployed job behavior is covered by the `e2e` VLAN lab. |
| `cudn_evpn` Kubernetes Manager input checks, CUDN/namespace/NetworkPolicy manifests, owner claim, SecurityGroup rule translation and conditional patches, and `osac_result`/`osac_error` shapes | Unit plus component integration | `tests/unit/test_cudn_evpn_role.py` and `tests/unit/test_cudn_evpn_playbook.py` (the latter runs tasks through `playbook_osac_network_task.yml` in a child Ansible process against an unreachable hub API). Hub behavior against a real Kubernetes API server is covered by `tests/integration/targets/cudn_evpn/tasks/baseline.yml` (from `tests/integration/` run `uv run ansible-playbook targets/cudn_evpn/tasks/baseline.yml -e '@common_vars.yml'`; also in `make test`). The ClusterUserDefinedNetwork and VTEP CRDs are fakes (accept-anything schema, no status subresource) and the play, not OVN-Kubernetes, sets CUDN readiness conditions. Not covered: OVN-Kubernetes behavior, real CRD validation, and the EVPN/DHCP data plane. |
| Netris Fabric Manager through the generic networking playbook: all 21 Fabric tasks, VirtualNetwork/Subnet results and reruns, V-Net gateway and DHCP, `l3Vni` mismatch, physical attachment and DHCP lease, ExternalIP/NAT names and addresses, FabricDomain, ordered absent-tolerant deletes (VNI freed last), 5xx versus 4xx errors without response bodies, tenant isolation, drift, and partial-create and lost-response recovery | Component integration | `tests/integration/targets/netris_fabric/tasks/baseline.yml` (from `tests/integration/` run `uv run ansible-playbook targets/netris_fabric/tasks/baseline.yml -e '@common_vars.yml'`; also in `make test`); child Ansible processes with an unusable kubeconfig against the stateful fake `tests/integration/fixtures/netris/fake_netris.py`; no Kubernetes objects, AAP, operator, switches or live Netris. Gap: real Netris stays the lab E2E (QE); live ExternalIP coverage is [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) |
| Netris VirtualNetwork, Subnet and SecurityGroup tasks through the generic networking playbook: contract inputs, `osac_result` (`backend_id`, `l3_vni`, evpn `network_segment.l2_vni`), retryable/final errors, get-or-create reruns, V-Net gateway/DHCP plan and update, and absent-object deletes | Unit plus component contract | `tests/unit/test_netris_vn_subnet.py` and `tests/unit/test_netris_playbook.py`; child Ansible processes against a stateful in-memory Netris double (`tests/unit/netris_fake.py`); no AAP, Kubernetes, switches, or live Netris. Deployed journeys: `netris_fabric`. |
| Netris NetworkAttachment tasks through the generic networking playbook: overlay no-op, physical port move from the provisioning V-Net to the Subnet V-Net (EVPN Subnets get DHCP over the physical range first), `query_dhcp_lease` by interface MAC, tenant/VPC ownership refusals and absent-object deletes | Unit plus component contract | `tests/unit/test_netris_attachment.py`; child Ansible processes against the in-memory Netris double (`tests/unit/netris_fake.py`, with servers, ports and IPAM hosts); no AAP, Kubernetes, switches, or live Netris. Deployed journeys: `netris_fabric`. |
| Netris FabricDomain (Server Cluster `osac-fd-<id>` in the VirtualNetwork VPC), `reserve_cluster_ingress_ip` (final error) and `release_cluster_ingress_ip` (no-op) tasks, and `create_virtual_network` (no NAT rule, leftover Netris DNS settings ignored), through the generic networking playbook: template choice, membership, foreign-VPC refusal, absent-object deletes | Unit plus component contract | `tests/unit/test_netris_fabric_domain.py` and `tests/unit/test_server_cluster_identity.py` (controller role); child Ansible processes against the stateful Netris double (`tests/unit/netris_fake.py`); no AAP, Kubernetes, switches, or live Netris. Deployed journeys: `netris_fabric`. |
| MetalLB ExternalIP detach annotation cleanup under concurrent stale reads | Component integration | `tests/integration/targets/metallb_l2_detach_annotation/tasks/baseline.yml`; two independent Ansible processes patch the same annotation against real Kind; no deployed AAP or MetalLB provider |
| MetalLB ExternalIP v1 allocation/release, UID-named Service identity, name-based dual-lookup adoption, ambiguity and conflict rejection | Component integration ([DEV] OSAC-6779) | `tests/integration/targets/metallb_l2_external_ip/tasks/baseline.yml`; included by `make test`. Real Kind resources and top-level playbooks, with a local fixture patching Service status; no deployed AAP, MetalLB controller, or real address allocation. Qualify real-provider contract coverage under [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843). |
| Operator-to-AAP task inputs and results (`l3_vni`, `l2_vni`, `network_segment`, `external_ip`, `vm_attachment`, `nat_gateway_context`) for the networking managers | Unit plus component contract against the shared wire goldens | `tests/unit/test_network_task_wire_contract.py` reads `../tests/contract/network-task`; run `uv run --all-groups python -m pytest tests/unit/test_network_task_wire_contract.py`. The operator half is `networking_wire_golden_test.go`; change both in one PR. Bound: create/apply/attach tasks of VirtualNetwork, Subnet, ExternalIPPool, ExternalIP, ExternalIPAttachment, NetworkAttachment (overlay) and NATGateway. Not bound (no ticket): delete_*/detach_* tasks, SecurityGroup tasks, query_dhcp_lease, FabricDomain, cluster ingress, physical interfaces |
| Netris ExternalIPPool, ExternalIP, ExternalIPAttachment and NATGateway tasks on the contract (API-ID object names, `external_ip.address` result, DNAT/SNAT in the destination VPC, final versus retryable errors, tenant-isolated get-or-create and absent-tolerant deletes), and the API-ID-owned `osac.templates.netris_external_ip` module | Unit | `tests/unit/test_netris_eip_nat.py`, `tests/unit/test_netris_external_ip.py` and `tests/unit/test_netris_external_ip_module.py`; run `uv run pytest tests/unit`. The tasks run through `playbook_osac_network_task.yml` in child Ansible processes against the in-memory Netris double `tests/unit/netris_fake.py`; no live Netris controller (OSAC-4843); the deployed journey is the `netris_fabric` target. |
| VMI stuck-finalizer recovery during ComputeInstance deletion (crashed/absent launcher, running-launcher refusal) | Component integration | `tests/integration/targets/compute_instance_delete_vmi_recovery/tasks/baseline.yml`; real Kind VMI and Pod resources with KubeVirt CRDs (operator scaled to 0); no AAP, real virt-launcher, or KubeVirt controller |
| Ansible roles, workflow tasks, hooks, leases, finalizers, or Kubernetes resources | Component integration | `make test` or the focused target command |
| Windows/Linux user-data routing and Windows base-spec regressions | Unit / isolated role transform | `ocp_virt_vm:test_windows_userdata_sysprep`, `ocp_virt_vm:test_windows_no_userdata`, and `ocp_virt_vm:test_linux_userdata_cloudinit` in `make test`; existing role Tests 15–17 run in separate Ansible processes without Kubernetes Secret operations |
| Tenant StorageClass tier resolution, DataVolume `storageClassName` wiring, and ambiguity guards | Unit + Component integration ([DEV] OSAC-6596) | `collections/ansible_collections/osac/service/roles/tenant_storage_class/tests/test.yml` and `compute_instance_create:csp_storage_tiers` in `make test` |
| Windows sysprep Secret copying and VM-spec wiring | Component integration ([DEV] OSAC-6182) | `compute_instance_create:sysprep_secret_copy` in `make test`; real Kind Secrets and `create_secrets.yaml`, including missing-tenant rejection; VM creation and readiness are overridden |
| ComputeInstance tier-to-StorageClass selection and disk specs | Unit / isolated role plus component integration ([DEV] OSAC-6682) | `uv run pytest tests/unit/test_tenant_storage_class.py` and `compute_instance_create:storage_tier_mapping` in `make test`; [focused setup and boundaries](tests/integration/targets/compute_instance_create/README.md). Real top-level playbook, Kind DataVolume specs, unchanged shared classes, rejected missing/ambiguous mappings, retries, and LVMS JIT class creation; no VM controllers or storage data plane. Fixed synthetic Volume IDs are fixture inputs only. Volume-ID coverage under [OSAC-6682](https://redhat.atlassian.net/browse/OSAC-6682) is deferred to the post-preview Volume API workflow. Operator CSP filtering remains [OSAC-6596](https://redhat.atlassian.net/browse/OSAC-6596). |
| Execution-environment definition or dependency inputs | Build/package validation plus applicable integration tests | `make execution-environment-build`, then `make test` |
| AAP, OpenStack, KubeVirt/RHACM, or provider provisioning | Contract or real-provider integration | Use the qualifying [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) suite |
| Storage-provider behavior | Component integration (focused) plus real-provider coverage when required | Tenant and compute-instance JIT target-routing run in `make test`; provider tests require `STORAGE_TESTS_ENABLED=true make test` (or the relevant storage target and provider suite) |

Storage integration requires `STORAGE_TESTS_ENABLED=true`; image builds are separate build/package validation.

AgentlessNet unit tests use real allocation/locked state and Linux helpers with
mocked `ip`/`iptables`, systemd/Supervisor, dnsmasq, and switch command output,
including allocation exclusions, retries, service selection, reconciliation,
and verification. Pool-state unit tests use SQLite directly to prove the
current v1 schema, identity preservation, and idempotent deletion. Inventory
contract tests run real Ansible parsing and
`add_host` without contacting AAP or the selected node. Service readiness does
not establish client DHCP leases or traffic delivery. Deployed VN retry/delete
and packet isolation coverage remains a QE gap under
[OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) /
[OSAC-4850](https://redhat.atlassian.net/browse/OSAC-4850); no qualifying VN
runner is tracked here.

The isolated HostedCluster role regression runs with
`uv run --group development ansible-playbook collections/ansible_collections/osac/service/roles/hosted_cluster/tests/test.yml`.
It executes NodePool definition transforms for distinct NodeSets sharing an
instance type and independent scaling. It does not create Kubernetes resources
or exercise AAP/provider endpoints; the applicable component-integration and
provider-boundary validations remain required.

## Generated and vendored files

- There is no source-code generator for roles. Do not hand-edit third-party content under `vendor/`.
- Reinstall vendored collections after dependency changes. Confirm Automation Hub credentials and required environment variables first, then run:
  `rm -rf vendor && ansible-galaxy collection install -r collections/requirements.yml`.
- Review the vendor diff and commit it with the requirements change when vendoring is required.

## Validation

From `osac-aap/`:

```bash
uv sync --all-groups
uv run pytest tests/unit      # Unit tests
make lint                     # uv run ansible-lint
make test                     # Kind setup, integration tests, teardown
helm lint charts/aap
```

`make test` creates and removes a Kind test environment; run it for
integration-affecting changes.

For a focused workflow, run the relevant playbook from `tests/integration/`,
for example `ansible-playbook targets/<workflow>/tasks/baseline.yml -e "@common_vars.yml" -v`.
Build the execution environment with `make execution-environment-build` when
its definition or dependency inputs change.
