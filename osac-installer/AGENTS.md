# OSAC installer

Never edit this file except for minor corrections. Do not append new content.

Helm charts and scripts that deploy OSAC in three ordered layers: `osac-deps`,
`osac-infra`, then the `osac` platform chart.

## Required context

Read the applicable references before changing behavior:

- Deployment architecture: [`../docs/guides/installation/helm-deployment-guide.md`](../docs/guides/installation/helm-deployment-guide.md)
- Script behavior: [`README.md`](README.md) and `scripts/`
- Values and schema: `charts/osac/values.yaml` and `charts/osac/values.schema.json`
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
- Review values, schema, passthrough and `values/` overlays together with
  rendered output; profile overrides can differ from umbrella defaults.

## Generated and dependency files

- After changing chart dependencies, run `make helm-deps` and review `Chart.lock`/packaged dependency diffs.
- `make sync-charts` is an alias for `make helm-deps`, not another generation step.

<a id="integration-testing"></a>

## Validation

### Tools and working directory

- Run the commands below from `osac-installer/` unless a different cwd is stated.
- Use Helm and Python/uv for chart contracts and script tests.
- Chart dependency targets may download archives. Review lock/archive changes
  and distinguish dependency preparation from tests.
- Read the [suite boundaries](../docs/INTEGRATION-TESTING.md#osac-installer)
  and [README.md](README.md) for profile and runtime prerequisites.

### Local chart checks

For values, schema, chart dependencies or rendered resource changes:

```bash
make helm-validate
```

`helm-validate` combines Helm lint and render contracts. For isolated changes,
select the relevant existing render check below rather than running every check.

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

For metering reader values, run `make -C ../osac-metering readers-render-test`.
Offline chart rendering does not prove live exporter or broker behavior.

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

### Deployed integration checks

Select an explicitly authorized disposable environment. Deployment targets
change cluster state and require explicit platform, profile and namespace:

```bash
make install-infra PLATFORM=kind PROFILE=dev NS=osac
make install-osac PLATFORM=kind PROFILE=dev NS=osac
make test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment
```

- Supported suites are `all`, `fulfillment`, `compute-storage`, `operator`,
  `bmf` and the opt-in `caas` suite (`PLATFORM=kind PROFILE=dev NS=osac`).
  Select with `SUITE`; inspect profile-specific prerequisites and cleanup.
- For combined disk handoff, run
  `make test PLATFORM=kind PROFILE=dev NS=osac SUITE=compute-storage`.
  This uses real Fulfillment/PostgreSQL/operator with a recording AAP double.
- For preflight real-API, upgrade and failed-hook logs, run
  `uv run --script scripts/test-preflight-kind.py` against the selected Kind context.
- For UI Gateway reachability, run `./scripts/test-ui-gateway.sh` against
  deployed dev-full. Local rendering does not prove hook token acceptance.
- Render tests and stubs do not establish live readiness, networking traffic
  or provider provisioning. Report those boundaries separately.
