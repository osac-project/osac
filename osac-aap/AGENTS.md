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
| Keycloak token lookup, cache refresh, and error handling | Unit | `uv run pytest tests/unit/plugins/lookup/test_fulfillment_token.py` |
| AAP publish-templates credential and Kubernetes token wiring | Unit | `uv run pytest tests/unit/test_publish_templates_auth_config.py` |
| Template publishing certificate validation | Local protocol integration | Run the `test_cert_validation` play in `collections/ansible_collections/osac/service/roles/publish_templates/tests/test.yml` with `uv run ansible-playbook`; the mock HTTPS server tests the client transport only |
| AgentlessNet VirtualNetwork/Subnet allocation, current SQLite schema-v1 validation, ExternalIPPool identity/deletion checks, parent locks, VLAN interfaces, switch discovery parsing, and derived DHCP-manager helpers | Unit | `tests/unit/test_agentless_network_state.py`, `tests/unit/test_agentless_net_network.py`, and `tests/unit/test_agentless_net_subnet.py`; run `uv run pytest tests/unit` |
| AgentlessNet VN and Cumulus Subnet inventory, host registration, trunk validation, and password rejection | Contract | `agentless_net_stub` baseline target runs `files/validate_vn_inventory.yml` in fresh Ansible processes; omitted switch platform defaults to Cumulus and explicit unsupported platforms fail; no SSH/provider commands |
| AgentlessNet Subnet Fulfillment UUID to Kubernetes UID/tenant mapping | Component integration | `agentless_net_subnet` target includes `_resolve_subnet_parent.yaml` against real Kind; no AAP, SSH, or switch operations |
| AgentlessNet ExternalIP UID-owned reservation, retry, and release | Unit | `tests/unit/test_agentless_network_state.py`; run `uv run pytest tests/unit`. Temporary SQLite is real. Kubernetes, AAP, SSH, switches, and Linux provider commands are omitted. |
| AgentlessNet ExternalIP allocation and release playbooks | Component integration ([DEV] OSAC-6307) | `tests/integration/targets/agentless_net_external_ip/tasks/baseline.yml`; included by `make test`. Kind CR fixtures, Ansible playbooks, JSON job artifacts, and temporary SQLite are real; ExternalIP jobs receive unusable Kubernetes credentials to prove they do not access the API. AAP server, SSH, and provider networking are omitted. |
| AgentlessNet ExternalIPPool CR identity to saved provider state, idempotent registration, and identity-checked deletion | Component integration | `tests/integration/targets/agentless_net_external_ip_pool/tasks/baseline.yml`; real Kind CRs run through the top-level pool playbooks and state module against temporary SQLite using local delegation; no AAP, SSH, switches, or provider commands |
| AgentlessNet BMI access-port inventory and DHCP lease lookup | Unit plus component contract | `tests/unit/test_agentless_network_state.py`, `tests/unit/test_agentless_net_subnet.py`, and `tests/integration/targets/agentless_net_attachment/tasks/baseline.yml`; saved-state/lease parsing uses temporary SQLite/files, and a child Ansible process exercises real host/interface inventory validation including duplicate mappings and trunk overlap. AAP, SSH, switches, and provider commands are omitted; deployed job behavior is covered by the `e2e` VLAN lab. |
| Netris Subnet deletion through the top-level delete playbook, its `osac.templates.<implementation-strategy>` dispatch, and the Netris roles' provider response handling | Component integration | `tests/integration/targets/netris_subnet_delete/tasks/baseline.yml`; real Kind namespace/ConfigMap and the top-level delete playbook in a child Ansible process against a fake Netris HTTP endpoint; no AAP controller, operator, switches, or real provider |
| Netris VirtualNetwork and ExternalIPPool deletion through top-level playbooks, VPC/IPAM-allocation response handling, and ordered cleanup | Component integration | `tests/integration/targets/netris_parent_delete/tasks/baseline.yml`; real Ansible playbooks and local HTTP test endpoint; no Kubernetes objects, AAP controller, operator, switches, or real Netris provider |
| MetalLB ExternalIP detach annotation cleanup under concurrent stale reads | Component integration | `tests/integration/targets/metallb_l2_detach_annotation/tasks/baseline.yml`; two independent Ansible processes patch the same annotation against real Kind; no deployed AAP or MetalLB provider |
| Netris ExternalIP create/delete through the top-level playbooks, the `osac.openshift.io/externalip-contract-version` dispatch, the v1 `osac.templates.netris_external_ip` module, and exact v1 job artifacts | Component integration ([DEV] OSAC-6376) | `tests/integration/targets/netris_external_ip/tasks/baseline.yml`; included by `make test`. Real Kind CR fixtures and child Ansible processes against a stateful fake Netris HTTP endpoint; v1 jobs receive unusable Kubernetes credentials to prove they do not access the API; the fixture's atomic duplicate name//32 rejection and lost-create behavior are explicit fixture assumptions. Real-provider Contract coverage remains under [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843); deployed AAP/provider journeys are QE coverage. |
| Ansible roles, workflow tasks, hooks, leases, finalizers, or Kubernetes resources | Component integration | `make test` or the focused target command |
| Windows/Linux user-data routing and Windows base-spec regressions | Unit / isolated role transform | `ocp_virt_vm:test_windows_userdata_sysprep`, `ocp_virt_vm:test_windows_no_userdata`, and `ocp_virt_vm:test_linux_userdata_cloudinit` in `make test`; existing role Tests 15–17 run in separate Ansible processes without Kubernetes Secret operations |
| Windows sysprep Secret copying and VM-spec wiring | Component integration ([DEV] OSAC-6182) | `compute_instance_create:sysprep_secret_copy` in `make test`; real Kind Secrets and `create_secrets.yaml`, including missing-tenant rejection; VM creation and readiness are overridden |
| Execution-environment definition or dependency inputs | Build/package validation plus applicable integration tests | `make execution-environment-build`, then `make test` |
| AAP, OpenStack, KubeVirt/RHACM, or provider provisioning | Contract or real-provider integration | Use the qualifying [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) suite |
| Storage-provider behavior | Component integration (focused) plus real-provider coverage when required | Tenant target-routing runs in `make test`; provider tests require `STORAGE_TESTS_ENABLED=true make test` (or the relevant storage target and provider suite) |

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
