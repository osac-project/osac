# osac-installer

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers, ownership and
evidence. Follow this guide for suite setup and limits; inspect test source for
individual cases. Commands and inline paths are relative to `osac-installer/`
unless explicitly marked as repository-root commands.

Touched-area requirements: [component guide](../../osac-installer/AGENTS.md#integration-testing).

## Test tiers and commands

| Tier | Command from `osac-installer/` | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | `make kind-runtime-test` | Makefile/runtime routing and shell behavior | Container engines, Helm, host privilege changes and downloads are command stubs. |
| Contract (Helm render) | `make mce-render-test`; `make fulfillment-trust-render-test`; `make helm-networking-test` | Production chart templates and their configuration contracts | No Kubernetes API, running hooks, MCE catalog, deployed TLS or provider calls. |
| Component integration | `make test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment` (or `operator`, `bmf`) | Current images and chart wiring in Kind | Boundaries and omitted services depend on the selected component suite. Provider networking is disabled in the standard profile. |

`scripts/it-networking-checks.py` runs against the deployed installer release
and its idempotent upgrade. It checks Kubernetes/API configuration and admission
with provider networking disabled; it does not establish the enabled AAP path.
Read the script and the repository-root `.github/ci-tests.yml` registration for the
current checks. Keep render assertions in their tests instead of listing them here.

## Install preflight (OSAC-6561)

Preflight runs while Helm renders the chart. The unit/render/Kind tiers are
registered in the repository-root `.github/ci-tests.yml` and
`.github/workflows/tests.yml`, without
a Makefile wrapper. The Kind script refuses a non-Kind context.

| Tier | Command | Real | Faked or not covered |
|---|---|---|---|
| Unit, [DEV] OSAC-6566 | `helm unittest charts/osac` from `osac-installer/` (plugin pinned in the workflow) | Helm rendering of the real chart, the engine, catalog, report and failure message | The cluster: `lookup`, capabilities and the release revision are mocked. The plugin's fake client rejects a LIST of a kind with no objects, so empty-list branches are not covered here. |
| Helm contract, [DEV] OSAC-6562, OSAC-6566 | `uv run --script scripts/test-preflight-render.py` from `osac-installer/` | Offline `helm template` of the umbrella and CSI charts; report schema validation; catalog consistency; the packaged chart archive | No cluster. Asserts the log policy on every hook Job, the CSI hook deadlines, that an offline render never blocks, that `preflight.*` values are closed by the schema, and that `helm package` ships the catalog, schema and preflight templates and no unit tests. |
| Component integration, [DEV] OSAC-6566 | `uv run --script scripts/test-preflight-kind.py` against a Kind context | Helm 3.18.0, 3.22.0 and 4.2.2, a real API server, real `lookup`, release Secrets, hooks and failed-hook logs | A preflight-only copy of the chart (no workloads); the prerequisites are stub CRDs, a Service with an EndpointSlice and Secrets; OpenShift-only checks run as `skipped: not OpenShift`. |

The Kind harness uses a preflight-only chart with no OSAC workloads. Its stub
prerequisite resources do not prove OpenShift operators or a deployed stack.
Read the scripts for individual fault, upgrade, rollback and packaging assertions.

Coverage gaps:

- The OpenShift-only checks (`ocp-version`, `aap-operator-ready`,
  `aap-license-secret`) run against a real OpenShift API only in the deployed
  OpenShift lanes. Owner: [OSAC-6568](https://redhat.atlassian.net/browse/OSAC-6568).
- Resource shortage and image-pull failures are not detected before the
  installation starts; they surface at `--wait` time. Later slices of
  [OSAC-6561](https://redhat.atlassian.net/browse/OSAC-6561) cover them.
- Helm 4 `--dry-run=server` (and `helm install --dry-run=server` on Helm 3)
  cannot print a report on a cluster that lacks a CRD the chart renders, even
  in report mode, because Helm maps every rendered kind first. On Helm 3 the
  `helm template --dry-run=server -s templates/preflight/report.yaml` form
  works anywhere; on Helm 4 the failing render's JSON line is the portable
  channel.


## OSAC-5343 deployed enablement coverage

The release E2E path adds these assertions to existing user journeys. The
umbrella chart defaults `global.fulfillmentTrust.enabled=true`, which enables
trust reconciliation for tenant-scoped ClusterOrders. Run release E2E only
after the compatible admission image and tenant CSI trust chart are deployed
and trust identities exist. The
standard dev and CI profiles override this feature to disabled. Set
`OSAC_FULFILLMENT_TRUST_E2E=true` only for the release suite. These tests use
real Fulfillment Service, operator, tenant Kubernetes API, CSI, and (where
enabled) Kafka/metering services; they do not use protocol test doubles. The
test harness may read the hosted-cluster kubeconfig to inspect target state;
the automatic trust path never sends it to an AAP job.

| Case | Tier and owner | Location and command | Required boundary |
|---|---|---|---|
| CaaS trust and metering | E2E, OSAC-5547 | `METERING_ADAPTER_URL=<adapter> OSAC_FULFILLMENT_TRUST_E2E=true uv run pytest -n 0 tests/e2e/caas/sanity/test_cluster_create.py` from repo root | New ClusterOrder, real tenant ConfigMap, verified management clients, event delivery. |
| Tenant CSI rollout | E2E, OSAC-5547 | `OSAC_FULFILLMENT_TRUST_E2E=true uv run pytest -n 0 tests/e2e/storage/test_caas_cluster_storage.py` from repo root | Real tenant CSI Deployment and storage provisioning. |
| VMaaS and BMaaS feedback | E2E, OSAC-5547 | `METERING_ADAPTER_URL=<adapter> OSAC_FULFILLMENT_TRUST_E2E=true uv run pytest -n 0 tests/e2e/vmaas/regression/test_compute_instance_creation.py tests/e2e/bmaas/sanity/test_baremetal_instance_lifecycle.py` from repo root | Deployed resource lifecycle and verified operator connection. |
| Installer hooks and AAP publishing | E2E, OSAC-5547 | `uv run pytest -n 0 tests/e2e/enablement/test_installer_trust.py` from repo root | Deployed Helm post-install hooks and successful AAP template publish. |
| Dev-full UI Gateway reachability | E2E, local dev-full | `OSAC_UI_URL=http://ui.osac.localhost:8080 osac-installer/scripts/test-ui-gateway.sh` from repo root | Browser-reachable `osac-ui` Deployment through the shared Gateway. |
| Overlapping-root rotation | E2E release gate, OSAC-5547 | `OSAC_TRUST_ROTATION_PHASE=overlap OSAC_TRUST_ROTATION_EXPECTED_HASH=<sha256> OSAC_TRUST_LEAF_ENDPOINT=<dns:port> OSAC_TRUST_OLD_ROOT_PEM_PATH=<file> OSAC_TRUST_NEW_ROOT_PEM_PATH=<file> uv run pytest -n 0 tests/e2e/enablement/test_ca_rotation_gate.py` from repo root; repeat with `OSAC_TRUST_ROTATION_PHASE=post-switch` and `OSAC_TRUST_ROTATION_PHASE=final` at the corresponding hash | All selected target hashes and CSI rollouts, operator and metering verified-client metrics, and leaf trust under the expected root before advancing rotation. |

The rotation gate is read-only. The operator or administrator changes the
Bundle sources and serving leaf between runs. A passing overlap run permits
the leaf switch; a passing post-switch run permits old-root removal. The final
run verifies convergence after old-root removal. The suite needs an environment
with a published tenant admission image, a compatible tenant CSI chart and
trust identities, fulfillment trust enabled, and a working external provider.
Until that environment exists, collection is execution readiness only and
does not count as a passed E2E boundary. Deployment and execution are owned by
[OSAC-5547](https://redhat.atlassian.net/browse/OSAC-5547).
