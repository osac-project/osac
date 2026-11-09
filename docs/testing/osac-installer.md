# osac-installer

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers, ownership and
evidence. Paths are relative to `osac-installer/` unless noted. Touched-area
requirements: [component guide](../../osac-installer/AGENTS.md#integration-testing).
Run `make help` to list the test targets.

## Test tiers

| Tier | Suite | Real | Faked or omitted |
|---|---|---|---|
| Unit | `make kind-runtime-test` | Makefile/runtime routing and shell behavior | Container engines, Helm, host privilege changes and downloads are command stubs |
| Unit | `make infra-ready-wait-test` | The readiness waits that end `make install-infra` fail the step naming a dependency that never becomes ready | Commands and time are mocked through a stub `kubectl`; no cluster is started |
| Contract (Helm render) | `make mce-render-test`, `make fulfillment-trust-render-test`, `make helm-networking-test` | Production chart templates and their configuration contracts | No Kubernetes API, running hooks, MCE catalog, deployed TLS or provider calls |
| Component integration | `make test` (suite selection in `make help`) | Current images and chart wiring in Kind | Depends on the selected suite; provider networking is disabled in the standard profile |

The installer networking checks script under `scripts/` runs against the
deployed installer release and its idempotent upgrade: Kubernetes/API
configuration and admission with provider networking disabled. It does not
establish the enabled AAP path. Read the script and its registration in the
repository-root `.github/ci-tests.yml` for the current checks.

`helm upgrade --install --wait-for-jobs` waits for hook Jobs but not for
Deployments, so infra dependencies can still be starting when
`make install-osac` begins; the `infra-ready-wait-test` suite guards that
gap.

## Install preflight (OSAC-6561)

Preflight runs while Helm renders the chart. The unit/render/Kind tiers are
registered in `.github/ci-tests.yml` without a Makefile wrapper; read that
registration for how each tier is invoked.

Run the Kind tier only on a dedicated, disposable Kind cluster. The harness
changes cluster-scoped state and restores it only on success, and its cleanup
deletes the `osac-kafka` and `osac-infra` namespaces and the stub CRDs, even
after a setup failure. Refusing a non-Kind context does not protect an existing
Kind development cluster.

| Tier | Real | Faked or not covered |
|---|---|---|
| Unit ([DEV] OSAC-6566, OSAC-6562) | Helm rendering of the real chart, the engine, catalog, report and failure message | The cluster: `lookup`, capabilities, release revision are mocked. The plugin's fake client rejects a LIST of a kind with no objects, so empty-list branches are not covered |
| Harness unit | CRD readiness deadlines and fixture-error diagnostics, through the preflight harness script under `scripts/` | Commands and time are mocked; does not replace the Kind tier. Runs in the installer unit-test CI leg |
| Helm contract | Offline `helm template` of the umbrella and CSI charts, report schema validation, catalog consistency, the packaged archive | No cluster. Asserts the log policy on every hook Job, CSI hook deadlines, that an offline render never blocks, that `preflight.*` values are closed by the schema, and that `helm package` ships the catalog, schema and preflight templates |
| Component integration ([DEV] OSAC-6566) | Multiple Helm majors, a real API server, real `lookup`, release Secrets, hooks, failed-hook logs | A preflight-only chart copy (no workloads); stub CRDs, a Service with an EndpointSlice, Secrets; OpenShift-only checks run as `skipped: not OpenShift` |

The Kind harness uses a preflight-only chart with no OSAC workloads; its stub
prerequisites prove no OpenShift operators or deployed stack. Read the harness
scripts under `scripts/` for fault, upgrade, rollback and packaging assertions.

Coverage gaps:

- The OpenShift-only checks (`ocp-version`, `aap-operator-ready`,
  `aap-license-secret`) run against a real OpenShift API only in the deployed
  OpenShift lanes. Owner: [OSAC-6568](https://redhat.atlassian.net/browse/OSAC-6568).
- Resource shortage and image-pull failures are not detected before
  installation starts; they surface at `--wait` time. Later slices of
  [OSAC-6561](https://redhat.atlassian.net/browse/OSAC-6561) cover them.
- Helm 4 `--dry-run=server` (and `helm install --dry-run=server` on Helm 3)
  cannot print a report on a cluster lacking a CRD the chart renders, even in
  report mode, because Helm maps every rendered kind first. On Helm 3,
  `helm template --dry-run=server -s templates/preflight/report.yaml` works
  anywhere; on Helm 4 the failing render's JSON line is the portable channel.

The `helm unittest` suites for the `osac-infra` chart run in the same
helm-unit CI leg as the umbrella-chart unit tier. They render-test the
monitoring and exporter templates only, not a live exporter or Prometheus;
see the [osac-metering guide](osac-metering.md) for the reader boundaries
and remaining gaps.

## OSAC-5343 deployed enablement coverage

The release E2E path adds trust assertions to existing `tests/e2e/` journeys
(see the [tests/e2e guide](e2e.md) for prerequisites). The release E2E
environment must set `global.fulfillmentTrust.enabled=true` (standard dev and
CI profiles keep it disabled) and `OSAC_FULFILLMENT_TRUST_E2E=true` only for
the release suite, after the compatible admission image and tenant CSI trust
chart are deployed and trust identities exist. Real Fulfillment, operator,
tenant Kubernetes API, CSI and (where enabled) Kafka/metering services; no
protocol test doubles. The harness may read the hosted-cluster kubeconfig to
inspect target state; the automatic trust path never sends it to an AAP job.

Journeys, E2E owned by [OSAC-5547](https://redhat.atlassian.net/browse/OSAC-5547)
unless noted:

- CaaS trust and metering: new ClusterOrder, real tenant ConfigMap, verified
  management clients, event delivery.
- Tenant CSI rollout: real tenant CSI Deployment and storage provisioning.
- VMaaS and BMaaS feedback: deployed resource lifecycle, verified operator
  connection.
- Installer hooks and AAP publishing: deployed Helm post-install hooks,
  successful AAP template publish.
- Dev-full UI Gateway reachability (local dev-full): browser-reachable
  `osac-ui` Deployment through the shared Gateway; check script in
  `osac-installer/scripts/`.
- Overlapping-root rotation (release gate): selected target hashes and CSI
  rollouts, operator and metering verified-client metrics, leaf trust under
  the expected root before advancing rotation.

The rotation gate is read-only; the operator or administrator changes the
Bundle sources and serving leaf between runs (see the rotation-gate suite
fixtures for the required environment variables). A passing overlap run
permits the leaf switch; a passing post-switch run permits old-root removal;
the final run verifies convergence after removal. The suite needs a published
tenant admission image, a compatible tenant CSI chart, trust identities,
fulfillment trust enabled and a working external provider. Until that
environment exists, collection is execution readiness only, not a passed E2E
boundary. Deployment and execution are owned by
[OSAC-5547](https://redhat.atlassian.net/browse/OSAC-5547).
