# OSAC E2E tests

Never edit this file except for minor corrections. Do not append new content.

Cross-component pytest suites for VMaaS, CaaS, BMaaS, storage, and
resource-reference workflows.

## Invariants

- Cross-component pytest suites live under `tests/e2e/`, not in the external `osac-test-infra` checkout; that repository owns infrastructure backends, not suites.
- Put service-owned tests in their service-tier directory. Cross-service
  networking tests live in `tests/e2e/networking/{vmaas,bmaas,stretched}/`.
- Test-infra selects networking suites by directory through `TEST_SUITE`;
  do not add backend-specific pytest markers. Standard service runs select
  different directories and therefore skip these suites.
- Avoid fixed sleeps; wait for observable resource conditions with bounded timeouts.
- Tests clean up resources they create and never assume the caller's shared-cluster namespace or context.
- A new long wait or traffic check reports a failure kind from
  `tests/e2e/core/failures.py`: `Kind`, `E2EFailure`, or `poll_or_fail(kind=...)`
  in the VMaaS stack.
- Suites with guests register a snapshot source and remove it in teardown:
  `stack.register_guest_snapshot` for VMaaS or
  `bmaas/guest_ssh.register_guest_snapshot` for BMaaS.
- Failure reporting never changes a test's result and never prints a token: use `run()` (its `CommandFailed` and `CommandTimedOut` carry a sanitized command) and keep polled containers out of messages.
- Telemetry is optional and never changes outcomes. Preserve worker attribution
  (`osac.ci.xdist_worker`) and fixture-reuse spans; overlapping fixture spans
  are not additive runner wall time.

## Touched-area map

