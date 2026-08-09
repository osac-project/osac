# OSAC Installer

Helm-based deployment orchestrator for OSAC components. No Go code, no builds, no unit tests — only structural validation.

Helm-based deployment system for the OSAC platform. Component repos (osac-operator, osac-fulfillment-service, osac-aap, bare-metal-fulfillment-operator, osac-ui) are aggregated as Git submodules under `base/` for version tracking. Deployment uses three Helm charts in sequence: `charts/osac-operators/` (Phase 1), `charts/osac-prereqs/` (Phase 2), `charts/osac/` (Phase 3).

## Quick Start

```bash
# Initialize submodules
git submodule update --init --recursive

# Validate changes
yamllint --strict .
pre-commit run --all-files   # gitleaks hook is staged-only; use `git commit` or CI for secrets
make helm-lint
make helm-validate
```

## Common Commands

```bash
# Initialize submodules
git submodule update --init --recursive

# Helm lint (all three charts)
make helm-lint

# Helm template render (dry-run validation against all values files)
make helm-validate

# Sync submodules and rebuild chart dependencies
make sync-charts

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

**Submodules (READ ONLY):**
- Never modify any `base/*/` directories (discover submodules with: `git submodule status`)
- Changes belong in component repos
- All git commands must run from installer root — never `cd` into submodule directories or use `git -C base/...`

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
- Submodule organization and version tracking
- Prerequisites and operator deployment patterns
- Values file organization per environment

```text
charts/osac/           # Helm umbrella chart (Chart.yaml, values.yaml, values.schema.json)
charts/osac-operators/ # Phase 1: OLM operator subscriptions
charts/osac-prereqs/   # Phase 2: CRD instances, certs, Keycloak
values/<env>/          # Environment values (development, vmaas-ci, caas-ci)
base/                  # Git submodules — discover with: git submodule status
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
      -- osac-csi-driver, the one remaining real submodule under base/,
      also via a file:// reference
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

osac-operator, fulfillment-service, osac-aap, and bare-metal-fulfillment-operator
are mono-repo-resident directories, not submodules -- they share this repo's own
commit history with osac-installer itself (only osac-csi-driver, under `base/`,
remains a real submodule). There is deliberately no image-tag pinning/syncing
for these four in `values/*/values.yaml`: CI values files use the live tag
published by each component's own workflow -- `main` for fulfillment-service
(the only one of the four that doesn't publish a current `latest`) and
`latest` for osac-operator, osac-aap, and bare-metal-fulfillment-operator.
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
- **lib.sh** -- Shared shell functions: `retry_until`, `wait_for_resource`, `wait_for_namespace_cleanup`, `retry_command`, `http_retry`, `http_json`, `resolve_release_tag`, `check_postgres_prerequisites`

### CI Workflows

GitHub Actions only discovers workflows under the repo root's `.github/workflows/`,
so osac-installer-specific CI now lives there (not under `osac-installer/.github/`):
`nightly-build.yaml` (nightly umbrella chart build+publish, tested via e2e against
the current commit directly -- no submodule bump step) and
`publish-osac-installer-chart.yaml` (manual-dispatch umbrella chart release; takes
one mono-repo release `version` plus an independent `ui_version` for osac-ui).
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
- **Component repos:** `base/*/AGENTS.md` (discover with: `git submodule status`)
- **Design docs:** [osac-project/docs/architecture](https://github.com/osac-project/docs/tree/main/architecture)
