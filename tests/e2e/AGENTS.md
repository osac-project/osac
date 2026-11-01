# OSAC E2E tests

Cross-component pytest suites for VMaaS, CaaS, BMaaS, storage, and
resource-reference workflows.

This test suite is part of the OSAC monorepo, not an isolated project. Its
scenarios, fixtures, and shared helpers may affect other components. Apply the
repository-wide rules in [`../../AGENTS.md`](../../AGENTS.md), consider downstream
effects, and follow the instructions for every affected component.

## Invariants

- E2E suites live only under `tests/e2e/`, not in the external `osac-test-infra` checkout; that repository owns infrastructure backends, not suites.
- Put service-owned tests in their service-tier directory. Cross-service networking E2E tests live in `tests/e2e/networking/{vmaas,bmaas,stretched}/`. Test-infra selects them by directory through `TEST_SUITE`; do not add backend-specific pytest markers to the tests. Standard service runs skip them because they select different directories.
- Avoid fixed sleeps; wait for observable resource conditions with bounded timeouts.
- Tests clean up resources they create and never assume the caller's shared-cluster namespace or context.
- Shared fixture changes require collecting all affected suites.
- A new long wait or traffic check reports a failure kind (`tests/e2e/core/failures.py`: `Kind`, `E2EFailure`, `poll_or_fail(kind=...)` in the VMaaS stack); a suite with guests registers a guest snapshot source (`stack.register_guest_snapshot` for VMaaS, `bmaas/guest_ssh.register_guest_snapshot` for BMaaS) and removes it in teardown.
- Failure reporting never changes a test's result and never prints a token: use `run()` (its `CommandFailed` and `CommandTimedOut` carry a sanitized command) and keep polled containers out of messages.

## Touched-area map

