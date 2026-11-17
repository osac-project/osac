# OSAC E2E tests

Never edit this file except for minor corrections. Do not append new content.

Cross-component pytest suites for VMaaS, CaaS, BMaaS, storage, and
resource-reference workflows.

## Invariants

- Cross-component pytest suites live under `tests/e2e/`, not in the external `osac-test-infra` checkout; that repository owns infrastructure backends, not suites.
- Put service-owned tests in their service-tier directory. Cross-service networking E2E tests live in `tests/e2e/networking/{vmaas,bmaas,stretched}/`. Test-infra selects them by directory through `TEST_SUITE`; do not add backend-specific pytest markers to the tests. Standard service runs skip them because they select different directories.
- Avoid fixed sleeps; wait for observable resource conditions with bounded timeouts.
- Tests clean up resources they create and never assume the caller's shared-cluster namespace or context.
- Shared fixture changes require collecting all affected suites.
- A new long wait or traffic check reports a failure kind (`tests/e2e/core/failures.py`: `Kind`, `E2EFailure`, `poll_or_fail(kind=...)` in the VMaaS stack); a suite with guests registers a guest snapshot source (`stack.register_guest_snapshot` for VMaaS, `bmaas/guest_ssh.register_guest_snapshot` for BMaaS) and removes it in teardown.
- Failure reporting never changes a test's result and never prints a token: use `run()` (its `CommandFailed` and `CommandTimedOut` carry a sanitized command) and keep polled containers out of messages.
- Telemetry is optional and never changes outcomes. Preserve worker attribution
  (`osac.ci.xdist_worker`) and fixture-reuse spans; overlapping fixture spans
  are not additive runner wall time.

## Touched-area map

All commands below run from the repository root. Read
[suite boundaries and prerequisites](../../docs/testing/e2e.md#suite-boundaries)
and [integration boundaries](../../docs/INTEGRATION-TESTING.md#testse2e)
before selecting deployed coverage.

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
| Shared helpers, cleanup, reference oracles and failure/telemetry plugins | DEV Unit: `uv run pytest -n 0 tests/unit`; collect every affected deployed suite |
| VMaaS/CaaS/BMaaS/storage/reference journeys | QE deployed E2E: collect and run the affected service/tier under `tests/e2e/` with real dependencies |
| Netris VMaaS networking and billing | Whole ordered lifecycle: `uv run pytest -n 0 -x tests/e2e/networking/vmaas/test_vmaas_networking.py` |
| Netris BMaaS connectivity/isolation | `uv run pytest -n 0 tests/e2e/networking/bmaas` against its real backend |
| Natural bare-metal CaaS teardown | `uv run pytest -n 0 tests/e2e/caas/sanity/test_cluster_create.py::test_cluster_create`; two-NodeSet journey is in `caas/regression/test_cluster_node_sets.py` |

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

From the repository root, run `osac-installer/scripts/test-ui-gateway.sh`
against a deployed dev-full
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
- Run the narrowest independent affected test when prerequisites are available;
  the ordered Netris VMaaS whole-suite rule above overrides narrow selection.
- Read a nested suite README, such as [`projects/README.md`](projects/README.md), for service-specific credentials and prerequisites.
