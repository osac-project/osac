# OSAC AAP

Never edit this file except for minor corrections. Do not append new content.

Ansible roles and playbooks used by OSAC to provision infrastructure through
Ansible Automation Platform.

## Required context

Read the applicable references before changing behavior:

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
- Use fully qualified Ansible module names and give every task a `name:`.
- Role directories use underscores. A network template role must define at least one `fabric_manager` or `k8s_manager`, and manager names must match the corresponding role directory names.
- A template role's `meta/osac.yaml` must match its actual `template_type`; verify the real template before copying fields from another type.
- Network roles are not published as ComputeClass-family resources by `playbook_osac_config_as_code.yml`. Cluster, compute-instance, and bare-metal template metadata is published by config-as-code; do not assume that behavior applies to network roles.
- NetworkClass resources are created by the installer from configured manager names; role capabilities are informational. Only one NetworkClass may exist per deployment, so adding a backend replaces the configured NetworkClass rather than creating another.
- Preserve tenant and owner-reference annotations on resources created by playbooks.
- Remote workflows use `collections/ansible_collections/osac/service/roles/common/tasks/get_remote_cluster_kubeconfig.yaml`.
- Create `cudn_evpn` VirtualNetwork namespaces with `k8s.ovn.org/primary-user-defined-network: ""`
  at creation time; adding the label later is ignored by OVN-Kubernetes. See
  `collections/ansible_collections/osac/templates/roles/cudn_evpn/tasks/create_virtual_network.yaml`.
- Never expose credentials in tasks, logs, fixtures, examples, or generated artifacts.
- Keep supplied and response-derived Netris session cookies off redirect
  targets; preserve tenant-isolated API-ID ownership and retry-safe deletes.
- Manager credentials and networking CA mounts must stay out of unrelated
  cluster-fulfillment workers.

<a id="integration-testing"></a>

## Validation

See [suite boundaries and coverage gaps](../docs/INTEGRATION-TESTING.md#osac-aap).

Shared operator/AAP goldens bind physical NetworkAttachment apply/delete and
`query_dhcp_lease` (including the `dhcp_lease` result), plus ClusterOrder ingress
reservation. Update both operator Go suites, AAP's
`tests/unit/test_network_task_wire_contract.py` and
`../tests/contract/network-task` together in one PR. From `osac-aap/`, run
`uv run --all-groups python -m pytest tests/unit/test_network_task_wire_contract.py`.
DHCP validation checks the interface MAC and physical subnet range. Overlay
delete/detach, other deletes, SecurityGroup, FabricDomain and ingress release
remain unbound; see [wire boundaries](../docs/testing/osac-aap.md#boundaries-and-gaps).

Commands run from `osac-aap/` unless noted; see target READMEs under
`tests/integration/targets/` for prerequisites and focused selections.

| Touched area | Required tier and command |
|---|---|
| Ansible or Helm inputs | Static: `make lint`; `helm lint charts/aap` |
| Filters, token/auth wiring, transforms, transport and input validation | Unit: `uv run pytest tests/unit` |
| Roles, workflows, hooks, leases, finalizers or Kubernetes resources | Component integration: `make test` |
| Networking manager tasks and operator wire contracts | Unit plus component contract: `make test`; keep shared `../tests/contract/network-task` goldens in sync with operator consumers |
| Netris/cudn_evpn task and Python coverage | Measured gate: `make test` fails on gaps; `make role-coverage` runs the focused unit/Kind targets and report |
| Tenant storage mappings, disk IDs, VM/DataVolume annotations and Secret copying | Unit plus Kind component integration: `make test`; see `tests/integration/targets/compute_instance_create/README.md` |
| Storage providers, including NetApp | `STORAGE_TESTS_ENABLED=true make test`, plus qualifying real-provider coverage |
| Execution-environment definition or dependencies | `make execution-environment-build`, then applicable `make test` targets |
| AAP, OpenStack, KubeVirt/RHACM or network/provider behavior | Real-provider Contract/E2E; missing boundaries remain [coverage gaps](../docs/INTEGRATION-TESTING.md#osac-aap) |

New manager tasks, `when`, `rescue` and fail paths need tests or reasoned entries
in `tests/coverage/exclusions.yaml`; `netris.controller` roles are reported, not gated.

Kind resources and child Ansible jobs are real, but AAP/provider endpoints,
Netris, switches and VM controllers may be doubles. `cudn_evpn` uses fixture
CRDs and test-controlled conditions, not real OVN/EVPN/DHCP behavior.
AgentlessNet units use real SQLite/allocation state with mocked Linux and
switch commands; inventory contracts do not establish SSH, DHCP leases or
traffic isolation. Service readiness is not data-plane proof. Deployed
AgentlessNet VN retry/delete and packet isolation have no qualifying tracked
runner. HostedCluster transform checks are isolated, not provisioning tests.

## Generated and vendored files

- There is no source-code generator for roles. Do not hand-edit third-party content under `vendor/`.
- After dependency changes, check Automation Hub credentials and required
  environment variables, then reinstall collections as described in
  [the README](README.md#re-vendor-ansible-collections):
  `rm -rf vendor && ansible-galaxy collection install -r collections/requirements.yml`.
- Review the complete vendor diff and commit it with the requirements change
  when vendoring is required.

`make test` manages Kind setup, integration tests, teardown and coverage.
For focused playbooks, run from `tests/integration/`, for example
`ansible-playbook targets/<workflow>/tasks/baseline.yml -e "@common_vars.yml" -v`;
check the target README for prerequisites.
