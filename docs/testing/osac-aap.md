# osac-aap

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers, ownership and
evidence. Paths are relative to `osac-aap/` unless noted. Touched-area
requirements: [component guide](../../osac-aap/AGENTS.md#integration-testing).
Read the [integration harness README](../../osac-aap/tests/integration/README.md)
for target selection and setup. Run the component integration suite from the
repository root with `make -C osac-aap test`; the remaining targets are listed
in `osac-aap/Makefile`.

## Test tiers

| Tier | Suite (by directory/role) | Real | Faked or omitted |
|---|---|---|---|
| Unit | `tests/unit/` | Filter and isolated plugin behavior | Kubernetes, AAP, cloud and storage services are mocked or fixture-driven |
| Component integration | `make -C osac-aap test` (creates Kind, runs playbooks, tears it down) | Ansible roles/playbooks against real Kind APIs, a second isolated API for storage target routing, CRDs, leases, finalizers, test-runner pod | AAP, OpenStack, KubeVirt/RHACM and other providers are not generally real; the VMS storage target uses a mock server |
| Component integration (focused) | Targets under `tests/integration/targets/` | The role workflow and its documented fixtures | Only the dependencies that target declares; inspect its setup before claiming a real boundary |
| Contract | No dedicated suite; use the qualifying [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) task | Nothing | Kind API, mock VMS server and fixture-driven providers prove no AAP or provider contract |
| E2E | Cross-component OSAC E2E suites | Complete fulfillment and provisioning flows | Depends on the deployed AAP and provider environment |

## Focused harness boundaries

- `compute_instance_create` (see its
  [README](../../osac-aap/tests/integration/targets/compute_instance_create/README.md)):
  real Kind StorageClasses, Secrets, DataVolume task execution. VM
  readiness/creation may be overridden; synthetic Volume IDs prove no Volume
  API recovery, CDI/KubeVirt, PVC binding or guest I/O; minimal CRDs prove no
  provider admission.
- Storage target-routing scenarios: separate management/workload Kind APIs;
  placement and kubeconfig use are real. Tenant status resolution, VM boot and
  deployed AAP/provider lifecycle are outside the fixture.
- `network_task_playbook`, `netris_fabric`, `cudn_evpn` (also in `make test`):
  Netris is a stateful in-process fake; CUDN/VTEP CRDs are minimal schemas
  with fixture-supplied readiness — no OVN, real CRD validation, data plane or
  AAP server. The CUDN target refuses pre-existing CUDN/VTEP CRDs.
- `config_as_code_job_templates`, `config_as_code_pod_specs`: rendered AAP
  template/pod configuration only; no controller, Kubernetes objects or live
  provider.
- Netris transport unit tests: real HTTP transport and module subprocesses
  against a synthetic loopback provider; no live Netris login, so cookie
  refresh and live-provider compatibility remain unverified.
- Wire-contract unit tests: shared operator/AAP goldens against doubles and
  the CUDN planner; no deployed AAP, Kubernetes objects or provider execution.

The shared goldens do not bind delete/detach, SecurityGroup, DHCP-lease,
FabricDomain, cluster-ingress or physical-interface shapes; those gaps have no
owning ticket. Read both sides' suites before extending a task contract.
AgentlessNet Fabric and switch-VLAN targets ([OSAC-7156](https://redhat.atlassian.net/browse/OSAC-7156))
run generic-playbook child jobs with real SQLite against one stateful fake SSH
switch endpoint: physical NetworkAttachment apply, partial-retry, delete-wins
guards, port restoration, VLAN discovery and creation on declared trunks, and
shared ExternalIPPool lifecycle. DHCP leases, Linux and provider commands,
Kind, an AAP server, a live switch, BGP convergence and packet delivery are
not run.

## Network role coverage gate

`make test` measures `roles/netris`, `roles/cudn_evpn` and their four plugins.
`make role-coverage` runs the focused Unit/Kind targets on its own cluster;
`make role-coverage-report` prints the report. The callback and reporter in
`tests/coverage/` check task execution, both outcomes of each `when`, entered
`rescue` blocks, and asserted fail classification/message; plugins require
full line/branch coverage, and a combined boolean expression counts as one
condition. Justified exclusions go in `tests/coverage/exclusions.yaml`; stale
entries fail. `netris.controller` roles are reported, not gated. The gate
proves no live-provider behavior.

## Build/package validation

Run `make execution-environment-build` when the execution-environment
definition or dependencies change; it validates image assembly and packaging
only. Run the applicable integration tests separately for workflow behavior.

## Coverage gaps

- Stale SecurityGroup ACL pruning: [OSAC-4888](https://redhat.atlassian.net/browse/OSAC-4888);
  see the [Netris operation inventory](../../osac-aap/docs/netris-idempotency-inventory.md).
  Fixture coverage does not close this live-provider gap.
- Subnet lifecycle, real DHCP client leases, same-Subnet L2 traffic, service
  restart, retry recovery, `/30` addressing, peer-preserving cleanup,
  BMI-shaped access-port attachment and AAP DHCP lease discovery need the
  deployed lab tracked by [DEV OSAC-5530](https://redhat.atlassian.net/browse/OSAC-5530)
  and [Feature OSAC-1562](https://redhat.atlassian.net/browse/OSAC-1562). It
  proves no physical hardware, BMI provisioning/reboot/status feedback,
  ExternalIPAttachment DNAT, NAT or full-install CI; unit and contract tests
  cover lease lookup and inventory validation only. ExternalIPAttachment and
  broader fabric-manager coverage: [OSAC-3664](https://redhat.atlassian.net/browse/OSAC-3664),
  [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) /
  [OSAC-4850](https://redhat.atlassian.net/browse/OSAC-4850).
- Provider-dependent integration scenarios cannot run without AAP or extra
  infrastructure. Provisioning changes must state the real or contract
  boundary and link missing coverage to [OSAC-4850](https://redhat.atlassian.net/browse/OSAC-4850)
  or the relevant [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843)
  follow-up.
- Template publishing OAuth has unit and local mock-server coverage only; no
  deployed test proves AAP worker secret delivery or connectivity across
  Keycloak, AAP and Fulfillment. Track under
  [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843).
