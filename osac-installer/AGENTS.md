# OSAC Installer

Helm-based deployment orchestrator for OSAC components. No Go code, no builds, no unit tests — only structural validation.

Helm-based deployment system for the OSAC platform in the `osac` mono-repo.
osac-operator, fulfillment-service, osac-aap, bare-metal-fulfillment-operator,
osac-csi-driver, and osac-metering are sibling directories at the repository
root (referenced via `file://` in `charts/osac/Chart.yaml`). **osac-ui** is an
external OCI chart dependency. Deployment uses three Helm charts in sequence:
`charts/osac-operators/` (Phase 1), `charts/osac-prereqs/` (Phase 2),
`charts/osac/` (Phase 3).

## Quick Start

```bash
# Build umbrella chart dependencies
make helm-deps

# Validate changes
yamllint --strict .
pre-commit run --all-files   # gitleaks hook is staged-only; use `git commit` or CI for secrets
make helm-lint
make helm-validate
```

## Common Commands

```bash
# Helm lint (all three charts)
make helm-lint

# Helm template render (dry-run validation against all values files)
make helm-validate

# Rebuild chart dependencies
make helm-deps

# Deploy to OpenShift (three-phase Helm install)
make install VALUES_FILE=values/<env>/values.yaml

# Individual install phases
make install-operators VALUES_FILE=values/<env>/values.yaml
make install-prereqs VALUES_FILE=values/<env>/values.yaml
make install-osac VALUES_FILE=values/<env>/values.yaml

# Uninstall
make uninstall
```

## Critical Rules

**Mono-repo components (READ ONLY from osac-installer paths):**
- osac-operator, fulfillment-service, osac-aap, bare-metal-fulfillment-operator,
  osac-csi-driver, and osac-metering live as sibling directories at the `osac`
  repo root — edit them there and land one mono-repo PR; do not treat
  `osac-installer/` as the source of truth for component code.
- **osac-ui** is external (OCI chart); the installer references a released
  version in `charts/osac/Chart.yaml` unless a workflow overrides it (e.g.
  nightly `rewrite_umbrella_osac_ui_dependency()`).

**Helm Schema:**
- Every value in `charts/osac/values.yaml` **must** have matching `values.schema.json` entry
- Use `enum` for fields with known valid values

**Shell Scripts:**
- Use `set -euo pipefail` in all `scripts/*.sh`
- Source `scripts/lib.sh` for: `retry_until`, `wait_for_resource`, `wait_for_namespace_cleanup`

**Git Workflow:**
- Push to `fork` remote, never `origin`
- PRs: `fork/<branch>` → `origin/main`
- Commits: DCO (`-s`) + `Assisted-by: Claude Code <noreply@anthropic.com>`

**Shared Clusters:**
- Always use `-n <namespace>` in `oc`/`kubectl` — never rely on context

## Architecture

See `docs/helm-deployment-guide.md` for complete architecture details, including:
- Helm chart structure and dependencies
- Mono-repo component layout and version tracking (per-component git tags)
- Prerequisites and operator deployment patterns
- Values file organization per environment

```text
charts/osac/           # Helm umbrella chart (Chart.yaml, values.yaml, values.schema.json)
charts/osac-operators/ # Phase 1: OLM operator subscriptions
charts/osac-prereqs/   # Phase 2: CRD instances, certs, Keycloak
values/<env>/          # Environment values (development, vmaas-ci, caas-ci)
prerequisites/         # Reference manifests for manual prerequisite installation
scripts/               # Automation scripts (see README.md for full list)
```

### Helm Charts (Three-Phase Deployment)