All commands below run from the repository root. Read
[suite boundaries and prerequisites](../../docs/testing/e2e.md#suite-boundaries)
and [integration boundaries](../../docs/INTEGRATION-TESTING.md#testse2e)
before selecting deployed coverage.
| Area | Required coverage | Location and command | Boundary and prerequisites |
|---|---|---|---|
| Disabled ExternalIP references | [DEV] Exact gRPC error-oracle units; [QE] pool ID/name rejection, no stored ExternalIP and unchanged observed allocations/pool counters | Units: `uv run pytest -n 0 tests/unit/test_reference_networking_assertions.py`; deployed BMaaS includes `references/test_networking_references.py` with `reference_networking` | Real disabled Fulfillment/database, tenant auth and operator; private API permissions to create/read/delete the fixture-owned shared pool and observe/clean up its ExternalIPs. The fixture waits for pool readiness and deletes it afterward; no existing pool prerequisite, synthetic allocation or concurrent allocators during snapshots. Run/collect the full BMaaS selection in [integration testing](../../docs/testing/e2e.md#suite-boundaries). API registration and Helm defaults do not establish allocation/connectivity. |
| ExternalIP allocation, same-record retry, release, and BMaaS/Netris evidence ([QE] [OSAC-6001](https://redhat.atlassian.net/browse/OSAC-6001)) | Deployed E2E checks the canonical eligible `ExternalIP.status.address`, matching Fulfillment API status, stored `status.taskInput.resource.id`, pool capacity change, same-resource retry with stable address/capacity, tenant metadata, and safe owned-resource cleanup. It triggers the second attempt by changing the completed `status.jobs` entry to `Cancelled` with JSON Patch preconditions; the operator then reuses stored task input. Kubernetes UID/generation only confirm that the retry used the same CR snapshot; the Fabric ownership key is the Fulfillment API ID. JUnit records source-declared AAP collection versions, not deployed runtime versions. | `tests/e2e/networking/bmaas/test_external_ip_ownership.py`; from the repository root run `uv run pytest -n 0 tests/e2e/networking/bmaas/test_external_ip_ownership.py` in the Netris BMaaS profile. | Requires [OSAC-5977](https://redhat.atlassian.net/browse/OSAC-5977) deployed, Fulfillment/operator/AAP/Netris, private API and hub access, and an available IPv4 `/30`. The E2E uses no mocks. It verifies OSAC pool accounting and status; it cannot read Netris IPAM reservations, so provider-side reservation visibility remains under [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843). Exact AAP task-envelope/result validation and provider release/idempotency cases belong to OSAC-5977 contract/component coverage. |
| Enabled allocated networking references | [DEV] Request/oracle units; [QE] one NATGateway and one ExternalIPAttachment, every reference by ID (name references are a gap: fulfillment-service integration tier, see `docs/agent-context/networking-decisions.md`) | Units: `uv run pytest -n 0 tests/unit/test_networking_reference_selectors.py`; deployed: `uv run pytest -n 0 -x tests/e2e/networking/vmaas/test_vmaas_networking.py` | Real Netris plus cudn_evpn, enabled provisioning, compatible AAP, routed pool/capacity, KubeVirt VMs and guest SSH helper. Each needs API/CR ALLOCATED, canonical IDs, Ready and egress or host-key-verified SSH, and stays for the ordered lifecycle tests. Never isolate phase C. Local units/collection are not deployed evidence; lab authorization remains a QE/CI operator dependency. |
| E2E failure reporting | [DEV] Unit for sanitizing, `CommandFailed`/`StuckState`, failure classification, and the pytest plugin driven through real `python -m pytest` subprocesses (immediate `E2E FAILURE` log line, fsynced `results-<worker>.jsonl`, snapshot cap, source errors and budget, `-x`, xdist workers, kill) | From the root: `uv run pytest -n 0 tests/unit/test_e2e_failure_report.py tests/unit/test_e2e_failure_plugin.py tests/unit/test_e2e_failure_snapshot.py` | Writes `results-<worker>.jsonl` and `failures/<worker>/<nn>-<test>/` under `OSAC_E2E_OUTPUT_DIR` (default: the parent of the `log_file` ini value, `/tmp/test-output`). A snapshot holds API and CR lists, events, and logs of the fulfillment API server and controller, the OSAC operator and the BMF operator since the failed test's module began (an ordered suite builds the failing state in earlier tests), guest network state with the capture and boot time for guest kinds, and `fabric.json` when the backend provides `OSAC_E2E_FABRIC_SNAPSHOT_CMD` (a command that prints the fabric state; the backend owns it and its credentials). The Netris VMaaS suite adds each VM namespace's VMs, VMIs, events and virt-launcher logs from the VM cluster; the Netris BMaaS suite adds each BareMetalHost's provisioning, power and operation history (`bmh-status.json`). Does not prove the snapshot content against a deployed cluster, a real fabric, or live console output under xdist workers. |
| E2E telemetry: worker attribution and fixture reuse | [DEV] Unit driving real `python -m pytest` xdist subprocesses with a JSONL recording exporter: `osac.ci.xdist_worker` on the test, fixture, phase and wait spans of every worker, one `fixture_use` span per test per session/module-scoped fixture with no duplicates, skipped setups uncounted, and identical outcomes with the telemetry wiring present and absent | From the root: `uv run pytest -n 0 tests/unit/test_e2e_telemetry.py` | The span assertions run only where `opentelemetry-sdk` and `pytest-opentelemetry` are installed (the E2E test image); elsewhere they skip and the inert and outcome-preservation tests still run. The upstream plugin loses some parent spans; the worker attribute, not the parent link, is the dashboard's grouping key. Watch the trace size: a monolithic single-trace run of the whole `tests/e2e/` tree could add roughly 2-2.5 MB of fixture-use spans and approach Tempo's default 5 MB per-trace limit; per-suite CI traces stay far below it. |
| Managed service-account lifecycle and authorization ([QE] [OSAC-6557](https://redhat.atlassian.net/browse/OSAC-6557)) | Regression E2E for account CRUD and cleanup, RoleBinding access/revocation, tenant isolation, deletion token invalidation, and credential rotation | From the repository root: `uv run pytest -n 0 tests/e2e/regression/test_tenant_service_accounts.py` | Requires the deployed `full-ci` profile, Keycloak admin credentials, Fulfillment, and BMaaS/CaaS/VMaaS public APIs. Project-filtered service List calls use empty results and create no provider-backed workloads. |
| VMaaS deletion-test finalizer retries | [DEV] Unit for bounded conflict retries, fresh versions, preserved concurrent finalizers, idempotence, and non-conflict failures | From the root: `uv run pytest -n 0 tests/unit/test_vmaas_deletion_finalizer.py` | Test-helper logic only; gRPC responses and time are mocked. Does not establish deployed deletion. |
| VMaaS Netris parallel setup and staged teardown | [DEV] Unit with fakes that record call order: `run_parallel` re-raises `pytest.fail`, waits for every worker and raises the first failure in submit order; Subnet and SecurityGroup start only after the VirtualNetwork waits and overlap; networks map correctly when they finish out of order; VM creates are serial within a tenant; the SSH helper applies once per namespace from eight threads; teardown stages never overlap and a finalizer rerun after a partial teardown is safe; the pool fixture check | From the root: `uv run pytest -n 0 tests/unit/test_vmaas_parallel_setup.py tests/unit/test_vmaas_networking_helpers.py tests/unit/test_vmaas_ssh_helper.py` | Test-helper logic only; gRPC, Kubernetes, SSH and time are faked. Does not prove the deployed suite or its run time. |
| Netris VMaaS and BMaaS convergence waits | [DEV] Unit with faked SSH, gRPC, kubectl and time: `retry_on_error` retries a command timeout; a probe that never ran (`ProbeTransportError`) is retried by positive waits and never read as blocked; BMaaS waits for SSH on every BMI after RUNNING (the network handoff power-cycles the host, so RUNNING comes before sshd) and fails fast on a rejected key; the VMaaS NAT precondition waits for two blocked attempts in a row, so a previous row's deleted NATGateway that the softgates still apply is outlasted; ExternalIP ingress counts as stopped after three failed SSH attempts in a row, also between `test_10` rows; deletion waits read gone only from an exact NotFound | From the root: `uv run pytest -n 0 tests/unit/test_netris_e2e_waits.py tests/unit/test_networking_reference_selectors.py` | Helper logic only; the softgate and boot timings come from the deployed periodic runs and are not proven here. Creating a NATGateway or ExternalIPAttachment waits for the ExternalIP to be ALLOCATED in the API, and teardown waits for API records as well as CRs, because the server refuses the next create or delete while they exist. |
| VMaaS deletion feedback with a removed DiskImage | [QE] Sanity E2E for status feedback with a stale spec reference, ComputeInstance removal, and explicit dependent SecurityGroup, Subnet, and VirtualNetwork deletion | From the repository root: `uv run pytest -n 0 tests/e2e/vmaas/sanity/test_compute_instance_disk_image.py -k deletion_feedback_with_removed_disk_image` | Deployed Fulfillment/database, operator, AAP, and VM provider; a private finalizer holds deletion while the image is removed. Requires the VMaaS template, instance type, storage tier, and networking provider. |
| AgentlessNet Subnet VLAN, real DHCP clients, retry, and cleanup | Deployed lab E2E for saved allocations, leases, gateway/L2 traffic, service restart, `/30`, and peer-preserving lifecycle | Root orchestrator `vlan-e2e.sh`; reusable client and VLAN assertions live in that script | Requires the running SNO and Containerlab Cumulus VX lab, deployed Fulfillment/operator/AAP, source-pinned EE/project, and container access. Client macvlan children use host links `leaf1-swp2`, `leaf2-swp2`, and `leaf2-swp3`; the runner snapshots and restores the switch ports. Does not establish physical-hardware, BMaaS lease-status, NAT, or full-install CI coverage. |
| VMaaS ComputeInstance InstanceType resize, including CatalogItem-created instances | Sanity E2E for CLI/API outcomes, applied ComputeInstance configuration, and VMI resources | `tests/e2e/vmaas/sanity/test_compute_instance_instance_type.py`; from the repository root, run `uv run pytest tests/e2e/vmaas/sanity/test_compute_instance_instance_type.py` | Uses the deployed VMaaS API and Kubernetes endpoints, VM kubeconfig, image, storage tier, subnet, and VM template. Restart-required behavior assumes the single-node VMaaS profile. |
| DiskImage-backed VM provisioning and reference release | [QE] Sanity E2E for running/image projection and image deletion after workload teardown | From the repository root: `uv run pytest -n 0 tests/e2e/vmaas/sanity/test_compute_instance_disk_image.py` | Real Fulfillment, operator, AAP, and VM provider; requires the configured VMaaS template, network, instance type, and storage tier. API-only DiskImage CRUD/lifecycle, template defaults/protection, obsolete-image rejection, CaaS missing-image rejection, and BMI catalog cross-tenant rejection belong in Fulfillment `it/`, not E2E. |
| VMaaS ComputeInstance additional-disk CatalogItem defaults | Sanity E2E for omitted versus explicitly empty `additional_disks`, including the resulting ComputeInstance CR | `tests/e2e/vmaas/sanity/test_compute_instance_storage_tier.py`; from the repository root, run `uv run pytest tests/e2e/vmaas/sanity/test_compute_instance_storage_tier.py -k empty_additional_disks_disables_catalog_item_default` | Uses the deployed VMaaS API and Kubernetes endpoints, storage tiers, subnet, disk image, instance type, and VM template. |
| VMaaS deployed disk Volume IDs ([QE] OSAC-6614) | Existing multi-disk sanity E2E checks the exact public `status.volumes` key/ID map against the managed CR, actual DataVolume annotations and private Volume records; it checks original record absence after normal instance deletion | From the repository root: `uv run pytest -n 0 tests/e2e/vmaas/sanity/test_compute_instance_storage_tier.py -k multiple_disks_different_tiers` | Requires deployed Fulfillment/database, operator, AAP, CDI/KubeVirt, a provisionable VMaaS storage tier, public/private API auth and tenant networking. The detailed Volume field matrix is in Fulfillment `it/`. This test does not hold AAP, trace each Delete request/response, or cover pre-feature workloads; those OSAC-6614 criteria remain gaps. |
| Compute-reference cleanup ordering | [DEV] Unit for cleanup policy; [QE] E2E for deployed deletion | From the root: `uv run pytest -n 0 tests/unit/test_e2e_helpers.py`; deployed: `uv run pytest -n 0 tests/e2e/references/test_compute_references.py` | Unit checks use mocked API responses and time. The deployed suite requires VMaaS Fulfillment, operator, AAP, KubeVirt, storage, and tenant networking; it waits for the exact ComputeInstance CR and API record to disappear before releasing references, without removing finalizers. |
| Netris VMaaS connectivity (OSAC-4291) | Ordered regression E2E (`test_00`..`test_14`) selected by `/test vmaas-netris` (`DEPLOY_SUITE=vmaas`), run with `-x` so the job stops at the first failed test: same-Subnet ARP/ICMP, cross-VirtualNetwork and cross-tenant isolation, cluster DNS from OVN-K DHCP, SecurityGroup TCP ingress/egress allow and deny with rule updates, NAT egress, manual and automatic ExternalIP ingress for tenant1 and manual ExternalIP ingress for tenant2, ExternalIPAttachment delete stopping ingress, and one teardown test (`test_14_teardown_leaves_nothing`) that deletes everything in dependency order and checks that nothing is left, including that none of the suite's ExternalIPs is left and the pool `allocated` count equals the live ExternalIPs on the pool (tenant default `default-nat` addresses also draw from the pool, so an owned pool they hold is not deleted; the test names them and fails on any other holder). The routed ExternalIPPool (host route, READY, at least 3 free addresses, the peak of the serial tests) is checked by the `netris_external_path` fixture, so a lab without it errors phase C instead of failing a test. **Removed E2E requirements (not equal coverage):** node service isolation from a tenant VM (no E2E covers it; [networking decisions](../../docs/agent-context/networking-decisions.md) holds the measured behavior; add a check when needed); ICMP SecurityGroup rule rejection that follows the NetworkClass `securityGroupIcmp` capability (the lower tiers cover the rejection but not the capable branch's READY through AAP, which cudn_evpn cannot reach) and second-tenant automatic ExternalIP ingress and garbage collection (`test_12` and `test_13` prove the automatic flow and tenant2 ingress separately). `test_12b`..`test_12d` bill on the same resources; see the next row. | `uv run pytest -n 0 -x tests/e2e/networking/vmaas/test_vmaas_networking.py` | Requires `vmaas-ci` with `values/vmaas-ci/netris.yaml` (which sets `operator.aap.statusPollInterval` to `5s`), or `full-ci` with `values/full-ci/netris.yaml`, the OCP CUDN EVPN VTEP/underlay, VM kubeconfig, and `kubectl`, `ssh` and `ssh-keygen` on the pytest host. Guest commands run over `kubectl exec` in a test-owned helper Pod in each VM namespace (it joins the VM's primary network and runs `ssh`); the Pod image, by default the digest-pinned OSAC execution-environment image, must carry an ssh client and be pullable by the cluster (`OSAC_E2E_SSH_HELPER_IMAGE`), and each VM SecurityGroup must keep ingress tcp/22 open. Setup creates independent resources at the same time (`stack.run_parallel`): the three networks, then each network's Subnet and SecurityGroup, then the VMs (one tenant's creates one after the other, the tenants at once), then the five VM waits; a child never starts before its own parent is READY in both the CR and the API (VirtualNetwork before Subnet and SecurityGroup, those before VMs, an ExternalIP ALLOCATED before a NATGateway or attachment), and teardown runs the reverse stages. The NAT and ExternalIP tests always run and fail with a diagnosis when the external path is not wired; they need the routed pool (`OSAC_EXTERNAL_IP_POOL_CIDR`, default `198.51.100.24/29`) and pytest on a host that routes it. Tests are numbered and serial, share module-scoped fixtures, and cannot be sliced with `-k`. A stopped run never reaches `test_14`, so the fixture finalizers delete what is left. To debug a live lab, set `OSAC_E2E_KEEP_RESOURCES=true`: `test_14` skips, the finalizers delete nothing and log what stays, and you delete it by hand before the next run; no job sets it. Inter-subnet routing is not covered: each VirtualNetwork has one Subnet. |
| ExternalIP and NATGateway billing | `test_12b`..`test_12d` of `test_vmaas_networking.py`, with the journeys in `billing.py`: an ExternalIP billed through deletion, attach/detach/reattach from VM a1 to a2 with disjoint attached and unattached meters, and NATGateway billing on network b. They reuse the module's VMs, network and pool, and run before `test_13` so the pool peak stays 3. Under `-x` an earlier failure hides them and a failure in them hides `test_13` and `test_14`. The shared assertions have unit cases on synthetic heartbeats (`uv run pytest -n 0 tests/unit/test_metering_collector.py`), which do not prove deployed billing. **Unresolved gap:** no E2E attaches an ExternalIP manually to a VM that has just become Ready, where the tenant address can drop for about 10 seconds; no ticket owns this yet | `METERING_ADAPTER_URL=<echo adapter URL> uv run pytest -n 0 -x tests/e2e/networking/vmaas/test_vmaas_networking.py` | Needs `global.networking.provisioningEnabled: true`: with it disabled the Fulfillment Service rejects ExternalIP and ExternalIPAttachment creation with `FailedPrecondition`, so standard service profiles cannot run these. Requires `vmaas-ci` with `values/vmaas-ci/netris.yaml`, VM kubeconfig, the routed pool (`OSAC_EXTERNAL_IP_POOL_CIDR`), and the metering echo adapter; they skip without an adapter and carry no `metering` marker, which would fail the whole session at collection. Lower-tier capacity, attachment validation and reference cases complement the enabled reference/traffic rows above; see [integration testing](../../docs/testing/e2e.md#suite-boundaries) for their boundaries. ExternalIPPool capacity initialization, which needs no provider networking, stays in `tests/e2e/vmaas/sanity/external_ip/`. |
| Netris BMaaS connectivity | Numbered regression E2E for same-subnet and cross-subnet BMI networking plus cross-tenant isolation; selected by `/test bmaas-netris` (`DEPLOY_SUITE=bmaas`) | `uv run pytest -n 0 tests/e2e/networking/bmaas` | Requires the Netris BMaaS profile, available BMH SSH endpoints, the configured `OSAC_BMI_NETWORK_INTERFACE`, and fabric networking. The PR job excludes the slower cross-tenant class; run it separately with `-k TestCrossTenantBmaasNetworking`. |
| Stretched BM/ComputeInstance connectivity | Backend-agnostic regression E2E for address uniqueness and bidirectional ARP/ICMP on one OSAC Subnet; selected by `DEPLOY_SUITE=full TEST_SUITE=stretched` and the Netris infrastructure job `/test stretched-netris` | `uv run pytest -n 0 tests/e2e/networking/stretched` | The Netris runner uses the `full-ci` profile, BMaaS SSH access, `OSAC_BMI_NETWORK_INTERFACE`, a VM template, `kubectl` on the pytest host, and the OCP CUDN EVPN VTEP/underlay. Its guest commands run over `kubectl exec` in the SSH helper Pod described for the VMaaS row (`OSAC_E2E_SSH_HELPER_IMAGE`). The test itself checks OSAC networking behavior and creates/cleans its VirtualNetwork, Subnet, SecurityGroup, BMI, and ComputeInstance. |
| CaaS default-networking readiness gate | [DEV] Unit coverage for private Tenant lookup, exact protobuf condition values, pending/terminal states, timeout diagnostics, and propagation of API errors | From the repository root: `uv run pytest -n 0 tests/unit/test_e2e_core_helpers.py tests/unit/test_grpc_client.py` | Fulfillment API responses and time are mocked; this does not verify the deployed private API contract. |
| CaaS default-networking startup | [QE] E2E coverage that the shared fixture observes Fulfillment `DefaultNetworkingReady=True` with reason `AllResourcesReady` or `ProvisioningDisabled`, and waits for subnet feedback before Cluster Create | From the repository root: `METERING_ADAPTER_URL=<adapter> uv run pytest -n 0 tests/e2e/caas/sanity/test_cluster_create.py::test_cluster_create` | Requires the deployed CaaS environment, private/public Fulfillment endpoints and credentials, tenant defaults, healthy Kafka/metering, and provider-backed test prerequisites. The E2E path is not mocked. |
| CaaS Cluster Create NATGateway readiness gate ([QE] [OSAC-5564](https://redhat.atlassian.net/browse/OSAC-5564)) | E2E verifies Create rejects a Cluster attached to a READY Subnet and SecurityGroup when its VirtualNetwork has no NATGateway, and persists no Cluster | From the repository root: `uv run pytest -n 0 tests/e2e/caas/sanity/test_cluster_nat_gateway_gate.py` | Requires provider networking enabled, public/private Fulfillment APIs, Kubernetes access for networking CR readiness, a ClusterTemplate/version, and pull-secret/SSH-key fixtures. It skips when provider networking is disabled; it creates no ClusterOrder or provider workload. |
| CaaS deletion helpers and focused teardown ordering | [DEV] Unit regressions for exact NotFound, parent-before-dependent ordering, polling budgets, safe diagnostics, and no mutation | From the repository root: `uv run pytest -n 0 tests/unit/test_caas_teardown_order.py tests/unit/test_cluster_deletion_polling.py tests/unit/test_caas_deletion_diagnostics.py tests/unit/test_caas_worker_bmi_visibility.py tests/unit/test_caas_two_node_sets.py tests/unit/test_caas_selector_contracts.py` | Kubernetes, fulfillment responses, and time are mocked; selector regressions cover NodeSet identity with shared BMITs and shared-only BMIT reference expectations. Does not prove deployed reconciliation. |
| Focused bare-metal CaaS natural teardown | [QE] E2E for catalog-item-authored cluster creation, worker BMI removal, natural ClusterOrder removal, independent InfraEnv GC, fulfillment removal, and deleted metering | From the repository root: `uv run pytest -n 0 tests/e2e/caas/sanity/test_cluster_create.py::test_cluster_create --junitxml=/tmp/test-output/caas-bm-teardown-junit.xml` | Requires compatible source-pinned OSAC/operator/installer/AAP images and project source, hub access/auth, cluster template/release/disk images, pull-secret/SSH-key inputs, available virtual BMHs, and healthy Kafka/metering. No teardown completion is mocked. |

VMaaS service tests, including ExternalIP capacity, live in `vmaas/sanity/`;
shared fixtures and lifecycle helpers remain under `vmaas/`. From the root,
collect with `uv run pytest --collect-only tests/e2e/vmaas/sanity/` and run
with `uv run pytest -n 0 tests/e2e/vmaas/sanity/` when deployed prerequisites
are available. The manual VMaaS full-install workflow defaults to
`vmaas/sanity`, with `vmaas/serial` or all `vmaas` also selectable, not
`vmaas/regression`. These service suites are distinct from ordered Netris
networking under `networking/vmaas/`.

For deployed Netris BMaaS ExternalIP ownership, run
`uv run pytest -n 0 tests/e2e/networking/bmaas/test_external_ip_ownership.py`
in the required BMaaS/Netris profile. It needs compatible deployed services,
private API/hub access and a free IPv4 `/30`, with no mocks. It checks OSAC
status and pool accounting, not provider IPAM visibility; see the canonical
[suite boundary](../../docs/testing/e2e.md#suite-boundaries).

| Area | Required tier and command |
|---|---|
| Shared helpers, cleanup, reference oracles and failure/telemetry plugins | Collect affected deployed suites; run focused existing checks when behavior changes |
| VMaaS/CaaS/BMaaS/storage/reference lifecycles | QE deployed E2E: collect and run the affected service/tier under `tests/e2e/` with real dependencies |
| BMaaS inventory backend (metal3/netbox) | Unit: `uv run pytest tests/unit/test_netbox_client.py tests/unit/test_inventory.py` with injected fakes; deployed backend routing via the bmaas service suites |
| Netris VMaaS networking and billing | Whole ordered lifecycle: `uv run pytest -n 0 -x tests/e2e/networking/vmaas/test_vmaas_networking.py` |
| Netris BMaaS connectivity/isolation | `uv run pytest -n 0 tests/e2e/networking/bmaas` against its real backend |
| Stretched networking | `uv run pytest -n 0 tests/e2e/networking/stretched` against its real backends |
| Natural bare-metal CaaS teardown | `uv run pytest -n 0 tests/e2e/caas/sanity/test_cluster_create.py::test_cluster_create`; two-NodeSet coverage is in `caas/regression/test_cluster_node_sets.py` |

Ordered Netris VMaaS must run whole, serial (`-n 0`) and fail-fast (`-x`),
without `-k` or isolated lifecycle phases. It requires enabled provisioning,
compatible AAP, real Netris/cudn_evpn, pool capacity, KubeVirt and guest SSH;
unit fakes and collection are not connectivity, billing or isolation proof.
Fixture-owned shared pools require private API permissions and cleanup; never
substitute synthetic allocations or concurrent allocators for stable snapshots.

Natural CaaS teardown waits independently for verified workers, ClusterOrder
and InfraEnv removal, then Fulfillment/deleted-metering evidence; do not use
forced cleanup as proof. Preserve API worker tenant/order/owner identity and CR
tenant/BMI identity (the API owner-reference annotation is not copied to the CR).
Local mocks do not establish hardware, provider or storage behavior; report gaps.

The CaaS CatalogItem sanity and two-NodeSet regression commands listed here
also cover add-on installation: cert-manager/serverless from the catalog, and
direct nvidia-gpu input with NFD dependency resolution, respectively. They use
deployed Fulfillment/operator/AAP, a HostedCluster and OLM CatalogSources,
checking AddOnOperatorsReady, OLM Subscription/CSV and public API status.
These are installation checks, not GPU workload proof; they require no GPU
hardware. Combined catalog+direct input and negative installation are not covered.

## Fulfillment trust enablement

Trust checks require `OSAC_FULFILLMENT_TRUST_E2E=true` and an explicitly
trust-enabled release environment; dev/CI profiles disable trust by default.
CaaS/CSI checks use tenant-admin HostedControlPlane kubeconfig Secrets through
`kubectl` stdin only: never write them to disk or expose them. Per-ClusterOrder
observer credentials are not required. Follow the
[trust prerequisites and rotation sequence](../../docs/testing/e2e.md#fulfillment-trust-enablement).
The rotation gate does not mutate CA or leaf resources. Its operator metrics
path creates a TokenRequest before port-forwarding; the caller needs `create`
on `serviceaccounts/token` for that service account/namespace. Follow the linked
gate input and authorization requirements; do not perform rotation in the test.

## Validation

Reuse the owning suite's `conftest.py` and `tests/e2e/core/` helpers. Validate
helper changes through relevant existing checks; do not introduce a separate
helper-unit harness for routine E2E changes.

### Local checks

Use the root `pyproject.toml` environment through uv. From the repository root:

```bash
uv run ruff check tests/e2e/
uv run ruff format --check tests/e2e/
uv run pytest --collect-only tests/e2e/<suite>/
```

Replace `<suite>` with the affected suite; shared fixture changes may affect
several suites. Collection does not prove deployed provisioning or cleanup.

### Deployed selection

- Run the narrowest independent affected test when prerequisites are available;
  the ordered Netris VMaaS whole-suite rule above overrides narrow selection.
- For independent cases, use
  `uv run pytest tests/e2e/<suite>/<tier>/<test>.py -k '<expression>'`.
  Do not apply that selection pattern to the ordered Netris VMaaS suite.
- For two-NodeSet CaaS lifecycle changes, run
  `uv run pytest -n 0 tests/e2e/caas/regression/test_cluster_node_sets.py::test_cluster_create_with_two_node_sets`.
- Read a nested suite README, such as [`projects/README.md`](projects/README.md), for service-specific credentials and prerequisites.
