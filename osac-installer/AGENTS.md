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
| Networking values, manager settings, overlays and admission | `make helm-validate PYTEST_TARGETS=scripts/helm-validate/tests/network` |
| NetworkClass step against a fake fulfillment API | `make helm-validate PYTEST_TARGETS=scripts/helm-validate/tests/network/test_network-class-step.py` |
| MCE defaults and lifecycle gate | `make helm-validate PYTEST_TARGETS=scripts/helm-validate/tests/test_mce-render.py` |
| Optional LVMS worker/device allowlist | `make helm-validate PYTEST_TARGETS=scripts/helm-validate/tests/test_lvms-optional-path-render.py` |
| Fulfillment trust and CA mounts | `make helm-validate PYTEST_TARGETS=scripts/helm-validate/tests/test_fulfillment-trust-render.py` |
| CSI/storage-provider render contract (`OSAC_VENDOR_CONTROLLERS`, fulfillment-endpoint cardinality, csi-driver node DaemonSet arguments and credentials) | `make helm-validate PYTEST_TARGETS=scripts/helm-validate/tests/test_csi-render.py` |
| Service-account IDP credentials and realm roles | `make helm-validate PYTEST_TARGETS=scripts/helm-validate/tests/test_service-account-idp-render.py` |
| Fulfillment hook token exchange and dev-full wiring | `make helm-validate PYTEST_TARGETS=scripts/helm-validate/tests/test_fulfillment-auth.py` and `make helm-validate PYTEST_TARGETS=scripts/helm-validate/tests/test_dev-full-render.py` |
| Billing provider mounts and tuning bounds | `make helm-validate PYTEST_TARGETS=scripts/helm-validate/tests/test_billing-render.py` |
| EVPN host networking | `make helm-validate PYTEST_TARGETS=scripts/helm-validate/tests/network/test_evpn-render.py` |
| OpenBao security context on Kind/OpenShift | `make helm-validate PYTEST_TARGETS=scripts/helm-validate/tests/test_openbao-security-context.py` |

For metering reader values, run `make -C ../osac-metering readers-render-test`.
Offline chart rendering does not prove live exporter or broker behavior.

### Local script and harness checks

- Runtime, image/dependency loading, cleanup and test-phase wiring:
  `make helm-validate PYTEST_TARGETS=scripts/helm-validate/tests/test_kind-runtime.py`
  uses command stubs.
- Mac Fedora image build/load and catalog contracts:
  `make dev-full-fedora-test` uses fake commands.
- Infra readiness waits: `make infra-ready-wait-test` uses command stubs.
- Upfront input gate and schema rejection: `make input-validation-test`.
- Preflight render/hook policy:
  `make helm-validate PYTEST_TARGETS=scripts/helm-validate/tests/test_preflight-render.py`.
- Preflight harness restoration:
  `make helm-validate PYTEST_TARGETS=scripts/helm-validate/tests/test_preflight-harness.py`.
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
  `uv run --script scripts/helm-validate/preflight-kind.py` against the selected Kind context.
- For UI Gateway reachability, run `./scripts/test-ui-gateway.sh` against
  deployed dev-full. Local rendering does not prove hook token acceptance.
- Render tests and stubs do not establish live readiness, networking traffic
  or provider provisioning. Report those boundaries separately.