| Area | Required coverage | Location and command | Boundary and prerequisites |
|---|---|---|---|
| E2E failure reporting | [DEV] Unit for sanitizing, `CommandFailed`/`StuckState`, failure classification, and the pytest plugin driven through real `python -m pytest` subprocesses (immediate `E2E FAILURE` log line, fsynced `results-<worker>.jsonl`, snapshot cap, source errors and budget, `-x`, xdist workers, kill) | From the root: `uv run pytest -n 0 tests/unit/test_e2e_failure_report.py tests/unit/test_e2e_failure_plugin.py tests/unit/test_e2e_failure_snapshot.py` | Writes `results-<worker>.jsonl` and `failures/<worker>/<nn>-<test>/` under `OSAC_E2E_OUTPUT_DIR` (default: the parent of the `log_file` ini value, `/tmp/test-output`). A snapshot holds API and CR lists, events, and logs of the fulfillment API server, controller and operator, guest network state for guest kinds, and `fabric.json` when the backend provides `OSAC_E2E_FABRIC_SNAPSHOT_CMD` (a command that prints the fabric state; the backend owns it and its credentials). Does not prove the snapshot content against a deployed cluster, a real fabric, or live console output under xdist workers. |
| VMaaS deletion-test finalizer retries | [DEV] Unit for bounded conflict retries, fresh versions, preserved concurrent finalizers, idempotence, and non-conflict failures | From the root: `uv run pytest -n 0 tests/unit/test_vmaas_deletion_finalizer.py` | Test-helper logic only; gRPC responses and time are mocked. Does not establish deployed deletion. |
| VMaaS Netris parallel setup and staged teardown | [DEV] Unit with fakes that record call order: `run_parallel` re-raises `pytest.fail`, waits for every worker and raises the first failure in submit order; Subnet and SecurityGroup start only after the VirtualNetwork waits and overlap; networks map correctly when they finish out of order; VM creates are serial within a tenant; the SSH helper applies once per namespace from eight threads; teardown stages never overlap and a finalizer rerun after a partial teardown is safe; the pool fixture check | From the root: `uv run pytest -n 0 tests/unit/test_vmaas_parallel_setup.py tests/unit/test_vmaas_networking_helpers.py tests/unit/test_vmaas_ssh_helper.py` | Test-helper logic only; gRPC, Kubernetes, SSH and time are faked. Does not prove the deployed suite or its run time. |
| VMaaS deletion feedback with a removed DiskImage | [QE] Regression E2E for status feedback with a stale spec reference, ComputeInstance removal, and explicit dependent SecurityGroup, Subnet, and VirtualNetwork deletion | From the repository root: `uv run pytest -n 0 tests/e2e/vmaas/regression/test_compute_instance_disk_image.py -k deletion_feedback_with_removed_disk_image` | Deployed Fulfillment/database, operator, AAP, and VM provider; a private finalizer holds deletion while the image is removed. Requires the VMaaS template, instance type, storage tier, and networking provider. |
| AgentlessNet Subnet VLAN, real DHCP clients, retry, and cleanup | Deployed lab E2E for saved allocations, leases, gateway/L2 traffic, service restart, `/30`, and peer-preserving lifecycle | Root orchestrator `vlan-e2e.sh`; reusable client and VLAN assertions live in that script | Requires the running SNO and Containerlab Cumulus VX lab, deployed Fulfillment/operator/AAP, source-pinned EE/project, and container access. Client macvlan children use host links `leaf1-swp2`, `leaf2-swp2`, and `leaf2-swp3`; the runner snapshots and restores the switch ports. Does not establish physical-hardware, BMaaS lease-status, NAT, or full-install CI coverage. |
| VMaaS ComputeInstance InstanceType resize, including CatalogItem-created instances | Regression E2E for CLI/API outcomes, applied ComputeInstance configuration, and VMI resources | `tests/e2e/vmaas/regression/test_compute_instance_instance_type.py`; from the repository root, run `uv run pytest tests/e2e/vmaas/regression/test_compute_instance_instance_type.py` | Uses the deployed VMaaS API and Kubernetes endpoints, VM kubeconfig, image, storage tier, subnet, and VM template. Restart-required behavior assumes the single-node VMaaS profile. |
| DiskImage-backed VM provisioning and reference release | [QE] Regression E2E for running/image projection and image deletion after workload teardown | From the repository root: `uv run pytest -n 0 tests/e2e/vmaas/regression/test_compute_instance_disk_image.py` | Real Fulfillment, operator, AAP, and VM provider; requires the configured VMaaS template, network, instance type, and storage tier. API-only DiskImage CRUD/lifecycle, template defaults/protection, obsolete-image rejection, CaaS missing-image rejection, and BMI catalog cross-tenant rejection belong in Fulfillment `it/`, not E2E. |
| VMaaS ComputeInstance additional-disk CatalogItem defaults | Regression E2E for omitted versus explicitly empty `additional_disks`, including the resulting ComputeInstance CR | `tests/e2e/vmaas/regression/test_compute_instance_storage_tier.py`; from the repository root, run `uv run pytest tests/e2e/vmaas/regression/test_compute_instance_storage_tier.py -k empty_additional_disks_disables_catalog_item_default` | Uses the deployed VMaaS API and Kubernetes endpoints, storage tiers, subnet, disk image, instance type, and VM template. |
| Compute-reference cleanup ordering | [DEV] Unit for cleanup policy; [QE] E2E for deployed deletion | From the root: `uv run pytest -n 0 tests/unit/test_e2e_helpers.py`; deployed: `uv run pytest -n 0 tests/e2e/references/test_compute_references.py` | Unit checks use mocked API responses and time. The deployed suite requires VMaaS Fulfillment, operator, AAP, KubeVirt, storage, and tenant networking; it waits for the exact ComputeInstance CR and API record to disappear before releasing references, without removing finalizers. |
| Netris VMaaS connectivity (OSAC-4291) | Ordered regression E2E (`test_00`..`test_14`) selected by `/test vmaas-netris` (`DEPLOY_SUITE=vmaas`), run with `-x` so the job stops at the first failed test: same-Subnet ARP/ICMP, cross-VirtualNetwork and cross-tenant isolation, cluster DNS from OVN-K DHCP, SecurityGroup TCP ingress/egress allow and deny with rule updates, NAT egress, manual and automatic ExternalIP ingress for tenant1 and manual ExternalIP ingress for tenant2, ExternalIPAttachment delete stopping ingress, and one teardown test (`test_14_teardown_leaves_nothing`) that deletes everything in dependency order and checks that nothing is left, including that none of the suite's ExternalIPs is left and the pool `allocated` count equals the live ExternalIPs on the pool (tenant default `default-nat` addresses also draw from the pool, so an owned pool they hold is not deleted; the test names them and fails on any other holder). The routed ExternalIPPool (host route, READY, at least 3 free addresses, the peak of the serial tests) is checked by the `netris_external_path` fixture, so a lab without it errors phase C instead of failing a test. **Removed E2E requirements (not equal coverage):** node service isolation from a tenant VM (no E2E covers it; [networking decisions](../../docs/agent-context/networking-decisions.md) holds the measured behavior; add a check when needed); ICMP SecurityGroup rule rejection that follows the NetworkClass `securityGroupIcmp` capability (the lower tiers cover the rejection but not the capable branch's READY through AAP, which cudn_evpn cannot reach) and second-tenant automatic ExternalIP ingress and garbage collection (`test_12` and `test_13` prove the automatic flow and tenant2 ingress separately). After the module the job runs the billing files as a second pytest run; see the next row. | `uv run pytest -n 0 -x tests/e2e/networking/vmaas/test_vmaas_networking.py` | Requires `vmaas-ci` with `values/vmaas-ci/netris.yaml` (which sets `operator.aap.statusPollInterval` to `5s`), or `full-ci` with `values/full-ci/netris.yaml`, the OCP CUDN EVPN VTEP/underlay, VM kubeconfig, and `kubectl`, `ssh` and `ssh-keygen` on the pytest host. Guest commands run over `kubectl exec` in a test-owned helper Pod in each VM namespace (it joins the VM's primary network and runs `ssh`); the Pod image, by default the digest-pinned OSAC execution-environment image, must carry an ssh client and be pullable by the cluster (`OSAC_E2E_SSH_HELPER_IMAGE`), and each VM SecurityGroup must keep ingress tcp/22 open. Setup creates independent resources at the same time (`stack.run_parallel`): the three networks, then each network's Subnet and SecurityGroup, then the VMs (one tenant's creates one after the other, the tenants at once), then the five VM waits; a child never starts before its own parent is READY in both the CR and the API (VirtualNetwork before Subnet and SecurityGroup, those before VMs, an ExternalIP ALLOCATED before a NATGateway or attachment), and teardown runs the reverse stages. The NAT and ExternalIP tests always run and fail with a diagnosis when the external path is not wired; they need the routed pool (`OSAC_EXTERNAL_IP_POOL_CIDR`, default `198.51.100.24/29`) and pytest on a host that routes it. Tests are numbered and serial, share module-scoped fixtures, and cannot be sliced with `-k`. A stopped run never reaches `test_14`, so the fixture finalizers delete what is left. To debug a live lab, set `OSAC_E2E_KEEP_RESOURCES=true`: `test_14` skips, the finalizers delete nothing and log what stays, and you delete it by hand before the next run; no job sets it. Inter-subnet routing is not covered: each VirtualNetwork has one Subnet. |
| ExternalIP and NATGateway billing | Regression E2E in `test_external_ip_billing.py`, `test_external_ip_reattach_billing.py` and `test_nat_gateway_billing.py`: an ExternalIP billed through deletion, attach/detach/reattach with disjoint attached and unattached meters, and NATGateway billing. The job runs them as a second pytest run after `test_vmaas_networking.py`, also with `-x`: the run starts even when the module failed, and a failure in either run fails the job. `conftest.py` keeps them after the module when both are selected | `METERING_ADAPTER_URL=<echo adapter URL> uv run pytest -n 0 -x tests/e2e/networking/vmaas/test_external_ip_billing.py tests/e2e/networking/vmaas/test_external_ip_reattach_billing.py tests/e2e/networking/vmaas/test_nat_gateway_billing.py` | Needs `global.networking.provisioningEnabled: true`: with it disabled the Fulfillment Service rejects ExternalIP and ExternalIPAttachment creation with `FailedPrecondition`, so standard service profiles cannot run these. Requires `vmaas-ci` with `values/vmaas-ci/netris.yaml`, VM kubeconfig, the routed pool (`OSAC_EXTERNAL_IP_POOL_CIDR`), and the metering echo adapter; they skip without an adapter and carry no `metering` marker, which would fail the whole session at collection. They send no traffic and need no helper Pod. ExternalIPPool capacity, attachment validation and references by name moved to lower tiers named in [integration testing](../../docs/INTEGRATION-TESTING.md). ExternalIPPool capacity initialization, which needs no provider networking, stays in `tests/e2e/vmaas/regression/external_ip/`. |
| Netris BMaaS connectivity | Numbered regression E2E for same-subnet and cross-subnet BMI networking plus cross-tenant isolation; selected by `/test bmaas-netris` (`DEPLOY_SUITE=bmaas`) | `uv run pytest -n 0 tests/e2e/networking/bmaas` | Requires the Netris BMaaS profile, available BMH SSH endpoints, the configured `OSAC_BMI_NETWORK_INTERFACE`, and fabric networking. The PR job excludes the slower cross-tenant class; run it separately with `-k TestCrossTenantBmaasNetworking`. |
| Stretched BM/ComputeInstance connectivity | Backend-agnostic regression E2E for address uniqueness and bidirectional ARP/ICMP on one OSAC Subnet; selected by `DEPLOY_SUITE=full TEST_SUITE=stretched` and the Netris infrastructure job `/test stretched-netris` | `uv run pytest -n 0 tests/e2e/networking/stretched` | The Netris runner uses the `full-ci` profile, BMaaS SSH access, `OSAC_BMI_NETWORK_INTERFACE`, a VM template, `kubectl` on the pytest host, and the OCP CUDN EVPN VTEP/underlay. Its guest commands run over `kubectl exec` in the SSH helper Pod described for the VMaaS row (`OSAC_E2E_SSH_HELPER_IMAGE`). The test itself checks OSAC networking behavior and creates/cleans its VirtualNetwork, Subnet, SecurityGroup, BMI, and ComputeInstance. |
| CaaS deletion helpers and focused teardown ordering | [DEV] Unit regressions for exact NotFound, parent-before-dependent ordering, polling budgets, safe diagnostics, and no mutation | From the repository root: `uv run pytest -n 0 tests/unit/test_caas_teardown_order.py tests/unit/test_cluster_deletion_polling.py tests/unit/test_caas_deletion_diagnostics.py tests/unit/test_caas_worker_bmi_visibility.py tests/unit/test_caas_two_node_sets.py tests/unit/test_caas_selector_contracts.py` | Kubernetes, fulfillment responses, and time are mocked; selector regressions cover NodeSet identity with shared BMITs and shared-only BMIT reference expectations. Does not prove deployed reconciliation. |
| Focused bare-metal CaaS natural teardown | [QE] E2E for catalog-item-authored cluster creation, worker BMI removal, natural ClusterOrder removal, independent InfraEnv GC, fulfillment removal, and deleted metering | From the repository root: `uv run pytest -n 0 tests/e2e/caas/sanity/test_cluster_create.py::test_cluster_create --junitxml=/tmp/test-output/caas-bm-teardown-junit.xml` | Requires compatible source-pinned OSAC/operator/installer/AAP images and project source, hub access/auth, cluster template/release/disk images, pull-secret/SSH-key inputs, available virtual BMHs, and healthy Kafka/metering. No teardown completion is mocked. |

The `test_cluster_create` and `test_cluster_create_with_two_node_sets` scenarios
share natural teardown assertions without the legacy helper's forced cleanup.
`test_cluster_create` authors a published shared ClusterCatalogItem that locks the
version, node sets, and SSH key, and creates its cluster from that item with only a
tenant-local pull secret supplied at create time. The two-node-set scenario still
creates its cluster directly with two node sets and verifies ready worker aggregates,
installed Agents, and per-NodeSet NodePool isolation before deleting; it uses the same
source-pinned environment prerequisites as the focused lifecycle. Run it with
`uv run pytest -n 0 tests/e2e/caas/regression/test_cluster_node_sets.py::test_cluster_create_with_two_node_sets`.
CaaS sanity contains the single-nodepool lifecycle and the fast deletion
feedback test; the lifecycle verifies deployed version resolution through the
ClusterOrder and HostedCluster release image. Two-node-set isolation belongs in
CaaS regression; invalid-version API validation and ClusterVersion deletion
protection belong in the Fulfillment integration suite.
Other CaaS scenarios still using `wait_for_cluster_deletion` do not establish
that boundary. Both scenarios verify the API worker BMI's tenant, cluster-order
label and owner-reference annotation, and the Kubernetes CR's tenant annotation
and BMI UUID label. The API owner-reference annotation is not copied to the CR.
They wait for all verified workers to disappear (480 attempts at five-second
intervals). Only then does the parent wait start, with 121 attempts at
ten-second intervals, followed by
60 InfraEnv attempts at five-second intervals. These are separate polling
budgets (1,200 and 295 seconds of scheduled sleeps), not wall-clock deadlines or
production SLAs; API calls and diagnostics add time. Sanitized snapshots are
sampled approximately every sixty seconds and once on either stage's timeout.
See [integration-testing boundaries](../../docs/INTEGRATION-TESTING.md#testse2e)
for real dependencies and omitted hardware/storage coverage.

Multi-node live hot-plug resize remains outside this suite and is tracked by
[OSAC-5335](https://redhat.atlassian.net/browse/OSAC-5335).

## Fulfillment trust enablement

Set `OSAC_FULFILLMENT_TRUST_E2E=true` to add trust assertions to the CaaS,
CSI storage, VMaaS, and BMaaS journeys. The CaaS and CSI checks read the
HostedControlPlane kubeconfig Secret and pass it to `kubectl` through stdin
without writing it to disk. This kubeconfig provides tenant-admin access to
verify the tenant ConfigMap and CSI Deployment hashes against the management
bundle; per-ClusterOrder observer credentials are not required. Run the
installer hook check in `enablement/test_installer_trust.py` against a full
OSAC Helm release. Dev and CI profile values disable trust by default; enable
trust explicitly when deploying the release E2E environment.

Run `../osac-installer/scripts/test-ui-gateway.sh` against a deployed dev-full
environment to verify the OSAC UI is reachable through its shared Gateway
route. Set `OSAC_UI_URL` when the listener uses a non-default host or port.

Run `enablement/test_ca_rotation_gate.py` with
`OSAC_TRUST_ROTATION_PHASE=overlap` and the candidate
`OSAC_TRUST_ROTATION_EXPECTED_HASH` before selecting the new leaf certificate.
Run it with `OSAC_TRUST_ROTATION_PHASE=post-switch` before removing the retired
root, and with `OSAC_TRUST_ROTATION_PHASE=final` after removal. All runs require
deployed operator and metering metrics and separate old/new root PEM files for
leaf verification.
The rotation test reads resources and metrics; it does not mutate CA or leaf
certificate resources.

## Validation

- Format and lint changed Python with the repository's configured Ruff checks: `uv run ruff check tests/e2e/` and `uv run ruff format --check tests/e2e/`.
- Collect the affected suite with `uv run pytest --collect-only tests/e2e/<suite>/` before running it.
- Run the narrowest affected test when the required cluster and services are available, for example `uv run pytest tests/e2e/<suite>/<tier>/<test>.py -k '<expression>'`.
- Read a nested suite README, such as [`projects/README.md`](projects/README.md), for service-specific credentials and prerequisites.
