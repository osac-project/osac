# OSAC installer

Never edit this file except for minor corrections. Do not append new content.

Helm charts and scripts that deploy OSAC in three ordered layers: `osac-deps`,
`osac-infra`, then the `osac` platform chart.

## Required context

Read the applicable references before changing behavior:

- Deployment architecture: [`../docs/guides/installation/helm-deployment-guide.md`](../docs/guides/installation/helm-deployment-guide.md)
- Script behavior: [`README.md`](README.md) and `scripts/`
- Values and schema: `charts/osac/values.yaml` and `charts/osac/values.schema.json`
- Changes to installer values or schema: read the
  [Enclave Wizard pipeline](../docs/agent-context/enclave-wizard-pipeline.md)
  and account for schema consumption by the Enclave plugin and UI.
- Sibling component contracts: the relevant `../<component>/AGENTS.md`

## Invariants

- Every value in `charts/osac/values.yaml` must have a matching schema definition; use closed enums for fixed value sets.
- Preserve deployment order: `install-infra` handles `osac-deps` and `osac-infra` before `install-osac` installs the platform chart.
- Sibling component charts use source directories in this mono-repo; edit the owning component, not a copied packaged dependency.
- The umbrella chart sources `osac-ui` from the tracked `../osac-ui/charts/ui` chart; release packaging may rewrite that dependency to its OCI chart reference.
- Shell scripts use `set -euo pipefail` and shared helpers from `scripts/lib.sh`.
- Always pass an explicit namespace to `oc` and `kubectl` on shared clusters.
- Do not modify cluster-scoped prerequisites on shared clusters; coordinate with cluster administrators before changing them.
- The cluster-scoped `ca-bundle` resource is intentionally shared; do not rename it to avoid collisions.
- Review chart dependency and values changes as deployment changes, including their rendered output.

## Generated and dependency files

- After changing chart dependencies, run `make helm-deps` and review `Chart.lock`/packaged dependency diffs.
- Do not edit sibling component sources through installer chart paths.
- `make sync-charts` is an alias for `make helm-deps`, not another generation step.

## Common Tasks

### Add a value or change component wiring

- Trace the value from `charts/osac/values.yaml` and `values.schema.json`
  through the umbrella templates into the owning sibling chart. Keep default,
  validation, passthrough and rendered-resource behavior consistent.
- Inspect affected overlays under `values/` rather than assuming every
  profile uses the umbrella default. A value exposed to the Enclave Wizard
  also needs the schema-consumer review described above.
- Add render assertions to the owning chart's `tests/` or existing focused
  script contract. Run `make helm-validate` and the relevant helm-unittest
  suite; select additional wiring contracts from the table below.

### Change a dependency or installation step

- Put component chart changes in the sibling source chart. Update umbrella
  dependency metadata only when composition changes, then run `make helm-deps`
  and inspect locks and packaged chart changes before rendering.
- Trace the Makefile target into `scripts/` and reuse `scripts/lib.sh` for
  shared shell behavior. Keep dependency, infrastructure and platform phases
  ordered; a successful template render does not establish readiness.
- For script changes, use the input-validation, readiness/runtime or preflight
  stub checks below before selecting a deployed check. Read the profile's
  prerequisites and cleanup behavior before running any installation target.

<a id="integration-testing"></a>

## Validation

### Tools and working directory

- Run the commands below from `osac-installer/` unless a different cwd is stated.
- Use Helm and Python/uv for chart contracts and script tests. Helm unit suites
  require the helm-unittest plugin; CI pins v1.2.1.
- Chart dependency targets may download archives. Review lock/archive changes
  and distinguish dependency preparation from tests.
- Read the [suite boundaries](../docs/INTEGRATION-TESTING.md#osac-installer)
  and [README.md](README.md) for profile and runtime prerequisites.

### Local chart checks

For values, schema, chart dependencies or rendered resource changes:

```bash
make helm-validate
helm unittest charts/osac
make helm-infra-deps
helm unittest charts/osac-infra
helm unittest ../osac-metering/charts/osac-metering
```

`helm-validate` combines Helm lint and render contracts; the helm-unittest
suites above are separate. Use the owning suite for isolated chart changes.

Focused contracts, selected by the changed wiring:

| Wiring | Command |
|---|---|
| Networking values, manager settings, overlays and admission | `make helm-networking-test` |
| NetworkClass step against a fake fulfillment API | `make network-class-step-test` |
| MCE defaults and lifecycle gate | `make mce-render-test` |
| Optional LVMS worker/device allowlist | `make lvms-optional-path-render-test` |
| Fulfillment trust and CA mounts | `make fulfillment-trust-render-test` |
| Service-account IDP credentials and realm roles | `make service-account-idp-render-test` |
| Fulfillment hook token exchange and dev-full wiring | `make fulfillment-auth-test dev-full-render-test` |
| Billing provider mounts and tuning bounds | `make billing-render-test` |
| EVPN host networking | `make evpn-render-test` |
| OpenBao security context on Kind/OpenShift | `make openbao-security-context-test` |

For metering reader values, the focused selection is
`helm unittest -f 'tests/metering_readers*_test.yaml' charts/osac`; the full
umbrella suite includes it. Kafka exporter/PodMonitor contracts belong to the
infra suite, not a live-exporter check.

### Local script and harness checks

- Runtime, image/dependency loading, cleanup and test-phase wiring:
  `make kind-runtime-test` uses command stubs.
- Infra readiness waits: `make infra-ready-wait-test` uses command stubs.
- Upfront input gate and schema rejection: `make input-validation-test`.
- Preflight render/hook policy: `uv run --script scripts/test-preflight-render.py`.
- Preflight harness restoration: `uv run --script scripts/test-preflight-harness.py`.
- Disk-handoff AAP recording double:
  `python3 -m unittest discover -s ../fulfillment-service/it/testdata -p test_compute_storage_aap.py`.

### Repository checks

From the repository root, run
`yamllint --strict -c .yamllint.yaml osac-installer/`; the shared config excludes
the entire `osac-installer/charts/` tree, including chart metadata and values.
Other commands here run from `osac-installer/`.

Apply the repository-wide checks in the root guide in addition to these
installer-specific checks.

### Deployed integration checks

Select an explicitly authorized disposable environment. Deployment targets
change cluster state and require explicit platform, profile and namespace:

```bash
make install-infra PLATFORM=kind PROFILE=dev NS=osac
make install-osac PLATFORM=kind PROFILE=dev NS=osac
make test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment
```

- Supported suites are `all`, `fulfillment`, `compute-storage`, `operator` and
  `bmf`. Select with `SUITE`; inspect profile-specific prerequisites and cleanup.
- For combined disk handoff, run
  `make test PLATFORM=kind PROFILE=dev NS=osac SUITE=compute-storage`.
  This uses real Fulfillment/PostgreSQL/operator with a recording AAP double.
- For preflight real-API, upgrade and failed-hook logs, run
  `uv run --script scripts/test-preflight-kind.py` against the selected Kind context.
- For UI Gateway reachability, run `./scripts/test-ui-gateway.sh` against
  deployed dev-full. Local rendering does not prove hook token acceptance.
- Render tests and stubs do not establish live readiness, networking traffic
  or provider provisioning. Report those boundaries separately.
