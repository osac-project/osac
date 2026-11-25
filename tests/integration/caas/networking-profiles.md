# Networking profile matrix

[`networking-profiles.json`](networking-profiles.json) is the machine-readable
profile list used by the local runner. Each immutable manager configuration
uses a fresh Kind cluster. A candidate or runnable-after-dependency row is not
evidence that its integrated profile has passed.

Run an individual runnable profile with
`make -C tests/integration/caas profile PROFILE=<profile-name>`. The runner
creates a unique cluster, refuses to reuse an existing one, and removes only
the cluster it created. Network-enabled profiles start the OSAC-6194 Netris
fixture around AWX bootstrap and preparation.

The runner's current suite verifies profile registration, tenant defaults,
production AWX jobs and fixture state. Workload lifecycle scenarios listed
below are consumer coverage tracked separately by OSAC-7924 and OSAC-7926;
they are not claimed as passed by profile preparation.

Before tenant setup, each enabled profile creates or reuses a profile-owned
IPv4 pool through the private API. The real Fabric Manager job must make that
pool ready so default ExternalIP and NATGateway resources can be provisioned.
The disabled profile creates no pool.

## Service and manager combinations

| Profile | Services | Fabric Manager | Kubernetes Manager | Expected result | Required scenario | Current profile evidence |
|---|---|---|---|---|---|---|
| `networking-disabled` | CaaS, BMaaS, VMaaS | Configured Netris registration; provisioning off | Configured CUDN EVPN registration; provisioning off | Pass; no manager jobs | Tenant defaults become ready without provider jobs | Existing regression profile; runner can use current fixture-free setup. |
| `caas-bmaas-netris-no-vmaas` | CaaS, BMaaS | `netris` | None | Pass | Production Fabric tasks run; CaaS worker path remains available | Profile values added; integrated run awaits OSAC-6194. |
| `bmaas-netris` | BMaaS | `netris` | None | Pass | Production Fabric tasks and physical/DHCP path | Profile values added; integrated run awaits OSAC-6194. |
| `vmaas-netris-cudn-evpn` | VMaaS | `netris` | `cudn_evpn` | Pass | Production Fabric and Kubernetes Manager jobs; ComputeInstance controller enabled | Profile values and OVN API fixture exist; integrated run awaits OSAC-6194. |
| `all-services-netris-cudn-evpn` | CaaS, BMaaS, VMaaS | `netris` | `cudn_evpn` | Pass | All service prerequisites enabled; real AWX dispatch for both managers | Profile values and OVN API fixture exist; integrated run awaits OSAC-6194. |
| `bmaas-agentless-net` | BMaaS | `agentless_net` | None | Pass | Production AgentlessNet role records physical switch changes and cleanup | Blocked by OSAC-6195 fixture integration. |
| `caas-agentless-net` | CaaS, BMaaS | `agentless_net` | None | Needs contract evidence | Verify worker attachment requirements against AgentlessNet behavior | Unqualified; do not count as supported or rejected. |
| `vmaas-agentless-cudn-evpn` | VMaaS | `agentless_net` | `cudn_evpn` | Reject | Manager resolution rejects the missing EVPN network type | Contract rejection; no profile run required. |
| `vmaas-no-kubernetes-manager` | VMaaS | `netris` | None | Reject | Installer rejects VMaaS without a Kubernetes Manager | Contract rejection; no profile run required. |

The base Kind installation enables CaaS, BMaaS and VMaaS. The VMaaS networking
profiles explicitly enable `operator.controllers.computeInstance`; the older
shared test values disabled this controller, which would have hidden the VMaaS
path. CaaS requires BMaaS because its workers are bare-metal instances.

## Capability and configuration rules

| Configuration | Expected result | Evidence |
|---|---|---|
| `cudn_evpn` paired with Netris | Valid | Both registrations advertise EVPN. |
| `cudn_evpn` paired with AgentlessNet | Reject | AgentlessNet advertises no network type. |
| VMaaS enabled without a Kubernetes Manager | Reject | Installer requires one for VM networking. |
| Default ICMP SecurityGroup rules with Netris or AgentlessNet | Valid | Both Fabric registrations declare `security_group_icmp`. |
| Default ICMP SecurityGroup rules with `cudn_evpn` | Reject | The Kubernetes Manager does not declare ICMP support; use `PROTOCOL_ALL`. |
| East-west Ethernet with Netris | Valid candidate | Netris declares `eastWestEthernet`; integrated proof is not yet recorded. |
| East-west Ethernet with AgentlessNet | Reject | AgentlessNet does not declare the capability and does not implement FabricDomain creation. |
| No Kubernetes Manager with CaaS and/or BMaaS, VMaaS disabled | Valid | Overlay address share is zero when no Kubernetes Manager is selected. |
| Change manager or address plan after installation | Reject | NetworkClass configuration is immutable; run a fresh cluster. |