```text
Phase 1: charts/osac-operators/         # OLM operator subscriptions
  Installs: cert-manager, AAP, LVMS, CNV, MCE, MetalLB
  Hook scripts wait for operators to be ready before proceeding

Phase 2: charts/osac-prereqs/           # Cluster prerequisites
  Configures: certificates (CA issuer, trust-manager), Keycloak,
  operator CRs (HyperConverged, LVMCluster, MetalLB, MCE)
  Hook scripts configure each operator after its CRD is ready

Phase 3: charts/osac/                   # OSAC platform (umbrella chart)
  Dependencies:
    osac-operator-crds, osac-operator, fulfillment-service, osac-aap,
      bare-metal-fulfillment-operator-crds,
      bare-metal-fulfillment-operator (conditional: bmf.enabled)
      -- mono-repo-resident sibling directories, via file:// references
    csi-driver, csi-backends (conditional: csiDriver.enabled)
      -- osac-csi-driver, a mono-repo-resident sibling directory checked
      out at the repository root, also via a file:// reference
    osac-ui (conditional: ui.enabled)
      -- a real external chart, via an oci:// reference pinned to a
      released version in Chart.yaml
  Templates: bundled-postgres, hub-access, hooks (create-hub,
    pre-install-validate, publish-templates, seed-cluster-versions)
  values.schema.json validates all configuration
```

### Values Environments

```text
values/
  development/values.yaml              # All controllers, latest images
  vmaas-ci/values.yaml                 # VMaaS CI: computeInstance + tenant + networking
  caas-ci/values.yaml                  # CaaS CI: clusterOrder + tenant + networking
  bmaas-ci/values.yaml                 # BM-as-a-Service CI: bmf + storage + bareMetalInstance
```

Pull secrets and AAP license files are stored alongside values files (e.g.,
`values/<env>/pull-secret.json`, `values/<env>/license.zip`).

osac-operator, fulfillment-service, osac-aap, bare-metal-fulfillment-operator,
and osac-csi-driver are all mono-repo-resident directories checked out at the
repository root, not submodules -- they share this repo's own commit history
with osac-installer itself (there are no submodules under `base/` any longer).
There is deliberately no image-tag pinning/syncing in `values/*/values.yaml` for
fulfillment-service, osac-operator, osac-aap, and bare-metal-fulfillment-operator:
CI values files use the live tag published by each component's own workflow --
`main` for fulfillment-service (the only one of the four that doesn't publish a
current `latest`) and `latest` for osac-operator, osac-aap, and
bare-metal-fulfillment-operator.
There is no separate commit/tag to keep in sync and no bump-bot involved.

Prerequisites are installed via Phase 1 (`make install-operators`) and Phase 2
(`make install-prereqs`), each gated by values toggles. `ca-bundle` Bundle is
cluster-scoped and managed by the `osac-prereqs` chart via trust-manager. See
`Makefile` for underlying commands and `docs/helm-deployment-guide.md` for
phase details.

## Key Scripts

See `README.md` for complete script documentation. Most commonly used:

- **teardown.sh** -- Full teardown: uninstalls Helm releases, removes operators and CRDs
- **setup-remote-cluster.sh** -- CI-only: prepares a fresh remote cluster (LVMS, CNV, service accounts)
- **create-hub-access-kubeconfig.sh** -- Generates `kubeconfig.hub-access` from the hub-access ServiceAccount token
- **oc.sh** -- Wraps `oc` with `--as` impersonation when `OC_IMPERSONATE` is set
- **refresh-after-snapshot.py** -- Refreshes Helm-deployed cluster after booting from cold snapshot
- **setup-caas-agents.sh** -- Sets up CaaS agent infrastructure (InfraEnv + agent VM + label + approve)
- **lib.sh** -- Shared shell functions: `retry_until`, `wait_for_resource`, `wait_for_namespace_cleanup`, `retry_command`, `http_retry`, `http_json`, `resolve_release_tag(path, [tag_prefix])` (nearest `<prefix>/vX.Y.Z` git tag; default prefix `osac`), `resolve_bare_release_tag(path)` (nearest bare `vX.Y.Z` tag for external repos like osac-ui), `resolve_bare_release_tag_at(path, ref)` (nearest ancestor bare tag at a pinned commit), `check_postgres_prerequisites`
- **nightly-charts.sh** -- Nightly chart manifest + Slack helpers (`check_osac_ui_image`, `append_chart_version`, `build_slack_charts_published_summary`, `stamp_osac_ui_chart`, `stamp_umbrella_ui_image_ref`, `stamp_umbrella_ui_values`, `rewrite_umbrella_osac_ui_dependency`, `rewrite_umbrella_osac_ui_dependency_and_rebuild`)

### CI Workflows

