# OSAC installer

Helm charts and scripts that deploy OSAC in three ordered layers: `osac-deps`,
`osac-infra`, then the `osac` platform chart.

This component is part of the OSAC monorepo, not an isolated project. Its APIs,
generated artifacts, deployment configuration, and runtime behavior may affect
other components. Apply the repository-wide rules in
[`../AGENTS.md`](../AGENTS.md), consider downstream consumers before changing
behavior, and follow the instructions for every affected component.

## Required context

Before changing this component, identify the documents relevant to the change
below, then read and follow them. These documents are authoritative for their
respective areas.

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
- `make sync-charts` is an alias for `make helm-deps`; review dependency changes before committing.

## Integration Testing

| Touched area | Required validation | Command / follow-up |
|---|---|---|
| Shared provider-networking provisioning configuration | Helm contract | `make helm-networking-test` (GATE-CONFIG-01) |
| MCE defaults, lifecycle gate, image overrides, or compatibility RBAC | Helm contract | `make mce-render-test` |
| OSAC-managed LVMS device allowlist | Helm contract | `make lvms-optional-path-render-test`; verifies the worker selector and configured optional paths without cluster access. |
| Kind runtime selection, sudo authentication, image loading, device-mapper permissions, cluster/storage lifecycle, or devstack Helm apply flags | Unit with command stubs | `make kind-runtime-test` |
| Deployed dev-full OSAC UI Gateway reachability | Component integration | `./scripts/test-ui-gateway.sh` |
| TopoLVM chart dependency download, cache reuse, build scoping, or install preparation | Unit with command stubs | `make kind-runtime-test` |
| `install-infra` readiness waits for the CA issuer, bundled PostgreSQL, or Kafka | Unit with command stubs | `make infra-ready-wait-test` |
| Fulfillment integration report opt-in and output paths | Unit with expanded Makefile commands | `make kind-runtime-test` |
| Operator-phase AAP recorder wiring for provider-disabled ComputeInstance coverage (GATE-VM-04) | Unit with expanded Makefile commands | `make kind-runtime-test` |
| Combined ComputeInstance disk handoff runner and AAP recorder | Unit and component integration | `make kind-runtime-test`; `python3 -m unittest discover -s ../fulfillment-service/it/testdata -p test_compute_storage_aap.py`; `make test PLATFORM=kind PROFILE=dev NS=osac SUITE=compute-storage`. Real Fulfillment APIs/PostgreSQL, operator and target StorageClass lookup; AAP execution is a recording double. See [coverage boundaries](../docs/INTEGRATION-TESTING.md#computeinstance-disk-handoff-osac-6685). |
| Fulfillment hook commands, CA mounts, or gate rendering | Helm contract | `make fulfillment-trust-render-test` |
| Billing provider values, Secret mount, or tuning bounds | Helm contract | `make billing-render-test` |
| Fulfillment token helper or dev-full hook credential wiring | Unit and Helm contract | `make fulfillment-auth-test` and `make dev-full-render-test` |
| Service-account IDP credential mount and Keycloak management roles | Helm contract | `make service-account-idp-render-test` |
| Deployed dev-full hook authentication | Component integration | Coverage gap tracked by [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843); local `install-devstack` invokes the hooks but does not assert token acceptance. |
| Fulfillment Volume lifecycle | Component integration | `../fulfillment-service/it/it_volume_lifecycle_test.go` covers Volume creation and deletion, status, hub assignment, and cleanup through `make test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment`. |
| `global.networking` values, schema, networking validation, cudn_evpn or Netris registration (`test_cudn_evpn_registration_is_contract_format`, `test_netris_registration_is_contract_format`), admission policy, or `network-fulfillment-ig` rendering | Helm contract | `make helm-networking-test` |
| Netris manager settings, launch protection, CA ConfigMap mount, and cluster-job isolation | Helm contract | `make helm-networking-test` (`AapSettingsTest`) |
| Netris CI overlays (`values/{vmaas-ci,bmaas-ci,full-ci}/netris.yaml`) | Helm contract | `make helm-networking-test` (`NetrisOverlayTest`) |
| NetworkClass step (`files/network-class-step.py`) | Unit with a fake fulfillment API | `make network-class-step-test` |
| OpenBao security context, podSecurityContext, or Kind/OpenShift UID handling | Helm contract | `make openbao-security-context-test` |
| `evpn` values, EVPN ConfigMap, or configure-evpn hook | Helm contract | `make evpn-render-test` |
| Kafka exporter, its PodMonitor, its consumer lag rule, `kafka.exporter.*` values, or the PodMonitor labels | Unit (mocked cluster) | `make helm-infra-deps && helm unittest charts/osac-infra` (helm-unittest plugin; CI pins v1.2.1 and runs the suite in the `helm-unit` leg) renders `spec.kafkaExporter` only when `kafka.exporter.enabled` is on, renders the PodMonitor and `MeteringConsumerLag` rule, merges `kafka.exporter.podMonitor.labels`, and asserts all three are absent without Kafka, the exporter value, or the monitoring CRDs; a live exporter is not checked |
| Install preflight engine, catalog, report schema, or `preflight.*` values | Unit (mocked cluster) | `helm unittest charts/osac` (helm-unittest plugin; CI pins v1.2.1) |
| `metering.readers` values (reader name only, all tenants), schema, umbrella passthrough, or reserved names | Unit (mocked cluster) | `helm unittest -f 'tests/metering_readers*_test.yaml' charts/osac` (the suites also run in the full `helm unittest charts/osac`) |
| Install preflight rendering contract, hook log policy, or CSI hook deadlines | Helm contract | `uv run --script scripts/test-preflight-render.py` |
| Kind preflight harness readiness or fixture restoration | Unit (mocked commands) | `uv run --script scripts/test-preflight-harness.py` |
| Install preflight against a real API server, upgrade behavior, or failed-hook logs | Component integration | `uv run --script scripts/test-preflight-kind.py` against a Kind context; see [install preflight](../docs/INTEGRATION-TESTING.md#install-preflight-osac-6561) |
| Deployed hooks or cross-component startup | Component integration | `make test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment` with a deployed Kind environment; see [suite boundaries](../docs/INTEGRATION-TESTING.md#osac-installer) |

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