## Registered and shipped implementations

The stock installer registers exactly the three managers below from
`osac-installer/charts/osac/templates/networking/`. The inventory also includes
the shipped legacy roles because their files alone do not make them installed
or supported profiles.

| Implementation | Stock registration | Capabilities / network types | Task entrypoints |
|---|---|---|---|
| Netris Fabric Manager (`netris`) | Yes: `netris-registration.yaml` | `evpn`; `eastWestEthernet`; `security_group_icmp` | All 19 current Fabric tasks: `create_virtual_network`, `delete_virtual_network`, `create_subnet`, `delete_subnet`, `create_security_group`, `delete_security_group`, `apply_network_attachment`, `delete_network_attachment`, `query_dhcp_lease`, `create_external_ip_pool`, `delete_external_ip_pool`, `create_external_ip`, `delete_external_ip`, `attach_external_ip`, `detach_external_ip`, `create_nat_gateway`, `delete_nat_gateway`, `create_fabric_domain`, `delete_fabric_domain`. |
| AgentlessNet Fabric Manager (`agentless_net`) | Yes: `agentless-net-registration.yaml` | No declared network type; `security_group_icmp`; no `eastWestEthernet` | All 19 current Fabric task files are shipped. FabricDomain creation returns unsupported; physical SecurityGroup create is an allowed no-op. |
| CUDN EVPN Kubernetes Manager (`cudn_evpn`) | Yes: `cudn-evpn-registration.yaml` | `evpn`; no declared capabilities | 8 Kubernetes tasks: create/delete VirtualNetwork, Subnet and SecurityGroup; apply/delete NetworkAttachment. |
| Legacy `cudn_net` role | No | No stock registration; role metadata identifies a Fabric Manager | 6 old create/delete tasks for VirtualNetwork, Subnet and SecurityGroup. Not a supported stock profile. |
| Legacy `k8s_only` role | No | No stock registration; runtime fallback is restricted to this explicit legacy manager name | 12 old tasks for VirtualNetwork, Subnet, SecurityGroup, ExternalIPPool and ExternalIP operations. No NATGateway or NetworkAttachment tasks. Not a stock profile or replacement for `cudn_evpn`. |

Netris and AgentlessNet also ship the legacy `reserve_cluster_ingress_ip` and
`release_cluster_ingress_ip` files. They are not dispatched by current CaaS
networking: guest ingress allocation and release belong to Fulfillment.

Production manager jobs must use
`osac-aap/playbook_osac_network_task.yml`, the `networking-operations` inventory
and `networking-operations-ig`. Worker settings come from the
`network-fulfillment-ig` ConfigMap and Secret. The profile checks project
synchronization, manager registration, selected group, job attempt, worker pod
and fixture state; a Ready resource or successful job alone is insufficient.

## Fixture and execution boundary

- The local OVN fixture installs minimal CUDN and test VTEP API schemas. Its
  simulator only supplies external status conditions and deterministic CUDN
  hold/retry behavior; it does not simulate traffic or replace the production
  `cudn_evpn` role.
- OSAC-6194 owns the AWX-reachable Netris fake and per-run controls. PR #2612 is
  open with changes requested, so Netris profiles remain blocked until that
  fixture is merged and wired into the runner.
- OSAC-6195 owns the AgentlessNet AWX state and switch fixture. PR #2611 is open
  with changes requested, so the AgentlessNet BMaaS row remains blocked.
- Physical dataplane and vendor certification are outside this test boundary.

## Design references

- [Networking decisions](../../../docs/agent-context/networking-decisions.md)
- [Network Manager integration contract](../../../enhancement-proposals/enhancements/OSAC-1433-network-manager-integration-contract-networking/design.md#42-registration-and-capabilities)
- [Unified networking deployment profiles and integration tests](../../../enhancement-proposals/enhancements/OSAC-1433-unified-networking/design.md#deployment-profiles)
- [VMaaS networking](../../../enhancement-proposals/enhancements/OSAC-1435-vmaas-networking/design.md#integration-tests)
- [CaaS networking](../../../enhancement-proposals/enhancements/OSAC-1436-caas-networking/design.md#integration-tests)
- [BMaaS networking](../../../enhancement-proposals/enhancements/OSAC-1437-bmaas-networking/design.md#integration-tests)