GitHub Actions only discovers workflows under the repo root's `.github/workflows/`,
so osac-installer-specific CI now lives there (not under `osac-installer/.github/`):
`nightly-build.yaml` (scheduled nightly + manual dispatch: `prepare` gates
osac-ui@main on an existing `ghcr.io/.../osac-ui:sha-<7>` image, writes
`pinned/osac-ui.commit`, stamps umbrella `ui.images.ui` on umbrella + CI
values, and pushes a temp branch; E2E validates that branch; `publish`
versions every mono-repo sub-chart from per-component `<component>/vX.Y.Z`
tags, packages, and pushes each to GHCR, then checks out pinned osac-ui,
stamps/publishes its sub-chart with the gated image ref, rebuilds umbrella
dependencies, and publishes the umbrella chart — this workflow publishes charts
only; mono-repo and osac-ui container images are referenced from values/chart
stamps, not rebuilt here (`images.txt` lists the rendered refs);
`tag-and-notify` tags `osac/v<version>` and Slack-lists `chart-versions.txt`)
and `publish-osac-installer-chart.yaml` (manual-dispatch umbrella chart release;
takes one mono-repo release `version` plus an independent `ui_version` for
osac-ui).
Nightly sub-chart OCI publishing covers osac-operator (operator +
operator-crds), fulfillment-service, osac-aap, bare-metal-fulfillment-operator
(+ crds), osac-metering, osac-csi-driver (csi-driver + csi-backends), external
osac-ui, and the umbrella chart. Baseline semver uses `resolve_release_tag()`
per mono-repo component; components without a `<component>/vX.Y.Z` tag yet
(e.g. osac-metering, osac-csi-driver) are skipped until their first release
tag is cut. osac-ui uses `resolve_bare_release_tag_at()` on the pinned commit.

**osac-ui nightly source strategy** (external repo; see `nightly-build.yaml`
`prepare` and `Resolve gated osac-ui SHA` steps):

| Step | Behavior |
|------|----------|
| `check_osac_ui_image()` | `ls-remote osac-ui@main` → verify `ghcr.io/.../osac-ui:sha-<7>` manifest exists (HTTP 200); fail if image not published yet |
| Pin once per run | Full SHA written to `pinned/osac-ui.commit` on temp branch; publish checks out that exact commit |
| Baseline semver | `resolve_bare_release_tag_at()` uses `git describe` on the pinned SHA (nearest ancestor `vX.Y.Z`) |
| Umbrella image | `stamp_umbrella_ui_values()` sets `ui.images.ui` to `ghcr.io/.../osac-ui:sha-<7>` on umbrella + CI values (parent values win over subchart defaults) |
| OCI chart dep | `rewrite_umbrella_osac_ui_dependency_and_rebuild()` rewrites Chart.yaml, deletes Chart.lock, runs `helm dependency build` |

Slack success notifications list all published charts (including osac-ui) in a
box table. `chart_version_url()` emits `::warning::` when a GHCR package page
lookup fails so unlinked versions in Slack are visible in the Actions log.
osac-installer's own `e2e-*-full-install.yml`, `helm-lint.yaml`, and
`integration-tests.yml` coverage is also at root (matrixed/composed alongside the
other components). See root `.github/workflows/` for the full list.

## Workflows

AI-assisted workflows reference detailed phase instructions:

- **Bugfix workflow:** `.ai-bot/new-ticket-workflow.md` → phases in `.ai-workflows/bugfix/skills/`
- **Review feedback:** `.ai-bot/feedback-workflow.md` → phases in `.ai-workflows/bugfix/skills/feedback.md`

## Documentation

Detailed information moved from this file to specialized docs:

- **Bugfix workflow orchestrator:** `.ai-bot/new-ticket-workflow.md` (phases: assess → diagnose → fix → validate → review → pr)
- **Review feedback workflow:** `.ai-bot/feedback-workflow.md`
- **Validation commands & conventions:** `.ai-bot/instructions.md`
- **Architecture & deployment:** `docs/helm-deployment-guide.md`
- **Script reference:** `README.md`
- **CLI usage:** `OSAC-CLI-HOWTO.md`
- **Component conventions:** sibling dirs at repo root (e.g.
  `../fulfillment-service/AGENTS.md`, `../osac-operator/AGENTS.md`)
- **Design docs:** [osac-project/docs/architecture](https://github.com/osac-project/docs/tree/main/architecture)
