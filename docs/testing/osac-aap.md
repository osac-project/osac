# osac-aap

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers, ownership and
evidence. Follow this guide for suite setup and limits; inspect test source for
individual cases. Commands and inline paths are relative to `osac-aap/`
unless explicitly marked as repository-root commands.

Touched-area requirements: [component guide](../../osac-aap/AGENTS.md#integration-testing).
Read the [integration harness README](../../osac-aap/tests/integration/README.md)
for target selection and setup; commands below run from `osac-aap/` unless noted.

## Test tiers and commands

| Tier | Location / command | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | `tests/unit/`; `uv run pytest tests/unit` | Filter and isolated plugin behavior | Kubernetes, AAP, cloud, and storage services are mocked or fixture-driven. |
| Component integration | `tests/integration/`; `make test` (creates Kind, runs playbooks, and tears it down) | Ansible roles/playbooks against real Kind APIs, including a second isolated API for storage target routing, plus CRDs, leases, finalizers, and test-runner pod | AAP, OpenStack, KubeVirt/RHACM, and other provider APIs are not generally real; the VMS storage target uses a mock server. |
| Component integration (focused) | A target under `tests/integration/targets/`; run the corresponding playbook from `tests/integration/` | The specific role workflow and its documented fixtures | Only the dependencies declared by that target; inspect its setup and overrides before claiming a real boundary. |
| Contract | No dedicated contract suite; use the qualifying [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) task for AAP/provider coverage | No AAP or provider endpoint is exercised as a contract | The Kind API, mock VMS server, and fixture-driven provider behavior do not prove an AAP or provider contract. |
| E2E | Cross-component OSAC E2E suites | Complete fulfillment and provisioning flows | Depends on the deployed AAP and provider environment. |

## Focused harness boundaries

| Harness / source | Boundary and prerequisites |
|---|---|
| `tests/integration/targets/compute_instance_create/` | Kind StorageClasses, Secrets and DataVolume task execution. Follow its [README](../../osac-aap/tests/integration/targets/compute_instance_create/README.md). VM readiness/creation may be overridden; synthetic Volume IDs do not prove Volume API recovery, CDI/KubeVirt, PVC binding or guest I/O. Minimal CRDs do not prove provider admission. |
| Storage target-routing scenarios in `tests/integration/` | Separate management/workload Kind APIs. Placement and kubeconfig use are real; Tenant status resolution, VM boot and deployed AAP/provider lifecycle are outside this fixture. |
| `network_task_playbook`, `netris_fabric`, `cudn_evpn` targets | Run the target's `tasks/baseline.yml` from `tests/integration/` with `uv run ansible-playbook targets/<target>/tasks/baseline.yml -e '@common_vars.yml'`; also included by `make test`. Netris is stateful in-process fake. CUDN/VTEP CRDs are minimal schemas and the fixture supplies readiness; no OVN, real CRD validation, data plane or AAP server. The CUDN target refuses pre-existing CUDN/VTEP CRDs. |
| `config_as_code_job_templates`, `config_as_code_pod_specs` targets | Rendered AAP template/pod configuration only; no controller, Kubernetes objects or live provider. |
| `tests/unit/test_netris_external_ip_transport.py` | Real HTTP transport/module subprocesses against a synthetic loopback provider; no live Netris login. Cookie refresh and live-provider compatibility remain unverified. |
| `tests/unit/test_network_task_wire_contract.py` | `uv run --all-groups python -m pytest tests/unit/test_network_task_wire_contract.py`; shared operator/AAP goldens against doubles and the CUDN planner. No deployed AAP, Kubernetes objects or provider execution. |

The shared goldens do not bind delete/detach, SecurityGroup, DHCP-lease,
FabricDomain, cluster-ingress or physical-interface shapes; those gaps have no
owning ticket. Read both sides' suites before extending a task contract.
AgentlessNet targets use fixture provider commands: service-readiness checks
alone do not prove DHCP leases, BGP convergence or packet delivery.

## Network role coverage gate

`make test` measures `roles/netris`, `roles/cudn_evpn` and their four plugins.
`make role-coverage` runs the focused Unit/Kind targets on its own cluster;
`make role-coverage-report` prints the report. The callback and reporter in
`tests/coverage/` check task execution, both outcomes of each `when`, entered
`rescue` blocks, and asserted fail classification/message. Plugins require full
line/branch coverage. A combined boolean expression is measured as one condition.
Use `tests/coverage/exclusions.yaml` for a justified exclusion; stale entries fail.
The `netris.controller` roles are reported, not gated. This gate does not prove
live-provider behavior.

## Build/package validation

Run `make execution-environment-build` when the execution-environment definition
or dependencies change. This validates image assembly and packaging; run the
applicable integration tests separately to validate workflow behavior.

## Coverage gaps

Subnet lifecycle, real DHCP client leases, same-Subnet L2 traffic, service
restart, retry recovery, `/30` addressing, peer-preserving cleanup, BMI-shaped
access-port attachment, and AAP DHCP lease discovery require the deployed lab
journey owned by the `e2e` branch and tracked by [DEV OSAC-5530](https://redhat.atlassian.net/browse/OSAC-5530)
and [Feature OSAC-1562](https://redhat.atlassian.net/browse/OSAC-1562). The lab
uses deployed Fulfillment/database/operator/AAP/SSH/Linux/dnsmasq/Cumulus VX
components and untagged test clients. It does not establish physical-hardware,
BMI provisioning/reboot/status feedback, ExternalIPAttachment DNAT, NAT, or
full-install CI coverage. Unit and contract tests cover lease lookup and
inventory validation only. ExternalIPAttachment and broader fabric-manager
coverage remain tracked under [Feature OSAC-3664](https://redhat.atlassian.net/browse/OSAC-3664)
and [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) /
[OSAC-4850](https://redhat.atlassian.net/browse/OSAC-4850).

The integration harness still has provider-dependent scenarios that cannot run
without AAP or additional infrastructure. Changes to provisioning behavior
must identify the real or contract boundary explicitly and link any missing
coverage to [OSAC-4850](https://redhat.atlassian.net/browse/OSAC-4850) or the relevant [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) follow-up.

Template publishing OAuth has unit and local mock-server coverage, but no
deployed test currently proves AAP worker secret delivery or connectivity across
the Keycloak, AAP, and Fulfillment boundaries. Track that deployed journey under
[OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843).
