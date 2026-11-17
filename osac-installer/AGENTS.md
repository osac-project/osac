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

## Integration Testing

Commands run from `osac-installer/`; use the matching render target in the
Makefile and the [suite boundaries](../docs/INTEGRATION-TESTING.md#osac-installer).

| Touched area | Required tier and command |
|---|---|
| Values, schema, dependencies or rendered charts | Helm contract: `make helm-validate`; `helm unittest charts/osac`; for infra, `make helm-infra-deps` then `helm unittest charts/osac-infra` |
| Networking, MCE, LVMS, trust, billing or auth wiring | Matching `helm-networking-test`, `mce-render-test`, `lvms-optional-path-render-test`, `fulfillment-trust-render-test`, `billing-render-test`, `fulfillment-auth-test` or `dev-full-render-test` Make target |
| Kind runtime, image/dependency loading, lifecycle or test-phase wiring | Stubbed unit: `make kind-runtime-test` |
| Infra readiness or input validation | Stubbed unit/Helm: `make infra-ready-wait-test input-validation-test` |
| Preflight rendering/hook policy or harness restoration | `uv run --script scripts/test-preflight-render.py`; `uv run --script scripts/test-preflight-harness.py` |
| Preflight real API/upgrade/failure logs | Opt-in Kind component integration: `uv run --script scripts/test-preflight-kind.py` |
| Combined disk handoff | `make kind-runtime-test` and `make test PLATFORM=kind PROFILE=dev NS=osac SUITE=compute-storage`; real Fulfillment/PostgreSQL/operator, recording AAP double |
| Deployed hooks/startup or UI Gateway | Applicable installer `make test` suite; `./scripts/test-ui-gateway.sh` requires deployed dev-full |

Render/unit tests do not prove live readiness or provider behavior. Deployed
checks require an explicitly selected disposable environment and prerequisites.

## Validation

From the repository root, load the shared yamllint config so Helm templates
are excluded and regular YAML is checked with the repository rules:

```bash
yamllint --strict -c .yamllint.yaml osac-installer/
```

From `osac-installer/`:

```bash
make helm-validate
pre-commit run --all-files
make mce-render-test
make fulfillment-trust-render-test
helm unittest charts/osac
make helm-infra-deps   # the infra chart suites need its vendored dependencies
helm unittest charts/osac-infra
helm unittest ../osac-metering/charts/osac-metering
make input-validation-test
uv run --script scripts/test-preflight-render.py
```

`pre-commit run --all-files` does not constitute a full repository secret scan;
the gitleaks hook examines staged changes. Do not claim a full secret scan from
that command alone.

Deployment and integration targets require explicit parameters, for example:

```bash
make install-infra PLATFORM=kind PROFILE=dev NS=osac
make install-osac PLATFORM=kind PROFILE=dev NS=osac
make test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment
```

Supported test suites are `all`, `fulfillment`, `compute-storage`, `operator`, and `bmf`; see the
Makefile and README for profile-specific prerequisites and cleanup.
